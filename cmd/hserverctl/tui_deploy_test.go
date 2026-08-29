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
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/remotenodes"
)

func TestLoadTUIDeployPreservesTargetsAcrossPartialHistoryFailureAndManagedBoundaries(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/deploy/targets":
			_, _ = writer.Write([]byte(`[{"id":12,"name":"Example app","repoUrl":"https://example.com/app.git","branch":"main","projectDir":"/srv/example-app","environment":"production","deploymentKind":"compose","composeFile":"compose.yaml","webhookProvider":"github","webhookStatus":"healthy","autoDeploy":true,"isActive":true}]`))
		case "/api/deploy/history":
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"error":"history unavailable"}`))
		case "/api/nodes/edge-1/deploy":
			_, _ = writer.Write([]byte(`[{"id":"example-app","name":"Example app","kind":"application","path":"/srv/example-app","status":"ready","eligible":true,"actions":["preflight","deploy"]}]`))
		case "/api/nodes/edge-1/deploy/jobs":
			writer.WriteHeader(http.StatusBadGateway)
			_, _ = writer.Write([]byte(`{"error":"job history unavailable"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	local, err := loadTUIDeploy(context.Background(), client, initialTUITargets()[0])
	if err != nil || !local.Supported || len(local.Targets) != 1 || len(local.Runs) != 0 || len(local.Warnings) != 1 || !strings.Contains(local.Warnings[0], "history unavailable") {
		t.Fatalf("local=%#v err=%v", local, err)
	}
	before := requests.Load()
	offline, err := loadTUIDeploy(context.Background(), client, tuiTarget{ID: "offline", Online: false, Capabilities: map[string]bool{agenthub.CapabilityDeployRead: true}})
	if err != nil || offline.Supported || !strings.Contains(offline.Message, "offline") || requests.Load() != before {
		t.Fatalf("offline=%#v err=%v requests=%d", offline, err, requests.Load())
	}
	missing, err := loadTUIDeploy(context.Background(), client, tuiTarget{ID: "legacy", Online: true, Capabilities: map[string]bool{}})
	if err != nil || missing.Supported || !strings.Contains(missing.Message, agenthub.CapabilityDeployRead) || requests.Load() != before {
		t.Fatalf("missing=%#v err=%v requests=%d", missing, err, requests.Load())
	}
	remote, err := loadTUIDeploy(context.Background(), client, tuiTarget{ID: "edge-1", Online: true, Capabilities: map[string]bool{agenthub.CapabilityDeployRead: true}})
	if err != nil || !remote.Supported || len(remote.RemoteTargets) != 1 || len(remote.RemoteJobs) != 0 || len(remote.Warnings) != 1 {
		t.Fatalf("remote=%#v err=%v", remote, err)
	}
}

