package agenthub

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	profileStateConfigured      = "configured"
	profileStateNotConfigured   = "not_configured"
	profileObservedNotReported  = "not_reported"
	profileApplyManualRequired  = "manual_required"
	profileApplySelfApplyReason = "self_apply_not_supported"
	profileApplyNotRequested    = "not_requested"
	profileApplyQueued          = "queued"
	profileApplyRunning         = "running"
	profileApplyAwaiting        = "awaiting_heartbeat"
	profileApplyApplied         = "applied"
	profileApplyFailed          = "failed"
	profileApplyDrifted         = "drifted"
	maxProfilePathBytes         = 4096
	maxProfileWriteRoots        = 16
	// The profile schema is bounded by its path/root limits; these explicit
	// envelope limits keep a future schema expansion from turning a task into
	// an unbounded queue payload.
	maxProfileJSONBytes       = 16 << 10
	maxProfileEncodedBytes    = ((maxProfileJSONBytes + 2) / 3) * 4
	profileApplySchemaVersion = 1
)

const (
	// AgentProfileObservationState values are the only states accepted from
	// a managed agent heartbeat. A missing observation is represented by a
	// missing row, and is exposed as not_reported by the panel.
	ProfileObservationNotConfigured    = "not_configured"
	ProfileObservationPendingRestart   = "pending_restart"
	ProfileObservationApplied          = "applied"
	ProfileObservationFailed           = "failed"
	ProfileApplyResultRestartScheduled = "restart_scheduled"
	ProfileApplyResultAlreadyActive    = "already_active"

	// Profile apply result/error codes are intentionally closed. They are
	// suitable for an operator-facing status and never carry paths, command
	// output, or other agent-local details.
	ProfileErrorCodeNotConfigured      = "not_configured"
	ProfileErrorCodeInvalidProfile     = "invalid_profile"
	ProfileErrorCodePermissionDenied   = "permission_denied"
	ProfileErrorCodeWriteFailed        = "write_failed"
	ProfileErrorCodeRestartFailed      = "restart_failed"
	ProfileErrorCodeApplyFailed        = "apply_failed"
	ProfileErrorCodeProfileApplyFailed = "profile_apply_failed"
	ProfileErrorCodeUnsupported        = "unsupported"
	ProfileErrorCodeNotSupported       = "not_supported"
	ProfileErrorCodeTimeout            = "timeout"
	ProfileErrorCodeStaleRevision      = "stale_revision"
	ProfileErrorCodeInvalidRevision    = "invalid_revision"
	ProfileErrorCodeAgentError         = "agent_error"
	ProfileErrorCodeUnknown            = "unknown"
	ProfileErrorCodeMissing            = "profile_missing"
	ProfileErrorCodeCorrupt            = "profile_corrupt"
	ProfileErrorCodeStateCorrupt       = "profile_state_corrupt"
	ProfileErrorCodeRevisionInvalid    = "profile_revision_invalid"
	ProfileErrorCodePayloadInvalid     = "profile_payload_invalid"
	ProfileErrorCodePayloadTooLarge    = "profile_payload_too_large"
	ProfileErrorCodeApplyUnavailable   = "profile_apply_unavailable"
	ProfileErrorCodeScheduleFailed     = "profile_schedule_failed"
	ProfileErrorCodeStoreFailed        = "profile_store_failed"
	ProfileErrorCodeSuperseded         = "profile_superseded"
)

var (
	ErrProfileRevisionStale = errors.New("agent hub: profile revision is stale")
	ErrProfileNotConfigured = errors.New("agent hub: profile is not configured")
	ErrProfileApplyInFlight = errors.New("agent hub: profile apply is already in flight")
	// ErrProfileApplyConflict is an alias with the name used by callers that
	// want to distinguish a different in-flight revision from a generic
	// revision conflict. Both values intentionally match for errors.Is.
	ErrProfileApplyConflict = ErrProfileApplyInFlight
	profileSafePathPattern  = regexp.MustCompile(`^[A-Za-z0-9._/+:-]+$`)
)

