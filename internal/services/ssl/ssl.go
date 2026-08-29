// Package ssl provides SSL/TLS certificate management via certbot.
package ssl

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/models"
)

const (
	certbotBinEnv            = "HSERVER_CERTBOT_BIN"
	certbotConfigDirEnv      = "HSERVER_CERTBOT_CONFIG_DIR"
	cloudflareCredentialsEnv = "HSERVER_CERTBOT_CLOUDFLARE_CREDENTIALS"
	defaultCertbotBin        = "certbot"
	defaultCertbotConfigDir  = "/etc/letsencrypt"
	certbotVersionTimeout    = 15 * time.Second
	certbotPluginsTimeout    = 30 * time.Second
)

var (
	validBinaryName = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	pluginLine      = regexp.MustCompile(`^\*\s+([a-z0-9_-]+)\s*$`)

	// ErrNotConfigured identifies a host without an installed Certbot
	// executable. It is intentionally stable so callers can map the optional
	// integration to the canonical not_configured state without exposing the
	// configured command or PATH.
	ErrNotConfigured = errors.New("certbot is not configured")
)

// ReadinessState describes whether certbot can perform local certificate
// operations. Certificate-file inventory remains independently observable.
type ReadinessState string

const (
	StateHealthy        ReadinessState = "healthy"
	StateCertbotMissing ReadinessState = "certbot-missing"
	StateUnavailable    ReadinessState = "unavailable"
)

// Status reports certbot and challenge-plugin readiness without returning
// credential paths or values.
type Status struct {
	Available                          bool           `json:"available"`
	Installed                          bool           `json:"installed"`
	State                              ReadinessState `json:"state"`
	Version                            string         `json:"version,omitempty"`
	Plugins                            []string       `json:"plugins"`
	PluginsAvailable                   bool           `json:"pluginsAvailable"`
	NginxPlugin                        bool           `json:"nginxPlugin"`
	DNSCloudflarePlugin                bool           `json:"dnsCloudflarePlugin"`
	DNSCloudflareCredentialsConfigured bool           `json:"dnsCloudflareCredentialsConfigured"`
	DNSCloudflareCredentialsReadable   bool           `json:"dnsCloudflareCredentialsReadable"`
	Error                              string         `json:"error,omitempty"`
	PluginError                        string         `json:"pluginError,omitempty"`
}

// RenewResult holds the outcome of a certbot renew operation.
type RenewResult struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Domain  string `json:"domain"`
}

// IssueRequest holds parameters for issuing a new certificate.
type IssueRequest struct {
	Domain string   `json:"domain"`
	SANs   []string `json:"sans"`
	Method string   `json:"method"` // "nginx" or "dns-cloudflare"
	Email  string   `json:"email"`
}

// IssueResult holds the outcome of a certbot certonly operation.
type IssueResult struct {
	Success bool   `json:"success"`
	Output  string `json:"output"`
	Domain  string `json:"domain"`
}

// GetStatus observes certbot and its installed authenticator plugins. Missing
// DNS credentials never prevent renewals or nginx-plugin certificates.
func GetStatus() Status {
	status := Status{State: StateUnavailable, Plugins: []string{}}
	bin, err := resolveCertbotBinary()
	if err != nil {
		status.State = ClassifyCertbotError(err)
		status.Error = err.Error()
		return status
	}

	status.Installed = true
	version, success, err := runResolvedCertbot(bin, certbotVersionTimeout, "--version")
	if err != nil || !success {
		status.Error = commandFailure("certbot --version", version, err)
		return status
	}

	status.Available = true
	status.State = StateHealthy
	status.Version = strings.TrimSpace(version)

	pluginsOutput, pluginsOK, pluginsErr := runResolvedCertbot(bin, certbotPluginsTimeout, "plugins")
	if pluginsErr != nil || !pluginsOK {
		status.PluginError = commandFailure("certbot plugins", pluginsOutput, pluginsErr)
	} else {
		status.PluginsAvailable = true
		status.Plugins = parsePlugins(pluginsOutput)
		for _, plugin := range status.Plugins {
			switch plugin {
			case "nginx":
				status.NginxPlugin = true
			case "dns-cloudflare":
				status.DNSCloudflarePlugin = true
			}
		}
	}

	configured, readable, credentialErr := cloudflareCredentialsReadiness()
	status.DNSCloudflareCredentialsConfigured = configured
	status.DNSCloudflareCredentialsReadable = readable
	if credentialErr != nil {
		status.PluginError = appendDiagnostic(status.PluginError, credentialErr.Error())
	}

	return status
}

