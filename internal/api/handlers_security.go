package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/auth"
	"github.com/IamYGT/heyserver/internal/config"
	"github.com/IamYGT/heyserver/internal/db"
	"github.com/IamYGT/heyserver/internal/services/security"
)

// ── 2FA ──────────────────────────────────────────────────────────────────────

var recoveryCodePattern = regexp.MustCompile(`^[A-F0-9]{5}-[A-F0-9]{5}$`)

// handleTOTPStatus returns the persisted 2FA state for the current user without
// exposing the stored secret. The setup_pending flag lets the UI recover safely
// after a refresh instead of assuming 2FA is disabled and overwriting an active
// configuration.
// GET /api/auth/2fa/status
func handleTOTPStatus() http.HandlerFunc {
	users := db.NewUserRepository(db.Instance())
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		user := getUserFromContext(r.Context())
		if user == nil {
			jsonError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		dbUser, err := users.FindByID(user.ID)
		if err != nil {
			jsonError(w, http.StatusUnauthorized, "user not found")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]bool{
			"enabled":       dbUser.TOTPEnabled,
			"setup_pending": !dbUser.TOTPEnabled && dbUser.TOTPSecret != "",
		})
	}
}

// handleTOTPSetup generates a new TOTP secret + QR code for the current user
// and immediately persists the secret (not yet enabled) so that verify can
// look it up from the database instead of trusting the client to echo it back.
// POST /api/auth/2fa/setup
func handleTOTPSetup(_ *config.Config) http.HandlerFunc {
	users := db.NewUserRepository(db.Instance())
	audit := db.NewAuditRepository(db.Instance())
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if err := requireEmptyRequestBody(r); err != nil {
			jsonError(w, http.StatusBadRequest, "2FA setup request body must be empty")
			return
		}
		user := getUserFromContext(r.Context())
		if user == nil {
			jsonError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		dbUser, err := users.FindByID(user.ID)
		if err != nil {
			jsonError(w, http.StatusUnauthorized, "user not found")
			return
		}
		if dbUser.TOTPEnabled {
			jsonError(w, http.StatusConflict, "2FA is already enabled")
			return
		}
		setup, err := security.GenerateTOTP(user.Email)
		if err != nil {
			slog.Error("failed to generate TOTP secret", "user_id", user.ID, "err", err)
			jsonError(w, http.StatusInternalServerError, "failed to generate TOTP secret")
			return
		}
		// Persist the secret immediately (totp_enabled stays false until verify).
		if err := users.UpdateUserTOTP(user.ID, setup.Secret, false); err != nil {
			slog.Error("failed to save TOTP secret", "user_id", user.ID, "err", err)
			jsonError(w, http.StatusInternalServerError, "failed to save TOTP secret")
			return
		}
		// Hash and persist recovery codes — plaintext codes are returned once
		// to the client and never stored.
		if err := users.SaveRecoveryCodes(user.ID, setup.RecoveryCodes); err != nil {
			slog.Error("failed to save recovery codes", "user_id", user.ID, "err", err)
			jsonError(w, http.StatusInternalServerError, "failed to save recovery codes")
			return
		}
		_ = audit.Insert(buildAuditEntry(user.ID, user.Name, "2fa_setup_initiated", "auth", "TOTP setup started", r))
		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"secret":        setup.Secret,
			"otpAuthUrl":    setup.OTPAuthURL,
			"qrCode":        base64.StdEncoding.EncodeToString(setup.QRCodePNG),
			"recoveryCodes": setup.RecoveryCodes,
		})
	}
}

