package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/IamYGT/heyserver/extensions"
)

func TestRunIntegrationsListUsesAuthenticatedCatalogGETAndJSON(t *testing.T) {
	t.Parallel()
	catalog := integrationTestCatalog()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != integrationCatalogEndpoint {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer integration-token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(catalog)
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL, "integrations", "list",
	}, &out, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "integration-token"
		}
		return ""
	})
	if err != nil {
		t.Fatal(err)
	}
	var got cliIntegrationCatalog
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if got.SchemaVersion != 1 || len(got.Entries) != len(catalog.Entries) {
		t.Fatalf("catalog = schema %d, entries %d", got.SchemaVersion, len(got.Entries))
	}
	if got.Entries[0].ID != "cloudflare.dns" || got.Entries[0].Configuration.SecretKeyNames[0] != "HSERVER_CF_API_TOKEN" {
		t.Fatalf("first entry = %#v", got.Entries[0])
	}
}

func TestRunIntegrationsListHumanOutputContainsRequiredColumns(t *testing.T) {
	t.Parallel()
	server := integrationCatalogServer(integrationTestCatalog())
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL, "integrations", "list", "--format", "text",
	}, &out, &bytes.Buffer{}, integrationTestEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, fragment := range []string{
		"HServer integrations catalog (schema v1)",
		"Catalog metadata only; it does not probe or report live integration health.",
		"ID\tNAME\tREQUIREMENT\tTARGETS",
		"cloudflare.dns\tCloudflare\toptional\tlocal_host",
		"web.nginx\tnginx\tfeature_specific\tlocal_host, managed_node",
	} {
		if !strings.Contains(text, fragment) {
			t.Errorf("human output does not contain %q:\n%s", fragment, text)
		}
	}
}

func TestRunIntegrationsShowHumanAndJSON(t *testing.T) {
	t.Parallel()
	server := integrationCatalogServer(integrationTestCatalog())
	defer server.Close()

	var human bytes.Buffer
	if err := run(context.Background(), []string{
		"--server", server.URL, "integrations", "show", "--format", "text", "cloudflare.dns",
	}, &human, &bytes.Buffer{}, integrationTestEnvironment); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"Integration: Cloudflare (cloudflare.dns)",
		"Purpose: Scoped Cloudflare DNS zone, record, proxy, cache, and mail-DNS reconciliation.",
		"Classes: provider_adapter, client_surface",
		"Targets: local_host",
		"Non-secret keys: HSERVER_CF_API_EMAIL",
		"Secret key names: HSERVER_CF_API_TOKEN",
		"api_token_missing -> not_configured:",
		"API route prefixes: /api/cloudflare",
		"Live health: not reported; this catalog is metadata and does not perform a live-health probe.",
	} {
		if !strings.Contains(human.String(), fragment) {
			t.Errorf("human output does not contain %q:\n%s", fragment, human.String())
		}
	}

	var machine bytes.Buffer
	if err := run(context.Background(), []string{
		"--server", server.URL, "integrations", "show", "cloudflare.dns",
	}, &machine, &bytes.Buffer{}, integrationTestEnvironment); err != nil {
		t.Fatal(err)
	}
	var entry cliIntegrationCatalogEntry
	if err := json.Unmarshal(machine.Bytes(), &entry); err != nil {
		t.Fatalf("show output is not JSON: %v\n%s", err, machine.String())
	}
	if entry.ID != "cloudflare.dns" || entry.DisplayName != "Cloudflare" || len(entry.Status.RawStateMappings) != 3 {
		t.Fatalf("show entry = %#v", entry)
	}
}

func TestRunIntegrationsStatusUsesAuthenticatedGETAndJSON(t *testing.T) {
	t.Parallel()
	status := integrationTestStatus()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer integration-token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case integrationCatalogEndpoint:
			_ = json.NewEncoder(w).Encode(integrationTestCatalog())
		case integrationStatusEndpoint:
			_ = json.NewEncoder(w).Encode(status)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL, "integrations", "status",
	}, &out, &bytes.Buffer{}, integrationTestEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	var got cliIntegrationStatusReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if got.SchemaVersion != 1 || got.ObservedAt != status.ObservedAt || got.Target.Scope != "local_host" {
		t.Fatalf("status envelope = %#v", got)
	}
	if !got.Partial || len(got.Results) != 2 || len(got.Unprobed) != 2 {
		t.Fatalf("status = %#v", got)
	}
	if got.Results[0].ID != "process.pm2" || got.Results[0].State != "healthy" || got.Results[0].Probe != "pm2_inventory" {
		t.Fatalf("first result = %#v", got.Results[0])
	}
	if got.Results[1].ErrorCode != "probe_failed" || got.Results[1].DurationMS != 5021 {
		t.Fatalf("second result = %#v", got.Results[1])
	}
}

