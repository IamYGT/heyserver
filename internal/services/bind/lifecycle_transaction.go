package bind

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func applyZoneCreateTransaction(
	configPath string,
	zonePath string,
	configContent []byte,
	zoneContent []byte,
	validateConfig func(candidatePath string) error,
	validateZone func(candidatePath string) error,
	reload func() error,
	journal *lifecycleJournalStore,
) error {
	configSnapshot, err := snapshotRegularFile(configPath)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(zonePath); err == nil {
		return fmt.Errorf("zone file %s already exists", zonePath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("checking zone file: %w", err)
	}
	zoneMetadata, err := metadataForNewFile(filepath.Dir(zonePath), 0o644)
	if err != nil {
		return err
	}

	zoneCandidate, err := stageFile(zonePath, zoneContent, zoneMetadata)
	if err != nil {
		return fmt.Errorf("staging new zone file: %w", err)
	}
	defer func() { _ = os.Remove(zoneCandidate) }()
	if validateZone != nil {
		if err := validateZone(zoneCandidate); err != nil {
			return fmt.Errorf("staged zone validation failed: %w", err)
		}
	}

	configCandidate, err := stageFile(configPath, configContent, configSnapshot)
	if err != nil {
		return fmt.Errorf("staging BIND configuration: %w", err)
	}
	defer func() { _ = os.Remove(configCandidate) }()
	if validateConfig != nil {
		if err := validateConfig(configCandidate); err != nil {
			return fmt.Errorf("staged BIND configuration validation failed: %w", err)
		}
	}
	if err := journalPrepareCreate(journal, configPath, zonePath, configSnapshot); err != nil {
		return fmt.Errorf("preparing BIND lifecycle journal: %w", err)
	}

	if err := commitNewStagedFile(zoneCandidate, zonePath); err != nil {
		_ = journalClear(journal)
		return fmt.Errorf("committing new zone file: %w", err)
	}
	if err := journalUpdateStage(journal, stageZoneCommitted); err != nil {
		return rollbackCreateAfterJournalFailure(journal, configPath, zonePath, configSnapshot, reload, err)
	}
	if err := commitStagedFile(configCandidate, configPath); err != nil {
		rollbackErr := rollbackCreatedZone(configPath, zonePath, configSnapshot)
		if rollbackErr != nil {
			return fmt.Errorf("committing BIND configuration: %v; create rollback failed: %w", err, rollbackErr)
		}
		if clearErr := journalClear(journal); clearErr != nil {
			return fmt.Errorf("committing BIND configuration: %v; created zone rolled back but journal cleanup failed: %w", err, clearErr)
		}
		return fmt.Errorf("committing BIND configuration: %w; created zone rolled back", err)
	}
	if err := journalUpdateStage(journal, stageConfigCommitted); err != nil {
		return rollbackCreateAfterJournalFailure(journal, configPath, zonePath, configSnapshot, reload, err)
	}

	if reload != nil {
		if reloadErr := reload(); reloadErr != nil {
			if rollbackErr := rollbackCreatedZone(configPath, zonePath, configSnapshot); rollbackErr != nil {
				return fmt.Errorf("BIND reload failed: %v; create rollback failed: %w", reloadErr, rollbackErr)
			}
			if rollbackReloadErr := reload(); rollbackReloadErr != nil {
				return fmt.Errorf("BIND reload failed: %v; created zone rolled back on disk but runtime rollback reload failed: %w", reloadErr, rollbackReloadErr)
			}
			if clearErr := journalClear(journal); clearErr != nil {
				return fmt.Errorf("BIND reload failed: %v; created zone rolled back and reloaded but journal cleanup failed: %w", reloadErr, clearErr)
			}
			return fmt.Errorf("BIND reload failed: %w; created zone and configuration rolled back and reloaded", reloadErr)
		}
	}
	if err := journalUpdateStage(journal, stageReloaded); err != nil {
		return rollbackCreateAfterJournalFailure(journal, configPath, zonePath, configSnapshot, reload, err)
	}
	if err := journalClear(journal); err != nil {
		return fmt.Errorf("BIND zone created and reloaded but lifecycle journal cleanup failed: %w", err)
	}
	return nil
}

func applyZoneDeleteTransaction(
	configPath string,
	zonePath string,
	configContent []byte,
	validateConfig func(candidatePath string) error,
	reload func() error,
	journal *lifecycleJournalStore,
) error {
	configSnapshot, err := snapshotRegularFile(configPath)
	if err != nil {
		return err
	}
	zoneSnapshot, err := snapshotRegularFile(zonePath)
	if err != nil {
		return err
	}

	configCandidate, err := stageFile(configPath, configContent, configSnapshot)
	if err != nil {
		return fmt.Errorf("staging BIND configuration: %w", err)
	}
	defer func() { _ = os.Remove(configCandidate) }()
	if validateConfig != nil {
		if err := validateConfig(configCandidate); err != nil {
			return fmt.Errorf("staged BIND configuration validation failed: %w", err)
		}
	}

	tombstone, err := reserveTombstonePath(zonePath)
	if err != nil {
		return fmt.Errorf("reserving zone deletion snapshot: %w", err)
	}
	if err := journalPrepareDelete(journal, configPath, zonePath, tombstone, configSnapshot, zoneSnapshot); err != nil {
		return fmt.Errorf("preparing BIND lifecycle journal: %w", err)
	}
	if err := moveFileToTombstone(zonePath, tombstone); err != nil {
		_ = journalClear(journal)
		return fmt.Errorf("staging zone deletion: %w", err)
	}
	if err := journalUpdateStage(journal, stageZoneCommitted); err != nil {
		return rollbackDeleteAfterJournalFailure(journal, configPath, zonePath, tombstone, configSnapshot, zoneSnapshot, reload, err)
	}
	if err := commitStagedFile(configCandidate, configPath); err != nil {
		rollbackErr := rollbackDeletedZone(configPath, zonePath, tombstone, configSnapshot, zoneSnapshot)
		if rollbackErr != nil {
			return fmt.Errorf("committing BIND configuration: %v; delete rollback failed: %w", err, rollbackErr)
		}
		if clearErr := journalClear(journal); clearErr != nil {
			return fmt.Errorf("committing BIND configuration: %v; deleted zone rolled back but journal cleanup failed: %w", err, clearErr)
		}
		return fmt.Errorf("committing BIND configuration: %w; deleted zone rolled back", err)
	}
	if err := journalUpdateStage(journal, stageConfigCommitted); err != nil {
		return rollbackDeleteAfterJournalFailure(journal, configPath, zonePath, tombstone, configSnapshot, zoneSnapshot, reload, err)
	}

	if reload != nil {
		if reloadErr := reload(); reloadErr != nil {
			if rollbackErr := rollbackDeletedZone(configPath, zonePath, tombstone, configSnapshot, zoneSnapshot); rollbackErr != nil {
				return fmt.Errorf("BIND reload failed: %v; delete rollback failed: %w", reloadErr, rollbackErr)
			}
			if rollbackReloadErr := reload(); rollbackReloadErr != nil {
				return fmt.Errorf("BIND reload failed: %v; deleted zone restored on disk but runtime rollback reload failed: %w", reloadErr, rollbackReloadErr)
			}
			if clearErr := journalClear(journal); clearErr != nil {
				return fmt.Errorf("BIND reload failed: %v; deleted zone restored and reloaded but journal cleanup failed: %w", reloadErr, clearErr)
			}
			return fmt.Errorf("BIND reload failed: %w; deleted zone and configuration restored and reloaded", reloadErr)
		}
	}

	if err := os.Remove(tombstone); err != nil {
		if rollbackErr := rollbackDeletedZone(configPath, zonePath, tombstone, configSnapshot, zoneSnapshot); rollbackErr != nil {
			return fmt.Errorf("removing deleted zone snapshot: %v; cleanup rollback failed: %w", err, rollbackErr)
		}
		if reload != nil {
			if rollbackReloadErr := reload(); rollbackReloadErr != nil {
				return fmt.Errorf("removing deleted zone snapshot: %v; deletion rolled back on disk but runtime rollback reload failed: %w", err, rollbackReloadErr)
			}
		}
		if clearErr := journalClear(journal); clearErr != nil {
			return fmt.Errorf("removing deleted zone snapshot: %v; deletion rolled back but journal cleanup failed: %w", err, clearErr)
		}
		return fmt.Errorf("removing deleted zone snapshot: %w; deletion rolled back", err)
	}
	if err := syncDirectory(filepath.Dir(zonePath)); err != nil {
		return err
	}
	if err := journalUpdateStage(journal, stageReloaded); err != nil {
		return rollbackDeleteAfterJournalFailure(journal, configPath, zonePath, tombstone, configSnapshot, zoneSnapshot, reload, err)
	}
	if err := journalClear(journal); err != nil {
		return fmt.Errorf("BIND zone deleted and reloaded but lifecycle journal cleanup failed: %w", err)
	}
	return nil
}

func metadataForNewFile(dir string, mode os.FileMode) (fileSnapshot, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return fileSnapshot{}, fmt.Errorf("reading zone directory metadata: %w", err)
	}
	if !info.IsDir() {
		return fileSnapshot{}, fmt.Errorf("zone directory %s is not a directory", dir)
	}
	metadata := fileSnapshot{mode: mode, uid: -1, gid: -1}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		metadata.uid = int(stat.Uid)
		metadata.gid = int(stat.Gid)
	}
	return metadata, nil
}

