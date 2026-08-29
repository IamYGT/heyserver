package firewall

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/integrationstate"
	"github.com/IamYGT/heyserver/internal/services/shell"
)

// Status holds the complete UFW firewall state.
type Status struct {
	Available       bool           `json:"available"`
	State           ReadinessState `json:"state"`
	Backend         string         `json:"backend"`
	Error           string         `json:"error,omitempty"`
	Active          bool           `json:"active"`
	DefaultIncoming string         `json:"defaultIncoming"`
	DefaultOutgoing string         `json:"defaultOutgoing"`
	DefaultRouted   string         `json:"defaultRouted"`
	LoggingLevel    string         `json:"loggingLevel"`
	Rules           []Rule         `json:"rules"`
}

// ReadinessState separates observable firewall rules from UFW management
// readiness. iptables fallback is deliberately read-only in the panel.
type ReadinessState string

const (
	StateHealthy     ReadinessState = "healthy"
	StateUFWMissing  ReadinessState = "ufw-missing"
	StateUnavailable ReadinessState = "unavailable"
)

// Rule represents a single UFW firewall rule.
type Rule struct {
	Number    int    `json:"number"`
	To        string `json:"to"`
	Action    string `json:"action"`
	Direction string `json:"direction"` // IN, OUT, FWD
	From      string `json:"from"`
	Protocol  string `json:"protocol,omitempty"`
	IsIPv6    bool   `json:"isIPv6"`
	Comment   string `json:"comment,omitempty"`
}

// AddRuleRequest is the input for adding a new firewall rule.
type AddRuleRequest struct {
	Action    string `json:"action"`             // allow | deny | reject | limit
	Direction string `json:"direction"`          // in | out
	Protocol  string `json:"protocol,omitempty"` // tcp | udp | any
	Port      string `json:"port,omitempty"`
	From      string `json:"from,omitempty"` // IP, CIDR, or "any"
	To        string `json:"to,omitempty"`   // IP, CIDR, or "any"
	Comment   string `json:"comment,omitempty"`
}

// regexps for parsing `ufw status verbose` output
var (
	defaultLineRe = regexp.MustCompile(`(?i)^Default:\s+(.+)$`)
	loggingLineRe = regexp.MustCompile(`(?i)^Logging:\s+(.+)$`)

	// ErrNotConfigured identifies a host without an installed UFW executable.
	// It is intentionally stable so aggregate callers can map the optional
	// integration without exposing executable paths or command diagnostics.
	ErrNotConfigured = errors.New("ufw is not configured")
)

const ufwProbeTimeout = 5 * time.Second

// ufwProbeRunner is the narrow command boundary used by ProbeContext.
// Keeping executable lookup and process execution behind one typed seam makes
// the read-only readiness contract deterministic in tests while production
// still uses the real local UFW subprocess.
type ufwProbeRunner interface {
	LookPath(string) (string, error)
	Run(context.Context, string, ...string) error
}

type execUFWProbeRunner struct{}

