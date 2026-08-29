package deploy

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
)

func templateServiceFixture(t *testing.T) (*Service, string) {
	t.Helper()
	dataDir := t.TempDir()
	service, err := NewWithDataDir(openMemDB(t), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	return service, filepath.Join(dataDir, "deploy-templates")
}

func writeDeployTemplateFixture(t *testing.T, directory, name, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestServiceTemplatesDistinguishesNotConfiguredAndHealthy(t *testing.T) {
	service, directory := templateServiceFixture(t)
	inventory := service.Templates()
	if inventory.Status != models.DeployTemplatesNotConfigured || inventory.Directory != directory || len(inventory.Templates) != 0 || len(inventory.Issues) != 0 {
		t.Fatalf("not-configured inventory=%+v", inventory)
	}
	writeDeployTemplateFixture(t, directory, "node-script.json", `{
  "schemaVersion": 1,
  "id": "node-script",
  "name": "Node.js script",
  "description": "Install locked dependencies and build the application.",
  "branch": "main",
  "deploymentKind": "script",
  "composeFile": "",
  "deployScript": "npm ci\nnpm run build\n"
}`, 0o644)
	writeDeployTemplateFixture(t, directory, "compose.json", `{
  "schemaVersion": 1,
  "id": "compose",
  "name": "Docker Compose",
  "description": "Use a contained Compose file.",
  "branch": "stable",
  "deploymentKind": "compose",
  "composeFile": "deploy/compose.yaml",
  "deployScript": ""
}`, 0o644)
	inventory = service.Templates()
	if inventory.Status != models.DeployTemplatesHealthy || len(inventory.Issues) != 0 || len(inventory.Templates) != 2 {
		t.Fatalf("healthy inventory=%+v", inventory)
	}
	if inventory.Templates[0].ID != "compose" || inventory.Templates[0].Branch != "stable" || inventory.Templates[0].ComposeFile != "deploy/compose.yaml" {
		t.Fatalf("compose template=%+v", inventory.Templates[0])
	}
	if inventory.Templates[1].ID != "node-script" || inventory.Templates[1].DeployScript != "npm ci\nnpm run build\n" {
		t.Fatalf("script template=%+v", inventory.Templates[1])
	}
}

func TestServiceTemplatesRetainsValidFilesWhileReportingUnavailableInventory(t *testing.T) {
	service, directory := templateServiceFixture(t)
	writeDeployTemplateFixture(t, directory, "compose.json", `{
  "schemaVersion": 1,
  "id": "compose",
  "name": "Compose",
  "description": "",
  "branch": "",
  "deploymentKind": "compose",
  "composeFile": "",
  "deployScript": ""
}`, 0o644)
	writeDeployTemplateFixture(t, directory, "broken.json", `{
  "schemaVersion": 1,
  "id": "another-id",
  "name": "Broken",
  "deploymentKind": "compose"
}`, 0o644)
	inventory := service.Templates()
	if inventory.Status != models.DeployTemplatesUnavailable || len(inventory.Templates) != 1 || len(inventory.Issues) != 1 {
		t.Fatalf("partial inventory=%+v", inventory)
	}
	if inventory.Templates[0].Branch != "main" || inventory.Issues[0].File != "broken.json" || inventory.Issues[0].Message != "id must match the template filename" {
		t.Fatalf("partial inventory details=%+v", inventory)
	}
}

func TestServiceTemplatesRejectsWritableFilesAndSymlinks(t *testing.T) {
	service, directory := templateServiceFixture(t)
	writeDeployTemplateFixture(t, directory, "writable.json", `{"schemaVersion":1,"id":"writable","name":"Writable","deploymentKind":"compose"}`, 0o666)
	if err := os.Symlink(filepath.Join(directory, "writable.json"), filepath.Join(directory, "linked.json")); err != nil {
		t.Fatal(err)
	}
	inventory := service.Templates()
	if inventory.Status != models.DeployTemplatesUnavailable || len(inventory.Templates) != 0 || len(inventory.Issues) != 2 {
		t.Fatalf("unsafe inventory=%+v", inventory)
	}
	issues := map[string]string{}
	for _, issue := range inventory.Issues {
		issues[issue.File] = issue.Message
	}
	if issues["linked.json"] != "symbolic links are not accepted" || issues["writable.json"] != "template file must not be group- or world-writable" {
		t.Fatalf("issues=%+v", issues)
	}
}

func TestServiceTemplatesRejectsUnknownFieldsAndUnsafeScript(t *testing.T) {
	service, directory := templateServiceFixture(t)
	writeDeployTemplateFixture(t, directory, "unknown.json", `{"schemaVersion":1,"id":"unknown","name":"Unknown","deploymentKind":"compose","repositoryUrl":"https://example.com/private.git"}`, 0o644)
	writeDeployTemplateFixture(t, directory, "unsafe.json", `{"schemaVersion":1,"id":"unsafe","name":"Unsafe","deploymentKind":"script","deployScript":"bash -i"}`, 0o644)
	inventory := service.Templates()
	if inventory.Status != models.DeployTemplatesUnavailable || len(inventory.Templates) != 0 || len(inventory.Issues) != 2 {
		t.Fatalf("invalid inventory=%+v", inventory)
	}
}
