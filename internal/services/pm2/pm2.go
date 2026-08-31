// Package pm2 provides PM2 process manager integration.
package pm2

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/models"
)

var (
	ErrNotConfigured     = errors.New("PM2 integration not configured")
	ErrInvalidDeploy     = errors.New("invalid PM2 deploy request")
	validUserName        = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	validBinaryName      = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	validApplicationName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:@-]{0,127}$`)
)

// Config defines which unprivileged Linux account owns the PM2 daemon.
// User is required; Heyserver never silently creates or controls a root PM2
// daemon. Bin may be an absolute NVM path or a command available in PATH.
type Config struct {
	User         string
	Home         string
	Bin          string
	AllowedRoots []string
}

type Service struct {
	config Config
}

// SystemdServiceName returns the provider-neutral PM2 unit created by
// `pm2 startup` for a validated, unprivileged Linux owner.
func SystemdServiceName(user string) (string, bool) {
	user = strings.TrimSpace(user)
	if user == "" || user == "root" || !validUserName.MatchString(user) {
		return "", false
	}
	return "pm2-" + user, true
}

func New(config Config) (*Service, error) {
	config.User = strings.TrimSpace(config.User)
	config.Home = strings.TrimSpace(config.Home)
	config.Bin = strings.TrimSpace(config.Bin)
	if config.Bin == "" {
		config.Bin = "pm2"
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	allowedRoots, err := normalizeAllowedRoots(config.AllowedRoots)
	if err != nil {
		return nil, err
	}
	config.AllowedRoots = allowedRoots
	return &Service{config: config}, nil
}

func (c Config) validate() error {
	if c.User == "" {
		return fmt.Errorf("%w: set HSERVER_PM2_USER", ErrNotConfigured)
	}
	if c.User == "root" {
		return fmt.Errorf("%w: HSERVER_PM2_USER must be an unprivileged account", ErrNotConfigured)
	}
	if _, ok := SystemdServiceName(c.User); !ok {
		return fmt.Errorf("%w: HSERVER_PM2_USER is invalid", ErrNotConfigured)
	}
	if c.Home != "" && !filepath.IsAbs(c.Home) {
		return fmt.Errorf("%w: HSERVER_PM2_HOME must be an absolute path", ErrNotConfigured)
	}
	if strings.Contains(c.Bin, "/") {
		if !filepath.IsAbs(c.Bin) {
			return fmt.Errorf("%w: HSERVER_PM2_BIN must be an absolute path or command name", ErrNotConfigured)
		}
	} else if !validBinaryName.MatchString(c.Bin) {
		return fmt.Errorf("%w: HSERVER_PM2_BIN is invalid", ErrNotConfigured)
	}
	return nil
}

func (s *Service) commandArgs(args ...string) []string {
	return s.commandArgsWithNodeEnv("", args...)
}

func (s *Service) commandArgsWithNodeEnv(nodeEnv string, args ...string) []string {
	command := []string{"-H", "-u", s.config.User, "--", "env"}
	if s.config.Home != "" {
		command = append(command, "PM2_HOME="+s.config.Home)
	}
	if nodeEnv != "" {
		command = append(command, "NODE_ENV="+nodeEnv)
	}
	command = append(command, s.config.Bin)
	return append(command, args...)
}

// run executes a PM2 command as the configured unprivileged Linux user.
func (s *Service) run(timeout time.Duration, args ...string) (string, error) {
	return s.runWithContext(context.Background(), timeout, "", args...)
}

func (s *Service) runWithNodeEnv(timeout time.Duration, nodeEnv string, args ...string) (string, error) {
	return s.runWithContext(context.Background(), timeout, nodeEnv, args...)
}

// runWithContext executes a PM2 command with both the caller's cancellation
// and the service operation deadline.  Status aggregation uses this boundary
// so a timed-out read cannot continue as an orphaned subprocess.
func (s *Service) runWithContext(parent context.Context, timeout time.Duration, nodeEnv string, args ...string) (string, error) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "sudo", s.commandArgsWithNodeEnv(nodeEnv, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s: %w", strings.Join(args, " "), err)
	}
	return string(out), nil
}

// pm2RawProcess mirrors the JSON structure produced by `pm2 jlist`.
// Only the fields we need are mapped; unknown fields are silently ignored.
type pm2RawProcess struct {
	PMID   int    `json:"pm_id"`
	Name   string `json:"name"`
	PID    int    `json:"pid"`
	PM2Env struct {
		Status      string `json:"status"`
		ExecMode    string `json:"exec_mode"`
		RestartTime int    `json:"restart_time"`
		PMUptime    int64  `json:"pm_uptime"`
		CreatedAt   int64  `json:"created_at"`
		Script      string `json:"pm_exec_path"`
		OutLogPath  string `json:"pm_out_log_path"`
		ErrLogPath  string `json:"pm_err_log_path"`
	} `json:"pm2_env"`
	Monit struct {
		CPU    float64 `json:"cpu"`
		Memory int64   `json:"memory"`
	} `json:"monit"`
}

// DeployRequest holds parameters for deploying a new PM2 application.
type DeployRequest struct {
	Name      string `json:"name"`
	Script    string `json:"script"`
	Cwd       string `json:"cwd"`
	Instances int    `json:"instances"`
	ExecMode  string `json:"exec_mode"`          // "fork" or "cluster"
	NodeEnv   string `json:"node_env,omitempty"` // "production" or "development"
}

// ProcessInventory is the read-only PM2 provider observation returned by the
// API. State describes PM2 availability; individual process Status values
// remain runtime states such as "online", "stopped", and "errored".
type ProcessInventory struct {
	Processes []models.PM2Process    `json:"processes"`
	State     integrationstate.State `json:"state"`
}

// List returns all PM2 managed processes by running `pm2 jlist`.
func (s *Service) List() ([]models.PM2Process, error) {
	return s.ListContext(context.Background())
}

// ListContext returns all PM2 managed processes while honoring ctx.  The
// command remains the same read-only `pm2 jlist` inventory used by List.
func (s *Service) ListContext(ctx context.Context) ([]models.PM2Process, error) {
	if s == nil {
		return nil, ErrNotConfigured
	}
	if s.binaryMissing() {
		return nil, fmt.Errorf("%w: PM2 executable %q was not found", ErrNotConfigured, s.config.Bin)
	}

	out, err := s.runWithContext(ctx, 30*time.Second, "", "jlist")
	if err != nil {
		if isMissingBinaryError(err) || isMissingBinaryMessage(out) {
			return nil, fmt.Errorf("%w: PM2 executable %q was not found", ErrNotConfigured, s.config.Bin)
		}
		return nil, fmt.Errorf("pm2 jlist: %w", err)
	}
	if strings.TrimSpace(out) != "" && !strings.Contains(out, "[") {
		return nil, fmt.Errorf("pm2 jlist returned no JSON process inventory")
	}

	raw, err := parseJList(out)
	if err != nil {
		return nil, err
	}

	procs := make([]models.PM2Process, 0, len(raw))
	for _, r := range raw {
		procs = append(procs, toModel(r))
	}
	return procs, nil
}

// ProbeProcesses runs the PM2 inventory command and classifies the provider
// observation using the shared optional-integration state contract. A
// successful empty list is healthy; runtime process states never affect the
// provider availability state.
func (s *Service) ProbeProcesses() (ProcessInventory, error) {
	return s.ProbeProcessesContext(context.Background())
}

// ProbeProcessesContext performs one fresh read-only PM2 inventory observation
// and honors the caller's cancellation/deadline.
func (s *Service) ProbeProcessesContext(ctx context.Context) (ProcessInventory, error) {
	inventory := ProcessInventory{
		Processes: []models.PM2Process{},
		State:     integrationstate.Unavailable,
	}
	if s == nil {
		inventory.State = integrationstate.NotConfigured
		return inventory, ErrNotConfigured
	}

	processes, err := s.ListContext(ctx)
	if err != nil {
		inventory.State = ClassifyInventoryError(err)
		return inventory, err
	}
	if processes == nil {
		processes = []models.PM2Process{}
	}
	inventory.Processes = processes
	inventory.State = integrationstate.Healthy
	return inventory, nil
}

// ClassifyInventoryError maps a PM2 inventory observation to the shared
// optional-integration state contract. A missing owner or executable means
// PM2 is not configured; a configured command that cannot return inventory is
// unavailable. A nil error represents a successful probe, including an empty
// process list.
func ClassifyInventoryError(err error) integrationstate.State {
	if err == nil {
		return integrationstate.Healthy
	}
	if errors.Is(err, ErrNotConfigured) || isMissingBinaryError(err) {
		return integrationstate.NotConfigured
	}
	return integrationstate.Unavailable
}

// binaryMissing performs only executable discovery. PM2_HOME and the daemon
// are intentionally not treated as configuration evidence: `jlist` remains
// the authoritative probe for a configured PM2 context.
func (s *Service) binaryMissing() bool {
	if s == nil {
		return true
	}
	bin := strings.TrimSpace(s.config.Bin)
	if bin == "" {
		return true
	}
	if filepath.IsAbs(bin) {
		_, err := os.Stat(bin)
		return errors.Is(err, os.ErrNotExist)
	}
	// A command name is resolved by the configured unprivileged user's
	// environment when `sudo ... env pm2` runs. Resolving it in Heyserver's PATH
	// would falsely report not_configured for NVM/user-local installations.
	return false
}

func isMissingBinaryError(err error) bool {
	if err == nil {
		return false
	}
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return execErr.Name != "sudo"
	}
	return isMissingBinaryMessage(err.Error())
}

func isMissingBinaryMessage(raw string) bool {
	message := strings.ToLower(raw)
	return strings.Contains(message, "executable file not found") ||
		strings.Contains(message, "command not found") ||
		strings.Contains(message, "not found in $path") ||
		strings.Contains(message, "pm2: no such file or directory") ||
		strings.Contains(message, "pm2: not found")
}

// Get returns a single PM2 process by id (numeric pm_id as string) or name.
func (s *Service) Get(id string) (*models.PM2Process, error) {
	procs, err := s.List()
	if err != nil {
		return nil, err
	}

	// Try numeric pm_id match first
	if numID, err := strconv.Atoi(id); err == nil {
		for i := range procs {
			if procs[i].ID == numID {
				return &procs[i], nil
			}
		}
	}

	// Fall back to name match
	for i := range procs {
		if procs[i].Name == id {
			return &procs[i], nil
		}
	}

	return nil, fmt.Errorf("pm2 process %q not found", id)
}

// Control sends start/stop/restart/reload/delete to a specific process by id or name.
// Returns the combined stdout+stderr output from pm2.
func (s *Service) Control(id string, action string) (string, error) {
	if !IsControlAction(action) {
		return "", fmt.Errorf("unsupported action: %s", action)
	}

	if strings.ContainsAny(id, ";|&`$()\n\r\x00") {
		return "", fmt.Errorf("invalid process id: %s", id)
	}

	result, err := s.run(30*time.Second, action, id)
	if err != nil {
		return "", fmt.Errorf("pm2 %s %s: %w", action, id, err)
	}

	combined := strings.TrimSpace(result)
	return combined, nil
}

