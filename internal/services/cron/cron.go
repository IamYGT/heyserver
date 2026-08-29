// Package cron provides crontab management: list, add, edit, delete user and system cron jobs.
package cron

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrCrontabUnavailable identifies a host where user crontabs cannot be read
// or written because the crontab executable is absent.
var ErrCrontabUnavailable = errors.New("crontab command is unavailable")

// validUsernameRe matches POSIX-compliant Unix usernames.
// Usernames start with a letter or underscore; may contain letters, digits,
// hyphens, underscores, and dots; max 32 characters per POSIX spec.
var validUsernameRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_\-\.]{0,31}$`)

// isValidUsername returns true when u is a safe Unix username.
func isValidUsername(u string) bool {
	return validUsernameRe.MatchString(u)
}

var systemCronDirs = []string{
	"/etc/cron.d",
	"/etc/cron.daily",
	"/etc/cron.hourly",
	"/etc/cron.weekly",
	"/etc/cron.monthly",
}

var cronLineRe = regexp.MustCompile(
	`^((?:\*|[0-9,\-*/]+))\s+` +
		`((?:\*|[0-9,\-*/]+))\s+` +
		`((?:\*|[0-9,\-*/]+))\s+` +
		`((?:\*|[0-9,\-*/]+))\s+` +
		`((?:\*|[0-9,\-*/]+))\s+` +
		`(.+)$`,
)

var specialScheduleRe = regexp.MustCompile(
	`^(@(?:reboot|yearly|annually|monthly|weekly|daily|midnight|hourly))\s+(.+)$`,
)

func ListUserJobs(user string) ([]Job, error) {
	raw, err := runCrontabList(user)
	if err != nil {
		if strings.Contains(err.Error(), "no crontab for") {
			return []Job{}, nil
		}
		return nil, fmt.Errorf("crontab -l -u %s: %w", user, err)
	}
	return parseCrontabOutput(user, raw), nil
}

func ListAllJobs() ([]Job, error) {
	users, err := listUsers()
	if err != nil {
		return nil, err
	}
	var all []Job
	for _, u := range users {
		jobs, err := ListUserJobs(u)
		if err != nil {
			return nil, err
		}
		all = append(all, jobs...)
	}
	return all, nil
}

type statusLookPath func(string) (string, error)
type statusRun func(context.Context, string, ...string) (string, error)

// GetStatus reports whether crontab management and scheduled execution are
// both available on the native host.
func GetStatus(ctx context.Context) Status {
	return detectStatus(ctx, exec.LookPath, runStatusCommand)
}

func detectStatus(ctx context.Context, lookPath statusLookPath, run statusRun) Status {
	if _, err := lookPath("crontab"); err != nil {
		return Status{
			State:       StateNotInstalled,
			DaemonState: "unknown",
			Error:       "crontab command is not installed",
		}
	}

	status := Status{Installed: true, State: StateUnavailable, DaemonState: "unknown"}
	if _, err := lookPath("systemctl"); err != nil {
		status.Error = "systemctl is unavailable; cron daemon state cannot be verified"
		return status
	}

	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	output, err := run(probeCtx, "systemctl", "is-active", "cron")
	daemonState := strings.TrimSpace(output)
	if daemonState == "" {
		daemonState = "unknown"
	}
	status.DaemonState = daemonState
	if err == nil && daemonState == "active" {
		status.Available = true
		status.Running = true
		status.State = StateHealthy
		return status
	}

	switch daemonState {
	case "inactive", "failed", "activating", "deactivating":
		status.State = StateStopped
		status.Error = "cron service is " + daemonState
	default:
		status.Error = "cron daemon state could not be verified"
		if err != nil {
			status.Error += ": " + err.Error()
		}
	}
	return status
}

func runStatusCommand(ctx context.Context, name string, args ...string) (string, error) {
	output, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	return string(output), err
}

func AddJob(req CreateRequest) (*Job, error) {
	if err := ValidateExpression(req.Schedule); err != nil {
		return nil, err
	}
	existing, err := ListUserJobs(req.User)
	if err != nil {
		return nil, err
	}
	lines := cronJobsToLines(existing)
	if req.Description != "" {
		lines = append(lines, "# "+req.Description)
	}
	lines = append(lines, req.Schedule+" "+req.Command)
	if err := writeCrontab(req.User, lines); err != nil {
		return nil, err
	}
	return &Job{
		ID:            generateID(req.User, req.Schedule, req.Command),
		User:          req.User,
		Schedule:      req.Schedule,
		Command:       req.Command,
		Description:   req.Description,
		IsActive:      true,
		HumanSchedule: HumanReadable(req.Schedule),
	}, nil
}

func EditJob(id string, user string, req UpdateRequest) (*Job, error) {
	if err := ValidateExpression(req.Schedule); err != nil {
		return nil, err
	}
	jobs, err := ListUserJobs(user)
	if err != nil {
		return nil, err
	}
	found := false
	var newLines []string
	for _, j := range jobs {
		if j.ID == id {
			found = true
			if req.Description != "" {
				newLines = append(newLines, "# "+req.Description)
			}
			line := req.Schedule + " " + req.Command
			if !req.IsActive {
				line = "# " + line
			}
			newLines = append(newLines, line)
		} else {
			if j.Description != "" {
				newLines = append(newLines, "# "+j.Description)
			}
			l := j.Schedule + " " + j.Command
			if !j.IsActive {
				l = "# " + l
			}
			newLines = append(newLines, l)
		}
	}
	if !found {
		return nil, fmt.Errorf("cron job %s not found for user %s", id, user)
	}
	if err := writeCrontab(user, newLines); err != nil {
		return nil, err
	}
	return &Job{
		ID:            generateID(user, req.Schedule, req.Command),
		User:          user,
		Schedule:      req.Schedule,
		Command:       req.Command,
		Description:   req.Description,
		IsActive:      req.IsActive,
		HumanSchedule: HumanReadable(req.Schedule),
	}, nil
}

func DeleteJob(id string, user string) error {
	jobs, err := ListUserJobs(user)
	if err != nil {
		return err
	}
	var newLines []string
	found := false
	for _, j := range jobs {
		if j.ID == id {
			found = true
			continue
		}
		if j.Description != "" {
			newLines = append(newLines, "# "+j.Description)
		}
		l := j.Schedule + " " + j.Command
		if !j.IsActive {
			l = "# " + l
		}
		newLines = append(newLines, l)
	}
	if !found {
		return fmt.Errorf("cron job %s not found for user %s", id, user)
	}
	return writeCrontab(user, newLines)
}

func ListSystemFiles() ([]SystemFile, error) {
	var result []SystemFile
	for _, dir := range systemCronDirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		dirName := filepath.Base(dir)
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			info, err := entry.Info()
			if err != nil {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var cronEntries []string
			for _, line := range strings.Split(string(data), "\n") {
				t := strings.TrimSpace(line)
				if t != "" && !strings.HasPrefix(t, "#") {
					cronEntries = append(cronEntries, t)
				}
			}
			result = append(result, SystemFile{
				Path:    path,
				Name:    entry.Name(),
				Dir:     dirName,
				Entries: cronEntries,
				Size:    info.Size(),
			})
		}
	}
	return result, nil
}

func ValidateExpression(expr string) error {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return fmt.Errorf("cron expression cannot be empty")
	}
	if strings.HasPrefix(expr, "@") {
		valid := map[string]bool{
			"@reboot": true, "@yearly": true, "@annually": true,
			"@monthly": true, "@weekly": true, "@daily": true,
			"@midnight": true, "@hourly": true,
		}
		if !valid[expr] {
			return fmt.Errorf("unknown special schedule: %s", expr)
		}
		return nil
	}
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return fmt.Errorf("cron expression must have exactly 5 fields (got %d): %s", len(parts), expr)
	}
	specs := []struct {
		name string
		min  int
		max  int
	}{
		{"minute", 0, 59},
		{"hour", 0, 23},
		{"day-of-month", 1, 31},
		{"month", 1, 12},
		{"day-of-week", 0, 7},
	}
	for i, s := range specs {
		if err := validateCronField(parts[i], s.name, s.min, s.max); err != nil {
			return err
		}
	}
	return nil
}

func HumanReadable(expr string) string {
	expr = strings.TrimSpace(expr)
	switch expr {
	case "@reboot":
		return "At system reboot"
	case "@yearly", "@annually":
		return "Once a year (January 1st at midnight)"
	case "@monthly":
		return "Once a month (1st day at midnight)"
	case "@weekly":
		return "Once a week (Sunday at midnight)"
	case "@daily", "@midnight":
		return "Every day at midnight"
	case "@hourly":
		return "Every hour"
	}
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return expr
	}
	minute, hour, dom, month, dow := parts[0], parts[1], parts[2], parts[3], parts[4]
	var segments []string
	switch {
	case minute == "*" && hour == "*":
		segments = append(segments, "Every minute")
	case strings.Contains(minute, "/"):
		step := strings.SplitN(minute, "/", 2)[1]
		segments = append(segments, fmt.Sprintf("Every %s minutes", step))
		if hour != "*" {
			segments = append(segments, fmt.Sprintf("during hour %s", hour))
		}
	case minute == "*":
		segments = append(segments, fmt.Sprintf("Every minute of hour %s", hour))
	default:
		h := parseSimpleInt(hour, 0)
		m := parseSimpleInt(minute, 0)
		segments = append(segments, fmt.Sprintf("At %02d:%02d", h, m))
	}
	if dom != "*" {
		if dom == "1" {
			segments = append(segments, "on the 1st of the month")
		} else {
			segments = append(segments, fmt.Sprintf("on day %s of the month", dom))
		}
	}
	if dow != "*" {
		days := map[string]string{
			"0": "Sunday", "1": "Monday", "2": "Tuesday", "3": "Wednesday",
			"4": "Thursday", "5": "Friday", "6": "Saturday", "7": "Sunday",
			"sun": "Sunday", "mon": "Monday", "tue": "Tuesday", "wed": "Wednesday",
			"thu": "Thursday", "fri": "Friday", "sat": "Saturday",
		}
		if name, ok := days[strings.ToLower(dow)]; ok {
			segments = append(segments, "on "+name)
		} else {
			segments = append(segments, "on day-of-week "+dow)
		}
	}
	if month != "*" {
		names := map[string]string{
			"1": "January", "2": "February", "3": "March", "4": "April",
			"5": "May", "6": "June", "7": "July", "8": "August",
			"9": "September", "10": "October", "11": "November", "12": "December",
		}
		if name, ok := names[month]; ok {
			segments = append(segments, "in "+name)
		} else {
			segments = append(segments, "in month "+month)
		}
	}
	return strings.Join(segments, " ")
}

func GetLastRunTime(command string) time.Time {
	logPaths := []string{"/var/log/syslog", "/var/log/cron", "/var/log/cron.log"}
	keyword := filepath.Base(command)
	if keyword == "" {
		return time.Time{}
	}
	for _, logPath := range logPaths {
		if _, err := os.Stat(logPath); err != nil {
			continue
		}
		data, err := os.ReadFile(logPath)
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for i := len(lines) - 1; i >= 0; i-- {
			line := lines[i]
			if strings.Contains(line, "CRON") && strings.Contains(line, keyword) {
				if len(line) >= 15 {
					now := time.Now()
					for _, f := range []string{"Jan  2 15:04:05", "Jan 02 15:04:05"} {
						if t, err := time.Parse(f, line[:len(f)]); err == nil {
							return t.AddDate(now.Year(), 0, 0)
						}
					}
				}
			}
		}
	}
	return time.Time{}
}

func runCrontabList(user string) (string, error) {
	if !isValidUsername(user) {
		return "", fmt.Errorf("invalid username: %q", user)
	}
	crontabPath, err := exec.LookPath("crontab")
	if err != nil {
		return "", fmt.Errorf("%w: install the cron package for this host", ErrCrontabUnavailable)
	}
	cmd := exec.Command(crontabPath, "-l", "-u", user)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := strings.TrimSpace(string(exitErr.Stderr))
			if strings.Contains(stderr, "no crontab for") {
				return "", fmt.Errorf("no crontab for %s", user)
			}
			return "", fmt.Errorf("exit status %d: %s", exitErr.ExitCode(), stderr)
		}
		return "", err
	}
	return string(out), nil
}

func writeCrontab(user string, lines []string) error {
	if !isValidUsername(user) {
		return fmt.Errorf("invalid username: %q", user)
	}
	crontabPath, err := exec.LookPath("crontab")
	if err != nil {
		return fmt.Errorf("%w: install the cron package for this host", ErrCrontabUnavailable)
	}
	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	cmd := exec.Command(crontabPath, "-u", user, "-")
	cmd.Stdin = strings.NewReader(content)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("crontab write failed: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

func parseCrontabOutput(user string, raw string) []Job {
	var jobs []Job
	lines := strings.Split(raw, "\n")
	pendingComment := ""
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			pendingComment = ""
			continue
		}
		if strings.HasPrefix(trimmed, "#") {
			text := strings.TrimSpace(strings.TrimPrefix(trimmed, "#"))
			if m := cronLineRe.FindStringSubmatch(text); m != nil {
				schedule := m[1] + " " + m[2] + " " + m[3] + " " + m[4] + " " + m[5]
				command := strings.TrimSpace(m[6])
				jobs = append(jobs, Job{
					ID:   generateID(user, schedule, command),
					User: user, Schedule: schedule, Command: command,
					IsActive: false, HumanSchedule: HumanReadable(schedule),
				})
				pendingComment = ""
			} else {
				pendingComment = text
			}
			continue
		}
		if m := specialScheduleRe.FindStringSubmatch(trimmed); m != nil {
			schedule := m[1]
			command := strings.TrimSpace(m[2])
			jobs = append(jobs, Job{
				ID: generateID(user, schedule, command), User: user,
				Schedule: schedule, Command: command, Description: pendingComment,
				IsActive: true, HumanSchedule: HumanReadable(schedule),
			})
			pendingComment = ""
			continue
		}
		if m := cronLineRe.FindStringSubmatch(trimmed); m != nil {
			schedule := m[1] + " " + m[2] + " " + m[3] + " " + m[4] + " " + m[5]
			command := strings.TrimSpace(m[6])
			jobs = append(jobs, Job{
				ID: generateID(user, schedule, command), User: user,
				Schedule: schedule, Command: command, Description: pendingComment,
				IsActive: true, HumanSchedule: HumanReadable(schedule),
			})
			pendingComment = ""
		}
	}
	return jobs
}

func cronJobsToLines(jobs []Job) []string {
	var lines []string
	for _, j := range jobs {
		if j.Description != "" {
			lines = append(lines, "# "+j.Description)
		}
		l := j.Schedule + " " + j.Command
		if !j.IsActive {
			l = "# " + l
		}
		lines = append(lines, l)
	}
	return lines
}

func generateID(user, schedule, command string) string {
	sum := md5.Sum([]byte(user + "|" + schedule + "|" + command))
	return fmt.Sprintf("%x", sum[:8])
}

func listUsers() ([]string, error) {
	data, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return []string{"root"}, nil
	}
	seen := map[string]bool{}
	var users []string
	for _, line := range strings.Split(string(data), "\n") {
		parts := strings.Split(line, ":")
		if len(parts) < 7 {
			continue
		}
		username, shell := parts[0], parts[6]
		if username == "" {
			continue
		}
		if shell == "/usr/sbin/nologin" || shell == "/bin/false" || shell == "/sbin/nologin" {
			continue
		}
		if !seen[username] {
			seen[username] = true
			users = append(users, username)
		}
	}
	if len(users) == 0 {
		return []string{"root"}, nil
	}
	return users, nil
}

func validateCronField(field, name string, min, max int) error {
	if field == "*" {
		return nil
	}
	for _, part := range strings.Split(field, ",") {
		if err := validateCronPart(part, name, min, max); err != nil {
			return err
		}
	}
	return nil
}

func validateCronPart(part, name string, min, max int) error {
	if strings.Contains(part, "/") {
		segs := strings.SplitN(part, "/", 2)
		step, err := strconv.Atoi(segs[1])
		if err != nil || step < 1 {
			return fmt.Errorf("invalid step in %s field: %s", name, part)
		}
		if segs[0] != "*" {
			return validateCronPart(segs[0], name, min, max)
		}
		return nil
	}
	if strings.Contains(part, "-") {
		bounds := strings.SplitN(part, "-", 2)
		lo, err1 := strconv.Atoi(bounds[0])
		hi, err2 := strconv.Atoi(bounds[1])
		if err1 != nil || err2 != nil {
			return fmt.Errorf("invalid range in %s field: %s", name, part)
		}
		if lo < min || hi > max || lo > hi {
			return fmt.Errorf("%s field range %s out of bounds [%d-%d]", name, part, min, max)
		}
		return nil
	}
	n, err := strconv.Atoi(part)
	if err != nil {
		return fmt.Errorf("invalid value in %s field: %s", name, part)
	}
	if n < min || n > max {
		return fmt.Errorf("%s field value %d out of bounds [%d-%d]", name, n, min, max)
	}
	return nil
}

func parseSimpleInt(s string, fallback int) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}
