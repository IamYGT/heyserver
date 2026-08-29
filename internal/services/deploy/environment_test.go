package deploy

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
)

func environmentServiceFixture(t *testing.T) (*Service, *models.DeployTarget, string) {
	t.Helper()
	dataDir := t.TempDir()
	service, err := NewWithDataDir(openMemDB(t), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.CreateTarget(models.CreateDeployTargetRequest{
		Name: "environment-app", RepoURL: "https://example.com/team/app.git",
		ProjectDir: filepath.Join(t.TempDir(), "not-cloned-yet"), DeployKind: models.DeployKindCompose,
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, target, dataDir
}

func TestService_EnvironmentValuesAreWriteOnlyAndStoredWithPrivateModes(t *testing.T) {
	service, target, dataDir := environmentServiceFixture(t)
	if _, err := service.SetEnvironmentVariable(target.ID, "DATABASE_URL", "postgres://db/app?sslmode=require"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetEnvironmentVariable(target.ID, "APP_MODE", "production"); err != nil {
		t.Fatal(err)
	}
	environment, err := service.Environment(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !environment.Configured || !reflect.DeepEqual(environment.Variables, []models.DeployEnvironmentVariable{{Key: "APP_MODE"}, {Key: "DATABASE_URL"}}) {
		t.Fatalf("environment=%+v", environment)
	}
	directory := filepath.Join(dataDir, "deploy-env")
	file := filepath.Join(directory, "target-1.env")
	directoryInfo, err := os.Stat(directory)
	if err != nil || directoryInfo.Mode().Perm() != 0o700 {
		t.Fatalf("directory mode=%v err=%v", directoryInfo.Mode().Perm(), err)
	}
	fileInfo, err := os.Stat(file)
	if err != nil || fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("file mode=%v err=%v", fileInfo.Mode().Perm(), err)
	}
	content, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	want := "APP_MODE='production'\nDATABASE_URL='postgres://db/app?sslmode=require'\n"
	if string(content) != want {
		t.Fatalf("environment file encoding mismatch")
	}
}

func TestService_EnvironmentDeleteRemovesEmptySecretFile(t *testing.T) {
	service, target, _ := environmentServiceFixture(t)
	if _, err := service.SetEnvironmentVariable(target.ID, "APP_MODE", "production"); err != nil {
		t.Fatal(err)
	}
	environment, err := service.DeleteEnvironmentVariable(target.ID, "APP_MODE")
	if err != nil {
		t.Fatal(err)
	}
	if environment.Configured || len(environment.Variables) != 0 {
		t.Fatalf("environment=%+v", environment)
	}
	if _, err := os.Stat(service.environmentPath(target.ID)); !os.IsNotExist(err) {
		t.Fatalf("empty environment file remains: %v", err)
	}
}

func TestService_DeleteTargetRemovesProjectEnvironment(t *testing.T) {
	service, target, _ := environmentServiceFixture(t)
	if _, err := service.SetEnvironmentVariable(target.ID, "APP_MODE", "production"); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteTarget(target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(service.environmentPath(target.ID)); !os.IsNotExist(err) {
		t.Fatalf("deleted target environment remains: %v", err)
	}
	stored, err := service.GetTarget(target.ID)
	if err != nil || stored != nil {
		t.Fatalf("target=%+v err=%v", stored, err)
	}
}

func TestService_EnvironmentRejectsBoundaryBreakingInput(t *testing.T) {
	service, target, _ := environmentServiceFixture(t)
	invalid := []struct{ key, value string }{
		{key: "--env-file", value: "x"},
		{key: "WITH SPACE", value: "x"},
		{key: "VALID", value: "line-one\nline-two"},
		{key: "VALID", value: "can't-encode"},
	}
	for _, item := range invalid {
		if _, err := service.SetEnvironmentVariable(target.ID, item.key, item.value); err == nil {
			t.Fatalf("accepted key=%q", item.key)
		}
	}
}
