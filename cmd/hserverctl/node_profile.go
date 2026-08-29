package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxNodeProfileFileBytes  = 16 << 10
	maxNodeProfilePathBytes  = 4096
	maxNodeProfileWriteRoots = 16
	maxNodeProfileApplyWait  = 10 * time.Minute
	nodeProfileApplyPollTime = 100 * time.Millisecond
)

var cliNodeProfileSafePathPattern = regexp.MustCompile(`^[A-Za-z0-9._/+:-]+$`)

// cliNodeProfile is the fixed file and API profile shape. Keep this local to
// the CLI so a profile file can be rejected before any authenticated request
// is made and so the client does not depend on the server's storage package.
type cliNodeProfile struct {
	AllowDeployRead          bool     `json:"allowDeployRead"`
	AllowDeployActions       bool     `json:"allowDeployActions"`
	AllowDeployDomainRead    bool     `json:"allowDeployDomainRead"`
	AllowDeployDomainActions bool     `json:"allowDeployDomainActions"`
	DeployPlansFile          string   `json:"deployPlansFile"`
	DeployAcmeWebroot        string   `json:"deployAcmeWebroot"`
	DeployWriteRoots         []string `json:"deployWriteRoots"`
}

type cliNodeProfilePutRequest struct {
	Profile          cliNodeProfile `json:"profile"`
	ExpectedRevision int64          `json:"expectedRevision"`
}

type cliNodeProfileResponse struct {
	NodeID   string                 `json:"nodeId"`
	Desired  cliNodeProfileDesired  `json:"desired"`
	Observed cliNodeProfileObserved `json:"observed"`
	Apply    cliNodeProfileApply    `json:"apply"`
}

type cliNodeProfileDesired struct {
	State    string          `json:"state"`
	Revision *int64          `json:"revision"`
	Profile  *cliNodeProfile `json:"profile"`
}

type cliNodeProfileObserved struct {
	Capabilities    []string        `json:"capabilities"`
	Online          bool            `json:"online"`
	LastSeenAt      json.RawMessage `json:"lastSeenAt"`
	AgentVersion    string          `json:"agentVersion"`
	ProtocolVersion string          `json:"protocolVersion"`
	ProfileState    string          `json:"profileState"`
}

type cliNodeProfileApply struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
}

type cliNodeProfileApplyRequest struct {
	ExpectedRevision int64 `json:"expectedRevision"`
	Confirmed        bool  `json:"confirmed"`
}

func runNodeProfile(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(nodeProfileUsage)
	}
	switch args[0] {
	case "get":
		return runNodeProfileGet(ctx, client, args[1:], out)
	case "set":
		return runNodeProfileSet(ctx, client, args[1:], out)
	case "export":
		return runNodeProfileExport(ctx, client, args[1:], out)
	case "apply":
		return runNodeProfileApply(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown node profile command %q", args[0])
	}
}

const nodeProfileUsage = "usage: hserverctl nodes profile get NODE | nodes profile set --confirm --profile-file PATH NODE | nodes profile export NODE [--format env-fragment] | nodes profile apply --confirm [--wait DURATION] NODE"

func runNodeProfileGet(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) != 1 || args[0] == "" {
		return errors.New("usage: hserverctl nodes profile get NODE")
	}
	return printRequest(ctx, client, out, http.MethodGet, nodeProfileEndpoint(args[0]), nil, true)
}

func runNodeProfileSet(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	confirmed, profilePath, nodeID, err := parseNodeProfileSetArgs(args)
	if err != nil {
		return err
	}
	if !confirmed {
		return errors.New("node profile update requires explicit --confirm")
	}
	profile, err := readCLINodeProfileFile(profilePath)
	if err != nil {
		return err
	}

	response, err := fetchCLINodeProfile(ctx, client, nodeID)
	if err != nil {
		return err
	}
	revision, err := response.desiredRevision()
	if err != nil {
		return err
	}
	payload := cliNodeProfilePutRequest{Profile: profile, ExpectedRevision: revision}
	return printRequest(ctx, client, out, http.MethodPut, nodeProfileEndpoint(nodeID), payload, true)
}

