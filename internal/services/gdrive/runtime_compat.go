package gdrive

import (
	"log/slog"
	"time"
)

// Configured reports whether OAuth client credentials are available without
// claiming that an account is connected or reachable.
func (s *Service) Configured(_ string) bool {
	s.syncOAuthCredentials()
	return s.oauth.configured()
}

// Connected reports whether the current OAuth session is usable.
func (s *Service) Connected(redirectURI string) bool {
	status, err := s.Status(redirectURI)
	return err == nil && status != nil && status.Connected
}

// StartHealthProbe periodically refreshes the Drive connection state.
func (s *Service) StartHealthProbe(redirectURI string, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Minute
	}
	go func() {
		probe := func() {
			if _, err := s.Status(redirectURI); err != nil {
				slog.Warn("Google Drive health probe failed", "error", err)
			}
		}
		probe()
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			probe()
		}
	}()
}
