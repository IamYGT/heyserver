package domain

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	phpsvc "github.com/IamYGT/heyserver/internal/services/php"
	"github.com/IamYGT/heyserver/internal/services/pm2"
)

func TestSanitize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"example.com", "example.com"},
		{"api_example.com", "api_example.com"},
		{"evil;rm -rf", "evilrm-rf"},
		{"../traversal", "..traversal"},
		{"foo bar", "foobar"},
		{"site-name_v2.conf", "site-name_v2.conf"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			if got := sanitize(tc.input); got != tc.want {
				t.Errorf("sanitize(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSanitizeDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"Example.COM", "Example.COM"},
		{"app.example.com", "app.example.com"},
		{"bad/domain", "baddomain"},
		{"$(evil)", "evil"},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			if got := sanitizeDomain(tc.input); got != tc.want {
				t.Errorf("sanitizeDomain(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestNormalizeDomainRejectsRatherThanRewriting(t *testing.T) {
	valid, err := NormalizeDomain(" Example.COM ")
	if err != nil || valid != "example.com" {
		t.Fatalf("NormalizeDomain() = %q, %v", valid, err)
	}
	for _, value := range []string{
		"example", "bad/domain.com", "bad_domain.com", "-bad.example.com",
		"bad-.example.com", "bad..example.com", strings.Repeat("a", 64) + ".example.com",
	} {
		if normalized, err := NormalizeDomain(value); !errors.Is(err, ErrInvalidRequest) || normalized != "" {
			t.Fatalf("NormalizeDomain(%q) = %q, %v; want ErrInvalidRequest", value, normalized, err)
		}
	}
}

func TestDomainToPoolName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		domain string
		want   string
	}{
		{"api.example.com", "api_example_com"},
		{"my-site.example.com", "my_site_example_com"},
		{"example.com", "example_com"},
	}

	for _, tc := range tests {
		t.Run(tc.domain, func(t *testing.T) {
			t.Parallel()
			if got := domainToPoolName(tc.domain); got != tc.want {
				t.Errorf("domainToPoolName(%q) = %q, want %q", tc.domain, got, tc.want)
			}
		})
	}
}

func TestParseServerNames(t *testing.T) {
	t.Parallel()

	content := `server {
    server_name example.com www.example.com _;
}

func TestParseServerNamesReturnsEmptyArray(t *testing.T) {
	t.Parallel()

	if got := parseServerNames("server { listen 80; }"); got == nil || len(got) != 0 {
		t.Fatalf("parseServerNames() = %#v, want non-nil empty slice", got)
	}
}
server {
    server_name api.example.com;
}`

	got := parseServerNames(content)
	want := []string{"example.com", "www.example.com", "api.example.com"}
	if len(got) != len(want) {
		t.Fatalf("parseServerNames len = %d, want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("parseServerNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestParseDirective(t *testing.T) {
	t.Parallel()

	content := `server {
    root /var/www/vhosts/example.com/public_html;
    index index.php index.html;
}`

	if got := parseDirective(content, "root"); got != "/var/www/vhosts/example.com/public_html" {
		t.Errorf("parseDirective(root) = %q", got)
	}
	if got := parseDirective(content, "missing"); got != "" {
		t.Errorf("parseDirective(missing) = %q, want empty", got)
	}
}

func TestParsePhpVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "php 8.4 socket",
			content: `fastcgi_pass unix:/run/php/php8.4-fpm-example_com.sock;`,
			want:    "8.4",
		},
		{
			name:    "php 7.4 socket",
			content: `fastcgi_pass unix:/run/php/php7.4-fpm-example_com.sock;`,
			want:    "7.4",
		},
		{
			name:    "no php",
			content: `root /var/www/html;`,
			want:    "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := parsePhpVersion(tc.content); got != tc.want {
				t.Errorf("parsePhpVersion() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseProxyPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    int
	}{
		{
			name:    "host port without scheme",
			content: `proxy_pass 127.0.0.1:3085;`,
			want:    3085,
		},
		{
			name:    "no proxy",
			content: `root /var/www/html;`,
			want:    0,
		},
		{
			name:    "invalid port segment",
			content: `proxy_pass http://127.0.0.1:abc;`,
			want:    0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := parseProxyPort(tc.content); got != tc.want {
				t.Errorf("parseProxyPort() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestDetectTypeFromContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"php fastcgi", `fastcgi_pass unix:/run/php/php8.4-fpm.sock;`, "php"},
		{"php-fpm snippet", `include snippets/php-fpm.conf;`, "php"},
		{"proxy", `proxy_pass http://127.0.0.1:3000;`, "proxy"},
		{"static", `root /var/www/html;`, "static"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := detectType(tc.content); got != tc.want {
				t.Errorf("detectType() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseConfig(t *testing.T) {
	t.Parallel()

	content := `server {
    listen 443 ssl;
    server_name example.com www.example.com;
    root /var/www/vhosts/example.com/public_html;
    fastcgi_pass unix:/run/php/php8.4-fpm-example_com.sock;
}`

	got := parseConfig("fallback.example.com.conf", content)
	if got.Name != "example.com" {
		t.Errorf("Name = %q, want example.com", got.Name)
	}
	if got.Type != "php" {
		t.Errorf("Type = %q, want php", got.Type)
	}
	if got.Root != "/var/www/vhosts/example.com/public_html" {
		t.Errorf("Root = %q", got.Root)
	}
	if got.PHPVersion != "8.4" {
		t.Errorf("PHPVersion = %q, want 8.4", got.PHPVersion)
	}
	if !got.SSLEnabled {
		t.Error("expected SSLEnabled=true")
	}
}

func TestParseConfig_FallbackName(t *testing.T) {
	t.Parallel()

	got := parseConfig("orphan.example.com.conf", "server { listen 80; }")
	if got.Name != "orphan.example.com" {
		t.Errorf("Name = %q, want orphan.example.com", got.Name)
	}
}

func TestIsSubdomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		domain string
		want   bool
	}{
		{"example.com", false},
		{"app.example.com", true},
		{"staging.app.example.com", true},
		{"co.uk", false},
	}

	for _, tc := range tests {
		t.Run(tc.domain, func(t *testing.T) {
			t.Parallel()
			if got := IsSubdomain(tc.domain); got != tc.want {
				t.Errorf("IsSubdomain(%q) = %v, want %v", tc.domain, got, tc.want)
			}
		})
	}
}

func TestParentDomain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		domain string
		want   string
	}{
		{"example.com", "example.com"},
		{"app.example.com", "example.com"},
		{"staging.app.example.com", "example.com"},
	}

	for _, tc := range tests {
		t.Run(tc.domain, func(t *testing.T) {
			t.Parallel()
			if got := ParentDomain(tc.domain); got != tc.want {
				t.Errorf("ParentDomain(%q) = %q, want %q", tc.domain, got, tc.want)
			}
		})
	}
}

func TestSubdomainPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		domain string
		want   string
	}{
		{"example.com", ""},
		{"app.example.com", "app"},
		{"staging.app.example.com", "staging.app"},
	}

	for _, tc := range tests {
		t.Run(tc.domain, func(t *testing.T) {
			t.Parallel()
			if got := SubdomainPrefix(tc.domain); got != tc.want {
				t.Errorf("SubdomainPrefix(%q) = %q, want %q", tc.domain, got, tc.want)
			}
		})
	}
}

func TestDefaultWebRootUsesInstallationRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		domain string
		want   string
	}{
		{domain: "example.com", want: "/srv/hserver/sites/example.com/public_html"},
		{domain: "app.example.com", want: "/srv/hserver/sites/example.com/app.example.com/public_html"},
	}
	for _, tc := range tests {
		if got := DefaultWebRoot("/srv/hserver/sites", tc.domain); got != tc.want {
			t.Fatalf("DefaultWebRoot(%q) = %q, want %q", tc.domain, got, tc.want)
		}
	}
}

func TestDefaultWebRootWithoutConfiguredRootFailsClosed(t *testing.T) {
	t.Parallel()

	for _, root := range []string{"", "   ", "relative/sites"} {
		if got := DefaultWebRoot(root, "example.com"); got != "" {
			t.Fatalf("DefaultWebRoot(%q) = %q, want empty", root, got)
		}
	}
}

func TestNewAndPM2CompatibilityConstructorsLeaveVhostsRootUnconfigured(t *testing.T) {
	t.Parallel()

	constructors := map[string]*Service{
		"New":                        New(),
		"NewWithPM2":                 NewWithPM2(pm2.Config{}),
		"NewWithPM2AndVhostsRoot":    NewWithPM2AndVhostsRoot(pm2.Config{}, ""),
		"NewWithPM2AndVhostsRoot ws": NewWithPM2AndVhostsRoot(pm2.Config{}, "   "),
	}
	for name, service := range constructors {
		if service.vhostsRoot != "" {
			t.Errorf("%s vhostsRoot = %q, want unconfigured", name, service.vhostsRoot)
		}
		if got := service.Check("example.com", nil).SuggestedWebroot; got != "" {
			t.Errorf("%s suggested webroot = %q, want empty", name, got)
		}
	}
}

func TestCheckUsesInstallationVhostsRoot(t *testing.T) {
	service := NewWithPM2AndVhostsRoot(pm2.Config{}, "/srv/hserver/sites")
	result := service.Check("portal.example.com", nil)
	if result.SuggestedWebroot != "/srv/hserver/sites/example.com/portal.example.com/public_html" {
		t.Fatalf("suggested webroot = %q", result.SuggestedWebroot)
	}
}

func TestServiceUsesInstallationNginxPaths(t *testing.T) {
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
	configPath := filepath.Join(sitesAvailable, "app.example.com.conf")
	if err := os.WriteFile(configPath, []byte("server { listen 80; server_name app.example.com; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(configPath, filepath.Join(sitesEnabled, filepath.Base(configPath))); err != nil {
		t.Fatal(err)
	}

	service := NewWithConfig(ServiceConfig{
		VhostsRoot:     filepath.Join(root, "sites"),
		SitesAvailable: sitesAvailable,
		SitesEnabled:   sitesEnabled,
		SnippetsDir:    filepath.Join(root, "snippets"),
	})
	domains, err := service.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(domains) != 1 || domains[0].ID != "app.example.com" || domains[0].Name != "app.example.com" || !domains[0].IsActive {
		t.Fatalf("List() = %+v", domains)
	}
}

func TestListReportsMissingConfiguredDirectoryUnavailable(t *testing.T) {
	root := t.TempDir()
	service := NewWithConfig(ServiceConfig{
		VhostsRoot:     filepath.Join(root, "sites"),
		SitesAvailable: filepath.Join(root, "missing-available"),
		SitesEnabled:   filepath.Join(root, "enabled"),
	})
	if _, err := service.List(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("List() error = %v, want ErrNotConfigured", err)
	}
}

func TestServiceRejectsRelativeInstallationPaths(t *testing.T) {
	t.Parallel()

	service := NewWithConfig(ServiceConfig{
		VhostsRoot:     "relative/sites",
		SitesAvailable: "relative/available",
		SitesEnabled:   "relative/enabled",
	})
	if _, err := service.List(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("List() error = %v, want ErrNotConfigured", err)
	}
	if err := service.Create(CreateRequest{Domain: "app.example.com", Type: "static"}); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("Create() error = %v, want ErrNotConfigured", err)
	}
	if got := service.Check("app.example.com", nil).SuggestedWebroot; got != "" {
		t.Fatalf("SuggestedWebroot = %q, want empty", got)
	}
}

func TestCreateWithoutCertificateKeepsHTTPConfig(t *testing.T) {
	root := t.TempDir()
	service, configPath := newDomainCreateTestService(t, root, "")

	result, err := service.CreateWithResult(CreateRequest{
		Domain:  "plain.example.com",
		Type:    "static",
		WebRoot: filepath.Join(root, "web"),
	})
	if err != nil {
		t.Fatalf("CreateWithResult() error = %v", err)
	}
	if result.CertificateIssued || result.HTTPSActive || result.Warning != "" {
		t.Fatalf("result = %+v", result)
	}
	content, err := os.ReadFile(configPath("plain.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "listen 443") || strings.Contains(string(content), "ssl_certificate") {
		t.Fatalf("HTTP-only domain references TLS:\n%s", content)
	}
	if !strings.Contains(string(content), filepath.Join(root, "snippets", "hserver-static-cache.conf")) {
		t.Fatalf("domain config does not use installation-owned managed snippets:\n%s", content)
	}
}

func TestCreateNeverReplacesExistingDomainConfig(t *testing.T) {
	root := t.TempDir()
	service, configPath := newDomainCreateTestService(t, root, "")
	path := configPath("existing.example.com")
	const existing = "# operator-owned existing config\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}
	err := service.Create(CreateRequest{
		Domain:  "existing.example.com",
		Type:    "static",
		WebRoot: filepath.Join(root, "web"),
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Create() error = %v, want ErrConflict", err)
	}
	content, readErr := os.ReadFile(path)
	if readErr != nil || string(content) != existing {
		t.Fatalf("existing config changed: content=%q err=%v", content, readErr)
	}
}

func TestDomainIdentityMutationsRequireObservedConfig(t *testing.T) {
	root := t.TempDir()
	sitesAvailable := filepath.Join(root, "available")
	sitesEnabled := filepath.Join(root, "enabled")
	for _, path := range []string{sitesAvailable, sitesEnabled} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	service := NewWithConfig(ServiceConfig{
		VhostsRoot:     filepath.Join(root, "sites"),
		SitesAvailable: sitesAvailable,
		SitesEnabled:   sitesEnabled,
	})
	if err := service.Delete("missing.example.com", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete() error = %v, want ErrNotFound", err)
	}
	if err := service.Toggle("missing.example.com", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Toggle() error = %v, want ErrNotFound", err)
	}
}

func TestDeleteFilesRemovesCanonicalSubdomainTree(t *testing.T) {
	root := t.TempDir()
	service, configPath := newDomainCreateTestService(t, root, "")
	if err := os.WriteFile(configPath("app.example.com"), []byte("server { server_name app.example.com; }"), 0o644); err != nil {
		t.Fatal(err)
	}
	siteRoot := filepath.Join(root, "sites", "example.com", "app.example.com")
	if err := os.MkdirAll(filepath.Join(siteRoot, "public_html"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete("app.example.com", true); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := os.Stat(siteRoot); !os.IsNotExist(err) {
		t.Fatalf("subdomain tree still exists or stat failed unexpectedly: %v", err)
	}
}

func TestCreateRejectsUnsafePHPVersionBeforeMutation(t *testing.T) {
	root := t.TempDir()
	service, configPath := newDomainCreateTestService(t, root, "")
	err := service.Create(CreateRequest{
		Domain:     "php.example.com",
		Type:       "php",
		PHPVersion: "../../etc",
		WebRoot:    filepath.Join(root, "web"),
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Create() error = %v, want ErrInvalidRequest", err)
	}
	if _, err := os.Stat(configPath("php.example.com")); !os.IsNotExist(err) {
		t.Fatalf("config created before PHP version rejection: %v", err)
	}
}

func TestCreateRejectsIncompleteManagedSnippetSetBeforeMutation(t *testing.T) {
	root := t.TempDir()
	service, configPath := newDomainCreateTestService(t, root, "")
	missing := filepath.Join(root, "snippets", "hserver-static-cache.conf")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}

	_, err := service.CreateWithResult(CreateRequest{
		Domain:  "missing-snippet.example.com",
		Type:    "static",
		WebRoot: filepath.Join(root, "web"),
	})
	if !errors.Is(err, ErrNotConfigured) || !strings.Contains(err.Error(), "hserver-static-cache.conf") {
		t.Fatalf("CreateWithResult() error = %v", err)
	}
	if _, statErr := os.Stat(configPath("missing-snippet.example.com")); !os.IsNotExist(statErr) {
		t.Fatalf("Nginx config mutated before snippet preflight: %v", statErr)
	}
}

func TestCreateStaticSPAModeUsesIndexFallback(t *testing.T) {
	root := t.TempDir()
	service, configPath := newDomainCreateTestService(t, root, "")

	_, err := service.CreateWithResult(CreateRequest{
		Domain:  "spa.example.com",
		Type:    "static",
		WebRoot: filepath.Join(root, "web"),
		SPAMode: true,
	})
	if err != nil {
		t.Fatalf("CreateWithResult() error = %v", err)
	}
	content, err := os.ReadFile(configPath("spa.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "try_files $uri $uri/ /index.html;") {
		t.Fatalf("SPA fallback missing from generated config:\n%s", content)
	}
}

func TestCreateRejectsUnknownFPMPresetBeforeMutation(t *testing.T) {
	root := t.TempDir()
	service, configPath := newDomainCreateTestService(t, root, "")

	_, err := service.CreateWithResult(CreateRequest{
		Domain:     "php.example.com",
		Type:       "php",
		WebRoot:    filepath.Join(root, "web"),
		FPMPreset:  "unbounded",
		PHPVersion: "8.4",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("CreateWithResult() error = %v, want ErrInvalidRequest", err)
	}
	if _, statErr := os.Stat(configPath("php.example.com")); !os.IsNotExist(statErr) {
		t.Fatalf("nginx config was mutated before preset rejection: %v", statErr)
	}
}

func TestCreateRejectsInvalidNodeEnvironmentBeforeMutation(t *testing.T) {
	root := t.TempDir()
	service, configPath := newDomainCreateTestService(t, root, "")

	_, err := service.CreateWithResult(CreateRequest{
		Domain:    "node.example.com",
		Type:      "proxy",
		ProxyPort: 3000,
		NodeEnv:   "staging",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("CreateWithResult() error = %v, want ErrInvalidRequest", err)
	}
	if _, statErr := os.Stat(configPath("node.example.com")); !os.IsNotExist(statErr) {
		t.Fatalf("nginx config was mutated before nodeEnv rejection: %v", statErr)
	}
}

func TestCreateRejectsPartialPM2DefinitionBeforeMutation(t *testing.T) {
	root := t.TempDir()
	service, configPath := newDomainCreateTestService(t, root, "")

	_, err := service.CreateWithResult(CreateRequest{
		Domain:    "node.example.com",
		Type:      "proxy",
		ProxyPort: 3000,
		PM2App:    "node-app",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("CreateWithResult() error = %v, want ErrInvalidRequest", err)
	}
	if _, statErr := os.Stat(configPath("node.example.com")); !os.IsNotExist(statErr) {
		t.Fatalf("nginx config was mutated before PM2 definition rejection: %v", statErr)
	}
}

func TestPHPTemplatesApplySelectedPoolPreset(t *testing.T) {
	low, err := phpsvc.GetPreset("low")
	if err != nil {
		t.Fatal(err)
	}
	high, err := phpsvc.GetPreset("high")
	if err != nil {
		t.Fatal(err)
	}

	lowContent := phpPoolTemplate("8.4", "low_example_com", "/srv/www/low", low.Config)
	for _, want := range []string{
		"user = www-data",
		"group = www-data",
		"pm = ondemand",
		"pm.max_children = 5",
		"pm.process_idle_timeout = 10s",
		"php_admin_value[memory_limit] = 128M",
	} {
		if !strings.Contains(lowContent, want) {
			t.Fatalf("low preset output missing %q:\n%s", want, lowContent)
		}
	}
	highContent := phpPoolTemplateIsolated("8.4", "high_example_com", "/srv/www/high", "portaluser", high.Config)
	for _, want := range []string{
		"pm = dynamic",
		"pm.max_children = 30",
		"pm.start_servers = 5",
		"php_admin_value[memory_limit] = 512M",
	} {
		if !strings.Contains(highContent, want) {
			t.Fatalf("high preset output missing %q:\n%s", want, highContent)
		}
	}
}

func TestCreateUsesConfiguredCertbotAndPromotesHTTPS(t *testing.T) {
	root := t.TempDir()
	certbotArgs := filepath.Join(root, "certbot-args")
	certbotBin := filepath.Join(root, "certbot-custom")
	writeExecutable(t, certbotBin, "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CERTBOT_ARGS_FILE\"\n")
	t.Setenv("CERTBOT_ARGS_FILE", certbotArgs)

	service, configPath := newDomainCreateTestService(t, root, certbotBin)
	result, err := service.CreateWithResult(CreateRequest{
		Domain:      "secure.example.com",
		Type:        "static",
		WebRoot:     filepath.Join(root, "web"),
		IssueSSL:    true,
		SSLEmail:    "admin@example.com",
		WWWRedirect: true,
	})
	if err != nil {
		t.Fatalf("CreateWithResult() error = %v", err)
	}
	if !result.CertificateIssued || !result.HTTPSActive || result.Warning != "" {
		t.Fatalf("result = %+v", result)
	}
	content, err := os.ReadFile(configPath("secure.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	certDir := filepath.Join(root, "certbot")
	if !strings.Contains(string(content), "listen 443 ssl") ||
		!strings.Contains(string(content), filepath.Join(certDir, "live", "secure.example.com", "fullchain.pem")) {
		t.Fatalf("HTTPS config does not use configured certificate root:\n%s", content)
	}
	args, err := os.ReadFile(certbotArgs)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--config-dir", certDir, "--webroot", filepath.Join(root, "acme"), "-d", "secure.example.com"} {
		if !strings.Contains(string(args), want) {
			t.Fatalf("certbot args missing %q:\n%s", want, args)
		}
	}
}

func TestCreateRunsCertificateHookAfterHTTPAndBeforeCertbot(t *testing.T) {
	root := t.TempDir()
	eventsPath := filepath.Join(root, "events")
	certbotBin := filepath.Join(root, "certbot-order")
	writeExecutable(t, certbotBin, "#!/bin/sh\nprintf '%s\\n' certbot >> \"$EVENT_LOG\"\n")
	t.Setenv("EVENT_LOG", eventsPath)
	service, configPath := newDomainCreateTestService(t, root, certbotBin)

	result, err := service.CreateWithHooks(CreateRequest{
		Domain:   "ordered.example.com",
		Type:     "static",
		WebRoot:  filepath.Join(root, "web"),
		IssueSSL: true,
		SSLEmail: "admin@example.com",
	}, CreateHooks{BeforeCertificate: func() error {
		content, readErr := os.ReadFile(configPath("ordered.example.com"))
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(content), "listen 443") {
			return errors.New("HTTPS activated before certificate prerequisite")
		}
		file, openErr := os.OpenFile(eventsPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if openErr != nil {
			return openErr
		}
		defer file.Close()
		_, writeErr := file.WriteString("dns\n")
		return writeErr
	}})
	if err != nil {
		t.Fatalf("CreateWithHooks() error = %v", err)
	}
	if !result.CertificateIssued || !result.HTTPSActive || result.Warning != "" {
		t.Fatalf("result = %+v", result)
	}
	events, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(events), "dns\ncertbot\n"; got != want {
		t.Fatalf("event order = %q, want %q", got, want)
	}
}

func TestCreateSkipsCertbotWhenCertificateHookFails(t *testing.T) {
	root := t.TempDir()
	certbotMarker := filepath.Join(root, "certbot-ran")
	certbotBin := filepath.Join(root, "certbot-must-not-run")
	writeExecutable(t, certbotBin, "#!/bin/sh\ntouch \"$CERTBOT_MARKER\"\n")
	t.Setenv("CERTBOT_MARKER", certbotMarker)
	service, configPath := newDomainCreateTestService(t, root, certbotBin)

	result, err := service.CreateWithHooks(CreateRequest{
		Domain:   "dns-failed.example.com",
		Type:     "static",
		WebRoot:  filepath.Join(root, "web"),
		IssueSSL: true,
		SSLEmail: "admin@example.com",
	}, CreateHooks{BeforeCertificate: func() error {
		return errors.New("DNS reconciliation unavailable")
	}})
	if err != nil {
		t.Fatalf("CreateWithHooks() error = %v", err)
	}
	if result.CertificateIssued || result.HTTPSActive || !strings.Contains(result.Warning, "DNS reconciliation unavailable") {
		t.Fatalf("result = %+v", result)
	}
	if _, statErr := os.Stat(certbotMarker); !os.IsNotExist(statErr) {
		t.Fatalf("Certbot ran after failed prerequisite: %v", statErr)
	}
	content, readErr := os.ReadFile(configPath("dns-failed.example.com"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	if strings.Contains(string(content), "listen 443") {
		t.Fatalf("failed prerequisite activated HTTPS:\n%s", content)
	}
}

func TestCreateKeepsHTTPWhenCertbotFails(t *testing.T) {
	root := t.TempDir()
	certbotBin := filepath.Join(root, "certbot-fails")
	writeExecutable(t, certbotBin, "#!/bin/sh\necho 'challenge failed' >&2\nexit 9\n")
	service, configPath := newDomainCreateTestService(t, root, certbotBin)

	result, err := service.CreateWithResult(CreateRequest{
		Domain:   "fallback.example.com",
		Type:     "static",
		WebRoot:  filepath.Join(root, "web"),
		IssueSSL: true,
		SSLEmail: "admin@example.com",
	})
	if err != nil {
		t.Fatalf("CreateWithResult() error = %v", err)
	}
	if result.CertificateIssued || result.HTTPSActive || !strings.Contains(result.Warning, "challenge failed") {
		t.Fatalf("result = %+v", result)
	}
	content, err := os.ReadFile(configPath("fallback.example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "listen 443") || !strings.Contains(string(content), filepath.Join(root, "acme")) {
		t.Fatalf("failed issuance did not retain ACME-capable HTTP config:\n%s", content)
	}
}

func newDomainCreateTestService(t *testing.T, root, certbotBin string) (*Service, func(string) string) {
	t.Helper()
	fakeBin := filepath.Join(root, "bin")
	if err := os.MkdirAll(fakeBin, 0o755); err != nil {
		t.Fatal(err)
	}
	writeExecutable(t, filepath.Join(fakeBin, "nginx"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(fakeBin, "systemctl"), "#!/bin/sh\nexit 0\n")
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	sitesAvailable := filepath.Join(root, "available")
	sitesEnabled := filepath.Join(root, "enabled")
	snippetsDir := filepath.Join(root, "snippets")
	for _, dir := range []string{sitesAvailable, sitesEnabled, snippetsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range requiredDomainSnippetNames {
		if err := os.WriteFile(filepath.Join(snippetsDir, name), []byte("# test\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	service := NewWithConfig(ServiceConfig{
		VhostsRoot:     filepath.Join(root, "sites"),
		SitesAvailable: sitesAvailable,
		SitesEnabled:   sitesEnabled,
		SnippetsDir:    snippetsDir,
		CertbotBin:     certbotBin,
		CertbotDir:     filepath.Join(root, "certbot"),
		ACMEWebroot:    filepath.Join(root, "acme"),
	})
	return service, func(domain string) string {
		return filepath.Join(sitesAvailable, domain+".conf")
	}
}

func writeExecutable(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestPhpTemplate_ContainsExpectedValues(t *testing.T) {
	t.Parallel()

	out := phpTemplate(
		"example.com",
		"8.4",
		"example_com",
		"/var/www/vhosts/example.com/public_html",
		"/etc/letsencrypt/live/example.com/fullchain.pem",
		"/etc/letsencrypt/live/example.com/privkey.pem",
		false,
	)

	for _, needle := range []string{
		"server_name example.com",
		"fastcgi_pass unix:/run/php/php8.4-fpm-example_com.sock",
		"ssl_certificate /etc/letsencrypt/live/example.com/fullchain.pem",
	} {
		if !strings.Contains(out, needle) {
			t.Errorf("phpTemplate missing %q", needle)
		}
	}
}
