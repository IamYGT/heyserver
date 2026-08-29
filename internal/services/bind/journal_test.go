package bind

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLifecycleJournalRoundTripAndClear(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newLifecycleJournalStore(root)
	configPath := filepath.Join(root, "named.conf.local")
	zonePath := filepath.Join(root, "zones", "db.example.com")
	config := fileSnapshot{content: []byte("original config\n"), mode: 0o640, uid: 12, gid: 34}

	if err := store.prepareCreate(configPath, zonePath, config); err != nil {
		t.Fatalf("prepareCreate() error: %v", err)
	}
	journal, err := store.load()
	if err != nil {
		t.Fatalf("load() error: %v", err)
	}
	if journal.Operation != operationCreate || journal.Stage != stagePrepared {
		t.Fatalf("journal operation=%q stage=%q", journal.Operation, journal.Stage)
	}
	restored := snapshotFromJournal(journal.Config)
	if string(restored.content) != "original config\n" || restored.mode != 0o640 || restored.uid != 12 || restored.gid != 34 {
		t.Fatalf("restored snapshot=%#v", restored)
	}

	if err := store.updateStage(stageConfigCommitted); err != nil {
		t.Fatalf("updateStage() error: %v", err)
	}
	journal, err = store.load()
	if err != nil || journal.Stage != stageConfigCommitted {
		t.Fatalf("updated journal=%#v error=%v", journal, err)
	}
	info, err := os.Stat(store.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("journal mode=%#o, want 0600", info.Mode().Perm())
	}
	dirInfo, err := os.Stat(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("journal directory mode=%#o, want 0700", dirInfo.Mode().Perm())
	}

	if err := store.clear(); err != nil {
		t.Fatalf("clear() error: %v", err)
	}
	if exists, err := store.exists(); err != nil || exists {
		t.Fatalf("exists()=%v error=%v after clear", exists, err)
	}
}

func TestLifecycleJournalRefusesToOverwritePendingRecovery(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newLifecycleJournalStore(root)
	configPath := filepath.Join(root, "named.conf.local")
	zonePath := filepath.Join(root, "zones", "db.example.com")
	first := fileSnapshot{content: []byte("first config\n"), mode: 0o640, uid: -1, gid: -1}
	if err := store.prepareCreate(configPath, zonePath, first); err != nil {
		t.Fatal(err)
	}

	err := store.prepareCreate(configPath, zonePath, fileSnapshot{content: []byte("second config\n"), mode: 0o640, uid: -1, gid: -1})
	if err == nil || !strings.Contains(err.Error(), "already pending recovery") {
		t.Fatalf("prepareCreate() error=%v, want pending recovery refusal", err)
	}
	journal, err := store.load()
	if err != nil {
		t.Fatal(err)
	}
	if string(journal.Config.Content) != "first config\n" {
		t.Fatalf("pending journal was overwritten with %q", journal.Config.Content)
	}
}

func TestLifecycleDeleteJournalPreservesRecoverySnapshot(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newLifecycleJournalStore(root)
	configPath := filepath.Join(root, "named.conf.local")
	zonePath := filepath.Join(root, "zones", "db.example.com")
	tombstonePath := filepath.Join(root, "zones", ".db.example.com.hserver-deleted-test")
	config := fileSnapshot{content: []byte("config\n"), mode: 0o640, uid: 1, gid: 2}
	zone := fileSnapshot{content: []byte("zone\n"), mode: 0o644, uid: 3, gid: 4}

	if err := store.prepareDelete(configPath, zonePath, tombstonePath, config, zone); err != nil {
		t.Fatalf("prepareDelete() error: %v", err)
	}
	journal, err := store.load()
	if err != nil {
		t.Fatalf("load() error: %v", err)
	}
	if journal.Operation != operationDelete || journal.Zone == nil || string(journal.Zone.Content) != "zone\n" {
		t.Fatalf("delete journal=%#v", journal)
	}
}

func TestLifecycleJournalRejectsLoosePermissions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newLifecycleJournalStore(root)
	if err := store.prepareCreate(
		filepath.Join(root, "named.conf.local"),
		filepath.Join(root, "zones", "db.example.com"),
		fileSnapshot{content: []byte("config\n"), mode: 0o640, uid: -1, gid: -1},
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(store.path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.load(); err == nil {
		t.Fatal("load() with loose journal permissions expected error")
	}
}

func TestLifecycleJournalRejectsOwnerExecutePermission(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store := newLifecycleJournalStore(root)
	if err := store.prepareCreate(
		filepath.Join(root, "named.conf.local"),
		filepath.Join(root, "zones", "db.example.com"),
		fileSnapshot{content: []byte("config\n"), mode: 0o640, uid: -1, gid: -1},
	); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(store.path, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.load(); err == nil {
		t.Fatal("load() with executable journal permissions expected error")
	}
}