func runNodeProfileExport(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	format, nodeID, err := parseNodeProfileExportArgs(args)
	if err != nil {
		return err
	}
	if format != "" && format != "env-fragment" {
		return fmt.Errorf("node profile export format must be env-fragment, got %q", format)
	}

	response, err := fetchCLINodeProfile(ctx, client, nodeID)
	if err != nil {
		return err
	}
	if response.Desired.State == "not_configured" {
		return errors.New("node profile is not configured; export requires a configured profile")
	}
	if response.Desired.Profile == nil {
		return errors.New("node profile response has a null profile; export requires a configured profile")
	}
	if response.Desired.State != "configured" {
		return fmt.Errorf("node profile response has unsupported desired state %q", response.Desired.State)
	}
	if err := validateCLINodeProfile(*response.Desired.Profile); err != nil {
		return fmt.Errorf("server returned invalid node profile: %w", err)
	}
	return writeCLINodeProfileEnvFragment(out, *response.Desired.Profile)
}

func runNodeProfileApply(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	confirmed, wait, waitRequested, nodeID, err := parseNodeProfileApplyArgs(args)
	if err != nil {
		return err
	}
	if !confirmed {
		return errors.New("node profile apply requires explicit --confirm")
	}
	if waitRequested {
		if wait <= 0 {
			return errors.New("node profile apply wait must be greater than zero")
		}
		if wait > maxNodeProfileApplyWait {
			return fmt.Errorf("node profile apply wait must be at most %s", maxNodeProfileApplyWait)
		}
	}

	requestCtx := ctx
	requestClient := client
	var cancel context.CancelFunc
	if waitRequested {
		requestCtx, cancel = context.WithTimeout(ctx, wait)
		defer cancel()
		requestClient = client.withTimeout(wait)
	}

	response, err := fetchCLINodeProfileForApply(requestCtx, requestClient, nodeID)
	if err != nil {
		return err
	}
	revision, err := validateCLINodeProfileApplyPreflight(response, nodeID)
	if err != nil {
		return err
	}

	payload := cliNodeProfileApplyRequest{ExpectedRevision: revision, Confirmed: true}
	raw, err := requestClient.request(requestCtx, http.MethodPost, nodeProfileApplyEndpoint(nodeID), payload, true)
	if err != nil {
		return err
	}
	if !waitRequested {
		return prettyJSON(out, raw)
	}

	finalRaw, err := waitForCLINodeProfileApply(requestCtx, requestClient, nodeID)
	if err != nil {
		return err
	}
	return prettyJSON(out, finalRaw)
}

func parseNodeProfileApplyArgs(args []string) (confirmed bool, wait time.Duration, waitRequested bool, nodeID string, err error) {
	var positional []string
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--confirm", arg == "--confirm=true":
			confirmed = true
		case arg == "--confirm=false":
			confirmed = false
		case arg == "--wait":
			if waitRequested {
				return false, 0, false, "", errors.New("node profile apply accepts --wait only once")
			}
			if index+1 >= len(args) {
				return false, 0, false, "", errors.New("node profile apply requires a duration after --wait")
			}
			parsed, parseErr := time.ParseDuration(args[index+1])
			if parseErr != nil {
				return false, 0, false, "", fmt.Errorf("node profile apply wait must be a duration: %w", parseErr)
			}
			wait = parsed
			waitRequested = true
			index++
		case strings.HasPrefix(arg, "--wait="):
			if waitRequested {
				return false, 0, false, "", errors.New("node profile apply accepts --wait only once")
			}
			value := strings.TrimPrefix(arg, "--wait=")
			if value == "" {
				return false, 0, false, "", errors.New("node profile apply requires a non-empty --wait duration")
			}
			parsed, parseErr := time.ParseDuration(value)
			if parseErr != nil {
				return false, 0, false, "", fmt.Errorf("node profile apply wait must be a duration: %w", parseErr)
			}
			wait = parsed
			waitRequested = true
		case arg == "--":
			positional = append(positional, args[index+1:]...)
			index = len(args)
		case strings.HasPrefix(arg, "-"):
			return false, 0, false, "", fmt.Errorf("unknown node profile apply option %q", arg)
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 || positional[0] == "" {
		return false, 0, false, "", errors.New("usage: hserverctl nodes profile apply --confirm [--wait DURATION] NODE")
	}
	return confirmed, wait, waitRequested, positional[0], nil
}

