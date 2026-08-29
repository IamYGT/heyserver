package cron

import (
	"context"
	"errors"
	"testing"
)

func TestDetectStatusDistinguishesRuntimeStates(t *testing.T) {
	tests := []struct {
		name      string
		lookPath  statusLookPath
		run       statusRun
		wantState ReadinessState
		installed bool
		running   bool
		available bool
	}{
		{
			name: "crontab is not installed",
			lookPath: func(name string) (string, error) {
				return "", errors.New(name + " not found")
			},
			run:       func(context.Context, string, ...string) (string, error) { return "", nil },
			wantState: StateNotInstalled,
		},
		{
			name: "daemon is active",
			lookPath: func(name string) (string, error) {
				return "/usr/bin/" + name, nil
			},
			run:       func(context.Context, string, ...string) (string, error) { return "active\n", nil },
			wantState: StateHealthy,
			installed: true,
			running:   true,
			available: true,
		},
		{
			name: "daemon is stopped",
			lookPath: func(name string) (string, error) {
				return "/usr/bin/" + name, nil
			},
			run: func(context.Context, string, ...string) (string, error) {
				return "inactive\n", errors.New("exit status 3")
			},
			wantState: StateStopped,
			installed: true,
		},
		{
			name: "daemon state is unavailable",
			lookPath: func(name string) (string, error) {
				if name == "crontab" {
					return "/usr/bin/crontab", nil
				}
				return "", errors.New("systemctl not found")
			},
			run:       func(context.Context, string, ...string) (string, error) { return "", nil },
			wantState: StateUnavailable,
			installed: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := detectStatus(context.Background(), tc.lookPath, tc.run)
			if got.State != tc.wantState || got.Installed != tc.installed || got.Running != tc.running || got.Available != tc.available {
				t.Fatalf("detectStatus() = %#v", got)
			}
		})
	}
}

func TestListAllJobsRejectsMissingCrontabInsteadOfReturningEmptyInventory(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	jobs, err := ListAllJobs()
	if !errors.Is(err, ErrCrontabUnavailable) {
		t.Fatalf("ListAllJobs() error = %v, want ErrCrontabUnavailable", err)
	}
	if jobs != nil {
		t.Fatalf("ListAllJobs() jobs = %#v, want nil", jobs)
	}
}

func TestValidateExpression(t *testing.T) {
	valid := []string{
		"* * * * *",
		"0 3 * * *",
		"*/15 * * * *",
		"0 0 1 1 *",
		"30 6 * * 1-5",
		"0,30 * * * *",
		"0 8-18 * * 1-5",
		"@daily", "@hourly", "@reboot", "@weekly", "@monthly", "@yearly", "@annually", "@midnight",
	}
	for _, expr := range valid {
		if err := ValidateExpression(expr); err != nil {
			t.Errorf("expected valid for %q, got error: %v", expr, err)
		}
	}

	invalid := []string{
		"",
		"60 * * * *",
		"* 25 * * *",
		"* * 0 * *",
		"* * * 13 *",
		"* * * * 8",
		"@unknown",
		"only four fields",
	}
	for _, expr := range invalid {
		if err := ValidateExpression(expr); err == nil {
			t.Errorf("expected error for %q, got nil", expr)
		}
	}
}

func TestHumanReadable(t *testing.T) {
	cases := []struct {
		expr string
		want string
	}{
		{"@reboot", "At system reboot"},
		{"@daily", "Every day at midnight"},
		{"@hourly", "Every hour"},
		{"@monthly", "Once a month (1st day at midnight)"},
		{"* * * * *", "Every minute"},
		{"0 3 * * *", "At 03:00"},
		{"0 0 1 * *", "At 00:00 on the 1st of the month"},
		{"0 12 * * 1", "At 12:00 on Monday"},
		{"*/15 * * * *", "Every 15 minutes"},
	}
	for _, tc := range cases {
		got := HumanReadable(tc.expr)
		if got != tc.want {
			t.Errorf("HumanReadable(%q) = %q, want %q", tc.expr, got, tc.want)
		}
	}
}

func TestParseCrontabOutput(t *testing.T) {
	raw := "# Nightly backup\n0 3 * * * /usr/bin/backup.sh\n# Weekly cleanup\n0 0 * * 0 /usr/bin/cleanup.sh\n# 0 6 * * * /usr/bin/disabled.sh\n"
	jobs := parseCrontabOutput("root", raw)
	if len(jobs) != 3 {
		t.Fatalf("expected 3 jobs, got %d", len(jobs))
	}
	if jobs[0].Schedule != "0 3 * * *" {
		t.Errorf("job[0] schedule = %q", jobs[0].Schedule)
	}
	if jobs[0].Command != "/usr/bin/backup.sh" {
		t.Errorf("job[0] command = %q", jobs[0].Command)
	}
	if jobs[0].Description != "Nightly backup" {
		t.Errorf("job[0] description = %q", jobs[0].Description)
	}
	if !jobs[0].IsActive {
		t.Error("job[0] should be active")
	}
	if jobs[2].IsActive {
		t.Error("job[2] should be inactive")
	}
}

func TestGenerateID_Deterministic(t *testing.T) {
	id1 := generateID("root", "0 3 * * *", "/bin/backup.sh")
	id2 := generateID("root", "0 3 * * *", "/bin/backup.sh")
	id3 := generateID("root", "0 4 * * *", "/bin/backup.sh")
	if id1 != id2 {
		t.Error("same inputs should produce same ID")
	}
	if id1 == id3 {
		t.Error("different inputs should produce different IDs")
	}
	if len(id1) != 16 {
		t.Errorf("ID length = %d, want 16", len(id1))
	}
}