func TestDecodeIntegrationStatusAcceptsFifteenCanonicalLocalProbes(t *testing.T) {
	report := cliIntegrationStatusReport{
		SchemaVersion: 1,
		ObservedAt:    "2026-08-28T12:00:00Z",
		Target:        cliIntegrationStatusTarget{Scope: "local_host"},
		Results: []cliIntegrationStatusResult{
			{ID: "process.pm2", State: "healthy", Probe: "pm2_inventory"},
			{ID: "cloudflare.dns", State: "healthy", Probe: "cloudflare_zone_list"},
			{ID: "container.docker", State: "healthy", Probe: "docker_info"},
			{ID: "web.nginx", State: "healthy", Probe: "nginx_readiness"},
			{ID: "firewall.ufw", State: "not_configured", Probe: "ufw_readiness", ErrorCode: "not_configured"},
			{ID: "tls.certbot", State: "healthy", Probe: "certbot_readiness"},
			{ID: "dns.bind9", State: "healthy", Probe: "bind9_readiness"},
			{ID: "runtime.php_fpm", State: "healthy", Probe: "php_fpm_readiness"},
			{ID: "database.local", State: "healthy", Probe: "database_readiness"},
			{ID: "storage.smartmontools", State: "not_configured", Probe: "smartmontools_readiness", ErrorCode: "not_configured"},
			{ID: "stalwart.mail", State: "healthy", Probe: "stalwart_readiness"},
			{ID: "mail.access", State: "healthy", Probe: "mail_access_readiness"},
			{ID: "backup.gdrive", State: "healthy", Probe: "gdrive_readiness"},
			{ID: "backup.snapshot.restic", State: "healthy", Probe: "restic_readiness"},
			{ID: "notification.delivery", State: "unavailable", Probe: "notification_readiness", ErrorCode: "probe_failed"},
		},
		Unprobed: []string{},
		Partial:  true,
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeIntegrationStatus(raw)
	if err != nil {
		t.Fatalf("decodeIntegrationStatus: %v", err)
	}
	if len(decoded.Results) != 15 || len(decoded.Unprobed) != 0 {
		t.Fatalf("decoded aggregate = results %d, unprobed %d; want 15 and 0", len(decoded.Results), len(decoded.Unprobed))
	}
}

func TestRunIntegrationsStatusTextShowsObservationResultsAndUnprobed(t *testing.T) {
	t.Parallel()
	server := integrationStatusServer(integrationTestStatus())
	defer server.Close()

	var out bytes.Buffer
	err := run(context.Background(), []string{
		"--server", server.URL, "integrations", "status", "--format", "text",
	}, &out, &bytes.Buffer{}, integrationTestEnvironment)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, fragment := range []string{
		"HServer integrations status (schema v1)",
		"Observed at (observed_at): 2026-08-28T12:00:00Z",
		"Target scope: local_host",
		"Partial: true",
		"process.pm2\thealthy\tpm2_inventory",
		"cloudflare.dns\tunavailable\tcloudflare_zone_list\tprobe_failed\t5021",
		"Unprobed: mail.access, backup.gdrive",
	} {
		if !strings.Contains(text, fragment) {
			t.Errorf("status text does not contain %q:\n%s", fragment, text)
		}
	}
}

func TestRunIntegrationsStatusManagedUsesAuthenticatedNodeGETAndIdentifiesTarget(t *testing.T) {
	t.Parallel()
	const nodeID = "edge-1"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/nodes/edge-1/integrations/status" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer integration-token" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"schema_version":1,"observed_at":"2026-08-28T12:00:00Z","target":{"scope":"managed_node","node_id":"edge-1"},"results":[{"id":"process.pm2","state":"healthy","probe":"pm2_inventory","duration_ms":184},{"id":"container.docker","state":"not_configured","probe":"docker_info","error_code":"not_configured","duration_ms":4}],"partial":true}`))
	}))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), []string{
		"--server", server.URL, "integrations", "status", "--node", nodeID,
	}, &out, &bytes.Buffer{}, integrationTestEnvironment); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("managed output is not JSON: %v\n%s", err, out.String())
	}
	target, ok := got["target"].(map[string]any)
	if !ok || target["scope"] != "managed_node" || target["node_id"] != nodeID {
		t.Fatalf("managed target = %#v", got["target"])
	}
	if got["partial"] != true {
		t.Fatalf("managed partial = %#v", got["partial"])
	}
	if _, exists := got["unprobed"]; exists {
		t.Fatalf("managed output unexpectedly contains unprobed: %s", out.String())
	}
}

func TestRunIntegrationsStatusManagedTextIdentifiesTargetAndPartial(t *testing.T) {
	t.Parallel()
	server := managedIntegrationStatusServer("edge-1", []byte(`{"schema_version":1,"observed_at":"2026-08-28T12:00:00Z","target":{"scope":"managed_node","node_id":"edge-1"},"results":[{"id":"process.pm2","state":"healthy","probe":"pm2_inventory"},{"id":"container.docker","state":"healthy","probe":"docker_info"}],"partial":false}`))
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), []string{
		"--server", server.URL, "integrations", "status", "--node", "edge-1", "--format", "text",
	}, &out, &bytes.Buffer{}, integrationTestEnvironment); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"Target scope: managed_node",
		"Target node: edge-1",
		"Partial: false",
		"process.pm2\thealthy\tpm2_inventory",
		"container.docker\thealthy\tdocker_info",
	} {
		if !strings.Contains(out.String(), fragment) {
			t.Errorf("managed status text does not contain %q:\n%s", fragment, out.String())
		}
	}
}

func TestRunIntegrationsStatusManagedRejectsMismatchedOrMalformedPayload(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		payload string
		want    string
	}{
		{
			name:    "target mismatch",
			payload: `{"schema_version":1,"observed_at":"2026-08-28T12:00:00Z","target":{"scope":"managed_node","node_id":"edge-2"},"results":[{"id":"process.pm2","state":"healthy","probe":"pm2_inventory"},{"id":"container.docker","state":"healthy","probe":"docker_info"}],"partial":false}`,
			want:    "target.node_id",
		},
		{
			name:    "wrong target scope",
			payload: `{"schema_version":1,"observed_at":"2026-08-28T12:00:00Z","target":{"scope":"local_host","node_id":"edge-1"},"results":[{"id":"process.pm2","state":"healthy","probe":"pm2_inventory"},{"id":"container.docker","state":"healthy","probe":"docker_info"}],"partial":false}`,
			want:    "invalid managed integration status result",
		},
		{
			name:    "wrong probe",
			payload: `{"schema_version":1,"observed_at":"2026-08-28T12:00:00Z","target":{"scope":"managed_node","node_id":"edge-1"},"results":[{"id":"process.pm2","state":"healthy","probe":"pm2_logs"},{"id":"container.docker","state":"healthy","probe":"docker_info"}],"partial":false}`,
			want:    "wrong probe",
		},
		{
			name:    "unknown raw field",
			payload: `{"schema_version":1,"observed_at":"2026-08-28T12:00:00Z","target":{"scope":"managed_node","node_id":"edge-1"},"results":[{"id":"process.pm2","state":"healthy","probe":"pm2_inventory"},{"id":"container.docker","state":"healthy","probe":"docker_info"}],"partial":false,"provider_error":"raw-secret"}`,
			want:    "unknown field",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := managedIntegrationStatusServer("edge-1", []byte(test.payload))
			defer server.Close()
			err := run(context.Background(), []string{
				"--server", server.URL, "integrations", "status", "--node", "edge-1",
			}, &bytes.Buffer{}, &bytes.Buffer{}, integrationTestEnvironment)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestRunIntegrationsStatusManagedRejectsEmptyNodeWithoutRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	err := run(context.Background(), []string{
		"--server", server.URL, "integrations", "status", "--node=  ",
	}, &bytes.Buffer{}, &bytes.Buffer{}, integrationTestEnvironment)
	if err == nil || err.Error() != "integrations status --node must not be empty" {
		t.Fatalf("error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestRunIntegrationsStatusManagedPathEscapesNodeID(t *testing.T) {
	if got, want := managedIntegrationStatusEndpoint("edge/one"), "/api/nodes/edge%2Fone/integrations/status"; got != want {
		t.Fatalf("endpoint = %q, want %q", got, want)
	}
}

func TestRunIntegrationsStatusManagedNoSecretFailsBeforeRequest(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()

	err := run(context.Background(), []string{
		"--server", server.URL, "integrations", "status", "--node", "edge-1",
	}, &bytes.Buffer{}, &bytes.Buffer{}, func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), "authentication token is not configured") {
		t.Fatalf("error = %v", err)
	}
	if requests.Load() != 0 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestRunIntegrationsStatusManagedHTTPErrorIsActionable(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/nodes/edge-1/integrations/status" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":"capability_unavailable"}`))
	}))
	defer server.Close()

	err := run(context.Background(), []string{
		"--server", server.URL, "integrations", "status", "--node", "edge-1",
	}, &bytes.Buffer{}, &bytes.Buffer{}, integrationTestEnvironment)
	if err == nil || !strings.Contains(err.Error(), "capability_unavailable") {
		t.Fatalf("error = %v", err)
	}
	if actionable := fmt.Sprintf("%v", err); !strings.Contains(actionable, "next: run hserverctl doctor") {
		t.Fatalf("actionable error = %q", actionable)
	}
}

