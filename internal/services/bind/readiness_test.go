package bind

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

type readinessFakeRunner struct {
	mu      sync.Mutex
	outputs map[string][]byte
	errors  map[string]error
	calls   []string
	block   bool
}

func (r *readinessFakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
	if r.block {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return r.outputs[call], r.errors[call]
}

func newReadinessTestService(t *testing.T, runner *readinessFakeRunner) *Service {
	t.Helper()
	configPath := filepath.Join(t.TempDir(), "named.conf.local")
	if err := os.WriteFile(configPath, []byte("options {};\n"), 0o644); err != nil {
		t.Fatalf("write test BIND config: %v", err)
	}
	return &Service{
		runner:     runner,
		lookPath:   func(string) (string, error) { return "/usr/bin/bind-tool", nil },
		configPath: configPath,
	}
}

func TestProbeContextReportsMissingBinaryAsNotConfigured(t *testing.T) {
	runner := &readinessFakeRunner{}
	service := newReadinessTestService(t, runner)
	service.lookPath = func(name string) (string, error) {
		if name == namedBin {
			return "", exec.ErrNotFound
		}
		return "/usr/bin/bind-tool", nil
	}

	state, err := service.ProbeContext(context.Background())
	if state != integrationstate.NotConfigured || !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("ProbeContext() = state %q, err %v; want not_configured", state, err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %#v, want none when named is missing", runner.calls)
	}
}

func TestProbeContextReportsActiveValidBINDAsHealthy(t *testing.T) {
	runner := &readinessFakeRunner{
		outputs: map[string][]byte{
			"systemctl is-active named": []byte("active\n"),
		},
		errors: map[string]error{},
	}
	service := newReadinessTestService(t, runner)

	state, err := service.ProbeContext(context.Background())
	if err != nil || state != integrationstate.Healthy {
		t.Fatalf("ProbeContext() = state %q, err %v; want healthy", state, err)
	}
	wantCalls := []string{"systemctl is-active named", "named-checkconf -z"}
	if !equalReadinessStrings(runner.calls, wantCalls) {
		t.Fatalf("runner calls = %#v, want %#v", runner.calls, wantCalls)
	}
}

func TestProbeContextReportsInactiveBINDAsUnavailable(t *testing.T) {
	runner := &readinessFakeRunner{
		outputs: map[string][]byte{
			"systemctl is-active named": []byte("inactive\n"),
		},
		errors: map[string]error{
			"systemctl is-active named": errors.New("exit status 3"),
		},
	}
	service := newReadinessTestService(t, runner)

	state, err := service.ProbeContext(context.Background())
	if state != integrationstate.Unavailable || err == nil {
		t.Fatalf("ProbeContext() = state %q, err %v; want unavailable", state, err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %#v, want only systemd observation", runner.calls)
	}
}

func TestProbeContextReportsSystemdObservationFailureAsUnavailable(t *testing.T) {
	runner := &readinessFakeRunner{
		outputs: map[string][]byte{
			"systemctl is-active named": []byte("active\n"),
		},
		errors: map[string]error{
			"systemctl is-active named": errors.New("systemd transport failed"),
		},
	}
	service := newReadinessTestService(t, runner)

	state, err := service.ProbeContext(context.Background())
	if state != integrationstate.Unavailable || err == nil {
		t.Fatalf("ProbeContext() = state %q, err %v; want unavailable", state, err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("runner calls = %#v, want only failed systemd observation", runner.calls)
	}
}

func TestProbeContextReportsValidationFailureWithoutCommandDetails(t *testing.T) {
	runner := &readinessFakeRunner{
		outputs: map[string][]byte{
			"systemctl is-active named": []byte("active\n"),
		},
		errors: map[string]error{
			"named-checkconf -z": errors.New("syntax error in /srv/private/named.conf"),
		},
	}
	service := newReadinessTestService(t, runner)

	state, err := service.ProbeContext(context.Background())
	if state != integrationstate.Unavailable || err == nil {
		t.Fatalf("ProbeContext() = state %q, err %v; want unavailable", state, err)
	}
	if strings.Contains(err.Error(), "/srv/private/named.conf") || strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("probe error leaked command details: %v", err)
	}
}

func TestProbeContextHonorsCancellation(t *testing.T) {
	runner := &readinessFakeRunner{block: true}
	service := newReadinessTestService(t, runner)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	started := time.Now()
	state, err := service.ProbeContext(ctx)
	if state != integrationstate.Unavailable || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ProbeContext() = state %q, err %v; want unavailable/deadline", state, err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cancelled probe took %s", elapsed)
	}
}

func TestProbeContextRequiresConfigAndManagementTools(t *testing.T) {
	runner := &readinessFakeRunner{}
	service := newReadinessTestService(t, runner)
	service.configPath = filepath.Join(t.TempDir(), "missing-named.conf")

	state, err := service.ProbeContext(context.Background())
	if state != integrationstate.NotConfigured || !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("missing config = state %q, err %v; want not_configured", state, err)
	}

	for _, missing := range []string{namedCheckBin, namedCheckZone, rndcBin} {
		service = newReadinessTestService(t, runner)
		service.lookPath = func(name string) (string, error) {
			if name == missing {
				return "", exec.ErrNotFound
			}
			return "/usr/bin/bind-tool", nil
		}
		state, err = service.ProbeContext(context.Background())
		if state != integrationstate.NotConfigured || !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("missing %s = state %q, err %v; want not_configured", missing, state, err)
		}
	}
}

func TestProbeContextClassifiesDiscoveryFailureAsUnavailable(t *testing.T) {
	runner := &readinessFakeRunner{}
	service := newReadinessTestService(t, runner)
	service.lookPath = func(name string) (string, error) {
		if name == namedCheckZone {
			return "", errors.New("permission denied at /private/bin")
		}
		return "/usr/bin/bind-tool", nil
	}

	state, err := service.ProbeContext(context.Background())
	if state != integrationstate.Unavailable || err == nil {
		t.Fatalf("discovery failure = state %q, err %v; want unavailable", state, err)
	}
	if strings.Contains(err.Error(), "/private/bin") || strings.Contains(err.Error(), "permission denied") {
		t.Fatalf("discovery error leaked host details: %v", err)
	}
}

func equalReadinessStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
