package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const doctorReportSchemaVersion = 1

var doctorCapabilityPattern = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)

type doctorReport struct {
	SchemaVersion int                   `json:"schema_version"`
	OK            bool                  `json:"ok"`
	Server        string                `json:"server"`
	Checks        []doctorCheck         `json:"checks"`
	Panel         *doctorPanelSummary   `json:"panel,omitempty"`
	Account       *doctorAccountSummary `json:"account,omitempty"`
	Fleet         *doctorFleetSummary   `json:"fleet,omitempty"`
	Node          *doctorNodeSummary    `json:"node,omitempty"`
}

type doctorCheck struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

type doctorPanelSummary struct {
	Status      string `json:"status"`
	Version     string `json:"version"`
	Uptime      int64  `json:"uptime_seconds"`
	BuildCommit string `json:"build_commit,omitempty"`
}

type doctorHealthResponse struct {
	Status      string `json:"status"`
	Version     string `json:"version"`
	Uptime      int64  `json:"uptime"`
	BuildCommit string `json:"build_commit,omitempty"`
}

type doctorAccountSummary struct {
	Role        string `json:"role"`
	TOTPEnabled bool   `json:"totp_enabled"`
}

type doctorFleetSummary struct {
	Observed int `json:"observed"`
	Online   int `json:"online"`
	Offline  int `json:"offline"`
}

type doctorNodeSummary struct {
	ID              string   `json:"id"`
	Online          bool     `json:"online"`
	Architecture    string   `json:"architecture,omitempty"`
	AgentVersion    string   `json:"agent_version,omitempty"`
	ProtocolVersion string   `json:"protocol_version,omitempty"`
	Capabilities    []string `json:"capabilities"`
}

