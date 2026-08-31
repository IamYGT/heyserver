package systemactions

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/IamYGT/heyserver/internal/services/pm2"
)

var (
	ErrInvalidSignal      = errors.New("invalid process signal")
	ErrInvalidPID         = errors.New("invalid process id")
	ErrProcessNotFound    = errors.New("process not found")
	ErrProtectedProcess   = errors.New("protected process")
	ErrProcessChanged     = errors.New("process identity changed")
	ErrProcessIdentity    = errors.New("process identity is required")
	ErrInsufficientMemory = errors.New("insufficient available memory")
	ErrInvalidService     = errors.New("service is not managed by the panel")
	ErrInvalidAction      = errors.New("invalid service action")
	ErrActionInProgress   = errors.New("another host maintenance action is already running")
)

const swapSafetyReserve = 512 * 1024 * 1024

var managedServiceUnits = configuredManagedServiceUnits()

func configuredManagedServiceUnits() map[string]string {
	units := map[string]string{
		"nginx":        "nginx.service",
		"php8.4-fpm":   "php8.4-fpm.service",
		"php8.5-fpm":   "php8.5-fpm.service",
		"php7.4-fpm":   "php7.4-fpm.service",
		"postgresql":   "postgresql@18-main.service",
		"mariadb":      "mariadb.service",
		"redis-server": "redis-server.service",
	}
	if name, ok := pm2.SystemdServiceName(os.Getenv("HSERVER_PM2_USER")); ok {
		units[name] = name + ".service"
	}
	return units
}

var maintenanceActions = map[string]struct{}{
	"memory-optimize": {},
	"swap-reset":      {},
	"temp-clean":      {},
	"reboot":          {},
	"reboot-cancel":   {},
	"disk-cleanup":    {},
}

type commandRunner interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

// Service owns the small, explicit allowlist of host-level actions exposed by
// the panel. Arbitrary commands remain confined to the admin terminal.
type Service struct {
	runner  commandRunner
	kill    func(int, syscall.Signal) error
	pid     func() int
	read    func(string) ([]byte, error)
	sleep   func(time.Duration)
	write   func(string, []byte, os.FileMode) error
	statfs  func(string, *syscall.Statfs_t) error
	now     func() time.Time
	mu      sync.Mutex
	stateMu sync.Mutex
	active  ActionStatus
}

