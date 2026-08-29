package mail

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

type fakeMailReadinessRunner struct {
	mu      sync.Mutex
	output  []byte
	err     error
	calls   []string
	started chan struct{}
	block   bool
}

func (r *fakeMailReadinessRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, strings.Join(append([]string{name}, args...), " "))
	if r.started != nil {
		select {
		case <-r.started:
		default:
			close(r.started)
		}
	}
	block := r.block
	output := append([]byte(nil), r.output...)
	err := r.err
	r.mu.Unlock()
	if block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return output, err
}

func (r *fakeMailReadinessRunner) Calls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

// controlledDeadlineContext lets the HTTP test arm DeadlineExceeded only
// after the request reaches the server. Starting a short timeout before
// ProbeReadinessContext is scheduled can otherwise legitimately prevent the
// request from being issued under load.
type controlledDeadlineContext struct {
	context.Context
	deadline time.Time
	done     chan struct{}
	once     sync.Once
}

func newControlledDeadlineContext() *controlledDeadlineContext {
	return &controlledDeadlineContext{
		Context:  context.Background(),
		deadline: time.Now().Add(time.Hour),
		done:     make(chan struct{}),
	}
}

func (c *controlledDeadlineContext) Deadline() (time.Time, bool) {
	return c.deadline, true
}

func (c *controlledDeadlineContext) Done() <-chan struct{} {
	return c.done
}

func (c *controlledDeadlineContext) Err() error {
	select {
	case <-c.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func (c *controlledDeadlineContext) expire() {
	c.once.Do(func() { close(c.done) })
}

func newMailReadinessTestService(serverURL string, runner readinessCommandRunner) *Service {
	service := New(serverURL, "mail-admin", "test-password").WithRuntime(
		"stalwart-test",
		"/etc/stalwart/config.toml",
		"/usr/local/bin/stalwart-mail",
	)
	service.readinessRunner = runner
	return service
}

func TestProbeReadinessContextRequiresURLAndRuntimeSettings(t *testing.T) {
	tests := []struct {
		name string
		make func(*fakeMailReadinessRunner) *Service
	}{
		{
			name: "URL",
			make: func(runner *fakeMailReadinessRunner) *Service {
				service := newMailReadinessTestService("", runner)
				return service
			},
		},
		{
			name: "service",
			make: func(runner *fakeMailReadinessRunner) *Service {
				service := newMailReadinessTestService("http://mail.example.test", runner)
				service.serviceName = ""
				return service
			},
		},
		{
			name: "config",
			make: func(runner *fakeMailReadinessRunner) *Service {
				service := newMailReadinessTestService("http://mail.example.test", runner)
				service.configPath = ""
				return service
			},
		},
		{
			name: "binary",
			make: func(runner *fakeMailReadinessRunner) *Service {
				service := newMailReadinessTestService("http://mail.example.test", runner)
				service.binary = ""
				return service
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := &fakeMailReadinessRunner{}
			state, err := test.make(runner).ProbeReadinessContext(context.Background())
			if state != integrationstate.NotConfigured || !errors.Is(err, ErrNotConfigured) {
				t.Fatalf("ProbeReadinessContext() = state %q, err %v; want not_configured", state, err)
			}
			if calls := runner.Calls(); len(calls) != 0 {
				t.Fatalf("missing setting invoked systemd: %#v", calls)
			}
		})
	}
}

func TestProbeReadinessContextRequiresManagementAuthBeforeHTTP(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requests++
	}))
	defer server.Close()

	runner := &fakeMailReadinessRunner{output: []byte("active\n")}
	service := newMailReadinessTestService(server.URL, runner)
	service.username = "mail-admin"
	service.password = ""
	service.apiKey = ""
	state, err := service.ProbeReadinessContext(context.Background())
	if state != integrationstate.NotConfigured || !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("missing password = state %q, err %v; want not_configured", state, err)
	}
	if requests != 0 {
		t.Fatalf("management API requests = %d without credentials, want 0", requests)
	}
	if calls := runner.Calls(); len(calls) != 0 {
		t.Fatalf("missing credentials invoked systemd: %#v", calls)
	}
}

