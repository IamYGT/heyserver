package snapshot

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

const (
	snapshotReadinessTimeout     = 5 * time.Second
	maxSnapshotReadinessFileSize = 1 << 20
	maxSnapshotReadinessOutput   = 16 * 1024
)

var (
	// errSnapshotReadinessUnavailable is deliberately generic. Readiness is
	// consumed by an aggregate API, so command output, repository names,
	// credential paths, and provider diagnostics must never cross this seam.
	// It shares the existing destination-unavailable identity for callers that
	// already classify snapshot provider failures with errors.Is.
	errSnapshotReadinessUnavailable = ErrDestinationUnavailable

	// ErrReadinessNotConfigured is the descriptive alias for the package's
	// existing safe not-configured sentinel. Keeping the alias lets callers
	// distinguish readiness intent without creating a second errors.Is class.
	ErrReadinessNotConfigured = ErrNotConfigured

	// ErrReadinessUnavailable is the exported safe readiness failure sentinel.
	// Detailed subprocess and provider errors are intentionally not wrapped.
	ErrReadinessUnavailable = errSnapshotReadinessUnavailable
)

// snapshotReadinessRunner is the narrow command boundary for one fresh
// snapshot observation. LookPath distinguishes an absent local dependency;
// Run receives the caller-derived context and returns only bounded output for
// in-process classification. The production runner never exposes that output
// through the readiness API.
type snapshotReadinessRunner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, ...string) ([]byte, error)
}

// snapshotReadinessFileReader is the bounded local-file seam used before the
// remote observation. The context is checked by the production implementation
// before and after each operation; no goroutine is used to wrap an
// uncancellable os.ReadFile call.
type snapshotReadinessFileReader interface {
	Lstat(context.Context, string) (os.FileInfo, error)
	ReadFile(context.Context, string, int64) ([]byte, error)
}

type osSnapshotReadinessFileReader struct{}

func (osSnapshotReadinessFileReader) Lstat(ctx context.Context, name string) (os.FileInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(name)
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	return info, err
}

func (osSnapshotReadinessFileReader) ReadFile(ctx context.Context, name string, maxBytes int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if maxBytes <= 0 || maxBytes > maxSnapshotReadinessFileSize {
		maxBytes = maxSnapshotReadinessFileSize
	}

	// Reject links and non-regular files before opening. In particular, this
	// prevents a FIFO from turning a readiness request into an unbounded,
	// uncancellable read.
	info, err := os.Lstat(name)
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("readiness file is not regular")
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}

	file, err := os.Open(name)
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxBytes {
		return nil, errors.New("readiness file is too large")
	}
	return data, nil
}

// execSnapshotReadinessRunner executes only the read-only readiness command.
// env is assembled from the selected provider's existing environment/file
// references and is never logged or returned.
type execSnapshotReadinessRunner struct {
	env []string
}

// boundedSnapshotReadinessOutput keeps only enough private command output to
// classify the expected first-use repository-not-initialized response. It is
// never returned by the readiness API or copied into a public error.
type boundedSnapshotReadinessOutput struct {
	mu   sync.Mutex
	data []byte
}

func (o *boundedSnapshotReadinessOutput) Write(data []byte) (int, error) {
	originalLen := len(data)
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.data) < maxSnapshotReadinessOutput {
		remaining := maxSnapshotReadinessOutput - len(o.data)
		if len(data) > remaining {
			data = data[:remaining]
		}
		o.data = append(o.data, data...)
	}
	return originalLen, nil
}

func (o *boundedSnapshotReadinessOutput) Bytes() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]byte(nil), o.data...)
}

func (execSnapshotReadinessRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (r execSnapshotReadinessRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append([]string(nil), r.env...)

	// A wrapper/provider process can leave descendants holding stdio open
	// after the direct process is canceled. Kill the process group as well and
	// bound the post-cancellation wait so a status request cannot leak.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	cmd.WaitDelay = 250 * time.Millisecond

	// Keep only a small private prefix: first-use repositories return a
	// provider-specific "repository does not exist" diagnostic on stderr, and
	// that case is readiness-ready without initialization. The bounded bytes
	// never cross the probe's sanitized error boundary.
	var output boundedSnapshotReadinessOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return output.Bytes(), err
	}
	return output.Bytes(), nil
}

// ProbeReadiness performs one fresh, read-only snapshot readiness observation.
func (s *Service) ProbeReadiness() (integrationstate.State, error) {
	return s.ProbeReadinessContext(context.Background())
}

