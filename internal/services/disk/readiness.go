package disk

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

const rootSmartProbeTimeout = 5 * time.Second

var (
	// ErrNotConfigured identifies a host where smartmontools or a single
	// physical root disk cannot be selected safely. It intentionally carries no
	// executable path, device name, or command output because readiness errors
	// may be consumed by an aggregate API.
	ErrNotConfigured = errors.New("smartmontools root-disk integration is not configured")

	// ErrSmartNotConfigured is kept as a descriptive alias for callers that
	// prefer a provider-specific error name. Both names classify the same safe
	// not_configured boundary.
	ErrSmartNotConfigured = ErrNotConfigured
)

// smartProbeRunner is the narrow subprocess seam used by the root SMART
// readiness probe. Run returns combined command output so smartctl health can
// be classified without allowing command diagnostics to cross the seam.
// Keeping df, lsblk, and smartctl behind the same seam makes the full
// provider-neutral selection path deterministic in tests.
type smartProbeRunner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, ...string) ([]byte, error)
}

type execSmartProbeRunner struct{}

func (execSmartProbeRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (execSmartProbeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	// A provider or shell wrapper can leave descendants holding the output
	// pipe after the direct process is canceled. Kill the process group as well
	// so a bounded readiness request cannot leak a child or wait indefinitely.
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

	// Output is retained only in a bounded private buffer for in-process status
	// classification. The readiness API never returns this data.
	var output boundedSmartProbeOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	runErr := cmd.Run()
	return output.Bytes(), runErr
}

type boundedSmartProbeOutput struct {
	mu   sync.Mutex
	data []byte
}

func (o *boundedSmartProbeOutput) Write(data []byte) (int, error) {
	const maxBytes = 64 * 1024
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

func (o *boundedSmartProbeOutput) Bytes() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]byte(nil), o.data...)
}

// ProbeRootSmartContext performs one fresh, read-only SMART readiness
// observation for the physical device backing `/`. It first verifies that
// smartctl is installed, then resolves the root filesystem with df and walks
// its observed ancestry with lsblk. A virtual, non-block, or ambiguous root
// is not configured rather than guessed. A selected device is healthy only
// when smartctl completes successfully and reports a fresh PASSED result.
func ProbeRootSmartContext(parent context.Context) (integrationstate.State, error) {
	return probeRootSmartContext(parent, execSmartProbeRunner{})
}

// ProbeRootSmart is the background-context convenience form of
// ProbeRootSmartContext.
func ProbeRootSmart() (integrationstate.State, error) {
	return ProbeRootSmartContext(context.Background())
}

// ProbeContext is an intentionally small compatibility alias for callers that
// use the same standalone probe naming as the other local services.
func ProbeContext(parent context.Context) (integrationstate.State, error) {
	return ProbeRootSmartContext(parent)
}

// Probe is the background-context compatibility form of ProbeContext.
func Probe() (integrationstate.State, error) {
	return ProbeRootSmart()
}

func probeRootSmartContext(parent context.Context, runner smartProbeRunner) (integrationstate.State, error) {
	if parent == nil {
		parent = context.Background()
	}
	if runner == nil {
		return integrationstate.Unavailable, errors.New("smartmontools readiness runner is unavailable")
	}

	ctx, cancel := context.WithTimeout(parent, rootSmartProbeTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	resolvedSmartctl, err := runner.LookPath("smartctl")
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		if isMissingExecutable(err) {
			return integrationstate.NotConfigured, ErrNotConfigured
		}
		return integrationstate.Unavailable, errors.New("smartctl executable discovery failed")
	}
	if strings.TrimSpace(resolvedSmartctl) == "" {
		return integrationstate.NotConfigured, ErrNotConfigured
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	dfOutput, err := runSmartProbeCommand(ctx, runner, "df", "--output=source", "/")
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		return integrationstate.Unavailable, errors.New("root filesystem observation failed")
	}
	source, err := rootFilesystemSource(string(dfOutput))
	if err != nil || !safeRootDevicePath(source) {
		return integrationstate.NotConfigured, ErrNotConfigured
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	lsblkOutput, err := runSmartProbeCommand(ctx, runner, "lsblk", "-s", "-lnpo", "PATH,TYPE", source)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		return integrationstate.Unavailable, errors.New("root block-device observation failed")
	}
	device, _ := rootPhysicalDevice(string(lsblkOutput))
	if !safePhysicalRootDevice(device) {
		return integrationstate.NotConfigured, ErrNotConfigured
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	smartOutput, err := runSmartProbeCommand(ctx, runner, resolvedSmartctl, "-H", "-i", "-A", device)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return integrationstate.Unavailable, contextErr
		}
		return integrationstate.Unavailable, errors.New("SMART health observation failed")
	}
	if err := ctx.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	if smartctlHealthStatus(string(smartOutput)) != "PASSED" {
		return integrationstate.Unavailable, errors.New("SMART health observation was not a definite PASSED result")
	}
	return integrationstate.Healthy, nil
}

func runSmartProbeCommand(ctx context.Context, runner smartProbeRunner, name string, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	output, err := runner.Run(ctx, name, args...)
	if contextErr := ctx.Err(); contextErr != nil {
		return nil, contextErr
	}
	if err != nil {
		return nil, err
	}
	return output, nil
}

func isMissingExecutable(err error) bool {
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

func safeRootDevicePath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) || !strings.HasPrefix(path, "/dev/") || strings.ContainsAny(path, "\x00\r\n\t") {
		return false
	}
	return filepath.Clean(path) == path && path != "/dev/"
}

func safePhysicalRootDevice(path string) bool {
	if !safeRootDevicePath(path) {
		return false
	}
	base := filepath.Base(path)
	return base != "." && base != ".." && isValidDevice(base)
}

// smartctlHealthStatus extracts only the health result token from a health
// line. Generic words in model, attribute, or diagnostic text must never turn
// an otherwise unknown observation into healthy.
func smartctlHealthStatus(output string) string {
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		upper := strings.ToUpper(trimmed)
		value, ok := smartHealthValue(upper)
		if !ok {
			continue
		}
		for _, field := range strings.Fields(value) {
			field = strings.Trim(field, "[]():,;")
			switch field {
			case "PASSED", "FAILED", "UNKNOWN":
				return field
			}
		}
	}
	return "UNKNOWN"
}

func smartHealthValue(upper string) (string, bool) {
	colon := strings.IndexByte(upper, ':')
	if colon < 0 {
		return "", false
	}
	label := strings.TrimSpace(upper[:colon])
	for _, canonical := range []string{
		"SMART OVERALL-HEALTH SELF-ASSESSMENT TEST RESULT",
		"SMART HEALTH STATUS",
		"SMART HEALTH RESULT",
		"SMART HEALTH SELF-ASSESSMENT",
	} {
		if label == canonical {
			return strings.TrimSpace(upper[colon+1:]), true
		}
	}
	return "", false
}
