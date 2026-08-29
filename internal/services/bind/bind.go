package bind

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	zonesDir       = "/etc/bind/zones"
	namedConf      = "/etc/bind/named.conf.local"
	namedBin       = "named"
	namedCheckBin  = "named-checkconf"
	namedCheckZone = "named-checkzone"
	rndcBin        = "rndc"
)

// Zone represents a BIND DNS zone.
type Zone struct {
	Domain      string `json:"domain"`
	File        string `json:"file"`
	Serial      uint32 `json:"serial"`
	RecordCount int    `json:"recordCount"`
}

// Record represents a single DNS resource record.
type Record struct {
	Name     string `json:"name"`
	TTL      string `json:"ttl,omitempty"`
	Class    string `json:"class,omitempty"`
	Type     string `json:"type"`
	Value    string `json:"value"`
	Priority int    `json:"priority,omitempty"` // used for MX and SRV records
}

// ZoneDetail contains zone metadata and all its records.
type ZoneDetail struct {
	Zone
	Records []Record `json:"records"`
}

// CreateZoneRequest holds the parameters for creating a new zone.
type CreateZoneRequest struct {
	Domain string `json:"domain"`
	IP     string `json:"ip"`
}

// AddRecordRequest holds the parameters for adding a new record.
type AddRecordRequest struct {
	Name       string `json:"name"`
	TTL        string `json:"ttl,omitempty"`
	Type       string `json:"type"`
	Value      string `json:"value"`
	Priority   int    `json:"priority,omitempty"`
	AutoReload bool   `json:"autoReload,omitempty"`
}

// UpdateRecordRequest holds the parameters for replacing a record.
// Matching is done by Name+Type+OldValue; the record is replaced with new values.
type UpdateRecordRequest struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	OldValue   string `json:"oldValue"`
	NewValue   string `json:"newValue"`
	NewTTL     string `json:"newTtl,omitempty"`
	Priority   int    `json:"priority,omitempty"`
	AutoReload bool   `json:"autoReload,omitempty"`
}

// DeleteRecordRequest identifies a record to remove.
type DeleteRecordRequest struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Value      string `json:"value"`
	AutoReload bool   `json:"autoReload,omitempty"`
}

// UpdateSOARequest holds the fields for editing the SOA record.
type UpdateSOARequest struct {
	PrimaryNs  string `json:"primaryNs"`
	Hostmaster string `json:"hostmaster"`
	Refresh    uint32 `json:"refresh"`
	Retry      uint32 `json:"retry"`
	Expire     uint32 `json:"expire"`
	Minimum    uint32 `json:"minimum"`
}

// SOARecord represents the parsed fields of a SOA record.
type SOARecord struct {
	PrimaryNs  string `json:"primaryNs"`
	Hostmaster string `json:"hostmaster"`
	Serial     uint32 `json:"serial"`
	Refresh    uint32 `json:"refresh"`
	Retry      uint32 `json:"retry"`
	Expire     uint32 `json:"expire"`
	Minimum    uint32 `json:"minimum"`
}

// CheckResult holds the output of named-checkconf.
type CheckResult struct {
	OK         bool              `json:"ok"`
	Output     string            `json:"output"`
	ZoneChecks []ZoneCheckResult `json:"zoneChecks,omitempty"`
}

// ZoneCheckResult holds the result of named-checkzone for a single zone.
type ZoneCheckResult struct {
	Domain string `json:"domain"`
	OK     bool   `json:"ok"`
	Output string `json:"output"`
}

// ServiceStatus holds the current state of the BIND service.
type ServiceStatus struct {
	Available           bool           `json:"available"`
	Installed           bool           `json:"installed"`
	State               ReadinessState `json:"state"`
	Active              bool           `json:"active"`
	ServiceState        string         `json:"serviceState"`
	Version             string         `json:"version,omitempty"`
	ConfigAvailable     bool           `json:"configAvailable"`
	CheckToolsAvailable bool           `json:"checkToolsAvailable"`
	ReloadAvailable     bool           `json:"reloadAvailable"`
	ZoneManagementReady bool           `json:"zoneManagementReady"`
	RecoveryPending     bool           `json:"recoveryPending"`
	Error               string         `json:"error,omitempty"`
}

// ReadinessState describes whether this installation can safely manage BIND.
type ReadinessState string

const (
	StateHealthy       ReadinessState = "healthy"
	StateNotInstalled  ReadinessState = "not-installed"
	StateNotConfigured ReadinessState = "not-configured"
	StateStopped       ReadinessState = "stopped"
	StateUnavailable   ReadinessState = "unavailable"
)

type readinessProbe struct {
	installed           bool
	version             string
	versionError        error
	active              bool
	serviceState        string
	serviceObservable   bool
	configAvailable     bool
	checkToolsAvailable bool
	reloadAvailable     bool
}

// DNSLookupResult holds the result of a DNS lookup from one resolver.
type DNSLookupResult struct {
	Resolver string   `json:"resolver"`
	Records  []string `json:"records"`
	TTL      uint32   `json:"ttl"`
	Error    string   `json:"error,omitempty"`
}