func TestRunIntegrationsStatusRejectsMalformedPayload(t *testing.T) {
	t.Parallel()
	base := integrationTestStatus()
	tests := []struct {
		name string
		want string
		edit func(*cliIntegrationStatusReport)
	}{
		{
			name: "unsupported schema version",
			want: "unsupported schema version",
			edit: func(status *cliIntegrationStatusReport) { status.SchemaVersion = 2 },
		},
		{
			name: "non local target",
			want: "target.scope must be local_host",
			edit: func(status *cliIntegrationStatusReport) { status.Target.Scope = "managed_node" },
		},
		{
			name: "non canonical state",
			want: "not a canonical integration state",
			edit: func(status *cliIntegrationStatusReport) { status.Results[0].State = "degraded" },
		},
		{
			name: "unknown result ID",
			want: "is unknown",
			edit: func(status *cliIntegrationStatusReport) { status.Results[0].ID = "not-a-real-integration" },
		},
		{
			name: "malformed result ID",
			want: "is malformed",
			edit: func(status *cliIntegrationStatusReport) { status.Results[0].ID = "process pm2" },
		},
		{
			name: "duplicate result ID",
			want: "duplicate integration ID",
			edit: func(status *cliIntegrationStatusReport) { status.Results = append(status.Results, status.Results[0]) },
		},
		{
			name: "duplicate unprobed ID",
			want: "duplicate integration ID",
			edit: func(status *cliIntegrationStatusReport) {
				status.Unprobed = append(status.Unprobed, status.Unprobed[0])
			},
		},
		{
			name: "result and unprobed duplicate",
			want: "duplicate integration ID",
			edit: func(status *cliIntegrationStatusReport) {
				status.Unprobed = append(status.Unprobed, status.Results[0].ID)
			},
		},
		{
			name: "malformed unprobed ID",
			want: "is malformed",
			edit: func(status *cliIntegrationStatusReport) { status.Unprobed[0] = "not a real integration" },
		},
		{
			name: "unknown unprobed ID",
			want: "is unknown",
			edit: func(status *cliIntegrationStatusReport) { status.Unprobed[0] = "not-a-real-integration" },
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status := base
			status.Results = append([]cliIntegrationStatusResult(nil), base.Results...)
			status.Unprobed = append([]string(nil), base.Unprobed...)
			test.edit(&status)
			server := integrationStatusServer(status)
			defer server.Close()

			var out bytes.Buffer
			err := run(context.Background(), []string{
				"--server", server.URL, "integrations", "status",
			}, &out, &bytes.Buffer{}, integrationTestEnvironment)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestRunIntegrationsStatusJSONOmitsRawProviderErrorsAndSecrets(t *testing.T) {
	t.Parallel()
	payload := map[string]any{
		"schema_version": 1,
		"observed_at":    "2026-08-28T12:00:00Z",
		"target":         map[string]string{"scope": "local_host"},
		"results": []map[string]any{{
			"id":             "cloudflare.dns",
			"state":          "unavailable",
			"probe":          "cloudflare_zone_list",
			"error_code":     "probe_failed",
			"provider_error": "provider API token=raw-secret-value-123",
			"raw_error":      "raw provider failure raw-secret-value-123",
		}},
		"unprobed": []string{"process.pm2"},
		"partial":  true,
	}
	server := integrationStatusServer(payload)
	defer server.Close()

	var out bytes.Buffer
	if err := run(context.Background(), []string{
		"--server", server.URL, "integrations", "status",
	}, &out, &bytes.Buffer{}, integrationTestEnvironment); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out.String(), "raw-secret-value-123") || strings.Contains(out.String(), "provider_error") || strings.Contains(out.String(), "raw_error") {
		t.Fatalf("status output exposed provider diagnostics or secret:\n%s", out.String())
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if _, exists := got["provider_error"]; exists {
		t.Fatal("raw provider error was retained in JSON output")
	}
}

func TestRunIntegrationsShowRejectsUnknownIDFromServerCatalog(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		_ = json.NewEncoder(w).Encode(integrationTestCatalog())
	}))
	defer server.Close()

	err := run(context.Background(), []string{
		"--server", server.URL, "integrations", "show", "not-a-real-integration",
	}, &bytes.Buffer{}, &bytes.Buffer{}, integrationTestEnvironment)
	if err == nil || err.Error() != `integration ID "not-a-real-integration" is not present in the server catalog` {
		t.Fatalf("error = %v", err)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestRunIntegrationsAcceptsAdditiveCatalogEntryAndUnprobedStatus(t *testing.T) {
	t.Parallel()
	catalog := integrationTestCatalog()
	catalog.Entries = append(catalog.Entries, cliIntegrationCatalogEntry{
		ID: "community.example", DisplayName: "Community example", Requirement: "optional", Targets: []string{"local_host"},
	})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case integrationCatalogEndpoint:
			_ = json.NewEncoder(w).Encode(catalog)
		case integrationStatusEndpoint:
			status := integrationTestStatus()
			status.Results = append(status.Results, cliIntegrationStatusResult{
				ID: "community.example", State: "healthy", Probe: "community_example",
			})
			_ = json.NewEncoder(w).Encode(status)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	for _, args := range [][]string{
		{"--server", server.URL, "integrations", "list"},
		{"--server", server.URL, "integrations", "show", "community.example"},
	} {
		if err := run(context.Background(), args, &bytes.Buffer{}, &bytes.Buffer{}, integrationTestEnvironment); err != nil {
			t.Fatalf("run(%v): %v", args, err)
		}
	}
	var out bytes.Buffer
	if err := run(context.Background(), []string{"--server", server.URL, "integrations", "status"}, &out, &bytes.Buffer{}, integrationTestEnvironment); err != nil {
		t.Fatalf("run(status): %v", err)
	}
	var got cliIntegrationStatusReport
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("status output is not JSON: %v\n%s", err, out.String())
	}
	var found bool
	for _, result := range got.Results {
		if result.ID != "community.example" {
			continue
		}
		found = true
		if result.State != "healthy" || result.Probe != "community_example" {
			t.Fatalf("additive result = %#v", result)
		}
	}
	if !found {
		t.Fatalf("additive result missing from CLI status: %#v", got)
	}
}

func TestRunIntegrationsRejectsDuplicateAdditiveCatalogID(t *testing.T) {
	t.Parallel()
	catalog := integrationTestCatalog()
	catalog.Entries = append(catalog.Entries, catalog.Entries[0])
	server := integrationCatalogServer(catalog)
	defer server.Close()
	err := run(context.Background(), []string{"--server", server.URL, "integrations", "list"}, &bytes.Buffer{}, &bytes.Buffer{}, integrationTestEnvironment)
	if err == nil || !strings.Contains(err.Error(), "duplicate integration ID") {
		t.Fatalf("error = %v", err)
	}
}

func TestRunIntegrationsHelpAndCompletionIncludeNestedCommandsAndIDs(t *testing.T) {
	t.Parallel()
	var help bytes.Buffer
	if err := run(context.Background(), []string{"help"}, &help, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"integrations list [--format json|text]",
		"integrations show [--format json|text] ID",
		"integrations status [--format json|text]",
	} {
		if !strings.Contains(help.String(), fragment) {
			t.Errorf("help does not contain %q", fragment)
		}
	}
	for _, shell := range []string{"bash", "zsh", "fish"} {
		var completion bytes.Buffer
		if err := run(context.Background(), []string{"completion", shell}, &completion, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
			t.Fatalf("%s completion: %v", shell, err)
		}
		for _, fragment := range []string{"integrations", "list show status", "cloudflare.dns", "notification.delivery"} {
			if !strings.Contains(completion.String(), fragment) {
				t.Errorf("%s completion does not contain %q", shell, fragment)
			}
		}
		formatFragment := "--format"
		if shell == "fish" {
			formatFragment = "-l format"
		}
		if !strings.Contains(completion.String(), formatFragment) {
			t.Errorf("%s completion does not contain format flag %q", shell, formatFragment)
		}
	}
}

func TestIntegrationCompletionIDsComeFromAdditiveEmbeddedCatalog(t *testing.T) {
	catalog, err := extensions.LoadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	additional := catalog.Entries[0]
	additional.ID = "community.example"
	catalog.Entries = append(catalog.Entries, additional)
	ids := integrationCompletionIDsFromCatalog(catalog)
	if got := ids[len(ids)-1]; got != "community.example" {
		t.Fatalf("last completion ID = %q, want community.example", got)
	}
}

func integrationStatusServer(payload any) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case integrationCatalogEndpoint:
			_ = json.NewEncoder(w).Encode(integrationTestCatalog())
		case integrationStatusEndpoint:
			_ = json.NewEncoder(w).Encode(payload)
		default:
			http.NotFound(w, r)
		}
	}))
}

