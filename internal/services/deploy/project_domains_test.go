package deploy

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

type recordingProjectDomainRuntime struct {
	applied     []models.DeployDomain
	removed     []models.DeployDomain
	tlsEnabled  int
	tlsDisabled int
	tlsRenewed  int
	tlsState    string
	err         error
}

func (runtime *recordingProjectDomainRuntime) Apply(domain models.DeployDomain) error {
	runtime.applied = append(runtime.applied, domain)
	return runtime.err
}

func (runtime *recordingProjectDomainRuntime) Remove(domain models.DeployDomain) error {
	runtime.removed = append(runtime.removed, domain)
	return runtime.err
}

func (runtime *recordingProjectDomainRuntime) EnableTLS(_ models.DeployDomain, _ string) (models.DeployDomainTLSState, error) {
	runtime.tlsEnabled++
	return models.DeployDomainTLSState{Status: "healthy", Message: "valid"}, runtime.err
}

func (runtime *recordingProjectDomainRuntime) DisableTLS(_ models.DeployDomain) error {
	runtime.tlsDisabled++
	return runtime.err
}

func TestProjectDomainTLSPersistsDesiredStateWithoutClaimingFilesAlone(t *testing.T) {
	runtime := &recordingProjectDomainRuntime{}
	service, targetID := newProjectDomainService(t, runtime)
	domain, err := service.CreateProjectDomain(targetID, models.CreateDeployDomainRequest{Domain: "tls.example.com", Service: "web", HostPort: 8080})
	if err != nil {
		t.Fatal(err)
	}
	domain, err = service.EnableProjectDomainTLS(targetID, domain.ID, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if domain.TLSStatus != "healthy" || runtime.tlsEnabled != 1 {
		t.Fatalf("enabled domain=%#v runtime=%#v", domain, runtime)
	}
	domain, err = service.DisableProjectDomainTLS(targetID, domain.ID)
	if err != nil {
		t.Fatal(err)
	}
	if domain.TLSStatus != "not_configured" || runtime.tlsDisabled != 1 {
		t.Fatalf("disabled domain=%#v runtime=%#v", domain, runtime)
	}
	if _, err := service.EnableProjectDomainTLS(targetID, domain.ID, "Display Name <admin@example.com>"); !errors.Is(err, ErrInvalidProjectDomain) {
		t.Fatalf("display-name email error = %v", err)
	}
	if _, err := service.EnableProjectDomainTLS(targetID, domain.ID, strings.Repeat("a", 243)+"@example.com"); !errors.Is(err, ErrInvalidProjectDomain) {
		t.Fatalf("oversized email error = %v", err)
	}
}

func (runtime *recordingProjectDomainRuntime) TLSState(domain models.DeployDomain) (models.DeployDomainTLSState, error) {
	if runtime.err != nil {
		return models.DeployDomainTLSState{}, runtime.err
	}
	if domain.TLSEnabled {
		status := runtime.tlsState
		if status == "" {
			status = "healthy"
		}
		return models.DeployDomainTLSState{Status: status, Message: "valid"}, nil
	}
	return models.DeployDomainTLSState{Status: "not_configured", Message: "not configured"}, nil
}

func (runtime *recordingProjectDomainRuntime) RenewTLS(_ models.DeployDomain) (models.DeployDomainTLSState, error) {
	runtime.tlsRenewed++
	if runtime.err != nil {
		return models.DeployDomainTLSState{}, runtime.err
	}
	runtime.tlsState = "healthy"
	return models.DeployDomainTLSState{Status: "healthy", Message: "renewed"}, nil
}

func TestProjectDomainTLSMaintenanceRenewsOnlyExpiringMappings(t *testing.T) {
	runtime := &recordingProjectDomainRuntime{}
	service, targetID := newProjectDomainService(t, runtime)
	domain, err := service.CreateProjectDomain(targetID, models.CreateDeployDomainRequest{Domain: "renew.example.com", Service: "web", HostPort: 8080})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.EnableProjectDomainTLS(targetID, domain.ID, ""); err != nil {
		t.Fatal(err)
	}
	runtime.tlsState = "expiring"
	report := service.MaintainProjectDomainTLS()
	if report.Checked != 1 || report.Renewed != 1 || report.Failed != 0 || runtime.tlsRenewed != 1 {
		t.Fatalf("maintenance=%#v runtime=%#v", report, runtime)
	}
	report = service.MaintainProjectDomainTLS()
	if report.Checked != 1 || report.Renewed != 0 || report.Failed != 0 || runtime.tlsRenewed != 1 {
		t.Fatalf("healthy maintenance=%#v runtime=%#v", report, runtime)
	}
}

func newProjectDomainService(t *testing.T, runtime ProjectDomainRuntime) (*Service, int64) {
	t.Helper()
	database := openMemDB(t)
	service, err := NewWithProjectDomainRuntime(database, t.TempDir(), runtime)
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.CreateTarget(models.CreateDeployTargetRequest{
		Name: "Example project", ProjectDir: "/srv/example", DeployKind: models.DeployKindCompose,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, target.ID
}

func TestProjectDomainsPersistOnlyValidatedLoopbackMappings(t *testing.T) {
	runtime := &recordingProjectDomainRuntime{}
	service, targetID := newProjectDomainService(t, runtime)
	domain, err := service.CreateProjectDomain(targetID, models.CreateDeployDomainRequest{
		Domain: " App.Example.COM. ", Service: "web", HostPort: 8080,
	})
	if err != nil {
		t.Fatal(err)
	}
	if domain.Domain != "app.example.com" || domain.Upstream != "http://127.0.0.1:8080" || domain.TLSStatus != "not_configured" {
		t.Fatalf("unexpected domain: %#v", domain)
	}
	if len(runtime.applied) != 1 || runtime.applied[0].Service != "web" {
		t.Fatalf("runtime apply = %#v", runtime.applied)
	}
	list, err := service.ProjectDomains(targetID)
	if err != nil || len(list) != 1 || list[0].ID != domain.ID {
		t.Fatalf("list = %#v, err=%v", list, err)
	}
	if _, err := service.CreateProjectDomain(targetID, models.CreateDeployDomainRequest{Domain: "app.example.com", Service: "web", HostPort: 8081}); !errors.Is(err, ErrProjectDomainConflict) {
		t.Fatalf("duplicate error = %v", err)
	}
	if err := service.DeleteTarget(targetID); !errors.Is(err, ErrProjectDomainsAttached) {
		t.Fatalf("delete target error = %v", err)
	}
	if err := service.DeleteProjectDomain(targetID, domain.ID); err != nil {
		t.Fatal(err)
	}
	if len(runtime.removed) != 1 {
		t.Fatalf("runtime removals = %#v", runtime.removed)
	}
}

func TestProjectDomainRuntimeFailureDoesNotPersistMapping(t *testing.T) {
	runtime := &recordingProjectDomainRuntime{err: errors.New("nginx unavailable")}
	service, targetID := newProjectDomainService(t, runtime)
	_, err := service.CreateProjectDomain(targetID, models.CreateDeployDomainRequest{Domain: "app.example.com", Service: "web", HostPort: 8080})
	if err == nil || !strings.Contains(err.Error(), "nginx unavailable") {
		t.Fatalf("create error = %v", err)
	}
	domains, listErr := service.ProjectDomains(targetID)
	if listErr != nil || len(domains) != 0 {
		t.Fatalf("domains = %#v, err=%v", domains, listErr)
	}
}

func TestProjectDomainHealthDistinguishesHealthyAndUnhealthyResponses(t *testing.T) {
	for _, test := range []struct {
		code int
		want string
	}{{http.StatusNoContent, "healthy"}, {http.StatusServiceUnavailable, "unhealthy"}} {
		t.Run(test.want, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(test.code) }))
			defer server.Close()
			port := server.Listener.Addr().(*net.TCPAddr).Port
			runtime := &recordingProjectDomainRuntime{}
			service, targetID := newProjectDomainService(t, runtime)
			domain, err := service.CreateProjectDomain(targetID, models.CreateDeployDomainRequest{Domain: "app.example.com", Service: "web", HostPort: port})
			if err != nil {
				t.Fatal(err)
			}
			health, err := service.ProjectDomainHealth(context.Background(), targetID, domain.ID)
			if err != nil {
				t.Fatal(err)
			}
			if health.Status != test.want || health.StatusCode != test.code {
				t.Fatalf("health = %#v", health)
			}
		})
	}
}

