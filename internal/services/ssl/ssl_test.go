package ssl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

type fakeCertbotProbeRunner struct {
	lookupPath string
	lookupErr  error
	run        func(context.Context, string, ...string) error
}

func (f fakeCertbotProbeRunner) LookPath(string) (string, error) {
	return f.lookupPath, f.lookupErr
}

func (f fakeCertbotProbeRunner) Run(ctx context.Context, name string, args ...string) error {
	if f.run == nil {
		return nil
	}
	return f.run(ctx, name, args...)
}

func TestProbeContextClassifiesCertbotReadiness(t *testing.T) {
	t.Setenv(certbotBinEnv, "certbot")

	tests := []struct {
		name      string
		runner    fakeCertbotProbeRunner
		wantState integrationstate.State
		wantErr   bool
		forbidden string
	}{
		{
			name:      "missing binary",
			wantState: integrationstate.NotConfigured,
			wantErr:   true,
			forbidden: "/srv/private/certbot",
			runner: fakeCertbotProbeRunner{
				lookupErr: errors.New(`exec: "certbot": executable file not found in $PATH /srv/private/certbot`),
			},
		},
		{
			name:      "healthy requires both probes",
			wantState: integrationstate.Healthy,
			runner:    fakeCertbotProbeRunner{lookupPath: "/opt/bin/certbot"},
		},
		{
			name:      "version failure",
			wantState: integrationstate.Unavailable,
			wantErr:   true,
			forbidden: "/etc/letsencrypt/private-version",
			runner: fakeCertbotProbeRunner{
				lookupPath: "/opt/bin/certbot",
				run: func(_ context.Context, _ string, args ...string) error {
					if len(args) == 1 && args[0] == "--version" {
						return errors.New("version failed /etc/letsencrypt/private-version")
					}
					return nil
				},
			},
		},
		{
			name:      "plugin inventory failure",
			wantState: integrationstate.Unavailable,
			wantErr:   true,
			forbidden: "/etc/letsencrypt/private-plugin",
			runner: fakeCertbotProbeRunner{
				lookupPath: "/opt/bin/certbot",
				run: func(_ context.Context, _ string, args ...string) error {
					if len(args) > 0 && args[len(args)-1] == "plugins" {
						return errors.New("plugin failed /etc/letsencrypt/private-plugin")
					}
					return nil
				},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state, err := probeContext(context.Background(), test.runner)
			if state != test.wantState {
				t.Fatalf("probeContext() state = %q, want %q", state, test.wantState)
			}
			if (err != nil) != test.wantErr {
				t.Fatalf("probeContext() error = %v, want error=%v", err, test.wantErr)
			}
			if test.forbidden != "" && (strings.Contains(errString(err), test.forbidden) || strings.Contains(string(state), test.forbidden)) {
				t.Fatalf("probeContext() leaked private detail: state=%q err=%v", state, err)
			}
		})
	}
}

func TestProbeContextSendsPluginLogsToNullDevice(t *testing.T) {
	t.Setenv(certbotBinEnv, "certbot")
	var calls [][]string
	runner := fakeCertbotProbeRunner{
		lookupPath: "/opt/bin/certbot",
		run: func(_ context.Context, _ string, args ...string) error {
			calls = append(calls, append([]string(nil), args...))
			return nil
		},
	}
	state, err := probeContext(context.Background(), runner)
	if err != nil || state != integrationstate.Healthy {
		t.Fatalf("probeContext() = state %q, err %v; want healthy", state, err)
	}
	want := [][]string{{"--version"}, {"--logs-dir", os.DevNull, "plugins"}}
	if len(calls) != len(want) || !slices.Equal(calls[0], want[0]) || !slices.Equal(calls[1], want[1]) {
		t.Fatalf("probe calls = %#v, want %#v", calls, want)
	}
}

