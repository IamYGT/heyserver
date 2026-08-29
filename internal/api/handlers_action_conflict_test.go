package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/services/systemactions"
)

type staticSystemActionStatus struct {
	status systemactions.ActionStatus
}

type stubDiskMaintenance struct {
	err      error
	begins   int
	released bool
}

func (s *stubDiskMaintenance) BeginMaintenance(_ string) (func(), error) {
	s.begins++
	if s.err != nil {
		return nil, s.err
	}
	return func() { s.released = true }, nil
}

func (s staticSystemActionStatus) MaintenanceStatus() systemactions.ActionStatus { return s.status }

func TestSystemActionInProgressReturnsConflict(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeSystemActionError(recorder, systemactions.ErrActionInProgress)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusConflict)
	}
}

func TestSystemActionStatusHandlerReturnsActiveAction(t *testing.T) {
	started := time.Date(2026, 8, 26, 3, 0, 0, 0, time.UTC)
	recorder := httptest.NewRecorder()
	handleSystemActionStatus(staticSystemActionStatus{status: systemactions.ActionStatus{
		Running: true, Action: "temp-clean", StartedAt: started.Format(time.RFC3339Nano),
	}}).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/system/actions/status", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var body systemactions.ActionStatus
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Running || body.Action != "temp-clean" || body.StartedAt != started.Format(time.RFC3339Nano) {
		t.Fatalf("body = %#v", body)
	}
}

func TestDiskCleanupValidatesBeforeMaintenanceAndReturnsConflict(t *testing.T) {
	invalid := &stubDiskMaintenance{}
	recorder := httptest.NewRecorder()
	handleDiskCleanupExecute(invalid).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/disk/cleanup/execute", strings.NewReader(`{"targets":["npm-cache","unknown"]}`)))
	if recorder.Code != http.StatusBadRequest || invalid.begins != 0 {
		t.Fatalf("invalid status=%d begins=%d", recorder.Code, invalid.begins)
	}

	busy := &stubDiskMaintenance{err: systemactions.ErrActionInProgress}
	recorder = httptest.NewRecorder()
	handleDiskCleanupExecute(busy).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/disk/cleanup/execute", strings.NewReader(`{"targets":["npm-cache"]}`)))
	if recorder.Code != http.StatusConflict || busy.begins != 1 {
		t.Fatalf("busy status=%d begins=%d", recorder.Code, busy.begins)
	}
}
