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

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func TestLoadTUISecurityAccessListsKeepsBothLocalListsAndSkipsManagedTargets(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/security/ip-blacklist":
			_, _ = io.WriteString(writer, `[{"id":7,"ip":"198.51.100.0/24","listType":"blacklist","comment":"blocked range","createdAt":"2026-08-29T00:00:00Z","expiresAt":"2026-08-30T00:00:00Z"}]`)
		case "GET /api/security/ip-whitelist":
			_, _ = io.WriteString(writer, `[{"id":8,"ip":"2001:db8::/64","listType":"whitelist","comment":"office","createdAt":"2026-08-29T01:00:00Z"}]`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	local := loadTUISecurityAccessLists(context.Background(), client, initialTUITargets()[0])
	if !local.Supported || !local.BlacklistLoaded || !local.WhitelistLoaded || len(local.Blacklist) != 1 || len(local.Whitelist) != 1 {
		t.Fatalf("local state = %#v", local)
	}
	if local.Blacklist[0].IP != "198.51.100.0/24" || local.Whitelist[0].Comment != "office" {
		t.Fatalf("local entries = %#v", local)
	}
	before := requests.Load()
	managed := loadTUISecurityAccessLists(context.Background(), client, tuiTarget{ID: "edge-1", Name: "Edge", Online: true})
	if managed.Supported || managed.UnsupportedNote == "" || len(managed.Blacklist) != 0 || len(managed.Whitelist) != 0 {
		t.Fatalf("managed state = %#v", managed)
	}
	if requests.Load() != before {
		t.Fatal("managed access-list load made a panel-local request")
	}
}

func TestLoadTUISecurityAccessListsKeepsPartialSuccessAndSafeErrors(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/security/ip-blacklist":
			http.Error(writer, "raw-backend-secret", http.StatusServiceUnavailable)
		case "/api/security/ip-whitelist":
			_, _ = io.WriteString(writer, `[]`)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	state := loadTUISecurityAccessLists(context.Background(), client, initialTUITargets()[0])
	if state.BlacklistLoaded || !state.WhitelistLoaded || len(state.Warnings) != 1 {
		t.Fatalf("partial state = %#v", state)
	}
	if !strings.Contains(state.Warnings[0], "IP blacklist unavailable") || strings.Contains(state.Warnings[0], "raw-backend-secret") {
		t.Fatalf("unsafe warning = %q", state.Warnings[0])
	}
}

func TestProjectAndRenderTUISecurityAccessEntriesAreTerminalSafeAndBounded(t *testing.T) {
	t.Parallel()
	entries := make([]cliSecurityIPEntry, 0, 5)
	for index := 0; index < 5; index++ {
		entries = append(entries, cliSecurityIPEntry{
			ID:       int64(index + 1),
			IP:       "198.51.100.20",
			Comment:  "office\n\x1b[31m" + strings.Repeat("x", 80),
			ListType: "blacklist",
		})
	}
	projected := projectTUISecurityAccessEntries(entries, 2)
	if len(projected) != 2 {
		t.Fatalf("projected rows = %d, want 2", len(projected))
	}
	if strings.ContainsAny(projected[0].Comment, "\r\n\x1b") || len([]rune(projected[0].Comment)) > tuiSecurityAccessMaxCommentRunes {
		t.Fatalf("unsafe projected comment = %q", projected[0].Comment)
	}
	rows := renderTUISecurityAccessListRows(entries, 44, 2)
	if len(rows) != 2 {
		t.Fatalf("rendered rows = %d, want 2 including omission marker", len(rows))
	}
	for _, row := range rows {
		if strings.ContainsAny(row, "\r\n\x1b") || lipgloss.Width(row) > 44 {
			t.Fatalf("unsafe/broad rendered row = %q width=%d", row, lipgloss.Width(row))
		}
	}
	if !strings.Contains(rows[1], "additional") {
		t.Fatalf("omission marker = %q", rows[1])
	}
	if got := renderTUISecurityAccessListRows(entries, 80, 1); len(got) != 1 || !strings.Contains(got[0], "additional") {
		t.Fatalf("one-row rendering = %#v", got)
	}
}

func TestValidateTUISecurityAccessIPOrCIDRMatchesCanonicalStrictForms(t *testing.T) {
	t.Parallel()
	valid := []string{"198.51.100.20", "198.51.100.0/24", "2001:db8::1", "2001:db8::/64"}
	for _, value := range valid {
		if got, err := validateTUISecurityAccessIPOrCIDR(value); err != nil || got != value {
			t.Errorf("valid %q: got %q err=%v", value, got, err)
		}
	}
	invalid := []string{"", "not-an-ip", "198.51.100.1/33", "2001:db8::1/129", "fe80::1%eth0", "198.51.100.1/255.255.255.0"}
	for _, value := range invalid {
		if _, err := validateTUISecurityAccessIPOrCIDR(value); err == nil {
			t.Errorf("invalid %q unexpectedly succeeded", value)
		}
	}
	if _, err := validateTUISecurityAccessIPOrCIDR("\x1b[31mnot-an-ip"); err == nil || strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("control-bearing IP error = %v", err)
	}
}