func TestProbeContextHonorsParentCancellation(t *testing.T) {
	started := make(chan struct{})
	runner := fakeCertbotProbeRunner{
		lookupPath: "/opt/bin/certbot",
		run: func(ctx context.Context, _ string, args ...string) error {
			if len(args) == 1 && args[0] == "--version" {
				close(started)
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan struct {
		state integrationstate.State
		err   error
	}, 1)
	go func() {
		state, err := probeContext(ctx, runner)
		result <- struct {
			state integrationstate.State
			err   error
		}{state: state, err: err}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("version probe did not start")
	}
	cancel()
	select {
	case got := <-result:
		if got.state != integrationstate.Unavailable {
			t.Fatalf("probeContext() state = %q, want %q", got.state, integrationstate.Unavailable)
		}
		if !errors.Is(got.err, context.Canceled) {
			t.Fatalf("probeContext() error = %v, want context.Canceled", got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("probeContext() did not honor cancellation")
	}
}

func TestProbeContextHonorsParentDeadline(t *testing.T) {
	runner := fakeCertbotProbeRunner{
		lookupPath: "/opt/bin/certbot",
		run: func(ctx context.Context, _ string, _ ...string) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	state, err := probeContext(ctx, runner)
	if state != integrationstate.Unavailable {
		t.Fatalf("probeContext() state = %q, want %q", state, integrationstate.Unavailable)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("probeContext() error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("probeContext() took %s after parent deadline", elapsed)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestClassifyCertbotError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want ReadinessState
	}{
		{name: "healthy", want: StateHealthy},
		{name: "missing", err: errors.New(`exec: "certbot": executable file not found in $PATH`), want: StateCertbotMissing},
		{name: "unavailable", err: errors.New("certbot --version: permission denied"), want: StateUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyCertbotError(tc.err); got != tc.want {
				t.Fatalf("ClassifyCertbotError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

func TestConfiguredCertbotConfigDir(t *testing.T) {
	t.Setenv(certbotConfigDirEnv, "/srv/hserver/letsencrypt")
	if got, err := configuredCertbotConfigDir(); err != nil || got != "/srv/hserver/letsencrypt" {
		t.Fatalf("configuredCertbotConfigDir() = %q, %v", got, err)
	}
	t.Setenv(certbotConfigDirEnv, "relative/path")
	if _, err := configuredCertbotConfigDir(); err == nil {
		t.Fatal("relative Certbot config directory should be rejected")
	}
}

func TestParsePlugins(t *testing.T) {
	output := `
* nginx
Description: Nginx Web Server plugin
* standalone
* dns-cloudflare
Description: Obtain certificates using a DNS TXT record
`
	want := []string{"nginx", "standalone", "dns-cloudflare"}
	if got := parsePlugins(output); !slices.Equal(got, want) {
		t.Fatalf("parsePlugins() = %#v, want %#v", got, want)
	}
}

func TestCloudflareCredentialsReadiness(t *testing.T) {
	t.Setenv(cloudflareCredentialsEnv, "")
	configured, readable, err := cloudflareCredentialsReadiness()
	if configured || readable || err != nil {
		t.Fatalf("empty credentials = (%v, %v, %v), want false, false, nil", configured, readable, err)
	}

	path := filepath.Join(t.TempDir(), "cloudflare.ini")
	if err := os.WriteFile(path, []byte("dns_cloudflare_api_token = placeholder\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(cloudflareCredentialsEnv, path)
	configured, readable, err = cloudflareCredentialsReadiness()
	if !configured || !readable || err != nil {
		t.Fatalf("protected credentials = (%v, %v, %v), want true, true, nil", configured, readable, err)
	}

	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	configured, readable, err = cloudflareCredentialsReadiness()
	if !configured || readable || err == nil || !strings.Contains(err.Error(), "must not be readable") {
		t.Fatalf("open credentials = (%v, %v, %v), want configured unreadable permission error", configured, readable, err)
	}
}

func TestGet_InvalidDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		domain string
	}{
		{"path traversal", "../etc/passwd"},
		{"slash", "example.com/evil"},
		{"backslash", `example.com\evil`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Get(tc.domain); err == nil {
				t.Fatalf("Get(%q) expected validation error", tc.domain)
			}
		})
	}
}

func TestRenew_InvalidDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		domain string
	}{
		{"path traversal", "../evil"},
		{"shell injection", "example.com;rm -rf"},
		{"pipe", "example.com|cat"},
		{"backtick", "example.com`id`"},
		{"dollar", "example.com$(id)"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Renew(tc.domain); err == nil {
				t.Fatalf("Renew(%q) expected validation error", tc.domain)
			}
		})
	}
}

func TestIssue_Validation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		req  *IssueRequest
	}{
		{
			name: "empty domain",
			req:  &IssueRequest{Domain: ""},
		},
		{
			name: "invalid domain slash",
			req:  &IssueRequest{Domain: "evil/../local"},
		},
		{
			name: "invalid domain shell",
			req:  &IssueRequest{Domain: "example.com;id"},
		},
		{
			name: "invalid SAN",
			req: &IssueRequest{
				Domain: "example.com",
				SANs:   []string{"www.example.com", "bad;domain"},
			},
		},
		{name: "unsupported method", req: &IssueRequest{Domain: "example.com", Method: "manual"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := Issue(tc.req); err == nil {
				t.Fatalf("Issue(%v) expected validation error", tc.req)
			}
		})
	}
}

func TestIssueDNSCloudflareRequiresInstallationCredentials(t *testing.T) {
	t.Setenv(cloudflareCredentialsEnv, "")
	_, err := Issue(&IssueRequest{Domain: "example.com", Method: "dns-cloudflare"})
	if err == nil || !strings.Contains(err.Error(), cloudflareCredentialsEnv) {
		t.Fatalf("Issue(dns-cloudflare) error = %v, want missing configuration guidance", err)
	}
}