func managedIntegrationStatusServer(nodeID string, payload []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/nodes/"+nodeID+"/integrations/status" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
}

func integrationTestStatus() cliIntegrationStatusReport {
	return cliIntegrationStatusReport{
		SchemaVersion: 1,
		ObservedAt:    "2026-08-28T12:00:00Z",
		Target:        cliIntegrationStatusTarget{Scope: "local_host"},
		Results: []cliIntegrationStatusResult{
			{ID: "process.pm2", State: "healthy", Probe: "pm2_inventory", DurationMS: 184},
			{ID: "cloudflare.dns", State: "unavailable", Probe: "cloudflare_zone_list", ErrorCode: "probe_failed", DurationMS: 5021},
		},
		Unprobed: []string{"mail.access", "backup.gdrive"},
		Partial:  true,
	}
}

func integrationCatalogServer(catalog cliIntegrationCatalog) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != integrationCatalogEndpoint {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(catalog)
	}))
}

func integrationTestEnvironment(key string) string {
	if key == "HSERVER_TOKEN" {
		return "integration-token"
	}
	return ""
}

func integrationTestCatalog() cliIntegrationCatalog {
	entries := make([]cliIntegrationCatalogEntry, 0, len(integrationCoreIDs))
	for _, id := range integrationCoreIDs {
		entries = append(entries, cliIntegrationCatalogEntry{
			ID:          id,
			DisplayName: id,
			Requirement: "optional",
			Targets:     []string{"local_host"},
		})
	}
	entries[0] = cliIntegrationCatalogEntry{
		ID:            "cloudflare.dns",
		DisplayName:   "Cloudflare",
		Purpose:       "Scoped Cloudflare DNS zone, record, proxy, cache, and mail-DNS reconciliation.",
		Requirement:   "optional",
		DocsRowMarker: "optional-integrations:v1:cloudflare",
		Classes:       []string{"provider_adapter", "client_surface"},
		Targets:       []string{"local_host"},
		Configuration: cliIntegrationConfiguration{
			NonSecretKeys:  []string{"HSERVER_CF_API_EMAIL"},
			SecretKeyNames: []string{"HSERVER_CF_API_TOKEN"},
		},
		Status: cliIntegrationStatus{
			CanonicalStates: []string{"not_configured", "unavailable", "healthy"},
			RawStateMappings: []cliIntegrationRawStateMapping{
				{Raw: "api_token_missing", Canonical: "not_configured", Meaning: "The installation has no Cloudflare API token."},
				{Raw: "zone_probe_failed", Canonical: "unavailable", Meaning: "Configured Cloudflare access could not complete a read-only zone probe."},
				{Raw: "zone_probe_succeeded", Canonical: "healthy", Meaning: "A fresh read-only Cloudflare zone observation succeeded."},
			},
			APIRoutePrefixes: []string{"/api/cloudflare"},
		},
	}
	entries[6].Requirement = "feature_specific"
	entries[6].DisplayName = "nginx"
	entries[6].Targets = []string{"local_host", "managed_node"}
	return cliIntegrationCatalog{Schema: "./catalog.schema.json", SchemaVersion: 1, Entries: entries}
}
