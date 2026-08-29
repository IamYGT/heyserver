package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/testutil"
)

// TestIntegration_AllProtectedGETWithAdmin exercises every authenticated GET route with a valid
// admin token to improve handler coverage without requiring system exec mocks.
func TestIntegration_AllProtectedGETWithAdmin(t *testing.T) {
	handler := integrationRouter(t)
	token := testutil.MakeToken(t, testutil.MakeUser(1, "admin@test.com", models.RoleAdmin))

	skipGET := map[string]bool{
		"/api/backups/jobs/stream": true, // SSE — blocks test runner
		"/api/logs/stream":         true,
		"/api/terminal/ws":         true,
	}

	for _, spec := range AllRoutes() {
		if spec.Method != http.MethodGet {
			continue
		}
		if spec.Auth == RoutePublic || spec.Auth == RouteInternalCron || spec.Auth == RouteAgent {
			continue
		}
		if skipGET[spec.Path] {
			continue
		}

		spec := spec
		t.Run(spec.Path, func(t *testing.T) {
			path := fillRoutePath(spec.Path)
			req := testutil.NewRequest(t, http.MethodGet, path, token)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code == http.StatusUnauthorized {
				t.Fatalf("%s returned 401 with admin token", path)
			}
			if rec.Code == http.StatusNotFound && strings.Contains(rec.Body.String(), "page not found") {
				t.Fatalf("%s mux 404 — route not registered", path)
			}
		})
	}
}

// TestIntegration_BackupHandlersWithAuth hits backup job/schedule endpoints (backupSvc is always wired).
// TestIntegration_AllProtectedPOSTEmptyBody sends {} to authenticated POST routes.
// Handlers may return 400/404/500; goal is execution coverage, not happy path.
func TestIntegration_AllProtectedPOSTEmptyBody(t *testing.T) {
	handler := integrationRouter(t)
	token := testutil.MakeToken(t, testutil.MakeUser(1, "admin@test.com", models.RoleAdmin))

	skipPOST := map[string]bool{
		"/api/auth/login":                true,
		"/api/auth/totp":                 true,
		"/api/backups/jobs/stream":       true,
		"/api/internal/cron/backup":      true,
		"/api/internal/deploy/preflight": true,
	}

	for _, spec := range AllRoutes() {
		if spec.Method != http.MethodPost {
			continue
		}
		if spec.Auth == RoutePublic || spec.Auth == RouteInternalCron || spec.Auth == RouteAgent {
			continue
		}
		if skipPOST[spec.Path] {
			continue
		}

		spec := spec
		t.Run(spec.Path, func(t *testing.T) {
			path := fillRoutePath(spec.Path)
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code == http.StatusUnauthorized {
				t.Fatalf("%s returned 401", path)
			}
			if rec.Code == http.StatusNotFound && strings.Contains(rec.Body.String(), "page not found") {
				t.Fatalf("%s mux 404", path)
			}
		})
	}
}

func TestIntegration_BackupHandlersWithAuth(t *testing.T) {
	handler := integrationRouter(t)
	token := testutil.MakeToken(t, testutil.MakeUser(1, "admin@test.com", models.RoleAdmin))

	paths := []string{
		"/api/backups",
		"/api/backups/schedules",
		"/api/backups/jobs",
		"/api/backups/jobs/1",
	}
	for _, path := range paths {
		path := path
		t.Run(path, func(t *testing.T) {
			req := testutil.NewRequest(t, http.MethodGet, path, token)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code == http.StatusUnauthorized {
				t.Fatalf("%s returned 401", path)
			}
		})
	}
}
