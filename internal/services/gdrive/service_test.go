package gdrive

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/store"
	_ "github.com/mattn/go-sqlite3"
)

func newSettingsService(t *testing.T) (*Service, *store.SettingsRepository) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "settings.db")
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on", dbPath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.MigrateSettings(db); err != nil {
		t.Fatal(err)
	}
	repo := store.NewSettingsRepository(db)
	return New(t.TempDir(), 0, "client-id", "client-secret", "", "rclone", repo, nil), repo
}

func newStatusService(t *testing.T, clientID, clientSecret, rcloneBin string) (*Service, *store.SettingsRepository) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "settings.db")
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on", dbPath))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.MigrateSettings(db); err != nil {
		t.Fatal(err)
	}
	repo := store.NewSettingsRepository(db)
	return New(t.TempDir(), 0, clientID, clientSecret, "", rcloneBin, repo, nil), repo
}

func fakeRclone(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rclone")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func seedStatusToken(t *testing.T, service *Service) {
	t.Helper()
	if err := service.oauth.saveToken(&tokenData{
		AccessToken:  "access-token",
		RefreshToken: "refresh-token",
		TokenType:    "Bearer",
		Expiry:       time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(service.rclone.configPath, []byte("[hserver-gdrive]\ntype = drive\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreFromRemoteRejectsUnboundedFileNames(t *testing.T) {
	for _, name := range []string{
		"readme.txt",
		"dump.sql",
		"../nightly-full.tar.gz",
		"folder/nightly-full.tar.gz",
		"nightly full.tar.gz",
		".tar.gz",
		"_nightly-full.tar.gz",
	} {
		if _, err := validateRemoteBackupName(name); err == nil {
			t.Fatalf("expected error for %q", name)
		}
	}
	for _, name := range []string{"nightly-full.tar.gz", "nightly-db-postgresql.sql.gz"} {
		if got, err := validateRemoteBackupName(name); err != nil || got != name {
			t.Fatalf("name=%q got=%q err=%v", name, got, err)
		}
	}
}

func TestUpdateSettingsPreservesRuntimeFieldsAndRetentionZero(t *testing.T) {
	service, repo := newSettingsService(t)
	if err := repo.Set(settingsKey, `{"folder":"legacy","autoUpload":false,"remoteRetentionDays":30,"notifyOnSuccess":true,"notifyOnFailure":true,"lastUploadAt":"2026-08-27T00:00:00Z","lastUploadFile":"backup.tar.gz","lastError":"observed failure"}`); err != nil {
		t.Fatal(err)
	}

	err := service.UpdateSettings(SettingsUpdate{
		Folder:              "agency/backups",
		AutoUpload:          true,
		RemoteRetentionDays: 0,
		NotifyOnSuccess:     false,
		NotifyOnFailure:     true,
	})
	if err != nil {
		t.Fatal(err)
	}

	settings, err := service.loadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.Folder != "agency/backups" || !settings.AutoUpload || settings.RemoteRetentionDays != 0 {
		t.Fatalf("operator settings=%+v", settings)
	}
	if settings.NotifyOnSuccess || !settings.NotifyOnFailure {
		t.Fatalf("notification settings=%+v", settings)
	}
	if settings.LastUploadAt != "2026-08-27T00:00:00Z" || settings.LastUploadFile != "backup.tar.gz" || settings.LastError != "observed failure" {
		t.Fatalf("runtime fields were not preserved: %+v", settings)
	}
}

func TestUpdateSettingsRejectsUnsafeOrOutOfRangePolicy(t *testing.T) {
	service, _ := newSettingsService(t)
	for _, update := range []SettingsUpdate{
		{Folder: "", RemoteRetentionDays: 30},
		{Folder: "../backups", RemoteRetentionDays: 30},
		{Folder: "agency//backups", RemoteRetentionDays: 30},
		{Folder: "agency backups", RemoteRetentionDays: 30},
		{Folder: "hserver-backups", RemoteRetentionDays: -1},
		{Folder: "hserver-backups", RemoteRetentionDays: 366},
	} {
		if err := service.UpdateSettings(update); !errors.Is(err, ErrInvalidSettings) {
			t.Fatalf("update=%+v err=%v", update, err)
		}
	}
}

func TestLoadSettingsDefaultsOnlyMissingRetention(t *testing.T) {
	service, repo := newSettingsService(t)
	if err := repo.Set(settingsKey, `{"folder":"legacy","autoUpload":false,"notifyOnSuccess":true,"notifyOnFailure":true}`); err != nil {
		t.Fatal(err)
	}
	settings, err := service.loadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.RemoteRetentionDays != 30 {
		t.Fatalf("missing retention=%d, want 30", settings.RemoteRetentionDays)
	}
}

func TestStatusFetchAboutErrorIsUnavailableAndNotConnected(t *testing.T) {
	service, _ := newStatusService(t, "client-id", "client-secret", fakeRclone(t))
	seedStatusToken(t, service)
	service.statusFetchAbout = func(string) (string, string, *StorageQuota, error) {
		return "", "", nil, errors.New("drive about API: status 503")
	}

	status, err := service.Status("")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != integrationstate.Unavailable {
		t.Fatalf("state=%q, want %q", status.State, integrationstate.Unavailable)
	}
	if status.Connected {
		t.Fatal("fetchAbout failure must not report connected")
	}
	if status.Message != "drive about API: status 503" {
		t.Fatalf("message=%q", status.Message)
	}
}

func TestStatusTokenRefreshErrorIsUnavailableAndNotConnected(t *testing.T) {
	service, _ := newStatusService(t, "client-id", "client-secret", fakeRclone(t))
	seedStatusToken(t, service)
	service.statusRefresh = func(*tokenData, string) (*tokenData, error) {
		return nil, errors.New("token refresh failed: temporary outage")
	}

	status, err := service.Status("")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != integrationstate.Unavailable {
		t.Fatalf("state=%q, want %q", status.State, integrationstate.Unavailable)
	}
	if status.Connected {
		t.Fatal("token refresh failure must not report connected")
	}
}

func TestStatusSuccessfulFetchAboutIsHealthyAndConnected(t *testing.T) {
	service, _ := newStatusService(t, "client-id", "client-secret", fakeRclone(t))
	seedStatusToken(t, service)
	service.statusFetchAbout = func(string) (string, string, *StorageQuota, error) {
		return "operator@example.com", "Operator", &StorageQuota{Limit: 100, Usage: 25}, nil
	}

	status, err := service.Status("")
	if err != nil {
		t.Fatal(err)
	}
	if status.State != integrationstate.Healthy || !status.Connected {
		t.Fatalf("state=%q connected=%v, want healthy/true", status.State, status.Connected)
	}
	if status.Email != "operator@example.com" || status.DisplayName != "Operator" {
		t.Fatalf("account=%q/%q", status.Email, status.DisplayName)
	}
}

func TestStatusMissingOAuthRcloneOrTokenIsNotConfigured(t *testing.T) {
	tests := []struct {
		name         string
		clientID     string
		clientSecret string
		rcloneBin    string
		seedToken    bool
	}{
		{name: "oauth", clientID: "", clientSecret: "", rcloneBin: fakeRclone(t)},
		{name: "rclone", clientID: "client-id", clientSecret: "client-secret", rcloneBin: filepath.Join(t.TempDir(), "missing-rclone"), seedToken: true},
		{name: "token", clientID: "client-id", clientSecret: "client-secret", rcloneBin: fakeRclone(t)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, _ := newStatusService(t, test.clientID, test.clientSecret, test.rcloneBin)
			if test.seedToken {
				seedStatusToken(t, service)
			}
			status, err := service.Status("")
			if err != nil {
				t.Fatal(err)
			}
			if status.State != integrationstate.NotConfigured {
				t.Fatalf("state=%q, want %q", status.State, integrationstate.NotConfigured)
			}
			if status.Connected {
				t.Fatal("not configured integration must not report connected")
			}
		})
	}
}
