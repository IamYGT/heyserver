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

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/services/releaseupdates"
)

func TestLoadTUIUpdatesPreservesReleaseStateWhenStageIsUnavailable(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/system/update":
			_, _ = writer.Write([]byte(`{"status":"healthy","signature_status":"verified","current_version":"v1.0.0","latest_version":"v1.2.0","latest_version_state":"ahead","update_available":true,"platform":"linux_amd64","message":"A newer release is available.","checked_at":"2026-08-28T00:00:00Z"}`))
		case "/api/system/update/stage":
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"error":"stage storage unavailable"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	state, err := loadTUIUpdates(context.Background(), client, initialTUITargets()[0])
	if err != nil {
		t.Fatal(err)
	}
	if !state.Supported || state.ReleaseStatus != releaseupdates.StatusHealthy || state.SignatureStatus != releaseupdates.SignatureVerified || !state.UpdateAvailable || state.LatestVersion != "v1.2.0" {
		t.Fatalf("state = %#v", state)
	}
	if state.Stage != nil || len(state.Warnings) != 1 || !strings.Contains(state.Warnings[0], "stage storage unavailable") {
		t.Fatalf("partial stage state = %#v", state)
	}
}

func TestLoadTUIUpdatesKeepsManagedCapabilityBoundariesExplicit(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"release_status":"healthy","signature_status":"verified","current_version":"v1.0.0","latest_version":"v1.2.0","latest_version_state":"ahead","update_available":true,"platform":"linux_amd64","release_message":"A newer agent release is available.","release_checked_at":"2026-08-28T00:00:00Z","operation":"none","operation_status":"idle","operation_detail":"No lifecycle operation is active.","rollback_available":true}`))
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	offline, err := loadTUIUpdates(context.Background(), client, tuiTarget{ID: "offline", Online: false, Capabilities: map[string]bool{agenthub.CapabilityAgentUpdateRead: true}})
	if err != nil || offline.Supported || !strings.Contains(offline.ReleaseMessage, "offline") {
		t.Fatalf("offline=%#v err=%v", offline, err)
	}
	missing, err := loadTUIUpdates(context.Background(), client, tuiTarget{ID: "legacy", Online: true, Capabilities: map[string]bool{}})
	if err != nil || missing.Supported || !strings.Contains(missing.ReleaseMessage, agenthub.CapabilityAgentUpdateRead) {
		t.Fatalf("missing=%#v err=%v", missing, err)
	}
	if requests.Load() != 0 {
		t.Fatalf("unsupported managed targets sent %d request(s)", requests.Load())
	}

	healthy, err := loadTUIUpdates(context.Background(), client, tuiTarget{ID: "edge-1", Online: true, Capabilities: map[string]bool{agenthub.CapabilityAgentUpdateRead: true}})
	if err != nil || !healthy.Supported || healthy.ReleaseStatus != releaseupdates.StatusHealthy || !healthy.RollbackAvailable {
		t.Fatalf("healthy=%#v err=%v", healthy, err)
	}
	if requests.Load() != 1 {
		t.Fatalf("healthy managed target sent %d requests", requests.Load())
	}
}

func TestTUIUpdatesPanelMutationsRequireConfirmationAndFreshObservation(t *testing.T) {
	t.Parallel()
	var stagePosts atomic.Int32
	var installPosts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/system/update":
			_, _ = writer.Write([]byte(`{"status":"healthy","signature_status":"verified","current_version":"v1.0.0","latest_version":"v1.2.0","latest_version_state":"ahead","update_available":true,"platform":"linux_amd64","message":"ready","checked_at":"2026-08-28T00:00:00Z"}`))
		case "POST /api/system/update/stage":
			stagePosts.Add(1)
			body, _ := io.ReadAll(request.Body)
			if len(body) != 0 {
				t.Errorf("stage body = %q", body)
			}
			_, _ = writer.Write([]byte(`{"id":"v1.2.0-0123456789ab","version":"v1.2.0","current_version":"v1.0.0","platform":"linux_amd64","sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","size_bytes":1024,"status":"staged"}`))
		case "GET /api/system/update/stage":
			_, _ = writer.Write([]byte(`{"stage":{"id":"v1.2.0-0123456789ab","version":"v1.2.0","current_version":"v1.0.0","platform":"linux_amd64","sha256":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","size_bytes":1024,"status":"staged"}}`))
		case "POST /api/system/update/install":
			installPosts.Add(1)
			var payload map[string]any
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode install: %v", err)
			}
			if payload["stage_id"] != "v1.2.0-0123456789ab" || payload["version"] != "v1.2.0" || payload["confirmed"] != true || len(payload) != 3 {
				t.Errorf("install payload = %#v", payload)
			}
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"id":"v1.2.0-0123456789ab","version":"v1.2.0","status":"scheduled"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	stage := &releaseupdates.Stage{ID: "v1.2.0-0123456789ab", Version: "v1.2.0", Status: releaseupdates.StageStaged}
	state := tuiUpdateState{
		Supported: true, Local: true, ReleaseStatus: releaseupdates.StatusHealthy, SignatureStatus: releaseupdates.SignatureVerified,
		CurrentVersion: "v1.0.0", LatestVersion: "v1.2.0", UpdateAvailable: true, Stage: stage,
	}
	model := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
	model.loading = false
	model.tab = tuiTabUpdates
	model.snapshot.Selected = model.snapshot.Targets[0]
	model.updates, model.updatesTarget, model.updatesLoaded = state, localTargetID, true

	updated, command := model.updateKey("s")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || stagePosts.Load() != 0 {
		t.Fatalf("stage confirmation=%#v posts=%d", model.dialog, stagePosts.Load())
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || stagePosts.Load() != 0 {
		t.Fatal("Enter bypassed release stage confirmation")
	}
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("Y did not start release staging")
	}
	message := command().(tuiOperationMsg)
	if message.Err != nil || stagePosts.Load() != 1 || !strings.Contains(message.Message, "staged and verified") {
		t.Fatalf("stage message=%#v posts=%d", message, stagePosts.Load())
	}

	messageText, err := runTUIUpdateOperation(context.Background(), client, tuiOperation{Kind: tuiOperationUpdate, Target: initialTUITargets()[0], Action: "install", Update: state})
	if err != nil || installPosts.Load() != 1 || !strings.Contains(messageText, "automatic rollback") {
		t.Fatalf("install message=%q err=%v posts=%d", messageText, err, installPosts.Load())
	}
}

