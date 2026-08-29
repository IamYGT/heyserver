package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	remoteFirewallRuleIDPattern = regexp.MustCompile(`^fw-[a-f0-9]{12}$`)
	firewallRevisionPattern     = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type remoteFirewallRule struct {
	ID       string `json:"id"`
	Action   string `json:"action"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port,omitempty"`
	Source   string `json:"source,omitempty"`
	Comment  string `json:"comment,omitempty"`
	Managed  bool   `json:"managed"`
	Raw      string `json:"raw,omitempty"`
}

type remoteFirewallInventory struct {
	Backend          string               `json:"backend"`
	Policy           string               `json:"policy"`
	Persistence      string               `json:"persistence"`
	Rules            []remoteFirewallRule `json:"rules"`
	Revision         string               `json:"revision"`
	ProtectedSources []string             `json:"protected_sources"`
	ProtectedPorts   []int                `json:"protected_ports"`
}

type firewallAddOptions struct {
	Node      string
	Action    string
	Direction string
	Protocol  string
	Port      string
	Source    string
	Target    string
	Comment   string
	Wait      time.Duration
}

func runFirewall(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl firewall status|list|add|delete|toggle")
	}
	switch args[0] {
	case "status", "list":
		node, err := parseOptionalNode("firewall "+args[0], args[1:])
		if err != nil {
			return err
		}
		endpoint := "/api/firewall/status"
		if node != "" {
			endpoint = "/api/nodes/" + url.PathEscape(node) + "/firewall"
		}
		return printRequest(ctx, client.withTimeout(45*time.Second), out, http.MethodGet, endpoint, nil, true)
	case "add":
		return runFirewallAdd(ctx, client, args[1:], out)
	case "delete":
		return runFirewallDelete(ctx, client, args[1:], out)
	case "toggle":
		return runFirewallToggle(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown firewall command %q", args[0])
	}
}

