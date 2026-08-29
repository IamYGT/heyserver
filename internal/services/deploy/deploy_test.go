package deploy

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/IamYGT/heyserver/internal/models"
	_ "github.com/mattn/go-sqlite3"
)

// computeSignature returns the GitHub-style HMAC-SHA256 signature for a payload.
func computeSignature(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// openMemDB opens an in-memory SQLite database for testing.
func openMemDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// ---------------------------------------------------------------------------
// migrate() tests
// ---------------------------------------------------------------------------

func TestMigrate_CreatesDeployTargetsTable(t *testing.T) {
	db := openMemDB(t)
	svc := &Service{db: db}

	if err := svc.migrate(); err != nil {
		t.Fatalf("migrate(): %v", err)
	}

	var tableName string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='deploy_targets'`,
	).Scan(&tableName)
	if err != nil {
		t.Fatalf("deploy_targets table not found: %v", err)
	}
	if tableName != "deploy_targets" {
		t.Errorf("table name: got %q, want %q", tableName, "deploy_targets")
	}
}

func TestMigrate_CreatesDeployRunsTable(t *testing.T) {
	db := openMemDB(t)
	svc := &Service{db: db}

	if err := svc.migrate(); err != nil {
		t.Fatalf("migrate(): %v", err)
	}

	var tableName string
	err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='deploy_runs'`,
	).Scan(&tableName)
	if err != nil {
		t.Fatalf("deploy_runs table not found: %v", err)
	}
	if tableName != "deploy_runs" {
		t.Errorf("table name: got %q, want %q", tableName, "deploy_runs")
	}
}