func TestRunTUISecurityAccessListOperationRequiresConfirmationAndUsesLocalAPI(t *testing.T) {
	t.Parallel()
	var addRequests, deleteRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "POST /api/security/ip-whitelist":
			addRequests.Add(1)
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode add payload: %v", err)
			}
			if payload["ip"] != "2001:db8::/64" || payload["comment"] != "office" || payload["expiresInMinutes"] != float64(60) {
				t.Errorf("add payload = %#v", payload)
			}
			_, _ = io.WriteString(writer, `{"id":9,"ip":"2001:db8::/64","listType":"whitelist","comment":"office","createdAt":"2026-08-29T01:00:00Z"}`)
		case "DELETE /api/security/ip-blacklist/198.51.100.0/24":
			deleteRequests.Add(1)
			if request.URL.EscapedPath() != "/api/security/ip-blacklist/198.51.100.0%2F24" {
				t.Errorf("delete escaped path = %q", request.URL.EscapedPath())
			}
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
	local := initialTUITargets()[0]
	expires := 60

	if _, err := runTUISecurityAccessListOperation(context.Background(), client, tuiSecurityAccessListOperation{
		Target: local, Action: "add", ListType: "whitelist", IP: "2001:db8::/64", Confirmed: false,
	}); err == nil || !strings.Contains(err.Error(), "explicit confirmation") || addRequests.Load() != 0 {
		t.Fatalf("unconfirmed add err=%v requests=%d", err, addRequests.Load())
	}
	message, err := runTUISecurityAccessListOperation(context.Background(), client, tuiSecurityAccessListOperation{
		Target: local, Action: "add", ListType: "whitelist", IP: "2001:db8::/64", Comment: "office", ExpiresInMinutes: &expires, Confirmed: true,
	})
	if err != nil || message != "Added 2001:db8::/64 to whitelist" || addRequests.Load() != 1 {
		t.Fatalf("add message=%q err=%v requests=%d", message, err, addRequests.Load())
	}
	message, err = runTUISecurityAccessListOperation(context.Background(), client, tuiSecurityAccessListOperation{
		Target: local, Action: "delete", ListType: "blacklist", IP: "198.51.100.0/24", Confirmed: true,
	})
	if err != nil || message != "Deleted 198.51.100.0/24 from blacklist" || deleteRequests.Load() != 1 {
		t.Fatalf("delete message=%q err=%v requests=%d", message, err, deleteRequests.Load())
	}
}

func TestRunTUISecurityAccessListOperationNeverUsesManagedTargetOrRawErrorBody(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "raw-backend-secret", http.StatusInternalServerError)
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	managed := tuiTarget{ID: "edge-1", Name: "Edge", Online: true}
	if _, err := runTUISecurityAccessListOperation(context.Background(), client, tuiSecurityAccessListOperation{
		Target: managed, Action: "delete", ListType: "blacklist", IP: "198.51.100.20", Confirmed: true,
	}); err == nil || !strings.Contains(err.Error(), "only on the panel host") || requests.Load() != 0 {
		t.Fatalf("managed operation err=%v requests=%d", err, requests.Load())
	}
	if _, err := runTUISecurityAccessListOperation(context.Background(), client, tuiSecurityAccessListOperation{
		Target: initialTUITargets()[0], Action: "delete", ListType: "blacklist", IP: "198.51.100.20", Confirmed: true,
	}); err == nil || strings.Contains(err.Error(), "raw-backend-secret") || requests.Load() != 1 {
		t.Fatalf("raw error err=%v requests=%d", err, requests.Load())
	}
}

