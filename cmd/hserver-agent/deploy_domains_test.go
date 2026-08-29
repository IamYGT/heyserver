package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/models"
)

type fakeDeployDomainRuntime struct {
	available string
	enabled   string
	states    map[string]string
	renewed   int
	applied   int
}

func (runtime *fakeDeployDomainRuntime) Apply(domain models.DeployDomain) error {
	runtime.applied++
	if _, err := os.Lstat(runtime.path(domain.Domain)); err == nil {
		return fmt.Errorf("domain already exists")
	}
	return runtime.write(domain)
}

func (runtime *fakeDeployDomainRuntime) Remove(domain models.DeployDomain) error {
	if err := os.Remove(filepath.Join(runtime.enabled, domain.Domain+".conf")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Remove(runtime.path(domain.Domain))
}

func (runtime *fakeDeployDomainRuntime) EnableTLS(domain models.DeployDomain, _ string) (models.DeployDomainTLSState, error) {
	domain.TLSEnabled = true
	runtime.states[domain.Domain] = "healthy"
	if err := runtime.write(domain); err != nil {
		return models.DeployDomainTLSState{}, err
	}
	return runtime.TLSState(domain)
}

func (runtime *fakeDeployDomainRuntime) DisableTLS(domain models.DeployDomain) error {
	domain.TLSEnabled = false
	delete(runtime.states, domain.Domain)
	return runtime.write(domain)
}

func (runtime *fakeDeployDomainRuntime) TLSState(domain models.DeployDomain) (models.DeployDomainTLSState, error) {
	if !domain.TLSEnabled {
		return models.DeployDomainTLSState{Status: "not_configured", Message: "not configured"}, nil
	}
	status := runtime.states[domain.Domain]
	if status == "" {
		status = "healthy"
	}
	expires := time.Now().UTC().Add(60 * 24 * time.Hour)
	return models.DeployDomainTLSState{Status: status, Message: "observed certificate", ExpiresAt: &expires, DaysRemaining: 60}, nil
}

func (runtime *fakeDeployDomainRuntime) RenewTLS(domain models.DeployDomain) (models.DeployDomainTLSState, error) {
	runtime.renewed++
	runtime.states[domain.Domain] = "healthy"
	return runtime.TLSState(domain)
}

func (runtime *fakeDeployDomainRuntime) path(domain string) string {
	return filepath.Join(runtime.available, domain+".conf")
}

func (runtime *fakeDeployDomainRuntime) write(domain models.DeployDomain) error {
	tls := ""
	if domain.TLSEnabled {
		tls = "    listen 443 ssl;\n"
	}
	content := fmt.Sprintf("# Managed by HServer project domains. Do not edit manually.\n# hserver-project-target: %s\n# hserver-project-domain: %s\nserver {\n%s    proxy_pass http://127.0.0.1:%d;\n}\n", domain.RuntimeOwner, domain.Domain, tls, domain.HostPort)
	if err := os.WriteFile(runtime.path(domain.Domain), []byte(content), 0o644); err != nil {
		return err
	}
	if err := os.MkdirAll(runtime.enabled, 0o755); err != nil {
		return err
	}
	if err := os.Remove(filepath.Join(runtime.enabled, domain.Domain+".conf")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(runtime.path(domain.Domain), filepath.Join(runtime.enabled, domain.Domain+".conf"))
}

func TestDeployDomainControllerOwnsLifecycleHealthAndTLSMaintenance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	defer server.Close()
	port := server.Listener.Addr().(*net.TCPAddr).Port
	controller, runtime := newDeployDomainTestController(t, port, true, true)

	created, err := controller.Create(context.Background(), "example-app", " App.Example.COM. ")
	if err != nil {
		t.Fatal(err)
	}
	if created.Domain != "app.example.com" || created.HostPort != port || created.Status != "active" || created.TLSStatus != "not_configured" {
		t.Fatalf("created domain = %#v", created)
	}
	health, err := controller.Health(context.Background(), "example-app", created.Domain)
	if err != nil || health.Status != "healthy" || health.StatusCode != http.StatusNoContent {
		t.Fatalf("health = %#v, err=%v", health, err)
	}

	executor := taskExecutor{deployDomains: controller}
	tlsResult := executor.execute(context.Background(), &agenthub.Task{Kind: agenthub.TaskDeployDomainAction, Payload: map[string]string{
		"target": "example-app", "domain": created.Domain, "action": "tls-enable", "email": "admin@example.com",
	}})
	if tlsResult.Status != agenthub.TaskStatusCompleted || !strings.Contains(tlsResult.Result["data"], `"tls_status":"healthy"`) {
		t.Fatalf("TLS task result = %#v", tlsResult)
	}
	runtime.states[created.Domain] = "expiring"
	report := controller.MaintainTLS()
	if report.Checked != 1 || report.Renewed != 1 || report.Failed != 0 || runtime.renewed != 1 {
		t.Fatalf("maintenance = %#v, renewed=%d", report, runtime.renewed)
	}
	if _, err := controller.DisableTLS(context.Background(), "example-app", created.Domain); err != nil {
		t.Fatal(err)
	}
	if err := controller.Delete(context.Background(), "example-app", created.Domain); err != nil {
		t.Fatal(err)
	}
	domains, err := controller.Inventory(context.Background(), "example-app")
	if err != nil || len(domains) != 0 {
		t.Fatalf("domains after delete = %#v, err=%v", domains, err)
	}
}

func TestDeployDomainControllerRequiresLocalOptInAndPlanPort(t *testing.T) {
	disabled, _ := newDeployDomainTestController(t, 8080, false, false)
	if _, err := disabled.Inventory(context.Background(), "example-app"); err == nil {
		t.Fatal("disabled project domain inventory succeeded")
	}
	if _, err := disabled.Create(context.Background(), "example-app", "app.example.com"); err == nil {
		t.Fatal("disabled project domain action succeeded")
	}
	controller, _ := newDeployDomainTestController(t, 0, true, true)
	if _, err := controller.Create(context.Background(), "example-app", "app.example.com"); err == nil || !strings.Contains(err.Error(), "host_port") {
		t.Fatalf("missing host_port error = %v", err)
	}
	if _, err := controller.Create(context.Background(), "example-app", "localhost"); err == nil {
		t.Fatal("invalid domain succeeded")
	}
}

func TestDeployDomainEnsureCreatesAbsentWithTypedReceipt(t *testing.T) {
	controller, runtime := newDeployDomainTestController(t, 8080, true, true)
	receipt, err := controller.Ensure(context.Background(), "example-app", "app.example.com", "absent")
	if err != nil {
		t.Fatal(err)
	}
	if !receipt.Changed || receipt.Observation.Status != "active" || !receipt.Observation.Enabled || receipt.Observation.HostPort != 8080 {
		t.Fatalf("ensure receipt = %#v", receipt)
	}
	if !agentDeployDomainRevisionPattern.MatchString(receipt.Observation.Revision) || runtime.applied != 1 {
		t.Fatalf("ensure revision/apply count = %q/%d", receipt.Observation.Revision, runtime.applied)
	}
}

func TestDeployDomainEnsureHealthyNoOpDoesNotWrite(t *testing.T) {
	controller, runtime := newDeployDomainTestController(t, 8080, true, true)
	created, err := controller.Ensure(context.Background(), "example-app", "app.example.com", "absent")
	if err != nil {
		t.Fatal(err)
	}
	before := runtime.applied
	receipt, err := controller.Ensure(context.Background(), "example-app", "app.example.com", created.Observation.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Changed || receipt.Observation.Revision != created.Observation.Revision || runtime.applied != before {
		t.Fatalf("healthy ensure = %#v, applied=%d before=%d", receipt, runtime.applied, before)
	}
}

func TestDeployDomainEnsureStaleObservationDoesNotMutate(t *testing.T) {
	controller, runtime := newDeployDomainTestController(t, 8080, true, true)
	created, err := controller.Ensure(context.Background(), "example-app", "app.example.com", "absent")
	if err != nil {
		t.Fatal(err)
	}
	before := runtime.applied
	result := (taskExecutor{deployDomains: controller}).execute(context.Background(), &agenthub.Task{
		Kind:    agenthub.TaskDeployDomainAction,
		Payload: map[string]string{"target": "example-app", "domain": "app.example.com", "action": "ensure", "expected_revision": strings.Repeat("0", 64)},
	})
	if result.Status != agenthub.TaskStatusFailed || result.Error != "stale_observation" || runtime.applied != before {
		t.Fatalf("stale result=%#v created=%#v applied=%d before=%d", result, created, runtime.applied, before)
	}
}

func TestDeployDomainEnsureRejectsCallerRuntimeFields(t *testing.T) {
	controller, runtime := newDeployDomainTestController(t, 8080, true, true)
	result := (taskExecutor{deployDomains: controller}).execute(context.Background(), &agenthub.Task{
		Kind: agenthub.TaskDeployDomainAction,
		Payload: map[string]string{
			"target": "example-app", "domain": "app.example.com", "action": "ensure",
			"expected_revision": "absent", "host_port": "8080",
		},
	})
	if result.Status != agenthub.TaskStatusFailed || runtime.applied != 0 {
		t.Fatalf("unsafe ensure result=%#v applied=%d", result, runtime.applied)
	}
}

func TestDeployDomainEnsureRefusesDriftAndForeignWithoutMutation(t *testing.T) {
	controller, runtime := newDeployDomainTestController(t, 8080, true, true)
	_, err := controller.Ensure(context.Background(), "example-app", "app.example.com", "absent")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(runtime.enabled, "app.example.com.conf")); err != nil {
		t.Fatal(err)
	}
	driftInventory, err := controller.Inventory(context.Background(), "example-app")
	if err != nil || len(driftInventory) != 1 {
		t.Fatalf("drift inventory = %#v, err=%v", driftInventory, err)
	}
	before := runtime.applied
	drift := (taskExecutor{deployDomains: controller}).execute(context.Background(), &agenthub.Task{
		Kind:    agenthub.TaskDeployDomainAction,
		Payload: map[string]string{"target": "example-app", "domain": "app.example.com", "action": "ensure", "expected_revision": driftInventory[0].Revision},
	})
	if drift.Status != agenthub.TaskStatusFailed || drift.Error != "domain_drift" || runtime.applied != before {
		t.Fatalf("drift result=%#v applied=%d before=%d", drift, runtime.applied, before)
	}

	if err := os.Remove(runtime.path("app.example.com")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtime.path("app.example.com"), []byte("server { listen 80; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	foreign := (taskExecutor{deployDomains: controller}).execute(context.Background(), &agenthub.Task{
		Kind:    agenthub.TaskDeployDomainAction,
		Payload: map[string]string{"target": "example-app", "domain": "app.example.com", "action": "ensure", "expected_revision": "absent"},
	})
	if foreign.Status != agenthub.TaskStatusFailed || foreign.Error != "domain_conflict" || runtime.applied != before {
		t.Fatalf("foreign result=%#v applied=%d before=%d", foreign, runtime.applied, before)
	}
}

func TestDeployDomainInventoryRevisionChangesWithConfigAndEnabledState(t *testing.T) {
	controller, runtime := newDeployDomainTestController(t, 8080, true, true)
	created, err := controller.Ensure(context.Background(), "example-app", "app.example.com", "absent")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := controller.Inventory(context.Background(), "example-app")
	if err != nil || len(entries) != 1 {
		t.Fatalf("inventory = %#v, err=%v", entries, err)
	}
	if entries[0].Revision != created.Observation.Revision || !entries[0].Enabled {
		t.Fatalf("healthy inventory = %#v, created=%#v", entries[0], created)
	}
	if err := os.Remove(filepath.Join(runtime.enabled, "app.example.com.conf")); err != nil {
		t.Fatal(err)
	}
	disabled, err := controller.Inventory(context.Background(), "example-app")
	if err != nil || len(disabled) != 1 || disabled[0].Enabled || disabled[0].Revision == entries[0].Revision || disabled[0].Status != "drifted" {
		t.Fatalf("disabled inventory = %#v, err=%v", disabled, err)
	}
	contentPath := runtime.path("app.example.com")
	content, err := os.ReadFile(contentPath)
	if err != nil {
		t.Fatal(err)
	}
	content = append(content, []byte("# changed\n")...)
	if err := os.WriteFile(contentPath, content, 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := controller.Inventory(context.Background(), "example-app")
	if err != nil || len(changed) != 1 || changed[0].Revision == disabled[0].Revision {
		t.Fatalf("changed inventory = %#v, err=%v", changed, err)
	}
}

func newDeployDomainTestController(t *testing.T, hostPort int, allowRead, allowActions bool) (deployDomainController, *fakeDeployDomainRuntime) {
	t.Helper()
	root := t.TempDir()
	available := filepath.Join(root, "available")
	if err := os.Mkdir(available, 0o755); err != nil {
		t.Fatal(err)
	}
	enabled := filepath.Join(root, "enabled")
	if err := os.Mkdir(enabled, 0o755); err != nil {
		t.Fatal(err)
	}
	plansPath := filepath.Join(root, "plans.json")
	document := deployPlanDocument{Plans: []deployPlanConfig{{
		ID: "example-app", Name: "Example app", Kind: "application", Path: root, HostPort: hostPort,
		Actions: map[string]deployActionConfig{"deploy": {Command: "/bin/true"}},
	}}}
	data, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(plansPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	deploys := newDeployController(&fakeDeployProcessRunner{}, true, true, plansPath)
	runtime := &fakeDeployDomainRuntime{available: available, enabled: enabled, states: map[string]string{}}
	controller := newDeployDomainController(deploys, runtime, allowRead, allowActions, available, enabled, &sync.Mutex{})
	return controller, runtime
}
