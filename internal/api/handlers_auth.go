package api

import (
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"

	"github.com/IamYGT/heyserver/internal/auth"
	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/db"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/security"
)

const cookieName = "hserver_token"

const authRequestBodyLimit = 4096

var totpCodePattern = regexp.MustCompile(`^[0-9]{6}$`)

func decodeAuthJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, authRequestBodyLimit)
	if err := decodeStrictJSON(r, target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			jsonError(w, http.StatusRequestEntityTooLarge, "authentication request body is too large")
		} else {
			jsonError(w, http.StatusBadRequest, "invalid request body")
		}
		return false
	}
	return true
}

// authCookie returns a secure, production-hardened session cookie.
// Secure is derived from X-Forwarded-Proto because TLS is terminated at nginx;
// r.TLS is always nil when the backend is behind a reverse proxy.
// SameSite=Strict prevents CSRF by blocking cross-site cookie delivery entirely.
func authCookie(r *http.Request, token string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteStrictMode,
		MaxAge:   maxAge,
	}
}

// handleLogin validates credentials and either issues a JWT (when 2FA is not
// enabled) or returns {"requires_totp": true} so the client can redirect to
// the TOTP challenge screen. The actual JWT is then issued by handleTOTPLogin.
func handleLogin(cfg *config.Config, limiter *security.RateLimiter) http.HandlerFunc {
	users := db.NewUserRepository(db.Instance())
	audit := db.NewAuditRepository(db.Instance())

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ip := security.RealIP(r)

		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}

		if !decodeAuthJSON(w, r, &req) {
			return
		}
		req.Email = strings.TrimSpace(req.Email)
		if req.Email == "" || req.Password == "" {
			jsonError(w, http.StatusBadRequest, "email and password are required")
			return
		}
		// Hard cap prevents bcrypt DoS (bcrypt is O(n) on input length past 72 bytes).
		if len(req.Email) > 254 || len(req.Password) > 128 {
			jsonError(w, http.StatusBadRequest, "invalid credentials")
			return
		}

		user, err := users.FindByEmail(req.Email)
		if err != nil {
			// Run a dummy bcrypt check to prevent timing-based account enumeration.
			// Without this, a missing user returns ~0 ms while a wrong password
			// returns ~100 ms (bcrypt cost), leaking whether the account exists.
			auth.DummyCheckPassword()
			// Increment ban counter even for unknown emails so attackers cannot
			// bypass account lockout by guessing non-existent addresses.
			limiter.RecordFailure(ip)
			jsonError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}

		if !auth.CheckPassword(user.Password, req.Password) {
			limiter.RecordFailure(ip)
			_ = audit.Insert(buildAuditEntry(user.ID, user.Name, "login_failed", "auth", "bad password", r))
			jsonError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}

		// If TOTP is enabled, demand a second factor before issuing the JWT.
		if user.TOTPEnabled {
			_ = audit.Insert(buildAuditEntry(user.ID, user.Name, "login_totp_required", "auth", "2FA challenge sent", r))
			jsonResponse(w, http.StatusOK, map[string]any{
				"requires_totp": true,
				"email":         user.Email,
			})
			return
		}

		token, err := auth.GenerateToken(cfg.JWTSecret, user)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "could not generate token")
			return
		}

		limiter.RecordSuccess(ip)

		http.SetCookie(w, authCookie(r, token, int(auth.TokenTTL.Seconds())))

		_ = audit.Insert(buildAuditEntry(user.ID, user.Name, "login", "auth", "successful login", r))

		// Clear password hash from response — it is already json:"-" in the
		// model but an extra explicit clear is defensive.
		user.Password = ""

		jsonResponse(w, http.StatusOK, map[string]any{
			"token": token,
			"user":  user,
		})
	}
}

