package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/IamYGT/heyserver/internal/services/releaseupdates"
)

const (
	agentUpdateIdle      = "idle"
	agentUpdateScheduled = "scheduled"
	agentUpdateRunning   = "running"
	agentUpdateCompleted = "completed"
	agentUpdateFailed    = "failed"

	maxAgentReleaseArchiveBytes = int64(1 << 30)
	maxAgentReleaseExpanded     = int64(512 << 20)
	maxAgentBinaryBytes         = int64(256 << 20)
	maxAgentInstallerBytes      = int64(2 << 20)
	maxAgentVersionBytes        = int64(128)
	// Bound detached installers so a hung lifecycle operation cannot run indefinitely.
	agentLifecycleRuntimeMax = "3min"
)

var (
	errAgentUpdateConflict      = errors.New("an agent lifecycle operation is already active")
	errAgentUpdateUnavailable   = errors.New("the configured agent release is unavailable")
	errAgentUpdateVersion       = errors.New("the requested agent release is no longer current")
	errAgentRollbackUnavailable = errors.New("no verified agent rollback snapshot is available")
)

type managedAgentUpdateStatus struct {
	ReleaseStatus     string `json:"release_status"`
	SignatureStatus   string `json:"signature_status"`
	CurrentVersion    string `json:"current_version"`
	LatestVersion     string `json:"latest_version,omitempty"`
	LatestState       string `json:"latest_version_state,omitempty"`
	UpdateAvailable   bool   `json:"update_available"`
	Platform          string `json:"platform"`
	ReleaseNotesURL   string `json:"release_notes_url,omitempty"`
	ReleaseMessage    string `json:"release_message"`
	ReleaseCheckedAt  string `json:"release_checked_at"`
	Operation         string `json:"operation"`
	OperationStatus   string `json:"operation_status"`
	OperationVersion  string `json:"operation_version,omitempty"`
	OperationDetail   string `json:"operation_detail"`
	OperationUpdated  string `json:"operation_updated_at,omitempty"`
	RollbackAvailable bool   `json:"rollback_available"`
}

type agentUpdateOperation struct {
	ID        string
	Action    string
	Status    string
	Version   string
	Detail    string
	UpdatedAt string
}

type agentUpdateController struct {
	mu                 sync.Mutex
	currentVersion     string
	manifestURL        string
	manifestPublicKeys string
	stateDir           string
	lifecycleInstaller string
	systemdRunBinary   string
	systemctlBinary    string
	platform           string
	http               *http.Client
	runner             commandRunner
	now                func() time.Time
}

func newAgentUpdateController(currentVersion, manifestURL, manifestPublicKeys, stateDir, lifecycleInstaller, systemdRunBinary, systemctlBinary string, runner commandRunner) *agentUpdateController {
	return &agentUpdateController{
		currentVersion:     currentVersion,
		manifestURL:        manifestURL,
		manifestPublicKeys: manifestPublicKeys,
		stateDir:           stateDir,
		lifecycleInstaller: lifecycleInstaller,
		systemdRunBinary:   systemdRunBinary,
		systemctlBinary:    systemctlBinary,
		platform:           runtime.GOOS + "_" + runtime.GOARCH,
		http:               &http.Client{Timeout: 10 * time.Minute},
		runner:             runner,
		now:                time.Now,
	}
}

func (c *agentUpdateController) Status(ctx context.Context) (managedAgentUpdateStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	release := c.releaseChecker().Check(ctx)
	operation := c.loadOperation()
	if operation.Status == agentUpdateScheduled || operation.Status == agentUpdateRunning {
		operation = c.reconcileOperation(ctx, operation)
	}
	return managedAgentUpdateStatus{
		ReleaseStatus:     release.Status,
		SignatureStatus:   release.SignatureStatus,
		CurrentVersion:    release.CurrentVersion,
		LatestVersion:     release.LatestVersion,
		LatestState:       string(release.LatestVersionState),
		UpdateAvailable:   release.UpdateAvailable,
		Platform:          release.Platform,
		ReleaseNotesURL:   release.ReleaseNotesURL,
		ReleaseMessage:    release.Message,
		ReleaseCheckedAt:  release.CheckedAt.Format(time.RFC3339Nano),
		Operation:         operation.Action,
		OperationStatus:   operation.Status,
		OperationVersion:  operation.Version,
		OperationDetail:   operation.Detail,
		OperationUpdated:  operation.UpdatedAt,
		RollbackAvailable: c.rollbackAvailable(),
	}, nil
}