// IsControlAction reports whether action belongs to the fixed local PM2
// process-control vocabulary.
func IsControlAction(action string) bool {
	switch action {
	case "start", "stop", "restart", "reload", "delete":
		return true
	default:
		return false
	}
}

// Logs returns up to `lines` recent log lines for the given process id or name.
// It merges stdout and stderr from `pm2 logs ID --lines N --nostream`.
func (s *Service) Logs(id string, lines int) ([]string, error) {
	if lines <= 0 {
		lines = 100
	}
	if strings.ContainsAny(id, ";|&`$()\n\r\x00") {
		return nil, fmt.Errorf("invalid process id: %s", id)
	}

	linesStr := strconv.Itoa(lines)
	result, err := s.run(15*time.Second, "logs", id, "--lines", linesStr, "--nostream")
	if err != nil {
		return nil, fmt.Errorf("pm2 logs %s: %w", id, err)
	}

	// pm2 logs writes to stderr even for normal output; merge both
	combined := result
	rawLines := strings.Split(strings.TrimRight(combined, "\n"), "\n")

	out := make([]string, 0, len(rawLines))
	for _, l := range rawLines {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out, nil
}

// Save persists the current process list so it survives reboots (`pm2 save`).
// Returns the output from pm2.
func (s *Service) Save() (string, error) {
	result, err := s.run(15*time.Second, "save")
	if err != nil {
		return "", fmt.Errorf("pm2 save: %w", err)
	}
	combined := strings.TrimSpace(result)
	return combined, nil
}

// Deploy starts a new application via `pm2 start` with inline options.
// Returns the output from pm2.
func (s *Service) Deploy(req *DeployRequest) (string, error) {
	if req == nil {
		return "", fmt.Errorf("%w: request is required", ErrInvalidDeploy)
	}
	if len(s.config.AllowedRoots) == 0 {
		return "", fmt.Errorf("%w: set HSERVER_PM2_ALLOWED_ROOTS", ErrNotConfigured)
	}
	normalized, err := normalizeDeployRequest(*req, s.config.AllowedRoots)
	if err != nil {
		return "", err
	}
	req = &normalized

	args := []string{
		"start", req.Script,
		"--name", req.Name,
	}

	if req.Cwd != "" {
		args = append(args, "--cwd", req.Cwd)
	}

	args = append(args, "--exec-mode", req.ExecMode)
	args = append(args, "--instances", strconv.Itoa(req.Instances))

	result, err := s.runWithNodeEnv(60*time.Second, req.NodeEnv, args...)
	if err != nil {
		return "", fmt.Errorf("pm2 start: %w", err)
	}
	combined := strings.TrimSpace(result)
	return combined, nil
}

func normalizeDeployRequest(req DeployRequest, allowedRoots []string) (DeployRequest, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Script = strings.TrimSpace(req.Script)
	req.Cwd = strings.TrimSpace(req.Cwd)
	req.ExecMode = strings.TrimSpace(req.ExecMode)
	req.NodeEnv = strings.TrimSpace(req.NodeEnv)
	if req.Name == "" || req.Script == "" {
		return DeployRequest{}, fmt.Errorf("%w: name and script are required", ErrInvalidDeploy)
	}
	if !validApplicationName.MatchString(req.Name) {
		return DeployRequest{}, fmt.Errorf("%w: invalid app name: %s", ErrInvalidDeploy, req.Name)
	}
	if strings.ContainsAny(req.Script, ";|&`$()\n\r\x00") {
		return DeployRequest{}, fmt.Errorf("%w: invalid script path: %s", ErrInvalidDeploy, req.Script)
	}
	scriptClean := filepath.Clean(req.Script)
	if !pathWithinAllowedRoots(scriptClean, allowedRoots) {
		return DeployRequest{}, fmt.Errorf("%w: script path not in allowed directory: %s", ErrInvalidDeploy, req.Script)
	}
	req.Script = scriptClean
	if req.Cwd != "" {
		cwdClean := filepath.Clean(req.Cwd)
		if !pathWithinAllowedRoots(cwdClean, allowedRoots) {
			return DeployRequest{}, fmt.Errorf("%w: cwd not in allowed directory: %s", ErrInvalidDeploy, req.Cwd)
		}
		req.Cwd = cwdClean
	}
	if req.ExecMode == "" {
		req.ExecMode = "fork"
	}
	if req.ExecMode != "fork" && req.ExecMode != "cluster" {
		return DeployRequest{}, fmt.Errorf("%w: exec_mode must be fork or cluster", ErrInvalidDeploy)
	}
	if req.Instances == 0 {
		req.Instances = 1
	}
	if req.Instances < 1 || req.Instances > 64 {
		return DeployRequest{}, fmt.Errorf("%w: instances must be between 1 and 64", ErrInvalidDeploy)
	}
	if req.NodeEnv != "" && req.NodeEnv != "production" && req.NodeEnv != "development" {
		return DeployRequest{}, fmt.Errorf("%w: node_env must be production or development", ErrInvalidDeploy)
	}
	return req, nil
}

// parseJList parses the raw JSON output of `pm2 jlist`.
// pm2 may prepend log/debug lines before the JSON array; we extract by finding
// the first '[' character.
func parseJList(raw string) ([]pm2RawProcess, error) {
	if !strings.Contains(raw, "[") {
		// pm2 returned empty or error text — treat as empty process list
		return nil, nil
	}
	var lastErr error
	for index := 0; index < len(raw); index++ {
		if raw[index] != '[' {
			continue
		}
		jsonPart, err := firstJSONArray(raw[index:])
		if err != nil {
			lastErr = err
			continue
		}
		var processes []pm2RawProcess
		if err := json.Unmarshal([]byte(jsonPart), &processes); err != nil {
			lastErr = err
			continue
		}
		return processes, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no JSON process array found")
	}
	return nil, fmt.Errorf("parsing pm2 jlist JSON: %w", lastErr)
}

// firstJSONArray isolates the first balanced JSON array from PM2's combined
// stdout/stderr stream. PM2 sometimes writes `[]module notice` without even a
// separating newline, so a regular JSON decoder still rejects the stream.
func firstJSONArray(raw string) (string, error) {
	depth := 0
	inString := false
	escaped := false
	for index, char := range raw {
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if char == '"' {
				inString = false
			}
			continue
		}
		switch char {
		case '"':
			inString = true
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return raw[:index+1], nil
			}
			if depth < 0 {
				return "", fmt.Errorf("unexpected closing array bracket")
			}
		}
	}
	return "", fmt.Errorf("truncated JSON array")
}

