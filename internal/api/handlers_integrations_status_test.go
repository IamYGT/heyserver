package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/extensions"
	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/gdrive"
	"github.com/IamYGT/heyserver/internal/services/integrationstatus"
	"github.com/IamYGT/heyserver/internal/services/snapshot"
	"github.com/IamYGT/heyserver/internal/testutil"
)

func integrationStatusRouter(t *testing.T, service *integrationstatus.Service) http.Handler {
	t.Helper()
	deps := contractTestDeps(t)
	deps.IntegrationStatus = service
	return NewRouter(testutil.TestConfig(), testutil.MinimalWebFS(t), deps)
}

func TestIntegrationStatusRequiresAuthentication(t *testing.T) {
	handler := integrationStatusRouter(t, integrationstatus.New(nil, nil))
	req := httptest.NewRequest(http.MethodGet, "/api/integrations/status", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestIntegrationStatusReturnsSchemaV1LocalAggregate(t *testing.T) {
	// Keep the real constructor deterministic across hosts that may have a
	// Docker CLI installed: this test proves the response shape and catalog
	// join, not the host's daemon state.
	t.Setenv("PATH", t.TempDir())
	service := newIntegrationStatusService(testutil.TestConfig())
	handler := integrationStatusRouter(t, service)
	viewer := testutil.MakeUser(1, "viewer@test.com", models.RoleViewer)
	req := testutil.NewRequest(t, http.MethodGet, "/api/integrations/status", testutil.MakeToken(t, viewer))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}

	var body integrationstatus.Response
	testutil.ParseJSON(t, rec, &body)
	if body.SchemaVersion != integrationstatus.SchemaVersion {
		t.Fatalf("schema_version = %d, want %d", body.SchemaVersion, integrationstatus.SchemaVersion)
	}
	if body.ObservedAt.IsZero() || body.ObservedAt.Location() != time.UTC {
		t.Fatalf("observed_at = %v, want a UTC timestamp", body.ObservedAt)
	}
	if body.Target.Scope != integrationstatus.ScopeLocalHost {
		t.Fatalf("target.scope = %q, want %q", body.Target.Scope, integrationstatus.ScopeLocalHost)
	}
	if len(body.Results) != 15 {
		t.Fatalf("results = %d, want 15", len(body.Results))
	}
	if len(body.Unprobed) != 0 {
		t.Fatalf("unprobed = %d, want 0", len(body.Unprobed))
	}
	if body.Partial {
		t.Fatal("partial = true, want false when all 15 probes are explicitly not configured")
	}
	for _, result := range body.Results {
		if result.State != integrationstate.NotConfigured || result.ErrorCode != integrationstatus.ErrorCodeNotConfigured {
			t.Fatalf("result = %#v, want not_configured with safe error code", result)
		}
	}
}

func writeIntegrationFakeCommand(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
}

func TestNewIntegrationStatusServiceWiresReadinessProbes(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	available := filepath.Join(root, "sites-available")
	enabled := filepath.Join(root, "sites-enabled")
	snippets := filepath.Join(root, "snippets")
	for _, directory := range []string{bin, available, enabled, snippets} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeIntegrationFakeCommand(t, bin, "nginx", `
if [ "$1" = "-t" ]; then
  exit 0
fi
exit 2`)
	writeIntegrationFakeCommand(t, bin, "systemctl", `
if [ "$1" = "is-active" ]; then
  echo active
  exit 0
fi
exit 2`)
	t.Setenv("PATH", bin)

	cfg := testutil.TestConfig()
	cfg.VhostsRoot = filepath.Join(root, "vhosts")
	cfg.NginxSitesAvailable = available
	cfg.NginxSitesEnabled = enabled
	cfg.NginxSnippetsDir = snippets
	if err := os.MkdirAll(cfg.VhostsRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	response, err := newIntegrationStatusService(cfg).Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(response.Results) != 15 || len(response.Unprobed) != 0 {
		t.Fatalf("aggregate sizes = results %d, unprobed %d; want 15 and 0", len(response.Results), len(response.Unprobed))
	}
	expected := map[string]struct {
		probe string
		state integrationstate.State
	}{
		integrationstatus.NginxID:                {probe: integrationstatus.NginxReadinessProbe, state: integrationstate.Healthy},
		integrationstatus.FirewallUFWID:          {probe: integrationstatus.UFWReadinessProbe, state: integrationstate.NotConfigured},
		integrationstatus.CertbotTLSID:           {probe: integrationstatus.CertbotReadinessProbe, state: integrationstate.NotConfigured},
		integrationstatus.Bind9DNSID:             {probe: integrationstatus.Bind9ReadinessProbe, state: integrationstate.NotConfigured},
		integrationstatus.PHPFPMRuntimeID:        {probe: integrationstatus.PHPFPMReadinessProbe, state: integrationstate.NotConfigured},
		integrationstatus.DatabaseLocalID:        {probe: integrationstatus.DatabaseReadinessProbe, state: integrationstate.NotConfigured},
		integrationstatus.SmartmontoolsID:        {probe: integrationstatus.SmartmontoolsReadinessProbe, state: integrationstate.NotConfigured},
		integrationstatus.StalwartMailID:         {probe: integrationstatus.StalwartReadinessProbe, state: integrationstate.NotConfigured},
		integrationstatus.MailAccessID:           {probe: integrationstatus.MailAccessReadinessProbe, state: integrationstate.NotConfigured},
		integrationstatus.GDriveBackupID:         {probe: integrationstatus.GDriveReadinessProbe, state: integrationstate.NotConfigured},
		integrationstatus.ResticSnapshotID:       {probe: integrationstatus.ResticReadinessProbe, state: integrationstate.NotConfigured},
		integrationstatus.NotificationDeliveryID: {probe: integrationstatus.NotificationReadinessProbe, state: integrationstate.NotConfigured},
	}
	for _, result := range response.Results {
		want, ok := expected[result.ID]
		if !ok {
			continue
		}
		if result.Probe != want.probe || result.State != want.state {
			t.Fatalf("result = %#v; want probe %q state %q", result, want.probe, want.state)
		}
		if want.state == integrationstate.Healthy && result.ErrorCode != "" {
			t.Fatalf("healthy result = %#v; want no error code", result)
		}
		if want.state == integrationstate.NotConfigured && result.ErrorCode != integrationstatus.ErrorCodeNotConfigured {
			t.Fatalf("not-configured result = %#v; want safe error code", result)
		}
		delete(expected, result.ID)
	}
	if len(expected) != 0 {
		t.Fatalf("wired probe results missing: %#v; results=%#v", expected, response.Results)
	}
}

func TestNewIntegrationStatusServiceWiresCatalogBackedAdditiveProbe(t *testing.T) {
	const additiveID = "community.example"
	t.Setenv("PATH", t.TempDir())
	loader := func() (extensions.Catalog, error) {
		catalog, err := extensions.LoadCatalog()
		if err != nil {
			return extensions.Catalog{}, err
		}
		catalog.Entries = append(catalog.Entries, extensions.Entry{ID: additiveID})
		return catalog, nil
	}
	service := newIntegrationStatusServiceWithCatalog(testutil.TestConfig(), loader, &Deps{
		IntegrationStatusProbes: []integrationstatus.Probe{{
			ID: additiveID,
			Run: func(context.Context) (integrationstate.State, error) {
				return integrationstate.Healthy, nil
			},
		}},
	})
	handler := integrationStatusRouter(t, service)
	admin := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	req := testutil.NewRequest(t, http.MethodGet, "/api/integrations/status", testutil.MakeToken(t, admin))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d (body=%s)", rec.Code, http.StatusOK, rec.Body.String())
	}
	var response integrationstatus.Response
	testutil.ParseJSON(t, rec, &response)
	for _, result := range response.Results {
		if result.ID != additiveID {
			continue
		}
		if result.State != integrationstate.Healthy || result.Probe != "community_example" || result.ErrorCode != "" {
			t.Fatalf("additive result = %#v; want healthy community_example result", result)
		}
		if containsIntegrationStatusID(response.Unprobed, additiveID) {
			t.Fatalf("additive result also listed as unprobed: %#v", response)
		}
		return
	}
	t.Fatalf("catalog-backed additive result %q missing: results=%#v unprobed=%#v", additiveID, response.Results, response.Unprobed)
}

func containsIntegrationStatusID(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func TestNewIntegrationStatusServiceUsesInjectedBackupServices(t *testing.T) {
	// Keep the legacy backup globals empty so a constructor that accidentally
	// falls back to them cannot make these two results appear configured. The
	// real services below have complete local prerequisites but deliberately
	// failing read-only executables, which is distinguishable from nil service
	// injection (not_configured).
	savedGDrive, savedSnapshot := gdriveSvc, snapshotSvc
	gdriveSvc, snapshotSvc = nil, nil
	t.Cleanup(func() {
		gdriveSvc, snapshotSvc = savedGDrive, savedSnapshot
	})

	root := t.TempDir()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	rclonePath := filepath.Join(bin, "rclone")
	resticPath := filepath.Join(bin, "restic")
	writeIntegrationFakeCommand(t, bin, "rclone", `
if [ "$1" = "version" ]; then
  exit 0
fi
printf 'private rclone failure\n' >&2
exit 17`)
	writeIntegrationFakeCommand(t, bin, "restic", `
printf 'private restic failure\n' >&2
exit 17`)
	t.Setenv("PATH", bin)

	gdriveDir := filepath.Join(root, "gdrive")
	if err := os.MkdirAll(gdriveDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gdriveDir, "gdrive-token.json"), []byte(`{"access_token":"access-token","refresh_token":"refresh-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gdriveDir, "rclone.conf"), []byte("[hserver-gdrive]\ntype = drive\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gdriveService := gdrive.New(gdriveDir, 0, "client-id", "client-secret", "", rclonePath, nil, nil)

	snapshotService := snapshot.New(
		filepath.Join(root, "snapshot"), "", "", 0,
		resticPath, rclonePath, "restic-password", gdriveService, nil,
	)
	service := newIntegrationStatusService(testutil.TestConfig(), &Deps{
		GDrive:   gdriveService,
		Snapshot: snapshotService,
	})
	response, err := service.Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if len(response.Results) != 15 || len(response.Unprobed) != 0 {
		t.Fatalf("aggregate sizes = results %d, unprobed %d; want 15 and 0", len(response.Results), len(response.Unprobed))
	}

	want := map[string]string{
		integrationstatus.GDriveBackupID:   integrationstatus.GDriveReadinessProbe,
		integrationstatus.ResticSnapshotID: integrationstatus.ResticReadinessProbe,
	}
	for _, result := range response.Results {
		probe, ok := want[result.ID]
		if !ok {
			continue
		}
		if result.Probe != probe || result.State != integrationstate.Unavailable || result.ErrorCode != integrationstatus.ErrorCodeProbeFailed {
			t.Fatalf("injected result = %#v; want probe %q unavailable/probe_failed", result, probe)
		}
		delete(want, result.ID)
	}
	if len(want) != 0 {
		t.Fatalf("injected backup results missing: %#v; results=%#v", want, response.Results)
	}
}

func TestNewIntegrationStatusServiceUsesFreshNotificationReceipt(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	deps := contractTestDeps(t)
	channel := &models.NotificationChannel{
		Name:    "Verified notification",
		Type:    models.ChannelSlack,
		Config:  `{"webhook_url":"https://hooks.example.com/verified"}`,
		Enabled: true,
	}
	if err := deps.ChannelRepo.Create(channel); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = deps.ChannelRepo.Delete(channel.ID) })
	if err := deps.DeliveryRepo.Upsert(&models.NotificationDeliveryReceipt{
		ChannelID:             channel.ID,
		ChannelConfigRevision: channel.ConfigRevision,
		Outcome:               models.NotificationDeliveryOutcomeSuccess,
		Source:                models.NotificationDeliverySourceManualTest,
		ObservedAt:            time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}

	response, err := newIntegrationStatusService(testutil.TestConfig(), deps).Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	for _, result := range response.Results {
		if result.ID != integrationstatus.NotificationDeliveryID {
			continue
		}
		if result.Probe != integrationstatus.NotificationReadinessProbe || result.State != integrationstate.Healthy || result.ErrorCode != "" {
			t.Fatalf("notification result = %#v, want healthy without error code", result)
		}
		return
	}
	t.Fatal("notification delivery result missing")
}

func TestIntegrationStatusKeepsProviderFailureHTTP200AndOmitsRawError(t *testing.T) {
	const secret = "cf-token-super-secret"
	service := integrationstatus.NewWithCatalog(func() (extensions.Catalog, error) {
		return extensions.Catalog{
			SchemaVersion: 1,
			Entries: []extensions.Entry{
				{ID: integrationstatus.ProcessPM2ID},
				{ID: integrationstatus.CloudflareDNSID},
			},
		}, nil
	},
		integrationstatus.Probe{ID: integrationstatus.ProcessPM2ID, Run: func(context.Context) (integrationstate.State, error) {
			return integrationstate.Healthy, nil
		}},
		integrationstatus.Probe{ID: integrationstatus.CloudflareDNSID, Run: func(context.Context) (integrationstate.State, error) {
			return integrationstate.Unavailable, errors.New("provider response included " + secret + " at /etc/cloudflare/token")
		}},
	)
	handler := integrationStatusRouter(t, service)
	admin := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	req := testutil.NewRequest(t, http.MethodGet, "/api/integrations/status", testutil.MakeToken(t, admin))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want HTTP 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var body integrationstatus.Response
	testutil.ParseJSON(t, rec, &body)
	if body.Results[1].State != integrationstate.Unavailable || body.Results[1].ErrorCode != integrationstatus.ErrorCodeProbeFailed {
		t.Fatalf("Cloudflare result = %#v", body.Results[1])
	}
	if !body.Partial {
		t.Fatal("partial = false, want true for provider failure")
	}
	for _, forbidden := range []string{secret, "/etc/cloudflare/token", "provider response"} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Fatalf("response leaked %q: %s", forbidden, rec.Body.String())
		}
	}
}

func TestIntegrationStatusMapsRequestDeadlineToSafeTimeoutResult(t *testing.T) {
	service := integrationstatus.NewWithCatalog(func() (extensions.Catalog, error) {
		return extensions.Catalog{
			SchemaVersion: 1,
			Entries: []extensions.Entry{
				{ID: integrationstatus.ProcessPM2ID},
				{ID: integrationstatus.CloudflareDNSID},
			},
		}, nil
	},
		integrationstatus.Probe{ID: integrationstatus.ProcessPM2ID, Run: func(ctx context.Context) (integrationstate.State, error) {
			<-ctx.Done()
			return integrationstate.Unavailable, ctx.Err()
		}},
		integrationstatus.Probe{ID: integrationstatus.CloudflareDNSID, Run: func(context.Context) (integrationstate.State, error) {
			return integrationstate.Healthy, nil
		}},
	)
	handler := integrationStatusRouter(t, service)
	admin := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req := testutil.NewRequest(t, http.MethodGet, "/api/integrations/status", testutil.MakeToken(t, admin)).WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want HTTP 200 (body=%s)", rec.Code, rec.Body.String())
	}
	var body integrationstatus.Response
	testutil.ParseJSON(t, rec, &body)
	if body.Results[0].State != integrationstate.Unavailable || body.Results[0].ErrorCode != integrationstatus.ErrorCodeTimeout {
		t.Fatalf("timeout result = %#v", body.Results[0])
	}
	if body.Results[1].State != integrationstate.Healthy {
		t.Fatalf("healthy result = %#v", body.Results[1])
	}
}

func TestIntegrationStatusRouteManifestIsProtected(t *testing.T) {
	for _, route := range AllRoutes() {
		if route.Method == http.MethodGet && route.Path == "/api/integrations/status" {
			if route.Auth != RouteProtected {
				t.Fatalf("status route auth = %q, want %q", route.Auth, RouteProtected)
			}
			return
		}
	}
	t.Fatal("integration status route missing from route manifest")
}

func TestIntegrationStatusWireDoesNotExposeUnknownFields(t *testing.T) {
	service := integrationstatus.New(nil, nil)
	recorder := httptest.NewRecorder()
	handleIntegrationStatus(service)(recorder, httptest.NewRequest(http.MethodGet, "/api/integrations/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want HTTP 200", recorder.Code)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode status response: %v", err)
	}
	for key := range raw {
		switch key {
		case "schema_version", "observed_at", "target", "results", "unprobed", "partial":
		default:
			t.Fatalf("unexpected response field %q", key)
		}
	}
}
