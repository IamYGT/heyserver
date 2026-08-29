package settings

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

func newTestService(t *testing.T) *Service {
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

	repo := store.NewSettingsRepository(db)
	return New(repo, "test-panel-1.0.0")
}

func TestServiceGetDefault(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	got, err := svc.Get("missing.key", "fallback")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "fallback" {
		t.Fatalf("Get = %q, want fallback", got)
	}
}

func TestServiceSetAndGet(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	if err := svc.Set("app.theme", "dark"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := svc.Get("app.theme", "light")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "dark" {
		t.Fatalf("Get = %q, want dark", got)
	}
}

func TestServiceSetManyAndGetAll(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	pairs := map[string]string{
		"mail.host": "localhost",
		"mail.port": "25",
	}
	if err := svc.SetMany(pairs); err != nil {
		t.Fatalf("SetMany: %v", err)
	}

	all, err := svc.GetAll()
	if err != nil {
		t.Fatalf("GetAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("GetAll len = %d, want 2", len(all))
	}
}

func TestServiceContextVariantsHonorCancellation(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := svc.GetContext(ctx, "missing.key", "fallback"); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetContext error = %v, want context.Canceled", err)
	}
	if _, err := svc.GetAllContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetAllContext error = %v, want context.Canceled", err)
	}
	if _, err := svc.EditableSettingsContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("EditableSettingsContext error = %v, want context.Canceled", err)
	}
}

func TestServiceDelete(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	if err := svc.Set("temp.key", "value"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := svc.Delete("temp.key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err := svc.Get("temp.key", "gone")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != "gone" {
		t.Fatalf("Get after delete = %q, want gone", got)
	}
}

func TestServiceHealth(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	health := svc.Health()
	if health.Status != "ok" {
		t.Fatalf("Status = %q, want ok", health.Status)
	}
	if health.Version != "test-panel-1.0.0" {
		t.Fatalf("Version = %q", health.Version)
	}
	if health.Uptime < 0 {
		t.Fatalf("Uptime = %d", health.Uptime)
	}
}

func TestServiceSystemInfo(t *testing.T) {
	t.Parallel()

	svc := newTestService(t)
	info := svc.SystemInfo()

	if info.Hostname == "" {
		t.Fatal("Hostname is empty")
	}
	if info.Arch == "" {
		t.Fatal("Arch is empty")
	}
	if info.BootID == "" {
		t.Fatal("BootID is empty")
	}
	if info.GoVersion == "" {
		t.Fatal("GoVersion is empty")
	}
	if info.PanelVersion != "test-panel-1.0.0" {
		t.Fatalf("PanelVersion = %q", info.PanelVersion)
	}
	if info.OS == "" {
		t.Fatal("OS is empty")
	}
	if info.PHP == nil {
		t.Fatal("PHP inventory is null")
	}
	if info.Interfaces == nil {
		t.Fatal("network interface inventory is null")
	}
	for _, iface := range info.Interfaces {
		if iface.Addrs == nil {
			t.Fatalf("addresses for interface %q are null", iface.Name)
		}
	}
}

func TestReadOS(t *testing.T) {
	t.Parallel()

	osName := readOS()
	if osName == "" {
		t.Fatal("readOS returned empty string")
	}
}

func TestSysHostname(t *testing.T) {
	t.Parallel()

	host := sysHostname()
	if host == "" {
		t.Fatal("sysHostname returned empty")
	}
}

func TestNetworkInterfaces(t *testing.T) {
	t.Parallel()

	ifaces := networkInterfaces()
	// May be empty in restricted environments; just ensure no panic and valid shape.
	for _, iface := range ifaces {
		if iface.Name == "" {
			t.Fatal("interface name is empty")
		}
	}
}
