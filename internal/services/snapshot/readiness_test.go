package snapshot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

type snapshotReadinessFakeCall struct {
	name string
	args []string
}

type snapshotReadinessFakeRunner struct {
	mu         sync.Mutex
	lookupErrs map[string]error
	runOutput  []byte
	runErr     error
	run        func(context.Context, string, ...string) ([]byte, error)
	lookups    []string
	calls      []snapshotReadinessFakeCall
}

type snapshotReadinessFakeFileReader struct {
	lstat    func(context.Context, string) (os.FileInfo, error)
	readFile func(context.Context, string, int64) ([]byte, error)
}

func (r snapshotReadinessFakeFileReader) Lstat(ctx context.Context, name string) (os.FileInfo, error) {
	if r.lstat != nil {
		return r.lstat(ctx, name)
	}
	return nil, os.ErrNotExist
}

func (r snapshotReadinessFakeFileReader) ReadFile(ctx context.Context, name string, maxBytes int64) ([]byte, error) {
	if r.readFile != nil {
		return r.readFile(ctx, name, maxBytes)
	}
	return nil, os.ErrNotExist
}

func (r *snapshotReadinessFakeRunner) LookPath(name string) (string, error) {
	r.mu.Lock()
	r.lookups = append(r.lookups, name)
	r.mu.Unlock()
	if err := r.lookupErrs[name]; err != nil {
		return "", err
	}
	return name, nil
}

func (r *snapshotReadinessFakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, snapshotReadinessFakeCall{name: name, args: append([]string(nil), args...)})
	r.mu.Unlock()
	if r.run != nil {
		return r.run(ctx, name, args...)
	}
	return r.runOutput, r.runErr
}

func (r *snapshotReadinessFakeRunner) lookupCalls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.lookups...)
}

func (r *snapshotReadinessFakeRunner) commandCalls() []snapshotReadinessFakeCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make([]snapshotReadinessFakeCall, len(r.calls))
	for i, call := range r.calls {
		result[i] = snapshotReadinessFakeCall{name: call.name, args: append([]string(nil), call.args...)}
	}
	return result
}

type snapshotReadinessDrive struct {
	configPath string
	configured bool
}

func (d snapshotReadinessDrive) EnsureReady(string) error {
	panic("readiness probe must not refresh or mutate Drive credentials")
}

func (d snapshotReadinessDrive) RefreshSession(string) error {
	panic("readiness probe must not refresh or mutate Drive credentials")
}

func (d snapshotReadinessDrive) Connected(string) bool {
	panic("readiness probe must use its context-aware observation, not cached Drive status")
}

func (d snapshotReadinessDrive) RcloneConfigPath() string { return d.configPath }

func (d snapshotReadinessDrive) InternalRedirectURI(int) string {
	panic("readiness probe must not derive an OAuth refresh redirect")
}

func (d snapshotReadinessDrive) Configured(string) bool { return d.configured }

func newSnapshotReadinessDriveService(t *testing.T) (*Service, *snapshotReadinessFakeRunner, string) {
	t.Helper()
	dataDir := t.TempDir()
	configPath := filepath.Join(dataDir, "rclone.conf")
	if err := os.WriteFile(configPath, []byte("[hserver-gdrive]\ntype = drive\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := New(dataDir, filepath.Join(dataDir, "vhosts"), filepath.Join(dataDir, "backups"), 0, "restic", "rclone", "restic-password", snapshotReadinessDrive{
		configPath: configPath,
		configured: true,
	}, nil)
	fake := &snapshotReadinessFakeRunner{lookupErrs: map[string]error{}}
	service.readinessRunner = fake
	return service, fake, configPath
}

