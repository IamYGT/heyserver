package backup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetSchedulePropagatesCrontabReadFailure(t *testing.T) {
	s := NewAt(t.TempDir())
	s.readCronTab = func(context.Context) (string, error) {
		return "", ErrCrontabUnavailable
	}

	entries, err := s.GetSchedule()
	if !errors.Is(err, ErrCrontabUnavailable) {
		t.Fatalf("error=%v", err)
	}
	if entries != nil {
		t.Fatalf("entries=%+v", entries)
	}
}

func TestReadSystemCrontabDistinguishesEmptyFromUnavailable(t *testing.T) {
	for _, test := range []struct {
		name       string
		message    string
		wantErr    bool
		wantOutput string
	}{
		{name: "no crontab", message: "no crontab for hserver", wantOutput: ""},
		{name: "permission denied", message: "permission denied", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "crontab")
			script := "#!/bin/sh\nprintf '%s\\n' '" + test.message + "' >&2\nexit 1\n"
			if err := os.WriteFile(binary, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)

			output, err := readSystemCrontab(context.Background())
			if test.wantErr && !errors.Is(err, ErrCrontabUnavailable) {
				t.Fatalf("error=%v", err)
			}
			if !test.wantErr && err != nil {
				t.Fatal(err)
			}
			if output != test.wantOutput {
				t.Fatalf("output=%q", output)
			}
		})
	}
}

func TestScheduleMutationsStopWhenCrontabCannotBeRead(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func(*Service) error
	}{
		{
			name: "set",
			run: func(s *Service) error {
				return s.SetSchedule(ScheduleOptions{Cron: "0 3 * * *", Type: "full", RetentionCount: 10})
			},
		},
		{
			name: "delete",
			run: func(s *Service) error {
				return s.DeleteSchedule("0 3 * * * /tmp/run-backup.sh type=full # hserver-backup")
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			s := NewAt(dir)
			writeCalled := false
			s.readCronTab = func(context.Context) (string, error) {
				return "", ErrCrontabUnavailable
			}
			s.writeCronTab = func(context.Context, string) error {
				writeCalled = true
				return nil
			}

			err := test.run(s)
			if !errors.Is(err, ErrCrontabUnavailable) {
				t.Fatalf("error=%v", err)
			}
			if writeCalled {
				t.Fatal("crontab write attempted after failed read")
			}
			if _, statErr := os.Stat(filepath.Join(dir, "run-backup.sh")); !os.IsNotExist(statErr) {
				t.Fatalf("backup script changed after failed read: %v", statErr)
			}
		})
	}
}

func TestSetScheduleRejectsInvalidOptionsBeforeHostObservation(t *testing.T) {
	for _, test := range []struct {
		name string
		opts ScheduleOptions
	}{
		{name: "cron", opts: ScheduleOptions{Cron: "@daily", Type: "full", RetentionCount: 10}},
		{name: "type", opts: ScheduleOptions{Cron: "0 3 * * *", Type: "archive", RetentionCount: 10}},
		{name: "retention zero", opts: ScheduleOptions{Cron: "0 3 * * *", Type: "full", RetentionCount: 0}},
		{name: "retention too large", opts: ScheduleOptions{Cron: "0 3 * * *", Type: "full", RetentionCount: 366}},
		{name: "database metadata", opts: ScheduleOptions{Cron: "0 3 * * *", Type: "database", Database: "db name", RetentionCount: 10}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := NewAt(t.TempDir())
			readCalled := false
			s.readCronTab = func(context.Context) (string, error) {
				readCalled = true
				return "", nil
			}

			err := s.SetSchedule(test.opts)
			if !errors.Is(err, ErrInvalidScheduleOptions) {
				t.Fatalf("error=%v", err)
			}
			if readCalled {
				t.Fatal("host crontab was observed for invalid input")
			}
		})
	}
}

func TestDeleteScheduleRejectsUnmanagedOrMissingTargets(t *testing.T) {
	t.Run("unmanaged line", func(t *testing.T) {
		s := NewAt(t.TempDir())
		readCalled := false
		s.readCronTab = func(context.Context) (string, error) {
			readCalled = true
			return "", nil
		}

		err := s.DeleteSchedule("0 3 * * * /usr/local/bin/unrelated")
		if !errors.Is(err, ErrInvalidScheduleTarget) {
			t.Fatalf("error=%v", err)
		}
		if readCalled {
			t.Fatal("crontab read attempted for an unmanaged target")
		}
	})

	t.Run("managed line not present", func(t *testing.T) {
		s := NewAt(t.TempDir())
		writeCalled := false
		s.readCronTab = func(context.Context) (string, error) {
			return "0 4 * * * /var/lib/hserver/backups/run-backup.sh type=files retention=5 # hserver-backup\n", nil
		}
		s.writeCronTab = func(context.Context, string) error {
			writeCalled = true
			return nil
		}

		err := s.DeleteSchedule("0 3 * * * /var/lib/hserver/backups/run-backup.sh type=full retention=5 # hserver-backup")
		if !errors.Is(err, ErrScheduleNotFound) {
			t.Fatalf("error=%v", err)
		}
		if writeCalled {
			t.Fatal("crontab write attempted for a missing target")
		}
	})
}

func TestDeleteScheduleRemovesOnlyTheExactManagedTarget(t *testing.T) {
	target := "0 3 * * * /var/lib/hserver/backups/run-backup.sh type=full retention=5 # hserver-backup"
	unrelated := "15 2 * * * /usr/local/bin/unrelated"
	otherManaged := "0 4 * * * /var/lib/hserver/backups/run-backup.sh type=files retention=5 # hserver-backup"
	s := NewAt(t.TempDir())
	s.readCronTab = func(context.Context) (string, error) {
		return strings.Join([]string{unrelated, target, otherManaged}, "\n") + "\n", nil
	}
	var written string
	s.writeCronTab = func(_ context.Context, content string) error {
		written = content
		return nil
	}

	if err := s.DeleteSchedule(target); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(written, target) {
		t.Fatalf("target remains:\n%s", written)
	}
	if !strings.Contains(written, unrelated) || !strings.Contains(written, otherManaged) {
		t.Fatalf("unrelated entries changed:\n%s", written)
	}
}

func TestSetScheduleReplacesOnlyTheExactManagedType(t *testing.T) {
	s := NewAt(t.TempDir())
	s.readCronTab = func(context.Context) (string, error) {
		return strings.Join([]string{
			"SHELL=/bin/bash",
			"0 1 * * * /old type=fuller retention=1 # hserver-backup",
			"0 2 * * * /old type=full retention=2 # hserver-backup",
		}, "\n"), nil
	}
	var written string
	s.writeCronTab = func(_ context.Context, content string) error {
		written = content
		return nil
	}

	if err := s.SetSchedule(ScheduleOptions{Cron: "30 3 * * *", Type: "full", RetentionCount: 7}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(written, "type=fuller retention=1") {
		t.Fatalf("unrelated managed schedule was removed:\n%s", written)
	}
	if strings.Contains(written, "0 2 * * * /old type=full retention=2") {
		t.Fatalf("old full schedule was preserved:\n%s", written)
	}
	if !strings.Contains(written, "30 3 * * *") || !strings.Contains(written, "type=full retention=7") {
		t.Fatalf("replacement schedule missing:\n%s", written)
	}
}
