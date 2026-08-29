package snapshot

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestRepoStatsJSON_parse(t *testing.T) {
	raw := `{"total_size":1048576,"total_file_size":5242880,"snapshot_count":3}`
	var parsed struct {
		TotalSize     int64 `json:"total_size"`
		TotalFileSize int64 `json:"total_file_size"`
		SnapshotCount int   `json:"snapshot_count"`
	}
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.TotalSize != 1048576 || parsed.TotalFileSize != 5242880 || parsed.SnapshotCount != 3 {
		t.Fatalf("unexpected parse: %+v", parsed)
	}
}

func TestSnapshotTimeout_defaultSixHours(t *testing.T) {
	t.Setenv("HSERVER_SNAPSHOT_TIMEOUT_HOURS", "")
	if got := snapshotTimeout(); got != 6*time.Hour {
		t.Fatalf("expected 6h, got %v", got)
	}
}

func TestResticRunner_cacheDirInEnv(t *testing.T) {
	dir := t.TempDir()
	r := &resticRunner{password: "x", cacheDir: dir}
	env := r.env()
	found := false
	for _, e := range env {
		if strings.HasPrefix(e, "RESTIC_CACHE_DIR=") {
			found = true
			if e != "RESTIC_CACHE_DIR="+dir {
				t.Fatalf("unexpected cache env: %s", e)
			}
		}
	}
	if !found {
		t.Fatal("RESTIC_CACHE_DIR missing from env")
	}
	packFound := false
	for _, e := range env {
		if e == "RESTIC_PACK_SIZE=64" {
			packFound = true
		}
	}
	if !packFound {
		t.Fatal("RESTIC_PACK_SIZE missing from env")
	}
}

func TestSnapshotTimeout_envOverride(t *testing.T) {
	t.Setenv("HSERVER_SNAPSHOT_TIMEOUT_HOURS", "12")
	if got := snapshotTimeout(); got != 12*time.Hour {
		t.Fatalf("expected 12h, got %v", got)
	}
}
