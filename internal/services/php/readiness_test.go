package php

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

type phpReadinessFakeRunner struct {
	mu      sync.Mutex
	outputs map[string][]byte
	errors  map[string]error
	calls   []string
	run     func(context.Context, string, ...string) ([]byte, error)
}

func (r *phpReadinessFakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	call := strings.Join(append([]string{name}, args...), " ")
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
	if r.run != nil {
		return r.run(ctx, name, args...)
	}
	return r.outputs[call], r.errors[call]
}

func newPHPFPMReadinessFixture(t *testing.T) (*Service, *phpReadinessFakeRunner, string, string) {
	t.Helper()
	root := t.TempDir()
	configRoot := filepath.Join(root, "etc", "php")
	binaryRoot := filepath.Join(root, "usr", "sbin")
	poolDir := filepath.Join(configRoot, "8.4", "fpm", "pool.d")
	if err := os.MkdirAll(poolDir, 0o755); err != nil {
		t.Fatalf("create PHP-FPM pool directory: %v", err)
	}
	if err := os.MkdirAll(binaryRoot, 0o755); err != nil {
		t.Fatalf("create PHP-FPM binary directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(poolDir, "www.conf"), []byte("[www]\nlisten = /run/php/php8.4-fpm.sock\n"), 0o644); err != nil {
		t.Fatalf("write PHP-FPM pool configuration: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binaryRoot, "php-fpm8.4"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write PHP-FPM binary fixture: %v", err)
	}

	runner := &phpReadinessFakeRunner{
		outputs: map[string][]byte{
			"systemctl is-active php8.4-fpm.service": []byte("active\n"),
		},
		errors: map[string]error{},
	}
	service := NewWithConfig(ServiceConfig{ConfigRoot: configRoot, BinaryRoot: binaryRoot})
	service.runCommandContext = runner.Run
	return service, runner, configRoot, binaryRoot
}

func TestPHPFPMProbeReadinessContextHealthyAfterCompleteObservation(t *testing.T) {
	service, runner, _, binaryRoot := newPHPFPMReadinessFixture(t)

	state, err := service.ProbeReadinessContext(context.Background())
	if err != nil || state != integrationstate.Healthy {
		t.Fatalf("ProbeReadinessContext() = state %q, err %v; want healthy", state, err)
	}
	wantCalls := []string{
		"systemctl is-active php8.4-fpm.service",
		filepath.Join(binaryRoot, "php-fpm8.4") + " -t",
	}
	if !equalPHPFPMReadinessStrings(runner.calls, wantCalls) {
		t.Fatalf("runner calls = %#v, want %#v", runner.calls, wantCalls)
	}
}

func TestPHPFPMProbeReadinessContextMissingOrInvalidConfigRootIsNotConfigured(t *testing.T) {
	tests := []struct {
		name      string
		root      func(string) string
		wantState integrationstate.State
	}{
		{name: "missing", root: func(parent string) string { return filepath.Join(parent, "missing") }, wantState: integrationstate.NotConfigured},
		{name: "regular file", root: func(parent string) string {
			path := filepath.Join(parent, "php")
			if err := os.WriteFile(path, []byte("not a directory"), 0o644); err != nil {
				t.Fatalf("write invalid PHP config root: %v", err)
			}
			return path
		}, wantState: integrationstate.Unavailable},
		{name: "relative", root: func(string) string { return "relative/php" }, wantState: integrationstate.NotConfigured},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			service := &Service{configRoot: test.root(parent), binaryRoot: filepath.Join(parent, "bin")}
			state, err := service.ProbeReadinessContext(context.Background())
			if state != test.wantState || err == nil {
				t.Fatalf("ProbeReadinessContext() = state %q, err %v; want %q", state, err, test.wantState)
			}
			if test.wantState == integrationstate.NotConfigured && !errors.Is(err, ErrNotConfigured) {
				t.Fatalf("ProbeReadinessContext() error = %v, want ErrNotConfigured", err)
			}
		})
	}
}

func TestNewWithConfigDoesNotFallbackExplicitRelativePHPRoots(t *testing.T) {
	service := NewWithConfig(ServiceConfig{ConfigRoot: "relative/php", BinaryRoot: "relative/bin"})
	if service.configRoot != "" || service.binaryRoot != "" {
		t.Fatalf("relative roots became config=%q binary=%q; want unavailable empty roots", service.configRoot, service.binaryRoot)
	}
}

func TestDefaultPHPCommandContextRunnerKillsDescendants(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, _ = defaultPHPCommandContextRunner(ctx, "/bin/sh", "-c", "sleep 2 & wait")
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("context error = %v, want deadline exceeded", ctx.Err())
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("context runner waited %s for descendant", elapsed)
	}
}

