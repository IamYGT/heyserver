package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	maxDeployPlans       = 32
	maxDeployPlanBytes   = 64 << 10
	maxDeployArgs        = 32
	maxDeployOutputBytes = 1 << 20
	defaultDeployTimeout = 15 * time.Minute
	maxDeployTimeout     = 35 * time.Minute
)

type deployActionConfig struct {
	Command        string   `json:"command"`
	Args           []string `json:"args,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
}

type deployPlanConfig struct {
	ID          string                        `json:"id"`
	Name        string                        `json:"name"`
	Description string                        `json:"description,omitempty"`
	Kind        string                        `json:"kind,omitempty"`
	Path        string                        `json:"path"`
	HostPort    int                           `json:"host_port,omitempty"`
	Actions     map[string]deployActionConfig `json:"actions"`
}

type deployPlanDocument struct {
	Plans []deployPlanConfig `json:"plans"`
}

type managedDeployTarget struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Kind        string   `json:"kind"`
	Path        string   `json:"path"`
	Status      string   `json:"status"`
	Eligible    bool     `json:"eligible"`
	Reason      string   `json:"reason,omitempty"`
	Actions     []string `json:"actions"`
	HostPort    int      `json:"host_port,omitempty"`
}

type deployProcessRunner interface {
	run(context.Context, string, string, []string, int) ([]byte, bool, error)
}

type execDeployProcessRunner struct{}

func (execDeployProcessRunner) run(ctx context.Context, directory, command string, args []string, limit int) ([]byte, bool, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = directory
	capture := &boundedDeployOutput{limit: limit}
	cmd.Stdout, cmd.Stderr = capture, capture
	err := cmd.Run()
	return capture.Bytes(), capture.truncated, err
}

type boundedDeployOutput struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	limit     int
	truncated bool
}

func (w *boundedDeployOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	accepted := len(data)
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		_, _ = w.buffer.Write(data[:min(remaining, len(data))])
	}
	if len(data) > remaining {
		w.truncated = true
	}
	return accepted, nil
}

func (w *boundedDeployOutput) Bytes() []byte {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buffer.Bytes()...)
}

type deployController struct {
	runner     deployProcessRunner
	allowRead  bool
	allowRun   bool
	plansPath  string
	operations *sync.Mutex
}

func newDeployController(runner deployProcessRunner, allowRead, allowRun bool, plansPath string) deployController {
	return deployController{runner: runner, allowRead: allowRead, allowRun: allowRun, plansPath: plansPath, operations: &sync.Mutex{}}
}

func (c deployController) Inventory(_ context.Context) ([]managedDeployTarget, error) {
	if !c.allowRead {
		return nil, errors.New("deploy inventory is not enabled locally")
	}
	plans, err := c.loadPlans()
	if err != nil {
		return nil, err
	}
	targets := make([]managedDeployTarget, 0, len(plans))
	for _, plan := range plans {
		actions := orderedDeployActions(plan.Actions)
		reason := deployPlanUnavailableReason(plan)
		status := "ready"
		if reason != "" {
			status = "unavailable"
		}
		targets = append(targets, managedDeployTarget{ID: plan.ID, Name: plan.Name, Description: plan.Description, Kind: plan.Kind, Path: plan.Path, Status: status, Eligible: reason == "", Reason: reason, Actions: actions, HostPort: plan.HostPort})
	}
	return targets, nil
}

func (c deployController) Run(ctx context.Context, targetID, action string) (string, string, error) {
	if !c.allowRun {
		return "", "", errors.New("deploy actions are not enabled locally")
	}
	plans, err := c.loadPlans()
	if err != nil {
		return "", "", err
	}
	var selected *deployPlanConfig
	for index := range plans {
		if plans[index].ID == targetID {
			selected = &plans[index]
			break
		}
	}
	if selected == nil {
		return "", "", errors.New("deploy target is not configured locally")
	}
	actionConfig, ok := selected.Actions[action]
	if !ok {
		return "", "", errors.New("deploy action is not configured for this target")
	}
	if reason := deployPlanUnavailableReason(*selected); reason != "" {
		return "", "", errors.New(reason)
	}
	timeout := defaultDeployTimeout
	if actionConfig.TimeoutSeconds > 0 {
		timeout = time.Duration(actionConfig.TimeoutSeconds) * time.Second
	}
	actionCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c.operations.Lock()
	defer c.operations.Unlock()
	output, truncated, runErr := c.runner.run(actionCtx, selected.Path, actionConfig.Command, actionConfig.Args, maxDeployOutputBytes)
	if !utf8.Valid(output) {
		output = []byte(strings.ToValidUTF8(string(output), "�"))
	}
	text := strings.TrimSpace(string(output))
	if truncated {
		text += "\n[output truncated at 1 MiB]"
	}
	if runErr != nil {
		if errors.Is(actionCtx.Err(), context.DeadlineExceeded) {
			return "", text, fmt.Errorf("deploy action exceeded its %s local timeout", timeout)
		}
		return "", text, fmt.Errorf("deploy action failed: %w", runErr)
	}
	return fmt.Sprintf("%s %s completed", selected.Name, action), text, nil
}

func (c deployController) loadPlans() ([]deployPlanConfig, error) {
	file, err := os.Open(c.plansPath)
	if err != nil {
		return nil, fmt.Errorf("open deploy plans: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxDeployPlanBytes+1))
	if err != nil || len(data) > maxDeployPlanBytes {
		return nil, errors.New("deploy plan file is unreadable or exceeds 64 KiB")
	}
	var document deployPlanDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return nil, fmt.Errorf("decode deploy plans: %w", err)
	}
	if len(document.Plans) > maxDeployPlans {
		return nil, fmt.Errorf("deploy plans exceed the %d-plan limit", maxDeployPlans)
	}
	seen := make(map[string]struct{}, len(document.Plans))
	for index := range document.Plans {
		plan := &document.Plans[index]
		if plan.Kind == "" {
			plan.Kind = "application"
		}
		if !validDeployPlan(*plan) {
			return nil, fmt.Errorf("deploy plan %d is invalid", index+1)
		}
		if _, exists := seen[plan.ID]; exists {
			return nil, fmt.Errorf("duplicate deploy plan %q", plan.ID)
		}
		seen[plan.ID] = struct{}{}
	}
	sort.Slice(document.Plans, func(i, j int) bool { return document.Plans[i].Name < document.Plans[j].Name })
	return document.Plans, nil
}

func validDeployPlan(plan deployPlanConfig) bool {
	if !agentNginxConfigNamePattern.MatchString(plan.ID) || plan.Name == "" || len(plan.Name) > 128 || strings.ContainsAny(plan.Name+plan.Description, "\r\n\x00") || len(plan.Description) > 512 || !filepath.IsAbs(plan.Path) || filepath.Clean(plan.Path) != plan.Path || plan.Path == "/" || len(plan.Actions) == 0 || len(plan.Actions) > 4 || plan.HostPort < 0 || plan.HostPort > 65535 {
		return false
	}
	switch plan.Kind {
	case "application", "compose", "service", "custom":
	default:
		return false
	}
	for action, config := range plan.Actions {
		if !validAgentDeployAction(action) || !filepath.IsAbs(config.Command) || filepath.Clean(config.Command) != config.Command || len(config.Args) > maxDeployArgs || config.TimeoutSeconds < 0 || time.Duration(config.TimeoutSeconds)*time.Second > maxDeployTimeout {
			return false
		}
		for _, argument := range config.Args {
			if len(argument) > 4096 || strings.ContainsRune(argument, '\x00') {
				return false
			}
		}
	}
	return true
}

func validAgentDeployAction(action string) bool {
	return action == "preflight" || action == "deploy" || action == "restart" || action == "rollback"
}

func orderedDeployActions(actions map[string]deployActionConfig) []string {
	result := make([]string, 0, len(actions))
	for _, action := range []string{"preflight", "deploy", "restart", "rollback"} {
		if _, exists := actions[action]; exists {
			result = append(result, action)
		}
	}
	return result
}

func deployPlanUnavailableReason(plan deployPlanConfig) string {
	info, err := os.Stat(plan.Path)
	if err != nil || !info.IsDir() {
		return "Configured working directory is unavailable"
	}
	for _, action := range orderedDeployActions(plan.Actions) {
		info, err := os.Stat(plan.Actions[action].Command)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			return fmt.Sprintf("Configured %s executable is unavailable", action)
		}
	}
	return ""
}
