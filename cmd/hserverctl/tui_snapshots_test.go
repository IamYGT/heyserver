package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
)

func TestLoadTUISnapshotsUsesObservedHealthyRepository(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/backups/snapshot/status":
			if request.Method != http.MethodGet || request.URL.Query().Get("refresh") != "1" {
				t.Errorf("status request = %s %s", request.Method, request.RequestURI)
			}
			_, _ = writer.Write([]byte(`{"resticFound":true,"repoInitialized":true,"passwordSet":true,"destination":"s3","destinationStatus":"healthy","settings":{"destination":"s3","repoFolder":"encrypted/server","enabledPaths":["vhosts"],"keepDaily":14,"keepWeekly":8,"keepMonthly":6,"passwordAcknowledged":true},"manifest":[{"id":"vhosts","label":"Websites"},{"id":"nginx","label":"Nginx"},{"id":"root-crontab","label":"Root crontab"}],"repoStats":{"snapshotCount":2,"totalSize":1024,"totalFileSize":4096},"lastSnapshots":[]}`))
		case "/api/backups/snapshot/list":
			_, _ = writer.Write([]byte(`{"snapshots":[{"id":"abcdef12","time":"2026-08-26T10:00:00Z","hostname":"old","paths":2},{"id":"1234abcd","time":"2026-08-27T10:00:00Z","hostname":"new","paths":3}]}`))
		case "/api/backups/snapshot/vhosts":
			_, _ = writer.Write([]byte(`{"vhosts":["zeta.example","alpha.example"]}`))
		default:
			t.Errorf("unexpected request: %s", request.RequestURI)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadTUISnapshots(context.Background(), client, initialTUITargets()[0])
	if err != nil {
		t.Fatal(err)
	}
	if !state.Supported || !state.ready() || state.Destination != "s3" || len(state.Snapshots) != 2 || state.Snapshots[0].ID != "1234abcd" {
		t.Fatalf("snapshot state = %#v", state)
	}
	if state.RepoStats == nil || state.RepoStats.SnapshotCount != 2 || !reflect.DeepEqual(state.Vhosts, []string{"alpha.example", "zeta.example"}) || requests.Load() != 3 {
		t.Fatalf("repo stats = %#v, requests = %d", state.RepoStats, requests.Load())
	}
}

func TestLoadTUISnapshotsMakesManagedTargetExplicitlyNotApplicable(t *testing.T) {
	t.Parallel()
	state, err := loadTUISnapshots(context.Background(), &apiClient{}, tuiTarget{ID: "edge-1", Name: "Edge"})
	if err != nil {
		t.Fatal(err)
	}
	if state.Supported || state.DestinationStatus != "not_configured" || !strings.Contains(state.DestinationMessage, "select the Local server") {
		t.Fatalf("managed snapshot state = %#v", state)
	}
}

