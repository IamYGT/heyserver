package gdrive

import (
	"os"
	"strings"
	"time"
)

// isAuthFailure reports errors that require OAuth reconnect (not transient network).
func isAuthFailure(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "401") ||
		(strings.Contains(msg, "403") && strings.Contains(msg, "invalid")) ||
		strings.Contains(msg, "invalid_grant") ||
		strings.Contains(msg, "invalid credentials") ||
		strings.Contains(msg, "token refresh failed") ||
		strings.Contains(msg, "invalid token") ||
		strings.Contains(msg, "invalid expiry") ||
		strings.Contains(msg, "empty access token") ||
		strings.Contains(msg, "unauthorized")
}

func tokenNeedsRefresh(td *tokenData) bool {
	if td == nil || td.RefreshToken == "" {
		return true
	}
	if td.AccessToken == "" {
		return true
	}
	if td.Expiry.IsZero() || td.Expiry.Year() < 2000 {
		return true
	}
	return time.Now().After(td.Expiry.Add(-2 * time.Minute))
}

func isInvalidGrant(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "invalid_grant")
}

// handleAuthError records transient auth failures without wiping tokens.
// Credentials are deleted only on confirmed invalid_grant or explicit Disconnect.
func (s *Service) handleAuthError(err error) {
	if err == nil {
		return
	}
	if isInvalidGrant(err) {
		s.invalidateCredentials(err.Error())
		return
	}
	s.recordError(err)
}

func (s *Service) invalidateCredentials(reason string) {
	_ = s.oauth.deleteToken()
	_ = os.Remove(s.rclone.configPath)
	msg := strings.TrimSpace(reason)
	if msg == "" {
		msg = "Google Drive OAuth geçersiz"
	}
	if !strings.Contains(strings.ToLower(msg), "yeniden bağlan") {
		msg += " — Google Drive OAuth ile yeniden bağlanın"
	}
	st, _ := s.loadSettings()
	st.LastError = msg
	_ = s.saveSettings(st)
}

func (s *Service) clearLastError() {
	st, err := s.loadSettings()
	if err != nil || st.LastError == "" {
		return
	}
	st.LastError = ""
	_ = s.saveSettings(st)
}
