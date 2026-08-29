// Package rcloneprofile centralizes Google Drive + restic tuning for hserver-panel.
//
// Sources: rclone drive backend defaults (/rclone/rclone), restic RESTIC_PACK_SIZE
// and rclone.connections (/restic/restic), restic+rclone+Drive forum guidance
// (pacer 125ms/75 burst, 64Mi packs, use_trash=false, fast_list).
package rcloneprofile

import (
	"fmt"
	"strings"
)

// RemoteName is the single rclone remote used by GDrive uploads and restic snapshots.
const RemoteName = "hserver-gdrive"

// DriveTuning holds Google Drive remote options written to rclone.conf.
type DriveTuning struct {
	ChunkSize        string
	UploadCutoff     string
	PacerMinSleep    string
	PacerBurst       int
	UseTrash         bool
	FastList         bool
	AcknowledgeAbuse bool
	ListChunk        int
}

// DefaultDriveTuning returns settings tuned for restic incremental backups on Drive.
func DefaultDriveTuning() DriveTuning {
	return DriveTuning{
		ChunkSize:        "64Mi",
		UploadCutoff:     "64Mi",
		PacerMinSleep:    "125ms",
		PacerBurst:       75,
		UseTrash:         false,
		FastList:         true,
		AcknowledgeAbuse: true,
		ListChunk:        1000,
	}
}

// RenderDriveRemoteConfig builds the [remote] section for rclone.conf.
func RenderDriveRemoteConfig(remoteName, tokenJSON string, t DriveTuning) string {
	useTrash := "false"
	if t.UseTrash {
		useTrash = "true"
	}
	fastList := "false"
	if t.FastList {
		fastList = "true"
	}
	ackAbuse := "false"
	if t.AcknowledgeAbuse {
		ackAbuse = "true"
	}
	return fmt.Sprintf(`[%s]
type = drive
scope = drive.file
token = %s
chunk_size = %s
upload_cutoff = %s
acknowledge_abuse = %s
pacer_min_sleep = %s
pacer_burst = %d
use_trash = %s
fast_list = %s
list_chunk = %d
`, remoteName, tokenJSON,
		t.ChunkSize, t.UploadCutoff, ackAbuse,
		t.PacerMinSleep, t.PacerBurst, useTrash, fastList, t.ListChunk)
}

// ResticPackSizeMiB is the pack file size for restic (fewer blobs on Drive).
const ResticPackSizeMiB = 64

// ResticRcloneConnections limits parallel rclone backend calls (restic default 5).
const ResticRcloneConnections = 4

// ResticEnvExtras returns env vars for restic subprocesses (no duplicate drive tuning).
func ResticEnvExtras() []string {
	return []string{
		"RCLONE_RETRIES=10",
		"RCLONE_LOW_LEVEL_RETRIES=20",
		"RCLONE_TIMEOUT=5m",
		fmt.Sprintf("RESTIC_PACK_SIZE=%d", ResticPackSizeMiB),
	}
}

// ResticGlobalOptions are appended to every restic CLI invocation.
func ResticGlobalOptions() []string {
	return []string{
		"--pack-size", fmt.Sprintf("%d", ResticPackSizeMiB),
		"-o", fmt.Sprintf("rclone.connections=%d", ResticRcloneConnections),
	}
}

// CLICopyFlags are prepended to rclone copy/lsjson for large file uploads.
func CLICopyFlags() []string {
	return []string{
		"--drive-chunk-size", "64M",
		"--transfers", "4",
		"--checkers", "8",
		"--tpslimit", "10",
		"--tpslimit-burst", "20",
	}
}

// JoinCLI builds a human-readable command string for job logs.
func JoinCLI(parts ...string) string {
	return strings.Join(parts, " ")
}
