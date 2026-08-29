package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

type tuiEncryptedSnapshot struct {
	ID       string    `json:"id"`
	Time     time.Time `json:"time"`
	Hostname string    `json:"hostname"`
	Tags     []string  `json:"tags,omitempty"`
	Paths    int       `json:"paths"`
	Size     int64     `json:"size,omitempty"`
}

type tuiSnapshotRepoStats struct {
	SnapshotCount int   `json:"snapshotCount"`
	TotalSize     int64 `json:"totalSize"`
	TotalFileSize int64 `json:"totalFileSize"`
}

type tuiSnapshotManifestEntry struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Required  bool   `json:"required"`
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
}

type tuiSnapshotState struct {
	Supported          bool
	ResticFound        bool                       `json:"resticFound"`
	RepoInitialized    bool                       `json:"repoInitialized"`
	PasswordSet        bool                       `json:"passwordSet"`
	Destination        string                     `json:"destination"`
	DestinationStatus  string                     `json:"destinationStatus"`
	DestinationMessage string                     `json:"destinationMessage,omitempty"`
	CanPurgeRepository bool                       `json:"canPurgeRepository"`
	Settings           cliSnapshotSettings        `json:"settings"`
	Manifest           []tuiSnapshotManifestEntry `json:"manifest"`
	RepoStats          *tuiSnapshotRepoStats      `json:"repoStats,omitempty"`
	LastSnapshots      []tuiEncryptedSnapshot     `json:"lastSnapshots"`
	Snapshots          []tuiEncryptedSnapshot     `json:"-"`
	Vhosts             []string                   `json:"-"`
	Warnings           []string                   `json:"-"`
}

type tuiSnapshotsMsg struct {
	TargetID string
	State    tuiSnapshotState
	Err      error
}

func (state tuiSnapshotState) ready() bool {
	return state.Supported && state.ResticFound && state.PasswordSet && state.DestinationStatus == "healthy"
}

func loadTUISnapshotsCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		state, err := loadTUISnapshots(ctx, client, target)
		return tuiSnapshotsMsg{TargetID: target.ID, State: state, Err: err}
	}
}

func loadTUISnapshots(ctx context.Context, client *apiClient, target tuiTarget) (tuiSnapshotState, error) {
	if !target.Local {
		return tuiSnapshotState{
			Supported:          false,
			DestinationStatus:  "not_configured",
			DestinationMessage: "Encrypted snapshot repositories are owned by the HServer hub; select the Local server.",
		}, nil
	}
	state, err := requestJSON[tuiSnapshotState](ctx, client.withTimeout(3*time.Minute), http.MethodGet, "/api/backups/snapshot/status?refresh=1", nil, true)
	if err != nil {
		return tuiSnapshotState{}, err
	}
	state.Supported = true
	state.Destination = strings.ToLower(strings.TrimSpace(state.Destination))
	if state.Destination != "gdrive" && state.Destination != "s3" {
		return tuiSnapshotState{}, fmt.Errorf("server returned unsupported snapshot destination %q", state.Destination)
	}
	state.DestinationStatus = strings.ToLower(strings.TrimSpace(state.DestinationStatus))
	if state.DestinationStatus != "not_configured" && state.DestinationStatus != "unavailable" && state.DestinationStatus != "healthy" {
		return tuiSnapshotState{}, fmt.Errorf("server returned unsupported snapshot destination status %q", state.DestinationStatus)
	}
	if len(state.Manifest) > len(snapshotRestoreManifest)+1 {
		return tuiSnapshotState{}, errors.New("server returned too many encrypted snapshot manifest entries")
	}
	allowedManifest := make(map[string]struct{}, len(snapshotRestoreManifest)+1)
	for id := range snapshotRestoreManifest {
		allowedManifest[id] = struct{}{}
	}
	allowedManifest["root-crontab"] = struct{}{}
	seenManifest := make(map[string]struct{}, len(state.Manifest))
	for _, entry := range state.Manifest {
		if _, allowed := allowedManifest[entry.ID]; !allowed {
			return tuiSnapshotState{}, fmt.Errorf("server returned unsupported encrypted snapshot manifest identity %q", entry.ID)
		}
		if _, exists := seenManifest[entry.ID]; exists {
			return tuiSnapshotState{}, fmt.Errorf("server returned duplicate encrypted snapshot manifest identity %q", entry.ID)
		}
		seenManifest[entry.ID] = struct{}{}
	}
	state.Snapshots = append([]tuiEncryptedSnapshot(nil), state.LastSnapshots...)
	if state.DestinationStatus == "healthy" && state.RepoInitialized {
		response, listErr := requestJSON[struct {
			Snapshots []tuiEncryptedSnapshot `json:"snapshots"`
		}](ctx, client.withTimeout(3*time.Minute), http.MethodGet, "/api/backups/snapshot/list", nil, true)
		if listErr != nil {
			state.Warnings = append(state.Warnings, "Encrypted snapshot inventory refresh unavailable: "+listErr.Error())
		} else {
			state.Snapshots = response.Snapshots
		}
	}
	if state.ready() && len(state.Snapshots) > 0 {
		response, vhostErr := requestJSON[struct {
			Vhosts []string `json:"vhosts"`
		}](ctx, client.withTimeout(3*time.Minute), http.MethodGet, "/api/backups/snapshot/vhosts", nil, true)
		if vhostErr != nil {
			state.Warnings = append(state.Warnings, "Encrypted snapshot vhost selectors unavailable: "+vhostErr.Error())
		} else {
			if len(response.Vhosts) > 4096 {
				return tuiSnapshotState{}, errors.New("server returned too many encrypted snapshot vhost selectors")
			}
			if err := validateUniqueBackupIdentities("vhost", response.Vhosts, 4096); err != nil {
				return tuiSnapshotState{}, fmt.Errorf("server returned invalid encrypted snapshot vhost selectors: %w", err)
			}
			sort.SliceStable(response.Vhosts, func(i, j int) bool { return strings.ToLower(response.Vhosts[i]) < strings.ToLower(response.Vhosts[j]) })
			state.Vhosts = response.Vhosts
		}
	}
	if len(state.Snapshots) > 128 {
		return tuiSnapshotState{}, errors.New("server returned too many encrypted snapshots")
	}
	seen := make(map[string]struct{}, len(state.Snapshots))
	for _, snapshot := range state.Snapshots {
		if !snapshotIdentityPattern.MatchString(snapshot.ID) {
			return tuiSnapshotState{}, errors.New("server returned an invalid encrypted snapshot identity")
		}
		if _, exists := seen[snapshot.ID]; exists {
			return tuiSnapshotState{}, fmt.Errorf("server returned duplicate encrypted snapshot identity %q", snapshot.ID)
		}
		seen[snapshot.ID] = struct{}{}
	}
	sort.SliceStable(state.Snapshots, func(i, j int) bool { return state.Snapshots[i].Time.After(state.Snapshots[j].Time) })
	return state, nil
}

func runTUISnapshotOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	if !operation.Target.Local {
		return "", errors.New("encrypted snapshot operations are local to the HServer hub")
	}
	switch operation.Action {
	case "create":
		longClient := client.withTimeout(7 * time.Minute)
		response, err := requestJSON[map[string]any](ctx, longClient, http.MethodPost, "/api/backups/snapshot/run", nil, true)
		if err != nil {
			return "", err
		}
		return backupOperationMessage(response, "Encrypted snapshot started"), nil
	case "restore-all", "restore-selected":
		if !snapshotIdentityPattern.MatchString(operation.EncryptedSnapshot.ID) {
			return "", errors.New("encrypted snapshot identity is invalid")
		}
		manifestIDs := append([]string(nil), operation.SnapshotManifestIDs...)
		vhosts := append([]string(nil), operation.SnapshotVhosts...)
		if operation.Action == "restore-all" {
			if len(manifestIDs) > 0 || len(vhosts) > 0 {
				return "", errors.New("full encrypted snapshot restore cannot include selectors")
			}
		} else if len(manifestIDs) == 0 && len(vhosts) == 0 {
			return "", errors.New("select at least one encrypted snapshot restore scope")
		}
		seenManifest := make(map[string]struct{}, len(manifestIDs))
		for _, id := range manifestIDs {
			if _, ok := snapshotRestoreManifest[id]; !ok {
				return "", fmt.Errorf("encrypted snapshot manifest identity %q is not selectable", id)
			}
			if _, exists := seenManifest[id]; exists {
				return "", fmt.Errorf("duplicate encrypted snapshot manifest identity %q", id)
			}
			seenManifest[id] = struct{}{}
		}
		if err := validateUniqueBackupIdentities("vhost", vhosts, 16); err != nil {
			return "", err
		}
		if _, allVhosts := seenManifest["vhosts"]; allVhosts && len(vhosts) > 0 {
			return "", errors.New("choose either the vhosts manifest or specific encrypted snapshot vhosts")
		}
		longClient := client.withTimeout(7 * time.Minute)
		response, err := requestJSON[map[string]any](ctx, longClient, http.MethodPost, "/api/backups/snapshot/restore", map[string]any{
			"snapshotId":  operation.EncryptedSnapshot.ID,
			"manifestIds": manifestIDs,
			"vhosts":      vhosts,
		}, true)
		if err != nil {
			return "", err
		}
		return backupOperationMessage(response, "Encrypted snapshot restore started"), nil
	case "destination-gdrive", "destination-s3":
		destination := strings.TrimPrefix(operation.Action, "destination-")
		settings, err := requestJSON[cliSnapshotSettings](ctx, client.withTimeout(3*time.Minute), http.MethodGet, "/api/backups/snapshot/settings", nil, true)
		if err != nil {
			return "", err
		}
		settings.Destination = destination
		if _, err := requestJSON[map[string]any](ctx, client.withTimeout(3*time.Minute), http.MethodPut, "/api/backups/snapshot/settings", settings, true); err != nil {
			return "", err
		}
		return "Encrypted snapshot destination changed to " + snapshotDestinationLabel(destination), nil
	default:
		return "", fmt.Errorf("unsupported encrypted snapshot TUI action %q", operation.Action)
	}
}

func snapshotDestinationLabel(destination string) string {
	if destination == "s3" {
		return "S3-compatible / MinIO"
	}
	return "Google Drive"
}
