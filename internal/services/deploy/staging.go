package deploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

var ErrStagingTargetsAttached = errors.New("remove staging targets before deleting the production deploy target")

// CreateStagingTarget derives an isolated target from one production target.
// It intentionally copies only repository and executor intent. Runtime
// environment values, webhook signing material, domains, TLS, and DNS state
// remain unconfigured for the new target.
func (s *Service) CreateStagingTarget(sourceTargetID int64, req models.CreateDeployStagingRequest) (*models.DeployStagingReceipt, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Branch = strings.TrimSpace(req.Branch)
	req.ProjectDir = strings.TrimSpace(req.ProjectDir)
	if req.ProjectDir == "" || !filepath.IsAbs(req.ProjectDir) {
		return nil, fmt.Errorf("%w: staging project directory must be absolute", ErrInvalidTarget)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	source, err := s.GetTarget(sourceTargetID)
	if err != nil {
		return nil, err
	}
	if source == nil {
		return nil, fmt.Errorf("%w: %d", ErrDeployTargetNotFound, sourceTargetID)
	}
	if source.Environment != models.DeployEnvironmentProduction || source.SourceTargetID != nil {
		return nil, fmt.Errorf("%w: staging targets must be derived from a production target", ErrInvalidTarget)
	}
	if req.Name == "" {
		req.Name = source.Name + " Staging"
	}
	if len(req.Name) > 128 || strings.ContainsAny(req.Name, "\x00\r\n") {
		return nil, fmt.Errorf("%w: staging name must be one line of at most 128 bytes", ErrInvalidTarget)
	}
	if req.Branch == "" {
		req.Branch = source.Branch
	}
	if err := validateDeployTarget(
		req.Name, source.RepoURL, req.Branch, req.ProjectDir, source.DeployKind,
		source.ComposeFile, source.DeployScript, source.WebhookProvider, "", false,
	); err != nil {
		return nil, err
	}
	if err := s.validateIsolatedProjectDir(0, req.ProjectDir); err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	result, err := s.db.Exec(`
		INSERT INTO deploy_targets
			(name, repo_url, branch, project_dir, environment, source_target_id,
			 deployment_kind, compose_file, deploy_script, webhook_provider,
			 webhook_token, auto_deploy, is_active, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'staging', ?, ?, ?, ?, ?, '', 0, 1, ?, ?)
	`, req.Name, source.RepoURL, req.Branch, filepath.Clean(req.ProjectDir), source.ID,
		source.DeployKind, source.ComposeFile, source.DeployScript, source.WebhookProvider, now, now)
	if err != nil {
		return nil, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	target, err := s.GetTarget(id)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, errors.New("created staging target could not be read")
	}
	target.WebhookToken = ""
	return &models.DeployStagingReceipt{
		Target:                  *target,
		StorageBoundary:         "isolated_project_directory",
		EnvironmentValuesCopied: false,
		WebhookSecretCopied:     false,
		DomainsCopied:           false,
		DNSConfigured:           false,
	}, nil
}

func (s *Service) validateIsolatedProjectDir(excludeTargetID int64, candidate string) error {
	resolvedCandidate, err := resolvePotentialProjectDir(candidate)
	if err != nil {
		return fmt.Errorf("%w: staging project directory cannot be resolved: %v", ErrInvalidTarget, err)
	}
	if s.dataDir != "" {
		resolvedDataDir, err := resolvePotentialProjectDir(s.dataDir)
		if err != nil {
			return fmt.Errorf("%w: Heyserver data directory cannot be resolved: %v", ErrInvalidTarget, err)
		}
		if projectDirsOverlap(resolvedCandidate, resolvedDataDir) {
			return fmt.Errorf("%w: staging project directory must not overlap the Heyserver data directory", ErrInvalidTarget)
		}
	}
	rows, err := s.db.Query(`SELECT id, project_dir FROM deploy_targets WHERE id <> ? ORDER BY id`, excludeTargetID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var projectDir string
		if err := rows.Scan(&id, &projectDir); err != nil {
			return err
		}
		resolvedExisting, err := resolvePotentialProjectDir(projectDir)
		if err != nil {
			return fmt.Errorf("%w: deployment target %d project directory cannot be resolved", ErrInvalidTarget, id)
		}
		if projectDirsOverlap(resolvedCandidate, resolvedExisting) {
			return fmt.Errorf("%w: staging project directory overlaps deployment target %d", ErrInvalidTarget, id)
		}
	}
	return rows.Err()
}

func (s *Service) validateProductionProjectDir(excludeTargetID int64, candidate string) error {
	resolvedCandidate, err := resolvePotentialProjectDir(candidate)
	if err != nil {
		return fmt.Errorf("%w: production project directory cannot be resolved: %v", ErrInvalidTarget, err)
	}
	rows, err := s.db.Query(`
		SELECT id, project_dir FROM deploy_targets
		WHERE environment = 'staging' AND id <> ? ORDER BY id
	`, excludeTargetID)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		var projectDir string
		if err := rows.Scan(&id, &projectDir); err != nil {
			return err
		}
		resolvedStaging, err := resolvePotentialProjectDir(projectDir)
		if err != nil {
			return fmt.Errorf("%w: staging target %d project directory cannot be resolved", ErrInvalidTarget, id)
		}
		if projectDirsOverlap(resolvedCandidate, resolvedStaging) {
			return fmt.Errorf("%w: production project directory overlaps staging target %d", ErrInvalidTarget, id)
		}
	}
	return rows.Err()
}

func (s *Service) stagingChildrenCount(targetID int64) (int, error) {
	var count int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM deploy_targets WHERE source_target_id = ?`, targetID).Scan(&count)
	return count, err
}

func resolvePotentialProjectDir(value string) (string, error) {
	clean := filepath.Clean(value)
	if !filepath.IsAbs(clean) {
		return "", errors.New("path is not absolute")
	}
	current := clean
	suffix := make([]string, 0, 4)
	for {
		resolved, err := filepath.EvalSymlinks(current)
		if err == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return clean, nil
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
	}
}

func projectDirsOverlap(left, right string) bool {
	return projectDirContains(left, right) || projectDirContains(right, left)
}

func projectDirContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}
