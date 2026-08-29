package deploy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/IamYGT/heyserver/internal/models"
)

const webhookSecretReferencePrefix = "file:"

// Webhook signing secrets live in installation-owned files. The legacy
// webhook_token column stores only the deterministic file reference once a
// data directory is configured.
func (s *Service) webhookSecretPath(targetID int64) string {
	return filepath.Join(s.webhookSecretDir, fmt.Sprintf("target-%d.secret", targetID))
}

func (s *Service) webhookSecretReference(targetID int64) string {
	return webhookSecretReferencePrefix + filepath.Base(s.webhookSecretPath(targetID))
}

func (s *Service) resolveWebhookSecret(targetID int64, stored string) (string, models.DeployWebhookStatus) {
	if stored == "" {
		return "", models.DeployWebhookNotConfigured
	}
	if s.webhookSecretDir == "" {
		// Focused legacy callers without a data directory retain the pre-file
		// behavior. The native product runtime always supplies HSERVER_DATA_DIR.
		if validateStoredWebhookSecret(stored) != nil {
			return "", models.DeployWebhookUnavailable
		}
		return stored, models.DeployWebhookHealthy
	}
	if stored != s.webhookSecretReference(targetID) {
		return "", models.DeployWebhookUnavailable
	}
	content, err := os.ReadFile(s.webhookSecretPath(targetID))
	if err != nil {
		return "", models.DeployWebhookUnavailable
	}
	secret := string(content)
	if err := validateStoredWebhookSecret(secret); err != nil {
		return "", models.DeployWebhookUnavailable
	}
	return secret, models.DeployWebhookHealthy
}

func (s *Service) writeWebhookSecret(targetID int64, secret string) error {
	if s.webhookSecretDir == "" {
		return errors.New("deploy webhook secret store is not configured")
	}
	if err := validateStoredWebhookSecret(secret); err != nil {
		return err
	}
	if err := os.MkdirAll(s.webhookSecretDir, 0o700); err != nil {
		return fmt.Errorf("create deploy webhook secret directory: %w", err)
	}
	if err := os.Chmod(s.webhookSecretDir, 0o700); err != nil {
		return fmt.Errorf("secure deploy webhook secret directory: %w", err)
	}
	temporary, err := os.CreateTemp(s.webhookSecretDir, ".target-secret-*")
	if err != nil {
		return fmt.Errorf("create deploy webhook secret temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(secret); err != nil {
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
	path := s.webhookSecretPath(targetID)
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace deploy webhook secret: %w", err)
	}
	return os.Chmod(path, 0o600)
}

func validateStoredWebhookSecret(secret string) error {
	if secret == "" || len(secret) > 4096 || strings.ContainsAny(secret, "\r\n\x00") {
		return errors.New("deploy webhook secret is invalid")
	}
	return nil
}

func (s *Service) migrateWebhookSecrets() error {
	if s.webhookSecretDir == "" {
		return nil
	}
	rows, err := s.db.Query(`SELECT id, webhook_token FROM deploy_targets WHERE webhook_token <> ''`)
	if err != nil {
		return err
	}
	type legacySecret struct {
		id     int64
		stored string
	}
	var legacy []legacySecret
	for rows.Next() {
		var item legacySecret
		if err := rows.Scan(&item.id, &item.stored); err != nil {
			_ = rows.Close()
			return err
		}
		legacy = append(legacy, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range legacy {
		reference := s.webhookSecretReference(item.id)
		if item.stored == reference {
			continue
		}
		if strings.HasPrefix(item.stored, webhookSecretReferencePrefix) {
			return fmt.Errorf("target %d has an invalid deploy webhook secret reference", item.id)
		}
		if err := s.writeWebhookSecret(item.id, item.stored); err != nil {
			return fmt.Errorf("migrate target %d deploy webhook secret: %w", item.id, err)
		}
		if _, err := s.db.Exec(`UPDATE deploy_targets SET webhook_token = ? WHERE id = ? AND webhook_token = ?`, reference, item.id, item.stored); err != nil {
			return err
		}
	}
	return nil
}
