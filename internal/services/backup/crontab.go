package backup

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const crontabCommandTimeout = 10 * time.Second

// ErrCrontabUnavailable means HServer could not safely observe or update the
// installation-owned user crontab. Callers must not treat it as an empty
// schedule because doing so could overwrite unrelated entries.
var ErrCrontabUnavailable = errors.New("backup scheduling unavailable")

// ErrInvalidScheduleTarget means a delete request did not identify a valid
// HServer-managed backup schedule line.
var ErrInvalidScheduleTarget = errors.New("invalid backup schedule target")

// ErrScheduleNotFound means the requested managed schedule is not present in
// the currently observed crontab.
var ErrScheduleNotFound = errors.New("backup schedule not found")

// ErrInvalidScheduleOptions means a schedule cannot be represented safely or
// would request unsupported backup behavior.
var ErrInvalidScheduleOptions = errors.New("invalid backup schedule options")

type crontabReader func(context.Context) (string, error)
type crontabWriter func(context.Context, string) error

func (s *Service) loadCrontab() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), crontabCommandTimeout)
	defer cancel()
	reader := s.readCronTab
	if reader == nil {
		reader = readSystemCrontab
	}
	return reader(ctx)
}

func (s *Service) installCrontab(content string) error {
	ctx, cancel := context.WithTimeout(context.Background(), crontabCommandTimeout)
	defer cancel()
	writer := s.writeCronTab
	if writer == nil {
		writer = writeSystemCrontab
	}
	return writer(ctx, content)
}

func readSystemCrontab(ctx context.Context) (string, error) {
	cmd := exec.CommandContext(ctx, "crontab", "-l")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	output, err := cmd.CombinedOutput()
	if err == nil {
		return string(output), nil
	}
	if isEmptySystemCrontab(err, string(output)) {
		return "", nil
	}
	return "", crontabCommandError(ctx, "read", err, string(output))
}

func writeSystemCrontab(ctx context.Context, content string) error {
	cmd := exec.CommandContext(ctx, "crontab", "-")
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	cmd.Stdin = strings.NewReader(content)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	return crontabCommandError(ctx, "write", err, string(output))
}

func isEmptySystemCrontab(err error, output string) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == 1 &&
		strings.Contains(strings.ToLower(output), "no crontab for")
}

func crontabCommandError(ctx context.Context, operation string, err error, output string) error {
	detail := strings.TrimSpace(output)
	if ctxErr := ctx.Err(); ctxErr != nil {
		detail = ctxErr.Error()
	} else if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("%w: crontab %s failed: %s", ErrCrontabUnavailable, operation, detail)
}
