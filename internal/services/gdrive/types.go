package gdrive

import (
	"errors"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

var ErrInvalidSettings = errors.New("invalid Google Drive settings")

const (
	settingsKey     = "gdrive_settings"
	defaultFolder   = "hserver-backups"
	tokenFileName   = "gdrive-token.json"
	rcloneConfName  = "rclone.conf"
	uploadTimeout   = 30 * time.Minute
	listTimeout     = 2 * time.Minute
	downloadTimeout = 30 * time.Minute
	oauthStateTTL   = 10 * time.Minute
)

// Settings holds user-configurable Google Drive backup options (stored in SQLite settings).
type Settings struct {
	Folder              string `json:"folder"`
	AutoUpload          bool   `json:"autoUpload"`
	RemoteRetentionDays int    `json:"remoteRetentionDays"`
	NotifyOnSuccess     bool   `json:"notifyOnSuccess"`
	NotifyOnFailure     bool   `json:"notifyOnFailure"`
	LastUploadAt        string `json:"lastUploadAt,omitempty"`
	LastUploadFile      string `json:"lastUploadFile,omitempty"`
	LastError           string `json:"lastError,omitempty"`
}

// SettingsUpdate is the complete operator-controlled Google Drive policy.
// Runtime upload result fields remain server-owned and are never accepted from
// an API mutation request.
type SettingsUpdate struct {
	Folder              string
	AutoUpload          bool
	RemoteRetentionDays int
	NotifyOnSuccess     bool
	NotifyOnFailure     bool
}

// Status is returned to the UI — connection state + quota + settings.
type Status struct {
	Connected         bool                   `json:"connected"`
	State             integrationstate.State `json:"state"`
	Message           string                 `json:"message,omitempty"`
	ReconnectRequired bool                   `json:"reconnectRequired"`
	Configured        bool                   `json:"configured"` // OAuth client credentials present
	Email             string                 `json:"email,omitempty"`
	DisplayName       string                 `json:"displayName,omitempty"`
	Quota             *StorageQuota          `json:"quota,omitempty"`
	Settings          Settings               `json:"settings"`
	RcloneFound       bool                   `json:"rcloneFound"`
	RedirectURI       string                 `json:"redirectUri,omitempty"`
	CredentialsSource string                 `json:"credentialsSource,omitempty"`
	OAuthApp          *OAuthAppInfo          `json:"oauthApp,omitempty"`
}

// StorageQuota reflects Google Drive storage usage.
type StorageQuota struct {
	Limit           int64   `json:"limit"`
	Usage           int64   `json:"usage"`
	UsageInDrive    int64   `json:"usageInDrive"`
	UsagePercentage float64 `json:"usagePercentage"`
}

// RemoteBackup represents a file on Google Drive.
type RemoteBackup struct {
	Name    string    `json:"name"`
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modTime"`
}

// tokenData is persisted to disk (mode 0600) — never logged or returned via API.
type tokenData struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	TokenType    string    `json:"token_type"`
	Expiry       time.Time `json:"expiry"`
	RedirectURI  string    `json:"redirect_uri,omitempty"` // OAuth callback used at connect — reuse for refresh
}

// oauthStartResponse is returned when initiating OAuth.
type OAuthStartResponse struct {
	AuthURL string `json:"authUrl"`
	State   string `json:"state"`
}