func TestTUIUpdatesManagedMutationsReobserveAndUseExactPayloads(t *testing.T) {
	t.Parallel()
	var upgradePosts atomic.Int32
	var rollbackPosts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/nodes/edge-1/agent-update":
			_, _ = writer.Write([]byte(`{"release_status":"healthy","signature_status":"verified","current_version":"v1.0.0","latest_version":"v1.2.0","latest_version_state":"ahead","update_available":true,"platform":"linux_amd64","release_message":"ready","release_checked_at":"2026-08-28T00:00:00Z","operation":"none","operation_status":"idle","operation_detail":"idle","rollback_available":true}`))
		case "POST /api/nodes/edge-1/agent-update/upgrade":
			upgradePosts.Add(1)
			var payload map[string]any
			_ = json.NewDecoder(request.Body).Decode(&payload)
			if payload["version"] != "v1.2.0" || payload["confirmed"] != true || len(payload) != 2 {
				t.Errorf("upgrade payload = %#v", payload)
			}
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"operation":"upgrade","operation_status":"scheduled","operation_version":"v1.2.0"}`))
		case "POST /api/nodes/edge-1/agent-update/rollback":
			rollbackPosts.Add(1)
			var payload map[string]any
			_ = json.NewDecoder(request.Body).Decode(&payload)
			if payload["confirmed"] != true || len(payload) != 1 {
				t.Errorf("rollback payload = %#v", payload)
			}
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"operation":"rollback","operation_status":"scheduled"}`))
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
		agenthub.CapabilityAgentUpdateRead: true, agenthub.CapabilityAgentUpdateAction: true,
	}}
	state := tuiUpdateState{
		Supported: true, ReleaseStatus: releaseupdates.StatusHealthy, SignatureStatus: releaseupdates.SignatureVerified,
		CurrentVersion: "v1.0.0", LatestVersion: "v1.2.0", UpdateAvailable: true, OperationStatus: "idle", RollbackAvailable: true,
	}

	upgrade, err := runTUIUpdateOperation(context.Background(), client, tuiOperation{Kind: tuiOperationUpdate, Target: target, Action: "upgrade", Update: state})
	if err != nil || !strings.Contains(upgrade, "v1.2.0") || upgradePosts.Load() != 1 {
		t.Fatalf("upgrade=%q err=%v posts=%d", upgrade, err, upgradePosts.Load())
	}
	rollback, err := runTUIUpdateOperation(context.Background(), client, tuiOperation{Kind: tuiOperationUpdate, Target: target, Action: "rollback", Update: state})
	if err != nil || !strings.Contains(rollback, "rollback scheduled") || rollbackPosts.Load() != 1 {
		t.Fatalf("rollback=%q err=%v posts=%d", rollback, err, rollbackPosts.Load())
	}
}