func TestProbeReadinessContextMissingDestinationOrPasswordIsNotConfigured(t *testing.T) {
	t.Run("password", func(t *testing.T) {
		service, fake, _ := newSnapshotReadinessDriveService(t)
		service.password = ""

		state, err := service.ProbeReadinessContext(context.Background())
		if state != integrationstate.NotConfigured || !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("state=%q err=%v; want not_configured/ErrNotConfigured", state, err)
		}
		if len(fake.lookupCalls()) != 0 || len(fake.commandCalls()) != 0 {
			t.Fatalf("readiness touched commands: lookups=%v calls=%v", fake.lookupCalls(), fake.commandCalls())
		}
	})

	t.Run("destination", func(t *testing.T) {
		service := New(t.TempDir(), "", "", 0, "restic", "rclone", "restic-password", nil, nil)
		fake := &snapshotReadinessFakeRunner{lookupErrs: map[string]error{}}
		service.readinessRunner = fake

		state, err := service.ProbeReadinessContext(context.Background())
		if state != integrationstate.NotConfigured || !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("state=%q err=%v; want not_configured/ErrNotConfigured", state, err)
		}
		if len(fake.lookupCalls()) != 0 || len(fake.commandCalls()) != 0 {
			t.Fatalf("readiness touched commands: lookups=%v calls=%v", fake.lookupCalls(), fake.commandCalls())
		}
	})

	t.Run("missing S3 credential file", func(t *testing.T) {
		dataDir := t.TempDir()
		s3 := validS3Config(t)
		if err := os.Remove(s3.AccessKeyFile); err != nil {
			t.Fatal(err)
		}
		service := NewWithS3(dataDir, "", "", 0, "restic", "rclone", "restic-password", nil, s3, nil)
		service.readinessRunner = &snapshotReadinessFakeRunner{lookupErrs: map[string]error{}}
		if err := service.UpdateSettings(SettingsUpdate{
			Destination: DestinationS3,
			RepoFolder:  defaultRepoFolder,
			KeepDaily:   14,
			KeepWeekly:  8,
			KeepMonthly: 6,
		}); err != nil {
			t.Fatal(err)
		}

		state, err := service.ProbeReadinessContext(context.Background())
		if state != integrationstate.NotConfigured || !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("state=%q err=%v; want not_configured/ErrNotConfigured", state, err)
		}
	})
}

func TestProbeReadinessContextMissingResticOrRcloneIsUnavailable(t *testing.T) {
	t.Run("restic", func(t *testing.T) {
		service, fake, _ := newSnapshotReadinessDriveService(t)
		fake.lookupErrs["restic"] = errors.New("executable file not found: /private/restic")

		state, err := service.ProbeReadinessContext(context.Background())
		if state != integrationstate.Unavailable || !errors.Is(err, ErrReadinessUnavailable) {
			t.Fatalf("state=%q err=%v; want unavailable/ErrReadinessUnavailable", state, err)
		}
		if got := fake.lookupCalls(); !equalSnapshotReadinessStrings(got, []string{"restic"}) {
			t.Fatalf("lookups=%v; want only restic", got)
		}
	})

	t.Run("rclone", func(t *testing.T) {
		service, fake, _ := newSnapshotReadinessDriveService(t)
		fake.lookupErrs["rclone"] = errors.New("executable file not found: /private/rclone")

		state, err := service.ProbeReadinessContext(context.Background())
		if state != integrationstate.Unavailable || !errors.Is(err, ErrReadinessUnavailable) {
			t.Fatalf("state=%q err=%v; want unavailable/ErrReadinessUnavailable", state, err)
		}
		if got := fake.lookupCalls(); !equalSnapshotReadinessStrings(got, []string{"restic", "rclone"}) {
			t.Fatalf("lookups=%v; want restic then rclone", got)
		}
		if len(fake.commandCalls()) != 0 {
			t.Fatalf("destination command ran after missing rclone: %v", fake.commandCalls())
		}
	})
}