func (c *agentUpdateController) Upgrade(ctx context.Context, expectedVersion string) (managedAgentUpdateStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if operation := c.loadOperation(); operation.Status == agentUpdateScheduled || operation.Status == agentUpdateRunning {
		return managedAgentUpdateStatus{}, errAgentUpdateConflict
	}

	release := c.releaseChecker().Check(ctx)
	if release.Status != releaseupdates.StatusHealthy || release.Artifact == nil || !release.UpdateAvailable {
		return managedAgentUpdateStatus{}, errAgentUpdateUnavailable
	}
	if release.SignatureStatus != releaseupdates.SignatureVerified {
		return managedAgentUpdateStatus{}, releaseupdates.ErrSignedManifestRequired
	}
	if expectedVersion == "" || expectedVersion != release.LatestVersion {
		return managedAgentUpdateStatus{}, errAgentUpdateVersion
	}

	operation, err := c.stageUpgrade(ctx, release.LatestVersion, *release.Artifact)
	if err != nil {
		return managedAgentUpdateStatus{}, err
	}
	if err := c.schedule(ctx, operation, filepath.Join(c.updateDir(), operation.ID, "agent-install.sh"), filepath.Join(c.updateDir(), operation.ID, "hserver-agent")); err != nil {
		c.writeOperationStatus(operation.ID, agentUpdateFailed, "Could not create the detached systemd lifecycle unit.")
		return managedAgentUpdateStatus{}, err
	}
	return c.statusFromKnownRelease(release)
}

func (c *agentUpdateController) Rollback(ctx context.Context) (managedAgentUpdateStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if operation := c.loadOperation(); operation.Status == agentUpdateScheduled || operation.Status == agentUpdateRunning {
		return managedAgentUpdateStatus{}, errAgentUpdateConflict
	}
	if !c.rollbackAvailable() || !regularExecutable(c.lifecycleInstaller) {
		return managedAgentUpdateStatus{}, errAgentRollbackUnavailable
	}
	operation, err := c.newOperation("rollback", "")
	if err != nil {
		return managedAgentUpdateStatus{}, err
	}
	if err := c.schedule(ctx, operation, c.lifecycleInstaller, ""); err != nil {
		c.writeOperationStatus(operation.ID, agentUpdateFailed, "Could not create the detached systemd lifecycle unit.")
		return managedAgentUpdateStatus{}, err
	}
	release := c.releaseChecker().Check(ctx)
	return c.statusFromKnownRelease(release)
}

func (c *agentUpdateController) releaseChecker() *releaseupdates.Checker {
	return releaseupdates.New(c.manifestURL, c.currentVersion, releaseupdates.WithManifestPublicKeys(c.manifestPublicKeys))
}

func (c *agentUpdateController) statusFromKnownRelease(release releaseupdates.Result) (managedAgentUpdateStatus, error) {
	operation := c.loadOperation()
	return managedAgentUpdateStatus{
		ReleaseStatus:     release.Status,
		SignatureStatus:   release.SignatureStatus,
		CurrentVersion:    release.CurrentVersion,
		LatestVersion:     release.LatestVersion,
		LatestState:       string(release.LatestVersionState),
		UpdateAvailable:   release.UpdateAvailable,
		Platform:          release.Platform,
		ReleaseNotesURL:   release.ReleaseNotesURL,
		ReleaseMessage:    release.Message,
		ReleaseCheckedAt:  release.CheckedAt.Format(time.RFC3339Nano),
		Operation:         operation.Action,
		OperationStatus:   operation.Status,
		OperationVersion:  operation.Version,
		OperationDetail:   operation.Detail,
		OperationUpdated:  operation.UpdatedAt,
		RollbackAvailable: c.rollbackAvailable(),
	}, nil
}

func (c *agentUpdateController) stageUpgrade(ctx context.Context, version string, artifact releaseupdates.Artifact) (agentUpdateOperation, error) {
	operation, err := c.newOperation("upgrade", version)
	if err != nil {
		return agentUpdateOperation{}, err
	}
	stageDir := filepath.Join(c.updateDir(), operation.ID)
	archivePath := filepath.Join(stageDir, "release.tar.gz")
	if err := c.downloadArchive(ctx, artifact, archivePath); err != nil {
		c.writeOperationStatus(operation.ID, agentUpdateFailed, "Release archive download or checksum verification failed.")
		return agentUpdateOperation{}, err
	}
	if err := extractAgentRelease(archivePath, stageDir, version, c.platform); err != nil {
		_ = os.Remove(archivePath)
		c.writeOperationStatus(operation.ID, agentUpdateFailed, "Release archive validation failed.")
		return agentUpdateOperation{}, err
	}
	if err := os.Remove(archivePath); err != nil {
		return agentUpdateOperation{}, fmt.Errorf("remove verified agent archive: %w", err)
	}
	return operation, nil
}

