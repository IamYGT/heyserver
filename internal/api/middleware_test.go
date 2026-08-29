package api

import (
	"context"
	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/testutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type deadlineResponseWriter struct {
	http.ResponseWriter
	deadlineSet bool
}

func (w *deadlineResponseWriter) SetWriteDeadline(time.Time) error {
	w.deadlineSet = true
	return nil
}

func TestStatusWriterUnwrapsForDeadlineOverride(t *testing.T) {
	underlying := &deadlineResponseWriter{ResponseWriter: httptest.NewRecorder()}
	w := &statusWriter{ResponseWriter: underlying}
	if err := http.NewResponseController(w).SetWriteDeadline(time.Time{}); err != nil {
		t.Fatalf("SetWriteDeadline: %v", err)
	}
	if !underlying.deadlineSet {
		t.Fatal("deadline override did not reach underlying writer")
	}
}

func TestBoundedLongOperationsDisableGlobalWriteDeadline(t *testing.T) {
	paths := []string{
		"/api/nodes/contabo/databases",
		"/api/php/composer/8.4/install",
		"/api/backups/gdrive/remote",
		"/api/backups/snapshot/list",
		"/api/ssl/renew/example.com",
		"/api/domains",
		"/api/domains/example.com",
		"/api/disk/dirsize",
		"/api/databases/pgm-restore",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			underlying := &deadlineResponseWriter{ResponseWriter: httptest.NewRecorder()}
			req := httptest.NewRequest(http.MethodGet, path, nil)

			withBoundedOperationWriteDeadline(okH()).ServeHTTP(underlying, req)

			if !underlying.deadlineSet {
				t.Fatal("bounded long operation left the global write deadline active")
			}
		})
	}
}

func TestNormalAPIRequestsKeepGlobalWriteDeadline(t *testing.T) {
	paths := []string{
		"/api/system/stats",
		"/api/databases",
		"/api/backups",
		"/api/nodes",
		"/api/auth/me",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			underlying := &deadlineResponseWriter{ResponseWriter: httptest.NewRecorder()}
			req := httptest.NewRequest(http.MethodGet, path, nil)

			withBoundedOperationWriteDeadline(okH()).ServeHTTP(underlying, req)

			if underlying.deadlineSet {
				t.Fatal("normal API request unexpectedly disabled the global write deadline")
			}
		})
	}
}

func okH() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) }
}
func testCfg() *config.Config { return &config.Config{JWTSecret: testutil.TestSecret} }

