package snapshot

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSettingsDistinguishesMissingFromInvalid(t *testing.T) {
	t.Run("missing uses defaults", func(t *testing.T) {
		s := &Service{dataDir: t.TempDir()}
		settings, err := s.loadSettings()
		if err != nil {
			t.Fatal(err)
		}
		if settings.KeepDaily != 14 || settings.KeepWeekly != 8 || settings.KeepMonthly != 6 {
			t.Fatalf("settings=%+v", settings)
		}
		if settings.Destination != DestinationGoogleDrive {
			t.Fatalf("legacy default destination=%q", settings.Destination)
		}
	})

	for _, test := range []struct {
		name  string
		setup func(*testing.T, string)
	}{
		{
			name: "corrupt",
			setup: func(t *testing.T, path string) {
				if err := os.WriteFile(path, []byte("{not-json"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "unreadable path",
			setup: func(t *testing.T, path string) {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			test.setup(t, filepath.Join(dir, "snapshot-settings.json"))
			s := &Service{dataDir: dir}
			if _, err := s.loadSettings(); !errors.Is(err, ErrSettingsUnavailable) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestInvalidSettingsStopSnapshotOperations(t *testing.T) {
	dir := t.TempDir()
	settingsPath := filepath.Join(dir, "snapshot-settings.json")
	original := []byte("{not-json")
	if err := os.WriteFile(settingsPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Service{dataDir: dir}

	operations := []struct {
		name string
		run  func() error
	}{
		{name: "status", run: func() error { _, err := s.Status("", true, false); return err }},
		{name: "update settings", run: func() error {
			return s.UpdateSettings(SettingsUpdate{RepoFolder: "hserver-snapshots", KeepDaily: 7, KeepWeekly: 8, KeepMonthly: 6})
		}},
		{name: "run", run: func() error { _, err := s.RunAsync("manual"); return err }},
		{name: "purge", run: func() error {
			return s.PurgeRemoteRepo(PurgeRequest{RepoFolder: defaultRepoFolder, Confirmation: PurgeConfirmation}, "")
		}},
		{name: "list", run: func() error { _, err := s.ListSnapshots("", 5); return err }},
		{name: "restore", run: func() error { _, err := s.RestoreAsync(RestoreRequest{SnapshotID: "abcdef12"}, ""); return err }},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.run(); !errors.Is(err, ErrSettingsUnavailable) {
				t.Fatalf("error=%v", err)
			}
		})
	}

	after, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(original) {
		t.Fatalf("invalid settings changed: %q", after)
	}
}