func (c *agentUpdateController) downloadArchive(ctx context.Context, artifact releaseupdates.Artifact, destination string) error {
	if artifact.SizeBytes > maxAgentReleaseArchiveBytes {
		return errors.New("agent release archive exceeds the size limit")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return fmt.Errorf("create agent release request: %w", err)
	}
	request.Header.Set("Accept", "application/gzip, application/octet-stream")
	request.Header.Set("User-Agent", "hserver-agent/"+c.currentVersion)
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("download agent release: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("agent release returned HTTP %d", response.StatusCode)
	}
	limit := maxAgentReleaseArchiveBytes
	if artifact.SizeBytes > 0 {
		limit = artifact.SizeBytes
	}
	temporary := destination + ".tmp"
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create agent release archive: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, limit+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written > limit || artifact.SizeBytes > 0 && written != artifact.SizeBytes {
		_ = os.Remove(temporary)
		return errors.New("agent release archive size validation failed")
	}
	if hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		_ = os.Remove(temporary)
		return errors.New("agent release archive checksum validation failed")
	}
	if err := os.Rename(temporary, destination); err != nil {
		_ = os.Remove(temporary)
		return fmt.Errorf("publish agent release archive: %w", err)
	}
	return nil
}

func extractAgentRelease(archivePath, destination, version, platform string) error {
	archive, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	expectedRoot := "hserver-panel-" + version + "-" + strings.ReplaceAll(platform, "_", "-")
	targets := map[string]struct {
		name string
		max  int64
	}{
		expectedRoot + "/VERSION":          {name: "VERSION", max: maxAgentVersionBytes},
		expectedRoot + "/hserver-agent":    {name: "hserver-agent", max: maxAgentBinaryBytes},
		expectedRoot + "/agent-install.sh": {name: "agent-install.sh", max: maxAgentInstallerBytes},
	}
	found := make(map[string]bool, len(targets))
	var expanded int64
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		entryName := header.Name
		if header.Typeflag == tar.TypeDir {
			entryName = strings.TrimSuffix(entryName, "/")
		}
		clean := path.Clean(entryName)
		if strings.Contains(header.Name, `\`) || clean != entryName || clean == "." || strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "../") || clean != expectedRoot && !strings.HasPrefix(clean, expectedRoot+"/") {
			return errors.New("agent release archive contains an invalid path")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			continue
		case tar.TypeReg, tar.TypeRegA:
		default:
			return errors.New("agent release archive contains an unsupported entry type")
		}
		if header.Size < 0 || header.Size > maxAgentReleaseExpanded-expanded {
			return errors.New("agent release archive exceeds the expanded size limit")
		}
		expanded += header.Size
		target, wanted := targets[clean]
		if !wanted {
			if _, err := io.Copy(io.Discard, io.LimitReader(reader, header.Size)); err != nil {
				return err
			}
			continue
		}
		if found[clean] || header.Size > target.max {
			return errors.New("agent release archive contains an invalid lifecycle asset")
		}
		outputPath := filepath.Join(destination, target.name)
		output, err := os.OpenFile(outputPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
		if err != nil {
			return err
		}
		written, copyErr := io.Copy(output, io.LimitReader(reader, header.Size))
		closeErr := output.Close()
		if copyErr != nil || closeErr != nil || written != header.Size {
			return errors.New("agent release lifecycle asset is incomplete")
		}
		found[clean] = true
	}
	for target := range targets {
		if !found[target] {
			return errors.New("agent release archive is missing a lifecycle asset")
		}
	}
	versionBytes, err := os.ReadFile(filepath.Join(destination, "VERSION"))
	if err != nil || strings.TrimSpace(string(versionBytes)) != version {
		return errors.New("agent release VERSION does not match the manifest")
	}
	installerPrefix := make([]byte, len("#!/usr/bin/env sh"))
	installer, err := os.Open(filepath.Join(destination, "agent-install.sh"))
	if err != nil {
		return err
	}
	_, readErr := io.ReadFull(installer, installerPrefix)
	_ = installer.Close()
	if readErr != nil || string(installerPrefix) != "#!/usr/bin/env sh" {
		return errors.New("agent lifecycle installer has an invalid format")
	}
	if err := verifyAgentELF(filepath.Join(destination, "hserver-agent"), platform); err != nil {
		return err
	}
	return nil
}

func verifyAgentELF(binaryPath, platform string) error {
	binary, err := elf.Open(binaryPath)
	if err != nil {
		return errors.New("agent release binary is not a valid ELF executable")
	}
	defer binary.Close()
	wantMachine := elf.EM_NONE
	switch platform {
	case "linux_amd64":
		wantMachine = elf.EM_X86_64
	case "linux_arm64":
		wantMachine = elf.EM_AARCH64
	default:
		return errors.New("agent release platform is unsupported")
	}
	if binary.FileHeader.Class != elf.ELFCLASS64 || binary.FileHeader.Machine != wantMachine || binary.FileHeader.Type != elf.ET_EXEC && binary.FileHeader.Type != elf.ET_DYN {
		return errors.New("agent release binary architecture does not match this server")
	}
	return nil
}

func (c *agentUpdateController) newOperation(action, version string) (agentUpdateOperation, error) {
	if err := os.MkdirAll(c.updateDir(), 0o700); err != nil {
		return agentUpdateOperation{}, fmt.Errorf("create agent update directory: %w", err)
	}
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		return agentUpdateOperation{}, err
	}
	id := c.now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(random)
	stageDir := filepath.Join(c.updateDir(), id)
	if err := os.Mkdir(stageDir, 0o700); err != nil {
		return agentUpdateOperation{}, err
	}
	operation := agentUpdateOperation{ID: id, Action: action, Status: agentUpdateScheduled, Version: version, Detail: "Detached agent lifecycle operation is scheduled.", UpdatedAt: c.now().UTC().Format(time.RFC3339Nano)}
	for name, value := range map[string]string{"action": action, "status": operation.Status, "version": version, "status-detail": operation.Detail, "updated-at": operation.UpdatedAt} {
		if err := writeAgentUpdateFile(filepath.Join(stageDir, name), value); err != nil {
			return agentUpdateOperation{}, err
		}
	}
	if err := writeAgentUpdateFile(filepath.Join(c.updateDir(), "current"), id); err != nil {
		return agentUpdateOperation{}, err
	}
	return operation, nil
}

func (c *agentUpdateController) schedule(ctx context.Context, operation agentUpdateOperation, installer, binary string) error {
	stageDir := filepath.Join(c.updateDir(), operation.ID)
	runnerPath := filepath.Join(stageDir, "lifecycle-runner.sh")
	if err := os.WriteFile(runnerPath, []byte(agentLifecycleRunner), 0o700); err != nil {
		return err
	}
	args := []string{
		"--collect", "--quiet", "--unit=hserver-agent-lifecycle", "--on-active=3s", "--timer-property=AccuracySec=1s",
		"--property=RuntimeMaxSec=" + agentLifecycleRuntimeMax,
		"/bin/sh", runnerPath,
		filepath.Join(stageDir, "status"), filepath.Join(stageDir, "status-detail"), filepath.Join(stageDir, "updated-at"),
		installer, operation.Action,
	}
	if operation.Action == "upgrade" {
		args = append(args, binary)
	}
	commandCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := c.runner.run(commandCtx, c.systemdRunBinary, args...); err != nil {
		return fmt.Errorf("schedule detached agent lifecycle operation: %w", err)
	}
	return nil
}

func (c *agentUpdateController) loadOperation() agentUpdateOperation {
	idBytes, err := os.ReadFile(filepath.Join(c.updateDir(), "current"))
	if err != nil {
		return agentUpdateOperation{Status: agentUpdateIdle, Detail: "No agent lifecycle operation has been scheduled."}
	}
	id := strings.TrimSpace(string(idBytes))
	if id == "" || filepath.Base(id) != id {
		return agentUpdateOperation{Status: agentUpdateFailed, Detail: "The agent lifecycle state marker is invalid."}
	}
	stageDir := filepath.Join(c.updateDir(), id)
	operation := agentUpdateOperation{ID: id}
	operation.Action = readAgentUpdateFile(filepath.Join(stageDir, "action"))
	operation.Status = readAgentUpdateFile(filepath.Join(stageDir, "status"))
	operation.Version = readAgentUpdateFile(filepath.Join(stageDir, "version"))
	operation.Detail = readAgentUpdateFile(filepath.Join(stageDir, "status-detail"))
	operation.UpdatedAt = readAgentUpdateFile(filepath.Join(stageDir, "updated-at"))
	if operation.Status != agentUpdateScheduled && operation.Status != agentUpdateRunning && operation.Status != agentUpdateCompleted && operation.Status != agentUpdateFailed {
		operation.Status = agentUpdateFailed
		operation.Detail = "The persisted agent lifecycle status is invalid."
	}
	return operation
}

func (c *agentUpdateController) reconcileOperation(ctx context.Context, operation agentUpdateOperation) agentUpdateOperation {
	commandCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := c.runner.run(commandCtx, c.systemctlBinary, "show", "--property=LoadState", "--property=ActiveState", "hserver-agent-lifecycle.timer", "hserver-agent-lifecycle.service")
	if err != nil {
		return operation
	}
	active, conclusive := detachedAgentLifecycleState(output)
	if !conclusive || active {
		return operation
	}
	detail := "Detached agent lifecycle unit ended before writing a terminal result; the operation was interrupted."
	_ = c.writeOperationStatus(operation.ID, agentUpdateFailed, detail)
	operation.Status = agentUpdateFailed
	operation.Detail = detail
	operation.UpdatedAt = c.now().UTC().Format(time.RFC3339Nano)
	return operation
}

func detachedAgentLifecycleState(output []byte) (bool, bool) {
	blocks := strings.Split(strings.TrimSpace(string(output)), "\n\n")
	if len(blocks) != 2 {
		return false, false
	}
	for _, block := range blocks {
		properties := make(map[string]string, 2)
		for _, line := range strings.Split(block, "\n") {
			key, value, found := strings.Cut(strings.TrimSpace(line), "=")
			if found {
				properties[key] = value
			}
		}
		if properties["LoadState"] == "" {
			return false, false
		}
		switch properties["ActiveState"] {
		case "active", "activating", "reloading", "deactivating":
			return true, true
		case "inactive", "failed":
		default:
			return false, false
		}
	}
	return false, true
}

func (c *agentUpdateController) writeOperationStatus(id, status, detail string) error {
	stageDir := filepath.Join(c.updateDir(), id)
	if err := writeAgentUpdateFile(filepath.Join(stageDir, "status"), status); err != nil {
		return err
	}
	if err := writeAgentUpdateFile(filepath.Join(stageDir, "status-detail"), detail); err != nil {
		return err
	}
	return writeAgentUpdateFile(filepath.Join(stageDir, "updated-at"), c.now().UTC().Format(time.RFC3339Nano))
}

func (c *agentUpdateController) rollbackAvailable() bool {
	marker := filepath.Join(c.stateDir, "releases", "latest-pre-upgrade")
	value, err := os.ReadFile(marker)
	if err != nil {
		return false
	}
	target := strings.TrimSpace(string(value))
	releasesDir := filepath.Join(c.stateDir, "releases")
	if target == "" || target == releasesDir || !strings.HasPrefix(target, releasesDir+string(filepath.Separator)) {
		return false
	}
	info, err := os.Stat(filepath.Join(target, "hserver-agent"))
	return err == nil && info.Mode().IsRegular() && info.Size() > 0
}

func (c *agentUpdateController) updateDir() string {
	return filepath.Join(c.stateDir, "updates")
}

func writeAgentUpdateFile(filename, value string) error {
	temporary := filename + ".tmp"
	if err := os.WriteFile(temporary, []byte(value+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.Rename(temporary, filename); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func readAgentUpdateFile(filename string) string {
	value, err := os.ReadFile(filename)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(value))
}

func regularExecutable(filename string) bool {
	info, err := os.Lstat(filename)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0
}

const agentLifecycleRunner = `#!/usr/bin/env sh
set -eu

[ "$#" -ge 5 ] || exit 2
status_file=$1
detail_file=$2
updated_file=$3
installer=$4
action=$5
binary=${6:-}

write_status() {
  value=$1
  detail=$2
  printf '%s\n' "$value" >"$status_file.tmp"
  mv -f "$status_file.tmp" "$status_file"
  printf '%s\n' "$detail" >"$detail_file.tmp"
  mv -f "$detail_file.tmp" "$detail_file"
  date -u +%Y-%m-%dT%H:%M:%SZ >"$updated_file.tmp"
  mv -f "$updated_file.tmp" "$updated_file"
}

write_status running "Agent lifecycle installer is running."
if [ "$action" = upgrade ]; then
  if "$installer" upgrade --binary "$binary"; then
    write_status completed "Agent upgrade completed and the managed service passed its active-state check."
    exit 0
  fi
  write_status failed "Agent upgrade failed; the lifecycle installer attempted automatic rollback."
  exit 1
fi
if [ "$action" = rollback ]; then
  if "$installer" rollback; then
    write_status completed "Agent rollback completed and the restored service passed its active-state check."
    exit 0
  fi
  write_status failed "Agent rollback failed; inspect the hserver-agent-lifecycle journal."
  exit 1
fi
write_status failed "Unsupported agent lifecycle action."
exit 2
`
