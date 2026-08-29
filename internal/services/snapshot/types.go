package snapshot

import "time"

// Destination selects the installation-owned remote repository provider.
type Destination string

const (
	DestinationGoogleDrive Destination = "gdrive"
	DestinationS3          Destination = "s3"
)

// DestinationStatus distinguishes an absent optional integration from one
// that is configured but currently unusable.
type DestinationStatus string

const (
	DestinationNotConfigured DestinationStatus = "not_configured"
	DestinationUnavailable   DestinationStatus = "unavailable"
	DestinationHealthy       DestinationStatus = "healthy"
)

// S3Config is installation-owned configuration for a portable S3-compatible
// restic repository. Credential values are read from protected files and are
// never persisted in snapshot settings.
type S3Config struct {
	Endpoint      string
	Bucket        string
	Region        string
	AccessKeyFile string
	SecretKeyFile string
	BucketLookup  string
}

// ManifestEntry is a filesystem path included in server snapshots.
type ManifestEntry struct {
	ID        string   `json:"id"`
	Path      string   `json:"path"`
	Label     string   `json:"label"`
	Required  bool     `json:"required"`
	Enabled   bool     `json:"enabled"`
	Available bool     `json:"available,omitempty"` // path exists on disk
	Exclude   []string `json:"exclude,omitempty"`
}

// Settings persisted for incremental snapshot policy.
type Settings struct {
	Destination          Destination `json:"destination"`
	RepoFolder           string      `json:"repoFolder"`
	EnabledPaths         []string    `json:"enabledPaths"` // nil/absent = all defaults; [] = only required paths
	KeepDaily            int         `json:"keepDaily"`
	KeepWeekly           int         `json:"keepWeekly"`
	KeepMonthly          int         `json:"keepMonthly"`
	LastRunAt            string      `json:"lastRunAt,omitempty"`
	LastSnapshot         string      `json:"lastSnapshotId,omitempty"`
	LastError            string      `json:"lastError,omitempty"`
	PasswordAcknowledged bool        `json:"passwordAcknowledged,omitempty"`
}

// SettingsUpdate is the complete operator-controlled snapshot policy. Runtime
// fields such as last run, last snapshot, and last error remain server-owned.
type SettingsUpdate struct {
	Destination          Destination `json:"destination"`
	RepoFolder           string      `json:"repoFolder"`
	EnabledPaths         []string    `json:"enabledPaths"`
	KeepDaily            int         `json:"keepDaily"`
	KeepWeekly           int         `json:"keepWeekly"`
	KeepMonthly          int         `json:"keepMonthly"`
	PasswordAcknowledged bool        `json:"passwordAcknowledged"`
}

// Snapshot is a restic point-in-time backup on the configured destination.
type Snapshot struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Hostname string    `json:"hostname"`
	Tags     []string  `json:"tags,omitempty"`
	Paths    int       `json:"paths"`
	Size     int64     `json:"size,omitempty"`
}

// RestoreRequest selects what to pull back from a snapshot.
type RestoreRequest struct {
	SnapshotID  string   `json:"snapshotId"`
	ManifestIDs []string `json:"manifestIds,omitempty"`
	Vhosts      []string `json:"vhosts,omitempty"`
}

// PurgeRequest identifies the currently observed repository and carries the
// fixed destructive-action acknowledgement. The remote path is still resolved
// exclusively from the installation-owned settings file.
type PurgeRequest struct {
	RepoFolder   string `json:"repoFolder"`
	Confirmation string `json:"confirmation"`
}

// RepoStats summarizes the client-side encrypted restic repository.
type RepoStats struct {
	SnapshotCount int   `json:"snapshotCount"`
	TotalSize     int64 `json:"totalSize"`     // deduplicated bytes in repo
	TotalFileSize int64 `json:"totalFileSize"` // logical file bytes
}

// Status summarizes snapshot subsystem health.
type Status struct {
	ResticFound        bool              `json:"resticFound"`
	RepoInitialized    bool              `json:"repoInitialized"`
	PasswordSet        bool              `json:"passwordSet"`
	Destination        Destination       `json:"destination"`
	DestinationStatus  DestinationStatus `json:"destinationStatus"`
	DestinationMessage string            `json:"destinationMessage,omitempty"`
	CanPurgeRepository bool              `json:"canPurgeRepository"`
	DriveConnected     bool              `json:"driveConnected"` // compatibility for older clients
	Settings           Settings          `json:"settings"`
	Manifest           []ManifestEntry   `json:"manifest"`
	RepoStats          *RepoStats        `json:"repoStats,omitempty"`
	LastSnapshots      []Snapshot        `json:"lastSnapshots,omitempty"`
}
