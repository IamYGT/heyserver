// Package integrationstatus collects the small, local integration status
// surface exposed by the panel.  The catalog remains the source of truth for
// the complete integration list; this package supplies the fifteen core probes
// and any additional code-owned probes admitted by the catalog.
package integrationstatus

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/extensions"
	"github.com/IamYGT/heyserver/internal/integrationstate"
)

const (
	// SchemaVersion is the wire version of the local aggregate response.
	SchemaVersion = 1

	// ScopeLocalHost identifies the only target supported by this aggregate.
	ScopeLocalHost = "local_host"

	// The v1 status slice has exactly fifteen live, read-only probes.  Their
	// names describe the operation without exposing command output or provider
	// details in the response.
	ProcessPM2ID                = "process.pm2"
	CloudflareDNSID             = "cloudflare.dns"
	DockerContainerID           = "container.docker"
	DockerID                    = DockerContainerID
	NginxID                     = "web.nginx"
	FirewallUFWID               = "firewall.ufw"
	CertbotTLSID                = "tls.certbot"
	Bind9DNSID                  = "dns.bind9"
	PHPFPMRuntimeID             = "runtime.php_fpm"
	DatabaseLocalID             = "database.local"
	SmartmontoolsID             = "storage.smartmontools"
	StalwartMailID              = "stalwart.mail"
	MailAccessID                = "mail.access"
	GDriveBackupID              = "backup.gdrive"
	ResticSnapshotID            = "backup.snapshot.restic"
	NotificationDeliveryID      = "notification.delivery"
	PM2InventoryProbe           = "pm2_inventory"
	CloudflareZoneProbe         = "cloudflare_zone_list"
	DockerInfoProbe             = "docker_info"
	NginxReadinessProbe         = "nginx_readiness"
	UFWReadinessProbe           = "ufw_readiness"
	CertbotReadinessProbe       = "certbot_readiness"
	Bind9ReadinessProbe         = "bind9_readiness"
	PHPFPMReadinessProbe        = "php_fpm_readiness"
	DatabaseReadinessProbe      = "database_readiness"
	SmartmontoolsReadinessProbe = "smartmontools_readiness"
	StalwartReadinessProbe      = "stalwart_readiness"
	MailAccessReadinessProbe    = "mail_access_readiness"
	GDriveReadinessProbe        = "gdrive_readiness"
	ResticReadinessProbe        = "restic_readiness"
	NotificationReadinessProbe  = "notification_readiness"

	// MaxConcurrency lets every core v1 probe start immediately while still
	// bounding fan-out. Keeping the earlier limit of four would require four
	// scheduling waves and make later probes time out behind healthy but slow
	// predecessors before their own five-second budget even started.
	MaxConcurrency = 15

	// Each live probe gets its own deadline.  AggregateTimeout is deliberately
	// longer so a slow probe can be represented as an item result rather than
	// turning the entire authenticated endpoint into a gateway error.
	ProbeTimeout     = 5 * time.Second
	AggregateTimeout = 8 * time.Second
)

var (
	// ErrCatalogUnavailable identifies an aggregate that could not obtain its
	// embedded catalog.  The HTTP handler turns it into a generic 500 response;
	// catalog or provider details never cross the wire.
	ErrCatalogUnavailable = errors.New("integration catalog unavailable")
	ErrInvalidCatalog     = errors.New("invalid integration catalog")
)

// ProbeFunc is one fresh, read-only integration observation.  A successful
// function must return integrationstate.Healthy.  Not-configured and
// unavailable observations may return an error so their cause stays inside
// the service boundary; the aggregate emits only a safe error code.
type ProbeFunc func(context.Context) (integrationstate.State, error)

// Probe describes one registry entry. ID is matched against the embedded
// catalog. Core IDs use their fixed wire names; additive IDs use a stable,
// ID-derived token, so caller-supplied strings (including paths, output, or
// secrets) can never enter a response.
type Probe struct {
	ID  string
	Run ProbeFunc
}

// CatalogLoader is injectable for focused service tests.  Production uses
// extensions.LoadCatalog, which parses the embedded catalog and returns a
// defensive copy.
type CatalogLoader func() (extensions.Catalog, error)

// Service owns the local probe registry. It deliberately carries no last-known
// values or cache: every Status call starts fresh observations.
type Service struct {
	loadCatalog CatalogLoader
	probes      map[string]Probe
}

