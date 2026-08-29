package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func writeBackupFixture(t *testing.T, dir, name string, size int) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), make([]byte, size), 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixtureDiskBytes(t *testing.T, dir, name string) int64 {
	t.Helper()
	info, err := os.Stat(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return allocatedFileBytes(info)
}

func TestListMarksFullBackupPartialsAsOrphaned(t *testing.T) {
	dir := t.TempDir()
	s := &Service{backupDir: dir, jobs: make(map[string]*Job)}
	writeBackupFixture(t, dir, "backup-20260825164606-partial-files.tar.gz", 2048)
	writeBackupFixture(t, dir, "backup-20260825164606-partial-db-all-postgresql.sql.gz", minDatabaseBackupBytes)
	writeBackupFixture(t, dir, "backup-20260825164606-full.tar.gz", 2048)

	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("list length = %d, want 3", len(list))
	}
	statuses := make(map[string]string)
	for _, item := range list {
		statuses[item.Name] = item.Status
	}
	if got := statuses["backup-20260825164606-partial-files.tar.gz"]; got != "orphaned" {
		t.Fatalf("files partial status = %q, want orphaned", got)
	}
	if got := statuses["backup-20260825164606-partial-db-all-postgresql.sql.gz"]; got != "orphaned" {
		t.Fatalf("database partial status = %q, want orphaned", got)
	}
	if got := statuses["backup-20260825164606-full.tar.gz"]; got != "completed" {
		t.Fatalf("completed backup status = %q, want completed", got)
	}
}

func TestListMarksInterruptedPartFileAsOrphaned(t *testing.T) {
	dir := t.TempDir()
	s := &Service{backupDir: dir, jobs: make(map[string]*Job)}
	writeBackupFixture(t, dir, "backup-files.tar.gz.part", 2048)

	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Status != "orphaned" || list[0].ID != "backup-files" {
		t.Fatalf("unexpected part artifact: %+v", list)
	}
}

func TestLegacyDirectoryInventoryIncludesOnlyOrphanedArtifacts(t *testing.T) {
	dir := t.TempDir()
	s := &Service{backupDir: t.TempDir(), jobs: make(map[string]*Job)}
	writeBackupFixture(t, dir, "backup-a-partial-files.tar.gz", 2048)
	writeBackupFixture(t, dir, "backup-b-full.tar.gz", 2048)

	list := s.listDirectory(dir, true, "legacy-")
	if len(list) != 1 {
		t.Fatalf("legacy list = %+v", list)
	}
	if list[0].ID != "legacy-backup-a-partial-files" || list[0].Status != "orphaned" {
		t.Fatalf("legacy artifact = %+v", list[0])
	}
}

func TestPurgeOrphanedDeletesOnlyExplicitSelectedArtifacts(t *testing.T) {
	dir := t.TempDir()
	s := &Service{backupDir: dir, jobs: make(map[string]*Job)}
	selected := "backup-a-partial-files.tar.gz"
	other := "backup-b-partial-files.tar.gz"
	completed := "backup-c-full.tar.gz"
	writeBackupFixture(t, dir, selected, 2048)
	writeBackupFixture(t, dir, other, 4096)
	writeBackupFixture(t, dir, completed, 8192)

	selectedDiskBytes := fixtureDiskBytes(t, dir, selected)
	removed, freed, err := s.PurgeOrphaned([]string{buildID(selected)})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 || freed != selectedDiskBytes {
		t.Fatalf("removed=%d freed=%d", removed, freed)
	}
	if _, err := os.Stat(filepath.Join(dir, selected)); !os.IsNotExist(err) {
		t.Fatalf("selected orphan still exists: %v", err)
	}
	for _, name := range []string{other, completed} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("unselected artifact %q was changed: %v", name, err)
		}
	}
}

func TestStorageSeparatesCompletedInvalidAndOrphanedBytes(t *testing.T) {
	dir := t.TempDir()
	s := &Service{backupDir: dir, jobs: make(map[string]*Job)}
	writeBackupFixture(t, dir, "backup-a-partial-files.tar.gz", 2048)
	writeBackupFixture(t, dir, "backup-b-full.tar.gz", 4096)
	writeBackupFixture(t, dir, "backup-c-db-all-postgresql.sql.gz", 1)
	orphanedBytes := fixtureDiskBytes(t, dir, "backup-a-partial-files.tar.gz")
	completedBytes := fixtureDiskBytes(t, dir, "backup-b-full.tar.gz")
	invalidBytes := fixtureDiskBytes(t, dir, "backup-c-db-all-postgresql.sql.gz")

	storage, err := s.Storage()
	if err != nil {
		t.Fatal(err)
	}
	if storage.OrphanedCount != 1 || storage.OrphanedBytes != orphanedBytes {
		t.Fatalf("orphaned summary = %+v", storage)
	}
	if storage.CompletedCount != 1 || storage.CompletedBytes != completedBytes {
		t.Fatalf("completed summary = %+v", storage)
	}
	if storage.InvalidCount != 1 || storage.InvalidBytes != invalidBytes {
		t.Fatalf("invalid summary = %+v", storage)
	}
	if storage.TotalBytes != orphanedBytes+completedBytes+invalidBytes {
		t.Fatalf("total bytes = %d", storage.TotalBytes)
	}
}

func TestPurgeOrphanedRejectsCompletedBackup(t *testing.T) {
	dir := t.TempDir()
	s := &Service{backupDir: dir, jobs: make(map[string]*Job)}
	name := "backup-c-full.tar.gz"
	writeBackupFixture(t, dir, name, 2048)

	if _, _, err := s.PurgeOrphaned([]string{buildID(name)}); err == nil {
		t.Fatal("expected completed backup deletion to be rejected")
	}
	if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
		t.Fatalf("completed backup was changed: %v", err)
	}
}

func TestRestoreRejectsOrphanedArtifactBeforeExtraction(t *testing.T) {
	dir := t.TempDir()
	s := &Service{backupDir: dir, jobs: make(map[string]*Job)}
	name := "backup-a-partial-files.tar.gz"
	writeBackupFixture(t, dir, name, 2048)

	if _, err := s.Restore(buildID(name)); err == nil {
		t.Fatal("expected orphaned artifact restore to be rejected")
	}
}

func TestRequiredBackupBytesIncludesWorkingCopyAndReserve(t *testing.T) {
	const source = uint64(10 * 1024 * 1024 * 1024)
	if got := requiredBackupBytes("database", source); got != backupReserve {
		t.Fatalf("database required bytes = %d", got)
	}
	if got := requiredBackupBytes("files", source); got != source+backupReserve {
		t.Fatalf("files required bytes = %d", got)
	}
	if got := requiredBackupBytes("full", source); got != source*2+backupReserve {
		t.Fatalf("full required bytes = %d", got)
	}
}

func TestFullPartialPathsAreExactAndScopedToBackupDir(t *testing.T) {
	dir := t.TempDir()
	s := &Service{backupDir: dir}
	paths := s.fullPartialPaths("nightly", CreateOptions{})
	want := []string{
		filepath.Join(dir, "nightly-partial-db-all-postgresql.sql.gz"),
		filepath.Join(dir, "nightly-partial-files.tar.gz"),
	}
	if len(paths) != len(want) {
		t.Fatalf("paths = %v", paths)
	}
	for i := range want {
		if paths[i] != want[i] {
			t.Fatalf("paths[%d] = %q, want %q", i, paths[i], want[i])
		}
	}
}
