package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/releaseversion"
	"github.com/IamYGT/heyserver/internal/services/releaseupdates"
)

var updateNodeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

const signedManifestRequiredMessage = "Signed manifest required for installation"

type cliReleaseStage struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Status  string `json:"status"`
}

type cliReleaseStageEnvelope struct {
	Stage *cliReleaseStage `json:"stage"`
}

type cliReleaseUpdateStatus struct {
	Status          string `json:"status"`
	SignatureStatus string `json:"signature_status"`
}

type cliAgentUpdateStatus struct {
	ReleaseStatus     string `json:"release_status"`
	SignatureStatus   string `json:"signature_status"`
	LatestVersion     string `json:"latest_version"`
	UpdateAvailable   bool   `json:"update_available"`
	OperationStatus   string `json:"operation_status"`
	RollbackAvailable bool   `json:"rollback_available"`
}

func runUpdates(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl updates status|stage-status|stage|install|agent")
	}
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return errors.New("usage: hserverctl updates status")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/system/update", nil, true)
	case "stage-status":
		if len(args) != 1 {
			return errors.New("usage: hserverctl updates stage-status")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/system/update/stage", nil, true)
	case "stage":
		return runPanelUpdateStage(ctx, client, args[1:], out)
	case "install":
		return runPanelUpdateInstall(ctx, client, args[1:], out)
	case "agent":
		return runAgentUpdates(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown updates command %q", args[0])
	}
}

func runPanelUpdateStage(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("updates stage", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm server-side download and verification")
	wait := flags.Duration("wait", 16*time.Minute, "maximum staging request wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl updates stage --confirm [--wait DURATION]")
	}
	if !*confirmed {
		return errors.New("release staging requires explicit --confirm")
	}
	if err := validateUpdateWait("release staging", *wait, 20*time.Minute); err != nil {
		return err
	}
	if err := requireVerifiedReleaseManifest(ctx, client.withTimeout(*wait)); err != nil {
		return err
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, "/api/system/update/stage", nil, true)
}

func runPanelUpdateInstall(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("updates install", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm panel restart and automatic rollback boundary")
	wait := flags.Duration("wait", 2*time.Minute, "maximum scheduling request wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl updates install --confirm [--wait DURATION]")
	}
	if !*confirmed {
		return errors.New("release installation requires explicit --confirm")
	}
	if err := validateUpdateWait("release installation", *wait, 5*time.Minute); err != nil {
		return err
	}
	if err := requireVerifiedReleaseManifest(ctx, client.withTimeout(*wait)); err != nil {
		return err
	}
	observed, err := requestJSON[cliReleaseStageEnvelope](ctx, client.withTimeout(*wait), http.MethodGet, "/api/system/update/stage", nil, true)
	if err != nil {
		return err
	}
	if observed.Stage == nil {
		return errors.New("no verified release stage is available; run updates stage first")
	}
	stage := observed.Stage
	if strings.TrimSpace(stage.ID) == "" || !stableUpdateVersion(stage.Version) {
		return errors.New("latest release stage returned an invalid identity or version")
	}
	if stage.Status != "staged" && stage.Status != "failed" {
		return fmt.Errorf("latest release stage is not ready for installation: %s", stage.Status)
	}
	payload := map[string]any{"stage_id": stage.ID, "version": stage.Version, "confirmed": true}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, "/api/system/update/install", payload, true)
}