func TestProbeReadinessContextDestinationFailureIsUnavailableAndSafe(t *testing.T) {
	service, fake, configPath := newSnapshotReadinessDriveService(t)
	const secret = "restic-password-not-for-errors"
	fake.runOutput = []byte(strings.Repeat("provider output with secret "+secret+" and path "+configPath+"\n", 10000))
	fake.runErr = errors.New("provider failed for " + secret + " at " + configPath)

	state, err := service.ProbeReadinessContext(context.Background())
	if state != integrationstate.Unavailable || !errors.Is(err, ErrReadinessUnavailable) {
		t.Fatalf("state=%q err=%v; want unavailable/ErrReadinessUnavailable", state, err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), configPath) || strings.Contains(err.Error(), "provider failed") {
		t.Fatalf("probe error leaked provider details: %v", err)
	}
	calls := fake.commandCalls()
	if len(calls) != 1 || calls[0].name != "restic" {
		t.Fatalf("calls=%v; want one read-only restic call", calls)
	}
	if containsSnapshotReadinessMutator(calls[0].args) {
		t.Fatalf("mutating command reached readiness runner: %v", calls[0].args)
	}
}

func TestProbeReadinessContextUninitializedRepositoryIsHealthyButAuthAndNetworkFailuresAreUnavailable(t *testing.T) {
	tests := []struct {
		name      string
		output    string
		runErr    error
		wantState integrationstate.State
		wantErr   error
	}{
		{
			name:      "first-use repository",
			output:    "Fatal: unable to open config file: The specified key does not exist",
			runErr:    errors.New("exit status 1"),
			wantState: integrationstate.Healthy,
		},
		{
			name:      "wrong password",
			output:    "Fatal: unable to open config file: crypto: wrong password or no key found",
			runErr:    errors.New("exit status 1"),
			wantState: integrationstate.Unavailable,
			wantErr:   ErrReadinessUnavailable,
		},
		{
			name:      "wrong password with uninitialized prefix",
			output:    "config file does not exist: crypto: wrong password or no key found",
			runErr:    errors.New("exit status 1"),
			wantState: integrationstate.Unavailable,
			wantErr:   ErrReadinessUnavailable,
		},
		{
			name:      "network failure",
			output:    "Fatal: unable to open config file: dial tcp: connection refused",
			runErr:    errors.New("exit status 1"),
			wantState: integrationstate.Unavailable,
			wantErr:   ErrReadinessUnavailable,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, fake, _ := newSnapshotReadinessDriveService(t)
			fake.runOutput = []byte(test.output)
			fake.runErr = test.runErr

			state, err := service.ProbeReadinessContext(context.Background())
			if state != test.wantState {
				t.Fatalf("state=%q want %q err=%v", state, test.wantState, err)
			}
			if test.wantErr != nil && !errors.Is(err, test.wantErr) {
				t.Fatalf("err=%v; want errors.Is(..., %v)", err, test.wantErr)
			}
			if test.wantErr == nil && err != nil {
				t.Fatalf("err=%v; want nil for readiness-ready first-use repository", err)
			}
		})
	}
}

func TestProbeReadinessContextHealthyAfterFreshReadOnlyObservation(t *testing.T) {
	service, fake, _ := newSnapshotReadinessDriveService(t)

	state, err := service.ProbeReadinessContext(context.Background())
	if state != integrationstate.Healthy || err != nil {
		t.Fatalf("state=%q err=%v; want healthy", state, err)
	}
	if got := fake.lookupCalls(); !equalSnapshotReadinessStrings(got, []string{"restic", "rclone"}) {
		t.Fatalf("lookups=%v; want restic and rclone", got)
	}
	calls := fake.commandCalls()
	if len(calls) != 1 || calls[0].name != "restic" {
		t.Fatalf("calls=%v; want one restic observation", calls)
	}
	if !containsSnapshotReadinessArgs(calls[0].args, "--no-cache") || !containsSnapshotReadinessArgs(calls[0].args, "snapshots") || !containsSnapshotReadinessArgs(calls[0].args, "--json") {
		t.Fatalf("args=%v; want --no-cache snapshots --json", calls[0].args)
	}
	if containsSnapshotReadinessMutator(calls[0].args) {
		t.Fatalf("mutating command reached readiness runner: %v", calls[0].args)
	}
}

