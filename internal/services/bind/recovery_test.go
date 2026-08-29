package bind

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRecoverLifecycleJournalRollsBackInterruptedCreate(t *testing.T) {
	configPath, zonePath := writeLifecycleFixture(t, false)
	store := newLifecycleJournalStore(filepath.Join(t.TempDir(), "state"))
	configSnapshot, err := snapshotRegularFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.prepareCreate(configPath, zonePath, configSnapshot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zonePath, []byte("new zone\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("changed config\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := store.updateStage(stageConfigCommitted); err != nil {
		t.Fatal(err)
	}

	reloadCalls := 0
	if err := recoverLifecycleJournal(store, func() error { reloadCalls++; return nil }); err != nil {
		t.Fatalf("recoverLifecycleJournal() error: %v", err)
	}
	if reloadCalls != 1 {
		t.Fatalf("reload calls=%d, want 1", reloadCalls)
	}
	if got := readLifecycleFile(t, configPath); got != "// local\n" {
		t.Fatalf("config=%q after recovery", got)
	}
	if _, err := os.Stat(zonePath); !os.IsNotExist(err) {
		t.Fatalf("created zone remains after recovery: %v", err)
	}
	assertJournalCleared(t, store)
}

func TestRecoverLifecycleJournalRestoresInterruptedDelete(t *testing.T) {
	configPath, zonePath := writeLifecycleFixture(t, true)
	store := newLifecycleJournalStore(filepath.Join(t.TempDir(), "state"))
	configSnapshot, err := snapshotRegularFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	zoneSnapshot, err := snapshotRegularFile(zonePath)
	if err != nil {
		t.Fatal(err)
	}
	tombstone, err := reserveTombstonePath(zonePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.prepareDelete(configPath, zonePath, tombstone, configSnapshot, zoneSnapshot); err != nil {
		t.Fatal(err)
	}
	if err := moveFileToTombstone(zonePath, tombstone); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("// local\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := store.updateStage(stageConfigCommitted); err != nil {
		t.Fatal(err)
	}

	if err := recoverLifecycleJournal(store, func() error { return nil }); err != nil {
		t.Fatalf("recoverLifecycleJournal() error: %v", err)
	}
	if got := readLifecycleFile(t, configPath); got != string(configSnapshot.content) {
		t.Fatalf("config=%q, want %q", got, configSnapshot.content)
	}
	if got := readLifecycleFile(t, zonePath); got != string(zoneSnapshot.content) {
		t.Fatalf("zone=%q, want %q", got, zoneSnapshot.content)
	}
	if _, err := os.Stat(tombstone); !os.IsNotExist(err) {
		t.Fatalf("tombstone remains after recovery: %v", err)
	}
	assertJournalCleared(t, store)
}

func TestRecoverLifecycleJournalRecreatesDeletedZoneFromSnapshot(t *testing.T) {
	configPath, zonePath := writeLifecycleFixture(t, true)
	store := newLifecycleJournalStore(filepath.Join(t.TempDir(), "state"))
	configSnapshot, err := snapshotRegularFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	zoneSnapshot, err := snapshotRegularFile(zonePath)
	if err != nil {
		t.Fatal(err)
	}
	tombstone := filepath.Join(filepath.Dir(zonePath), ".db.example.com.hserver-deleted-recovery")
	if err := store.prepareDelete(configPath, zonePath, tombstone, configSnapshot, zoneSnapshot); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(zonePath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("// local\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := store.updateStage(stageConfigCommitted); err != nil {
		t.Fatal(err)
	}

	if err := recoverLifecycleJournal(store, func() error { return nil }); err != nil {
		t.Fatalf("recoverLifecycleJournal() error: %v", err)
	}
	if got := readLifecycleFile(t, zonePath); got != string(zoneSnapshot.content) {
		t.Fatalf("zone=%q, want recovered %q", got, zoneSnapshot.content)
	}
	assertJournalCleared(t, store)
}

func TestRecoverLifecycleJournalFinalizesReloadedTransactions(t *testing.T) {
	t.Run("create", func(t *testing.T) {
		configPath, zonePath := writeLifecycleFixture(t, false)
		store := newLifecycleJournalStore(filepath.Join(t.TempDir(), "state"))
		snapshot, err := snapshotRegularFile(configPath)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.prepareCreate(configPath, zonePath, snapshot); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(zonePath, []byte("committed zone\n"), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(configPath, []byte("committed config\n"), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := store.updateStage(stageReloaded); err != nil {
			t.Fatal(err)
		}
		if err := recoverLifecycleJournal(store, func() error { return errors.New("must not reload") }); err != nil {
			t.Fatal(err)
		}
		if got := readLifecycleFile(t, zonePath); got != "committed zone\n" {
			t.Fatalf("committed zone changed to %q", got)
		}
		assertJournalCleared(t, store)
	})

	t.Run("delete", func(t *testing.T) {
		configPath, zonePath := writeLifecycleFixture(t, true)
		store := newLifecycleJournalStore(filepath.Join(t.TempDir(), "state"))
		configSnapshot, _ := snapshotRegularFile(configPath)
		zoneSnapshot, _ := snapshotRegularFile(zonePath)
		tombstone, err := reserveTombstonePath(zonePath)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.prepareDelete(configPath, zonePath, tombstone, configSnapshot, zoneSnapshot); err != nil {
			t.Fatal(err)
		}
		if err := moveFileToTombstone(zonePath, tombstone); err != nil {
			t.Fatal(err)
		}
		if err := store.updateStage(stageReloaded); err != nil {
			t.Fatal(err)
		}
		if err := recoverLifecycleJournal(store, func() error { return errors.New("must not reload") }); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(tombstone); !os.IsNotExist(err) {
			t.Fatalf("completed deletion tombstone remains: %v", err)
		}
		if _, err := os.Stat(zonePath); !os.IsNotExist(err) {
			t.Fatalf("deleted zone was restored: %v", err)
		}
		assertJournalCleared(t, store)
	})
}

func TestRecoverLifecycleJournalRetainsJournalWhenReloadFails(t *testing.T) {
	configPath, zonePath := writeLifecycleFixture(t, false)
	store := newLifecycleJournalStore(filepath.Join(t.TempDir(), "state"))
	snapshot, err := snapshotRegularFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.prepareCreate(configPath, zonePath, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(zonePath, []byte("new zone\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := store.updateStage(stageZoneCommitted); err != nil {
		t.Fatal(err)
	}

	err = recoverLifecycleJournal(store, func() error { return errors.New("rndc unavailable") })
	if err == nil || !strings.Contains(err.Error(), "reloading BIND after lifecycle rollback") {
		t.Fatalf("error=%v, want reload recovery failure", err)
	}
	exists, existsErr := store.exists()
	if existsErr != nil || !exists {
		t.Fatalf("journal exists=%v error=%v, want retained", exists, existsErr)
	}
}

func assertJournalCleared(t *testing.T, store *lifecycleJournalStore) {
	t.Helper()
	exists, err := store.exists()
	if err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("lifecycle journal still exists")
	}
}
