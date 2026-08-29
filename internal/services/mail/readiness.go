package mail

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

const (
	mailReadinessTimeout = 5 * time.Second

	// This is an intentionally small, read-only management API request. The
	// existing principal listing endpoint is used instead of a provider-specific
	// health endpoint so the observation remains useful for the catalog seam.
	mailReadinessAPIPath = "/api/principal?type=domain&limit=1"
)

var (
	// errMailReadinessUnavailable is deliberately generic. Provider responses,
	// configured URLs, local paths, credentials, and command output must stay
	// inside the mail service boundary.
	errMailReadinessUnavailable = errors.New("mail readiness is unavailable")

	// errMailServiceObservationUnavailable distinguishes the two internal
	// observations while retaining a safe error for direct probe callers.
	errMailServiceObservationUnavailable = errors.New("mail service state is unavailable")
	// errMailManagementAPIUnavailable is returned only inside this package and
	// is never populated with an HTTP response or URL.
	errMailManagementAPIUnavailable = errors.New("mail management API is unavailable")
)

// readinessCommandRunner is the context-aware systemd boundary used by the
// read-only readiness probe. The production implementation invokes the real
// systemctl command; tests inject a fake so no host state is mutated or
// required.
type readinessCommandRunner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}

type execReadinessCommandRunner struct{}

func (execReadinessCommandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// `systemctl is-active` only needs its short state token. Keep even a
	// misbehaving observer from retaining unbounded output, and discard stderr
	// because it is never part of a readiness result.
	var output boundedMailReadinessOutput
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	// A wrapper can leave descendants alive with inherited descriptors after
	// CommandContext kills only the direct child. Bound the whole process group
	// and its post-cancellation wait so aggregate deadlines remain real.
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
	runErr := cmd.Run()
	// Read only after Run completes: output.Bytes() before cmd.Run() would
	// capture an empty slice because Go evaluates return expressions left to
	// right.
	return output.Bytes(), runErr
}

// boundedMailReadinessOutput prevents command output from becoming an
// unbounded in-memory value. Its contents are used only to compare the
// systemd state and are never returned to a caller.
type boundedMailReadinessOutput struct {
	data []byte
}

func (o *boundedMailReadinessOutput) Write(data []byte) (int, error) {
	const maxBytes = 4096
	originalLen := len(data)
	if len(o.data) < maxBytes {
		remaining := maxBytes - len(o.data)
		if len(data) > remaining {
			data = data[:remaining]
		}
		o.data = append(o.data, data...)
	}
	return originalLen, nil
}

func (o *boundedMailReadinessOutput) Bytes() []byte {
	return append([]byte(nil), o.data...)
}

// ProbeReadiness performs one fresh, read-only local Stalwart readiness
// observation using a background context.
func (s *Service) ProbeReadiness() (integrationstate.State, error) {
	return s.ProbeReadinessContext(context.Background())
}

// ProbeReadinessContext implements the provider-neutral `stalwart.mail`
// catalog seam. The integration is not_configured until its API URL and all
// installation-owned local runtime settings are present. It is healthy only
// after the configured systemd unit is observed active and a fresh management
// API read succeeds. No lifecycle, configuration, or data mutation occurs.
//
// The caller context is wrapped in a five-second deadline and propagated to
// both the real systemctl subprocess and HTTP transport. Detailed command,
// URL, path, provider, and response errors are intentionally replaced with
// safe internal classifications.
func (s *Service) ProbeReadinessContext(parent context.Context) (integrationstate.State, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, mailReadinessTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	if s == nil {
		return integrationstate.NotConfigured, ErrNotConfigured
	}

	if setting := s.missingReadinessSetting(); setting != "" {
		return integrationstate.NotConfigured, notConfigured(setting)
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	runner := s.readinessRunner
	if runner == nil {
		return integrationstate.Unavailable, errMailReadinessUnavailable
	}

	if err := observeMailServiceContext(ctx, runner, s.serviceName); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		return integrationstate.Unavailable, err
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	if err := s.observeManagementAPIContext(ctx); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		return integrationstate.Unavailable, err
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	return integrationstate.Healthy, nil
}

// ProbeContext is the short compatibility name used by local integration
// registries that do not include the word Readiness in probe methods.
func (s *Service) ProbeContext(parent context.Context) (integrationstate.State, error) {
	return s.ProbeReadinessContext(parent)
}

// Probe is the background-context compatibility form of ProbeContext.
func (s *Service) Probe() (integrationstate.State, error) {
	return s.ProbeReadiness()
}

// missingReadinessSetting returns a stable setting label, never the missing
// value itself. Runtime paths are already normalized by WithRuntime; the
// absolute/clean check also protects callers that construct Service directly.
func (s *Service) missingReadinessSetting() string {
	switch {
	case strings.TrimSpace(s.baseURL) == "":
		return "mail API base URL"
	case strings.TrimSpace(s.serviceName) == "":
		return "mail service name"
	case strings.TrimSpace(s.configPath) == "":
		return "mail config path"
	case strings.TrimSpace(s.configPath) != s.configPath:
		return "mail config path"
	case !isCleanAbsolutePath(s.configPath):
		return "mail config path"
	case strings.TrimSpace(s.binary) == "":
		return "mail binary"
	case !s.hasReadinessAuth():
		return "mail management API credentials"
	default:
		return ""
	}
}

// hasReadinessAuth checks the two supported management API authentication
// shapes without changing either secret value. A Bearer key is sufficient on
// its own; Basic authentication requires both username and password.
func (s *Service) hasReadinessAuth() bool {
	return s.apiKey != "" || (strings.TrimSpace(s.username) != "" && s.password != "")
}

func isCleanAbsolutePath(path string) bool {
	return strings.TrimSpace(path) == path && path != "" && filepath.IsAbs(path) && filepath.Clean(path) == path
}

func observeMailServiceContext(ctx context.Context, runner readinessCommandRunner, serviceName string) error {
	output, err := runner.Run(ctx, "systemctl", "is-active", serviceName)
	if contextErr := ctx.Err(); contextErr != nil {
		return contextErr
	}
	if err != nil || strings.TrimSpace(string(output)) != "active" {
		return errMailServiceObservationUnavailable
	}
	return nil
}

func (s *Service) observeManagementAPIContext(ctx context.Context) error {
	// A successful status code is the observation. The response body is not
	// decoded or copied: readiness must not depend on provider payload shape or
	// retain provider data in the error path.
	client := s.client
	if client == nil {
		client = &http.Client{Timeout: httpTimeout}
	} else {
		// Do not mutate the shared client: legacy mail APIs retain their normal
		// redirect behavior. Readiness must observe the configured endpoint
		// itself and must never turn a login redirect into a healthy result.
		clientCopy := *client
		client = &clientCopy
	}
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	if err := s.getContextWithClient(ctx, mailReadinessAPIPath, nil, client); err != nil {
		return errMailManagementAPIUnavailable
	}
	return nil
}