func runFirewallAdd(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("firewall add", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	confirmed := flags.Bool("confirm", false, "confirm the firewall mutation")
	action := flags.String("action", "", "allow, deny, or a local-only reject/limit action")
	direction := flags.String("direction", "in", "local UFW direction: in or out")
	protocol := flags.String("protocol", "tcp", "tcp, udp, or any")
	port := flags.String("port", "", "numeric port, local range, or local service name")
	source := flags.String("source", "any", "source IP or CIDR")
	target := flags.String("to", "any", "local destination IP or CIDR")
	comment := flags.String("comment", "", "bounded rule description")
	wait := flags.Duration("wait", 2*time.Minute, "maximum action wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 || strings.TrimSpace(*action) == "" {
		return errors.New("usage: hserverctl firewall add --confirm [--node NODE] --action allow|deny|reject|limit [--protocol tcp|udp|any] [--port PORT] [--source IP|CIDR] [--direction in|out] [--to IP|CIDR] [--comment TEXT] [--wait DURATION]")
	}
	if !*confirmed {
		return errors.New("firewall rule creation requires explicit --confirm")
	}
	options := firewallAddOptions{
		Node: strings.TrimSpace(*node), Action: strings.ToLower(strings.TrimSpace(*action)),
		Direction: strings.ToLower(strings.TrimSpace(*direction)), Protocol: strings.ToLower(strings.TrimSpace(*protocol)),
		Port: strings.TrimSpace(*port), Source: strings.TrimSpace(*source), Target: strings.TrimSpace(*target),
		Comment: strings.TrimSpace(*comment), Wait: *wait,
	}
	if err := validateFirewallAddOptions(options); err != nil {
		return err
	}
	if options.Node == "" {
		payload := map[string]any{
			"action": options.Action, "direction": options.Direction, "protocol": options.Protocol,
			"port": options.Port, "from": options.Source, "to": options.Target, "comment": options.Comment,
		}
		return printRequest(ctx, client.withTimeout(options.Wait), out, http.MethodPost, "/api/firewall/rules", payload, true)
	}

	inventory, err := loadRemoteFirewallInventory(ctx, client, options.Node)
	if err != nil {
		return err
	}
	portNumber := 0
	if options.Port != "" {
		portNumber, _ = strconv.Atoi(options.Port)
	}
	remoteAction := "ACCEPT"
	if options.Action == "deny" {
		remoteAction = "DROP"
	}
	remoteProtocol := options.Protocol
	if remoteProtocol == "any" {
		remoteProtocol = "all"
	}
	payload := map[string]any{
		"action": remoteAction, "protocol": remoteProtocol, "port": portNumber,
		"source": normalizeFirewallAny(options.Source, "0.0.0.0/0"), "comment": options.Comment,
		"revision": inventory.Revision,
	}
	endpoint := "/api/nodes/" + url.PathEscape(options.Node) + "/firewall"
	return printRequest(ctx, client.withTimeout(options.Wait), out, http.MethodPost, endpoint, payload, true)
}

func runFirewallDelete(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("firewall delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	confirmed := flags.Bool("confirm", false, "confirm the firewall mutation")
	wait := flags.Duration("wait", 2*time.Minute, "maximum action wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl firewall delete --confirm [--node NODE] [--wait DURATION] RULE")
	}
	if !*confirmed {
		return errors.New("firewall rule deletion requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("firewall action wait must be greater than zero")
	}
	identity := strings.TrimSpace(flags.Args()[0])
	if strings.TrimSpace(*node) == "" {
		number, err := strconv.Atoi(identity)
		if err != nil || number < 1 {
			return errors.New("local firewall rule must be a positive observed rule number")
		}
		return printRequest(ctx, client.withTimeout(*wait), out, http.MethodDelete, "/api/firewall/rules/"+strconv.Itoa(number), nil, true)
	}
	if !remoteFirewallRuleIDPattern.MatchString(identity) {
		return errors.New("managed firewall rule must be an observed fw- identity")
	}
	nodeID := strings.TrimSpace(*node)
	inventory, err := loadRemoteFirewallInventory(ctx, client, nodeID)
	if err != nil {
		return err
	}
	managed := false
	for _, rule := range inventory.Rules {
		if rule.ID == identity && rule.Managed {
			managed = true
			break
		}
	}
	if !managed {
		return errors.New("managed firewall rule is not present in the current installation-owned inventory")
	}
	endpoint := "/api/nodes/" + url.PathEscape(nodeID) + "/firewall/" + url.PathEscape(identity)
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodDelete, endpoint, map[string]string{"revision": inventory.Revision}, true)
}

func runFirewallToggle(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("firewall toggle", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm the local UFW mutation")
	wait := flags.Duration("wait", 2*time.Minute, "maximum action wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 || flags.Args()[0] != "enable" && flags.Args()[0] != "disable" {
		return errors.New("usage: hserverctl firewall toggle --confirm [--wait DURATION] enable|disable")
	}
	if !*confirmed {
		return errors.New("firewall toggle requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("firewall action wait must be greater than zero")
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, "/api/firewall/toggle", map[string]bool{"enable": flags.Args()[0] == "enable"}, true)
}

func validateFirewallAddOptions(options firewallAddOptions) error {
	if options.Wait <= 0 {
		return errors.New("firewall action wait must be greater than zero")
	}
	if options.Action != "allow" && options.Action != "deny" && (options.Node != "" || options.Action != "reject" && options.Action != "limit") {
		if options.Node != "" {
			return errors.New("managed firewall action must be allow or deny")
		}
		return errors.New("local firewall action must be allow, deny, reject, or limit")
	}
	if options.Direction != "in" && options.Direction != "out" {
		return errors.New("firewall direction must be in or out")
	}
	if options.Protocol != "tcp" && options.Protocol != "udp" && options.Protocol != "any" {
		return errors.New("firewall protocol must be tcp, udp, or any")
	}
	if len(options.Comment) > 80 || strings.ContainsAny(options.Comment, "\r\n\x00") {
		return errors.New("firewall comment must be at most 80 control-free characters")
	}
	if err := validateFirewallAddress("source", options.Source); err != nil {
		return err
	}
	if err := validateFirewallAddress("destination", options.Target); err != nil {
		return err
	}
	if options.Node != "" {
		if options.Direction != "in" || !isFirewallAny(options.Target) {
			return errors.New("managed firewall rules support inbound source filters only")
		}
		if !isFirewallAny(options.Source) && !isIPv4AddressOrCIDR(options.Source) {
			return errors.New("managed firewall source must be an IPv4 address or CIDR")
		}
		if options.Protocol == "any" && options.Port != "" {
			return errors.New("managed any-protocol rules cannot select a port")
		}
		if options.Port != "" {
			port, err := strconv.Atoi(options.Port)
			if err != nil || port < 1 || port > 65535 {
				return errors.New("managed firewall port must be between 1 and 65535")
			}
		}
		return nil
	}
	if len(options.Port) > 20 {
		return errors.New("local firewall port must be a bounded port, range, or service name")
	}
	for _, character := range options.Port {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune(":/_-", character) {
			continue
		}
		return errors.New("local firewall port must be a bounded port, range, or service name")
	}
	return nil
}

func loadRemoteFirewallInventory(ctx context.Context, client *apiClient, node string) (remoteFirewallInventory, error) {
	inventory, err := requestJSON[remoteFirewallInventory](ctx, client.withTimeout(45*time.Second), http.MethodGet,
		"/api/nodes/"+url.PathEscape(strings.TrimSpace(node))+"/firewall", nil, true)
	if err != nil {
		return remoteFirewallInventory{}, err
	}
	if !firewallRevisionPattern.MatchString(inventory.Revision) {
		return remoteFirewallInventory{}, errors.New("managed firewall inventory returned an invalid revision")
	}
	return inventory, nil
}

func validateFirewallAddress(label, value string) error {
	if isFirewallAny(value) {
		return nil
	}
	if net.ParseIP(value) != nil {
		return nil
	}
	if _, _, err := net.ParseCIDR(value); err == nil {
		return nil
	}
	return fmt.Errorf("firewall %s must be any, an IP address, or CIDR", label)
}

func isIPv4AddressOrCIDR(value string) bool {
	if ip := net.ParseIP(value); ip != nil {
		return ip.To4() != nil
	}
	ip, _, err := net.ParseCIDR(value)
	return err == nil && ip.To4() != nil
}

func isFirewallAny(value string) bool {
	value = strings.TrimSpace(value)
	return value == "" || strings.EqualFold(value, "any") || strings.EqualFold(value, "Anywhere")
}

func normalizeFirewallAny(value, fallback string) string {
	if isFirewallAny(value) {
		return fallback
	}
	return value
}
