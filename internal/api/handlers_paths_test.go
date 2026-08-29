package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/testutil"
)

// authenticatedGETPaths exercises read-only handlers to improve integration coverage.
func TestIntegration_AuthenticatedGETPaths(t *testing.T) {
	handler := integrationRouter(t)
	token := testutil.MakeToken(t, testutil.MakeUser(1, "admin@test.com", models.RoleAdmin))
	paths := []string{
		"/api/system/stats",
		"/api/system/services",
		"/api/system/info",
		"/api/nginx/status",
		"/api/nginx/configs",
		"/api/php/versions",
		"/api/php/pools",
		"/api/ssl/status",
		"/api/ssl/certificates",
		"/api/pm2/processes",
		"/api/databases",
		"/api/firewall/status",
		"/api/monitoring/stats",
		"/api/docker/status",
		"/api/dns/zones",
		"/api/logs/sources",
		"/api/deploy/targets",
		"/api/deploy/history",
		"/api/security/score",
		"/api/notify/rules",
		"/api/notify/history",
		"/api/settings",
		"/api/onboarding",
		"/api/databases/users",
		"/api/databases/pgm-backups",
		"/api/uptime/monitors/summary",
		"/api/security/fail2ban/status",
		"/api/metrics/history?duration=1h",
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
			// A read-only capability may be intentionally unavailable on a clean
			// self-hosted machine. In that case handlers use 503 instead of
			// inventing an empty inventory; only unexpected server errors fail
			// this broad route smoke.
			if rec.Code >= 500 && rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("%s returned %d", path, rec.Code)
			}
		})
	}
}