func TestMigrateCreatesWebhookDeliveryTable(t *testing.T) {
	database := openMemDB(t)
	service := &Service{db: database}
	if err := service.migrate(); err != nil {
		t.Fatal(err)
	}
	var tableName string
	if err := database.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='deploy_webhook_deliveries'`).Scan(&tableName); err != nil {
		t.Fatalf("deploy_webhook_deliveries table not found: %v", err)
	}
}

func TestMigrateAddsTLSStateToExistingProjectDomains(t *testing.T) {
	database := openMemDB(t)
	if _, err := database.Exec(`CREATE TABLE deploy_domains (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		target_id INTEGER NOT NULL,
		domain TEXT NOT NULL UNIQUE,
		service TEXT NOT NULL,
		host_port INTEGER NOT NULL,
		created_at DATETIME NOT NULL,
		updated_at DATETIME NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	service := &Service{db: database}
	if err := service.migrate(); err != nil {
		t.Fatal(err)
	}
	exists, err := deployDomainColumnExists(database, "tls_enabled")
	if err != nil || !exists {
		t.Fatalf("tls_enabled exists=%v err=%v", exists, err)
	}
	var defaultValue string
	if err := database.QueryRow(`SELECT dflt_value FROM pragma_table_info('deploy_domains') WHERE name = 'tls_enabled'`).Scan(&defaultValue); err != nil {
		t.Fatal(err)
	}
	if defaultValue != "0" {
		t.Fatalf("tls_enabled default = %q", defaultValue)
	}
}

func TestMigrate_Idempotent(t *testing.T) {
	db := openMemDB(t)
	svc := &Service{db: db}

	if err := svc.migrate(); err != nil {
		t.Fatalf("first migrate(): %v", err)
	}
	if err := svc.migrate(); err != nil {
		t.Fatalf("second migrate(): %v", err)
	}
}

func TestMigrate_DeployTargetsInsertAndRead(t *testing.T) {
	db := openMemDB(t)
	svc := &Service{db: db}

	if err := svc.migrate(); err != nil {
		t.Fatalf("migrate(): %v", err)
	}

	_, err := db.Exec(`
		INSERT INTO deploy_targets (name, project_dir) VALUES (?, ?)
	`, "test-app", "/var/www/test")
	if err != nil {
		t.Fatalf("insert into deploy_targets: %v", err)
	}

	var name, dir string
	err = db.QueryRow(`SELECT name, project_dir FROM deploy_targets WHERE name = ?`, "test-app").
		Scan(&name, &dir)
	if err != nil {
		t.Fatalf("select from deploy_targets: %v", err)
	}
	if name != "test-app" {
		t.Errorf("name: got %q, want %q", name, "test-app")
	}
	if dir != "/var/www/test" {
		t.Errorf("project_dir: got %q, want %q", dir, "/var/www/test")
	}
}

func TestMigrate_DeployRunsInsertAndRead(t *testing.T) {
	db := openMemDB(t)
	svc := &Service{db: db}

	if err := svc.migrate(); err != nil {
		t.Fatalf("migrate(): %v", err)
	}

	res, err := db.Exec(`INSERT INTO deploy_targets (name, project_dir) VALUES (?, ?)`, "app", "/app")
	if err != nil {
		t.Fatalf("insert deploy_targets: %v", err)
	}
	targetID, _ := res.LastInsertId()

	_, err = db.Exec(`
		INSERT INTO deploy_runs (target_id, status) VALUES (?, ?)
	`, targetID, "pending")
	if err != nil {
		t.Fatalf("insert into deploy_runs: %v", err)
	}

	var status string
	err = db.QueryRow(`SELECT status FROM deploy_runs WHERE target_id = ?`, targetID).Scan(&status)
	if err != nil {
		t.Fatalf("select from deploy_runs: %v", err)
	}
	if status != "pending" {
		t.Errorf("status: got %q, want %q", status, "pending")
	}
}

func TestMigrate_DefaultValues(t *testing.T) {
	db := openMemDB(t)
	svc := &Service{db: db}

	if err := svc.migrate(); err != nil {
		t.Fatalf("migrate(): %v", err)
	}

	_, err := db.Exec(`INSERT INTO deploy_targets (name, project_dir) VALUES (?, ?)`, "defaults-test", "/tmp")
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	var branch, webhookProvider, environment string
	var autoDeploy, isActive int
	err = db.QueryRow(`SELECT branch, webhook_provider, environment, auto_deploy, is_active FROM deploy_targets WHERE name = ?`, "defaults-test").
		Scan(&branch, &webhookProvider, &environment, &autoDeploy, &isActive)
	if err != nil {
		t.Fatalf("select: %v", err)
	}

	if branch != "main" {
		t.Errorf("branch default: got %q, want %q", branch, "main")
	}
	if webhookProvider != string(models.DeployWebhookGitHub) {
		t.Errorf("webhook_provider default: got %q, want %q", webhookProvider, models.DeployWebhookGitHub)
	}
	if environment != string(models.DeployEnvironmentProduction) {
		t.Errorf("environment default: got %q, want %q", environment, models.DeployEnvironmentProduction)
	}
	if autoDeploy != 1 {
		t.Errorf("auto_deploy default: got %d, want 1", autoDeploy)
	}
	if isActive != 1 {
		t.Errorf("is_active default: got %d, want 1", isActive)
	}
}

// ---------------------------------------------------------------------------
// New() helper
// ---------------------------------------------------------------------------

func TestNew_ReturnsServiceOnSuccess(t *testing.T) {
	db := openMemDB(t)
	svc, err := New(db)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}
	if svc == nil {
		t.Fatal("New() returned nil service")
	}
}

// ---------------------------------------------------------------------------
// VerifySignature tests
// ---------------------------------------------------------------------------

func TestVerifySignature_Valid(t *testing.T) {
	secret := "my-webhook-secret"
	body := []byte(`{"ref":"refs/heads/main","repository":{"name":"myapp"}}`)
	sig := computeSignature(secret, body)

	if !VerifySignature(secret, sig, body) {
		t.Error("expected valid signature to pass")
	}
}

func TestVerifySignature_InvalidPrefix(t *testing.T) {
	if VerifySignature("secret", "md5=abc123", []byte("body")) {
		t.Error("should reject non-sha256 prefix")
	}
}

func TestVerifySignature_EmptySignature(t *testing.T) {
	if VerifySignature("secret", "", []byte("body")) {
		t.Error("should reject empty signature")
	}
}

func TestVerifySignature_TamperedBody(t *testing.T) {
	secret := "my-webhook-secret"
	originalBody := []byte("original body")
	tamperedBody := []byte("tampered body")

	sig := computeSignature(secret, originalBody)

	if VerifySignature(secret, sig, tamperedBody) {
		t.Error("should reject signature for tampered body")
	}
}

func TestVerifySignature_WrongSecret(t *testing.T) {
	body := []byte("payload")
	sig := computeSignature("correct-secret", body)

	if VerifySignature("wrong-secret", sig, body) {
		t.Error("should reject signature computed with different secret")
	}
}

