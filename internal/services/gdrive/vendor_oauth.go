package gdrive

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

const vendorOAuthFileName = "gdrive-vendor-oauth.json"

const maxVendorOAuthBytes = 64 << 10

// loadVendorOAuth reads deployment-managed shared OAuth credentials from data dir.
// File format: {"clientId":"...","clientSecret":"..."} — chmod 600, not in git.
func (s *Service) loadVendorOAuth() (OAuthAppConfig, error) {
	var cfg OAuthAppConfig
	path := filepath.Join(s.dataDir, vendorOAuthFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// loadVendorOAuthContext is the readiness-safe variant of loadVendorOAuth.
// It never follows a symlink or reads an unbounded/non-regular source, and it
// checks the caller context around each filesystem operation. The legacy
// loadVendorOAuth path remains unchanged for operational callers.
func (s *Service) loadVendorOAuthContext(parent context.Context) (OAuthAppConfig, error) {
	var cfg OAuthAppConfig
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return cfg, err
	}
	if s == nil {
		return cfg, errors.New("vendor OAuth settings are unavailable")
	}
	path := filepath.Join(s.dataDir, vendorOAuthFileName)
	info, err := os.Lstat(path)
	if contextErr := parent.Err(); contextErr != nil {
		return cfg, contextErr
	}
	if err != nil {
		return cfg, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return cfg, errors.New("vendor OAuth settings are unavailable")
	}
	if err := parent.Err(); err != nil {
		return cfg, err
	}
	file, err := os.Open(path)
	if err != nil {
		return cfg, err
	}
	defer func() { _ = file.Close() }()
	if err := parent.Err(); err != nil {
		return cfg, err
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxVendorOAuthBytes+1))
	if err != nil {
		return cfg, err
	}
	if contextErr := parent.Err(); contextErr != nil {
		return cfg, contextErr
	}
	if len(raw) > maxVendorOAuthBytes {
		return cfg, errors.New("vendor OAuth settings exceed readiness size limit")
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return cfg, err
	}
	if err := parent.Err(); err != nil {
		return cfg, err
	}
	return cfg, nil
}
