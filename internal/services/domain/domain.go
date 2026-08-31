package domain

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
	phpsvc "github.com/IamYGT/heyserver/internal/services/php"
	"github.com/IamYGT/heyserver/internal/services/pm2"
	"github.com/IamYGT/heyserver/internal/services/portaluser"
	"github.com/IamYGT/heyserver/internal/services/shell"
)

const (
	defaultSitesAvailable = "/etc/nginx/sites-available"
	defaultSitesEnabled   = "/etc/nginx/sites-enabled"
	defaultSnippetsDir    = "/etc/nginx/snippets"
	defaultCertbotBin     = "certbot"
	defaultCertbotDir     = "/etc/letsencrypt"
	defaultACMEWebroot    = "/var/www/hserver-acme"
	phpFPMPoolDir         = "/etc/php/%s/fpm/pool.d"
)

var validCommandName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
var validPHPVersion = regexp.MustCompile(`^(7\.4|8\.[0-5])$`)

var requiredDomainSnippetNames = []string{
	"hserver-ssl-params.conf",
	"hserver-security-headers.conf",
	"hserver-security-deny.conf",
	"hserver-static-cache.conf",
	"hserver-php-fpm.conf",
	"hserver-proxy-params.conf",
}

// ErrNotConfigured identifies missing or invalid installation-owned paths.
var ErrNotConfigured = errors.New("domain paths are not configured")

// ErrInvalidRequest identifies a caller-correctable domain provisioning request.
var ErrInvalidRequest = errors.New("invalid domain provisioning request")

// ErrNotFound identifies an observed domain identity that no longer exists.
var ErrNotFound = errors.New("domain not found")

// ErrConflict identifies an existing Nginx identity that must not be replaced.
var ErrConflict = errors.New("domain already exists")

// ServiceConfig binds domain inventory and mutations to one installation.
type ServiceConfig struct {
	PM2            pm2.Config
	VhostsRoot     string
	SitesAvailable string
	SitesEnabled   string
	SnippetsDir    string
	CertbotBin     string
	CertbotDir     string
	ACMEWebroot    string
}

// Service manages nginx domain configurations.
type Service struct {
	pm2Config      pm2.Config
	vhostsRoot     string
	sitesAvailable string
	sitesEnabled   string
	snippetsDir    string
	certbotBin     string
	certbotDir     string
	acmeWebroot    string
}

func New() *Service {
	return NewWithConfig(ServiceConfig{
		SitesAvailable: defaultSitesAvailable,
		SitesEnabled:   defaultSitesEnabled,
		SnippetsDir:    defaultSnippetsDir,
		CertbotBin:     defaultCertbotBin,
		CertbotDir:     defaultCertbotDir,
		ACMEWebroot:    defaultACMEWebroot,
	})
}

// NewWithPM2 configures optional PM2 deployment for proxy domains. The
// integration is validated only when a request actually asks Heyserver to start
// a PM2 application, so ordinary domain management remains available when PM2
// is not configured.
func NewWithPM2(config pm2.Config) *Service {
	return NewWithPM2AndVhostsRoot(config, "")
}

// NewWithPM2AndVhostsRoot configures domain creation for an installation-owned
// document root without changing the standard Nginx inventory locations.
func NewWithPM2AndVhostsRoot(config pm2.Config, root string) *Service {
	return NewWithConfig(ServiceConfig{
		PM2:            config,
		VhostsRoot:     root,
		SitesAvailable: defaultSitesAvailable,
		SitesEnabled:   defaultSitesEnabled,
		SnippetsDir:    defaultSnippetsDir,
		CertbotBin:     defaultCertbotBin,
		CertbotDir:     defaultCertbotDir,
		ACMEWebroot:    defaultACMEWebroot,
	})
}

// NewWithConfig creates an installation-scoped domain service. Invalid
// relative paths remain unavailable instead of falling back to another
// installation's filesystem locations.
func NewWithConfig(config ServiceConfig) *Service {
	return &Service{
		pm2Config:      config.PM2,
		vhostsRoot:     cleanAbsoluteDir(config.VhostsRoot),
		sitesAvailable: cleanAbsoluteDir(config.SitesAvailable),
		sitesEnabled:   cleanAbsoluteDir(config.SitesEnabled),
		snippetsDir:    cleanAbsoluteDir(config.SnippetsDir),
		certbotBin:     cleanCommand(config.CertbotBin),
		certbotDir:     cleanAbsoluteDir(config.CertbotDir),
		acmeWebroot:    cleanAbsoluteDir(config.ACMEWebroot),
	}
}

func cleanCommand(command string) string {
	command = strings.TrimSpace(command)
	if strings.Contains(command, "/") {
		if !filepath.IsAbs(command) || filepath.Clean(command) != command {
			return ""
		}
		return command
	}
	if !validCommandName.MatchString(command) {
		return ""
	}
	return command
}

func cleanAbsoluteDir(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	return filepath.Clean(path)
}

func (s *Service) requireNginxPaths() error {
	if s.sitesAvailable == "" || s.sitesEnabled == "" {
		return fmt.Errorf("%w: sites directories must be configured as absolute paths", ErrNotConfigured)
	}
	return nil
}

func (s *Service) requireSnippetsDir() error {
	if s.snippetsDir == "" {
		return fmt.Errorf("%w: Nginx snippets directory must be configured as an absolute path", ErrNotConfigured)
	}
	return nil
}

func (s *Service) requireManagedSnippets() error {
	if err := s.requireSnippetsDir(); err != nil {
		return err
	}
	for _, name := range requiredDomainSnippetNames {
		info, err := os.Stat(filepath.Join(s.snippetsDir, name))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("%w: managed Nginx snippet %s is missing from %s", ErrNotConfigured, name, s.snippetsDir)
		}
	}
	return nil
}

func (s *Service) requireVhostsRoot() error {
	if s.vhostsRoot == "" {
		return fmt.Errorf("%w: vhosts root must be configured as an absolute path", ErrNotConfigured)
	}
	return nil
}

func (s *Service) requireCertificateConfig() error {
	if s.certbotBin == "" || s.certbotDir == "" || s.acmeWebroot == "" {
		return fmt.Errorf("%w: Certbot binary, config directory, and ACME webroot must be configured", ErrNotConfigured)
	}
	return nil
}

// DefaultWebRoot returns the conventional document root for a domain below an
// installation-owned vhost root. It returns an empty string when root is not
// configured as an absolute path.
func DefaultWebRoot(root, domain string) string {
	root = cleanAbsoluteDir(root)
	if root == "" {
		return ""
	}
	domain = sanitizeDomain(domain)
	parts := strings.Split(domain, ".")
	if len(parts) > 2 {
		parent := strings.Join(parts[len(parts)-2:], ".")
		return filepath.Join(root, parent, domain, "public_html")
	}
	return filepath.Join(root, domain, "public_html")
}

