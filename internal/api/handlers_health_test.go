package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IamYGT/heyserver/internal/testutil"
)

func TestHandleHealth_Returns200WithDeps(t *testing.T) {
	deps := contractTestDeps(t)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()

	handleHealth(deps.Settings)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]any
	testutil.ParseJSON(t, rec, &body)

	if body["status"] != "ok" {
		t.Errorf("status = %v, want %q", body["status"], "ok")
	}
	if _, ok := body["version"]; !ok {
		t.Error("response missing version field")
	}
	if _, ok := body["uptime"]; !ok {
		t.Error("response missing uptime field")
	}
}

func TestHandleHealthRejectsUninitializedSettings(t *testing.T) {
	recorder := httptest.NewRecorder()
	handleHealth(nil)(recorder, httptest.NewRequest(http.MethodGet, "/api/health", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d, body = %s", recorder.Code, http.StatusServiceUnavailable, recorder.Body.String())
	}
}