// ProbeReadinessContext classifies the selected snapshot destination without
// starting a backup or changing any local/remote state:
//
//   - an absent repository password or selected destination is not_configured;
//   - a missing restic/rclone executable or failed destination observation is
//     unavailable;
//   - healthy requires a successful, fresh `restic snapshots --json` probe;
//     the existing first-use "repository does not exist" contract is also
//     readiness-ready after that non-mutating observation.
//
// The restic command runs with --no-cache and the production runner retains
// only a bounded private output prefix for classification. No init, unlock,
// backup, forget/prune, token refresh, cache creation, or settings write is
// reachable from this method.
func (s *Service) ProbeReadinessContext(parent context.Context) (integrationstate.State, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, snapshotReadinessTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	if s == nil {
		return integrationstate.NotConfigured, ErrNotConfigured
	}

	settings, err := s.loadReadinessSettings(ctx)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		return integrationstate.Unavailable, errSnapshotReadinessUnavailable
	}
	settings.Destination = normalizedDestination(settings.Destination)
	if strings.TrimSpace(s.password) == "" {
		return integrationstate.NotConfigured, ErrNotConfigured
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	if !validSnapshotReadinessRepoFolder(settings.RepoFolder) {
		return integrationstate.Unavailable, errSnapshotReadinessUnavailable
	}

	// Validate the selected provider's local configuration before constructing
	// the restic runner. An absent destination is a configuration state; a
	// present but malformed/unreadable destination is an unavailable state.
	if state, configErr := s.readinessDestinationConfigState(ctx, settings); state != integrationstate.Healthy {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		if configErr != nil && (errors.Is(configErr, context.Canceled) || errors.Is(configErr, context.DeadlineExceeded)) {
			return integrationstate.Unavailable, configErr
		}
		return state, readinessErrorForState(state)
	}

	// runner() reads only installation-owned provider credentials. It never
	// performs a remote command; all returned errors are reduced to the safe
	// readiness vocabulary below.
	restic, err := s.readinessResticRunner(ctx, settings)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return integrationstate.Unavailable, err
		}
		// Missing installation-owned credential files mean the optional
		// destination is not configured yet. Present but unreadable, unsafe, or
		// malformed files remain unavailable.
		if errors.Is(err, os.ErrNotExist) {
			return integrationstate.NotConfigured, ErrNotConfigured
		}
		return integrationstate.Unavailable, errSnapshotReadinessUnavailable
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	runner := s.readinessRunner
	if runner == nil {
		runner = execSnapshotReadinessRunner{env: restic.readinessEnv()}
	}

	resticBin := strings.TrimSpace(restic.bin)
	if resticBin == "" {
		resticBin = "restic"
	}
	resolvedRestic, err := runner.LookPath(resticBin)
	if err != nil || strings.TrimSpace(resolvedRestic) == "" {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		return integrationstate.Unavailable, errSnapshotReadinessUnavailable
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	// Google Drive is the rclone-backed provider. S3 uses restic's native S3
	// backend and therefore does not require an unrelated rclone installation.
	if normalizedDestination(settings.Destination) == DestinationGoogleDrive {
		rcloneBin := strings.TrimSpace(restic.rcloneBin)
		if rcloneBin == "" {
			rcloneBin = "rclone"
		}
		resolvedRclone, lookupErr := runner.LookPath(rcloneBin)
		if lookupErr != nil || strings.TrimSpace(resolvedRclone) == "" {
			if contextErr := ctx.Err(); contextErr != nil {
				return integrationstate.Unavailable, contextErr
			}
			return integrationstate.Unavailable, errSnapshotReadinessUnavailable
		}
		if err := ctx.Err(); err != nil {
			return integrationstate.Unavailable, err
		}
	}

	// Keep global restic options before the subcommand. --no-cache ensures the
	// observation cannot create or update a local cache directory.
	args := restic.withGlobalOpts()
	args = append(args, "--no-cache", "snapshots", "--json")
	output, err := runner.Run(ctx, resolvedRestic, args...)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		if isReadinessRepositoryUninitialized(output, err) {
			return integrationstate.Healthy, nil
		}
		return integrationstate.Unavailable, errSnapshotReadinessUnavailable
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	return integrationstate.Healthy, nil
}

// ProbeContext is the short compatibility alias used by aggregate probe
// registries. It intentionally shares the read-only implementation above.
func (s *Service) ProbeContext(parent context.Context) (integrationstate.State, error) {
	return s.ProbeReadinessContext(parent)
}