// DomainDetail extends models.Domain with nginx config content and pool info.
type DomainDetail struct {
	models.Domain
	ServerNames      []string `json:"serverNames"`
	NginxConfig      string   `json:"nginxConfig"` // filename
	NginxContent     string   `json:"nginxContent"`
	PhpPoolPath      string   `json:"phpPoolPath,omitempty"`
	PhpPoolContent   string   `json:"phpPoolContent,omitempty"`
	SslCertPath      string   `json:"sslCertPath,omitempty"`
	SslKeyPath       string   `json:"sslKeyPath,omitempty"`
	AccessLogPath    string   `json:"accessLogPath"`
	ErrorLogPath     string   `json:"errorLogPath"`
	SslDaysRemaining int      `json:"sslDaysRemaining,omitempty"`
}

// HealthResult holds domain connectivity checks.
type HealthResult struct {
	Domain       string `json:"domain"`
	DNSResolves  bool   `json:"dnsResolves"`
	DNSIP        string `json:"dnsIp,omitempty"`
	SSLValid     bool   `json:"sslValid"`
	SSLDays      int    `json:"sslDaysRemaining,omitempty"`
	HTTPStatus   int    `json:"httpStatus,omitempty"`
	HTTPSStatus  int    `json:"httpsStatus,omitempty"`
	ResponseTime int64  `json:"responseTime,omitempty"`
}

// CreateRequest holds parameters for domain creation.
type CreateRequest struct {
	Domain          string `json:"domain"`
	Type            string `json:"type"` // php | proxy | static
	PHPVersion      string `json:"phpVersion,omitempty"`
	ProxyPort       int    `json:"proxyPort,omitempty"`
	WebRoot         string `json:"webRoot,omitempty"`
	FPMPreset       string `json:"fpmPreset,omitempty"` // low | medium | high
	SPAMode         bool   `json:"spaMode"`
	WWWRedirect     bool   `json:"wwwRedirect"`
	IssueSSL        bool   `json:"issueSSL"`
	SSLEmail        string `json:"sslEmail,omitempty"`
	ExistingCert    string `json:"existingCertName,omitempty"`
	CreateDNSRecord bool   `json:"createDnsRecord"`

	// PM2 fields — only used when Type == "proxy"
	PM2App    string `json:"pm2_app"`           // PM2 app name (optional)
	PM2Script string `json:"pm2_script"`        // Script path, e.g. "server.js" or absolute
	PM2Cwd    string `json:"pm2_cwd"`           // Working directory for the app
	PM2Port   int    `json:"pm2_port"`          // App port (overrides ProxyPort when set)
	NodeEnv   string `json:"nodeEnv,omitempty"` // production | development

	// IsolatedLinuxUser creates a dedicated system user, owns the site tree with
	// that identity, and narrows open_basedir to the site install root only.
	IsolatedLinuxUser bool `json:"isolatedLinuxUser"`
}

// CreateResult reports non-fatal work that could not be completed after the
// HTTP domain became active.
type CreateResult struct {
	CertificateIssued bool
	HTTPSActive       bool
	Warning           string
}

// CreateHooks lets the API complete provider-owned prerequisites only after
// the local HTTP site and runtime are active, but before certificate issuance.
type CreateHooks struct {
	BeforeCertificate func() error
}

// ─── List ─────────────────────────────────────────────────────────────────────

// List reads all .conf files from sites-available and returns summaries.
func (s *Service) List() ([]models.Domain, error) {
	if err := s.requireNginxPaths(); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(s.sitesAvailable)
	if err != nil {
		return nil, fmt.Errorf("%w: read sites-available: %v", ErrNotConfigured, err)
	}

	var domains []models.Domain
	for _, e := range entries {
		if e.IsDir() || (!strings.HasSuffix(e.Name(), ".conf") && strings.Contains(e.Name(), ".")) {
			continue
		}
		content, err := os.ReadFile(filepath.Join(s.sitesAvailable, e.Name()))
		if err != nil {
			continue
		}
		d := parseConfigWithSitesEnabled(e.Name(), string(content), s.sitesEnabled)
		domains = append(domains, d)
	}

	if len(domains) == 0 {
		return []models.Domain{}, nil
	}

	return domains, nil
}

// Get returns full detail for a domain by id (filename slug).
func (s *Service) Get(id string) (*DomainDetail, error) {
	if err := s.requireNginxPaths(); err != nil {
		return nil, err
	}
	domain, err := normalizeDomainID(id)
	if err != nil {
		return nil, err
	}
	filename := domain + ".conf"

	contentBytes, err := os.ReadFile(filepath.Join(s.sitesAvailable, filename))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNotFound, domain)
		}
		return nil, fmt.Errorf("read domain %q: %w", domain, err)
	}

	content := string(contentBytes)
	d := parseConfigWithSitesEnabled(filename, content, s.sitesEnabled)

	detail := &DomainDetail{
		Domain:        d,
		ServerNames:   parseServerNames(content),
		NginxConfig:   filename,
		NginxContent:  content,
		AccessLogPath: "/var/log/nginx/" + d.Name + "-access.log",
		ErrorLogPath:  "/var/log/nginx/" + d.Name + "-error.log",
	}

	if s.certbotDir != "" {
		certDir := filepath.Join(s.certbotDir, "live", d.Name)
		detail.SslCertPath = filepath.Join(certDir, "fullchain.pem")
		detail.SslKeyPath = filepath.Join(certDir, "privkey.pem")
	}

	// PHP pool
	if d.PHPVersion != "" {
		poolPath := fmt.Sprintf(phpFPMPoolDir, d.PHPVersion) + "/" + d.Name + ".conf"
		if data, err := os.ReadFile(poolPath); err == nil {
			detail.PhpPoolPath = poolPath
			detail.PhpPoolContent = string(data)
		}
	}

	return detail, nil
}

// ─── Create ───────────────────────────────────────────────────────────────────

// Create sets up nginx config, PHP-FPM pool (if PHP), web dir, and optionally SSL.
// After each service config write the corresponding service is reloaded.
// If nginx config test fails the written config is removed before returning.
func (s *Service) Create(req CreateRequest) error {
	return s.create(req, nil, CreateHooks{})
}

// CreateWithResult creates a domain and exposes non-fatal certificate warnings
// to API callers without treating an active HTTP site as a total failure.
func (s *Service) CreateWithResult(req CreateRequest) (CreateResult, error) {
	return s.CreateWithHooks(req, CreateHooks{})
}

// CreateWithHooks preserves the HTTP-first transaction while allowing a DNS
// provider reconciliation to run at the only safe point before Certbot.
func (s *Service) CreateWithHooks(req CreateRequest, hooks CreateHooks) (CreateResult, error) {
	result := CreateResult{}
	err := s.create(req, &result, hooks)
	return result, err
}