// DNSLookupResponse is the full response for a DNS propagation check.
type DNSLookupResponse struct {
	Query   DNSLookupQuery    `json:"query"`
	Results []DNSLookupResult `json:"results"`
}

// DNSLookupQuery holds the parameters of the lookup.
type DNSLookupQuery struct {
	Domain string `json:"domain"`
	Type   string `json:"type"`
}

// Service provides BIND9 zone management operations.
type Service struct {
	mutationMu      sync.Mutex
	recoveryMu      sync.RWMutex
	journal         *lifecycleJournalStore
	recoveryPending bool
	recoveryErr     error

	// runner, lookPath, and configPath are the read-only readiness seams. They
	// are initialized by the constructors and kept on the service so tests can
	// exercise the probe without changing the host's BIND installation.
	runner     commandRunner
	lookPath   func(string) (string, error)
	configPath string
}

// New returns a new bind Service instance.
func New() *Service {
	return newService(nil)
}

// NewWithStateDir returns a service with durable lifecycle recovery enabled.
func NewWithStateDir(dataDir string) *Service {
	return newService(newLifecycleJournalStore(dataDir))
}

func newService(journal *lifecycleJournalStore) *Service {
	return &Service{
		journal:    journal,
		runner:     execRunner{},
		lookPath:   exec.LookPath,
		configPath: namedConf,
	}
}

// RecoverPendingTransaction restores or finalizes an interrupted zone lifecycle
// transaction. The journal is retained when recovery cannot complete.
func (s *Service) RecoverPendingTransaction() error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	err := recoverLifecycleJournal(s.journal, reloadCommand)
	s.refreshRecoveryState(err)
	return err
}

// ---------- Zone operations ----------

// ListZones returns all zones declared in named.conf.local with their serial numbers.
func (s *Service) ListZones() ([]Zone, error) {
	if err := requireZoneInventory(); err != nil {
		return nil, err
	}
	zones, err := parseNamedConf()
	if err != nil {
		return nil, err
	}

	// Enrich each zone with its current serial and record count
	for i := range zones {
		if serial, err := readSerial(zones[i].File); err == nil {
			zones[i].Serial = serial
		}
		if recs, err := parseZoneFile(zones[i].File); err == nil {
			zones[i].RecordCount = len(recs)
		}
	}
	return zones, nil
}

// GetZone returns metadata and all records for a single zone.
func (s *Service) GetZone(domain string) (*ZoneDetail, error) {
	if err := validateDomain(domain); err != nil {
		return nil, err
	}

	zones, err := parseNamedConf()
	if err != nil {
		return nil, err
	}

	var found *Zone
	for i := range zones {
		if zones[i].Domain == domain {
			found = &zones[i]
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("zone %q not found", domain)
	}

	if serial, err := readSerial(found.File); err == nil {
		found.Serial = serial
	}

	records, err := parseZoneFile(found.File)
	if err != nil {
		return nil, err
	}

	return &ZoneDetail{
		Zone:    *found,
		Records: records,
	}, nil
}

// CreateZone creates a new zone file and registers it in named.conf.local.
func (s *Service) CreateZone(req CreateZoneRequest) (*ZoneDetail, error) {
	var err error
	req, err = ValidateAndNormalizeCreateZone(req)
	if err != nil {
		return nil, err
	}

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	if err := s.requireZoneManagement(); err != nil {
		return nil, err
	}

	// Check zone doesn't already exist
	zones, err := parseNamedConf()
	if err != nil {
		return nil, err
	}
	for _, z := range zones {
		if z.Domain == req.Domain {
			return nil, fmt.Errorf("zone %q already exists", req.Domain)
		}
	}

	if err := os.MkdirAll(zonesDir, 0755); err != nil {
		return nil, fmt.Errorf("creating zones dir: %w", err)
	}

	zoneFile := filepath.Join(zonesDir, "db."+req.Domain)
	serial := uint32(time.Now().Unix())
	zoneContent := []byte(buildZoneFile(req.Domain, req.IP, serial))
	configContent, err := os.ReadFile(namedConf)
	if err != nil {
		return nil, fmt.Errorf("reading named.conf.local: %w", err)
	}
	entry := fmt.Sprintf("\nzone \"%s\" {\n\ttype master;\n\tfile \"%s\";\n};\n", req.Domain, zoneFile)
	configContent = append(configContent, []byte(entry)...)

	if err := applyZoneCreateTransaction(
		namedConf,
		zoneFile,
		configContent,
		zoneContent,
		validateConfigCandidate,
		func(candidate string) error { return validateZoneCandidate(req.Domain, candidate) },
		reloadCommand,
		s.journal,
	); err != nil {
		s.refreshRecoveryState(err)
		return nil, err
	}
	s.refreshRecoveryState(nil)

	return s.GetZone(req.Domain)
}

// DeleteZone removes the zone file and its entry from named.conf.local.
func (s *Service) DeleteZone(domain string) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	if err := s.requireZoneManagement(); err != nil {
		return err
	}
	if err := validateDomain(domain); err != nil {
		return err
	}

	zones, err := parseNamedConf()
	if err != nil {
		return err
	}

	var target *Zone
	for i := range zones {
		if zones[i].Domain == domain {
			target = &zones[i]
			break
		}
	}
	if target == nil {
		return fmt.Errorf("zone %q not found", domain)
	}

	configContent, err := os.ReadFile(namedConf)
	if err != nil {
		return fmt.Errorf("reading named.conf.local: %w", err)
	}
	updatedConfig, removed := removeZoneBlockContent(string(configContent), domain)
	if !removed {
		return fmt.Errorf("zone %q declaration was not found in named.conf.local", domain)
	}
	err = applyZoneDeleteTransaction(
		namedConf,
		target.File,
		[]byte(updatedConfig),
		validateConfigCandidate,
		reloadCommand,
		s.journal,
	)
	s.refreshRecoveryState(err)
	return err
}

