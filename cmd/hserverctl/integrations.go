package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/extensions"
	"github.com/IamYGT/heyserver/internal/managedintegrationstatus"
)

const integrationCatalogEndpoint = "/api/integrations/catalog"
const integrationStatusEndpoint = "/api/integrations/status"
const integrationStatusSynopsis = "integrations status [--format json|text] [--node NODE_ID]"
const integrationStatusUsage = "usage: hserverctl " + integrationStatusSynopsis

// The managed endpoint waits for a fresh agent observation. Keep enough room
// for the panel's bounded 45-second wait while retaining the caller context
// as the outer cancellation boundary.
const managedIntegrationStatusClientTimeout = 60 * time.Second

// integrationCoreIDs are the schema-v1 IDs whose live local probes are known
// to this CLI build. Catalog metadata and completion come from the embedded
// catalog so additive community entries do not require a second allowlist.
var integrationCoreIDs = []string{
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

var integrationCompletionIDs = strings.Join(embeddedIntegrationCatalogIDs(), " ")

func embeddedIntegrationCatalogIDs() []string {
	catalog, err := extensions.LoadCatalog()
	if err != nil {
		return nil
	}
	return integrationCompletionIDsFromCatalog(catalog)
}

func integrationCompletionIDsFromCatalog(catalog extensions.Catalog) []string {
	ids := make([]string, 0, len(catalog.Entries))
	for _, entry := range catalog.Entries {
		ids = append(ids, entry.ID)
	}
	return ids
}

type cliIntegrationCatalog struct {
	Schema        string                       `json:"$schema,omitempty"`
	SchemaVersion int                          `json:"schema_version"`
	Documentation cliIntegrationDocumentation  `json:"documentation"`
	Entries       []cliIntegrationCatalogEntry `json:"entries"`
}

type cliIntegrationDocumentation struct {
	TablePath        string `json:"table_path"`
	TableHeader      string `json:"table_header"`
	MarkerPrefix     string `json:"marker_prefix"`
	MarkerConvention string `json:"marker_convention"`
}

type cliIntegrationCatalogEntry struct {
	ID            string                      `json:"id"`
	DisplayName   string                      `json:"display_name"`
	Purpose       string                      `json:"purpose"`
	Requirement   string                      `json:"requirement"`
	DocsRowMarker string                      `json:"docs_row_marker"`
	Classes       []string                    `json:"classes"`
	Targets       []string                    `json:"targets"`
	Configuration cliIntegrationConfiguration `json:"configuration"`
	Status        cliIntegrationStatus        `json:"status"`
	Agent         *cliIntegrationAgent        `json:"agent,omitempty"`
	Evidence      cliIntegrationEvidence      `json:"evidence"`
}

type cliIntegrationConfiguration struct {
	NonSecretKeys  []string `json:"non_secret_keys"`
	SecretKeyNames []string `json:"secret_key_names"`
	SecretFileRefs []string `json:"secret_file_refs"`
	Boundary       string   `json:"boundary"`
}

type cliIntegrationStatus struct {
	CanonicalStates  []string                        `json:"canonical_states"`
	RawStateMappings []cliIntegrationRawStateMapping `json:"raw_state_mappings"`
	APIRoutePrefixes []string                        `json:"api_route_prefixes"`
}

type cliIntegrationRawStateMapping struct {
	Raw       string `json:"raw"`
	Canonical string `json:"canonical"`
	Meaning   string `json:"meaning"`
}

type cliIntegrationAgent struct {
	Tasks        []string                    `json:"tasks"`
	Capabilities []string                    `json:"capabilities"`
	Evidence     cliIntegrationAgentEvidence `json:"evidence"`
}

type cliIntegrationAgentEvidence struct {
	Tasks        []cliIntegrationEvidenceItem `json:"tasks"`
	Capabilities []cliIntegrationEvidenceItem `json:"capabilities"`
}

type cliIntegrationEvidence struct {
	Web   []cliIntegrationEvidenceItem `json:"web"`
	Docs  []cliIntegrationEvidenceItem `json:"docs"`
	Tests []cliIntegrationEvidenceItem `json:"tests"`
}

type cliIntegrationEvidenceItem struct {
	Path  string `json:"path"`
	Claim string `json:"claim"`
}

type cliIntegrationStatusTarget struct {
	Scope  string `json:"scope"`
	NodeID string `json:"node_id,omitempty"`
}

// cliIntegrationStatusReport is intentionally narrower than the server
// response envelope.  The status endpoint may carry diagnostic fields for
// server-side logging, but the CLI only emits the non-secret, stable status
// contract below. In particular, raw provider errors and command output are
// not retained or rendered.
type cliIntegrationStatusReport struct {
	SchemaVersion int                          `json:"schema_version"`
	ObservedAt    string                       `json:"observed_at"`
	Target        cliIntegrationStatusTarget   `json:"target"`
	Results       []cliIntegrationStatusResult `json:"results"`
	Unprobed      []string                     `json:"unprobed"`
	Partial       bool                         `json:"partial"`
}

type cliIntegrationStatusResult struct {
	ID         string `json:"id"`
	State      string `json:"state"`
	Probe      string `json:"probe"`
	ErrorCode  string `json:"error_code,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`
}

func runIntegrations(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl integrations list [--format json|text] | integrations show [--format json|text] ID | " + integrationStatusSynopsis)
	}
	switch args[0] {
	case "list":
		return runIntegrationsList(ctx, client, args[1:], out)
	case "show":
		return runIntegrationsShow(ctx, client, args[1:], out)
	case "status":
		return runIntegrationsStatus(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown integrations command %q", args[0])
	}
}

func runIntegrationsList(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	outputFormat, err := parseIntegrationOutputFormat("integrations list", args)
	if err != nil {
		return err
	}
	catalog, err := loadIntegrationCatalog(ctx, client)
	if err != nil {
		return err
	}
	if outputFormat == "json" {
		return writeIntegrationJSON(out, catalog)
	}
	return writeIntegrationListText(out, catalog)
}

func runIntegrationsShow(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("integrations show", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	outputFormat := flags.String("format", "json", "output format: json or text")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl integrations show [--format json|text] ID")
	}
	*outputFormat = strings.ToLower(strings.TrimSpace(*outputFormat))
	if *outputFormat != "json" && *outputFormat != "text" {
		return errors.New("integrations format must be json or text")
	}
	id := strings.TrimSpace(flags.Arg(0))
	if !isWellFormedIntegrationID(id) {
		return fmt.Errorf("integration ID %q is malformed", id)
	}
	catalog, err := loadIntegrationCatalog(ctx, client)
	if err != nil {
		return err
	}
	entry, ok := findIntegrationCatalogEntry(catalog, id)
	if !ok {
		return fmt.Errorf("integration ID %q is not present in the server catalog", id)
	}
	if *outputFormat == "json" {
		return writeIntegrationJSON(out, entry)
	}
	return writeIntegrationShowText(out, entry)
}

func runIntegrationsStatus(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	outputFormat, nodeID, err := parseIntegrationStatusArgs(args)
	if err != nil {
		return err
	}
	if nodeID == "" {
		status, err := loadIntegrationStatus(ctx, client)
		if err != nil {
			return err
		}
		if outputFormat == "json" {
			return writeIntegrationJSON(out, status)
		}
		return writeIntegrationStatusText(out, status)
	}

	status, err := loadManagedIntegrationStatus(ctx, client, nodeID)
	if err != nil {
		return err
	}
	if outputFormat == "json" {
		// Emit the shared managed schema directly. Unlike the local report, a
		// managed result has no unprobed catalog field and must retain its
		// explicit target.node_id.
		return writeIntegrationJSON(out, status)
	}
	return writeManagedIntegrationStatusText(out, status)
}

func parseIntegrationStatusArgs(args []string) (format, nodeID string, err error) {
	flags := flag.NewFlagSet("integrations status", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	outputFormat := flags.String("format", "json", "output format: json or text")
	node := flags.String("node", "", "managed node ID; omit for the local host")
	if err := flags.Parse(args); err != nil {
		return "", "", err
	}
	if len(flags.Args()) != 0 {
		return "", "", errors.New("integrations status does not accept positional arguments")
	}
	format = strings.ToLower(strings.TrimSpace(*outputFormat))
	if format != "json" && format != "text" {
		return "", "", errors.New("integrations format must be json or text")
	}
	nodeID = strings.TrimSpace(*node)
	if flags.Lookup("node") != nil && flagWasSet(flags, "node") && nodeID == "" {
		return "", "", errors.New("integrations status --node must not be empty")
	}
	return format, nodeID, nil
}

func flagWasSet(flags *flag.FlagSet, name string) bool {
	wasSet := false
	flags.Visit(func(item *flag.Flag) {
		if item.Name == name {
			wasSet = true
		}
	})
	return wasSet
}

func parseIntegrationOutputFormat(command string, args []string) (string, error) {
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	outputFormat := flags.String("format", "json", "output format: json or text")
	if err := flags.Parse(args); err != nil {
		return "", err
	}
	if len(flags.Args()) != 0 {
		return "", fmt.Errorf("%s does not accept positional arguments", command)
	}
	format := strings.ToLower(strings.TrimSpace(*outputFormat))
	if format != "json" && format != "text" {
		return "", errors.New("integrations format must be json or text")
	}
	return format, nil
}

func loadIntegrationCatalog(ctx context.Context, client *apiClient) (cliIntegrationCatalog, error) {
	catalog, err := requestJSON[cliIntegrationCatalog](ctx, client, http.MethodGet, integrationCatalogEndpoint, nil, true)
	if err != nil {
		return catalog, err
	}
	if catalog.SchemaVersion != 1 {
		return catalog, fmt.Errorf("decode %s: unsupported catalog schema version %d", integrationCatalogEndpoint, catalog.SchemaVersion)
	}
	seen := make(map[string]struct{}, len(catalog.Entries))
	for index, entry := range catalog.Entries {
		if !isWellFormedIntegrationID(entry.ID) {
			return catalog, fmt.Errorf("decode %s: entries[%d].id is malformed", integrationCatalogEndpoint, index)
		}
		if _, exists := seen[entry.ID]; exists {
			return catalog, fmt.Errorf("decode %s: duplicate integration ID %q", integrationCatalogEndpoint, entry.ID)
		}
		seen[entry.ID] = struct{}{}
	}
	for _, id := range integrationCoreIDs {
		if _, exists := seen[id]; !exists {
			return catalog, fmt.Errorf("decode %s: required integration ID %q is missing", integrationCatalogEndpoint, id)
		}
	}
	return catalog, nil
}

func loadIntegrationStatus(ctx context.Context, client *apiClient) (cliIntegrationStatusReport, error) {
	// The catalog is the authoritative ID boundary for additive in-tree
	// probes. Keep the core-only decoder as a strict compatibility helper for
	// unit fixtures, but production status must validate against this
	// authenticated server catalog before accepting any result or unprobed ID.
	catalog, err := loadIntegrationCatalog(ctx, client)
	if err != nil {
		return cliIntegrationStatusReport{}, err
	}
	raw, err := client.request(ctx, http.MethodGet, integrationStatusEndpoint, nil, true)
	if err != nil {
		return cliIntegrationStatusReport{}, err
	}
	status, err := decodeIntegrationStatusWithCatalog(raw, catalog)
	if err != nil {
		return cliIntegrationStatusReport{}, err
	}
	return status, nil
}

func loadManagedIntegrationStatus(ctx context.Context, client *apiClient, nodeID string) (managedintegrationstatus.ManagedIntegrationStatusResponse, error) {
	endpoint := managedIntegrationStatusEndpoint(nodeID)
	raw, err := client.withTimeout(managedIntegrationStatusClientTimeout).request(ctx, http.MethodGet, endpoint, nil, true)
	if err != nil {
		return managedintegrationstatus.ManagedIntegrationStatusResponse{}, err
	}
	if err := validateManagedIntegrationStatusEnvelope(raw, endpoint); err != nil {
		return managedintegrationstatus.ManagedIntegrationStatusResponse{}, err
	}
	status, err := managedintegrationstatus.Decode(raw)
	if err != nil {
		return managedintegrationstatus.ManagedIntegrationStatusResponse{}, fmt.Errorf("decode %s: %w", endpoint, err)
	}
	if status.Target.NodeID != nodeID {
		return managedintegrationstatus.ManagedIntegrationStatusResponse{}, fmt.Errorf(
			"decode %s: target.node_id %q does not match requested node %q", endpoint, status.Target.NodeID, nodeID,
		)
	}
	return status, nil
}

func validateManagedIntegrationStatusEnvelope(raw []byte, endpoint string) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("decode %s: %w", endpoint, err)
	}
	if fields == nil {
		return fmt.Errorf("decode %s: status response must be an object", endpoint)
	}
	for _, field := range []string{"schema_version", "observed_at", "target", "results", "partial"} {
		if _, ok := fields[field]; !ok {
			return fmt.Errorf("decode %s: missing %s", endpoint, field)
		}
	}
	if rawJSONIsNull(fields["partial"]) {
		return fmt.Errorf("decode %s: partial must be a boolean", endpoint)
	}
	var partial bool
	if err := json.Unmarshal(fields["partial"], &partial); err != nil {
		return fmt.Errorf("decode %s: partial must be a boolean", endpoint)
	}
	return nil
}

