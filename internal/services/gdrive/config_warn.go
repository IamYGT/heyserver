package gdrive

import "log/slog"

// WarnConfig logs deploy-time warnings when OAuth is partially configured.
func WarnConfig(clientID, redirectURI string) {
	if clientID == "" {
		return
	}
	if redirectURI == "" {
		slog.Warn("gdrive: HSERVER_GDRIVE_REDIRECT_URI unset — set public OAuth callback URL (must match Google Cloud Console); token refresh fails after domain change without it")
	}
}