func (s *Service) create(req CreateRequest, result *CreateResult, hooks CreateHooks) error {
	if err := s.requireNginxPaths(); err != nil {
		return err
	}
	safeDomain, err := NormalizeDomain(req.Domain)
	if err != nil {
		return err
	}
	req.Domain = safeDomain
	switch req.Type {
	case "php", "proxy", "static":
	default:
		return fmt.Errorf("%w: type must be php, proxy, or static", ErrInvalidRequest)
	}
	if req.Type == "proxy" {
		if req.ProxyPort < 0 || req.ProxyPort > 65535 || req.PM2Port < 0 || req.PM2Port > 65535 {
			return fmt.Errorf("%w: proxy ports must be between 1 and 65535 when supplied", ErrInvalidRequest)
		}
		req.NodeEnv = strings.TrimSpace(req.NodeEnv)
		if req.NodeEnv != "" && req.NodeEnv != "production" && req.NodeEnv != "development" {
			return fmt.Errorf("%w: nodeEnv must be production or development", ErrInvalidRequest)
		}
		if (strings.TrimSpace(req.PM2App) == "") != (strings.TrimSpace(req.PM2Script) == "") {
			return fmt.Errorf("%w: pm2_app and pm2_script must be provided together", ErrInvalidRequest)
		}
		if req.PM2App != "" && req.NodeEnv == "" {
			req.NodeEnv = "production"
		}
	}
	if len(req.WebRoot) > 4096 || strings.ContainsAny(req.WebRoot, "\r\n\x00") {
		return fmt.Errorf("%w: webRoot is invalid", ErrInvalidRequest)
	}
	if req.WebRoot != "" {
		if !filepath.IsAbs(req.WebRoot) {
			return fmt.Errorf("%w: webRoot must be an absolute path", ErrInvalidRequest)
		}
		req.WebRoot = filepath.Clean(req.WebRoot)
	}
	if req.Type != "proxy" && strings.TrimSpace(req.WebRoot) == "" {
		if err := s.requireVhostsRoot(); err != nil {
			return err
		}
	}
	if req.IssueSSL {
		req.SSLEmail = strings.TrimSpace(req.SSLEmail)
		address, emailErr := mail.ParseAddress(req.SSLEmail)
		if req.SSLEmail == "" || len(req.SSLEmail) > 254 || emailErr != nil || address.Address != req.SSLEmail {
			return fmt.Errorf("%w: sslEmail must be a valid email address", ErrInvalidRequest)
		}
		if err := s.requireCertificateConfig(); err != nil {
			return err
		}
	}
	if req.ExistingCert != "" {
		req.ExistingCert = strings.TrimSpace(req.ExistingCert)
		if len(req.ExistingCert) > 253 || req.ExistingCert == "." || req.ExistingCert == ".." || sanitize(req.ExistingCert) != req.ExistingCert {
			return fmt.Errorf("%w: existingCertName is invalid", ErrInvalidRequest)
		}
		if s.certbotDir == "" {
			return fmt.Errorf("%w: Certbot config directory must be configured", ErrNotConfigured)
		}
	}
	var fpmPreset phpsvc.PoolConfig
	if req.Type == "php" {
		if req.PHPVersion == "" {
			req.PHPVersion = "8.4"
		}
		if !validPHPVersion.MatchString(req.PHPVersion) {
			return fmt.Errorf("%w: phpVersion is not supported", ErrInvalidRequest)
		}
		presetName := strings.TrimSpace(req.FPMPreset)
		if presetName == "" {
			presetName = "medium"
		}
		if presetName != "low" && presetName != "medium" && presetName != "high" {
			return fmt.Errorf("%w: FPM preset %q must be low, medium, or high", ErrInvalidRequest, presetName)
		}
		preset, err := phpsvc.GetPreset(presetName)
		if err != nil {
			return fmt.Errorf("%w: resolve FPM preset %q: %v", ErrInvalidRequest, presetName, err)
		}
		fpmPreset = preset.Config
	}
	if err := s.requireManagedSnippets(); err != nil {
		return err
	}
	configPath := filepath.Join(s.sitesAvailable, safeDomain+".conf")
	if _, err := os.Lstat(configPath); err == nil {
		return fmt.Errorf("%w: %s", ErrConflict, safeDomain)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect existing domain config: %w", err)
	}

	// ── Subdomain web-root heuristic ──────────────────────────────────────────
	// e.g. sub.domain.com  →  <vhosts-root>/domain.com/sub.domain.com/public_html
	// e.g. domain.com      →  <vhosts-root>/domain.com/public_html
	webRoot := req.WebRoot
	if webRoot == "" && req.Type != "proxy" {
		webRoot = DefaultWebRoot(s.vhostsRoot, safeDomain)
	}

	certName := safeDomain
	if req.ExistingCert != "" {
		certName = sanitize(req.ExistingCert)
	}
	sslCertPath := filepath.Join(s.certbotDir, "live", certName, "fullchain.pem")
	sslKeyPath := filepath.Join(s.certbotDir, "live", certName, "privkey.pem")

	// ── Create web root ───────────────────────────────────────────────────────
	if req.Type != "proxy" {
		if err := os.MkdirAll(webRoot, 0o755); err != nil {
			return fmt.Errorf("create web root: %w", err)
		}
	}
	if req.IssueSSL {
		if err := os.MkdirAll(s.acmeWebroot, 0o755); err != nil {
			return fmt.Errorf("create ACME webroot: %w", err)
		}
	}

	// ── Resolve proxy port ────────────────────────────────────────────────────
	proxyPort := req.ProxyPort
	if req.PM2Port > 0 {
		proxyPort = req.PM2Port // PM2Port takes precedence when set
	}
	if proxyPort <= 0 {
		proxyPort = 3000
	}

	// A new certificate cannot be issued behind an HTTPS redirect that points at
	// files which do not exist yet. Start with an HTTP configuration and promote
	// it only after the webroot challenge succeeds.
	initialTLS := req.ExistingCert != "" && !req.IssueSSL
	acmeWebroot := ""
	if req.IssueSSL {
		acmeWebroot = s.acmeWebroot
	}
	nginxContent := domainNginxContent(req, safeDomain, webRoot, proxyPort, sslCertPath, sslKeyPath, initialTLS, acmeWebroot, s.snippetsDir)

	configFile, err := os.OpenFile(configPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if os.IsExist(err) {
			return fmt.Errorf("%w: %s", ErrConflict, safeDomain)
		}
		return fmt.Errorf("write nginx config: %w", err)
	}
	if _, err := configFile.WriteString(nginxContent); err != nil {
		_ = configFile.Close()
		_ = os.Remove(configPath)
		return fmt.Errorf("write nginx config: %w", err)
	}
	if err := configFile.Close(); err != nil {
		_ = os.Remove(configPath)
		return fmt.Errorf("close nginx config: %w", err)
	}

	// ── Enable nginx config (symlink) ─────────────────────────────────────────
	symlink := filepath.Join(s.sitesEnabled, safeDomain+".conf")
	os.Remove(symlink) //nolint:errcheck
	if err := os.Symlink(configPath, symlink); err != nil {
		os.Remove(configPath) //nolint:errcheck
		return fmt.Errorf("create symlink: %w", err)
	}

	// ── nginx config test ─────────────────────────────────────────────────────
	testResult, err := shell.ExecuteWithTimeout(15*time.Second, "nginx", "-t")
	if err != nil || (testResult != nil && testResult.ExitCode != 0) {
		// Roll back: remove config and symlink
		stderr := ""
		if testResult != nil {
			stderr = testResult.Stderr
		}
		slog.Error("nginx config test failed, rolling back", "domain", safeDomain, "stderr", stderr)
		os.Remove(symlink)    //nolint:errcheck
		os.Remove(configPath) //nolint:errcheck
		return fmt.Errorf("nginx config test failed: %s", stderr)
	}

	// ── Reload nginx ──────────────────────────────────────────────────────────
	if reloadRes, err := shell.ExecuteWithTimeout(15*time.Second, "systemctl", "reload", "nginx"); err != nil || (reloadRes != nil && reloadRes.ExitCode != 0) {
		stderr := ""
		if reloadRes != nil {
			stderr = reloadRes.Stderr
		}
		slog.Error("nginx reload failed", "domain", safeDomain, "stderr", stderr)
		return fmt.Errorf("nginx reload failed: %s", stderr)
	}
	if result != nil && initialTLS {
		result.HTTPSActive = true
	}

	// ── PHP-FPM pool ──────────────────────────────────────────────────────────
	if req.Type == "php" {
		phpVersion := req.PHPVersion
		if phpVersion == "" {
			phpVersion = "8.4"
		}
		poolName := domainToPoolName(safeDomain)
		var poolContent string
		if req.IsolatedLinuxUser {
			portalRoot := portaluser.PortalRootFromWebRoot(webRoot)
			linuxUser := portaluser.UsernameForDomain(safeDomain)
			if err := portaluser.EnsureUser(linuxUser, portalRoot); err != nil {
				return fmt.Errorf("portal isolated user: %w", err)
			}
			if err := portaluser.ChownPortalTree(portalRoot, linuxUser); err != nil {
				return fmt.Errorf("portal chown: %w", err)
			}
			poolContent = phpPoolTemplateIsolated(phpVersion, poolName, portalRoot, linuxUser, fpmPreset)
		} else {
			poolContent = phpPoolTemplate(phpVersion, poolName, webRoot, fpmPreset)
		}
		poolDir := fmt.Sprintf(phpFPMPoolDir, phpVersion)
		poolPath := filepath.Join(poolDir, safeDomain+".conf")

		if err := os.WriteFile(poolPath, []byte(poolContent), 0o644); err != nil {
			slog.Error("write php-fpm pool failed", "domain", safeDomain, "path", poolPath, "err", err)
			return fmt.Errorf("write php-fpm pool: %w", err)
		}

		// Reload PHP-FPM so the new pool is picked up immediately.
		fpmService := fmt.Sprintf("php%s-fpm", phpVersion)
		if reloadRes, err := shell.ExecuteWithTimeout(20*time.Second, "systemctl", "reload", fpmService); err != nil || (reloadRes != nil && reloadRes.ExitCode != 0) {
			stderr := ""
			if reloadRes != nil {
				stderr = reloadRes.Stderr
			}
			slog.Error("php-fpm reload failed", "service", fpmService, "domain", safeDomain, "stderr", stderr)
			return fmt.Errorf("php-fpm reload failed (%s): %s", fpmService, stderr)
		}
	}

	// ── PM2 integration (proxy type only) ────────────────────────────────────
	if req.Type == "proxy" && req.PM2App != "" && req.PM2Script != "" {
		pm2Service, err := pm2.New(s.pm2Config)
		if err != nil {
			return fmt.Errorf("pm2 start skipped: %w", err)
		}
		script := req.PM2Script
		// If script is relative, resolve against PM2Cwd
		if !filepath.IsAbs(script) && req.PM2Cwd != "" {
			script = filepath.Join(req.PM2Cwd, script)
		}

		deployReq := &pm2.DeployRequest{
			Name:     req.PM2App,
			Script:   script,
			Cwd:      req.PM2Cwd,
			ExecMode: "fork",
			NodeEnv:  req.NodeEnv,
		}

		if _, err := pm2Service.Deploy(deployReq); err != nil {
			// PM2 start failure is non-fatal: nginx is already configured.
			// Log the error and return it so the caller is aware.
			slog.Error("pm2 deploy failed", "app", req.PM2App, "domain", safeDomain, "err", err)
			return fmt.Errorf("pm2 start failed (nginx config is active): %w", err)
		}

		// Persist the process list so it survives reboots.
		if _, err := pm2Service.Save(); err != nil {
			slog.Error("pm2 save failed", "app", req.PM2App, "err", err)
			// Non-fatal: process is running, just not persisted.
		}
	}

	// ── Issue SSL and promote the working HTTP site to HTTPS ────────────────
	if req.IssueSSL {
		if hooks.BeforeCertificate != nil {
			if err := hooks.BeforeCertificate(); err != nil {
				message := "certificate prerequisites failed; keeping HTTP domain active: " + err.Error()
				slog.Error("certificate prerequisite failed; keeping HTTP domain active", "domain", safeDomain, "err", err)
				if result != nil {
					result.Warning = message
				}
				return nil
			}
		}
		certbotArgs := []string{
			"certonly", "--config-dir", s.certbotDir,
			"--webroot", "-w", s.acmeWebroot,
			"-d", safeDomain,
			"-m", req.SSLEmail,
			"--agree-tos", "--non-interactive",
		}
		certbotResult, certbotErr := executeCertbot(3*time.Minute, s.certbotBin, certbotArgs...)
		if certbotErr != nil || (certbotResult != nil && certbotResult.ExitCode != 0) {
			message := commandFailure("certbot certificate issuance", certbotResult, certbotErr)
			slog.Error("certbot failed; keeping HTTP domain active", "domain", safeDomain, "error", message)
			if result != nil {
				result.Warning = message
			}
			return nil
		}
		if result != nil {
			result.CertificateIssued = true
		}

		tlsCertPath := filepath.Join(s.certbotDir, "live", safeDomain, "fullchain.pem")
		tlsKeyPath := filepath.Join(s.certbotDir, "live", safeDomain, "privkey.pem")
		tlsContent := domainNginxContent(req, safeDomain, webRoot, proxyPort, tlsCertPath, tlsKeyPath, true, "", s.snippetsDir)
		if err := promoteDomainTLS(configPath, nginxContent, tlsContent, safeDomain); err != nil {
			slog.Error("certificate issued but HTTPS activation failed; attempted HTTP rollback", "domain", safeDomain, "err", err)
			if result != nil {
				result.Warning = "Certificate issued, but HTTPS activation failed; Heyserver attempted HTTP rollback: " + err.Error()
			}
		} else if result != nil {
			result.HTTPSActive = true
		}
	}

	return nil
}