// AgentProfileObservation is the bounded profile state reported by a managed
// agent. It contains only a revision, a finite state, and a safe error code;
// raw agent errors and paths never cross the heartbeat boundary.
type AgentProfileObservation struct {
	State     string `json:"state"`
	Revision  int64  `json:"revision"`
	ErrorCode string `json:"error_code,omitempty"`
}

// ProfileObservation is kept as a short compatibility alias for transport
// adapters and tests that use the domain name without the Agent prefix.
type ProfileObservation = AgentProfileObservation

// AgentProfileApplyObservation is a descriptive alias for callers that use
// the apply protocol name rather than the heartbeat observation name.
type AgentProfileApplyObservation = AgentProfileObservation

// UnmarshalJSON keeps the additive heartbeat field closed and bounded. The
// state/revision pair is required whenever the optional profile object is
// present, while error_code remains optional and must stay in the safe enum.
func (observation *AgentProfileObservation) UnmarshalJSON(data []byte) error {
	fields, err := decodeStrictJSONObject(data)
	if err != nil {
		return err
	}
	for field := range fields {
		switch field {
		case "state", "revision", "error_code":
		default:
			return fmt.Errorf("agent hub: unknown profile observation field %q", field)
		}
	}
	var state string
	if err := decodeRequiredJSONField(fields, "state", &state); err != nil {
		return err
	}
	var revision int64
	if err := decodeRequiredJSONField(fields, "revision", &revision); err != nil {
		return err
	}
	errorCode := ""
	if raw, ok := fields["error_code"]; ok {
		if isJSONNull(raw) {
			return errors.New("agent hub: profile observation error_code must be a string")
		}
		if err := json.Unmarshal(raw, &errorCode); err != nil {
			return fmt.Errorf("agent hub: profile observation error_code must be a string: %w", err)
		}
	}
	value := AgentProfileObservation{State: state, Revision: revision, ErrorCode: errorCode}
	if err := validateAgentProfileObservation(&value); err != nil {
		return err
	}
	*observation = value
	return nil
}

// AgentProfileObservationRecord is the persisted last observation. Absence
// of a row means not_reported; the table therefore never needs a synthetic
// not_reported enum value.
type AgentProfileObservationRecord struct {
	State     string
	Revision  int64
	ErrorCode string
	UpdatedAt time.Time
}

// profileApplyDocument is the strict, versioned document carried inside the
// profile_json_b64 task field. The outer task payload remains exactly the two
// strings revision and profile_json_b64; this envelope lets an agent validate
// the document independently before touching local state.
type profileApplyDocument struct {
	SchemaVersion int          `json:"schema_version"`
	Revision      int64        `json:"revision"`
	Profile       AgentProfile `json:"profile"`
}

// AgentProfileApplyRequest is the strict admin apply envelope. The profile
// body is deliberately absent: the service always reads the desired profile
// for the requested compare-and-swap revision.
type AgentProfileApplyRequest struct {
	ExpectedRevision int64 `json:"expectedRevision"`
	Confirmed        bool  `json:"confirmed"`
}

