package backup

import (
	"strings"
	"testing"
)

// TestCreateFullErrorMessage documents expected failure when no partials succeed.
// Full integration requires pg_dump/tar; this tests the error contract via statBackup edge.
func TestStatBackup_missingFileIsFailed(t *testing.T) {
	s := &Service{backupDir: t.TempDir()}
	info := s.statBackup("nonexistent-full.tar.gz", "full")
	if info.Status != "failed" {
		t.Errorf("status = %q, want failed", info.Status)
	}
}

func TestFrequencyToCron_invalidInput(t *testing.T) {
	_, err := FrequencyToCron("hourly", "03:00")
	if err == nil {
		t.Error("expected error for unknown frequency")
	}
	_, err = FrequencyToCron("daily", "invalid")
	if err == nil {
		t.Error("expected error for invalid time")
	}
}

func TestSanitize_backupName(t *testing.T) {
	got := sanitize("backup'; rm -rf /")
	if strings.Contains(got, "'") || strings.Contains(got, " ") {
		t.Errorf("sanitize should strip shell chars: %q", got)
	}
}
