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

	mailsvc "github.com/IamYGT/heyserver/internal/services/mail"
)

func TestLoadTUIMailReadsBoundedOperationalInventory(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", request.Method)
		}
		if request.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/mail/service/status":
			_, _ = io.WriteString(writer, `{"running":true,"status":"running","pid":"1234","uptime":"today","secret":"must-not-print"}`)
		case "/api/mail/service/overview":
			_, _ = io.WriteString(writer, `{"status":{"running":true,"status":"running","pid":"1234"},"version":{"raw":"Stalwart Mail Server v0.15.5","version":"0.15.5"},"listeners":[{"id":"smtp","protocol":"smtp","bind":"0.0.0.0","port":25,"tls":false}],"storage":{"backend":"rocksdb","path":"/var/lib/mail","sizeBytes":42},"sources":{"status":{"available":true,"state":"healthy"},"version":{"available":true,"state":"healthy"},"listeners":{"available":true,"state":"healthy"},"storage":{"available":true,"state":"healthy"}},"secret":"must-not-print"}`)
		case "/api/mail/logs":
			if request.URL.Query().Get("lines") != "50" {
				t.Errorf("lines = %q, want 50", request.URL.Query().Get("lines"))
			}
			entries := make([]mailsvc.LogEntry, 55)
			for index := range entries {
				entries[index] = mailsvc.LogEntry{Timestamp: time.Date(2026, 8, 29, 0, index, 0, 0, time.UTC).Format(time.RFC3339), Level: "INFO", Message: "delivered"}
			}
			_ = json.NewEncoder(writer).Encode(mailLogResponse{Lines: 55, Count: len(entries), Entries: entries})
		case "/api/mail/queue":
			if request.URL.Query().Get("limit") != "100" {
				t.Errorf("limit = %q, want 100", request.URL.Query().Get("limit"))
			}
			_ = json.NewEncoder(writer).Encode([]mailsvc.QueueMessage{{ID: "queue-1", Sender: "alice@example.com", Recipients: []string{"bob@example.com"}, CreatedAt: time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC), Retries: 1}})
		case "/api/mail/domains":
			_, _ = io.WriteString(writer, `[{"name":"example.com","description":"1 accounts","secret":"must-not-print"}]`)
		case "/api/mail/accounts":
			_, _ = io.WriteString(writer, `[{"email":"alice@example.com","name":"Alice","domain":"example.com","quota":1048576,"usedStorage":42,"isEnabled":true,"aliases":["ops@example.com"],"password":"must-not-print","secret":"must-not-print"}]`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadTUIMail(context.Background(), client, initialTUITargets()[0])
	if err != nil {
		t.Fatal(err)
	}
	if !state.Local || !state.Supported || state.Status != "healthy" {
		t.Fatalf("state status = %#v, want local supported healthy", state)
	}
	if !state.StatusAvailable || !state.OverviewAvailable || !state.LogsAvailable || !state.QueueAvailable || !state.DomainsAvailable || !state.AccountsAvailable {
		t.Fatalf("availability = %#v", state)
	}
	if len(state.Logs) != tuiMailRecentLogLimit || state.Logs[0].Message != "delivered" {
		t.Fatalf("logs = %d first=%#v", len(state.Logs), state.Logs[0])
	}
	if len(state.Queue) != 1 || state.Queue[0].ID != "queue-1" || len(state.Domains) != 1 || len(state.Accounts) != 1 {
		t.Fatalf("inventory = queue=%#v domains=%#v accounts=%#v", state.Queue, state.Domains, state.Accounts)
	}
	if requests.Load() != 6 {
		t.Fatalf("requests = %d, want 6 canonical mail endpoints", requests.Load())
	}

	model := tuiModel{
		tab: tuiTabMail, width: 120, height: 36, selectedTargetID: localTargetID,
		snapshot: tuiSnapshot{Selected: initialTUITargets()[0]}, mail: state, mailTarget: localTargetID, mailLoaded: true,
	}
	view := model.renderMail(116, 30)
	for _, expected := range []string{"Mail", "HEALTHY", "RUNNING", "0.15.5", "delivered", "queue-1", "example.com", "alice@example.com"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("mail view missing %q:\n%s", expected, view)
		}
	}
	if strings.Contains(view, "must-not-print") || strings.Contains(view, "password") || strings.Contains(view, "secret") {
		t.Fatalf("mail view leaked provider fields:\n%s", view)
	}
}