// UnmarshalJSON enforces the exact apply body. This keeps apply requests from
// smuggling a second profile or arbitrary task payload into the queue.
func (request *AgentProfileApplyRequest) UnmarshalJSON(data []byte) error {
	fields, err := decodeStrictJSONObject(data)
	if err != nil {
		return err
	}
	for field := range fields {
		if field != "expectedRevision" && field != "confirmed" {
			return fmt.Errorf("agent hub: unknown profile apply request field %q", field)
		}
	}
	revisionRaw, ok := fields["expectedRevision"]
	if !ok || isJSONNull(revisionRaw) {
		return errors.New("agent hub: profile apply request requires a non-null expectedRevision")
	}
	confirmedRaw, ok := fields["confirmed"]
	if !ok || isJSONNull(confirmedRaw) {
		return errors.New("agent hub: profile apply request requires confirmed:true")
	}
	var revision int64
	if err := json.Unmarshal(revisionRaw, &revision); err != nil {
		return fmt.Errorf("agent hub: profile apply expectedRevision must be an integer: %w", err)
	}
	var confirmed bool
	if err := json.Unmarshal(confirmedRaw, &confirmed); err != nil {
		return fmt.Errorf("agent hub: profile apply confirmed must be a boolean: %w", err)
	}
	if revision < 1 {
		return fmt.Errorf("agent hub: profile apply expectedRevision must be at least 1: %w", ErrInvalidInput)
	}
	if !confirmed {
		return fmt.Errorf("agent hub: profile apply requires confirmed:true: %w", ErrInvalidInput)
	}
	*request = AgentProfileApplyRequest{ExpectedRevision: revision, Confirmed: confirmed}
	return nil
}

// AgentProfile is the bounded desired deployment capability profile kept by
// the panel. It is intentionally not an environment map: paths are optional,
// clean absolute values and no secret, executable, or arbitrary key is part of
// the schema.
type AgentProfile struct {
	AllowDeployRead          bool     `json:"allowDeployRead"`
	AllowDeployActions       bool     `json:"allowDeployActions"`
	AllowDeployDomainRead    bool     `json:"allowDeployDomainRead"`
	AllowDeployDomainActions bool     `json:"allowDeployDomainActions"`
	DeployPlansFile          string   `json:"deployPlansFile"`
	DeployAcmeWebroot        string   `json:"deployAcmeWebroot"`
	DeployWriteRoots         []string `json:"deployWriteRoots"`
}

// UnmarshalJSON is intentionally stricter than the zero-value Go struct. The
// public PUT contract requires every profile field to be present, non-null,
// and of the declared JSON type. Persisted legacy rows are decoded through
// decodePersistedAgentProfile instead, where only the historical string form
// of deployWriteRoots is tolerated.
func (profile *AgentProfile) UnmarshalJSON(data []byte) error {
	decoded, err := decodeAgentProfileJSON(data, false)
	if err != nil {
		return err
	}
	*profile = decoded
	return nil
}

// AgentProfilePutRequest is the strict panel-session PUT body. Revision zero
// is the compare-and-swap value for a node without a stored profile.
type AgentProfilePutRequest struct {
	Profile          AgentProfile `json:"profile"`
	ExpectedRevision int64        `json:"expectedRevision"`
}

// UnmarshalJSON enforces the envelope's required, non-null fields before the
// service or compare-and-swap layer is reached.
func (request *AgentProfilePutRequest) UnmarshalJSON(data []byte) error {
	fields, err := decodeStrictJSONObject(data)
	if err != nil {
		return err
	}
	for field := range fields {
		if field != "profile" && field != "expectedRevision" {
			return fmt.Errorf("agent hub: unknown profile request field %q", field)
		}
	}
	profileRaw, ok := fields["profile"]
	if !ok || isJSONNull(profileRaw) {
		return errors.New("agent hub: profile request requires a non-null profile")
	}
	revisionRaw, ok := fields["expectedRevision"]
	if !ok || isJSONNull(revisionRaw) {
		return errors.New("agent hub: profile request requires a non-null expectedRevision")
	}
	profile, err := decodeAgentProfileJSON(profileRaw, false)
	if err != nil {
		return err
	}
	var revision int64
	if err := json.Unmarshal(revisionRaw, &revision); err != nil {
		return fmt.Errorf("agent hub: expectedRevision must be an integer: %w", err)
	}
	*request = AgentProfilePutRequest{Profile: profile, ExpectedRevision: revision}
	return nil
}

// AgentProfileRecord is the persisted desired profile and monotonic revision.
type AgentProfileRecord struct {
	Profile    AgentProfile
	Revision   int64
	Configured bool
}

type AgentProfileDesiredResponse struct {
	State    string        `json:"state"`
	Revision int64         `json:"revision"`
	Profile  *AgentProfile `json:"profile"`
}

