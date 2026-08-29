package releaseupdates

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

type recordingRunner struct {
	name   string
	args   []string
	output []byte
	err    error
	calls  int
}

func (r *recordingRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	r.calls++
	r.name = name
	r.args = append([]string(nil), args...)
	return r.output, r.err
}

func TestResolveInstalledPathsKeepsNativeDefaultsAndObservesCustomLayout(t *testing.T) {
	native := resolveInstalledPaths("", "", "/tmp/go-build/test-binary")
	if native.panel != nativePanelPath || native.cli != nativeCLIPath || native.preserveMode {
		t.Fatalf("native paths = %#v", native)
	}

	custom := resolveInstalledPaths("", "", "/opt/hserver-panel/bin/hserver-panel")
	if custom.panel != "/opt/hserver-panel/bin/hserver-panel" || custom.cli != "/opt/hserver-panel/bin/hserverctl" || !custom.preserveMode {
		t.Fatalf("custom paths = %#v", custom)
	}

	explicit := resolveInstalledPaths("/srv/hserver/bin/hserver-panel", "/srv/hserver/tools/hserverctl", "/opt/ignored/hserver-panel")
	if explicit.panel != "/srv/hserver/bin/hserver-panel" || explicit.cli != "/srv/hserver/tools/hserverctl" || !explicit.preserveMode {
		t.Fatalf("explicit paths = %#v", explicit)
	}
}

func TestScheduleRequiresConfirmationAndRunsDetachedUpgrade(t *testing.T) {
	manager, staged := stageForLauncher(t)
	runner := &recordingRunner{}
	manager.runner = runner

	if _, err := manager.Schedule(context.Background(), staged.ID, staged.Version, false); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("unconfirmed Schedule() error = %v", err)
	}
	if runner.calls != 0 {
		t.Fatalf("runner calls after unconfirmed request = %d", runner.calls)
	}

	scheduled, err := manager.Schedule(context.Background(), staged.ID, staged.Version, true)
	if err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}
	if scheduled.Status != StageScheduled {
		t.Fatalf("Schedule() status = %q", scheduled.Status)
	}
	stageDir := filepath.Join(manager.dataDir, "updates", staged.ID)
	wantArgs := []string{
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
		staged.Version,
		manager.panelPath,
		manager.cliPath,
		manager.dataDir,
		"preserve",
	}
	if runner.name != "/usr/bin/systemd-run" || !reflect.DeepEqual(runner.args, wantArgs) {
		t.Fatalf("runner = %q %#v", runner.name, runner.args)
	}
	if _, err := manager.Schedule(context.Background(), staged.ID, staged.Version, true); !errors.Is(err, ErrStageConflict) {
		t.Fatalf("second Schedule() error = %v", err)
	}
}

func TestScheduleRefusesModifiedStage(t *testing.T) {
	for _, name := range []string{"hserver-panel", "hserverctl", filepath.Join("nginx-snippets", managedReleaseSnippetNames[0])} {
		t.Run(name, func(t *testing.T) {
			manager, staged := stageForLauncher(t)
			runner := &recordingRunner{}
			manager.runner = runner
			binaryPath := filepath.Join(manager.dataDir, "updates", staged.ID, name)
			if err := os.WriteFile(binaryPath, []byte("modified"), 0o700); err != nil {
				t.Fatal(err)
			}

			if _, err := manager.Schedule(context.Background(), staged.ID, staged.Version, true); !errors.Is(err, ErrStageIntegrity) {
				t.Fatalf("Schedule() error = %v", err)
			}
			if runner.calls != 0 {
				t.Fatalf("runner calls = %d", runner.calls)
			}
		})
	}
}