// ExportZone returns the raw zone file content.
func (s *Service) ExportZone(domain string) (string, error) {
	if err := validateDomain(domain); err != nil {
		return "", err
	}

	zoneFile, err := zoneFileForDomain(domain)
	if err != nil {
		return "", err
	}

	data, err := os.ReadFile(zoneFile)
	if err != nil {
		return "", fmt.Errorf("reading zone file: %w", err)
	}

	return string(data), nil
}

// GetSOA parses and returns the SOA record fields for a zone.
func (s *Service) GetSOA(domain string) (*SOARecord, error) {
	if err := validateDomain(domain); err != nil {
		return nil, err
	}

	zoneFile, err := zoneFileForDomain(domain)
	if err != nil {
		return nil, err
	}

	return parseSOA(zoneFile)
}

// UpdateSOA replaces the SOA fields in the zone file and bumps the serial.
func (s *Service) UpdateSOA(domain string, req UpdateSOARequest) error {
	var err error
	req, err = ValidateAndNormalizeSOA(req)
	if err != nil {
		return err
	}

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	if err := s.requireZoneManagement(); err != nil {
		return err
	}
	if err := validateDomain(domain); err != nil {
		return err
	}
	zoneFile, err := zoneFileForDomain(domain)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(zoneFile)
	if err != nil {
		return fmt.Errorf("reading zone file: %w", err)
	}

	current, err := parseSOA(zoneFile)
	if err != nil {
		return err
	}

	newSerial := bumpSerialValue(current.Serial)

	// Replace the SOA block. The SOA section looks like:
	// @ IN SOA ns1.example.com. hostmaster.example.com. (
	//     2024010101 ; Serial
	//     3600       ; Refresh
	//     900        ; Retry
	//     604800     ; Expire
	//     300 )      ; Minimum TTL
	soaBlockRe := regexp.MustCompile(
		`(?s)([@\w]+\s+(?:\d+\s+)?(?:IN\s+)?SOA\s+)\S+\s+\S+\s*\(\s*` +
			`\d+([^)]*)\)`)
	if !soaBlockRe.Match(data) {
		return fmt.Errorf("SOA record not found in %s", zoneFile)
	}

	replacement := fmt.Sprintf("${1}%s %s (\n\t\t\t\t%d\t; Serial\n\t\t\t\t%d\t; Refresh\n\t\t\t\t%d\t; Retry\n\t\t\t\t%d\t; Expire\n\t\t\t\t%d )\t; Minimum TTL",
		req.PrimaryNs, req.Hostmaster,
		newSerial, req.Refresh, req.Retry, req.Expire, req.Minimum)

	result := soaBlockRe.ReplaceAllString(string(data), replacement)
	return applyZoneMutation(domain, zoneFile, []byte(result), true)
}

// ---------- Record operations ----------

// ListRecords returns all resource records for a zone.
func (s *Service) ListRecords(domain string) ([]Record, error) {
	detail, err := s.GetZone(domain)
	if err != nil {
		return nil, err
	}
	return detail.Records, nil
}

// AddRecord appends a new record to the zone file.
func (s *Service) AddRecord(domain string, req AddRecordRequest) error {
	var err error
	req, err = ValidateAndNormalizeAddRecord(req)
	if err != nil {
		return err
	}

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	if err := s.requireZoneManagement(); err != nil {
		return err
	}
	if err := validateDomain(domain); err != nil {
		return err
	}
	zoneFile, err := zoneFileForDomain(domain)
	if err != nil {
		return err
	}

	ttl := req.TTL
	if ttl == "" {
		ttl = "3600"
	}
	name := req.Name
	if name == "" {
		name = "@"
	}

	rtype := strings.ToUpper(req.Type)
	var line string
	switch rtype {
	case "MX":
		priority := req.Priority
		if priority == 0 {
			priority = 10
		}
		line = fmt.Sprintf("%s\t%s\tIN\t%s\t%d %s\n", name, ttl, rtype, priority, req.Value)
	case "SRV":
		priority := req.Priority
		if priority == 0 {
			priority = 10
		}
		line = fmt.Sprintf("%s\t%s\tIN\t%s\t%d %s\n", name, ttl, rtype, priority, req.Value)
	default:
		line = fmt.Sprintf("%s\t%s\tIN\t%s\t%s\n", name, ttl, rtype, req.Value)
	}

	data, err := os.ReadFile(zoneFile)
	if err != nil {
		return fmt.Errorf("reading zone file: %w", err)
	}
	content := string(data)
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	content += line
	content, err = bumpSerialContent(content)
	if err != nil {
		return err
	}
	return applyZoneMutation(domain, zoneFile, []byte(content), req.AutoReload)
}