func managedIntegrationStatusEndpoint(nodeID string) string {
	return "/api/nodes/" + url.PathEscape(nodeID) + "/integrations/status"
}

func decodeIntegrationStatus(raw []byte) (cliIntegrationStatusReport, error) {
	return decodeIntegrationStatusWithAllowedIDs(raw, integrationCoreIDSet())
}

func decodeIntegrationStatusWithCatalog(raw []byte, catalog cliIntegrationCatalog) (cliIntegrationStatusReport, error) {
	return decodeIntegrationStatusWithAllowedIDs(raw, integrationCatalogIDSet(catalog))
}

func decodeIntegrationStatusWithAllowedIDs(raw []byte, allowedIDs map[string]struct{}) (cliIntegrationStatusReport, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return cliIntegrationStatusReport{}, fmt.Errorf("decode %s: %w", integrationStatusEndpoint, err)
	}
	if fields == nil {
		return cliIntegrationStatusReport{}, fmt.Errorf("decode %s: status response must be an object", integrationStatusEndpoint)
	}
	for _, field := range []string{"schema_version", "observed_at", "target", "results", "unprobed", "partial"} {
		if _, ok := fields[field]; !ok {
			return cliIntegrationStatusReport{}, fmt.Errorf("decode %s: missing %s", integrationStatusEndpoint, field)
		}
	}

	var status cliIntegrationStatusReport
	if err := json.Unmarshal(raw, &status); err != nil {
		return cliIntegrationStatusReport{}, fmt.Errorf("decode %s: %w", integrationStatusEndpoint, err)
	}
	// The local endpoint is scoped to local_host and has no node identity.
	// Keep the shared DTO able to inspect managed JSON in tests/callers while
	// deliberately omitting any additive node_id field from local output.
	status.Target.NodeID = ""
	if status.SchemaVersion != 1 {
		return cliIntegrationStatusReport{}, fmt.Errorf("decode %s: unsupported schema version %d", integrationStatusEndpoint, status.SchemaVersion)
	}
	if rawJSONIsNull(fields["observed_at"]) || strings.TrimSpace(status.ObservedAt) == "" {
		return cliIntegrationStatusReport{}, fmt.Errorf("decode %s: observed_at is required", integrationStatusEndpoint)
	}
	if _, err := time.Parse(time.RFC3339, status.ObservedAt); err != nil {
		return cliIntegrationStatusReport{}, fmt.Errorf("decode %s: observed_at must be an RFC3339 timestamp", integrationStatusEndpoint)
	}
	if rawJSONIsNull(fields["target"]) || status.Target.Scope != "local_host" {
		return cliIntegrationStatusReport{}, fmt.Errorf("decode %s: target.scope must be local_host", integrationStatusEndpoint)
	}
	if rawJSONIsNull(fields["results"]) || status.Results == nil {
		return cliIntegrationStatusReport{}, fmt.Errorf("decode %s: results must be an array", integrationStatusEndpoint)
	}
	if rawJSONIsNull(fields["unprobed"]) || status.Unprobed == nil {
		return cliIntegrationStatusReport{}, fmt.Errorf("decode %s: unprobed must be an array", integrationStatusEndpoint)
	}
	if rawJSONIsNull(fields["partial"]) {
		return cliIntegrationStatusReport{}, fmt.Errorf("decode %s: partial must be a boolean", integrationStatusEndpoint)
	}

	seen := make(map[string]string, len(status.Results)+len(status.Unprobed))
	for index, result := range status.Results {
		if err := validateIntegrationStatusID(result.ID, fmt.Sprintf("results[%d].id", index), seen, allowedIDs); err != nil {
			return cliIntegrationStatusReport{}, err
		}
		seen[result.ID] = "result"
		if !isCanonicalIntegrationState(result.State) {
			return cliIntegrationStatusReport{}, fmt.Errorf("decode %s: results[%d].state is not a canonical integration state", integrationStatusEndpoint, index)
		}
		if !isSafeIntegrationCode(result.Probe) {
			return cliIntegrationStatusReport{}, fmt.Errorf("decode %s: results[%d].probe is malformed", integrationStatusEndpoint, index)
		}
		if result.ErrorCode != "" && !isSafeIntegrationCode(result.ErrorCode) {
			return cliIntegrationStatusReport{}, fmt.Errorf("decode %s: results[%d].error_code is malformed", integrationStatusEndpoint, index)
		}
		if result.DurationMS < 0 {
			return cliIntegrationStatusReport{}, fmt.Errorf("decode %s: results[%d].duration_ms must not be negative", integrationStatusEndpoint, index)
		}
	}
	for index, id := range status.Unprobed {
		field := fmt.Sprintf("unprobed[%d]", index)
		if err := validateIntegrationStatusID(id, field, seen, allowedIDs); err != nil {
			return cliIntegrationStatusReport{}, err
		}
		seen[id] = "unprobed"
	}
	return status, nil
}

func rawJSONIsNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

func validateIntegrationStatusID(id, field string, seen map[string]string, allowedIDs map[string]struct{}) error {
	if !isWellFormedIntegrationID(id) {
		return fmt.Errorf("decode %s: %s is malformed", integrationStatusEndpoint, field)
	}
	if _, allowed := allowedIDs[id]; !allowed {
		return fmt.Errorf("decode %s: %s is unknown", integrationStatusEndpoint, field)
	}
	if previous, ok := seen[id]; ok {
		return fmt.Errorf("decode %s: duplicate integration ID in %s and %s", integrationStatusEndpoint, previous, field)
	}
	return nil
}

func isWellFormedIntegrationID(id string) bool {
	if id == "" || id != strings.TrimSpace(id) {
		return false
	}
	for index, char := range id {
		alphaNumeric := (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9')
		if alphaNumeric || char == '.' || char == '_' || char == '-' {
			if index == 0 && !alphaNumeric {
				return false
			}
			continue
		}
		return false
	}
	last := id[len(id)-1]
	return (last >= 'a' && last <= 'z') || (last >= 'A' && last <= 'Z') || (last >= '0' && last <= '9')
}

func isCanonicalIntegrationState(state string) bool {
	switch state {
	case "not_configured", "unavailable", "healthy":
		return true
	default:
		return false
	}
}

// Probe and error_code are machine identifiers, not free-form provider
// messages. Restricting them to a bounded ASCII code also prevents accidental
// emission of provider errors or credentials through either output format.
func isSafeIntegrationCode(value string) bool {
	if value == "" || len(value) > 128 || value != strings.TrimSpace(value) {
		return false
	}
	for index, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' || char == '-' || char == '.' {
			if index == 0 && (char == '-' || char == '.' || char == '_') {
				return false
			}
			continue
		}
		return false
	}
	return true
}

