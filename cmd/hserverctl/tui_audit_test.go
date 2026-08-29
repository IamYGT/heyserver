package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestLoadTUIAuditUsesSelectedTargetScope(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/audit" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if request.URL.Query().Get("server") != "edge-1" || request.URL.Query().Get("limit") != "100" || request.URL.Query().Get("offset") != "0" {
			t.Errorf("query = %q", request.URL.RawQuery)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"data":[
				{"id":2,"userId":1,"userName":"Operator","action":"remote_system_action","resource":"system","details":"server=edge-1 action=swap-reset status=completed","ip":"192.0.2.10","createdAt":"2026-08-27T12:00:00Z"},
				{"id":1,"userId":1,"userName":"Operator","action":"remote_system_action","resource":"system","details":"server=edge-1 action=reboot status=failed","ip":"192.0.2.10","createdAt":"2026-08-27T11:00:00Z"}
			],"total":2,"limit":100,"offset":0
		}`))
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadTUIAudit(context.Background(), client, tuiTarget{ID: "edge-1", Name: "Edge One"})
	if err != nil {
		t.Fatal(err)
	}
	if state.Total != 2 || len(state.Entries) != 2 || state.Entries[0].Action != "remote_system_action" {
		t.Fatalf("audit state = %#v", state)
	}
	filtered := filteredTUIAuditEntries(state.Entries, "reboot failed")
	if len(filtered) != 1 || !strings.Contains(filtered[0].Details, "reboot") {
		t.Fatalf("filtered = %#v", filtered)
	}
}

func TestTUIAuditJumpLoadAndLocalFilter(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/audit" || request.URL.Query().Get("server") != "local" {
			t.Errorf("request = %s", request.RequestURI)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":[
			{"id":2,"userName":"Operator","action":"swap_reset","resource":"system","details":"status=completed","ip":"127.0.0.1","createdAt":"2026-08-27T12:00:00Z"},
			{"id":1,"userName":"Operator","action":"temp_clean","resource":"system","details":"status=completed","ip":"127.0.0.1","createdAt":"2026-08-27T11:00:00Z"}
		],"total":2,"limit":100,"offset":0}`))
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	model := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
	model.loading = false

	updated, command := model.updateKey("A")
	model = updated.(tuiModel)
	if model.tab != tuiTabAudit || command == nil || !model.resourceLoading {
		t.Fatalf("audit jump tab=%v command=%v loading=%v", model.tab, command != nil, model.resourceLoading)
	}
	message := command()
	updated, _ = model.Update(message)
	model = updated.(tuiModel)
	if !model.auditLoaded || len(model.audit.Entries) != 2 || model.currentItemCount() != 2 {
		t.Fatalf("loaded model = %#v", model.audit)
	}

	updated, _ = model.updateKey("/")
	model = updated.(tuiModel)
	if model.dialog.Mode != tuiDialogAuditFilter {
		t.Fatalf("dialog = %#v", model.dialog)
	}
	for _, key := range []string{"s", "w", "a", "p", "enter"} {
		updated, _ = model.updateDialogKey(key)
		model = updated.(tuiModel)
	}
	if model.auditFilter != "swap" || model.currentItemCount() != 1 {
		t.Fatalf("filter=%q count=%d", model.auditFilter, model.currentItemCount())
	}
	view := model.renderAudit(120, 20)
	if !strings.Contains(view, "swap_reset") || strings.Contains(view, "temp_clean") || !strings.Contains(view, "1 matching") {
		t.Fatalf("filtered audit view:\n%s", view)
	}
}

func TestLoadTUIAuditRejectsInvalidPaginationMetadata(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"data":[{"id":1}],"total":0,"limit":100,"offset":0}`))
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, err = loadTUIAudit(context.Background(), client, initialTUITargets()[0])
	if err == nil || !strings.Contains(err.Error(), "invalid pagination") {
		t.Fatalf("error = %v", err)
	}
}
