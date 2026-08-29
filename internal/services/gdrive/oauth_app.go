package gdrive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/IamYGT/heyserver/internal/models"
)

var (
	errClientIDRequired     = errors.New("Google Client ID gerekli")
	errClientSecretRequired = errors.New("Google Client Secret gerekli")
)

const oauthAppKey = "gdrive_oauth_app"

const maxOAuthAppValueBytes = 64 << 10

// contextSettingsReader is optional until the shared settings repository
// exposes GetContext. The type assertion lets this package use that seam when
// present without changing the repository interface or breaking the current
// build while the repository-side addition lands.
type contextSettingsReader interface {
	GetContext(context.Context, string) (*models.Setting, error)
}

// OAuthAppConfig stores Google Cloud OAuth app credentials (panel UI).
type OAuthAppConfig struct {
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret"`
	GCPProjectID string `json:"gcpProjectId,omitempty"`
}

// OAuthSetupLinks are deep links into Google Cloud Console (manual steps only).
type OAuthSetupLinks struct {
	ConsoleCredentials string `json:"consoleCredentials"`
	EnableDriveAPI     string `json:"enableDriveAPI"`
	CreateOAuthClient  string `json:"createOAuthClient"`
}

// OAuthAppInfo is returned to the UI — secret never exposed.
type OAuthAppInfo struct {
	Configured         bool             `json:"configured"`
	ClientID           string           `json:"clientId,omitempty"`
	ClientIDMasked     string           `json:"clientIdMasked,omitempty"`
	HasSecret          bool             `json:"hasSecret"`
	RedirectURI        string           `json:"redirectUri"`
	CredentialsSource  string           `json:"credentialsSource"` // env | vendor | panel | none
	GCPProjectID       string           `json:"gcpProjectId,omitempty"`
	SetupLinks         *OAuthSetupLinks `json:"setupLinks,omitempty"`
	ExpressAvailable   bool             `json:"expressAvailable"`
	ConsoleAutomatable bool             `json:"consoleAutomatable"` // always false for standard OAuth web clients
}

func (s *Service) loadOAuthApp() (OAuthAppConfig, error) {
	return s.loadOAuthAppContext(context.Background())
}

// loadOAuthAppContext reads panel OAuth settings with the best context seam
// currently available. A repository with GetContext is preferred; the current
// repository fallback remains synchronous and is surrounded by context checks
// because its Get method predates context support.
func (s *Service) loadOAuthAppContext(parent context.Context) (OAuthAppConfig, error) {
	var cfg OAuthAppConfig
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return cfg, err
	}
	if s == nil || s.settingsRepo == nil {
		return cfg, errors.New("OAuth app settings are unavailable")
	}
	var (
		setting *models.Setting
		err     error
	)
	if reader, ok := any(s.settingsRepo).(contextSettingsReader); ok {
		setting, err = reader.GetContext(parent, oauthAppKey)
	} else {
		setting, err = s.settingsRepo.Get(oauthAppKey)
	}
	if contextErr := parent.Err(); contextErr != nil {
		return cfg, contextErr
	}
	if err != nil || setting == nil {
		return cfg, err
	}
	if len(setting.Value) > maxOAuthAppValueBytes {
		return cfg, errors.New("OAuth app settings exceed readiness size limit")
	}
	if err := json.Unmarshal([]byte(setting.Value), &cfg); err != nil {
		return cfg, err
	}
	if err := parent.Err(); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func (s *Service) saveOAuthApp(cfg OAuthAppConfig) error {
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return s.settingsRepo.Set(oauthAppKey, string(raw))
}

func (s *Service) resolveCredentials() (clientID, clientSecret, source string) {
	if s.envClientID != "" && s.envClientSecret != "" {
		return s.envClientID, s.envClientSecret, "env"
	}
	vendor, err := s.loadVendorOAuth()
	if err == nil && vendor.ClientID != "" && vendor.ClientSecret != "" {
		return vendor.ClientID, vendor.ClientSecret, "vendor"
	}
	app, err := s.loadOAuthApp()
	if err == nil && app.ClientID != "" && app.ClientSecret != "" {
		return app.ClientID, app.ClientSecret, "panel"
	}
	return "", "", "none"
}

func buildSetupLinks(projectID string) *OAuthSetupLinks {
	if projectID == "" {
		return &OAuthSetupLinks{
			ConsoleCredentials: "https://console.cloud.google.com/apis/credentials",
			EnableDriveAPI:     "https://console.cloud.google.com/apis/library/drive.googleapis.com",
			CreateOAuthClient:  "https://console.cloud.google.com/auth/clients/create",
		}
	}
	q := "project=" + url.QueryEscape(projectID)
	return &OAuthSetupLinks{
		ConsoleCredentials: "https://console.cloud.google.com/apis/credentials?" + q,
		EnableDriveAPI:     "https://console.cloud.google.com/apis/library/drive.googleapis.com?" + q,
		CreateOAuthClient:  "https://console.cloud.google.com/auth/clients/create?" + q,
	}
}

