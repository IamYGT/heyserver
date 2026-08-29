package gdrive

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

func TestProbeReadinessContextClassifiesMissingPrerequisitesAsNotConfigured(t *testing.T) {
	tests := []struct {
		name         string
		clientID     string
		clientSecret string
		setup        func(*Service)
	}{
		{
			name:         "oauth client",
			clientID:     "",
			clientSecret: "",
			setup: func(service *Service) {
				service.readinessRcloneCheck = func(context.Context) error {
					t.Fatal("rclone check must not run without OAuth configuration")
					return nil
				}
			},
		},
		{
			name:         "rclone",
			clientID:     "client-id",
			clientSecret: "client-secret",
			setup: func(service *Service) {
				seedStatusToken(t, service)
				service.readinessRcloneCheck = func(context.Context) error { return ErrNotConfigured }
			},
		},
		{
			name:         "token",
			clientID:     "client-id",
			clientSecret: "client-secret",
			setup: func(service *Service) {
				service.readinessRcloneCheck = func(context.Context) error { return nil }
				service.readinessFetchAbout = func(context.Context, string) error {
					t.Fatal("Drive about must not run without a stored token")
					return nil
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, _ := newStatusService(t, test.clientID, test.clientSecret, fakeRclone(t))
			test.setup(service)
			state, err := service.ProbeReadinessContext(context.Background())
			if state != integrationstate.NotConfigured || !errors.Is(err, ErrNotConfigured) {
				t.Fatalf("ProbeReadinessContext() = state %q, err %v; want not_configured", state, err)
			}
		})
	}
}

func TestProbeReadinessContextMapsConfiguredObservationFailureToSafeUnavailable(t *testing.T) {
	service, _ := newStatusService(t, "client-id", "client-secret", fakeRclone(t))
	seedStatusToken(t, service)
	const secret = "gdrive-access-secret"
	service.readinessRcloneCheck = func(context.Context) error { return nil }
	service.readinessFetchAbout = func(_ context.Context, accessToken string) error {
		if accessToken != "access-token" {
			t.Fatalf("access token = %q, want stored token", accessToken)
		}
		return errors.New("provider response " + secret + " at /srv/private/rclone.conf?token=" + secret)
	}

	state, err := service.ProbeReadinessContext(context.Background())
	if state != integrationstate.Unavailable {
		t.Fatalf("ProbeReadinessContext() state = %q, want unavailable", state)
	}
	if err == nil || err.Error() != errReadinessUnavailable.Error() {
		t.Fatalf("ProbeReadinessContext() error = %v, want safe readiness error", err)
	}
	for _, forbidden := range []string{secret, "/srv/private/rclone.conf", "provider response", "token="} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("readiness error leaked %q: %v", forbidden, err)
		}
	}
}

func TestProbeReadinessContextHealthyOnlyAfterFreshDriveAboutRead(t *testing.T) {
	service, _ := newStatusService(t, "client-id", "client-secret", fakeRclone(t))
	seedStatusToken(t, service)
	called := false
	service.readinessRcloneCheck = func(ctx context.Context) error {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("rclone readiness check has no deadline")
		}
		return nil
	}
	service.readinessFetchAbout = func(ctx context.Context, accessToken string) error {
		called = true
		if accessToken != "access-token" {
			t.Fatalf("access token = %q, want stored token", accessToken)
		}
		if _, ok := ctx.Deadline(); !ok {
			t.Error("Drive about readiness check has no deadline")
		}
		return nil
	}

	state, err := service.ProbeReadinessContext(context.Background())
	if err != nil || state != integrationstate.Healthy {
		t.Fatalf("ProbeReadinessContext() = state %q, err %v; want healthy", state, err)
	}
	if !called {
		t.Fatal("Drive about was not observed")
	}
}

