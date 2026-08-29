package disk

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestRemoveCachePathsRemovesOnlySelectedEntries(t *testing.T) {
	root := t.TempDir()
	selected := filepath.Join(root, "_cacache")
	selectedFile := filepath.Join(selected, "content", "artifact")
	sibling := filepath.Join(root, "config")
	if err := os.MkdirAll(filepath.Dir(selectedFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(selectedFile, []byte("cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sibling, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := removeCachePaths([]string{selected}); err != nil {
		t.Fatalf("removeCachePaths: %v", err)
	}
	if _, err := os.Stat(selected); !os.IsNotExist(err) {
		t.Fatalf("selected cache still exists: %v", err)
	}
	if data, err := os.ReadFile(sibling); err != nil || string(data) != "preserve" {
		t.Fatalf("sibling changed: data=%q err=%v", data, err)
	}
}

func TestRemoveCachePathsRejectsFilesystemRoot(t *testing.T) {
	if err := removeCachePaths([]string{"/"}); err == nil {
		t.Fatal("expected root path to be rejected")
	}
}

func TestGoBuildCacheTargetUsesOnlyCompilerCache(t *testing.T) {
	want := []string{"/root/.cache/go-build"}
	if !slices.Equal(goBuildCachePaths, want) {
		t.Fatalf("goBuildCachePaths = %v, want %v", goBuildCachePaths, want)
	}
	if !IsCleanupTarget("go-build-cache") {
		t.Fatal("go-build-cache must be an allowed cleanup target")
	}
}

func TestTemporaryCleanupTargetsAreFixedAndIncludeVarTmp(t *testing.T) {
	want := map[string]string{"tmp": "/tmp", "var-tmp": "/var/tmp"}
	if len(temporaryCleanupPaths) != len(want) {
		t.Fatalf("temporaryCleanupPaths = %v, want %v", temporaryCleanupPaths, want)
	}
	for id, path := range want {
		if temporaryCleanupPaths[id] != path || !IsCleanupTarget(id) {
			t.Fatalf("temporary target %q = %q, allowed=%v", id, temporaryCleanupPaths[id], IsCleanupTarget(id))
		}
	}
}

func TestOldHServerBinariesKeepNewestFive(t *testing.T) {
	root := t.TempDir()
	base := time.Date(2026, 8, 26, 0, 0, 0, 0, time.UTC)
	for index := 0; index < 7; index++ {
		path := filepath.Join(root, "hserver-panel.pre-test-"+string(rune('a'+index)))
		content := make([]byte, index+1)
		if err := os.WriteFile(path, content, 0o700); err != nil {
			t.Fatal(err)
		}
		modified := base.Add(time.Duration(index) * time.Minute)
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
	}

	paths, size := oldHServerBinariesIn(root, 5)
	wantPaths := []string{
		filepath.Join(root, "hserver-panel.pre-test-a"),
		filepath.Join(root, "hserver-panel.pre-test-b"),
	}
	if !slices.Equal(paths, wantPaths) || size != 3 {
		t.Fatalf("paths=%v size=%d, want paths=%v size=3", paths, size, wantPaths)
	}
}

func TestHServerTemporaryArtifactsSelectOnlyFixedDirectChildren(t *testing.T) {
	root := t.TempDir()
	targets := map[string]string{
		"hserver-panel-release-1": "panel-build",
		"hserver-go-cache":        "go-cache",
	}
	for name, content := range targets {
		path := filepath.Join(root, name, "artifact")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	for _, name := range []string{"hserver-panel", "unrelated-build", "go-cache", "go-build123"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte("preserve"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	paths, size := hserverTemporaryArtifacts(root)
	wantPaths := make([]string, 0, len(targets))
	var wantSize uint64
	for name, content := range targets {
		wantPaths = append(wantPaths, filepath.Join(root, name))
		wantSize += uint64(len(content))
	}
	slices.Sort(wantPaths)
	if !slices.Equal(paths, wantPaths) {
		t.Fatalf("paths = %v, want %v", paths, wantPaths)
	}
	if size != wantSize {
		t.Fatalf("size = %d, want %d", size, wantSize)
	}
}

func TestRemoveHServerTemporaryArtifactsDoesNotFollowSymlinksOrDeleteSiblings(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "must-remain")
	if err := os.WriteFile(outsideFile, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}

	targetDir := filepath.Join(root, "hserver-panel-old")
	if err := os.MkdirAll(targetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, "binary"), []byte("discard"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "hserver-panel-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	sibling := filepath.Join(root, "operator-data")
	if err := os.WriteFile(sibling, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	count, err := removeHServerTemporaryArtifacts(root)
	if err != nil {
		t.Fatalf("removeHServerTemporaryArtifacts: %v", err)
	}
	if count != 2 {
		t.Fatalf("count = %d, want 2", count)
	}
	for _, removed := range []string{targetDir, link} {
		if _, err := os.Lstat(removed); !os.IsNotExist(err) {
			t.Fatalf("target still exists: %s (%v)", removed, err)
		}
	}
	if data, err := os.ReadFile(outsideFile); err != nil || string(data) != "preserve" {
		t.Fatalf("symlink target changed: data=%q err=%v", data, err)
	}
	if data, err := os.ReadFile(sibling); err != nil || string(data) != "keep" {
		t.Fatalf("sibling changed: data=%q err=%v", data, err)
	}
}
