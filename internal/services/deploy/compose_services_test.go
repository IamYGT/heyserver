package deploy

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
)

func composeServiceFixture(t *testing.T) (*Service, *models.DeployTarget, string) {
	t.Helper()
	projectDir := t.TempDir()
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "docker-args.log")
	docker := `#!/bin/sh
printf '%s\n' "$*" >> "$HSERVER_TEST_DOCKER_LOG"
case "$*" in
  *" ps --all --format json")
    printf '%s\n' '[{"Service":"web","Name":"app-web-1","Image":"example/app:latest","State":"running","Health":"healthy","ExitCode":0,"Publishers":[{"URL":"0.0.0.0","TargetPort":80,"PublishedPort":8080,"Protocol":"tcp"}]}]'
    ;;
  *" logs --no-color --timestamps --tail 200 web")
    printf 'web-1 | ready\n'
    ;;
esac
`
	if err := os.WriteFile(filepath.Join(binDir, "docker"), []byte(docker), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HSERVER_TEST_DOCKER_LOG", logPath)
	db := openMemDB(t)
	service, err := NewWithDataDir(db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.CreateTarget(models.CreateDeployTargetRequest{
		Name: "compose-app", ProjectDir: projectDir,
		DeployKind: models.DeployKindCompose, ComposeFile: "ops/compose.yaml",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetEnvironmentVariable(target.ID, "APP_MODE", "production"); err != nil {
		t.Fatal(err)
	}
	return service, target, logPath
}

func TestService_ComposeServicesAndLogsUseTargetBoundary(t *testing.T) {
	service, target, logPath := composeServiceFixture(t)
	services, err := service.ComposeServices(target.ID)
	if err != nil || len(services) != 1 {
		t.Fatalf("services=%+v err=%v", services, err)
	}
	got := services[0]
	if got.Service != "web" || got.Container != "app-web-1" || got.State != "running" || got.Health != "healthy" || len(got.Ports) != 1 || got.Ports[0] != "0.0.0.0:8080->80/tcp" {
		t.Fatalf("service=%+v", got)
	}
	logs, err := service.ComposeServiceLogs(target.ID, "web", 200)
	if err != nil || logs.Logs != "web-1 | ready\n" || logs.Tail != 200 || logs.Truncated {
		t.Fatalf("logs=%+v err=%v", logs, err)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "compose --env-file " + service.environmentPath(target.ID) + " -f ops/compose.yaml"
	for _, command := range []string{
		prefix + " ps --all --format json",
		prefix + " logs --no-color --timestamps --tail 200 web",
	} {
		if !strings.Contains(string(logged), command) {
			t.Fatalf("commands=%q missing %q", string(logged), command)
		}
	}
}

func TestService_ComposeServiceActionsUseFixedCommands(t *testing.T) {
	service, target, logPath := composeServiceFixture(t)
	for _, action := range []string{"start", "stop", "restart", "recreate"} {
		if err := service.ComposeServiceAction(target.ID, "web", action); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	prefix := "compose --env-file " + service.environmentPath(target.ID) + " -f ops/compose.yaml"
	for _, command := range []string{
		prefix + " start web",
		prefix + " stop web",
		prefix + " restart web",
		prefix + " up -d --build --no-deps web",
	} {
		if !strings.Contains(string(logged), command) {
			t.Fatalf("commands=%q missing %q", string(logged), command)
		}
	}
}

func TestService_ComposeServiceBoundaryRejectsScriptAndUnsafeNames(t *testing.T) {
	service, target, _ := composeServiceFixture(t)
	if err := service.ComposeServiceAction(target.ID, "--project-directory", "start"); err == nil {
		t.Fatal("option-like service name accepted")
	}
	scriptTarget, err := service.CreateTarget(models.CreateDeployTargetRequest{Name: "script", ProjectDir: t.TempDir(), DeployKind: models.DeployKindScript})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.ComposeServices(scriptTarget.ID); !errors.Is(err, ErrComposeTargetRequired) {
		t.Fatalf("script target error=%v", err)
	}
}
