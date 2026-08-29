package rcloneprofile

import (
	"strings"
	"testing"
)

func TestRenderDriveRemoteConfig_resticTuning(t *testing.T) {
	cfg := RenderDriveRemoteConfig(RemoteName, `{"access_token":"x"}`, DefaultDriveTuning())
	for _, want := range []string{
		"[" + RemoteName + "]",
		"scope = drive.file",
		"chunk_size = 64Mi",
		"pacer_min_sleep = 125ms",
		"pacer_burst = 75",
		"use_trash = false",
		"fast_list = true",
		"list_chunk = 1000",
	} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("config missing %q:\n%s", want, cfg)
		}
	}
}

func TestResticEnvExtras_packSize(t *testing.T) {
	found := false
	for _, e := range ResticEnvExtras() {
		if e == "RESTIC_PACK_SIZE=64" {
			found = true
		}
		if strings.HasPrefix(e, "RCLONE_DRIVE_") {
			t.Fatalf("drive tuning must live in rclone.conf only, not env: %s", e)
		}
	}
	if !found {
		t.Fatal("RESTIC_PACK_SIZE missing")
	}
}

func TestResticGlobalOptions_connections(t *testing.T) {
	opts := ResticGlobalOptions()
	if len(opts) < 4 {
		t.Fatal(opts)
	}
	if opts[0] != "--pack-size" || opts[1] != "64" {
		t.Fatalf("unexpected pack-size: %v", opts)
	}
	if opts[2] != "-o" || opts[3] != "rclone.connections=4" {
		t.Fatalf("unexpected connections: %v", opts)
	}
}