func executeCertbot(timeout time.Duration, command string, args ...string) (*shell.Result, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, args...)
	output, err := cmd.CombinedOutput()
	result := &shell.Result{Stdout: string(output)}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
		result.Stderr = string(output)
		return result, nil
	}
	if ctx.Err() != nil {
		return result, fmt.Errorf("certbot command timed out: %w", ctx.Err())
	}
	return result, err
}

func commandFailure(operation string, result *shell.Result, err error) string {
	if err != nil {
		return operation + ": " + err.Error()
	}
	if result == nil {
		return operation + " failed"
	}
	detail := strings.TrimSpace(result.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(result.Stdout)
	}
	if detail == "" {
		detail = fmt.Sprintf("exit code %d", result.ExitCode)
	}
	return fmt.Sprintf("%s failed (exit %d): %s", operation, result.ExitCode, detail)
}

func promoteDomainTLS(configPath, httpContent, tlsContent, domain string) error {
	if err := os.WriteFile(configPath, []byte(tlsContent), 0o644); err != nil {
		return fmt.Errorf("write HTTPS config: %w", err)
	}
	rollback := func() {
		if err := os.WriteFile(configPath, []byte(httpContent), 0o644); err != nil {
			slog.Error("restore HTTP config after HTTPS activation failure", "domain", domain, "err", err)
			return
		}
		_, _ = shell.ExecuteWithTimeout(15*time.Second, "nginx", "-t")
		_, _ = shell.ExecuteWithTimeout(15*time.Second, "systemctl", "reload", "nginx")
	}

	testResult, err := shell.ExecuteWithTimeout(15*time.Second, "nginx", "-t")
	if err != nil || (testResult != nil && testResult.ExitCode != 0) {
		rollback()
		return fmt.Errorf("nginx config test: %s", commandFailure("nginx -t", testResult, err))
	}
	reloadResult, err := shell.ExecuteWithTimeout(15*time.Second, "systemctl", "reload", "nginx")
	if err != nil || (reloadResult != nil && reloadResult.ExitCode != 0) {
		rollback()
		return fmt.Errorf("nginx reload: %s", commandFailure("systemctl reload nginx", reloadResult, err))
	}
	return nil
}