func TestLoadTUISnapshotsPreservesCachedInventoryWhenOptionalDiscoveryFails(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/backups/snapshot/status":
			_, _ = writer.Write([]byte(`{"resticFound":true,"repoInitialized":true,"passwordSet":true,"destination":"gdrive","destinationStatus":"healthy","manifest":[{"id":"nginx","label":"Nginx"}],"lastSnapshots":[{"id":"abcdef12","time":"2026-08-27T10:00:00Z","hostname":"cached","paths":1}]}`))
		case "/api/backups/snapshot/list", "/api/backups/snapshot/vhosts":
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"error":"temporarily unavailable"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadTUISnapshots(context.Background(), client, initialTUITargets()[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Snapshots) != 1 || state.Snapshots[0].Hostname != "cached" || len(state.Warnings) != 2 {
		t.Fatalf("partial snapshot state = %#v", state)
	}
}

func TestLoadTUISnapshotsRejectsUntrustedInventory(t *testing.T) {
	t.Parallel()
	for _, item := range []struct {
		name string
		body string
		want string
	}{
		{name: "destination", body: `{"destination":"ftp","destinationStatus":"healthy"}`, want: "unsupported snapshot destination"},
		{name: "state", body: `{"destination":"gdrive","destinationStatus":"configured"}`, want: "unsupported snapshot destination status"},
		{name: "manifest", body: `{"destination":"gdrive","destinationStatus":"unavailable","manifest":[{"id":"arbitrary-path"}]}`, want: "unsupported encrypted snapshot manifest identity"},
		{name: "identity", body: `{"destination":"gdrive","destinationStatus":"unavailable","lastSnapshots":[{"id":"../snapshot"}]}`, want: "invalid encrypted snapshot identity"},
	} {
		t.Run(item.name, func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(item.body))
			}))
			defer server.Close()
			client, clientErr := newAPIClient(server.URL, "test-token", time.Second)
			if clientErr != nil {
				t.Fatal(clientErr)
			}
			_, err := loadTUISnapshots(context.Background(), client, initialTUITargets()[0])
			if err == nil || !strings.Contains(err.Error(), item.want) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestRunTUISnapshotOperationsUseFixedContracts(t *testing.T) {
	t.Parallel()
	var createRequests atomic.Int32
	var restoreRequests atomic.Int32
	var settingsReads atomic.Int32
	var settingsWrites atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "POST /api/backups/snapshot/run":
			createRequests.Add(1)
			_, _ = writer.Write([]byte(`{"status":"running","jobId":"snapshot-job"}`))
		case "POST /api/backups/snapshot/restore":
			restoreRequests.Add(1)
			var body struct {
				SnapshotID  string   `json:"snapshotId"`
				ManifestIDs []string `json:"manifestIds"`
				Vhosts      []string `json:"vhosts"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.SnapshotID != "abcdef1234567890" {
				t.Errorf("restore body = %#v", body)
			}
			if restoreRequests.Load() == 1 && (len(body.ManifestIDs) != 0 || len(body.Vhosts) != 0) {
				t.Errorf("full restore body = %#v", body)
			}
			if restoreRequests.Load() == 2 && (!reflect.DeepEqual(body.ManifestIDs, []string{"nginx"}) || !reflect.DeepEqual(body.Vhosts, []string{"example.com"})) {
				t.Errorf("selected restore body = %#v", body)
			}
			_, _ = writer.Write([]byte(`{"status":"restoring","jobId":"restore-job"}`))
		case "GET /api/backups/snapshot/settings":
			settingsReads.Add(1)
			_, _ = writer.Write([]byte(`{"destination":"gdrive","repoFolder":"encrypted/server","enabledPaths":["vhosts","nginx"],"keepDaily":30,"keepWeekly":12,"keepMonthly":6,"passwordAcknowledged":true}`))
		case "PUT /api/backups/snapshot/settings":
			settingsWrites.Add(1)
			var settings cliSnapshotSettings
			if err := json.NewDecoder(request.Body).Decode(&settings); err != nil {
				t.Fatal(err)
			}
			if settings.Destination != "s3" || settings.RepoFolder != "encrypted/server" || !reflect.DeepEqual(settings.EnabledPaths, []string{"vhosts", "nginx"}) || settings.KeepDaily != 30 || settings.KeepWeekly != 12 || settings.KeepMonthly != 6 || !settings.PasswordAcknowledged {
				t.Errorf("settings = %#v", settings)
			}
			_, _ = writer.Write([]byte(`{"success":true}`))
		default:
			t.Errorf("unexpected request: %s %s", request.Method, request.RequestURI)
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	target := initialTUITargets()[0]

	operations := []tuiOperation{
		{Kind: tuiOperationSnapshot, Target: target, Action: "create"},
		{Kind: tuiOperationSnapshot, Target: target, Action: "restore-all", EncryptedSnapshot: tuiEncryptedSnapshot{ID: "abcdef1234567890"}},
		{Kind: tuiOperationSnapshot, Target: target, Action: "restore-selected", EncryptedSnapshot: tuiEncryptedSnapshot{ID: "abcdef1234567890"}, SnapshotManifestIDs: []string{"nginx"}, SnapshotVhosts: []string{"example.com"}},
		{Kind: tuiOperationSnapshot, Target: target, Action: "destination-s3"},
	}
	for _, operation := range operations {
		message, err := runTUISnapshotOperation(context.Background(), client, operation)
		if err != nil || strings.TrimSpace(message) == "" {
			t.Fatalf("%s message=%q error=%v", operation.Action, message, err)
		}
	}
	if createRequests.Load() != 1 || restoreRequests.Load() != 2 || settingsReads.Load() != 1 || settingsWrites.Load() != 1 {
		t.Fatalf("requests create=%d restore=%d settings=%d/%d", createRequests.Load(), restoreRequests.Load(), settingsReads.Load(), settingsWrites.Load())
	}
}

func TestEncryptedSnapshotTUIActionsRequireReadinessAndConfirmation(t *testing.T) {
	t.Parallel()
	targets := initialTUITargets()
	snapshot := tuiEncryptedSnapshot{ID: "abcdef1234567890", Time: time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC), Hostname: "node-1", Paths: 4}
	model := tuiModel{
		tab: tuiTabSnapshots, snapshot: tuiSnapshot{Targets: targets, Selected: targets[0]}, selectedTargetID: localTargetID,
		encryptedSnapshotsLoaded: true, encryptedSnapshotsTarget: localTargetID,
		encryptedSnapshots: tuiSnapshotState{
			Supported: true, ResticFound: true, PasswordSet: true, RepoInitialized: true,
			Destination: "s3", DestinationStatus: "healthy", Snapshots: []tuiEncryptedSnapshot{snapshot},
			Manifest: []tuiSnapshotManifestEntry{{ID: "nginx", Label: "Nginx"}, {ID: "root-crontab", Label: "Root crontab"}},
			Vhosts:   []string{"example.com"},
		},
	}

	view := model.renderEncryptedSnapshots(100, 20)
	for _, expected := range []string{"Encrypted snapshots", "S3-compatible / MinIO", "abcdef1234567890", "node-1"} {
		if !strings.Contains(view, expected) {
			t.Fatalf("snapshot view missing %q:\n%s", expected, view)
		}
	}
	model.openEncryptedSnapshotCreate()
	if model.dialog.Mode != tuiDialogConfirm || model.dialog.Operation.Action != "create" {
		t.Fatalf("create dialog = %#v", model.dialog)
	}
	model.dialog = tuiDialog{}
	model.openEncryptedSnapshotDestination()
	if model.dialog.Mode != tuiDialogChoices || len(model.dialog.Options) != 2 {
		t.Fatalf("destination dialog = %#v", model.dialog)
	}
	model.dialog = tuiDialog{}
	model.openEncryptedSnapshotRestore()
	if model.dialog.Mode != tuiDialogChoices || len(model.dialog.Options) != 3 {
		t.Fatalf("restore dialog = %#v", model.dialog)
	}
	model.dialog.Cursor = 1
	updated, _ := model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if model.dialog.Mode != tuiDialogSnapshotSelectors || len(model.dialog.SnapshotSelectors) != 1 || model.dialog.SnapshotSelectors[0].Action != "nginx" {
		t.Fatalf("manifest selector dialog = %#v", model.dialog)
	}
	updated, _ = model.updateDialogKey(" ")
	model = updated.(tuiModel)
	updated, _ = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if model.dialog.Mode != tuiDialogConfirm || model.dialog.Operation.Action != "restore-selected" || !reflect.DeepEqual(model.dialog.Operation.SnapshotManifestIDs, []string{"nginx"}) || !model.dialog.Operation.Dangerous {
		t.Fatalf("selected restore dialog = %#v", model.dialog)
	}

	model.dialog = tuiDialog{}
	model.openEncryptedSnapshotRestore()
	updated, _ = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if model.dialog.Mode != tuiDialogConfirm || model.dialog.Operation.Action != "restore-all" || model.dialog.Operation.EncryptedSnapshot.ID != snapshot.ID {
		t.Fatalf("full restore dialog = %#v", model.dialog)
	}

	model.dialog = tuiDialog{}
	model.openEncryptedSnapshotRestore()
	model.dialog.Cursor = 2
	updated, _ = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if model.dialog.Mode != tuiDialogSnapshotSelectors || model.dialog.SnapshotSelectors[0].Action != "example.com" {
		t.Fatalf("vhost selector dialog = %#v", model.dialog)
	}
	updated, _ = model.updateDialogKey(" ")
	model = updated.(tuiModel)
	updated, _ = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if model.dialog.Operation.Action != "restore-selected" || !reflect.DeepEqual(model.dialog.Operation.SnapshotVhosts, []string{"example.com"}) {
		t.Fatalf("vhost restore dialog = %#v", model.dialog)
	}

	model.dialog = tuiDialog{}
	model.encryptedSnapshots.DestinationStatus = "unavailable"
	model.openEncryptedSnapshotCreate()
	if model.dialog.Mode != tuiDialogNone || !model.noticeError {
		t.Fatalf("unavailable create dialog=%#v notice=%q", model.dialog, model.notice)
	}
}

func TestSnapshotTabKeepsWideNavigationBounded(t *testing.T) {
	t.Parallel()
	model := tuiModel{tab: tuiTabSnapshots}
	tabs := model.renderTabs(132)
	if !strings.Contains(tabs, "N Snapshots") || lipgloss.Width(tabs) > 132 {
		t.Fatalf("tabs width=%d:\n%s", lipgloss.Width(tabs), tabs)
	}
}

func TestRunTUISnapshotOperationRejectsRemoteAndInvalidIdentityBeforeRequest(t *testing.T) {
	t.Parallel()
	for _, operation := range []tuiOperation{
		{Kind: tuiOperationSnapshot, Target: tuiTarget{ID: "edge-1", Name: "Edge"}, Action: "create"},
		{Kind: tuiOperationSnapshot, Target: initialTUITargets()[0], Action: "restore-all", EncryptedSnapshot: tuiEncryptedSnapshot{ID: "../snapshot"}},
		{Kind: tuiOperationSnapshot, Target: initialTUITargets()[0], Action: "restore-selected", EncryptedSnapshot: tuiEncryptedSnapshot{ID: "abcdef1234567890"}, SnapshotManifestIDs: []string{"root-crontab"}},
		{Kind: tuiOperationSnapshot, Target: initialTUITargets()[0], Action: "restore-selected", EncryptedSnapshot: tuiEncryptedSnapshot{ID: "abcdef1234567890"}, SnapshotVhosts: []string{"../site"}},
	} {
		if _, err := runTUISnapshotOperation(context.Background(), &apiClient{}, operation); err == nil {
			t.Fatalf("operation %#v was accepted", operation)
		}
	}
}
