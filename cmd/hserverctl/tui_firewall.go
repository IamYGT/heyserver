package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

type tuiFirewallRule struct {
	ID        string
	Number    int
	Action    string
	Direction string
	Protocol  string
	Target    string
	Source    string
	Comment   string
	Managed   bool
}

type tuiFirewallState struct {
	Backend          string
	State            string
	Active           bool
	Manageable       bool
	Policy           string
	Persistence      string
	DefaultIncoming  string
	DefaultOutgoing  string
	Revision         string
	ProtectedSources []string
	ProtectedPorts   []int
	Rules            []tuiFirewallRule
}

type tuiFirewallSpec struct {
	Action   string
	Protocol string
	Port     int
	Source   string
	Comment  string
}

type tuiFirewallMsg struct {
	TargetID string
	State    tuiFirewallState
	Err      error
}

func loadTUIFirewallCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		state, err := loadTUIFirewall(ctx, client, target)
		return tuiFirewallMsg{TargetID: target.ID, State: state, Err: err}
	}
}

func loadTUIFirewall(ctx context.Context, client *apiClient, target tuiTarget) (tuiFirewallState, error) {
	if !target.Local {
		if !target.Online {
			return tuiFirewallState{}, errors.New("managed node is offline")
		}
		if !target.capability(agenthub.CapabilityFirewallRead) {
			return tuiFirewallState{}, errors.New("managed agent does not advertise firewall.read")
		}
		inventory, err := loadRemoteFirewallInventory(ctx, client, target.ID)
		if err != nil {
			return tuiFirewallState{}, err
		}
		state := tuiFirewallState{
			Backend: inventory.Backend, State: "healthy", Active: true,
			Manageable: target.capability(agenthub.CapabilityFirewallWrite),
			Policy:     inventory.Policy, Persistence: inventory.Persistence, Revision: inventory.Revision,
			ProtectedSources: append([]string(nil), inventory.ProtectedSources...),
			ProtectedPorts:   append([]int(nil), inventory.ProtectedPorts...),
			Rules:            make([]tuiFirewallRule, 0, len(inventory.Rules)),
		}
		for _, rule := range inventory.Rules {
			target := "all"
			if rule.Port > 0 {
				target = strconv.Itoa(rule.Port)
			}
			state.Rules = append(state.Rules, tuiFirewallRule{
				ID: rule.ID, Action: rule.Action, Direction: "IN", Protocol: rule.Protocol,
				Target: target, Source: rule.Source, Comment: rule.Comment, Managed: rule.Managed,
			})
		}
		sort.SliceStable(state.Rules, func(i, j int) bool {
			if state.Rules[i].Managed != state.Rules[j].Managed {
				return state.Rules[i].Managed
			}
			return strings.ToLower(state.Rules[i].ID+state.Rules[i].Target) < strings.ToLower(state.Rules[j].ID+state.Rules[j].Target)
		})
		return state, nil
	}

	status, err := requestJSON[struct {
		Available       bool   `json:"available"`
		State           string `json:"state"`
		Backend         string `json:"backend"`
		Active          bool   `json:"active"`
		DefaultIncoming string `json:"defaultIncoming"`
		DefaultOutgoing string `json:"defaultOutgoing"`
		Rules           []struct {
			Number    int    `json:"number"`
			To        string `json:"to"`
			Action    string `json:"action"`
			Direction string `json:"direction"`
			From      string `json:"from"`
			Protocol  string `json:"protocol"`
			Comment   string `json:"comment"`
		} `json:"rules"`
	}](ctx, client.withTimeout(45*time.Second), http.MethodGet, "/api/firewall/status", nil, true)
	if err != nil {
		return tuiFirewallState{}, err
	}
	state := tuiFirewallState{
		Backend: status.Backend, State: status.State, Active: status.Active,
		Manageable:      status.Available && status.Backend == "ufw",
		DefaultIncoming: status.DefaultIncoming, DefaultOutgoing: status.DefaultOutgoing,
		Rules: make([]tuiFirewallRule, 0, len(status.Rules)),
	}
	for _, rule := range status.Rules {
		state.Rules = append(state.Rules, tuiFirewallRule{
			ID: strconv.Itoa(rule.Number), Number: rule.Number, Action: rule.Action,
			Direction: rule.Direction, Protocol: rule.Protocol, Target: rule.To,
			Source: rule.From, Comment: rule.Comment, Managed: state.Manageable,
		})
	}
	return state, nil
}

