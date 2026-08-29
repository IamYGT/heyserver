package db

import (
	"database/sql"
	"os"
	"testing"
)

var testDB *sql.DB

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "hserver-db-test-*")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	dbPath := dir + "/test.db"
	testDB, err = Open(dbPath)
	if err != nil {
		panic(err)
	}

	os.Exit(m.Run())
}

func TestOpenRunsMigrations(t *testing.T) {
	t.Parallel()

	var count int
	if err := testDB.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count); err != nil {
		t.Fatalf("schema_migrations query: %v", err)
	}
	if count < 1 {
		t.Fatalf("expected at least 1 migration recorded, got %d", count)
	}

	var maxVersion int
	if err := testDB.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&maxVersion); err != nil {
		t.Fatalf("max migration version: %v", err)
	}
	if maxVersion < len(migrations) {
		t.Fatalf("expected migration version >= %d, got %d", len(migrations), maxVersion)
	}
}

func TestSchemaMigrationsTableExists(t *testing.T) {
	t.Parallel()

	var name string
	err := testDB.QueryRow(
		`SELECT name FROM sqlite_master WHERE type='table' AND name='schema_migrations'`,
	).Scan(&name)
	if err != nil {
		t.Fatalf("schema_migrations table missing: %v", err)
	}
	if name != "schema_migrations" {
		t.Fatalf("unexpected table name: %q", name)
	}
}

func TestUsersTableColumns(t *testing.T) {
	t.Parallel()

	want := []string{
		"id", "email", "name", "password", "role",
		"created_at", "updated_at", "totp_secret", "totp_enabled",
	}

	rows, err := testDB.Query(`PRAGMA table_info(users)`)
	if err != nil {
		t.Fatalf("pragma table_info(users): %v", err)
	}
	defer func() { _ = rows.Close() }()

	got := make(map[string]bool)
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var dfltValue sql.NullString
		var pk int
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan column info: %v", err)
		}
		got[name] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows err: %v", err)
	}

	for _, col := range want {
		if !got[col] {
			t.Errorf("users table missing column %q", col)
		}
	}
}
