// Package extensions exposes the reviewed optional-integration catalog.
//
// catalog.json is the source of truth for this package. It is embedded into
// the binary and parsed once on first use so API consumers cannot depend on a
// working-directory-relative file. Callers receive a defensive copy of the
// parsed DTO on every load.
package extensions

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"
)

//go:embed catalog.json
var embeddedCatalogJSON []byte

// Catalog is the schema-v1 optional integration catalog.
type Catalog struct {
	Schema        string        `json:"$schema,omitempty"`
	SchemaVersion int           `json:"schema_version"`
	Documentation Documentation `json:"documentation"`
	Entries       []Entry       `json:"entries"`
}

// Documentation identifies the human-readable catalog table and its marker
// convention.
type Documentation struct {
	TablePath        string `json:"table_path"`
	TableHeader      string `json:"table_header"`
	MarkerPrefix     string `json:"marker_prefix"`
	MarkerConvention string `json:"marker_convention"`
}

// Entry describes one optional or feature-specific integration.
type Entry struct {
	ID            string        `json:"id"`
	DisplayName   string        `json:"display_name"`
	Purpose       string        `json:"purpose"`
	Requirement   string        `json:"requirement"`
	DocsRowMarker string        `json:"docs_row_marker"`
	Classes       []string      `json:"classes"`
	Targets       []string      `json:"targets"`
	Configuration Configuration `json:"configuration"`
	Status        Status        `json:"status"`
	Agent         *Agent        `json:"agent,omitempty"`
	Evidence      Evidence      `json:"evidence"`
}

// Configuration describes installation-owned configuration names and their
// secret-storage boundary. It intentionally contains names and references,
// never secret values.
type Configuration struct {
	NonSecretKeys  []string `json:"non_secret_keys"`
	SecretKeyNames []string `json:"secret_key_names"`
	SecretFileRefs []string `json:"secret_file_refs"`
	Boundary       string   `json:"boundary"`
}

// Status maps implementation-specific observations to the canonical catalog
// states. It is metadata and does not represent a live health observation.
type Status struct {
	CanonicalStates  []string          `json:"canonical_states"`
	RawStateMappings []RawStateMapping `json:"raw_state_mappings"`
	APIRoutePrefixes []string          `json:"api_route_prefixes"`
}

// RawStateMapping describes one raw-to-canonical status mapping.
type RawStateMapping struct {
	Raw       string `json:"raw"`
	Canonical string `json:"canonical"`
	Meaning   string `json:"meaning"`
}

// Agent describes the fixed managed-node task/capability contract for an
// integration that supports managed nodes.
type Agent struct {
	Tasks        []string      `json:"tasks"`
	Capabilities []string      `json:"capabilities"`
	Evidence     AgentEvidence `json:"evidence"`
}

// AgentEvidence keeps task and capability evidence separate.
type AgentEvidence struct {
	Tasks        []EvidenceItem `json:"tasks"`
	Capabilities []EvidenceItem `json:"capabilities"`
}

// Evidence groups source references by surface.
type Evidence struct {
	Web   []EvidenceItem `json:"web"`
	Docs  []EvidenceItem `json:"docs"`
	Tests []EvidenceItem `json:"tests"`
}

// EvidenceItem references a repository file and the claim supported by it.
type EvidenceItem struct {
	Path  string `json:"path"`
	Claim string `json:"claim"`
}

var (
	catalogOnce  sync.Once
	catalogValue Catalog
	catalogErr   error
)

// LoadCatalog returns a defensive copy of the embedded schema-v1 catalog.
// Parsing is performed at most once. An invalid embedded asset is returned as
// an error at this explicit boundary rather than causing an init panic.
func LoadCatalog() (Catalog, error) {
	catalogOnce.Do(func() {
		catalogValue, catalogErr = parseCatalog(embeddedCatalogJSON)
	})
	if catalogErr != nil {
		return Catalog{}, catalogErr
	}
	return cloneCatalog(catalogValue), nil
}