// New builds the v1 registry from the fifteen implemented local probe
// functions. Passing nil is allowed for a provider that is unavailable; that
// item is represented as unavailable rather than causing the whole response
// to fail. The variadic optional arguments keep callers that only supply the
// original two probes source-compatible: optional functions are Docker,
// Nginx, UFW, Certbot, BIND9, PHP-FPM, database, SMART, Stalwart, mail
// access, Google Drive, Restic, and notification delivery probes in that order.
func New(pm2Probe, cloudflareProbe ProbeFunc, optional ...ProbeFunc) *Service {
	var runDocker, runNginx, runUFW, runCertbot, runBind9, runPHPFPM, runDatabase, runSmart, runStalwart, runMailAccess, runGDrive, runRestic, runNotification ProbeFunc
	if len(optional) > 0 {
		runDocker = optional[0]
	}
	if len(optional) > 1 {
		runNginx = optional[1]
	}
	if len(optional) > 2 {
		runUFW = optional[2]
	}
	if len(optional) > 3 {
		runCertbot = optional[3]
	}
	if len(optional) > 4 {
		runBind9 = optional[4]
	}
	if len(optional) > 5 {
		runPHPFPM = optional[5]
	}
	if len(optional) > 6 {
		runDatabase = optional[6]
	}
	if len(optional) > 7 {
		runSmart = optional[7]
	}
	if len(optional) > 8 {
		runStalwart = optional[8]
	}
	if len(optional) > 9 {
		runMailAccess = optional[9]
	}
	if len(optional) > 10 {
		runGDrive = optional[10]
	}
	if len(optional) > 11 {
		runRestic = optional[11]
	}
	if len(optional) > 12 {
		runNotification = optional[12]
	}
	return NewWithCatalog(extensions.LoadCatalog,
		Probe{ID: ProcessPM2ID, Run: pm2Probe},
		Probe{ID: CloudflareDNSID, Run: cloudflareProbe},
		Probe{ID: DockerID, Run: runDocker},
		Probe{ID: NginxID, Run: runNginx},
		Probe{ID: FirewallUFWID, Run: runUFW},
		Probe{ID: CertbotTLSID, Run: runCertbot},
		Probe{ID: Bind9DNSID, Run: runBind9},
		Probe{ID: PHPFPMRuntimeID, Run: runPHPFPM},
		Probe{ID: DatabaseLocalID, Run: runDatabase},
		Probe{ID: SmartmontoolsID, Run: runSmart},
		Probe{ID: StalwartMailID, Run: runStalwart},
		Probe{ID: MailAccessID, Run: runMailAccess},
		Probe{ID: GDriveBackupID, Run: runGDrive},
		Probe{ID: ResticSnapshotID, Run: runRestic},
		Probe{ID: NotificationDeliveryID, Run: runNotification},
	)
}

// NewWithCatalog builds a service with an injectable catalog source. Probe
// definitions are code-owned and retained for both the fifteen core IDs and
// valid additive IDs. A probe is collected only when its ID exists in the
// loaded catalog; catalog entries without a registered probe remain unprobed.
func NewWithCatalog(loader CatalogLoader, probes ...Probe) *Service {
	if loader == nil {
		loader = extensions.LoadCatalog
	}
	registry := make(map[string]Probe, len(probes))
	for _, probe := range probes {
		if !validIntegrationID(probe.ID) {
			continue
		}
		// Keep the first definition deterministic if a caller accidentally
		// repeats an ID.  Production supplies each definition once.
		if _, exists := registry[probe.ID]; !exists {
			registry[probe.ID] = probe
		}
	}
	return &Service{loadCatalog: loader, probes: registry}
}

// Target identifies the scope of an aggregate observation.
type Target struct {
	Scope string `json:"scope"`
}

// Result is one canonical integration observation.  ErrorCode is intentionally
// a small safe vocabulary and DurationMS is a wall-clock measurement only.
type Result struct {
	ID         string                 `json:"id"`
	State      integrationstate.State `json:"state"`
	Probe      string                 `json:"probe"`
	ErrorCode  string                 `json:"error_code,omitempty"`
	DurationMS int64                  `json:"duration_ms,omitempty"`
}

// Response is the schema-v1 local aggregate wire object.
type Response struct {
	SchemaVersion int       `json:"schema_version"`
	ObservedAt    time.Time `json:"observed_at"`
	Target        Target    `json:"target"`
	Results       []Result  `json:"results"`
	Unprobed      []string  `json:"unprobed"`
	Partial       bool      `json:"partial"`
}

var probeNames = map[string]string{
	ProcessPM2ID:           PM2InventoryProbe,
	CloudflareDNSID:        CloudflareZoneProbe,
	DockerID:               DockerInfoProbe,
	NginxID:                NginxReadinessProbe,
	FirewallUFWID:          UFWReadinessProbe,
	CertbotTLSID:           CertbotReadinessProbe,
	Bind9DNSID:             Bind9ReadinessProbe,
	PHPFPMRuntimeID:        PHPFPMReadinessProbe,
	DatabaseLocalID:        DatabaseReadinessProbe,
	SmartmontoolsID:        SmartmontoolsReadinessProbe,
	StalwartMailID:         StalwartReadinessProbe,
	MailAccessID:           MailAccessReadinessProbe,
	GDriveBackupID:         GDriveReadinessProbe,
	ResticSnapshotID:       ResticReadinessProbe,
	NotificationDeliveryID: NotificationReadinessProbe,
}

