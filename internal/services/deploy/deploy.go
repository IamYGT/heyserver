// Package deploy provides automated deployment management driven by signed
// provider webhooks or manual triggers.
package deploy

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

// Service manages deployment targets and executes deploy runs.
type Service struct {
	db                   *sql.DB
	dataDir              string
	envDir               string
	webhookSecretDir     string
	templatesDir         string
	projectDomainRuntime ProjectDomainRuntime
	// mu prevents two concurrent deployments from racing on a shared git tree.
	mu sync.Mutex
}

// ErrInvalidTarget identifies deployment configuration rejected before it can
// reach a process boundary. API handlers map it to a client error.
var ErrInvalidTarget = errors.New("invalid deploy target")

// ErrDeployTargetChanged identifies an optimistic-concurrency conflict. API
// and CLI clients must refresh the target rather than replacing newer state.
var ErrDeployTargetChanged = errors.New("deploy target changed")

// ErrPreflight marks a deploy or rollback that was refused because current
// target readiness is not proven.
var ErrPreflight = errors.New("deploy preflight failed")

// New creates a new deploy Service and initialises the SQLite schema.
func New(db *sql.DB) (*Service, error) {
	return NewWithDataDir(db, "")
}

// NewWithDataDir creates a deploy service whose installation-owned project
// environment files live below dataDir. An empty dataDir keeps environment
// management unavailable for legacy callers and focused tests.
func NewWithDataDir(db *sql.DB, dataDir string) (*Service, error) {
	return NewWithProjectDomainRuntime(db, dataDir, NewNginxProjectDomainRuntime("", ""))
}

// NewWithProjectDomainRuntime permits focused tests and alternative native
// Nginx layouts without weakening the fixed project-domain operation contract.
func NewWithProjectDomainRuntime(db *sql.DB, dataDir string, runtime ProjectDomainRuntime) (*Service, error) {
	if runtime == nil {
		return nil, errors.New("deploy: project domain runtime is required")
	}
	s := &Service{db: db, projectDomainRuntime: runtime}
	if strings.TrimSpace(dataDir) != "" {
		if !filepath.IsAbs(dataDir) {
			return nil, errors.New("deploy: data directory must be absolute")
		}
		s.dataDir = filepath.Clean(dataDir)
		s.envDir = filepath.Join(s.dataDir, "deploy-env")
		s.webhookSecretDir = filepath.Join(s.dataDir, "deploy-webhook-secrets")
		s.templatesDir = filepath.Join(s.dataDir, "deploy-templates")
	}
	if err := s.migrate(); err != nil {
		return nil, fmt.Errorf("deploy: migrate: %w", err)
	}
	if err := s.migrateWebhookSecrets(); err != nil {
		return nil, fmt.Errorf("deploy: migrate webhook secrets: %w", err)
	}
	return s, nil
}

// ---------------------------------------------------------------------------
// Schema migration
// ---------------------------------------------------------------------------

func (s *Service) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS deploy_targets (
			id             INTEGER PRIMARY KEY AUTOINCREMENT,
			name           TEXT    NOT NULL,
			repo_url        TEXT    NOT NULL DEFAULT '',
			branch         TEXT    NOT NULL DEFAULT 'main',
			project_dir    TEXT    NOT NULL,
			environment    TEXT    NOT NULL DEFAULT 'production',
			source_target_id INTEGER REFERENCES deploy_targets(id) ON DELETE RESTRICT,
			deployment_kind TEXT   NOT NULL DEFAULT 'script',
			compose_file    TEXT    NOT NULL DEFAULT '',
			deploy_script  TEXT    NOT NULL DEFAULT '',
			webhook_provider TEXT  NOT NULL DEFAULT 'github',
			webhook_token  TEXT    NOT NULL DEFAULT '',
			auto_deploy    INTEGER NOT NULL DEFAULT 1,
			is_active      INTEGER NOT NULL DEFAULT 1,
			created_at     DATETIME NOT NULL DEFAULT (datetime('now')),
			updated_at     DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
		`CREATE TABLE IF NOT EXISTS deploy_runs (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			target_id    INTEGER NOT NULL REFERENCES deploy_targets(id) ON DELETE CASCADE,
			"trigger"    TEXT    NOT NULL DEFAULT 'manual',
			branch       TEXT    NOT NULL DEFAULT '',
			"commit"     TEXT    NOT NULL DEFAULT '',
			prev_commit  TEXT    NOT NULL DEFAULT '',
			status       TEXT    NOT NULL DEFAULT 'pending',
			logs         TEXT    NOT NULL DEFAULT '',
			started_at   DATETIME NOT NULL DEFAULT (datetime('now')),
			finished_at  DATETIME,
			duration_ms  INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS deploy_webhook_deliveries (
			target_id    INTEGER NOT NULL REFERENCES deploy_targets(id) ON DELETE CASCADE,
			provider     TEXT    NOT NULL,
			delivery_id  TEXT    NOT NULL,
			received_at  DATETIME NOT NULL DEFAULT (datetime('now')),
			PRIMARY KEY (target_id, provider, delivery_id)
		)`,
		`CREATE TABLE IF NOT EXISTS deploy_domains (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			target_id  INTEGER NOT NULL REFERENCES deploy_targets(id) ON DELETE CASCADE,
			domain     TEXT    NOT NULL UNIQUE,
			service    TEXT    NOT NULL,
			host_port  INTEGER NOT NULL,
			tls_enabled INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL DEFAULT (datetime('now')),
			updated_at DATETIME NOT NULL DEFAULT (datetime('now'))
		)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.Exec(stmt); err != nil {
			return err
		}
	}
	// Existing installations predate deployment_kind and compose_file. Add the
	// columns in place with defaults that preserve every current script target.
	for _, column := range []struct {
		name       string
		definition string
	}{
		{name: "deployment_kind", definition: "TEXT NOT NULL DEFAULT 'script'"},
		{name: "compose_file", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "webhook_provider", definition: "TEXT NOT NULL DEFAULT 'github'"},
		{name: "environment", definition: "TEXT NOT NULL DEFAULT 'production'"},
		{name: "source_target_id", definition: "INTEGER REFERENCES deploy_targets(id) ON DELETE RESTRICT"},
	} {
		exists, err := deployTargetColumnExists(s.db, column.name)
		if err != nil {
			return err
		}
		if !exists {
			if _, err := s.db.Exec("ALTER TABLE deploy_targets ADD COLUMN " + column.name + " " + column.definition); err != nil {
				return err
			}
		}
	}
	exists, err := deployDomainColumnExists(s.db, "tls_enabled")
	if err != nil {
		return err
	}
	if !exists {
		if _, err := s.db.Exec("ALTER TABLE deploy_domains ADD COLUMN tls_enabled INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_deploy_targets_source_target ON deploy_targets(source_target_id)`); err != nil {
		return err
	}
	return nil
}

