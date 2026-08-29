package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
)

const maxPM2Processes = 512

var agentPM2NamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:@-]{0,127}$`)

type pm2Process struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	PID      int     `json:"pid"`
	CPU      float64 `json:"cpu"`
	Memory   int64   `json:"memory"`
	Uptime   int64   `json:"uptime"`
	Restarts int     `json:"restarts"`
	Mode     string  `json:"mode"`
	CWD      string  `json:"cwd"`
	Script   string  `json:"script"`
	Version  string  `json:"version"`
}

type pm2RawProcess struct {
	ID    int    `json:"pm_id"`
	Name  string `json:"name"`
	PID   int    `json:"pid"`
	Monit struct {
		CPU    float64 `json:"cpu"`
		Memory int64   `json:"memory"`
	} `json:"monit"`
	Environment struct {
		Status   string `json:"status"`
		Uptime   int64  `json:"pm_uptime"`
		Restarts int    `json:"restart_time"`
		Mode     string `json:"exec_mode"`
		CWD      string `json:"pm_cwd"`
		Script   string `json:"pm_exec_path"`
		Version  string `json:"version"`
	} `json:"pm2_env"`
}

type pm2Controller struct {
	runner         commandRunner
	allowRead      bool
	allowedActions map[string]struct{}
	binary         string
	home           string
	user           string
	mu             *sync.Mutex
}

func newPM2Controller(runner commandRunner, allowRead bool, allowedActions map[string]struct{}, binary, home, user string) pm2Controller {
	return pm2Controller{runner: runner, allowRead: allowRead, allowedActions: allowedActions, binary: binary, home: home, user: user, mu: &sync.Mutex{}}
}

func (c pm2Controller) List(ctx context.Context) ([]pm2Process, error) {
	if !c.allowRead {
		return nil, errors.New("PM2 inventory is not enabled locally")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	output, err := c.runLocked(ctx, 30*time.Second, "jlist")
	if err != nil {
		return nil, fmt.Errorf("PM2 inventory failed: %w", err)
	}
	var raw []pm2RawProcess
	if err := json.Unmarshal(output, &raw); err != nil {
		return nil, fmt.Errorf("parse PM2 inventory: %w", err)
	}
	if len(raw) > maxPM2Processes {
		return nil, fmt.Errorf("PM2 inventory exceeds %d processes", maxPM2Processes)
	}
	processes := make([]pm2Process, 0, len(raw))
	for _, item := range raw {
		processes = append(processes, pm2Process{
			ID: item.ID, Name: truncateUTF8(item.Name, 128), Status: truncateUTF8(item.Environment.Status, 64), PID: item.PID,
			CPU: item.Monit.CPU, Memory: item.Monit.Memory, Uptime: item.Environment.Uptime, Restarts: item.Environment.Restarts,
			Mode: truncateUTF8(item.Environment.Mode, 128), CWD: truncateUTF8(item.Environment.CWD, 4096),
			Script: truncateUTF8(item.Environment.Script, 4096), Version: truncateUTF8(item.Environment.Version, 128),
		})
	}
	return processes, nil
}

// Probe performs the bounded, read-only PM2 observation used by the managed
// integration-status task. It validates only that jlist returned a JSON
// array; process inventory never leaves this method.
func (c pm2Controller) Probe(ctx context.Context) (integrationstate.State, error) {
	if !c.allowRead || c.binary == "" || c.home == "" || c.user == "" {
		return integrationstate.NotConfigured, errors.New("PM2 integration is not configured locally")
	}
	info, err := os.Stat(c.binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return integrationstate.NotConfigured, errors.New("configured PM2 binary is unavailable")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	output, err := c.runLocked(ctx, 5*time.Second, "jlist")
	if err != nil {
		return integrationstate.Unavailable, errors.New("PM2 status probe failed")
	}
	if len(output) > maxCommandOutputBytes {
		return integrationstate.Unavailable, errors.New("PM2 status probe output exceeded the limit")
	}
	var raw []json.RawMessage
	trimmed := strings.TrimSpace(string(output))
	if len(trimmed) == 0 || trimmed[0] != '[' || json.Unmarshal(output, &raw) != nil {
		return integrationstate.Unavailable, errors.New("PM2 status probe returned invalid JSON")
	}
	return integrationstate.Healthy, nil
}

func (c pm2Controller) Logs(ctx context.Context, name string, lines int) (string, error) {
	if !c.allowRead {
		return "", errors.New("PM2 log reading is not enabled locally")
	}
	if !agentPM2NamePattern.MatchString(name) || lines < 1 || lines > 500 {
		return "", errors.New("invalid PM2 log request")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.runLocked(ctx, 20*time.Second, "describe", name); err != nil {
		return "", fmt.Errorf("PM2 process lookup failed: %w", err)
	}
	output, err := c.runLocked(ctx, 30*time.Second, "logs", name, "--nostream", "--raw", "--lines", strconv.Itoa(lines))
	if err != nil {
		return "", fmt.Errorf("PM2 log read failed: %w", err)
	}
	return string(output), nil
}

func (c pm2Controller) Action(ctx context.Context, name, action string) (string, error) {
	if !agentPM2NamePattern.MatchString(name) {
		return "", errors.New("invalid PM2 process name")
	}
	if _, allowed := c.allowedActions[action]; !allowed {
		return "", errors.New("PM2 action is not in the local allowlist")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.runLocked(ctx, 20*time.Second, "describe", name); err != nil {
		return "", fmt.Errorf("PM2 process lookup failed: %w", err)
	}
	if _, err := c.runLocked(ctx, 90*time.Second, action, name, "--update-env"); err != nil {
		return "", fmt.Errorf("PM2 %s failed: %w", action, err)
	}
	if _, err := c.runLocked(ctx, 30*time.Second, "save", "--force"); err != nil {
		return "", fmt.Errorf("PM2 action completed but process-list save failed: %w", err)
	}
	return "PM2 process " + action + " completed and process list saved", nil
}

func (c pm2Controller) runLocked(ctx context.Context, timeout time.Duration, args ...string) ([]byte, error) {
	info, err := os.Stat(c.binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return nil, errors.New("configured PM2 binary is unavailable")
	}
	commandCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	environment := "PM2_HOME=" + c.home
	if c.user == "root" {
		return c.runner.run(commandCtx, "env", append([]string{environment, c.binary}, args...)...)
	}
	return c.runner.run(commandCtx, "runuser", append([]string{"-u", c.user, "--", "env", environment, c.binary}, args...)...)
}
