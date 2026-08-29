package backup

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPurgeInvalid_removesStubFiles(t *testing.T) {
	dir := t.TempDir()
	s := &Service{backupDir: dir, jobs: make(map[string]*Job)}
	name := "backup-20260101000000-db-all-postgresql.sql.gz"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	n, freed, err := s.PurgeInvalid()
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || freed != 1 {
		t.Fatalf("removed=%d freed=%d", n, freed)
	}
}