// ParseCatalog parses and validates catalog JSON. It is exported so packaging
// and fixture tests can exercise malformed embedded-data paths without
// changing the compiled asset.
func ParseCatalog(data []byte) (Catalog, error) {
	return parseCatalog(data)
}

func parseCatalog(data []byte) (Catalog, error) {
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return Catalog{}, fmt.Errorf("parse catalog JSON: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var catalog Catalog
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("parse catalog JSON: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return Catalog{}, fmt.Errorf("parse catalog JSON: trailing value")
		}
		return Catalog{}, fmt.Errorf("parse catalog JSON: trailing data: %w", err)
	}

	if err := validateCatalog(catalog); err != nil {
		return Catalog{}, fmt.Errorf("validate catalog: %w", err)
	}
	return catalog, nil
}

// rejectDuplicateJSONKeys mirrors the catalog verifier's duplicate-key guard.
// encoding/json otherwise accepts a later duplicate value silently.
func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := walkJSONValue(decoder, "$"); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return fmt.Errorf("trailing JSON value")
		}
		return fmt.Errorf("trailing JSON data: %w", err)
	}
	return nil
}

func walkJSONValue(decoder *json.Decoder, path string) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		return nil
	}

	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("%s has a non-string object key", path)
			}
			if _, exists := seen[key]; exists {
				return fmt.Errorf("duplicate JSON key at %s.%s", path, key)
			}
			seen[key] = struct{}{}
			if err := walkJSONValue(decoder, path+"."+key); err != nil {
				return err
			}
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim('}') {
			return fmt.Errorf("%s object did not close", path)
		}
	case '[':
		index := 0
		for decoder.More() {
			if err := walkJSONValue(decoder, fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
			index++
		}
		end, err := decoder.Token()
		if err != nil {
			return err
		}
		if end != json.Delim(']') {
			return fmt.Errorf("%s array did not close", path)
		}
	default:
		return fmt.Errorf("%s has unexpected delimiter %q", path, delim)
	}
	return nil
}

// expectedIDs are the reviewed schema-v1 core entries. The catalog may grow
// with additional entries, but these IDs remain required for compatibility.
var expectedIDs = [...]string{
	"cloudflare.dns",
	"stalwart.mail",
	"mail.access",
	"backup.gdrive",
	"backup.snapshot.restic",
	"process.pm2",
	"web.nginx",
	"runtime.php_fpm",
	"firewall.ufw",
	"tls.certbot",
	"dns.bind9",
	"database.local",
	"container.docker",
	"storage.smartmontools",
	"notification.delivery",
}

var canonicalStates = [...]string{"not_configured", "unavailable", "healthy"}

var catalogIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

var catalogClasses = [...]string{
	"local_capability",
	"managed_node_capability",
	"provider_adapter",
	"client_surface",
}

var catalogTargets = [...]string{"local_host", "managed_node"}

const catalogMarkerPrefix = "optional-integrations:v1:"

