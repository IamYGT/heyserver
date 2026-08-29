package snapshot

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPurgeRemoteRepoRequiresExactObservedIdentity(t *testing.T) {
	dir := t.TempDir()
	service := New(dir, filepath.Join(dir, "vhosts"), filepath.Join(dir, "backups"), 0, "", "", "", nil, nil)
	if err := service.UpdateSettings(SettingsUpdate{
		RepoFolder:           "agency/snapshots",
		EnabledPaths:         []string{"vhosts"},
		KeepDaily:            14,
		KeepWeekly:           8,
		KeepMonthly:          6,
		PasswordAcknowledged: true,
	}); err != nil {
		t.Fatal(err)
	}

	for _, request := range []PurgeRequest{
		{RepoFolder: "agency/snapshots"},
		{RepoFolder: "other/snapshots", Confirmation: PurgeConfirmation},
		{RepoFolder: "agency/snapshots", Confirmation: "PURGE"},
	} {
		if err := service.PurgeRemoteRepo(request, ""); !errors.Is(err, ErrInvalidPurgeRequest) {
			t.Fatalf("request=%+v error=%v", request, err)
		}
	}

	err := service.PurgeRemoteRepo(PurgeRequest{
		RepoFolder:   "agency/snapshots",
		Confirmation: PurgeConfirmation,
	}, "")
	if err == nil || !strings.Contains(err.Error(), "Google Drive is not configured") {
		t.Fatalf("valid request did not reach provider readiness boundary: %v", err)
	}
}

func TestPurgeRemoteRepoReportsUnsupportedS3Capability(t *testing.T) {
	dir := t.TempDir()
	service := NewWithS3(dir, filepath.Join(dir, "vhosts"), filepath.Join(dir, "backups"), 0, "", "", "password", nil, S3Config{}, nil)
	if err := service.UpdateSettings(SettingsUpdate{
		Destination:          DestinationS3,
		RepoFolder:           "agency/snapshots",
		EnabledPaths:         []string{"vhosts"},
		KeepDaily:            14,
		KeepWeekly:           8,
		KeepMonthly:          6,
		PasswordAcknowledged: true,
	}); err != nil {
		t.Fatal(err)
	}
	err := service.PurgeRemoteRepo(PurgeRequest{
		RepoFolder:   "agency/snapshots",
		Confirmation: PurgeConfirmation,
	}, "")
	if !errors.Is(err, ErrUnsupportedCapability) {
		t.Fatalf("error=%v", err)
	}
}

func TestPurgeRemoteRepoRejectsUnsafePersistedRepository(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "snapshot-settings.json"), []byte(`{"repoFolder":"../other","keepDaily":14,"keepWeekly":8,"keepMonthly":6}`), 0o600); err != nil {
		t.Fatal(err)
	}
	service := New(dir, "", filepath.Join(dir, "backups"), 0, "", "", "", nil, nil)
	err := service.PurgeRemoteRepo(PurgeRequest{RepoFolder: "../other", Confirmation: PurgeConfirmation}, "")
	if !errors.Is(err, ErrSettingsUnavailable) {
		t.Fatalf("error=%v", err)
	}
}

func TestListVhostsUsesInstallationOwnedRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "sites")
	for _, name := range []string{"example.com", "example.net", "system", "default"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o750); err != nil {
			t.Fatal(err)
		}
	}
	service := New(t.TempDir(), root, t.TempDir(), 0, "", "", "", nil, nil)
	names, err := service.ListVhosts()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(names, []string{"example.com", "example.net"}) {
		t.Fatalf("vhosts=%v", names)
	}
}

func TestListVhostsRequiresConfiguredRoot(t *testing.T) {
	service := New(t.TempDir(), "", t.TempDir(), 0, "", "", "", nil, nil)
	if _, err := service.ListVhosts(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("empty root error = %v, want ErrNotConfigured", err)
	}

	service = New(t.TempDir(), "relative/sites", t.TempDir(), 0, "", "", "", nil, nil)
	if _, err := service.ListVhosts(); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("relative root error = %v, want ErrNotConfigured", err)
	}
}
