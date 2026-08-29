package nginx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

func writeFakeCommand(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatalf("write fake %s: %v", name, err)
	}
}

func writeFakeNginx(t *testing.T, dir string) {
	t.Helper()
	writeFakeCommand(t, dir, "nginx", `
if [ "$1" = "-v" ]; then
  echo "nginx version: nginx/1.26.0" >&2
  exit 0
fi
if [ "$1" = "-t" ]; then
  echo "nginx: configuration file test is successful"
  exit 0
fi
exit 2`)
}

func TestStatusDistinguishesNginxReadiness(t *testing.T) {
	t.Run("not installed", func(t *testing.T) {
		dir := t.TempDir()
		t.Setenv("PATH", dir)

		got := New().Status()

		if got.Installed || got.Status != "not-installed" || got.StatusAvailable {
			t.Fatalf("unexpected missing status: %+v", got)
		}
		if got.ConfigTest.OK || got.ConfigTest.Output != "nginx executable not found" {
			t.Fatalf("unexpected missing config test: %+v", got.ConfigTest)
		}
	})

	t.Run("active", func(t *testing.T) {
		dir := t.TempDir()
		writeFakeNginx(t, dir)
		writeFakeCommand(t, dir, "systemctl", `
if [ "$1" = "is-active" ]; then
  echo active
  exit 0
fi
if [ "$1" = "show" ]; then
  echo "ActiveEnterTimestamp=Tue 2026-08-25 10:00:00 UTC"
  exit 0
fi
exit 2`)
		t.Setenv("PATH", dir)

		got := New().Status()

		if !got.Installed || !got.StatusAvailable || got.Status != "active" {
			t.Fatalf("unexpected active status: %+v", got)
		}
		if got.Version != "nginx/1.26.0" || got.Uptime != "Tue 2026-08-25 10:00:00 UTC" {
			t.Fatalf("unexpected active metadata: %+v", got)
		}
		if !got.ConfigTest.OK {
			t.Fatalf("expected successful config test: %+v", got.ConfigTest)
		}
	})

	t.Run("inactive", func(t *testing.T) {
		dir := t.TempDir()
		writeFakeNginx(t, dir)
		writeFakeCommand(t, dir, "systemctl", `
if [ "$1" = "is-active" ]; then
  echo inactive
  exit 3
fi
exit 2`)
		t.Setenv("PATH", dir)

		got := New().Status()

		if !got.Installed || !got.StatusAvailable || got.Status != "inactive" {
			t.Fatalf("unexpected inactive status: %+v", got)
		}
	})

	t.Run("systemd unavailable", func(t *testing.T) {
		dir := t.TempDir()
		writeFakeNginx(t, dir)
		t.Setenv("PATH", dir)

		got := New().Status()

		if !got.Installed || got.StatusAvailable || got.Status != "unknown" {
			t.Fatalf("unexpected unavailable status: %+v", got)
		}
		if !got.ConfigTest.OK {
			t.Fatalf("config test should remain independently observable: %+v", got.ConfigTest)
		}
	})
}

func readinessTestService(t *testing.T) (*Service, string) {
	t.Helper()
	root := t.TempDir()
	available := filepath.Join(root, "sites-available")
	enabled := filepath.Join(root, "sites-enabled")
	snippets := filepath.Join(root, "snippets")
	for _, directory := range []string{available, enabled, snippets} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return NewWithConfig(ServiceConfig{
		SitesAvailable: available,
		SitesEnabled:   enabled,
		SnippetsDir:    snippets,
	}), root
}

func writeReadinessNginx(t *testing.T, dir string, exitCode int, output string) {
	t.Helper()
	writeFakeCommand(t, dir, "nginx", `
if [ "$1" = "-t" ]; then
  `+output+`
	  exit `+strconv.Itoa(exitCode)+`
fi
exit 2`)
}

func writeReadinessSystemctl(t *testing.T, dir, state string, exitCode int) {
	t.Helper()
	writeFakeCommand(t, dir, "systemctl", `
if [ "$1" = "is-active" ]; then
  printf '`+state+`\n'
	  exit `+strconv.Itoa(exitCode)+`
fi
exit 2`)
}