func TestProbeReadinessContextUsesBoundedDriveAboutHTTPRead(t *testing.T) {
	service, _ := newStatusService(t, "client-id", "client-secret", fakeRclone(t))
	seedStatusToken(t, service)
	service.readinessRcloneCheck = func(context.Context) error { return nil }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
			t.Errorf("authorization header = %q, want stored bearer token", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"user":{"emailAddress":"operator@example.com","displayName":"Operator"},"storageQuota":{"limit":"100","usage":"25","usageInDrive":"20"}}`))
	}))
	defer server.Close()
	service.oauth.aboutURL = server.URL
	service.oauth.httpClient = server.Client()

	state, err := service.ProbeReadinessContext(context.Background())
	if err != nil || state != integrationstate.Healthy {
		t.Fatalf("ProbeReadinessContext() = state %q, err %v; want healthy", state, err)
	}
}

func TestProbeReadinessContextDoesNotRefreshOrPersistToken(t *testing.T) {
	service, _ := newStatusService(t, "client-id", "client-secret", fakeRclone(t))
	expired := &tokenData{
		AccessToken:  "expired-access",
		RefreshToken: "refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(-time.Hour),
	}
	if err := service.oauth.saveToken(expired); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(service.rclone.configPath, []byte("[hserver-gdrive]\ntype = drive\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(service.oauth.tokenPath())
	if err != nil {
		t.Fatal(err)
	}
	service.statusRefresh = func(*tokenData, string) (*tokenData, error) {
		t.Fatal("readiness must not invoke the mutating Status refresh hook")
		return nil, nil
	}
	service.readinessRcloneCheck = func(context.Context) error { return nil }
	service.readinessFetchAbout = func(_ context.Context, accessToken string) error {
		if accessToken != expired.AccessToken {
			t.Fatalf("readiness changed access token before Drive about: %q", accessToken)
		}
		return nil
	}

	state, err := service.ProbeReadinessContext(context.Background())
	if err != nil || state != integrationstate.Healthy {
		t.Fatalf("ProbeReadinessContext() = state %q, err %v; want healthy", state, err)
	}
	after, err := os.ReadFile(service.oauth.tokenPath())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("readiness probe persisted a token mutation")
	}
}

func TestProbeReadinessContextHonorsCancellationWithoutLeakingDetails(t *testing.T) {
	service, _ := newStatusService(t, "client-id", "client-secret", fakeRclone(t))
	seedStatusToken(t, service)
	service.readinessRcloneCheck = func(ctx context.Context) error {
		<-ctx.Done()
		return errors.New("private rclone path /srv/private/rclone")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	state, err := service.ProbeReadinessContext(ctx)
	if state != integrationstate.Unavailable || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ProbeReadinessContext() = state %q, err %v; want unavailable/deadline", state, err)
	}
	if strings.Contains(err.Error(), "/srv/private/rclone") {
		t.Fatalf("cancellation error leaked provider detail: %v", err)
	}
}

func TestFetchAboutContextUsesCallerCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()

	oauth := newOAuthManager(t.TempDir())
	oauth.aboutURL = server.URL
	oauth.httpClient = server.Client()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, _, _, err := oauth.fetchAboutContext(ctx, "access-token")
		result <- err
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("Drive about request did not start")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("fetchAboutContext() error = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Drive about request did not honor cancellation")
	}
}

func TestProbeReadinessContextRejectsMissingOrUnsafeProtectedFiles(t *testing.T) {
	tests := []struct {
		name      string
		prepare   func(*Service)
		wantState integrationstate.State
		wantErr   error
	}{
		{
			name: "missing token",
			prepare: func(service *Service) {
				if err := os.WriteFile(service.rclone.configPath, []byte("[hserver-gdrive]\ntype = drive\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantState: integrationstate.NotConfigured,
			wantErr:   ErrNotConfigured,
		},
		{
			name: "missing rclone config",
			prepare: func(service *Service) {
				seedStatusToken(t, service)
				if err := os.Remove(service.rclone.configPath); err != nil {
					t.Fatal(err)
				}
			},
			wantState: integrationstate.NotConfigured,
			wantErr:   ErrNotConfigured,
		},
		{
			name: "token is group readable",
			prepare: func(service *Service) {
				seedStatusToken(t, service)
				if err := os.Chmod(service.oauth.tokenPath(), 0o640); err != nil {
					t.Fatal(err)
				}
			},
			wantState: integrationstate.Unavailable,
			wantErr:   errReadinessUnavailable,
		},
		{
			name: "rclone config is group readable",
			prepare: func(service *Service) {
				seedStatusToken(t, service)
				if err := os.Chmod(service.rclone.configPath, 0o640); err != nil {
					t.Fatal(err)
				}
			},
			wantState: integrationstate.Unavailable,
			wantErr:   errReadinessUnavailable,
		},
		{
			name: "token is symlink",
			prepare: func(service *Service) {
				seedStatusToken(t, service)
				target := filepath.Join(service.dataDir, "token-target.json")
				if err := os.WriteFile(target, []byte(`{"access_token":"access-token","refresh_token":"refresh-token"}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(service.oauth.tokenPath()); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, service.oauth.tokenPath()); err != nil {
					t.Fatal(err)
				}
			},
			wantState: integrationstate.Unavailable,
			wantErr:   errReadinessUnavailable,
		},
		{
			name: "rclone config is directory",
			prepare: func(service *Service) {
				seedStatusToken(t, service)
				if err := os.Remove(service.rclone.configPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(service.rclone.configPath, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			wantState: integrationstate.Unavailable,
			wantErr:   errReadinessUnavailable,
		},
		{
			name: "rclone config is symlink",
			prepare: func(service *Service) {
				seedStatusToken(t, service)
				target := filepath.Join(service.dataDir, "rclone-target.conf")
				if err := os.WriteFile(target, []byte("[hserver-gdrive]\ntype = drive\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(service.rclone.configPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, service.rclone.configPath); err != nil {
					t.Fatal(err)
				}
			},
			wantState: integrationstate.Unavailable,
			wantErr:   errReadinessUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, _ := newStatusService(t, "client-id", "client-secret", fakeRclone(t))
			test.prepare(service)
			service.readinessRcloneCheck = func(context.Context) error {
				t.Fatal("rclone observation must not run before protected-file validation")
				return nil
			}
			state, err := service.ProbeReadinessContext(context.Background())
			if state != test.wantState || !errors.Is(err, test.wantErr) {
				t.Fatalf("ProbeReadinessContext() = state %q, err %v; want %q/%v", state, err, test.wantState, test.wantErr)
			}
		})
	}
}

func TestProbeReadinessContextRejectsDriveAboutWithoutEmail(t *testing.T) {
	for _, body := range []string{`{}`, `{"user":null}`, `{"user":{"emailAddress":"   "}}`} {
		t.Run(body, func(t *testing.T) {
			service, _ := newStatusService(t, "client-id", "client-secret", fakeRclone(t))
			seedStatusToken(t, service)
			service.readinessRcloneCheck = func(context.Context) error { return nil }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
			}))
			defer server.Close()
			service.oauth.aboutURL = server.URL
			service.oauth.httpClient = server.Client()

			state, err := service.ProbeReadinessContext(context.Background())
			if state != integrationstate.Unavailable || !errors.Is(err, ErrReadinessUnavailable) {
				t.Fatalf("ProbeReadinessContext() = state %q, err %v; want unavailable", state, err)
			}
		})
	}
}

