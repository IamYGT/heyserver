package database

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

const databaseProbeTimeout = 5 * time.Second

var (
	// ErrNotConfigured identifies a host where neither supported local
	// database source has its client/management identity boundary installed.
	// It intentionally contains no command, path, socket, or authentication
	// detail so it is safe to pass to the aggregate integration-status layer.
	ErrNotConfigured = errors.New("database integration is not configured")

	// ErrDatabaseNotConfigured is an explicit alias for callers that prefer a
	// database-qualified sentinel. Both names retain the same errors.Is
	// identity and the same safe message.
	ErrDatabaseNotConfigured = ErrNotConfigured
)

// databaseProbeRunner is the narrow command boundary used by ProbeContext.
// LookPath is used only to distinguish an absent client from a configured
// source whose local observation failed. Run receives the caller-derived
// context, so cancellation and deadlines reach the real subprocess.
type databaseProbeRunner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, ...string) ([]byte, error)
}

// execDatabaseProbeRunner is the production local command runner. Readiness
// output is retained only in a small private buffer so the one missing-
// identity boundary can be classified; inventory rows, paths, and
// database/authentication diagnostics never cross the aggregate seam.
type execDatabaseProbeRunner struct{}

func (execDatabaseProbeRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (execDatabaseProbeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// The PostgreSQL path runs through sudo and both supported clients may
	// spawn helpers. Kill the whole process group on cancellation so a bounded
	// readiness request cannot leave a child attached to the host.
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
	var output boundedReadinessOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	runErr := cmd.Run()
	return output.Bytes(), runErr
}

// boundedReadinessOutput allows a production runner to classify the one
// installation-boundary case that is only present in command stderr (a
// missing postgres OS identity) without retaining unbounded SQL/client output.
// The bytes remain internal to the probe and are never included in its error.
type boundedReadinessOutput struct {
	mu   sync.Mutex
	data []byte
}

func (o *boundedReadinessOutput) Write(data []byte) (int, error) {
	const maxBytes = 4096
	originalLen := len(data)
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.data) < maxBytes {
		remaining := maxBytes - len(o.data)
		if len(data) > remaining {
			data = data[:remaining]
		}
		o.data = append(o.data, data...)
	}
	return originalLen, nil
}

func (o *boundedReadinessOutput) Bytes() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]byte(nil), o.data...)
}

// ProbeContext performs one fresh, read-only local database inventory
// observation. PostgreSQL and MariaDB are independent optional sources:
//
//   - at least one configured source completing its bounded inventory is
//     healthy, even when the other source fails;
//   - both clients genuinely absent is not_configured;
//   - a configured source that is stopped, socket/auth/permission denied, or
//     timed out is unavailable.
//
// The legacy List*Databases APIs remain unchanged and are not used here: they
// do not accept a caller context and their detailed errors are suitable for
// the database page, not for the aggregate readiness seam.
func ProbeContext(parent context.Context) (integrationstate.State, error) {
	return probeContext(parent, execDatabaseProbeRunner{})
}

// Probe is the background-context convenience form of ProbeContext.
func Probe() (integrationstate.State, error) {
	return ProbeContext(context.Background())
}

// ProbeDatabaseContext is the explicit database-named form used by callers
// that keep several local integration probes in one registry.
func ProbeDatabaseContext(parent context.Context) (integrationstate.State, error) {
	return ProbeContext(parent)
}

// ProbeDatabase is the background-context form of ProbeDatabaseContext.
func ProbeDatabase() (integrationstate.State, error) {
	return ProbeDatabaseContext(context.Background())
}

// ProbeReadinessContext is a descriptive alias for callers that use the
// readiness naming used by other local services.
func ProbeReadinessContext(parent context.Context) (integrationstate.State, error) {
	return ProbeContext(parent)
}

// ProbeReadiness is the background-context form of ProbeReadinessContext.
func ProbeReadiness() (integrationstate.State, error) {
	return ProbeReadinessContext(context.Background())
}

type databaseSourceProbe struct {
	engine       Engine
	configured   bool
	lookupError  error
	lookupAbsent bool
}

type databaseProbeResult struct {
	state integrationstate.State
	err   error
}