func TestProbeReadinessContextHealthyAfterFreshBoundedObservation(t *testing.T) {
	service, root := readinessTestService(t)
	writeReadinessNginx(t, root, 0, "")
	writeReadinessSystemctl(t, root, "active", 0)
	t.Setenv("PATH", root)

	state, err := service.ProbeReadinessContext(context.Background())
	if err != nil || state != integrationstate.Healthy {
		t.Fatalf("ProbeReadinessContext() = state %q, err %v; want healthy", state, err)
	}
}

func TestProbeReadinessContextNotConfiguredForMissingBinaryOrRoots(t *testing.T) {
	t.Run("missing binary", func(t *testing.T) {
		service, root := readinessTestService(t)
		t.Setenv("PATH", root)

		state, err := service.ProbeReadinessContext(context.Background())
		if state != integrationstate.NotConfigured || !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("ProbeReadinessContext() = state %q, err %v; want not_configured", state, err)
		}
	})

	t.Run("missing configured root", func(t *testing.T) {
		service, root := readinessTestService(t)
		if err := os.RemoveAll(service.snippetsDir); err != nil {
			t.Fatal(err)
		}
		writeReadinessNginx(t, root, 0, "")
		writeReadinessSystemctl(t, root, "active", 0)
		t.Setenv("PATH", root)

		state, err := service.ProbeReadinessContext(context.Background())
		if state != integrationstate.NotConfigured || !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("ProbeReadinessContext() = state %q, err %v; want not_configured", state, err)
		}
	})
}

func TestProbeReadinessContextInactiveAndConfigFailureAreUnavailable(t *testing.T) {
	t.Run("inactive service", func(t *testing.T) {
		service, root := readinessTestService(t)
		writeReadinessNginx(t, root, 0, "")
		writeReadinessSystemctl(t, root, "inactive", 3)
		t.Setenv("PATH", root)

		state, err := service.ProbeReadinessContext(context.Background())
		if state != integrationstate.Unavailable || err == nil {
			t.Fatalf("ProbeReadinessContext() = state %q, err %v; want unavailable", state, err)
		}
	})

	t.Run("configuration failure", func(t *testing.T) {
		service, root := readinessTestService(t)
		writeReadinessNginx(t, root, 1, "printf 'secret /etc/nginx/private.conf\\n' >&2")
		writeReadinessSystemctl(t, root, "active", 0)
		t.Setenv("PATH", root)

		state, err := service.ProbeReadinessContext(context.Background())
		if state != integrationstate.Unavailable || err == nil {
			t.Fatalf("ProbeReadinessContext() = state %q, err %v; want unavailable", state, err)
		}
		if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "/etc/nginx/private.conf") {
			t.Fatalf("probe error leaked command output: %v", err)
		}
	})
}

func TestProbeReadinessContextHonorsTimeoutAndCancellation(t *testing.T) {
	t.Run("timeout", func(t *testing.T) {
		service, root := readinessTestService(t)
		writeReadinessNginx(t, root, 0, "")
		writeFakeCommand(t, root, "systemctl", `
if [ "$1" = "is-active" ]; then
  /bin/sleep 10
fi`)
		t.Setenv("PATH", root)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		defer cancel()

		started := time.Now()
		state, err := service.ProbeReadinessContext(ctx)
		if state != integrationstate.Unavailable || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("ProbeReadinessContext() = state %q, err %v; want deadline unavailable", state, err)
		}
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatalf("timed probe took %s", elapsed)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		service, root := readinessTestService(t)
		writeReadinessNginx(t, root, 0, "")
		writeFakeCommand(t, root, "systemctl", `
if [ "$1" = "is-active" ]; then
  /bin/sleep 10
fi`)
		t.Setenv("PATH", root)
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			time.Sleep(30 * time.Millisecond)
			cancel()
		}()

		state, err := service.ProbeReadinessContext(ctx)
		if state != integrationstate.Unavailable || !errors.Is(err, context.Canceled) {
			t.Fatalf("ProbeReadinessContext() = state %q, err %v; want canceled unavailable", state, err)
		}
	})
}
