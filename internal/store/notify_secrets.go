package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/models"
)

const (
	notificationConfigReferencePrefix = "file:"
	notificationConfigPendingPrefix   = "pending:"
	notificationConfigMaxBytes        = 64 * 1024
)

var ErrNotificationSecretStoreUnavailable = errors.New("notification secret store unavailable")

// NotificationChannelRepository persists channel credentials in protected
// installation-owned files. SQLite contains only a deterministic file
// reference; List and Get hydrate the config only for trusted server-side
// consumers. API handlers must return a redacted view.
type NotificationChannelRepository struct {
	db        *sql.DB
	secretDir string
	initErr   error
}

func NewNotificationChannelRepository(db *sql.DB, dataDir string) (*NotificationChannelRepository, error) {
	r := &NotificationChannelRepository{db: db}
	if db == nil {
		r.initErr = fmt.Errorf("%w: database is nil", ErrNotificationSecretStoreUnavailable)
		return r, r.initErr
	}
	if dataDir == "" || !filepath.IsAbs(dataDir) {
		r.initErr = fmt.Errorf("%w: HServer data directory must be absolute", ErrNotificationSecretStoreUnavailable)
		return r, r.initErr
	}
	r.secretDir = filepath.Join(filepath.Clean(dataDir), "notification-channel-secrets")
	if err := r.prepareSecretDir(); err != nil {
		r.initErr = fmt.Errorf("%w: %v", ErrNotificationSecretStoreUnavailable, err)
		return r, r.initErr
	}
	if err := r.migrateLegacyChannelConfigs(); err != nil {
		r.initErr = fmt.Errorf("%w: %v", ErrNotificationSecretStoreUnavailable, err)
		return r, r.initErr
	}
	return r, nil
}

func (r *NotificationChannelRepository) ready() error {
	if r == nil {
		return ErrNotificationSecretStoreUnavailable
	}
	return r.initErr
}

