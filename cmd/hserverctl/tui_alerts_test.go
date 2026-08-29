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

	"github.com/IamYGT/heyserver/internal/models"
)

func TestLoadTUIAlertsPreservesPartialInventoryAndLocalBoundary(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/notify/channels":
			_, _ = writer.Write([]byte(`[]`))
		case "/api/notify/rules":
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"error":"rules unavailable"}`))
		case "/api/notify/history":
			if request.URL.RawQuery != "limit=100&offset=0" {
				t.Errorf("history query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"items":[],"total":0,"limit":100,"offset":0}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	state, err := loadTUIAlerts(context.Background(), client, initialTUITargets()[0])
	if err != nil || !state.Supported || state.Status != "not_configured" || !state.ChannelsAvailable || state.RulesAvailable || !state.HistoryAvailable || len(state.Warnings) != 1 || !strings.Contains(state.Warnings[0], "rules unavailable") {
		t.Fatalf("state=%#v err=%v", state, err)
	}
	before := requests.Load()
	managed, err := loadTUIAlerts(context.Background(), client, tuiTarget{ID: "edge-1", Online: true})
	if err != nil || managed.Supported || managed.Local || !strings.Contains(managed.Message, "central panel") || requests.Load() != before {
		t.Fatalf("managed=%#v err=%v requests=%d", managed, err, requests.Load())
	}
}

func TestTUIAlertsChannelMutationUsesChoicesConfirmationAndFreshObservation(t *testing.T) {
	t.Parallel()
	channel := cliNotificationChannel{ID: 1, Name: "Ops webhook", Type: models.ChannelDiscord, Config: `{"webhook":"__redacted__"}`, Enabled: true}
	var puts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/notify/channels/1":
			_ = json.NewEncoder(writer).Encode(channel)
		case "PUT /api/notify/channels/1":
			puts.Add(1)
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["name"] != channel.Name || payload["enabled"] != false || len(payload) != 2 {
				t.Errorf("payload=%#v", payload)
			}
			updated := channel
			updated.Enabled = false
			_ = json.NewEncoder(writer).Encode(updated)
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
	model.alerts = tuiAlertsState{Local: true, Supported: true, Status: "healthy", Channels: []cliNotificationChannel{channel}}
	model.alertsTarget, model.alertsLoaded = localTargetID, true

	updatedModel, command := model.updateKey("L")
	model = updatedModel.(tuiModel)
	if command != nil || model.tab != tuiTabAlerts {
		t.Fatal("L did not open the already loaded Alerts section")
	}
	updatedModel, command = model.updateKey("enter")
	model = updatedModel.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogChoices || len(model.dialog.Options) != 3 || puts.Load() != 0 {
		t.Fatalf("channel choices=%#v puts=%d", model.dialog, puts.Load())
	}
	model.dialog.Cursor = 1
	updatedModel, command = model.updateDialogKey("enter")
	model = updatedModel.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || puts.Load() != 0 {
		t.Fatal("channel choice bypassed separate confirmation")
	}
	updatedModel, command = model.updateDialogKey("enter")
	model = updatedModel.(tuiModel)
	if command != nil || puts.Load() != 0 {
		t.Fatal("Enter bypassed alert confirmation")
	}
	updatedModel, command = model.updateDialogKey("y")
	model = updatedModel.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("Y did not start alert channel mutation")
	}
	message := command().(tuiOperationMsg)
	if message.Err != nil || puts.Load() != 1 || !strings.Contains(message.Message, "disabled") {
		t.Fatalf("message=%#v puts=%d", message, puts.Load())
	}
}

func TestTUIAlertsChannelTestAndRuleActionsValidateReceipts(t *testing.T) {
	t.Parallel()
	channel := cliNotificationChannel{ID: 2, Name: "Primary email", Type: models.ChannelEmail, Config: `{"password":"__redacted__"}`, Enabled: true}
	now := time.Date(2026, 8, 28, 1, 0, 0, 0, time.UTC)
	rule := models.AlertRule{ID: 4, Name: "CPU pressure", Type: models.AlertCPUUsage, Threshold: 90, DurationMins: 5, Enabled: true, CooldownMins: 30, CreatedAt: now.Add(-time.Hour), UpdatedAt: now}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/notify/channels/2":
			_ = json.NewEncoder(writer).Encode(channel)
		case "POST /api/notify/channels/2/test":
			assertAlertsNoBody(t, request)
			_, _ = writer.Write([]byte(`{"status":"sent"}`))
		case "GET /api/notify/rules/4":
			_ = json.NewEncoder(writer).Encode(rule)
		case "PUT /api/notify/rules/4":
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			if payload["enabled"] != false || len(payload) != 1 {
				t.Errorf("payload=%#v", payload)
			}
			updated := rule
			updated.Enabled = false
			updated.UpdatedAt = now.Add(time.Minute)
			_ = json.NewEncoder(writer).Encode(updated)
		case "DELETE /api/notify/rules/4":
			assertAlertsNoBody(t, request)
			_, _ = writer.Write([]byte(`{"status":"deleted"}`))
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
	message, err := runTUIAlertOperation(context.Background(), client, tuiOperation{Target: local, AlertResource: tuiAlertResourceChannel, AlertChannel: channel, Action: "test"})
	if err != nil || !strings.Contains(message, "sent") {
		t.Fatalf("channel test=%q err=%v", message, err)
	}
	message, err = runTUIAlertOperation(context.Background(), client, tuiOperation{Target: local, AlertResource: tuiAlertResourceRule, AlertRule: rule, Action: "disable"})
	if err != nil || !strings.Contains(message, "disabled") {
		t.Fatalf("rule disable=%q err=%v", message, err)
	}
	message, err = runTUIAlertOperation(context.Background(), client, tuiOperation{Target: local, AlertResource: tuiAlertResourceRule, AlertRule: rule, Action: "delete"})
	if err != nil || !strings.Contains(message, "Deleted") {
		t.Fatalf("rule delete=%q err=%v", message, err)
	}
}

func TestTUIAlertsRejectsStaleObservationBeforeMutation(t *testing.T) {
	t.Parallel()
	var mutations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method != http.MethodGet {
			mutations.Add(1)
		}
		switch request.URL.Path {
		case "/api/notify/channels/1":
			_, _ = writer.Write([]byte(`{"id":1,"name":"Changed","type":"slack","config":"{}","enabled":true}`))
		case "/api/notify/rules/4":
			_, _ = writer.Write([]byte(`{"id":4,"name":"Changed","type":"cpu_usage","threshold":95,"enabled":true,"cooldownMins":30}`))
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
	channel := cliNotificationChannel{ID: 1, Name: "Ops", Type: models.ChannelSlack, Config: `{}`, Enabled: true}
	if _, err := runTUIAlertOperation(context.Background(), client, tuiOperation{Target: local, AlertResource: tuiAlertResourceChannel, AlertChannel: channel, Action: "disable"}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("channel err=%v", err)
	}
	rule := models.AlertRule{ID: 4, Name: "CPU", Type: models.AlertCPUUsage, Threshold: 90, Enabled: true, CooldownMins: 30}
	if _, err := runTUIAlertOperation(context.Background(), client, tuiOperation{Target: local, AlertResource: tuiAlertResourceRule, AlertRule: rule, Action: "delete"}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("rule err=%v", err)
	}
	if mutations.Load() != 0 {
		t.Fatalf("stale observations sent %d mutation(s)", mutations.Load())
	}
}

func TestTUIAlertsEventShortcutAndPalette(t *testing.T) {
	t.Parallel()
	channel := cliNotificationChannel{ID: 1, Name: "Ops", Type: models.ChannelTelegram, Enabled: true}
	rule := models.AlertRule{ID: 4, Name: "CPU", Type: models.AlertCPUUsage, Threshold: 90, Enabled: true, CooldownMins: 30}
	event := models.AlertHistory{ID: 8, RuleID: 4, RuleName: "CPU", Type: models.AlertCPUUsage, Message: "CPU reached 94%", Value: 94}
	model := newTUIModel(context.Background(), nil, "http://127.0.0.1", 5*time.Second)
	model.loading = false
	model.snapshot.Selected = model.snapshot.Targets[0]
	model.alerts = tuiAlertsState{Local: true, Supported: true, Status: "healthy", Channels: []cliNotificationChannel{channel}, Rules: []models.AlertRule{rule}, History: []models.AlertHistory{event}}
	model.alertsTarget, model.alertsLoaded = localTargetID, true
	updatedModel, command := model.updateKey("L")
	model = updatedModel.(tuiModel)
	if command != nil || model.tab != tuiTabAlerts {
		t.Fatal("L shortcut did not open Alerts")
	}
	model.cursor = 2
	updatedModel, command = model.updateKey("enter")
	model = updatedModel.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogLogs || model.dialog.LogReloadNotice == "" || !strings.Contains(strings.Join(model.dialog.LogLines, "\n"), "CPU reached 94%") {
		t.Fatalf("event dialog=%#v", model.dialog)
	}
	foundSection, foundTest, foundRule := false, false, false
	for _, item := range model.buildPaletteItems() {
		foundSection = foundSection || item.Kind == tuiPaletteNavigate && item.Tab == tuiTabAlerts
		foundTest = foundTest || item.Kind == tuiPaletteOperation && item.Operation.Kind == tuiOperationAlert && item.Operation.AlertResource == tuiAlertResourceChannel && item.Operation.Action == "test"
		foundRule = foundRule || item.Kind == tuiPaletteOperation && item.Operation.Kind == tuiOperationAlert && item.Operation.AlertResource == tuiAlertResourceRule && item.Operation.Action == "disable"
	}
	if !foundSection || !foundTest || !foundRule {
		t.Fatalf("section=%t test=%t rule=%t", foundSection, foundTest, foundRule)
	}
}

func assertAlertsNoBody(t *testing.T, request *http.Request) {
	t.Helper()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Errorf("request body = %q, want empty", body)
	}
}
