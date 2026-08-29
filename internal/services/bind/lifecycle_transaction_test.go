package bind

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyZoneCreateTransactionCommitsBothFiles(t *testing.T) {
	t.Parallel()

	configPath, zonePath := writeLifecycleFixture(t, false)
	configContent := []byte("// local\nzone \"example.com\" { file \"" + zonePath + "\"; };\n")
	zoneContent := []byte("valid zone\n")
	validatedConfig := false
	validatedZone := false
	reloadCalls := 0

	err := applyZoneCreateTransaction(
		configPath,
		zonePath,
		configContent,
		zoneContent,
		func(candidate string) error {
			validatedConfig = readLifecycleFile(t, candidate) == string(configContent)
			return nil
		},
		func(candidate string) error {
			validatedZone = readLifecycleFile(t, candidate) == string(zoneContent)
			return nil
		},
		func() error { reloadCalls++; return nil },
		nil,
	)
	if err != nil {
		t.Fatalf("applyZoneCreateTransaction() error: %v", err)
	}
	if !validatedConfig || !validatedZone || reloadCalls != 1 {
		t.Fatalf("validated config=%v zone=%v reloads=%d", validatedConfig, validatedZone, reloadCalls)
	}
	if got := readLifecycleFile(t, configPath); got != string(configContent) {
		t.Fatalf("config=%q, want %q", got, configContent)
	}
	if got := readLifecycleFile(t, zonePath); got != string(zoneContent) {
		t.Fatalf("zone=%q, want %q", got, zoneContent)
	}
	assertNoLifecycleTemps(t, filepath.Dir(zonePath))
}

func TestApplyZoneCreateTransactionClearsDurableJournal(t *testing.T) {
	configPath, zonePath := writeLifecycleFixture(t, false)
	store := newLifecycleJournalStore(filepath.Join(t.TempDir(), "state"))
	err := applyZoneCreateTransaction(
		configPath,
		zonePath,
		[]byte("changed config\n"),
		[]byte("valid zone\n"),
		func(string) error { return nil },
		func(string) error { return nil },
		func() error { return nil },
		store,
	)
	if err != nil {
		t.Fatalf("applyZoneCreateTransaction() error: %v", err)
	}
	assertJournalCleared(t, store)
}

func TestApplyZoneCreateTransactionRejectsCandidateBeforeMutation(t *testing.T) {
	t.Parallel()

	configPath, zonePath := writeLifecycleFixture(t, false)
	reloaded := false
	err := applyZoneCreateTransaction(
		configPath,
		zonePath,
		[]byte("changed config\n"),
		[]byte("invalid zone\n"),
		func(string) error { return nil },
		func(string) error { return errors.New("invalid zone") },
		func() error { reloaded = true; return nil },
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "staged zone validation failed") {
		t.Fatalf("error=%v, want staged validation failure", err)
	}
	if reloaded {
		t.Fatal("reload ran after validation failure")
	}
	if got := readLifecycleFile(t, configPath); got != "// local\n" {
		t.Fatalf("config changed to %q", got)
	}
	if _, statErr := os.Stat(zonePath); !os.IsNotExist(statErr) {
		t.Fatalf("zone file exists after validation failure: %v", statErr)
	}
}

func TestApplyZoneCreateTransactionRollsBackReloadFailure(t *testing.T) {
	t.Parallel()

	configPath, zonePath := writeLifecycleFixture(t, false)
	reloadCalls := 0
	err := applyZoneCreateTransaction(
		configPath,
		zonePath,
		[]byte("changed config\n"),
		[]byte("valid zone\n"),
		func(string) error { return nil },
		func(string) error { return nil },
		func() error {
			reloadCalls++
			if reloadCalls == 1 {
				return errors.New("rndc reload failed")
			}
			return nil
		},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "created zone and configuration rolled back and reloaded") {
		t.Fatalf("error=%v, want create rollback detail", err)
	}
	if reloadCalls != 2 {
		t.Fatalf("reload calls=%d, want 2", reloadCalls)
	}
	if got := readLifecycleFile(t, configPath); got != "// local\n" {
		t.Fatalf("config=%q after rollback", got)
	}
	if _, statErr := os.Stat(zonePath); !os.IsNotExist(statErr) {
		t.Fatalf("zone file exists after rollback: %v", statErr)
	}
}

func TestApplyZoneDeleteTransactionCommitsBothFiles(t *testing.T) {
	t.Parallel()

	configPath, zonePath := writeLifecycleFixture(t, true)
	reloadCalls := 0
	err := applyZoneDeleteTransaction(
		configPath,
		zonePath,
		[]byte("// local\n"),
		func(candidate string) error {
			if got := readLifecycleFile(t, candidate); got != "// local\n" {
				return errors.New("unexpected staged config")
			}
			return nil
		},
		func() error { reloadCalls++; return nil },
		nil,
	)
	if err != nil {
		t.Fatalf("applyZoneDeleteTransaction() error: %v", err)
	}
	if reloadCalls != 1 {
		t.Fatalf("reload calls=%d, want 1", reloadCalls)
	}
	if got := readLifecycleFile(t, configPath); got != "// local\n" {
		t.Fatalf("config=%q after delete", got)
	}
	if _, statErr := os.Stat(zonePath); !os.IsNotExist(statErr) {
		t.Fatalf("zone still exists after delete: %v", statErr)
	}
	assertNoLifecycleTemps(t, filepath.Dir(zonePath))
}

func TestApplyZoneDeleteTransactionRestoresReloadFailure(t *testing.T) {
	t.Parallel()

	configPath, zonePath := writeLifecycleFixture(t, true)
	originalConfig := readLifecycleFile(t, configPath)
	originalZone := readLifecycleFile(t, zonePath)
	reloadCalls := 0
	err := applyZoneDeleteTransaction(
		configPath,
		zonePath,
		[]byte("// local\n"),
		func(string) error { return nil },
		func() error {
			reloadCalls++
			if reloadCalls == 1 {
				return errors.New("rndc reload failed")
			}
			return nil
		},
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "deleted zone and configuration restored and reloaded") {
		t.Fatalf("error=%v, want delete rollback detail", err)
	}
	if reloadCalls != 2 {
		t.Fatalf("reload calls=%d, want 2", reloadCalls)
	}
	if got := readLifecycleFile(t, configPath); got != originalConfig {
		t.Fatalf("config=%q, want restored %q", got, originalConfig)
	}
	if got := readLifecycleFile(t, zonePath); got != originalZone {
		t.Fatalf("zone=%q, want restored %q", got, originalZone)
	}
	assertNoLifecycleTemps(t, filepath.Dir(zonePath))
}

func writeLifecycleFixture(t *testing.T, withZone bool) (string, string) {
	t.Helper()
	root := t.TempDir()
	zonesDir := filepath.Join(root, "zones")
	if err := os.Mkdir(zonesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	zonePath := filepath.Join(zonesDir, "db.example.com")
	configPath := filepath.Join(root, "named.conf.local")
	config := "// local\n"
	if withZone {
		config += "zone \"example.com\" { file \"" + zonePath + "\"; };\n"
		if err := os.WriteFile(zonePath, []byte("original zone\n"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(configPath, []byte(config), 0o640); err != nil {
		t.Fatal(err)
	}
	return configPath, zonePath
}

func readLifecycleFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func assertNoLifecycleTemps(t *testing.T, dir string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".*.hserver-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary lifecycle files remain: %v", matches)
	}
}