func TestProbeReadinessContextMapsBrokenTokenToUnavailable(t *testing.T) {
	service, _ := newStatusService(t, "client-id", "client-secret", fakeRclone(t))
	if err := os.WriteFile(service.oauth.tokenPath(), []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(service.rclone.configPath, []byte("[hserver-gdrive]\ntype = drive\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service.readinessRcloneCheck = func(context.Context) error { return nil }
	service.readinessFetchAbout = func(context.Context, string) error {
		t.Fatal("Drive about must not run for a broken token")
		return nil
	}

	state, err := service.ProbeReadinessContext(context.Background())
	if state != integrationstate.Unavailable || !errors.Is(err, ErrReadinessUnavailable) {
		t.Fatalf("ProbeReadinessContext() = state %q, err %v; want unavailable", state, err)
	}
}

func TestLoadTokenContextIsBoundedAndCancellationAware(t *testing.T) {
	oauth := newOAuthManager(t.TempDir())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := oauth.loadToken(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled loadToken() error = %v, want context.Canceled", err)
	}
	if err := os.WriteFile(oauth.tokenPath(), []byte(strings.Repeat("x", maxTokenBytes+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := oauth.loadToken(context.Background()); err == nil {
		t.Fatal("oversized token file should be rejected")
	}
}

func TestProbeReadinessContextCredentialConfigRaceIsSynchronized(t *testing.T) {
	service, _ := newStatusService(t, "client-id", "client-secret", fakeRclone(t))
	seedStatusToken(t, service)
	service.readinessRcloneCheck = func(context.Context) error { return nil }
	service.readinessFetchAbout = func(context.Context, string) error { return nil }

	var wg sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for iteration := 0; iteration < 40; iteration++ {
				service.oauth.setCredentials("client-id-"+string(rune('a'+worker)), "client-secret")
				_ = service.oauth.oauthConfig("http://127.0.0.1/callback")
				_, _ = service.ProbeReadinessContext(context.Background())
			}
		}(worker)
	}
	wg.Wait()
}

func TestRcloneReadinessRunnerUsesConfiguredReadOnlyObservation(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "rclone-helper")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = version ]; then printf 'provider-secret\n'; exit 0; fi\n" +
		"if [ \"$1\" = --config ] && [ \"$3\" = listremotes ] && [ -f \"$2\" ]; then printf 'hserver-gdrive:\\n'; exit 0; fi\n" +
		"exit 9\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := newRcloneRunner(t.TempDir(), bin)
	if err := os.WriteFile(runner.configPath, []byte("[hserver-gdrive]\ntype = drive\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := runner.foundContext(context.Background()); err != nil {
		t.Fatalf("foundContext() error = %v, want nil", err)
	}
}

func TestRcloneReadinessRunnerMapsBrokenConfigToSafeUnavailable(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "rclone-helper")
	script := "#!/bin/sh\n" +
		"if [ \"$1\" = version ]; then exit 0; fi\n" +
		"if [ \"$1\" = --config ] && [ \"$3\" = listremotes ]; then printf 'private provider config failure\n' >&2; exit 17; fi\n" +
		"exit 9\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := newRcloneRunner(t.TempDir(), bin)
	if err := os.WriteFile(runner.configPath, []byte("[hserver-gdrive]\ntype = drive\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := runner.foundContext(context.Background())
	if !errors.Is(err, errRcloneReadinessUnavailable) {
		t.Fatalf("foundContext() error = %v, want safe unavailable", err)
	}
	if strings.Contains(err.Error(), "private provider config failure") || strings.Contains(err.Error(), runner.configPath) {
		t.Fatalf("rclone readiness error leaked command details: %v", err)
	}
}

func TestRcloneReadinessRunnerHonorsContextCancellation(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "rclone-helper")
	script := "#!/bin/sh\nif [ \"$1\" = version ]; then sleep 10; fi\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := newRcloneRunner(t.TempDir(), bin)
	if err := os.WriteFile(runner.configPath, []byte("[hserver-gdrive]\ntype = drive\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := runner.foundContext(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("foundContext() error = %v, want context deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("foundContext() waited %s after cancellation", elapsed)
	}
}
