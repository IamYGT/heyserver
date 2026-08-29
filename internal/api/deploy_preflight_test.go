package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IamYGT/heyserver/internal/services/backup"
)

func TestInternalDeployPreflight_blocksWithoutSecret(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/internal/deploy/preflight", nil)
	rec := httptest.NewRecorder()
	handleInternalDeployPreflight("secret")(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", rec.Code)
	}
}

func TestInternalDeployPreflight_okWhenNoSnapshot(t *testing.T) {
	backupSvc = backup.New()
	req := httptest.NewRequest(http.MethodGet, "/api/internal/deploy/preflight", nil)
	req.RemoteAddr = "127.0.0.1:12345"
	req.Header.Set("X-Cron-Secret", "secret")
	rec := httptest.NewRecorder()
	handleInternalDeployPreflight("secret")(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}
