package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSmoke_ProtectedRoutesRequireAuth(t *testing.T) {
	handler := integrationRouter(t)

	tests := []struct {
		name   string
		method string
		path   string
	}{
		// firewall
		{name: "firewall status", method: http.MethodGet, path: "/api/firewall/status"},
		{name: "firewall rules list", method: http.MethodGet, path: "/api/firewall/rules"},
		{name: "firewall rules create", method: http.MethodPost, path: "/api/firewall/rules"},
		{name: "firewall rule delete", method: http.MethodDelete, path: "/api/firewall/rules/1"},
		{name: "firewall toggle", method: http.MethodPost, path: "/api/firewall/toggle"},

		// ssl
		{name: "ssl readiness", method: http.MethodGet, path: "/api/ssl/status"},
		{name: "ssl certificates list", method: http.MethodGet, path: "/api/ssl/certificates"},
		{name: "ssl certificate get", method: http.MethodGet, path: "/api/ssl/certificates/example.com"},
		{name: "ssl renew", method: http.MethodPost, path: "/api/ssl/renew/example.com"},
		{name: "ssl issue", method: http.MethodPost, path: "/api/ssl/issue"},

		// cron
		{name: "cron jobs list", method: http.MethodGet, path: "/api/cron/jobs"},
		{name: "cron job create", method: http.MethodPost, path: "/api/cron/jobs"},
		{name: "cron job delete", method: http.MethodDelete, path: "/api/cron/jobs/1"},
		{name: "cron system list", method: http.MethodGet, path: "/api/cron/system"},

		// docker
		{name: "docker status", method: http.MethodGet, path: "/api/docker/status"},
		{name: "docker containers list", method: http.MethodGet, path: "/api/docker/containers"},
		{name: "docker container control", method: http.MethodPost, path: "/api/docker/containers/abc123/start"},
		{name: "docker images list", method: http.MethodGet, path: "/api/docker/images"},

		// settings
		{name: "settings list", method: http.MethodGet, path: "/api/settings"},
		{name: "settings get key", method: http.MethodGet, path: "/api/settings/memory_limit"},
		{name: "settings update", method: http.MethodPut, path: "/api/settings"},
		{name: "settings delete key", method: http.MethodDelete, path: "/api/settings/memory_limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d for %s %s", rec.Code, http.StatusUnauthorized, tt.method, tt.path)
			}
		})
	}
}
