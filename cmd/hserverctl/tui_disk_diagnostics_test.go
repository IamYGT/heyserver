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

	"github.com/IamYGT/heyserver/internal/agenthub"
)

func TestLoadTUIDiskDiagnosticsSummaryUsesLocalEndpointsAndBoundsRows(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodGet {
			t.Errorf("method = %s, want GET", request.Method)
		}
		if body, err := io.ReadAll(request.Body); err != nil || len(body) != 0 {
			t.Errorf("request body = %q (err=%v)", body, err)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/disk/mounts":
			_, _ = io.WriteString(writer, `[ {"device":"/dev/sda1","mountPoint":"/","fsType":"ext4","options":"rw","source":"active"} ]`)
		case "/api/disk/io":
			_, _ = io.WriteString(writer, `[{"device":"sda","readsCompleted":4,"writesCompleted":8,"readBytes":4096,"writeBytes":8192}]`)
		case "/api/disk/smart/root":
			_, _ = io.WriteString(writer, `{"available":true,"healthy":true,"device":"/dev/sda","model":"Safe model","serial":"do-not-render","status":"PASSED","rawOutput":"do-not-render"}`)
		case "/api/disk/analysis/status":
			_, _ = io.WriteString(writer, `{"status":"completed","message":"done","entries":[{"path":"/var","size":100}]}`)
		default:
			t.Errorf("unexpected summary endpoint %s", request.URL.Path)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client := newDiskDiagnosticsTestClient(t, server.URL)
	state, err := loadTUIDiskDiagnostics(context.Background(), client, initialTUITargets()[0], tuiDiskDefaultPath)
	if err != nil {
		t.Fatalf("load summary: %v", err)
	}
	if !state.Loaded || !state.Supported || state.Path != tuiDiskDefaultPath {
		t.Fatalf("summary state = %#v", state)
	}
	if !state.MountsLoaded || len(state.Mounts) != 1 || !state.IOLoaded || len(state.IO) != 1 || !state.SMARTLoaded || state.SMART == nil || state.SMART.Device != "/dev/sda" || !state.AnalysisLoaded {
		t.Fatalf("summary projections = %#v", state)
	}
	if state.SMART.Model == "do-not-render" || strings.Contains(state.SMART.Message, "rawOutput") {
		t.Fatalf("SMART projection leaked raw/sensitive fields: %#v", state.SMART)
	}
	if state.UsageLoaded || state.LargestLoaded {
		t.Fatal("summary unexpectedly ran recursive usage/largest scans")
	}
	if requests.Load() != 4 {
		t.Fatalf("summary requests = %d, want 4", requests.Load())
	}
}

func TestLoadTUIDiskDiagnosticsManagedTargetIsUnsupportedWithoutLocalRequests(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected local disk request", http.StatusInternalServerError)
	}))
	defer server.Close()
	client := newDiskDiagnosticsTestClient(t, server.URL)
	target := tuiTarget{ID: "edge-1", Name: "Edge", Online: true, Capabilities: map[string]bool{agenthub.CapabilityDiskCleanup: true}}

	state, err := loadTUIDiskDiagnostics(context.Background(), client, target, tuiDiskDefaultPath)
	if err != nil || !state.Loaded || state.Supported || !strings.Contains(state.UnsupportedNote, "managed") {
		t.Fatalf("managed summary state=%#v err=%v", state, err)
	}
	for _, kind := range []tuiDiskDiagnosticKind{tuiDiskDiagnosticMounts, tuiDiskDiagnosticUsage, tuiDiskDiagnosticLargest, tuiDiskDiagnosticIO, tuiDiskDiagnosticSMART, tuiDiskDiagnosticAnalysis} {
		state, err = loadTUIDiskDiagnostic(context.Background(), client, target, kind, tuiDiskDefaultPath)
		if err != nil || state.Supported {
			t.Fatalf("managed %s state=%#v err=%v", kind, state, err)
		}
	}
	if requests.Load() != 0 {
		t.Fatalf("managed target caused %d local endpoint request(s)", requests.Load())
	}
}

func TestLoadTUIDiskUsageAndLargestRequireExplicitSafePathAndBoundRequests(t *testing.T) {
	t.Parallel()
	var usageRequests, largestRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/disk/usage":
			usageRequests.Add(1)
			if request.URL.Query().Get("path") != "/var" || request.URL.Query().Get("depth") != "1" {
				t.Errorf("usage query = %v", request.URL.Query())
			}
			rows := make([]string, 0, tuiDiskMaxUsageRows+8)
			for index := 0; index < tuiDiskMaxUsageRows+8; index++ {
				rows = append(rows, `{"path":"/var/entry-`+string(rune('a'+index%26))+`","size":100}`)
			}
			_, _ = io.WriteString(writer, `[`+strings.Join(rows, ",")+`]`)
		case "/api/disk/largest":
			largestRequests.Add(1)
			if request.URL.Query().Get("path") != "/var" || request.URL.Query().Get("limit") != "20" {
				t.Errorf("largest query = %v", request.URL.Query())
			}
			rows := make([]string, 0, tuiDiskMaxLargestRows+8)
			for index := 0; index < tuiDiskMaxLargestRows+8; index++ {
				rows = append(rows, `{"path":"/var/file-`+string(rune('a'+index%26))+`","size":100}`)
			}
			_, _ = io.WriteString(writer, `[`+strings.Join(rows, ",")+`]`)
		default:
			t.Errorf("unexpected endpoint %s", request.URL.Path)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client := newDiskDiagnosticsTestClient(t, server.URL)
	target := initialTUITargets()[0]

	usage, err := loadTUIDiskDiagnostic(context.Background(), client, target, tuiDiskDiagnosticUsage, "/var")
	if err != nil || !usage.UsageLoaded || usage.Path != "/var" || len(usage.Usage) != tuiDiskMaxUsageRows {
		t.Fatalf("usage state=%#v err=%v", usage, err)
	}
	largest, err := loadTUIDiskDiagnostic(context.Background(), client, target, tuiDiskDiagnosticLargest, "/var")
	if err != nil || !largest.LargestLoaded || largest.Path != "/var" || len(largest.Largest) != tuiDiskMaxLargestRows {
		t.Fatalf("largest state=%#v err=%v", largest, err)
	}
	if _, err := loadTUIDiskDiagnostic(context.Background(), client, target, tuiDiskDiagnosticUsage, ""); err == nil || !strings.Contains(err.Error(), "explicit safe path") {
		t.Fatalf("pathless usage error = %v", err)
	}
	if _, err := loadTUIDiskDiagnostic(context.Background(), client, target, tuiDiskDiagnosticLargest, "/home/../proc"); err == nil || !strings.Contains(err.Error(), "fixed safe roots") {
		t.Fatalf("unsafe largest path error = %v", err)
	}
	if usageRequests.Load() != 1 || largestRequests.Load() != 1 {
		t.Fatalf("requests usage=%d largest=%d, want one each", usageRequests.Load(), largestRequests.Load())
	}
}

