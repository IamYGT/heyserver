package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

func TestTUIQuickActionsFilterAndRequireExplicitConfirmation(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/api/system/actions/swap-reset" {
			t.Errorf("unexpected request: %s %s", request.Method, request.URL.Path)
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]string{"message": "swap reset completed"})
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	model := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
	model.loading = false
	model.snapshot.Selected = model.snapshot.Targets[0]

	updated, command := model.updateKey("ctrl+k")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogPalette || len(model.dialog.PaletteItems) == 0 {
		t.Fatalf("palette = %#v, command=%v", model.dialog, command != nil)
	}
	for _, key := range []string{"s", "w", "a", "p"} {
		updated, command = model.updateDialogKey(key)
		model = updated.(tuiModel)
		if command != nil {
			t.Fatalf("typing %q returned a command", key)
		}
	}
	filtered := filteredPaletteItems(model.dialog.PaletteItems, model.dialog.PaletteQuery)
	if len(filtered) != 1 || filtered[0].Operation.Action != "swap-reset" {
		t.Fatalf("filtered items = %#v", filtered)
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || model.dialog.Operation.Action != "swap-reset" || requests.Load() != 0 {
		t.Fatalf("confirmation = %#v, command=%v, requests=%d", model.dialog, command != nil, requests.Load())
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || requests.Load() != 0 {
		t.Fatalf("enter confirmed quick action: command=%v requests=%d", command != nil, requests.Load())
	}
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("explicit y did not start the quick action")
	}
	message := command()
	result, ok := message.(tuiOperationMsg)
	if !ok || result.Err != nil || result.Message != "swap reset completed" || requests.Load() != 1 {
		t.Fatalf("operation result = %#v, requests=%d", message, requests.Load())
	}
}

func TestTUIQuickActionsNavigateAndRespectTargetCapabilities(t *testing.T) {
	t.Parallel()
	targets := initialTUITargets()
	targets = append(targets, tuiTarget{ID: "offline", Name: "Offline node", Online: false, Capabilities: map[string]bool{
		agenthub.CapabilityHostAction: true, agenthub.CapabilityNginxAction: true,
	}})
	model := newTUIModel(context.Background(), nil, "http://127.0.0.1:3085", 5*time.Second)
	model.snapshot.Targets = targets
	model.snapshot.Selected = targets[1]
	model.selectedTargetID = targets[1].ID
	items := model.buildPaletteItems()
	for _, item := range items {
		if item.Kind == tuiPaletteOperation {
			t.Fatalf("offline target exposed operation %q", item.Label)
		}
	}
	if len(filteredPaletteItems(items, "switch offline")) != 1 {
		t.Fatalf("server filter = %#v", filteredPaletteItems(items, "switch offline"))
	}

	model.snapshot.Selected = targets[0]
	model.selectedTargetID = localTargetID
	model.openPalette()
	model.dialog.PaletteQuery = "open web ops"
	updated, command := model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if model.dialog.Mode != tuiDialogNone || model.tab != tuiTabWeb || command == nil {
		t.Fatalf("navigation: tab=%v dialog=%v command=%v", model.tab, model.dialog.Mode, command != nil)
	}

	model.webLoaded = true
	model.webTarget = localTargetID
	model.webResources = []tuiWebResource{
		{Kind: tuiWebDomain, ID: "example.com", Name: "example.com", Enabled: true},
		{Kind: tuiWebSSL, ID: "example.com", Name: "example.com", State: "valid"},
	}
	labels := make([]string, 0)
	for _, item := range model.buildPaletteItems() {
		if item.Kind == tuiPaletteOperation {
			labels = append(labels, item.Label)
		}
	}
	joined := strings.Join(labels, "|")
	for _, expected := range []string{"Disable domain example.com", "Renew certificate example.com", "Reset swap", "Reload Nginx"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("palette operations %q do not contain %q", joined, expected)
		}
	}
}