func parseNodeProfileSetArgs(args []string) (confirmed bool, profilePath, nodeID string, err error) {
	var positional []string
	profileSeen := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--confirm":
			confirmed = true
		case arg == "--confirm=true":
			confirmed = true
		case arg == "--confirm=false":
			confirmed = false
		case arg == "--profile-file":
			if profileSeen {
				return false, "", "", errors.New("node profile set accepts --profile-file only once")
			}
			if index+1 >= len(args) {
				return false, "", "", errors.New("node profile set requires a path after --profile-file")
			}
			index++
			profilePath = args[index]
			profileSeen = true
		case strings.HasPrefix(arg, "--profile-file="):
			if profileSeen {
				return false, "", "", errors.New("node profile set accepts --profile-file only once")
			}
			profilePath = strings.TrimPrefix(arg, "--profile-file=")
			profileSeen = true
		case arg == "--":
			positional = append(positional, args[index+1:]...)
			index = len(args)
		case strings.HasPrefix(arg, "-"):
			return false, "", "", fmt.Errorf("unknown node profile set option %q", arg)
		default:
			positional = append(positional, arg)
		}
	}
	if !profileSeen || profilePath == "" || len(positional) != 1 || positional[0] == "" {
		return false, "", "", errors.New("usage: hserverctl nodes profile set --confirm --profile-file PATH NODE")
	}
	return confirmed, profilePath, positional[0], nil
}

func parseNodeProfileExportArgs(args []string) (format, nodeID string, err error) {
	var positional []string
	formatSeen := false
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--format":
			if formatSeen {
				return "", "", errors.New("node profile export accepts --format only once")
			}
			if index+1 >= len(args) {
				return "", "", errors.New("node profile export requires a value after --format")
			}
			index++
			format = args[index]
			if format == "" {
				return "", "", errors.New("node profile export requires a non-empty --format value")
			}
			formatSeen = true
		case strings.HasPrefix(arg, "--format="):
			if formatSeen {
				return "", "", errors.New("node profile export accepts --format only once")
			}
			format = strings.TrimPrefix(arg, "--format=")
			if format == "" {
				return "", "", errors.New("node profile export requires a non-empty --format value")
			}
			formatSeen = true
		case arg == "--":
			positional = append(positional, args[index+1:]...)
			index = len(args)
		case strings.HasPrefix(arg, "-"):
			return "", "", fmt.Errorf("unknown node profile export option %q", arg)
		default:
			positional = append(positional, arg)
		}
	}
	if len(positional) != 1 || positional[0] == "" {
		return "", "", errors.New("usage: hserverctl nodes profile export NODE [--format env-fragment]")
	}
	return format, positional[0], nil
}

func nodeProfileEndpoint(nodeID string) string {
	return "/api/nodes/" + url.PathEscape(nodeID) + "/profile"
}

func nodeProfileApplyEndpoint(nodeID string) string {
	return "/api/nodes/" + url.PathEscape(nodeID) + "/profile/apply"
}

func fetchCLINodeProfile(ctx context.Context, client *apiClient, nodeID string) (cliNodeProfileResponse, error) {
	var response cliNodeProfileResponse
	raw, err := client.request(ctx, http.MethodGet, nodeProfileEndpoint(nodeID), nil, true)
	if err != nil {
		return response, err
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return response, fmt.Errorf("decode %s: %w", nodeProfileEndpoint(nodeID), err)
	}
	return response, nil
}

// fetchCLINodeProfileForApply uses a presence- and type-aware response
// decoder. The regular profile commands intentionally retain their phase-one
// permissiveness, while an apply must never turn an omitted required field
// into a zero-value mutation input.
func fetchCLINodeProfileForApply(ctx context.Context, client *apiClient, nodeID string) (cliNodeProfileResponse, error) {
	var response cliNodeProfileResponse
	endpoint := nodeProfileEndpoint(nodeID)
	raw, err := client.request(ctx, http.MethodGet, endpoint, nil, true)
	if err != nil {
		return response, err
	}
	response, err = decodeCLINodeProfileResponseForApply(raw, endpoint)
	if err != nil {
		return response, err
	}
	return response, nil
}