func deployTargetColumnExists(db *sql.DB, column string) (bool, error) {
	var found int
	err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('deploy_targets') WHERE name = ?`, column).Scan(&found)
	return found == 1, err
}

func deployDomainColumnExists(db *sql.DB, column string) (bool, error) {
	var found int
	err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('deploy_domains') WHERE name = ?`, column).Scan(&found)
	return found == 1, err
}

// ---------------------------------------------------------------------------
// Target CRUD
// ---------------------------------------------------------------------------

// ListTargets returns all registered deployment targets ordered by id.
func (s *Service) ListTargets() ([]models.DeployTarget, error) {
	rows, err := s.db.Query(`
		SELECT id, name, repo_url, branch, project_dir, environment, source_target_id, deployment_kind,
		       compose_file, deploy_script,
		       webhook_provider, webhook_token, auto_deploy, is_active, created_at, updated_at
		FROM deploy_targets ORDER BY id ASC
	`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var targets []models.DeployTarget
	for rows.Next() {
		var t models.DeployTarget
		var autoDeploy, isActive int
		var sourceTargetID sql.NullInt64
		if err := rows.Scan(
			&t.ID, &t.Name, &t.RepoURL, &t.Branch, &t.ProjectDir,
			&t.Environment, &sourceTargetID,
			&t.DeployKind, &t.ComposeFile, &t.DeployScript,
			&t.WebhookProvider, &t.WebhookToken, &autoDeploy, &isActive,
			&t.CreatedAt, &t.UpdatedAt,
		); err != nil {
			return nil, err
		}
		t.AutoDeploy = autoDeploy == 1
		t.IsActive = isActive == 1
		if sourceTargetID.Valid {
			sourceID := sourceTargetID.Int64
			t.SourceTargetID = &sourceID
		}
		_, t.WebhookStatus = s.resolveWebhookSecret(t.ID, t.WebhookToken)
		// Inventory never exposes a secret value or its installation-owned file
		// reference. GetTarget resolves the secret only for server-side flows.
		t.WebhookToken = ""
		targets = append(targets, t)
	}
	return targets, rows.Err()
}

// GetTarget returns a single deployment target by ID, or nil if not found.
func (s *Service) GetTarget(id int64) (*models.DeployTarget, error) {
	var t models.DeployTarget
	var autoDeploy, isActive int
	var sourceTargetID sql.NullInt64
	err := s.db.QueryRow(`
		SELECT id, name, repo_url, branch, project_dir, environment, source_target_id, deployment_kind,
		       compose_file, deploy_script,
		       webhook_provider, webhook_token, auto_deploy, is_active, created_at, updated_at
		FROM deploy_targets WHERE id = ?
	`, id).Scan(
		&t.ID, &t.Name, &t.RepoURL, &t.Branch, &t.ProjectDir,
		&t.Environment, &sourceTargetID,
		&t.DeployKind, &t.ComposeFile, &t.DeployScript,
		&t.WebhookProvider, &t.WebhookToken, &autoDeploy, &isActive,
		&t.CreatedAt, &t.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t.AutoDeploy = autoDeploy == 1
	t.IsActive = isActive == 1
	if sourceTargetID.Valid {
		sourceID := sourceTargetID.Int64
		t.SourceTargetID = &sourceID
	}
	t.WebhookToken, t.WebhookStatus = s.resolveWebhookSecret(t.ID, t.WebhookToken)
	return &t, nil
}

// CreateTarget inserts a new deployment target and returns the persisted record.
func (s *Service) CreateTarget(req models.CreateDeployTargetRequest) (*models.DeployTarget, error) {
	if err := normalizeCreateTargetRequest(&req); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.validateProductionProjectDir(0, req.ProjectDir); err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	now := time.Now()
	storedToken := req.WebhookToken
	if s.webhookSecretDir != "" {
		storedToken = ""
	}
	res, err := tx.Exec(`
		INSERT INTO deploy_targets
			(name, repo_url, branch, project_dir, deployment_kind, compose_file,
			 deploy_script, webhook_provider, webhook_token, auto_deploy, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?, ?)
	`, req.Name, req.RepoURL, req.Branch, req.ProjectDir, req.DeployKind,
		req.ComposeFile, req.DeployScript,
		req.WebhookProvider, storedToken, boolToInt(req.AutoDeploy), now, now)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	secretWritten := false
	if s.webhookSecretDir != "" && req.WebhookToken != "" {
		if err := s.writeWebhookSecret(id, req.WebhookToken); err != nil {
			_ = tx.Rollback()
			return nil, err
		}
		secretWritten = true
		if _, err := tx.Exec(`UPDATE deploy_targets SET webhook_token = ? WHERE id = ?`, s.webhookSecretReference(id), id); err != nil {
			_ = tx.Rollback()
			_ = os.Remove(s.webhookSecretPath(id))
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		if secretWritten {
			_ = os.Remove(s.webhookSecretPath(id))
		}
		return nil, err
	}
	return s.GetTarget(id)
}

// UpdateTarget replaces all mutable fields of an existing deployment target.
func (s *Service) UpdateTarget(id int64, req models.UpdateDeployTargetRequest) (*models.DeployTarget, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, err := s.GetTarget(id)
	if err != nil {
		return nil, err
	}
	if existing == nil {
		return nil, nil
	}
	if req.ExpectedUpdatedAt.IsZero() {
		return nil, fmt.Errorf("%w: expectedUpdatedAt is required", ErrInvalidTarget)
	}
	if !existing.UpdatedAt.Equal(req.ExpectedUpdatedAt) {
		return nil, fmt.Errorf("%w: refresh the target before updating it", ErrDeployTargetChanged)
	}
	if req.WebhookProvider == "" {
		req.WebhookProvider = existing.WebhookProvider
	}
	if err := normalizeUpdateTargetRequest(&req, existing); err != nil {
		return nil, err
	}
	if existing.Environment == models.DeployEnvironmentStaging {
		if err := s.validateIsolatedProjectDir(id, req.ProjectDir); err != nil {
			return nil, err
		}
	} else if err := s.validateProductionProjectDir(id, req.ProjectDir); err != nil {
		return nil, err
	}
	preserveWebhookToken := req.WebhookToken == "" && !req.ClearWebhookToken
	storedToken := req.WebhookToken
	if s.webhookSecretDir != "" && !preserveWebhookToken {
		storedToken = ""
		if req.WebhookToken != "" {
			if err := s.writeWebhookSecret(id, req.WebhookToken); err != nil {
				return nil, err
			}
			storedToken = s.webhookSecretReference(id)
		}
	}
	_, err = s.db.Exec(`
		UPDATE deploy_targets SET
			name          = ?,
			repo_url       = ?,
			branch         = ?,
			project_dir   = ?,
			deployment_kind = ?,
			compose_file   = ?,
			deploy_script = ?,
			webhook_provider = ?,
			webhook_token = CASE WHEN ? = 1 THEN webhook_token ELSE ? END,
			auto_deploy   = ?,
			is_active     = ?,
			updated_at    = ?
		WHERE id = ?
	`, req.Name, req.RepoURL, req.Branch, req.ProjectDir, req.DeployKind,
		req.ComposeFile, req.DeployScript,
		req.WebhookProvider, boolToInt(preserveWebhookToken), storedToken, boolToInt(req.AutoDeploy), boolToInt(req.IsActive),
		time.Now(), id)
	if err != nil {
		if s.webhookSecretDir != "" && !preserveWebhookToken {
			if existing.WebhookToken == "" {
				_ = os.Remove(s.webhookSecretPath(id))
			} else {
				_ = s.writeWebhookSecret(id, existing.WebhookToken)
			}
		}
		return nil, err
	}
	if s.webhookSecretDir != "" && req.ClearWebhookToken {
		if err := os.Remove(s.webhookSecretPath(id)); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove deploy webhook secret: %w", err)
		}
	}
	return s.GetTarget(id)
}

func normalizeCreateTargetRequest(req *models.CreateDeployTargetRequest) error {
	req.Name = strings.TrimSpace(req.Name)
	req.RepoURL = strings.TrimSpace(req.RepoURL)
	req.Branch = strings.TrimSpace(req.Branch)
	req.ProjectDir = strings.TrimSpace(req.ProjectDir)
	req.ComposeFile = strings.TrimSpace(req.ComposeFile)
	if req.Branch == "" {
		req.Branch = "main"
	}
	if req.DeployKind == "" {
		req.DeployKind = models.DeployKindScript
	}
	if req.WebhookProvider == "" {
		req.WebhookProvider = models.DeployWebhookGitHub
	}
	return validateDeployTarget(req.Name, req.RepoURL, req.Branch, req.ProjectDir, req.DeployKind, req.ComposeFile, req.DeployScript, req.WebhookProvider, req.WebhookToken, req.AutoDeploy)
}

func normalizeUpdateTargetRequest(req *models.UpdateDeployTargetRequest, existing *models.DeployTarget) error {
	req.Name = strings.TrimSpace(req.Name)
	req.RepoURL = strings.TrimSpace(req.RepoURL)
	req.Branch = strings.TrimSpace(req.Branch)
	req.ProjectDir = strings.TrimSpace(req.ProjectDir)
	req.ComposeFile = strings.TrimSpace(req.ComposeFile)
	if req.Branch == "" {
		req.Branch = "main"
	}
	if req.DeployKind == "" {
		req.DeployKind = models.DeployKindScript
	}
	if req.WebhookProvider == "" {
		req.WebhookProvider = models.DeployWebhookGitHub
	}
	if req.ClearWebhookToken && req.WebhookToken != "" {
		return fmt.Errorf("%w: webhookToken and clearWebhookToken cannot be used together", ErrInvalidTarget)
	}
	effectiveWebhookToken := req.WebhookToken
	effectiveAutoDeploy := req.AutoDeploy
	if req.WebhookToken == "" && !req.ClearWebhookToken && existing != nil {
		effectiveWebhookToken = existing.WebhookToken
		if existing.WebhookStatus == models.DeployWebhookUnavailable {
			if req.WebhookProvider != existing.WebhookProvider {
				return fmt.Errorf("%w: replace the unavailable webhook token before changing provider", ErrInvalidTarget)
			}
			// The protected secret reference remains configured even though its
			// current file cannot be read. Preserve that honest unavailable state
			// without pretending the secret is absent or replacing it silently.
			effectiveAutoDeploy = false
		}
	}
	return validateDeployTarget(req.Name, req.RepoURL, req.Branch, req.ProjectDir, req.DeployKind, req.ComposeFile, req.DeployScript, req.WebhookProvider, effectiveWebhookToken, effectiveAutoDeploy)
}

func validateDeployTarget(name, repoURL, branch, projectDir string, kind models.DeployKind, composeFile, deployScript string, webhookProvider models.DeployWebhookProvider, webhookToken string, autoDeploy bool) error {
	if name == "" || projectDir == "" {
		return fmt.Errorf("%w: name and project directory are required", ErrInvalidTarget)
	}
	if len(name) > 128 || strings.ContainsAny(name, "\r\n\x00") {
		return fmt.Errorf("%w: name must be one line of at most 128 bytes", ErrInvalidTarget)
	}
	if !filepath.IsAbs(projectDir) {
		return fmt.Errorf("%w: project directory must be absolute", ErrInvalidTarget)
	}
	if err := validateRepositoryURL(repoURL); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTarget, err)
	}
	if !validGitBranch(branch) {
		return fmt.Errorf("%w: branch is invalid", ErrInvalidTarget)
	}
	switch kind {
	case models.DeployKindScript:
		if composeFile != "" {
			return fmt.Errorf("%w: compose file is only valid for compose targets", ErrInvalidTarget)
		}
		if err := validateDeployScript(deployScript); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalidTarget, err)
		}
	case models.DeployKindCompose:
		if strings.TrimSpace(deployScript) != "" {
			return fmt.Errorf("%w: compose targets use fixed commands and cannot define a deploy script", ErrInvalidTarget)
		}
		if composeFile != "" && !validComposeFile(composeFile) {
			return fmt.Errorf("%w: compose file must be a relative path inside the project directory", ErrInvalidTarget)
		}
	default:
		return fmt.Errorf("%w: deployment kind must be script or compose", ErrInvalidTarget)
	}
	if webhookProvider != models.DeployWebhookGitHub && webhookProvider != models.DeployWebhookGitLab {
		return fmt.Errorf("%w: webhook provider must be github or gitlab", ErrInvalidTarget)
	}
	if len(webhookToken) > 4096 || strings.ContainsAny(webhookToken, "\r\n\x00") {
		return fmt.Errorf("%w: webhook token is invalid", ErrInvalidTarget)
	}
	if webhookProvider == models.DeployWebhookGitLab && webhookToken != "" {
		if _, err := decodeGitLabSigningSecret(webhookToken); err != nil {
			return fmt.Errorf("%w: GitLab webhook token must be a valid whsec_ signing token", ErrInvalidTarget)
		}
	}
	if autoDeploy && strings.TrimSpace(webhookToken) == "" {
		return fmt.Errorf("%w: automatic deployment requires a webhook token", ErrInvalidTarget)
	}
	return nil
}

