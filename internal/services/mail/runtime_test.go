package mail

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnconfiguredMailRuntimeReturnsErrNotConfigured(t *testing.T) {
	service := New("", "", "")

	tests := []struct {
		name string
		call func() error
	}{
		{name: "start", call: service.StartService},
		{name: "stop", call: service.StopService},
		{name: "restart", call: service.RestartService},
		{name: "config", call: func() error {
			_, err := service.GetConfig()
			return err
		}},
		{name: "listeners", call: func() error {
			_, err := service.GetListenerInfo()
			return err
		}},
		{name: "storage", call: func() error {
			_, err := service.GetStorageInfo()
			return err
		}},
		{name: "version", call: func() error {
			_, err := service.GetVersion()
			return err
		}},
		{name: "mail logs", call: func() error {
			_, err := service.GetMailLogs(10)
			return err
		}},
		{name: "search logs", call: func() error {
			_, err := service.SearchMailLogs("delivery")
			return err
		}},
		{name: "API", call: func() error {
			_, err := service.ListDomains()
			return err
		}},
		{name: "failed logins", call: func() error {
			_, err := service.GetFailedLogins(10)
			return err
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); !errors.Is(err, ErrNotConfigured) {
				t.Fatalf("error = %v, want ErrNotConfigured", err)
			}
		})
	}
}

func TestGetVersionUsesExplicitConfiguredBinary(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "mail-version")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' 'Mail Server v1.2.3'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	service := New("", "", "").WithRuntime("", "", bin)
	version, err := service.GetVersion()
	if err != nil {
		t.Fatalf("GetVersion() error = %v", err)
	}
	if version.Version != "1.2.3" || version.Raw != "Mail Server v1.2.3" {
		t.Fatalf("version = %#v, want explicit binary output", version)
	}
}

func TestMailLogsUseConfiguredServiceName(t *testing.T) {
	binDir := t.TempDir()
	argsPath := filepath.Join(t.TempDir(), "journalctl.args")
	journalctl := filepath.Join(binDir, "journalctl")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$JOURNAL_ARGS\"\nprintf '%s\\n' '2025-01-15T10:23:45+0000 host mail-service[1234]: INFO delivery complete'\n"
	if err := os.WriteFile(journalctl, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JOURNAL_ARGS", argsPath)
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	service := New("", "", "").WithRuntime("mail-custom", "", "")
	entries, err := service.GetMailLogs(2)
	if err != nil {
		t.Fatalf("GetMailLogs() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Message != "INFO delivery complete" {
		t.Fatalf("entries = %#v", entries)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSpace(string(args)), "\n"); len(got) < 2 || got[0] != "-u" || got[1] != "mail-custom" {
		t.Fatalf("journalctl args = %q, want -u mail-custom", string(args))
	}

	if _, err := service.SearchMailLogs("delivery"); err != nil {
		t.Fatalf("SearchMailLogs() error = %v", err)
	}
	args, err = os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Split(strings.TrimSpace(string(args)), "\n")
	if len(got) < 2 || got[0] != "-u" || got[1] != "mail-custom" {
		t.Fatalf("search journalctl args = %q, want -u mail-custom", string(args))
	}
	if !strings.Contains(string(args), "--grep\ndelivery") {
		t.Fatalf("search journalctl args = %q, want --grep delivery", string(args))
	}
}