type ActionStatus struct {
	Running   bool   `json:"running"`
	Action    string `json:"action,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
}

type RebootStatus struct {
	Pending          bool   `json:"pending"`
	ScheduledFor     string `json:"scheduled_for,omitempty"`
	RemainingSeconds int64  `json:"remaining_seconds,omitempty"`
}

type ProcessSignalResult struct {
	Message   string `json:"message"`
	Exited    bool   `json:"exited"`
	Confirmed bool   `json:"confirmed"`
}

func New() *Service {
	return &Service{
		runner: execRunner{},
		kill:   syscall.Kill,
		pid:    os.Getpid,
		read:   os.ReadFile,
		sleep:  time.Sleep,
		write:  os.WriteFile,
		statfs: syscall.Statfs,
		now:    time.Now,
	}
}

// TerminateProcess sends either SIGTERM (normal stop) or SIGKILL (force stop)
// to the requested live PID. PID 1 and the panel itself are never targets.
func (s *Service) TerminateProcess(pid int, signal string, expectedStartTime uint64) (ProcessSignalResult, error) {
	if pid <= 1 {
		return ProcessSignalResult{}, ErrInvalidPID
	}
	if pid == s.pid() {
		return ProcessSignalResult{}, ErrProtectedProcess
	}
	if expectedStartTime == 0 {
		return ProcessSignalResult{}, ErrProcessIdentity
	}

	actualStartTime, err := s.processStartTime(pid)
	if err != nil {
		return ProcessSignalResult{}, err
	}
	if actualStartTime != expectedStartTime {
		return ProcessSignalResult{}, ErrProcessChanged
	}

	processName, err := s.processName(pid)
	if err != nil {
		return ProcessSignalResult{}, err
	}

	var sig syscall.Signal
	switch signal {
	case "term", "":
		sig = syscall.SIGTERM
		signal = "term"
	case "kill":
		sig = syscall.SIGKILL
	default:
		return ProcessSignalResult{}, ErrInvalidSignal
	}

	if err := s.kill(pid, sig); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return ProcessSignalResult{}, ErrProcessNotFound
		}
		return ProcessSignalResult{}, fmt.Errorf("signal process: %w", err)
	}

	attempts := 20
	if signal == "kill" {
		attempts = 10
	}
	exited, confirmed := s.waitForProcessExit(pid, expectedStartTime, attempts)
	result := ProcessSignalResult{Exited: exited, Confirmed: confirmed}
	signalName := strings.ToUpper(signal)
	switch {
	case exited:
		result.Message = fmt.Sprintf("%s stopped %s (PID %d)", signalName, processName, pid)
	case confirmed:
		result.Message = fmt.Sprintf("%s sent to %s (PID %d), but it is still running", signalName, processName, pid)
	default:
		result.Message = fmt.Sprintf("%s sent to %s (PID %d); exit could not be confirmed", signalName, processName, pid)
	}
	return result, nil
}

func (s *Service) waitForProcessExit(pid int, expectedStartTime uint64, attempts int) (bool, bool) {
	for range attempts {
		s.sleep(100 * time.Millisecond)
		actualStartTime, state, err := s.processIdentity(pid)
		if errors.Is(err, ErrProcessNotFound) {
			return true, true
		}
		if err != nil {
			return false, false
		}
		if actualStartTime != expectedStartTime || state == "Z" || state == "X" {
			return true, true
		}
	}
	return false, true
}

func (s *Service) processStartTime(pid int) (uint64, error) {
	startTime, _, err := s.processIdentity(pid)
	return startTime, err
}

func (s *Service) processIdentity(pid int) (uint64, string, error) {
	raw, err := s.read(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, "", ErrProcessNotFound
		}
		return 0, "", fmt.Errorf("read process identity: %w", err)
	}
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return 0, "", fmt.Errorf("parse process identity: invalid stat")
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) <= 19 {
		return 0, "", fmt.Errorf("parse process identity: start time missing")
	}
	startTime, parseErr := strconv.ParseUint(fields[19], 10, 64)
	if parseErr != nil || startTime == 0 {
		return 0, "", fmt.Errorf("parse process identity: invalid start time")
	}
	return startTime, fields[0], nil
}

// ControlService performs a bounded systemd action against the same explicit
// service allowlist shown on the monitoring screen. The panel itself is not in
// this list, so an operator cannot accidentally cut off panel access here.
func (s *Service) ControlService(ctx context.Context, service, action string) (string, error) {
	unit, ok := managedServiceUnits[service]
	if !ok {
		return "", ErrInvalidService
	}
	if action != "start" && action != "stop" && action != "restart" {
		return "", ErrInvalidAction
	}

	actionCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	output, err := s.runner.Run(actionCtx, "/usr/bin/systemctl", action, unit)
	if err != nil {
		return "", commandError("service "+action, output, err)
	}
	return fmt.Sprintf("%s %s completed", service, action), nil
}

// ServiceLogEntry is the bounded, display-safe journal shape returned to the
// dashboard. The fixed service allowlist remains the source of the unit name.
type ServiceLogEntry struct {
	Timestamp string `json:"timestamp"`
	Unit      string `json:"unit"`
	Priority  int    `json:"priority"`
	Message   string `json:"message"`
}

// ServiceLogs returns recent journal entries for the same fixed service
// allowlist used by ControlService. Callers cannot select an arbitrary unit.
func (s *Service) ServiceLogs(ctx context.Context, service string, lines int) ([]ServiceLogEntry, error) {
	unit, ok := managedServiceUnits[service]
	if !ok {
		return nil, ErrInvalidService
	}
	if lines < 1 || lines > 500 {
		return nil, fmt.Errorf("%w: lines must be between 1 and 500", ErrInvalidAction)
	}

	actionCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	output, err := s.runner.Run(actionCtx,
		"/usr/bin/journalctl",
		"--unit", unit,
		"--lines", strconv.Itoa(lines),
		"--no-pager",
		"--output=json",
	)
	if err != nil {
		return nil, commandError("read service journal", output, err)
	}

	entries := make([]ServiceLogEntry, 0)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var row struct {
			Timestamp  string `json:"__REALTIME_TIMESTAMP"`
			Unit       string `json:"_SYSTEMD_UNIT"`
			Identifier string `json:"SYSLOG_IDENTIFIER"`
			Priority   string `json:"PRIORITY"`
			Message    string `json:"MESSAGE"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &row); err != nil || row.Message == "" {
			continue
		}

		priority, _ := strconv.Atoi(row.Priority)
		entryUnit := row.Unit
		if entryUnit == "" {
			entryUnit = row.Identifier
		}
		timestamp := row.Timestamp
		if micros, parseErr := strconv.ParseInt(row.Timestamp, 10, 64); parseErr == nil {
			timestamp = time.UnixMicro(micros).UTC().Format(time.RFC3339Nano)
		}

		entries = append(entries, ServiceLogEntry{
			Timestamp: timestamp,
			Unit:      entryUnit,
			Priority:  priority,
			Message:   row.Message,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse service journal: %w", err)
	}
	return entries, nil
}

