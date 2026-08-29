package deploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
)

func TestRevisionComparisonReportsCurrentAndRollbackDiff(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	projectDir := t.TempDir()
	runGitForRevisionTest(t, projectDir, "init", "-q")
	runGitForRevisionTest(t, projectDir, "config", "user.email", "contributor@example.com")
	runGitForRevisionTest(t, projectDir, "config", "user.name", "HServer Test")
	tracked := filepath.Join(projectDir, "app.txt")
	if err := os.WriteFile(tracked, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForRevisionTest(t, projectDir, "add", "app.txt")
	runGitForRevisionTest(t, projectDir, "commit", "-qm", "first")
	rollbackCommit := runGitForRevisionTest(t, projectDir, "rev-parse", "HEAD")
	if err := os.WriteFile(tracked, []byte("second\nextra\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitForRevisionTest(t, projectDir, "commit", "-qam", "second")
	currentCommit := runGitForRevisionTest(t, projectDir, "rev-parse", "HEAD")
	if err := os.WriteFile(tracked, []byte("working tree\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	database := openMemDB(t)
	service, err := New(database)
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.CreateTarget(models.CreateDeployTargetRequest{
		Name: "revision-app", ProjectDir: projectDir, DeployKind: models.DeployKindScript, DeployScript: "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO deploy_runs (target_id, status, "commit", prev_commit)
		VALUES (?, 'success', ?, ?)
	`, target.ID, currentCommit, rollbackCommit); err != nil {
		t.Fatal(err)
	}

	report, err := service.RevisionComparison(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.State != "ready" || report.CurrentCommit != currentCommit || report.DeployedCommit != currentCommit {
		t.Fatalf("revision report = %+v", report)
	}
	if !report.MatchesDeployed || !report.RollbackAvailable || !report.TrackedChanges {
		t.Fatalf("revision flags = %+v", report)
	}
	if report.RollbackCommit != rollbackCommit || report.CommitsAheadRollback != 1 || report.CommitsBehindRollback != 0 {
		t.Fatalf("rollback comparison = %+v", report)
	}
	if report.FilesChanged != 1 || report.Insertions != 2 || report.Deletions != 1 {
		t.Fatalf("diff summary = %+v", report)
	}
}

func TestRevisionComparisonReportsUnprovisionedTarget(t *testing.T) {
	database := openMemDB(t)
	service, err := New(database)
	if err != nil {
		t.Fatal(err)
	}
	target, err := service.CreateTarget(models.CreateDeployTargetRequest{
		Name: "new-app", RepoURL: "https://github.com/example/new-app.git", ProjectDir: filepath.Join(t.TempDir(), "new-app"), DeployKind: models.DeployKindCompose,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.RevisionComparison(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.State != "not_deployed" || report.CurrentCommit != "" || report.RollbackAvailable {
		t.Fatalf("revision report = %+v", report)
	}
}

func runGitForRevisionTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
	return string(bytesTrimSpace(output))
}

func bytesTrimSpace(value []byte) []byte {
	start, end := 0, len(value)
	for start < end && (value[start] == ' ' || value[start] == '\n' || value[start] == '\r' || value[start] == '\t') {
		start++
	}
	for end > start && (value[end-1] == ' ' || value[end-1] == '\n' || value[end-1] == '\r' || value[end-1] == '\t') {
		end--
	}
	return value[start:end]
}