func TestVerifySignature_InvalidHex(t *testing.T) {
	if VerifySignature("secret", "sha256=gggg-not-valid-hex", []byte("body")) {
		t.Error("should reject invalid hex in signature")
	}
}

func TestVerifySignature_TableDriven(t *testing.T) {
	secret := "test-secret"
	body := []byte("test body")
	validSig := computeSignature(secret, body)

	tests := []struct {
		name   string
		secret string
		sig    string
		body   []byte
		want   bool
	}{
		{"valid", secret, validSig, body, true},
		{"wrong secret", "other-secret", validSig, body, false},
		{"tampered body", secret, validSig, []byte("different"), false},
		{"no prefix", secret, hex.EncodeToString([]byte("abc")), body, false},
		{"empty sig", secret, "", body, false},
		{"bad hex", secret, "sha256=xyz!", body, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := VerifySignature(tc.secret, tc.sig, tc.body)
			if got != tc.want {
				t.Errorf("VerifySignature() = %v, want %v", got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// boolToInt tests
// ---------------------------------------------------------------------------

func TestBoolToInt(t *testing.T) {
	if boolToInt(true) != 1 {
		t.Error("boolToInt(true) must return 1")
	}
	if boolToInt(false) != 0 {
		t.Error("boolToInt(false) must return 0")
	}
}

func TestService_TargetCRUD(t *testing.T) {
	db := openMemDB(t)
	svc, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	target, err := svc.CreateTarget(models.CreateDeployTargetRequest{
		Name:         "panel",
		RepoURL:      "https://github.com/example/panel.git",
		ProjectDir:   "/var/www/panel",
		WebhookToken: "secret-token",
		AutoDeploy:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if target.ID == 0 || target.Branch != "main" {
		t.Errorf("target = %+v", target)
	}
	list, err := svc.ListTargets()
	if err != nil || len(list) != 1 {
		t.Fatalf("ListTargets: err=%v len=%d", err, len(list))
	}
	got, err := svc.GetTarget(target.ID)
	if err != nil || got.Name != "panel" {
		t.Fatalf("GetTarget: err=%v got=%+v", err, got)
	}
	if err := svc.DeleteTarget(target.ID); err != nil {
		t.Fatal(err)
	}
	list, _ = svc.ListTargets()
	if len(list) != 0 {
		t.Errorf("expected empty after delete, got %d", len(list))
	}
}

func TestMigrate_AddsComposeColumnsToExistingTargetTable(t *testing.T) {
	db := openMemDB(t)
	_, err := db.Exec(`CREATE TABLE deploy_targets (
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
	)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO deploy_targets (name, project_dir, deploy_script) VALUES ('legacy', '/srv/legacy', './deploy.sh')`); err != nil {
		t.Fatal(err)
	}

	svc, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	target, err := svc.GetTarget(1)
	if err != nil {
		t.Fatal(err)
	}
	if target.DeployKind != models.DeployKindScript || target.ComposeFile != "" || target.DeployScript != "./deploy.sh" || target.WebhookProvider != models.DeployWebhookGitHub || target.Environment != models.DeployEnvironmentProduction || target.SourceTargetID != nil {
		t.Fatalf("legacy target migration = %+v", target)
	}
}

func TestService_ComposeTargetValidationAndPersistence(t *testing.T) {
	db := openMemDB(t)
	svc, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	target, err := svc.CreateTarget(models.CreateDeployTargetRequest{
		Name:        "compose-app",
		RepoURL:     "https://github.com/example/compose-app.git",
		ProjectDir:  "/srv/compose-app",
		DeployKind:  models.DeployKindCompose,
		ComposeFile: "deploy/compose.yaml",
	})
	if err != nil {
		t.Fatal(err)
	}
	if target.DeployKind != models.DeployKindCompose || target.ComposeFile != "deploy/compose.yaml" {
		t.Fatalf("compose target = %+v", target)
	}

	invalid := []models.CreateDeployTargetRequest{
		{Name: "multi\nline", ProjectDir: "/srv/app", DeployKind: models.DeployKindScript},
		{Name: strings.Repeat("a", 129), ProjectDir: "/srv/app", DeployKind: models.DeployKindScript},
		{Name: "relative", ProjectDir: "srv/app", DeployKind: models.DeployKindCompose},
		{Name: "escape", ProjectDir: "/srv/app", DeployKind: models.DeployKindCompose, ComposeFile: "../compose.yaml"},
		{Name: "script", ProjectDir: "/srv/app", DeployKind: models.DeployKindCompose, DeployScript: "docker compose up"},
		{Name: "kind", ProjectDir: "/srv/app", DeployKind: "custom"},
		{Name: "webhook", ProjectDir: "/srv/app", AutoDeploy: true},
	}
	for _, request := range invalid {
		if _, err := svc.CreateTarget(request); !errors.Is(err, ErrInvalidTarget) {
			t.Errorf("CreateTarget(%+v) error = %v, want ErrInvalidTarget", request, err)
		}
	}
}

func TestComposeCommandArgs(t *testing.T) {
	if got, want := composeCommandArgs("", "config", "--quiet"), []string{"compose", "config", "--quiet"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("auto-discovery args = %#v, want %#v", got, want)
	}
	if got, want := composeCommandArgs("ops/compose.yml", "up", "-d", "--build", "--remove-orphans"), []string{"compose", "-f", "ops/compose.yml", "up", "-d", "--build", "--remove-orphans"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("explicit file args = %#v, want %#v", got, want)
	}
	if got, want := composeCommandArgsWithEnvironment("ops/compose.yml", "/var/lib/hserver/deploy-env/target-7.env", "config", "--quiet"), []string{"compose", "--env-file", "/var/lib/hserver/deploy-env/target-7.env", "-f", "ops/compose.yml", "config", "--quiet"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("environment args = %#v, want %#v", got, want)
	}
}

func TestRunDeploymentExecutor_ComposeUsesFixedCommands(t *testing.T) {
	projectDir := t.TempDir()
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "docker-args.log")
	dockerPath := filepath.Join(binDir, "docker")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$HSERVER_TEST_DOCKER_LOG\"\necho ok\n"
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HSERVER_TEST_DOCKER_LOG", logPath)

	output, err := runDeploymentExecutor(&models.DeployTarget{
		ProjectDir:  projectDir,
		DeployKind:  models.DeployKindCompose,
		ComposeFile: "ops/compose.yml",
	})
	if err != nil {
		t.Fatal(err)
	}
	if output != "ok\nok\n" {
		t.Fatalf("output = %q", output)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	want := strings.Join([]string{
		"compose -f ops/compose.yml config --quiet",
		"compose -f ops/compose.yml up -d --build --remove-orphans",
		"",
	}, "\n")
	if string(logged) != want {
		t.Fatalf("docker commands = %q, want %q", string(logged), want)
	}
}

func TestService_PreflightReportsComposeReadiness(t *testing.T) {
	projectDir := t.TempDir()
	binDir := t.TempDir()
	for _, command := range []string{"git", "docker"} {
		path := filepath.Join(binDir, command)
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho ready\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	db := openMemDB(t)
	svc, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	target, err := svc.CreateTarget(models.CreateDeployTargetRequest{
		Name:       "compose-app",
		ProjectDir: projectDir,
		DeployKind: models.DeployKindCompose,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := svc.Preflight(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Eligible || len(report.Checks) != 6 {
		t.Fatalf("preflight = %+v", report)
	}
	for _, check := range report.Checks {
		if check.Status != "pass" {
			t.Fatalf("preflight check = %+v", check)
		}
	}
}

func TestService_PreflightAllowsDeferredRepositoryProvisioning(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "apps", "compose-app")
	binDir := t.TempDir()
	for _, command := range []string{"git", "docker"} {
		path := filepath.Join(binDir, command)
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho ready\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	db := openMemDB(t)
	svc, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	target, err := svc.CreateTarget(models.CreateDeployTargetRequest{
		Name:       "new-compose-app",
		RepoURL:    "https://github.com/example/compose-app.git",
		ProjectDir: projectDir,
		DeployKind: models.DeployKindCompose,
	})
	if err != nil {
		t.Fatal(err)
	}
	report, err := svc.Preflight(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Eligible {
		t.Fatalf("provisioning preflight = %+v", report)
	}
	pending := map[string]bool{}
	for _, check := range report.Checks {
		if check.Status == "pending" {
			pending[check.ID] = true
		}
	}
	for _, id := range []string{"project-directory", "git-checkout", "compose-config"} {
		if !pending[id] {
			t.Fatalf("check %q was not pending: %+v", id, report.Checks)
		}
	}
}

func TestEnsureDeployCheckoutClonesWithFixedArguments(t *testing.T) {
	root := t.TempDir()
	projectDir := filepath.Join(root, "apps", "panel")
	binDir := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "git-args.log")
	gitPath := filepath.Join(binDir, "git")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$HSERVER_TEST_GIT_LOG"
if [ "$1" = "clone" ]; then
  for last do :; done
  mkdir -p "$last"
  echo cloned
else
  echo deadbeef
fi
`
	if err := os.WriteFile(gitPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("HSERVER_TEST_GIT_LOG", logPath)

	provisioned, output, err := ensureDeployCheckout(&models.DeployTarget{
		RepoURL:    "https://github.com/example/panel.git",
		Branch:     "main",
		ProjectDir: projectDir,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !provisioned || output != "cloned\n" {
		t.Fatalf("provisioned=%t output=%q", provisioned, output)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	wantClone := "clone --branch main --single-branch -- https://github.com/example/panel.git " + projectDir
	if !strings.Contains(string(logged), wantClone) {
		t.Fatalf("git commands = %q, want clone %q", string(logged), wantClone)
	}
}

func TestService_RejectsUnsafeRepositoryAndBranchConfiguration(t *testing.T) {
	db := openMemDB(t)
	svc, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	invalid := []models.CreateDeployTargetRequest{
		{Name: "credential", RepoURL: "https://token@example.com/team/app.git", Branch: "main", ProjectDir: "/srv/app"},
		{Name: "query-secret", RepoURL: "https://example.com/team/app.git?token=secret", Branch: "main", ProjectDir: "/srv/app"},
		{Name: "scheme", RepoURL: "file:///tmp/repo", Branch: "main", ProjectDir: "/srv/app"},
		{Name: "branch-option", RepoURL: "https://example.com/team/app.git", Branch: "--upload-pack", ProjectDir: "/srv/app"},
		{Name: "branch-ref", RepoURL: "git@example.com:team/app.git", Branch: "feature..escape", ProjectDir: "/srv/app"},
	}
	for _, request := range invalid {
		if _, err := svc.CreateTarget(request); !errors.Is(err, ErrInvalidTarget) {
			t.Errorf("CreateTarget(%+v) error = %v, want ErrInvalidTarget", request, err)
		}
	}
}

func TestService_TriggerDeployRequiresFreshPreflight(t *testing.T) {
	db := openMemDB(t)
	svc, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	target, err := svc.CreateTarget(models.CreateDeployTargetRequest{
		Name:       "unready-compose",
		ProjectDir: t.TempDir(),
		DeployKind: models.DeployKindCompose,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.TriggerDeploy(target.ID, "manual"); !errors.Is(err, ErrPreflight) {
		t.Fatalf("TriggerDeploy error = %v, want ErrPreflight", err)
	}
	runs, err := svc.ListRuns(target.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 0 {
		t.Fatalf("preflight refusal created %d runs", len(runs))
	}
}

func TestService_RollbackPreflightAllowsBrokenCurrentComposeConfig(t *testing.T) {
	projectDir := t.TempDir()
	binDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "git"), []byte("#!/bin/sh\necho true\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	docker := `#!/bin/sh
case "$*" in
  "compose version --short") echo 2.0.0; exit 0 ;;
  *"config --quiet") echo invalid-compose >&2; exit 1 ;;
esac
exit 0
`
	if err := os.WriteFile(filepath.Join(binDir, "docker"), []byte(docker), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	db := openMemDB(t)
	svc, err := New(db)
	if err != nil {
		t.Fatal(err)
	}
	target, err := svc.CreateTarget(models.CreateDeployTargetRequest{
		Name:       "broken-compose",
		ProjectDir: projectDir,
		DeployKind: models.DeployKindCompose,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.requireEligible(target.ID); !errors.Is(err, ErrPreflight) {
		t.Fatalf("deploy preflight error = %v, want ErrPreflight", err)
	}
	if err := svc.requireRollbackEligible(target.ID); err != nil {
		t.Fatalf("rollback preflight rejected recovery: %v", err)
	}
}
