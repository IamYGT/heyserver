package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGdriveServiceUnavailable(t *testing.T) {
	t.Parallel()
	saved := gdriveSvc
	gdriveSvc = nil
	t.Cleanup(func() { gdriveSvc = saved })

	rec := httptest.NewRecorder()
	if !gdriveServiceUnavailable(rec) {
		t.Fatal("expected unavailable")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}

func TestSnapshotServiceUnavailable(t *testing.T) {
	t.Parallel()
	saved := snapshotSvc
	snapshotSvc = nil
	t.Cleanup(func() { snapshotSvc = saved })

	rec := httptest.NewRecorder()
	if !snapshotServiceUnavailable(rec) {
		t.Fatal("expected unavailable")
	}
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rec.Code)
	}
}