func findIntegrationCatalogEntry(catalog cliIntegrationCatalog, id string) (cliIntegrationCatalogEntry, bool) {
	for _, entry := range catalog.Entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return cliIntegrationCatalogEntry{}, false
}

func integrationCoreIDSet() map[string]struct{} {
	return integrationIDSet(integrationCoreIDs)
}

func integrationCatalogIDSet(catalog cliIntegrationCatalog) map[string]struct{} {
	ids := make([]string, 0, len(catalog.Entries))
	for _, entry := range catalog.Entries {
		ids = append(ids, entry.ID)
	}
	return integrationIDSet(ids)
}

func integrationIDSet(ids []string) map[string]struct{} {
	known := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		known[id] = struct{}{}
	}
	return known
}

func writeIntegrationJSON(out io.Writer, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode integration catalog: %w", err)
	}
	return prettyJSON(out, raw)
}

func writeIntegrationListText(out io.Writer, catalog cliIntegrationCatalog) error {
	fmt.Fprintf(out, "HServer integrations catalog (schema v%d)\n", catalog.SchemaVersion)
	fmt.Fprintln(out, "Catalog metadata only; it does not probe or report live integration health.")
	fmt.Fprintln(out, "ID\tNAME\tREQUIREMENT\tTARGETS")
	for _, entry := range catalog.Entries {
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\n",
			integrationTextValue(entry.ID),
			integrationTextValue(entry.DisplayName),
			integrationTextValue(entry.Requirement),
			integrationTextList(entry.Targets),
		)
	}
	return nil
}

