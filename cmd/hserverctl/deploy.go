package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/remotenodes"
)

var deployServiceNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
var deployEnvironmentKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)

const maxCLIDeployScriptBytes = 64 << 10

func runDeploy(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl deploy templates|targets|target|environment|domains|domain|staging|revision|preflight|history|logs|run|rollback|services|service|remote")
	}
	switch args[0] {
	case "templates":
		if len(args) != 1 {
			return errors.New("usage: hserverctl deploy templates")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/deploy/templates", nil, true)
	case "targets":
		if len(args) != 1 {
			return errors.New("usage: hserverctl deploy targets")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/deploy/targets", nil, true)
	case "target":
		return runDeployTarget(ctx, client, args[1:], out)
	case "environment":
		return runDeployEnvironment(ctx, client, args[1:], out)
	case "domain":
		return runDeployDomain(ctx, client, args[1:], out)
	case "staging":
		return runDeployStaging(ctx, client, args[1:], out)
	case "revision", "preflight", "services", "domains":
		if len(args) != 2 {
			return fmt.Errorf("usage: hserverctl deploy %s TARGET", args[0])
		}
		target, err := positiveDeployID("target", args[1])
		if err != nil {
			return err
		}
		endpoint := "/api/deploy/targets/" + strconv.FormatInt(target, 10) + "/" + args[0]
		return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
	case "history":
		return runDeployHistory(ctx, client, args[1:], out)
	case "logs":
		if len(args) != 2 {
			return errors.New("usage: hserverctl deploy logs RUN")
		}
		runID, err := positiveDeployID("run", args[1])
		if err != nil {
			return err
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/deploy/history/"+strconv.FormatInt(runID, 10)+"/logs", nil, true)
	case "run", "rollback":
		return runDeployAction(ctx, client, args[0], args[1:], out)
	case "service":
		return runDeployService(ctx, client, args[1:], out)
	case "remote":
		return runDeployRemote(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown deploy command %q", args[0])
	}
}

func runDeployRemote(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl deploy remote targets|jobs|action|domains|domain-ensure|domain-create|domain-delete|domain-health|tls-provision|tls-renew|tls-delete")
	}

	switch args[0] {
	case "targets", "jobs":
		flags := flag.NewFlagSet("deploy remote "+args[0], flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		node := flags.String("node", "", "managed node ID")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 0 {
			return fmt.Errorf("usage: hserverctl deploy remote %s --node NODE", args[0])
		}
		nodeID := strings.TrimSpace(*node)
		if nodeID == "" {
			return fmt.Errorf("usage: hserverctl deploy remote %s --node NODE", args[0])
		}
		endpoint := remoteDeployEndpoint(nodeID)
		if args[0] == "jobs" {
			endpoint += "/jobs"
		}
		requestClient := client
		if args[0] == "targets" {
			// The panel waits for the managed agent's bounded inventory
			// observation, which may take longer than the CLI default timeout.
			requestClient = client.withTimeout(time.Minute)
		}
		return printRequest(ctx, requestClient, out, http.MethodGet, endpoint, nil, true)

	case "action":
		flags := flag.NewFlagSet("deploy remote action", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		confirmed := flags.Bool("confirm", false, "confirm the managed deployment action")
		node := flags.String("node", "", "managed node ID")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		positional := flags.Args()
		if len(positional) != 2 {
			return errors.New("usage: hserverctl deploy remote action --confirm --node NODE TARGET ACTION")
		}
		if !*confirmed {
			return errors.New("remote deployment action requires explicit --confirm")
		}
		nodeID := strings.TrimSpace(*node)
		if nodeID == "" {
			return errors.New("remote deployment action requires a non-empty --node")
		}
		targetID := strings.TrimSpace(positional[0])
		if targetID == "" {
			return errors.New("remote deployment action requires a non-empty target")
		}
		action := positional[1]

		// Always re-read the agent-reported inventory immediately before a
		// mutation. The command must not rely on a stale TUI or prior CLI read.
		inventoryEndpoint := remoteDeployEndpoint(nodeID)
		targets, err := requestJSON[[]remotenodes.RemoteDeployTarget](ctx, client.withTimeout(time.Minute), http.MethodGet, inventoryEndpoint, nil, true)
		if err != nil {
			return fmt.Errorf("refresh remote deployment targets: %w", err)
		}
		target, found := findRemoteDeployTarget(targets, targetID)
		if !found {
			return fmt.Errorf("remote deployment target %q was not found in the refreshed inventory", targetID)
		}
		if !target.Eligible {
			return fmt.Errorf("remote deployment target %q is not eligible", targetID)
		}
		if !validCLIRemoteDeployAction(action) {
			return fmt.Errorf("unsupported remote deployment action %q", action)
		}
		if !remoteDeployActionAdvertised(target.Actions, action) {
			return fmt.Errorf("remote deployment action %q is not advertised for target %q", action, targetID)
		}

		endpoint := inventoryEndpoint + "/" + url.PathEscape(targetID) + "/actions/" + url.PathEscape(action)
		// The agent endpoint accepts an empty POST body. Keep the API's JSON
		// receipt untouched instead of decoding or wrapping it locally.
		return printRequest(ctx, client.withTimeout(2*time.Minute), out, http.MethodPost, endpoint, nil, true)
	case "domains":
		return runRemoteDeployDomains(ctx, client, args[1:], out)
	case "domain-ensure":
		return runRemoteDeployDomainEnsure(ctx, client, args[1:], out)
	case "domain-create":
		return runRemoteDeployDomainAction(ctx, client, "domain-create", "create", args[1:], out)
	case "domain-delete":
		return runRemoteDeployDomainAction(ctx, client, "domain-delete", "delete", args[1:], out)
	case "domain-health":
		return runRemoteDeployDomainHealth(ctx, client, args[1:], out)
	case "tls-provision":
		return runRemoteDeployDomainTLSProvision(ctx, client, args[1:], out)
	case "tls-renew":
		return runRemoteDeployDomainTLSAction(ctx, client, "renew", args[1:], out)
	case "tls-delete":
		return runRemoteDeployDomainTLSAction(ctx, client, "delete", args[1:], out)

	default:
		return fmt.Errorf("unknown deploy remote command %q", args[0])
	}
}

const (
	remoteDeployDomainReadWait    = 2 * time.Minute
	remoteDeployDomainActionWait  = 7 * time.Minute
	remoteDeployDomainReadMaximum = 2 * time.Minute
	remoteDeployDomainMaximum     = 7 * time.Minute
)

var remoteDeployDomainRevisionPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// remoteDeployDomainEnsureInventoryItem deliberately contains only the
// revision-aware fields needed for the client-side CAS preflight. The full
// domain observation remains an API concern and must not be echoed from this
// read response by the ensure command.
type remoteDeployDomainEnsureInventoryItem struct {
	Domain   string `json:"domain"`
	Revision string `json:"revision"`
}

type remoteDeployDomainEnsureRequest struct {
	ExpectedRevision string `json:"expected_revision"`
	Confirmed        bool   `json:"confirmed"`
}

// remoteDeployDomainEnsureObservation is the bounded, typed receipt surface
// for domain-ensure. Unknown response fields are intentionally ignored rather
// than copied into a map or json.RawMessage, so server-side/raw fields cannot
// escape through the CLI output.
type remoteDeployDomainEnsureObservation struct {
	TargetID         string `json:"target_id,omitempty"`
	Domain           string `json:"domain,omitempty"`
	HostPort         int    `json:"host_port,omitempty"`
	DesiredHostPort  int    `json:"desired_host_port,omitempty"`
	Upstream         string `json:"upstream,omitempty"`
	Status           string `json:"status,omitempty"`
	Enabled          *bool  `json:"enabled,omitempty"`
	Revision         string `json:"revision,omitempty"`
	Message          string `json:"message,omitempty"`
	TLSStatus        string `json:"tls_status,omitempty"`
	TLSExpiresAt     string `json:"tls_expires_at,omitempty"`
	TLSDaysRemaining int    `json:"tls_days_remaining,omitempty"`
	TLSMessage       string `json:"tls_message,omitempty"`
	UpdatedAt        string `json:"updated_at,omitempty"`
}

type remoteDeployDomainEnsureReceipt struct {
	Changed     bool                                `json:"changed"`
	Observation remoteDeployDomainEnsureObservation `json:"observation"`
}

type remoteDeployDomainEnsureReceiptEnvelope struct {
	Changed     *bool                                `json:"changed"`
	Observation *remoteDeployDomainEnsureObservation `json:"observation"`
}

type remoteDeployDomainEnsureOptions struct {
	Node   string
	Target string
	Domain string
	Wait   time.Duration
	Format string
}

func runRemoteDeployDomainEnsure(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	options, err := parseRemoteDeployDomainEnsureOptions(args)
	if err != nil {
		return err
	}

	// This is intentionally the first network request. It obtains the
	// agent-reported project-domain revision immediately before the PUT and
	// avoids reusing a stale TUI/CLI observation.
	inventory, err := requestJSON[[]remoteDeployDomainEnsureInventoryItem](
		ctx,
		client.withTimeout(options.Wait),
		http.MethodGet,
		remoteDeployDomainsEndpoint(options.Node, options.Target),
		nil,
		true,
	)
	if err != nil {
		return fmt.Errorf("refresh remote deployment domain inventory: %s", clientErrorMessage(err))
	}
	expectedRevision, err := remoteDeployDomainEnsureExpectedRevision(inventory, options.Domain)
	if err != nil {
		return err
	}

	payload := remoteDeployDomainEnsureRequest{ExpectedRevision: expectedRevision, Confirmed: true}
	raw, err := client.withTimeout(options.Wait).request(
		ctx,
		http.MethodPut,
		remoteDeployDomainEndpoint(options.Node, options.Target, options.Domain),
		payload,
		true,
	)
	if err != nil {
		// A stale revision or a request timeout may mean that the agent already
		// changed the filesystem. Never retry the same revision automatically;
		// make the refresh boundary explicit to the operator instead.
		return errors.New("remote deployment domain ensure was not retried; refresh the project-domain inventory before retrying: " + clientErrorMessage(err))
	}

	receipt, err := decodeRemoteDeployDomainEnsureReceipt(raw, options.Domain)
	if err != nil {
		return err
	}
	if options.Format == "json" {
		return printJSONValue(out, receipt)
	}
	return writeRemoteDeployDomainEnsureTable(out, receipt)
}

func parseRemoteDeployDomainEnsureOptions(args []string) (remoteDeployDomainEnsureOptions, error) {
	flags := flag.NewFlagSet("deploy remote domain-ensure", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm the managed project-domain ensure")
	node := flags.String("node", "", "managed node ID")
	wait := flags.Duration("wait", remoteDeployDomainActionWait, "maximum managed project-domain ensure wait")
	format := flags.String("format", "json", "output format: json, text, or table")
	if err := flags.Parse(args); err != nil {
		return remoteDeployDomainEnsureOptions{}, err
	}
	if len(flags.Args()) != 2 {
		return remoteDeployDomainEnsureOptions{}, errors.New("usage: hserverctl deploy remote domain-ensure --confirm --node NODE [--wait DURATION] [--format json|text|table] TARGET DOMAIN")
	}
	if !*confirmed {
		return remoteDeployDomainEnsureOptions{}, errors.New("remote deployment domain-ensure requires explicit --confirm")
	}
	if *wait <= 0 || *wait > remoteDeployDomainMaximum {
		return remoteDeployDomainEnsureOptions{}, fmt.Errorf("remote deployment domain-ensure wait must be greater than zero and at most %s", remoteDeployDomainMaximum)
	}
	cleanFormat, err := normalizeRemoteDeployDomainEnsureFormat(*format)
	if err != nil {
		return remoteDeployDomainEnsureOptions{}, err
	}
	cleanNode := strings.TrimSpace(*node)
	if cleanNode == "" {
		return remoteDeployDomainEnsureOptions{}, errors.New("remote deployment domain-ensure requires a non-empty --node")
	}
	cleanTarget := strings.TrimSpace(flags.Args()[0])
	if cleanTarget == "" {
		return remoteDeployDomainEnsureOptions{}, errors.New("remote deployment domain-ensure requires a non-empty target")
	}
	cleanDomain, err := validateCLIDeployProjectDomain(flags.Args()[1])
	if err != nil {
		return remoteDeployDomainEnsureOptions{}, err
	}
	return remoteDeployDomainEnsureOptions{Node: cleanNode, Target: cleanTarget, Domain: cleanDomain, Wait: *wait, Format: cleanFormat}, nil
}

func normalizeRemoteDeployDomainEnsureFormat(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "json":
		return "json", nil
	case "table", "text":
		return "table", nil
	default:
		return "", errors.New("remote deployment domain-ensure format must be json, text, or table")
	}
}

func remoteDeployDomainEnsureExpectedRevision(inventory []remoteDeployDomainEnsureInventoryItem, domain string) (string, error) {
	var match *remoteDeployDomainEnsureInventoryItem
	for index := range inventory {
		item := &inventory[index]
		if item.Domain != domain {
			continue
		}
		if match != nil {
			return "", errors.New("remote deployment domain inventory contains duplicate domain observations")
		}
		match = item
	}
	if match == nil {
		return "absent", nil
	}
	if !remoteDeployDomainRevisionPattern.MatchString(match.Revision) {
		return "", errors.New("remote deployment domain inventory returned a revision that is not a lowercase 64-character hexadecimal value")
	}
	return match.Revision, nil
}

func decodeRemoteDeployDomainEnsureReceipt(raw []byte, expectedDomain string) (remoteDeployDomainEnsureReceipt, error) {
	var envelope remoteDeployDomainEnsureReceiptEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return remoteDeployDomainEnsureReceipt{}, fmt.Errorf("decode remote deployment domain-ensure receipt: %w", err)
	}
	if envelope.Changed == nil || envelope.Observation == nil {
		return remoteDeployDomainEnsureReceipt{}, errors.New("remote deployment domain-ensure returned an incomplete typed receipt")
	}
	if envelope.Observation.Domain != "" && envelope.Observation.Domain != expectedDomain {
		return remoteDeployDomainEnsureReceipt{}, errors.New("remote deployment domain-ensure receipt does not match the requested domain")
	}
	if envelope.Observation.Revision != "" && !remoteDeployDomainRevisionPattern.MatchString(envelope.Observation.Revision) {
		return remoteDeployDomainEnsureReceipt{}, errors.New("remote deployment domain-ensure receipt returned an invalid revision")
	}
	return remoteDeployDomainEnsureReceipt{Changed: *envelope.Changed, Observation: *envelope.Observation}, nil
}

func writeRemoteDeployDomainEnsureTable(out io.Writer, receipt remoteDeployDomainEnsureReceipt) error {
	if _, err := fmt.Fprintln(out, "FIELD\tVALUE"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "changed\t%t\n", receipt.Changed); err != nil {
		return err
	}
	observation := receipt.Observation
	rows := []struct {
		name  string
		value string
	}{
		{name: "observation.target_id", value: observation.TargetID},
		{name: "observation.domain", value: observation.Domain},
		{name: "observation.host_port", value: strconv.Itoa(observation.HostPort)},
		{name: "observation.desired_host_port", value: strconv.Itoa(observation.DesiredHostPort)},
		{name: "observation.upstream", value: observation.Upstream},
		{name: "observation.status", value: observation.Status},
		{name: "observation.revision", value: observation.Revision},
		{name: "observation.message", value: observation.Message},
		{name: "observation.tls_status", value: observation.TLSStatus},
		{name: "observation.tls_expires_at", value: observation.TLSExpiresAt},
		{name: "observation.tls_days_remaining", value: strconv.Itoa(observation.TLSDaysRemaining)},
		{name: "observation.tls_message", value: observation.TLSMessage},
		{name: "observation.updated_at", value: observation.UpdatedAt},
	}
	if observation.Enabled != nil {
		rows = append(rows, struct {
			name  string
			value string
		}{name: "observation.enabled", value: strconv.FormatBool(*observation.Enabled)})
	}
	for _, row := range rows {
		if row.value == "" || (strings.HasPrefix(row.name, "observation.") && strings.HasSuffix(row.name, "_port") && row.value == "0") || (row.name == "observation.tls_days_remaining" && row.value == "0") {
			continue
		}
		if _, err := fmt.Fprintf(out, "%s\t%s\n", row.name, row.value); err != nil {
			return err
		}
	}
	return nil
}

func runRemoteDeployDomains(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("deploy remote domains", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl deploy remote domains --node NODE TARGET")
	}
	nodeID := strings.TrimSpace(*node)
	if nodeID == "" {
		return errors.New("remote deployment domains requires a non-empty --node")
	}
	targetID := strings.TrimSpace(flags.Args()[0])
	if targetID == "" {
		return errors.New("remote deployment domains requires a non-empty target")
	}
	return printRequest(ctx, client.withTimeout(remoteDeployDomainReadWait), out, http.MethodGet, remoteDeployDomainsEndpoint(nodeID, targetID), nil, true)
}

func runRemoteDeployDomainAction(ctx context.Context, client *apiClient, command, action string, args []string, out io.Writer) error {
	options, err := parseRemoteDeployDomainOptions(command, args, true, false, remoteDeployDomainReadWait, remoteDeployDomainReadMaximum)
	if err != nil {
		return err
	}
	if _, err := refreshRemoteDeployTarget(ctx, client, options.Node, options.Target, agenthub.CapabilityDeployDomainRead, agenthub.CapabilityDeployDomainAction); err != nil {
		return err
	}
	endpoint := remoteDeployDomainEndpoint(options.Node, options.Target, options.Domain)
	if action == "delete" {
		return printRequest(ctx, client.withTimeout(options.Wait), out, http.MethodDelete, endpoint, nil, true)
	}
	return printRequest(ctx, client.withTimeout(options.Wait), out, http.MethodPost, remoteDeployDomainsEndpoint(options.Node, options.Target), remotenodes.CreateRemoteDeployDomainRequest{Domain: options.Domain}, true)
}

func runRemoteDeployDomainHealth(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	options, err := parseRemoteDeployDomainOptions("health", args, false, false, remoteDeployDomainReadWait, remoteDeployDomainReadMaximum)
	if err != nil {
		return err
	}
	return printRequest(ctx, client.withTimeout(options.Wait), out, http.MethodGet, remoteDeployDomainEndpoint(options.Node, options.Target, options.Domain)+"/health", nil, true)
}

func runRemoteDeployDomainTLSProvision(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	options, err := parseRemoteDeployDomainOptions("tls-provision", args, true, true, remoteDeployDomainActionWait, remoteDeployDomainMaximum)
	if err != nil {
		return err
	}
	if _, err := refreshRemoteDeployTarget(ctx, client, options.Node, options.Target, agenthub.CapabilityDeployDomainRead, agenthub.CapabilityDeployDomainAction); err != nil {
		return err
	}
	payload := remotenodes.EnableRemoteDeployDomainTLSRequest{Email: options.Email}
	return printRequest(ctx, client.withTimeout(options.Wait), out, http.MethodPost, remoteDeployDomainEndpoint(options.Node, options.Target, options.Domain)+"/tls", payload, true)
}

func runRemoteDeployDomainTLSAction(ctx context.Context, client *apiClient, action string, args []string, out io.Writer) error {
	options, err := parseRemoteDeployDomainOptions("tls-"+action, args, true, false, remoteDeployDomainActionWait, remoteDeployDomainMaximum)
	if err != nil {
		return err
	}
	if _, err := refreshRemoteDeployTarget(ctx, client, options.Node, options.Target, agenthub.CapabilityDeployDomainRead, agenthub.CapabilityDeployDomainAction); err != nil {
		return err
	}
	var endpoint string
	var method string
	if action == "renew" {
		endpoint = remoteDeployDomainEndpoint(options.Node, options.Target, options.Domain) + "/tls/renew"
		method = http.MethodPost
	} else {
		endpoint = remoteDeployDomainEndpoint(options.Node, options.Target, options.Domain) + "/tls"
		method = http.MethodDelete
	}
	return printRequest(ctx, client.withTimeout(options.Wait), out, method, endpoint, nil, true)
}

type remoteDeployDomainOptions struct {
	Node   string
	Target string
	Domain string
	Email  string
	Wait   time.Duration
}

func parseRemoteDeployDomainOptions(command string, args []string, mutation, emailAllowed bool, defaultWait, maximumWait time.Duration) (remoteDeployDomainOptions, error) {
	flags := flag.NewFlagSet("deploy remote "+command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm the managed project-domain action")
	node := flags.String("node", "", "managed node ID")
	wait := flags.Duration("wait", defaultWait, "maximum managed project-domain request wait")
	email := flags.String("email", "", "optional ACME account email")
	if err := flags.Parse(args); err != nil {
		return remoteDeployDomainOptions{}, err
	}
	usage := fmt.Sprintf("usage: hserverctl deploy remote %s --confirm --node NODE [--wait DURATION] TARGET DOMAIN", command)
	if emailAllowed {
		usage = fmt.Sprintf("usage: hserverctl deploy remote %s --confirm --node NODE [--email EMAIL] [--wait DURATION] TARGET DOMAIN", command)
	}
	if len(flags.Args()) != 2 {
		return remoteDeployDomainOptions{}, errors.New(usage)
	}
	if mutation && !*confirmed {
		return remoteDeployDomainOptions{}, fmt.Errorf("remote deployment %s requires explicit --confirm", command)
	}
	if !mutation && *confirmed {
		return remoteDeployDomainOptions{}, fmt.Errorf("--confirm is only valid for managed deployment mutations")
	}
	if !emailAllowed && strings.TrimSpace(*email) != "" {
		return remoteDeployDomainOptions{}, fmt.Errorf("--email is only valid for remote tls-provision")
	}
	if *wait <= 0 || *wait > maximumWait {
		return remoteDeployDomainOptions{}, fmt.Errorf("remote deployment %s wait must be greater than zero and at most %s", command, maximumWait)
	}
	nodeID := strings.TrimSpace(*node)
	if nodeID == "" {
		return remoteDeployDomainOptions{}, fmt.Errorf("remote deployment %s requires a non-empty --node", command)
	}
	targetID := strings.TrimSpace(flags.Args()[0])
	if targetID == "" {
		return remoteDeployDomainOptions{}, fmt.Errorf("remote deployment %s requires a non-empty target", command)
	}
	domain, err := validateCLIDeployProjectDomain(flags.Args()[1])
	if err != nil {
		return remoteDeployDomainOptions{}, err
	}
	accountEmail := strings.TrimSpace(*email)
	if emailAllowed && accountEmail != "" {
		address, err := mail.ParseAddress(accountEmail)
		if err != nil || address.Address != accountEmail || address.Name != "" {
			return remoteDeployDomainOptions{}, errors.New("invalid ACME account email")
		}
	}
	return remoteDeployDomainOptions{Node: nodeID, Target: targetID, Domain: domain, Email: accountEmail, Wait: *wait}, nil
}

func refreshRemoteDeployTarget(ctx context.Context, client *apiClient, nodeID, targetID string, requiredCapabilities ...string) (remotenodes.RemoteDeployTarget, error) {
	node, err := requestJSON[managedNodeEnvelope](ctx, client.withTimeout(time.Minute), http.MethodGet, "/api/nodes/"+url.PathEscape(nodeID), nil, true)
	if err != nil {
		return remotenodes.RemoteDeployTarget{}, fmt.Errorf("refresh managed node %q: %w", nodeID, err)
	}
	if node.ID != nodeID {
		return remotenodes.RemoteDeployTarget{}, fmt.Errorf("managed-node response identity %q does not match requested node %q", node.ID, nodeID)
	}
	if !node.Online {
		return remotenodes.RemoteDeployTarget{}, errors.New("managed node is offline")
	}
	for _, capability := range requiredCapabilities {
		if !managedNodeHasCapability(node, capability) {
			return remotenodes.RemoteDeployTarget{}, fmt.Errorf("managed agent does not advertise %s", capability)
		}
	}
	endpoint := remoteDeployEndpoint(nodeID)
	targets, err := requestJSON[[]remotenodes.RemoteDeployTarget](ctx, client.withTimeout(time.Minute), http.MethodGet, endpoint, nil, true)
	if err != nil {
		return remotenodes.RemoteDeployTarget{}, fmt.Errorf("refresh remote deployment targets: %w", err)
	}
	target, found := findRemoteDeployTarget(targets, targetID)
	if !found {
		return remotenodes.RemoteDeployTarget{}, fmt.Errorf("remote deployment target %q was not found in the refreshed inventory", targetID)
	}
	if !target.Eligible {
		return remotenodes.RemoteDeployTarget{}, fmt.Errorf("remote deployment target %q is not eligible", targetID)
	}
	return target, nil
}

func remoteDeployEndpoint(nodeID string) string {
	return "/api/nodes/" + url.PathEscape(nodeID) + "/deploy"
}

func remoteDeployDomainsEndpoint(nodeID, targetID string) string {
	return remoteDeployEndpoint(nodeID) + "/" + url.PathEscape(targetID) + "/domains"
}

func remoteDeployDomainEndpoint(nodeID, targetID, domain string) string {
	return remoteDeployDomainsEndpoint(nodeID, targetID) + "/" + url.PathEscape(domain)
}

func validCLIRemoteDeployAction(action string) bool {
	switch action {
	case "preflight", "deploy", "restart", "rollback":
		return true
	default:
		return false
	}
}

func remoteDeployActionAdvertised(actions []string, requested string) bool {
	for _, action := range actions {
		if action == requested {
			return true
		}
	}
	return false
}

func runDeployDomain(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl deploy domain create|delete|health|tls")
	}
	switch args[0] {
	case "create":
		flags := flag.NewFlagSet("deploy domain create", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		confirmed := flags.Bool("confirm", false, "confirm project domain creation")
		service := flags.String("service", "", "Compose service identity")
		hostPort := flags.Int("host-port", 0, "published loopback host port")
		wait := flags.Duration("wait", 2*time.Minute, "maximum domain activation wait")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 2 {
			return errors.New("usage: hserverctl deploy domain create --confirm --service SERVICE --host-port PORT [--wait DURATION] TARGET DOMAIN")
		}
		if !*confirmed {
			return errors.New("deployment project domain creation requires explicit --confirm")
		}
		if *wait <= 0 {
			return errors.New("deployment project domain creation wait must be greater than zero")
		}
		target, err := positiveDeployID("target", flags.Args()[0])
		if err != nil {
			return err
		}
		domain, err := validateCLIDeployProjectDomain(flags.Args()[1])
		if err != nil {
			return err
		}
		serviceName := strings.TrimSpace(*service)
		if !validCLIDeployServiceName(serviceName) {
			return errors.New("invalid Compose service name")
		}
		if *hostPort < 1 || *hostPort > 65535 {
			return errors.New("project domain host port must be between 1 and 65535")
		}
		payload := models.CreateDeployDomainRequest{Domain: domain, Service: serviceName, HostPort: *hostPort}
		return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, deployDomainsEndpoint(target), payload, true)
	case "delete":
		flags := flag.NewFlagSet("deploy domain delete", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		confirmed := flags.Bool("confirm", false, "confirm project domain deletion")
		wait := flags.Duration("wait", 2*time.Minute, "maximum domain deletion wait")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 2 {
			return errors.New("usage: hserverctl deploy domain delete --confirm [--wait DURATION] TARGET DOMAIN_ID")
		}
		if !*confirmed {
			return errors.New("deployment project domain deletion requires explicit --confirm")
		}
		if *wait <= 0 {
			return errors.New("deployment project domain deletion wait must be greater than zero")
		}
		target, domain, err := deployDomainIdentity(flags.Args())
		if err != nil {
			return err
		}
		raw, err := client.withTimeout(*wait).request(ctx, http.MethodDelete, deployDomainEndpoint(target, domain), nil, true)
		if err != nil {
			return err
		}
		if strings.TrimSpace(string(raw)) != "" {
			return errors.New("project domain delete returned an unexpected response body")
		}
		return printJSONValue(out, map[string]any{"message": "project domain deleted", "targetId": target, "domainId": domain})
	case "health":
		if len(args) != 3 {
			return errors.New("usage: hserverctl deploy domain health TARGET DOMAIN_ID")
		}
		target, domain, err := deployDomainIdentity(args[1:])
		if err != nil {
			return err
		}
		return printRequest(ctx, client, out, http.MethodGet, deployDomainEndpoint(target, domain)+"/health", nil, true)
	case "tls":
		return runDeployDomainTLS(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown deploy domain command %q", args[0])
	}
}

func runDeployDomainTLS(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 || (args[0] != "enable" && args[0] != "disable") {
		return errors.New("usage: hserverctl deploy domain tls enable|disable --confirm [OPTIONS] TARGET DOMAIN_ID")
	}
	action := args[0]
	flags := flag.NewFlagSet("deploy domain tls "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm project domain TLS mutation")
	waitDefault := 2 * time.Minute
	if action == "enable" {
		waitDefault = 7 * time.Minute
	}
	wait := flags.Duration("wait", waitDefault, "maximum TLS mutation wait")
	email := flags.String("email", "", "optional ACME account email")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if len(flags.Args()) != 2 {
		return fmt.Errorf("usage: hserverctl deploy domain tls %s --confirm [--email EMAIL] [--wait DURATION] TARGET DOMAIN_ID", action)
	}
	if !*confirmed {
		return errors.New("deployment project domain TLS mutation requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("deployment project domain TLS wait must be greater than zero")
	}
	target, domain, err := deployDomainIdentity(flags.Args())
	if err != nil {
		return err
	}
	cleanEmail := strings.TrimSpace(*email)
	if action == "disable" && cleanEmail != "" {
		return errors.New("--email is only valid when enabling project domain TLS")
	}
	var payload any
	if cleanEmail != "" {
		address, parseErr := mail.ParseAddress(cleanEmail)
		if len(cleanEmail) > 254 || parseErr != nil || address.Address != cleanEmail || address.Name != "" {
			return errors.New("ACME email must be a plain valid address of at most 254 bytes")
		}
		payload = models.EnableDeployDomainTLSRequest{Email: cleanEmail}
	}
	method := http.MethodPost
	if action == "disable" {
		method = http.MethodDelete
	}
	return printRequest(ctx, client.withTimeout(*wait), out, method, deployDomainEndpoint(target, domain)+"/tls", payload, true)
}

func deployDomainsEndpoint(target int64) string {
	return "/api/deploy/targets/" + strconv.FormatInt(target, 10) + "/domains"
}

func deployDomainEndpoint(target, domain int64) string {
	return deployDomainsEndpoint(target) + "/" + strconv.FormatInt(domain, 10)
}

func deployDomainIdentity(values []string) (int64, int64, error) {
	target, err := positiveDeployID("target", values[0])
	if err != nil {
		return 0, 0, err
	}
	domain, err := positiveDeployID("domain", values[1])
	if err != nil {
		return 0, 0, err
	}
	return target, domain, nil
}

func validateCLIDeployProjectDomain(value string) (string, error) {
	domain, err := validateLocalDomainName(value)
	if err != nil {
		return "", errors.New("project domain must be a valid ASCII hostname")
	}
	return domain, nil
}

func validCLIDeployServiceName(value string) bool {
	return deployServiceNamePattern.MatchString(value) && !strings.Contains(value, "..")
}

func runDeployEnvironment(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl deploy environment list|set|delete")
	}
	switch args[0] {
	case "list":
		if len(args) != 2 {
			return errors.New("usage: hserverctl deploy environment list TARGET")
		}
		target, err := positiveDeployID("target", args[1])
		if err != nil {
			return err
		}
		return printRequest(ctx, client, out, http.MethodGet, deployEnvironmentEndpoint(target), nil, true)
	case "set":
		flags := flag.NewFlagSet("deploy environment set", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		confirmed := flags.Bool("confirm", false, "confirm environment value creation or replacement")
		valueFile := flags.String("value-file", "", "protected file containing the write-only value")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 2 {
			return errors.New("usage: hserverctl deploy environment set --confirm --value-file PATH TARGET KEY")
		}
		if !*confirmed {
			return errors.New("deployment environment update requires explicit --confirm")
		}
		target, err := positiveDeployID("target", flags.Args()[0])
		if err != nil {
			return err
		}
		key, err := validCLIDeployEnvironmentKey(flags.Args()[1])
		if err != nil {
			return err
		}
		if strings.TrimSpace(*valueFile) == "" {
			return errors.New("deployment environment update requires --value-file")
		}
		value, err := readCLIDeployEnvironmentValue(*valueFile)
		if err != nil {
			return err
		}
		payload := map[string]string{"key": key, "value": value}
		return printRequest(ctx, client, out, http.MethodPut, deployEnvironmentEndpoint(target), payload, true)
	case "delete":
		flags := flag.NewFlagSet("deploy environment delete", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		confirmed := flags.Bool("confirm", false, "confirm environment variable deletion")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 2 {
			return errors.New("usage: hserverctl deploy environment delete --confirm TARGET KEY")
		}
		if !*confirmed {
			return errors.New("deployment environment deletion requires explicit --confirm")
		}
		target, err := positiveDeployID("target", flags.Args()[0])
		if err != nil {
			return err
		}
		key, err := validCLIDeployEnvironmentKey(flags.Args()[1])
		if err != nil {
			return err
		}
		return printRequest(ctx, client, out, http.MethodDelete, deployEnvironmentEndpoint(target)+"/"+url.PathEscape(key), nil, true)
	default:
		return fmt.Errorf("unknown deploy environment command %q", args[0])
	}
}

func deployEnvironmentEndpoint(target int64) string {
	return "/api/deploy/targets/" + strconv.FormatInt(target, 10) + "/environment"
}

func validCLIDeployEnvironmentKey(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !deployEnvironmentKeyPattern.MatchString(value) {
		return "", errors.New("invalid deployment environment key")
	}
	return value, nil
}

func readCLIDeployEnvironmentValue(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect environment value file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("environment value file must be a regular file and not a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("environment value file must not be accessible by group or others")
	}
	if info.Size() > maxCLIDeployScriptBytes {
		return "", fmt.Errorf("environment value file exceeds %d bytes", maxCLIDeployScriptBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read environment value file: %w", err)
	}
	if len(data) > maxCLIDeployScriptBytes || !utf8.Valid(data) {
		return "", fmt.Errorf("environment value file must be UTF-8 text of at most %d bytes", maxCLIDeployScriptBytes)
	}
	value := string(data)
	if strings.HasSuffix(value, "\r\n") {
		value = strings.TrimSuffix(value, "\r\n")
	} else if strings.HasSuffix(value, "\n") {
		value = strings.TrimSuffix(value, "\n")
	}
	if strings.ContainsAny(value, "\x00\r\n'") {
		return "", errors.New("environment value contains an unsupported NUL, newline, carriage return, or single quote")
	}
	return value, nil
}

func runDeployTarget(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl deploy target create|update|delete")
	}
	switch args[0] {
	case "create":
		return runDeployTargetCreate(ctx, client, args[1:], out)
	case "update":
		return runDeployTargetUpdate(ctx, client, args[1:], out)
	case "delete":
		flags := flag.NewFlagSet("deploy target delete", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		confirmed := flags.Bool("confirm", false, "confirm deployment target deletion")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 1 {
			return errors.New("usage: hserverctl deploy target delete --confirm TARGET")
		}
		if !*confirmed {
			return errors.New("deployment target deletion requires explicit --confirm")
		}
		target, err := positiveDeployID("target", flags.Args()[0])
		if err != nil {
			return err
		}
		return printRequest(ctx, client, out, http.MethodDelete, "/api/deploy/targets/"+strconv.FormatInt(target, 10), nil, true)
	default:
		return fmt.Errorf("unknown deploy target command %q", args[0])
	}
}

func runDeployTargetUpdate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("deploy target update", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm deployment target update")
	name := flags.String("name", "", "replacement deployment target name")
	projectDir := flags.String("project-dir", "", "replacement absolute project directory")
	kind := flags.String("type", "", "replacement deployment type: compose or script")
	repo := flags.String("repo", "", "replacement credential-free repository URL; empty clears it")
	branch := flags.String("branch", "", "replacement Git branch")
	composeFile := flags.String("compose-file", "", "replacement relative Compose file; empty uses discovery")
	scriptFile := flags.String("script-file", "", "replacement local UTF-8 deploy script file")
	webhookProvider := flags.String("webhook-provider", "", "replacement webhook provider: github or gitlab")
	webhookTokenFile := flags.String("webhook-token-file", "", "protected replacement webhook token file")
	clearWebhookToken := flags.Bool("clear-webhook-token", false, "remove the configured webhook token")
	autoDeploy := flags.Bool("auto-deploy", false, "replacement automatic deployment state")
	active := flags.Bool("active", false, "replacement target active state")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl deploy target update --confirm [OPTIONS] TARGET")
	}
	if !*confirmed {
		return errors.New("deployment target update requires explicit --confirm")
	}
	targetID, err := positiveDeployID("target", flags.Args()[0])
	if err != nil {
		return err
	}
	provided := make(map[string]bool)
	flags.Visit(func(item *flag.Flag) { provided[item.Name] = true })
	delete(provided, "confirm")
	if len(provided) == 0 {
		return errors.New("deployment target update requires at least one replacement option")
	}
	if provided["webhook-token-file"] && provided["clear-webhook-token"] {
		return errors.New("--webhook-token-file and --clear-webhook-token cannot be used together")
	}
	if provided["name"] {
		value := strings.TrimSpace(*name)
		if value == "" || len(value) > 128 || strings.ContainsAny(value, "\r\n\x00") {
			return errors.New("deployment target name must be one line of at most 128 bytes")
		}
	}
	if provided["project-dir"] {
		value := strings.TrimSpace(*projectDir)
		if value == "" || strings.ContainsRune(value, '\x00') || !filepath.IsAbs(value) {
			return errors.New("deployment target project directory must be absolute")
		}
	}
	if provided["repo"] {
		if err := validateCLIDeployRepositoryURL(strings.TrimSpace(*repo)); err != nil {
			return err
		}
	}
	if provided["branch"] && !validCLIDeployBranch(strings.TrimSpace(*branch)) {
		return errors.New("deployment target branch is invalid")
	}
	if provided["type"] && strings.TrimSpace(*kind) != string(models.DeployKindCompose) && strings.TrimSpace(*kind) != string(models.DeployKindScript) {
		return errors.New("deployment target type must be compose or script")
	}
	if provided["compose-file"] && strings.TrimSpace(*composeFile) != "" && !validCLIDeployComposeFile(strings.TrimSpace(*composeFile)) {
		return errors.New("compose file must be a relative path inside the project directory")
	}
	if provided["webhook-provider"] && strings.TrimSpace(*webhookProvider) != string(models.DeployWebhookGitHub) && strings.TrimSpace(*webhookProvider) != string(models.DeployWebhookGitLab) {
		return errors.New("webhook provider must be github or gitlab")
	}

	var replacementScript string
	if provided["script-file"] {
		if strings.TrimSpace(*scriptFile) == "" {
			return errors.New("--script-file requires a file path")
		}
		replacementScript, err = readCLIDeployScript(*scriptFile)
		if err != nil {
			return err
		}
	}
	var replacementWebhookToken string
	if provided["webhook-token-file"] {
		if strings.TrimSpace(*webhookTokenFile) == "" {
			return errors.New("--webhook-token-file requires a file path")
		}
		replacementWebhookToken, err = readSecretFile(*webhookTokenFile, 4096)
		if err != nil {
			return fmt.Errorf("read webhook token file: %w", err)
		}
		if strings.ContainsAny(replacementWebhookToken, "\r\n\x00") {
			return errors.New("webhook token must be one line without NUL bytes")
		}
	}

	targets, err := requestJSON[[]models.DeployTarget](ctx, client, http.MethodGet, "/api/deploy/targets", nil, true)
	if err != nil {
		return fmt.Errorf("refresh deployment targets: %w", err)
	}
	var current *models.DeployTarget
	for i := range targets {
		if targets[i].ID == targetID {
			current = &targets[i]
			break
		}
	}
	if current == nil {
		return fmt.Errorf("deploy target %d was not found in the refreshed inventory", targetID)
	}
	if current.UpdatedAt.IsZero() {
		return errors.New("refreshed deployment target is missing updatedAt; update the panel before using target update")
	}

	request := models.UpdateDeployTargetRequest{
		Name:              current.Name,
		RepoURL:           current.RepoURL,
		Branch:            current.Branch,
		ProjectDir:        current.ProjectDir,
		DeployKind:        current.DeployKind,
		ComposeFile:       current.ComposeFile,
		DeployScript:      current.DeployScript,
		WebhookProvider:   current.WebhookProvider,
		AutoDeploy:        current.AutoDeploy,
		IsActive:          current.IsActive,
		ExpectedUpdatedAt: current.UpdatedAt,
	}
	if provided["name"] {
		request.Name = strings.TrimSpace(*name)
	}
	if provided["project-dir"] {
		request.ProjectDir = filepath.Clean(strings.TrimSpace(*projectDir))
	}
	if provided["repo"] {
		request.RepoURL = strings.TrimSpace(*repo)
	}
	if provided["branch"] {
		request.Branch = strings.TrimSpace(*branch)
	}
	if provided["type"] {
		request.DeployKind = models.DeployKind(strings.TrimSpace(*kind))
		if request.DeployKind == models.DeployKindCompose {
			request.DeployScript = ""
		} else {
			request.ComposeFile = ""
		}
	}
	if provided["compose-file"] {
		request.ComposeFile = strings.TrimSpace(*composeFile)
		if request.ComposeFile != "" {
			request.ComposeFile = filepath.Clean(request.ComposeFile)
		}
	}
	if provided["script-file"] {
		request.DeployScript = replacementScript
	}
	if request.DeployKind == models.DeployKindCompose {
		if provided["script-file"] {
			return errors.New("--script-file is only valid for script deployment targets")
		}
		request.DeployScript = ""
	} else {
		if provided["compose-file"] {
			return errors.New("--compose-file is only valid for compose deployment targets")
		}
		request.ComposeFile = ""
		if current.DeployKind != models.DeployKindScript && !provided["script-file"] {
			return errors.New("changing to a script deployment target requires --script-file")
		}
	}
	if provided["webhook-provider"] {
		request.WebhookProvider = models.DeployWebhookProvider(strings.TrimSpace(*webhookProvider))
	}
	if provided["webhook-provider"] && request.WebhookProvider != current.WebhookProvider && !provided["webhook-token-file"] {
		return errors.New("changing webhook provider requires --webhook-token-file")
	}
	if provided["webhook-token-file"] {
		request.WebhookToken = replacementWebhookToken
	}
	if provided["clear-webhook-token"] {
		request.ClearWebhookToken = *clearWebhookToken
	}
	if provided["auto-deploy"] {
		request.AutoDeploy = *autoDeploy
	}
	if provided["active"] {
		request.IsActive = *active
	}
	if request.WebhookProvider == models.DeployWebhookGitLab && request.WebhookToken != "" && !validCLIGitLabSigningSecret(request.WebhookToken) {
		return errors.New("GitLab webhook token must be a valid whsec_ signing token")
	}
	if request.ClearWebhookToken && request.AutoDeploy {
		return errors.New("clearing the webhook token requires --auto-deploy=false")
	}
	if request.AutoDeploy && request.WebhookToken == "" && current.WebhookStatus == models.DeployWebhookNotConfigured {
		return errors.New("automatic deployment requires --webhook-token-file")
	}
	if !deployTargetUpdateChanges(current, request) {
		return errors.New("requested values do not change the deployment target")
	}
	endpoint := "/api/deploy/targets/" + strconv.FormatInt(targetID, 10)
	return printRequest(ctx, client, out, http.MethodPut, endpoint, request, true)
}

func deployTargetUpdateChanges(current *models.DeployTarget, request models.UpdateDeployTargetRequest) bool {
	return current.Name != request.Name || current.RepoURL != request.RepoURL || current.Branch != request.Branch ||
		current.ProjectDir != request.ProjectDir || current.DeployKind != request.DeployKind || current.ComposeFile != request.ComposeFile ||
		current.DeployScript != request.DeployScript || current.WebhookProvider != request.WebhookProvider || request.WebhookToken != "" ||
		request.ClearWebhookToken || current.AutoDeploy != request.AutoDeploy || current.IsActive != request.IsActive
}

func runDeployTargetCreate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("deploy target create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm deployment target creation")
	name := flags.String("name", "", "deployment target name")
	projectDir := flags.String("project-dir", "", "absolute project directory")
	kind := flags.String("type", "compose", "deployment type: compose or script")
	repo := flags.String("repo", "", "credential-free HTTPS or SSH repository URL")
	branch := flags.String("branch", "main", "Git branch")
	composeFile := flags.String("compose-file", "", "relative Compose file path")
	scriptFile := flags.String("script-file", "", "local UTF-8 deploy script file")
	webhookProvider := flags.String("webhook-provider", "github", "webhook provider: github or gitlab")
	webhookTokenFile := flags.String("webhook-token-file", "", "protected webhook token file")
	autoDeploy := flags.Bool("auto-deploy", false, "enable signed webhook deployments")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl deploy target create --confirm --name NAME --project-dir PATH [OPTIONS]")
	}
	if !*confirmed {
		return errors.New("deployment target creation requires explicit --confirm")
	}

	cleanName := strings.TrimSpace(*name)
	if cleanName == "" || len(cleanName) > 128 || strings.ContainsAny(cleanName, "\r\n\x00") {
		return errors.New("deployment target name must be one line of at most 128 bytes")
	}
	cleanProjectDir := strings.TrimSpace(*projectDir)
	if cleanProjectDir == "" || strings.ContainsRune(cleanProjectDir, '\x00') || !filepath.IsAbs(cleanProjectDir) {
		return errors.New("deployment target project directory must be absolute")
	}
	cleanProjectDir = filepath.Clean(cleanProjectDir)
	cleanRepo := strings.TrimSpace(*repo)
	if err := validateCLIDeployRepositoryURL(cleanRepo); err != nil {
		return err
	}
	cleanBranch := strings.TrimSpace(*branch)
	if !validCLIDeployBranch(cleanBranch) {
		return errors.New("deployment target branch is invalid")
	}

	request := models.CreateDeployTargetRequest{
		Name:       cleanName,
		RepoURL:    cleanRepo,
		Branch:     cleanBranch,
		ProjectDir: cleanProjectDir,
		AutoDeploy: *autoDeploy,
	}
	switch strings.TrimSpace(*kind) {
	case string(models.DeployKindCompose):
		if strings.TrimSpace(*scriptFile) != "" {
			return errors.New("--script-file is only valid for script deployment targets")
		}
		request.DeployKind = models.DeployKindCompose
		request.ComposeFile = strings.TrimSpace(*composeFile)
		if request.ComposeFile != "" {
			if !validCLIDeployComposeFile(request.ComposeFile) {
				return errors.New("compose file must be a relative path inside the project directory")
			}
			request.ComposeFile = filepath.Clean(request.ComposeFile)
		}
	case string(models.DeployKindScript):
		if strings.TrimSpace(*composeFile) != "" {
			return errors.New("--compose-file is only valid for compose deployment targets")
		}
		if strings.TrimSpace(*scriptFile) == "" {
			return errors.New("script deployment targets require --script-file")
		}
		script, err := readCLIDeployScript(*scriptFile)
		if err != nil {
			return err
		}
		request.DeployKind = models.DeployKindScript
		request.DeployScript = script
	default:
		return errors.New("deployment target type must be compose or script")
	}

	switch strings.TrimSpace(*webhookProvider) {
	case string(models.DeployWebhookGitHub):
		request.WebhookProvider = models.DeployWebhookGitHub
	case string(models.DeployWebhookGitLab):
		request.WebhookProvider = models.DeployWebhookGitLab
	default:
		return errors.New("webhook provider must be github or gitlab")
	}
	if strings.TrimSpace(*webhookTokenFile) != "" {
		token, err := readSecretFile(*webhookTokenFile, 4096)
		if err != nil {
			return fmt.Errorf("read webhook token file: %w", err)
		}
		if strings.ContainsAny(token, "\r\n\x00") {
			return errors.New("webhook token must be one line without NUL bytes")
		}
		request.WebhookToken = token
	}
	if request.WebhookProvider == models.DeployWebhookGitLab && request.WebhookToken != "" && !validCLIGitLabSigningSecret(request.WebhookToken) {
		return errors.New("GitLab webhook token must be a valid whsec_ signing token")
	}
	if request.AutoDeploy && request.WebhookToken == "" {
		return errors.New("automatic deployment requires --webhook-token-file")
	}
	return printRequest(ctx, client, out, http.MethodPost, "/api/deploy/targets", request, true)
}

func validateCLIDeployRepositoryURL(value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 2048 || strings.HasPrefix(value, "-") || strings.IndexFunc(value, func(r rune) bool {
		return r <= ' ' || r == 0x7f
	}) >= 0 {
		return errors.New("repository URL is invalid")
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Host == "" || parsed.Path == "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return errors.New("repository URL is invalid")
		}
		switch parsed.Scheme {
		case "https":
			if parsed.User != nil {
				return errors.New("repository URL must not contain credentials")
			}
		case "ssh":
			if parsed.User != nil {
				if _, hasPassword := parsed.User.Password(); hasPassword {
					return errors.New("repository URL must not contain credentials")
				}
			}
		default:
			return errors.New("repository URL must use HTTPS or SSH")
		}
		return nil
	}
	at := strings.IndexByte(value, '@')
	colon := strings.IndexByte(value, ':')
	if at <= 0 || colon <= at+1 || colon == len(value)-1 || strings.Contains(value[colon+1:], "\\") {
		return errors.New("repository URL must use HTTPS or SSH")
	}
	return nil
}

func validCLIDeployBranch(value string) bool {
	if value == "" || len(value) > 255 || strings.HasPrefix(value, "-") || strings.HasPrefix(value, ".") ||
		strings.HasSuffix(value, ".") || strings.HasSuffix(value, "/") || strings.Contains(value, "..") ||
		strings.Contains(value, "//") || strings.Contains(value, "@{") {
		return false
	}
	return !strings.ContainsAny(value, " ~^:?*[\\\x00\x7f")
}

func validCLIDeployComposeFile(value string) bool {
	if filepath.IsAbs(value) || strings.ContainsRune(value, '\x00') {
		return false
	}
	clean := filepath.Clean(value)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(filepath.Separator))
}

func readCLIDeployScript(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("inspect deploy script file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("deploy script file must be a regular file and not a symlink")
	}
	if info.Size() > maxCLIDeployScriptBytes {
		return "", fmt.Errorf("deploy script file exceeds %d bytes", maxCLIDeployScriptBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read deploy script file: %w", err)
	}
	if len(data) == 0 {
		return "", errors.New("deploy script file is empty")
	}
	if len(data) > maxCLIDeployScriptBytes || !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return "", fmt.Errorf("deploy script file must be NUL-free UTF-8 text of at most %d bytes", maxCLIDeployScriptBytes)
	}
	return string(data), nil
}

func validCLIGitLabSigningSecret(secret string) bool {
	if !strings.HasPrefix(secret, "whsec_") {
		return false
	}
	encoded := strings.TrimPrefix(secret, "whsec_")
	if encoded == "" || len(encoded) > 1024 {
		return false
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		key, err = base64.RawStdEncoding.DecodeString(encoded)
	}
	return err == nil && len(key) >= 16 && len(key) <= 512
}

func runDeployStaging(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 || args[0] != "create" {
		return errors.New("usage: hserverctl deploy staging create --confirm --project-dir PATH [--name NAME] [--branch BRANCH] SOURCE_TARGET")
	}
	flags := flag.NewFlagSet("deploy staging create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm staging target creation")
	projectDir := flags.String("project-dir", "", "isolated absolute staging project directory")
	name := flags.String("name", "", "optional staging target name")
	branch := flags.String("branch", "", "optional staging branch")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl deploy staging create --confirm --project-dir PATH [--name NAME] [--branch BRANCH] SOURCE_TARGET")
	}
	if !*confirmed {
		return errors.New("staging target creation requires explicit --confirm")
	}
	cleanProjectDir := filepath.Clean(strings.TrimSpace(*projectDir))
	if strings.TrimSpace(*projectDir) == "" || !filepath.IsAbs(cleanProjectDir) {
		return errors.New("staging project directory must be absolute")
	}
	target, err := positiveDeployID("source target", flags.Args()[0])
	if err != nil {
		return err
	}
	payload := map[string]string{
		"name":       strings.TrimSpace(*name),
		"branch":     strings.TrimSpace(*branch),
		"projectDir": cleanProjectDir,
	}
	endpoint := "/api/deploy/targets/" + strconv.FormatInt(target, 10) + "/staging"
	return printRequest(ctx, client, out, http.MethodPost, endpoint, payload, true)
}

func runDeployHistory(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("deploy history", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	target := flags.Int64("target", 0, "optional deployment target ID")
	limit := flags.Int("limit", 50, "maximum deployment runs")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl deploy history [--target TARGET] [--limit N]")
	}
	if *target < 0 {
		return errors.New("deploy target ID must be a positive integer")
	}
	if *limit < 1 || *limit > 500 {
		return errors.New("deploy history limit must be between 1 and 500")
	}
	query := url.Values{"limit": {strconv.Itoa(*limit)}}
	if *target > 0 {
		query.Set("targetId", strconv.FormatInt(*target, 10))
	}
	return printRequest(ctx, client, out, http.MethodGet, "/api/deploy/history?"+query.Encode(), nil, true)
}

func runDeployAction(ctx context.Context, client *apiClient, action string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("deploy "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm deployment mutation")
	wait := flags.Duration("wait", 10*time.Minute, "maximum request wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return fmt.Errorf("usage: hserverctl deploy %s --confirm [--wait DURATION] TARGET", action)
	}
	if !*confirmed {
		return errors.New("deployment action requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("deployment action wait must be greater than zero")
	}
	target, err := positiveDeployID("target", flags.Args()[0])
	if err != nil {
		return err
	}
	endpoint := "/api/deploy/manual/" + strconv.FormatInt(target, 10)
	if action == "rollback" {
		endpoint = "/api/deploy/rollback/" + strconv.FormatInt(target, 10)
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, nil, true)
}

func runDeployService(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl deploy service logs|action")
	}
	switch args[0] {
	case "logs":
		flags := flag.NewFlagSet("deploy service logs", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		tail := flags.Int("tail", 200, "number of log lines")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 2 {
			return errors.New("usage: hserverctl deploy service logs [--tail N] TARGET SERVICE")
		}
		if *tail < 1 || *tail > 1000 {
			return errors.New("Compose service log tail must be between 1 and 1000")
		}
		target, service, err := deployServiceIdentity(flags.Args())
		if err != nil {
			return err
		}
		endpoint := deployServiceEndpoint(target, service) + "/logs?tail=" + strconv.Itoa(*tail)
		return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
	case "action":
		flags := flag.NewFlagSet("deploy service action", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		confirmed := flags.Bool("confirm", false, "confirm Compose service mutation")
		wait := flags.Duration("wait", 10*time.Minute, "maximum action wait")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 3 {
			return errors.New("usage: hserverctl deploy service action --confirm [--wait DURATION] TARGET SERVICE ACTION")
		}
		if !*confirmed {
			return errors.New("Compose service action requires explicit --confirm")
		}
		if *wait <= 0 {
			return errors.New("Compose service action wait must be greater than zero")
		}
		target, service, err := deployServiceIdentity(flags.Args()[:2])
		if err != nil {
			return err
		}
		action := strings.TrimSpace(flags.Args()[2])
		if action != "start" && action != "stop" && action != "restart" && action != "recreate" {
			return fmt.Errorf("unsupported Compose service action %q", action)
		}
		return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, deployServiceEndpoint(target, service)+"/"+action, nil, true)
	default:
		return fmt.Errorf("unknown deploy service command %q", args[0])
	}
}

func positiveDeployID(name, value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("deploy %s ID must be a positive integer", name)
	}
	return id, nil
}

func deployServiceIdentity(values []string) (int64, string, error) {
	target, err := positiveDeployID("target", values[0])
	if err != nil {
		return 0, "", err
	}
	service := strings.TrimSpace(values[1])
	if !validCLIDeployServiceName(service) {
		return 0, "", errors.New("invalid Compose service name")
	}
	return target, service, nil
}

func deployServiceEndpoint(target int64, service string) string {
	return "/api/deploy/targets/" + strconv.FormatInt(target, 10) + "/services/" + url.PathEscape(service)
}