type AgentProfileObservedResponse struct {
	Capabilities     []string   `json:"capabilities"`
	Online           bool       `json:"online"`
	LastSeenAt       *time.Time `json:"lastSeenAt"`
	AgentVersion     string     `json:"agentVersion"`
	ProtocolVersion  string     `json:"protocolVersion"`
	ProfileState     string     `json:"profileState"`
	ProfileRevision  *int64     `json:"profileRevision"`
	ProfileErrorCode *string    `json:"profileErrorCode"`
}

type AgentProfileApplyResponse struct {
	State            string  `json:"state"`
	Reason           string  `json:"reason"`
	DesiredRevision  *int64  `json:"desiredRevision"`
	TaskID           *int64  `json:"taskId"`
	ObservedRevision *int64  `json:"observedRevision"`
	ObservedState    *string `json:"observedState"`
}

// MarshalJSON retains the phase-one two-field manual_required wire shape for
// the legacy manual path. Additive phase-two fields are emitted for the
// machine-readable apply states.
func (response AgentProfileApplyResponse) MarshalJSON() ([]byte, error) {
	if response.State == profileApplyManualRequired &&
		response.Reason == profileApplySelfApplyReason {
		return json.Marshal(struct {
			State  string `json:"state"`
			Reason string `json:"reason"`
		}{State: response.State, Reason: response.Reason})
	}
	type responseAlias AgentProfileApplyResponse
	return json.Marshal(responseAlias(response))
}

// AgentProfileResponse deliberately separates panel desired state from raw
// node observation and keeps task acknowledgement distinct from applied
// state, which is authoritative only after a heartbeat observation.
type AgentProfileResponse struct {
	NodeID   string                       `json:"nodeId"`
	Desired  AgentProfileDesiredResponse  `json:"desired"`
	Observed AgentProfileObservedResponse `json:"observed"`
	Apply    AgentProfileApplyResponse    `json:"apply"`
}

func emptyAgentProfile() AgentProfile {
	return AgentProfile{}
}

func decodePersistedAgentProfile(data []byte) (AgentProfile, error) {
	return decodeAgentProfileJSON(data, true)
}

func decodeAgentProfileJSON(data []byte, allowLegacyRoots bool) (AgentProfile, error) {
	fields, err := decodeStrictJSONObject(data)
	if err != nil {
		return AgentProfile{}, err
	}
	for field := range fields {
		switch field {
		case "allowDeployRead", "allowDeployActions", "allowDeployDomainRead", "allowDeployDomainActions",
			"deployPlansFile", "deployAcmeWebroot", "deployWriteRoots":
		default:
			return AgentProfile{}, fmt.Errorf("agent hub: unknown profile field %q", field)
		}
	}
	var profile AgentProfile
	if err := decodeRequiredJSONField(fields, "allowDeployRead", &profile.AllowDeployRead); err != nil {
		return AgentProfile{}, err
	}
	if err := decodeRequiredJSONField(fields, "allowDeployActions", &profile.AllowDeployActions); err != nil {
		return AgentProfile{}, err
	}
	if err := decodeRequiredJSONField(fields, "allowDeployDomainRead", &profile.AllowDeployDomainRead); err != nil {
		return AgentProfile{}, err
	}
	if err := decodeRequiredJSONField(fields, "allowDeployDomainActions", &profile.AllowDeployDomainActions); err != nil {
		return AgentProfile{}, err
	}
	if err := decodeRequiredJSONField(fields, "deployPlansFile", &profile.DeployPlansFile); err != nil {
		return AgentProfile{}, err
	}
	if err := decodeRequiredJSONField(fields, "deployAcmeWebroot", &profile.DeployAcmeWebroot); err != nil {
		return AgentProfile{}, err
	}
	rootsRaw, ok := fields["deployWriteRoots"]
	if !ok || isJSONNull(rootsRaw) {
		return AgentProfile{}, errors.New("agent hub: profile field deployWriteRoots is required and non-null")
	}
	if allowLegacyRoots && len(bytes.TrimSpace(rootsRaw)) > 0 && bytes.TrimSpace(rootsRaw)[0] == '"' {
		var legacyRoots string
		if err := json.Unmarshal(rootsRaw, &legacyRoots); err != nil {
			return AgentProfile{}, fmt.Errorf("agent hub: decode legacy deployWriteRoots: %w", err)
		}
		if legacyRoots == "" {
			profile.DeployWriteRoots = []string{}
		} else {
			profile.DeployWriteRoots = strings.Split(legacyRoots, ",")
			for index := range profile.DeployWriteRoots {
				profile.DeployWriteRoots[index] = strings.TrimSpace(profile.DeployWriteRoots[index])
			}
		}
	} else {
		roots, err := decodeProfileRoots(rootsRaw)
		if err != nil {
			return AgentProfile{}, err
		}
		profile.DeployWriteRoots = roots
	}
	return profile, nil
}