func writeIntegrationShowText(out io.Writer, entry cliIntegrationCatalogEntry) error {
	fmt.Fprintf(out, "Integration: %s (%s)\n", integrationTextValue(entry.DisplayName), integrationTextValue(entry.ID))
	fmt.Fprintf(out, "Purpose: %s\n", integrationTextValue(entry.Purpose))
	fmt.Fprintf(out, "Requirement: %s\n", integrationTextValue(entry.Requirement))
	fmt.Fprintf(out, "Documentation row: %s\n", integrationTextValue(entry.DocsRowMarker))
	fmt.Fprintf(out, "Classes: %s\n", integrationTextList(entry.Classes))
	fmt.Fprintf(out, "Targets: %s\n", integrationTextList(entry.Targets))

	fmt.Fprintln(out, "Configuration key names (secret values are never included):")
	fmt.Fprintf(out, "  Non-secret keys: %s\n", integrationTextList(entry.Configuration.NonSecretKeys))
	fmt.Fprintf(out, "  Secret key names: %s\n", integrationTextList(entry.Configuration.SecretKeyNames))
	fmt.Fprintf(out, "  Secret file references: %s\n", integrationTextList(entry.Configuration.SecretFileRefs))
	fmt.Fprintf(out, "  Boundary: %s\n", integrationTextValue(entry.Configuration.Boundary))
	if entry.Agent != nil {
		fmt.Fprintf(out, "Agent tasks: %s\n", integrationTextList(entry.Agent.Tasks))
		fmt.Fprintf(out, "Agent capabilities: %s\n", integrationTextList(entry.Agent.Capabilities))
	}

	fmt.Fprintf(out, "Canonical states: %s\n", integrationTextList(entry.Status.CanonicalStates))
	fmt.Fprintln(out, "Status mappings:")
	for _, mapping := range entry.Status.RawStateMappings {
		fmt.Fprintf(out, "  %s -> %s: %s\n",
			integrationTextValue(mapping.Raw),
			integrationTextValue(mapping.Canonical),
			integrationTextValue(mapping.Meaning),
		)
	}
	fmt.Fprintf(out, "API route prefixes: %s\n", integrationTextList(entry.Status.APIRoutePrefixes))
	fmt.Fprintln(out, "Live health: not reported; this catalog is metadata and does not perform a live-health probe.")
	return nil
}

