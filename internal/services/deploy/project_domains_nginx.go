package deploy

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

const (
	defaultNginxSitesAvailable = "/etc/nginx/sites-available"
	defaultNginxSitesEnabled   = "/etc/nginx/sites-enabled"
	defaultCertbotBinary       = "certbot"
	defaultCertbotConfigDir    = "/etc/letsencrypt"
	defaultCertbotWorkDir      = "/var/lib/letsencrypt"
	defaultCertbotLogsDir      = "/var/log/letsencrypt"
	defaultACMEWebroot         = "/var/www/hserver-acme"
)

var (
	projectDomainBinaryPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	projectDomainOwnerPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,254}$`)
)

// ErrProjectDomainCleanup is intentionally a closed boundary marker. Callers
// may map it to a safe result code without exposing an OS error or filesystem
// path returned while rolling back a failed Nginx mutation.
var ErrProjectDomainCleanup = errors.New("project domain cleanup failed")

type projectDomainCommand func(time.Duration, string, ...string) (string, error)

// NginxProjectDomainRuntime writes only HServer-owned HTTP reverse-proxy
// virtual hosts. It tests the complete Nginx configuration before every reload
// and restores the prior filesystem/runtime state when a step fails.
type NginxProjectDomainRuntime struct {
	sitesAvailable string
	sitesEnabled   string
	certbotBin     string
	certbotConfig  string
	certbotWork    string
	certbotLogs    string
	acmeWebroot    string
	run            projectDomainCommand
	remove         func(string) error
}

func NewNginxProjectDomainRuntime(sitesAvailable, sitesEnabled string) *NginxProjectDomainRuntime {
	return NewNginxProjectDomainRuntimeWithTLS(sitesAvailable, sitesEnabled, "", "", "")
}

func NewNginxProjectDomainRuntimeWithTLS(sitesAvailable, sitesEnabled, certbotBin, certbotConfig, acmeWebroot string) *NginxProjectDomainRuntime {
	return NewNginxProjectDomainRuntimeWithCertbotStorage(sitesAvailable, sitesEnabled, certbotBin, certbotConfig, "", "", acmeWebroot)
}

func NewNginxProjectDomainRuntimeWithCertbotStorage(sitesAvailable, sitesEnabled, certbotBin, certbotConfig, certbotWork, certbotLogs, acmeWebroot string) *NginxProjectDomainRuntime {
	if strings.TrimSpace(sitesAvailable) == "" {
		sitesAvailable = defaultNginxSitesAvailable
	}
	if strings.TrimSpace(sitesEnabled) == "" {
		sitesEnabled = defaultNginxSitesEnabled
	}
	if strings.TrimSpace(certbotBin) == "" {
		certbotBin = defaultCertbotBinary
	}
	if strings.TrimSpace(certbotConfig) == "" {
		certbotConfig = defaultCertbotConfigDir
	}
	if strings.TrimSpace(certbotWork) == "" {
		certbotWork = defaultCertbotWorkDir
	}
	if strings.TrimSpace(certbotLogs) == "" {
		certbotLogs = defaultCertbotLogsDir
	}
	if strings.TrimSpace(acmeWebroot) == "" {
		acmeWebroot = defaultACMEWebroot
	}
	return &NginxProjectDomainRuntime{
		sitesAvailable: filepath.Clean(sitesAvailable),
		sitesEnabled:   filepath.Clean(sitesEnabled),
		certbotBin:     strings.TrimSpace(certbotBin),
		certbotConfig:  filepath.Clean(certbotConfig),
		certbotWork:    filepath.Clean(certbotWork),
		certbotLogs:    filepath.Clean(certbotLogs),
		acmeWebroot:    filepath.Clean(acmeWebroot),
		run:            runProjectDomainCommand,
		remove:         removeProjectDomainPath,
	}
}

func (runtime *NginxProjectDomainRuntime) Apply(domain models.DeployDomain) error {
	if err := runtime.validate(); err != nil {
		return err
	}
	if !validProjectDomain(domain.Domain) || domain.HostPort < 1 || domain.HostPort > 65535 {
		return ErrInvalidProjectDomain
	}
	if _, err := projectDomainOwner(domain); err != nil {
		return ErrInvalidProjectDomain
	}
	filename := domain.Domain + ".conf"
	availablePath := filepath.Join(runtime.sitesAvailable, filename)
	enabledPath := filepath.Join(runtime.sitesEnabled, filename)
	if _, err := os.Lstat(availablePath); err == nil {
		return fmt.Errorf("%w: Nginx config %s is already owned", ErrProjectDomainConflict, filename)
	} else if !os.IsNotExist(err) {
		return err
	}
	if _, err := os.Lstat(enabledPath); err == nil {
		return fmt.Errorf("%w: enabled Nginx site %s already exists", ErrProjectDomainConflict, filename)
	} else if !os.IsNotExist(err) {
		return err
	}

	temp, err := os.CreateTemp(runtime.sitesAvailable, ".hserver-project-domain-*.tmp")
	if err != nil {
		return fmt.Errorf("stage Nginx project domain: %w", err)
	}
	tempPath := temp.Name()
	committed := false
	availableCommitted := false
	defer func() {
		_ = temp.Close()
		if !committed {
			_ = os.Remove(tempPath)
			if availableCommitted {
				_ = os.Remove(enabledPath)
				_ = os.Remove(availablePath)
			}
		}
	}()
	if err := temp.Chmod(0o644); err != nil {
		return err
	}
	content, err := runtime.configContent(domain)
	if err != nil {
		return err
	}
	if _, err := temp.WriteString(content); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, availablePath); err != nil {
		return fmt.Errorf("commit Nginx project domain: %w", err)
	}
	availableCommitted = true
	if err := os.Symlink(availablePath, enabledPath); err != nil {
		remove := runtime.remove
		if remove == nil {
			remove = removeProjectDomainPath
		}
		if cleanupErr := remove(availablePath); cleanupErr != nil {
			return ErrProjectDomainCleanup
		}
		return fmt.Errorf("enable Nginx project domain: %w", err)
	}
	if err := runtime.testAndReload(); err != nil {
		remove := runtime.remove
		if remove == nil {
			remove = removeProjectDomainPath
		}
		cleanupErr := errors.Join(remove(enabledPath), remove(availablePath))
		_, _ = runtime.run(15*time.Second, "nginx", "-t")
		_, _ = runtime.run(15*time.Second, "systemctl", "reload", "nginx")
		if cleanupErr != nil {
			return ErrProjectDomainCleanup
		}
		return err
	}
	committed = true
	return nil
}

func (runtime *NginxProjectDomainRuntime) EnableTLS(domain models.DeployDomain, email string) (models.DeployDomainTLSState, error) {
	if err := runtime.validateTLS(); err != nil {
		return models.DeployDomainTLSState{}, err
	}
	if !validProjectDomain(domain.Domain) {
		return models.DeployDomainTLSState{}, ErrInvalidProjectDomain
	}
	if err := runtime.requireOwnedConfig(domain); err != nil {
		return models.DeployDomainTLSState{}, err
	}
	if err := os.MkdirAll(runtime.acmeWebroot, 0o755); err != nil {
		return models.DeployDomainTLSState{}, fmt.Errorf("create ACME webroot: %w", err)
	}
	if err := os.Chmod(runtime.acmeWebroot, 0o755); err != nil {
		return models.DeployDomainTLSState{}, fmt.Errorf("set ACME webroot permissions: %w", err)
	}
	args := []string{
		"certonly", "--webroot", "--webroot-path", runtime.acmeWebroot,
		"--config-dir", runtime.certbotConfig,
		"--work-dir", runtime.certbotWork,
		"--logs-dir", runtime.certbotLogs,
		"--cert-name", domain.Domain,
		"--domain", domain.Domain,
		"--non-interactive", "--agree-tos", "--keep-until-expiring",
		"--deploy-hook", "systemctl reload nginx",
	}
	if email == "" {
		args = append(args, "--register-unsafely-without-email")
	} else {
		args = append(args, "--email", email)
	}
	if output, err := runtime.run(5*time.Minute, runtime.certbotBin, args...); err != nil {
		detail := boundedProcessDetail(output)
		if detail == "" {
			detail = err.Error()
		}
		return models.DeployDomainTLSState{}, fmt.Errorf("Certbot issuance failed: %s", detail)
	}
	state, err := runtime.TLSState(models.DeployDomain{Domain: domain.Domain, TLSEnabled: true})
	if err != nil {
		return models.DeployDomainTLSState{}, err
	}
	domain.TLSEnabled = true
	content, err := runtime.configContent(domain)
	if err != nil {
		return models.DeployDomainTLSState{}, err
	}
	if err := runtime.replaceOwnedConfig(domain, content); err != nil {
		return models.DeployDomainTLSState{}, err
	}
	return state, nil
}

func (runtime *NginxProjectDomainRuntime) DisableTLS(domain models.DeployDomain) error {
	if err := runtime.validate(); err != nil {
		return err
	}
	if err := runtime.requireOwnedConfig(domain); err != nil {
		return err
	}
	domain.TLSEnabled = false
	content, err := runtime.configContent(domain)
	if err != nil {
		return err
	}
	return runtime.replaceOwnedConfig(domain, content)
}

func (runtime *NginxProjectDomainRuntime) TLSState(domain models.DeployDomain) (models.DeployDomainTLSState, error) {
	if !domain.TLSEnabled {
		return models.DeployDomainTLSState{Status: "not_configured", Message: "TLS is not enabled for this project domain."}, nil
	}
	if err := runtime.validateTLSPaths(); err != nil {
		return models.DeployDomainTLSState{}, err
	}
	certPath, keyPath := runtime.certificatePaths(domain.Domain)
	if info, err := os.Stat(keyPath); err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = errors.New("private key path is not a regular file")
		}
		return models.DeployDomainTLSState{}, fmt.Errorf("inspect TLS private key: %w", err)
	}
	data, err := os.ReadFile(certPath)
	if err != nil {
		return models.DeployDomainTLSState{}, fmt.Errorf("read TLS certificate: %w", err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return models.DeployDomainTLSState{}, errors.New("TLS certificate contains no PEM block")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return models.DeployDomainTLSState{}, fmt.Errorf("parse TLS certificate: %w", err)
	}
	if err := certificate.VerifyHostname(domain.Domain); err != nil {
		return models.DeployDomainTLSState{}, fmt.Errorf("certificate does not cover %s: %w", domain.Domain, err)
	}
	now := time.Now().UTC()
	expires := certificate.NotAfter.UTC()
	days := int(time.Until(expires).Hours() / 24)
	state := models.DeployDomainTLSState{Status: "healthy", ExpiresAt: &expires, DaysRemaining: days, Message: "Managed TLS certificate is valid."}
	if !certificate.NotBefore.IsZero() && now.Before(certificate.NotBefore) {
		state.Status = "unavailable"
		state.Message = "Managed TLS certificate is not valid yet."
	} else if !now.Before(expires) {
		state.Status = "expired"
		state.Message = "Managed TLS certificate has expired."
	} else if days <= 30 {
		state.Status = "expiring"
		state.Message = "Managed TLS certificate expires within 30 days."
	}
	return state, nil
}

func (runtime *NginxProjectDomainRuntime) RenewTLS(domain models.DeployDomain) (models.DeployDomainTLSState, error) {
	if err := runtime.validateTLS(); err != nil {
		return models.DeployDomainTLSState{}, err
	}
	if !domain.TLSEnabled || !validProjectDomain(domain.Domain) {
		return models.DeployDomainTLSState{}, ErrInvalidProjectDomain
	}
	if err := runtime.requireOwnedConfig(domain); err != nil {
		return models.DeployDomainTLSState{}, err
	}
	args := []string{
		"renew", "--config-dir", runtime.certbotConfig,
		"--work-dir", runtime.certbotWork,
		"--logs-dir", runtime.certbotLogs,
		"--cert-name", domain.Domain,
		"--non-interactive", "--deploy-hook", "systemctl reload nginx",
	}
	if output, err := runtime.run(5*time.Minute, runtime.certbotBin, args...); err != nil {
		detail := boundedProcessDetail(output)
		if detail == "" {
			detail = err.Error()
		}
		return models.DeployDomainTLSState{}, fmt.Errorf("Certbot renewal failed: %s", detail)
	}
	state, err := runtime.TLSState(domain)
	if err != nil {
		return models.DeployDomainTLSState{}, err
	}
	if state.Status == "expired" {
		return models.DeployDomainTLSState{}, errors.New("Certbot renewal completed without replacing the expired certificate")
	}
	return state, nil
}

func (runtime *NginxProjectDomainRuntime) Remove(domain models.DeployDomain) error {
	if err := runtime.validate(); err != nil {
		return err
	}
	filename := domain.Domain + ".conf"
	availablePath := filepath.Join(runtime.sitesAvailable, filename)
	enabledPath := filepath.Join(runtime.sitesEnabled, filename)
	content, err := os.ReadFile(availablePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !ownedProjectDomainConfig(string(content), domain) {
		return fmt.Errorf("refusing to remove Nginx config not owned by this project domain")
	}

	suffix := fmt.Sprintf(".hserver-delete-%d", time.Now().UnixNano())
	availableTombstone := availablePath + suffix
	enabledTombstone := enabledPath + suffix
	enabledMoved := false
	if info, statErr := os.Lstat(enabledPath); statErr == nil {
		if info.Mode()&os.ModeSymlink == 0 {
			return errors.New("refusing to remove a non-symlink enabled Nginx site")
		}
		link, readErr := os.Readlink(enabledPath)
		if readErr != nil || filepath.Clean(link) != availablePath {
			return errors.New("refusing to remove an unexpected enabled Nginx symlink")
		}
		if err := os.Rename(enabledPath, enabledTombstone); err != nil {
			return err
		}
		enabledMoved = true
	} else if !os.IsNotExist(statErr) {
		return statErr
	}
	if err := os.Rename(availablePath, availableTombstone); err != nil {
		if enabledMoved {
			_ = os.Rename(enabledTombstone, enabledPath)
		}
		return err
	}
	restore := func() {
		_ = os.Rename(availableTombstone, availablePath)
		if enabledMoved {
			_ = os.Rename(enabledTombstone, enabledPath)
		}
	}
	if err := runtime.testAndReload(); err != nil {
		restore()
		_, _ = runtime.run(15*time.Second, "nginx", "-t")
		_, _ = runtime.run(15*time.Second, "systemctl", "reload", "nginx")
		return err
	}
	_ = os.Remove(availableTombstone)
	if enabledMoved {
		_ = os.Remove(enabledTombstone)
	}
	return nil
}

func (runtime *NginxProjectDomainRuntime) validate() error {
	for label, path := range map[string]string{"sites-available": runtime.sitesAvailable, "sites-enabled": runtime.sitesEnabled} {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("Nginx %s path must be absolute", label)
		}
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("inspect Nginx %s: %w", label, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("Nginx %s path is not a directory", label)
		}
	}
	return nil
}

func (runtime *NginxProjectDomainRuntime) validateTLS() error {
	if err := runtime.validate(); err != nil {
		return err
	}
	if err := runtime.validateTLSPaths(); err != nil {
		return err
	}
	if strings.Contains(runtime.certbotBin, "/") {
		if !filepath.IsAbs(runtime.certbotBin) || filepath.Clean(runtime.certbotBin) != runtime.certbotBin {
			return errors.New("Certbot binary must be an absolute normalized path or command name")
		}
	} else if !projectDomainBinaryPattern.MatchString(runtime.certbotBin) {
		return errors.New("Certbot binary contains an invalid command name")
	}
	return nil
}

func (runtime *NginxProjectDomainRuntime) validateTLSPaths() error {
	if !filepath.IsAbs(runtime.certbotConfig) || !filepath.IsAbs(runtime.certbotWork) || !filepath.IsAbs(runtime.certbotLogs) || !filepath.IsAbs(runtime.acmeWebroot) {
		return errors.New("Certbot config, work, logs, and ACME webroot paths must be absolute")
	}
	return nil
}

func (runtime *NginxProjectDomainRuntime) requireOwnedConfig(domain models.DeployDomain) error {
	content, err := os.ReadFile(filepath.Join(runtime.sitesAvailable, domain.Domain+".conf"))
	if err != nil {
		return err
	}
	if !ownedProjectDomainConfig(string(content), domain) {
		return errors.New("Nginx config is not owned by this project domain")
	}
	return nil
}

func (runtime *NginxProjectDomainRuntime) replaceOwnedConfig(domain models.DeployDomain, content string) error {
	availablePath := filepath.Join(runtime.sitesAvailable, domain.Domain+".conf")
	current, err := os.ReadFile(availablePath)
	if err != nil {
		return err
	}
	if !ownedProjectDomainConfig(string(current), domain) {
		return errors.New("Nginx config is not owned by this project domain")
	}
	temp, err := os.CreateTemp(runtime.sitesAvailable, ".hserver-project-domain-tls-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() { _ = os.Remove(tempPath) }()
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.WriteString(content); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	backupPath := availablePath + fmt.Sprintf(".hserver-tls-%d", time.Now().UnixNano())
	if err := os.Rename(availablePath, backupPath); err != nil {
		return err
	}
	restore := func() {
		_ = os.Remove(availablePath)
		_ = os.Rename(backupPath, availablePath)
	}
	if err := os.Rename(tempPath, availablePath); err != nil {
		restore()
		return err
	}
	if err := runtime.testAndReload(); err != nil {
		restore()
		_, _ = runtime.run(15*time.Second, "nginx", "-t")
		_, _ = runtime.run(15*time.Second, "systemctl", "reload", "nginx")
		return err
	}
	_ = os.Remove(backupPath)
	return nil
}

func (runtime *NginxProjectDomainRuntime) testAndReload() error {
	if output, err := runtime.run(15*time.Second, "nginx", "-t"); err != nil {
		return fmt.Errorf("Nginx configuration test failed: %s", boundedProcessDetail(output))
	}
	if output, err := runtime.run(15*time.Second, "systemctl", "reload", "nginx"); err != nil {
		return fmt.Errorf("Nginx reload failed: %s", boundedProcessDetail(output))
	}
	return nil
}

func removeProjectDomainPath(path string) error {
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func runProjectDomainCommand(timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if ctx.Err() != nil {
		return string(output), ctx.Err()
	}
	return string(output), err
}

func (runtime *NginxProjectDomainRuntime) configContent(domain models.DeployDomain) (string, error) {
	owner, err := projectDomainOwner(domain)
	if err != nil {
		return "", err
	}
	if domain.TLSEnabled {
		if _, err := runtime.TLSState(domain); err != nil {
			return "", err
		}
		certPath, keyPath := runtime.certificatePaths(domain.Domain)
		return fmt.Sprintf(`# Managed by HServer project domains. Do not edit manually.
