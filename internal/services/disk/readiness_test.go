package disk

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

type fakeSmartProbeRunner struct {
	mu          sync.Mutex
	path        string
	lookPathErr error
	outputs     map[string][]byte
	runErrs     map[string]error
	calls       []string
	run         func(context.Context, string, ...string) ([]byte, error)
}

func (f *fakeSmartProbeRunner) LookPath(string) (string, error) {
	return f.path, f.lookPathErr
}

func (f *fakeSmartProbeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	if f.run != nil {
		return f.run(ctx, name, args...)
	}
	if err := f.runErrs[call]; err != nil {
		return f.outputs[call], err
	}
	return f.outputs[call], nil
}

func smartProbeFixture() *fakeSmartProbeRunner {
	return &fakeSmartProbeRunner{
		path: "/usr/sbin/smartctl",
		outputs: map[string][]byte{
			"df --output=source /":                    []byte("Filesystem\n/dev/nvme0n1p2\n"),
			"lsblk -s -lnpo PATH,TYPE /dev/nvme0n1p2": []byte("/dev/nvme0n1p2 part\n/dev/nvme0n1 disk\n"),
		},
		runErrs: map[string]error{},
	}
}

func TestProbeRootSmartContextMissingSmartctlIsNotConfigured(t *testing.T) {
	runner := &fakeSmartProbeRunner{lookPathErr: exec.ErrNotFound}

	state, err := probeRootSmartContext(context.Background(), runner)
	if state != integrationstate.NotConfigured || !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("missing smartctl = state %q, err %v; want not_configured", state, err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("missing smartctl invoked commands: %#v", runner.calls)
	}
}

func TestProbeRootSmartContextRequiresSinglePhysicalRoot(t *testing.T) {
	tests := []struct {
		name    string
		lsblk   string
		wantMsg string
	}{
		{name: "no physical root", lsblk: "/dev/mapper/root crypt\n"},
		{name: "ambiguous physical root", lsblk: "/dev/md0 raid1\n/dev/sda disk\n/dev/sdb disk\n", wantMsg: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := smartProbeFixture()
			runner.outputs["lsblk -s -lnpo PATH,TYPE /dev/nvme0n1p2"] = []byte(test.lsblk)

			state, err := probeRootSmartContext(context.Background(), runner)
			if state != integrationstate.NotConfigured || !errors.Is(err, ErrNotConfigured) {
				t.Fatalf("root selection = state %q, err %v; want not_configured", state, err)
			}
			if len(runner.calls) != 2 {
				t.Fatalf("root selection calls = %#v, want df and lsblk only", runner.calls)
			}
		})
	}
}

func TestProbeRootSmartContextHealthyOnlyAfterFreshPassed(t *testing.T) {
	runner := smartProbeFixture()
	runner.outputs["/usr/sbin/smartctl -H -i -A /dev/nvme0n1"] = []byte("SMART overall-health self-assessment test result: PASSED\n")

	state, err := probeRootSmartContext(context.Background(), runner)
	if err != nil || state != integrationstate.Healthy {
		t.Fatalf("healthy SMART = state %q, err %v; want healthy", state, err)
	}
	wantCalls := []string{
		"df --output=source /",
		"lsblk -s -lnpo PATH,TYPE /dev/nvme0n1p2",
		"/usr/sbin/smartctl -H -i -A /dev/nvme0n1",
	}
	if !equalSmartProbeStrings(runner.calls, wantCalls) {
		t.Fatalf("SMART calls = %#v, want %#v", runner.calls, wantCalls)
	}
}