func TestProbeReadinessContextProductionRunnerAcceptsUninitializedRepositoryWithoutCacheCreation(t *testing.T) {
	dir := t.TempDir()
	resticPath := filepath.Join(dir, "restic")
	rclonePath := filepath.Join(dir, "rclone")
	for path, script := range map[string]string{
		resticPath: `#!/bin/sh
echo "Fatal: unable to open config file: The specified key does not exist" >&2
exit 1
`,
		rclonePath: `#!/bin/sh
exit 0
`,
	} {
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(dir, "rclone.conf")
	if err := os.WriteFile(configPath, []byte("[hserver-gdrive]\ntype = drive\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := New(dir, filepath.Join(dir, "vhosts"), filepath.Join(dir, "backups"), 0, resticPath, rclonePath, "restic-password", snapshotReadinessDrive{
		configPath: configPath,
		configured: true,
	}, nil)

	state, err := service.ProbeReadinessContext(context.Background())
	if state != integrationstate.Healthy || err != nil {
		t.Fatalf("state=%q err=%v; want healthy for first-use repository", state, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "restic-cache")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("readiness created or touched cache directory: stat err=%v", err)
	}
}

func TestProbeReadinessContextProductionRunnerSanitizesProviderFailure(t *testing.T) {
	dir := t.TempDir()
	resticPath := filepath.Join(dir, "restic")
	rclonePath := filepath.Join(dir, "rclone")
	secret := "provider-secret-must-not-escape"
	for path, script := range map[string]string{
		resticPath: "#!/bin/sh\nprintf '%s\\n' 'dial tcp: " + secret + "' >&2\nexit 1\n",
		rclonePath: "#!/bin/sh\nexit 0\n",
	} {
		if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(dir, "rclone.conf")
	if err := os.WriteFile(configPath, []byte("[hserver-gdrive]\ntype = drive\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := New(dir, "", "", 0, resticPath, rclonePath, "restic-password", snapshotReadinessDrive{
		configPath: configPath,
		configured: true,
	}, nil)

	state, err := service.ProbeReadinessContext(context.Background())
	if state != integrationstate.Unavailable || !errors.Is(err, ErrReadinessUnavailable) {
		t.Fatalf("state=%q err=%v; want unavailable/ErrReadinessUnavailable", state, err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), configPath) || strings.Contains(err.Error(), "dial tcp") {
		t.Fatalf("production provider details leaked through readiness error: %v", err)
	}
}

func TestExecSnapshotReadinessRunnerBoundsOutputAndPreservesClassificationInput(t *testing.T) {
	runner := execSnapshotReadinessRunner{env: os.Environ()}
	output, runErr := runner.Run(context.Background(), "/bin/sh", "-c", `
printf '%s\n' 'repository does not exist'
dd if=/dev/zero bs=65536 count=1 2>/dev/null
exit 1
`)
	if runErr == nil {
		t.Fatal("runner unexpectedly succeeded")
	}
	if len(output) > maxSnapshotReadinessOutput {
		t.Fatalf("captured output length=%d; max=%d", len(output), maxSnapshotReadinessOutput)
	}
	if !isReadinessRepositoryUninitialized(output, runErr) {
		t.Fatalf("bounded first-use output was not classified as uninitialized: %q", output[:minSnapshotReadinessTest(len(output), 80)])
	}
}

func TestExecSnapshotReadinessRunnerCancelsProcessGroup(t *testing.T) {
	runner := execSnapshotReadinessRunner{env: os.Environ()}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, runErr := runner.Run(ctx, "/bin/sh", "-c", "sleep 30 & wait")
	if runErr == nil {
		t.Fatal("runner unexpectedly succeeded")
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("context error=%v; want deadline exceeded", ctx.Err())
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("process-group cancellation took %s; descendant likely remained attached", elapsed)
	}
}

func TestProbeReadinessContextS3DoesNotRequireRclone(t *testing.T) {
	service := NewWithS3(t.TempDir(), "", "", 0, "restic", "rclone-not-installed", "restic-password", nil, validS3Config(t), nil)
	fake := &snapshotReadinessFakeRunner{lookupErrs: map[string]error{}}
	service.readinessRunner = fake
	if err := service.UpdateSettings(SettingsUpdate{
		Destination: DestinationS3,
		RepoFolder:  defaultRepoFolder,
		KeepDaily:   14, KeepWeekly: 8, KeepMonthly: 6,
	}); err != nil {
		t.Fatal(err)
	}

	state, err := service.ProbeReadinessContext(context.Background())
	if state != integrationstate.Healthy || err != nil {
		t.Fatalf("state=%q err=%v; want healthy with native S3 backend", state, err)
	}
	if got := fake.lookupCalls(); !equalSnapshotReadinessStrings(got, []string{"restic"}) {
		t.Fatalf("lookups=%v; want only restic for S3", got)
	}
}

func TestProbeReadinessContextPropagatesCancellationToDestinationRunner(t *testing.T) {
	service, fake, _ := newSnapshotReadinessDriveService(t)
	started := make(chan struct{})
	finished := make(chan struct{})
	fake.run = func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		close(started)
		defer close(finished)
		<-ctx.Done()
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan struct {
		state integrationstate.State
		err   error
	}, 1)
	go func() {
		state, err := service.ProbeReadinessContext(ctx)
		result <- struct {
			state integrationstate.State
			err   error
		}{state: state, err: err}
	}()

	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("readiness probe did not reach destination runner")
	}
	select {
	case got := <-result:
		if got.state != integrationstate.Unavailable || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("cancelled probe = state %q err %v; want unavailable/canceled", got.state, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled readiness probe did not return")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("cancelled destination runner did not finish")
	}
}

func TestLoadReadinessSettingsStopsContextAwareBoundedRead(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	maxBytes := make(chan int64, 1)
	reader := snapshotReadinessFakeFileReader{
		readFile: func(ctx context.Context, _ string, max int64) ([]byte, error) {
			maxBytes <- max
			close(started)
			defer close(finished)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	service := &Service{dataDir: t.TempDir(), readinessFileReader: reader}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := service.loadReadinessSettings(ctx)
		result <- err
	}()

	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("settings read did not reach context-aware file seam")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("settings read error=%v; want canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled settings read did not return")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("canceled settings file seam did not finish")
	}
	if got := <-maxBytes; got != maxSnapshotReadinessFileSize {
		t.Fatalf("settings maxBytes=%d; want %d", got, maxSnapshotReadinessFileSize)
	}
}

func TestReadinessCredentialObservationStopsContextAwareFileOperation(t *testing.T) {
	started := make(chan struct{})
	finished := make(chan struct{})
	credentialPath := filepath.Join(t.TempDir(), "credential")
	reader := snapshotReadinessFakeFileReader{
		lstat: func(ctx context.Context, _ string) (os.FileInfo, error) {
			close(started)
			defer close(finished)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := readProtectedCredentialContext(ctx, reader, credentialPath, "TEST_CREDENTIAL_FILE")
		result <- err
	}()

	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("credential observation did not reach context-aware file seam")
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("credential read error=%v; want canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled credential observation did not return")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("canceled credential file seam did not finish")
	}
}

func equalSnapshotReadinessStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func containsSnapshotReadinessArgs(args []string, wanted string) bool {
	for _, arg := range args {
		if arg == wanted {
			return true
		}
	}
	return false
}

func containsSnapshotReadinessMutator(args []string) bool {
	for _, arg := range args {
		switch arg {
		case "backup", "init", "unlock", "forget", "prune", "restore", "mkdir", "purge", "deletefile":
			return true
		}
	}
	return false
}

func minSnapshotReadinessTest(a, b int) int {
	if a < b {
		return a
	}
	return b
}
