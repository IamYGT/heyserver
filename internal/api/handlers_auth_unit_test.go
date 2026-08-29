package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/testutil"
)

func TestBuildAuditEntry_forwardedFor(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.42, 10.0.0.1")

	entry := buildAuditEntry(1, "admin", "login", "auth", "ok", req)
	if entry.IP != "198.51.100.42" {
		t.Fatalf("IP = %q, want client from X-Forwarded-For", entry.IP)
	}
	if entry.UserID != 1 || entry.Action != "login" {
		t.Fatalf("entry = %+v", entry)
	}
}

func TestIntegration_HandleMeWithToken(t *testing.T) {
	handler := integrationRouter(t)
	user := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	req := testutil.NewRequest(t, http.MethodGet, "/api/auth/me", testutil.MakeToken(t, user))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 body=%s", rec.Code, rec.Body.String())
	}
	var body models.User
	testutil.ParseJSON(t, rec, &body)
	if body.Email != user.Email {
		t.Fatalf("user = %+v", body)
	}
}

func TestIntegration_HandleLogout(t *testing.T) {
	handler := integrationRouter(t)
	user := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	req := testutil.NewRequest(t, http.MethodPost, "/api/auth/logout", testutil.MakeToken(t, user))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}