func TestDiskDiagnosticLinesAreBoundedAndTerminalSafe(t *testing.T) {
	attrs := make([]tuiDiskSMARTAttr, tuiDiskMaxSMARTAttrs+5)
	for index := range attrs {
		attrs[index] = tuiDiskSMARTAttr{ID: index, Name: "attribute\x1b[31m", Raw: "0\nunsafe"}
	}
	state := tuiDiskDiagnosticsState{
		Supported:   true,
		SMARTLoaded: true,
		SMART: &tuiDiskSMART{
			Available: true, Healthy: true, Device: "/dev/sda", Status: "PASSED", Attrs: attrs,
		},
	}
	lines := diskDiagnosticLines(tuiDiskDiagnosticSMART, state)
	if len(lines) > tuiDiskMaxSMARTAttrs+12 {
		t.Fatalf("SMART lines = %d, want bounded output", len(lines))
	}
	for _, line := range lines {
		if strings.ContainsAny(line, "\x00\x1b\n\r\t") {
			t.Fatalf("unsafe diagnostic line = %q", line)
		}
	}

	model := tuiModel{tab: tuiTabDisk, width: 72, height: 24, snapshot: tuiSnapshot{Selected: tuiTarget{Local: false}}}
	view := model.renderDisk(68, 18)
	if !strings.Contains(view, "unsupported") && !strings.Contains(view, "Unsupported") {
		t.Fatalf("managed disk view omitted unsupported state: %q", view)
	}
}

func TestTUIDiskAnalysisStartRequiresExplicitConfirmation(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/api/disk/analysis/start" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		if body, err := io.ReadAll(request.Body); err != nil || len(body) != 0 {
			t.Errorf("analysis start body = %q (err=%v)", body, err)
		}
		_, _ = io.WriteString(writer, `{"status":"queued","message":"queued","id":"disk-test"}`)
	}))
	defer server.Close()
	client := newDiskDiagnosticsTestClient(t, server.URL)
	model := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
	model.loading = false
	model.tab = tuiTabDisk
	model.snapshot.Selected = model.snapshot.Targets[0]

	updated, command := model.updateKey("d")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || model.dialog.DiskAnalysisStart == nil || requests.Load() != 0 {
		t.Fatalf("start confirmation = %#v command=%v requests=%d", model.dialog, command != nil, requests.Load())
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || requests.Load() != 0 {
		t.Fatal("Enter bypassed deep analysis confirmation")
	}
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating || requests.Load() != 0 {
		t.Fatalf("Y did not start queued analysis command=%v operating=%v requests=%d", command != nil, model.operating, requests.Load())
	}
	message, ok := command().(tuiDiskDiagnosticsMsg)
	if !ok || message.Err != nil || !message.Mutation || requests.Load() != 1 {
		t.Fatalf("analysis result = %#v requests=%d", message, requests.Load())
	}
	updated, command = model.Update(message)
	model = updated.(tuiModel)
	if command != nil || model.operating || model.dialog.Mode != tuiDialogNone || !strings.Contains(model.notice, "queued") {
		t.Fatalf("analysis result handling = dialog=%v operating=%v notice=%q command=%v", model.dialog.Mode, model.operating, model.notice, command != nil)
	}
}

func TestTUIDiskDiagnosticKeyDoesNotRequestManagedEndpoint(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		http.Error(writer, "unexpected managed local endpoint", http.StatusInternalServerError)
	}))
	defer server.Close()
	client := newDiskDiagnosticsTestClient(t, server.URL)
	model := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
	model.loading = false
	model.tab = tuiTabDisk
	model.snapshot.Selected = tuiTarget{ID: "edge-1", Name: "Edge", Online: true, Local: false}
	model.selectedTargetID = "edge-1"
	model.diskDiagnostics = tuiDiskDiagnosticsState{Loaded: true, Supported: false, UnsupportedNote: "managed unsupported"}
	model.diskDiagnosticsTarget = "edge-1"

	for _, key := range []string{"m", "u", "w", "i", "p", "a", "d"} {
		updated, command := model.updateKey(key)
		model = updated.(tuiModel)
		if command != nil || requests.Load() != 0 {
			t.Fatalf("managed key %q command=%v requests=%d", key, command != nil, requests.Load())
		}
	}
	if !strings.Contains(strings.ToLower(model.notice), "unsupported") {
		t.Fatalf("managed notice = %q", model.notice)
	}
}
