package releaseupdates

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const upgradeRunnerScript = `#!/bin/sh
set -eu

status_file=$1
detail_file=$2
installer=$3
binary=$4
cli_binary=$5
expected_version=$6
installed_binary=$7
installed_cli=$8
data_dir=$9
layout_mode=${10}

write_state() {
  status=$1
  detail=$2
  status_temporary="${status_file}.tmp.$$"
  detail_temporary="${detail_file}.tmp.$$"
  umask 077
  printf '%s\n' "$detail" >"$detail_temporary"
  mv -f "$detail_temporary" "$detail_file"
  printf '%s\n' "$status" >"$status_temporary"
  mv -f "$status_temporary" "$status_file"
}

installed_identity_matches() {
  expected_panel_identity=$("$binary" --version 2>/dev/null || true)
  expected_cli_identity=$("$cli_binary" version 2>/dev/null || true)
  installed_panel_identity=$("$installed_binary" --version 2>/dev/null || true)
  installed_cli_identity=$("$installed_cli" version 2>/dev/null || true)
  [ -n "$expected_panel_identity" ] && [ "$installed_panel_identity" = "$expected_panel_identity" ] || return 1
  [ -n "$expected_cli_identity" ] && [ "$installed_cli_identity" = "$expected_cli_identity" ] || return 1
  case "$installed_panel_identity" in "hserver-panel $expected_version (commit "*) ;; *) return 1 ;; esac
  case "$installed_cli_identity" in "hserverctl $expected_version ("*) ;; *) return 1 ;; esac
}

run_installer() {
  if [ "$layout_mode" = preserve ]; then
    HSERVER_PRESERVE_LAYOUT=1 \
    HSERVER_BINARY_PATH="$installed_binary" \
    HSERVER_CLI_PATH="$installed_cli" \
    HSERVER_DATA_DIR_PATH="$data_dir" \
      "$installer" "$@"
  else
    "$installer" "$@"
  fi
}

write_state running "Packaged panel installer is running."
if run_installer upgrade --binary "$binary" --cli-binary "$cli_binary"; then
  if installed_identity_matches; then
    write_state completed "New panel and CLI release identities match the verified stage and passed the health check."
  else
    write_state running "Installed panel or CLI release identity does not match the verified stage; automatic rollback is running."
    if run_installer rollback; then
      write_state failed "Installed release identity did not match the verified stage; the previous release was restored."
    else
      write_state failed "Installed release identity did not match the verified stage and automatic rollback failed; inspect the lifecycle journals."
    fi
    exit 1
  fi
else
  write_state failed "Packaged panel installer failed; inspect the upgrade and panel service journals."
  exit 1
fi
`

var (
	ErrConfirmationRequired = errors.New("explicit update confirmation is required")
	ErrStageConflict        = errors.New("update stage cannot be scheduled in its current state")
	ErrStageIntegrity       = errors.New("update stage integrity check failed")
	ErrUpgradeSchedule      = errors.New("could not schedule the panel upgrade")
	ErrInstalledPath        = errors.New("installed panel or CLI path is unsafe")
)

const (
	nativePanelPath = "/usr/local/bin/hserver-panel"
	nativeCLIPath   = "/usr/local/bin/hserverctl"
)

type installedPaths struct {
	panel        string
	cli          string
	preserveMode bool
}

func resolveInstalledPaths(configuredPanel, configuredCLI, observedExecutable string) installedPaths {
	panel := strings.TrimSpace(configuredPanel)
	if panel == "" {
		if filepath.Base(observedExecutable) == "hserver-panel" {
			panel = observedExecutable
		} else {
			panel = nativePanelPath
		}
	}
	cli := strings.TrimSpace(configuredCLI)
	if cli == "" {
		if panel == nativePanelPath {
			cli = nativeCLIPath
		} else {
			cli = filepath.Join(filepath.Dir(panel), "hserverctl")
		}
	}
	return installedPaths{panel: panel, cli: cli, preserveMode: panel != nativePanelPath || cli != nativeCLIPath}
}

