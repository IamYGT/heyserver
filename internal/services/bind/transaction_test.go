package bind

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyZoneFileTransactionCommitsValidatedContent(t *testing.T) {
	t.Parallel()

	zoneFile := writeTransactionFixture(t, "original\n", 0o640)
	validated := false
	reloaded := 0
	err := applyZoneFileTransaction(zoneFile, []byte("replacement\n"), func(candidate string) error {
		content, readErr := os.ReadFile(candidate)
		if readErr != nil {
			return readErr
		}
		validated = string(content) == "replacement\n"
		return nil
	}, func() error {
		reloaded++
		return nil
	})
	if err != nil {
		t.Fatalf("applyZoneFileTransaction() error: %v", err)
	}
	if !validated || reloaded != 1 {
		t.Fatalf("validated=%v reload calls=%d, want true and 1", validated, reloaded)
	}
	assertFileContentAndMode(t, zoneFile, "replacement\n", 0o640)
}

func TestApplyZoneFileTransactionRejectsInvalidCandidate(t *testing.T) {
	t.Parallel()

	zoneFile := writeTransactionFixture(t, "original\n", 0o600)
	reloaded := false
	err := applyZoneFileTransaction(zoneFile, []byte("invalid\n"), func(string) error {
		return errors.New("named-checkzone rejected candidate")
	}, func() error {
		reloaded = true
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "staged zone validation failed") {
		t.Fatalf("applyZoneFileTransaction() error = %v, want validation failure", err)
	}
	if reloaded {
		t.Fatal("reload ran after validation failure")
	}
	assertFileContentAndMode(t, zoneFile, "original\n", 0o600)
}

func TestApplyZoneFileTransactionRestoresFileAfterReloadFailure(t *testing.T) {
	t.Parallel()

	zoneFile := writeTransactionFixture(t, "original\n", 0o644)
	reloadCalls := 0
	err := applyZoneFileTransaction(zoneFile, []byte("replacement\n"), func(string) error {
		return nil
	}, func() error {
		reloadCalls++
		if reloadCalls == 1 {
			return errors.New("rndc rejected replacement")
		}
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "original zone file restored and reloaded") {
		t.Fatalf("applyZoneFileTransaction() error = %v, want successful rollback detail", err)
	}
	if reloadCalls != 2 {
		t.Fatalf("reload calls=%d, want 2", reloadCalls)
	}
	assertFileContentAndMode(t, zoneFile, "original\n", 0o644)
}

func TestApplyZoneFileTransactionReportsRuntimeRollbackFailure(t *testing.T) {
	t.Parallel()

	zoneFile := writeTransactionFixture(t, "original\n", 0o644)
	err := applyZoneFileTransaction(zoneFile, []byte("replacement\n"), func(string) error {
		return nil
	}, func() error {
		return errors.New("rndc unavailable")
	})
	if err == nil || !strings.Contains(err.Error(), "runtime rollback reload failed") {
		t.Fatalf("applyZoneFileTransaction() error = %v, want runtime rollback failure", err)
	}
	assertFileContentAndMode(t, zoneFile, "original\n", 0o644)
}

func writeTransactionFixture(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "db.example.com")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertFileContentAndMode(t *testing.T, path, wantContent string, wantMode os.FileMode) {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != wantContent {
		t.Fatalf("content=%q, want %q", content, wantContent)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != wantMode {
		t.Fatalf("mode=%#o, want %#o", info.Mode().Perm(), wantMode)
	}
}