func decodeCLINodeProfileResponseForApply(raw []byte, endpoint string) (cliNodeProfileResponse, error) {
	var response cliNodeProfileResponse
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return response, fmt.Errorf("decode %s: %w", endpoint, err)
	}
	if fields == nil {
		return response, fmt.Errorf("decode %s: response must be a JSON object", endpoint)
	}
	for _, field := range []string{"nodeId", "desired", "observed", "apply"} {
		value, ok := fields[field]
		if !ok || rawJSONIsNull(value) {
			return response, fmt.Errorf("decode %s: response is missing required field %q", endpoint, field)
		}
	}

	if err := decodeNodeProfileRequiredString(fields["nodeId"], "nodeId", &response.NodeID); err != nil {
		return response, fmt.Errorf("decode %s: %w", endpoint, err)
	}
	if err := decodeCLINodeProfileDesiredForApply(fields["desired"], &response.Desired); err != nil {
		return response, fmt.Errorf("decode %s: desired: %w", endpoint, err)
	}
	if err := decodeCLINodeProfileObservedForApply(fields["observed"], &response.Observed); err != nil {
		return response, fmt.Errorf("decode %s: observed: %w", endpoint, err)
	}
	if err := decodeCLINodeProfileApplyForApply(fields["apply"], &response.Apply); err != nil {
		return response, fmt.Errorf("decode %s: apply: %w", endpoint, err)
	}
	return response, nil
}

func decodeCLINodeProfileDesiredForApply(raw json.RawMessage, desired *cliNodeProfileDesired) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("must be an object: %w", err)
	}
	if fields == nil {
		return errors.New("must be an object")
	}
	for _, field := range []string{"state", "revision"} {
		value, ok := fields[field]
		if !ok || rawJSONIsNull(value) {
			return fmt.Errorf("missing required field %q", field)
		}
	}
	if _, ok := fields["profile"]; !ok {
		return errors.New("missing required field \"profile\"")
	}
	if err := decodeNodeProfileRequiredString(fields["state"], "state", &desired.State); err != nil {
		return err
	}
	var revision int64
	if err := decodeNodeProfileRequiredInt64(fields["revision"], "revision", &revision); err != nil {
		return err
	}
	desired.Revision = &revision
	if rawJSONIsNull(fields["profile"]) {
		desired.Profile = nil
		return nil
	}
	profile, err := decodeCLINodeProfile(fields["profile"])
	if err != nil {
		return fmt.Errorf("profile: %w", err)
	}
	desired.Profile = &profile
	return nil
}

func decodeCLINodeProfileObservedForApply(raw json.RawMessage, observed *cliNodeProfileObserved) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("must be an object: %w", err)
	}
	if fields == nil {
		return errors.New("must be an object")
	}
	for _, field := range []string{"capabilities", "online", "lastSeenAt", "agentVersion", "protocolVersion", "profileState"} {
		value, ok := fields[field]
		if !ok {
			return fmt.Errorf("missing required field %q", field)
		}
		if field != "lastSeenAt" && rawJSONIsNull(value) {
			return fmt.Errorf("field %q must not be null", field)
		}
	}
	if err := json.Unmarshal(fields["capabilities"], &observed.Capabilities); err != nil || observed.Capabilities == nil {
		if err == nil {
			err = errors.New("must be an array of strings")
		}
		return fmt.Errorf("field %q %w", "capabilities", err)
	}
	if err := json.Unmarshal(fields["online"], &observed.Online); err != nil {
		return fmt.Errorf("field %q must be a boolean: %w", "online", err)
	}
	lastSeen := bytes.TrimSpace(fields["lastSeenAt"])
	if !rawJSONIsNull(fields["lastSeenAt"]) {
		var value string
		if err := json.Unmarshal(lastSeen, &value); err != nil {
			return fmt.Errorf("field %q must be a string or null: %w", "lastSeenAt", err)
		}
	}
	observed.LastSeenAt = append(observed.LastSeenAt[:0], lastSeen...)
	if err := decodeNodeProfileRequiredString(fields["agentVersion"], "agentVersion", &observed.AgentVersion); err != nil {
		return err
	}
	if err := decodeNodeProfileRequiredString(fields["protocolVersion"], "protocolVersion", &observed.ProtocolVersion); err != nil {
		return err
	}
	if err := decodeNodeProfileRequiredString(fields["profileState"], "profileState", &observed.ProfileState); err != nil {
		return err
	}
	return nil
}