// certbotProbeRunner is the narrow command boundary used by ProbeContext.
// Keeping executable lookup and process execution behind one typed seam makes
// the read-only readiness contract deterministic in tests while production
// still uses the real local Certbot subprocess.
type certbotProbeRunner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, ...string) error
}

type execCertbotProbeRunner struct{}

func (execCertbotProbeRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (execCertbotProbeRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	// Probe output is deliberately discarded. Readiness callers receive only
	// the canonical state and a safe classification error, never Certbot's
	// output, configured paths, or process diagnostics.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// ProbeContext performs one fresh, read-only Certbot readiness observation.
// Certbot is healthy only when both `certbot --version` and `certbot plugins`
// complete successfully. A missing executable is not_configured; all other
// failures, including command timeouts and caller cancellation, are
// unavailable. The legacy GetStatus endpoint remains unchanged and continues
// to expose its existing detailed status shape.
func ProbeContext(parent context.Context) (integrationstate.State, error) {
	return probeContext(parent, execCertbotProbeRunner{})
}

// Probe is the background-context convenience form of ProbeContext.
func Probe() (integrationstate.State, error) {
	return ProbeContext(context.Background())
}

func probeContext(parent context.Context, runner certbotProbeRunner) (integrationstate.State, error) {
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	if runner == nil {
		return integrationstate.Unavailable, errors.New("certbot readiness runner is unavailable")
	}

	bin, err := configuredCertbotBinary()
	if err != nil {
		return integrationstate.Unavailable, errors.New("certbot configuration is unavailable")
	}
	resolved, err := runner.LookPath(bin)
	if err != nil {
		if ClassifyCertbotError(err) == StateCertbotMissing {
			return integrationstate.NotConfigured, ErrNotConfigured
		}
		return integrationstate.Unavailable, errors.New("certbot executable is unavailable")
	}
	if strings.TrimSpace(resolved) == "" {
		return integrationstate.Unavailable, errors.New("certbot executable is unavailable")
	}
	if err := parent.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	if err := runProbeCommand(parent, runner, resolved, certbotVersionTimeout, "--version"); err != nil {
		return integrationstate.Unavailable, err
	}
	// Certbot otherwise writes a debug log for this read-only inventory command.
	// Direct the log sink to the platform null device so status polling has no
	// persistent filesystem side effect.
	if err := runProbeCommand(parent, runner, resolved, certbotPluginsTimeout, "--logs-dir", os.DevNull, "plugins"); err != nil {
		return integrationstate.Unavailable, err
	}
	if err := parent.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	return integrationstate.Healthy, nil
}

func runProbeCommand(parent context.Context, runner certbotProbeRunner, bin string, timeout time.Duration, args ...string) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := runner.Run(ctx, bin, args...); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if args[0] == "--version" {
			return errors.New("certbot version probe failed")
		}
		return errors.New("certbot plugin inventory probe failed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// ClassifyCertbotError exposes a stable state for executable discovery errors.
func ClassifyCertbotError(err error) ReadinessState {
	if err == nil {
		return StateHealthy
	}
	message := strings.ToLower(err.Error())
	if errors.Is(err, exec.ErrNotFound) ||
		errors.Is(err, os.ErrNotExist) ||
		strings.Contains(message, "executable file not found") ||
		strings.Contains(message, "not found in $path") ||
		strings.Contains(message, "certbot: no such file or directory") ||
		strings.Contains(message, "no such file or directory") {
		return StateCertbotMissing
	}
	return StateUnavailable
}

func configuredCertbotBinary() (string, error) {
	bin := strings.TrimSpace(os.Getenv(certbotBinEnv))
	if bin == "" {
		bin = defaultCertbotBin
	}
	if strings.Contains(bin, "/") {
		if !filepath.IsAbs(bin) || filepath.Clean(bin) != bin {
			return "", fmt.Errorf("%s must be an absolute normalized path or command name", certbotBinEnv)
		}
	} else if !validBinaryName.MatchString(bin) {
		return "", fmt.Errorf("%s contains an invalid command name", certbotBinEnv)
	}
	return bin, nil
}

func configuredCertbotConfigDir() (string, error) {
	path := strings.TrimSpace(os.Getenv(certbotConfigDirEnv))
	if path == "" {
		path = defaultCertbotConfigDir
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return "", fmt.Errorf("%s must be an absolute normalized path", certbotConfigDirEnv)
	}
	return path, nil
}

func resolveCertbotBinary() (string, error) {
	bin, err := configuredCertbotBinary()
	if err != nil {
		return "", err
	}
	resolved, err := exec.LookPath(bin)
	if err != nil {
		return "", fmt.Errorf("certbot executable %q: %w", bin, err)
	}
	return resolved, nil
}

func runCertbot(timeout time.Duration, args ...string) (string, bool, error) {
	bin, err := resolveCertbotBinary()
	if err != nil {
		return "", false, err
	}
	return runResolvedCertbot(bin, timeout, args...)
}

func runResolvedCertbot(bin string, timeout time.Duration, args ...string) (string, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	output, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(output))
	if ctx.Err() != nil {
		return text, false, fmt.Errorf("certbot command timed out: %w", ctx.Err())
	}
	if err == nil {
		return text, true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return text, false, nil
	}
	return text, false, err
}

func parsePlugins(output string) []string {
	plugins := make([]string, 0)
	seen := make(map[string]struct{})
	for _, line := range strings.Split(strings.ToLower(output), "\n") {
		match := pluginLine.FindStringSubmatch(strings.TrimSpace(line))
		if len(match) != 2 {
			continue
		}
		if _, exists := seen[match[1]]; exists {
			continue
		}
		seen[match[1]] = struct{}{}
		plugins = append(plugins, match[1])
	}
	return plugins
}

func cloudflareCredentialsReadiness() (configured bool, readable bool, err error) {
	path := strings.TrimSpace(os.Getenv(cloudflareCredentialsEnv))
	if path == "" {
		return false, false, nil
	}
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return true, false, fmt.Errorf("%s must be an absolute normalized path", cloudflareCredentialsEnv)
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		return true, false, fmt.Errorf("%s is not readable: %w", cloudflareCredentialsEnv, statErr)
	}
	if !info.Mode().IsRegular() {
		return true, false, fmt.Errorf("%s must reference a regular file", cloudflareCredentialsEnv)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return true, false, fmt.Errorf("%s must not be readable by group or other users", cloudflareCredentialsEnv)
	}
	return true, true, nil
}

func cloudflareCredentialsPath() (string, error) {
	configured, readable, err := cloudflareCredentialsReadiness()
	if err != nil {
		return "", err
	}
	if !configured {
		return "", fmt.Errorf("set %s to use the dns-cloudflare challenge", cloudflareCredentialsEnv)
	}
	if !readable {
		return "", fmt.Errorf("%s is not readable", cloudflareCredentialsEnv)
	}
	return strings.TrimSpace(os.Getenv(cloudflareCredentialsEnv)), nil
}

func commandFailure(command, output string, err error) string {
	parts := []string{command + " failed"}
	if output != "" {
		parts = append(parts, output)
	}
	if err != nil {
		parts = append(parts, err.Error())
	}
	return strings.Join(parts, ": ")
}

func appendDiagnostic(current, next string) string {
	if current == "" {
		return next
	}
	return current + "; " + next
}

// List returns all certificates found below the configured Certbot live root.
// Each subdirectory is a certbot cert-name; we parse cert.pem with crypto/x509.
func List() ([]models.SSLCertificate, error) {
	configDir, err := configuredCertbotConfigDir()
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(configDir, "live"))
	if err != nil {
		if os.IsNotExist(err) {
			return []models.SSLCertificate{}, nil
		}
		return nil, fmt.Errorf("reading letsencrypt live dir: %w", err)
	}

	certs := make([]models.SSLCertificate, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || e.Name() == "README" {
			continue
		}
		cert, err := parseCertDir(e.Name())
		if err != nil {
			// Skip unreadable/malformed certs without aborting the list
			continue
		}
		certs = append(certs, *cert)
	}
	return certs, nil
}

