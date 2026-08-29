package security

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

var (
	// ErrFail2BanUnavailable identifies a host where fail2ban mutations cannot
	// run because the client or daemon is unavailable.
	ErrFail2BanUnavailable = errors.New("fail2ban is unavailable")
	// ErrInvalidFail2BanInput identifies a rejected jail or IP argument.
	ErrInvalidFail2BanInput = errors.New("invalid fail2ban input")
)

// Fail2BanReadinessState separates an optional missing installation, a stopped
// daemon, and an indeterminate runtime from a healthy mutation boundary.
type Fail2BanReadinessState string

const (
	Fail2BanStateHealthy      Fail2BanReadinessState = "healthy"
	Fail2BanStateNotInstalled Fail2BanReadinessState = "not-installed"
	Fail2BanStateStopped      Fail2BanReadinessState = "stopped"
	Fail2BanStateUnavailable  Fail2BanReadinessState = "unavailable"
)

// Jail represents a fail2ban jail.
type Jail struct {
	Name            string   `json:"name"`
	Filter          string   `json:"filter"`
	Actions         string   `json:"actions"`
	LogPath         string   `json:"logPath"`
	CurrentlyFailed int      `json:"currentlyFailed"`
	Currently       int      `json:"currentlyBanned"`
	Total           int      `json:"totalBanned"`
	BannedIPs       []string `json:"bannedIPs"`
}

// Fail2BanStatus is the top-level status.
type Fail2BanStatus struct {
	Available      bool                   `json:"available"`
	Installed      bool                   `json:"installed"`
	Running        bool                   `json:"running"`
	State          Fail2BanReadinessState `json:"state"`
	DaemonState    string                 `json:"daemonState"`
	Error          string                 `json:"error,omitempty"`
	AvailableJails []string               `json:"availableJails"`
	Jails          []Jail                 `json:"jails"`
}

// Fail2BanService wraps fail2ban-client commands.
type Fail2BanService struct {
	lookPath func(string) (string, error)
	run      func(string, ...string) (string, error)
}

func NewFail2BanService() *Fail2BanService {
	return &Fail2BanService{lookPath: exec.LookPath, run: runF2BCmd}
}

func (s *Fail2BanService) IsAvailable() bool {
	_, err := s.lookPath("fail2ban-client")
	return err == nil
}

func (s *Fail2BanService) IsRunning() bool {
	out, err := s.run("systemctl", "is-active", "fail2ban")
	if err != nil {
		return false
	}
	return strings.TrimSpace(out) == "active"
}

// Readiness reports client and daemon state without querying jail inventory.
func (s *Fail2BanService) Readiness() *Fail2BanStatus {
	status := &Fail2BanStatus{
		State:          Fail2BanStateNotInstalled,
		DaemonState:    "unknown",
		AvailableJails: []string{},
		Jails:          []Jail{},
	}
	if !s.IsAvailable() {
		status.Error = "fail2ban-client is not installed"
		return status
	}
	status.Installed = true
	status.State = Fail2BanStateUnavailable
	if _, err := s.lookPath("systemctl"); err != nil {
		status.Error = "systemctl is unavailable; fail2ban daemon state cannot be verified"
		return status
	}

	daemonOutput, daemonErr := s.run("systemctl", "is-active", "fail2ban")
	daemonState := strings.TrimSpace(daemonOutput)
	if daemonState == "" {
		daemonState = "unknown"
	}
	status.DaemonState = daemonState
	if daemonErr != nil || daemonState != "active" {
		switch daemonState {
		case "inactive", "failed", "activating", "deactivating":
			status.State = Fail2BanStateStopped
			status.Error = "fail2ban service is " + daemonState
		default:
			status.Error = "fail2ban daemon state could not be verified"
			if daemonErr != nil {
				status.Error += ": " + daemonErr.Error()
			}
		}
		return status
	}

	status.Running = true
	status.State = Fail2BanStateHealthy
	return status
}

func (s *Fail2BanService) Status() (*Fail2BanStatus, error) {
	status := s.Readiness()
	if !status.Running {
		return status, nil
	}
	jailNames, err := s.listJails()
	if err != nil {
		status.State = Fail2BanStateUnavailable
		status.Error = err.Error()
		return status, nil
	}
	status.AvailableJails = jailNames
	for _, name := range jailNames {
		jail, err := s.JailDetail(name)
		if err != nil {
			status.State = Fail2BanStateUnavailable
			status.Error = err.Error()
			status.Jails = []Jail{}
			return status, nil
		}
		status.Jails = append(status.Jails, *jail)
	}
	status.Available = true
	status.State = Fail2BanStateHealthy
	return status, nil
}

func (s *Fail2BanService) JailDetail(jail string) (*Jail, error) {
	if err := validateJailName(jail); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidFail2BanInput, err)
	}
	out, err := s.run("fail2ban-client", "status", jail)
	if err != nil {
		return nil, fmt.Errorf("fail2ban status %s: %w", jail, err)
	}
	return parseJailStatus(jail, out), nil
}