func (execUFWProbeRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func (execUFWProbeRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	// Readiness callers receive only a canonical state and safe classification
	// error. UFW output can contain installation paths or provider diagnostics,
	// so discard both streams at the subprocess boundary.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

// ProbeContext performs one fresh, read-only UFW readiness observation.
// UFW is healthy only when both `ufw status verbose` and `ufw status numbered`
// complete successfully. A missing executable is not_configured; all other
// failures, including command timeouts and caller cancellation, are
// unavailable. The legacy GetStatus endpoint remains unchanged and continues
// to expose its existing detailed status shape.
func ProbeContext(parent context.Context) (integrationstate.State, error) {
	return probeContext(parent, execUFWProbeRunner{})
}

// Probe is the background-context convenience form of ProbeContext.
func Probe() (integrationstate.State, error) {
	return ProbeContext(context.Background())
}

func probeContext(parent context.Context, runner ufwProbeRunner) (integrationstate.State, error) {
	if parent == nil {
		parent = context.Background()
	}
	if runner == nil {
		return integrationstate.Unavailable, errors.New("ufw readiness runner is unavailable")
	}
	if err := parent.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	resolved, err := runner.LookPath("ufw")
	if err != nil {
		if ClassifyUFWError(err) == StateUFWMissing {
			return integrationstate.NotConfigured, ErrNotConfigured
		}
		return integrationstate.Unavailable, errors.New("ufw executable is unavailable")
	}
	if strings.TrimSpace(resolved) == "" {
		return integrationstate.Unavailable, errors.New("ufw executable is unavailable")
	}
	if err := parent.Err(); err != nil {
		return integrationstate.Unavailable, err
	}

	if err := runUFWProbeCommand(parent, runner, resolved, "status", "verbose"); err != nil {
		if errors.Is(err, ErrNotConfigured) {
			return integrationstate.NotConfigured, ErrNotConfigured
		}
		return integrationstate.Unavailable, err
	}
	if err := runUFWProbeCommand(parent, runner, resolved, "status", "numbered"); err != nil {
		if errors.Is(err, ErrNotConfigured) {
			return integrationstate.NotConfigured, ErrNotConfigured
		}
		return integrationstate.Unavailable, err
	}
	if err := parent.Err(); err != nil {
		return integrationstate.Unavailable, err
	}
	return integrationstate.Healthy, nil
}

func runUFWProbeCommand(parent context.Context, runner ufwProbeRunner, bin string, args ...string) error {
	ctx, cancel := context.WithTimeout(parent, ufwProbeTimeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := runner.Run(ctx, bin, args...); err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if ClassifyUFWError(err) == StateUFWMissing {
			return ErrNotConfigured
		}
		if len(args) > 1 && args[1] == "verbose" {
			return errors.New("ufw verbose status observation failed")
		}
		return errors.New("ufw numbered status observation failed")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}

// GetStatus parses UFW status if available, otherwise falls back to iptables.
func GetStatus() (*Status, error) {
	verboseOut, err := shell.ExecuteRaw("ufw", "status", "verbose")
	if err == nil {
		// UFW is available — parse its output.
		status := &Status{Available: true, State: StateHealthy, Backend: "ufw"}
		parseVerboseHeader(verboseOut, status)

		numberedOut, numErr := shell.ExecuteRaw("ufw", "status", "numbered")
		if numErr == nil {
			status.Rules = parseNumberedRules(numberedOut)
		}

		return status, nil
	}

	if ClassifyUFWError(err) == StateUFWMissing {
		// Preserve legacy iptables visibility without claiming that UFW-only
		// mutation controls can manage those rules.
		return getStatusIPTables(err), nil
	}

	return &Status{
		Available: false,
		State:     StateUnavailable,
		Backend:   "ufw",
		Error:     err.Error(),
	}, nil
}

// ClassifyUFWError maps command discovery failures to a stable readiness state.
func ClassifyUFWError(err error) ReadinessState {
	if err == nil {
		return StateHealthy
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, "executable file not found") ||
		strings.Contains(message, "command not found") ||
		strings.Contains(message, "not found in $path") ||
		strings.Contains(message, "ufw: no such file or directory") {
		return StateUFWMissing
	}
	return StateUnavailable
}

// getStatusIPTables reads legacy rules for observation only. HServer's local
// firewall mutations remain UFW-specific and must stay disabled in this state.
func getStatusIPTables(ufwErr error) *Status {
	out, err := shell.ExecuteRaw("iptables", "-L", "-n", "--line-numbers")
	if err != nil {
		return &Status{
			Available: false,
			State:     StateUFWMissing,
			Backend:   "none",
			Error:     fmt.Sprintf("ufw unavailable: %v; iptables observation unavailable: %v", ufwErr, err),
		}
	}

	status := &Status{
		Available:       false,
		State:           StateUFWMissing,
		Backend:         "iptables",
		Error:           ufwErr.Error(),
		Active:          true,
		DefaultIncoming: "unknown",
		DefaultOutgoing: "unknown",
	}
	status.Rules = parseIPTablesRules(out)
	return status
}

// parseIPTablesRules parses the output of `iptables -L -n --line-numbers`.
//
// Example output:
//
//	Chain INPUT (policy DROP)
//	num  target     prot opt source               destination
//	1    ACCEPT     tcp  --  0.0.0.0/0            0.0.0.0/0            tcp dpt:22
//	2    ACCEPT     all  --  0.0.0.0/0            0.0.0.0/0            state RELATED,ESTABLISHED
func parseIPTablesRules(out string) []Rule {
	var rules []Rule

	chainHeaderRe := regexp.MustCompile(`^Chain\s+(\S+)\s+\(policy\s+(\S+)\)`)
	ruleRe := regexp.MustCompile(`^(\d+)\s+(\S+)\s+(\S+)\s+--\s+(\S+)\s+(\S+)\s*(.*)$`)

	currentChain := ""
	ruleCounter := 0

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Chain header line
		if m := chainHeaderRe.FindStringSubmatch(line); m != nil {
			currentChain = strings.ToUpper(m[1])
			continue
		}

		// Skip column header line
		if strings.HasPrefix(line, "num") {
			continue
		}

		m := ruleRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		num, _ := strconv.Atoi(m[1])
		action := strings.ToUpper(m[2])
		proto := m[3]
		src := m[4]
		dst := m[5]
		extra := strings.TrimSpace(m[6])

		// Normalize "all" protocol to empty
		if proto == "all" {
			proto = ""
		}

		// Extract destination port from extra info (e.g. "tcp dpt:22")
		port := ""
		if idx := strings.Index(extra, "dpt:"); idx != -1 {
			rest := extra[idx+4:]
			if end := strings.IndexAny(rest, " \t"); end != -1 {
				port = rest[:end]
			} else {
				port = rest
			}
		}

		toField := dst
		if port != "" {
			toField = port
		}

		// Normalize source: "0.0.0.0/0" → "Anywhere"
		fromField := src
		if src == "0.0.0.0/0" || src == "::/0" {
			fromField = "Anywhere"
		}

		// Map chain to direction
		direction := "IN"
		switch currentChain {
		case "OUTPUT":
			direction = "OUT"
		case "FORWARD":
			direction = "FWD"
		}

		// Map iptables targets to UFW-style actions
		mappedAction := action
		switch action {
		case "ACCEPT":
			mappedAction = "ALLOW"
		case "DROP":
			mappedAction = "DENY"
		case "REJECT":
			mappedAction = "REJECT"
		}

		ruleCounter++
		rules = append(rules, Rule{
			Number:    num,
			To:        toField,
			Action:    mappedAction,
			Direction: direction,
			From:      fromField,
			Protocol:  proto,
		})
		_ = ruleCounter
	}

	return rules
}

// parseVerboseHeader extracts status, defaults and logging from `ufw status verbose`.
func parseVerboseHeader(out string, s *Status) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		lower := strings.ToLower(line)

		if strings.HasPrefix(lower, "status:") {
			s.Active = strings.Contains(lower, "active")
			continue
		}

		if m := defaultLineRe.FindStringSubmatch(line); m != nil {
			// Example: "deny (incoming), allow (outgoing), disabled (routed)"
			parts := strings.Split(m[1], ",")
			for _, p := range parts {
				p = strings.TrimSpace(p)
				pl := strings.ToLower(p)
				switch {
				case strings.Contains(pl, "incoming"):
					s.DefaultIncoming = extractPolicy(p)
				case strings.Contains(pl, "outgoing"):
					s.DefaultOutgoing = extractPolicy(p)
				case strings.Contains(pl, "routed"):
					s.DefaultRouted = extractPolicy(p)
				}
			}
			continue
		}

		if m := loggingLineRe.FindStringSubmatch(line); m != nil {
			s.LoggingLevel = strings.TrimSpace(m[1])
		}
	}
}