func TestProbeReadinessContextPreservesBearerKeyAndBasicAuth(t *testing.T) {
	t.Run("bearer", func(t *testing.T) {
		const key = "bearer-key with internal spaces"
		var authorization string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authorization = r.Header.Get("Authorization")
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		runner := &fakeMailReadinessRunner{output: []byte("active\n")}
		service := NewWithAPIKey(server.URL, key, "", "").WithRuntime(
			"stalwart-test", "/etc/stalwart/config.toml", "/usr/local/bin/stalwart-mail",
		)
		service.readinessRunner = runner
		state, err := service.ProbeReadinessContext(context.Background())
		if err != nil || state != integrationstate.Healthy {
			t.Fatalf("Bearer readiness = state %q, err %v; want healthy", state, err)
		}
		if authorization != "Bearer "+key {
			t.Fatalf("Authorization = %q, want exact bearer value", authorization)
		}
	})

	t.Run("basic", func(t *testing.T) {
		var gotUser, gotPassword string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotUser, gotPassword, _ = r.BasicAuth()
			w.WriteHeader(http.StatusNoContent)
		}))
		defer server.Close()

		runner := &fakeMailReadinessRunner{output: []byte("active\n")}
		service := New(server.URL, "mail-admin", " password with spaces ").WithRuntime(
			"stalwart-test", "/etc/stalwart/config.toml", "/usr/local/bin/stalwart-mail",
		)
		service.readinessRunner = runner
		state, err := service.ProbeReadinessContext(context.Background())
		if err != nil || state != integrationstate.Healthy {
			t.Fatalf("Basic readiness = state %q, err %v; want healthy", state, err)
		}
		if gotUser != "mail-admin" || gotPassword != " password with spaces " {
			t.Fatalf("Basic credentials = user %q password %q, want exact values", gotUser, gotPassword)
		}
	})
}

func TestProbeReadinessContextRequiresActiveServiceAndFreshAPIRead(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.String() != mailReadinessAPIPath {
			t.Errorf("management API path = %q, want %q", r.URL.String(), mailReadinessAPIPath)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	runner := &fakeMailReadinessRunner{output: []byte("active\n")}
	state, err := newMailReadinessTestService(server.URL, runner).ProbeReadinessContext(context.Background())
	if err != nil || state != integrationstate.Healthy {
		t.Fatalf("ProbeReadinessContext() = state %q, err %v; want healthy", state, err)
	}
	if requests != 1 {
		t.Fatalf("management API requests = %d, want one fresh read", requests)
	}
	if calls := runner.Calls(); len(calls) != 1 || calls[0] != "systemctl is-active stalwart-test" {
		t.Fatalf("systemd calls = %#v, want one is-active observation", calls)
	}
}

func TestProbeReadinessContextMapsStoppedOrFailedServiceToUnavailable(t *testing.T) {
	for _, serviceState := range []string{"inactive", "failed"} {
		t.Run(serviceState, func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				requests++
			}))
			defer server.Close()

			runner := &fakeMailReadinessRunner{
				output: []byte(serviceState + "\n"),
				err:    errors.New("systemctl exit details at /private/service.log"),
			}
			state, err := newMailReadinessTestService(server.URL, runner).ProbeReadinessContext(context.Background())
			if state != integrationstate.Unavailable || err == nil {
				t.Fatalf("stopped service = state %q, err %v; want unavailable", state, err)
			}
			if strings.Contains(err.Error(), "/private/service.log") || strings.Contains(err.Error(), "systemctl exit details") {
				t.Fatalf("service observation leaked command details: %v", err)
			}
			if requests != 0 {
				t.Fatalf("management API requests = %d after stopped service, want 0", requests)
			}
		})
	}
}

func TestProbeReadinessContextMapsManagementAPIFailureWithoutLeak(t *testing.T) {
	const secret = "mail-management-secret"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "provider failure "+secret+" /srv/mail/private", http.StatusBadGateway)
	}))
	defer server.Close()

	runner := &fakeMailReadinessRunner{output: []byte("active\n")}
	state, err := newMailReadinessTestService(server.URL, runner).ProbeReadinessContext(context.Background())
	if state != integrationstate.Unavailable || err == nil {
		t.Fatalf("management API failure = state %q, err %v; want unavailable", state, err)
	}
	for _, forbidden := range []string{secret, "/srv/mail/private", "provider failure", server.URL} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("readiness error leaked %q: %v", forbidden, err)
		}
	}
}

