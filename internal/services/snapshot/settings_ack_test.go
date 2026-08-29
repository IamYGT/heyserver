package snapshot

import (
	"os"
	"testing"
)

func TestUpdateSettings_passwordAcknowledged(t *testing.T) {
	dir := t.TempDir()
	s := &Service{dataDir: dir}
	policy := SettingsUpdate{
		RepoFolder:   "hserver-snapshots",
		EnabledPaths: []string{"vhosts", "nginx"},
		KeepDaily:    7,
		KeepWeekly:   8,
		KeepMonthly:  6,
	}
	if err := s.UpdateSettings(policy); err != nil {
		t.Fatal(err)
	}
	policy.PasswordAcknowledged = true
	if err := s.UpdateSettings(policy); err != nil {
		t.Fatal(err)
	}
	cur, err := s.loadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !cur.PasswordAcknowledged {
		t.Fatal("passwordAcknowledged should persist")
	}
	if cur.KeepDaily != 7 {
		t.Fatalf("keepDaily=%d want 7", cur.KeepDaily)
	}
	if len(cur.EnabledPaths) != 2 || cur.EnabledPaths[1] != "nginx" {
		t.Fatalf("enabledPaths=%v", cur.EnabledPaths)
	}
	info, err := os.Stat(s.settingsFile())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("settings mode=%o", info.Mode().Perm())
	}
	_ = os.Chmod(dir, 0o755)
}
