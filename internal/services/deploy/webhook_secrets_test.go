package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
)

func TestServiceStoresWebhookSecretsAsPrivateFileReferences(t *testing.T) {
	database := openMemDB(t)
	dataDir := t.TempDir()
	service, err := NewWithDataDir(database, dataDir)
	if err != nil {
		t.Fatal(err)
	}
	secret := "github-private-signing-secret"
	target, err := service.CreateTarget(models.CreateDeployTargetRequest{
		Name: "private-webhook", ProjectDir: "/srv/private-webhook", DeployKind: models.DeployKindScript,
		WebhookProvider: models.DeployWebhookGitHub, WebhookToken: secret, AutoDeploy: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if target.WebhookToken != secret || target.WebhookStatus != models.DeployWebhookHealthy {
		t.Fatalf("resolved webhook state=%q secret mismatch", target.WebhookStatus)
	}
	var stored string
	if err := database.QueryRow(`SELECT webhook_token FROM deploy_targets WHERE id = ?`, target.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != service.webhookSecretReference(target.ID) || strings.Contains(stored, secret) {
		t.Fatalf("stored webhook token is not a file reference: %q", stored)
	}
	directoryInfo, err := os.Stat(filepath.Join(dataDir, "deploy-webhook-secrets"))
	if err != nil || directoryInfo.Mode().Perm() != 0o700 {
		t.Fatalf("secret directory mode=%v err=%v", directoryInfo.Mode().Perm(), err)
	}
	fileInfo, err := os.Stat(service.webhookSecretPath(target.ID))
	if err != nil || fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("secret file mode=%v err=%v", fileInfo.Mode().Perm(), err)
	}
	content, err := os.ReadFile(service.webhookSecretPath(target.ID))
	if err != nil || string(content) != secret {
		t.Fatalf("secret file content mismatch: err=%v", err)
	}
	targets, err := service.ListTargets()
	if err != nil || len(targets) != 1 || targets[0].WebhookToken != "" || targets[0].WebhookStatus != models.DeployWebhookHealthy {
		t.Fatalf("target inventory leaked secret metadata: targets=%+v err=%v", targets, err)
	}
	originalUpdatedAt := target.UpdatedAt
	preserved, err := service.UpdateTarget(target.ID, models.UpdateDeployTargetRequest{
		Name: target.Name, ProjectDir: target.ProjectDir, DeployKind: target.DeployKind,
		WebhookProvider: models.DeployWebhookGitHub, AutoDeploy: true, IsActive: true,
		ExpectedUpdatedAt: target.UpdatedAt,
	})
	if err != nil || preserved.WebhookToken != secret || preserved.WebhookStatus != models.DeployWebhookHealthy {
		t.Fatalf("preserved target=%+v err=%v", preserved, err)
	}
	content, err = os.ReadFile(service.webhookSecretPath(target.ID))
	if err != nil || string(content) != secret {
		t.Fatalf("preserved secret file mismatch: err=%v", err)
	}
	if _, err := service.UpdateTarget(target.ID, models.UpdateDeployTargetRequest{
		Name: target.Name, ProjectDir: target.ProjectDir, DeployKind: target.DeployKind,
		WebhookProvider: models.DeployWebhookGitHub, AutoDeploy: true, IsActive: true,
		ExpectedUpdatedAt: originalUpdatedAt,
	}); !errors.Is(err, ErrDeployTargetChanged) {
		t.Fatalf("stale update error = %v", err)
	}
	replacement := "replacement-github-secret"
	updated, err := service.UpdateTarget(target.ID, models.UpdateDeployTargetRequest{
		Name: target.Name, ProjectDir: target.ProjectDir, DeployKind: target.DeployKind,
		WebhookProvider: models.DeployWebhookGitHub, WebhookToken: replacement, AutoDeploy: true, IsActive: true,
		ExpectedUpdatedAt: preserved.UpdatedAt,
	})
	if err != nil || updated.WebhookToken != replacement {
		t.Fatalf("updated target=%+v err=%v", updated, err)
	}
	content, err = os.ReadFile(service.webhookSecretPath(target.ID))
	if err != nil || string(content) != replacement {
		t.Fatalf("replacement secret file mismatch: err=%v", err)
	}
	if err := os.Remove(service.webhookSecretPath(target.ID)); err != nil {
		t.Fatal(err)
	}
	unavailable, err := service.GetTarget(target.ID)
	if err != nil || unavailable.WebhookToken != "" || unavailable.WebhookStatus != models.DeployWebhookUnavailable {
		t.Fatalf("unavailable target=%+v err=%v", unavailable, err)
	}
	preservedUnavailable, err := service.UpdateTarget(target.ID, models.UpdateDeployTargetRequest{
		Name: "private-webhook-renamed", ProjectDir: target.ProjectDir, DeployKind: target.DeployKind,
		WebhookProvider: models.DeployWebhookGitHub, AutoDeploy: true, IsActive: true,
		ExpectedUpdatedAt: unavailable.UpdatedAt,
	})
	if err != nil || preservedUnavailable.WebhookStatus != models.DeployWebhookUnavailable || !preservedUnavailable.AutoDeploy {
		t.Fatalf("preserved unavailable target=%+v err=%v", preservedUnavailable, err)
	}
	cleared, err := service.UpdateTarget(target.ID, models.UpdateDeployTargetRequest{
		Name: preservedUnavailable.Name, ProjectDir: target.ProjectDir, DeployKind: target.DeployKind,
		WebhookProvider: models.DeployWebhookGitHub, ClearWebhookToken: true, AutoDeploy: false, IsActive: true,
		ExpectedUpdatedAt: preservedUnavailable.UpdatedAt,
	})
	if err != nil || cleared.WebhookToken != "" || cleared.WebhookStatus != models.DeployWebhookNotConfigured {
		t.Fatalf("cleared target=%+v err=%v", cleared, err)
	}
	if err := service.DeleteTarget(target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(service.webhookSecretPath(target.ID)); !os.IsNotExist(err) {
		t.Fatalf("deleted target webhook secret remains: %v", err)
	}
}

func TestServiceMigratesLegacyWebhookSecretsOutOfSQLite(t *testing.T) {
	database := openMemDB(t)
	legacySecret := "legacy-github-secret"
	if _, err := database.Exec(`CREATE TABLE deploy_targets (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		repo_url TEXT NOT NULL DEFAULT '',
		branch TEXT NOT NULL DEFAULT 'main',
		project_dir TEXT NOT NULL,
		deploy_script TEXT NOT NULL DEFAULT '',
		webhook_token TEXT NOT NULL DEFAULT '',
		auto_deploy INTEGER NOT NULL DEFAULT 1,
		is_active INTEGER NOT NULL DEFAULT 1,
		created_at DATETIME NOT NULL DEFAULT (datetime('now')),
		updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO deploy_targets (name, project_dir, webhook_token) VALUES ('legacy', '/srv/legacy', ?)`, legacySecret); err != nil {
		t.Fatal(err)
	}
	service, err := NewWithDataDir(database, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := database.QueryRow(`SELECT webhook_token FROM deploy_targets WHERE id = 1`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != service.webhookSecretReference(1) || stored == legacySecret {
		t.Fatalf("legacy secret was not replaced by a file reference: %q", stored)
	}
	target, err := service.GetTarget(1)
	if err != nil || target.WebhookToken != legacySecret || target.WebhookStatus != models.DeployWebhookHealthy {
		t.Fatalf("migrated target=%+v err=%v", target, err)
	}
}