// probeContext is kept separate from ProbeContext so package tests can inject
// a fake runner without altering the host's clients or socket/auth policy.
func probeContext(parent context.Context, runner databaseProbeRunner) (integrationstate.State, error) {
	if parent == nil {
		parent = context.Background()
	}
	if err := parent.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	if runner == nil {
		return integrationstate.Unavailable, errors.New("database readiness runner is unavailable")
	}

	sources := discoverDatabaseSources(runner)
	configured := 0
	for _, source := range sources {
		if source.configured {
			configured++
		}
	}
	if configured == 0 {
		// A non-missing client discovery failure is still a configured-looking
		// source. It must never be downgraded to not_configured merely because
		// the discovery itself was denied.
		for _, source := range sources {
			if source.lookupError != nil && !source.lookupAbsent {
				return integrationstate.Unavailable, errors.New("database client discovery is unavailable")
			}
		}
		return integrationstate.NotConfigured, ErrNotConfigured
	}

	// Run configured source observations concurrently. A healthy MariaDB
	// source must not wait behind a PostgreSQL socket timeout (and vice versa),
	// while the buffered channel ensures a cancellation path cannot strand a
	// completed runner trying to report its result.
	results := make(chan databaseProbeResult, configured)
	for _, source := range sources {
		if !source.configured {
			continue
		}
		go func(source databaseSourceProbe) {
			state, err := runDatabaseSource(parent, runner, source)
			results <- databaseProbeResult{state: state, err: err}
		}(source)
	}

	seen := 0
	healthy := false
	notConfiguredOnly := true
	var firstFailure error
	for seen < configured {
		select {
		case <-parent.Done():
			return integrationstate.Unavailable, parent.Err()
		case result := <-results:
			seen++
			if result.err == nil && result.state == integrationstate.Healthy {
				healthy = true
				notConfiguredOnly = false
				continue
			}
			if result.state != integrationstate.NotConfigured {
				notConfiguredOnly = false
			}
			if firstFailure == nil ||
				(errors.Is(firstFailure, ErrNotConfigured) && result.state != integrationstate.NotConfigured) {
				firstFailure = result.err
			}
		}
	}

	if err := parent.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	if healthy {
		return integrationstate.Healthy, nil
	}
	if notConfiguredOnly {
		return integrationstate.NotConfigured, ErrNotConfigured
	}
	if firstFailure == nil {
		firstFailure = errors.New("database inventory observation failed")
	}
	return integrationstate.Unavailable, firstFailure
}

func discoverDatabaseSources(runner databaseProbeRunner) []databaseSourceProbe {
	return []databaseSourceProbe{
		discoverDatabaseSource(runner, EnginePostgres, "psql"),
		discoverDatabaseSource(runner, EngineMariaDB, "mysql"),
	}
}

func discoverDatabaseSource(runner databaseProbeRunner, engine Engine, client string) databaseSourceProbe {
	resolved, err := runner.LookPath(client)
	if err == nil {
		if strings.TrimSpace(resolved) == "" {
			return databaseSourceProbe{engine: engine, lookupError: errors.New("database client is unavailable")}
		}
		return databaseSourceProbe{engine: engine, configured: true}
	}
	if isMissingDatabaseClient(err) {
		return databaseSourceProbe{engine: engine, lookupAbsent: true}
	}
	return databaseSourceProbe{engine: engine, lookupError: errors.New("database client discovery is unavailable")}
}

func isMissingDatabaseClient(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, exec.ErrNotFound) || errors.Is(err, os.ErrNotExist) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "executable file not found") ||
		strings.Contains(message, "not found in $path") ||
		strings.Contains(message, "no such file or directory")
}

func runDatabaseSource(parent context.Context, runner databaseProbeRunner, source databaseSourceProbe) (integrationstate.State, error) {
	ctx, cancel := context.WithTimeout(parent, databaseProbeTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	var command string
	var args []string
	switch source.engine {
	case EnginePostgres:
		command = "sudo"
		args = []string{"-u", "postgres", "psql", "-d", "postgres", "-t", "-A", "-F\t", "-c", postgresReadinessQuery}
	case EngineMariaDB:
		command = "mysql"
		args = []string{"-u", "root", "-N", "-B", "-e", mariaDBReadinessQuery}
	default:
		return integrationstate.Unavailable, errors.New("unsupported database source")
	}

	output, err := runner.Run(ctx, command, args...)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		// A missing PostgreSQL OS identity is an absent source boundary. The
		// regular client can be installed while the installation-owned
		// postgres identity is not, so this is distinct from a stopped,
		// socket, authentication, or permission failure.
		if source.engine == EnginePostgres && (isMissingPostgresIdentity(err) || isMissingPostgresIdentityOutput(output)) {
			return integrationstate.NotConfigured, ErrNotConfigured
		}
		return integrationstate.Unavailable, errors.New("database inventory observation failed")
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	return integrationstate.Healthy, nil
}

func isMissingPostgresIdentity(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNotConfigured) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unknown user") ||
		strings.Contains(message, "unknown account") ||
		strings.Contains(message, "no such user") ||
		strings.Contains(message, "user postgres does not exist") ||
		strings.Contains(message, "postgres user not found") ||
		strings.Contains(message, "postgres identity missing") ||
		strings.Contains(message, "unknown postgres identity")
}

func isMissingPostgresIdentityOutput(output []byte) bool {
	return isMissingPostgresIdentity(errors.New(string(output)))
}

const postgresReadinessQuery = "SELECT datname, pg_get_userbyid(datdba), pg_size_pretty(pg_database_size(datname)) FROM pg_database WHERE NOT datistemplate AND datallowconn ORDER BY datname;"

const mariaDBReadinessQuery = "SELECT s.SCHEMA_NAME, COALESCE(ROUND(SUM(t.DATA_LENGTH + t.INDEX_LENGTH) / 1024 / 1024, 2), 0), COUNT(t.TABLE_NAME) FROM information_schema.SCHEMATA s LEFT JOIN information_schema.TABLES t ON t.TABLE_SCHEMA = s.SCHEMA_NAME WHERE s.SCHEMA_NAME NOT IN ('information_schema','performance_schema','mysql','sys') GROUP BY s.SCHEMA_NAME ORDER BY s.SCHEMA_NAME;"
