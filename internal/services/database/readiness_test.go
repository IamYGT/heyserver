package database

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

type fakeDatabaseProbeRunner struct {
	mu       sync.Mutex
	paths    map[string]string
	pathErrs map[string]error
	runErrs  map[string]error
	calls    []string
	run      func(context.Context, string, ...string) ([]byte, error)
}

func (f *fakeDatabaseProbeRunner) LookPath(name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.pathErrs[name]; err != nil {
		return "", err
	}
	if path, ok := f.paths[name]; ok {
		return path, nil
	}
	return "", exec.ErrNotFound
}

func (f *fakeDatabaseProbeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	f.mu.Lock()
	f.calls = append(f.calls, call)
	run := f.run
	err := f.runErrs[call]
	f.mu.Unlock()
	if run != nil {
		return run(ctx, name, args...)
	}
	return nil, err
}

func (f *fakeDatabaseProbeRunner) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...)
}

func TestProbeContextPostgresOnlyHealthy(t *testing.T) {
	runner := &fakeDatabaseProbeRunner{paths: map[string]string{"psql": "/usr/bin/psql"}}

	state, err := probeContext(context.Background(), runner)
	if err != nil || state != integrationstate.Healthy {
		t.Fatalf("postgres-only probe = state %q, err %v; want healthy", state, err)
	}
	calls := runner.Calls()
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "sudo -u postgres psql") {
		t.Fatalf("postgres calls = %#v, want one sudo psql inventory", calls)
	}
}

func TestProbeContextMariaDBOnlyHealthy(t *testing.T) {
	runner := &fakeDatabaseProbeRunner{paths: map[string]string{"mysql": "/usr/bin/mysql"}}

	state, err := probeContext(context.Background(), runner)
	if err != nil || state != integrationstate.Healthy {
		t.Fatalf("mariadb-only probe = state %q, err %v; want healthy", state, err)
	}
	calls := runner.Calls()
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "mysql -u root -N -B -e") {
		t.Fatalf("MariaDB calls = %#v, want one mysql inventory", calls)
	}
}

func TestProbeContextBothClientsMissingIsNotConfigured(t *testing.T) {
	runner := &fakeDatabaseProbeRunner{}

	state, err := probeContext(context.Background(), runner)
	if state != integrationstate.NotConfigured || !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("both missing = state %q, err %v; want not_configured", state, err)
	}
	if calls := runner.Calls(); len(calls) != 0 {
		t.Fatalf("both missing invoked inventory commands: %#v", calls)
	}
}

func TestProbeContextMixedSuccessAndFailureIsHealthy(t *testing.T) {
	secret := "/srv/private/database.sock"
	runner := &fakeDatabaseProbeRunner{
		paths:   map[string]string{"psql": "/usr/bin/psql", "mysql": "/usr/bin/mysql"},
		runErrs: map[string]error{},
		run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			if name == "mysql" {
				return nil, errors.New("permission denied at " + secret)
			}
			return []byte("appdb\tpostgres\t10 MB\n"), nil
		},
	}

	state, err := probeContext(context.Background(), runner)
	if err != nil || state != integrationstate.Healthy {
		t.Fatalf("mixed probe = state %q, err %v; want healthy", state, err)
	}
}

func TestProbeContextBothConfiguredFailureIsUnavailableWithoutLeak(t *testing.T) {
	secret := "/etc/private/db-password"
	runner := &fakeDatabaseProbeRunner{
		paths:   map[string]string{"psql": "/usr/bin/psql", "mysql": "/usr/bin/mysql"},
		runErrs: map[string]error{},
		run: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return nil, errors.New("authentication failed using " + secret)
		},
	}

	state, err := probeContext(context.Background(), runner)
	if state != integrationstate.Unavailable || err == nil {
		t.Fatalf("both failed = state %q, err %v; want unavailable", state, err)
	}
	if strings.Contains(err.Error(), secret) || strings.Contains(err.Error(), "authentication failed") {
		t.Fatalf("probe error leaked source details: %v", err)
	}
}

func TestProbeContextHonorsParentTimeoutAndReachesBothRunners(t *testing.T) {
	started := make(chan struct{}, 2)
	runner := &fakeDatabaseProbeRunner{
		paths: map[string]string{"psql": "/usr/bin/psql", "mysql": "/usr/bin/mysql"},
		run: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
			started <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	start := time.Now()
	state, err := probeContext(ctx, runner)
	if state != integrationstate.Unavailable || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed probe = state %q, err %v; want unavailable/deadline", state, err)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("timed probe took %s", elapsed)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("probe did not reach a source runner")
	}
}

func TestProbeContextHonorsAlreadyCancelledParentWithoutRunningCommands(t *testing.T) {
	runner := &fakeDatabaseProbeRunner{paths: map[string]string{"psql": "/usr/bin/psql", "mysql": "/usr/bin/mysql"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	state, err := probeContext(ctx, runner)
	if state != integrationstate.Unavailable || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled probe = state %q, err %v; want unavailable/canceled", state, err)
	}
	if calls := runner.Calls(); len(calls) != 0 {
		t.Fatalf("cancelled probe invoked commands: %#v", calls)
	}
}

func TestProbeContextMissingPostgresIdentityIsNotConfiguredWhenMariaDBAbsent(t *testing.T) {
	runner := &fakeDatabaseProbeRunner{
		paths: map[string]string{"psql": "/usr/bin/psql"},
		run: func(_ context.Context, _ string, _ ...string) ([]byte, error) {
			return nil, errors.New("sudo: unknown user postgres")
		},
	}

	state, err := probeContext(context.Background(), runner)
	if state != integrationstate.NotConfigured || !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("missing postgres identity = state %q, err %v; want not_configured", state, err)
	}
}

func TestExecDatabaseProbeRunnerCapturesOutputAfterCommandCompletes(t *testing.T) {
	script := filepath.Join(t.TempDir(), "database-probe")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf 'sudo: unknown user postgres' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	output, err := (execDatabaseProbeRunner{}).Run(context.Background(), script)
	if err == nil {
		t.Fatal("runner error = nil, want command failure")
	}
	if !strings.Contains(string(output), "unknown user postgres") {
		t.Fatalf("runner output = %q, want completed stderr", output)
	}
}

func TestMissingDatabaseClientClassificationDoesNotHideDiscoveryFailure(t *testing.T) {
	for _, err := range []error{
		errors.New("permission denied: metadata not found"),
		errors.New("client registry missing permission"),
	} {
		if isMissingDatabaseClient(err) {
			t.Fatalf("isMissingDatabaseClient(%q) = true, want false", err)
		}
	}
}