func TestProbeRootSmartContextFailedUnknownAndUnsupportedAreUnavailable(t *testing.T) {
	tests := []struct {
		name   string
		output string
		err    error
	}{
		{name: "failed", output: "SMART overall-health self-assessment test result: FAILED\n"},
		{name: "unknown", output: "SMART Health Status: OK\n"},
		{name: "unsupported", output: "SMART support is: Unavailable - device lacks SMART capability.\n"},
		{name: "command failure", output: "private device diagnostics\n", err: errors.New("smartctl: permission denied /srv/private/device")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runner := smartProbeFixture()
			runner.outputs["/usr/sbin/smartctl -H -i -A /dev/nvme0n1"] = []byte(test.output)
			runner.runErrs["/usr/sbin/smartctl -H -i -A /dev/nvme0n1"] = test.err

			state, err := probeRootSmartContext(context.Background(), runner)
			if state != integrationstate.Unavailable || err == nil {
				t.Fatalf("%s = state %q, err %v; want unavailable/error", test.name, state, err)
			}
			if strings.Contains(err.Error(), "private device") || strings.Contains(err.Error(), "/srv/private") {
				t.Fatalf("%s leaked smartctl detail: %v", test.name, err)
			}
		})
	}
}

func TestProbeRootSmartContextObserverFailuresAreUnavailable(t *testing.T) {
	for _, command := range []string{
		"df --output=source /",
		"lsblk -s -lnpo PATH,TYPE /dev/nvme0n1p2",
	} {
		t.Run(command, func(t *testing.T) {
			runner := smartProbeFixture()
			runner.runErrs[command] = errors.New("permission denied at /srv/private/device")
			state, err := probeRootSmartContext(context.Background(), runner)
			if state != integrationstate.Unavailable || err == nil {
				t.Fatalf("observer failure = state %q, err %v; want unavailable", state, err)
			}
			if strings.Contains(err.Error(), "/srv/private") || strings.Contains(err.Error(), "permission denied") {
				t.Fatalf("observer failure leaked detail: %v", err)
			}
		})
	}
}

func TestProbeRootSmartContextCancellationIsUnavailableAndStopsRunner(t *testing.T) {
	started := make(chan struct{})
	runner := smartProbeFixture()
	runner.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "/usr/sbin/smartctl" && len(args) == 4 {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return runner.outputs[strings.Join(append([]string{name}, args...), " ")], nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan struct {
		state integrationstate.State
		err   error
	}, 1)
	go func() {
		state, err := probeRootSmartContext(ctx, runner)
		result <- struct {
			state integrationstate.State
			err   error
		}{state: state, err: err}
	}()

	select {
	case <-started:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("SMART probe did not reach smartctl runner")
	}
	select {
	case got := <-result:
		if got.state != integrationstate.Unavailable || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("cancelled SMART probe = state %q, err %v; want unavailable/canceled", got.state, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled SMART probe did not return")
	}
}

func TestProbeRootSmartContextTimeoutDoesNotLeakRunner(t *testing.T) {
	runner := smartProbeFixture()
	started := make(chan struct{})
	finished := make(chan struct{})
	runner.run = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if name == "/usr/sbin/smartctl" && len(args) == 4 {
			close(started)
			defer close(finished)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return runner.outputs[strings.Join(append([]string{name}, args...), " ")], nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	state, err := probeRootSmartContext(ctx, runner)
	if state != integrationstate.Unavailable || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed out SMART probe = state %q, err %v; want unavailable/deadline", state, err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("timed out SMART probe took %s", elapsed)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("SMART probe did not reach the blocking runner")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("SMART runner did not receive cancellation")
	}
}

func TestSmartctlHealthStatusDoesNotTreatGenericTextAsPassed(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{name: "overall health", output: "SMART overall-health self-assessment test result: PASSED", want: "PASSED"},
		{name: "health status", output: "SMART Health Status: FAILED", want: "FAILED"},
		{name: "generic passed", output: "SMART support is: Available\nprevious test PASSED", want: "UNKNOWN"},
		{name: "model text mimics label", output: "Device Model: vendor OVERALL-HEALTH PASSED", want: "UNKNOWN"},
		{name: "diagnostic prefix mimics label", output: "Warning SMART Health Status: PASSED", want: "UNKNOWN"},
		{name: "unknown health", output: "SMART Health Status: OK", want: "UNKNOWN"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := smartctlHealthStatus(test.output); got != test.want {
				t.Fatalf("smartctlHealthStatus() = %q, want %q", got, test.want)
			}
		})
	}
}

func equalSmartProbeStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}