// domainToPoolName converts a domain name to a safe PHP-FPM pool name.
// Dots and hyphens are replaced with underscores: api.example.com → api_example_com
func domainToPoolName(domain string) string {
	r := strings.NewReplacer(".", "_", "-", "_")
	return r.Replace(domain)
}

// ─── Delete ───────────────────────────────────────────────────────────────────

// Delete removes nginx config, PHP pool, and optionally web files.
func (s *Service) Delete(id string, deleteFiles bool) error {
	if err := s.requireNginxPaths(); err != nil {
		return err
	}
	if deleteFiles {
		if err := s.requireVhostsRoot(); err != nil {
			return err
		}
	}
	domain, err := normalizeDomainID(id)
	if err != nil {
		return err
	}
	filename := domain + ".conf"
	configPath := filepath.Join(s.sitesAvailable, filename)
	if _, err := os.Lstat(configPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrNotFound, domain)
		}
		return fmt.Errorf("inspect domain config: %w", err)
	}

	// Disable
	os.Remove(filepath.Join(s.sitesEnabled, filename)) //nolint:errcheck

	// Delete nginx config
	if err := os.Remove(configPath); err != nil {
		return fmt.Errorf("remove nginx config: %w", err)
	}

	// Remove PHP pool for all versions; track which FPM services need a reload.
	fpmReload := make(map[string]bool)
	for _, v := range []string{"7.4", "8.0", "8.1", "8.2", "8.3", "8.4", "8.5"} {
		poolPath := fmt.Sprintf(phpFPMPoolDir, v) + "/" + domain + ".conf"
		if err := os.Remove(poolPath); err == nil {
			// Pool was successfully removed — mark this version for reload.
			fpmReload[v] = true
			slog.Info("removed php-fpm pool", "version", v, "domain", domain)
		}
	}

	// Delete web files — validate path before rm -rf to prevent traversal.
	// H-3 fix: ensure the resolved path stays inside vhostsRoot.
	// Document root is NOT deleted by default (deleteFiles must be explicitly true).
	if deleteFiles {
		webRoot := filepath.Dir(DefaultWebRoot(s.vhostsRoot, domain))
		// Clean the path and verify it is a direct child of vhostsRoot (no ../../ escape).
		cleaned := filepath.Clean(webRoot)
		if cleaned == s.vhostsRoot || cleaned == "/" || !strings.HasPrefix(cleaned, s.vhostsRoot+"/") {
			return fmt.Errorf("invalid web root path — refusing to delete: %s", cleaned)
		}
		if err := os.RemoveAll(cleaned); err != nil {
			return fmt.Errorf("delete files: %w", err)
		}
		slog.Info("deleted web root", "path", cleaned)
	}

	// Reload nginx so the deleted vhost takes effect immediately.
	if reloadRes, err := shell.ExecuteWithTimeout(15*time.Second, "systemctl", "reload", "nginx"); err != nil || (reloadRes != nil && reloadRes.ExitCode != 0) {
		stderr := ""
		if reloadRes != nil {
			stderr = reloadRes.Stderr
		}
		slog.Error("nginx reload failed after domain delete", "domain", domain, "stderr", stderr)
		// Non-fatal — config is gone, nginx will be fine on next manual reload.
	}

	// Reload PHP-FPM for any version where a pool was removed.
	for v := range fpmReload {
		svcName := "php" + v + "-fpm"
		result, err := shell.ExecuteWithTimeout(3*time.Second, "systemctl", "is-active", svcName)
		if err == nil && strings.TrimSpace(result.Stdout) == "active" {
			if _, reloadErr := shell.ExecuteWithTimeout(15*time.Second, "systemctl", "reload", svcName); reloadErr == nil {
				slog.Info("reloaded php-fpm after pool removal", "version", v, "domain", domain)
			}
		}
	}

	return nil
}

// ─── Toggle ───────────────────────────────────────────────────────────────────

// Toggle enables or disables a domain.
func (s *Service) Toggle(id string, active bool) error {
	if err := s.requireNginxPaths(); err != nil {
		return err
	}
	domain, err := normalizeDomainID(id)
	if err != nil {
		return err
	}
	filename := domain + ".conf"

	src := filepath.Join(s.sitesAvailable, filename)
	dest := filepath.Join(s.sitesEnabled, filename)
	if _, err := os.Lstat(src); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%w: %s", ErrNotFound, domain)
		}
		return fmt.Errorf("inspect domain config: %w", err)
	}

	if active {
		os.Remove(dest) //nolint:errcheck
		return os.Symlink(src, dest)
	}
	return os.Remove(dest)
}

// ─── Health Check ─────────────────────────────────────────────────────────────

// CheckHealth runs DNS, SSL, and HTTP checks for a domain.
func (s *Service) CheckHealth(domain string) HealthResult {
	safe := sanitizeDomain(domain)
	result := HealthResult{Domain: safe}

	// DNS
	dnsResult, err := shell.ExecuteWithTimeout(5*time.Second, "dig", "+short", safe, "A")
	if err == nil && strings.TrimSpace(dnsResult.Stdout) != "" {
		result.DNSResolves = true
		result.DNSIP = strings.TrimSpace(strings.Split(dnsResult.Stdout, "\n")[0])
	}

	// HTTP timing
	start := time.Now()
	httpResult, err := shell.ExecuteWithTimeout(8*time.Second, "curl",
		"-s", "-o", "/dev/null", "-w", "%{http_code}", "--max-time", "5",
		"http://"+safe,
	)
	if err == nil && httpResult.Stdout != "" {
		var code int
		_, _ = fmt.Sscanf(httpResult.Stdout, "%d", &code)
		result.HTTPStatus = code
		result.ResponseTime = time.Since(start).Milliseconds()
	}

	httpsResult, err := shell.ExecuteWithTimeout(8*time.Second, "curl",
		"-s", "-o", "/dev/null", "-w", "%{http_code}", "--max-time", "5", "-k",
		"https://"+safe,
	)
	if err == nil && httpsResult.Stdout != "" {
		var code int
		_, _ = fmt.Sscanf(httpsResult.Stdout, "%d", &code)
		result.HTTPSStatus = code
	}

	// SSL days remaining via openssl
	sslResult, err := shell.ExecuteWithTimeout(5*time.Second, "openssl",
		"s_client", "-connect", safe+":443", "-servername", safe,
	)
	if err == nil && strings.Contains(sslResult.Stdout+sslResult.Stderr, "notAfter") {
		result.SSLValid = true
	}

	return result
}

