package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const firewallManagedChain = "HSERVER-INPUT"

var (
	agentFirewallIDPattern = regexp.MustCompile(`^fw-[a-f0-9]{12}$`)
	errFirewallChanged     = errors.New("firewall rules changed on the server")
	errFirewallNotFound    = errors.New("firewall rule was not found")
	errFirewallInvalid     = errors.New("firewall rule is invalid")
	errFirewallProtected   = errors.New("firewall rule would block a locally protected management path")
	errFirewallPersistence = errors.New("firewall persistence failed")
)

type firewallRule struct {
	ID       string `json:"id"`
	Action   string `json:"action"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port,omitempty"`
	Source   string `json:"source,omitempty"`
	Comment  string `json:"comment,omitempty"`
	Managed  bool   `json:"managed"`
	Raw      string `json:"raw,omitempty"`
}

type firewallInventory struct {
	Backend          string         `json:"backend"`
	Policy           string         `json:"policy"`
	Persistence      string         `json:"persistence"`
	Rules            []firewallRule `json:"rules"`
	Revision         string         `json:"revision"`
	ProtectedSources []string       `json:"protected_sources"`
	ProtectedPorts   []int          `json:"protected_ports"`
}

type firewallController struct {
	runner           commandRunner
	allowRead        bool
	allowWrite       bool
	iptables         string
	saveBinary       string
	lockPath         string
	persistenceSvc   string
	protectedSources []*net.IPNet
	protectedLabels  []string
	protectedPorts   map[int]struct{}
	mu               *sync.Mutex
}

func newFirewallController(runner commandRunner, allowRead, allowWrite bool, iptables, saveBinary, lockPath, persistenceSvc string, protectedSources []string, protectedPorts map[int]struct{}) firewallController {
	networks := make([]*net.IPNet, 0, len(protectedSources))
	for _, source := range protectedSources {
		_, network, err := net.ParseCIDR(source)
		if err == nil && network.IP.To4() != nil {
			networks = append(networks, network)
		}
	}
	ports := make(map[int]struct{}, len(protectedPorts))
	for port := range protectedPorts {
		ports[port] = struct{}{}
	}
	return firewallController{
		runner: runner, allowRead: allowRead, allowWrite: allowWrite,
		iptables: iptables, saveBinary: saveBinary, lockPath: lockPath, persistenceSvc: persistenceSvc,
		protectedSources: networks, protectedLabels: append([]string(nil), protectedSources...), protectedPorts: ports,
		mu: &sync.Mutex{},
	}
}

