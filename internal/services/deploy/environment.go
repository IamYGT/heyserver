package deploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/IamYGT/heyserver/internal/models"
)

const maxEnvironmentValueBytes = 64 << 10

var (
	ErrEnvironmentStoreUnavailable = errors.New("deploy environment store is not configured")
	ErrInvalidEnvironmentVariable  = errors.New("invalid deploy environment variable")
	environmentKeyPattern          = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
)

// Environment returns only variable names. Stored values intentionally have
// no read API so browser state, logs, exports, and generated OpenAPI examples
// cannot become a secret retrieval channel.
func (s *Service) Environment(targetID int64) (*models.DeployEnvironment, error) {
	if _, err := s.configuredComposeTarget(targetID); err != nil {
		return nil, err
	}
	values, exists, err := s.readEnvironment(targetID)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	variables := make([]models.DeployEnvironmentVariable, 0, len(keys))
	for _, key := range keys {
		variables = append(variables, models.DeployEnvironmentVariable{Key: key})
	}
	return &models.DeployEnvironment{Configured: exists, Variables: variables}, nil
}

// SetEnvironmentVariable atomically creates or replaces one write-only value.
func (s *Service) SetEnvironmentVariable(targetID int64, key, value string) (*models.DeployEnvironment, error) {
	if _, err := s.configuredComposeTarget(targetID); err != nil {
		return nil, err
	}
	key = strings.TrimSpace(key)
	if !environmentKeyPattern.MatchString(key) {
		return nil, fmt.Errorf("%w: key", ErrInvalidEnvironmentVariable)
	}
	if len(value) > maxEnvironmentValueBytes {
		return nil, fmt.Errorf("%w: value exceeds 64 KiB", ErrInvalidEnvironmentVariable)
	}
	// Generated Compose env files use literal single-quoted values. Reject the
	// characters that would cross the one-variable-per-line storage boundary.
	if strings.ContainsAny(value, "\x00\r\n'") {
		return nil, fmt.Errorf("%w: value contains an unsupported character", ErrInvalidEnvironmentVariable)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	values, _, err := s.readEnvironment(targetID)
	if err != nil {
		return nil, err
	}
	values[key] = value
	if err := s.writeEnvironment(targetID, values); err != nil {
		return nil, err
	}
	return environmentMetadata(values, true), nil
}

// DeleteEnvironmentVariable removes one key and deletes the secret file when
// the target no longer has any project variables.
func (s *Service) DeleteEnvironmentVariable(targetID int64, key string) (*models.DeployEnvironment, error) {
	if _, err := s.configuredComposeTarget(targetID); err != nil {
		return nil, err
	}
	key = strings.TrimSpace(key)
	if !environmentKeyPattern.MatchString(key) {
		return nil, fmt.Errorf("%w: key", ErrInvalidEnvironmentVariable)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	values, exists, err := s.readEnvironment(targetID)
	if err != nil {
		return nil, err
	}
	if !exists {
		return environmentMetadata(values, false), nil
	}
	delete(values, key)
	if len(values) == 0 {
		if err := os.Remove(s.environmentPath(targetID)); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("remove deploy environment: %w", err)
		}
		return environmentMetadata(values, false), nil
	}
	if err := s.writeEnvironment(targetID, values); err != nil {
		return nil, err
	}
	return environmentMetadata(values, true), nil
}

func (s *Service) configuredComposeTarget(targetID int64) (*models.DeployTarget, error) {
	target, err := s.GetTarget(targetID)
	if err != nil {
		return nil, err
	}
	if target == nil {
		return nil, fmt.Errorf("%w: %d", ErrDeployTargetNotFound, targetID)
	}
	if target.DeployKind != models.DeployKindCompose {
		return nil, ErrComposeTargetRequired
	}
	if !validComposeFileOrEmpty(target.ComposeFile) {
		return nil, fmt.Errorf("%w: Compose file is outside the project directory", ErrComposeTargetRequired)
	}
	return target, nil
}

func (s *Service) environmentPath(targetID int64) string {
	return filepath.Join(s.envDir, fmt.Sprintf("target-%d.env", targetID))
}

func (s *Service) environmentFile(targetID int64) (string, error) {
	if s.envDir == "" {
		return "", nil
	}
	path := s.environmentPath(targetID)
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("inspect deploy environment: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("deploy environment path is not a regular file")
	}
	return path, nil
}

func (s *Service) readEnvironment(targetID int64) (map[string]string, bool, error) {
	if s.envDir == "" {
		return nil, false, ErrEnvironmentStoreUnavailable
	}
	path := s.environmentPath(targetID)
	content, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]string{}, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read deploy environment: %w", err)
	}
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(content), "\n"), "\n") {
		if line == "" {
			continue
		}
		separator := strings.IndexByte(line, '=')
		if separator < 1 || !environmentKeyPattern.MatchString(line[:separator]) {
			return nil, true, errors.New("deploy environment file has an invalid key")
		}
		encoded := line[separator+1:]
		if len(encoded) < 2 || encoded[0] != '\'' || encoded[len(encoded)-1] != '\'' {
			return nil, true, errors.New("deploy environment file has an invalid value encoding")
		}
		values[line[:separator]] = encoded[1 : len(encoded)-1]
	}
	return values, true, nil
}

func (s *Service) writeEnvironment(targetID int64, values map[string]string) error {
	if s.envDir == "" {
		return ErrEnvironmentStoreUnavailable
	}
	if err := os.MkdirAll(s.envDir, 0o700); err != nil {
		return fmt.Errorf("create deploy environment directory: %w", err)
	}
	if err := os.Chmod(s.envDir, 0o700); err != nil {
		return fmt.Errorf("secure deploy environment directory: %w", err)
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var content strings.Builder
	for _, key := range keys {
		content.WriteString(key)
		content.WriteString("='")
		content.WriteString(values[key])
		content.WriteString("'\n")
	}
	temporary, err := os.CreateTemp(s.envDir, ".target-env-*")
	if err != nil {
		return fmt.Errorf("create deploy environment temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(content.String()); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, s.environmentPath(targetID)); err != nil {
		return fmt.Errorf("replace deploy environment: %w", err)
	}
	return os.Chmod(s.environmentPath(targetID), 0o600)
}

func environmentMetadata(values map[string]string, configured bool) *models.DeployEnvironment {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	variables := make([]models.DeployEnvironmentVariable, 0, len(keys))
	for _, key := range keys {
		variables = append(variables, models.DeployEnvironmentVariable{Key: key})
	}
	return &models.DeployEnvironment{Configured: configured, Variables: variables}
}