func validateCatalog(catalog Catalog) error {
	if catalog.Schema != "" && catalog.Schema != "./catalog.schema.json" {
		return fmt.Errorf("$schema must be ./catalog.schema.json")
	}
	if catalog.SchemaVersion != 1 {
		return fmt.Errorf("schema_version must be 1")
	}
	if catalog.Documentation.TablePath != "docs/optional-integrations.md" {
		return fmt.Errorf("documentation.table_path must be docs/optional-integrations.md")
	}
	if catalog.Documentation.TableHeader != "Integration" {
		return fmt.Errorf("documentation.table_header must be Integration")
	}
	if catalog.Documentation.MarkerPrefix != "optional-integrations:v1:" {
		return fmt.Errorf("documentation.marker_prefix must be optional-integrations:v1:")
	}
	if catalog.Documentation.MarkerConvention != "marker_prefix + slug(display_name), where slug lowercases and joins non-alphanumeric runs with a hyphen" {
		return fmt.Errorf("documentation.marker_convention is not the v1 convention")
	}
	if len(catalog.Entries) < len(expectedIDs) {
		return fmt.Errorf("entries must contain at least %d objects", len(expectedIDs))
	}

	wantIDs := make(map[string]struct{}, len(expectedIDs))
	for _, id := range expectedIDs {
		wantIDs[id] = struct{}{}
	}
	seenIDs := make(map[string]struct{}, len(catalog.Entries))
	seenDisplays := make(map[string]struct{}, len(catalog.Entries))
	seenMarkers := make(map[string]struct{}, len(catalog.Entries))
	for index, entry := range catalog.Entries {
		if strings.TrimSpace(entry.ID) == "" || strings.TrimSpace(entry.DisplayName) == "" || strings.TrimSpace(entry.Purpose) == "" || strings.TrimSpace(entry.DocsRowMarker) == "" {
			return fmt.Errorf("entries[%d] has an empty required string", index)
		}
		if !catalogIDPattern.MatchString(entry.ID) {
			return fmt.Errorf("entries[%d] has invalid id %q", index, entry.ID)
		}
		if _, ok := seenIDs[entry.ID]; ok {
			return fmt.Errorf("entries contains duplicate id %q", entry.ID)
		}
		seenIDs[entry.ID] = struct{}{}
		if _, ok := seenDisplays[entry.DisplayName]; ok {
			return fmt.Errorf("entries contains duplicate display_name %q", entry.DisplayName)
		}
		seenDisplays[entry.DisplayName] = struct{}{}
		if _, ok := seenMarkers[entry.DocsRowMarker]; ok {
			return fmt.Errorf("entries contains duplicate docs_row_marker %q", entry.DocsRowMarker)
		}
		seenMarkers[entry.DocsRowMarker] = struct{}{}
		if entry.Requirement != "optional" && entry.Requirement != "feature_specific" {
			return fmt.Errorf("entries[%d].requirement is invalid", index)
		}
		if err := validateStringList(entry.Classes, true, fmt.Sprintf("entries[%d].classes", index)); err != nil {
			return err
		}
		for _, class := range entry.Classes {
			if !contains(catalogClasses[:], class) {
				return fmt.Errorf("entries[%d].classes contains invalid class %q", index, class)
			}
		}
		if err := validateStringList(entry.Targets, true, fmt.Sprintf("entries[%d].targets", index)); err != nil {
			return err
		}
		for _, target := range entry.Targets {
			if !contains(catalogTargets[:], target) {
				return fmt.Errorf("entries[%d].targets contains invalid target %q", index, target)
			}
		}
		expectedMarker, ok := catalogDocsRowMarker(entry.DisplayName)
		if !ok || entry.DocsRowMarker != expectedMarker {
			return fmt.Errorf("entries[%d].docs_row_marker must follow the stable slug convention: %s", index, expectedMarker)
		}
		if err := validateConfiguration(entry.Configuration, index); err != nil {
			return err
		}
		if err := validateStatus(entry.Status, index); err != nil {
			return err
		}
		if err := validateEvidence(entry.Evidence, fmt.Sprintf("entries[%d].evidence", index)); err != nil {
			return err
		}

		managed := contains(entry.Classes, "managed_node_capability") || contains(entry.Targets, "managed_node")
		if managed && entry.Agent == nil {
			return fmt.Errorf("entries[%d] managed support requires agent data", index)
		}
		if !managed && entry.Agent != nil {
			return fmt.Errorf("entries[%d] declares agent data without managed support", index)
		}
		if entry.Agent != nil {
			if err := validateAgent(*entry.Agent, index); err != nil {
				return err
			}
		}
	}
	for id := range wantIDs {
		if _, ok := seenIDs[id]; !ok {
			return fmt.Errorf("entries is missing id %q", id)
		}
	}
	return nil
}

func validateConfiguration(configuration Configuration, index int) error {
	for name, values := range map[string][]string{
		"non_secret_keys":  configuration.NonSecretKeys,
		"secret_key_names": configuration.SecretKeyNames,
		"secret_file_refs": configuration.SecretFileRefs,
	} {
		if values == nil {
			return fmt.Errorf("entries[%d].configuration.%s is required", index, name)
		}
		if err := validateStringList(values, false, fmt.Sprintf("entries[%d].configuration.%s", index, name)); err != nil {
			return err
		}
	}
	if strings.TrimSpace(configuration.Boundary) == "" {
		return fmt.Errorf("entries[%d].configuration.boundary must not be empty", index)
	}
	if overlap(configuration.NonSecretKeys, configuration.SecretKeyNames) {
		return fmt.Errorf("entries[%d].configuration repeats a key across secret boundaries", index)
	}
	return nil
}