func (s *Fail2BanService) BanIP(jail, ip string) error {
	if err := validateJailName(jail); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidFail2BanInput, err)
	}
	if err := validateIPArg(ip); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidFail2BanInput, err)
	}
	if err := s.requireReady(); err != nil {
		return err
	}
	_, err := s.run("fail2ban-client", "set", jail, "banip", strings.TrimSpace(ip))
	return err
}

func (s *Fail2BanService) UnbanIP(jail, ip string) error {
	if err := validateJailName(jail); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidFail2BanInput, err)
	}
	if err := validateIPArg(ip); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidFail2BanInput, err)
	}
	if err := s.requireReady(); err != nil {
		return err
	}
	_, err := s.run("fail2ban-client", "set", jail, "unbanip", strings.TrimSpace(ip))
	return err
}

func (s *Fail2BanService) requireReady() error {
	status, err := s.Status()
	if err != nil {
		return err
	}
	if !status.Available {
		return fmt.Errorf("%w: %s", ErrFail2BanUnavailable, status.Error)
	}
	return nil
}

func (s *Fail2BanService) LogEntries(n int) ([]string, error) {
	if n <= 0 {
		n = 100
	}
	out, err := s.run("tail", "-n", fmt.Sprintf("%d", n), "/var/log/fail2ban.log")
	if err != nil {
		out, err = s.run("journalctl", "-u", "fail2ban", "-n", fmt.Sprintf("%d", n), "--no-pager")
		if err != nil {
			return nil, fmt.Errorf("fail2ban log: %w", err)
		}
	}
	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, nil
}

func (s *Fail2BanService) listJails() ([]string, error) {
	out, err := s.run("fail2ban-client", "status")
	if err != nil {
		return nil, fmt.Errorf("fail2ban status: %w", err)
	}
	return parseJailList(out), nil
}

func runF2BCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func parseJailList(output string) []string {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "Jail list:") {
			parts := strings.SplitN(line, ":", 2)
			if len(parts) < 2 {
				return nil
			}
			raw := strings.TrimSpace(parts[1])
			if raw == "" {
				return nil
			}
			var result []string
			for _, j := range strings.Split(raw, ",") {
				if name := strings.TrimSpace(j); name != "" {
					result = append(result, name)
				}
			}
			return result
		}
	}
	return nil
}

func parseJailStatus(name, output string) *Jail {
	j := &Jail{Name: name, BannedIPs: []string{}}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		// Strip leading pipe/dash characters and spaces from fail2ban output formatting.
		// e.g. "|  |- Currently banned:	2" -> "Currently banned:	2"
		line := strings.TrimSpace(scanner.Text())
		line = strings.TrimLeft(line, "|`- \t")
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "Currently failed:"):
			_, _ = fmt.Sscanf(afterColon(line), "%d", &j.CurrentlyFailed)
		case strings.HasPrefix(line, "Currently banned:"):
			_, _ = fmt.Sscanf(afterColon(line), "%d", &j.Currently)
		case strings.HasPrefix(line, "Total banned:"):
			_, _ = fmt.Sscanf(afterColon(line), "%d", &j.Total)
		case strings.HasPrefix(line, "Banned IP list:"):
			raw := strings.TrimSpace(afterColon(line))
			if raw != "" {
				j.BannedIPs = append(j.BannedIPs, strings.Fields(raw)...)
			}
		case strings.HasPrefix(line, "File list:"):
			j.LogPath = strings.TrimSpace(afterColon(line))
		case strings.HasPrefix(line, "Filter:") && j.Filter == "":
			j.Filter = strings.TrimSpace(afterColon(line))
		case strings.HasPrefix(line, "Actions:") && j.Actions == "":
			j.Actions = strings.TrimSpace(afterColon(line))
		}
	}
	return j
}

func afterColon(s string) string {
	idx := strings.Index(s, ":")
	if idx < 0 {
		return ""
	}
	return s[idx+1:]
}

func validateIPArg(ip string) error {
	clean := strings.TrimSpace(ip)
	if clean == "" || strings.ContainsAny(clean, " \t\n\r;|&\x60$") {
		return fmt.Errorf("invalid IP address: %q", ip)
	}
	return nil
}

// validateJailName ensures a fail2ban jail name contains only safe characters.
// Jail names are lowercase alphanumeric strings with optional hyphens/underscores.
func validateJailName(name string) error {
	if name == "" {
		return fmt.Errorf("jail name must not be empty")
	}
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return fmt.Errorf("invalid jail name %q: contains disallowed character %q", name, c)
		}
	}
	if len(name) > 64 {
		return fmt.Errorf("jail name too long (max 64)")
	}
	return nil
}