func TestProbeReadinessContextRejectsRedirectWithoutFollowingIt(t *testing.T) {
	initialRequests := 0
	loginRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/principal":
			initialRequests++
			http.Redirect(w, r, "/login", http.StatusFound)
		case "/login":
			loginRequests++
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner := &fakeMailReadinessRunner{output: []byte("active\n")}
	service := newMailReadinessTestService(server.URL, runner)
	state, err := service.ProbeReadinessContext(context.Background())
	if state != integrationstate.Unavailable || err == nil {
		t.Fatalf("redirected readiness = state %q, err %v; want unavailable", state, err)
	}
	if initialRequests != 1 || loginRequests != 0 {
		t.Fatalf("readiness requests = initial %d login %d, want 1 and 0", initialRequests, loginRequests)
	}

	// The legacy helper intentionally retains the default follow-redirect
	// policy used by existing mail CRUD callers.
	if err := service.getContext(context.Background(), mailReadinessAPIPath, nil); err != nil {
		t.Fatalf("legacy GET after redirect = %v, want followed 200", err)
	}
	if loginRequests != 1 {
		t.Fatalf("legacy helper login requests = %d, want 1", loginRequests)
	}
}

func TestProbeReadinessContextPropagatesDeadlineToSystemdAndHTTP(t *testing.T) {
	t.Run("systemd", func(t *testing.T) {
		started := make(chan struct{})
		runner := &fakeMailReadinessRunner{started: started, block: true}
		service := newMailReadinessTestService("http://mail.example.test", runner)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()

		startedAt := time.Now()
		state, err := service.ProbeReadinessContext(ctx)
		if state != integrationstate.Unavailable || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("timed systemd probe = state %q, err %v; want unavailable/deadline", state, err)
		}
		if elapsed := time.Since(startedAt); elapsed > time.Second {
			t.Fatalf("timed systemd probe took %s", elapsed)
		}
	})

	t.Run("HTTP", func(t *testing.T) {
		started := make(chan struct{})
		cancelled := make(chan struct{})
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			<-r.Context().Done()
			close(cancelled)
		}))
		defer server.Close()

		runner := &fakeMailReadinessRunner{output: []byte("active\n")}
		service := newMailReadinessTestService(server.URL, runner)
		ctx := newControlledDeadlineContext()
		result := make(chan struct {
			state integrationstate.State
			err   error
		}, 1)
		go func() {
			state, err := service.ProbeReadinessContext(ctx)
			result <- struct {
				state integrationstate.State
				err   error
			}{state: state, err: err}
		}()
		select {
		case <-started:
			ctx.expire()
		case <-time.After(time.Second):
			ctx.expire()
			t.Fatal("management API request did not start")
		}
		select {
		case got := <-result:
			if got.state != integrationstate.Unavailable || !errors.Is(got.err, context.DeadlineExceeded) {
				t.Fatalf("timed HTTP probe = state %q, err %v; want unavailable/deadline", got.state, got.err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed HTTP probe did not return")
		}
		select {
		case <-cancelled:
		case <-time.After(time.Second):
			t.Fatal("management API request did not receive cancellation")
		}
	})
}

func TestExecReadinessCommandRunnerCapturesCompletedOutput(t *testing.T) {
	script := filepath.Join(t.TempDir(), "readiness-output")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' 'active'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	output, err := (execReadinessCommandRunner{}).Run(context.Background(), script)
	if err != nil {
		t.Fatalf("execReadinessCommandRunner.Run() error = %v", err)
	}
	if string(output) != "active\n" {
		t.Fatalf("captured output = %q, want %q", output, "active\\n")
	}
}

func TestExecReadinessCommandRunnerCancelsDescendantProcessGroup(t *testing.T) {
	tempDir := t.TempDir()
	script := filepath.Join(tempDir, "readiness-descendant")
	pidFile := filepath.Join(tempDir, "child.pid")
	scriptBody := "#!/bin/sh\n(while :; do sleep 1; done) &\necho $! > \"$1\"\nwait\n"
	if err := os.WriteFile(script, []byte(scriptBody), 0o755); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := (execReadinessCommandRunner{}).Run(ctx, script, pidFile)
		done <- err
	}()

	var childPID int
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			childPID, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil && childPID > 0 {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if childPID == 0 {
		cancel()
		<-done
		t.Fatal("descendant PID was not recorded")
	}

	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled runner error = nil, want cancellation/process termination")
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not return after descendant cancellation")
	}

	procStat := filepath.Join("/proc", strconv.Itoa(childPID), "stat")
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(procStat)
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if err == nil {
			fields := strings.Fields(string(data))
			if len(fields) > 2 && fields[2] == "Z" {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("descendant process %d remains after readiness cancellation", childPID)
}