// handleTOTPVerify confirms the user's authenticator app is in sync and enables 2FA.
// The code is validated against the secret already stored in the database —
// the client does NOT send the secret here.
// POST /api/auth/2fa/verify
func handleTOTPVerify(_ *config.Config) http.HandlerFunc {
	users := db.NewUserRepository(db.Instance())
	audit := db.NewAuditRepository(db.Instance())
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		user := getUserFromContext(r.Context())
		if user == nil {
			jsonError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		var req struct {
			Code string `json:"code"`
		}
		if !decodeAuthJSON(w, r, &req) {
			return
		}
		if !totpCodePattern.MatchString(req.Code) {
			jsonError(w, http.StatusBadRequest, "code must contain exactly 6 digits")
			return
		}
		// Re-fetch user to get the stored secret.
		dbUser, err := users.FindByID(user.ID)
		if err != nil {
			jsonError(w, http.StatusUnauthorized, "user not found")
			return
		}
		if dbUser.TOTPSecret == "" {
			jsonError(w, http.StatusBadRequest, "totp setup not initiated")
			return
		}
		valid, err := security.VerifyTOTP(dbUser.TOTPSecret, req.Code)
		if err != nil {
			slog.Error("totp verify error", "user_id", user.ID, "err", err)
			jsonError(w, http.StatusBadRequest, "totp verification error")
			return
		}
		if !valid {
			_ = audit.Insert(buildAuditEntry(user.ID, user.Name, "2fa_enable_failed", "auth", "invalid TOTP code during setup", r))
			jsonError(w, http.StatusUnauthorized, "invalid code")
			return
		}
		if err := users.UpdateUserTOTP(user.ID, dbUser.TOTPSecret, true); err != nil {
			slog.Error("failed to enable 2FA in db", "user_id", user.ID, "err", err)
			jsonError(w, http.StatusInternalServerError, "failed to enable 2FA")
			return
		}
		_ = audit.Insert(buildAuditEntry(user.ID, user.Name, "2fa_enabled", "auth", "TOTP 2FA enabled", r))
		jsonResponse(w, http.StatusOK, map[string]bool{"enabled": true})
	}
}

// handleTOTPDisable verifies the current code against the stored secret before
// clearing TOTP from the user record.
// POST /api/auth/2fa/disable
func handleTOTPDisable(_ *config.Config) http.HandlerFunc {
	users := db.NewUserRepository(db.Instance())
	audit := db.NewAuditRepository(db.Instance())
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		user := getUserFromContext(r.Context())
		if user == nil {
			jsonError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		var req struct {
			Code string `json:"code"`
		}
		if !decodeAuthJSON(w, r, &req) {
			return
		}
		if !totpCodePattern.MatchString(req.Code) {
			jsonError(w, http.StatusBadRequest, "code must contain exactly 6 digits")
			return
		}
		dbUser, err := users.FindByID(user.ID)
		if err != nil {
			jsonError(w, http.StatusUnauthorized, "user not found")
			return
		}
		if !dbUser.TOTPEnabled || dbUser.TOTPSecret == "" {
			jsonError(w, http.StatusBadRequest, "2FA is not enabled")
			return
		}
		valid, err := security.VerifyTOTP(dbUser.TOTPSecret, req.Code)
		if err != nil || !valid {
			_ = audit.Insert(buildAuditEntry(user.ID, user.Name, "2fa_disable_failed", "auth", "invalid TOTP code when disabling 2FA", r))
			jsonError(w, http.StatusUnauthorized, "invalid code")
			return
		}
		if err := users.UpdateUserTOTP(user.ID, "", false); err != nil {
			slog.Error("failed to disable 2FA in db", "user_id", user.ID, "err", err)
			jsonError(w, http.StatusInternalServerError, "failed to disable 2FA")
			return
		}
		_ = audit.Insert(buildAuditEntry(user.ID, user.Name, "2fa_disabled", "auth", "TOTP 2FA disabled", r))
		jsonResponse(w, http.StatusOK, map[string]bool{"disabled": true})
	}
}

// handleTOTPRecovery completes login using a single-use recovery code when the
// user cannot access their authenticator app.
// POST /api/auth/2fa/recovery
func handleTOTPRecovery(cfg *config.Config, limiter *security.RateLimiter) http.HandlerFunc {
	users := db.NewUserRepository(db.Instance())
	audit := db.NewAuditRepository(db.Instance())
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		ip := security.RealIP(r)
		var req struct {
			Email        string `json:"email"`
			Password     string `json:"password"`
			RecoveryCode string `json:"recovery_code"`
		}
		if !decodeAuthJSON(w, r, &req) {
			return
		}
		req.Email = strings.TrimSpace(req.Email)
		req.RecoveryCode = strings.ToUpper(strings.TrimSpace(req.RecoveryCode))
		if req.Email == "" || req.Password == "" || req.RecoveryCode == "" {
			jsonError(w, http.StatusBadRequest, "email, password and recovery_code are required")
			return
		}
		if len(req.Email) > 254 || len(req.Password) > 128 || !recoveryCodePattern.MatchString(req.RecoveryCode) {
			jsonError(w, http.StatusBadRequest, "invalid credentials")
			return
		}

		user, err := users.FindByEmail(req.Email)
		if err != nil {
			// Timing defence: run a dummy bcrypt so missing-account and wrong-password
			// responses take the same time, preventing account enumeration.
			auth.DummyCheckPassword()
			limiter.RecordFailure(ip)
			jsonError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}

		if !auth.CheckPassword(user.Password, req.Password) {
			limiter.RecordFailure(ip)
			_ = audit.Insert(buildAuditEntry(user.ID, user.Name, "login_failed", "auth", "bad password (recovery flow)", r))
			jsonError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		if !user.TOTPEnabled {
			limiter.RecordFailure(ip)
			jsonError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}

		ok, err := users.UseRecoveryCode(user.ID, req.RecoveryCode)
		if err != nil {
			slog.Error("recovery code check failed", "user_id", user.ID, "err", err)
			jsonError(w, http.StatusInternalServerError, "recovery code check failed")
			return
		}
		if !ok {
			limiter.RecordFailure(ip)
			_ = audit.Insert(buildAuditEntry(user.ID, user.Name, "login_recovery_failed", "auth", "invalid or already-used recovery code", r))
			jsonError(w, http.StatusUnauthorized, "invalid or already-used recovery code")
			return
		}

		token, err := auth.GenerateToken(cfg.JWTSecret, user)
		if err != nil {
			slog.Error("could not generate token after recovery", "user_id", user.ID, "err", err)
			jsonError(w, http.StatusInternalServerError, "could not generate token")
			return
		}
		limiter.RecordSuccess(ip)
		http.SetCookie(w, authCookie(r, token, int(auth.TokenTTL.Seconds())))

		_ = audit.Insert(buildAuditEntry(user.ID, user.Name, "login_recovery", "auth", "successful login via recovery code", r))

		user.Password = ""
		jsonResponse(w, http.StatusOK, map[string]any{"token": token, "user": user})
	}
}