func TestTUIDeployLocalDetailMutationAndRollbackUseFreshObservations(t *testing.T) {
	t.Parallel()
	var manualPosts atomic.Int32
	var rollbackPosts atomic.Int32
	targetJSON := `{"id":12,"name":"Example app","repoUrl":"https://example.com/app.git","branch":"main","projectDir":"/srv/example-app","environment":"production","deploymentKind":"compose","composeFile":"compose.yaml","webhookProvider":"github","webhookStatus":"healthy","autoDeploy":true,"isActive":true}`
	preflightJSON := `{"targetId":12,"deploymentKind":"compose","eligible":true,"checks":[{"id":"project","status":"pass","message":"project ready"}]}`
	revisionJSON := `{"targetId":12,"state":"ready","branch":"main","currentCommit":"bbbbbbbbbbbbbbbb","deployedCommit":"bbbbbbbbbbbbbbbb","rollbackCommit":"aaaaaaaaaaaaaaaa","trackedChanges":false,"matchesDeployed":true,"rollbackAvailable":true,"commitsAheadRollback":1,"commitsBehindRollback":0,"filesChanged":1,"insertions":2,"deletions":1,"message":"rollback ready","checkedAt":"2026-08-28T00:00:00Z"}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/deploy/targets":
			_, _ = writer.Write([]byte("[" + targetJSON + "]"))
		case "GET /api/deploy/history":
			_, _ = writer.Write([]byte(`[]`))
		case "GET /api/deploy/targets/12/preflight":
			_, _ = writer.Write([]byte(preflightJSON))
		case "GET /api/deploy/targets/12/revision":
			_, _ = writer.Write([]byte(revisionJSON))
		case "POST /api/deploy/manual/12":
			manualPosts.Add(1)
			assertDeployNoBody(t, request)
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"message":"deployment queued","runId":71}`))
		case "POST /api/deploy/rollback/12":
			rollbackPosts.Add(1)
			assertDeployNoBody(t, request)
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"message":"rollback queued","runId":72}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadTUIDeploy(context.Background(), client, initialTUITargets()[0])
	if err != nil {
		t.Fatal(err)
	}
	model := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
	model.loading = false
	model.tab = tuiTabDeploy
	model.snapshot.Selected = model.snapshot.Targets[0]
	model.deploy, model.deployTarget, model.deployLoaded = state, localTargetID, true

	updated, command := model.updateKey("enter")
	model = updated.(tuiModel)
	if command == nil || !model.resourceLoading || manualPosts.Load() != 0 {
		t.Fatal("target Enter did not start read-only deploy detail loading")
	}
	detailMessage := command().(tuiDeployDetailMsg)
	updated, command = model.Update(detailMessage)
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogChoices || len(model.dialog.Options) != 3 || manualPosts.Load() != 0 {
		t.Fatalf("detail dialog=%#v posts=%d", model.dialog, manualPosts.Load())
	}
	detailDialog := model.dialog
	model.dialog.Cursor = 0
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogLogs || model.dialog.LogReloadNotice == "" {
		t.Fatal("readiness view did not open as a static observation")
	}
	updated, command = model.updateDialogKey("r")
	model = updated.(tuiModel)
	if command != nil || model.resourceLoading || !strings.Contains(model.notice, "Reinspect") {
		t.Fatal("static readiness reload attempted an unrelated log request")
	}
	model.dialog = detailDialog
	model.dialog.Cursor = 1
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || manualPosts.Load() != 0 {
		t.Fatal("deploy choice bypassed separate confirmation")
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || manualPosts.Load() != 0 {
		t.Fatal("Enter bypassed deployment confirmation")
	}
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("Y did not start deployment")
	}
	operationMessage := command().(tuiOperationMsg)
	if operationMessage.Err != nil || manualPosts.Load() != 1 || !strings.Contains(operationMessage.Message, "run 71") {
		t.Fatalf("deploy message=%#v posts=%d", operationMessage, manualPosts.Load())
	}

	detail := detailMessage.Detail
	message, err := runTUIDeployOperation(context.Background(), client, tuiOperation{
		Kind: tuiOperationDeploy, Target: initialTUITargets()[0], Action: "rollback",
		DeployTarget: detail.Target, DeployPreflight: detail.Preflight, DeployRevision: detail.Revision,
	})
	if err != nil || rollbackPosts.Load() != 1 || !strings.Contains(message, "run 72") {
		t.Fatalf("rollback=%q err=%v posts=%d", message, err, rollbackPosts.Load())
	}
}