func decodeRequiredJSONField(fields map[string]json.RawMessage, field string, target any) error {
	raw, ok := fields[field]
	if !ok || isJSONNull(raw) {
		return fmt.Errorf("agent hub: profile field %s is required and non-null", field)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("agent hub: profile field %s has the wrong JSON type: %w", field, err)
	}
	return nil
}

func decodeProfileRoots(raw json.RawMessage) ([]string, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, fmt.Errorf("agent hub: profile field deployWriteRoots must be an array of strings: %w", err)
	}
	if items == nil {
		return nil, errors.New("agent hub: profile field deployWriteRoots is required and non-null")
	}
	roots := make([]string, len(items))
	for index, item := range items {
		if isJSONNull(item) {
			return nil, fmt.Errorf("agent hub: profile field deployWriteRoots[%d] must be a non-null string", index)
		}
		if err := json.Unmarshal(item, &roots[index]); err != nil {
			return nil, fmt.Errorf("agent hub: profile field deployWriteRoots[%d] must be a string: %w", index, err)
		}
	}
	return roots, nil
}

func decodeProfileApplyDocument(data []byte) (profileApplyDocument, error) {
	if len(data) == 0 || len(data) > maxProfileJSONBytes {
		return profileApplyDocument{}, fmt.Errorf("profile apply document is too large")
	}
	fields, err := decodeStrictJSONObject(data)
	if err != nil {
		return profileApplyDocument{}, err
	}
	for field := range fields {
		switch field {
		case "schema_version", "revision", "profile":
		default:
			return profileApplyDocument{}, fmt.Errorf("unknown profile apply document field")
		}
	}
	var document profileApplyDocument
	if err := decodeRequiredJSONField(fields, "schema_version", &document.SchemaVersion); err != nil || document.SchemaVersion != profileApplySchemaVersion {
		return profileApplyDocument{}, fmt.Errorf("profile apply document schema is invalid")
	}
	if err := decodeRequiredJSONField(fields, "revision", &document.Revision); err != nil || document.Revision < 1 {
		return profileApplyDocument{}, fmt.Errorf("profile apply document revision is invalid")
	}
	profileRaw, ok := fields["profile"]
	if !ok || isJSONNull(profileRaw) {
		return profileApplyDocument{}, fmt.Errorf("profile apply document profile is required")
	}
	profile, err := decodeAgentProfileJSON(profileRaw, false)
	if err != nil {
		return profileApplyDocument{}, err
	}
	document.Profile, err = NormalizeAgentProfile(profile)
	if err != nil {
		return profileApplyDocument{}, err
	}
	canonical, err := json.Marshal(document)
	if err != nil || string(canonical) != string(data) {
		return profileApplyDocument{}, fmt.Errorf("profile apply document is not canonical")
	}
	return document, nil
}