func TestPHPFPMProbeReadinessContextConfiguredRootWithoutPoolIsUnavailable(t *testing.T) {
	service, runner, configRoot, _ := newPHPFPMReadinessFixture(t)
	if err := os.Remove(filepath.Join(configRoot, "8.4", "fpm", "pool.d", "www.conf")); err != nil {
		t.Fatalf("remove pool fixture: %v", err)
	}

	state, err := service.ProbeReadinessContext(context.Background())
	if state != integrationstate.Unavailable || err == nil {
		t.Fatalf("ProbeReadinessContext() = state %q, err %v; want unavailable", state, err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %#v, want no commands without a pool", runner.calls)
	}
}

func TestPHPFPMProbeReadinessContextMissingBinaryIsUnavailable(t *testing.T) {
	service, runner, _, binaryRoot := newPHPFPMReadinessFixture(t)
	if err := os.Remove(filepath.Join(binaryRoot, "php-fpm8.4")); err != nil {
		t.Fatalf("remove binary fixture: %v", err)
	}

	state, err := service.ProbeReadinessContext(context.Background())
	if state != integrationstate.Unavailable || err == nil {
		t.Fatalf("ProbeReadinessContext() = state %q, err %v; want unavailable", state, err)
	}
	if len(runner.calls) != 0 {
		t.Fatalf("runner calls = %#v, want no commands without a binary", runner.calls)
	}
}

func TestPHPFPMProbeReadinessContextInactiveServiceIsUnavailable(t *testing.T) {
	service, runner, _, _ := newPHPFPMReadinessFixture(t)
	runner.outputs["systemctl is-active php8.4-fpm.service"] = []byte("inactive\n")
	runner.errors["systemctl is-active php8.4-fpm.service"] = errors.New("exit status 3")

	state, err := service.ProbeReadinessContext(context.Background())
	if state != integrationstate.Unavailable || err == nil {
		t.Fatalf("ProbeReadinessContext() = state %q, err %v; want unavailable", state, err)
	}
	if len(runner.calls) != 1 || runner.calls[0] != "systemctl is-active php8.4-fpm.service" {
		t.Fatalf("runner calls = %#v, want only inactive service observation", runner.calls)
	}
}

func TestPHPFPMProbeReadinessContextConfigTestFailureIsUnavailableAndSafe(t *testing.T) {
	service, runner, _, binaryRoot := newPHPFPMReadinessFixture(t)
	testCommand := filepath.Join(binaryRoot, "php-fpm8.4") + " -t"
	runner.outputs[testCommand] = []byte("syntax error in /srv/private/php-fpm.conf\n")
	runner.errors[testCommand] = errors.New("private configuration failure at /srv/private/php-fpm.conf")

	state, err := service.ProbeReadinessContext(context.Background())
	if state != integrationstate.Unavailable || err == nil {
		t.Fatalf("ProbeReadinessContext() = state %q, err %v; want unavailable", state, err)
	}
	if strings.Contains(err.Error(), "/srv/private") || strings.Contains(err.Error(), "syntax error") {
		t.Fatalf("probe error leaked command details: %v", err)
	}
}

func TestPHPFPMProbeReadinessContextCancellationReachesRunnerAndDoesNotLeak(t *testing.T) {
	service, runner, _, _ := newPHPFPMReadinessFixture(t)
	started := make(chan struct{})
	finished := make(chan struct{})
	runner.run = func(ctx context.Context, name string, _ ...string) ([]byte, error) {
		if name != "systemctl" {
			return nil, nil
		}
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
		t.Fatal("PHP-FPM probe did not reach systemd runner")
	}
	select {
	case got := <-result:
		if got.state != integrationstate.Unavailable || !errors.Is(got.err, context.Canceled) {
			t.Fatalf("cancelled probe = state %q, err %v; want unavailable/canceled", got.state, got.err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled PHP-FPM probe did not return")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("cancelled PHP-FPM runner did not finish")
	}
}

func TestPHPFPMProbeReadinessContextTimeoutDoesNotLeakRunner(t *testing.T) {
	service, runner, _, _ := newPHPFPMReadinessFixture(t)
	started := make(chan struct{})
	finished := make(chan struct{})
	runner.run = func(ctx context.Context, name string, _ ...string) ([]byte, error) {
		if name != "systemctl" {
			return nil, nil
		}
		close(started)
		defer close(finished)
		<-ctx.Done()
		return nil, ctx.Err()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	startedAt := time.Now()
	state, err := service.ProbeReadinessContext(ctx)
	if state != integrationstate.Unavailable || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("timed probe = state %q, err %v; want unavailable/deadline", state, err)
	}
	if elapsed := time.Since(startedAt); elapsed > time.Second {
		t.Fatalf("timed PHP-FPM probe took %s", elapsed)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("PHP-FPM probe did not reach blocking runner")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("timed PHP-FPM runner did not finish")
	}
}

func equalPHPFPMReadinessStrings(got, want []string) bool {
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