func (c firewallController) Inventory(ctx context.Context) (firewallInventory, error) {
	if !c.allowRead {
		return firewallInventory{}, errors.New("firewall inventory is not enabled locally")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	inputOutput, err := c.run(ctx, c.iptables, "-S", "INPUT")
	if err != nil {
		return firewallInventory{}, fmt.Errorf("read INPUT rules: %w", err)
	}
	managedLines, _, err := c.managedLines(ctx)
	if err != nil {
		return firewallInventory{}, err
	}
	policy, systemRules := parseInputRules(string(inputOutput))
	managedRules := make([]firewallRule, 0, len(managedLines))
	for _, line := range managedLines {
		if rule, ok := parseManagedFirewallRule(line); ok {
			managedRules = append(managedRules, rule)
		}
	}
	rules := append(managedRules, systemRules...)
	if rules == nil {
		rules = []firewallRule{}
	}
	ports := make([]int, 0, len(c.protectedPorts))
	for port := range c.protectedPorts {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return firewallInventory{
		Backend: "iptables", Policy: policy, Persistence: c.persistenceState(ctx), Rules: rules,
		Revision: firewallRevision(managedLines), ProtectedSources: append([]string(nil), c.protectedLabels...), ProtectedPorts: ports,
	}, nil
}

func (c firewallController) Add(ctx context.Context, rule firewallRule, expectedRevision string) (string, error) {
	if !c.allowWrite {
		return "", errors.New("firewall writing is not enabled locally")
	}
	rule.ID, rule.Managed, rule.Raw = "", true, ""
	normalized, err := normalizeFirewallRule(rule)
	if err != nil {
		return "", err
	}
	if c.blocksProtectedPath(normalized) {
		return "", errFirewallProtected
	}
	id, err := newFirewallID()
	if err != nil {
		return "", err
	}
	normalized.ID = id
	if err := c.withMutationLock(func() error {
		lines, chainExists, err := c.managedLines(ctx)
		if err != nil {
			return err
		}
		if !agentSHA256Pattern.MatchString(expectedRevision) || firewallRevision(lines) != expectedRevision {
			return errFirewallChanged
		}
		if !chainExists {
			if _, err := c.run(ctx, c.iptables, "-N", firewallManagedChain); err != nil {
				return fmt.Errorf("create managed firewall chain: %w", err)
			}
		}
		if _, err := c.run(ctx, c.iptables, "-C", "INPUT", "-j", firewallManagedChain); err != nil {
			if _, err := c.run(ctx, c.iptables, "-I", "INPUT", "1", "-j", firewallManagedChain); err != nil {
				return fmt.Errorf("attach managed firewall chain: %w", err)
			}
		}
		args := firewallAppendArgs(normalized)
		if _, err := c.run(ctx, c.iptables, args...); err != nil {
			return fmt.Errorf("append managed firewall rule: %w", err)
		}
		if _, err := c.run(ctx, c.saveBinary, "save"); err != nil {
			rollback := append([]string{"-D", firewallManagedChain}, args[2:]...)
			if _, rollbackErr := c.run(ctx, c.iptables, rollback...); rollbackErr != nil {
				return fmt.Errorf("%w; rollback failed: %v", errFirewallPersistence, rollbackErr)
			}
			return errFirewallPersistence
		}
		return nil
	}); err != nil {
		return "", err
	}
	return id, nil
}

func (c firewallController) Delete(ctx context.Context, id, expectedRevision string) error {
	if !c.allowWrite {
		return errors.New("firewall writing is not enabled locally")
	}
	if !agentFirewallIDPattern.MatchString(id) {
		return errFirewallInvalid
	}
	return c.withMutationLock(func() error {
		lines, _, err := c.managedLines(ctx)
		if err != nil {
			return err
		}
		if !agentSHA256Pattern.MatchString(expectedRevision) || firewallRevision(lines) != expectedRevision {
			return errFirewallChanged
		}
		chainOutput, err := c.run(ctx, c.iptables, "-S", firewallManagedChain)
		if err != nil {
			return errFirewallNotFound
		}
		var target []string
		position := 0
		for _, line := range strings.Split(string(chainOutput), "\n") {
			args := strings.Fields(line)
			if len(args) < 2 || args[0] != "-A" || args[1] != firewallManagedChain {
				continue
			}
			position++
			if hasFirewallComment(args, "hserver:"+id+":") {
				target = args
				break
			}
		}
		if target == nil {
			return errFirewallNotFound
		}
		deleteArgs := append([]string(nil), target...)
		deleteArgs[0] = "-D"
		if _, err := c.run(ctx, c.iptables, deleteArgs...); err != nil {
			return fmt.Errorf("delete managed firewall rule: %w", err)
		}
		if _, err := c.run(ctx, c.saveBinary, "save"); err != nil {
			restoreArgs := append([]string{"-I", firewallManagedChain, strconv.Itoa(position)}, target[2:]...)
			if _, rollbackErr := c.run(ctx, c.iptables, restoreArgs...); rollbackErr != nil {
				return fmt.Errorf("%w; rollback failed: %v", errFirewallPersistence, rollbackErr)
			}
			return errFirewallPersistence
		}
		return nil
	})
}

func (c firewallController) withMutationLock(apply func() error) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(c.lockPath), 0o755); err != nil {
		return err
	}
	lock, err := os.OpenFile(c.lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	return apply()
}

func (c firewallController) managedLines(ctx context.Context) ([]string, bool, error) {
	output, err := c.run(ctx, c.iptables, "-S", firewallManagedChain)
	if err != nil {
		return []string{}, false, nil
	}
	lines := make([]string, 0)
	for _, raw := range strings.Split(string(output), "\n") {
		line := strings.TrimSpace(raw)
		args := strings.Fields(line)
		if len(args) >= 2 && args[0] == "-A" && args[1] == firewallManagedChain && hasFirewallComment(args, "hserver:fw-") {
			lines = append(lines, line)
		}
	}
	return lines, true, nil
}

func (c firewallController) persistenceState(ctx context.Context) string {
	output, err := c.run(ctx, "/usr/bin/systemctl", "show", "--property=ActiveState", "--value", c.persistenceSvc)
	if err != nil || strings.TrimSpace(string(output)) == "" {
		return "unavailable"
	}
	return strings.TrimSpace(string(output))
}

func (c firewallController) run(ctx context.Context, name string, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return c.runner.run(commandCtx, name, args...)
}

func (c firewallController) blocksProtectedPath(rule firewallRule) bool {
	if rule.Action != "DROP" || len(c.protectedSources) == 0 || len(c.protectedPorts) == 0 {
		return false
	}
	if rule.Port != 0 {
		if _, protected := c.protectedPorts[rule.Port]; !protected {
			return false
		}
	}
	_, source, err := net.ParseCIDR(rule.Source)
	if err != nil {
		return false
	}
	for _, protected := range c.protectedSources {
		if networksOverlap(source, protected) {
			return true
		}
	}
	return false
}

func networksOverlap(left, right *net.IPNet) bool {
	return left.Contains(right.IP) || right.Contains(left.IP)
}

func normalizeFirewallRule(rule firewallRule) (firewallRule, error) {
	if rule.Action != "ACCEPT" && rule.Action != "DROP" || rule.Protocol != "tcp" && rule.Protocol != "udp" && rule.Protocol != "all" {
		return firewallRule{}, errFirewallInvalid
	}
	if rule.Protocol == "all" && rule.Port != 0 || rule.Protocol != "all" && (rule.Port < 0 || rule.Port > 65535) || len(rule.Comment) > 80 || strings.ContainsAny(rule.Comment, "\r\n\x00") {
		return firewallRule{}, errFirewallInvalid
	}
	source := strings.TrimSpace(rule.Source)
	if source == "" {
		source = "0.0.0.0/0"
	}
	normalized, err := normalizeIPv4Network(source)
	if err != nil {
		return firewallRule{}, errFirewallInvalid
	}
	rule.Source = normalized
	return rule, nil
}

func firewallAppendArgs(rule firewallRule) []string {
	note := base64.RawURLEncoding.EncodeToString([]byte(rule.Comment))
	args := []string{"-A", firewallManagedChain}
	if rule.Source != "0.0.0.0/0" {
		args = append(args, "-s", rule.Source)
	}
	if rule.Protocol != "all" {
		args = append(args, "-p", rule.Protocol)
	}
	if rule.Port != 0 {
		args = append(args, "--dport", strconv.Itoa(rule.Port))
	}
	return append(args, "-m", "comment", "--comment", "hserver:"+rule.ID+":"+note, "-j", rule.Action)
}

func parseInputRules(output string) (string, []firewallRule) {
	policy := "unknown"
	rules := make([]firewallRule, 0)
	for index, raw := range strings.Split(output, "\n") {
		line := strings.TrimSpace(raw)
		args := strings.Fields(line)
		if len(args) >= 3 && args[0] == "-P" && args[1] == "INPUT" {
			policy = args[2]
			continue
		}
		if len(args) < 2 || args[0] != "-A" || args[1] != "INPUT" || containsArgPair(args, "-j", firewallManagedChain) {
			continue
		}
		rules = append(rules, firewallRule{ID: fmt.Sprintf("input-%d", index), Action: argAfter(args, "-j"), Protocol: defaultString(argAfter(args, "-p"), "all"), Source: argAfter(args, "-s"), Managed: false, Raw: line})
	}
	return policy, rules
}

func parseManagedFirewallRule(line string) (firewallRule, bool) {
	args := strings.Fields(line)
	comment := argAfter(args, "--comment")
	parts := strings.SplitN(comment, ":", 3)
	if len(parts) != 3 || parts[0] != "hserver" || !agentFirewallIDPattern.MatchString(parts[1]) {
		return firewallRule{}, false
	}
	note, _ := base64.RawURLEncoding.DecodeString(parts[2])
	port, _ := strconv.Atoi(argAfter(args, "--dport"))
	return firewallRule{ID: parts[1], Action: argAfter(args, "-j"), Protocol: defaultString(argAfter(args, "-p"), "all"), Port: port, Source: defaultString(argAfter(args, "-s"), "0.0.0.0/0"), Comment: string(note), Managed: true, Raw: line}, true
}

func hasFirewallComment(args []string, prefix string) bool {
	return strings.HasPrefix(argAfter(args, "--comment"), prefix)
}

func containsArgPair(args []string, key, value string) bool {
	return argAfter(args, key) == value
}

func argAfter(args []string, key string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == key {
			return strings.Trim(args[index+1], `"'`)
		}
	}
	return ""
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func firewallRevision(lines []string) string {
	canonical, _ := json.Marshal(lines)
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

func newFirewallID() (string, error) {
	var raw [6]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return "fw-" + hex.EncodeToString(raw[:]), nil
}