// ─── Config Parsing ───────────────────────────────────────────────────────────

func parseConfig(filename, content string) models.Domain {
	return parseConfigWithSitesEnabled(filename, content, defaultSitesEnabled)
}

func parseConfigWithSitesEnabled(filename, content, sitesEnabled string) models.Domain {
	serverNames := parseServerNames(content)
	name := ""
	if len(serverNames) > 0 {
		for _, sn := range serverNames {
			if !strings.HasPrefix(sn, "www.") {
				name = sn
				break
			}
		}
		if name == "" {
			name = serverNames[0]
		}
	}
	if name == "" {
		name = strings.TrimSuffix(filename, ".conf")
	}

	enabled := false
	if sitesEnabled != "" {
		if _, err := os.Lstat(filepath.Join(sitesEnabled, filename)); err == nil {
			enabled = true
		}
	}

	return models.Domain{
		ID:         strings.TrimSuffix(filename, ".conf"),
		Name:       name,
		Type:       detectType(content),
		Root:       parseDirective(content, "root"),
		PHPVersion: parsePhpVersion(content),
		ProxyPort:  parseProxyPort(content),
		SSLEnabled: strings.Contains(content, "listen 443"),
		IsActive:   enabled,
		UpdatedAt:  time.Now(),
	}
}

func detectType(content string) string {
	if strings.Contains(content, "fastcgi_pass") || strings.Contains(content, "php-fpm") {
		return "php"
	}
	if strings.Contains(content, "proxy_pass") {
		return "proxy"
	}
	return "static"
}

func parseServerNames(content string) []string {
	names := make([]string, 0)
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "server_name") {
			line = strings.TrimPrefix(line, "server_name")
			line = strings.TrimSuffix(line, ";")
			for _, part := range strings.Fields(line) {
				if part != "_" {
					names = append(names, part)
				}
			}
		}
	}
	return names
}

func parseDirective(content, directive string) string {
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, directive+" ") {
			val := strings.TrimPrefix(line, directive+" ")
			val = strings.TrimSuffix(val, ";")
			return strings.TrimSpace(val)
		}
	}
	return ""
}

func parsePhpVersion(content string) string {
	remaining := content
	for {
		idx := strings.Index(remaining, "php")
		if idx < 0 {
			break
		}
		rest := remaining[idx+3:]
		if len(rest) >= 3 && rest[0] >= '0' && rest[0] <= '9' && rest[1] == '.' && rest[2] >= '0' && rest[2] <= '9' {
			return string(rest[0]) + "." + string(rest[2])
		}
		remaining = rest
	}
	return ""
}

func parseProxyPort(content string) int {
	idx := strings.Index(content, "proxy_pass")
	if idx < 0 {
		return 0
	}
	rest := content[idx:]
	colonIdx := strings.Index(rest, ":")
	if colonIdx < 0 {
		return 0
	}
	rest = rest[colonIdx+1:]
	var port int
	_, _ = fmt.Sscanf(rest, "%d", &port)
	return port
}

// ─── Sanitize ─────────────────────────────────────────────────────────────────

func sanitize(s string) string {
	var out strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

func sanitizeDomain(s string) string {
	var out strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			out.WriteRune(r)
		}
	}
	return out.String()
}

// NormalizeDomain returns the canonical lowercase ASCII hostname used as the
// Nginx configuration identity. It rejects rather than silently deleting
// caller-supplied characters so mutations can never target a different name.
func NormalizeDomain(value string) (string, error) {
	domain := strings.ToLower(strings.TrimSpace(value))
	if len(domain) < 3 || len(domain) > 253 || !strings.Contains(domain, ".") || strings.HasPrefix(domain, ".") || strings.HasSuffix(domain, ".") {
		return "", fmt.Errorf("%w: domain must be a valid DNS hostname", ErrInvalidRequest)
	}
	for _, label := range strings.Split(domain, ".") {
		if len(label) < 1 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("%w: domain must be a valid DNS hostname", ErrInvalidRequest)
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return "", fmt.Errorf("%w: domain must be a valid DNS hostname", ErrInvalidRequest)
			}
		}
	}
	return domain, nil
}

func normalizeDomainID(value string) (string, error) {
	value = strings.TrimSuffix(strings.TrimSpace(value), ".conf")
	return NormalizeDomain(value)
}

// ─── Nginx Templates ──────────────────────────────────────────────────────────

func domainNginxContent(req CreateRequest, domain, webRoot string, proxyPort int, certPath, keyPath string, useTLS bool, acmeWebroot, snippetsDir string) string {
	phpVersion := req.PHPVersion
	if phpVersion == "" {
		phpVersion = "8.4"
	}
	var content string
	if useTLS {
		switch req.Type {
		case "php":
			content = phpTemplate(domain, phpVersion, domainToPoolName(domain), webRoot, certPath, keyPath, req.WWWRedirect)
		case "proxy":
			content = proxyTemplate(domain, proxyPort, certPath, keyPath, req.WWWRedirect)
		default:
			content = staticTemplate(domain, webRoot, certPath, keyPath, req.WWWRedirect, req.SPAMode)
		}
	} else {
		switch req.Type {
		case "php":
			content = phpHTTPTemplate(domain, phpVersion, domainToPoolName(domain), webRoot, acmeWebroot, req.WWWRedirect)
		case "proxy":
			content = proxyHTTPTemplate(domain, proxyPort, acmeWebroot, req.WWWRedirect)
		default:
			content = staticHTTPTemplate(domain, webRoot, acmeWebroot, req.WWWRedirect, req.SPAMode)
		}
	}
	return bindDomainSnippetPaths(content, snippetsDir)
}

func bindDomainSnippetPaths(content, snippetsDir string) string {
	for source, managed := range map[string]string{
		"php-fpm.conf":          "hserver-php-fpm.conf",
		"security-deny.conf":    "hserver-security-deny.conf",
		"static-cache.conf":     "hserver-static-cache.conf",
		"proxy-params.conf":     "hserver-proxy-params.conf",
		"ssl-params.conf":       "hserver-ssl-params.conf",
		"security-headers.conf": "hserver-security-headers.conf",
	} {
		content = strings.ReplaceAll(content, filepath.Join("/etc/nginx/snippets", source), filepath.Join(snippetsDir, managed))
	}
	return content
}

