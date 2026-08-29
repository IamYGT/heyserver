package systemactions

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

type fakeRunner struct {
	calls   []string
	errAt   int
	outputs map[int][]byte
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, fmt.Sprintf("%s %v", name, args))
	if output, ok := f.outputs[len(f.calls)]; ok {
		return output, nil
	}
	if f.errAt > 0 && len(f.calls) == f.errAt {
		return []byte("runner failure"), errors.New("exit 1")
	}
	return nil, nil
}

func TestCancelScheduledRebootStopsOnlyActivePanelTimer(t *testing.T) {
	runner := &fakeRunner{outputs: map[int][]byte{1: []byte("active\n")}}
	svc := New()
	svc.runner = runner

	message, err := svc.CancelScheduledReboot(context.Background())
	if err != nil {
		t.Fatalf("CancelScheduledReboot: %v", err)
	}
	if message != "Pending server reboot cancelled" {
		t.Fatalf("message = %q", message)
	}
	want := []string{
		"/usr/bin/systemctl [show --property=ActiveState --value hserver-reboot-request.timer]",
		"/usr/bin/systemctl [stop hserver-reboot-request.timer hserver-reboot-request.service]",
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestCancelScheduledRebootIsIdempotent(t *testing.T) {
	runner := &fakeRunner{outputs: map[int][]byte{1: []byte("inactive\n")}}
	svc := New()
	svc.runner = runner

	message, err := svc.CancelScheduledReboot(context.Background())
	if err != nil {
		t.Fatalf("CancelScheduledReboot: %v", err)
	}
	if message != "No pending server reboot was found" || len(runner.calls) != 1 {
		t.Fatalf("message=%q calls=%#v", message, runner.calls)
	}
}

func TestRebootPendingReadsOnlyPanelTimer(t *testing.T) {
	runner := &fakeRunner{outputs: map[int][]byte{1: []byte("active\n"), 2: []byte("inactive\n")}}
	svc := New()
	svc.runner = runner

	pending, err := svc.RebootPending(context.Background())
	if err != nil || !pending {
		t.Fatalf("active pending=%v err=%v", pending, err)
	}
	pending, err = svc.RebootPending(context.Background())
	if err != nil || pending {
		t.Fatalf("inactive pending=%v err=%v", pending, err)
	}
	for _, call := range runner.calls {
		if !strings.Contains(call, "hserver-reboot-request.timer") {
			t.Fatalf("unexpected status target: %q", call)
		}
	}
}

func TestRebootScheduleReportsPersistedDeadline(t *testing.T) {
	runner := &fakeRunner{outputs: map[int][]byte{1: []byte("NextElapseUSecRealtime=Wed 2026-08-26 04:00:10 UTC\nActiveState=active\n")}}
	svc := New()
	svc.runner = runner
	svc.now = func() time.Time { return time.Date(2026, 8, 26, 4, 0, 3, 500_000_000, time.UTC) }

	status, err := svc.RebootSchedule(context.Background())
	if err != nil {
		t.Fatalf("RebootSchedule: %v", err)
	}
	want := RebootStatus{Pending: true, ScheduledFor: "2026-08-26T04:00:10Z", RemainingSeconds: 7}
	if status != want {
		t.Fatalf("status = %#v, want %#v", status, want)
	}
	wantCall := "/usr/bin/systemctl [show --property=ActiveState --property=NextElapseUSecRealtime hserver-reboot-request.timer]"
	if runner.calls[0] != wantCall {
		t.Fatalf("call = %q, want %q", runner.calls[0], wantCall)
	}
}

func TestRebootScheduleKeepsActiveStatusWhenDeadlineCannotBeParsed(t *testing.T) {
	runner := &fakeRunner{outputs: map[int][]byte{1: []byte("ActiveState=active\nNextElapseUSecRealtime=n/a\n")}}
	svc := New()
	svc.runner = runner
	status, err := svc.RebootSchedule(context.Background())
	if err != nil || !status.Pending || status.ScheduledFor != "" || status.RemainingSeconds != 0 {
		t.Fatalf("status=%#v err=%v", status, err)
	}
}

func TestTerminateProcessSignalsRequestedPID(t *testing.T) {
	var gotPID int
	var gotSignal syscall.Signal
	statReads := 0
	svc := New()
	svc.pid = func() int { return 900 }
	svc.sleep = func(time.Duration) {}
	svc.read = func(path string) ([]byte, error) {
		switch path {
		case "/proc/123/stat":
			statReads++
			if statReads > 1 {
				return nil, os.ErrNotExist
			}
			return processStatForTest(456), nil
		case "/proc/123/comm":
			return []byte("php-fpm\n"), nil
		default:
			t.Fatalf("unexpected path %s", path)
			return nil, nil
		}
	}
	svc.kill = func(pid int, signal syscall.Signal) error {
		gotPID, gotSignal = pid, signal
		return nil
	}

	result, err := svc.TerminateProcess(123, "term", 456)
	if err != nil {
		t.Fatalf("TerminateProcess: %v", err)
	}
	if gotPID != 123 || gotSignal != syscall.SIGTERM {
		t.Fatalf("got pid=%d signal=%v", gotPID, gotSignal)
	}
	if !result.Exited || !result.Confirmed || result.Message != "TERM stopped php-fpm (PID 123)" {
		t.Fatalf("unexpected result %#v", result)
	}
}

func TestTerminateProcessReportsWhenTargetIsStillRunning(t *testing.T) {
	svc := New()
	svc.pid = func() int { return 900 }
	svc.sleep = func(time.Duration) {}
	svc.read = func(path string) ([]byte, error) {
		if path == "/proc/123/comm" {
			return []byte("worker\n"), nil
		}
		return processStatForTest(456), nil
	}
	svc.kill = func(int, syscall.Signal) error { return nil }

	result, err := svc.TerminateProcess(123, "kill", 456)
	if err != nil {
		t.Fatalf("TerminateProcess: %v", err)
	}
	if result.Exited || !result.Confirmed || !strings.Contains(result.Message, "still running") {
		t.Fatalf("unexpected result %#v", result)
	}
}

func TestTerminateProcessRejectsProtectedAndUnknownTargets(t *testing.T) {
	svc := New()
	svc.pid = func() int { return 42 }

	if _, err := svc.TerminateProcess(1, "term", 1); !errors.Is(err, ErrInvalidPID) {
		t.Fatalf("PID 1 error = %v", err)
	}
	if _, err := svc.TerminateProcess(42, "kill", 1); !errors.Is(err, ErrProtectedProcess) {
		t.Fatalf("own PID error = %v", err)
	}
	svc.read = func(string) ([]byte, error) { return nil, os.ErrNotExist }
	if _, err := svc.TerminateProcess(99, "term", 1); !errors.Is(err, ErrProcessNotFound) {
		t.Fatalf("missing PID error = %v", err)
	}
}

func TestTerminateProcessRejectsMissingOrChangedIdentity(t *testing.T) {
	svc := New()
	svc.pid = func() int { return 900 }
	if _, err := svc.TerminateProcess(123, "term", 0); !errors.Is(err, ErrProcessIdentity) {
		t.Fatalf("missing identity error = %v", err)
	}
	svc.read = func(string) ([]byte, error) { return processStatForTest(456), nil }
	called := false
	svc.kill = func(int, syscall.Signal) error { called = true; return nil }
	if _, err := svc.TerminateProcess(123, "term", 999); !errors.Is(err, ErrProcessChanged) {
		t.Fatalf("changed identity error = %v", err)
	}
	if called {
		t.Fatal("kill called after identity mismatch")
	}
}

func processStatForTest(startTime uint64) []byte {
	return []byte(fmt.Sprintf("123 (php worker) S%s %d 0\n", strings.Repeat(" 0", 18), startTime))
}

func TestResetSwapRunsOnlyTheAllowlistedCycle(t *testing.T) {
	runner := &fakeRunner{}
	svc := New()
	svc.runner = runner
	svc.read = func(path string) ([]byte, error) {
		if path != "/proc/meminfo" {
			t.Fatalf("unexpected path %s", path)
		}
		return []byte("MemAvailable: 8388608 kB\nSwapTotal: 2097152 kB\nSwapFree: 1048576 kB\n"), nil
	}

	if _, err := svc.ResetSwap(context.Background()); err != nil {
		t.Fatalf("ResetSwap: %v", err)
	}
	want := []string{"/sbin/swapoff [-a]", "/sbin/swapon [-a]"}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestResetSwapReportsMeasuredBeforeAndAfterUsage(t *testing.T) {
	runner := &fakeRunner{}
	svc := New()
	svc.runner = runner
	reads := 0
	svc.read = func(string) ([]byte, error) {
		reads++
		if reads == 1 {
			return []byte("MemAvailable: 8388608 kB\nSwapTotal: 2097152 kB\nSwapFree: 1048576 kB\n"), nil
		}
		return []byte("MemAvailable: 7340032 kB\nSwapTotal: 2097152 kB\nSwapFree: 2097152 kB\n"), nil
	}

	message, err := svc.ResetSwap(context.Background())
	if err != nil {
		t.Fatalf("ResetSwap: %v", err)
	}
	want := "Swap reset completed; used swap 1.0 GiB → 0 MiB (1.0 GiB cleared)"
	if message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}
}

func TestResetSwapRefusesMemoryPressure(t *testing.T) {
	runner := &fakeRunner{}
	svc := New()
	svc.runner = runner
	svc.read = func(string) ([]byte, error) {
		return []byte("MemAvailable: 524288 kB\nSwapTotal: 2097152 kB\nSwapFree: 0 kB\n"), nil
	}

	if _, err := svc.ResetSwap(context.Background()); !errors.Is(err, ErrInsufficientMemory) {
		t.Fatalf("error = %v", err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner called during refused reset: %#v", runner.calls)
	}
}

func TestResetSwapSkipsCycleWhenSwapIsAlreadyEmpty(t *testing.T) {
	runner := &fakeRunner{}
	svc := New()
	svc.runner = runner
	svc.read = func(string) ([]byte, error) {
		return []byte("MemAvailable: 8388608 kB\nSwapTotal: 2097152 kB\nSwapFree: 2097152 kB\n"), nil
	}

	message, err := svc.ResetSwap(context.Background())
	if err != nil {
		t.Fatalf("ResetSwap: %v", err)
	}
	if message != "Swap is already empty; no reset was needed" || len(runner.calls) != 0 {
		t.Fatalf("message=%q calls=%#v", message, runner.calls)
	}
}

func TestOptimizeMemoryDropsCachesWithoutCyclingSwap(t *testing.T) {
	runner := &fakeRunner{}
	svc := New()
	svc.runner = runner
	reads := 0
	svc.read = func(string) ([]byte, error) {
		reads++
		if reads == 1 {
			return []byte("MemAvailable: 6291456 kB\nSwapTotal: 2097152 kB\nSwapFree: 1048576 kB\n"), nil
		}
		return []byte("MemAvailable: 8388608 kB\nSwapTotal: 2097152 kB\nSwapFree: 1048576 kB\n"), nil
	}
	var writePath string
	var writeData []byte
	svc.write = func(path string, data []byte, _ os.FileMode) error {
		writePath = path
		writeData = append([]byte(nil), data...)
		return nil
	}

	message, err := svc.OptimizeMemory(context.Background())
	if err != nil {
		t.Fatalf("OptimizeMemory: %v", err)
	}
	wantCalls := []string{"/bin/sync []"}
	if !reflect.DeepEqual(runner.calls, wantCalls) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, wantCalls)
	}
	if writePath != "/proc/sys/vm/drop_caches" || string(writeData) != "3\n" {
		t.Fatalf("cache write = %q %q", writePath, string(writeData))
	}
	if message != "Memory optimized; available RAM 6.0 GiB → 8.0 GiB (+2.0 GiB). Filesystem caches dropped; running processes and swap were unchanged" {
		t.Fatalf("message = %q", message)
	}
}

func TestFormatMemoryChangeReportsIncreaseDecreaseAndStableValues(t *testing.T) {
	tests := []struct {
		name          string
		before, after uint64
		want          string
	}{
		{name: "increase", before: 4 << 30, after: 6 << 30, want: "4.0 GiB → 6.0 GiB (+2.0 GiB)"},
		{name: "decrease", before: 6 << 30, after: 5 << 30, want: "6.0 GiB → 5.0 GiB (-1.0 GiB)"},
		{name: "stable", before: 5 << 30, after: 5 << 30, want: "5.0 GiB → 5.0 GiB (no measurable change)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatMemoryChange(tt.before, tt.after); got != tt.want {
				t.Fatalf("formatMemoryChange() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHostMaintenanceCommandsAreFixed(t *testing.T) {
	runner := &fakeRunner{}
	svc := New()
	svc.runner = runner
	svc.now = func() time.Time { return time.Date(2026, 8, 25, 13, 30, 0, 0, time.UTC) }

	if _, err := svc.CleanTemporaryFiles(context.Background()); err != nil {
		t.Fatalf("CleanTemporaryFiles: %v", err)
	}
	if _, err := svc.ScheduleReboot(context.Background()); err != nil {
		t.Fatalf("ScheduleReboot: %v", err)
	}

	want := []string{
		"/usr/bin/systemd-run [--wait --pipe --collect --quiet --unit=hserver-temp-clean-20260825T133000 /usr/bin/systemd-tmpfiles --clean]",
		"/usr/bin/systemd-run [--collect --quiet --unit=hserver-reboot-request --on-active=10s --timer-property=AccuracySec=1s /usr/bin/systemctl reboot]",
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestCleanTemporaryFilesReportsMeasuredRootSpace(t *testing.T) {
	runner := &fakeRunner{}
	svc := New()
	svc.runner = runner
	measurements := []uint64{80 << 30, 82 << 30}
	svc.statfs = func(path string, stat *syscall.Statfs_t) error {
		if path != "/" {
			t.Fatalf("statfs path = %q", path)
		}
		value := measurements[0]
		measurements = measurements[1:]
		stat.Bsize = 4096
		stat.Bavail = value / 4096
		return nil
	}

	message, err := svc.CleanTemporaryFiles(context.Background())
	if err != nil {
		t.Fatalf("CleanTemporaryFiles: %v", err)
	}
	want := "Expired temporary files were cleaned using host tmpfiles policy; root free space 80.0 GiB → 82.0 GiB (+2.0 GiB)"
	if message != want {
		t.Fatalf("message = %q, want %q", message, want)
	}
}

func TestControlServiceUsesFixedAllowlist(t *testing.T) {
	runner := &fakeRunner{}
	svc := New()
	svc.runner = runner

	message, err := svc.ControlService(context.Background(), "postgresql", "restart")
	if err != nil {
		t.Fatalf("ControlService: %v", err)
	}
	if message != "postgresql restart completed" {
		t.Fatalf("message = %q", message)
	}
	want := []string{"/usr/bin/systemctl [restart postgresql@18-main.service]"}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}

	if _, err := svc.ControlService(context.Background(), "hserver", "stop"); !errors.Is(err, ErrInvalidService) {
		t.Fatalf("unmanaged service error = %v", err)
	}
	if _, err := svc.ControlService(context.Background(), "nginx", "reload-or-reboot"); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("invalid action error = %v", err)
	}
}

func TestServiceLogsUsesFixedAllowlistAndBoundedJournalRead(t *testing.T) {
	runner := &fakeRunner{outputs: map[int][]byte{1: []byte(
		`{"__REALTIME_TIMESTAMP":"1787688000000000","_SYSTEMD_UNIT":"postgresql@18-main.service","PRIORITY":"6","MESSAGE":"line one"}` + "\n" +
			`{"__REALTIME_TIMESTAMP":"2026-08-25T20:01:00Z","SYSLOG_IDENTIFIER":"postgres","PRIORITY":"3","MESSAGE":"line two"}` + "\n",
	)}}
	svc := New()
	svc.runner = runner

	lines, err := svc.ServiceLogs(context.Background(), "postgresql", 80)
	if err != nil {
		t.Fatalf("ServiceLogs: %v", err)
	}
	wantLines := []ServiceLogEntry{
		{Timestamp: "2026-08-25T20:00:00Z", Unit: "postgresql@18-main.service", Priority: 6, Message: "line one"},
		{Timestamp: "2026-08-25T20:01:00Z", Unit: "postgres", Priority: 3, Message: "line two"},
	}
	if !reflect.DeepEqual(lines, wantLines) {
		t.Fatalf("lines = %#v", lines)
	}
	want := []string{"/usr/bin/journalctl [--unit postgresql@18-main.service --lines 80 --no-pager --output=json]"}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
	if _, err := svc.ServiceLogs(context.Background(), "hserver", 80); !errors.Is(err, ErrInvalidService) {
		t.Fatalf("unmanaged service error = %v", err)
	}
	if _, err := svc.ServiceLogs(context.Background(), "nginx", 501); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("line bound error = %v", err)
	}
}

func TestMaintenanceActionsRejectConcurrentExecution(t *testing.T) {
	svc := New()
	svc.mu.Lock()
	defer svc.mu.Unlock()

	tests := []struct {
		name string
		run  func() (string, error)
	}{
		{name: "reset swap", run: func() (string, error) { return svc.ResetSwap(context.Background()) }},
		{name: "optimize memory", run: func() (string, error) { return svc.OptimizeMemory(context.Background()) }},
		{name: "clean temporary files", run: func() (string, error) { return svc.CleanTemporaryFiles(context.Background()) }},
		{name: "schedule reboot", run: func() (string, error) { return svc.ScheduleReboot(context.Background()) }},
		{name: "cancel reboot", run: func() (string, error) { return svc.CancelScheduledReboot(context.Background()) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.run(); !errors.Is(err, ErrActionInProgress) {
				t.Fatalf("error = %v, want ErrActionInProgress", err)
			}
		})
	}
}

func TestMaintenanceStatusTracksActiveAction(t *testing.T) {
	svc := New()
	started := time.Date(2026, 8, 26, 3, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return started }

	release, err := svc.BeginMaintenance("memory-optimize")
	if err != nil {
		t.Fatalf("beginMaintenance: %v", err)
	}
	status := svc.MaintenanceStatus()
	if !status.Running || status.Action != "memory-optimize" || status.StartedAt != started.Format(time.RFC3339Nano) {
		t.Fatalf("active status = %#v", status)
	}
	release()
	release()
	if status := svc.MaintenanceStatus(); status.Running || status.Action != "" || status.StartedAt != "" {
		t.Fatalf("completed status = %#v", status)
	}
	if _, err := svc.BeginMaintenance("arbitrary"); !errors.Is(err, ErrInvalidAction) {
		t.Fatalf("invalid maintenance error = %v", err)
	}
}

func TestConfiguredManagedServiceUnitsUsesDeclaredPM2Owner(t *testing.T) {
	t.Setenv("HSERVER_PM2_USER", "deploy")
	units := configuredManagedServiceUnits()
	if got := units["pm2-deploy"]; got != "pm2-deploy.service" {
		t.Fatalf("PM2 unit = %q, want pm2-deploy.service", got)
	}

	for _, user := range []string{"", "root", "invalid user", "../escape"} {
		t.Setenv("HSERVER_PM2_USER", user)
		for name := range configuredManagedServiceUnits() {
			if strings.HasPrefix(name, "pm2-") {
				t.Fatalf("PM2 service configured for invalid owner %q: %q", user, name)
			}
		}
	}
}