func TestLoadTUIMailPreservesNotConfiguredAndUnavailableStates(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		status string
		body   string
		want   string
	}{
		{name: "not configured", status: "not_configured", body: `{"running":false,"status":"not_configured"}`, want: "not_configured"},
		{name: "unavailable", status: "unavailable", body: `{"error":"upstream password=hunter2","secret":"body-secret","body":"raw-provider-body"}`, want: "unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				if test.status == "not_configured" {
					switch request.URL.Path {
					case "/api/mail/service/status":
						_, _ = io.WriteString(writer, test.body)
					case "/api/mail/service/overview":
						_, _ = io.WriteString(writer, `{"status":{"running":false,"status":"not_configured"},"sources":{"status":{"available":false,"state":"not_configured","error":"mail service is not configured"}}}`)
					default:
						writer.WriteHeader(http.StatusServiceUnavailable)
						_, _ = io.WriteString(writer, `{"error":"mail integration not configured password=hunter2","secret":"body-secret"}`)
					}
					return
				}
				writer.WriteHeader(http.StatusBadGateway)
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()
			client, err := newAPIClient(server.URL, "test-token", time.Second)
			if err != nil {
				t.Fatal(err)
			}
			state, err := loadTUIMail(context.Background(), client, initialTUITargets()[0])
			if err != nil {
				t.Fatal(err)
			}
			if state.Status != test.want {
				t.Fatalf("state status = %q, want %q; state=%#v", state.Status, test.want, state)
			}
			model := tuiModel{
				tab: tuiTabMail, width: 120, height: 30, selectedTargetID: localTargetID,
				snapshot: tuiSnapshot{Selected: initialTUITargets()[0]}, mail: state, mailTarget: localTargetID, mailLoaded: true,
			}
			view := model.renderMail(116, 24)
			if !strings.Contains(view, strings.ToUpper(test.want)) {
				t.Fatalf("view missing state %q:\n%s", test.want, view)
			}
			for _, forbidden := range []string{"hunter2", "body-secret", "raw-provider-body"} {
				if strings.Contains(view, forbidden) {
					t.Fatalf("view leaked %q:\n%s", forbidden, view)
				}
			}
		})
	}

	var remoteRequests atomic.Int32
	remoteServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { remoteRequests.Add(1) }))
	defer remoteServer.Close()
	remoteClient, err := newAPIClient(remoteServer.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	remote, err := loadTUIMail(context.Background(), remoteClient, tuiTarget{ID: "edge-1", Online: true})
	if err != nil || remote.Local || remote.Supported || remote.Status != "unsupported" || remoteRequests.Load() != 0 {
		t.Fatalf("remote state=%#v err=%v requests=%d", remote, err, remoteRequests.Load())
	}
}