func TestTUIUpdatesRejectsUnverifiedMutationsBeforePOST(t *testing.T) {
	t.Parallel()
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost {
			posts.Add(1)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"healthy","signature_status":"not_configured","current_version":"v1.0.0","latest_version":"v1.2.0","update_available":true}`))
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	localTarget := initialTUITargets()[0]
	localState := tuiUpdateState{
		Supported: true, Local: true, ReleaseStatus: releaseupdates.StatusHealthy, SignatureStatus: releaseupdates.SignatureNotConfigured,
		CurrentVersion: "v1.0.0", LatestVersion: "v1.2.0", UpdateAvailable: true,
		Stage: &releaseupdates.Stage{ID: "v1.2.0-0123456789ab", Version: "v1.2.0", Status: releaseupdates.StageStaged},
	}
	if localState.canStage() || localState.canInstall() {
		t.Fatal("unverified local release was actionable")
	}
	if _, err := runTUIUpdateOperation(context.Background(), client, tuiOperation{Target: localTarget, Action: "stage", Update: localState}); err == nil || err.Error() != signedManifestRequiredMessage {
		t.Fatalf("stage error = %v, want %q", err, signedManifestRequiredMessage)
	}
	if _, err := runTUIUpdateOperation(context.Background(), client, tuiOperation{Target: localTarget, Action: "install", Update: localState}); err == nil || err.Error() != signedManifestRequiredMessage {
		t.Fatalf("install error = %v, want %q", err, signedManifestRequiredMessage)
	}

	remoteTarget := tuiTarget{ID: "edge-1", Online: true, Capabilities: map[string]bool{agenthub.CapabilityAgentUpdateAction: true}}
	remoteState := tuiUpdateState{
		Supported: true, ReleaseStatus: releaseupdates.StatusHealthy, SignatureStatus: releaseupdates.SignatureUnavailable,
		CurrentVersion: "v1.0.0", LatestVersion: "v1.2.0", UpdateAvailable: true,
	}
	if remoteState.canUpgradeAgent(remoteTarget) {
		t.Fatal("unverified remote release was actionable")
	}
	if _, err := runTUIUpdateOperation(context.Background(), client, tuiOperation{Target: remoteTarget, Action: "upgrade", Update: remoteState}); err == nil || err.Error() != signedManifestRequiredMessage {
		t.Fatalf("upgrade error = %v, want %q", err, signedManifestRequiredMessage)
	}
	model := tuiModel{updates: localState, updatesLoaded: true, updatesTarget: localTarget.ID, selectedTargetID: localTarget.ID, snapshot: tuiSnapshot{Selected: localTarget}}
	model.openUpdateConfirmation("stage")
	if model.notice != signedManifestRequiredMessage || !model.noticeError {
		t.Fatalf("notice=%q error=%t, want signed-manifest notice", model.notice, model.noticeError)
	}
	if posts.Load() != 0 {
		t.Fatalf("unverified TUI preflight sent %d mutation request(s)", posts.Load())
	}
}

func TestTUIUpdatesRejectsStaleObservationsAndExposesShortcutPalette(t *testing.T) {
	t.Parallel()
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost {
			posts.Add(1)
		}
		switch request.URL.Path {
		case "/api/system/update":
			_, _ = writer.Write([]byte(`{"status":"healthy","signature_status":"verified","current_version":"v1.0.0","latest_version":"v1.2.0","update_available":true}`))
		case "/api/system/update/stage":
			_, _ = writer.Write([]byte(`{"stage":{"id":"v1.3.0-changed","version":"v1.3.0","status":"staged"}}`))
		case "/api/nodes/edge-1/agent-update":
			_, _ = writer.Write([]byte(`{"release_status":"healthy","signature_status":"verified","current_version":"v1.0.0","latest_version":"v1.3.0","update_available":true,"operation_status":"idle","rollback_available":true}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	localState := tuiUpdateState{Supported: true, Local: true, SignatureStatus: releaseupdates.SignatureVerified, Stage: &releaseupdates.Stage{ID: "v1.2.0-old", Version: "v1.2.0", Status: releaseupdates.StageStaged}}
	if _, err := runTUIUpdateOperation(context.Background(), client, tuiOperation{Target: initialTUITargets()[0], Action: "install", Update: localState}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale install err = %v", err)
	}
	target := tuiTarget{ID: "edge-1", Online: true, Capabilities: map[string]bool{agenthub.CapabilityAgentUpdateRead: true, agenthub.CapabilityAgentUpdateAction: true}}
	remoteState := tuiUpdateState{Supported: true, ReleaseStatus: releaseupdates.StatusHealthy, SignatureStatus: releaseupdates.SignatureVerified, CurrentVersion: "v1.0.0", LatestVersion: "v1.2.0", UpdateAvailable: true, OperationStatus: "idle", RollbackAvailable: true}
	if _, err := runTUIUpdateOperation(context.Background(), client, tuiOperation{Target: target, Action: "upgrade", Update: remoteState}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale upgrade err = %v", err)
	}
	if posts.Load() != 0 {
		t.Fatalf("stale observations sent %d mutation(s)", posts.Load())
	}

	model := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
	model.loading = false
	model.snapshot.Selected = model.snapshot.Targets[0]
	model.updates, model.updatesTarget, model.updatesLoaded = tuiUpdateState{
		Supported: true, Local: true, ReleaseStatus: releaseupdates.StatusHealthy, SignatureStatus: releaseupdates.SignatureVerified, CurrentVersion: "v1.0.0", LatestVersion: "v1.2.0", UpdateAvailable: true,
		Stage: &releaseupdates.Stage{ID: "v1.2.0-old", Version: "v1.2.0", Status: releaseupdates.StageStaged},
	}, localTargetID, true
	updated, command := model.updateKey("U")
	model = updated.(tuiModel)
	if command != nil || model.tab != tuiTabUpdates {
		t.Fatalf("updates shortcut tab=%v command=%v", model.tab, command != nil)
	}
	foundSection, foundStage, foundInstall := false, false, false
	for _, item := range model.buildPaletteItems() {
		foundSection = foundSection || item.Kind == tuiPaletteNavigate && item.Tab == tuiTabUpdates
		foundStage = foundStage || item.Kind == tuiPaletteOperation && item.Operation.Kind == tuiOperationUpdate && item.Operation.Action == "stage"
		foundInstall = foundInstall || item.Kind == tuiPaletteOperation && item.Operation.Kind == tuiOperationUpdate && item.Operation.Action == "install"
	}
	if !foundSection || !foundStage || !foundInstall {
		t.Fatalf("palette section=%t stage=%t install=%t", foundSection, foundStage, foundInstall)
	}
}