func TestTUIDeployManagedActionReobservesExactAdvertisedPlan(t *testing.T) {
	t.Parallel()
	var posts atomic.Int32
	planJSON := `{"id":"example-app","name":"Example app","kind":"application","path":"/srv/example-app","status":"ready","eligible":true,"actions":["preflight","deploy","rollback"],"branch":"main","head":"abcdef123456"}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/nodes/edge-1/deploy":
			_, _ = writer.Write([]byte("[" + planJSON + "]"))
		case "POST /api/nodes/edge-1/deploy/example-app/actions/deploy":
			posts.Add(1)
			assertDeployNoBody(t, request)
			writer.WriteHeader(http.StatusAccepted)
			_, _ = writer.Write([]byte(`{"id":"task-81","target_id":"example-app","action":"deploy","status":"queued","message":"Queued on managed node","created_at":"2026-08-28T00:00:00Z"}`))
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
		agenthub.CapabilityDeployRead: true, agenthub.CapabilityDeployAction: true,
	}}
	var plan remotenodes.RemoteDeployTarget
	if err := json.Unmarshal([]byte(planJSON), &plan); err != nil {
		t.Fatal(err)
	}
	message, err := runTUIDeployOperation(context.Background(), client, tuiOperation{Kind: tuiOperationDeploy, Target: target, Action: "deploy", RemoteDeployTarget: plan})
	if err != nil || posts.Load() != 1 || !strings.Contains(message, "task-81") {
		t.Fatalf("message=%q err=%v posts=%d", message, err, posts.Load())
	}
}

func TestTUIDeployRejectsStaleTargetsAndPlansBeforeMutation(t *testing.T) {
	t.Parallel()
	var posts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost {
			posts.Add(1)
		}
		switch request.URL.Path {
		case "/api/deploy/targets":
			_, _ = writer.Write([]byte(`[{"id":12,"name":"Example app","repoUrl":"https://example.com/app.git","branch":"changed","projectDir":"/srv/example-app","environment":"production","deploymentKind":"compose","composeFile":"compose.yaml","webhookProvider":"github","webhookStatus":"healthy","autoDeploy":true,"isActive":true}]`))
		case "/api/nodes/edge-1/deploy":
			_, _ = writer.Write([]byte(`[{"id":"example-app","name":"Example app","kind":"application","path":"/srv/changed","status":"ready","eligible":true,"actions":["deploy"]}]`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	local := models.DeployTarget{ID: 12, Name: "Example app", RepoURL: "https://example.com/app.git", Branch: "main", ProjectDir: "/srv/example-app", Environment: models.DeployEnvironmentProduction, DeployKind: models.DeployKindCompose, ComposeFile: "compose.yaml", WebhookProvider: models.DeployWebhookGitHub, WebhookStatus: models.DeployWebhookHealthy, AutoDeploy: true, IsActive: true}
	if _, err := runTUIDeployOperation(context.Background(), client, tuiOperation{Target: initialTUITargets()[0], Action: "deploy", DeployTarget: local}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale local err=%v", err)
	}
	remoteNode := tuiTarget{ID: "edge-1", Online: true, Capabilities: map[string]bool{agenthub.CapabilityDeployRead: true, agenthub.CapabilityDeployAction: true}}
	remote := remotenodes.RemoteDeployTarget{ID: "example-app", Name: "Example app", Kind: "application", Path: "/srv/example-app", Status: "ready", Eligible: true, Actions: []string{"deploy"}}
	if _, err := runTUIDeployOperation(context.Background(), client, tuiOperation{Target: remoteNode, Action: "deploy", RemoteDeployTarget: remote}); err == nil || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("stale remote err=%v", err)
	}
	if posts.Load() != 0 {
		t.Fatalf("stale deploy observations sent %d mutation(s)", posts.Load())
	}
}

func TestTUIDeployRejectsStaleLocalRevisionBeforeMutation(t *testing.T) {
	t.Parallel()
	var posts atomic.Int32
	targetJSON := `{"id":12,"name":"Example app","repoUrl":"https://example.com/app.git","branch":"main","projectDir":"/srv/example-app","environment":"production","deploymentKind":"compose","composeFile":"compose.yaml","webhookProvider":"github","webhookStatus":"healthy","autoDeploy":true,"isActive":true}`
	preflightJSON := `{"targetId":12,"deploymentKind":"compose","eligible":true,"checks":[{"id":"project","status":"pass","message":"project ready"}]}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if request.Method == http.MethodPost {
			posts.Add(1)
		}
		switch request.URL.Path {
		case "/api/deploy/targets":
			_, _ = writer.Write([]byte("[" + targetJSON + "]"))
		case "/api/deploy/targets/12/preflight":
			_, _ = writer.Write([]byte(preflightJSON))
		case "/api/deploy/targets/12/revision":
			_, _ = writer.Write([]byte(`{"targetId":12,"state":"ready","branch":"main","currentCommit":"cccccccccccccccc","deployedCommit":"bbbbbbbbbbbbbbbb","rollbackCommit":"aaaaaaaaaaaaaaaa","matchesDeployed":false,"rollbackAvailable":true}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var target models.DeployTarget
	var preflight models.DeployPreflight
	if err := json.Unmarshal([]byte(targetJSON), &target); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(preflightJSON), &preflight); err != nil {
		t.Fatal(err)
	}
	observedRevision := models.DeployRevisionComparison{
		TargetID: 12, State: "ready", Branch: "main", CurrentCommit: "bbbbbbbbbbbbbbbb", DeployedCommit: "bbbbbbbbbbbbbbbb",
		RollbackCommit: "aaaaaaaaaaaaaaaa", MatchesDeployed: true, RollbackAvailable: true,
	}
	_, err = runTUIDeployOperation(context.Background(), client, tuiOperation{
		Kind: tuiOperationDeploy, Target: initialTUITargets()[0], Action: "deploy",
		DeployTarget: target, DeployPreflight: preflight, DeployRevision: observedRevision,
	})
	if err == nil || !strings.Contains(err.Error(), "revision changed") || posts.Load() != 0 {
		t.Fatalf("err=%v posts=%d", err, posts.Load())
	}
}

func TestTUIDeployLogsShortcutPaletteAndActivePolling(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/deploy/history/71/logs":
			_, _ = writer.Write([]byte(`{"logs":"build ready\nrelease complete\n"}`))
		case "/api/nodes/edge-1/deploy/jobs":
			_, _ = writer.Write([]byte(`[{"id":"task-81","target_id":"example-app","action":"deploy","status":"completed","message":"complete","created_at":"2026-08-28T00:00:00Z","output":"remote ready"}]`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	_, localLines, err := loadTUIDeployLogs(context.Background(), client, initialTUITargets()[0], tuiDeployLogRef{LocalRunID: 71})
	if err != nil || strings.Join(localLines, "|") != "build ready|release complete" {
		t.Fatalf("local lines=%q err=%v", localLines, err)
	}
	remoteTarget := tuiTarget{ID: "edge-1", Online: true, Capabilities: map[string]bool{agenthub.CapabilityDeployRead: true}}
	_, remoteLines, err := loadTUIDeployLogs(context.Background(), client, remoteTarget, tuiDeployLogRef{RemoteJob: "task-81"})
	if err != nil || !strings.Contains(strings.Join(remoteLines, "\n"), "remote ready") {
		t.Fatalf("remote lines=%q err=%v", remoteLines, err)
	}

	model := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
	model.loading = false
	model.snapshot.Selected = model.snapshot.Targets[0]
	model.deploy, model.deployTarget, model.deployLoaded = tuiDeployState{
		Supported: true, Local: true,
		Targets: []models.DeployTarget{{ID: 12, Name: "Example app", Branch: "main", ProjectDir: "/srv/example-app", IsActive: true}},
		Runs:    []models.DeployRun{{ID: 71, TargetID: 12, Status: models.DeployStatusRunning}},
	}, localTargetID, true
	updated, command := model.updateKey("G")
	model = updated.(tuiModel)
	if command != nil || model.tab != tuiTabDeploy || !model.hasActiveDeployJobs() {
		t.Fatalf("deploy shortcut tab=%v command=%v active=%t", model.tab, command != nil, model.hasActiveDeployJobs())
	}
	foundSection, foundDeploy := false, false
	for _, item := range model.buildPaletteItems() {
		foundSection = foundSection || item.Kind == tuiPaletteNavigate && item.Tab == tuiTabDeploy
		foundDeploy = foundDeploy || item.Kind == tuiPaletteOperation && item.Operation.Kind == tuiOperationDeploy && item.Operation.Action == "deploy"
	}
	if !foundSection || !foundDeploy {
		t.Fatalf("palette section=%t deploy=%t", foundSection, foundDeploy)
	}
}

func assertDeployNoBody(t *testing.T, request *http.Request) {
	t.Helper()
	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(body) != 0 {
		t.Errorf("request body = %q, want empty", body)
	}
}