func (s *Service) processName(pid int) (string, error) {
	raw, err := s.read(fmt.Sprintf("/proc/%d/comm", pid))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", ErrProcessNotFound
		}
		return "", fmt.Errorf("read process name: %w", err)
	}
	name := strings.TrimSpace(string(raw))
	if name == "" {
		name = "process"
	}
	return name, nil
}

// ResetSwap cycles every configured swap target off and back on. It refuses to
// start if current MemAvailable cannot absorb used swap plus a fixed reserve.
func (s *Service) ResetSwap(ctx context.Context) (string, error) {
	release, err := s.BeginMaintenance("swap-reset")
	if err != nil {
		return "", err
	}
	defer release()
	return s.resetSwap(ctx)
}

func (s *Service) resetSwap(ctx context.Context) (string, error) {
	state, err := s.memoryState()
	if err != nil {
		return "", err
	}
	if state.swapTotal == 0 {
		return "No configured swap is active", nil
	}
	if state.swapUsed == 0 {
		return "Swap is already empty; no reset was needed", nil
	}
	if state.memAvailable < state.swapUsed+swapSafetyReserve {
		return "", fmt.Errorf(
			"%w: need at least %s available before swapoff, have %s",
			ErrInsufficientMemory,
			formatBytes(state.swapUsed+swapSafetyReserve),
			formatBytes(state.memAvailable),
		)
	}

	actionCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	if output, err := s.runner.Run(actionCtx, "/sbin/swapoff", "-a"); err != nil {
		return "", commandError("swapoff", output, err)
	}
	if output, err := s.runner.Run(actionCtx, "/sbin/swapon", "-a"); err != nil {
		// Best-effort second activation attempt before reporting the failure.
		_, _ = s.runner.Run(context.Background(), "/sbin/swapon", "-a")
		return "", commandError("swapon", output, err)
	}

	after, afterErr := s.memoryState()
	if afterErr != nil {
		return fmt.Sprintf("Swap reset completed; %s was cycled; post-reset measurement unavailable", formatBytes(state.swapUsed)), nil
	}
	return formatSwapResetMessage("Swap reset completed", state.swapUsed, after.swapUsed), nil
}

// OptimizeMemory flushes pending filesystem writes and releases reclaimable
// kernel caches. Swap is intentionally left alone: it has its own explicit
// action and may be unsafe to cycle when available memory is tight.
func (s *Service) OptimizeMemory(ctx context.Context) (string, error) {
	release, err := s.BeginMaintenance("memory-optimize")
	if err != nil {
		return "", err
	}
	defer release()
	before, beforeErr := s.memoryState()

	actionCtx, cancel := context.WithTimeout(ctx, 1*time.Minute)
	defer cancel()
	if output, err := s.runner.Run(actionCtx, "/bin/sync"); err != nil {
		return "", commandError("filesystem sync", output, err)
	}
	if err := s.write("/proc/sys/vm/drop_caches", []byte("3\n"), 0200); err != nil {
		return "", fmt.Errorf("drop filesystem caches: %w", err)
	}

	after, afterErr := s.memoryState()
	if beforeErr == nil && afterErr == nil {
		return fmt.Sprintf(
			"Memory optimized; available RAM %s. Filesystem caches dropped; running processes and swap were unchanged",
			formatMemoryChange(before.memAvailable, after.memAvailable),
		), nil
	}
	if afterErr == nil {
		return fmt.Sprintf("Memory optimized; filesystem caches dropped. %s is now available", formatBytes(after.memAvailable)), nil
	}
	return "Memory optimized; filesystem caches dropped", nil
}