// UpdateRecord replaces an existing record matched by Name+Type+OldValue.
func (s *Service) UpdateRecord(domain string, req UpdateRecordRequest) error {
	var err error
	req, err = ValidateAndNormalizeUpdateRecord(req)
	if err != nil {
		return err
	}

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	if err := s.requireZoneManagement(); err != nil {
		return err
	}
	if err := validateDomain(domain); err != nil {
		return err
	}
	zoneFile, err := zoneFileForDomain(domain)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(zoneFile)
	if err != nil {
		return fmt.Errorf("reading zone file: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	updated := false
	rtype := strings.ToUpper(req.Type)
	for i, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		if matchRecord(fields, req.Name, req.Type, req.OldValue) {
			ttl := req.NewTTL
			if ttl == "" && len(fields) >= 5 {
				ttl = fields[1] // preserve existing TTL
			}
			if ttl == "" {
				ttl = "3600"
			}
			switch rtype {
			case "MX", "SRV":
				priority := req.Priority
				if priority == 0 {
					priority = 10
				}
				lines[i] = fmt.Sprintf("%s\t%s\tIN\t%s\t%d %s",
					req.Name, ttl, rtype, priority, req.NewValue)
			default:
				lines[i] = fmt.Sprintf("%s\t%s\tIN\t%s\t%s",
					req.Name, ttl, rtype, req.NewValue)
			}
			updated = true
			break
		}
	}

	if !updated {
		return fmt.Errorf("record not found: %s %s %s", req.Name, req.Type, req.OldValue)
	}

	content, err := bumpSerialContent(strings.Join(lines, "\n"))
	if err != nil {
		return err
	}
	return applyZoneMutation(domain, zoneFile, []byte(content), req.AutoReload)
}

// DeleteRecord removes a record matched by Name+Type+Value.
// It accepts name/type/value either from the struct fields or, as a fallback,
// they may be provided via query parameters by the caller before constructing the struct.
func (s *Service) DeleteRecord(domain string, req DeleteRecordRequest) error {
	var err error
	req, err = ValidateAndNormalizeDeleteRecord(req)
	if err != nil {
		return err
	}

	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	if err := s.requireZoneManagement(); err != nil {
		return err
	}
	if err := validateDomain(domain); err != nil {
		return err
	}
	zoneFile, err := zoneFileForDomain(domain)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(zoneFile)
	if err != nil {
		return fmt.Errorf("reading zone file: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	var kept []string
	deleted := false
	for _, line := range lines {
		fields := strings.Fields(line)
		if !deleted && len(fields) >= 4 && matchRecord(fields, req.Name, req.Type, req.Value) {
			deleted = true
			continue
		}
		kept = append(kept, line)
	}

	if !deleted {
		return fmt.Errorf("record not found: %s %s %s", req.Name, req.Type, req.Value)
	}

	content, err := bumpSerialContent(strings.Join(kept, "\n"))
	if err != nil {
		return err
	}
	return applyZoneMutation(domain, zoneFile, []byte(content), req.AutoReload)
}

// ---------- Service control ----------

// Reload runs `rndc reload` to apply all zone changes without full restart.
func (s *Service) Reload() error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	if err := s.requireZoneManagement(); err != nil {
		return err
	}
	return reloadCommand()
}

func reloadCommand() error {
	out, err := exec.Command("rndc", "reload").CombinedOutput()
	if err != nil {
		return fmt.Errorf("rndc reload: %w — %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ReloadZone runs `rndc reload {domain}` for a zone-specific reload.
func (s *Service) ReloadZone(domain string) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	if err := validateDomain(domain); err != nil {
		return fmt.Errorf("ReloadZone: %w", err)
	}
	if err := s.requireZoneManagement(); err != nil {
		return err
	}
	return reloadZoneCommand(domain)
}

func reloadZoneCommand(domain string) error {
	out, err := exec.Command("rndc", "reload", domain).CombinedOutput()
	if err != nil {
		return fmt.Errorf("rndc reload %s: %w — %s", domain, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func applyZoneMutation(domain, zoneFile string, content []byte, autoReload bool) error {
	validate := func(candidate string) error { return validateZoneCandidate(domain, candidate) }
	var reload func() error
	if autoReload {
		reload = func() error { return reloadZoneCommand(domain) }
	}
	return applyZoneFileTransaction(zoneFile, content, validate, reload)
}

func validateZoneCandidate(domain, candidate string) error {
	out, err := exec.Command(namedCheckZone, domain, candidate).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %w — %s", namedCheckZone, domain, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func validateConfigCandidate(candidate string) error {
	out, err := exec.Command(namedCheckBin, candidate).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s: %w — %s", namedCheckBin, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Check runs named-checkconf -z to verify global syntax, then runs named-checkzone
// for each registered zone for per-zone validation.
func (s *Service) Check() CheckResult {
	status := s.Status()
	if !status.Installed || !status.ConfigAvailable || !status.CheckToolsAvailable {
		return CheckResult{OK: false, Output: readinessError(status).Error()}
	}
	out, err := exec.Command(namedCheckBin, "-z").CombinedOutput()
	result := CheckResult{
		OK:     err == nil,
		Output: strings.TrimSpace(string(out)),
	}

	// Per-zone validation
	zones, zErr := parseNamedConf()
	if zErr == nil {
		for _, z := range zones {
			zOut, zErr := exec.Command(namedCheckZone, z.Domain, z.File).CombinedOutput()
			result.ZoneChecks = append(result.ZoneChecks, ZoneCheckResult{
				Domain: z.Domain,
				OK:     zErr == nil,
				Output: strings.TrimSpace(string(zOut)),
			})
		}
	}

	return result
}

// Status returns the current state of the BIND9 service.
func (s *Service) Status() ServiceStatus {
	namedPath, namedErr := exec.LookPath(namedBin)
	probe := readinessProbe{
		installed:           namedErr == nil,
		serviceState:        "unknown",
		configAvailable:     regularFileExists(namedConf),
		checkToolsAvailable: commandAvailable(namedCheckBin) && commandAvailable(namedCheckZone),
		reloadAvailable:     commandAvailable("rndc"),
	}
	if namedErr != nil {
		return s.withRecoveryStatus(statusFromProbe(probe))
	}

	versionOut, versionErr := exec.Command(namedPath, "-v").CombinedOutput()
	probe.version = strings.TrimSpace(string(versionOut))
	probe.versionError = versionErr
	probe.serviceState, probe.serviceObservable = observeNamedService()
	probe.active = probe.serviceState == "active" || probe.serviceState == "activating" || probe.serviceState == "running"
	return s.withRecoveryStatus(statusFromProbe(probe))
}

func (s *Service) refreshRecoveryState(operationErr error) {
	pending := false
	var recoveryErr error
	if s.journal != nil {
		var err error
		pending, err = s.journal.exists()
		if err != nil {
			pending = true
			recoveryErr = err
		} else if pending {
			recoveryErr = operationErr
		}
	}
	s.recoveryMu.Lock()
	s.recoveryPending = pending
	s.recoveryErr = recoveryErr
	s.recoveryMu.Unlock()
}

func (s *Service) withRecoveryStatus(status ServiceStatus) ServiceStatus {
	s.recoveryMu.RLock()
	pending := s.recoveryPending
	recoveryErr := s.recoveryErr
	s.recoveryMu.RUnlock()
	if !pending {
		return status
	}
	status.State = StateUnavailable
	status.ZoneManagementReady = false
	status.RecoveryPending = true
	status.Error = "an interrupted BIND lifecycle transaction needs recovery; inspect the service log and restart HServer after correcting BIND"
	if recoveryErr == nil {
		status.Error = "an interrupted BIND lifecycle transaction is waiting for startup recovery"
	}
	return status
}

func statusFromProbe(probe readinessProbe) ServiceStatus {
	status := ServiceStatus{
		Installed:           probe.installed,
		State:               StateUnavailable,
		Active:              probe.active,
		ServiceState:        probe.serviceState,
		Version:             probe.version,
		ConfigAvailable:     probe.configAvailable,
		CheckToolsAvailable: probe.checkToolsAvailable,
		ReloadAvailable:     probe.reloadAvailable,
	}
	if !probe.installed {
		status.State = StateNotInstalled
		status.Error = "named executable was not found in PATH"
		return status
	}
	if probe.versionError != nil {
		status.Error = fmt.Sprintf("named -v failed: %v", probe.versionError)
		return status
	}
	status.Available = true
	if !probe.configAvailable || !probe.checkToolsAvailable || !probe.reloadAvailable {
		status.State = StateNotConfigured
		status.Error = missingBindRequirements(probe)
		return status
	}
	if !probe.serviceObservable {
		status.Error = "BIND service state could not be observed through systemd or the named process"
		return status
	}
	if !probe.active {
		status.State = StateStopped
		status.Error = "BIND is installed and configured but the named process is not running"
		return status
	}
	status.State = StateHealthy
	status.ZoneManagementReady = true
	return status
}

func missingBindRequirements(probe readinessProbe) string {
	missing := make([]string, 0, 3)
	if !probe.configAvailable {
		missing = append(missing, namedConf)
	}
	if !probe.checkToolsAvailable {
		missing = append(missing, namedCheckBin+" and "+namedCheckZone)
	}
	if !probe.reloadAvailable {
		missing = append(missing, "rndc")
	}
	return "BIND management requirements are missing: " + strings.Join(missing, ", ")
}

func observeNamedService() (string, bool) {
	for _, unit := range []string{"named", "bind9"} {
		out, _ := exec.Command("systemctl", "is-active", unit).CombinedOutput()
		state := strings.TrimSpace(string(out))
		if knownServiceState(state) {
			return state, true
		}
	}
	if err := exec.Command("pgrep", "-x", "named").Run(); err == nil {
		return "running", true
	} else if _, lookErr := exec.LookPath("pgrep"); lookErr == nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "stopped", true
		}
	}
	return "unknown", false
}

func knownServiceState(state string) bool {
	switch state {
	case "active", "activating", "inactive", "failed", "deactivating", "reloading":
		return true
	default:
		return false
	}
}

func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func regularFileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func requireZoneInventory() error {
	if regularFileExists(namedConf) {
		return nil
	}
	return fmt.Errorf("BIND zone inventory is unavailable: %s is not a regular file", namedConf)
}

func (s *Service) requireZoneManagement() error {
	status := s.Status()
	if status.ZoneManagementReady {
		return nil
	}
	return readinessError(status)
}

func readinessError(status ServiceStatus) error {
	if status.Error != "" {
		return fmt.Errorf("BIND zone management is not ready (%s): %s", status.State, status.Error)
	}
	return fmt.Errorf("BIND zone management is not ready (%s)", status.State)
}

// LookupDNS performs a live DNS lookup against multiple resolvers for propagation checking.
func (s *Service) LookupDNS(domain, qtype string) DNSLookupResponse {
	resolvers := []struct {
		name string
		addr string
	}{
		{"Google (8.8.8.8)", "8.8.8.8:53"},
		{"Cloudflare (1.1.1.1)", "1.1.1.1:53"},
		{"System", ""},
	}

	resp := DNSLookupResponse{
		Query: DNSLookupQuery{
			Domain: domain,
			Type:   strings.ToUpper(qtype),
		},
	}

	for _, r := range resolvers {
		result := lookupWithResolver(domain, qtype, r.name, r.addr)
		resp.Results = append(resp.Results, result)
	}

	return resp
}

// ---------- Helpers ----------

func validateDomain(domain string) error {
	_, err := normalizeDNSName("domain", domain, true, true, true)
	return err
}

// parseNamedConf reads /etc/bind/named.conf.local and extracts zone declarations.
func parseNamedConf() ([]Zone, error) {
	data, err := os.ReadFile(namedConf)
	if err != nil {
		if os.IsNotExist(err) {
			return []Zone{}, nil
		}
		return nil, fmt.Errorf("reading named.conf.local: %w", err)
	}

	// Simple regex-based parser for: zone "example.com" { ... file "path"; ... };
	var zones []Zone
	zoneRe := regexp.MustCompile(`zone\s+"([^"]+)"\s*\{[^}]*file\s+"([^"]+)"`)
	matches := zoneRe.FindAllStringSubmatch(string(data), -1)
	for _, m := range matches {
		zones = append(zones, Zone{
			Domain: m[1],
			File:   m[2],
		})
	}
	return zones, nil
}

// readSerial parses the SOA serial from a zone file.
func readSerial(zoneFile string) (uint32, error) {
	data, err := os.ReadFile(zoneFile)
	if err != nil {
		return 0, err
	}

	// SOA serial is the first numeric field after opening paren of the SOA record
	soaRe := regexp.MustCompile(`(?m)SOA\s+\S+\s+\S+\s+\(\s*(\d+)`)
	m := soaRe.FindStringSubmatch(string(data))
	if m == nil {
		return 0, fmt.Errorf("SOA record not found in %s", zoneFile)
	}
	n, err := strconv.ParseUint(m[1], 10, 32)
	if err != nil {
		return 0, err
	}
	return uint32(n), nil
}

// bumpSerialValue increments a serial number, using date-based format when possible.
func bumpSerialValue(current uint32) uint32 {
	today := uint32(time.Now().Unix())
	// Use YYYYMMDDNN format if serial looks like one (> year 2000 timestamp base)
	dateBase := uint32(20000101_00)
	if current >= dateBase {
		todayDate := uint32(time.Now().Format("20060102")[0:8][0]) // just increment
		_ = todayDate
		return current + 1
	}
	// For unix-timestamp based serials, just increment
	if current < today {
		return today
	}
	return current + 1
}

// bumpSerialContent increments the first SOA serial without touching disk.
func bumpSerialContent(content string) (string, error) {
	soaRe := regexp.MustCompile(`((?m)SOA\s+\S+\s+\S+\s+\(\s*)(\d+)`)
	indices := soaRe.FindStringSubmatchIndex(content)
	if len(indices) < 6 {
		return "", fmt.Errorf("SOA serial not found")
	}
	current, err := strconv.ParseUint(content[indices[4]:indices[5]], 10, 32)
	if err != nil {
		return "", fmt.Errorf("parsing SOA serial: %w", err)
	}
	next := strconv.FormatUint(uint64(bumpSerialValue(uint32(current))), 10)
	return content[:indices[4]] + next + content[indices[5]:], nil
}

// parseSOA extracts SOA fields from a zone file.
func parseSOA(zoneFile string) (*SOARecord, error) {
	data, err := os.ReadFile(zoneFile)
	if err != nil {
		return nil, fmt.Errorf("reading zone file: %w", err)
	}

	// Match: @ IN SOA ns1.example.com. hostmaster.example.com. (
	//            serial refresh retry expire minimum )
	soaRe := regexp.MustCompile(
		`(?s)(?:[@\w]+\s+)?(?:\d+\s+)?(?:IN\s+)?SOA\s+(\S+)\s+(\S+)\s*\(\s*` +
			`(\d+)\s+(\d+)\s+(\d+)\s+(\d+)\s+(\d+)`)
	m := soaRe.FindStringSubmatch(string(data))
	if m == nil {
		return nil, fmt.Errorf("SOA record not found in %s", zoneFile)
	}

	parse := func(s string) uint32 {
		n, _ := strconv.ParseUint(s, 10, 32)
		return uint32(n)
	}

	return &SOARecord{
		PrimaryNs:  m[1],
		Hostmaster: m[2],
		Serial:     parse(m[3]),
		Refresh:    parse(m[4]),
		Retry:      parse(m[5]),
		Expire:     parse(m[6]),
		Minimum:    parse(m[7]),
	}, nil
}

// parseZoneFile reads and returns all resource records from a zone file,
// skipping SOA, comments, and directives.
func parseZoneFile(zoneFile string) ([]Record, error) {
	f, err := os.Open(zoneFile)
	if err != nil {
		return nil, fmt.Errorf("opening zone file %s: %w", zoneFile, err)
	}
	defer func() { _ = f.Close() }()

	var records []Record
	scanner := bufio.NewScanner(f)
	inSOA := false
	defaultTTL := "3600"
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines
		if line == "" {
			continue
		}

		// Strip inline comments (semicolon not inside quotes)
		if idx := strings.Index(line, ";"); idx >= 0 {
			line = strings.TrimSpace(line[:idx])
			if line == "" {
				continue
			}
		}

		// Track SOA multi-line block
		if strings.Contains(line, "SOA") {
			inSOA = true
		}
		if inSOA {
			if strings.Contains(line, ")") {
				inSOA = false
			}
			continue
		}

		// Capture $TTL directive as default
		if strings.HasPrefix(line, "$TTL") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				defaultTTL = parts[1]
			}
			continue
		}

		// Skip other directives ($ORIGIN, etc.)
		if strings.HasPrefix(line, "$") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		rec := parseRecordFields(fields)
		if rec != nil {
			// Apply default TTL if not specified in the record
			if rec.TTL == "" {
				rec.TTL = defaultTTL
			}
			records = append(records, *rec)
		}
	}
	return records, scanner.Err()
}

// parseRecordFields parses a slice of fields from a zone file line into a Record.
// Handles: name ttl class type value  and  name class type value
// For MX and SRV records, priority is extracted from value and placed in Priority field.
func parseRecordFields(fields []string) *Record {
	if len(fields) < 3 {
		return nil
	}

	name := fields[0]
	idx := 1
	ttl := ""

	// Optional TTL (numeric)
	if _, err := strconv.Atoi(fields[idx]); err == nil {
		ttl = fields[idx]
		idx++
	}

	// Optional class
	class := "IN"
	if idx < len(fields) && strings.EqualFold(fields[idx], "IN") {
		class = fields[idx]
		idx++
	}

	if idx+1 > len(fields)-1 {
		return nil
	}

	rtype := strings.ToUpper(fields[idx])
	idx++
	valueFields := fields[idx:]

	rec := &Record{
		Name:  name,
		TTL:   ttl,
		Class: class,
		Type:  rtype,
	}

	switch rtype {
	case "MX":
		// MX: priority mail.example.com.
		if len(valueFields) >= 2 {
			if p, err := strconv.Atoi(valueFields[0]); err == nil {
				rec.Priority = p
				rec.Value = strings.Join(valueFields[1:], " ")
			} else {
				rec.Value = strings.Join(valueFields, " ")
			}
		} else {
			rec.Value = strings.Join(valueFields, " ")
		}
	case "SRV":
		// SRV: priority weight port target
		if len(valueFields) >= 2 {
			if p, err := strconv.Atoi(valueFields[0]); err == nil {
				rec.Priority = p
				rec.Value = strings.Join(valueFields[1:], " ")
			} else {
				rec.Value = strings.Join(valueFields, " ")
			}
		} else {
			rec.Value = strings.Join(valueFields, " ")
		}
	case "CAA":
		// CAA: flag tag "value" — keep full value as-is
		rec.Value = strings.Join(valueFields, " ")
	default:
		rec.Value = strings.Join(valueFields, " ")
	}

	return rec
}

// matchRecord checks if a line's fields match the given name, type, and value.
// For MX and SRV, value may be either the full "priority rest" string or just "rest"
// (after priority extraction), so we check both forms.
func matchRecord(fields []string, name, rtype, value string) bool {
	if fields[0] != name {
		return false
	}

	idx := 1
	// Skip optional TTL
	if idx < len(fields) {
		if _, err := strconv.Atoi(fields[idx]); err == nil {
			idx++
		}
	}
	// Skip optional class
	if idx < len(fields) && strings.EqualFold(fields[idx], "IN") {
		idx++
	}
	if idx >= len(fields) {
		return false
	}
	if !strings.EqualFold(fields[idx], rtype) {
		return false
	}
	idx++
	if idx >= len(fields) {
		return false
	}

	rawValue := strings.Join(fields[idx:], " ")
	if rawValue == value {
		return true
	}

	// For MX/SRV, the caller may pass only the non-priority part of the value.
	// Try matching after stripping the leading priority number.
	rt := strings.ToUpper(rtype)
	if rt == "MX" || rt == "SRV" {
		parts := strings.SplitN(rawValue, " ", 2)
		if len(parts) == 2 {
			if _, err := strconv.Atoi(parts[0]); err == nil && parts[1] == value {
				return true
			}
		}
	}

	return false
}

// zoneFileForDomain looks up the zone file path for a given domain.
func zoneFileForDomain(domain string) (string, error) {
	zones, err := parseNamedConf()
	if err != nil {
		return "", err
	}
	for _, z := range zones {
		if z.Domain == domain {
			return z.File, nil
		}
	}
	return "", fmt.Errorf("zone %q not found", domain)
}

// removeZoneBlockContent removes one exact zone declaration without touching disk.
func removeZoneBlockContent(content, domain string) (string, bool) {
	pattern := regexp.MustCompile(
		`(?s)(?:^|\n)\s*zone\s+"` + regexp.QuoteMeta(domain) + `"\s*\{[^}]*\};\s*`)
	location := pattern.FindStringIndex(content)
	if location == nil {
		return content, false
	}
	result := content[:location[0]] + "\n" + content[location[1]:]
	return strings.TrimLeft(result, "\n"), true
}

// buildZoneFile generates the initial content for a new BIND zone file.
func buildZoneFile(domain, ip string, serial uint32) string {
	return fmt.Sprintf(`$TTL 3600
@	IN	SOA	ns1.%s.	hostmaster.%s. (
			%d	; Serial
			3600	; Refresh
			900	; Retry
			604800	; Expire
			300 )	; Minimum TTL
;
@	IN	NS	ns1.%s.
@	IN	NS	ns2.%s.
@	IN	A	%s
ns1	IN	A	%s
ns2	IN	A	%s
www	IN	A	%s
`, domain, domain, serial, domain, domain, ip, ip, ip, ip)
}

// lookupWithResolver performs a DNS lookup using the specified resolver address.
// If addr is empty, the system default resolver is used.
func lookupWithResolver(domain, qtype, resolverName, addr string) DNSLookupResult {
	result := DNSLookupResult{
		Resolver: resolverName,
		Records:  []string{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var r *net.Resolver
	if addr != "" {
		r = &net.Resolver{
			PreferGo: true,
			Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
				d := net.Dialer{Timeout: 5 * time.Second}
				return d.DialContext(ctx, "udp", addr)
			},
		}
	} else {
		r = net.DefaultResolver
	}

	qtype = strings.ToUpper(qtype)
	switch qtype {
	case "A":
		addrs, err := r.LookupHost(ctx, domain)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		// Filter to only IPv4
		for _, a := range addrs {
			if ip := net.ParseIP(a); ip != nil && ip.To4() != nil {
				result.Records = append(result.Records, a)
			}
		}
	case "AAAA":
		addrs, err := r.LookupHost(ctx, domain)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		for _, a := range addrs {
			if ip := net.ParseIP(a); ip != nil && ip.To4() == nil {
				result.Records = append(result.Records, a)
			}
		}
	case "MX":
		mxs, err := r.LookupMX(ctx, domain)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		for _, mx := range mxs {
			result.Records = append(result.Records, fmt.Sprintf("%d %s", mx.Pref, mx.Host))
		}
	case "TXT":
		txts, err := r.LookupTXT(ctx, domain)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		result.Records = txts
	case "NS":
		nss, err := r.LookupNS(ctx, domain)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		for _, ns := range nss {
			result.Records = append(result.Records, ns.Host)
		}
	case "CNAME":
		cname, err := r.LookupCNAME(ctx, domain)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		result.Records = []string{cname}
	case "SRV":
		_, srvs, err := r.LookupSRV(ctx, "", "", domain)
		if err != nil {
			result.Error = err.Error()
			return result
		}
		for _, srv := range srvs {
			result.Records = append(result.Records,
				fmt.Sprintf("%d %d %d %s", srv.Priority, srv.Weight, srv.Port, srv.Target))
		}
	default:
		result.Error = fmt.Sprintf("unsupported query type: %s", qtype)
	}

	return result
}