func commitNewStagedFile(candidate, target string) error {
	if err := os.Link(candidate, target); err != nil {
		return err
	}
	if err := os.Remove(candidate); err != nil {
		_ = os.Remove(target)
		return err
	}
	return syncDirectory(filepath.Dir(target))
}

func reserveTombstonePath(path string) (string, error) {
	tombstoneFile, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".hserver-deleted-*")
	if err != nil {
		return "", err
	}
	tombstone := tombstoneFile.Name()
	if err := tombstoneFile.Close(); err != nil {
		_ = os.Remove(tombstone)
		return "", err
	}
	if err := os.Remove(tombstone); err != nil {
		return "", err
	}
	return tombstone, nil
}

func moveFileToTombstone(path, tombstone string) error {
	if filepath.Dir(path) != filepath.Dir(tombstone) {
		return fmt.Errorf("zone tombstone must be in the same directory as the zone file")
	}
	if err := os.Rename(path, tombstone); err != nil {
		return err
	}
	if err := syncDirectory(filepath.Dir(path)); err != nil {
		_ = os.Rename(tombstone, path)
		return err
	}
	return nil
}

func journalPrepareCreate(journal *lifecycleJournalStore, configPath, zonePath string, config fileSnapshot) error {
	if journal == nil {
		return nil
	}
	return journal.prepareCreate(configPath, zonePath, config)
}

