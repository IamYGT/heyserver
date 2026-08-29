package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	cfservice "github.com/IamYGT/heyserver/internal/services/cloudflare"
)

func TestLoadTUICloudflareDistinguishesProviderStatesAndLocalBoundary(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		statusCode int
		body       string
		wantStatus string
		wantOK     bool
	}{
		{name: "not configured", statusCode: http.StatusServiceUnavailable, body: `{"error":"Cloudflare API token not configured (HSERVER_CF_API_TOKEN)"}`, wantStatus: "not_configured"},
		{name: "unavailable", statusCode: http.StatusBadGateway, body: `{"error":"provider request failed"}`, wantStatus: "unavailable"},
		{name: "healthy", statusCode: http.StatusOK, body: `[{
			"id":"zone-1","name":"example.com","status":"active","plan":{"id":"free","name":"Free"},"name_servers":["ns1.example.net"]
		}]`, wantStatus: "healthy", wantOK: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				requests.Add(1)
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.statusCode)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			client, err := newAPIClient(server.URL, "test-token", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			state, err := loadTUICloudflare(context.Background(), client, initialTUITargets()[0])
			if err != nil || state.Status != test.wantStatus || state.Supported != test.wantOK || requests.Load() != 1 {
				t.Fatalf("state=%#v err=%v requests=%d", state, err, requests.Load())
			}
			before := requests.Load()
			managed, err := loadTUICloudflare(context.Background(), client, tuiTarget{ID: "edge-1", Online: true})
			if err != nil || managed.Local || managed.Supported || !strings.Contains(managed.Message, "central panel") || requests.Load() != before {
				t.Fatalf("managed=%#v err=%v requests=%d", managed, err, requests.Load())
			}
		})
	}
}