// integrationIDPattern mirrors the catalog's provider-neutral ID contract.
// Catalogs loaded from the embedded asset are already validated by the
// extensions package, but Status also accepts injectable catalog loaders for
// focused tests and must keep that boundary fail-closed.
var integrationIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

// probeNamePattern limits additive wire names to stable, non-sensitive tokens.
// The name is derived only from the catalog ID; display names, purposes, and
// configuration values never enter the status response.
var probeNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func validIntegrationID(id string) bool {
	return integrationIDPattern.MatchString(id)
}

func probeNameForID(id string) (string, bool) {
	if name, ok := probeNames[id]; ok {
		return name, true
	}
	if !validIntegrationID(id) {
		return "", false
	}
	name := strings.NewReplacer(".", "_", "-", "_").Replace(id)
	if name[0] >= '0' && name[0] <= '9' {
		name = "integration_" + name
	}
	if !probeNamePattern.MatchString(name) {
		return "", false
	}
	return name, true
}

var supportedProbeIDs = []string{
	ProcessPM2ID,
	CloudflareDNSID,
	DockerID,
	NginxID,
	FirewallUFWID,
	CertbotTLSID,
	Bind9DNSID,
	PHPFPMRuntimeID,
	DatabaseLocalID,
	SmartmontoolsID,
	StalwartMailID,
	MailAccessID,
	GDriveBackupID,
	ResticSnapshotID,
	NotificationDeliveryID,
}

// Status loads the embedded catalog, fans out the registered probes, and
// returns one fresh aggregate.  Per-item failures and timeouts are encoded in
// the result object; only a catalog/registry failure returns an error.
func (s *Service) Status(ctx context.Context) (Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if s == nil || s.loadCatalog == nil {
		return Response{}, ErrCatalogUnavailable
	}

	catalog, err := s.loadCatalog()
	if err != nil {
		// Keep provider/catalog implementation details inside the service
		// boundary.  The handler intentionally has only one safe 500 message.
		return Response{}, ErrCatalogUnavailable
	}
	catalogIDs, err := catalogIDSet(catalog)
	if err != nil {
		return Response{}, err
	}

	response := Response{
		SchemaVersion: SchemaVersion,
		ObservedAt:    time.Now().UTC(),
		Target:        Target{Scope: ScopeLocalHost},
		Results:       make([]Result, 0, len(s.probes)),
		Unprobed:      make([]string, 0),
	}

	// Preserve the established core registry order for live results. Additive
	// probes follow catalog order, which keeps their output deterministic while
	// avoiding a map iteration order dependency.
	var probeIDs []string
	for _, id := range supportedProbeIDs {
		if _, inCatalog := catalogIDs[id]; !inCatalog {
			continue
		}
		if _, registered := s.probes[id]; !registered {
			continue
		}
		probeIDs = append(probeIDs, id)
	}
	for _, entry := range catalog.Entries {
		if _, isCore := probeNames[entry.ID]; isCore {
			continue
		}
		if _, registered := s.probes[entry.ID]; registered {
			probeIDs = append(probeIDs, entry.ID)
		}
	}

	results := collect(ctx, s.probes, probeIDs)
	for _, id := range probeIDs {
		response.Results = append(response.Results, results[id])
	}

	for _, entry := range catalog.Entries {
		if _, probed := s.probes[entry.ID]; !probed || !contains(probeIDs, entry.ID) {
			response.Unprobed = append(response.Unprobed, entry.ID)
		}
	}
	response.Partial = len(response.Unprobed) > 0
	for _, result := range response.Results {
		if result.ErrorCode == ErrorCodeProbeFailed || result.ErrorCode == ErrorCodeTimeout {
			response.Partial = true
		}
	}
	return response, nil
}

const (
	// ErrorCodeNotConfigured is emitted only when a prerequisite is explicitly
	// absent.  Configuration presence alone is never promoted to healthy.
	ErrorCodeNotConfigured = "not_configured"
	ErrorCodeProbeFailed   = "probe_failed"
	ErrorCodeTimeout       = "timeout"
)

type probeOutcome struct {
	id         string
	state      integrationstate.State
	errorCode  string
	durationMS int64
}