func decodeCLINodeProfileApplyForApply(raw json.RawMessage, apply *cliNodeProfileApply) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("must be an object: %w", err)
	}
	if fields == nil {
		return errors.New("must be an object")
	}
	for _, field := range []string{"state", "reason"} {
		value, ok := fields[field]
		if !ok || rawJSONIsNull(value) {
			return fmt.Errorf("missing required field %q", field)
		}
	}
	if err := decodeNodeProfileRequiredString(fields["state"], "state", &apply.State); err != nil {
		return err
	}
	if err := decodeNodeProfileRequiredString(fields["reason"], "reason", &apply.Reason); err != nil {
		return err
	}
	return nil
}

func decodeNodeProfileRequiredString(raw json.RawMessage, field string, target *string) error {
	if rawJSONIsNull(raw) {
		return fmt.Errorf("field %q must be a string", field)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("field %q must be a string: %w", field, err)
	}
	return nil
}

func decodeNodeProfileRequiredInt64(raw json.RawMessage, field string, target *int64) error {
	if rawJSONIsNull(raw) {
		return fmt.Errorf("field %q must be an integer", field)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("field %q must be an integer: %w", field, err)
	}
	return nil
}

func validateCLINodeProfileApplyPreflight(response cliNodeProfileResponse, nodeID string) (int64, error) {
	if response.NodeID != nodeID {
		return 0, fmt.Errorf("node profile response nodeId %q does not match requested node %q", response.NodeID, nodeID)
	}
	if response.Desired.State != "configured" {
		return 0, fmt.Errorf("node profile apply requires a configured desired profile, got %q", response.Desired.State)
	}
	if response.Desired.Revision == nil || *response.Desired.Revision < 1 {
		return 0, errors.New("node profile apply requires desired revision at least 1")
	}
	if response.Desired.Profile == nil {
		return 0, errors.New("node profile apply requires a non-null desired profile")
	}
	if err := validateCLINodeProfile(*response.Desired.Profile); err != nil {
		return 0, fmt.Errorf("server returned invalid node profile: %w", err)
	}
	if !response.Observed.Online {
		return 0, errors.New("node profile apply requires an online managed node")
	}
	if !containsCLINodeProfileCapability(response.Observed.Capabilities, "agent.profile.apply") {
		return 0, errors.New("managed agent does not advertise agent.profile.apply")
	}
	switch response.Apply.State {
	case "queued", "running", "awaiting_heartbeat":
		return 0, fmt.Errorf("node profile apply is already in progress (%s)", response.Apply.State)
	case "manual_required":
		return 0, errors.New("node profile apply is not supported by this managed agent (manual_required)")
	case "not_requested", "applied", "failed", "drifted":
		return *response.Desired.Revision, nil
	default:
		return 0, fmt.Errorf("node profile response has unsupported apply state %q", response.Apply.State)
	}
}

func containsCLINodeProfileCapability(capabilities []string, wanted string) bool {
	for _, capability := range capabilities {
		if capability == wanted {
			return true
		}
	}
	return false
}

func waitForCLINodeProfileApply(ctx context.Context, client *apiClient, nodeID string) ([]byte, error) {
	endpoint := nodeProfileEndpoint(nodeID)
	for {
		raw, err := client.request(ctx, http.MethodGet, endpoint, nil, true)
		if err != nil {
			return nil, err
		}
		response, err := decodeCLINodeProfileResponseForApply(raw, endpoint)
		if err != nil {
			return nil, err
		}
		if response.NodeID != nodeID {
			return nil, fmt.Errorf("node profile response nodeId %q does not match requested node %q", response.NodeID, nodeID)
		}
		switch response.Apply.State {
		case "applied", "failed", "drifted", "manual_required":
			return raw, nil
		case "queued", "running", "awaiting_heartbeat":
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("node profile apply wait timed out: %w", ctx.Err())
			case <-time.After(nodeProfileApplyPollTime):
			}
		default:
			return nil, fmt.Errorf("node profile response has unsupported apply state %q", response.Apply.State)
		}
	}
}

