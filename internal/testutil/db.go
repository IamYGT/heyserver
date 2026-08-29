package testutil

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/IamYGT/heyserver/internal/db"
)

// SetupTestDB opens an isolated SQLite database for integration tests.
// Each call uses a unique temp file under t.TempDir().
func SetupTestDB(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hserver-test.db")
	if _, err := db.Open(path); err != nil {
		t.Fatalf("testutil.SetupTestDB: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(path) })
}