func TestWithAuth_NoToken(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	withAuth(testCfg(), okH())(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 401 {
		t.Errorf("got %d want 401", rec.Code)
	}
}

func TestWithAuth_ValidBearer(t *testing.T) {
	t.Parallel()
	user := testutil.MakeUser(1, "a@x.com", models.RoleAdmin)
	req := testutil.NewRequest(t, "GET", "/", testutil.MakeToken(t, user))
	rec := httptest.NewRecorder()
	withAuth(testCfg(), okH())(rec, req)
	if rec.Code != 200 {
		t.Errorf("got %d want 200", rec.Code)
	}
}

func TestWithAuth_ValidCookie(t *testing.T) {
	t.Parallel()
	user := testutil.MakeUser(2, "v@x.com", models.RoleViewer)
	req := testutil.NewRequestWithCookie(t, "GET", "/", testutil.MakeToken(t, user))
	rec := httptest.NewRecorder()
	withAuth(testCfg(), okH())(rec, req)
	if rec.Code != 200 {
		t.Errorf("got %d want 200", rec.Code)
	}
}

func TestWithAuth_ValidTerminalWebSocketProtocol(t *testing.T) {
	t.Parallel()
	user := testutil.MakeUser(3, "terminal@x.com", models.RoleAdmin)
	req := httptest.NewRequest("GET", "/api/terminal/ws", nil)
	req.Header.Set("Sec-WebSocket-Protocol", "hserver-terminal, jwt."+testutil.MakeToken(t, user))
	rec := httptest.NewRecorder()
	withAuth(testCfg(), okH())(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("got %d want %d", rec.Code, http.StatusOK)
	}
}

func TestWithAuth_IgnoresWebSocketProtocolOutsideTerminal(t *testing.T) {
	t.Parallel()
	user := testutil.MakeUser(4, "terminal@x.com", models.RoleAdmin)
	req := httptest.NewRequest("GET", "/api/system/info", nil)
	req.Header.Set("Sec-WebSocket-Protocol", "hserver-terminal, jwt."+testutil.MakeToken(t, user))
	rec := httptest.NewRecorder()
	withAuth(testCfg(), okH())(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestWithAuth_Expired(t *testing.T) {
	t.Parallel()
	user := testutil.MakeUser(1, "a@x.com", models.RoleAdmin)
	req := testutil.NewRequest(t, "GET", "/", testutil.MakeExpiredToken(t, user))
	rec := httptest.NewRecorder()
	withAuth(testCfg(), okH())(rec, req)
	if rec.Code != 401 {
		t.Errorf("expired: got %d want 401", rec.Code)
	}
}

func TestWithAuth_InvalidToken(t *testing.T) {
	t.Parallel()
	req := testutil.NewRequest(t, "GET", "/", "not.a.jwt")
	rec := httptest.NewRecorder()
	withAuth(testCfg(), okH())(rec, req)
	if rec.Code != 401 {
		t.Errorf("invalid: got %d want 401", rec.Code)
	}
}

func TestWithAuth_SetsContext(t *testing.T) {
	t.Parallel()
	user := testutil.MakeUser(42, "m@x.com", models.RoleManager)
	var captured *models.User
	h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		captured = getUserFromContext(r.Context())
	})
	withAuth(testCfg(), h)(httptest.NewRecorder(), testutil.NewRequest(t, "GET", "/", testutil.MakeToken(t, user)))
	if captured == nil || captured.ID != 42 {
		t.Error("context user not set")
	}
}

func TestRequireRole(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		role models.Role
		min  RoleLevel
		want int
	}{
		{"admin-admin", models.RoleAdmin, RoleAdmin, 200},
		{"admin-manager", models.RoleAdmin, RoleManager, 200},
		{"admin-viewer", models.RoleAdmin, RoleViewer, 200},
		{"manager-manager", models.RoleManager, RoleManager, 200},
		{"manager-viewer", models.RoleManager, RoleViewer, 200},
		{"manager-admin", models.RoleManager, RoleAdmin, 403},
		{"viewer-viewer", models.RoleViewer, RoleViewer, 200},
		{"viewer-manager", models.RoleViewer, RoleManager, 403},
		{"viewer-admin", models.RoleViewer, RoleAdmin, 403},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			user := testutil.MakeUser(1, "t@x.com", tc.role)
			req := httptest.NewRequest("GET", "/", nil)
			req = req.WithContext(context.WithValue(req.Context(), userContextKey, user))
			rec := httptest.NewRecorder()
			requireRole(tc.min, okH())(rec, req)
			if rec.Code != tc.want {
				t.Errorf("%s: got %d want %d", tc.name, rec.Code, tc.want)
			}
		})
	}
}

func TestRequireRole_NoUser(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	requireRole(RoleViewer, okH())(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != 401 {
		t.Errorf("no user: got %d want 401", rec.Code)
	}
}

func TestWithCORS_SecurityHeaders(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	withCORS(okH()).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	for hdr, want := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		// "0" disables the legacy XSS auditor; modern stacks rely on CSP instead.
		"X-XSS-Protection": "0",
	} {
		if got := rec.Header().Get(hdr); got != want {
			t.Errorf("%s: got %q want %q", hdr, got, want)
		}
	}
}

func TestWithCORS_Options(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	withCORS(okH()).ServeHTTP(rec, httptest.NewRequest("OPTIONS", "/", nil))
	if rec.Code != 204 {
		t.Errorf("OPTIONS: got %d want 204", rec.Code)
	}
}

func TestWithCORS_AllowsOnlyProviderNeutralDevelopmentOrigins(t *testing.T) {
	t.Parallel()

	allowedRequest := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	allowedRequest.Header.Set("Origin", "http://localhost:5173")
	allowedRecorder := httptest.NewRecorder()
	withCORS(okH()).ServeHTTP(allowedRecorder, allowedRequest)
	if got := allowedRecorder.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("allowed development origin = %q", got)
	}

	installationSpecificRequest := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	installationSpecificRequest.Header.Set("Origin", "https://panel.operator.example")
	installationSpecificRecorder := httptest.NewRecorder()
	withCORS(okH()).ServeHTTP(installationSpecificRecorder, installationSpecificRequest)
	if got := installationSpecificRecorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("installation-specific origin should not be built in, got %q", got)
	}
}

func TestRoleLevel(t *testing.T) {
	tests := []struct {
		role models.Role
		want RoleLevel
	}{
		{models.RoleAdmin, RoleAdmin}, {models.RoleManager, RoleManager},
		{models.RoleViewer, RoleViewer}, {"unknown", RoleViewer},
	}
	for _, tc := range tests {
		if got := roleLevel(tc.role); got != tc.want {
			t.Errorf("roleLevel(%q): got %d want %d", tc.role, got, tc.want)
		}
	}
}