func runDoctor(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	nodeID := flags.String("node", "", "managed node ID to diagnose")
	requiredArchitecture := flags.String("require-architecture", "", "required managed-node agent architecture: amd64 or arm64")
	outputFormat := flags.String("format", "json", "output format: json or text")
	outputPath := flags.String("output", "", "write the report to a new protected file")
	var requiredCapabilities stringValues
	flags.Var(&requiredCapabilities, "require-capability", "required managed-node capability (repeatable)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl doctor [--format json|text] [--output PATH] [--node NODE] [--require-architecture amd64|arm64] [--require-capability NAME]...")
	}
	*outputFormat = strings.ToLower(strings.TrimSpace(*outputFormat))
	if *outputFormat != "json" && *outputFormat != "text" {
		return errors.New("doctor format must be json or text")
	}
	*nodeID = strings.TrimSpace(*nodeID)
	*requiredArchitecture = strings.TrimSpace(*requiredArchitecture)
	if len(*nodeID) > 128 {
		return errors.New("doctor node must not exceed 128 bytes")
	}
	if len(requiredCapabilities) > 0 && *nodeID == "" {
		return errors.New("--require-capability requires --node")
	}
	if *requiredArchitecture != "" && *nodeID == "" {
		return errors.New("--require-architecture requires --node")
	}
	if *requiredArchitecture != "" && *requiredArchitecture != "amd64" && *requiredArchitecture != "arm64" {
		return errors.New("doctor required architecture must equal amd64 or arm64")
	}
	if err := validateUniqueValues("required capability", requiredCapabilities, 32); err != nil {
		return err
	}
	for _, capability := range requiredCapabilities {
		if !doctorCapabilityPattern.MatchString(capability) {
			return fmt.Errorf("invalid required capability %q", capability)
		}
	}
	sort.Strings(requiredCapabilities)
	*outputPath = strings.TrimSpace(*outputPath)
	if *outputPath != "" {
		if _, err := os.Lstat(*outputPath); err == nil {
			return fmt.Errorf("doctor output file already exists: %s", *outputPath)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("inspect doctor output file %s: %w", *outputPath, err)
		}
	}

	report := doctorReport{
		SchemaVersion: doctorReportSchemaVersion,
		OK:            true,
		Server:        client.baseURL.String(),
		Checks:        make([]doctorCheck, 0, 4+len(requiredCapabilities)),
	}
	failures := 0
	addCheck := func(name string, passed bool, message string) {
		status := "pass"
		if !passed {
			status = "fail"
			report.OK = false
			failures++
		}
		report.Checks = append(report.Checks, doctorCheck{Name: name, Status: status, Message: boundedDoctorMessage(message)})
	}

	health, healthErr := requestJSON[doctorHealthResponse](ctx, client, http.MethodGet, "/api/health", nil, false)
	if healthErr != nil {
		addCheck("panel.health", false, healthErr.Error())
	} else if health.Status != "ok" || health.Version == "" || health.Uptime < 0 {
		addCheck("panel.health", false, "health endpoint returned an invalid or unhealthy payload")
		report.Panel = &doctorPanelSummary{Status: health.Status, Version: health.Version, Uptime: health.Uptime, BuildCommit: health.BuildCommit}
	} else {
		report.Panel = &doctorPanelSummary{Status: health.Status, Version: health.Version, Uptime: health.Uptime, BuildCommit: health.BuildCommit}
		addCheck("panel.health", true, fmt.Sprintf("panel %s responded with status ok", health.Version))
	}

	account, accountErr := requestJSON[doctorAccountSummary](ctx, client, http.MethodGet, "/api/auth/me", nil, true)
	if accountErr != nil {
		addCheck("authentication", false, accountErr.Error())
	} else if account.Role != "admin" && account.Role != "manager" && account.Role != "viewer" {
		addCheck("authentication", false, "authenticated account returned an unknown role")
		report.Account = &account
	} else {
		report.Account = &account
		addCheck("authentication", true, "authenticated account role: "+account.Role)
	}

	if *nodeID == "" {
		nodes, nodesErr := requestJSON[[]managedNodeEnvelope](ctx, client, http.MethodGet, "/api/nodes", nil, true)
		if nodesErr != nil {
			addCheck("fleet.inventory", false, nodesErr.Error())
		} else {
			fleet := doctorFleetSummary{Observed: len(nodes)}
			for _, node := range nodes {
				if node.Online {
					fleet.Online++
				} else {
					fleet.Offline++
				}
			}
			report.Fleet = &fleet
			addCheck("fleet.inventory", true, fmt.Sprintf("observed %d managed node(s): %d online, %d offline", fleet.Observed, fleet.Online, fleet.Offline))
		}
	} else {
		node, nodeErr := requestJSON[managedNodeEnvelope](ctx, client, http.MethodGet, "/api/nodes/"+url.PathEscape(*nodeID), nil, true)
		if nodeErr != nil {
			addCheck("node.status", false, nodeErr.Error())
			if *requiredArchitecture != "" {
				addCheck("node.architecture", false, "managed node architecture could not be verified because node status is unavailable")
			}
			for _, capability := range requiredCapabilities {
				addCheck("node.capability."+capability, false, "managed node capability could not be verified because node status is unavailable")
			}
		} else {
			capabilities := append([]string(nil), node.Capabilities...)
			sort.Strings(capabilities)
			report.Node = &doctorNodeSummary{
				ID: node.ID, Online: node.Online, Architecture: node.Inventory.Arch, AgentVersion: node.AgentVersion,
				ProtocolVersion: node.ProtocolVersion, Capabilities: capabilities,
			}
			validIdentity := node.ID == *nodeID
			switch {
			case !validIdentity:
				addCheck("node.status", false, "managed-node response identity did not match the requested node")
			case !node.Online:
				addCheck("node.status", false, "managed node is offline")
			default:
				addCheck("node.status", true, "managed node is online")
			}
			if *requiredArchitecture != "" {
				architectureMatches := validIdentity && node.Inventory.Arch == *requiredArchitecture
				message := "managed node reports agent architecture " + node.Inventory.Arch
				if !validIdentity {
					message = "managed node architecture could not be trusted because node identity did not match"
				} else if node.Inventory.Arch == "" {
					message = "managed node does not report an agent architecture"
				} else if !architectureMatches {
					message = fmt.Sprintf("managed node reports agent architecture %s, expected %s", node.Inventory.Arch, *requiredArchitecture)
				}
				addCheck("node.architecture", architectureMatches, message)
			}
			for _, capability := range requiredCapabilities {
				available := validIdentity && managedNodeHasCapability(node, capability)
				message := "managed node advertises " + capability
				if !validIdentity {
					message = "managed node capability could not be trusted because node identity did not match"
				} else if !available {
					message = "managed node does not advertise " + capability
				}
				addCheck("node.capability."+capability, available, message)
			}
		}
	}

	var rendered bytes.Buffer
	if *outputFormat == "text" {
		if err := writeDoctorText(&rendered, report); err != nil {
			return err
		}
	} else {
		raw, err := json.Marshal(report)
		if err != nil {
			return fmt.Errorf("encode doctor report: %w", err)
		}
		if err := prettyJSON(&rendered, raw); err != nil {
			return err
		}
	}
	if *outputPath != "" {
		if err := writeProtectedDoctorReport(*outputPath, rendered.Bytes()); err != nil {
			return err
		}
		fmt.Fprintf(out, "Wrote protected Heyserver doctor report to %s\n", *outputPath)
	} else if _, err := rendered.WriteTo(out); err != nil {
		return err
	}
	if failures > 0 {
		return fmt.Errorf("doctor reported %d failed check(s)", failures)
	}
	return nil
}