func TestValidateTUISecurityAccessOptionalCommentAndExpiryBounds(t *testing.T) {
	t.Parallel()
	if got, err := validateTUISecurityAccessComment("  office  "); err != nil || got != "office" {
		t.Fatalf("comment normalization got=%q err=%v", got, err)
	}
	if _, err := validateTUISecurityAccessComment(strings.Repeat("x", tuiSecurityAccessMaxCommentRunes+1)); err == nil {
		t.Fatal("oversized comment unexpectedly succeeded")
	}
	if _, err := validateTUISecurityAccessComment("line\nfeed"); err == nil {
		t.Fatal("control comment unexpectedly succeeded")
	}
	if got, err := validateTUISecurityAccessExpiry(nil); err != nil || got != nil {
		t.Fatalf("nil expiry got=%v err=%v", got, err)
	}
	zero := 0
	if got, err := validateTUISecurityAccessExpiry(&zero); err != nil || got == nil || *got != 0 {
		t.Fatalf("zero expiry got=%v err=%v", got, err)
	}
	negative := -1
	if _, err := validateTUISecurityAccessExpiry(&negative); err == nil {
		t.Fatal("negative expiry unexpectedly succeeded")
	}
}

func TestTUISecurityAccessAddFlowRequiresYAndSchedulesRefresh(t *testing.T) {
	t.Parallel()
	var addRequests atomic.Int32
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method != http.MethodPost || request.URL.Path != tuiSecurityAccessWhitelistEndpoint {
			http.NotFound(writer, request)
			return
		}
		addRequests.Add(1)
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode add payload: %v", err)
		}
		_, _ = io.WriteString(writer, `{"id":11,"ip":"2001:db8::/64","listType":"whitelist","comment":"office","createdAt":"2026-08-29T01:00:00Z"}`)
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	model := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
	model.loading = false
	model.tab = tuiTabSecurity
	model.securityLoaded = true
	model.securityTarget = localTargetID
	model.security = tuiSecurityState{Supported: true, AccessListsLoaded: true}

	updated, command := model.updateKey("a")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogChoices || addRequests.Load() != 0 {
		t.Fatalf("add list choice = %#v command=%v requests=%d", model.dialog, command != nil, addRequests.Load())
	}
	updated, command = model.updateDialogKey("down")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Cursor != 1 {
		t.Fatalf("whitelist choice cursor=%d command=%v", model.dialog.Cursor, command != nil)
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogSecurityAccessForm || model.dialog.SecurityAccessForm.ListType != string(tuiSecurityAccessWhitelist) {
		t.Fatalf("access form = %#v command=%v", model.dialog, command != nil)
	}
	model.dialog.SecurityAccessForm.Fields[0].Value = "2001:db8::/64"
	model.dialog.SecurityAccessForm.Fields[1].Value = "office"
	model.dialog.SecurityAccessForm.Fields[2].Value = "60"
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || model.dialog.SecurityAccessOperation == nil || model.dialog.SecurityAccessOperation.Confirmed || addRequests.Load() != 0 {
		t.Fatalf("review dialog = %#v command=%v requests=%d", model.dialog, command != nil, addRequests.Load())
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.operating || addRequests.Load() != 0 {
		t.Fatal("Enter bypassed explicit access-list confirmation")
	}
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating || addRequests.Load() != 0 {
		t.Fatalf("Y start = operating=%v command=%v requests=%d", model.operating, command != nil, addRequests.Load())
	}
	message, ok := command().(tuiOperationMsg)
	if !ok || message.Err != nil || message.Message != "Added 2001:db8::/64 to whitelist" || addRequests.Load() != 1 {
		t.Fatalf("operation message=%#v requests=%d", message, addRequests.Load())
	}
	if payload["ip"] != "2001:db8::/64" || payload["comment"] != "office" || payload["expiresInMinutes"] != float64(60) {
		t.Fatalf("operation payload=%#v", payload)
	}
	updated, refresh := model.Update(message)
	model = updated.(tuiModel)
	if refresh == nil || model.securityLoaded || model.operating || model.dialog.Mode != tuiDialogNone {
		t.Fatalf("post-success refresh state=%#v refresh=%v", model, refresh != nil)
	}
}

func TestTUISecurityAccessDeleteFlowAndManagedBoundary(t *testing.T) {
	t.Parallel()
	var deleteRequests, managedRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodDelete && request.URL.Path == tuiSecurityAccessBlacklistEndpoint+"/198.51.100.0/24" {
			deleteRequests.Add(1)
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		managedRequests.Add(1)
		http.Error(writer, "raw-managed-body", http.StatusInternalServerError)
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	local := initialTUITargets()[0]
	model := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
	model.loading = false
	model.tab = tuiTabSecurity
	model.securityLoaded = true
	model.securityTarget = local.ID
	model.security = tuiSecurityState{
		Supported: true, AccessListsLoaded: true,
		Items: []tuiSecurityItem{{Kind: tuiSecurityBlacklistItem, AccessEntry: cliSecurityIPEntry{IP: "198.51.100.0/24", ListType: "blacklist", Comment: "temporary"}}},
	}

	updated, command := model.updateKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || model.dialog.SecurityAccessOperation == nil || model.dialog.SecurityAccessOperation.Confirmed || deleteRequests.Load() != 0 {
		t.Fatalf("delete confirmation=%#v command=%v requests=%d", model.dialog, command != nil, deleteRequests.Load())
	}
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating || deleteRequests.Load() != 0 {
		t.Fatalf("delete Y start operating=%v command=%v requests=%d", model.operating, command != nil, deleteRequests.Load())
	}
	message, ok := command().(tuiOperationMsg)
	if !ok || message.Err != nil || message.Message != "Deleted 198.51.100.0/24 from blacklist" || deleteRequests.Load() != 1 {
		t.Fatalf("delete operation message=%#v requests=%d", message, deleteRequests.Load())
	}

	managed := tuiTarget{ID: "edge-1", Name: "Edge", Online: true}
	managedModel := model
	managedModel.snapshot.Selected = managed
	managedModel.selectedTargetID = managed.ID
	managedModel.operating = false
	managedModel.dialog = tuiDialog{}
	managedModel.securityLoaded = true
	managedModel.security = tuiSecurityState{
		AccessListsLoaded: true,
		Items:             []tuiSecurityItem{{Kind: tuiSecurityBlacklistItem, IP: "198.51.100.20", AccessEntry: cliSecurityIPEntry{IP: "198.51.100.20"}}},
	}
	updated, command = managedModel.updateKey("a")
	managedModel = updated.(tuiModel)
	if command != nil || managedModel.dialog.Mode != tuiDialogNone || !managedModel.noticeError || !strings.Contains(managedModel.notice, "only on the panel host") || managedRequests.Load() != 0 {
		t.Fatalf("managed add boundary state=%#v command=%v requests=%d", managedModel, command != nil, managedRequests.Load())
	}
	updated, command = managedModel.updateKey("enter")
	managedModel = updated.(tuiModel)
	if command != nil || managedModel.dialog.Mode != tuiDialogNone || managedRequests.Load() != 0 {
		t.Fatalf("managed delete boundary state=%#v command=%v requests=%d", managedModel, command != nil, managedRequests.Load())
	}
}

func TestTUISecurityAccessDenialAndViewStaySafe(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && request.URL.Path == tuiSecurityAccessBlacklistEndpoint {
			http.Error(writer, `{"error":"permission denied","detail":"raw-backend-secret\u001b[31m"}`, http.StatusForbidden)
			return
		}
		http.NotFound(writer, request)
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	local := initialTUITargets()[0]
	message, err := runTUISecurityAccessListOperation(context.Background(), client, tuiSecurityAccessListOperation{
		Target: local, Action: "add", ListType: "blacklist", IP: "198.51.100.20", Comment: "blocked", Confirmed: true,
	})
	if err == nil || message != "" || strings.Contains(err.Error(), "raw-backend-secret") || strings.ContainsAny(err.Error(), "\r\n\x1b") || !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("denial message=%q err=%v", message, err)
	}
	modelAfterError := tuiModel{tab: tuiTabSecurity, securityLoaded: true, operating: true}
	updated, _ := modelAfterError.Update(tuiOperationMsg{Err: err})
	modelAfterError = updated.(tuiModel)
	if !modelAfterError.noticeError || strings.Contains(modelAfterError.notice, "raw-backend-secret") || strings.Contains(modelAfterError.notice, "\x1b") || !strings.Contains(modelAfterError.notice, "HTTP 403") {
		t.Fatalf("safe denial notice=%q", modelAfterError.notice)
	}

	model := tuiModel{
		tab: tuiTabSecurity, width: 120, height: 32, selectedTargetID: local.ID,
		snapshot:       tuiSnapshot{Selected: local, Targets: []tuiTarget{local}},
		securityLoaded: true, securityTarget: local.ID,
		security: tuiSecurityState{
			Supported: true, AccessListsLoaded: true,
			AccessLists: tuiSecurityAccessListsState{Supported: true, BlacklistLoaded: true, WhitelistLoaded: true, Blacklist: []cliSecurityIPEntry{{IP: "198.51.100.20", Comment: "secret\n\x1b[31m"}}, Whitelist: []cliSecurityIPEntry{}},
			Items:       []tuiSecurityItem{{Kind: tuiSecurityBlacklistItem, AccessEntry: cliSecurityIPEntry{IP: "198.51.100.20", Comment: "secret\n\x1b[31m"}}},
		},
	}
	view := ansi.Strip(model.View().Content)
	if !strings.Contains(view, "198.51.100.20") || strings.Contains(view, "raw-backend-secret") || strings.Contains(view, "\x1b") {
		t.Fatalf("unsafe access-list view=%q", view)
	}
}