func validateRepositoryURL(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 2048 || strings.HasPrefix(value, "-") || strings.IndexFunc(value, func(r rune) bool {
		return r <= ' ' || r == 0x7f
	}) >= 0 {
		return errors.New("repository URL is invalid")
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" || parsed.Path == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("repository URL is invalid")
		}
		switch parsed.Scheme {
		case "https":
			if parsed.User != nil {
				return errors.New("repository URL must not contain credentials")
			}
		case "ssh":
			if parsed.User != nil {
				if _, hasPassword := parsed.User.Password(); hasPassword {
					return errors.New("repository URL must not contain credentials")
				}
			}
		default:
			return errors.New("repository URL must use HTTPS or SSH")
		}
		return nil
	}

	// SCP-like SSH form, for example git@example.com:team/project.git.
	at := strings.IndexByte(value, '@')
	colon := strings.IndexByte(value, ':')
	if at <= 0 || colon <= at+1 || colon == len(value)-1 || strings.Contains(value[colon+1:], "\\") {
		return errors.New("repository URL must use HTTPS or SSH")
	}
	return nil
}

func validGitBranch(value string) bool {
	if value == "" || len(value) > 255 || strings.HasPrefix(value, "-") || strings.HasPrefix(value, ".") ||
		strings.HasSuffix(value, ".") || strings.HasSuffix(value, "/") || strings.Contains(value, "..") ||
		strings.Contains(value, "//") || strings.Contains(value, "@{") {
		return false
	}
	return !strings.ContainsAny(value, " ~^:?*[\\\x00\x7f")
}