func (response cliNodeProfileResponse) desiredRevision() (int64, error) {
	if response.Desired.Revision == nil {
		return 0, errors.New("node profile response is missing desired revision")
	}
	if *response.Desired.Revision < 0 {
		return 0, errors.New("node profile response has a negative desired revision")
	}
	return *response.Desired.Revision, nil
}

func readCLINodeProfileFile(path string) (cliNodeProfile, error) {
	var profile cliNodeProfile
	if strings.TrimSpace(path) == "" {
		return profile, errors.New("node profile file path must not be empty")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return profile, fmt.Errorf("read node profile file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return profile, errors.New("node profile file must be a regular, non-symlink file")
	}
	if info.Size() > maxNodeProfileFileBytes {
		return profile, fmt.Errorf("node profile file exceeds %d bytes", maxNodeProfileFileBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return profile, fmt.Errorf("read node profile file: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxNodeProfileFileBytes+1))
	if err != nil {
		return profile, fmt.Errorf("read node profile file: %w", err)
	}
	if len(data) > maxNodeProfileFileBytes {
		return profile, fmt.Errorf("node profile file exceeds %d bytes", maxNodeProfileFileBytes)
	}
	return decodeCLINodeProfile(data)
}

func decodeCLINodeProfile(data []byte) (cliNodeProfile, error) {
	var profile cliNodeProfile
	if !utf8.Valid(data) {
		return profile, errors.New("node profile file must be valid UTF-8 JSON")
	}
	var fields map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&fields); err != nil {
		return profile, fmt.Errorf("invalid node profile JSON: %w", err)
	}
	if fields == nil {
		return profile, errors.New("node profile JSON must be an object")
	}
	if err := ensureNodeProfileJSONEOF(decoder); err != nil {
		return profile, err
	}
	for _, field := range []string{
		"allowDeployRead",
		"allowDeployActions",
		"allowDeployDomainRead",
		"allowDeployDomainActions",
		"deployPlansFile",
		"deployAcmeWebroot",
		"deployWriteRoots",
	} {
		if _, ok := fields[field]; !ok {
			return profile, fmt.Errorf("node profile JSON is missing required field %q", field)
		}
	}
	if err := validateCLINodeProfileJSONFieldTypes(fields); err != nil {
		return profile, err
	}
	strictDecoder := json.NewDecoder(bytes.NewReader(data))
	strictDecoder.DisallowUnknownFields()
	if err := strictDecoder.Decode(&profile); err != nil {
		return profile, fmt.Errorf("invalid node profile JSON: %w", err)
	}
	if err := ensureNodeProfileJSONEOF(strictDecoder); err != nil {
		return profile, err
	}
	if err := validateCLINodeProfile(profile); err != nil {
		return profile, err
	}
	return profile, nil
}

func validateCLINodeProfileJSONFieldTypes(fields map[string]json.RawMessage) error {
	for _, field := range []string{
		"allowDeployRead",
		"allowDeployActions",
		"allowDeployDomainRead",
		"allowDeployDomainActions",
	} {
		value := bytes.TrimSpace(fields[field])
		if !bytes.Equal(value, []byte("true")) && !bytes.Equal(value, []byte("false")) {
			return fmt.Errorf("node profile field %q must be a boolean", field)
		}
	}
	for _, field := range []string{
		"deployPlansFile",
		"deployAcmeWebroot",
	} {
		value := bytes.TrimSpace(fields[field])
		if len(value) == 0 || value[0] != '"' {
			return fmt.Errorf("node profile field %q must be a string", field)
		}
	}
	writeRoots := bytes.TrimSpace(fields["deployWriteRoots"])
	if len(writeRoots) == 0 || writeRoots[0] != '[' {
		return errors.New("node profile field \"deployWriteRoots\" must be an array of strings")
	}
	var roots []json.RawMessage
	if err := json.Unmarshal(writeRoots, &roots); err != nil {
		return fmt.Errorf("node profile field %q must be an array of strings: %w", "deployWriteRoots", err)
	}
	for index, root := range roots {
		value := bytes.TrimSpace(root)
		if len(value) == 0 || value[0] != '"' {
			return fmt.Errorf("node profile field %q entry %d must be a string", "deployWriteRoots", index)
		}
	}
	return nil
}

func ensureNodeProfileJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("node profile JSON contains trailing data")
		}
		return fmt.Errorf("invalid trailing node profile JSON: %w", err)
	}
	return nil
}