// ── fail2ban ──────────────────────────────────────────────────────────────────

// handleFail2BanStatus returns an overview of all jails.
// GET /api/security/fail2ban/status
func handleFail2BanStatus() http.HandlerFunc {
	svc := security.NewFail2BanService()
	return func(w http.ResponseWriter, r *http.Request) {
		status, err := svc.Status()
		if err != nil {
			slog.Error("fail2ban status error", "err", err)
			jsonError(w, http.StatusInternalServerError, "failed to query fail2ban status")
			return
		}
		jsonResponse(w, http.StatusOK, status)
	}
}

// handleFail2BanJail returns detail for a specific jail.
// GET /api/security/fail2ban/jails/{jail}
func handleFail2BanJail() http.HandlerFunc {
	svc := security.NewFail2BanService()
	return func(w http.ResponseWriter, r *http.Request) {
		jail := r.PathValue("jail")
		if jail == "" {
			jsonError(w, http.StatusBadRequest, "jail name is required")
			return
		}
		detail, err := svc.JailDetail(jail)
		if err != nil {
			slog.Error("fail2ban jail detail error", "jail", jail, "err", err)
			status := http.StatusInternalServerError
			if errors.Is(err, security.ErrInvalidFail2BanInput) {
				status = http.StatusBadRequest
			}
			jsonError(w, status, "failed to query jail detail")
			return
		}
		jsonResponse(w, http.StatusOK, detail)
	}
}

// handleFail2BanBan manually bans an IP.
// POST /api/security/fail2ban/ban
func handleFail2BanBan() http.HandlerFunc {
	svc := security.NewFail2BanService()
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Jail string `json:"jail"`
			IP   string `json:"ip"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Jail == "" || req.IP == "" {
			jsonError(w, http.StatusBadRequest, "jail and ip are required")
			return
		}
		if err := svc.BanIP(req.Jail, req.IP); err != nil {
			slog.Error("fail2ban ban error", "jail", req.Jail, "ip", req.IP, "err", err)
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, security.ErrInvalidFail2BanInput):
				status = http.StatusBadRequest
			case errors.Is(err, security.ErrFail2BanUnavailable):
				status = http.StatusServiceUnavailable
			}
			jsonError(w, status, "failed to ban IP")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "banned", "ip": req.IP})
	}
}

// handleFail2BanUnban manually unbans an IP.
// POST /api/security/fail2ban/unban
func handleFail2BanUnban() http.HandlerFunc {
	svc := security.NewFail2BanService()
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Jail string `json:"jail"`
			IP   string `json:"ip"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.Jail == "" || req.IP == "" {
			jsonError(w, http.StatusBadRequest, "jail and ip are required")
			return
		}
		if err := svc.UnbanIP(req.Jail, req.IP); err != nil {
			slog.Error("fail2ban unban error", "jail", req.Jail, "ip", req.IP, "err", err)
			status := http.StatusInternalServerError
			switch {
			case errors.Is(err, security.ErrInvalidFail2BanInput):
				status = http.StatusBadRequest
			case errors.Is(err, security.ErrFail2BanUnavailable):
				status = http.StatusServiceUnavailable
			}
			jsonError(w, status, "failed to unban IP")
			return
		}
		jsonResponse(w, http.StatusOK, map[string]string{"status": "unbanned", "ip": req.IP})
	}
}

