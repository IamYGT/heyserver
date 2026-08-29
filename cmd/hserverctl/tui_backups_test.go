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

	tea "charm.land/bubbletea/v2"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

func TestLoadTUIBackupsNormalizesLocalAndManagedInventory(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/backups":
			_, _ = writer.Write([]byte(`{
				"backups":[
					{"id":"backup-old","name":"old.tar.gz","type":"files","size":1024,"status":"invalid","createdAt":"2026-08-26T09:00:00Z"},
					{"id":"backup-new","name":"full.tar.gz","type":"full","size":4096,"status":"completed","createdAt":"2026-08-27T10:00:00Z"}
				],
				"storage":{"totalBytes":5120,"completedBytes":4096,"invalidBytes":1024,"orphanedBytes":0,"backupVolumeAvailable":1048576,"backupVolumeUsePercent":12.5}
			}`))
		case "/api/backups/jobs":
			if request.URL.Query().Get("hours") != "24" {
				t.Errorf("jobs query = %q", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"jobs":[
				{"id":"job-old","type":"files","source":"scheduled","status":"completed","phase":"done","progress":100,"message":"done","startedAt":"2026-08-26T09:00:00Z"},
				{"id":"job-new","type":"full","source":"manual","status":"running","phase":"archive","progress":62,"message":"creating archive","startedAt":"2026-08-27T10:01:00Z","etaSeconds":18,"bytesDone":2048,"bytesTotal":4096,"logs":["started","archiving"]}
			]}`))
		case "/api/backups/schedules":
			_, _ = writer.Write([]byte(`{"schedules":[{"frequency":"daily","time":"03:00","type":"full","database":"","retention_count":10,"cron":"0 3 * * *","rawLine":"0 3 * * * /usr/local/bin/hserver-backup type=full retention=10"}]}`))
		case "/api/backups/targets":
			_, _ = writer.Write([]byte(`{"vhosts":["beta.example","alpha.example"],"maxSelectedVhosts":16,"emptySelection":"all-configured-vhosts"}`))
		case "/api/nodes/edge-1/backups":
			_, _ = writer.Write([]byte(`[
				{"id":"nightly","name":"Nightly","service":"backup-nightly.service","timer":"backup-nightly.timer","active":"active","enabled":"enabled","last_result":"success","last_run":"2026-08-27T01:00:00Z","next_run":"2026-08-28T01:00:00Z","verified":true,"total_size":8192,"files":[{"name":"db.sql","path":"/backup/db.sql","size":8192,"modified_at":"2026-08-27T01:00:00Z"}]}
			]`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	local, jobs, storage, warnings, err := loadTUIBackups(context.Background(), client, initialTUITargets()[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(local) != 2 || local[0].ID != "backup-new" || local[0].Status != "completed" {
		t.Fatalf("local backups = %#v", local)
	}
	if storage.TotalBytes != 5120 || storage.CompletedBytes != 4096 || storage.InvalidBytes != 1024 || storage.BackupAvailable != 1048576 || storage.BackupUsePercentage != 12.5 {
		t.Fatalf("storage = %#v", storage)
	}
	if len(jobs) != 2 || jobs[0].ID != "job-new" || !jobs[0].active() || jobs[0].Progress != 62 || len(jobs[0].Logs) != 2 || len(warnings) != 1 || !strings.Contains(warnings[0], "frequency=daily") || !strings.Contains(warnings[0], "retention=10") {
		t.Fatalf("jobs = %#v, warnings=%#v", jobs, warnings)
	}
	targets, err := loadTUIBackupTargets(context.Background(), client)
	if err != nil || len(targets.Vhosts) != 2 || targets.Vhosts[0] != "alpha.example" || targets.Vhosts[1] != "beta.example" || targets.MaxSelectedVhosts != 16 {
		t.Fatalf("targets = %#v", targets)
	}

	managedTarget := tuiTarget{
		ID: "edge-1", Name: "Edge", Online: true,
		Capabilities: map[string]bool{agenthub.CapabilityBackupRead: true},
	}
	managed, managedJobs, _, warnings, err := loadTUIBackups(context.Background(), client, managedTarget)
	if err != nil {
		t.Fatal(err)
	}
	if len(managed) != 1 || !managed[0].Managed || managed[0].ID != "nightly" || managed[0].Status != "success" || !managed[0].Verified || managed[0].FileCount != 1 {
		t.Fatalf("managed plans = %#v", managed)
	}
	if len(managedJobs) != 0 || len(warnings) != 0 {
		t.Fatalf("managed jobs = %#v, warnings=%#v", managedJobs, warnings)
	}

	managedTarget.Capabilities = map[string]bool{}
	if _, _, _, _, err := loadTUIBackups(context.Background(), client, managedTarget); err == nil || !strings.Contains(err.Error(), "backup.read") {
		t.Fatalf("missing capability error = %v", err)
	}
	managedTarget.Online = false
	if _, _, _, _, err := loadTUIBackups(context.Background(), client, managedTarget); err == nil || !strings.Contains(err.Error(), "offline") {
		t.Fatalf("offline error = %v", err)
	}
}

func TestLoadTUIBackupSchedulesHealthyInventoryRendersReadOnlyDetails(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/api/backups/schedules" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"schedules":[
			{"frequency":"daily","time":"03:05","type":"database","database":"hserver","retention_count":14,"cron":"5 3 * * *","rawLine":"5 3 * * * /usr/local/bin/hserver-backup type=database db=hserver retention=14"},
			{"frequency":"weekly","time":"04:10","type":"full","retention_days":7,"cron":"10 4 * * 0","rawLine":"10 4 * * 0 /usr/local/bin/hserver-backup type=full retention=7"}
		]}`))
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	schedules, err := loadTUIBackupSchedules(context.Background(), client)
	if err != nil || len(schedules) != 2 {
		t.Fatalf("schedules=%#v err=%v", schedules, err)
	}
	if schedules[0].Frequency != "daily" || schedules[0].Time != "03:05" || schedules[0].Database != "hserver" || schedules[0].retentionCount() != 14 {
		t.Fatalf("first schedule=%#v", schedules[0])
	}
	if schedules[1].retentionCount() != 7 {
		t.Fatalf("legacy retention alias not normalized: %#v", schedules[1])
	}
	warnings := formatTUIBackupScheduleInventory(schedules)
	if len(warnings) != 2 || !strings.Contains(warnings[0], "frequency=daily") || !strings.Contains(warnings[0], "time=03:05") || !strings.Contains(warnings[0], "type=database") || !strings.Contains(warnings[0], "database=hserver") || !strings.Contains(warnings[0], "retention=14") {
		t.Fatalf("schedule display=%#v", warnings)
	}
	model := tuiModel{
		tab: tuiTabBackups, backupsLoaded: true, snapshot: tuiSnapshot{Selected: initialTUITargets()[0]},
		backups:        []tuiBackupItem{{ID: "artifact-1", Name: "full.tar.gz", Type: "full", Status: "completed", Size: 4096}},
		backupWarnings: warnings,
	}
	rendered := model.renderBackups(120, 24)
	for _, value := range []string{"frequency=daily", "time=03:05", "type=database", "database=hserver", "retention=14", "full.tar.gz"} {
		if !strings.Contains(rendered, value) {
			t.Fatalf("rendered schedule missing %q: %q", value, rendered)
		}
	}
}

