package shell

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// allowedCommands is an explicit allowlist of commands that may be invoked
// via Execute/ExecuteWithTimeout.
//
// C-1 fix: "bash" and "sh" are intentionally absent — shell interpreters
// bypass argument injection protection entirely when called with -c.
// Callers that need pipelines must use exec.Cmd pipe chaining instead.
var allowedCommands = map[string]bool{
	"nginx":       true,
	"systemctl":   true,
	"certbot":     true,
	"pm2":         true,
	"ufw":         true,
	"iptables":    true,
	"psql":        true,
	"sudo":        true,
	"mysql":       true,
	"pg_dump":     true,
	"pg_dumpall":  true,
	"pg_restore":  true,
	"mysqldump":   true,
	"gunzip":      true,
	"crontab":     true,
	"df":          true,
	"free":        true,
	"uptime":      true,
	"top":         true,
	"cat":         true,
	"ls":          true,
	"stat":        true,
	"wc":          true,
	"du":          true,
	"hostname":    true,
	"lsb_release": true,
	"ss":          true,
	"ip":          true,
	"nproc":       true,
	"lscpu":       true,
	"openssl":     true,
	"ping":        true,
	"tail":        true,
	"head":        true,
	"find":        true,
	"chmod":       true,
	"chown":       true,
	"mkdir":       true,
	"rm":          true,
	"cp":          true,
	"mv":          true,
	"tar":         true,
	"gzip":        true,
	"lsblk":       true,
	"blkid":       true,
	"smartctl":    true,
	"mount":       true,
	"umount":      true,
	"journalctl":  true,
	"apt":         true,
	"sort":        true,
	"quota":       true,
	"repquota":    true,
	"setquota":    true,
	"quotaon":     true,
	"quotaoff":    true,
	// NOTE: "bash" and "sh" are intentionally excluded — shell injection bypass vector.
	// Use exec.Cmd pipe chaining instead of "bash -c" for pipeline operations.
}

type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

func Execute(command string, args ...string) (*Result, error) {
	return ExecuteWithTimeout(30*time.Second, command, args...)
}

func ExecuteWithTimeout(timeout time.Duration, command string, args ...string) (*Result, error) {
	if !allowedCommands[command] {
		return nil, fmt.Errorf("command not allowed: %s", command)
	}

	// Validate args — block shell metacharacters and null bytes.
	// exec.Command bypasses the shell so \n and \r are safe (no shell injection).
	// Null bytes are still blocked to prevent truncation attacks.
	for _, arg := range args {
		if strings.ContainsAny(arg, ";|&`$\x00") {
			return nil, fmt.Errorf("potentially unsafe argument: %q", arg)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := &Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}

	if exitErr, ok := err.(*exec.ExitError); ok {
		result.ExitCode = exitErr.ExitCode()
		return result, nil
	}

	return result, err
}

// Service wraps the shell execution functions as a struct-based service.
type Service struct{}

// New returns a new shell Service.
func New() *Service { return &Service{} }

// Run executes a simple command line (splits on whitespace).
func (s *Service) Run(cmdline string) (string, error) {
	parts := strings.Fields(cmdline)
	if len(parts) == 0 {
		return "", fmt.Errorf("empty command")
	}
	return ExecuteRaw(parts[0], parts[1:]...)
}

// ExecuteContext runs an allowed command with a caller-supplied context.
// The context controls deadline/cancellation; it does NOT bypass the allowlist.
func ExecuteContext(ctx context.Context, command string, args ...string) (string, error) {
	if !allowedCommands[command] {
		return "", fmt.Errorf("command not allowed: %s", command)
	}
	for _, arg := range args {
		if strings.ContainsAny(arg, ";|&`$\x00") {
			return "", fmt.Errorf("potentially unsafe argument: %q", arg)
		}
	}
	cmd := exec.CommandContext(ctx, command, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func ExecuteRaw(command string, args ...string) (string, error) {
	result, err := Execute(command, args...)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", fmt.Errorf("command failed (exit %d): %s", result.ExitCode, result.Stderr)
	}
	return strings.TrimSpace(result.Stdout), nil
}

// ExecuteRawTrusted runs a command without argument validation.
// Use ONLY when arguments are hardcoded or already validated by the caller
// (e.g., SQL queries passed to psql/mysql via -c flag).
// The command allowlist is still enforced.
func ExecuteRawTrusted(timeout time.Duration, command string, args ...string) (string, error) {
	if !allowedCommands[command] {
		return "", fmt.Errorf("command not allowed: %s", command)
	}
	// Only block null bytes — the rest is safe because exec.Command bypasses the shell.
	for _, arg := range args {
		if strings.Contains(arg, "\x00") {
			return "", fmt.Errorf("argument contains null byte")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("command failed (exit %d): %s", exitErr.ExitCode(), stderr.String())
		}
		return "", err
	}
	return strings.TrimSpace(stdout.String()), nil
}

