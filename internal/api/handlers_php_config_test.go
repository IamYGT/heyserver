package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/testutil"
)

func TestPHPComposerRouteUsesConfiguredVhostsRoot(t *testing.T) {
	cfg := testutil.TestConfig()
	cfg.VhostsRoot = t.TempDir()
	handler := NewRouter(cfg, testutil.MinimalWebFS(t), contractTestDeps(t))
	outside := t.TempDir()
	body := strings.NewReader(`{"project_dir":` + strconv.Quote(outside) + `}`)
	req := testutil.NewRequest(t, http.MethodPost, "/api/php/composer/8.4/install", testutil.MakeToken(t, testutil.MakeUser(1, "admin@test.com", models.RoleAdmin)))
	req.Body = io.NopCloser(body)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), cfg.VhostsRoot) {
		t.Fatalf("response does not identify configured vhosts root: %s", recorder.Body.String())
	}
}