// Probe is the background-context compatibility form of ProbeContext.
func (s *Service) Probe() (integrationstate.State, error) {
	return s.ProbeReadiness()
}

func (s *Service) readinessFileOps() snapshotReadinessFileReader {
	if s != nil && s.readinessFileReader != nil {
		return s.readinessFileReader
	}
	return osSnapshotReadinessFileReader{}
}

// loadReadinessSettings mirrors loadSettings without its unbounded direct
// os.ReadFile call. Missing settings retain the established defaults, while
// every present file is read through the bounded, context-aware seam.
func (s *Service) loadReadinessSettings(ctx context.Context) (Settings, error) {
	if err := ctx.Err(); err != nil {
		return Settings{}, err
	}
	defaults := Settings{
		Destination: DestinationGoogleDrive,
		RepoFolder:  defaultRepoFolder,
		KeepDaily:   14,
		KeepWeekly:  8,
		KeepMonthly: 6,
	}
	raw, err := s.readinessFileOps().ReadFile(ctx, s.settingsFile(), maxSnapshotReadinessFileSize)
	if contextErr := ctx.Err(); contextErr != nil {
		return Settings{}, contextErr
	}
	if err != nil {
		if os.IsNotExist(err) {
			return defaults, nil
		}
		return Settings{}, err
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return Settings{}, contextErr
	}

	var settings Settings
	if err := json.Unmarshal(raw, &settings); err != nil {
		return Settings{}, err
	}
	if contextErr := ctx.Err(); contextErr != nil {
		return Settings{}, contextErr
	}
	if settings.RepoFolder == "" {
		settings.RepoFolder = defaultRepoFolder
	}
	if settings.Destination == "" {
		settings.Destination = DestinationGoogleDrive
	}
	if settings.KeepDaily <= 0 {
		settings.KeepDaily = defaults.KeepDaily
	}
	if settings.KeepWeekly <= 0 {
		settings.KeepWeekly = defaults.KeepWeekly
	}
	if settings.KeepMonthly <= 0 {
		settings.KeepMonthly = defaults.KeepMonthly
	}
	return settings, nil
}

func (s *Service) readinessResticRunner(ctx context.Context, settings Settings) (*resticRunner, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if normalizedDestination(settings.Destination) != DestinationS3 {
		return s.runner(settings)
	}

	config := s.s3.normalized()
	credentials, err := config.readinessCredentials(ctx, s.readinessFileOps())
	if err != nil {
		return nil, err
	}
	runner := &resticRunner{
		bin:           s.resticBin,
		rcloneBin:     s.rcloneBin,
		password:      s.password,
		repoFolder:    settings.RepoFolder,
		cacheDir:      filepath.Join(s.dataDir, "restic-cache"),
		repositoryURL: config.repository(settings.RepoFolder),
		extraEnv: []string{
			"AWS_ACCESS_KEY_ID=" + credentials.accessKey,
			"AWS_SECRET_ACCESS_KEY=" + credentials.secretKey,
		},
		globalOptions: []string{"-o", "s3.bucket-lookup=" + config.BucketLookup},
	}
	if config.Region != "" {
		runner.extraEnv = append(runner.extraEnv, "AWS_DEFAULT_REGION="+config.Region)
	}
	return runner, nil
}

func readinessErrorForState(state integrationstate.State) error {
	if state == integrationstate.NotConfigured {
		return ErrNotConfigured
	}
	return errSnapshotReadinessUnavailable
}

func (s *Service) readinessDestinationConfigState(ctx context.Context, settings Settings) (integrationstate.State, error) {
	switch normalizedDestination(settings.Destination) {
	case DestinationGoogleDrive:
		return s.validateReadinessDriveConfig(ctx)
	case DestinationS3:
		return s.validateReadinessS3Config(), nil
	default:
		return integrationstate.Unavailable, nil
	}
}