// canonicalProfileApplyDocument is the one desired-state/apply envelope
// encoder. SaveNodeProfile calls it with the prospective revision before the
// compare-and-swap write, so every persisted desired profile is guaranteed to
// fit the agent's inclusive 16 KiB candidate boundary.
func canonicalProfileApplyDocument(profile AgentProfile, revision int64) (AgentProfile, []byte, error) {
	if revision < 1 {
		return AgentProfile{}, nil, fmt.Errorf("agent hub: profile apply revision: %w", ErrInvalidInput)
	}
	normalized, err := NormalizeAgentProfile(profile)
	if err != nil {
		return AgentProfile{}, nil, err
	}
	document, err := json.Marshal(profileApplyDocument{
		SchemaVersion: profileApplySchemaVersion,
		Revision:      revision,
		Profile:       normalized,
	})
	if err != nil {
		return AgentProfile{}, nil, fmt.Errorf("agent hub: encode profile apply document: %w", err)
	}
	if len(document) > maxProfileJSONBytes {
		return AgentProfile{}, nil, fmt.Errorf("agent hub: canonical profile apply document exceeds %d bytes: %w", maxProfileJSONBytes, ErrInvalidInput)
	}
	return normalized, document, nil
}

// decodeStrictJSONObject parses one complete JSON object and rejects duplicate
// keys. encoding/json's map decoding otherwise silently keeps the last value,
// which would make a supposedly exact profile/task envelope ambiguous.
func decodeStrictJSONObject(data []byte) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := token.(json.Delim)
	if !ok || delim != '{' {
		return nil, errors.New("agent hub: JSON value must be an object")
	}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, errors.New("agent hub: JSON object key is invalid")
		}
		if _, exists := fields[key]; exists {
			return nil, fmt.Errorf("agent hub: duplicate JSON object field %q", key)
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return nil, err
		}
		fields[key] = raw
	}
	closing, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if closing != json.Delim('}') {
		return nil, errors.New("agent hub: JSON object is not closed")
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, errors.New("agent hub: JSON object contains trailing data")
		}
		return nil, err
	}
	return fields, nil
}

func isJSONNull(raw []byte) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// NormalizeAgentProfile validates the public profile and canonicalizes the
// write-root list so equivalent input has one deterministic stored form.
func NormalizeAgentProfile(profile AgentProfile) (AgentProfile, error) {
	if err := validateProfileDependencies(profile); err != nil {
		return AgentProfile{}, err
	}

	for name, value := range map[string]string{
		"deployPlansFile":   profile.DeployPlansFile,
		"deployAcmeWebroot": profile.DeployAcmeWebroot,
	} {
		if err := validateOptionalProfilePath(value, name == "deployPlansFile"); err != nil {
			return AgentProfile{}, err
		}
	}

	roots, err := normalizeProfileRoots(profile.DeployWriteRoots)
	if err != nil {
		return AgentProfile{}, err
	}
	profile.DeployWriteRoots = roots
	return profile, nil
}

func validateProfileDependencies(profile AgentProfile) error {
	if profile.AllowDeployActions && !profile.AllowDeployRead {
		return fmt.Errorf("agent hub: allowDeployActions requires allowDeployRead: %w", ErrInvalidInput)
	}
	if profile.AllowDeployDomainRead && !profile.AllowDeployRead {
		return fmt.Errorf("agent hub: allowDeployDomainRead requires allowDeployRead: %w", ErrInvalidInput)
	}
	if profile.AllowDeployDomainActions && !profile.AllowDeployDomainRead {
		return fmt.Errorf("agent hub: allowDeployDomainActions requires allowDeployDomainRead: %w", ErrInvalidInput)
	}
	return nil
}