func (m *Manager) installedPaths() (installedPaths, error) {
	observed, _ := m.executablePath()
	paths := resolveInstalledPaths(m.panelPath, m.cliPath, observed)
	if err := validateInstalledExecutable(paths.panel, "hserver-panel", m.dataDir, m.pathValidationRoot); err != nil {
		return installedPaths{}, fmt.Errorf("%w: panel: %v", ErrInstalledPath, err)
	}
	if err := validateInstalledExecutable(paths.cli, "hserverctl", m.dataDir, m.pathValidationRoot); err != nil {
		return installedPaths{}, fmt.Errorf("%w: CLI: %v", ErrInstalledPath, err)
	}
	if paths.panel == paths.cli {
		return installedPaths{}, fmt.Errorf("%w: panel and CLI destinations must differ", ErrInstalledPath)
	}
	return paths, nil
}

func validateInstalledExecutable(candidate, expectedBase, dataDir, validationRoot string) error {
	if !filepath.IsAbs(candidate) || filepath.Clean(candidate) != candidate || filepath.Base(candidate) != expectedBase {
		return errors.New("path must be canonical, absolute, and name the expected executable")
	}
	logical := candidate
	if validationRoot != "" {
		root := filepath.Clean(validationRoot)
		if candidate != root && strings.HasPrefix(candidate, root+string(os.PathSeparator)) {
			logical = strings.TrimPrefix(candidate, root)
		}
	}
	for _, root := range []string{"/etc", "/proc", "/sys", "/dev", "/run", "/tmp", "/var"} {
		if logical == root || strings.HasPrefix(logical, root+string(os.PathSeparator)) {
			return errors.New("path is under an unsafe executable root")
		}
	}
	if dataDir != "" {
		cleanData := filepath.Clean(dataDir)
		if candidate == cleanData || strings.HasPrefix(candidate, cleanData+string(os.PathSeparator)) {
			return errors.New("path must not be inside the application data directory")
		}
	}
	info, err := os.Lstat(candidate)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o111 == 0 {
		return errors.New("destination must be an existing regular executable, not a symlink")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil || resolved != candidate {
		return errors.New("destination and its parent directories must not be symlinks")
	}
	return nil
}

type commandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Schedule revalidates every executable staged from the release archive and
// hands the upgrade to a separate transient systemd unit. The delayed unit is
// independent of the panel process that the installer will restart.
func (m *Manager) Schedule(ctx context.Context, id, version string, confirmed bool) (Stage, error) {
	if !confirmed {
		return Stage{}, ErrConfirmationRequired
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	record, err := m.getLocked(id)
	if err != nil {
		return Stage{}, err
	}
	record = m.reconcileInterruptedLocked(ctx, record)
	if version == "" || version != record.Version {
		return Stage{}, ErrInvalidStage
	}
	switch record.Status {
	case StageStaged, StageFailed:
	case StageScheduled, StageRunning, StageCompleted:
		return Stage{}, ErrStageConflict
	default:
		return Stage{}, ErrInvalidStage
	}

	stageDir := filepath.Join(m.dataDir, "updates", id)
	files := []struct {
		name       string
		sha256     string
		executable bool
	}{
		{name: "hserver-panel", sha256: record.BinarySHA256, executable: true},
		{name: "hserverctl", sha256: record.CLISHA256, executable: true},
		{name: "install.sh", sha256: record.InstallerSHA256, executable: true},
		{name: "doctor.sh", sha256: record.DoctorSHA256, executable: true},
		{name: "upgrade-runner.sh", sha256: record.RunnerSHA256, executable: true},
	}
	for _, name := range managedReleaseSnippetNames {
		files = append(files, struct {
			name       string
			sha256     string
			executable bool
		}{name: filepath.Join("nginx-snippets", name), sha256: record.NginxSnippetSHA256[name]})
	}
	for _, file := range files {
		if err := verifyStagedFile(filepath.Join(stageDir, file.name), file.sha256, file.executable); err != nil {
			return Stage{}, fmt.Errorf("%w: %s", ErrStageIntegrity, file.name)
		}
	}
	if err := verifyELFArchitecture(filepath.Join(stageDir, "hserver-panel"), record.Platform); err != nil {
		return Stage{}, ErrStageIntegrity
	}
	if err := verifyELFArchitecture(filepath.Join(stageDir, "hserverctl"), record.Platform); err != nil {
		return Stage{}, ErrStageIntegrity
	}
	paths, err := m.installedPaths()
	if err != nil {
		return Stage{}, err
	}

	if err := writeStageStatus(stageDir, StageScheduled, stageStatusDetail(StageScheduled)); err != nil {
		return Stage{}, fmt.Errorf("write scheduled update status: %w", err)
	}
	actionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	layoutMode := "native"
	if paths.preserveMode {
		layoutMode = "preserve"
	}
	_, err = m.runner.Run(
		actionCtx,
		"/usr/bin/systemd-run",
		"--collect", "--quiet",
		"--unit=hserver-panel-upgrade",
		"--on-active=3s",
		"--timer-property=AccuracySec=1s",
		"/bin/sh",
		filepath.Join(stageDir, "upgrade-runner.sh"),
		filepath.Join(stageDir, "status"),
		filepath.Join(stageDir, "status-detail"),
		filepath.Join(stageDir, "install.sh"),
		filepath.Join(stageDir, "hserver-panel"),
		filepath.Join(stageDir, "hserverctl"),
		record.Version,
		paths.panel,
		paths.cli,
		m.dataDir,
		layoutMode,
	)
	if err != nil {
		_ = writeStageStatus(stageDir, StageFailed, "Could not create the detached systemd upgrade unit.")
		return Stage{}, ErrUpgradeSchedule
	}
	record.Status = StageScheduled
	record.StatusDetail = stageStatusDetail(StageScheduled)
	if info, statErr := os.Stat(filepath.Join(stageDir, "status")); statErr == nil {
		record.UpdatedAt = info.ModTime().UTC()
	}
	return record.Stage, nil
}

func (m *Manager) reconcileInterruptedLocked(ctx context.Context, record stageRecord) stageRecord {
	if record.Status != StageScheduled && record.Status != StageRunning {
		return record
	}
	actionCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := m.runner.Run(
		actionCtx,
		"/usr/bin/systemctl",
		"show",
		"--property=LoadState",
		"--property=ActiveState",
		"hserver-panel-upgrade.timer",
		"hserver-panel-upgrade.service",
	)
	if err != nil {
		return record
	}
	active, conclusive := detachedUpgradeState(output)
	if !conclusive || active {
		return record
	}
	detail := "Detached upgrade unit ended before writing a terminal result; the operation was interrupted."
	stageDir := filepath.Join(m.dataDir, "updates", record.ID)
	if err := writeStageStatus(stageDir, StageFailed, detail); err != nil {
		return record
	}
	record.Status = StageFailed
	record.StatusDetail = detail
	if info, err := os.Stat(filepath.Join(stageDir, "status")); err == nil {
		record.UpdatedAt = info.ModTime().UTC()
	}
	return record
}

func detachedUpgradeState(output []byte) (active bool, conclusive bool) {
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
		state := properties["ActiveState"]
		switch state {
		case "active", "activating", "reloading", "deactivating":
			return true, true
		case "inactive", "failed":
		default:
			return false, false
		}
	}
	return false, true
}

func verifyStagedFile(path, expectedSHA256 string, executable bool) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return ErrStageIntegrity
	}
	if executable && info.Mode().Perm()&0o111 == 0 {
		return ErrStageIntegrity
	}
	digest, err := fileSHA256(path)
	if err != nil || digest != expectedSHA256 {
		return ErrStageIntegrity
	}
	return nil
}