func TestLoadTUIBackupSchedulesEmptyIsHonest(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/backups/schedules" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"schedules":[]}`))
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	schedules, err := loadTUIBackupSchedules(context.Background(), client)
	if err != nil || len(schedules) != 0 {
		t.Fatalf("schedules=%#v err=%v", schedules, err)
	}
	warnings := formatTUIBackupScheduleInventory(schedules)
	if len(warnings) != 1 || !strings.Contains(warnings[0], "empty") || !strings.Contains(warnings[0], "no backup schedules configured") {
		t.Fatalf("empty state=%#v", warnings)
	}
}

func TestLoadTUIBackupsPreservesInventoryWhenScheduleUnavailable(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/backups":
			_, _ = writer.Write([]byte(`{"backups":[{"id":"backup-1","name":"full.tar.gz","type":"full","size":4096,"status":"completed","createdAt":"2026-08-27T10:00:00Z"}],"storage":{"completedBytes":4096}}`))
		case "/api/backups/jobs":
			_, _ = writer.Write([]byte(`{"jobs":[{"id":"job-1","type":"full","status":"completed","startedAt":"2026-08-27T10:01:00Z"}]}`))
		case "/api/backups/schedules":
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"error":"backup scheduling unavailable: crontab read failed"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	items, jobs, _, warnings, err := loadTUIBackups(context.Background(), client, initialTUITargets()[0])
	if err != nil || len(items) != 1 || len(jobs) != 1 {
		t.Fatalf("inventory items=%#v jobs=%#v err=%v", items, jobs, err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "Schedules: unavailable") || !strings.Contains(warnings[0], "HTTP 503") {
		t.Fatalf("schedule unavailable state=%#v", warnings)
	}
}

func TestLoadTUIBackupsPreservesArtifactsWhenJobHistoryIsUnavailable(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/backups":
			_, _ = writer.Write([]byte(`{"backups":[{"id":"backup-1","name":"full.tar.gz","type":"full","size":4096,"status":"completed","createdAt":"2026-08-27T10:00:00Z"}],"storage":{"completedBytes":4096}}`))
		case "/api/backups/jobs":
			writer.WriteHeader(http.StatusForbidden)
			_, _ = writer.Write([]byte(`{"error":"permission denied"}`))
		case "/api/backups/schedules":
			_, _ = writer.Write([]byte(`{"schedules":[]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	items, jobs, _, warnings, err := loadTUIBackups(context.Background(), client, initialTUITargets()[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || len(jobs) != 0 || len(warnings) != 2 || !strings.Contains(warnings[0], "HTTP 403") || !strings.Contains(warnings[1], "empty") {
		t.Fatalf("items=%#v jobs=%#v warnings=%#v", items, jobs, warnings)
	}
}

func TestLoadTUIBackupsCommandPreservesInventoryWhenTargetDiscoveryIsUnavailable(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api/backups":
			_, _ = writer.Write([]byte(`{"backups":[{"id":"backup-1","name":"full.tar.gz","type":"full","size":4096,"status":"completed","createdAt":"2026-08-27T10:00:00Z"}],"storage":{"completedBytes":4096}}`))
		case "/api/backups/jobs":
			_, _ = writer.Write([]byte(`{"jobs":[]}`))
		case "/api/backups/schedules":
			_, _ = writer.Write([]byte(`{"schedules":[]}`))
		case "/api/backups/targets":
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"error":"backup target discovery is unavailable"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	message, ok := loadTUIBackupsCmd(context.Background(), client, initialTUITargets()[0])().(tuiBackupsMsg)
	if !ok || message.Err != nil || len(message.Items) != 1 || len(message.Targets.Vhosts) != 0 || len(message.Warnings) != 2 || !strings.Contains(message.Warnings[0], "empty") || !strings.Contains(message.Warnings[1], "Selective backup targets unavailable") {
		t.Fatalf("message=%#v", message)
	}
}

func TestTUIBackupValidationAndRestoreRequireBothConfirmations(t *testing.T) {
	t.Parallel()
	var validationRequests atomic.Int32
	var restoreRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "GET /api/backups/restore/backup-1/validate":
			validationRequests.Add(1)
			_, _ = writer.Write([]byte(`{"id":"backup-1","name":"full.tar.gz","type":"full","artifactBytes":4096,"includesDatabase":true,"includesFiles":true,"databaseEngine":"postgresql","databaseTarget":"all","databaseRecovery":true,"filesRollback":true}`))
		case "POST /api/backups/restore/backup-1":
			restoreRequests.Add(1)
			_, _ = writer.Write([]byte(`{"jobId":"restore-job-1","message":"restore started in background"}`))
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
	model.tab = tuiTabBackups
	model.snapshot.Selected = model.snapshot.Targets[0]
	model.backupsLoaded = true
	model.backupsTarget = localTargetID
	model.backups = []tuiBackupItem{{ID: "backup-1", Name: "full.tar.gz", Type: "full", Status: "completed", Size: 4096}}

	updated, command := model.updateKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogChoices || len(model.dialog.Options) != 2 {
		t.Fatalf("backup action menu = %#v, command=%v", model.dialog, command != nil)
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command == nil || model.dialog.Mode != tuiDialogNone || restoreRequests.Load() != 0 {
		t.Fatalf("validation start = %#v, command=%v, restores=%d", model.dialog, command != nil, restoreRequests.Load())
	}
	message := command()
	validationMessage, ok := message.(tuiBackupValidationMsg)
	if !ok || validationMessage.Err != nil || validationMessage.Validation.ID != "backup-1" {
		t.Fatalf("validation result = %#v", message)
	}
	if validationRequests.Load() != 1 || restoreRequests.Load() != 0 {
		t.Fatalf("validation=%d restore=%d", validationRequests.Load(), restoreRequests.Load())
	}

	updated, command = model.Update(validationMessage)
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogBackupValidation {
		t.Fatalf("validation receipt dialog = %#v, command=%v", model.dialog, command != nil)
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogBackupValidation || restoreRequests.Load() != 0 {
		t.Fatalf("enter bypassed receipt = %#v, command=%v", model.dialog, command != nil)
	}
	updated, command = model.updateDialogKey("r")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || model.dialog.Operation.Action != "restore" {
		t.Fatalf("restore confirmation = %#v, command=%v", model.dialog, command != nil)
	}
	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || restoreRequests.Load() != 0 {
		t.Fatalf("enter confirmed restore: command=%v restores=%d", command != nil, restoreRequests.Load())
	}
	updated, command = model.updateDialogKey("y")
	model = updated.(tuiModel)
	if command == nil || !model.operating {
		t.Fatal("explicit y did not start restore")
	}
	operationMessage, ok := command().(tuiOperationMsg)
	if !ok || operationMessage.Err != nil || operationMessage.Message != "restore started in background · job restore-job-1" {
		t.Fatalf("restore result = %#v", operationMessage)
	}
	if restoreRequests.Load() != 1 {
		t.Fatalf("restore requests = %d", restoreRequests.Load())
	}
}

func TestTUIBackupJobViewerRefreshesDetailsWithoutMutation(t *testing.T) {
	t.Parallel()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodGet || request.URL.Path != "/api/backups/jobs/job-1" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"id":"job-1","type":"full","source":"manual","status":"running","phase":"archive","progress":73,"message":"archive advancing","startedAt":"2026-08-27T10:00:00Z","etaSeconds":12,"bytesDone":3072,"bytesTotal":4096,"logs":["started","archive advancing"]}`))
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	model := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
	model.loading = false
	model.tab = tuiTabBackups
	model.snapshot.Selected = model.snapshot.Targets[0]
	model.backupsLoaded = true
	model.backupsTarget = localTargetID
	model.backupJobs = []tuiBackupJob{{
		ID: "job-1", Type: "full", Source: "manual", Status: "running", Phase: "files", Progress: 50,
		StartedAt: time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC), Logs: []string{"started"},
	}}

	updated, command := model.updateKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogLogs || model.dialog.BackupJob.ID != "job-1" || requests.Load() != 0 {
		t.Fatalf("job viewer = %#v, command=%v, requests=%d", model.dialog, command != nil, requests.Load())
	}
	if rendered := model.renderDialog(100, 30); !strings.Contains(rendered, "50%") || !strings.Contains(rendered, "started") {
		t.Fatalf("job dialog = %q", rendered)
	}
	updated, command = model.updateDialogKey("r")
	model = updated.(tuiModel)
	if command == nil || !model.resourceLoading {
		t.Fatal("job reload did not start")
	}
	message, ok := command().(tuiBackupJobMsg)
	if !ok || message.Err != nil || message.Job.Progress != 73 || requests.Load() != 1 {
		t.Fatalf("job refresh = %#v, requests=%d", message, requests.Load())
	}
	updated, command = model.Update(message)
	model = updated.(tuiModel)
	if command != nil || model.dialog.BackupJob.Progress != 73 || len(model.dialog.LogLines) < 5 || model.backupJobs[0].Progress != 73 {
		t.Fatalf("refreshed model = %#v, command=%v", model.dialog, command != nil)
	}
}