func validateStatus(status Status, index int) error {
	if len(status.CanonicalStates) != len(canonicalStates) {
		return fmt.Errorf("entries[%d].status.canonical_states must declare the canonical trio", index)
	}
	for stateIndex, expected := range canonicalStates {
		if status.CanonicalStates[stateIndex] != expected {
			return fmt.Errorf("entries[%d].status.canonical_states must be the exact canonical trio", index)
		}
	}
	if len(status.RawStateMappings) < 3 {
		return fmt.Errorf("entries[%d].status.raw_state_mappings must contain at least three mappings", index)
	}
	seenRaw := make(map[string]struct{}, len(status.RawStateMappings))
	seenCanonical := make(map[string]struct{}, len(canonicalStates))
	for mappingIndex, mapping := range status.RawStateMappings {
		if strings.TrimSpace(mapping.Raw) == "" || strings.TrimSpace(mapping.Meaning) == "" {
			return fmt.Errorf("entries[%d].status.raw_state_mappings[%d] has an empty required string", index, mappingIndex)
		}
		if _, ok := seenRaw[mapping.Raw]; ok {
			return fmt.Errorf("entries[%d].status.raw_state_mappings contains duplicate raw state %q", index, mapping.Raw)
		}
		seenRaw[mapping.Raw] = struct{}{}
		if !contains(canonicalStates[:], mapping.Canonical) {
			return fmt.Errorf("entries[%d].status.raw_state_mappings[%d] has invalid canonical state", index, mappingIndex)
		}
		seenCanonical[mapping.Canonical] = struct{}{}
	}
	for _, state := range canonicalStates {
		if _, ok := seenCanonical[state]; !ok {
			return fmt.Errorf("entries[%d].status.raw_state_mappings does not cover %s", index, state)
		}
	}
	if err := validateStringList(status.APIRoutePrefixes, true, fmt.Sprintf("entries[%d].status.api_route_prefixes", index)); err != nil {
		return err
	}
	for _, prefix := range status.APIRoutePrefixes {
		if prefix != "/api" && !strings.HasPrefix(prefix, "/api/") {
			return fmt.Errorf("entries[%d].status.api_route_prefixes contains non-API prefix %q", index, prefix)
		}
	}
	return nil
}

func validateAgent(agent Agent, index int) error {
	if err := validateStringList(agent.Tasks, true, fmt.Sprintf("entries[%d].agent.tasks", index)); err != nil {
		return err
	}
	if err := validateStringList(agent.Capabilities, true, fmt.Sprintf("entries[%d].agent.capabilities", index)); err != nil {
		return err
	}
	if err := validateEvidenceItems(agent.Evidence.Tasks, fmt.Sprintf("entries[%d].agent.evidence.tasks", index)); err != nil {
		return err
	}
	if err := validateEvidenceItems(agent.Evidence.Capabilities, fmt.Sprintf("entries[%d].agent.evidence.capabilities", index)); err != nil {
		return err
	}
	return nil
}

func validateEvidence(evidence Evidence, path string) error {
	if err := validateEvidenceItems(evidence.Web, path+".web"); err != nil {
		return err
	}
	if err := validateEvidenceItems(evidence.Docs, path+".docs"); err != nil {
		return err
	}
	if err := validateEvidenceItems(evidence.Tests, path+".tests"); err != nil {
		return err
	}
	return nil
}

func validateEvidenceItems(items []EvidenceItem, path string) error {
	if len(items) == 0 {
		return fmt.Errorf("%s must contain at least one item", path)
	}
	for index, item := range items {
		if strings.TrimSpace(item.Path) == "" || strings.TrimSpace(item.Claim) == "" {
			return fmt.Errorf("%s[%d] has an empty path or claim", path, index)
		}
		if strings.HasPrefix(item.Path, "/") || strings.Contains(item.Path, "../") {
			return fmt.Errorf("%s[%d].path must stay within the repository", path, index)
		}
	}
	return nil
}

