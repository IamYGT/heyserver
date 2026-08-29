package bind

import (
	"fmt"
	"os"
	"path/filepath"
)

// recoverLifecycleJournal finalizes a transaction that reached the reloaded
// stage, or restores the pre-transaction files for every earlier stage.
func recoverLifecycleJournal(store *lifecycleJournalStore, reload func() error) error {
	if store == nil {
		return nil
	}
	exists, err := store.exists()
	if err != nil {
		return fmt.Errorf("checking BIND lifecycle journal: %w", err)
	}
	if !exists {
		return nil
	}

	journal, err := store.load()
	if err != nil {
		return fmt.Errorf("loading BIND lifecycle journal: %w", err)
	}
	if journal.Stage == stageReloaded {
		if journal.Operation == operationDelete {
			if err := removeRecoveryTombstone(journal.TombstonePath); err != nil {
				return fmt.Errorf("finalizing recovered BIND zone deletion: %w", err)
			}
		}
		if err := store.clear(); err != nil {
			return fmt.Errorf("clearing completed BIND lifecycle journal: %w", err)
		}
		return nil
	}
	if reload == nil {
		return fmt.Errorf("BIND lifecycle rollback requires a reload function")
	}

	configSnapshot := snapshotFromJournal(journal.Config)
	switch journal.Operation {
	case operationCreate:
		if err := rollbackCreatedZone(journal.ConfigPath, journal.ZonePath, configSnapshot); err != nil {
			return fmt.Errorf("recovering interrupted BIND zone creation: %w", err)
		}
	case operationDelete:
		if journal.Zone == nil {
			return fmt.Errorf("recovering interrupted BIND zone deletion: zone snapshot is missing")
		}
		if err := rollbackDeletedZone(
			journal.ConfigPath,
			journal.ZonePath,
			journal.TombstonePath,
			configSnapshot,
			snapshotFromJournal(*journal.Zone),
		); err != nil {
			return fmt.Errorf("recovering interrupted BIND zone deletion: %w", err)
		}
	default:
		return fmt.Errorf("recovering BIND lifecycle transaction: unsupported operation %q", journal.Operation)
	}

	if err := reload(); err != nil {
		return fmt.Errorf("reloading BIND after lifecycle rollback: %w", err)
	}
	if err := store.clear(); err != nil {
		return fmt.Errorf("clearing recovered BIND lifecycle journal: %w", err)
	}
	return nil
}

func removeRecoveryTombstone(path string) error {
	if path == "" {
		return nil
	}
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}