func TestNginxProjectDomainRuntimeRollsBackFailedConfigTest(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	if err := os.Mkdir(available, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(enabled, 0o755); err != nil {
		t.Fatal(err)
	}
	runtime := NewNginxProjectDomainRuntime(available, enabled)
	runtime.run = func(_ time.Duration, name string, args ...string) (string, error) {
		if name == "nginx" {
			return "nginx: configuration file test failed", errors.New("exit 1")
		}
		return "", nil
	}
	domain := models.DeployDomain{TargetID: 7, Domain: "app.example.com", HostPort: 8080}
	if err := runtime.Apply(domain); err == nil {
		t.Fatal("expected config-test failure")
	}
	if _, err := os.Lstat(filepath.Join(available, "app.example.com.conf")); !os.IsNotExist(err) {
		t.Fatalf("available config remained: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(enabled, "app.example.com.conf")); !os.IsNotExist(err) {
		t.Fatalf("enabled config remained: %v", err)
	}
}

func TestNginxProjectDomainRuntimeUsesClosedCleanupError(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	if err := os.Mkdir(available, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(enabled, 0o755); err != nil {
		t.Fatal(err)
	}
	runtime := NewNginxProjectDomainRuntime(available, enabled)
	calls := 0
	runtime.run = func(_ time.Duration, name string, _ ...string) (string, error) {
		calls++
		if calls == 1 && name == "nginx" {
			return "configuration test failed", errors.New("exit 1")
		}
		return "ok", nil
	}
	runtime.remove = func(path string) error {
		return fmt.Errorf("cannot clean %s", path)
	}
	if err := runtime.Apply(models.DeployDomain{TargetID: 7, Domain: "cleanup.example.com", HostPort: 8080}); !errors.Is(err, ErrProjectDomainCleanup) {
		t.Fatalf("cleanup error = %v", err)
	} else if strings.Contains(err.Error(), available) || strings.Contains(err.Error(), enabled) {
		t.Fatalf("cleanup error leaked path: %v", err)
	}
}

func TestNginxProjectDomainRuntimeAppliesAndRemovesOwnedMapping(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	if err := os.Mkdir(available, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(enabled, 0o755); err != nil {
		t.Fatal(err)
	}
	runtime := NewNginxProjectDomainRuntime(available, enabled)
	commands := make([]string, 0, 4)
	runtime.run = func(_ time.Duration, name string, args ...string) (string, error) {
		commands = append(commands, strings.Join(append([]string{name}, args...), " "))
		return "ok", nil
	}
	domain := models.DeployDomain{TargetID: 7, Domain: "app.example.com", HostPort: 8080}
	if err := runtime.Apply(domain); err != nil {
		t.Fatal(err)
	}
	availablePath := filepath.Join(available, "app.example.com.conf")
	content, err := os.ReadFile(availablePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "proxy_pass http://127.0.0.1:8080;") || !ownedProjectDomainConfig(string(content), domain) {
		t.Fatalf("unexpected generated config:\n%s", content)
	}
	link, err := os.Readlink(filepath.Join(enabled, "app.example.com.conf"))
	if err != nil || link != availablePath {
		t.Fatalf("enabled link=%q err=%v", link, err)
	}
	if err := runtime.Remove(domain); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(availablePath); !os.IsNotExist(err) {
		t.Fatalf("available config remained: %v", err)
	}
	if got, want := strings.Join(commands, "\n"), "nginx -t\nsystemctl reload nginx\nnginx -t\nsystemctl reload nginx"; got != want {
		t.Fatalf("commands:\n%s\nwant:\n%s", got, want)
	}
}

func TestNginxProjectDomainRuntimeEnablesAndDisablesManagedTLS(t *testing.T) {
	root := t.TempDir()
	available := filepath.Join(root, "available")
	enabled := filepath.Join(root, "enabled")
	certbotConfig := filepath.Join(root, "letsencrypt")
	certbotWork := filepath.Join(root, "letsencrypt-work")
	certbotLogs := filepath.Join(root, "letsencrypt-logs")
	webroot := filepath.Join(root, "acme")
	for _, directory := range []string{available, enabled} {
		if err := os.Mkdir(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	runtime := NewNginxProjectDomainRuntimeWithCertbotStorage(available, enabled, "certbot", certbotConfig, certbotWork, certbotLogs, webroot)
	commands := make([]string, 0)
	runtime.run = func(_ time.Duration, name string, args ...string) (string, error) {
		commands = append(commands, strings.Join(append([]string{name}, args...), " "))
		if name == "certbot" {
			writeProjectDomainCertificate(t, certbotConfig, "tls.example.com", time.Now().Add(90*24*time.Hour))
		}
		return "ok", nil
	}
	domain := models.DeployDomain{TargetID: 8, Domain: "tls.example.com", HostPort: 8080}
	if err := runtime.Apply(domain); err != nil {
		t.Fatal(err)
	}
	state, err := runtime.EnableTLS(domain, "admin@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if state.Status != "healthy" || state.DaysRemaining < 89 {
		t.Fatalf("TLS state = %#v", state)
	}
	content, err := os.ReadFile(filepath.Join(available, "tls.example.com.conf"))
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"listen 443 ssl;", "return 301 https://$host$request_uri;", filepath.Join(certbotConfig, "live/tls.example.com/fullchain.pem"), "root " + webroot + ";"} {
		if !strings.Contains(string(content), required) {
			t.Fatalf("TLS config missing %q:\n%s", required, content)
		}
	}
	joined := strings.Join(commands, "\n")
	for _, required := range []string{"certbot certonly --webroot", "--config-dir " + certbotConfig, "--work-dir " + certbotWork, "--logs-dir " + certbotLogs, "--cert-name tls.example.com", "--domain tls.example.com", "--email admin@example.com", "--deploy-hook systemctl reload nginx"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("command missing %q:\n%s", required, joined)
		}
	}
	writeProjectDomainCertificate(t, certbotConfig, "tls.example.com", time.Now().Add(5*24*time.Hour))
	renewed, err := runtime.RenewTLS(models.DeployDomain{TargetID: 8, Domain: "tls.example.com", HostPort: 8080, TLSEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	joined = strings.Join(commands, "\n")
	if renewed.Status != "healthy" || !strings.Contains(joined, "certbot renew --config-dir "+certbotConfig) || !strings.Contains(joined, "--work-dir "+certbotWork) || !strings.Contains(joined, "--logs-dir "+certbotLogs) || !strings.Contains(joined, "--cert-name tls.example.com") {
		t.Fatalf("renewed=%#v commands=%v", renewed, commands)
	}
	if err := runtime.DisableTLS(models.DeployDomain{TargetID: 8, Domain: "tls.example.com", HostPort: 8080, TLSEnabled: true}); err != nil {
		t.Fatal(err)
	}
	content, err = os.ReadFile(filepath.Join(available, "tls.example.com.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), "listen 443 ssl;") || !strings.Contains(string(content), "proxy_pass http://127.0.0.1:8080;") {
		t.Fatalf("unexpected disabled config:\n%s", content)
	}
	if _, err := os.Stat(filepath.Join(certbotConfig, "live/tls.example.com/fullchain.pem")); err != nil {
		t.Fatalf("certificate should be preserved: %v", err)
	}
}

func writeProjectDomainCertificate(t *testing.T, configDir, domain string, notAfter time.Time) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-time.Hour)
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: domain}, DNSNames: []string{domain},
		NotBefore: now, NotAfter: notAfter, KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(configDir, "live", domain)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(filepath.Join(directory, "fullchain.pem"), certificate, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "privkey.pem"), []byte("test-private-key"), 0o600); err != nil {
		t.Fatal(err)
	}
}