func validateStringList(values []string, requireNonEmpty bool, path string) error {
	if requireNonEmpty && len(values) == 0 {
		return fmt.Errorf("%s must contain at least one value", path)
	}
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if strings.TrimSpace(value) == "" || strings.ContainsAny(value, " \t\r\n") {
			return fmt.Errorf("%s[%d] must be a non-empty token", path, index)
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("%s contains duplicate value %q", path, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// catalogDocsRowMarker returns the stable documentation-row marker derived
// from an entry display name. Non-ASCII runs are separators, matching the
// catalog's provider-neutral slug convention; false means the display name
// contains no usable marker token.
func catalogDocsRowMarker(displayName string) (string, bool) {
	var normalized strings.Builder
	pendingSeparator := false
	for _, character := range strings.ToLower(displayName) {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') {
			if pendingSeparator {
				normalized.WriteByte('-')
			}
			normalized.WriteRune(character)
			pendingSeparator = false
		} else if normalized.Len() > 0 {
			pendingSeparator = true
		}
	}
	if normalized.Len() == 0 {
		return catalogMarkerPrefix, false
	}
	return catalogMarkerPrefix + normalized.String(), true
}

func overlap(left, right []string) bool {
	seen := make(map[string]struct{}, len(left))
	for _, value := range left {
		seen[value] = struct{}{}
	}
	for _, value := range right {
		if _, ok := seen[value]; ok {
			return true
		}
	}
	return false
}

func cloneCatalog(source Catalog) Catalog {
	clone := source
	clone.Entries = make([]Entry, len(source.Entries))
	for index, sourceEntry := range source.Entries {
		cloneEntry := sourceEntry
		cloneEntry.Classes = cloneStrings(sourceEntry.Classes)
		cloneEntry.Targets = cloneStrings(sourceEntry.Targets)
		cloneEntry.Configuration.NonSecretKeys = cloneStrings(sourceEntry.Configuration.NonSecretKeys)
		cloneEntry.Configuration.SecretKeyNames = cloneStrings(sourceEntry.Configuration.SecretKeyNames)
		cloneEntry.Configuration.SecretFileRefs = cloneStrings(sourceEntry.Configuration.SecretFileRefs)
		cloneEntry.Status.CanonicalStates = cloneStrings(sourceEntry.Status.CanonicalStates)
		cloneEntry.Status.APIRoutePrefixes = cloneStrings(sourceEntry.Status.APIRoutePrefixes)
		cloneEntry.Status.RawStateMappings = append([]RawStateMapping(nil), sourceEntry.Status.RawStateMappings...)
		cloneEntry.Evidence.Web = cloneEvidenceItems(sourceEntry.Evidence.Web)
		cloneEntry.Evidence.Docs = cloneEvidenceItems(sourceEntry.Evidence.Docs)
		cloneEntry.Evidence.Tests = cloneEvidenceItems(sourceEntry.Evidence.Tests)
		if sourceEntry.Agent != nil {
			agent := *sourceEntry.Agent
			agent.Tasks = cloneStrings(sourceEntry.Agent.Tasks)
			agent.Capabilities = cloneStrings(sourceEntry.Agent.Capabilities)
			agent.Evidence.Tasks = cloneEvidenceItems(sourceEntry.Agent.Evidence.Tasks)
			agent.Evidence.Capabilities = cloneEvidenceItems(sourceEntry.Agent.Evidence.Capabilities)
			cloneEntry.Agent = &agent
		}
		clone.Entries[index] = cloneEntry
	}
	return clone
}

func cloneStrings(source []string) []string {
	if source == nil {
		return nil
	}
	clone := make([]string, len(source))
	copy(clone, source)
	return clone
}

func cloneEvidenceItems(source []EvidenceItem) []EvidenceItem {
	if source == nil {
		return nil
	}
	clone := make([]EvidenceItem, len(source))
	copy(clone, source)
	return clone
}