func (r *NotificationChannelRepository) prepareSecretDir() error {
	if err := os.MkdirAll(r.secretDir, 0o700); err != nil {
		return fmt.Errorf("create notification secret directory: %w", err)
	}
	info, err := os.Lstat(r.secretDir)
	if err != nil {
		return fmt.Errorf("inspect notification secret directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("notification secret path must be a regular directory")
	}
	if err := os.Chmod(r.secretDir, 0o700); err != nil {
		return fmt.Errorf("protect notification secret directory: %w", err)
	}
	return nil
}

func (r *NotificationChannelRepository) channelConfigPath(id int64) string {
	return filepath.Join(r.secretDir, fmt.Sprintf("channel-%d.json", id))
}

func (r *NotificationChannelRepository) channelConfigReference(id int64) string {
	return notificationConfigReferencePrefix + filepath.Base(r.channelConfigPath(id))
}

func (r *NotificationChannelRepository) channelConfigPendingReference(id int64) string {
	return notificationConfigPendingPrefix + filepath.Base(r.channelConfigPath(id))
}

func validateNotificationChannelConfig(config string) error {
	if config == "" || len(config) > notificationConfigMaxBytes || !json.Valid([]byte(config)) {
		return errors.New("notification channel config must be valid bounded JSON")
	}
	var value map[string]any
	if err := json.Unmarshal([]byte(config), &value); err != nil || value == nil {
		return errors.New("notification channel config must be a JSON object")
	}
	return nil
}

func (r *NotificationChannelRepository) writeChannelConfig(id int64, config string) error {
	if err := validateNotificationChannelConfig(config); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(r.secretDir, ".channel-config-*")
	if err != nil {
		return fmt.Errorf("create notification config temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer func() { _ = os.Remove(temporaryPath) }()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.WriteString(config); err != nil {
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
	path := r.channelConfigPath(id)
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("replace notification config: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("protect notification config: %w", err)
	}
	return nil
}

func (r *NotificationChannelRepository) readChannelConfig(id int64, stored string) (string, error) {
	if stored != r.channelConfigReference(id) {
		return "", fmt.Errorf("%w: channel %d has an invalid config reference", ErrNotificationSecretStoreUnavailable, id)
	}
	path := r.channelConfigPath(id)
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("%w: read channel %d config: %v", ErrNotificationSecretStoreUnavailable, id, err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() <= 0 || info.Size() > notificationConfigMaxBytes {
		return "", fmt.Errorf("%w: channel %d config must be a protected regular file", ErrNotificationSecretStoreUnavailable, id)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("%w: read channel %d config: %v", ErrNotificationSecretStoreUnavailable, id, err)
	}
	config := string(content)
	if err := validateNotificationChannelConfig(config); err != nil {
		return "", fmt.Errorf("%w: channel %d config is invalid", ErrNotificationSecretStoreUnavailable, id)
	}
	return config, nil
}

func (r *NotificationChannelRepository) hydrateChannelConfig(channel *models.NotificationChannel) error {
	config, err := r.readChannelConfig(channel.ID, channel.Config)
	if err != nil {
		return err
	}
	channel.Config = config
	return nil
}

func (r *NotificationChannelRepository) removeChannelConfig(id int64) error {
	err := os.Remove(r.channelConfigPath(id))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (r *NotificationChannelRepository) migrateLegacyChannelConfigs() error {
	rows, err := r.db.Query(`SELECT id, config FROM notification_channels ORDER BY id ASC`)
	if err != nil {
		return err
	}
	type legacyConfig struct {
		id     int64
		stored string
	}
	var configs []legacyConfig
	for rows.Next() {
		var item legacyConfig
		if err := rows.Scan(&item.id, &item.stored); err != nil {
			_ = rows.Close()
			return err
		}
		configs = append(configs, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range configs {
		if strings.HasPrefix(item.stored, notificationConfigPendingPrefix) {
			if item.stored != r.channelConfigPendingReference(item.id) {
				return fmt.Errorf("recover notification channel %d config reference: invalid pending reference", item.id)
			}
			// An interrupted update advances config_revision before replacing the
			// protected file. The atomic file replacement therefore leaves either
			// the complete old config or the complete new config. Promote whichever
			// protected file is present under the already-advanced revision; old
			// delivery receipts remain fenced by that revision.
			if _, err := r.readChannelConfig(item.id, r.channelConfigReference(item.id)); err != nil {
				return fmt.Errorf("recover notification channel %d config: %w", item.id, err)
			}
			res, err := r.db.Exec(`UPDATE notification_channels SET config = ? WHERE id = ? AND config = ?`, r.channelConfigReference(item.id), item.id, item.stored)
			if err != nil {
				return fmt.Errorf("recover notification channel %d config reference: %w", item.id, err)
			}
			affected, err := res.RowsAffected()
			if err != nil || affected != 1 {
				return fmt.Errorf("recover notification channel %d config reference: concurrent update", item.id)
			}
			continue
		}
		if strings.HasPrefix(item.stored, notificationConfigReferencePrefix) {
			if _, err := r.readChannelConfig(item.id, item.stored); err != nil {
				return err
			}
			continue
		}
		if err := r.writeChannelConfig(item.id, item.stored); err != nil {
			return fmt.Errorf("migrate notification channel %d: %w", item.id, err)
		}
		res, err := r.db.Exec(`UPDATE notification_channels SET config = ? WHERE id = ? AND config = ?`, r.channelConfigReference(item.id), item.id, item.stored)
		if err != nil {
			return fmt.Errorf("persist notification channel %d config reference: %w", item.id, err)
		}
		affected, err := res.RowsAffected()
		if err != nil || affected != 1 {
			return fmt.Errorf("persist notification channel %d config reference: concurrent update", item.id)
		}
	}
	return nil
}

func (r *NotificationChannelRepository) deleteChannel(id int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var stored string
	if err := tx.QueryRow(`SELECT config FROM notification_channels WHERE id = ?`, id).Scan(&stored); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if stored != r.channelConfigReference(id) {
		return fmt.Errorf("%w: channel %d has an invalid config reference", ErrNotificationSecretStoreUnavailable, id)
	}
	path := r.channelConfigPath(id)
	tombstone := fmt.Sprintf("%s.delete-%d", path, time.Now().UnixNano())
	if err := os.Rename(path, tombstone); err != nil {
		return fmt.Errorf("stage notification config removal: %w", err)
	}
	restore := true
	defer func() {
		if restore {
			_ = os.Rename(tombstone, path)
		}
	}()
	if _, err := tx.Exec(`DELETE FROM notification_channels WHERE id = ?`, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	restore = false
	if err := os.Remove(tombstone); err != nil {
		return fmt.Errorf("remove notification config: %w", err)
	}
	return nil
}
