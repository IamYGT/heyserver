package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/services/snapshot"
)

func withTempSnapshotService(t *testing.T) string {
	t.Helper()
	previous := snapshotSvc
	dir := t.TempDir()
	snapshotSvc = snapshot.New(dir, filepath.Join(dir, "vhosts"), filepath.Join(dir, "backups"), 0, "", "", "", nil, nil)
	t.Cleanup(func() { snapshotSvc = previous })
	return dir
}

func TestWriteSnapshotErrorMapsSettingsUnavailableTo503(t *testing.T) {
	rec := httptest.NewRecorder()
	writeSnapshotError(rec, snapshot.ErrSettingsUnavailable)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestWriteSnapshotErrorMapsDestinationBoundaries(t *testing.T) {
	for _, test := range []struct {
		err  error
		want int
	}{
		{err: snapshot.ErrDestinationUnavailable, want: http.StatusServiceUnavailable},
		{err: snapshot.ErrUnsupportedCapability, want: http.StatusUnprocessableEntity},
	} {
		rec := httptest.NewRecorder()
		writeSnapshotError(rec, test.err)
		if rec.Code != test.want {
			t.Fatalf("error=%v status=%d body=%s", test.err, rec.Code, rec.Body.String())
		}
	}
}

func TestHandleSnapshotSettingsRequiresCompleteClosedPolicy(t *testing.T) {
	_ = withTempSnapshotService(t)
	for _, body := range []string{
		`{"repoFolder":"hserver-snapshots","enabledPaths":[],"keepDaily":14,"keepWeekly":8,"keepMonthly":6,"passwordAcknowledged":false}`,
		`{"destination":"ftp","repoFolder":"hserver-snapshots","enabledPaths":[],"keepDaily":14,"keepWeekly":8,"keepMonthly":6,"passwordAcknowledged":false}`,
		`{"repoFolder":"hserver-snapshots","enabledPaths":[],"keepDaily":14,"keepWeekly":8,"keepMonthly":6,"passwordAcknowledged":false,"unknown":true}`,
		`{"repoFolder":"hserver-snapshots","keepDaily":14,"keepWeekly":8,"keepMonthly":6,"passwordAcknowledged":false}`,
		`{"repoFolder":"hserver-snapshots","enabledPaths":[],"keepDaily":14,"keepWeekly":8,"keepMonthly":6}`,
		`{"repoFolder":"../other","enabledPaths":[],"keepDaily":14,"keepWeekly":8,"keepMonthly":6,"passwordAcknowledged":false}`,
		`{"repoFolder":"hserver-snapshots","enabledPaths":[],"keepDaily":14,"keepWeekly":8,"keepMonthly":6,"passwordAcknowledged":false} {}`,
	} {
		req := httptest.NewRequest(http.MethodPut, "/api/backups/snapshot/settings", strings.NewReader(body))
		rec := httptest.NewRecorder()
		handleSnapshotSettings(&config.Config{})(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestHandleSnapshotSettingsReplacesPolicyAtomically(t *testing.T) {
	dir := withTempSnapshotService(t)
	body := `{"destination":"s3","repoFolder":"agency/snapshots","enabledPaths":["vhosts","nginx"],"keepDaily":30,"keepWeekly":12,"keepMonthly":12,"passwordAcknowledged":true}`
	put := httptest.NewRequest(http.MethodPut, "/api/backups/snapshot/settings", strings.NewReader(body))
	putRec := httptest.NewRecorder()
	handleSnapshotSettings(&config.Config{})(putRec, put)
	if putRec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", putRec.Code, putRec.Body.String())
	}

	raw, err := os.ReadFile(filepath.Join(dir, "snapshot-settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings snapshot.Settings
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Destination != snapshot.DestinationS3 || settings.RepoFolder != "agency/snapshots" || settings.KeepDaily != 30 || !settings.PasswordAcknowledged {
		t.Fatalf("settings=%+v", settings)
	}
	if len(settings.EnabledPaths) != 2 || settings.EnabledPaths[1] != "nginx" {
		t.Fatalf("enabledPaths=%v", settings.EnabledPaths)
	}
}

func TestHandleSnapshotRestoreRejectsUnboundedInputBeforeDriveAccess(t *testing.T) {
	_ = withTempSnapshotService(t)
	for _, body := range []string{
		`{"snapshotId":"abcdef12","target":"/etc"}`,
		`{"snapshotId":"--help"}`,
		`{"snapshotId":"abcdef12","includes":["/etc/shadow"]}`,
		`{"snapshotId":"abcdef12","manifestIds":["unknown"]}`,
		`{"snapshotId":"abcdef12","vhosts":["../private"]}`,
		`{"snapshotId":"abcdef12"} {}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/backups/snapshot/restore", strings.NewReader(body))
		rec := httptest.NewRecorder()
		handleSnapshotRestore(&config.Config{})(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestHandleSnapshotRunRejectsIgnoredBody(t *testing.T) {
	_ = withTempSnapshotService(t)
	req := httptest.NewRequest(http.MethodPost, "/api/backups/snapshot/run", strings.NewReader(`{}`))
	rec := httptest.NewRecorder()
	handleSnapshotRun()(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "request body must be empty") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHandleSnapshotPurgeRequiresClosedObservedConfirmation(t *testing.T) {
	_ = withTempSnapshotService(t)
	for _, body := range []string{
		`{}`,
		`{"repoFolder":"hserver-snapshots","confirmation":"PURGE"}`,
		`{"repoFolder":"other-snapshots","confirmation":"purge-snapshot-repository"}`,
		`{"repoFolder":"hserver-snapshots","confirmation":"purge-snapshot-repository","target":"other"}`,
		`{"repoFolder":"hserver-snapshots","confirmation":"purge-snapshot-repository"} {}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/backups/snapshot/purge-repo", strings.NewReader(body))
		rec := httptest.NewRecorder()
		handleSnapshotPurgeRepo(&config.Config{})(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("body=%s status=%d response=%s", body, rec.Code, rec.Body.String())
		}
	}
}
