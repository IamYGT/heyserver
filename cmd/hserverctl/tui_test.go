package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/creack/pty"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

func TestLoadTUISnapshotCombinesPanelHostAndManagedNodes(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/nodes":
			_, _ = writer.Write([]byte(`[{
				"id":"edge-1","name":"Edge One","hostname":"edge.example.com","online":true,
				"agent_version":"v0.1.0","protocol_version":"v1",
				"capabilities":["host.action","service.action","metrics.read"],
				"inventory":{"os":"Ubuntu 24.04","arch":"arm64","uptime_seconds":3600,"load_1":0.5,
				"memory_total_bytes":8589934592,"memory_available_bytes":4294967296,
				"disk_total_bytes":107374182400,"disk_used_bytes":53687091200,"disk_use_percent":50,
				"services":[{"name":"nginx.service","active":"active"}],
				"processes":[{"pid":42,"startTime":99,"user":"www-data","cpu":12.5,"memory":2.5,"rss":1048576,"command":"nginx"}]}
			}]`))
		case "/api/nodes/edge-1/metrics":
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"observed_at": time.Now().UTC().Format(time.RFC3339Nano),
				"cpu":         map[string]any{"usage_percent": 62.5, "core_count": 4},
				"load":        map[string]any{"one": 0.75, "five": 0.5, "fifteen": 0.25},
				"memory":      map[string]any{"total_bytes": 16000, "used_bytes": 4000, "available_bytes": 12000, "usage_percent": 25.0},
				"network":     map[string]any{"rx_bytes": 100, "tx_bytes": 200},
				"root_disk":   map[string]any{"total_bytes": 100000, "used_bytes": 40000, "available_bytes": 60000, "usage_percent": 40.0},
			})
		case "/api/system/stats":
			_, _ = writer.Write([]byte(`{
				"cpu":{"usage":21.5,"cores":8,"model":"Example CPU"},
				"memory":{"total":17179869184,"used":8589934592,"available":8589934592,"swapTotal":2147483648,"swapUsed":0},
				"disk":[{"mount":"/","total":214748364800,"used":107374182400,"free":107374182400,"percentage":50}],
				"load":[0.4,0.3,0.2],"uptime":7200,"hostname":"panel.example.com","os":"Ubuntu 24.04","network":[]
			}`))
		case "/api/system/services":
			_, _ = writer.Write([]byte(`["unexpected"]`))
		case "/api/monitoring/processes":
			_, _ = writer.Write([]byte(`[{"pid":7,"startTime":77,"user":"root","cpu":3.5,"memory":1.2,"rss":4096,"command":"hserver"}]`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := loadTUISnapshot(context.Background(), client, localTargetID, initialTUITargets(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Targets) != 2 || snapshot.Targets[1].ID != "edge-1" {
		t.Fatalf("targets = %#v", snapshot.Targets)
	}
	if snapshot.Host.Hostname != "panel.example.com" || snapshot.Host.CPUPercent != 21.5 {
		t.Fatalf("host = %#v", snapshot.Host)
	}
	if len(snapshot.Processes) != 1 || snapshot.Processes[0].StartTime != 77 {
		t.Fatalf("processes = %#v", snapshot.Processes)
	}
	if len(snapshot.Warnings) == 0 || !strings.Contains(snapshot.Warnings[0], "Service status unavailable") {
		t.Fatalf("warnings = %#v", snapshot.Warnings)
	}

	remote, err := loadTUISnapshot(context.Background(), client, "edge-1", snapshot.Targets, false)
	if err != nil {
		t.Fatal(err)
	}
	if remote.Selected.ID != "edge-1" || remote.Host.CPUPercent != 62.5 || remote.Host.Cores != 4 || remote.Host.MemoryUsed != 4000 || remote.Host.MemoryPercent != 25 || remote.Host.Load1 != 0.75 || remote.Host.Load5 != 0.5 || remote.Host.Load15 != 0.25 || remote.Host.NetworkRXBytes != 100 || remote.Host.NetworkTXBytes != 200 || remote.Host.DiskUsed != 40000 {
		t.Fatalf("remote snapshot = %#v", remote)
	}
	if len(remote.Services) != 1 || remote.Services[0].State != "active" {
		t.Fatalf("remote services = %#v", remote.Services)
	}
	model := tuiModel{snapshot: remote, selectedTargetID: "edge-1"}
	if overview := model.renderOverview(116); !strings.Contains(overview, "Architecture") || !strings.Contains(overview, "arm64") || !strings.Contains(overview, "CPU cores") || !strings.Contains(overview, "100 B") || !strings.Contains(overview, "200 B") {
		t.Fatalf("remote overview omitted architecture:\n%s", overview)
	}
	if fleet := model.renderServers(116, 20); !strings.Contains(fleet, "agent/arm64") {
		t.Fatalf("server fleet omitted architecture:\n%s", fleet)
	}
}

func TestLoadRemoteTUIHostMetricsWithholdsUnavailableOrStaleObservations(t *testing.T) {
	t.Parallel()
	var mode atomic.Int32
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.URL.Path != "/api/nodes/edge-1/metrics" {
			t.Errorf("request path = %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch mode.Load() {
		case 1:
			writer.WriteHeader(http.StatusConflict)
			_, _ = writer.Write([]byte(`{"error":"capability_unavailable"}`))
		case 2:
			writer.WriteHeader(http.StatusConflict)
			_, _ = writer.Write([]byte(`{"error":"managed_node_offline"}`))
		case 3:
			writer.WriteHeader(http.StatusBadGateway)
			_, _ = writer.Write([]byte(`{"error":"managed_metrics_failed"}`))
		default:
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"observed_at": time.Now().Add(-agenthub.MetricsSnapshotMaxAge - time.Second).UTC().Format(time.RFC3339Nano),
				"cpu":         map[string]any{"usage_percent": 1.0, "core_count": 1},
				"load":        map[string]any{"one": 0.1, "five": 0.1, "fifteen": 0.1},
				"memory":      map[string]any{"total_bytes": 100, "used_bytes": 10, "available_bytes": 90, "usage_percent": 10.0},
				"network":     map[string]any{"rx_bytes": 1, "tx_bytes": 2},
				"root_disk":   map[string]any{"total_bytes": 100, "used_bytes": 10, "available_bytes": 90, "usage_percent": 10.0},
			})
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name         string
		mode         int32
		target       tuiTarget
		wantWarning  string
		wantRequests int32
	}{
		{name: "missing capability", target: tuiTarget{ID: "edge-1", Online: true}, wantWarning: "metrics.read"},
		{name: "offline", target: tuiTarget{ID: "edge-1", Online: false, Capabilities: map[string]bool{agenthub.CapabilityMetricsRead: true}}, wantWarning: "offline"},
		{name: "capability conflict", mode: 1, target: tuiTarget{ID: "edge-1", Online: true, Capabilities: map[string]bool{agenthub.CapabilityMetricsRead: true}}, wantWarning: "metrics.read", wantRequests: 1},
		{name: "offline conflict", mode: 2, target: tuiTarget{ID: "edge-1", Online: true, Capabilities: map[string]bool{agenthub.CapabilityMetricsRead: true}}, wantWarning: "offline", wantRequests: 1},
		{name: "server failure", mode: 3, target: tuiTarget{ID: "edge-1", Online: true, Capabilities: map[string]bool{agenthub.CapabilityMetricsRead: true}}, wantWarning: "HTTP 502", wantRequests: 1},
		{name: "stale observation", target: tuiTarget{ID: "edge-1", Online: true, Capabilities: map[string]bool{agenthub.CapabilityMetricsRead: true}}, wantWarning: "stale", wantRequests: 1},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			mode.Store(test.mode)
			before := requests.Load()
			snapshot := tuiSnapshot{Selected: test.target}
			loadRemoteTUIHostMetrics(context.Background(), client, &snapshot)
			if snapshot.HostAvailable {
				t.Fatalf("host metrics reported available: %#v", snapshot.Host)
			}
			if len(snapshot.Warnings) == 0 || !strings.Contains(strings.ToLower(snapshot.Warnings[len(snapshot.Warnings)-1]), strings.ToLower(test.wantWarning)) {
				t.Fatalf("warnings = %#v, want %q", snapshot.Warnings, test.wantWarning)
			}
			if got := requests.Load() - before; got != test.wantRequests {
				t.Fatalf("metrics requests = %d, want %d", got, test.wantRequests)
			}
		})
	}
}