// extractPolicy pulls "allow", "deny", or "disabled" from a policy string like "deny (incoming)".
func extractPolicy(s string) string {
	lower := strings.ToLower(s)
	for _, policy := range []string{"allow", "deny", "reject", "disabled"} {
		if strings.Contains(lower, policy) {
			return policy
		}
	}
	return strings.TrimSpace(s)
}

// parseNumberedRules parses the output of `ufw status numbered`.
//
// Example lines:
//
//	[ 1] 22/tcp                     ALLOW IN    Anywhere
//	[ 2] 80/tcp                     ALLOW IN    Anywhere
//	[ 3] 22/tcp (v6)                ALLOW IN    Anywhere (v6)
func parseNumberedRules(out string) []Rule {
	var rules []Rule

	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Match: [ N] <to>   ACTION [DIRECTION]   <from>
		re := regexp.MustCompile(`^\[\s*(\d+)\]\s+(.+?)\s{2,}(ALLOW|DENY|REJECT|LIMIT)\s*(IN|OUT|FWD)?\s{0,10}(.*)$`)
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}

		num, _ := strconv.Atoi(strings.TrimSpace(m[1]))
		toField := strings.TrimSpace(m[2])
		action := strings.TrimSpace(m[3])
		direction := strings.ToUpper(strings.TrimSpace(m[4]))
		fromField := strings.TrimSpace(m[5])

		isIPv6 := strings.Contains(toField, "(v6)") || strings.Contains(fromField, "(v6)")
		toField = strings.TrimSuffix(strings.TrimSpace(strings.Replace(toField, "(v6)", "", 1)), "")
		fromField = strings.TrimSuffix(strings.TrimSpace(strings.Replace(fromField, "(v6)", "", 1)), "")

		proto := ""
		if idx := strings.Index(toField, "/"); idx != -1 {
			proto = toField[idx+1:]
			toField = toField[:idx]
		}

		if direction == "" {
			direction = "IN"
		}

		rules = append(rules, Rule{
			Number:    num,
			To:        toField,
			Action:    action,
			Direction: direction,
			From:      fromField,
			Protocol:  proto,
			IsIPv6:    isIPv6,
		})
	}

	return rules
}

