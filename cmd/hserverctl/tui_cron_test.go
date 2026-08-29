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

func TestLoadTUICronNormalizesLocalAndManagedInventory(t *testing.T) {
	t.Parallel()
	const revision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/cron/status":
			_, _ = writer.Write([]byte(`{"available":true,"state":"healthy","daemonState":"active"}`))
		case "/api/cron/jobs":
			_, _ = writer.Write([]byte(`{"jobs":[{"id":"0123456789abcdef","user":"root","schedule":"0 3 * * *","command":"/usr/local/bin/backup","description":"Nightly","isActive":true}]}`))
		case "/api/nodes/edge-1/cron":
			_, _ = writer.Write([]byte(`{"service":"active","jobs":[{"id":"cron-0123456789ab","schedule":"30 2 * * 1","user":"deploy","command":"/usr/local/bin/report","description":"Weekly","enabled":false}],"sources":[{"path":"/etc/cron.d/hserver-managed","entry_count":1,"managed":true}],"revision":"` + revision + `"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	local, err := loadTUICron(context.Background(), client, initialTUITargets()[0])
	if err != nil {
		t.Fatal(err)
	}
	if !local.Available || !local.Manageable || local.Runnable || local.Service != "active" || len(local.Jobs) != 1 || !local.Jobs[0].Enabled {
		t.Fatalf("local = %#v", local)
	}
	remoteTarget := tuiTarget{ID: "edge-1", Name: "Edge", Online: true, Capabilities: map[string]bool{
		agenthub.CapabilityCronRead: true, agenthub.CapabilityCronWrite: true, agenthub.CapabilityCronRun: true,
	}}
	remote, err := loadTUICron(context.Background(), client, remoteTarget)
	if err != nil {
		t.Fatal(err)
	}
	if !remote.Manageable || !remote.Runnable || remote.Revision != revision || len(remote.Jobs) != 1 || remote.Jobs[0].Enabled || len(remote.Sources) != 1 || !remote.Sources[0].Managed {
		t.Fatalf("remote = %#v", remote)
	}

	remoteTarget.Capabilities = map[string]bool{}
	if _, err := loadTUICron(context.Background(), client, remoteTarget); err == nil || !strings.Contains(err.Error(), "cron.read") {
		t.Fatalf("missing capability error = %v", err)
	}
	remoteTarget.Online = false
	if _, err := loadTUICron(context.Background(), client, remoteTarget); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline error = %v", err)
	}
}

func TestTUICronJobActionsRequireExplicitConfirmation(t *testing.T) {
	t.Parallel()
	const localID = "0123456789abcdef"
	var updateRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/cron/jobs":
			_, _ = writer.Write([]byte(`{"jobs":[{"id":"` + localID + `","user":"root","schedule":"0 3 * * *","command":"/usr/local/bin/backup","description":"Nightly","isActive":true}]}`))
		case "PUT /api/cron/jobs/" + localID:
			updateRequests.Add(1)
			if request.URL.Query().Get("user") != "root" {
				t.Errorf("user query = %q", request.URL.RawQuery)
			}
			var payload struct {
				Schedule string `json:"schedule"`
				Command  string `json:"command"`
				IsActive bool   `json:"isActive"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload.Schedule != "0 3 * * *" || payload.Command != "/usr/local/bin/backup" || payload.IsActive {
				t.Errorf("update payload = %#v, err=%v", payload, err)
			}
			_, _ = writer.Write([]byte(`{"message":"cron job updated"}`))
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
	model.tab = tuiTabCron
	model.snapshot.Selected = model.snapshot.Targets[0]
	model.cronLoaded = true
	model.cronTarget = localTargetID
	model.cron = tuiCronState{Service: "active", Available: true, Manageable: true, Jobs: []tuiCronJob{{
		ID: localID, User: "root", Schedule: "0 3 * * *", Command: "/usr/local/bin/backup", Description: "Nightly", Enabled: true,
	}}}

	updated, command := model.updateKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogChoices || len(model.dialog.Options) != 2 || updateRequests.Load() != 0 {
		t.Fatalf("cron menu = %#v", model.dialog)
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || model.dialog.Operation.Action != "disable" || updateRequests.Load() != 0 {
		t.Fatalf("cron confirmation = %#v", model.dialog)
	}
	updated, command = model.updateDialogKey("enter")
	if command != nil || updateRequests.Load() != 0 {
		t.Fatal("Enter bypassed cron confirmation")
	}
	model = updated.(tuiModel)
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("Y did not start cron update")
	}
	message := command().(tuiOperationMsg)
	if message.Err != nil || message.Message != "cron job updated" || updateRequests.Load() != 1 {
		t.Fatalf("message = %#v", message)
	}
}

func TestTUICronRemoteRunRefreshesObservedIdentity(t *testing.T) {
	t.Parallel()
	const (
		jobID    = "cron-0123456789ab"
		revision = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	)
	var getRequests atomic.Int32
	var runRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/nodes/edge-1/cron":
			getRequests.Add(1)
			_, _ = writer.Write([]byte(`{"service":"active","jobs":[{"id":"` + jobID + `","schedule":"0 3 * * *","user":"root","command":"/usr/local/bin/backup","enabled":true}],"revision":"` + revision + `"}`))
		case "POST /api/nodes/edge-1/cron/" + jobID + "/run":
			runRequests.Add(1)
			_, _ = writer.Write([]byte(`{"message":"Cron job completed","output":"backup ok\n"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	target := tuiTarget{ID: "edge-1", Name: "Edge", Online: true, Capabilities: map[string]bool{
		agenthub.CapabilityCronRead: true, agenthub.CapabilityCronRun: true,
	}}
	message, err := runTUICronOperation(context.Background(), client, tuiOperation{
		Kind: tuiOperationCron, Target: target, Action: "run", CronJob: tuiCronJob{ID: jobID},
	})
	if err != nil || !strings.Contains(message, "Cron job completed") || !strings.Contains(message, "backup ok") || getRequests.Load() != 1 || runRequests.Load() != 1 {
		t.Fatalf("message=%q err=%v get=%d run=%d", message, err, getRequests.Load(), runRequests.Load())
	}
}

func TestTUICronDirectJumpAndView(t *testing.T) {
	t.Parallel()
	model := tuiModel{
		tab: tuiTabOverview, width: 140, height: 30, selectedTargetID: localTargetID,
		snapshot:   tuiSnapshot{Selected: initialTUITargets()[0]},
		cronLoaded: true, cronTarget: localTargetID,
		cron: tuiCronState{Service: "active", Manageable: true, Jobs: []tuiCronJob{{ID: "0123456789abcdef", Schedule: "0 3 * * *", User: "root", Command: "backup", Enabled: true}}},
	}
	updated, command := model.updateKey("C")
	model = updated.(tuiModel)
	if command != nil || model.tab != tuiTabCron {
		t.Fatalf("direct jump tab=%v command=%v", model.tab, command != nil)
	}
	view := model.View().Content
	for _, expected := range []string{"Cron jobs", "0123456789abcdef", "0 3 * * *", "backup"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("view missing %q: %q", expected, view)
		}
	}
}
