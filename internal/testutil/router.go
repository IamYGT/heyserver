//go:build integration

package testutil

import (
	"io/fs"
	"net/http"
	"testing"

	"github.com/IamYGT/heyserver/internal/api"
	"github.com/IamYGT/heyserver/internal/config"
)

// TestConfig returns a config suitable for handler/router integration tests.
func TestConfig() *config.Config {
	return &config.Config{
		JWTSecret:           TestSecret,
		CronSecret:          "test-cron-secret-32-bytes-min!!",
		DataDir:             "/tmp/hserver-test-data",
		VhostsRoot:          "/tmp/hserver-test-vhosts",
		PHPConfigRoot:       "/tmp/hserver-test-php/config",
		PHPBinaryRoot:       "/tmp/hserver-test-php/bin",
		NginxSitesAvailable: "/tmp/hserver-test-nginx/available",
		NginxSitesEnabled:   "/tmp/hserver-test-nginx/enabled",
		PM2AllowedRoots:     []string{"/tmp/hserver-test-vhosts"},
	}
}

// NewTestRouter builds the full API router with optional web FS and deps.
func NewTestRouter(t *testing.T, webFS fs.FS, deps *api.Deps) http.Handler {
	t.Helper()
	if webFS == nil {
		webFS = MinimalWebFS(t)
	}
	return api.NewRouter(TestConfig(), webFS, deps)
}