func TestTUIActiveBackupJobSchedulesInventoryRefreshOnTick(t *testing.T) {
	t.Parallel()
	model := tuiModel{
		ctx: context.Background(), client: &apiClient{}, refreshInterval: 5 * time.Second,
		tab: tuiTabBackups, selectedTargetID: localTargetID,
		snapshot:      tuiSnapshot{Selected: initialTUITargets()[0], Targets: initialTUITargets()},
		backupsLoaded: true, backupJobs: []tuiBackupJob{{ID: "job-1", Status: "running"}},
	}
	updated, command := model.Update(tuiTickMsg(time.Now()))
	model = updated.(tuiModel)
	if command == nil || !model.loading || !model.resourceLoading {
		t.Fatalf("active refresh state: loading=%v resourceLoading=%v command=%v", model.loading, model.resourceLoading, command != nil)
	}
	message := command()
	batch, ok := message.(tea.BatchMsg)
	if !ok || len(batch) != 3 {
		t.Fatalf("active refresh batch = %#v", message)
	}
}

func TestTUIBackupCreateAndManagedPlanRunUseBoundedActions(t *testing.T) {
	t.Parallel()
	var createRequests atomic.Int32
	var runRequests atomic.Int32
	var deleteRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.Method + " " + request.URL.Path {
		case "POST /api/backups":
			createRequests.Add(1)
			var payload struct {
				Type        string   `json:"type"`
				Engine      string   `json:"engine"`
				Compression int      `json:"compression"`
				Retention   int      `json:"retention"`
				Vhosts      []string `json:"vhosts"`
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Errorf("decode create payload: %v", err)
			}
			if payload.Type != "full" || payload.Engine != "postgresql" || payload.Compression != 6 || payload.Retention != 14 || len(payload.Vhosts) != 2 || payload.Vhosts[0] != "alpha.example" || payload.Vhosts[1] != "beta.example" {
				t.Errorf("create payload = %#v", payload)
			}
			_, _ = writer.Write([]byte(`{"jobId":"backup-job-1","message":"backup started in background"}`))
		case "POST /api/nodes/edge-1/backups/nightly/run":
			runRequests.Add(1)
			_, _ = writer.Write([]byte(`{"message":"backup plan completed"}`))
		case "DELETE /api/backups/backup-1":
			deleteRequests.Add(1)
			_, _ = writer.Write([]byte(`{"status":"deleted","id":"backup-1"}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	local := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
	local.loading = false
	local.tab = tuiTabBackups
	local.snapshot.Selected = local.snapshot.Targets[0]
	local.backupVhosts = []string{"alpha.example", "beta.example"}
	local.backupVhostMax = 16
	updated, command := local.updateKey("c")
	local = updated.(tuiModel)
	if command != nil || local.dialog.Mode != tuiDialogChoices || len(local.dialog.Options) != 5 || createRequests.Load() != 0 {
		t.Fatalf("create profiles = %#v, command=%v", local.dialog, command != nil)
	}
	updated, command = local.updateDialogKey("enter")
	local = updated.(tuiModel)
	if command != nil || local.dialog.Mode != tuiDialogChoices || local.dialog.Title != "Backup file scope" || len(local.dialog.Options) != 2 || createRequests.Load() != 0 {
		t.Fatalf("scope choices = %#v, command=%v", local.dialog, command != nil)
	}
	updated, _ = local.updateDialogKey("down")
	local = updated.(tuiModel)
	updated, command = local.updateDialogKey("enter")
	local = updated.(tuiModel)
	if command != nil || local.dialog.Mode != tuiDialogBackupVhosts || len(local.dialog.BackupVhosts) != 2 {
		t.Fatalf("vhost choices = %#v, command=%v", local.dialog, command != nil)
	}
	updated, _ = local.updateDialogKey(" ")
	local = updated.(tuiModel)
	updated, _ = local.updateDialogKey("down")
	local = updated.(tuiModel)
	updated, _ = local.updateDialogKey(" ")
	local = updated.(tuiModel)
	updated, command = local.updateDialogKey("enter")
	local = updated.(tuiModel)
	if command != nil || local.dialog.Mode != tuiDialogChoices || local.dialog.Title != "Full-backup retention" || len(local.dialog.Options) != 4 || createRequests.Load() != 0 {
		t.Fatalf("retention choices = %#v, command=%v", local.dialog, command != nil)
	}
	updated, _ = local.updateDialogKey("down")
	local = updated.(tuiModel)
	updated, _ = local.updateDialogKey("down")
	local = updated.(tuiModel)
	updated, command = local.updateDialogKey("enter")
	local = updated.(tuiModel)
	if command != nil || local.dialog.Mode != tuiDialogConfirm || local.dialog.Operation.Action != "create" || local.dialog.Operation.BackupCreate.Type != "full" || local.dialog.Operation.BackupCreate.Retention != 14 || !local.dialog.Operation.Dangerous || createRequests.Load() != 0 {
		t.Fatalf("create confirmation = %#v, command=%v", local.dialog, command != nil)
	}
	updated, command = local.updateDialogKey("enter")
	local = updated.(tuiModel)
	if command != nil || createRequests.Load() != 0 {
		t.Fatalf("enter confirmed backup creation: command=%v requests=%d", command != nil, createRequests.Load())
	}
	updated, command = local.updateDialogKey("y")
	local = updated.(tuiModel)
	if command == nil || !local.operating {
		t.Fatal("explicit y did not start backup creation")
	}
	createMessage, ok := command().(tuiOperationMsg)
	if !ok || createMessage.Err != nil || !strings.Contains(createMessage.Message, "backup-job-1") || createRequests.Load() != 1 {
		t.Fatalf("create result = %#v, requests=%d", createMessage, createRequests.Load())
	}

	managedTarget := tuiTarget{
		ID: "edge-1", Name: "Edge", Online: true,
		Capabilities: map[string]bool{agenthub.CapabilityBackupRun: true},
	}
	managed := newTUIModel(context.Background(), client, server.URL, 5*time.Second)
	managed.loading = false
	managed.tab = tuiTabBackups
	managed.selectedTargetID = managedTarget.ID
	managed.snapshot.Selected = managedTarget
	managed.backupsLoaded = true
	managed.backupsTarget = managedTarget.ID
	managed.backups = []tuiBackupItem{{ID: "nightly", Name: "Nightly", Managed: true, Service: "backup-nightly.service"}}
	updated, command = managed.updateKey("enter")
	managed = updated.(tuiModel)
	if command != nil || managed.dialog.Mode != tuiDialogChoices || runRequests.Load() != 0 {
		t.Fatalf("plan actions = %#v, command=%v", managed.dialog, command != nil)
	}
	updated, command = managed.updateDialogKey("enter")
	managed = updated.(tuiModel)
	if command != nil || managed.dialog.Mode != tuiDialogConfirm || managed.dialog.Operation.Action != "run" {
		t.Fatalf("plan confirmation = %#v, command=%v", managed.dialog, command != nil)
	}
	updated, command = managed.updateDialogKey("y")
	managed = updated.(tuiModel)
	if command == nil || !managed.operating {
		t.Fatal("explicit y did not start managed backup")
	}
	runMessage, ok := command().(tuiOperationMsg)
	if !ok || runMessage.Err != nil || runMessage.Message != "backup plan completed" || runRequests.Load() != 1 {
		t.Fatalf("run result = %#v, requests=%d", runMessage, runRequests.Load())
	}

	deleted, err := runTUIOperation(context.Background(), client, tuiOperation{
		Kind: tuiOperationBackup, Target: initialTUITargets()[0], Action: "delete",
		Backup: tuiBackupItem{ID: "backup-1", Name: "full.tar.gz", Type: "full", Status: "completed"},
	})
	if err != nil || deleted != "Backup deleted" || deleteRequests.Load() != 1 {
		t.Fatalf("delete result = %q, err=%v, requests=%d", deleted, err, deleteRequests.Load())
	}
}

func TestTUIBackupRestoreRejectsMissingRecoveryReceipt(t *testing.T) {
	t.Parallel()
	client, err := newAPIClient("http://127.0.0.1:1", "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	item := tuiBackupItem{ID: "backup-1", Name: "full.tar.gz", Type: "full", Status: "completed"}
	_, err = runTUIOperation(context.Background(), client, tuiOperation{
		Kind: tuiOperationBackup, Target: initialTUITargets()[0], Action: "restore", Backup: item,
		BackupValidation: tuiBackupValidation{ID: "backup-1", IncludesDatabase: true, DatabaseRecovery: false},
	})
	if err == nil || !strings.Contains(err.Error(), "database recovery") {
		t.Fatalf("restore guard error = %v", err)
	}
}

func TestTUIBackupCreateProfilesMapToExplicitPayloads(t *testing.T) {
	t.Parallel()
	type createPayload struct {
		Type        string   `json:"type"`
		Engine      string   `json:"engine"`
		Compression int      `json:"compression"`
		Retention   int      `json:"retention"`
		Vhosts      []string `json:"vhosts"`
	}
	payloads := make(chan createPayload, 3)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if request.Method != http.MethodPost || request.URL.Path != "/api/backups" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
			http.NotFound(writer, request)
			return
		}
		var payload createPayload
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		payloads <- payload
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"jobId":"job-1","message":"backup started in background"}`))
	}))
	defer server.Close()
	client, err := newAPIClient(server.URL, "test-token", time.Second)
	if err != nil {
		t.Fatal(err)
	}

	for _, testCase := range []struct {
		name string
		spec tuiBackupCreateSpec
		want createPayload
	}{
		{name: "full MariaDB with retention", spec: tuiBackupCreateSpec{Type: "full", Engine: "mariadb", Retention: 14}, want: createPayload{Type: "full", Engine: "mariadb", Compression: 6, Retention: 14, Vhosts: []string{}}},
		{name: "all PostgreSQL databases", spec: tuiBackupCreateSpec{Type: "database", Engine: "postgresql"}, want: createPayload{Type: "database", Engine: "postgresql", Compression: 6, Vhosts: []string{}}},
		{name: "all website files", spec: tuiBackupCreateSpec{Type: "files", Engine: "postgresql"}, want: createPayload{Type: "files", Engine: "postgresql", Compression: 6, Vhosts: []string{}}},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			message, err := runTUIOperation(context.Background(), client, tuiOperation{
				Kind: tuiOperationBackup, Target: initialTUITargets()[0], Action: "create", BackupCreate: testCase.spec,
			})
			if err != nil || !strings.Contains(message, "job job-1") {
				t.Fatalf("result = %q, err=%v", message, err)
			}
			payload := <-payloads
			if payload.Type != testCase.want.Type || payload.Engine != testCase.want.Engine || payload.Compression != testCase.want.Compression || payload.Retention != testCase.want.Retention || payload.Vhosts == nil || len(payload.Vhosts) != len(testCase.want.Vhosts) {
				t.Fatalf("payload = %#v, want %#v", payload, testCase.want)
			}
		})
	}

	before := requests.Load()
	_, err = runTUIOperation(context.Background(), client, tuiOperation{
		Kind: tuiOperationBackup, Target: initialTUITargets()[0], Action: "create",
		BackupCreate: tuiBackupCreateSpec{Type: "database", Engine: "postgresql", Retention: 7},
	})
	if err == nil || !strings.Contains(err.Error(), "only for full") || requests.Load() != before {
		t.Fatalf("invalid retention error = %v, requests=%d", err, requests.Load())
	}
}

