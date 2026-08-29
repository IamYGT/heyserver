package store_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/IamYGT/heyserver/internal/store"

	_ "github.com/mattn/go-sqlite3"
)

func newSettingsRepo(t *testing.T) (*store.SettingsRepository, *sql.DB) {
	t.Helper()

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "settings.db")
	dsn := fmt.Sprintf("file:%s?_journal_mode=WAL&_foreign_keys=on", dbPath)

	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if err := store.MigrateSettings(db); err != nil {
		t.Fatalf("MigrateSettings: %v", err)
	}

	return store.NewSettingsRepository(db), db
}

func TestSettingsRepository_GetMissing(t *testing.T) {
	t.Parallel()

	repo, _ := newSettingsRepo(t)
	got, err := repo.Get("missing.key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != nil {
		t.Fatalf("Get missing key: got %+v want nil", got)
	}
}

func TestSettingsRepository_SetAndGet(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		key   string
		value string
	}{
		{"simple", "app.theme", "dark"},
		{"empty_value", "app.empty", ""},
		{"unicode", "app.label", "Merhaba — dünya"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repo, _ := newSettingsRepo(t)
			if err := repo.Set(tc.key, tc.value); err != nil {
				t.Fatalf("Set: %v", err)
			}

			got, err := repo.Get(tc.key)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got == nil {
				t.Fatal("Get returned nil")
			}
			if got.Key != tc.key {
				t.Errorf("Key: got %q want %q", got.Key, tc.key)
			}
			if got.Value != tc.value {
				t.Errorf("Value: got %q want %q", got.Value, tc.value)
			}
			if got.UpdatedAt.IsZero() {
				t.Error("UpdatedAt should be set")
			}
		})
	}
}

func TestSettingsRepository_SetUpsert(t *testing.T) {
	t.Parallel()

	repo, _ := newSettingsRepo(t)
	key := "notify.email"

	if err := repo.Set(key, "first@example.com"); err != nil {
		t.Fatalf("Set first: %v", err)
	}
	if err := repo.Set(key, "second@example.com"); err != nil {
		t.Fatalf("Set second: %v", err)
	}

	got, err := repo.Get(key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Value != "second@example.com" {
		t.Errorf("Value: got %q want %q", got.Value, "second@example.com")
	}
}

func TestSettingsRepository_Delete(t *testing.T) {
	t.Parallel()

	repo, _ := newSettingsRepo(t)
	key := "temp.flag"

	if err := repo.Set(key, "1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := repo.Delete(key); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	got, err := repo.Get(key)
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if got != nil {
		t.Fatalf("Get after delete: got %+v want nil", got)
	}

	// Deleting again should be a no-op.
	if err := repo.Delete(key); err != nil {
		t.Fatalf("Delete missing key: %v", err)
	}
}

func TestSettingsRepository_SetManyAndGetAll(t *testing.T) {
	t.Parallel()

	repo, _ := newSettingsRepo(t)
	pairs := map[string]string{
		"alpha": "1",
		"beta":  "2",
		"gamma": "3",
	}

	if err := repo.SetMany(pairs); err != nil {
		t.Fatalf("SetMany: %v", err)
	}

	all, err := repo.GetAll()
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(all) != len(pairs) {
		t.Fatalf("GetAll: got %d items want %d", len(all), len(pairs))
	}

	seen := make(map[string]string, len(all))
	for _, s := range all {
		seen[s.Key] = s.Value
	}
	for k, want := range pairs {
		if got, ok := seen[k]; !ok {
			t.Errorf("GetAll missing key %q", k)
		} else if got != want {
			t.Errorf("key %q: got %q want %q", k, got, want)
		}
	}

	// Keys should be ordered ascending.
	for i := 1; i < len(all); i++ {
		if all[i-1].Key > all[i].Key {
			t.Errorf("GetAll not sorted: %q before %q", all[i-1].Key, all[i].Key)
		}
	}
}

func TestSettingsRepository_ContextCancellation(t *testing.T) {
	t.Parallel()

	repo, _ := newSettingsRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := repo.GetContext(ctx, "missing.key"); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetContext error = %v, want context.Canceled", err)
	}
	if _, err := repo.GetAllContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetAllContext error = %v, want context.Canceled", err)
	}
}
