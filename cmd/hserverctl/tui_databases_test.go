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

func TestLoadTUIDatabasesNormalizesLocalAndManagedInventory(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/databases":
			_, _ = writer.Write([]byte(`{"databases":[{"name":"portal","engine":"postgres","owner":"portal","size":"16 MB","tables":12}],"sources":{"postgresql":{"available":true,"state":"healthy"},"mariadb":{"available":false,"state":"stopped","error":"connection refused"}}}`))
		case "/api/nodes/edge-1/databases":
			_, _ = writer.Write([]byte(`[{"id":"postgresql","name":"PostgreSQL","version":"16.4","unit":"postgresql@16-main.service","active":"active","data_size":16777216,"databases":[{"name":"portal","size":16777216,"connections":2,"objects":12}],"sessions":[{"id":"91","user":"portal","database":"portal","state":"active","age_seconds":4,"query":"SELECT 1"}]}]`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	local, err := loadTUIDatabases(context.Background(), client, initialTUITargets()[0])
	if err != nil {
		t.Fatal(err)
	}
	if !local.Manageable || local.Restartable || len(local.Items) != 3 || local.Items[0].Kind != tuiDatabaseEngineItem || local.Items[1].Kind != tuiDatabaseRowItem || local.Items[1].Name != "portal" || local.Sources["mariadb"].State != "stopped" {
		t.Fatalf("local = %#v", local)
	}
	remoteTarget := tuiTarget{ID: "edge-1", Name: "Edge", Online: true, Capabilities: map[string]bool{
		agenthub.CapabilityDatabaseRead: true, agenthub.CapabilityDatabaseAction: true,
	}}
	remote, err := loadTUIDatabases(context.Background(), client, remoteTarget)
	if err != nil {
		t.Fatal(err)
	}
	if remote.Manageable || !remote.Restartable || len(remote.Items) != 2 || remote.Items[0].SessionCount != 1 || remote.Items[1].Connections != 2 {
		t.Fatalf("remote = %#v", remote)
	}
	remoteTarget.Capabilities = map[string]bool{}
	if _, err := loadTUIDatabases(context.Background(), client, remoteTarget); err == nil || !strings.Contains(err.Error(), "database.read") {
		t.Fatalf("missing capability error = %v", err)
	}
	remoteTarget.Online = false
	if _, err := loadTUIDatabases(context.Background(), client, remoteTarget); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline error = %v", err)
	}
}

func TestTUIDatabaseLocalDropRequiresExplicitConfirmation(t *testing.T) {
	t.Parallel()
	var dropRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/databases":
			if request.URL.Query().Get("engine") != "postgres" {
				t.Errorf("engine query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"databases":[{"name":"portal","engine":"postgres","owner":"portal","size":"16 MB","tables":12}],"sources":{"postgresql":{"available":true,"state":"healthy"}}}`))
		case "DELETE /api/databases/postgres/portal":
			dropRequests.Add(1)
			var payload map[string]string
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil || payload["confirm"] != "DROP portal" {
				t.Errorf("drop payload = %#v, err=%v", payload, err)
			}
			_, _ = writer.Write([]byte(`{"message":"database dropped"}`))
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
	model.tab = tuiTabDatabases
	model.snapshot.Selected = model.snapshot.Targets[0]
	model.databasesLoaded = true
	model.databasesTarget = localTargetID
	model.databases = tuiDatabaseState{Manageable: true, Items: []tuiDatabaseItem{{
		Kind: tuiDatabaseRowItem, Engine: "postgresql", EngineName: "PostgreSQL", Name: "portal", Owner: "portal", SizeText: "16 MB", Tables: 12,
	}}}

	updated, command := model.updateKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || !model.dialog.Operation.Dangerous || dropRequests.Load() != 0 {
		t.Fatalf("drop confirmation = %#v", model.dialog)
	}
	updated, command = model.updateDialogKey("enter")
	if command != nil || dropRequests.Load() != 0 {
		t.Fatal("Enter bypassed database drop confirmation")
	}
	model = updated.(tuiModel)
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("Y did not start database drop")
	}
	message := command().(tuiOperationMsg)
	if message.Err != nil || message.Message != "database dropped" || dropRequests.Load() != 1 {
		t.Fatalf("message = %#v", message)
	}
}

func TestTUIDatabaseRemoteRestartRefreshesObservedEngine(t *testing.T) {
	t.Parallel()
	var getRequests atomic.Int32
	var restartRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/nodes/edge-1/databases":
			getRequests.Add(1)
			_, _ = writer.Write([]byte(`[{"id":"mariadb","name":"MariaDB","version":"11.4","unit":"mariadb.service","active":"active","data_size":4096,"databases":[],"sessions":[]}]`))
		case "POST /api/nodes/edge-1/databases/mariadb/actions/restart":
			restartRequests.Add(1)
			_, _ = writer.Write([]byte(`{"message":"MariaDB restarted and socket health check passed"}`))
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
		agenthub.CapabilityDatabaseRead: true, agenthub.CapabilityDatabaseAction: true,
	}}
	message, err := runTUIDatabaseOperation(context.Background(), client, tuiOperation{
		Kind: tuiOperationDatabase, Target: target, Action: "restart",
		Database: tuiDatabaseItem{Kind: tuiDatabaseEngineItem, Engine: "mariadb", EngineName: "MariaDB"},
	})
	if err != nil || message != "MariaDB restarted and socket health check passed" || getRequests.Load() != 1 || restartRequests.Load() != 1 {
		t.Fatalf("message=%q err=%v get=%d restart=%d", message, err, getRequests.Load(), restartRequests.Load())
	}
}

func TestTUIDatabasesDirectJumpAndView(t *testing.T) {
	t.Parallel()
	model := tuiModel{
		tab: tuiTabOverview, width: 150, height: 30, selectedTargetID: localTargetID,
		snapshot:        tuiSnapshot{Selected: initialTUITargets()[0]},
		databasesLoaded: true, databasesTarget: localTargetID,
		databases: tuiDatabaseState{Manageable: true, Items: []tuiDatabaseItem{
			{Kind: tuiDatabaseEngineItem, Engine: "postgresql", EngineName: "PostgreSQL", Name: "PostgreSQL", Active: "healthy"},
			{Kind: tuiDatabaseRowItem, Engine: "postgresql", EngineName: "PostgreSQL", Name: "portal", Owner: "portal", SizeText: "16 MB", Tables: 12},
		}},
	}
	updated, command := model.updateKey("D")
	model = updated.(tuiModel)
	if command != nil || model.tab != tuiTabDatabases {
		t.Fatalf("direct jump tab=%v command=%v", model.tab, command != nil)
	}
	view := model.View().Content
	for _, expected := range []string{"Databases", "PostgreSQL", "portal", "16 MB"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("view missing %q: %q", expected, view)
		}
	}
}
