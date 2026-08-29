package gdrive

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/store"
)

func TestSaveOAuthApp_andResolve(t *testing.T) {
	db := openTestDB(t)
	repo := store.NewSettingsRepository(db)
	s := New(t.TempDir(), 3085, "", "", "", "rclone", repo, nil)

	if err := s.SaveOAuthApp("123.apps.googleusercontent.com", "secret-abc", ""); err != nil {
		t.Fatal(err)
	}
	id, secret, source := s.resolveCredentials()
	if id != "123.apps.googleusercontent.com" || secret != "secret-abc" || source != "panel" {
		t.Fatalf("resolve = %q %q %q", id, secret, source)
	}

	info, err := s.GetOAuthAppInfo("https://panel.example/cb")
	if err != nil {
		t.Fatal(err)
	}
	if !info.Configured || !info.HasSecret {
		t.Error("expected configured with secret")
	}
	if info.ClientIDMasked == "" {
		t.Error("expected masked client id")
	}
}

func TestLoadOAuthAppContextHonorsCancellationBeforeSettingsRead(t *testing.T) {
	db := openTestDB(t)
	s := &Service{settingsRepo: store.NewSettingsRepository(db)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.loadOAuthAppContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled loadOAuthAppContext() error = %v, want context.Canceled", err)
	}
}

func TestResolveCredentialsContextFallsBackToPanelSettings(t *testing.T) {
	db := openTestDB(t)
	repo := store.NewSettingsRepository(db)
	s := New(t.TempDir(), 3085, "", "", "", "rclone", repo, nil)
	if err := s.SaveOAuthApp("panel-id", "panel-secret", ""); err != nil {
		t.Fatal(err)
	}
	id, secret, source := s.resolveCredentialsContext(context.Background())
	if id != "panel-id" || secret != "panel-secret" || source != "panel" {
		t.Fatalf("resolveCredentialsContext() = %q %q %q, want panel source", id, secret, source)
	}
}

func TestSaveOAuthApp_partialSecretUpdate(t *testing.T) {
	db := openTestDB(t)
	repo := store.NewSettingsRepository(db)
	s := New(t.TempDir(), 3085, "", "", "", "rclone", repo, nil)

	_ = s.SaveOAuthApp("id1.apps.googleusercontent.com", "secret1", "")
	if err := s.SaveOAuthApp("id2.apps.googleusercontent.com", "", ""); err != nil {
		t.Fatal(err)
	}
	_, secret, _ := s.resolveCredentials()
	if secret != "secret1" {
		t.Errorf("secret should be preserved, got %q", secret)
	}
}

func TestSaveOAuthAppRejectsOversizedMetadata(t *testing.T) {
	db := openTestDB(t)
	repo := store.NewSettingsRepository(db)
	s := New(t.TempDir(), 3085, "", "", "", "rclone", repo, nil)
	for _, test := range []struct {
		clientID     string
		clientSecret string
		projectID    string
	}{
		{clientID: strings.Repeat("a", 513), clientSecret: "secret"},
		{clientID: "client", clientSecret: strings.Repeat("s", 4097)},
		{projectID: strings.Repeat("p", 256)},
	} {
		if err := s.SaveOAuthApp(test.clientID, test.clientSecret, test.projectID); err == nil {
			t.Fatalf("expected oversized metadata rejection: %+v", test)
		}
	}
}

func TestBuildSetupLinksEscapesProjectID(t *testing.T) {
	links := buildSetupLinks("agency/project?x=1")
	if strings.Contains(links.ConsoleCredentials, "?x=1") || !strings.Contains(links.ConsoleCredentials, "project=agency%2Fproject%3Fx%3D1") {
		t.Fatalf("unescaped project link: %s", links.ConsoleCredentials)
	}
}

func TestEnvOverridesPanel(t *testing.T) {
	db := openTestDB(t)
	repo := store.NewSettingsRepository(db)
	s := New(t.TempDir(), 3085, "env-id", "env-secret", "", "rclone", repo, nil)
	_ = s.SaveOAuthApp("panel-id", "panel-secret", "")
	id, _, source := s.resolveCredentials()
	if id != "env-id" || source != "env" {
		t.Errorf("env should win: id=%s source=%s", id, source)
	}
}

func TestMaskClientID(t *testing.T) {
	m := maskClientID("123456789012-abcdefghijklmnop.apps.googleusercontent.com")
	if m == "" || m == "123456789012-abcdefghijklmnop.apps.googleusercontent.com" {
		t.Errorf("unexpected mask: %s", m)
	}
}