func TestTUIBackupCreateWizardExposesOnlyFixedProfiles(t *testing.T) {
	t.Parallel()
	model := tuiModel{snapshot: tuiSnapshot{Selected: initialTUITargets()[0]}}
	model.openBackupCreateWizard()
	if model.dialog.Mode != tuiDialogChoices || len(model.dialog.Options) != 5 {
		t.Fatalf("wizard = %#v", model.dialog)
	}
	want := map[string]tuiBackupCreateSpec{
		"create-full-postgresql":     {Type: "full", Engine: "postgresql"},
		"create-full-mariadb":        {Type: "full", Engine: "mariadb"},
		"create-database-postgresql": {Type: "database", Engine: "postgresql"},
		"create-database-mariadb":    {Type: "database", Engine: "mariadb"},
		"create-files":               {Type: "files", Engine: "postgresql"},
	}
	for _, option := range model.dialog.Options {
		spec, ok := backupCreateSpecForAction(option.Action)
		expected := want[option.Action]
		if !ok || spec.Type != expected.Type || spec.Engine != expected.Engine || spec.Retention != expected.Retention || len(spec.Vhosts) != 0 {
			t.Fatalf("option %q = %#v, ok=%v", option.Action, spec, ok)
		}
		if err := validateTUIBackupCreateSpec(spec); err != nil {
			t.Fatalf("option %q: %v", option.Action, err)
		}
	}

	remote := tuiModel{snapshot: tuiSnapshot{Selected: tuiTarget{ID: "edge-1", Name: "Edge", Online: true}}}
	remote.openBackupCreateWizard()
	if remote.dialog.Mode != tuiDialogNone || !remote.noticeError || !strings.Contains(remote.notice, "installation-owned plans") {
		t.Fatalf("remote wizard = %#v, notice=%q", remote.dialog, remote.notice)
	}
}