func TestTUIMailDeliveryIsBoundedAndUsesTheCanonicalEndpoint(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path != "/api/mail/logs/delivery" || request.URL.Query().Get("email") != "alice@example.com" {
			t.Errorf("request = %s %s", request.Method, request.URL.RequestURI())
		}
		entries := make([]mailsvc.LogEntry, 55)
		for index := range entries {
			entries[index] = mailsvc.LogEntry{Timestamp: "2026-08-29T00:00:00Z", Level: "INFO", Message: "password=hunter2 delivered\u001b[31m"}
		}
		_ = json.NewEncoder(writer).Encode(mailDeliveryLogResponse{Email: "alice@example.com", Count: len(entries), Entries: entries})
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := loadTUIMailDelivery(context.Background(), client, initialTUITargets()[0], "alice@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != tuiMailDeliveryLogLimit || strings.Contains(entries[0].Message, "hunter2") || strings.Contains(entries[0].Message, "\x1b") {
		t.Fatalf("delivery entries = %#v", entries[0])
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d, want 1", requests.Load())
	}

	model := tuiModel{
		ctx: context.Background(), tab: tuiTabMail, width: 100, height: 30, selectedTargetID: localTargetID,
		client: client, snapshot: tuiSnapshot{Selected: initialTUITargets()[0]},
		mail:       tuiMailState{Local: true, Supported: true, Status: "healthy", Accounts: []mailsvc.MailAccount{{Email: "alice@example.com", IsEnabled: true}}, AccountsAvailable: true},
		mailTarget: localTargetID, mailLoaded: true,
	}
	updated, command := model.updateKey("enter")
	model = updated.(tuiModel)
	if command == nil || !model.resourceLoading {
		t.Fatalf("account activation did not load delivery history: command=%v loading=%v", command != nil, model.resourceLoading)
	}
	message := command()
	updated, followup := model.Update(message)
	model = updated.(tuiModel)
	if followup != nil || model.dialog.Mode != tuiDialogLogs {
		t.Fatalf("delivery history dialog = %#v followup=%v", model.dialog, followup != nil)
	}
	if strings.Contains(strings.Join(model.dialog.LogLines, "\n"), "hunter2") || strings.Contains(strings.Join(model.dialog.LogLines, "\n"), "\x1b") {
		t.Fatalf("delivery dialog leaked provider fields: %#v", model.dialog.LogLines[0])
	}
}

func TestTUIMailQueueMutationUsesSeparateExplicitConfirmation(t *testing.T) {
	t.Parallel()
	message := mailsvc.QueueMessage{ID: "queue-1", Sender: "alice@example.com", Recipients: []string{"bob@example.com"}, CreatedAt: time.Date(2026, 8, 29, 0, 0, 0, 0, time.UTC), Retries: 1}
	var reads atomic.Int32
	var mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/mail/queue":
			if request.URL.Query().Get("limit") != "1000" {
				t.Errorf("fresh queue limit = %q, want 1000", request.URL.Query().Get("limit"))
			}
			reads.Add(1)
			_ = json.NewEncoder(writer).Encode([]mailsvc.QueueMessage{message})
		case "POST /api/mail/queue/queue-1/retry":
			mutations.Add(1)
			_, _ = io.WriteString(writer, `{"status":"retrying","secret":"must-not-print"}`)
		case "DELETE /api/mail/queue/queue-1":
			mutations.Add(1)
			_, _ = io.WriteString(writer, `{"status":"deleted","secret":"must-not-print"}`)
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
	model.tab = tuiTabMail
	model.snapshot.Selected = initialTUITargets()[0]
	model.mail = tuiMailState{Local: true, Supported: true, Status: "healthy", Queue: []mailsvc.QueueMessage{message}, QueueAvailable: true}
	model.mailTarget, model.mailLoaded = localTargetID, true

	updated, command := model.updateKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogChoices || len(model.dialog.Options) != 2 || reads.Load() != 0 || mutations.Load() != 0 {
		t.Fatalf("queue choices = %#v reads=%d mutations=%d", model.dialog, reads.Load(), mutations.Load())
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || reads.Load() != 0 || mutations.Load() != 0 {
		t.Fatalf("queue choice bypassed confirmation: %#v", model.dialog)
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || reads.Load() != 0 || mutations.Load() != 0 {
		t.Fatal("enter bypassed explicit Y confirmation")
	}
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating || reads.Load() != 0 || mutations.Load() != 0 {
		t.Fatalf("Y did not start queued mutation: command=%v operating=%v reads=%d mutations=%d", command != nil, model.operating, reads.Load(), mutations.Load())
	}
	result := command().(tuiOperationMsg)
	if result.Err != nil || !strings.Contains(result.Message, "Retried") || reads.Load() != 1 || mutations.Load() != 1 {
		t.Fatalf("queue mutation result=%#v reads=%d mutations=%d", result, reads.Load(), mutations.Load())
	}
	if strings.Contains(result.Message, "must-not-print") {
		t.Fatalf("queue mutation receipt leaked provider field: %q", result.Message)
	}
}