// Get returns a single certificate by domain (cert-name in certbot terms).
func Get(domain string) (*models.SSLCertificate, error) {
	if strings.ContainsAny(domain, "/\\") {
		return nil, fmt.Errorf("invalid domain: %s", domain)
	}
	cert, err := parseCertDir(domain)
	if err != nil {
		return nil, fmt.Errorf("certificate not found for domain %s: %w", domain, err)
	}
	return cert, nil
}

// Renew attempts to renew the certificate for the given cert-name via certbot.
func Renew(domain string) (*RenewResult, error) {
	if strings.ContainsAny(domain, "/\\;|&`$()") {
		return nil, fmt.Errorf("invalid domain: %s", domain)
	}

	configDir, err := configuredCertbotConfigDir()
	if err != nil {
		return nil, err
	}
	output, success, err := runCertbot(
		5*time.Minute,
		"renew",
		"--config-dir", configDir,
		"--cert-name", domain,
		"--non-interactive",
	)
	if err != nil {
		return nil, fmt.Errorf("certbot renew exec error: %w", err)
	}

	return &RenewResult{
		Success: success,
		Output:  output,
		Domain:  domain,
	}, nil
}

// Issue requests a new certificate via certbot certonly.
func Issue(req *IssueRequest) (*IssueResult, error) {
	if req.Domain == "" {
		return nil, fmt.Errorf("domain is required")
	}
	if strings.ContainsAny(req.Domain, "/\\;|&`$()") {
		return nil, fmt.Errorf("invalid domain: %s", req.Domain)
	}
	if req.Method != "nginx" && req.Method != "dns-cloudflare" {
		return nil, fmt.Errorf("unsupported certificate method: %s", req.Method)
	}

	credentialsPath := ""
	if req.Method == "dns-cloudflare" {
		var err error
		credentialsPath, err = cloudflareCredentialsPath()
		if err != nil {
			return nil, err
		}
	}
	configDir, err := configuredCertbotConfigDir()
	if err != nil {
		return nil, err
	}

	readiness := GetStatus()
	if !readiness.Available {
		return nil, fmt.Errorf("certbot is not ready: %s", readiness.Error)
	}
	if !readiness.PluginsAvailable {
		return nil, fmt.Errorf("certbot plugin inventory is unavailable: %s", readiness.PluginError)
	}
	if req.Method == "nginx" && !readiness.NginxPlugin {
		return nil, fmt.Errorf("certbot nginx authenticator is not installed")
	}
	if req.Method == "dns-cloudflare" && !readiness.DNSCloudflarePlugin {
		return nil, fmt.Errorf("certbot dns-cloudflare authenticator is not installed")
	}

	args := []string{
		"certonly",
		"--config-dir", configDir,
		"--non-interactive",
		"--agree-tos",
	}

	if req.Email != "" {
		args = append(args, "--email", req.Email)
	} else {
		args = append(args, "--register-unsafely-without-email")
	}

	switch req.Method {
	case "dns-cloudflare":
		args = append(args, "--dns-cloudflare")
		args = append(args, "--dns-cloudflare-credentials", credentialsPath)
	case "nginx":
		args = append(args, "--nginx")
	}

	args = append(args, "-d", req.Domain)
	for _, san := range req.SANs {
		if strings.ContainsAny(san, "/\\;|&`$()") {
			return nil, fmt.Errorf("invalid SAN: %s", san)
		}
		args = append(args, "-d", san)
	}

	output, success, err := runCertbot(5*time.Minute, args...)
	if err != nil {
		return nil, fmt.Errorf("certbot certonly exec error: %w", err)
	}

	return &IssueResult{
		Success: success,
		Output:  output,
		Domain:  req.Domain,
	}, nil
}