// collect performs bounded fan-out and returns safe results for every probe.
// The outcome channel is buffered so a probe that ignores cancellation cannot
// block its goroutine after the aggregate deadline has already been returned.
func collect(parent context.Context, probes map[string]Probe, ids []string) map[string]Result {
	results := make(map[string]Result, len(ids))
	if len(ids) == 0 {
		return results
	}

	aggregateCtx, cancel := context.WithTimeout(parent, AggregateTimeout)
	defer cancel()
	aggregateStarted := time.Now()

	outcomes := make(chan probeOutcome, len(ids))
	semaphore := make(chan struct{}, MaxConcurrency)
	for _, id := range ids {
		id := id
		go func() {
			started := time.Now()
			select {
			case semaphore <- struct{}{}:
			case <-aggregateCtx.Done():
				outcomes <- probeOutcome{id: id, state: integrationstate.Unavailable, errorCode: ErrorCodeTimeout, durationMS: elapsedMS(started)}
				return
			}
			defer func() { <-semaphore }()

			probeCtx, probeCancel := context.WithTimeout(aggregateCtx, ProbeTimeout)
			defer probeCancel()
			probeResult := make(chan probeOutcome, 1)
			go func() {
				state, err := runProbe(probeCtx, probes[id].Run)
				state, errorCode := classify(state, err, probeCtx.Err())
				probeResult <- probeOutcome{id: id, state: state, errorCode: errorCode, durationMS: elapsedMS(started)}
			}()
			select {
			case outcome := <-probeResult:
				outcomes <- outcome
			case <-probeCtx.Done():
				outcomes <- probeOutcome{id: id, state: integrationstate.Unavailable, errorCode: ErrorCodeTimeout, durationMS: elapsedMS(started)}
			}
		}()
	}

	seen := make(map[string]struct{}, len(ids))
	for len(seen) < len(ids) {
		select {
		case outcome := <-outcomes:
			seen[outcome.id] = struct{}{}
			probeName, _ := probeNameForID(outcome.id)
			results[outcome.id] = Result{
				ID:         outcome.id,
				State:      outcome.state,
				Probe:      probeName,
				ErrorCode:  outcome.errorCode,
				DurationMS: outcome.durationMS,
			}
		case <-aggregateCtx.Done():
			// Return a bounded response even if an injected function ignores
			// context cancellation.  Its eventual send is safe because the
			// channel has one slot per probe.
			for _, id := range ids {
				if _, exists := seen[id]; exists {
					continue
				}
				probeName, _ := probeNameForID(id)
				results[id] = Result{
					ID:         id,
					State:      integrationstate.Unavailable,
					Probe:      probeName,
					ErrorCode:  ErrorCodeTimeout,
					DurationMS: elapsedMSWithin(aggregateStarted, AggregateTimeout),
				}
				seen[id] = struct{}{}
			}
		}
	}
	return results
}

func runProbe(ctx context.Context, fn ProbeFunc) (integrationstate.State, error) {
	if fn == nil {
		return integrationstate.Unavailable, errors.New("probe is not configured")
	}
	return fn(ctx)
}

func classify(state integrationstate.State, err, contextErr error) (integrationstate.State, string) {
	// An explicit missing prerequisite remains not_configured even if the
	// provider returned an explanatory error. It is not a partial failure by
	// itself; the caller can configure and retry.
	if state == integrationstate.NotConfigured {
		return integrationstate.NotConfigured, ErrorCodeNotConfigured
	}
	if err == nil && state == integrationstate.Healthy {
		return integrationstate.Healthy, ""
	}
	if contextErr != nil && errors.Is(contextErr, context.DeadlineExceeded) {
		return integrationstate.Unavailable, ErrorCodeTimeout
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return integrationstate.Unavailable, ErrorCodeTimeout
	}
	return integrationstate.Unavailable, ErrorCodeProbeFailed
}

func catalogIDSet(catalog extensions.Catalog) (map[string]struct{}, error) {
	if catalog.SchemaVersion != SchemaVersion {
		return nil, fmt.Errorf("%w: schema_version must be %d", ErrInvalidCatalog, SchemaVersion)
	}
	ids := make(map[string]struct{}, len(catalog.Entries))
	for _, entry := range catalog.Entries {
		if entry.ID == "" {
			return nil, fmt.Errorf("%w: empty integration ID", ErrInvalidCatalog)
		}
		if !validIntegrationID(entry.ID) {
			return nil, fmt.Errorf("%w: invalid integration ID %q", ErrInvalidCatalog, entry.ID)
		}
		if _, exists := ids[entry.ID]; exists {
			return nil, fmt.Errorf("%w: duplicate integration ID", ErrInvalidCatalog)
		}
		ids[entry.ID] = struct{}{}
	}
	return ids, nil
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func elapsedMS(start time.Time) int64 {
	return time.Since(start).Milliseconds()
}

func elapsedMSWithin(start time.Time, maximum time.Duration) int64 {
	elapsed := time.Since(start)
	if elapsed > maximum {
		return maximum.Milliseconds()
	}
	return elapsed.Milliseconds()
}