func TestTUIBackupVhostSelectionEnforcesObservedLimit(t *testing.T) {
	t.Parallel()
	model := tuiModel{
		backupVhosts:   []string{"alpha.example", "beta.example", "gamma.example"},
		backupVhostMax: 2,
	}
	operation := tuiOperation{
		Kind: tuiOperationBackup, Target: initialTUITargets()[0], Action: "create",
		BackupCreate: tuiBackupCreateSpec{Type: "files", Engine: "postgresql"},
	}
	model.openBackupVhostChoices(operation)

	updated, command := model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogBackupVhosts || !model.noticeError || !strings.Contains(model.notice, "at least one") {
		t.Fatalf("empty selection = %#v notice=%q", model.dialog, model.notice)
	}
	for index := 0; index < 2; index++ {
		updated, _ = model.updateDialogKey(" ")
		model = updated.(tuiModel)
		updated, _ = model.updateDialogKey("down")
		model = updated.(tuiModel)
	}
	updated, _ = model.updateDialogKey(" ")
	model = updated.(tuiModel)
	if len(model.dialog.BackupSelected) != 2 || !model.noticeError || !strings.Contains(model.notice, "At most 2") {
		t.Fatalf("limit selection = %#v notice=%q", model.dialog.BackupSelected, model.notice)
	}
	if rendered := model.renderDialog(100, 30); !strings.Contains(rendered, "2 selected") || !strings.Contains(rendered, "maximum 2") {
		t.Fatalf("selection dialog = %q", rendered)
	}

	updated, command = model.updateDialogKey("enter")
	model = updated.(tuiModel)
	if command != nil || model.dialog.Mode != tuiDialogConfirm || len(model.dialog.Operation.BackupCreate.Vhosts) != 2 || model.dialog.Operation.BackupCreate.Vhosts[0] != "alpha.example" || model.dialog.Operation.BackupCreate.Vhosts[1] != "beta.example" {
		t.Fatalf("selected confirmation = %#v command=%v", model.dialog, command != nil)
	}
}
