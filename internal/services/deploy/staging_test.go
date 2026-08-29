package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
)

func TestCreateStagingTargetCreatesIsolatedTargetWithoutProductionState(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "hserver-data")
	service, err := NewWithDataDir(openMemDB(t), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	production, err := service.CreateTarget(models.CreateDeployTargetRequest{
		Name:            "Example App",
		RepoURL:         "https://github.com/example/app.git",
		Branch:          "main",
		ProjectDir:      filepath.Join(root, "projects", "production"),
		DeployKind:      models.DeployKindCompose,
		ComposeFile:     "deploy/compose.yaml",
		WebhookProvider: models.DeployWebhookGitHub,
		WebhookToken:    "production-signing-secret",
		AutoDeploy:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetEnvironmentVariable(production.ID, "DATABASE_URL", "postgres://production"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(`
		INSERT INTO deploy_domains (target_id, domain, service, host_port)
		VALUES (?, 'app.example.com', 'app', 8080)
	`, production.ID); err != nil {
		t.Fatal(err)
	}

	receipt, err := service.CreateStagingTarget(production.ID, models.CreateDeployStagingRequest{
		Name:       "Example App Preview",
		Branch:     "develop",
		ProjectDir: filepath.Join(root, "projects", "staging"),
	})
	if err != nil {
		t.Fatal(err)
	}
	target := receipt.Target
	if target.Environment != models.DeployEnvironmentStaging || target.SourceTargetID == nil || *target.SourceTargetID != production.ID {
		t.Fatalf("staging relationship = %+v", target)
	}
	if target.Name != "Example App Preview" || target.Branch != "develop" || target.ProjectDir != filepath.Join(root, "projects", "staging") {
		t.Fatalf("staging identity = %+v", target)
	}
	if target.RepoURL != production.RepoURL || target.DeployKind != production.DeployKind || target.ComposeFile != production.ComposeFile || target.WebhookProvider != production.WebhookProvider {
		t.Fatalf("staging inherited executor = %+v", target)
	}
	if target.WebhookToken != "" || target.WebhookStatus != models.DeployWebhookNotConfigured || target.AutoDeploy {
		t.Fatalf("staging webhook boundary = %+v", target)
	}
	if receipt.StorageBoundary != "isolated_project_directory" || receipt.EnvironmentValuesCopied || receipt.WebhookSecretCopied || receipt.DomainsCopied || receipt.DNSConfigured {
		t.Fatalf("staging receipt = %+v", receipt)
	}
	environment, err := service.Environment(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if environment.Configured || len(environment.Variables) != 0 {
		t.Fatalf("staging environment = %+v", environment)
	}
	var stagingDomains int
	if err := service.db.QueryRow(`SELECT COUNT(*) FROM deploy_domains WHERE target_id = ?`, target.ID).Scan(&stagingDomains); err != nil {
		t.Fatal(err)
	}
	if stagingDomains != 0 {
		t.Fatalf("staging domains = %d", stagingDomains)
	}
	productionEnvironment, err := service.Environment(production.ID)
	if err != nil || !productionEnvironment.Configured || len(productionEnvironment.Variables) != 1 {
		t.Fatalf("production environment changed: environment=%+v err=%v", productionEnvironment, err)
	}
	var productionDomains int
	if err := service.db.QueryRow(`SELECT COUNT(*) FROM deploy_domains WHERE target_id = ?`, production.ID).Scan(&productionDomains); err != nil {
		t.Fatal(err)
	}
	if productionDomains != 1 {
		t.Fatalf("production domains = %d", productionDomains)
	}
}

func TestCreateStagingTargetDefaultsNameAndBranch(t *testing.T) {
	root := t.TempDir()
	service, err := NewWithDataDir(openMemDB(t), filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	production, err := service.CreateTarget(models.CreateDeployTargetRequest{
		Name:       "Docs",
		RepoURL:    "https://github.com/example/docs.git",
		Branch:     "stable",
		ProjectDir: filepath.Join(root, "production"),
		DeployKind: models.DeployKindScript,
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := service.CreateStagingTarget(production.ID, models.CreateDeployStagingRequest{
		ProjectDir: filepath.Join(root, "staging"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Target.Name != "Docs Staging" || receipt.Target.Branch != "stable" {
		t.Fatalf("defaults = %+v", receipt.Target)
	}
}

func TestCreateStagingTargetRejectsInvalidSourceAndStorageBoundaries(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	productionDir := filepath.Join(root, "projects", "production")
	if err := os.MkdirAll(productionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	service, err := NewWithDataDir(openMemDB(t), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	production, err := service.CreateTarget(models.CreateDeployTargetRequest{
		Name:       "App",
		ProjectDir: productionDir,
		DeployKind: models.DeployKindCompose,
	})
	if err != nil {
		t.Fatal(err)
	}
	staging, err := service.CreateStagingTarget(production.ID, models.CreateDeployStagingRequest{
		ProjectDir: filepath.Join(root, "projects", "staging"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateStagingTarget(staging.Target.ID, models.CreateDeployStagingRequest{
		ProjectDir: filepath.Join(root, "projects", "nested-staging"),
	}); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("staging source error = %v", err)
	}
	if _, err := service.CreateStagingTarget(99999, models.CreateDeployStagingRequest{
		ProjectDir: filepath.Join(root, "projects", "missing"),
	}); !errors.Is(err, ErrDeployTargetNotFound) {
		t.Fatalf("missing source error = %v", err)
	}

	alias := filepath.Join(root, "production-alias")
	if err := os.Symlink(productionDir, alias); err != nil {
		t.Fatal(err)
	}
	for name, candidate := range map[string]string{
		"relative":            "staging",
		"same":                productionDir,
		"production child":    filepath.Join(productionDir, "staging"),
		"production parent":   filepath.Dir(productionDir),
		"hserver data child":  filepath.Join(dataDir, "projects", "staging"),
		"hserver data parent": filepath.Dir(dataDir),
		"symlink alias":       filepath.Join(alias, "preview"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.CreateStagingTarget(production.ID, models.CreateDeployStagingRequest{ProjectDir: candidate}); !errors.Is(err, ErrInvalidTarget) {
				t.Fatalf("CreateStagingTarget(%q) error = %v", candidate, err)
			}
		})
	}
}

func TestStagingTargetProtectsProductionDeleteAndProjectDirectoryUpdates(t *testing.T) {
	root := t.TempDir()
	service, err := NewWithDataDir(openMemDB(t), filepath.Join(root, "data"))
	if err != nil {
		t.Fatal(err)
	}
	production, err := service.CreateTarget(models.CreateDeployTargetRequest{
		Name:       "App",
		ProjectDir: filepath.Join(root, "projects", "production"),
		DeployKind: models.DeployKindCompose,
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := service.CreateStagingTarget(production.ID, models.CreateDeployStagingRequest{
		ProjectDir: filepath.Join(root, "projects", "staging"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteTarget(production.ID); !errors.Is(err, ErrStagingTargetsAttached) {
		t.Fatalf("production delete error = %v", err)
	}

	stagingUpdate := updateDeployTargetRequest(receipt.Target)
	stagingUpdate.ProjectDir = production.ProjectDir
	if _, err := service.UpdateTarget(receipt.Target.ID, stagingUpdate); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("staging update error = %v", err)
	}
	productionUpdate := updateDeployTargetRequest(*production)
	productionUpdate.ProjectDir = filepath.Join(receipt.Target.ProjectDir, "production")
	if _, err := service.UpdateTarget(production.ID, productionUpdate); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("production update error = %v", err)
	}
	if _, err := service.CreateTarget(models.CreateDeployTargetRequest{
		Name:       "Overlapping Production",
		ProjectDir: filepath.Join(receipt.Target.ProjectDir, "other"),
		DeployKind: models.DeployKindCompose,
	}); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("overlapping production create error = %v", err)
	}

	if err := service.DeleteTarget(receipt.Target.ID); err != nil {
		t.Fatalf("delete staging: %v", err)
	}
	if err := service.DeleteTarget(production.ID); err != nil {
		t.Fatalf("delete production: %v", err)
	}
}

func updateDeployTargetRequest(target models.DeployTarget) models.UpdateDeployTargetRequest {
	return models.UpdateDeployTargetRequest{
		Name:              target.Name,
		RepoURL:           target.RepoURL,
		Branch:            target.Branch,
		ProjectDir:        target.ProjectDir,
		DeployKind:        target.DeployKind,
		ComposeFile:       target.ComposeFile,
		DeployScript:      target.DeployScript,
		WebhookProvider:   target.WebhookProvider,
		WebhookToken:      target.WebhookToken,
		AutoDeploy:        target.AutoDeploy,
		IsActive:          target.IsActive,
		ExpectedUpdatedAt: target.UpdatedAt,
	}
}