// CleanTemporaryFiles asks the host systemd manager (outside the panel's
// PrivateTmp namespace) to apply the configured tmpfiles age policies.
func (s *Service) CleanTemporaryFiles(ctx context.Context) (string, error) {
	release, err := s.BeginMaintenance("temp-clean")
	if err != nil {
		return "", err
	}
	defer release()
	before, beforeErr := s.rootAvailableBytes()

	actionCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	unit := "hserver-temp-clean-" + s.now().UTC().Format("20060102T150405")
	output, err := s.runner.Run(actionCtx,
		"/usr/bin/systemd-run",
		"--wait", "--pipe", "--collect", "--quiet",
		"--unit="+unit,
		"/usr/bin/systemd-tmpfiles", "--clean",
	)
	if err != nil {
		return "", commandError("temporary file cleanup", output, err)
	}
	after, afterErr := s.rootAvailableBytes()
	if beforeErr == nil && afterErr == nil {
		return fmt.Sprintf(
			"Expired temporary files were cleaned using host tmpfiles policy; root free space %s",
			formatMemoryChange(before, after),
		), nil
	}
	if afterErr == nil {
		return fmt.Sprintf("Expired temporary files were cleaned using host tmpfiles policy; root free space is now %s", formatBytes(after)), nil
	}
	return "Expired temporary files were cleaned using host tmpfiles policy; post-cleanup disk measurement unavailable", nil
}

func (s *Service) rootAvailableBytes() (uint64, error) {
	var stat syscall.Statfs_t
	if err := s.statfs("/", &stat); err != nil {
		return 0, err
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}

func (s *Service) BeginMaintenance(action string) (func(), error) {
	if _, ok := maintenanceActions[action]; !ok {
		return nil, ErrInvalidAction
	}
	if !s.mu.TryLock() {
		return nil, ErrActionInProgress
	}
	s.stateMu.Lock()
	s.active = ActionStatus{Running: true, Action: action, StartedAt: s.now().UTC().Format(time.RFC3339Nano)}
	s.stateMu.Unlock()
	var once sync.Once
	return func() { once.Do(s.endMaintenance) }, nil
}

func (s *Service) endMaintenance() {
	s.stateMu.Lock()
	s.active = ActionStatus{}
	s.stateMu.Unlock()
	s.mu.Unlock()
}

func (s *Service) MaintenanceStatus() ActionStatus {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	return s.active
}

// ScheduleReboot creates a transient host timer so the HTTP response reaches
// the browser before systemd starts the reboot.
func (s *Service) ScheduleReboot(ctx context.Context) (string, error) {
	release, err := s.BeginMaintenance("reboot")
	if err != nil {
		return "", err
	}
	defer release()

	actionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	output, err := s.runner.Run(actionCtx,
		"/usr/bin/systemd-run",
		"--collect", "--quiet",
		"--unit=hserver-reboot-request",
		"--on-active=10s",
		"--timer-property=AccuracySec=1s",
		"/usr/bin/systemctl", "reboot",
	)
	if err != nil {
		return "", commandError("schedule reboot", output, err)
	}
	return "Server reboot scheduled in 10 seconds", nil
}

// RebootPending reports whether the panel-owned transient reboot timer is
// currently waiting. It never considers timers created outside Heyserver.
func (s *Service) RebootPending(ctx context.Context) (bool, error) {
	actionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	output, err := s.runner.Run(actionCtx,
		"/usr/bin/systemctl",
		"show", "--property=ActiveState", "--value", "hserver-reboot-request.timer",
	)
	if err != nil {
		return false, commandError("inspect scheduled reboot", output, err)
	}
	return strings.TrimSpace(string(output)) == "active", nil
}

// RebootSchedule reports the panel-owned timer together with its persisted
// systemd deadline, so every browser can render the same countdown after a
// refresh or server switch.
func (s *Service) RebootSchedule(ctx context.Context) (RebootStatus, error) {
	actionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	output, err := s.runner.Run(actionCtx,
		"/usr/bin/systemctl",
		"show", "--property=ActiveState", "--property=NextElapseUSecRealtime",
		"hserver-reboot-request.timer",
	)
	if err != nil {
		return RebootStatus{}, commandError("inspect scheduled reboot deadline", output, err)
	}
	properties := make(map[string]string)
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			properties[key] = strings.TrimSpace(value)
		}
	}
	if properties["ActiveState"] != "active" {
		return RebootStatus{Pending: false}, nil
	}
	status := RebootStatus{Pending: true}
	if scheduled, ok := parseSystemdRealtime(properties["NextElapseUSecRealtime"]); ok {
		status.ScheduledFor = scheduled.UTC().Format(time.RFC3339)
		duration := scheduled.Sub(s.now())
		if duration > 0 {
			status.RemainingSeconds = int64((duration + time.Second - 1) / time.Second)
		}
	}
	return status, nil
}