func writeIntegrationStatusText(out io.Writer, status cliIntegrationStatusReport) error {
	fmt.Fprintf(out, "HServer integrations status (schema v%d)\n", status.SchemaVersion)
	fmt.Fprintf(out, "Observed at (observed_at): %s\n", integrationTextValue(status.ObservedAt))
	fmt.Fprintf(out, "Target scope: %s\n", integrationTextValue(status.Target.Scope))
	fmt.Fprintf(out, "Partial: %t\n", status.Partial)
	fmt.Fprintln(out, "Results:")
	if len(status.Results) == 0 {
		fmt.Fprintln(out, "  none")
	} else {
		fmt.Fprintln(out, "  ID\tSTATE\tPROBE\tERROR_CODE\tDURATION_MS")
		for _, result := range status.Results {
			fmt.Fprintf(out, "  %s\t%s\t%s\t%s\t%d\n",
				integrationTextValue(result.ID),
				integrationTextValue(result.State),
				integrationTextValue(result.Probe),
				integrationTextValue(result.ErrorCode),
				result.DurationMS,
			)
		}
	}
	fmt.Fprintf(out, "Unprobed: %s\n", integrationTextList(status.Unprobed))
	return nil
}

func writeManagedIntegrationStatusText(out io.Writer, status managedintegrationstatus.ManagedIntegrationStatusResponse) error {
	fmt.Fprintf(out, "HServer integrations status (schema v%d)\n", status.SchemaVersion)
	fmt.Fprintf(out, "Observed at (observed_at): %s\n", integrationTextValue(status.ObservedAt.Format(time.RFC3339Nano)))
	fmt.Fprintf(out, "Target scope: %s\n", integrationTextValue(status.Target.Scope))
	fmt.Fprintf(out, "Target node: %s\n", integrationTextValue(status.Target.NodeID))
	fmt.Fprintf(out, "Partial: %t\n", status.Partial)
	fmt.Fprintln(out, "Results:")
	if len(status.Results) == 0 {
		fmt.Fprintln(out, "  none")
	} else {
		fmt.Fprintln(out, "  ID\tSTATE\tPROBE\tERROR_CODE\tDURATION_MS")
		for _, result := range status.Results {
			fmt.Fprintf(out, "  %s\t%s\t%s\t%s\t%d\n",
				integrationTextValue(result.ID),
				integrationTextValue(string(result.State)),
				integrationTextValue(result.Probe),
				integrationTextValue(result.ErrorCode),
				result.DurationMS,
			)
		}
	}
	return nil
}

func integrationTextList(values []string) string {
	if len(values) == 0 {
		return "none"
	}
	safe := make([]string, 0, len(values))
	for _, value := range values {
		safe = append(safe, integrationTextValue(value))
	}
	return strings.Join(safe, ", ")
}

func integrationTextValue(value string) string {
	value = strings.Join(strings.Fields(sanitizeTUIText(value)), " ")
	if value == "" {
		return "N/A"
	}
	return value
}
