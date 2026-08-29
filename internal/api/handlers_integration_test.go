package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/testutil"
)

func integrationRouter(t *testing.T) http.Handler {
	t.Helper()
	return NewRouter(testutil.TestConfig(), testutil.MinimalWebFS(t), contractTestDeps(t))
}

func TestIntegration_HealthWithoutAuth(t *testing.T) {
	handler := integrationRouter(t)
	req := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]any
	testutil.ParseJSON(t, rec, &body)
	if body["status"] != "ok" {
		t.Errorf("status = %v, want %q", body["status"], "ok")
	}
}

func TestIntegration_NginxStatusWithAdminToken(t *testing.T) {
	handler := integrationRouter(t)
	user := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	token := testutil.MakeToken(t, user)
	req := testutil.NewRequest(t, http.MethodGet, "/api/nginx/status", token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("status = %d, authenticated admin should not get 401", rec.Code)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	var body map[string]any
	testutil.ParseJSON(t, rec, &body)
	for _, key := range []string{"installed", "status", "statusAvailable", "version", "uptime", "configTest"} {
		if _, ok := body[key]; !ok {
			t.Errorf("response missing %q: %#v", key, body)
		}
	}
}

func TestIntegration_LoginBadBody(t *testing.T) {
	handler := integrationRouter(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader("{not-json"))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	var body map[string]string
	testutil.ParseJSON(t, rec, &body)
	if body["error"] != "invalid request body" {
		t.Errorf("error = %q, want %q", body["error"], "invalid request body")
	}
}
