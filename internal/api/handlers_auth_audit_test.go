package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/testutil"
)

func TestBuildAuditEntry(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	req.RemoteAddr = "203.0.113.10:443"
	user := testutil.MakeUser(9, "auditor@test.local", models.RoleAdmin)
	entry := buildAuditEntry(user.ID, user.Name, "login", "auth", "success", req)
	if entry.UserID != 9 || entry.Action != "login" {
		t.Errorf("entry = %+v", entry)
	}
	if entry.IP == "" {
		t.Error("expected IP from request")
	}
}

func TestIntegration_AuditListAuthenticated(t *testing.T) {
	handler := integrationRouter(t)
	user := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	req := testutil.NewRequest(t, http.MethodGet, "/api/audit?limit=10", testutil.MakeToken(t, user))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
}

func TestIntegration_AuditListRejectsInvalidServerScope(t *testing.T) {
	handler := integrationRouter(t)
	user := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	req := testutil.NewRequest(t, http.MethodGet, "/api/audit?server=../invalid", testutil.MakeToken(t, user))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body=%s", rec.Code, rec.Body.String())
	}
}

func TestIntegration_BackupListAuthenticated(t *testing.T) {
	withTempBackupService(t)
	handler := integrationRouter(t)
	user := testutil.MakeUser(1, "viewer@test.com", models.RoleViewer)
	req := testutil.NewRequest(t, http.MethodGet, "/api/backups", testutil.MakeToken(t, user))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
}

func TestIntegration_CronJobsAuthenticated(t *testing.T) {
	withFakeCrontab(t)
	handler := integrationRouter(t)
	user := testutil.MakeUser(1, "viewer@test.com", models.RoleViewer)
	req := testutil.NewRequest(t, http.MethodGet, "/api/cron/jobs", testutil.MakeToken(t, user))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
}

func TestIntegration_UptimeSummaryWithEngine(t *testing.T) {
	handler := NewRouter(testutil.TestConfig(), testutil.MinimalWebFS(t), uptimeTestDeps(t))
	user := testutil.MakeUser(1, "viewer@test.com", models.RoleViewer)
	req := testutil.NewRequest(t, http.MethodGet, "/api/uptime/monitors/summary", testutil.MakeToken(t, user))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
}