// ── IP access lists ──────────────────────────────────────────────────────────

var (
	defaultIPMgr     *security.IPManager
	defaultIPMgrPath = "/var/lib/hserver/iplists.db"
)

func getIPManager(cfg *config.Config) (*security.IPManager, error) {
	if defaultIPMgr != nil {
		return defaultIPMgr, nil
	}
	path := defaultIPMgrPath
	if cfg.DBPath != "" {
		parts := strings.Split(cfg.DBPath, "/")
		parts[len(parts)-1] = "iplists.db"
		path = strings.Join(parts, "/")
	}
	mgr, err := security.NewIPManager(path)
	if err != nil {
		return nil, err
	}
	defaultIPMgr = mgr
	return mgr, nil
}

func handleIPListList(cfg *config.Config, listType security.ListType) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		mgr, err := getIPManager(cfg)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "ip manager unavailable")
			return
		}
		entries, err := mgr.List(listType)
		if err != nil {
			slog.Error("ip list error", "list_type", listType, "err", err)
			jsonError(w, http.StatusInternalServerError, "failed to list "+string(listType))
			return
		}
		if entries == nil {
			entries = []security.IPEntry{}
		}
		jsonResponse(w, http.StatusOK, entries)
	}
}

func handleIPListAdd(cfg *config.Config, listType security.ListType) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			IP        string `json:"ip"`
			Comment   string `json:"comment"`
			ExpiresIn *int   `json:"expiresInMinutes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			jsonError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if req.IP == "" {
			jsonError(w, http.StatusBadRequest, "ip is required")
			return
		}
		var expiresAt *time.Time
		if req.ExpiresIn != nil && *req.ExpiresIn > 0 {
			t := time.Now().Add(time.Duration(*req.ExpiresIn) * time.Minute)
			expiresAt = &t
		}
		mgr, err := getIPManager(cfg)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "ip manager unavailable")
			return
		}
		entry, err := mgr.Add(req.IP, listType, req.Comment, expiresAt)
		if err != nil {
			// Validation errors (invalid IP format, duplicate) are user-facing.
			jsonError(w, http.StatusBadRequest, err.Error())
			return
		}
		jsonResponse(w, http.StatusCreated, entry)
	}
}

func handleIPListDelete(cfg *config.Config, listType security.ListType) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ip := r.PathValue("ip")
		if ip == "" {
			jsonError(w, http.StatusBadRequest, "ip is required")
			return
		}
		ip = strings.ReplaceAll(ip, "%3A", ":")
		mgr, err := getIPManager(cfg)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, "ip manager unavailable")
			return
		}
		if err := mgr.RemoveFromList(ip, listType); err != nil {
			slog.Error("ip list remove error", "list_type", listType, "ip", ip, "err", err)
			jsonError(w, http.StatusInternalServerError, "failed to remove IP from "+string(listType))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func handleIPBlacklistList(cfg *config.Config) http.HandlerFunc {
	return handleIPListList(cfg, security.ListBlacklist)
}

func handleIPBlacklistAdd(cfg *config.Config) http.HandlerFunc {
	return handleIPListAdd(cfg, security.ListBlacklist)
}

func handleIPBlacklistDelete(cfg *config.Config) http.HandlerFunc {
	return handleIPListDelete(cfg, security.ListBlacklist)
}

func handleIPWhitelistList(cfg *config.Config) http.HandlerFunc {
	return handleIPListList(cfg, security.ListWhitelist)
}

func handleIPWhitelistAdd(cfg *config.Config) http.HandlerFunc {
	return handleIPListAdd(cfg, security.ListWhitelist)
}

func handleIPWhitelistDelete(cfg *config.Config) http.HandlerFunc {
	return handleIPListDelete(cfg, security.ListWhitelist)
}

// handleSecurityScore returns a basic security health score.
func handleSecurityScore() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		score := 0
		checks := []map[string]interface{}{}

		// Check firewall — prefer UFW, fall back to iptables DROP policy
		fwActive := false
		fwDetail := ""
		if fwOut, err := securityCommandOutput(r.Context(), "ufw", "status"); err == nil && ufwStatusActive(string(fwOut)) {
			fwActive = true
			fwDetail = "UFW active"
		} else {
			// Check iptables INPUT policy — DROP means a whitelist firewall is in place
			if iptOut, err := securityCommandOutput(r.Context(), "iptables", "-L", "INPUT", "--line-numbers", "-n"); err == nil {
				if strings.Contains(string(iptOut), "DROP") || strings.Contains(string(iptOut), "REJECT") {
					fwActive = true
					fwDetail = "iptables active (DROP policy)"
				}
			}
		}
		if fwActive {
			score += 25
			checks = append(checks, map[string]interface{}{"name": "Firewall", "status": "pass", "detail": fwDetail})
		} else {
			checks = append(checks, map[string]interface{}{"name": "Firewall", "status": "fail", "detail": "No active firewall detected"})
		}

		// Check fail2ban
		f2bStatus := security.NewFail2BanService().Readiness()
		switch f2bStatus.State {
		case security.Fail2BanStateHealthy:
			score += 15
			checks = append(checks, map[string]interface{}{"name": "Fail2Ban", "status": "pass", "detail": "Running"})
		case security.Fail2BanStateNotInstalled:
			checks = append(checks, map[string]interface{}{"name": "Fail2Ban", "status": "warn", "detail": "Not installed (optional)"})
		case security.Fail2BanStateStopped:
			checks = append(checks, map[string]interface{}{"name": "Fail2Ban", "status": "fail", "detail": "Installed but daemon is " + f2bStatus.DaemonState})
		default:
			checks = append(checks, map[string]interface{}{"name": "Fail2Ban", "status": "warn", "detail": "Runtime status could not be verified"})
		}

		// Check installed Let's Encrypt certificates instead of assuming a fixed count.
		validCerts, invalidCerts := letsEncryptCertificateCounts(r.Context(), "/etc/letsencrypt/live")
		switch {
		case validCerts > 0 && invalidCerts == 0:
			score += 25
			checks = append(checks, map[string]interface{}{"name": "SSL Certificates", "status": "pass", "detail": fmt.Sprintf("%d valid certificate(s) detected", validCerts)})
		case validCerts > 0:
			checks = append(checks, map[string]interface{}{"name": "SSL Certificates", "status": "warn", "detail": fmt.Sprintf("%d valid and %d invalid certificate(s) detected", validCerts, invalidCerts)})
		default:
			checks = append(checks, map[string]interface{}{"name": "SSL Certificates", "status": "warn", "detail": "No valid Let's Encrypt certificates detected"})
		}

		// Check the effective sshd configuration rather than claiming key-only auth.
		sshdOut, sshdErr := securityCommandOutput(r.Context(), "sshd", "-T")
		if sshdErr == nil && sshPasswordAuthenticationDisabled(string(sshdOut)) {
			score += 20
			checks = append(checks, map[string]interface{}{"name": "SSH Key Auth", "status": "pass", "detail": "Effective SSH password and keyboard-interactive authentication are disabled"})
		} else if sshdErr != nil {
			checks = append(checks, map[string]interface{}{"name": "SSH Key Auth", "status": "warn", "detail": "Effective sshd configuration could not be read"})
		} else {
			checks = append(checks, map[string]interface{}{"name": "SSH Key Auth", "status": "fail", "detail": "SSH password or keyboard-interactive authentication is enabled"})
		}

		// DKIM is domain-specific and is assessed from the Mail module. Do not infer
		// it from a URL or from an installation-specific domain.
		checks = append(checks, map[string]interface{}{"name": "DKIM Signing", "status": "warn", "detail": "Review DKIM status for each configured domain in Mail"})

		jsonResponse(w, http.StatusOK, map[string]interface{}{
			"score":    score,
			"maxScore": 100,
			"checks":   checks,
		})
	}
}

func securityCommandOutput(parent context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

func ufwStatusActive(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		if strings.EqualFold(strings.TrimSpace(line), "Status: active") {
			return true
		}
	}
	return false
}

func sshPasswordAuthenticationDisabled(output string) bool {
	settings := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.ToLower(line))
		if len(fields) == 2 {
			settings[fields[0]] = fields[1]
		}
	}
	return settings["passwordauthentication"] == "no" && settings["kbdinteractiveauthentication"] == "no"
}

func letsEncryptCertificateCounts(ctx context.Context, liveDir string) (valid, invalid int) {
	entries, err := os.ReadDir(liveDir)
	if err != nil {
		return 0, 0
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		certPath := filepath.Join(liveDir, entry.Name(), "cert.pem")
		if _, err := os.Stat(certPath); err != nil {
			continue
		}
		if _, err := securityCommandOutput(ctx, "openssl", "x509", "-in", certPath, "-noout", "-checkend", "0"); err == nil {
			valid++
		} else {
			invalid++
		}
	}
	return valid, invalid
}