func TestTUIRejectsLateSnapshotForPreviousTarget(t *testing.T) {
	t.Parallel()
	oldTarget := tuiTarget{ID: "edge-old", Name: "Old", Online: true}
	newTarget := tuiTarget{ID: "edge-new", Name: "New", Online: true}
	model := tuiModel{
		selectedTargetID: newTarget.ID,
		loading:          true,
		snapshot:         tuiSnapshot{Selected: newTarget},
	}
	updated, _ := model.Update(tuiLoadMsg{
		TargetID: oldTarget.ID,
		Snapshot: tuiSnapshot{Selected: oldTarget, HostAvailable: true, Host: tuiHostSummary{CPUPercent: 99}},
	})
	got := updated.(tuiModel)
	if got.snapshot.Selected.ID != newTarget.ID || got.snapshot.HostAvailable || got.snapshot.Host.CPUPercent != 0 {
		t.Fatalf("late snapshot replaced current target: %#v", got.snapshot)
	}
}

func TestLoadTUISnapshotKeepsLocalInventoriesUsableWhenMetricsFail(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/nodes":
			_, _ = writer.Write([]byte(`[]`))
		case "/api/system/stats":
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"error":"metrics collector unavailable"}`))
		case "/api/system/services":
			_, _ = writer.Write([]byte(`[{"name":"nginx","status":"running","detail":"active","pid":42}]`))
		case "/api/monitoring/processes":
			_, _ = writer.Write([]byte(`[{"pid":7,"startTime":77,"user":"root","cpu":3.5,"memory":1.2,"rss":4096,"command":"hserver"}]`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := loadTUISnapshot(context.Background(), client, localTargetID, initialTUITargets(), false)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.HostAvailable {
		t.Fatal("host metrics reported available after a failed stats request")
	}
	if !snapshot.ServicesAvailable || len(snapshot.Services) != 1 || snapshot.Services[0].Name != "nginx" {
		t.Fatalf("services = %#v, available=%v", snapshot.Services, snapshot.ServicesAvailable)
	}
	if !snapshot.ProcessesAvailable || len(snapshot.Processes) != 1 || snapshot.Processes[0].PID != 7 {
		t.Fatalf("processes = %#v, available=%v", snapshot.Processes, snapshot.ProcessesAvailable)
	}
	if len(snapshot.Warnings) == 0 || !strings.Contains(snapshot.Warnings[0], "Panel-host metrics unavailable") {
		t.Fatalf("warnings = %#v", snapshot.Warnings)
	}

	model := tuiModel{snapshot: snapshot, width: 120, height: 30}
	if rendered := model.renderOverview(116); !strings.Contains(rendered, "Unavailable") || !strings.Contains(rendered, "1 running") {
		t.Fatalf("overview did not preserve partial availability:\n%s", rendered)
	}
}

func TestTUIEmptyInventoryDistinguishesUnavailableFromEmpty(t *testing.T) {
	t.Parallel()
	model := tuiModel{snapshot: tuiSnapshot{ServicesAvailable: false, ProcessesAvailable: true}}
	if rendered := model.renderServices(100, 20); !strings.Contains(rendered, "Service inventory is unavailable") {
		t.Fatalf("service failure state = %q", rendered)
	}
	if rendered := model.renderProcesses(100, 20); !strings.Contains(rendered, "No processes were returned") {
		t.Fatalf("empty process state = %q", rendered)
	}
}

func TestTUIMutationRequiresExplicitYConfirmation(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/api/system/actions/memory-optimize" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{"message": "Memory optimized"})
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	model := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
	model.loading = false
	model.tab = tuiTabMaintenance
	model.snapshot.Selected = model.snapshot.Targets[0]

	updated, command := model.updateKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || requests.Load() != 0 {
		t.Fatalf("first enter: dialog=%v command=%v requests=%d", model.dialog.Mode, command != nil, requests.Load())
	}
	updated, command = model.updateKey("enter")
	model = updated.(tuiModel)
	if command != nil || requests.Load() != 0 {
		t.Fatalf("enter confirmed mutation: command=%v requests=%d", command != nil, requests.Load())
	}
	updated, command = model.updateKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("explicit y did not start the operation")
	}
	message := command()
	result, ok := message.(tuiOperationMsg)
	if !ok || result.Err != nil || result.Message != "Memory optimized" {
		t.Fatalf("operation result = %#v", message)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestTUIRemoteDiskSelectionHonorsAgentLimit(t *testing.T) {
	t.Parallel()
	target := tuiTarget{
		ID: "edge-1", Name: "Edge", Online: true,
		Capabilities: map[string]bool{agenthub.CapabilityDiskCleanup: true},
	}
	model := tuiModel{
		tab: tuiTabDisk, selectedTargetID: target.ID, diskSelected: map[string]bool{},
		snapshot: tuiSnapshot{Selected: target, CleanupLoaded: true},
	}
	for index := 0; index < 5; index++ {
		model.snapshot.CleanupTargets = append(model.snapshot.CleanupTargets, tuiCleanupTarget{ID: string(rune('a' + index))})
		model.cursor = index
		model.toggleDiskTarget()
	}
	if len(model.diskSelected) != 4 {
		t.Fatalf("selected = %#v", model.diskSelected)
	}
	if !model.noticeError || !strings.Contains(model.notice, "at most 4") {
		t.Fatalf("notice = %q, error=%v", model.notice, model.noticeError)
	}
}

func TestLoadTUIContainersAndLogsForLocalTarget(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/docker/status":
			_, _ = writer.Write([]byte(`{"installed":true,"running":true}`))
		case "/api/docker/containers":
			_, _ = writer.Write([]byte(`[{
				"id":"abc123","name":"web","image":"nginx:1.27","status":"running","detail":"Up 2 hours",
				"ports":["0.0.0.0:80->80/tcp"],"cpuPercent":2.5,"memoryUsage":1048576,"memoryLimit":67108864
			}]`))
		case "/api/logs/sources":
			_, _ = writer.Write([]byte(`{"sources":[
				{"path":"/var/log/app & api.log","category":"application","label":"Application","sizeBytes":2048,"readable":true},
				{"path":"/var/log/private.log","category":"application","label":"Private","sizeBytes":10,"readable":false}
			]}`))
		case "/api/logs/read":
			if request.URL.Query().Get("path") != "/var/log/app & api.log" || request.URL.Query().Get("lines") != "200" {
				t.Errorf("log query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"path":"/var/log/app & api.log","lines":["ready","request complete"],"total":2}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	target := initialTUITargets()[0]
	containers, err := loadTUIContainers(context.Background(), client, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 || containers[0].Name != "web" || containers[0].Ports != "0.0.0.0:80->80/tcp" || containers[0].MemoryLimit != 67108864 {
		t.Fatalf("containers = %#v", containers)
	}
	sources, err := loadTUILogSources(context.Background(), client, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].Label != "Application" || sources[0].Detail != "2.0 KiB" {
		t.Fatalf("sources = %#v", sources)
	}
	lines, err := loadTUILogLines(context.Background(), client, target, sources[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(lines, "|") != "ready|request complete" {
		t.Fatalf("lines = %#v", lines)
	}
}

func TestLoadTUIContainersAndLogsForManagedTarget(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/nodes/edge-1/containers":
			_, _ = writer.Write([]byte(`[{"id":"web-1","name":"web","image":"nginx:alpine","state":"running","status":"Up 1 hour","ports":"80/tcp"}]`))
		case "/api/nodes/edge-1/logs":
			if request.URL.Query().Get("source") != "nginx" || request.URL.Query().Get("lines") != "200" {
				t.Errorf("log query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`[{"timestamp":"2026-08-27T10:00:00Z","unit":"nginx.service","priority":6,"message":"request complete"}]`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	target := tuiTarget{
		ID: "edge-1", Name: "Edge", Online: true,
		Capabilities: map[string]bool{
			agenthub.CapabilityContainerRead: true,
			agenthub.CapabilityLogsRead:      true,
		},
		Inventory: agenthub.Inventory{LogSources: []string{"nginx"}},
	}
	containers, err := loadTUIContainers(context.Background(), client, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 || containers[0].State != "running" || containers[0].Detail != "Up 1 hour" {
		t.Fatalf("containers = %#v", containers)
	}
	sources, err := loadTUILogSources(context.Background(), client, target)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].ID != "nginx" || sources[0].Label != "Nginx" {
		t.Fatalf("sources = %#v", sources)
	}
	lines, err := loadTUILogLines(context.Background(), client, target, sources[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || lines[0] != "2026-08-27T10:00:00Z nginx.service  request complete" {
		t.Fatalf("lines = %#v", lines)
	}

	target.Capabilities = map[string]bool{}
	if _, err := loadTUIContainers(context.Background(), client, target); err == nil || !strings.Contains(err.Error(), "container.read") {
		t.Fatalf("missing capability error = %v", err)
	}
}

func TestTUIContainerMutationRequiresConfirmation(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/api/docker/containers/abc123/restart" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		_ = json.NewEncoder(writer).Encode(map[string]string{"status": "ok"})
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	model := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
	model.loading = false
	model.tab = tuiTabContainers
	model.containersLoaded = true
	model.containers = []tuiContainer{{ID: "abc123", Name: "web", Image: "nginx:1.27", State: "running"}}
	model.snapshot.Selected = model.snapshot.Targets[0]

	updated, command := model.updateKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogChoices || requests.Load() != 0 {
		t.Fatalf("action menu: dialog=%v command=%v requests=%d", model.dialog.Mode, command != nil, requests.Load())
	}
	updated, command = model.updateDialogKey("down")
	model = updated.(tuiModel)
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || model.dialog.Operation.Action != "restart" || requests.Load() != 0 {
		t.Fatalf("confirmation: dialog=%v action=%q command=%v requests=%d", model.dialog.Mode, model.dialog.Operation.Action, command != nil, requests.Load())
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || requests.Load() != 0 {
		t.Fatalf("enter confirmed mutation: command=%v requests=%d", command != nil, requests.Load())
	}
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("explicit y did not start the container operation")
	}
	message := command()
	result, ok := message.(tuiOperationMsg)
	if !ok || result.Err != nil || result.Message != "Container restart completed for web" {
		t.Fatalf("operation result = %#v", message)
	}
	if requests.Load() != 1 {
		t.Fatalf("requests = %d", requests.Load())
	}
}

func TestTUILogViewerNavigationAndSanitization(t *testing.T) {
	t.Parallel()
	lines := make([]string, 25)
	for index := range lines {
		lines[index] = "line " + string(rune('a'+index))
	}
	lines[0] = "safe\x1b]52;c;payload\a log"
	model := tuiModel{
		width: 100, height: 30, tab: tuiTabLogs,
		dialog: tuiDialog{Mode: tuiDialogLogs, Title: "Logs · System", LogLines: lines, LogScroll: 24},
	}
	updated, _ := model.updateDialogKey("home")
	model = updated.(tuiModel)
	if model.dialog.LogScroll != 0 {
		t.Fatalf("home scroll = %d", model.dialog.LogScroll)
	}
	content := model.View().Content
	if !strings.Contains(content, "safe]52;c;payload log") || strings.Contains(content, "\x1b]52;c;payload") {
		t.Fatalf("log view = %q", content)
	}
	updated, _ = model.updateDialogKey("shift+g")
	model = updated.(tuiModel)
	if model.dialog.LogScroll != 24 {
		t.Fatalf("end scroll = %d", model.dialog.LogScroll)
	}
	updated, _ = model.updateDialogKey("pgup")
	model = updated.(tuiModel)
	if model.dialog.LogScroll != 14 {
		t.Fatalf("page-up scroll = %d", model.dialog.LogScroll)
	}
	updated, _ = model.updateDialogKey("q")
	if updated.(tuiModel).dialog.Mode != tuiDialogNone {
		t.Fatal("q did not close the log viewer")
	}
}

func TestTUIViewSanitizesServerControlledTerminalText(t *testing.T) {
	t.Parallel()
	targets := initialTUITargets()
	model := tuiModel{
		width: 100, height: 30, tab: tuiTabProcesses, selectedTargetID: localTargetID,
		snapshot: tuiSnapshot{
			Targets: targets, Selected: targets[0], FetchedAt: time.Now(),
			Processes: []tuiProcess{{PID: 42, StartTime: 9, User: "root", Command: "safe\x1b]52;c;payload\a command"}},
		},
		diskSelected: map[string]bool{},
	}
	content := model.View().Content
	if !strings.Contains(content, "HSERVER") || !strings.Contains(content, "safe]52;c;payload command") {
		t.Fatalf("view = %q", content)
	}
	if strings.Contains(content, "\x1b]52;c;payload") {
		t.Fatal("server-controlled OSC sequence reached rendered content")
	}
}

func TestTUIProgressBarRemainsReadableWithoutColor(t *testing.T) {
	t.Parallel()
	bar := renderProgressBar(25, 20)
	if !strings.Contains(bar, "━━━━━") || !strings.Contains(bar, "───────────────") {
		t.Fatalf("bar = %q", bar)
	}
}

func TestTUIHeaderIdentitySeparatesContextPanelTargetAndScope(t *testing.T) {
	t.Parallel()
	client, err := newAPIClient("https://panel.example.com/", "bearer-secret-value", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	model := newTUIModelWithContext(context.Background(), client, "production", "https://ignored.example.com/secret?token=should-not-render", 5*time.Second)
	model.loading = false

	local := model.renderHeader(72)
	for _, want := range []string{
		"Context: production",
		"Panel: https://panel.example.com",
		"Target: Panel host · Local",
		"ONLINE",
	} {
		if !strings.Contains(local, want) {
			t.Fatalf("local header missing %q:\n%s", want, local)
		}
	}
	if strings.Contains(local, "bearer-secret-value") || strings.Contains(local, "should-not-render") {
		t.Fatalf("local header leaked secret or token path: %q", local)
	}

	model.snapshot.Selected = tuiTarget{ID: "edge-1", Name: "Managed Edge", Online: true}
	managed := model.renderHeader(72)
	for _, want := range []string{"Context: production", "Panel: https://panel.example.com", "Target: Managed Edge · Managed", "ONLINE"} {
		if !strings.Contains(managed, want) {
			t.Fatalf("managed header missing %q:\n%s", want, managed)
		}
	}

	model.contextName = "production-context-with-a-name-that-needs-truncation"
	model.serverURL = "https://panel.example.com/with-a-long-display-name"
	model.snapshot.Selected.Name = "managed-target-with-a-name-that-needs-truncation"
	narrow := model.renderHeader(44)
	for _, line := range strings.Split(narrow, "\n") {
		if got := lipgloss.Width(line); got > 44 {
			t.Fatalf("narrow header line width = %d:\n%s", got, narrow)
		}
	}
	if !strings.Contains(narrow, "Context:") || !strings.Contains(narrow, "Panel:") || !strings.Contains(narrow, "Target:") || !strings.Contains(narrow, "ONLINE") {
		t.Fatalf("narrow header dropped identity or badge:\n%s", narrow)
	}
}

func TestRunUIValidatesRefreshBeforeStartingTerminal(t *testing.T) {
	t.Parallel()
	err := run(context.Background(), []string{"ui", "--refresh", "500ms"}, &bytes.Buffer{}, &bytes.Buffer{}, func(key string) string {
		if key == "HSERVER_TOKEN" {
			return "test-token"
		}
		return ""
	})
	if err == nil || !strings.Contains(err.Error(), "at least one second") {
		t.Fatalf("error = %v", err)
	}

	var output bytes.Buffer
	if err := run(context.Background(), []string{"help"}, &output, &bytes.Buffer{}, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "ui [--refresh DURATION]") {
		t.Fatalf("help = %q", output.String())
	}
}

func TestTUIModelHandlesWindowResize(t *testing.T) {
	t.Parallel()
	model := newTUIModel(context.Background(), nil, "http://127.0.0.1:3085", 5*time.Second)
	updated, _ := model.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	resized := updated.(tuiModel)
	if resized.width != 120 || resized.height != 40 {
		t.Fatalf("size = %dx%d", resized.width, resized.height)
	}
}

func tuiPTYTranscriptContains(transcript, needle string) bool {
	return strings.Contains(ansi.Strip(transcript), needle)
}

func TestTUIPTYTranscriptMatcherHandlesANSIUpdates(t *testing.T) {
	const transcript = "\x1b[15;12Hpty-\x1b[4hhost      \x1b[4l"
	if strings.Contains(transcript, "pty-host") {
		t.Fatal("fixture must contain an ANSI-split hostname")
	}
	if !tuiPTYTranscriptContains(transcript, "pty-host") {
		t.Fatalf("ANSI-stripped transcript = %q", ansi.Strip(transcript))
	}
}

func TestTUIRunsInsidePseudoTerminal(t *testing.T) {
	const userPassword = "terminal-only-secret"
	userCreated := make(chan map[string]string, 1)
	var userExists atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/nodes":
			_, _ = writer.Write([]byte(`[]`))
		case "/api/system/stats":
			_, _ = writer.Write([]byte(`{
				"cpu":{"usage":12.5,"cores":4,"model":"Example CPU"},
				"memory":{"total":8589934592,"used":2147483648,"available":6442450944,"swapTotal":0,"swapUsed":0},
				"disk":[{"mount":"/","total":107374182400,"used":26843545600,"free":80530636800,"percentage":25}],
				"load":[0.2,0.1,0.1],"uptime":3600,"hostname":"pty-host","os":"Ubuntu 24.04","network":[]
			}`))
		case "/api/system/services", "/api/monitoring/processes":
			_, _ = writer.Write([]byte(`[]`))
		case "/api/docker/status":
			_, _ = writer.Write([]byte(`{"installed":true,"running":true}`))
		case "/api/docker/containers":
			_, _ = writer.Write([]byte(`[{"id":"abc123","name":"demo-web","image":"nginx:1.27","status":"running","detail":"Up","ports":["80/tcp"],"cpuPercent":1.5,"memoryUsage":1048576,"memoryLimit":67108864}]`))
		case "/api/logs/sources":
			_, _ = writer.Write([]byte(`{"sources":[{"path":"/var/log/application.log","category":"application","label":"Application log","sizeBytes":2048,"readable":true}]}`))
		case "/api/logs/read":
			_, _ = writer.Write([]byte(`{"path":"/var/log/application.log","lines":["boot complete"],"total":1}`))
		case "/api/pm2/processes":
			_, _ = writer.Write([]byte(`[{"id":1,"name":"demo-api","status":"online","pid":42,"cpu":2.5,"memory":64,"uptime":120,"restarts":1,"mode":"fork_mode"}]`))
		case "/api/pm2/processes/1/logs":
			_, _ = writer.Write([]byte(`{"id":"1","lines":200,"output":["PM2 application ready"]}`))
		case "/api/nginx/configs":
			_, _ = writer.Write([]byte(`[{"filename":"site.conf","domain":"example.com","type":"proxy","isEnabled":true}]`))
		case "/api/domains":
			_, _ = writer.Write([]byte(`{"domains":[{"name":"example.com","type":"proxy","isActive":true}]}`))
		case "/api/ssl/certificates":
			_, _ = writer.Write([]byte(`[{"domain":"example.com","issuer":"Test CA","daysRemaining":60,"autoRenew":true}]`))
		case "/api/backups":
			_, _ = writer.Write([]byte(`{"backups":[{"id":"pty-backup","name":"pty-backup.tar.gz","type":"full","size":4096,"status":"completed","createdAt":"2026-08-27T10:00:00Z"}],"storage":{"totalBytes":4096,"completedBytes":4096,"invalidBytes":0,"orphanedBytes":0,"backupVolumeAvailable":1048576,"backupVolumeUsePercent":10}}`))
		case "/api/backups/jobs":
			_, _ = writer.Write([]byte(`{"jobs":[{"id":"pty-job","type":"full","source":"manual","status":"running","phase":"archive","progress":55,"message":"archiving","startedAt":"2026-08-27T10:01:00Z","logs":["backup started"]}]}`))
		case "/api/backups/targets":
			_, _ = writer.Write([]byte(`{"vhosts":["example.com"],"maxSelectedVhosts":16,"emptySelection":"all-configured-vhosts"}`))
		case "/api/firewall/status":
			_, _ = writer.Write([]byte(`{"available":true,"state":"healthy","backend":"ufw","active":true,"defaultIncoming":"deny","defaultOutgoing":"allow","rules":[{"number":1,"to":"22","action":"ALLOW","direction":"IN","from":"Anywhere","protocol":"tcp","comment":"SSH"}]}`))
		case "/api/auth/me":
			_, _ = writer.Write([]byte(`{"id":11,"email":"admin@example.com","name":"Admin Operator","role":"admin","totpEnabled":true,"createdAt":"2026-08-27T08:00:00Z","updatedAt":"2026-08-27T08:01:00Z"}`))
		case "/api/users":
			if request.Method == http.MethodGet {
				if userExists.Load() {
					_, _ = writer.Write([]byte(`{"data":[{"id":33,"email":"pty@example.com","name":"PTY Operator","role":"manager","totpEnabled":false,"createdAt":"2026-08-27T09:00:00Z","updatedAt":"2026-08-27T09:00:00Z"},{"id":11,"email":"admin@example.com","name":"Admin Operator","role":"admin","totpEnabled":true,"createdAt":"2026-08-27T08:00:00Z","updatedAt":"2026-08-27T08:01:00Z"}],"total":2,"limit":200,"offset":0}`))
					return
				}
				_, _ = writer.Write([]byte(`{"data":[{"id":11,"email":"admin@example.com","name":"Admin Operator","role":"admin","totpEnabled":true,"createdAt":"2026-08-27T08:00:00Z","updatedAt":"2026-08-27T08:01:00Z"}],"total":1,"limit":200,"offset":0}`))
				return
			}
			if request.Method != http.MethodPost {
				http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			payload := make(map[string]string)
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				http.Error(writer, "invalid JSON", http.StatusBadRequest)
				return
			}
			userCreated <- payload
			userExists.Store(true)
			writer.WriteHeader(http.StatusCreated)
			_, _ = writer.Write([]byte(`{"id":33,"email":"pty@example.com","name":"PTY Operator","role":"manager","totpEnabled":false,"createdAt":"2026-08-27T09:00:00Z","updatedAt":"2026-08-27T09:00:00Z"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	command := exec.Command(os.Args[0], "-test.run=^TestTUIHelperProcess$")
	command.Env = append(os.Environ(), "HSERVER_TUI_HELPER=1", "HSERVER_TUI_HELPER_URL="+server.URL)
	terminal, err := pty.StartWithSize(command, &pty.Winsize{Rows: 32, Cols: 110})
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()
	var output bytes.Buffer
	chunks := make(chan []byte, 32)
	readErrors := make(chan error, 1)
	go func() {
		buffer := make([]byte, 4096)
		for {
			count, readErr := terminal.Read(buffer)
			if count > 0 {
				chunk := append([]byte(nil), buffer[:count]...)
				chunks <- chunk
			}
			if readErr != nil {
				readErrors <- readErr
				return
			}
		}
	}()
	readUntil := func(needle string) {
		t.Helper()
		timer := time.NewTimer(12 * time.Second)
		defer timer.Stop()
		drain := func() {
			for {
				select {
				case chunk := <-chunks:
					_, _ = output.Write(chunk)
				default:
					return
				}
			}
		}
		for !tuiPTYTranscriptContains(output.String(), needle) {
			select {
			case chunk := <-chunks:
				_, _ = output.Write(chunk)
			case readErr := <-readErrors:
				t.Fatalf("read TUI: %v; output=%q", readErr, strings.ReplaceAll(output.String(), userPassword, "[MASKED TEST SECRET]"))
			case <-timer.C:
				// A PTY update can be split across ANSI cursor/style sequences.
				// Drain chunks already read before deciding the render timed out.
				drain()
				if tuiPTYTranscriptContains(output.String(), needle) {
					return
				}
				t.Fatalf("TUI did not render %q; output=%q", needle, strings.ReplaceAll(output.String(), userPassword, "[MASKED TEST SECRET]"))
			}
		}
	}
	readUntil("pty-host")
	if !strings.Contains(output.String(), "HSERVER") || !strings.Contains(output.String(), "CONTROL CENTER") {
		t.Fatalf("TUI output = %q", output.String())
	}
	if _, err := terminal.Write([]byte("7")); err != nil {
		t.Fatal(err)
	}
	readUntil("demo-web")
	if _, err := terminal.Write([]byte("8")); err != nil {
		t.Fatal(err)
	}
	readUntil("Application log")
	if _, err := terminal.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	readUntil("boot complete")
	if _, err := terminal.Write([]byte("q")); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.Write([]byte("9")); err != nil {
		t.Fatal(err)
	}
	readUntil("demo-api")
	if _, err := terminal.Write([]byte("v")); err != nil {
		t.Fatal(err)
	}
	readUntil("PM2 application ready")
	if _, err := terminal.Write([]byte("q")); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.Write([]byte("0")); err != nil {
		t.Fatal(err)
	}
	readUntil("site.conf")
	if _, err := terminal.Write([]byte{0x0b}); err != nil {
		t.Fatal(err)
	}
	readUntil("Quick actions")
	if _, err := terminal.Write([]byte("swap")); err != nil {
		t.Fatal(err)
	}
	readUntil("Reset swap")
	if _, err := terminal.Write([]byte{0x0b}); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.Write([]byte("f")); err != nil {
		t.Fatal(err)
	}
	readUntil("backend ufw")
	if _, err := terminal.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	readUntil("Add common inbound firewall rule")
	if _, err := terminal.Write([]byte("q")); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	readUntil("JOB full")
	if _, err := terminal.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	readUntil("backup started")
	if _, err := terminal.Write([]byte("q")); err != nil {
		t.Fatal(err)
	}
	readUntil("pty-backup.tar.gz")
	if _, err := terminal.Write([]byte("c")); err != nil {
		t.Fatal(err)
	}
	readUntil("Create local backup")
	if _, err := terminal.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}
	readUntil("Backup file scope")
	if _, err := terminal.Write([]byte("q")); err != nil {
		t.Fatal(err)
	}
	if _, err := terminal.Write([]byte("I")); err != nil {
		t.Fatal(err)
	}
	readUntil("admin@example.com")
	if _, err := terminal.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	readUntil("Create central panel user")
	if _, err := terminal.Write([]byte("pty@example.com")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(40 * time.Millisecond)
	for _, input := range []string{"\t", "PTY Operator"} {
		if _, err := terminal.Write([]byte(input)); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(40 * time.Millisecond)
	for _, input := range []string{"\t", "\x1b[D"} {
		if _, err := terminal.Write([]byte(input)); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(40 * time.Millisecond)
	for _, input := range []string{"\t", userPassword, "\t", userPassword, "\r"} {
		if _, err := terminal.Write([]byte(input)); err != nil {
			t.Fatal(err)
		}
	}
	readUntil("The password remains masked")
	if strings.Contains(output.String(), userPassword) {
		t.Fatal("panel-user password reached the PTY transcript")
	}
	if _, err := terminal.Write([]byte("y")); err != nil {
		t.Fatal(err)
	}
	readUntil("2 loaded · 2 total")
	select {
	case payload := <-userCreated:
		if len(payload) != 4 || payload["email"] != "pty@example.com" || payload["name"] != "PTY Operator" ||
			payload["role"] != "manager" || payload["password"] != userPassword {
			t.Fatalf("panel-user create payload email=%q name=%q role=%q password_match=%t fields=%d",
				payload["email"], payload["name"], payload["role"], payload["password"] == userPassword, len(payload))
		}
	default:
		t.Fatal("panel-user create request was not observed")
	}
	if strings.Contains(output.String(), userPassword) {
		t.Fatal("panel-user password reached the PTY transcript after confirmation")
	}
	if _, err := terminal.Write([]byte("q")); err != nil {
		t.Fatal(err)
	}
	if err := command.Wait(); err != nil {
		t.Fatalf("TUI exit: %v; output=%q", err, output.String())
	}
}

func TestTUIHelperProcess(t *testing.T) {
	if os.Getenv("HSERVER_TUI_HELPER") != "1" {
		return
	}
	client, err := newAPIClient(os.Getenv("HSERVER_TUI_HELPER_URL"), "test-token", 2*time.Second)
	if err != nil {
		_, _ = io.WriteString(os.Stderr, err.Error())
		os.Exit(2)
	}
	if err := runUI(context.Background(), client, nil, os.Getenv("HSERVER_TUI_HELPER_URL"), os.Stdout); err != nil {
		_, _ = io.WriteString(os.Stderr, err.Error())
		os.Exit(2)
	}
	os.Exit(0)
}