func TestLoadTUICloudflareDetailPreservesInventoryWhenEmailRoutingFails(t *testing.T) {
	t.Parallel()
	zone := testTUICloudflareZone()
	record := testTUICloudflareRecord()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/cloudflare/zones/zone-1":
			_ = json.NewEncoder(writer).Encode(zone)
		case "/api/cloudflare/zones/zone-1/records":
			_ = json.NewEncoder(writer).Encode([]cliCloudflareRecord{record})
		case "/api/cloudflare/zones/zone-1/email-routing":
			writer.WriteHeader(http.StatusBadGateway)
			_, _ = writer.Write([]byte(`{"error":"email routing unavailable"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := loadTUICloudflareDetail(context.Background(), client, initialTUITargets()[0], zone)
	if err != nil || detail.Zone.ID != zone.ID || len(detail.Records) != 1 || detail.Records[0] != record || detail.EmailAvailable || !strings.Contains(detail.EmailRoutingError, "email routing unavailable") {
		t.Fatalf("detail=%#v err=%v", detail, err)
	}
}

func TestTUICloudflareZoneMutationUsesConfirmationFreshObservationAndReceipts(t *testing.T) {
	t.Parallel()
	zone := testTUICloudflareZone()
	var purges atomic.Int32
	var reconciles atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/cloudflare/zones/zone-1":
			_ = json.NewEncoder(writer).Encode(zone)
		case "POST /api/cloudflare/zones/zone-1/purge":
			purges.Add(1)
			assertCloudflareNoBody(t, request)
			_, _ = writer.Write([]byte(`{"status":"purged"}`))
		case "POST /api/cloudflare/mail-autofix/example.com":
			reconciles.Add(1)
			assertCloudflareNoBody(t, request)
			_, _ = writer.Write([]byte(`{"domain":"example.com","zoneId":"zone-1","changes":[{"action":"created"}]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	model := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
	model.loading = false
	model.snapshot.Selected = model.snapshot.Targets[0]
	model.cloudflare = tuiCloudflareState{Local: true, Supported: true, Status: "healthy", Detail: &tuiCloudflareDetail{Zone: zone}}
	model.cloudflareTarget, model.cloudflareLoaded = localTargetID, true

	updated, command := model.updateKey("O")
	model = updated.(tuiModel)
	if command != nil || model.tab != tuiTabCloudflare {
		t.Fatal("O did not open the already loaded Cloudflare section")
	}
	updated, command = model.updateKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogChoices || len(model.dialog.Options) != 2 || purges.Load() != 0 {
		t.Fatalf("zone choices=%#v purges=%d", model.dialog, purges.Load())
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || purges.Load() != 0 {
		t.Fatal("zone choice bypassed separate confirmation")
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || purges.Load() != 0 {
		t.Fatal("Enter bypassed Cloudflare confirmation")
	}
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("Y did not start Cloudflare purge")
	}
	message := command().(tuiOperationMsg)
	if message.Err != nil || purges.Load() != 1 || !strings.Contains(message.Message, "Purged") {
		t.Fatalf("message=%#v purges=%d", message, purges.Load())
	}
	messageText, err := runTUICloudflareOperation(context.Background(), client, tuiOperation{Target: initialTUITargets()[0], CloudflareResource: tuiCloudflareResourceZone, CloudflareZone: zone, Action: "mail-autofix"})
	if err != nil || reconciles.Load() != 1 || !strings.Contains(messageText, "Reconciled 1") {
		t.Fatalf("mail reconcile=%q err=%v reconciles=%d", messageText, err, reconciles.Load())
	}
}

func TestTUICloudflareRecordMutationsReobserveAndValidateReceipts(t *testing.T) {
	t.Parallel()
	zone := testTUICloudflareZone()
	record := testTUICloudflareRecord()
	var proxies atomic.Int32
	var deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/cloudflare/zones/zone-1":
			_ = json.NewEncoder(writer).Encode(zone)
		case "GET /api/cloudflare/zones/zone-1/records":
			_ = json.NewEncoder(writer).Encode([]cliCloudflareRecord{record})
		case "PUT /api/cloudflare/zones/zone-1/records/record-1/proxy":
			proxies.Add(1)
			var payload map[string]bool
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if len(payload) != 1 || payload["proxied"] {
				t.Errorf("payload=%#v", payload)
			}
			updated := record
			updated.Proxied = false
			_ = json.NewEncoder(writer).Encode(updated)
		case "DELETE /api/cloudflare/zones/zone-1/records/record-1":
			deletes.Add(1)
			assertCloudflareNoBody(t, request)
			writer.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	base := tuiOperation{Target: initialTUITargets()[0], CloudflareResource: tuiCloudflareResourceRecord, CloudflareZone: zone, CloudflareRecord: record}
	proxy := base
	proxy.Action = "toggle-proxy"
	message, err := runTUICloudflareOperation(context.Background(), client, proxy)
	if err != nil || proxies.Load() != 1 || !strings.Contains(message, "DNS-only") {
		t.Fatalf("proxy=%q err=%v count=%d", message, err, proxies.Load())
	}
	remove := base
	remove.Action = "delete"
	message, err = runTUICloudflareOperation(context.Background(), client, remove)
	if err != nil || deletes.Load() != 1 || !strings.Contains(message, "Deleted") {
		t.Fatalf("delete=%q err=%v count=%d", message, err, deletes.Load())
	}
}

func TestTUICloudflareRejectsStaleObservationBeforeMutation(t *testing.T) {
	t.Parallel()
	zone := testTUICloudflareZone()
	record := testTUICloudflareRecord()
	var mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method != http.MethodGet {
			mutations.Add(1)
		}
		switch request.URL.Path {
		case "/api/cloudflare/zones/zone-1":
			changed := zone
			changed.Status = "pending"
			_ = json.NewEncoder(writer).Encode(changed)
		case "/api/cloudflare/zones/zone-1/records":
			changed := record
			changed.Content = "192.0.2.99"
			_ = json.NewEncoder(writer).Encode([]cliCloudflareRecord{changed})
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	local := initialTUITargets()[0]
	if _, err := runTUICloudflareOperation(context.Background(), client, tuiOperation{Target: local, CloudflareResource: tuiCloudflareResourceZone, CloudflareZone: zone, Action: "purge"}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("zone err=%v", err)
	}
	if mutations.Load() != 0 {
		t.Fatalf("stale zone sent %d mutation(s)", mutations.Load())
	}

	// Let the zone pass so the stale-record boundary is exercised independently.
	server.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method != http.MethodGet {
			mutations.Add(1)
		}
		switch request.URL.Path {
		case "/api/cloudflare/zones/zone-1":
			_ = json.NewEncoder(writer).Encode(zone)
		case "/api/cloudflare/zones/zone-1/records":
			changed := record
			changed.Content = "192.0.2.99"
			_ = json.NewEncoder(writer).Encode([]cliCloudflareRecord{changed})
		default:
			http.NotFound(writer, request)
		}
	})
	if _, err := runTUICloudflareOperation(context.Background(), client, tuiOperation{Target: local, CloudflareResource: tuiCloudflareResourceRecord, CloudflareZone: zone, CloudflareRecord: record, Action: "delete"}); err == nil || !strings.Contains(err.Error(), "record changed") {
		t.Fatalf("record err=%v", err)
	}
	if mutations.Load() != 0 {
		t.Fatalf("stale observations sent %d mutation(s)", mutations.Load())
	}
}

func TestTUICloudflareNavigationRecordViewerAndPalette(t *testing.T) {
	t.Parallel()
	zone := testTUICloudflareZone()
	record := testTUICloudflareRecord()
	model := newTUIModel(context.Background(), nil, "http://127.0.0.1", 5*time.Second)
	model.loading = false
	model.snapshot.Selected = model.snapshot.Targets[0]
	model.cloudflare = tuiCloudflareState{Local: true, Supported: true, Status: "healthy", Zones: []cfservice.CFZone{zone}, Detail: &tuiCloudflareDetail{Zone: zone, Records: []cliCloudflareRecord{record}}}
	model.cloudflareTarget, model.cloudflareLoaded = localTargetID, true
	updated, command := model.updateKey("O")
	model = updated.(tuiModel)
	if command != nil || model.tab != tuiTabCloudflare {
		t.Fatal("O shortcut did not open Cloudflare")
	}
	model.cursor = 1
	updated, command = model.updateKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogChoices || len(model.dialog.Options) != 3 {
		t.Fatalf("record choices=%#v", model.dialog)
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogLogs || !strings.Contains(strings.Join(model.dialog.LogLines, "\n"), "192.0.2.10") {
		t.Fatalf("record viewer=%#v", model.dialog)
	}
	model.dialog = tuiDialog{}
	updated, command = model.updateKey("backspace")
	model = updated.(tuiModel)
	if command != nil || model.cloudflare.Detail != nil || model.cursor != 0 {
		t.Fatalf("backspace did not return to zones: detail=%#v cursor=%d", model.cloudflare.Detail, model.cursor)
	}

	model.cloudflare.Detail = &tuiCloudflareDetail{Zone: zone, Records: []cliCloudflareRecord{record}}
	foundSection, foundPurge, foundProxy := false, false, false
	for _, item := range model.buildPaletteItems() {
		foundSection = foundSection || item.Kind == tuiPaletteNavigate && item.Tab == tuiTabCloudflare
		foundPurge = foundPurge || item.Kind == tuiPaletteOperation && item.Operation.Kind == tuiOperationCloudflare && item.Operation.Action == "purge"
		foundProxy = foundProxy || item.Kind == tuiPaletteOperation && item.Operation.Kind == tuiOperationCloudflare && item.Operation.Action == "toggle-proxy"
	}
	if !foundSection || !foundPurge || !foundProxy {
		t.Fatalf("section=%t purge=%t proxy=%t", foundSection, foundPurge, foundProxy)
	}
}

func testTUICloudflareZone() cfservice.CFZone {
	return cfservice.CFZone{ID: "zone-1", Name: "example.com", Status: "active", Plan: cfservice.CFPlan{ID: "free", Name: "Free"}, NS: []string{"ns1.example.net", "ns2.example.net"}}
}

func testTUICloudflareRecord() cliCloudflareRecord {
	return cliCloudflareRecord{ID: "record-1", Type: "A", Name: "www.example.com", Content: "192.0.2.10", TTL: 300, Proxied: true}
}

func assertCloudflareNoBody(t *testing.T, request *http.Request) {
	t.Helper()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Errorf("request body = %q, want empty", body)
	}
}
