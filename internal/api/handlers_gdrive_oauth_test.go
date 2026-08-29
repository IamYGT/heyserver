package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIntegration_GDriveOAuthCallback_nilService503(t *testing.T) {
	handler := integrationRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/backups/gdrive/oauth/callback?code=abc&state=xyz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "servis") {
		t.Errorf("body should mention service error, got %q", body)
	}
}

func TestIntegration_GDriveOAuthCallback_missingParamsHTML(t *testing.T) {
	// When service exists but params missing — only runs if gdriveSvc is wired.
	if gdriveSvc == nil {
		t.Skip("gdrive service not initialized in test env")
	}
	handler := integrationRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/backups/gdrive/oauth/callback", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 HTML error page", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "eksik") {
		t.Errorf("body = %q", rec.Body.String())
	}
}