func validComposeFile(value string) bool {
	if filepath.IsAbs(value) || strings.ContainsRune(value, '\x00') {
		return false
	}
	clean := filepath.Clean(value)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

// DeleteTarget removes a deployment target and all associated run history.
func (s *Service) DeleteTarget(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	stagingChildren, err := s.stagingChildrenCount(id)
	if err != nil {
		return err
	}
	if stagingChildren > 0 {
		return ErrStagingTargetsAttached
	}
	var domainCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM deploy_domains WHERE target_id = ?`, id).Scan(&domainCount); err != nil {
		return err
	}
	if domainCount > 0 {
		return ErrProjectDomainsAttached
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM deploy_webhook_deliveries WHERE target_id = ?`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM deploy_targets WHERE id = ?`, id); err != nil {
		_ = tx.Rollback()
		return err
	}
	type stagedRemoval struct {
		path      string
		tombstone string
		label     string
	}
	var staged []stagedRemoval
	stage := func(path, label string) error {
		tombstone := path + fmt.Sprintf(".delete-%d", time.Now().UnixNano())
		if err := os.Rename(path, tombstone); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("stage %s removal: %w", label, err)
		}
		staged = append(staged, stagedRemoval{path: path, tombstone: tombstone, label: label})
		return nil
	}
	if s.envDir != "" {
		if err := stage(s.environmentPath(id), "deploy environment"); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if s.webhookSecretDir != "" {
		if err := stage(s.webhookSecretPath(id), "deploy webhook secret"); err != nil {
			_ = tx.Rollback()
			for index := len(staged) - 1; index >= 0; index-- {
				_ = os.Rename(staged[index].tombstone, staged[index].path)
			}
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		for index := len(staged) - 1; index >= 0; index-- {
			_ = os.Rename(staged[index].tombstone, staged[index].path)
		}
		return err
	}
	for _, item := range staged {
		if err := os.Remove(item.tombstone); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", item.label, err)
		}
	}
	return nil
}

// ---------------------------------------------------------------------------
// Webhook signature verification
// ---------------------------------------------------------------------------

// VerifySignature validates the X-Hub-Signature-256 header produced by GitHub.
//
// GitHub computes HMAC-SHA256 of the raw request body using the webhook secret
// and sends the result as "sha256=<lowercase-hex>".  We replicate that
// computation and compare using hmac.Equal (constant-time) to prevent
// timing-oracle attacks — never use == for secret comparison.
func VerifySignature(secret, signature string, body []byte) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	sigHex := strings.TrimPrefix(signature, "sha256=")
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)

	// constant-time comparison — prevents timing side-channel
	return hmac.Equal(expected, sigBytes)
}

// ---------------------------------------------------------------------------
// Deployment execution
// ---------------------------------------------------------------------------

// TriggerDeploy starts a deployment for the given target asynchronously.
// It inserts a pending DeployRun immediately and returns it; actual execution
// happens in a background goroutine that updates status/logs as it progresses.
func (s *Service) TriggerDeploy(targetID int64, trigger string) (*models.DeployRun, error) {
	target, err := s.GetTarget(targetID)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, fmt.Errorf("deploy: target %d not found", targetID)
	}
	if !target.IsActive {
		return nil, fmt.Errorf("deploy: target %d is disabled", targetID)
	}
	if err := s.requireEligible(targetID); err != nil {
		return nil, err
	}

	now := time.Now()
	res, err := s.db.Exec(`
		INSERT INTO deploy_runs (target_id, "trigger", branch, status, started_at)
		VALUES (?, ?, ?, ?, ?)
	`, targetID, trigger, target.Branch, models.DeployStatusPending, now)
	if err != nil {
		return nil, err
	}
	runID, _ := res.LastInsertId()

	run := &models.DeployRun{
		ID:        runID,
		TargetID:  targetID,
		Trigger:   trigger,
		Branch:    target.Branch,
		Status:    models.DeployStatusPending,
		StartedAt: now,
	}

	go s.executeRun(run, target)
	return run, nil
}

// executeRun performs the git pull + deploy script steps in the background.
// All output (stdout + stderr) is accumulated and persisted to deploy_runs.logs.
func (s *Service) executeRun(run *models.DeployRun, target *models.DeployTarget) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var logBuf strings.Builder
	start := time.Now()

	logf := func(format string, args ...any) {
		line := fmt.Sprintf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
		logBuf.WriteString(line)
		slog.Info("deploy", "run_id", run.ID, "target", target.Name, "msg", strings.TrimSpace(line))
	}

	s.setRunStatus(run.ID, models.DeployStatusRunning, logBuf.String())

	// Step 1: use an existing checkout or provision an absent/empty target from
	// its validated repository URL. Clone arguments are fixed by Heyserver.
	logf("==> Preparing Git checkout")
	provisioned, checkoutOut, checkoutErr := ensureDeployCheckout(target)
	logBuf.WriteString(checkoutOut)
	if checkoutErr != nil {
		logf("ERROR: Git checkout preparation failed: %v", checkoutErr)
		s.finaliseRun(run.ID, models.DeployStatusFailed, logBuf.String(), "", "", start)
		return
	}

	var prevCommit string
	if provisioned {
		logf("Repository cloned for first deployment")
	} else {
		// Step 2: capture the current HEAD so we can roll back if needed.
		logf("==> Capturing current HEAD for rollback reference")
		parsedCommit, err := gitRevParse(target.ProjectDir, "HEAD")
		if err != nil {
			logf("WARNING: could not read HEAD (%v) — rollback reference unavailable", err)
			prevCommit = ""
		} else {
			prevCommit = parsedCommit
			logf("Previous HEAD: %s", prevCommit)
		}

		// Step 3: update the existing checkout. A freshly cloned repository is
		// already at the configured remote branch and does not need a second pull.
		logf("==> git pull origin %s", target.Branch)
		pullOut, pullErr := runCmd(target.ProjectDir, "git", "pull", "--ff-only", "origin", target.Branch)
		logBuf.WriteString(pullOut)
		if pullErr != nil {
			logf("ERROR: git pull failed: %v", pullErr)
			s.finaliseRun(run.ID, models.DeployStatusFailed, logBuf.String(), prevCommit, "", start)
			return
		}
	}

	// Step 4: capture the deployed HEAD.
	newCommit, err := gitRevParse(target.ProjectDir, "HEAD")
	if err != nil {
		logf("WARNING: could not read new HEAD: %v", err)
		newCommit = ""
	} else {
		logf("New HEAD: %s", newCommit)
	}

	// Step 5: run the configured executor. Compose targets use only fixed
	// arguments assembled below; script targets retain the existing workflow.
	logf("==> Running %s deployment executor", target.DeployKind)
	executorOut, executorErr := s.runDeploymentExecutor(target)
	logBuf.WriteString(executorOut)
	if executorErr != nil {
		logf("ERROR: deployment executor failed: %v", executorErr)
		s.finaliseRun(run.ID, models.DeployStatusFailed, logBuf.String(), prevCommit, newCommit, start)
		return
	}

	logf("==> Deployment completed successfully")
	s.finaliseRun(run.ID, models.DeployStatusSuccess, logBuf.String(), prevCommit, newCommit, start)
}

// ---------------------------------------------------------------------------
// Rollback
// ---------------------------------------------------------------------------

// Rollback checks out the previous commit of the latest successful run for a
// target, re-runs the deploy script, and records the operation as a new run.
func (s *Service) Rollback(targetID int64) (*models.DeployRun, error) {
	target, err := s.GetTarget(targetID)
	if err != nil || target == nil {
		return nil, fmt.Errorf("deploy: target %d not found", targetID)
	}
	if err := s.requireRollbackEligible(targetID); err != nil {
		return nil, err
	}

	// Find the most recent successful run that has a prev_commit recorded.
	var prevCommit string
	err = s.db.QueryRow(`
		SELECT prev_commit FROM deploy_runs
		WHERE target_id = ? AND status = 'success' AND prev_commit != ''
		ORDER BY id DESC LIMIT 1
	`, targetID).Scan(&prevCommit)
	if err == sql.ErrNoRows || prevCommit == "" {
		return nil, fmt.Errorf("deploy: no rollback commit available for target %d", targetID)
	}
	if err != nil {
		return nil, err
	}

	now := time.Now()
	res, err := s.db.Exec(`
		INSERT INTO deploy_runs (target_id, "trigger", branch, "commit", status, started_at)
		VALUES (?, 'rollback', ?, ?, ?, ?)
	`, targetID, target.Branch, prevCommit, models.DeployStatusPending, now)
	if err != nil {
		return nil, err
	}
	runID, _ := res.LastInsertId()

	run := &models.DeployRun{
		ID:        runID,
		TargetID:  targetID,
		Trigger:   "rollback",
		Branch:    target.Branch,
		Commit:    prevCommit,
		Status:    models.DeployStatusPending,
		StartedAt: now,
	}

	go s.executeRollback(run, target, prevCommit)
	return run, nil
}

func (s *Service) executeRollback(run *models.DeployRun, target *models.DeployTarget, toCommit string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var logBuf strings.Builder
	start := time.Now()

	logf := func(format string, args ...any) {
		line := fmt.Sprintf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(format, args...))
		logBuf.WriteString(line)
	}

	s.setRunStatus(run.ID, models.DeployStatusRunning, logBuf.String())

	logf("==> Rollback: git checkout %s", toCommit)
	out, err := runCmd(target.ProjectDir, "git", "checkout", toCommit)
	logBuf.WriteString(out)
	if err != nil {
		logf("ERROR: git checkout failed: %v", err)
		s.finaliseRun(run.ID, models.DeployStatusFailed, logBuf.String(), "", toCommit, start)
		return
	}

	logf("==> Running %s deployment executor after rollback", target.DeployKind)
	executorOut, executorErr := s.runDeploymentExecutor(target)
	logBuf.WriteString(executorOut)
	if executorErr != nil {
		logf("ERROR: deployment executor failed: %v", executorErr)
		s.finaliseRun(run.ID, models.DeployStatusFailed, logBuf.String(), "", toCommit, start)
		return
	}

	logf("==> Rollback completed successfully")
	s.finaliseRun(run.ID, models.DeployStatusSuccess, logBuf.String(), "", toCommit, start)
}

// Preflight performs read-only target and executor checks. It deliberately
// returns an eligibility report instead of treating an unavailable optional
// dependency such as Docker Compose as an internal API failure.
func (s *Service) Preflight(targetID int64) (*models.DeployPreflight, error) {
	target, err := s.GetTarget(targetID)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, fmt.Errorf("deploy: target %d not found", targetID)
	}

	report := &models.DeployPreflight{
		TargetID:       target.ID,
		DeploymentKind: target.DeployKind,
		Eligible:       true,
		Checks:         []models.DeployPreflightCheck{},
	}
	add := func(id, status, message string) {
		if status == "fail" {
			report.Eligible = false
		}
		report.Checks = append(report.Checks, models.DeployPreflightCheck{ID: id, Status: status, Message: message})
	}
	addBool := func(id string, ok bool, success, failure string) {
		if ok {
			add(id, "pass", success)
		} else {
			add(id, "fail", failure)
		}
	}

	addBool("active", target.IsActive, "Target is enabled", "Target is disabled")
	_, gitLookErr := exec.LookPath("git")
	gitAvailable := gitLookErr == nil
	addBool("git-client", gitAvailable, "Git client is available", "Git client is unavailable")

	info, statErr := os.Stat(target.ProjectDir)
	directoryReady := statErr == nil && info.IsDir()
	checkoutReady := false
	provisionPending := false
	commandDir := target.ProjectDir
	repoReady := target.RepoURL != "" && validateRepositoryURL(target.RepoURL) == nil

	switch {
	case directoryReady:
		add("project-directory", "pass", "Project directory is available")
		_, gitErr := gitRevParse(target.ProjectDir, "--is-inside-work-tree")
		checkoutReady = gitErr == nil
		if checkoutReady {
			add("git-checkout", "pass", "Git checkout is readable")
		} else {
			entries, readErr := os.ReadDir(target.ProjectDir)
			if readErr == nil && len(entries) == 0 && repoReady && gitAvailable {
				provisionPending = true
				add("git-checkout", "pending", "Empty project directory will be provisioned on first deployment")
			} else {
				add("git-checkout", "fail", "Project directory is not an empty or readable Git checkout")
			}
		}
	case os.IsNotExist(statErr):
		parent, parentErr := nearestExistingDirectory(filepath.Dir(target.ProjectDir))
		if parentErr == nil {
			commandDir = parent
		}
		if parentErr == nil && repoReady && gitAvailable {
			provisionPending = true
			add("project-directory", "pending", "Project directory will be created on first deployment")
			add("git-checkout", "pending", "Repository will be cloned on first deployment")
		} else {
			add("project-directory", "fail", "Project directory cannot be provisioned from the current configuration")
			add("git-checkout", "fail", "Repository URL and a reachable parent directory are required for first deployment")
		}
	default:
		add("project-directory", "fail", "Project path is unavailable or is not a directory")
		add("git-checkout", "fail", "Git checkout is unavailable")
	}

	switch target.DeployKind {
	case models.DeployKindCompose:
		dockerReady := false
		composeReady := false
		composeMessage := "Docker Compose configuration could not be inspected"
		if commandDir != "" {
			if _, lookErr := exec.LookPath("docker"); lookErr == nil {
				versionOut, versionErr := runCmdTimeout(commandDir, 30*time.Second, "docker", "compose", "version", "--short")
				dockerReady = versionErr == nil
				if dockerReady && checkoutReady {
					args, argsErr := s.composeTargetCommandArgs(target, "config", "--quiet")
					configOut := ""
					var configErr error
					if argsErr == nil {
						configOut, configErr = runCmdTimeout(target.ProjectDir, 30*time.Second, "docker", args...)
					} else {
						configErr = argsErr
					}
					composeReady = configErr == nil
					if composeReady {
						composeMessage = "Docker Compose configuration is valid"
					} else if argsErr != nil {
						composeMessage = "Docker Compose environment is unavailable: " + argsErr.Error()
					} else if detail := boundedProcessDetail(configOut); detail != "" {
						composeMessage = "Docker Compose configuration is invalid: " + detail
					}
				} else if detail := boundedProcessDetail(versionOut); detail != "" {
					composeMessage = "Docker Compose is unavailable: " + detail
				}
			}
		}
		addBool("docker-compose", dockerReady, "Docker Compose is available", "Docker Compose is unavailable")
		if provisionPending && dockerReady {
			add("compose-config", "pending", "Compose configuration will be validated after the repository is cloned")
		} else {
			addBool("compose-config", composeReady, "Docker Compose configuration is valid", composeMessage)
		}
	case models.DeployKindScript:
		scriptErr := validateDeployScript(target.DeployScript)
		addBool("deploy-script", scriptErr == nil, "Deploy script passed validation", "Deploy script failed validation")
	default:
		add("deployment-kind", "fail", "Deployment kind is unsupported")
	}

	return report, nil
}

func (s *Service) requireEligible(targetID int64) error {
	return s.requirePreflight(targetID, nil)
}

// Rollback is a recovery path: an invalid Compose file in the current commit
// must not prevent checkout of a previously working commit. Host, Git, and
// executor availability checks still have to pass.
func (s *Service) requireRollbackEligible(targetID int64) error {
	return s.requirePreflight(targetID, map[string]struct{}{"compose-config": {}})
}

func (s *Service) requirePreflight(targetID int64, ignoredFailures map[string]struct{}) error {
	report, err := s.Preflight(targetID)
	if err != nil {
		return err
	}
	failed := make([]string, 0, len(report.Checks))
	for _, check := range report.Checks {
		if check.Status == "fail" {
			if _, ignored := ignoredFailures[check.ID]; ignored {
				continue
			}
			failed = append(failed, check.ID)
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return fmt.Errorf("%w: %s", ErrPreflight, strings.Join(failed, ", "))
}

func runDeploymentExecutor(target *models.DeployTarget) (string, error) {
	return runDeploymentExecutorWithEnvironment(target, "")
}

func (s *Service) runDeploymentExecutor(target *models.DeployTarget) (string, error) {
	environmentFile, err := s.environmentFile(target.ID)
	if err != nil {
		return "", err
	}
	return runDeploymentExecutorWithEnvironment(target, environmentFile)
}

func runDeploymentExecutorWithEnvironment(target *models.DeployTarget, environmentFile string) (string, error) {
	switch target.DeployKind {
	case "", models.DeployKindScript:
		if strings.TrimSpace(target.DeployScript) == "" {
			return "(no deploy script configured — skipping)\n", nil
		}
		return runShellScript(target.ProjectDir, target.DeployScript)
	case models.DeployKindCompose:
		if !validComposeFileOrEmpty(target.ComposeFile) {
			return "", fmt.Errorf("compose file is outside the project directory")
		}
		var output strings.Builder
		configArgs := composeCommandArgsWithEnvironment(target.ComposeFile, environmentFile, "config", "--quiet")
		configOut, err := runCmd(target.ProjectDir, "docker", configArgs...)
		output.WriteString(configOut)
		if err != nil {
			return output.String(), fmt.Errorf("docker compose config failed: %w", err)
		}
		upArgs := composeCommandArgsWithEnvironment(target.ComposeFile, environmentFile, "up", "-d", "--build", "--remove-orphans")
		upOut, err := runCmd(target.ProjectDir, "docker", upArgs...)
		output.WriteString(upOut)
		if err != nil {
			return output.String(), fmt.Errorf("docker compose up failed: %w", err)
		}
		return output.String(), nil
	default:
		return "", fmt.Errorf("unsupported deployment kind %q", target.DeployKind)
	}
}

func composeCommandArgs(composeFile string, command ...string) []string {
	return composeCommandArgsWithEnvironment(composeFile, "", command...)
}

func composeCommandArgsWithEnvironment(composeFile, environmentFile string, command ...string) []string {
	args := []string{"compose"}
	if environmentFile != "" {
		args = append(args, "--env-file", environmentFile)
	}
	if composeFile != "" {
		args = append(args, "-f", composeFile)
	}
	return append(args, command...)
}

func (s *Service) composeTargetCommandArgs(target *models.DeployTarget, command ...string) ([]string, error) {
	environmentFile, err := s.environmentFile(target.ID)
	if err != nil {
		return nil, err
	}
	return composeCommandArgsWithEnvironment(target.ComposeFile, environmentFile, command...), nil
}

func validComposeFileOrEmpty(value string) bool {
	return value == "" || validComposeFile(value)
}

func boundedProcessDetail(output string) string {
	detail := strings.Join(strings.Fields(output), " ")
	if len(detail) > 240 {
		return detail[:240] + "…"
	}
	return detail
}

// ---------------------------------------------------------------------------
// History
// ---------------------------------------------------------------------------

// ListRuns returns deployment run history.
// Pass targetID=0 to get runs for all targets.
func (s *Service) ListRuns(targetID int64, limit int) ([]models.DeployRun, error) {
	if limit <= 0 {
		limit = 50
	}

	var (
		rows *sql.Rows
		err  error
	)
	if targetID > 0 {
		rows, err = s.db.Query(`
			SELECT id, target_id, "trigger", branch, "commit", prev_commit,
			       status, started_at, finished_at, duration_ms
			FROM deploy_runs WHERE target_id = ?
			ORDER BY id DESC LIMIT ?
		`, targetID, limit)
	} else {
		rows, err = s.db.Query(`
			SELECT id, target_id, "trigger", branch, "commit", prev_commit,
			       status, started_at, finished_at, duration_ms
			FROM deploy_runs
			ORDER BY id DESC LIMIT ?
		`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var runs []models.DeployRun
	for rows.Next() {
		var r models.DeployRun
		if err := rows.Scan(
			&r.ID, &r.TargetID, &r.Trigger, &r.Branch,
			&r.Commit, &r.PrevCommit, &r.Status,
			&r.StartedAt, &r.FinishedAt, &r.DurationMs,
		); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// GetRunLogs returns the full captured log output for a single deployment run.
func (s *Service) GetRunLogs(runID int64) (string, error) {
	var logs string
	err := s.db.QueryRow(`SELECT logs FROM deploy_runs WHERE id = ?`, runID).Scan(&logs)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("deploy: run %d not found", runID)
	}
	return logs, err
}

// ---------------------------------------------------------------------------
// Internal persistence helpers
// ---------------------------------------------------------------------------

func (s *Service) setRunStatus(id int64, status models.DeployStatus, logs string) {
	_, _ = s.db.Exec(
		`UPDATE deploy_runs SET status = ?, logs = ? WHERE id = ?`,
		status, logs, id,
	)
}

func (s *Service) finaliseRun(
	id int64,
	status models.DeployStatus,
	logs, prevCommit, commit string,
	start time.Time,
) {
	now := time.Now()
	_, _ = s.db.Exec(`
		UPDATE deploy_runs SET
			status      = ?,
			logs        = ?,
			prev_commit = ?,
			"commit"    = ?,
			finished_at = ?,
			duration_ms = ?
		WHERE id = ?
	`, status, logs, prevCommit, commit, now, now.Sub(start).Milliseconds(), id)
}

// ---------------------------------------------------------------------------
// Process helpers
// ---------------------------------------------------------------------------

func ensureDeployCheckout(target *models.DeployTarget) (bool, string, error) {
	if _, err := gitRevParse(target.ProjectDir, "--is-inside-work-tree"); err == nil {
		return false, "", nil
	}
	if target.RepoURL == "" {
		return false, "", errors.New("repository URL is required to provision a missing checkout")
	}
	if err := validateRepositoryURL(target.RepoURL); err != nil || !validGitBranch(target.Branch) {
		return false, "", errors.New("repository or branch configuration is invalid")
	}

	info, statErr := os.Stat(target.ProjectDir)
	switch {
	case statErr == nil && !info.IsDir():
		return false, "", errors.New("project path exists and is not a directory")
	case statErr == nil:
		entries, err := os.ReadDir(target.ProjectDir)
		if err != nil {
			return false, "", fmt.Errorf("read project directory: %w", err)
		}
		if len(entries) != 0 {
			return false, "", errors.New("project directory is not an empty or readable Git checkout")
		}
	case !os.IsNotExist(statErr):
		return false, "", fmt.Errorf("inspect project directory: %w", statErr)
	}

	parent := filepath.Dir(filepath.Clean(target.ProjectDir))
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return false, "", fmt.Errorf("create project parent directory: %w", err)
	}
	output, err := runCmd(parent, "git", "clone", "--branch", target.Branch, "--single-branch", "--", target.RepoURL, target.ProjectDir)
	if err != nil {
		return false, output, fmt.Errorf("git clone failed: %w", err)
	}
	if _, err := gitRevParse(target.ProjectDir, "HEAD"); err != nil {
		return false, output, fmt.Errorf("cloned checkout is unreadable: %w", err)
	}
	return true, output, nil
}

func nearestExistingDirectory(start string) (string, error) {
	current := filepath.Clean(start)
	for {
		info, err := os.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return "", errors.New("nearest existing project parent is not a directory")
			}
			return current, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("no existing project parent directory")
		}
		current = parent
	}
}

// gitRevParse returns the resolved SHA for ref inside dir.
func gitRevParse(dir, ref string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "rev-parse", ref)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// runCmd executes command name with args inside dir, returning combined output.
// GIT_TERMINAL_PROMPT=0 prevents git from blocking on a password prompt.
func runCmd(dir, name string, args ...string) (string, error) {
	return runCmdTimeout(dir, 10*time.Minute, name, args...)
}

func runCmdTimeout(dir string, timeout time.Duration, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err := cmd.Run()
	return buf.String(), err
}

// validateDeployScript rejects deploy_script content that contains patterns
// commonly associated with privilege escalation or host escape attempts.
//
// This is a defence-in-depth measure — deploy targets are admin-only, but we
// still want a best-effort guard against "stored XSS"-style attacks where a
// compromised admin account stores a malicious script.
//
// The check is intentionally narrow: it blocks the most dangerous patterns
// (network exfiltration, /etc/passwd writes, reverse shells) without trying
// to emulate a full shell sandbox — that would be impossible in practice.
func validateDeployScript(script string) error {
	if len(script) > 64*1024 {
		return fmt.Errorf("deploy script too large (max 64 KiB)")
	}
	// Patterns that indicate clear privilege-escalation or exfiltration attempts.
	// Each string is a substring match (case-sensitive) to keep it simple and fast.
	dangerousPatterns := []string{
		"curl http",  // plain-HTTP exfiltration
		"wget http",  // plain-HTTP exfiltration
		"bash -i",    // interactive reverse shell
		"nc -e",      // netcat reverse shell
		"ncat -e",    // netcat reverse shell
		"/dev/tcp/",  // bash /dev/tcp reverse shell
		"/dev/udp/",  // bash /dev/udp reverse shell
		"python -c",  // python one-liner (exec/shell)
		"python3 -c", // python3 one-liner
		"perl -e",    // perl one-liner
		"ruby -e",    // ruby one-liner
		"php -r",     // php one-liner
	}
	lower := strings.ToLower(script)
	for _, pat := range dangerousPatterns {
		if strings.Contains(lower, strings.ToLower(pat)) {
			return fmt.Errorf("deploy script contains disallowed pattern: %q", pat)
		}
	}
	return nil
}

// runShellScript writes the script to a temp file and executes it with bash.
// set -e causes the script to abort on the first non-zero exit code.
// The script content is validated before execution to reject the most
// dangerous patterns (reverse shells, exfiltration).
func runShellScript(dir, script string) (string, error) {
	if err := validateDeployScript(script); err != nil {
		return "", fmt.Errorf("deploy script rejected: %w", err)
	}

	tmp, err := os.CreateTemp("", "hserver-deploy-*.sh")
	if err != nil {
		return "", fmt.Errorf("could not create temp script: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := fmt.Fprintf(tmp, "#!/bin/bash\nset -e\n%s\n", script); err != nil {
		_ = tmp.Close()
		return "", err
	}
	_ = tmp.Close()

	if err := os.Chmod(tmp.Name(), 0o700); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/bash", tmp.Name())
	cmd.Dir = filepath.Clean(dir)
	cmd.Env = append(os.Environ(), "DEBIAN_FRONTEND=noninteractive")

	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf

	err = cmd.Run()
	return buf.String(), err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