// handleTOTPLogin completes the two-step login flow when TOTP is enabled.
// The client must provide email, password AND a valid TOTP code. On success a
// JWT is issued exactly as in the normal login flow.
// POST /api/auth/totp-verify
func handleTOTPLogin(cfg *config.Config, limiter *security.RateLimiter) http.HandlerFunc {
	users := db.NewUserRepository(db.Instance())
	audit := db.NewAuditRepository(db.Instance())

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ip := security.RealIP(r)

		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
			Code     string `json:"code"`
		}
		if !decodeAuthJSON(w, r, &req) {
			return
		}
		req.Email = strings.TrimSpace(req.Email)
		if req.Email == "" || req.Password == "" || req.Code == "" {
			jsonError(w, http.StatusBadRequest, "email, password and code are required")
			return
		}
		// Hard cap prevents bcrypt DoS.
		if len(req.Email) > 254 || len(req.Password) > 128 {
			jsonError(w, http.StatusBadRequest, "invalid credentials")
			return
		}
		// TOTP codes are 6 digits; reject obviously malformed input early.
		if !totpCodePattern.MatchString(req.Code) {
			jsonError(w, http.StatusBadRequest, "invalid credentials")
			return
		}

		user, err := users.FindByEmail(req.Email)
		if err != nil {
			auth.DummyCheckPassword()
			limiter.RecordFailure(ip)
			jsonError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}

		if !auth.CheckPassword(user.Password, req.Password) {
			limiter.RecordFailure(ip)
			_ = audit.Insert(buildAuditEntry(user.ID, user.Name, "login_failed", "auth", "bad password (totp flow)", r))
			jsonError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}

		if !user.TOTPEnabled || user.TOTPSecret == "" {
			// User somehow reached this endpoint without TOTP — fall through
			// to normal login response so the frontend can recover gracefully.
			token, err := auth.GenerateToken(cfg.JWTSecret, user)
			if err != nil {
				jsonError(w, http.StatusInternalServerError, "could not generate token")
				return
			}
			limiter.RecordSuccess(ip)
			http.SetCookie(w, authCookie(r, token, int(auth.TokenTTL.Seconds())))
			user.Password = ""
			jsonResponse(w, http.StatusOK, map[string]any{"token": token, "user": user})
			return
		}

		valid, err := security.VerifyTOTP(user.TOTPSecret, req.Code)
		if err != nil || !valid {
			limiter.RecordFailure(ip)
			_ = audit.Insert(buildAuditEntry(user.ID, user.Name, "login_totp_failed", "auth", "invalid TOTP code", r))
			jsonError(w, http.StatusUnauthorized, "invalid TOTP code")
			return
		}

		token, err := auth.GenerateToken(cfg.JWTSecret, user)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "could not generate token")
			return
		}

		limiter.RecordSuccess(ip)

		http.SetCookie(w, authCookie(r, token, int(auth.TokenTTL.Seconds())))

		_ = audit.Insert(buildAuditEntry(user.ID, user.Name, "login", "auth", "successful login (2FA)", r))

		user.Password = ""
		jsonResponse(w, http.StatusOK, map[string]any{
			"token": token,
			"user":  user,
		})
	}
}

// handleLogout clears the auth cookie and returns 200.
func handleLogout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if err := requireEmptyRequestBody(r); err != nil {
			jsonError(w, http.StatusBadRequest, "logout request body must be empty")
			return
		}
		http.SetCookie(w, authCookie(r, "", -1))
		jsonResponse(w, http.StatusOK, map[string]string{"message": "logged out"})
	}
}

// handleMe returns the currently authenticated user's profile.
func handleMe() http.HandlerFunc {
	users := db.NewUserRepository(db.Instance())

	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ctxUser := getUserFromContext(r.Context())
		if ctxUser == nil {
			jsonError(w, http.StatusUnauthorized, "unauthorized")
			return
		}

		// Re-fetch from DB so that the response always reflects the latest data.
		user, err := users.FindByID(ctxUser.ID)
		if err != nil {
			jsonError(w, http.StatusUnauthorized, "user not found")
			return
		}

		user.Password = ""
		jsonResponse(w, http.StatusOK, user)
	}
}

// buildAuditEntry is a small helper to reduce repetition in audit log calls.
func buildAuditEntry(userID int64, userName, action, resource, details string, r *http.Request) *models.AuditLog {
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	if ip == "" {
		ip = r.RemoteAddr
	}
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		// Take only the first (client) address and validate it.
		first := strings.SplitN(forwarded, ",", 2)[0]
		first = strings.TrimSpace(first)
		if net.ParseIP(first) != nil {
			ip = first
		}
	}
	return &models.AuditLog{
		UserID:   userID,
		UserName: userName,
		Action:   action,
		Resource: resource,
		Details:  details,
		IP:       ip,
	}
}
