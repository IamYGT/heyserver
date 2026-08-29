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

func TestLoadTUIPM2NormalizesLocalAndManagedProcesses(t *testing.T) {
	t.Parallel()
	remoteStarted := time.Now().Add(-90 * time.Second).UnixMilli()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/pm2/processes":
			_, _ = writer.Write([]byte(`[{"id":1,"name":"local-api","status":"online","pid":42,"cpu":2.5,"memory":64,"uptime":120,"restarts":2,"mode":"fork_mode"}]`))
		case "/api/nodes/edge-1/pm2":
			_ = json.NewEncoder(writer).Encode([]map[string]any{{
				"id": 2, "name": "remote-api", "status": "online", "pid": 84, "cpu": 3.5,
				"memory": 1048576, "uptime": remoteStarted, "restarts": 1, "mode": "cluster_mode",
			}})
		case "/api/pm2/processes/1/logs":
			_, _ = writer.Write([]byte(`{"output":["local ready","request complete"]}`))
		case "/api/nodes/edge-1/pm2/remote-api/logs":
			_, _ = writer.Write([]byte(`{"logs":"remote ready\nrequest complete\n"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	localTarget := initialTUITargets()[0]
	local, err := loadTUIPM2(context.Background(), client, localTarget)
	if err != nil {
		t.Fatal(err)
	}
	if len(local) != 1 || local[0].ID != "1" || local[0].MemoryBytes != 64*1024*1024 || local[0].UptimeSeconds != 120 {
		t.Fatalf("local PM2 = %#v", local)
	}
	localLogs, err := loadTUIPM2Logs(context.Background(), client, localTarget, local[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(localLogs, "|") != "local ready|request complete" {
		t.Fatalf("local logs = %#v", localLogs)
	}

	remoteTarget := tuiTarget{
		ID: "edge-1", Name: "Edge", Online: true,
		Capabilities: map[string]bool{agenthub.CapabilityPM2Read: true},
	}
	remote, err := loadTUIPM2(context.Background(), client, remoteTarget)
	if err != nil {
		t.Fatal(err)
	}
	if len(remote) != 1 || remote[0].Name != "remote-api" || remote[0].MemoryBytes != 1048576 || remote[0].UptimeSeconds < 88 || remote[0].UptimeSeconds > 92 {
		t.Fatalf("remote PM2 = %#v", remote)
	}
	remoteLogs, err := loadTUIPM2Logs(context.Background(), client, remoteTarget, remote[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(remoteLogs, "|") != "remote ready|request complete" {
		t.Fatalf("remote logs = %#v", remoteLogs)
	}

	remoteTarget.Capabilities = map[string]bool{}
	if _, err := loadTUIPM2(context.Background(), client, remoteTarget); err == nil || !strings.Contains(err.Error(), "pm2.read") {
		t.Fatalf("missing capability error = %v", err)
	}
}

func TestTUIPM2MutationRequiresConfirmation(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodPost {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		switch request.URL.Path {
		case "/api/pm2/processes/1/restart":
			_ = json.NewEncoder(writer).Encode(map[string]string{"status": "restarted", "output": "done"})
		case "/api/pm2/save":
			_ = json.NewEncoder(writer).Encode(map[string]string{"status": "saved"})
		default:
			t.Errorf("unexpected path = %s", request.URL.Path)
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
	model.tab = tuiTabPM2
	model.pm2Loaded = true
	model.pm2Processes = []tuiPM2Process{{ID: "1", Name: "api", Status: "online", PID: 42}}
	model.snapshot.Selected = model.snapshot.Targets[0]

	updated, command := model.updateKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogChoices || len(model.dialog.Options) != 6 || requests.Load() != 0 {
		t.Fatalf("action menu: dialog=%v options=%d command=%v requests=%d", model.dialog.Mode, len(model.dialog.Options), command != nil, requests.Load())
	}
	updated, _ = model.updateDialogKey("down")
	model = updated.(tuiModel)
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || model.dialog.Operation.Action != "restart" {
		t.Fatalf("confirmation = %#v", model.dialog)
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || requests.Load() != 0 {
		t.Fatalf("enter confirmed mutation: command=%v requests=%d", command != nil, requests.Load())
	}
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("explicit y did not start the PM2 operation")
	}
	message := command()
	result, ok := message.(tuiOperationMsg)
	if !ok || result.Err != nil || result.Message != "done" {
		t.Fatalf("operation result = %#v", message)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
	saved, err := runTUIOperation(context.Background(), client, tuiOperation{
		Kind: tuiOperationPM2, Target: model.snapshot.Targets[0], Action: "save", PM2Process: model.pm2Processes[0],
	})
	if err != nil || saved != "PM2 process list saved" || requests.Load() != 2 {
		t.Fatalf("save result = %q, err=%v, requests=%d", saved, err, requests.Load())
	}
}

func TestTUIResourceMutationCompletionForcesInventoryReload(t *testing.T) {
	t.Parallel()
	targets := initialTUITargets()
	for _, item := range []struct {
		name  string
		model tuiModel
		check func(tuiModel) bool
	}{
		{
			name:  "containers",
			model: tuiModel{tab: tuiTabContainers, selectedTargetID: localTargetID, containersLoaded: true, snapshot: tuiSnapshot{Targets: targets, Selected: targets[0]}},
			check: func(model tuiModel) bool { return !model.containersLoaded },
		},
		{
			name:  "pm2",
			model: tuiModel{tab: tuiTabPM2, selectedTargetID: localTargetID, pm2Loaded: true, snapshot: tuiSnapshot{Targets: targets, Selected: targets[0]}},
			check: func(model tuiModel) bool { return !model.pm2Loaded },
		},
		{
			name:  "web",
			model: tuiModel{tab: tuiTabWeb, selectedTargetID: localTargetID, webLoaded: true, snapshot: tuiSnapshot{Targets: targets, Selected: targets[0]}},
			check: func(model tuiModel) bool { return !model.webLoaded },
		},
		{
			name:  "backups",
			model: tuiModel{tab: tuiTabBackups, selectedTargetID: localTargetID, backupsLoaded: true, snapshot: tuiSnapshot{Targets: targets, Selected: targets[0]}},
			check: func(model tuiModel) bool { return !model.backupsLoaded },
		},
	} {
		item := item
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			updated, command := item.model.Update(tuiOperationMsg{Message: "completed"})
			model := updated.(tuiModel)
			if command == nil || !item.check(model) {
				t.Fatalf("reload state = %#v, command=%v", model, command != nil)
			}
		})
	}
}
