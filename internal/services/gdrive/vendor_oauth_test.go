package gdrive

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/store"
)

func TestVendorOAuthOverridesPanel(t *testing.T) {
	db := openTestDB(t)
	repo := store.NewSettingsRepository(db)
	dir := t.TempDir()

	vendor := OAuthAppConfig{
		ClientID:     "vendor-id.apps.googleusercontent.com",
		ClientSecret: "vendor-secret",
	}
	raw, _ := json.Marshal(vendor)
	if err := os.WriteFile(filepath.Join(dir, vendorOAuthFileName), raw, 0o600); err != nil {
		t.Fatal(err)
	}

	s := New(dir, 3085, "", "", "", "rclone", repo, nil)
	_ = s.SaveOAuthApp("panel-id", "panel-secret", "")

	id, secret, source := s.resolveCredentials()
	if id != vendor.ClientID || secret != vendor.ClientSecret || source != "vendor" {
		t.Fatalf("vendor should win: %q %q %q", id, secret, source)
	}
}

func TestEnvOverridesVendor(t *testing.T) {
	db := openTestDB(t)
	repo := store.NewSettingsRepository(db)
	dir := t.TempDir()

	vendor := OAuthAppConfig{ClientID: "vendor-id", ClientSecret: "vendor-secret"}
	raw, _ := json.Marshal(vendor)
	_ = os.WriteFile(filepath.Join(dir, vendorOAuthFileName), raw, 0o600)

	s := New(dir, 3085, "env-id", "env-secret", "", "rclone", repo, nil)
	id, _, source := s.resolveCredentials()
	if id != "env-id" || source != "env" {
		t.Fatalf("env should win: id=%s source=%s", id, source)
	}
}

func TestLoadVendorOAuthContextBoundsAndRejectsNonRegularSources(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*testing.T, string, string)
	}{
		{
			name: "symlink",
			setup: func(t *testing.T, dir, path string) {
				target := filepath.Join(dir, "vendor-target.json")
				if err := os.WriteFile(target, []byte(`{"clientId":"id","clientSecret":"secret"}`), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "directory",
			setup: func(t *testing.T, _ string, path string) {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "oversized",
			setup: func(t *testing.T, _ string, path string) {
				if err := os.WriteFile(path, []byte(strings.Repeat("x", maxVendorOAuthBytes+1)), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, vendorOAuthFileName)
			test.setup(t, dir, path)
			service := &Service{dataDir: dir}
			if _, err := service.loadVendorOAuthContext(context.Background()); err == nil {
				t.Fatal("loadVendorOAuthContext() unexpectedly accepted unsafe source")
			}
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service := &Service{dataDir: t.TempDir()}
	if _, err := service.loadVendorOAuthContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled loadVendorOAuthContext() error = %v, want context.Canceled", err)
	}
}

func TestResolveCredentialsContextUsesBoundedVendorSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, vendorOAuthFileName)
	if err := os.WriteFile(path, []byte(`{"clientId":"vendor-id","clientSecret":"vendor-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	service := &Service{dataDir: dir}
	id, secret, source := service.resolveCredentialsContext(context.Background())
	if id != "vendor-id" || secret != "vendor-secret" || source != "vendor" {
		t.Fatalf("resolveCredentialsContext() = %q %q %q, want vendor source", id, secret, source)
	}
}

func TestSaveOAuthApp_projectIDOnly(t *testing.T) {
	db := openTestDB(t)
	repo := store.NewSettingsRepository(db)
	s := New(t.TempDir(), 3085, "", "", "", "rclone", repo, nil)

	if err := s.SaveOAuthApp("", "", "gen-lang-client-0836204563"); err != nil {
		t.Fatal(err)
	}
	info, err := s.GetOAuthAppInfo("https://panel.example/cb")
	if err != nil {
		t.Fatal(err)
	}
	if info.GCPProjectID != "gen-lang-client-0836204563" {
		t.Errorf("project id = %q", info.GCPProjectID)
	}
	if info.SetupLinks == nil || info.SetupLinks.CreateOAuthClient == "" {
		t.Error("expected setup links")
	}
}