func TestCronJobsToLines(t *testing.T) {
	jobs := []Job{
		{Schedule: "0 3 * * *", Command: "/bin/backup.sh", Description: "nightly", IsActive: true},
		{Schedule: "0 5 * * *", Command: "/bin/maint.sh", Description: "", IsActive: false},
	}
	lines := cronJobsToLines(jobs)
	if lines[0] != "# nightly" {
		t.Errorf("lines[0] = %q, want %q", lines[0], "# nightly")
	}
	if lines[1] != "0 3 * * * /bin/backup.sh" {
		t.Errorf("lines[1] = %q", lines[1])
	}
	if lines[2] != "# 0 5 * * * /bin/maint.sh" {
		t.Errorf("lines[2] = %q, want %q", lines[2], "# 0 5 * * * /bin/maint.sh")
	}
}

func TestIsValidUsername(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		user  string
		valid bool
	}{
		{"root", "root", true},
		{"underscore start", "_deploy", true},
		{"with dot", "service.account", true},
		{"with hyphen", "app-user", true},
		{"max length 32", "abcdefghijklmnopqrstuvwxyz123456", true},
		{"empty", "", false},
		{"starts with digit", "1user", false},
		{"starts with hyphen", "-user", false},
		{"too long", "abcdefghijklmnopqrstuvwxyz1234567", false},
		{"semicolon injection", "root;rm", false},
		{"space", "root user", false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := isValidUsername(tc.user); got != tc.valid {
				t.Fatalf("isValidUsername(%q) = %v, want %v", tc.user, got, tc.valid)
			}
		})
	}
}

func TestValidateCronField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		field   string
		fieldNm string
		min     int
		max     int
		wantErr bool
	}{
		{"wildcard", "*", "minute", 0, 59, false},
		{"single value", "30", "minute", 0, 59, false},
		{"range", "1-5", "day-of-week", 0, 7, false},
		{"step", "*/15", "minute", 0, 59, false},
		{"list", "0,15,30,45", "minute", 0, 59, false},
		{"out of bounds high", "60", "minute", 0, 59, true},
		{"minimum bound", "1", "day-of-month", 1, 31, false},
		{"invalid range order", "10-5", "hour", 0, 23, true},
		{"invalid step", "*/0", "minute", 0, 59, true},
		{"non numeric", "abc", "hour", 0, 23, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateCronField(tc.field, tc.fieldNm, tc.min, tc.max)
			if tc.wantErr && err == nil {
				t.Fatalf("validateCronField(%q) expected error", tc.field)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateCronField(%q) unexpected error: %v", tc.field, err)
			}
		})
	}
}

func TestValidateExpression_TableDriven(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		expr    string
		wantErr bool
	}{
		{"every minute", "* * * * *", false},
		{"step hour", "0 */2 * * *", false},
		{"complex list range", "0 8-18/2 * * 1-5", false},
		{"sunday alias 7", "0 0 * * 7", false},
		{"special reboot", "@reboot", false},
		{"whitespace trimmed", "  0 3 * * *  ", false},
		{"six fields", "0 0 0 * * *", true},
		{"invalid month", "* * * 0 *", true},
		{"invalid special partial", "@daily extra", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateExpression(tc.expr)
			if tc.wantErr && err == nil {
				t.Fatalf("ValidateExpression(%q) expected error", tc.expr)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("ValidateExpression(%q) unexpected error: %v", tc.expr, err)
			}
		})
	}
}

func TestParseCrontabOutput_SpecialSchedules(t *testing.T) {
	t.Parallel()

	raw := "# On reboot\n@reboot /usr/local/bin/boot.sh\n@daily /usr/bin/daily.sh\n"
	jobs := parseCrontabOutput("deploy", raw)

	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs, got %d", len(jobs))
	}
	if jobs[0].Schedule != "@reboot" || jobs[0].Command != "/usr/local/bin/boot.sh" {
		t.Fatalf("job[0] = %+v", jobs[0])
	}
	if jobs[0].Description != "On reboot" || !jobs[0].IsActive {
		t.Fatalf("job[0] metadata = %+v", jobs[0])
	}
	if jobs[1].Schedule != "@daily" || jobs[1].Command != "/usr/bin/daily.sh" {
		t.Fatalf("job[1] = %+v", jobs[1])
	}
}

func TestParseSimpleInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		fallback int
		want     int
	}{
		{"12", 0, 12},
		{"bad", 7, 7},
		{"", 3, 3},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			if got := parseSimpleInt(tc.input, tc.fallback); got != tc.want {
				t.Fatalf("parseSimpleInt(%q, %d) = %d, want %d", tc.input, tc.fallback, got, tc.want)
			}
		})
	}
}

func TestHumanReadable_Extended(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		want string
	}{
		{"@yearly", "Once a year (January 1st at midnight)"},
		{"@annually", "Once a year (January 1st at midnight)"},
		{"@weekly", "Once a week (Sunday at midnight)"},
		{"@midnight", "Every day at midnight"},
		{"* 9 * * *", "Every minute of hour 9"},
		{"0 12 * * sun", "At 12:00 on Sunday"},
		{"0 0 15 6 *", "At 00:00 on day 15 of the month in June"},
		{"not a cron", "not a cron"},
	}

	for _, tc := range tests {
		t.Run(tc.expr, func(t *testing.T) {
			t.Parallel()
			if got := HumanReadable(tc.expr); got != tc.want {
				t.Fatalf("HumanReadable(%q) = %q, want %q", tc.expr, got, tc.want)
			}
		})
	}
}