func validateCLINodeProfile(profile cliNodeProfile) error {
	if profile.AllowDeployActions && !profile.AllowDeployRead {
		return errors.New("allowDeployActions requires allowDeployRead")
	}
	if profile.AllowDeployDomainRead && !profile.AllowDeployRead {
		return errors.New("allowDeployDomainRead requires allowDeployRead")
	}
	if profile.AllowDeployDomainActions && !profile.AllowDeployDomainRead {
		return errors.New("allowDeployDomainActions requires allowDeployDomainRead")
	}
	if err := validateCLIProfilePath(profile.DeployPlansFile, "deployPlansFile", true); err != nil {
		return err
	}
	if err := validateCLIProfilePath(profile.DeployAcmeWebroot, "deployAcmeWebroot", false); err != nil {
		return err
	}
	if profile.DeployWriteRoots == nil {
		return errors.New("deployWriteRoots must be an array")
	}
	if len(profile.DeployWriteRoots) > maxNodeProfileWriteRoots {
		return fmt.Errorf("deployWriteRoots accepts at most %d roots", maxNodeProfileWriteRoots)
	}
	seen := make(map[string]struct{}, len(profile.DeployWriteRoots))
	for _, root := range profile.DeployWriteRoots {
		if root == "" {
			return errors.New("deployWriteRoots must not contain an empty root")
		}
		if err := validateCLIProfilePath(root, "deployWriteRoots", false); err != nil {
			return err
		}
		if _, exists := seen[root]; exists {
			return fmt.Errorf("deployWriteRoots contains duplicate root %q", root)
		}
		seen[root] = struct{}{}
	}
	return nil
}

func validateCLIProfilePath(value, field string, file bool) error {
	if value == "" {
		return nil
	}
	if err := validateCLIProfileString(value, field); err != nil {
		return err
	}
	if value == "/" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return fmt.Errorf("%s must be a clean absolute path other than /", field)
	}
	if !cliNodeProfileSafePathPattern.MatchString(value) {
		return fmt.Errorf("%s must match ^[A-Za-z0-9._/+:-]+$", field)
	}
	if file && strings.HasSuffix(value, "/") {
		return fmt.Errorf("%s must not have a trailing slash", field)
	}
	return nil
}

func validateCLIProfileString(value, field string) error {
	if !utf8.ValidString(value) || len([]byte(value)) > maxNodeProfilePathBytes || strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("%s is invalid or exceeds %d UTF-8 bytes", field, maxNodeProfilePathBytes)
	}
	return nil
}

func writeCLINodeProfileEnvFragment(out io.Writer, profile cliNodeProfile) error {
	lines := []string{
		"HSERVER_AGENT_ALLOW_DEPLOY_READ=" + strconv.FormatBool(profile.AllowDeployRead),
		"HSERVER_AGENT_ALLOW_DEPLOY_ACTIONS=" + strconv.FormatBool(profile.AllowDeployActions),
		"HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_READ=" + strconv.FormatBool(profile.AllowDeployDomainRead),
		"HSERVER_AGENT_ALLOW_DEPLOY_DOMAIN_ACTIONS=" + strconv.FormatBool(profile.AllowDeployDomainActions),
		"HSERVER_AGENT_DEPLOY_PLANS_FILE=" + profile.DeployPlansFile,
		"HSERVER_AGENT_DEPLOY_ACME_WEBROOT=" + profile.DeployAcmeWebroot,
		"HSERVER_AGENT_DEPLOY_WRITE_ROOTS=" + strings.Join(profile.DeployWriteRoots, ","),
	}
	_, err := io.WriteString(out, strings.Join(lines, "\n")+"\n")
	return err
}