func journalPrepareDelete(
	journal *lifecycleJournalStore,
	configPath, zonePath, tombstonePath string,
	config, zone fileSnapshot,
) error {
	if journal == nil {
		return nil
	}
	return journal.prepareDelete(configPath, zonePath, tombstonePath, config, zone)
}

func journalUpdateStage(journal *lifecycleJournalStore, stage lifecycleStage) error {
	if journal == nil {
		return nil
	}
	return journal.updateStage(stage)
}

func journalClear(journal *lifecycleJournalStore) error {
	if journal == nil {
		return nil
	}
	return journal.clear()
}

func rollbackCreateAfterJournalFailure(
	journal *lifecycleJournalStore,
	configPath, zonePath string,
	configSnapshot fileSnapshot,
	reload func() error,
	journalErr error,
) error {
	if rollbackErr := rollbackCreatedZone(configPath, zonePath, configSnapshot); rollbackErr != nil {
		return fmt.Errorf("updating BIND lifecycle journal: %v; create rollback failed: %w", journalErr, rollbackErr)
	}
	if reload != nil {
		if reloadErr := reload(); reloadErr != nil {
			return fmt.Errorf("updating BIND lifecycle journal: %v; created zone rolled back on disk but runtime rollback reload failed: %w", journalErr, reloadErr)
		}
	}
	if clearErr := journalClear(journal); clearErr != nil {
		return fmt.Errorf("updating BIND lifecycle journal: %v; created zone rolled back but journal cleanup failed: %w", journalErr, clearErr)
	}
	return fmt.Errorf("updating BIND lifecycle journal: %w; created zone and configuration rolled back", journalErr)
}

