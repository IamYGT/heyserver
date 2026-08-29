package snapshot

import (
	"errors"
	"os"
	"reflect"
	"testing"
)

func TestUpdateSettingsRejectsInvalidPolicyWithoutReplacingCurrentFile(t *testing.T) {
	dir := t.TempDir()
	s := &Service{dataDir: dir, vhostsRoot: "/srv/www"}
	valid := SettingsUpdate{
		RepoFolder:           "agency/snapshots",
		EnabledPaths:         []string{"vhosts", "nginx"},
		KeepDaily:            14,
		KeepWeekly:           8,
		KeepMonthly:          6,
		PasswordAcknowledged: true,
	}
	if err := s.UpdateSettings(valid); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(s.settingsFile())
	if err != nil {
		t.Fatal(err)
	}

	invalidPolicies := []SettingsUpdate{
		{Destination: "ftp", RepoFolder: "snapshots", EnabledPaths: []string{"vhosts"}, KeepDaily: 14, KeepWeekly: 8, KeepMonthly: 6},
		{RepoFolder: "../other", EnabledPaths: []string{"vhosts"}, KeepDaily: 14, KeepWeekly: 8, KeepMonthly: 6},
		{RepoFolder: "snapshots", EnabledPaths: []string{"unknown"}, KeepDaily: 14, KeepWeekly: 8, KeepMonthly: 6},
		{RepoFolder: "snapshots", EnabledPaths: []string{"vhosts", "vhosts"}, KeepDaily: 14, KeepWeekly: 8, KeepMonthly: 6},
		{RepoFolder: "snapshots", EnabledPaths: []string{"vhosts"}, KeepDaily: 0, KeepWeekly: 8, KeepMonthly: 6},
		{RepoFolder: "snapshots", EnabledPaths: []string{"vhosts"}, KeepDaily: 14, KeepWeekly: 261, KeepMonthly: 6},
	}
	for _, policy := range invalidPolicies {
		if err := s.UpdateSettings(policy); !errors.Is(err, ErrInvalidSettings) {
			t.Fatalf("policy=%+v error=%v", policy, err)
		}
		after, err := os.ReadFile(s.settingsFile())
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(after, original) {
			t.Fatalf("invalid policy replaced settings: %s", after)
		}
	}
}

func TestRestoreIncludesResolveInstallationOwnedSelectors(t *testing.T) {
	s := &Service{dataDir: "/var/lib/hserver", vhostsRoot: "/srv/www"}
	valid := RestoreRequest{
		SnapshotID:  "abcdef1234567890",
		ManifestIDs: []string{"nginx"},
		Vhosts:      []string{"example.com"},
	}
	includes, err := s.restoreIncludes(valid)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(includes, []string{"/etc/nginx", "/srv/www/example.com"}) {
		t.Fatalf("includes=%v", includes)
	}

	invalidRequests := []RestoreRequest{
		{SnapshotID: "latest"},
		{SnapshotID: "--help"},
		{SnapshotID: "abcdef12", ManifestIDs: []string{"unknown"}},
		{SnapshotID: "abcdef12", ManifestIDs: []string{"root-crontab"}},
		{SnapshotID: "abcdef12", ManifestIDs: []string{"nginx", "nginx"}},
		{SnapshotID: "abcdef12", Vhosts: []string{"../private"}},
		{SnapshotID: "abcdef12", Vhosts: []string{"example.com", "example.com"}},
		{SnapshotID: "abcdef12", ManifestIDs: []string{"vhosts"}, Vhosts: []string{"example.com"}},
	}
	for _, request := range invalidRequests {
		if _, err := s.restoreIncludes(request); !errors.Is(err, ErrInvalidRestoreRequest) {
			t.Fatalf("request=%+v error=%v", request, err)
		}
	}
}
