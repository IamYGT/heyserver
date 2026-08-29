package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

func TestPM2ControllerParsesBoundedInventoryWithConfiguredRuntime(t *testing.T) {
	binary := preparePM2Binary(t)
	output := `[{
		"pm_id":2,"name":"api:blue","pid":420,
		"monit":{"cpu":4.5,"memory":67108864},
		"pm2_env":{"status":"online","pm_uptime":1787700000000,"restart_time":3,"exec_mode":"cluster_mode","pm_cwd":"/srv/api","pm_exec_path":"/srv/api/server.js","version":"2.4.0"}
	}]`
	runner := &fakeRunner{outputs: [][]byte{[]byte(output)}}
	controller := newPM2Controller(runner, true, nil, binary, "/srv/pm2/home", "root")

	processes, err := controller.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(processes) != 1 || processes[0].ID != 2 || processes[0].Name != "api:blue" || processes[0].Status != "online" || processes[0].Memory != 67108864 || processes[0].Restarts != 3 {
		t.Fatalf("processes = %#v", processes)
	}
	want := []recordedCommand{{name: "env", args: []string{"PM2_HOME=/srv/pm2/home", binary, "jlist"}}}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
}

func TestPM2ControllerRunsAllowlistedActionAsConfiguredUser(t *testing.T) {
	binary := preparePM2Binary(t)
	runner := &fakeRunner{outputs: [][]byte{nil, nil, nil}}
	controller := newPM2Controller(runner, false, map[string]struct{}{"restart": {}}, binary, "/home/deploy/.pm2", "deploy")
	message, err := controller.Action(context.Background(), "api", "restart")
	if err != nil || !strings.Contains(message, "process list saved") {
		t.Fatalf("Action = (%q, %v)", message, err)
	}
	prefix := []string{"-u", "deploy", "--", "env", "PM2_HOME=/home/deploy/.pm2", binary}
	want := []recordedCommand{
		{name: "runuser", args: append(append([]string(nil), prefix...), "describe", "api")},
		{name: "runuser", args: append(append([]string(nil), prefix...), "restart", "api", "--update-env")},
		{name: "runuser", args: append(append([]string(nil), prefix...), "save", "--force")},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
	if _, err := controller.Action(context.Background(), "api", "delete"); err == nil || !strings.Contains(err.Error(), "allowlist") {
		t.Fatalf("blocked action error = %v", err)
	}
}

func TestTaskExecutorReturnsStructuredPM2Logs(t *testing.T) {
	binary := preparePM2Binary(t)
	executor := newTaskExecutor(serviceController{}, nil, nil, nil, nil, nil, nil, false)
	executor.pm2 = newPM2Controller(&fakeRunner{outputs: [][]byte{nil, []byte("line one\nline two\n")}}, true, nil, binary, "/root/.pm2", "root")
	result := executor.execute(context.Background(), &agenthub.Task{Kind: agenthub.TaskPM2Logs, Payload: map[string]string{"name": "api", "lines": "200"}})
	if result.Status != agenthub.TaskStatusCompleted {
		t.Fatalf("result = %#v", result)
	}
	var logs string
	if err := json.Unmarshal([]byte(result.Result["data"]), &logs); err != nil || logs != "line one\nline two\n" {
		t.Fatalf("logs = %q, err=%v, result=%#v", logs, err, result)
	}
}

func preparePM2Binary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "pm2")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return binary
}