func writeProtectedDoctorReport(path string, report []byte) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("doctor output path is required")
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create doctor output directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".hserver-doctor-*")
	if err != nil {
		return fmt.Errorf("create temporary doctor report: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("protect temporary doctor report: %w", err)
	}
	if _, err := temporary.Write(report); err != nil {
		temporary.Close()
		return fmt.Errorf("write temporary doctor report: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary doctor report: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary doctor report: %w", err)
	}
	if err := os.Link(temporaryPath, path); err != nil {
		if _, inspectErr := os.Lstat(path); inspectErr == nil {
			return fmt.Errorf("doctor output file already exists: %s", path)
		}
		return fmt.Errorf("publish protected doctor report: %w", err)
	}
	return nil
}

func writeDoctorText(out io.Writer, report doctorReport) error {
	var text strings.Builder
	result := "PASS"
	if !report.OK {
		result = "FAIL"
	}
	fmt.Fprintf(&text, "Heyserver connection doctor: %s\n", result)
	fmt.Fprintf(&text, "Server: %s\n", doctorTextValue(report.Server))
	if report.Panel != nil {
		fmt.Fprintf(&text, "Panel: %s | version %s | uptime %ds",
			doctorTextValue(report.Panel.Status), doctorTextValue(report.Panel.Version), report.Panel.Uptime)
		if report.Panel.BuildCommit != "" {
			fmt.Fprintf(&text, " | commit %s", doctorTextValue(report.Panel.BuildCommit))
		}
		text.WriteByte('\n')
	}
	if report.Account != nil {
		totp := "disabled"
		if report.Account.TOTPEnabled {
			totp = "enabled"
		}
		fmt.Fprintf(&text, "Account: role %s | TOTP %s\n", doctorTextValue(report.Account.Role), totp)
	}
	if report.Fleet != nil {
		fmt.Fprintf(&text, "Fleet: %d observed | %d online | %d offline\n",
			report.Fleet.Observed, report.Fleet.Online, report.Fleet.Offline)
	}
	if report.Node != nil {
		state := "offline"
		if report.Node.Online {
			state = "online"
		}
		fmt.Fprintf(&text, "Node: %s | %s", doctorTextValue(report.Node.ID), state)
		if report.Node.AgentVersion != "" {
			fmt.Fprintf(&text, " | agent %s", doctorTextValue(report.Node.AgentVersion))
		}
		if report.Node.ProtocolVersion != "" {
			fmt.Fprintf(&text, " | protocol %s", doctorTextValue(report.Node.ProtocolVersion))
		}
		if report.Node.Architecture != "" {
			fmt.Fprintf(&text, " | arch %s", doctorTextValue(report.Node.Architecture))
		}
		text.WriteByte('\n')
		capabilities := "none"
		if len(report.Node.Capabilities) > 0 {
			safe := make([]string, 0, len(report.Node.Capabilities))
			for _, capability := range report.Node.Capabilities {
				safe = append(safe, doctorTextValue(capability))
			}
			capabilities = strings.Join(safe, ", ")
		}
		fmt.Fprintf(&text, "Capabilities: %s\n", capabilities)
	}
	text.WriteString("Checks:\n")
	for _, check := range report.Checks {
		fmt.Fprintf(&text, "  [%s] %s", strings.ToUpper(doctorTextValue(check.Status)), doctorTextValue(check.Name))
		if check.Message != "" {
			fmt.Fprintf(&text, " - %s", doctorTextValue(check.Message))
		}
		text.WriteByte('\n')
	}
	_, err := io.WriteString(out, text.String())
	return err
}

func doctorTextValue(value string) string {
	value = strings.Join(strings.Fields(sanitizeTUIText(value)), " ")
	if value == "" {
		return "N/A"
	}
	return value
}

func boundedDoctorMessage(message string) string {
	message = strings.TrimSpace(message)
	if len(message) <= 512 {
		return message
	}
	return message[:509] + "..."
}
