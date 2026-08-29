package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoadTUISecurityPreservesLocalStatesAndExplainsManagedBoundary(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/security/score":
			_, _ = io.WriteString(writer, `{"score":82,"maxScore":100,"checks":[{"name":"Firewall","status":"pass","detail":"Active"},{"name":"Fail2Ban","status":"pass","detail":"Running"}]}`)
		case "/api/security/fail2ban/status":
			_, _ = io.WriteString(writer, `{"available":true,"installed":true,"running":true,"state":"healthy","daemonState":"active","availableJails":["sshd"],"jails":[{"name":"sshd","currentlyFailed":2,"currentlyBanned":1,"totalBanned":4,"bannedIPs":["192.0.2.10"]}]}`)
		case "/api/security/ip-blacklist":
			_, _ = io.WriteString(writer, `[{"id":7,"ip":"198.51.100.0/24","listType":"blacklist","comment":"blocked range","createdAt":"2026-08-29T00:00:00Z"}]`)
		case "/api/security/ip-whitelist":
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
	local := loadTUISecurity(context.Background(), client, initialTUITargets()[0])
	if !local.Supported || !local.ScoreLoaded || !local.Fail2BanLoaded || !local.AccessListsLoaded || !local.AccessLists.BlacklistLoaded || !local.AccessLists.WhitelistLoaded || local.Score.Score != 82 || len(local.Items) != 6 || local.Items[2].Kind != tuiSecurityJailItem || local.Items[3].Kind != tuiSecurityBannedIPItem || local.Items[4].Kind != tuiSecurityBlacklistItem || local.Items[5].Kind != tuiSecurityWhitelistItem {
		t.Fatalf("local security state = %#v", local)
	}
	before := requests.Load()
	managed := loadTUISecurity(context.Background(), client, tuiTarget{ID: "edge-1", Online: true})
	if managed.Supported || managed.UnsupportedNote == "" || len(managed.Items) != 0 {
		t.Fatalf("managed security state = %#v", managed)
	}
	if requests.Load() != before {
		t.Fatal("managed security boundary made a local security API request")
	}
}

func TestLoadTUISecurityKeepsFail2BanWhenScoreFails(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/security/score":
			http.Error(writer, "score unavailable", http.StatusServiceUnavailable)
		case "/api/security/fail2ban/status":
			_, _ = io.WriteString(writer, `{"available":false,"installed":false,"running":false,"state":"not-installed","daemonState":"unknown","error":"fail2ban-client is not installed","availableJails":[],"jails":[]}`)
		case "/api/security/ip-blacklist", "/api/security/ip-whitelist":
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
	state := loadTUISecurity(context.Background(), client, initialTUITargets()[0])
	if state.ScoreLoaded || !state.Fail2BanLoaded || state.Fail2Ban.State != "not-installed" || len(state.Warnings) != 1 || !strings.Contains(state.Warnings[0], "Security score unavailable") {
		t.Fatalf("partial security state = %#v", state)
	}
}

func TestTUISecurityUnbanRequiresExplicitConfirmationAndReobservation(t *testing.T) {
	t.Parallel()
	var statusRequests, unbanRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/security/fail2ban/status":
			statusRequests.Add(1)
			_, _ = io.WriteString(writer, `{"available":true,"installed":true,"running":true,"state":"healthy","daemonState":"active","availableJails":["sshd"],"jails":[{"name":"sshd","currentlyBanned":1,"bannedIPs":["192.0.2.10"]}]}`)
		case "POST /api/security/fail2ban/unban":
			unbanRequests.Add(1)
			_, _ = io.WriteString(writer, `{"status":"unbanned","ip":"192.0.2.10"}`)
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
	model.tab = tuiTabSecurity
	model.securityLoaded = true
	model.securityTarget = localTargetID
	model.security = tuiSecurityState{
		Supported: true, Fail2BanLoaded: true,
		Fail2Ban: cliFail2BanStatus{Available: true, State: "healthy"},
		Items:    []tuiSecurityItem{{Kind: tuiSecurityBannedIPItem, Name: "192.0.2.10", IP: "192.0.2.10", Jail: "sshd"}},
	}

	updated, command := model.updateKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || !model.dialog.Operation.Dangerous || unbanRequests.Load() != 0 {
		t.Fatalf("confirmation = %#v command=%v unbans=%d", model.dialog, command != nil, unbanRequests.Load())
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || unbanRequests.Load() != 0 {
		t.Fatal("Enter bypassed Fail2Ban unban confirmation")
	}
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("Y did not start Fail2Ban unban")
	}
	message := command().(tuiOperationMsg)
	if message.Err != nil || message.Message != "Unbanned 192.0.2.10 from Fail2Ban jail sshd" || statusRequests.Load() != 1 || unbanRequests.Load() != 1 {
		t.Fatalf("message=%#v status=%d unban=%d", message, statusRequests.Load(), unbanRequests.Load())
	}
}

func TestTUISecurityDirectJumpViewAndPalette(t *testing.T) {
	t.Parallel()
	target := initialTUITargets()[0]
	model := tuiModel{
		tab: tuiTabOverview, width: 150, height: 32, selectedTargetID: localTargetID,
		snapshot:       tuiSnapshot{Selected: target, Targets: []tuiTarget{target}},
		securityLoaded: true, securityTarget: localTargetID,
		security: tuiSecurityState{
			Supported: true, ScoreLoaded: true, Score: cliSecurityScore{Score: 82, MaxScore: 100, Checks: []cliSecurityCheck{{Name: "Firewall", Status: "pass"}}},
			Fail2BanLoaded: true, Fail2Ban: cliFail2BanStatus{Available: true, State: "healthy", DaemonState: "active", Jails: []cliFail2BanJail{{Name: "sshd"}}},
			Items: []tuiSecurityItem{
				{Kind: tuiSecurityCheckItem, Name: "Firewall", Status: "pass", Detail: "Active"},
				{Kind: tuiSecurityJailItem, Name: "sshd", Jail: "sshd", CurrentlyBanned: 1, TotalBanned: 4},
				{Kind: tuiSecurityBannedIPItem, Name: "192.0.2.10", IP: "192.0.2.10", Jail: "sshd"},
			},
		},
	}
	updated, command := model.updateKey("S")
	model = updated.(tuiModel)
	if command != nil || model.tab != tuiTabSecurity {
		t.Fatalf("direct jump tab=%v command=%v", model.tab, command != nil)
	}
	view := model.View().Content
	for _, expected := range []string{"Security", "82/100", "Fail2Ban healthy", "sshd", "192.0.2.10"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("view missing %q: %q", expected, view)
		}
	}
	joined := ""
	for _, item := range model.buildPaletteItems() {
		joined += item.Label + "|"
	}
	if !strings.Contains(joined, "Unban 192.0.2.10 from sshd") {
		t.Fatalf("palette = %q", joined)
	}
}