// AddRule adds a new UFW rule based on the request.
// Returns an error if UFW is not available on this system.
func AddRule(req AddRuleRequest) error {
	req.Action = strings.TrimSpace(req.Action)
	req.Direction = strings.TrimSpace(req.Direction)
	req.Protocol = strings.TrimSpace(req.Protocol)
	req.Port = strings.TrimSpace(req.Port)
	req.From = strings.TrimSpace(req.From)
	req.To = strings.TrimSpace(req.To)
	if err := validateAddRequest(req); err != nil {
		return err
	}

	if !isUFWAvailable() {
		return fmt.Errorf("ufw is not installed on this server; add/delete operations require ufw")
	}

	args := buildUFWArgs(req)
	out, err := shell.ExecuteRaw("ufw", args...)
	if err != nil {
		return fmt.Errorf("ufw add rule: %w", err)
	}
	_ = out
	return nil
}

// isUFWAvailable returns true if the ufw command is present and executable.
func isUFWAvailable() bool {
	_, err := shell.ExecuteRaw("ufw", "version")
	return err == nil
}

// validateAddRequest validates the fields in AddRuleRequest.
func validateAddRequest(req AddRuleRequest) error {
	action := strings.TrimSpace(req.Action)
	if action != "allow" && action != "deny" && action != "reject" && action != "limit" {
		return fmt.Errorf("invalid action: %q (must be allow|deny|reject|limit)", req.Action)
	}

	direction := strings.TrimSpace(req.Direction)
	if direction != "" && direction != "in" && direction != "out" {
		return fmt.Errorf("invalid direction: %q (must be in|out)", req.Direction)
	}

	if req.Port != "" {
		// Port can be "80", "80:90" (range), or service name like "ssh"
		portRe := regexp.MustCompile(`^[a-zA-Z0-9:/_-]{1,20}$`)
		if !portRe.MatchString(req.Port) {
			return fmt.Errorf("invalid port: %q", req.Port)
		}
	}

	if req.From != "" && req.From != "any" && req.From != "Anywhere" {
		if !isValidIPOrCIDR(req.From) {
			return fmt.Errorf("invalid from address: %q", req.From)
		}
	}

	if req.To != "" && req.To != "any" && req.To != "Anywhere" {
		if !isValidIPOrCIDR(req.To) {
			return fmt.Errorf("invalid to address: %q", req.To)
		}
	}

	proto := strings.TrimSpace(req.Protocol)
	if proto != "" && proto != "tcp" && proto != "udp" && proto != "any" {
		return fmt.Errorf("invalid protocol: %q (must be tcp|udp|any)", req.Protocol)
	}

	return nil
}