func (s *Service) syncOAuthCredentials() {
	id, secret, _ := s.resolveCredentials()
	s.oauth.setCredentials(id, secret)
}

// syncOAuthCredentialsContext keeps the readiness seam cancellation-aware at
// the existing settings/filesystem boundary. Vendor files are read through the
// bounded context-safe helper; settings use GetContext when the repository has
// it and otherwise retain a synchronous fallback surrounded by context checks.
func (s *Service) syncOAuthCredentialsContext(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	id, secret, _ := s.resolveCredentialsContext(ctx)
	if err := ctx.Err(); err != nil {
		return err
	}
	s.oauth.setCredentials(id, secret)
	return ctx.Err()
}

// resolveCredentialsContext preserves the existing env/vendor/panel priority
// while making readiness credential resolution cancellation-aware. The
// context-safe vendor reader and optional settings GetContext seam are used
// only here; resolveCredentials remains the legacy operational path.
func (s *Service) resolveCredentialsContext(parent context.Context) (clientID, clientSecret, source string) {
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return "", "", "none"
	}
	if s.envClientID != "" && s.envClientSecret != "" {
		return s.envClientID, s.envClientSecret, "env"
	}
	vendor, err := s.loadVendorOAuthContext(parent)
	if contextErr := parent.Err(); contextErr != nil {
		return "", "", "none"
	}
	if err == nil && vendor.ClientID != "" && vendor.ClientSecret != "" {
		return vendor.ClientID, vendor.ClientSecret, "vendor"
	}
	app, err := s.loadOAuthAppContext(parent)
	if contextErr := parent.Err(); contextErr != nil {
		return "", "", "none"
	}
	if err == nil && app.ClientID != "" && app.ClientSecret != "" {
		return app.ClientID, app.ClientSecret, "panel"
	}
	return "", "", "none"
}

// GetOAuthAppInfo returns wizard metadata for the panel UI.
func (s *Service) GetOAuthAppInfo(redirectURI string) (*OAuthAppInfo, error) {
	s.syncOAuthCredentials()
	id, _, source := s.resolveCredentials()
	app, _ := s.loadOAuthApp()

	projectID := app.GCPProjectID
	info := &OAuthAppInfo{
		RedirectURI:        redirectURI,
		CredentialsSource:  source,
		Configured:         source != "none",
		HasSecret:          source != "none",
		ExpressAvailable:   source != "none",
		ConsoleAutomatable: false,
		GCPProjectID:       projectID,
		SetupLinks:         buildSetupLinks(projectID),
	}
	if id != "" {
		info.ClientID = id
		info.ClientIDMasked = maskClientID(id)
	} else if app.ClientID != "" {
		info.ClientID = app.ClientID
		info.ClientIDMasked = maskClientID(app.ClientID)
		info.HasSecret = app.ClientSecret != ""
	}
	return info, nil
}

// SaveOAuthApp persists OAuth app credentials from the panel wizard.
// Empty clientSecret keeps the existing secret (partial update).
// If only gcpProjectId is sent, stores project hint for Console deep links.
func (s *Service) SaveOAuthApp(clientID, clientSecret, gcpProjectID string) error {
	clientID = strings.TrimSpace(clientID)
	clientSecret = strings.TrimSpace(clientSecret)
	gcpProjectID = strings.TrimSpace(gcpProjectID)
	if len(clientID) > 512 {
		return fmt.Errorf("Google Client ID en fazla 512 karakter olabilir")
	}
	if len(clientSecret) > 4096 {
		return fmt.Errorf("Google Client Secret en fazla 4096 karakter olabilir")
	}
	if len(gcpProjectID) > 255 {
		return fmt.Errorf("GCP proje ID en fazla 255 karakter olabilir")
	}

	current, _ := s.loadOAuthApp()
	if clientID == "" && clientSecret == "" {
		if gcpProjectID == "" {
			return errClientIDRequired
		}
		cfg := current
		cfg.GCPProjectID = gcpProjectID
		return s.saveOAuthApp(cfg)
	}
	if clientID == "" {
		return errClientIDRequired
	}

	cfg := OAuthAppConfig{ClientID: clientID, GCPProjectID: gcpProjectID}
	if cfg.GCPProjectID == "" {
		cfg.GCPProjectID = current.GCPProjectID
	}
	if clientSecret != "" {
		cfg.ClientSecret = clientSecret
	} else if current.ClientSecret != "" {
		cfg.ClientSecret = current.ClientSecret
	} else {
		return errClientSecretRequired
	}

	if err := s.saveOAuthApp(cfg); err != nil {
		return err
	}
	s.syncOAuthCredentials()
	return nil
}

func maskClientID(id string) string {
	if len(id) <= 12 {
		return id[:4] + "…"
	}
	return id[:8] + "…" + id[len(id)-4:]
}