func normalizeProfileRoots(raw []string) ([]string, error) {
	if len(raw) > maxProfileWriteRoots {
		return nil, fmt.Errorf("agent hub: deployWriteRoots accepts at most %d roots: %w", maxProfileWriteRoots, ErrInvalidInput)
	}
	seen := make(map[string]struct{}, len(raw))
	roots := make([]string, 0, len(raw))
	for _, root := range raw {
		if err := validateOptionalProfilePath(root, false); err != nil {
			return nil, fmt.Errorf("agent hub: deployWriteRoots: %w", err)
		}
		if root == "" {
			return nil, fmt.Errorf("agent hub: deployWriteRoots contains an empty root: %w", ErrInvalidInput)
		}
		if _, exists := seen[root]; exists {
			return nil, fmt.Errorf("agent hub: deployWriteRoots contains a duplicate root: %w", ErrInvalidInput)
		}
		seen[root] = struct{}{}
		roots = append(roots, root)
	}
	sort.Strings(roots)
	return roots, nil
}

func validateOptionalProfilePath(value string, file bool) error {
	if value == "" {
		return nil
	}
	if err := validateProfileString(value, "profile path"); err != nil {
		return err
	}
	if value == "/" || !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return fmt.Errorf("agent hub: profile path must be a clean absolute path other than /: %w", ErrInvalidInput)
	}
	if !profileSafePathPattern.MatchString(value) {
		return fmt.Errorf("agent hub: profile path must match ^[A-Za-z0-9._/+:-]+$: %w", ErrInvalidInput)
	}
	if file && strings.HasSuffix(value, "/") {
		return fmt.Errorf("agent hub: profile file path must not have a trailing slash: %w", ErrInvalidInput)
	}
	return nil
}

func validateProfileString(value, field string) error {
	if !utf8.ValidString(value) || len([]byte(value)) > maxProfilePathBytes || strings.ContainsAny(value, "\r\n\x00") {
		return fmt.Errorf("agent hub: %s is invalid or exceeds %d UTF-8 bytes: %w", field, maxProfilePathBytes, ErrInvalidInput)
	}
	return nil
}

func validateAgentProfileObservation(observation *AgentProfileObservation) error {
	if observation == nil {
		return nil
	}
	if observation.Revision < 0 {
		return fmt.Errorf("agent hub: profile observation revision must be non-negative: %w", ErrInvalidInput)
	}
	switch observation.State {
	case ProfileObservationNotConfigured, ProfileObservationPendingRestart, ProfileObservationApplied, ProfileObservationFailed:
	default:
		return fmt.Errorf("agent hub: profile observation state is invalid: %w", ErrInvalidInput)
	}
	if !isSafeProfileErrorCode(observation.ErrorCode) {
		return fmt.Errorf("agent hub: profile observation error code is invalid: %w", ErrInvalidInput)
	}
	return nil
}

func isSafeProfileErrorCode(value string) bool {
	switch value {
	case "",
		ProfileErrorCodeNotConfigured,
		ProfileErrorCodeInvalidProfile,
		ProfileErrorCodePermissionDenied,
		ProfileErrorCodeWriteFailed,
		ProfileErrorCodeRestartFailed,
		ProfileErrorCodeApplyFailed,
		ProfileErrorCodeProfileApplyFailed,
		ProfileErrorCodeUnsupported,
		ProfileErrorCodeNotSupported,
		ProfileErrorCodeTimeout,
		ProfileErrorCodeStaleRevision,
		ProfileErrorCodeInvalidRevision,
		ProfileErrorCodeAgentError,
		ProfileErrorCodeUnknown,
		ProfileErrorCodeMissing,
		ProfileErrorCodeCorrupt,
		ProfileErrorCodeStateCorrupt,
		ProfileErrorCodeRevisionInvalid,
		ProfileErrorCodePayloadInvalid,
		ProfileErrorCodePayloadTooLarge,
		ProfileErrorCodeApplyUnavailable,
		ProfileErrorCodeScheduleFailed,
		ProfileErrorCodeStoreFailed,
		ProfileErrorCodeSuperseded:
		return true
	default:
		return false
	}
}