// parseCertDir reads and parses cert.pem for the given certbot cert-name.
func parseCertDir(name string) (*models.SSLCertificate, error) {
	configDir, err := configuredCertbotConfigDir()
	if err != nil {
		return nil, err
	}
	certPath := filepath.Join(configDir, "live", name, "cert.pem")
	keyPath := filepath.Join(configDir, "live", name, "privkey.pem")

	data, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("reading cert.pem: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in %s", certPath)
	}

	x509Cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("parsing certificate: %w", err)
	}

	now := time.Now()
	daysRemaining := int(x509Cert.NotAfter.Sub(now).Hours() / 24)

	sans := make([]string, 0, len(x509Cert.DNSNames))
	isWildcard := false
	for _, dnsName := range x509Cert.DNSNames {
		sans = append(sans, dnsName)
		if strings.HasPrefix(dnsName, "*.") {
			isWildcard = true
		}
	}

	primaryDomain := x509Cert.Subject.CommonName
	if primaryDomain == "" && len(sans) > 0 {
		primaryDomain = sans[0]
	}

	issuer := ""
	if len(x509Cert.Issuer.Organization) > 0 {
		issuer = x509Cert.Issuer.Organization[0]
	} else {
		issuer = x509Cert.Issuer.CommonName
	}

	subject := x509Cert.Subject.CommonName
	if subject == "" {
		subject = x509Cert.Subject.String()
	}

	serial := fmt.Sprintf("%X", x509Cert.SerialNumber)

	return &models.SSLCertificate{
		Domain:        primaryDomain,
		Issuer:        issuer,
		Subject:       subject,
		Serial:        serial,
		NotBefore:     x509Cert.NotBefore,
		NotAfter:      x509Cert.NotAfter,
		ExpiresAt:     x509Cert.NotAfter,
		IsWildcard:    isWildcard,
		AutoRenew:     true,
		DaysRemaining: daysRemaining,
		CertPath:      certPath,
		KeyPath:       keyPath,
		SANs:          sans,
	}, nil
}