// buildUFWArgs constructs the argument list for the ufw command.
func buildUFWArgs(req AddRuleRequest) []string {
	// ufw [direction] [action] [proto] [from X] [to Y port Z] [comment "..."]
	// Simplified form: ufw allow/deny [port/proto]
	args := []string{}

	direction := strings.ToLower(req.Direction)
	if direction == "" {
		direction = "in"
	}
	args = append(args, direction)
	args = append(args, strings.ToLower(req.Action))

	if req.From != "" && req.From != "any" {
		args = append(args, "from", req.From)
	}

	if req.To != "" && req.To != "any" {
		args = append(args, "to", req.To)
	}

	if req.Port != "" {
		args = append(args, "port", req.Port)
	}

	proto := strings.ToLower(req.Protocol)
	if proto != "" && proto != "any" {
		args = append(args, "proto", proto)
	}

	if req.Comment != "" {
		// Sanitize comment: strip special chars
		safeComment := sanitizeComment(req.Comment)
		if safeComment != "" {
			args = append(args, "comment", safeComment)
		}
	}

	return args
}

// DeleteRule deletes a UFW rule by its rule number.
// It first verifies the rule is not the last SSH allow rule.
// Returns an error if UFW is not available on this system.
func DeleteRule(number int) error {
	if !isUFWAvailable() {
		return fmt.Errorf("ufw is not installed on this server; add/delete operations require ufw")
	}

	if number < 1 {
		return fmt.Errorf("invalid rule number: %d", number)
	}

	// Safety check: fetch current rules and ensure we're not removing the last SSH allow.
	status, err := GetStatus()
	if err != nil {
		return fmt.Errorf("cannot fetch firewall status: %w", err)
	}
	if !status.Available {
		return fmt.Errorf("cannot fetch manageable UFW status: %s", status.Error)
	}

	if err := checkSSHSafety(number, status.Rules); err != nil {
		return err
	}

	// ufw --force delete N
	_, err = shell.ExecuteRaw("ufw", "--force", "delete", strconv.Itoa(number))
	if err != nil {
		return fmt.Errorf("ufw delete rule %d: %w", number, err)
	}
	return nil
}

// checkSSHSafety returns an error if deleting `ruleNum` would remove the last SSH allow rule.
func checkSSHSafety(ruleNum int, rules []Rule) error {
	// Count all SSH (port 22) ALLOW IN rules.
	sshAllowRules := []int{}
	for _, r := range rules {
		isSsh := r.To == "22" || strings.EqualFold(r.To, "ssh") ||
			strings.EqualFold(r.To, "OpenSSH")
		isAllow := strings.EqualFold(r.Action, "allow")
		isIn := r.Direction == "IN" || r.Direction == ""
		if isSsh && isAllow && isIn {
			sshAllowRules = append(sshAllowRules, r.Number)
		}
	}

	if len(sshAllowRules) == 0 {
		// No SSH rules at all — allow deletion.
		return nil
	}

	if len(sshAllowRules) == 1 && sshAllowRules[0] == ruleNum {
		return fmt.Errorf(
			"safety: cannot delete rule #%d — it is the last SSH (port 22) ALLOW IN rule; "+
				"deleting it would lock you out of the server",
			ruleNum,
		)
	}

	return nil
}

// Enable enables UFW with auto-confirmation.
func Enable() error {
	_, err := shell.ExecuteRaw("ufw", "--force", "enable")
	if err != nil {
		return fmt.Errorf("enabling ufw: %w", err)
	}
	return nil
}

// Disable disables UFW with auto-confirmation.
func Disable() error {
	_, err := shell.ExecuteRaw("ufw", "--force", "disable")
	if err != nil {
		return fmt.Errorf("disabling ufw: %w", err)
	}
	return nil
}

// Toggle enables UFW if it is disabled, and vice versa.
func Toggle() (bool, error) {
	status, err := GetStatus()
	if err != nil {
		return false, err
	}
	if !status.Available {
		return false, fmt.Errorf("cannot fetch manageable UFW status: %s", status.Error)
	}
	if status.Active {
		return false, Disable()
	}
	return true, Enable()
}

// isValidIPOrCIDR validates an IPv4 or IPv6 address/CIDR.
var ipCIDRRe = regexp.MustCompile(`^[0-9a-fA-F:./:]{2,50}$`)

func isValidIPOrCIDR(s string) bool {
	return ipCIDRRe.MatchString(s)
}

// sanitizeComment removes shell-dangerous characters from a comment string.
func sanitizeComment(s string) string {
	// Keep alphanumerics, spaces, hyphens, underscores, dots.
	re := regexp.MustCompile(`[^a-zA-Z0-9 _\-.]`)
	return strings.TrimSpace(re.ReplaceAllString(s, ""))
}