func parseSystemdRealtime(value string) (time.Time, bool) {
	fields := strings.Fields(strings.TrimSpace(value))
	if len(fields) < 3 {
		return time.Time{}, false
	}
	withoutWeekday := strings.Join(fields[1:], " ")
	for _, layout := range []string{"2006-01-02 15:04:05 MST", "2006-01-02 15:04:05.999999 MST"} {
		if parsed, err := time.Parse(layout, withoutWeekday); err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

// CancelScheduledReboot stops the transient reboot timer while it is still
// waiting. The operation is intentionally idempotent so a delayed UI click is
// reported clearly instead of looking like a failed host action.
func (s *Service) CancelScheduledReboot(ctx context.Context) (string, error) {
	release, err := s.BeginMaintenance("reboot-cancel")
	if err != nil {
		return "", err
	}
	defer release()

	actionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	pending, err := s.RebootPending(actionCtx)
	if err != nil {
		return "", err
	}
	if !pending {
		return "No pending server reboot was found", nil
	}

	output, err := s.runner.Run(actionCtx,
		"/usr/bin/systemctl", "stop",
		"hserver-reboot-request.timer", "hserver-reboot-request.service",
	)
	if err != nil {
		return "", commandError("cancel scheduled reboot", output, err)
	}
	return "Pending server reboot cancelled", nil
}

type memorySnapshot struct {
	memAvailable uint64
	swapTotal    uint64
	swapUsed     uint64
}

func (s *Service) memoryState() (memorySnapshot, error) {
	raw, err := s.read("/proc/meminfo")
	if err != nil {
		return memorySnapshot{}, fmt.Errorf("read memory state: %w", err)
	}
	values := map[string]uint64{}
	scanner := bufio.NewScanner(strings.NewReader(string(raw)))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 2 {
			continue
		}
		value, parseErr := strconv.ParseUint(fields[1], 10, 64)
		if parseErr != nil {
			continue
		}
		values[strings.TrimSuffix(fields[0], ":")] = value * 1024
	}
	if err := scanner.Err(); err != nil {
		return memorySnapshot{}, fmt.Errorf("parse memory state: %w", err)
	}
	if values["MemAvailable"] == 0 {
		return memorySnapshot{}, errors.New("MemAvailable is missing from /proc/meminfo")
	}
	swapTotal := values["SwapTotal"]
	swapFree := values["SwapFree"]
	if swapFree > swapTotal {
		swapFree = swapTotal
	}
	return memorySnapshot{
		memAvailable: values["MemAvailable"],
		swapTotal:    swapTotal,
		swapUsed:     swapTotal - swapFree,
	}, nil
}

func commandError(action string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if len(detail) > 1024 {
		detail = detail[:1024]
	}
	if detail == "" {
		return fmt.Errorf("%s failed: %w", action, err)
	}
	return fmt.Errorf("%s failed: %s: %w", action, detail, err)
}

func formatBytes(bytes uint64) string {
	const gib = 1024 * 1024 * 1024
	const mib = 1024 * 1024
	if bytes >= gib {
		return fmt.Sprintf("%.1f GiB", float64(bytes)/gib)
	}
	return fmt.Sprintf("%.0f MiB", float64(bytes)/mib)
}

func formatMemoryChange(before, after uint64) string {
	change := "no measurable change"
	switch {
	case after > before:
		change = "+" + formatBytes(after-before)
	case after < before:
		change = "-" + formatBytes(before-after)
	}
	return fmt.Sprintf("%s → %s (%s)", formatBytes(before), formatBytes(after), change)
}

func formatSwapResetMessage(prefix string, before, after uint64) string {
	change := "no measurable change"
	switch {
	case after < before:
		change = formatBytes(before-after) + " cleared"
	case after > before:
		change = formatBytes(after-before) + " higher after cycle"
	}
	return fmt.Sprintf("%s; used swap %s → %s (%s)", prefix, formatBytes(before), formatBytes(after), change)
}
