package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiskCleanupControllerMeasuresAndRemovesOnlyAllowedExpiredFiles(t *testing.T) {
	root := t.TempDir()
	tmpRoot := filepath.Join(root, "tmp")
	logRoot := filepath.Join(root, "log")
	if err := os.MkdirAll(tmpRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(logRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	oldTmp := writeCleanupTestFile(t, tmpRoot, "old.cache", "12345", now.Add(-8*24*time.Hour))
	newTmp := writeCleanupTestFile(t, tmpRoot, "fresh.cache", "keep", now.Add(-time.Hour))
	oldLog := writeCleanupTestFile(t, logRoot, "app.log.2", "1234567", now.Add(-8*24*time.Hour))
	activeLog := writeCleanupTestFile(t, logRoot, "app.log", "keep", now.Add(-8*24*time.Hour))

	controller := newDiskCleanupController(&fakeRunner{}, map[string]struct{}{"tmp-old": {}, "rotated-logs": {}})
	controller.paths.temporary = []string{tmpRoot}
	controller.paths.logs = logRoot
	controller.now = func() time.Time { return now }

	targets, err := controller.Scan(context.Background())
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(targets) != 2 || targets[0].ID != "rotated-logs" || targets[0].Size != 7 || targets[1].ID != "tmp-old" || targets[1].Size != 5 {
		t.Fatalf("targets = %#v", targets)
	}
	execution, err := controller.Execute(context.Background(), []string{"tmp-old", "rotated-logs"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(execution.Results) != 2 || execution.Results[0].Reclaimed != 5 || execution.Results[1].Reclaimed != 7 {
		t.Fatalf("execution = %#v", execution)
	}
	for _, removed := range []string{oldTmp, oldLog} {
		if _, err := os.Stat(removed); !os.IsNotExist(err) {
			t.Fatalf("cleanup target still exists: %s", removed)
		}
	}
	for _, preserved := range []string{newTmp, activeLog} {
		if _, err := os.Stat(preserved); err != nil {
			t.Fatalf("preserved file missing: %s: %v", preserved, err)
		}
	}
}

func writeCleanupTestFile(t *testing.T, root, name, contents string, modified time.Time) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, modified, modified); err != nil {
		t.Fatal(err)
	}
	return path
}