# hserver-project-target: %s
# hserver-project-domain: %s
server {
    listen 80;
    listen [::]:80;
    server_name %s;

    location /.well-known/acme-challenge/ {
        root %s;
        try_files $uri =404;
    }

    location / {
        return 301 https://$host$request_uri;
    }
}

server {
    listen 443 ssl;
    listen [::]:443 ssl;
    server_name %s;

    ssl_certificate %s;
    ssl_certificate_key %s;
    ssl_protocols TLSv1.2 TLSv1.3;
    ssl_session_cache shared:SSL:10m;

    location / {
        proxy_pass http://127.0.0.1:%d;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
`, owner, domain.Domain, domain.Domain, runtime.acmeWebroot, domain.Domain, certPath, keyPath, domain.HostPort), nil
	}
	return fmt.Sprintf(`# Managed by HServer project domains. Do not edit manually.
# hserver-project-target: %s
# hserver-project-domain: %s
server {
    listen 80;
    listen [::]:80;
    server_name %s;

    location /.well-known/acme-challenge/ {
        root %s;
        try_files $uri =404;
    }

    location / {
        proxy_pass http://127.0.0.1:%d;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
`, owner, domain.Domain, domain.Domain, runtime.acmeWebroot, domain.HostPort), nil
}

func (runtime *NginxProjectDomainRuntime) certificatePaths(domain string) (string, string) {
	root := filepath.Join(runtime.certbotConfig, "live", domain)
	return filepath.Join(root, "fullchain.pem"), filepath.Join(root, "privkey.pem")
}

func ownedProjectDomainConfig(content string, domain models.DeployDomain) bool {
	owner, err := projectDomainOwner(domain)
	return err == nil && strings.Contains(content, "# hserver-project-target: "+owner+"\n") &&
		strings.Contains(content, "# hserver-project-domain: "+domain.Domain+"\n")
}

func projectDomainOwner(domain models.DeployDomain) (string, error) {
	owner := strings.TrimSpace(domain.RuntimeOwner)
	if owner == "" && domain.TargetID > 0 {
		owner = strconv.FormatInt(domain.TargetID, 10)
	}
	if !projectDomainOwnerPattern.MatchString(owner) {
		return "", ErrInvalidProjectDomain
	}
	return owner, nil
}