func rollbackDeleteAfterJournalFailure(
	journal *lifecycleJournalStore,
	configPath, zonePath, tombstone string,
	configSnapshot, zoneSnapshot fileSnapshot,
	reload func() error,
	journalErr error,
) error {
	if rollbackErr := rollbackDeletedZone(configPath, zonePath, tombstone, configSnapshot, zoneSnapshot); rollbackErr != nil {
		return fmt.Errorf("updating BIND lifecycle journal: %v; delete rollback failed: %w", journalErr, rollbackErr)
	}
	if reload != nil {
		if reloadErr := reload(); reloadErr != nil {
			return fmt.Errorf("updating BIND lifecycle journal: %v; deleted zone restored on disk but runtime rollback reload failed: %w", journalErr, reloadErr)
		}
	}
	if clearErr := journalClear(journal); clearErr != nil {
		return fmt.Errorf("updating BIND lifecycle journal: %v; deleted zone restored but journal cleanup failed: %w", journalErr, clearErr)
	}
	return fmt.Errorf("updating BIND lifecycle journal: %w; deleted zone and configuration restored", journalErr)
}

func rollbackCreatedZone(configPath, zonePath string, configSnapshot fileSnapshot) error {
	var rollbackErrors []error
	if err := restoreFile(configPath, configSnapshot); err != nil {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("restoring configuration: %w", err))
	}
	if err := os.Remove(zonePath); err != nil && !os.IsNotExist(err) {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("removing created zone: %w", err))
	} else if err == nil {
		if syncErr := syncDirectory(filepath.Dir(zonePath)); syncErr != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("syncing zone directory: %w", syncErr))
		}
	}
	return errors.Join(rollbackErrors...)
}

func rollbackDeletedZone(
	configPath string,
	zonePath string,
	tombstone string,
	configSnapshot fileSnapshot,
	zoneSnapshot fileSnapshot,
) error {
	var rollbackErrors []error
	if _, err := os.Lstat(tombstone); err == nil {
		if _, targetErr := os.Lstat(zonePath); targetErr == nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("zone rollback target %s already exists", zonePath))
		} else if !os.IsNotExist(targetErr) {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("checking zone rollback target: %w", targetErr))
		} else if renameErr := os.Rename(tombstone, zonePath); renameErr != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("restoring zone file: %w", renameErr))
		} else if syncErr := syncDirectory(filepath.Dir(zonePath)); syncErr != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("syncing restored zone: %w", syncErr))
		}
	} else if os.IsNotExist(err) {
		if restoreErr := restoreFile(zonePath, zoneSnapshot); restoreErr != nil {
			rollbackErrors = append(rollbackErrors, fmt.Errorf("recreating zone file: %w", restoreErr))
		}
	} else {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("reading deleted zone snapshot: %w", err))
	}
	if err := restoreFile(configPath, configSnapshot); err != nil {
		rollbackErrors = append(rollbackErrors, fmt.Errorf("restoring configuration: %w", err))
	}
	return errors.Join(rollbackErrors...)
}
