package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/testutil"
)

// TestIntegration_MutationBadJSON exercises POST/PUT/PATCH handlers with malformed JSON
// to improve handler-layer coverage without requiring system exec.
func TestIntegration_MutationBadJSON(t *testing.T) {
	handler := integrationRouter(t)
	admin := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	token := testutil.MakeToken(t, admin)

	for _, spec := range AllRoutes() {
		if spec.Method != http.MethodPost && spec.Method != http.MethodPut && spec.Method != http.MethodPatch {
			continue
		}
		if spec.Auth == RoutePublic || spec.Auth == RouteInternalCron || spec.Auth == RouteAgent {
			continue
		}
		spec := spec
		t.Run(spec.Method+" "+spec.Path, func(t *testing.T) {
			path := fillRoutePath(spec.Path)
			req := httptest.NewRequest(spec.Method, path, strings.NewReader("{not-json"))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code == http.StatusNotFound && strings.Contains(rec.Body.String(), "page not found") {
				t.Fatalf("unregistered route (mux 404)")
			}
			if rec.Code == http.StatusUnauthorized {
				t.Fatalf("admin token rejected with 401")
			}
		})
	}
}

// TestIntegration_MutationEmptyBody sends {} to mutation routes expecting validation/not-found, not mux 404.
func TestIntegration_MutationEmptyBody(t *testing.T) {
	handler := integrationRouter(t)
	admin := testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)
	token := testutil.MakeToken(t, admin)

	targets := []string{
		"POST /api/settings",
		"POST /api/databases",
		"POST /api/firewall/rules",
		"POST /api/ssl/issue",
		"POST /api/cron/jobs",
		"POST /api/notify/channels",
		"POST /api/notify/rules",
		"POST /api/users",
		"POST /api/onboarding",
	}

	for _, target := range targets {
		parts := strings.SplitN(target, " ", 2)
		method, path := parts[0], parts[1]
		t.Run(target, func(t *testing.T) {
			req := httptest.NewRequest(method, path, strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code == http.StatusNotFound && strings.Contains(rec.Body.String(), "page not found") {
				t.Fatalf("unregistered route")
			}
			if rec.Code == http.StatusUnauthorized {
				t.Fatalf("admin rejected")
			}
		})
	}
}

// TestIntegration_AdminMutationsRequireAuth ensures admin-gated writes reject anonymous callers.
func TestIntegration_AdminMutationsRequireAuth(t *testing.T) {
	handler := integrationRouter(t)

	for _, spec := range AllRoutes() {
		if spec.Auth != RouteAdmin {
			continue
		}
		if spec.Method != http.MethodPost && spec.Method != http.MethodPut &&
			spec.Method != http.MethodDelete && spec.Method != http.MethodPatch {
			continue
		}

		spec := spec
		t.Run(spec.Method+" "+spec.Path, func(t *testing.T) {
			path := fillRoutePath(spec.Path)
			req := httptest.NewRequest(spec.Method, path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("unauthenticated mutation: got %d want 401", rec.Code)
			}
		})
	}
}
