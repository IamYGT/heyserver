package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/auth"
	"github.com/IamYGT/heyserver/internal/db"
	"github.com/IamYGT/heyserver/internal/models"
	securityservice "github.com/IamYGT/heyserver/internal/services/security"
	"github.com/IamYGT/heyserver/internal/testutil"
)

func newTOTPTestUser(t *testing.T) (*db.UserRepository, *models.User) {
	t.Helper()
	repo := db.NewUserRepository(db.Instance())
	password, err := auth.HashPassword("test-password")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	user := &models.User{
		Email:    fmt.Sprintf("totp-status-%d@example.test", time.Now().UnixNano()),
		Name:     "TOTP Status Test",
		Password: password,
		Role:     models.RoleAdmin,
	}
	if err := repo.Create(user); err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { _ = repo.Delete(user.ID) })
	return repo, user
}

func requestWithUser(t *testing.T, method, target string, user *models.User) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	return req.WithContext(context.WithValue(req.Context(), userContextKey, user))
}

func TestHandleTOTPStatusReflectsPersistedState(t *testing.T) {
	repo, user := newTOTPTestUser(t)

	tests := []struct {
		name        string
		secret      string
		enabled     bool
		wantPending bool
		wantEnabled bool
	}{
		{name: "disabled"},
		{name: "setup pending", secret: "pending-secret", wantPending: true},
		{name: "enabled", secret: "active-secret", enabled: true, wantEnabled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := repo.UpdateUserTOTP(user.ID, tt.secret, tt.enabled); err != nil {
				t.Fatalf("UpdateUserTOTP: %v", err)
			}
			rec := httptest.NewRecorder()
			handleTOTPStatus()(rec, requestWithUser(t, http.MethodGet, "/api/auth/2fa/status", user))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusOK, rec.Body.String())
			}
			var body struct {
				Enabled      bool `json:"enabled"`
				SetupPending bool `json:"setup_pending"`
			}
			testutil.ParseJSON(t, rec, &body)
			if body.Enabled != tt.wantEnabled || body.SetupPending != tt.wantPending {
				t.Fatalf("body = %#v, want enabled=%v setup_pending=%v", body, tt.wantEnabled, tt.wantPending)
			}
		})
	}
}

func TestHandleTOTPSetupDoesNotOverwriteEnabledConfiguration(t *testing.T) {
	repo, user := newTOTPTestUser(t)
	const existingSecret = "existing-enabled-secret"
	if err := repo.UpdateUserTOTP(user.ID, existingSecret, true); err != nil {
		t.Fatalf("UpdateUserTOTP: %v", err)
	}

	rec := httptest.NewRecorder()
	handleTOTPSetup(testCfg())(rec, requestWithUser(t, http.MethodPost, "/api/auth/2fa/setup", user))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusConflict, rec.Body.String())
	}

	stored, err := repo.FindByID(user.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if !stored.TOTPEnabled || stored.TOTPSecret != existingSecret {
		t.Fatalf("stored 2FA changed: enabled=%v secret=%q", stored.TOTPEnabled, stored.TOTPSecret)
	}
}

func TestHandleTOTPRecoveryAcceptsFrontendContractAndUsesSessionCookiePolicy(t *testing.T) {
	repo, user := newTOTPTestUser(t)
	const recoveryCode = "ABCDE-12345"
	if err := repo.UpdateUserTOTP(user.ID, "active-secret", true); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRecoveryCodes(user.ID, []string{recoveryCode}); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/auth/2fa/recovery", strings.NewReader(
		`{"email":"`+user.Email+`","password":"test-password","recovery_code":"abcde-12345"}`,
	))
	req.Header.Set("X-Forwarded-Proto", "https")
	recorder := httptest.NewRecorder()
	handleTOTPRecovery(testCfg(), securityservice.NewRateLimiter())(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q", recorder.Header().Get("Cache-Control"))
	}
	cookie := recorder.Header().Get("Set-Cookie")
	for _, attribute := range []string{"HttpOnly", "Secure", "SameSite=Strict", "Max-Age=86400"} {
		if !strings.Contains(cookie, attribute) {
			t.Fatalf("session cookie %q does not contain %q", cookie, attribute)
		}
	}

	legacy := httptest.NewRecorder()
	handleTOTPRecovery(testCfg(), securityservice.NewRateLimiter())(legacy, httptest.NewRequest(
		http.MethodPost,
		"/api/auth/2fa/recovery",
		strings.NewReader(`{"email":"`+user.Email+`","password":"test-password","code":"ABCDE-12345"}`),
	))
	if legacy.Code != http.StatusBadRequest {
		t.Fatalf("legacy field status = %d, body = %s", legacy.Code, legacy.Body.String())
	}
}
