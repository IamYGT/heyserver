package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/services/cloudflare"
	domainsvc "github.com/IamYGT/heyserver/internal/services/domain"
	"github.com/IamYGT/heyserver/internal/store"
)

func TestHandleDomainProvisioningCapabilitiesNotConfigured(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		VhostsRoot:          "/srv/hserver/sites",
		NginxSitesAvailable: "/srv/hserver/nginx/available",
		NginxSitesEnabled:   "/srv/hserver/nginx/enabled",
		NginxSnippetsDir:    "/srv/hserver/nginx/snippets",
	}
	recorder := httptest.NewRecorder()
	handleDomainProvisioningCapabilities(cfg)(recorder, httptest.NewRequest(http.MethodGet, "/api/domains/provisioning", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	var response domainProvisioningCapabilities
	if err := json.NewDecoder(recorder.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.VhostsRoot != "/srv/hserver/sites" {
		t.Fatalf("vhostsRoot = %q", response.VhostsRoot)
	}
	if response.NginxSitesAvailable != "/srv/hserver/nginx/available" || response.NginxSitesEnabled != "/srv/hserver/nginx/enabled" {
		t.Fatalf("nginx paths = %q, %q", response.NginxSitesAvailable, response.NginxSitesEnabled)
	}
	if response.NginxSnippetsDir != "/srv/hserver/nginx/snippets" {
		t.Fatalf("nginxSnippetsDir = %q", response.NginxSnippetsDir)
	}
	if response.DNS.Status != "not_configured" || response.DNS.Origin != "" {
		t.Fatalf("dns capability = %+v", response.DNS)
	}
}

func TestDomainDNSStatusRejectsInvalidOriginWithoutProviderCall(t *testing.T) {
	t.Parallel()

	status := domainDNSStatus(&config.Config{
		CloudflareAPIToken: "configured-token",
		DomainDNSOrigin:    "not-an-ip",
	}, true)
	if status.Status != "not_configured" || !strings.Contains(status.Message, "HSERVER_DOMAIN_DNS_ORIGIN") {
		t.Fatalf("status = %+v", status)
	}
}

func TestHandleDomainListUsesConfiguredNginxPaths(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sitesAvailable := filepath.Join(root, "available")
	sitesEnabled := filepath.Join(root, "enabled")
	if err := os.MkdirAll(sitesAvailable, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sitesEnabled, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(sitesAvailable, "custom.example.com.conf"),
		[]byte("server { listen 80; server_name custom.example.com; }"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		VhostsRoot:          filepath.Join(root, "sites"),
		NginxSitesAvailable: sitesAvailable,
		NginxSitesEnabled:   sitesEnabled,
		NginxSnippetsDir:    filepath.Join(root, "snippets"),
	}
	recorder := httptest.NewRecorder()
	handleDomainList(cfg)(recorder, httptest.NewRequest(http.MethodGet, "/api/domains", nil))

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"id":"custom.example.com"`) {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandleDomainListReportsInvalidNginxPathsUnavailable(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		VhostsRoot:          "/srv/hserver/sites",
		NginxSitesAvailable: "relative/available",
		NginxSitesEnabled:   "relative/enabled",
	}
	recorder := httptest.NewRecorder()
	handleDomainList(cfg)(recorder, httptest.NewRequest(http.MethodGet, "/api/domains", nil))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "absolute paths") {
		t.Fatalf("body=%s", recorder.Body.String())
	}
}

func TestHandleDomainGetDistinguishesMissingFromReadFailure(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	for _, dir := range []string{available, enabled} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &config.Config{NginxSitesAvailable: available, NginxSitesEnabled: enabled}
	request := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/api/domains/example.com", nil)
		req.SetPathValue("id", "example.com")
		return req
	}

	recorder := httptest.NewRecorder()
	handleDomainGet(cfg)(recorder, request())
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("missing status = %d, want 404; body=%s", recorder.Code, recorder.Body.String())
	}

	if err := os.Mkdir(filepath.Join(available, "example.com.conf"), 0o755); err != nil {
		t.Fatal(err)
	}
	recorder = httptest.NewRecorder()
	handleDomainGet(cfg)(recorder, request())
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("read failure status = %d, want 500; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestHandleDomainCreateRejectsUnconfiguredDNSBeforeHostMutation(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodPost, "/api/domains", strings.NewReader(`{
		"domain":"app.example.com",
		"type":"static",
		"createDnsRecord":true
	}`))
	recorder := httptest.NewRecorder()
	handleDomainCreate(&config.Config{VhostsRoot: "/srv/hserver/sites"}, nil)(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "HSERVER_DOMAIN_DNS_ORIGIN") {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

func TestHandleDomainCreateRequiresSSLContactEmail(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodPost, "/api/domains", strings.NewReader(`{
		"domain":"app.example.com",
		"type":"static",
		"issueSSL":true
	}`))
	recorder := httptest.NewRecorder()
	handleDomainCreate(&config.Config{}, nil)(recorder, request)

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "sslEmail") {
		t.Fatalf("status = %d; body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestDomainCertificateHooksReconcileDNSForTLS(t *testing.T) {
	req := domainsvc.CreateRequest{
		Domain:          "app.example.com",
		IssueSSL:        true,
		CreateDNSRecord: true,
	}
	status := domainDNSCapability{Origin: "192.0.2.10", Proxied: true}
	wantResult := &cloudflare.DomainDNSResult{Domain: req.Domain}
	hooks, attempt := newDomainCertificateHooks(req, status, func(domain, origin string, proxied bool) (*cloudflare.DomainDNSResult, error) {
		if domain != req.Domain || origin != status.Origin || !proxied {
			t.Fatalf("reconcile args = %q, %q, %v", domain, origin, proxied)
		}
		return wantResult, nil
	})

	if hooks.BeforeCertificate == nil {
		t.Fatal("BeforeCertificate hook is nil")
	}
	if err := hooks.BeforeCertificate(); err != nil {
		t.Fatalf("BeforeCertificate() error = %v", err)
	}
	if !attempt.Attempted || attempt.Result != wantResult {
		t.Fatalf("attempt = %+v", attempt)
	}
}

func TestDomainCertificateHooksExposeDNSFailureWithoutRetry(t *testing.T) {
	req := domainsvc.CreateRequest{
		Domain:          "app.example.com",
		IssueSSL:        true,
		CreateDNSRecord: true,
	}
	hooks, attempt := newDomainCertificateHooks(req, domainDNSCapability{}, func(string, string, bool) (*cloudflare.DomainDNSResult, error) {
		return nil, errors.New("provider unavailable")
	})

	err := hooks.BeforeCertificate()
	if err == nil || !strings.Contains(err.Error(), "Cloudflare DNS provisioning failed: provider unavailable") {
		t.Fatalf("BeforeCertificate() error = %v", err)
	}
	if !attempt.Attempted || attempt.Result != nil {
		t.Fatalf("attempt = %+v", attempt)
	}
}

func TestDomainCertificateHooksDeferDNSWithoutTLS(t *testing.T) {
	hooks, attempt := newDomainCertificateHooks(domainsvc.CreateRequest{
		Domain:          "app.example.com",
		CreateDNSRecord: true,
	}, domainDNSCapability{}, func(string, string, bool) (*cloudflare.DomainDNSResult, error) {
		t.Fatal("reconcile called before local create")
		return nil, nil
	})

	if hooks.BeforeCertificate != nil || attempt.Attempted {
		t.Fatalf("hooks = %+v, attempt = %+v", hooks, attempt)
	}
}

func TestNewDomainUptimeMonitorFollowsActiveHTTPS(t *testing.T) {
	tests := []struct {
		name        string
		httpsActive bool
		wantURL     string
		wantTLS     bool
	}{
		{name: "HTTP fallback", wantURL: "http://app.example.com"},
		{name: "active HTTPS", httpsActive: true, wantURL: "https://app.example.com", wantTLS: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			monitor := newDomainUptimeMonitor("app.example.com", test.httpsActive)
			if monitor.URL != test.wantURL || monitor.TLSCheck != test.wantTLS {
				t.Fatalf("monitor URL/TLS = %q/%v, want %q/%v", monitor.URL, monitor.TLSCheck, test.wantURL, test.wantTLS)
			}
		})
	}
}

func TestDomainMonitorMatchesExactHostOnly(t *testing.T) {
	for _, test := range []struct {
		monitor store.UptimeMonitor
		want    bool
	}{
		{monitor: store.UptimeMonitor{URL: "https://example.com/health"}, want: true},
		{monitor: store.UptimeMonitor{Hostname: "EXAMPLE.COM"}, want: true},
		{monitor: store.UptimeMonitor{URL: "https://notexample.com"}, want: false},
		{monitor: store.UptimeMonitor{Hostname: "app.example.com"}, want: false},
	} {
		if got := domainMonitorMatches(test.monitor, "example.com"); got != test.want {
			t.Fatalf("domainMonitorMatches(%+v) = %v, want %v", test.monitor, got, test.want)
		}
	}
}

func TestHandleDomainJSONBodiesAreStrictAndBounded(t *testing.T) {
	for _, test := range []struct {
		name   string
		method string
		path   string
		body   string
		create bool
		want   int
	}{
		{name: "create unknown", method: http.MethodPost, path: "/api/domains", body: `{"domain":"app.example.com","unknown":true}`, create: true, want: http.StatusBadRequest},
		{name: "create trailing", method: http.MethodPost, path: "/api/domains", body: `{"domain":"app.example.com"}{}`, create: true, want: http.StatusBadRequest},
		{name: "create rewritten domain", method: http.MethodPost, path: "/api/domains", body: `{"domain":"bad/domain.com"}`, create: true, want: http.StatusBadRequest},
		{name: "create oversized", method: http.MethodPost, path: "/api/domains", body: `{"domain":"app.example.com","webRoot":"` + strings.Repeat("x", domainRequestBodyLimit) + `"}`, create: true, want: http.StatusRequestEntityTooLarge},
		{name: "check unknown", method: http.MethodPost, path: "/api/domains/check", body: `{"domain":"app.example.com","unknown":true}`, want: http.StatusBadRequest},
		{name: "check trailing", method: http.MethodPost, path: "/api/domains/check", body: `{"domain":"app.example.com"}{}`, want: http.StatusBadRequest},
		{name: "check oversized", method: http.MethodPost, path: "/api/domains/check", body: `{"domain":"` + strings.Repeat("x", domainRequestBodyLimit) + `"}`, want: http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.create {
				handleDomainCreate(&config.Config{}, nil)(recorder, request)
			} else {
				handleDomainCheck(&config.Config{})(recorder, request)
			}
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d body=%s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}

func TestHandleDomainDeleteRequiresExactQueryAndEmptyBody(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
		body string
	}{
		{name: "empty value", path: "/api/domains/example.com?deleteFiles="},
		{name: "repeated", path: "/api/domains/example.com?deleteFiles=true&deleteFiles=false"},
		{name: "unsupported", path: "/api/domains/example.com?purge=true"},
		{name: "request body", path: "/api/domains/example.com", body: `{}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodDelete, test.path, strings.NewReader(test.body))
			request.SetPathValue("id", "example.com")
			recorder := httptest.NewRecorder()
			handleDomainDelete(&config.Config{}, nil)(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 body=%s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestHandleDomainCreateReportsInvalidCertificatePathsUnavailable(t *testing.T) {
	t.Parallel()

	request := httptest.NewRequest(http.MethodPost, "/api/domains", strings.NewReader(`{
		"domain":"app.example.com",
		"type":"static",
		"issueSSL":true,
		"sslEmail":"admin@example.com"
	}`))
	recorder := httptest.NewRecorder()
	handleDomainCreate(&config.Config{
		VhostsRoot:          "/srv/hserver/sites",
		NginxSitesAvailable: "/srv/hserver/nginx/available",
		NginxSitesEnabled:   "/srv/hserver/nginx/enabled",
		NginxSnippetsDir:    "/srv/hserver/nginx/snippets",
		CertbotBin:          "relative/certbot",
		CertbotConfigDir:    "relative/certbot-state",
		ACMEWebroot:         "relative/acme",
	}, nil)(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "Certbot binary") {
		t.Fatalf("body=%s", recorder.Body.String())
	}
}

func TestHandleDomainCreateRejectsUnknownFPMPreset(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	sitesAvailable := filepath.Join(root, "available")
	sitesEnabled := filepath.Join(root, "enabled")
	for _, dir := range []string{sitesAvailable, sitesEnabled} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/api/domains", strings.NewReader(`{
		"domain":"php.example.com",
		"type":"php",
		"phpVersion":"8.4",
		"webRoot":"/srv/hserver/sites/php.example.com/public_html",
		"fpmPreset":"unbounded"
	}`))
	recorder := httptest.NewRecorder()
	handleDomainCreate(&config.Config{
		VhostsRoot:          filepath.Join(root, "sites"),
		NginxSitesAvailable: sitesAvailable,
		NginxSitesEnabled:   sitesEnabled,
		NginxSnippetsDir:    filepath.Join(root, "snippets"),
	}, nil)(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "low, medium, or high") {
		t.Fatalf("body=%s", recorder.Body.String())
	}
	if entries, err := os.ReadDir(sitesAvailable); err != nil || len(entries) != 0 {
		t.Fatalf("host mutation occurred before preset rejection: entries=%v err=%v", entries, err)
	}
}

func TestHandleDomainToggleRequiresExactActiveField(t *testing.T) {
	tests := []struct {
		name string
		id   string
		body string
		want int
	}{
		{name: "invalid identity", id: "example", body: `{"active":true}`, want: http.StatusBadRequest},
		{name: "missing active", id: "example.com", body: `{}`, want: http.StatusBadRequest},
		{name: "unknown field", id: "example.com", body: `{"active":true,"unexpected":true}`, want: http.StatusBadRequest},
		{name: "trailing value", id: "example.com", body: `{"active":true}{}`, want: http.StatusBadRequest},
		{name: "oversized", id: "example.com", body: `{"active":true,"padding":"` + strings.Repeat("x", domainToggleRequestBodyLimit) + `"}`, want: http.StatusRequestEntityTooLarge},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/domains/"+test.id+"/toggle", strings.NewReader(test.body))
			request.SetPathValue("id", test.id)
			recorder := httptest.NewRecorder()

			handleDomainToggle(nil).ServeHTTP(recorder, request)

			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, test.want, recorder.Body.String())
			}
		})
	}
}
