package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IamYGT/heyserver/internal/testutil"
)

func TestIntegration_BackupPurgeInvalidWithoutAuth(t *testing.T) {
	handler := integrationRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/backups/purge-invalid", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestIntegration_BackupJobDismissWithoutAuth(t *testing.T) {
	handler := integrationRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/backups/jobs/1/dismiss", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestIntegration_InternalDeployPreflightNonLoopback(t *testing.T) {
	handler := integrationRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/internal/deploy/preflight", nil)
	req.RemoteAddr = "203.0.113.5:1234"
	req.Header.Set("X-Cron-Secret", "test-cron-secret-32-bytes-min!!")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d body=%s", rec.Code, http.StatusForbidden, rec.Body.String())
	}

	var body map[string]string
	testutil.ParseJSON(t, rec, &body)
	if body["error"] != "preflight only allowed from localhost" {
		t.Errorf("error = %q, want %q", body["error"], "preflight only allowed from localhost")
	}
}