func firewallSpecForAction(action string) (tuiFirewallSpec, bool) {
	switch action {
	case "firewall-add-ssh":
		return tuiFirewallSpec{Action: "allow", Protocol: "tcp", Port: 22, Source: "any", Comment: "SSH"}, true
	case "firewall-add-http":
		return tuiFirewallSpec{Action: "allow", Protocol: "tcp", Port: 80, Source: "any", Comment: "HTTP"}, true
	case "firewall-add-https":
		return tuiFirewallSpec{Action: "allow", Protocol: "tcp", Port: 443, Source: "any", Comment: "HTTPS"}, true
	case "firewall-add-dns":
		return tuiFirewallSpec{Action: "allow", Protocol: "udp", Port: 53, Source: "any", Comment: "DNS"}, true
	default:
		return tuiFirewallSpec{}, false
	}
}

func runTUIFirewallOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	if !operation.Target.Local && !operation.Target.Online {
		return "", errors.New("managed node is offline")
	}
	switch operation.Action {
	case "add":
		spec := operation.FirewallSpec
		if spec.Action != "allow" || spec.Protocol != "tcp" && spec.Protocol != "udp" || spec.Port < 1 || spec.Port > 65535 || !isFirewallAny(spec.Source) {
			return "", errors.New("invalid fixed firewall profile")
		}
		if operation.Target.Local {
			response, err := requestJSON[map[string]any](ctx, client.withTimeout(2*time.Minute), http.MethodPost, "/api/firewall/rules", map[string]any{
				"action": spec.Action, "direction": "in", "protocol": spec.Protocol,
				"port": strconv.Itoa(spec.Port), "from": "any", "to": "any", "comment": spec.Comment,
			}, true)
			return firewallOperationMessage(response, "Firewall rule added", err)
		}
		if !operation.Target.capability(agenthub.CapabilityFirewallWrite) {
			return "", errors.New("managed agent does not advertise firewall.write")
		}
		inventory, err := loadRemoteFirewallInventory(ctx, client, operation.Target.ID)
		if err != nil {
			return "", err
		}
		endpoint := "/api/nodes/" + url.PathEscape(operation.Target.ID) + "/firewall"
		response, err := requestJSON[map[string]any](ctx, client.withTimeout(2*time.Minute), http.MethodPost, endpoint, map[string]any{
			"action": "ACCEPT", "protocol": spec.Protocol, "port": spec.Port,
			"source": "0.0.0.0/0", "comment": spec.Comment, "revision": inventory.Revision,
		}, true)
		return firewallOperationMessage(response, "Firewall rule added and persisted", err)
	case "delete":
		rule := operation.FirewallRule
		if operation.Target.Local {
			if rule.Number < 1 || !operation.FirewallState.Manageable {
				return "", errors.New("local firewall rule is not manageable through UFW")
			}
			response, err := requestJSON[map[string]any](ctx, client.withTimeout(2*time.Minute), http.MethodDelete,
				"/api/firewall/rules/"+strconv.Itoa(rule.Number), nil, true)
			return firewallOperationMessage(response, "Firewall rule deleted", err)
		}
		if !operation.Target.capability(agenthub.CapabilityFirewallWrite) {
			return "", errors.New("managed agent does not advertise firewall.write")
		}
		if !rule.Managed || !remoteFirewallRuleIDPattern.MatchString(rule.ID) {
			return "", errors.New("only installation-owned managed firewall rules can be deleted")
		}
		inventory, err := loadRemoteFirewallInventory(ctx, client, operation.Target.ID)
		if err != nil {
			return "", err
		}
		found := false
		for _, current := range inventory.Rules {
			if current.ID == rule.ID && current.Managed {
				found = true
				break
			}
		}
		if !found {
			return "", errors.New("managed firewall rule is no longer present")
		}
		endpoint := "/api/nodes/" + url.PathEscape(operation.Target.ID) + "/firewall/" + url.PathEscape(rule.ID)
		response, err := requestJSON[map[string]any](ctx, client.withTimeout(2*time.Minute), http.MethodDelete, endpoint,
			map[string]string{"revision": inventory.Revision}, true)
		return firewallOperationMessage(response, "Firewall rule deleted and persisted", err)
	case "enable", "disable":
		if !operation.Target.Local {
			return "", errors.New("managed firewall activation is owned by the agent installation")
		}
		response, err := requestJSON[map[string]any](ctx, client.withTimeout(2*time.Minute), http.MethodPost, "/api/firewall/toggle",
			map[string]bool{"enable": operation.Action == "enable"}, true)
		return firewallOperationMessage(response, "UFW firewall "+operation.Action+"d", err)
	default:
		return "", fmt.Errorf("unsupported firewall TUI action %q", operation.Action)
	}
}

func firewallOperationMessage(response map[string]any, fallback string, err error) (string, error) {
	if err != nil {
		return "", err
	}
	if message, ok := response["message"].(string); ok && strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message), nil
	}
	return fallback, nil
}