func (s *Service) validateReadinessDriveConfig(ctx context.Context) (integrationstate.State, error) {
	if s == nil || s.drive == nil {
		return integrationstate.NotConfigured, nil
	}
	if configured, ok := s.drive.(driveConfigurationGate); ok && !configured.Configured("") {
		return integrationstate.NotConfigured, nil
	}
	configPath := strings.TrimSpace(s.drive.RcloneConfigPath())
	if configPath == "" {
		return integrationstate.NotConfigured, nil
	}
	if !filepath.IsAbs(configPath) || strings.ContainsAny(configPath, "\x00\r\n\t") || filepath.Clean(configPath) != configPath {
		return integrationstate.Unavailable, nil
	}
	info, err := s.readinessFileOps().Lstat(ctx, configPath)
	if contextErr := ctx.Err(); contextErr != nil {
		return integrationstate.Unavailable, contextErr
	}
	if err != nil {
		if os.IsNotExist(err) {
			return integrationstate.NotConfigured, nil
		}
		return integrationstate.Unavailable, err
	}
	if info == nil {
		return integrationstate.Unavailable, nil
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return integrationstate.Unavailable, nil
	}
	if info.Mode().Perm()&0o077 != 0 {
		return integrationstate.Unavailable, nil
	}
	return integrationstate.Healthy, nil
}

func (s *Service) validateReadinessS3Config() integrationstate.State {
	if s == nil {
		return integrationstate.NotConfigured
	}
	config := s.s3.normalized()
	if config.Endpoint == "" || config.Bucket == "" || config.AccessKeyFile == "" || config.SecretKeyFile == "" {
		return integrationstate.NotConfigured
	}
	return integrationstate.Healthy
}

func validSnapshotReadinessRepoFolder(folder string) bool {
	return folder != "" && repoFolderPattern.MatchString(folder) && filepath.ToSlash(filepath.Clean(folder)) == folder
}

// readinessEnv is the non-mutating counterpart to resticRunner.env. It does
// not create the cache directory and strips inherited provider credentials so
// the selected destination can receive only the existing password/file-ref
// material assembled by runner().
func (r *resticRunner) readinessEnv() []string {
	strip := []string{
		"RESTIC_", "RCLONE_CONFIG", "AWS_ACCESS_KEY_ID=", "AWS_SECRET_ACCESS_KEY=",
		"AWS_DEFAULT_REGION=", "AWS_SESSION_TOKEN=", "AWS_PROFILE=", "AWS_SHARED_CREDENTIALS_FILE=",
	}
	base := make([]string, 0, len(os.Environ())+8)
	for _, entry := range os.Environ() {
		remove := false
		for _, prefix := range strip {
			if strings.HasPrefix(entry, prefix) {
				remove = true
				break
			}
		}
		if !remove {
			base = append(base, entry)
		}
	}
	base = append(base,
		"RESTIC_PASSWORD="+r.password,
		"RESTIC_REPOSITORY="+r.repository(),
	)
	if r.repositoryURL == "" {
		base = append(base, "RCLONE_CONFIG="+r.rcloneConfig)
		return append(base, resticReadinessEnvExtras()...)
	}
	return append(base, r.extraEnv...)
}

func resticReadinessEnvExtras() []string {
	// Keep the same provider-neutral tuning as normal restic operations while
	// avoiding RESTIC_CACHE_DIR, whose creation would violate read-only probe
	// semantics.
	return []string{
		"RESTIC_PACK_SIZE=64",
		"RCLONE_RETRIES=10",
		"RCLONE_LOW_LEVEL_RETRIES=20",
		"RCLONE_TIMEOUT=5m",
	}
}

// isReadinessRepositoryUninitialized matches the established repository
// observation contract without treating authentication, network, or other
// provider failures as healthy. Both inputs are bounded before they are
// combined so fake runners cannot turn a readiness request into an
// unbounded classification allocation. The resulting error is private and
// never returned to callers.
func isReadinessRepositoryUninitialized(output []byte, runErr error) bool {
	if runErr == nil {
		return false
	}
	if len(output) > maxSnapshotReadinessOutput {
		output = output[:maxSnapshotReadinessOutput]
	}
	message := string(output)
	errText := runErr.Error()
	if len(errText) > maxSnapshotReadinessOutput {
		errText = errText[:maxSnapshotReadinessOutput]
	}
	combined := message + "\n" + errText
	lower := strings.ToLower(combined)
	// Restic's wrong-password response must remain unavailable even if a
	// wrapper prepends a generic config/repository phrase to the same error.
	for _, marker := range []string{
		"wrong password",
		"no key found",
		"authentication failed",
		"invalid password",
		"dial tcp",
		"connection refused",
		"connection reset",
		"i/o timeout",
		"no such host",
		"temporary failure",
		"tls handshake",
		"http status",
		"status code",
		"unauthorized",
		"forbidden",
		"access denied",
	} {
		if strings.Contains(lower, marker) {
			return false
		}
	}
	return isRepositoryUninitializedError(errors.New(combined))
}