func runAgentUpdates(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl updates agent status|upgrade|rollback --node NODE")
	}
	switch args[0] {
	case "status":
		options, err := parseAgentUpdateFlags("status", args[1:], false, 60*time.Second, 3*time.Minute)
		if err != nil {
			return err
		}
		return printRequest(ctx, client.withTimeout(options.Wait), out, http.MethodGet, agentUpdateEndpoint(options.Node, ""), nil, true)
	case "upgrade":
		options, err := parseAgentUpdateFlags("upgrade", args[1:], true, 12*time.Minute, 15*time.Minute)
		if err != nil {
			return err
		}
		status, err := loadAgentUpdateStatus(ctx, client.withTimeout(options.Wait), options.Node)
		if err != nil {
			return err
		}
		if status.SignatureStatus != releaseupdates.SignatureVerified {
			return releaseupdates.ErrSignedManifestRequired
		}
		if status.ReleaseStatus != "healthy" || !status.UpdateAvailable || !stableUpdateVersion(status.LatestVersion) {
			return errors.New("managed agent has no verified newer stable release available")
		}
		if updateOperationActive(status.OperationStatus) {
			return fmt.Errorf("managed agent lifecycle operation is already %s", status.OperationStatus)
		}
		payload := map[string]any{"version": status.LatestVersion, "confirmed": true}
		return printRequest(ctx, client.withTimeout(options.Wait), out, http.MethodPost, agentUpdateEndpoint(options.Node, "upgrade"), payload, true)
	case "rollback":
		options, err := parseAgentUpdateFlags("rollback", args[1:], true, 2*time.Minute, 5*time.Minute)
		if err != nil {
			return err
		}
		status, err := loadAgentUpdateStatus(ctx, client.withTimeout(options.Wait), options.Node)
		if err != nil {
			return err
		}
		if !status.RollbackAvailable {
			return errors.New("managed agent has no verified rollback snapshot available")
		}
		if updateOperationActive(status.OperationStatus) {
			return fmt.Errorf("managed agent lifecycle operation is already %s", status.OperationStatus)
		}
		return printRequest(ctx, client.withTimeout(options.Wait), out, http.MethodPost, agentUpdateEndpoint(options.Node, "rollback"), map[string]any{"confirmed": true}, true)
	default:
		return fmt.Errorf("unknown updates agent command %q", args[0])
	}
}

type agentUpdateOptions struct {
	Node string
	Wait time.Duration
}

func parseAgentUpdateFlags(action string, args []string, mutation bool, defaultWait, maximumWait time.Duration) (agentUpdateOptions, error) {
	flags := flag.NewFlagSet("updates agent "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID")
	wait := flags.Duration("wait", defaultWait, "maximum managed-agent request wait")
	confirmed := flags.Bool("confirm", false, "confirm managed-agent restart and lifecycle action")
	if err := flags.Parse(args); err != nil {
		return agentUpdateOptions{}, err
	}
	usage := fmt.Sprintf("usage: hserverctl updates agent %s %s--node NODE [--wait DURATION]", action, map[bool]string{true: "--confirm ", false: ""}[mutation])
	if len(flags.Args()) != 0 {
		return agentUpdateOptions{}, errors.New(usage)
	}
	if mutation && !*confirmed {
		return agentUpdateOptions{}, fmt.Errorf("managed agent %s requires explicit --confirm", action)
	}
	if !mutation && *confirmed {
		return agentUpdateOptions{}, errors.New("--confirm is only valid for managed-agent mutations")
	}
	nodeID := strings.TrimSpace(*node)
	if !updateNodeIDPattern.MatchString(nodeID) {
		return agentUpdateOptions{}, errors.New("managed agent update requires a valid --node NODE")
	}
	if err := validateUpdateWait("managed agent "+action, *wait, maximumWait); err != nil {
		return agentUpdateOptions{}, err
	}
	return agentUpdateOptions{Node: nodeID, Wait: *wait}, nil
}

func loadAgentUpdateStatus(ctx context.Context, client *apiClient, node string) (cliAgentUpdateStatus, error) {
	return requestJSON[cliAgentUpdateStatus](ctx, client, http.MethodGet, agentUpdateEndpoint(node, ""), nil, true)
}

func requireVerifiedReleaseManifest(ctx context.Context, client *apiClient) error {
	status, err := requestJSON[cliReleaseUpdateStatus](ctx, client, http.MethodGet, "/api/system/update", nil, true)
	if err != nil {
		return err
	}
	if status.SignatureStatus != releaseupdates.SignatureVerified {
		return releaseupdates.ErrSignedManifestRequired
	}
	return nil
}

func agentUpdateEndpoint(node, action string) string {
	endpoint := "/api/nodes/" + url.PathEscape(node) + "/agent-update"
	if action != "" {
		endpoint += "/" + action
	}
	return endpoint
}

func updateOperationActive(status string) bool {
	return status == "scheduled" || status == "running"
}

func stableUpdateVersion(version string) bool {
	return releaseversion.Compare(version, version) == releaseversion.Current
}

func validateUpdateWait(operation string, wait, maximum time.Duration) error {
	if wait <= 0 || wait > maximum {
		return fmt.Errorf("%s wait must be greater than zero and at most %s", operation, maximum)
	}
	return nil
}