func TestScheduleRejectsUnsafeInstalledPathsBeforeMutation(t *testing.T) {
	tests := []struct {
		name  string
		alter func(*testing.T, *Manager)
	}{
		{
			name: "symlink destination",
			alter: func(t *testing.T, manager *Manager) {
				t.Helper()
				target := manager.panelPath + ".target"
				if err := os.Rename(manager.panelPath, target); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, manager.panelPath); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "non regular destination",
			alter: func(t *testing.T, manager *Manager) {
				t.Helper()
				if err := os.Remove(manager.cliPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(manager.cliPath, 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "unsafe root",
			alter: func(_ *testing.T, manager *Manager) {
				manager.pathValidationRoot = ""
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manager, staged := stageForLauncher(t)
			runner := &recordingRunner{}
			manager.runner = runner
			test.alter(t, manager)

			if _, err := manager.Schedule(context.Background(), staged.ID, staged.Version, true); !errors.Is(err, ErrInstalledPath) {
				t.Fatalf("Schedule() error = %v", err)
			}
			if runner.calls != 0 {
				t.Fatalf("runner calls = %d", runner.calls)
			}
			current, err := manager.Get(context.Background(), staged.ID)
			if err != nil || current.Status != StageStaged {
				t.Fatalf("Get() = %#v, %v", current, err)
			}
		})
	}
}

func TestScheduleRecordsSystemdFailure(t *testing.T) {
	manager, staged := stageForLauncher(t)
	manager.runner = &recordingRunner{err: errors.New("systemd unavailable")}

	if _, err := manager.Schedule(context.Background(), staged.ID, staged.Version, true); !errors.Is(err, ErrUpgradeSchedule) {
		t.Fatalf("Schedule() error = %v", err)
	}
	current, err := manager.Get(context.Background(), staged.ID)
	if err != nil || current.Status != StageFailed {
		t.Fatalf("Get() = %#v, %v", current, err)
	}
}

func TestLatestReconcilesInterruptedDetachedUpgrade(t *testing.T) {
	manager, staged := stageForLauncher(t)
	manager.runner = &recordingRunner{}
	if _, err := manager.Schedule(context.Background(), staged.ID, staged.Version, true); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}

	manager.runner = &recordingRunner{output: []byte("LoadState=not-found\nActiveState=inactive\n\nLoadState=not-found\nActiveState=inactive\n")}
	latest, err := manager.Latest(context.Background())
	if err != nil || latest == nil {
		t.Fatalf("Latest() = %#v, %v", latest, err)
	}
	if latest.Status != StageFailed || !strings.Contains(latest.StatusDetail, "interrupted") {
		t.Fatalf("reconciled stage = %#v", latest)
	}
}

func TestLatestKeepsActiveDetachedUpgradeRunning(t *testing.T) {
	manager, staged := stageForLauncher(t)
	manager.runner = &recordingRunner{}
	if _, err := manager.Schedule(context.Background(), staged.ID, staged.Version, true); err != nil {
		t.Fatalf("Schedule() error = %v", err)
	}

	manager.runner = &recordingRunner{output: []byte("LoadState=loaded\nActiveState=active\n\nLoadState=loaded\nActiveState=inactive\n")}
	latest, err := manager.Latest(context.Background())
	if err != nil || latest == nil || latest.Status != StageScheduled {
		t.Fatalf("Latest() = %#v, %v", latest, err)
	}
}

func TestDetachedUpgradeStateRequiresConclusiveSystemdOutput(t *testing.T) {
	tests := []struct {
		output     string
		active     bool
		conclusive bool
	}{
		{output: "LoadState=loaded\nActiveState=active\n\nLoadState=not-found\nActiveState=inactive\n", active: true, conclusive: true},
		{output: "ActiveState=deactivating\nLoadState=loaded\n\nActiveState=inactive\nLoadState=loaded\n", active: true, conclusive: true},
		{output: "LoadState=not-found\nActiveState=inactive\n\nLoadState=not-found\nActiveState=inactive\n", active: false, conclusive: true},
		{output: "unexpected", active: false, conclusive: false},
	}
	for _, test := range tests {
		active, conclusive := detachedUpgradeState([]byte(test.output))
		if active != test.active || conclusive != test.conclusive {
			t.Fatalf("detachedUpgradeState(%q) = %t, %t", test.output, active, conclusive)
		}
	}
}

func TestUpgradeRunnerRecordsTerminalStatus(t *testing.T) {
	tests := []struct {
		name             string
		installer        string
		installedVersion string
		installedPanel   string
		wantStatus       string
		wantDetail       string
		wantError        bool
		wantRollback     bool
	}{
		{
			name:             "completed",
			installer:        "#!/bin/sh\nprintf '%s\\n' \"$@\" >>\"$HSERVER_TEST_ARGS\"\nexit 0\n",
			installedVersion: "v1.1.0",
			wantStatus:       StageCompleted,
			wantDetail:       "identities match",
		},
		{
			name:             "installer failed",
			installer:        "#!/bin/sh\nprintf '%s\\n' \"$@\" >>\"$HSERVER_TEST_ARGS\"\nexit 23\n",
			installedVersion: "v1.1.0",
			wantStatus:       StageFailed,
			wantDetail:       "installer failed",
			wantError:        true,
		},
		{
			name:             "identity mismatch rolled back",
			installer:        "#!/bin/sh\nprintf '%s\\n' \"$@\" >>\"$HSERVER_TEST_ARGS\"\nexit 0\n",
			installedVersion: "v9.9.9",
			wantStatus:       StageFailed,
			wantDetail:       "previous release was restored",
			wantError:        true,
			wantRollback:     true,
		},
		{
			name:             "build identity mismatch rolled back",
			installer:        "#!/bin/sh\nprintf '%s\\n' \"$@\" >>\"$HSERVER_TEST_ARGS\"\nexit 0\n",
			installedVersion: "v1.1.0",
			installedPanel:   "hserver-panel v1.1.0 (commit other, built other)",
			wantStatus:       StageFailed,
			wantDetail:       "previous release was restored",
			wantError:        true,
			wantRollback:     true,
		},
		{
			name:             "identity mismatch rollback failed",
			installer:        "#!/bin/sh\nprintf '%s\\n' \"$@\" >>\"$HSERVER_TEST_ARGS\"\n[ \"$1\" != rollback ]\n",
			installedVersion: "v9.9.9",
			wantStatus:       StageFailed,
			wantDetail:       "automatic rollback failed",
			wantError:        true,
			wantRollback:     true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			runnerPath := filepath.Join(directory, "runner.sh")
			installerPath := filepath.Join(directory, "install.sh")
			statusPath := filepath.Join(directory, "status")
			detailPath := filepath.Join(directory, "status-detail")
			binaryPath := filepath.Join(directory, "hserver-panel")
			cliPath := filepath.Join(directory, "hserverctl")
			installedBinaryPath := filepath.Join(directory, "installed-hserver-panel")
			installedCLIPath := filepath.Join(directory, "installed-hserverctl")
			argsPath := filepath.Join(directory, "installer-args")
			if err := os.WriteFile(runnerPath, []byte(upgradeRunnerScript), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(installerPath, []byte(test.installer), 0o700); err != nil {
				t.Fatal(err)
			}
			panelIdentity := "#!/bin/sh\nprintf 'hserver-panel %s (commit test, built test)\\n'\n"
			panelIdentity = strings.Replace(panelIdentity, "%s", test.installedVersion, 1)
			if test.installedPanel != "" {
				panelIdentity = "#!/bin/sh\nprintf '%s\\n' '" + test.installedPanel + "'\n"
			}
			cliIdentity := "#!/bin/sh\nprintf 'hserverctl %s (test, test)\\n'\n"
			cliIdentity = strings.Replace(cliIdentity, "%s", test.installedVersion, 1)
			candidatePanelIdentity := "#!/bin/sh\nprintf 'hserver-panel v1.1.0 (commit test, built test)\\n'\n"
			candidateCLIIdentity := "#!/bin/sh\nprintf 'hserverctl v1.1.0 (test, test)\\n'\n"
			if err := os.WriteFile(binaryPath, []byte(candidatePanelIdentity), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(cliPath, []byte(candidateCLIIdentity), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(installedBinaryPath, []byte(panelIdentity), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(installedCLIPath, []byte(cliIdentity), 0o700); err != nil {
				t.Fatal(err)
			}
			command := exec.Command(
				"/bin/sh", runnerPath, statusPath, detailPath, installerPath, binaryPath, cliPath,
				"v1.1.0", installedBinaryPath, installedCLIPath, directory, "native",
			)
			command.Env = append(os.Environ(), "HSERVER_TEST_ARGS="+argsPath)
			err := command.Run()
			if (err != nil) != test.wantError {
				t.Fatalf("runner error = %v", err)
			}
			payload, err := os.ReadFile(statusPath)
			if err != nil || strings.TrimSpace(string(payload)) != test.wantStatus {
				t.Fatalf("status = %q, %v", payload, err)
			}
			detail, err := os.ReadFile(detailPath)
			if err != nil || !strings.Contains(string(detail), test.wantDetail) {
				t.Fatalf("status detail = %q, %v", detail, err)
			}
			args, err := os.ReadFile(argsPath)
			wantArgs := []string{"upgrade", "--binary", binaryPath, "--cli-binary", cliPath}
			if test.wantRollback {
				wantArgs = append(wantArgs, "rollback")
			}
			want := strings.Join(wantArgs, "\n") + "\n"
			if err != nil || string(args) != want {
				t.Fatalf("installer args = %q, want %q, error %v", args, want, err)
			}
		})
	}
}

func TestUpgradeRunnerPassesFixedPreserveLayoutPaths(t *testing.T) {
	directory := t.TempDir()
	runnerPath := filepath.Join(directory, "runner.sh")
	installerPath := filepath.Join(directory, "install.sh")
	statusPath := filepath.Join(directory, "status")
	detailPath := filepath.Join(directory, "status-detail")
	candidatePanel := filepath.Join(directory, "candidate-panel")
	candidateCLI := filepath.Join(directory, "candidate-cli")
	installedDir := filepath.Join(directory, "opt", "hserver-panel", "bin")
	dataDir := filepath.Join(directory, "opt", "hserver-panel", "data")
	installedPanel := filepath.Join(installedDir, "hserver-panel")
	installedCLI := filepath.Join(installedDir, "hserverctl")
	if err := os.MkdirAll(installedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	panelIdentity := []byte("#!/bin/sh\nprintf 'hserver-panel v1.1.0 (commit test, built test)\\n'\n")
	cliIdentity := []byte("#!/bin/sh\nprintf 'hserverctl v1.1.0 (test, test)\\n'\n")
	for path, body := range map[string][]byte{
		runnerPath:     []byte(upgradeRunnerScript),
		candidatePanel: panelIdentity,
		candidateCLI:   cliIdentity,
		installedPanel: panelIdentity,
		installedCLI:   cliIdentity,
	} {
		if err := os.WriteFile(path, body, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	installer := `#!/bin/sh
set -eu
[ "$HSERVER_PRESERVE_LAYOUT" = 1 ]
[ "$HSERVER_BINARY_PATH" = "$HSERVER_EXPECT_PANEL" ]
[ "$HSERVER_CLI_PATH" = "$HSERVER_EXPECT_CLI" ]
[ "$HSERVER_DATA_DIR_PATH" = "$HSERVER_EXPECT_DATA" ]
exit 0
`
	if err := os.WriteFile(installerPath, []byte(installer), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(
		"/bin/sh", runnerPath, statusPath, detailPath, installerPath, candidatePanel, candidateCLI,
		"v1.1.0", installedPanel, installedCLI, dataDir, "preserve",
	)
	command.Env = append(os.Environ(),
		"HSERVER_EXPECT_PANEL="+installedPanel,
		"HSERVER_EXPECT_CLI="+installedCLI,
		"HSERVER_EXPECT_DATA="+dataDir,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("runner error = %v, output = %s", err, output)
	}
	payload, err := os.ReadFile(statusPath)
	if err != nil || strings.TrimSpace(string(payload)) != StageCompleted {
		t.Fatalf("status = %q, %v", payload, err)
	}
}

func stageForLauncher(t *testing.T) (*Manager, Stage) {
	t.Helper()
	version := "v1.1.0"
	platform := "linux_" + runtime.GOARCH
	archive := testReleaseArchive(t, version, platform, false)
	digest := sha256.Sum256(archive)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(archive)
	}))
	validationRoot := t.TempDir()
	installDir := filepath.Join(validationRoot, "opt", "hserver-panel", "bin")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatal(err)
	}
	panelPath := filepath.Join(installDir, "hserver-panel")
	cliPath := filepath.Join(installDir, "hserverctl")
	for _, destination := range []string{panelPath, cliPath} {
		if err := os.WriteFile(destination, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manager := NewManager(staticDiscovery{result: healthyUpdateResult(version, platform, server.URL, hex.EncodeToString(digest[:]), int64(len(archive)))}, t.TempDir())
	manager.panelPath = panelPath
	manager.cliPath = cliPath
	manager.pathValidationRoot = validationRoot
	manager.client = server.Client()
	staged, err := manager.Stage(context.Background())
	server.Close()
	if err != nil {
		t.Fatalf("Stage() error = %v", err)
	}
	return manager, staged
}
