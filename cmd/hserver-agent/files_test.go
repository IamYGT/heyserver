package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileControllerBrowsesReadsAndWritesConfiguredRoots(t *testing.T) {
	root := t.TempDir()
	readRoot := filepath.Join(root, "apps")
	if err := os.MkdirAll(filepath.Join(readRoot, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(readRoot, "config.txt")
	original := []byte("before\n")
	if err := os.WriteFile(path, original, 0o640); err != nil {
		t.Fatal(err)
	}
	controller := newFileController([]string{readRoot}, []string{readRoot})
	entries, err := controller.Browse(context.Background(), readRoot)
	if err != nil || len(entries) != 2 || entries[0].Name != "nested" || entries[0].Type != "directory" {
		t.Fatalf("Browse = (%#v, %v)", entries, err)
	}
	file, err := controller.Read(context.Background(), path)
	if err != nil || file.Content != string(original) || file.Path != path {
		t.Fatalf("Read = (%#v, %v)", file, err)
	}
	digest := sha256.Sum256(original)
	backup, err := controller.Write(context.Background(), path, []byte("after\n"), fmt.Sprintf("%x", digest))
	if err != nil || !strings.Contains(backup, ".hserver-backup-") {
		t.Fatalf("Write = (%q, %v)", backup, err)
	}
	updated, err := os.ReadFile(path)
	if err != nil || string(updated) != "after\n" {
		t.Fatalf("updated = %q, %v", updated, err)
	}
	backedUp, err := os.ReadFile(backup)
	if err != nil || string(backedUp) != string(original) {
		t.Fatalf("backup = %q, %v", backedUp, err)
	}
}

func TestFileControllerRejectsSymlinkEscapesAndUnconfiguredWrites(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "allowed")
	outside := filepath.Join(root, "outside.txt")
	if err := os.MkdirAll(allowed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outside, []byte("outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(allowed, "escape.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	controller := newFileController([]string{allowed}, nil)
	if _, err := controller.Read(context.Background(), link); err == nil {
		t.Fatal("symlink escape was read")
	}
	if _, err := controller.Write(context.Background(), outside, []byte("changed\n"), strings.Repeat("a", 64)); err == nil {
		t.Fatal("write succeeded without roots")
	}
}