func httpServerNames(domain string, includeWWW bool) string {
	if includeWWW {
		return domain + " www." + domain
	}
	return domain
}

func acmeLocation(webroot string) string {
	if webroot == "" {
		return ""
	}
	return fmt.Sprintf(`
    location ^~ /.well-known/acme-challenge/ {
        root %s;
        default_type text/plain;
    }
`, webroot)
}

func phpHTTPTemplate(domain, phpVersion, poolName, webRoot, acmeWebroot string, includeWWW bool) string {
	socketPath := fmt.Sprintf("/run/php/php%s-fpm-%s.sock", phpVersion, poolName)
	return fmt.Sprintf(`server {
    listen 80;
    listen [::]:80;
    server_name %s;
%s
    root %s;
    index index.php index.html;

    location / {
        try_files $uri $uri/ /index.php?$query_string;
    }

    location ~ \.php$ {
        include /etc/nginx/snippets/php-fpm.conf;
        fastcgi_pass unix:%s;
    }

    include /etc/nginx/snippets/security-deny.conf;
    include /etc/nginx/snippets/static-cache.conf;
}
`, httpServerNames(domain, includeWWW), acmeLocation(acmeWebroot), webRoot, socketPath)
}

func proxyHTTPTemplate(domain string, port int, acmeWebroot string, includeWWW bool) string {
	return fmt.Sprintf(`server {
    listen 80;
    listen [::]:80;
    server_name %s;
%s
    location / {
        proxy_pass http://127.0.0.1:%d;
        include /etc/nginx/snippets/proxy-params.conf;
    }
}
`, httpServerNames(domain, includeWWW), acmeLocation(acmeWebroot), port)
}

func staticHTTPTemplate(domain, webRoot, acmeWebroot string, includeWWW, spaMode bool) string {
	tryFilesFallback := "=404"
	if spaMode {
		tryFilesFallback = "/index.html"
	}
	return fmt.Sprintf(`server {
    listen 80;
    listen [::]:80;
    server_name %s;
%s
    root %s;
    index index.html;

    location / {
        try_files $uri $uri/ %s;
    }

    include /etc/nginx/snippets/static-cache.conf;
}
`, httpServerNames(domain, includeWWW), acmeLocation(acmeWebroot), webRoot, tryFilesFallback)
}

func phpTemplate(domain, phpVersion, poolName, webRoot, certPath, keyPath string, wwwRedirect bool) string {
	socketPath := fmt.Sprintf("/run/php/php%s-fpm-%s.sock", phpVersion, poolName)
	www := ""
	if wwwRedirect {
		www = fmt.Sprintf(`
server {
    listen 80;
    listen [::]:80;
    server_name www.%s;
    return 301 https://%s$request_uri;
}
`, domain, domain)
	}
	return fmt.Sprintf(`%s# HTTP → HTTPS redirect
server {
    listen 80;
    listen [::]:80;
    server_name %s;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    server_name %s;

    ssl_certificate %s;
    ssl_certificate_key %s;
    include /etc/nginx/snippets/ssl-params.conf;
    include /etc/nginx/snippets/security-headers.conf;

    root %s;
    index index.php index.html;

    location / {
        try_files $uri $uri/ /index.php?$query_string;
    }

    location ~ \.php$ {
        include /etc/nginx/snippets/php-fpm.conf;
        fastcgi_pass unix:%s;
    }

    include /etc/nginx/snippets/security-deny.conf;
    include /etc/nginx/snippets/static-cache.conf;
}
`, www, domain, domain, certPath, keyPath, webRoot, socketPath)
}

func proxyTemplate(domain string, port int, certPath, keyPath string, wwwRedirect bool) string {
	www := ""
	if wwwRedirect {
		www = fmt.Sprintf(`
server {
    listen 80;
    listen [::]:80;
    server_name www.%s;
    return 301 https://%s$request_uri;
}
`, domain, domain)
	}
	return fmt.Sprintf(`%sserver {
    listen 80;
    listen [::]:80;
    server_name %s;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    server_name %s;

    ssl_certificate %s;
    ssl_certificate_key %s;
    include /etc/nginx/snippets/ssl-params.conf;
    include /etc/nginx/snippets/security-headers.conf;

    location / {
        proxy_pass http://127.0.0.1:%d;
        include /etc/nginx/snippets/proxy-params.conf;
    }
}
`, www, domain, domain, certPath, keyPath, port)
}

func staticTemplate(domain, webRoot, certPath, keyPath string, wwwRedirect, spaMode bool) string {
	www := ""
	if wwwRedirect {
		www = fmt.Sprintf(`
server {
    listen 80;
    listen [::]:80;
    server_name www.%s;
    return 301 https://%s$request_uri;
}
`, domain, domain)
	}
	tryFilesFallback := "=404"
	if spaMode {
		tryFilesFallback = "/index.html"
	}
	return fmt.Sprintf(`%sserver {
    listen 80;
    listen [::]:80;
    server_name %s;
    return 301 https://$host$request_uri;
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    server_name %s;

    ssl_certificate %s;
    ssl_certificate_key %s;
    include /etc/nginx/snippets/ssl-params.conf;
    include /etc/nginx/snippets/security-headers.conf;

    root %s;
    index index.html;

    location / {
        try_files $uri $uri/ %s;
    }

    include /etc/nginx/snippets/static-cache.conf;
}
`, www, domain, domain, certPath, keyPath, webRoot, tryFilesFallback)
}

func phpPoolTemplateIsolated(phpVersion, poolName, portalRoot, linuxUser string, preset phpsvc.PoolConfig) string {
	socketPath := fmt.Sprintf("/run/php/php%s-fpm-%s.sock", phpVersion, poolName)
	openBasedir := portaluser.OpenBasedirForPortal(portalRoot)
	return fmt.Sprintf(`; PHP-FPM Pool: %s (isolated portal user)
; PHP Version: %s
; Generated by Heyserver — per-site isolation

[%s]

user = %s
group = www-data

listen = %s
listen.owner = www-data
listen.group = www-data
listen.mode = 0660

%s

chdir = /

catch_workers_output = yes
decorate_workers_output = no

; Security — portal install root only (sibling portals not visible)
php_admin_value[open_basedir] = %s
php_admin_value[disable_functions] = opcache_get_status
security.limit_extensions = .php

; Resource limits (per portal — tune via Heyserver pool presets)
php_admin_value[memory_limit] = %s
php_admin_value[upload_max_filesize] = %s
php_admin_value[post_max_size] = %s
php_admin_value[max_execution_time] = %d
php_admin_value[max_input_time] = %d

; Performance (OPcache)
php_value[opcache.memory_consumption] = 128
php_value[opcache.interned_strings_buffer] = 64
php_value[opcache.max_accelerated_files] = 10000
php_value[opcache.revalidate_freq] = 2
`, poolName, phpVersion, poolName, linuxUser, socketPath, phpProcessManagerConfig(preset), openBasedir,
		preset.MemoryLimit, preset.UploadMaxSize, preset.PostMaxSize, preset.MaxExecutionTime, preset.MaxInputTime)
}