// toModel converts a pm2RawProcess to models.PM2Process.
func toModel(r pm2RawProcess) models.PM2Process {
	var uptime int64
	if r.PM2Env.PMUptime > 0 {
		// pm_uptime is Unix timestamp in milliseconds when the process started
		startedAt := time.UnixMilli(r.PM2Env.PMUptime)
		uptime = int64(time.Since(startedAt).Seconds())
	}

	// memory is in bytes; convert to MB
	memoryMB := float64(r.Monit.Memory) / 1024 / 1024

	return models.PM2Process{
		ID:       r.PMID,
		Name:     r.Name,
		Status:   r.PM2Env.Status,
		CPU:      r.Monit.CPU,
		Memory:   memoryMB,
		Uptime:   uptime,
		Restarts: r.PM2Env.RestartTime,
		Mode:     r.PM2Env.ExecMode,
		PID:      r.PID,
	}
}

func normalizeAllowedRoots(roots []string) ([]string, error) {
	seen := make(map[string]struct{}, len(roots))
	cleaned := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		if !filepath.IsAbs(root) {
			return nil, fmt.Errorf("%w: HSERVER_PM2_ALLOWED_ROOTS entries must be absolute paths", ErrNotConfigured)
		}
		root = filepath.Clean(root)
		if root == "/" {
			return nil, fmt.Errorf("%w: HSERVER_PM2_ALLOWED_ROOTS cannot include filesystem root", ErrNotConfigured)
		}
		if _, exists := seen[root]; exists {
			continue
		}
		seen[root] = struct{}{}
		cleaned = append(cleaned, root)
	}
	return cleaned, nil
}

func pathWithinAllowedRoots(path string, roots []string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	for _, root := range roots {
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
