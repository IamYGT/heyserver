package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/backup"
	"github.com/IamYGT/heyserver/internal/testutil"
)

func withTempBackupService(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("BACKUP_DIR", dir)
	vhostsRoot := filepath.Join(dir, "vhosts")
	if err := os.MkdirAll(vhostsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	previous := backupSvc
	backupSvc = backup.NewAtWithVhostsRoot(dir, vhostsRoot)
	t.Cleanup(func() { backupSvc = previous })
	return dir
}

func withFakeCrontab(t *testing.T) {
	t.Helper()
	binDir := t.TempDir()
	crontabPath := filepath.Join(binDir, "crontab")
	if err := os.WriteFile(crontabPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir)
}

func waitForBackupJobTerminal(t *testing.T, service *backup.Service, jobID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		job := service.GetJob(jobID)
		if job != nil && (job.Status == backup.JobDone || job.Status == backup.JobFailed) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("backup job %s did not reach a terminal state", jobID)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestHandleBackupCreate_acceptsJSON(t *testing.T) {
	withTempBackupService(t)
	body := `{"type":"database","name":"nightly","database":"hserver_test_missing"}`
	req := httptest.NewRequest(http.MethodPost, "/api/backups", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handleBackupCreate()(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	jobID, _ := response["jobId"].(string)
	if jobID == "" || response["status"] != "pending" || response["message"] != "backup started in background" {
		t.Fatalf("response=%+v", response)
	}
	waitForBackupJobTerminal(t, backupSvc, jobID)
}

func TestHandleBackupCreate_rejectsUnknownTypeBeforeStartingJob(t *testing.T) {
	withTempBackupService(t)
	req := httptest.NewRequest(http.MethodPost, "/api/backups", strings.NewReader(`{"type":"archive"}`))
	rec := httptest.NewRecorder()
	handleBackupCreate()(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if active := backupSvc.ListActiveJobs(); len(active) != 0 {
		t.Fatalf("invalid request started jobs: %+v", active)
	}
}

func TestHandleBackupCreate_badJSON(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/backups", strings.NewReader("{bad"))
	rec := httptest.NewRecorder()
	handleBackupCreate()(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandleBackupCreate_rejectsUnknownFields(t *testing.T) {
	withTempBackupService(t)
	req := httptest.NewRequest(http.MethodPost, "/api/backups", strings.NewReader(`{"type":"full","note":"silently ignored before contract v5"}`))
	rec := httptest.NewRecorder()
	handleBackupCreate()(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if active := backupSvc.ListActiveJobs(); len(active) != 0 {
		t.Fatalf("unknown request field started jobs: %+v", active)
	}
}

func TestHandleBackupCreate_rejectsCallerSelectedFilesRoot(t *testing.T) {
	withTempBackupService(t)
	req := httptest.NewRequest(http.MethodPost, "/api/backups", strings.NewReader(`{"type":"files","filesRoot":"/etc"}`))
	rec := httptest.NewRecorder()
	handleBackupCreate()(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if active := backupSvc.ListActiveJobs(); len(active) != 0 {
		t.Fatalf("caller-selected root started jobs: %+v", active)
	}
}

func TestHandleBackupCreate_bindsSelectedVhostToObservedRoot(t *testing.T) {
	dir := withTempBackupService(t)
	if err := os.MkdirAll(filepath.Join(dir, "vhosts", "example.com"), 0o755); err != nil {
		t.Fatal(err)
	}

	bad := httptest.NewRequest(http.MethodPost, "/api/backups", strings.NewReader(`{"type":"files","vhosts":["../etc"]}`))
	badRec := httptest.NewRecorder()
	handleBackupCreate()(badRec, bad)
	if badRec.Code != http.StatusBadRequest {
		t.Fatalf("traversal status=%d body=%s", badRec.Code, badRec.Body.String())
	}

	good := httptest.NewRequest(http.MethodPost, "/api/backups", strings.NewReader(`{"type":"files","vhosts":["example.com"]}`))
	goodRec := httptest.NewRecorder()
	handleBackupCreate()(goodRec, good)
	if goodRec.Code != http.StatusAccepted {
		t.Fatalf("observed vhost status=%d body=%s", goodRec.Code, goodRec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(goodRec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	jobID, _ := response["jobId"].(string)
	if jobID == "" {
		t.Fatalf("response=%+v", response)
	}
	waitForBackupJobTerminal(t, backupSvc, jobID)
}

func TestHandleBackupTargetsReturnsObservedPortableIdentitiesWithoutRoot(t *testing.T) {
	dir := withTempBackupService(t)
	vhostsRoot := filepath.Join(dir, "vhosts")
	for _, name := range []string{"beta.example", "alpha.example"} {
		if err := os.Mkdir(filepath.Join(vhostsRoot, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(vhostsRoot, "not-a-vhost"), []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(vhostsRoot, "linked.example")); err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	handleBackupTargets()(rec, httptest.NewRequest(http.MethodGet, "/api/backups/targets", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Vhosts            []string `json:"vhosts"`
		MaxSelectedVhosts int      `json:"maxSelectedVhosts"`
		EmptySelection    string   `json:"emptySelection"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha.example", "beta.example"}
	if len(response.Vhosts) != len(want) || response.Vhosts[0] != want[0] || response.Vhosts[1] != want[1] {
		t.Fatalf("vhosts=%#v want=%#v", response.Vhosts, want)
	}
	if response.MaxSelectedVhosts != 16 || response.EmptySelection != "all-configured-vhosts" {
		t.Fatalf("response=%+v", response)
	}
	if strings.Contains(rec.Body.String(), vhostsRoot) || strings.Contains(rec.Body.String(), dir) {
		t.Fatalf("response exposed installation path: %s", rec.Body.String())
	}
}

func TestHandleBackupTargetsFailsClosedWhenConfiguredRootIsUnavailable(t *testing.T) {
	previous := backupSvc
	missingRoot := filepath.Join(t.TempDir(), "missing")
	backupSvc = backup.NewAtWithVhostsRoot(t.TempDir(), missingRoot)
	t.Cleanup(func() { backupSvc = previous })

	rec := httptest.NewRecorder()
	handleBackupTargets()(rec, httptest.NewRequest(http.MethodGet, "/api/backups/targets", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), missingRoot) {
		t.Fatalf("error response exposed installation path: %s", rec.Body.String())
	}
}

func TestHandleBackupScheduleList_empty(t *testing.T) {
	withTempBackupService(t)
	withFakeCrontab(t)
	rec := httptest.NewRecorder()
	handleBackupScheduleList()(rec, httptest.NewRequest(http.MethodGet, "/api/backups/schedules", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleBackupScheduleListReportsRetentionAsCount(t *testing.T) {
	withTempBackupService(t)
	binDir := t.TempDir()
	crontabPath := filepath.Join(binDir, "crontab")
	content := "#!/bin/sh\nprintf '%s\\n' '0 3 * * * /var/lib/hserver/backups/run-backup.sh type=full retention=7 # hserver-backup'\n"
	if err := os.WriteFile(crontabPath, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":/usr/bin:/bin")

	rec := httptest.NewRecorder()
	handleBackupScheduleList()(rec, httptest.NewRequest(http.MethodGet, "/api/backups/schedules", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Schedules []map[string]any `json:"schedules"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Schedules) != 1 || response.Schedules[0]["retention_count"] != float64(7) {
		t.Fatalf("schedules=%+v", response.Schedules)
	}
	if response.Schedules[0]["retention_days"] != float64(7) {
		t.Fatalf("compatibility alias missing: %+v", response.Schedules[0])
	}
}

func TestHandleBackupScheduleListReportsDatabaseWithoutExpandingRawLine(t *testing.T) {
	withTempBackupService(t)
	binDir := t.TempDir()
	databaseRawLine := "15 2 * * * /srv/hserver/backups/run-backup.sh type=database db=customer_prod retention=14 # hserver-backup"
	fullRawLine := "0 3 * * * /srv/hserver/backups/run-backup.sh type=full retention=7 # hserver-backup"
	filesRawLine := "30 4 * * 0 /srv/hserver/backups/run-backup.sh type=files retention=5 # hserver-backup"
	crontabPath := filepath.Join(binDir, "crontab")
	content := "#!/bin/sh\nprintf '%s\\n' '" + databaseRawLine + "' '" + fullRawLine + "' '" + filesRawLine + "'\n"
	if err := os.WriteFile(crontabPath, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":/usr/bin:/bin")

	rec := httptest.NewRecorder()
	handleBackupScheduleList()(rec, httptest.NewRequest(http.MethodGet, "/api/backups/schedules", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Schedules []map[string]any `json:"schedules"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Schedules) != 3 {
		t.Fatalf("schedules=%+v", response.Schedules)
	}
	checks := []struct {
		name     string
		item     map[string]any
		database string
		rawLine  string
	}{
		{name: "database", item: response.Schedules[0], database: "customer_prod", rawLine: databaseRawLine},
		{name: "full", item: response.Schedules[1], database: "", rawLine: fullRawLine},
		{name: "files", item: response.Schedules[2], database: "", rawLine: filesRawLine},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			if got, ok := check.item["database"].(string); !ok || got != check.database {
				t.Fatalf("database=%#v want=%q", check.item["database"], check.database)
			}
			if got, ok := check.item["rawLine"].(string); !ok || got != check.rawLine {
				t.Fatalf("rawLine=%#v want=%q", check.item["rawLine"], check.rawLine)
			}
		})
	}
}

func TestHandleBackupScheduleListLeavesCustomCronUnlabelled(t *testing.T) {
	withTempBackupService(t)
	binDir := t.TempDir()
	crontabPath := filepath.Join(binDir, "crontab")
	content := "#!/bin/sh\nprintf '%s\\n' '0 3 15 * * /var/lib/hserver/backups/run-backup.sh type=full retention=7 # hserver-backup'\n"
	if err := os.WriteFile(crontabPath, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":/usr/bin:/bin")

	rec := httptest.NewRecorder()
	handleBackupScheduleList()(rec, httptest.NewRequest(http.MethodGet, "/api/backups/schedules", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Schedules []map[string]any `json:"schedules"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Schedules) != 1 || response.Schedules[0]["cron"] != "0 3 15 * *" {
		t.Fatalf("schedules=%+v", response.Schedules)
	}
	if _, ok := response.Schedules[0]["frequency"]; ok {
		t.Fatalf("custom cron was mislabelled: %+v", response.Schedules[0])
	}
	if _, ok := response.Schedules[0]["time"]; ok {
		t.Fatalf("custom cron received a misleading preset time: %+v", response.Schedules[0])
	}
}

func TestHandleBackupScheduleSetAcceptsRetentionCount(t *testing.T) {
	withTempBackupService(t)
	binDir := t.TempDir()
	capturePath := filepath.Join(t.TempDir(), "installed-crontab")
	crontabPath := filepath.Join(binDir, "crontab")
	content := "#!/bin/sh\nif [ \"$1\" = \"-l\" ]; then\n  printf '%s\\n' 'no crontab for hserver' >&2\n  exit 1\nfi\ncat > \"$CAPTURE_CRONTAB\"\n"
	if err := os.WriteFile(crontabPath, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":/usr/bin:/bin")
	t.Setenv("CAPTURE_CRONTAB", capturePath)

	req := httptest.NewRequest(http.MethodPost, "/api/backups/schedules", strings.NewReader(`{"frequency":"daily","time":"03:00","retention_count":7}`))
	rec := httptest.NewRecorder()
	handleBackupScheduleSet()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	installed, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(installed), "type=full retention=7") {
		t.Fatalf("installed crontab=%q", installed)
	}
}

func TestBackupScheduleSetRequestAcceptsDocumentedCompatibilityAliases(t *testing.T) {
	for _, test := range []struct {
		name string
		req  backupScheduleSetRequest
		want int
	}{
		{name: "camel case count", req: backupScheduleSetRequest{Cron: "0 3 * * *", RetentionCountLegacy: intPointer(7)}, want: 7},
		{name: "deprecated days name", req: backupScheduleSetRequest{Cron: "0 3 * * *", RetentionDaysLegacy: intPointer(8)}, want: 8},
	} {
		t.Run(test.name, func(t *testing.T) {
			opts, err := test.req.scheduleOptions()
			if err != nil {
				t.Fatal(err)
			}
			if opts.RetentionCount != test.want {
				t.Fatalf("retention=%d want=%d", opts.RetentionCount, test.want)
			}
		})
	}
}

func TestHandleBackupScheduleSetRejectsAmbiguousOrUnknownFields(t *testing.T) {
	withTempBackupService(t)
	for _, body := range []string{
		`{"frequency":"daily","time":"03:00","retention_count":10,"unexpected":true}`,
		`{"cron":"0 3 * * *","frequency":"daily","time":"03:00","retention_count":10}`,
		`{"frequency":"daily","time":"03:00","retention_count":10,"retentionCount":10}`,
		`{"frequency":"daily","retention_count":10}`,
		`{"frequency":"daily","time":"03:00","retention_count":10} {}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/backups/schedules", strings.NewReader(body))
		rec := httptest.NewRecorder()
		handleBackupScheduleSet()(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestHandleBackupScheduleSetRejectsInvalidOptions(t *testing.T) {
	withTempBackupService(t)
	for _, body := range []string{
		`{"frequency":"daily","time":"03:00","retention_count":0}`,
		`{"frequency":"daily","time":"03:00","retention_count":366}`,
		`{"frequency":"daily","time":"03:00","retention_count":10,"type":"archive"}`,
		`{"frequency":"daily","time":"03:00","retention_count":"ten"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/backups/schedules", strings.NewReader(body))
		rec := httptest.NewRecorder()
		handleBackupScheduleSet()(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestHandleBackupScheduleDeleteRequiresExactObservedLine(t *testing.T) {
	withTempBackupService(t)
	for _, body := range []string{
		`{}`,
		`{"rawLine":""}`,
		`{"rawLine":"managed line","unexpected":true}`,
		`{"rawLine":7}`,
	} {
		req := httptest.NewRequest(http.MethodDelete, "/api/backups/schedules", strings.NewReader(body))
		rec := httptest.NewRecorder()
		handleBackupScheduleDelete()(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, rec.Code, rec.Body.String())
		}
	}
}

func intPointer(value int) *int {
	return &value
}

func TestWriteBackupScheduleErrorMapsUnavailableCrontabTo503(t *testing.T) {
	rec := httptest.NewRecorder()
	writeBackupScheduleError(rec, errors.Join(backup.ErrCrontabUnavailable, errors.New("permission denied")))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWriteBackupScheduleErrorMapsTargetErrors(t *testing.T) {
	for _, test := range []struct {
		err  error
		want int
	}{
		{err: backup.ErrInvalidScheduleTarget, want: http.StatusBadRequest},
		{err: backup.ErrInvalidScheduleOptions, want: http.StatusBadRequest},
		{err: backup.ErrScheduleNotFound, want: http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		writeBackupScheduleError(rec, test.err)
		if rec.Code != test.want {
			t.Fatalf("error=%v status=%d body=%s", test.err, rec.Code, rec.Body.String())
		}
	}
}

func TestHandleBackupJobList(t *testing.T) {
	rec := httptest.NewRecorder()
	handleBackupJobList()(rec, httptest.NewRequest(http.MethodGet, "/api/backups/jobs", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandleBackupPurgeInvalid(t *testing.T) {
	withTempBackupService(t)
	rec := httptest.NewRecorder()
	handleBackupPurgeInvalid()(rec, httptest.NewRequest(http.MethodPost, "/api/backups/purge-invalid", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleBackupPurgeOrphanedRequiresExplicitConfirmation(t *testing.T) {
	dir := withTempBackupService(t)
	name := "backup-a-partial-files.tar.gz"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, make([]byte, 2048), 0o600); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/backups/purge-orphaned", strings.NewReader(`{"ids":["backup-a-partial-files"]}`))
	rec := httptest.NewRecorder()
	handleBackupPurgeOrphaned()(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("artifact changed without confirmation: %v", err)
	}
}

func TestHandleBackupPurgeOrphanedDeletesOnlyConfirmedSelection(t *testing.T) {
	dir := withTempBackupService(t)
	name := "backup-a-partial-files.tar.gz"
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, make([]byte, 2048), 0o600); err != nil {
		t.Fatal(err)
	}

	body := `{"ids":["backup-a-partial-files"],"confirm":"DELETE_ORPHANED_PARTIALS"}`
	req := httptest.NewRequest(http.MethodPost, "/api/backups/purge-orphaned", strings.NewReader(body))
	rec := httptest.NewRecorder()
	handleBackupPurgeOrphaned()(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("artifact still exists: %v", err)
	}
}

func TestNormalizeJobResponse_statusMapping(t *testing.T) {
	job := &backup.Job{ID: "j1", Status: backup.JobDone, Progress: 0}
	resp := normalizeJobResponse(job)
	if resp["status"] != "completed" || resp["progress"] != 100 {
		t.Fatalf("resp = %+v", resp)
	}
	job.Status = backup.JobFailed
	resp = normalizeJobResponse(job)
	if resp["status"] != "failed" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestHandleBackupJobDismiss_missingID(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/backups/jobs//dismiss", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	handleBackupJobDismiss()(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandleBackupDelete_missingID(t *testing.T) {
	req := httptest.NewRequest(http.MethodDelete, "/api/backups/", nil)
	rec := httptest.NewRecorder()
	handleBackupDelete()(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandleBackupRestore_missingID(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/backups/restore/", nil)
	rec := httptest.NewRecorder()
	handleBackupRestore()(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandleBackupRestore_returnsStableAcceptedState(t *testing.T) {
	withTempBackupService(t)
	req := httptest.NewRequest(http.MethodPost, "/api/backups/restore/missing-backup", nil)
	req.SetPathValue("id", "missing-backup")
	rec := httptest.NewRecorder()
	handleBackupRestore()(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	jobID, _ := response["jobId"].(string)
	if jobID == "" || response["status"] != "pending" || response["message"] != "restore started in background" {
		t.Fatalf("response=%+v", response)
	}
	waitForBackupJobTerminal(t, backupSvc, jobID)
}

func TestHandleBackupRestoreValidate_missingID(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/backups/restore//validate", nil)
	rec := httptest.NewRecorder()
	handleBackupRestoreValidate()(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}

func TestHandleBackupJobStream_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/backups/jobs/stream", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	handleBackupJobStream(context.Background())(rec, req)
}

func TestIntegration_BackupCreateAndJobStatus(t *testing.T) {
	withTempBackupService(t)
	handler := integrationRouter(t)
	token := testutil.MakeToken(t, testutil.MakeUser(1, "admin@test.com", models.RoleAdmin))

	req := httptest.NewRequest(http.MethodPost, "/api/backups", strings.NewReader(`{"type":"full"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("create status=%d body=%s", rec.Code, rec.Body.String())
	}

	var created map[string]any
	testutil.ParseJSON(t, rec, &created)
	jobID, _ := created["jobId"].(string)
	if jobID == "" {
		t.Fatalf("missing jobId: %+v", created)
	}

	req = testutil.NewRequest(t, http.MethodGet, "/api/backups/jobs/"+jobID, token)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("job status=%d body=%s", rec.Code, rec.Body.String())
	}
	waitForBackupJobTerminal(t, backupSvc, jobID)
}
