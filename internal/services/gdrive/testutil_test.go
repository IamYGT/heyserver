package gdrive

import (
	"database/sql"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"github.com/IamYGT/heyserver/internal/store"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MigrateSettings(db); err != nil {
		t.Fatal(err)
	}
	return db
}