func profileResponse(record AgentProfileRecord, node Node, online bool, observation *AgentProfileObservationRecord, latestTask *Task) AgentProfileResponse {
	desiredState := profileStateNotConfigured
	var desiredProfile *AgentProfile
	var desiredRevision *int64
	if record.Configured {
		desiredState = profileStateConfigured
		profile := record.Profile
		desiredProfile = &profile
		revision := record.Revision
		desiredRevision = &revision
	}
	capabilities := append([]string(nil), node.Capabilities...)
	capable := hasCapability(node.Capabilities, CapabilityProfileApply)
	if !capable {
		// A row can only be written by a capability-bearing heartbeat, but a
		// capability downgrade or a manually repaired database must not expose
		// an old applied observation as current.
		observation = nil
	}
	profileState := profileObservedNotReported
	var observedRevision *int64
	var observedErrorCode *string
	var observedState *string
	if observation != nil {
		profileState = observation.State
		revision := observation.Revision
		observedRevision = &revision
		errorCode := observation.ErrorCode
		observedErrorCode = &errorCode
		state := observation.State
		observedState = &state
	}
	apply := AgentProfileApplyResponse{
		State:            profileApplyManualRequired,
		Reason:           profileApplySelfApplyReason,
		DesiredRevision:  desiredRevision,
		ObservedRevision: observedRevision,
		ObservedState:    observedState,
	}
	// A node without the capability still needs the legacy manual path. A
	// capable node has a machine-readable idle state even before a desired
	// profile is saved; the apply operation itself still rejects that absence.
	if capable {
		apply.State = profileApplyNotRequested
		apply.Reason = ""
	}
	latestTaskRevision, latestTaskRevisionErr := int64(0), errors.New("no profile task")
	if latestTask != nil {
		latestTaskRevision, latestTaskRevisionErr = profileApplyPayloadRevisionFromTask(*latestTask)
	}
	latestTaskMatchesDesired := record.Configured && latestTask != nil && latestTaskRevisionErr == nil && latestTaskRevision == record.Revision
	if capable && latestTaskMatchesDesired {
		taskID := latestTask.ID
		apply.TaskID = &taskID
		switch latestTask.Status {
		case TaskStatusQueued:
			apply.State = profileApplyQueued
			apply.Reason = ""
		case TaskStatusRunning:
			apply.State = profileApplyRunning
			apply.Reason = ""
		case TaskStatusCompleted:
			apply.State = profileApplyAwaiting
			apply.Reason = ""
		case TaskStatusFailed:
			apply.State = profileApplyFailed
			if latestTask.Error != "" && isSafeProfileErrorCode(latestTask.Error) {
				apply.Reason = latestTask.Error
			} else {
				apply.Reason = ProfileErrorCodeProfileApplyFailed
			}
		}
	}
	if record.Configured && observation != nil {
		switch {
		case observation.State == ProfileObservationApplied && observation.Revision == record.Revision:
			apply.State = profileApplyApplied
			apply.Reason = ""
		case observation.State == ProfileObservationPendingRestart && observation.Revision == record.Revision:
			apply.State = profileApplyAwaiting
			apply.Reason = ""
		case observation.State == ProfileObservationFailed && observation.Revision == record.Revision:
			apply.State = profileApplyFailed
			apply.Reason = observation.ErrorCode
			if apply.Reason == "" {
				apply.Reason = ProfileErrorCodeProfileApplyFailed
			}
		case observation.Revision != record.Revision && !latestTaskMatchesDesired:
			apply.State = profileApplyDrifted
			apply.Reason = "profile_revision_drift"
		}
	}
	return AgentProfileResponse{
		NodeID: node.ID,
		Desired: AgentProfileDesiredResponse{
			State: desiredState, Revision: record.Revision, Profile: desiredProfile,
		},
		Observed: AgentProfileObservedResponse{
			Capabilities:     capabilities,
			Online:           online,
			LastSeenAt:       node.LastSeenAt,
			AgentVersion:     node.AgentVersion,
			ProtocolVersion:  node.ProtocolVersion,
			ProfileState:     profileState,
			ProfileRevision:  observedRevision,
			ProfileErrorCode: observedErrorCode,
		},
		Apply: apply,
	}
}