func phpPoolTemplate(phpVersion, poolName, domainRoot string, preset phpsvc.PoolConfig) string {
	socketPath := fmt.Sprintf("/run/php/php%s-fpm-%s.sock", phpVersion, poolName)
	return fmt.Sprintf(`; PHP-FPM Pool: %s
; PHP Version: %s
; Generated by Heyserver

[%s]

user = www-data
group = www-data

listen = %s
listen.owner = www-data
listen.group = www-data
listen.mode = 0660

%s

chdir = /

catch_workers_output = yes
decorate_workers_output = no

; Security
php_admin_value[open_basedir] = %s:/tmp/:/usr/share/php/
php_admin_value[disable_functions] = opcache_get_status
security.limit_extensions = .php

; Resource limits
php_admin_value[memory_limit] = %s
php_admin_value[upload_max_filesize] = %s
php_admin_value[post_max_size] = %s
php_admin_value[max_execution_time] = %d
php_admin_value[max_input_time] = %d

; Performance (OPcache)
php_value[opcache.memory_consumption] = 128
php_value[opcache.interned_strings_buffer] = 64
php_value[opcache.max_accelerated_files] = 10000
php_value[opcache.revalidate_freq] = 2
`, poolName, phpVersion, poolName, socketPath, phpProcessManagerConfig(preset), domainRoot,
		preset.MemoryLimit, preset.UploadMaxSize, preset.PostMaxSize, preset.MaxExecutionTime, preset.MaxInputTime)
}

func phpProcessManagerConfig(preset phpsvc.PoolConfig) string {
	var b strings.Builder
	fmt.Fprintf(&b, "pm = %s\n", preset.PM)
	fmt.Fprintf(&b, "pm.max_children = %d\n", preset.MaxChildren)
	switch preset.PM {
	case "dynamic":
		fmt.Fprintf(&b, "pm.start_servers = %d\n", preset.StartServers)
		fmt.Fprintf(&b, "pm.min_spare_servers = %d\n", preset.MinSpareServers)
		fmt.Fprintf(&b, "pm.max_spare_servers = %d\n", preset.MaxSpareServers)
	case "ondemand":
		fmt.Fprintf(&b, "pm.process_idle_timeout = %ds\n", preset.ProcessIdleTimeout)
	}
	fmt.Fprintf(&b, "pm.max_requests = %d", preset.MaxRequests)
	return b.String()
}

// ─── Subdomain Helpers ────────────────────────────────────────────────────────

// IsSubdomain returns true if the domain has 3 or more dot-separated parts
// (e.g. "app.example.com" → true, "example.com" → false).
func IsSubdomain(domain string) bool {
	parts := strings.Split(domain, ".")
	return len(parts) > 2
}

// ParentDomain extracts the apex domain from a subdomain.
// "app.example.com" → "example.com", "example.com" → "example.com"
func ParentDomain(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) <= 2 {
		return domain
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

// SubdomainPrefix returns everything to the left of the apex domain.
// "app.example.com" → "app", "staging.app.example.com" → "staging.app"
func SubdomainPrefix(domain string) string {
	parts := strings.Split(domain, ".")
	if len(parts) <= 2 {
		return ""
	}
	return strings.Join(parts[:len(parts)-2], ".")
}

// ─── Domain Pre-Check ─────────────────────────────────────────────────────────

// CheckRequest is the body for POST /api/domains/check.
type CheckRequest struct {
	Domain string `json:"domain"`
}

// CheckResult holds the pre-creation analysis for a domain.
type CheckResult struct {
	Valid            bool     `json:"valid"`
	IsSubdomain      bool     `json:"is_subdomain"`
	ParentDomain     string   `json:"parent_domain,omitempty"`
	ParentExists     bool     `json:"parent_exists"`
	SuggestedWebroot string   `json:"suggested_webroot"`
	SuggestedPort    int      `json:"suggested_port"`
	DNSZones         []string `json:"dns_zones"`
	Conflicts        []string `json:"conflicts"`
}

// Check performs a pre-creation analysis for a domain name.
// It detects subdomains, suggests paths/ports, and finds conflicts.
func (s *Service) Check(domain string, cfZoneNames []string) CheckResult {
	result := CheckResult{
		DNSZones:  []string{},
		Conflicts: []string{},
	}

	safe, err := NormalizeDomain(domain)
	if err != nil {
		return result // valid stays false
	}
	result.Valid = true

	// Subdomain detection
	result.IsSubdomain = IsSubdomain(safe)
	if result.IsSubdomain {
		result.ParentDomain = ParentDomain(safe)
		// Check whether parent has an nginx config
		if s.sitesAvailable != "" {
			parentConfig := filepath.Join(s.sitesAvailable, result.ParentDomain+".conf")
			if _, err := os.Stat(parentConfig); err == nil {
				result.ParentExists = true
			}
		}
		// Subdomain web root lives under the parent domain's vhosts directory.
		if s.vhostsRoot != "" {
			result.SuggestedWebroot = filepath.Join(s.vhostsRoot, result.ParentDomain, safe, "public_html")
		}
	} else if s.vhostsRoot != "" {
		result.SuggestedWebroot = filepath.Join(s.vhostsRoot, safe, "public_html")
	}

	// Find next available proxy port (scan existing nginx configs).
	result.SuggestedPort = s.nextAvailablePort()

	// Match Cloudflare zones — a zone matches if the domain ends with the zone name.
	for _, zone := range cfZoneNames {
		if safe == zone || strings.HasSuffix(safe, "."+zone) {
			result.DNSZones = append(result.DNSZones, zone)
		}
	}

	// Conflict check — existing nginx configs that declare the same server_name.
	conflicts := s.findConflicts(safe)
	result.Conflicts = conflicts

	return result
}

// nextAvailablePort scans all nginx configs and returns the lowest unused port
// above 3000 that is not already referenced by a proxy_pass directive.
func (s *Service) nextAvailablePort() int {
	used := make(map[int]bool)
	entries, err := os.ReadDir(s.sitesAvailable)
	if err == nil {
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".conf") {
				continue
			}
			content, err := os.ReadFile(filepath.Join(s.sitesAvailable, e.Name()))
			if err != nil {
				continue
			}
			if port := parseProxyPort(string(content)); port > 0 {
				used[port] = true
			}
		}
	}
	// Also mark well-known ports used by the panel itself.
	for _, p := range []int{3085, 9080, 9222} {
		used[p] = true
	}
	candidate := 3080
	for used[candidate] {
		candidate++
	}
	return candidate
}

// findConflicts returns a list of nginx config filenames that already declare
// the given domain as a server_name.
func (s *Service) findConflicts(domain string) []string {
	var conflicts []string
	entries, err := os.ReadDir(s.sitesAvailable)
	if err != nil {
		return conflicts
	}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		content, err := os.ReadFile(filepath.Join(s.sitesAvailable, e.Name()))
		if err != nil {
			continue
		}
		for _, name := range parseServerNames(string(content)) {
			if name == domain {
				conflicts = append(conflicts, e.Name())
				break
			}
		}
	}
	return conflicts
}
