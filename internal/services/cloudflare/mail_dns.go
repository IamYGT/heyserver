package cloudflare

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// ErrMailDNSNotConfigured lets API adapters distinguish missing installation
// configuration from an upstream Cloudflare failure.
var ErrMailDNSNotConfigured = errors.New("mail DNS auto-fix not configured")

// --- Constants ----------------------------------------------------------------

const (
	defaultDMARCRecord = "v=DMARC1; p=none"
	defaultMXPriority  = 10

	// dnsRecordTTL is the default TTL (seconds) for records we create/update.
	dnsRecordTTL = 1 // 1 = "auto" in Cloudflare API
)

// MailDNSConfig describes the desired mail routing owned by an installation.
// No hostname or IP default is provided: DNS mutation must never inherit the
// maintainer's infrastructure.
type MailDNSConfig struct {
	Hostname    string
	PublicIP    string
	SPFRecord   string
	DMARCRecord string
	MXPriority  int
	StalwartURL string
}

func (c MailDNSConfig) normalized() MailDNSConfig {
	c.Hostname = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(c.Hostname), "."))
	c.PublicIP = strings.TrimSpace(c.PublicIP)
	c.SPFRecord = strings.TrimSpace(c.SPFRecord)
	c.DMARCRecord = strings.TrimSpace(c.DMARCRecord)
	c.StalwartURL = strings.TrimRight(strings.TrimSpace(c.StalwartURL), "/")
	if c.MXPriority <= 0 {
		c.MXPriority = defaultMXPriority
	}
	if c.DMARCRecord == "" {
		c.DMARCRecord = defaultDMARCRecord
	}
	return c
}

func (c MailDNSConfig) validate() error {
	if !validDNSHostname(c.Hostname) {
		return fmt.Errorf("%w: HSERVER_MAIL_DNS_HOSTNAME must be a valid hostname", ErrMailDNSNotConfigured)
	}
	if c.PublicIP != "" && net.ParseIP(c.PublicIP) == nil {
		return fmt.Errorf("%w: HSERVER_MAIL_DNS_PUBLIC_IP must be a valid IP address", ErrMailDNSNotConfigured)
	}
	if c.MXPriority < 1 || c.MXPriority > 65535 {
		return fmt.Errorf("%w: HSERVER_MAIL_DNS_MX_PRIORITY must be between 1 and 65535", ErrMailDNSNotConfigured)
	}
	if c.SPFRecord != "" && !strings.HasPrefix(strings.ToLower(c.SPFRecord), "v=spf1") {
		return fmt.Errorf("%w: HSERVER_MAIL_DNS_SPF must start with v=spf1", ErrMailDNSNotConfigured)
	}
	if !strings.HasPrefix(strings.ToLower(c.DMARCRecord), "v=dmarc1") {
		return fmt.Errorf("%w: HSERVER_MAIL_DNS_DMARC must start with v=DMARC1", ErrMailDNSNotConfigured)
	}
	return nil
}

func validDNSHostname(hostname string) bool {
	if hostname == "" || len(hostname) > 253 || !strings.Contains(hostname, ".") || strings.Contains(hostname, "..") {
		return false
	}
	for _, label := range strings.Split(hostname, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

// --- Stalwart DNS types -------------------------------------------------------

// stalwartDNSRecord mirrors one entry returned by Stalwart's
// GET /api/dns/records/{domain} endpoint.
type stalwartDNSRecord struct {
	Type     string `json:"type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl,omitempty"`
	Priority int    `json:"priority,omitempty"`
}

// --- Result types -------------------------------------------------------------

// RecordAction describes a single DNS change performed during AutoFixMailDNS.
type RecordAction struct {
	// Action is one of "created", "updated", "skipped" (already correct).
	Action   string `json:"action"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	OldValue string `json:"oldValue,omitempty"`
	NewValue string `json:"newValue"`
}

// AutoFixResult summarises what AutoFixMailDNS changed.
type AutoFixResult struct {
	Domain  string         `json:"domain"`
	ZoneID  string         `json:"zoneId"`
	Changes []RecordAction `json:"changes"`
	// DKIMPublished is true when a DKIM TXT record was published (sourced from
	// Stalwart). False means Stalwart had no DKIM key ready for this domain.
	DKIMPublished bool   `json:"dkimPublished"`
	DKIMNote      string `json:"dkimNote,omitempty"`
}

// --- Main function ------------------------------------------------------------

// AutoFixMailDNS reconciles Cloudflare DNS for domain so that it matches the
// installation-owned mail configuration:
//
//  1. Finds the Cloudflare zone for domain.
//  2. Queries Stalwart for recommended DNS records (DKIM public key etc.).
//  3. Compares with existing Cloudflare records.
//  4. Creates or updates records as needed:
//     - MX           → configured mail hostname
//     - SPF TXT      → configured value or a value derived from the public IP
//     - DKIM TXT     → from Stalwart (if available)
//     - DMARC TXT    → configured policy
//     - mail A/AAAA  → configured public IP when the hostname belongs to the zone
//
// The function returns a summary of every change made.
func (s *Service) AutoFixMailDNS(domain string) (*AutoFixResult, error) {
	var err error
	domain, err = NormalizeDomain(domain)
	if err != nil {
		return nil, err
	}
	if err := s.mailDNS.validate(); err != nil {
		return nil, err
	}

	// 1. Find the Cloudflare zone that owns this domain.
	zoneID, err := s.findZoneForDomain(domain)
	if err != nil {
		return nil, fmt.Errorf("find zone for %s: %w", domain, err)
	}

	result := &AutoFixResult{
		Domain: domain,
		ZoneID: zoneID,
	}

	// 2. Load all existing DNS records for this zone once (avoids N+1 calls).
	existing, err := s.ListRecords(zoneID, "", "")
	if err != nil {
		return nil, fmt.Errorf("list records for zone %s: %w", zoneID, err)
	}

	// 3. Build the desired record set.
	desired := s.buildDesiredRecords(domain)

	// 4. Try to enrich with Stalwart's DKIM key.
	dkimRecord, dkimNote := s.fetchStalwartDKIM(domain)
	if dkimRecord != nil {
		desired = append(desired, *dkimRecord)
		result.DKIMPublished = true
	} else {
		result.DKIMNote = dkimNote
	}

	// 5. Reconcile each desired record against Cloudflare.
	for _, want := range desired {
		action, err := s.reconcileRecord(zoneID, want, existing)
		if err != nil {
			return nil, fmt.Errorf("reconcile %s %s: %w", want.Type, want.Name, err)
		}
		result.Changes = append(result.Changes, action)
	}

	return result, nil
}

// --- Helper: find zone -------------------------------------------------------

// findZoneForDomain returns the Cloudflare zone ID for domain.
// It tries domain first, then progressively removes subdomains.
func (s *Service) findZoneForDomain(domain string) (string, error) {
	zones, err := s.ListZones()
	if err != nil {
		return "", err
	}

	// Build a map for fast lookup.
	zoneMap := make(map[string]string, len(zones))
	for _, z := range zones {
		zoneMap[strings.TrimSuffix(z.Name, ".")] = z.ID
	}

	// Walk up the DNS hierarchy: domain → parent → grandparent …
	candidate := strings.TrimSuffix(domain, ".")
	for {
		if id, ok := zoneMap[candidate]; ok {
			return id, nil
		}
		dot := strings.Index(candidate, ".")
		if dot == -1 {
			break
		}
		candidate = candidate[dot+1:]
	}

	return "", fmt.Errorf("no Cloudflare zone found for %s", domain)
}

// --- Helper: desired records -------------------------------------------------

// desiredRecord is a record we want to exist in Cloudflare.
type desiredRecord struct {
	Type     string
	Name     string // relative label (e.g. "@", "mail", "_dmarc", "_spf")
	Content  string
	Proxied  bool
	Priority int
}

func (s *Service) buildDesiredRecords(domain string) []desiredRecord {
	records := []desiredRecord{
		{
			Type:     "MX",
			Name:     domain,
			Content:  s.mailDNS.Hostname,
			Priority: s.mailDNS.MXPriority,
		},
		{
			Type:    "TXT",
			Name:    domain,
			Content: s.mailDNS.spfRecord(),
		},
		{
			Type:    "TXT",
			Name:    "_dmarc." + domain,
			Content: s.mailDNS.DMARCRecord,
		},
	}

	if s.mailDNS.PublicIP != "" && hostnameBelongsToDomain(s.mailDNS.Hostname, domain) {
		recordType := "A"
		if strings.Contains(s.mailDNS.PublicIP, ":") {
			recordType = "AAAA"
		}
		records = append(records, desiredRecord{
			Type:    recordType,
			Name:    s.mailDNS.Hostname,
			Content: s.mailDNS.PublicIP,
			Proxied: false,
		})
	}

	return records
}

func (c MailDNSConfig) spfRecord() string {
	if c.SPFRecord != "" {
		return c.SPFRecord
	}
	if c.PublicIP == "" {
		return "v=spf1 mx ~all"
	}
	mechanism := "ip4:"
	if strings.Contains(c.PublicIP, ":") {
		mechanism = "ip6:"
	}
	return fmt.Sprintf("v=spf1 mx %s%s ~all", mechanism, c.PublicIP)
}

func hostnameBelongsToDomain(hostname, domain string) bool {
	hostname = strings.ToLower(strings.TrimSuffix(hostname, "."))
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	return hostname == domain || strings.HasSuffix(hostname, "."+domain)
}

// --- Helper: Stalwart DKIM ---------------------------------------------------

// fetchStalwartDKIM queries the configured Stalwart instance for the recommended
// DKIM DNS record for domain. Returns nil + a note if no record is available.
func (s *Service) fetchStalwartDKIM(domain string) (*desiredRecord, string) {
	if s.mailDNS.StalwartURL == "" {
		return nil, "Stalwart DNS API not configured — DKIM record skipped"
	}
	endpoint := s.mailDNS.StalwartURL + "/api/dns/records/" + url.PathEscape(domain)

	resp, err := s.client.Get(endpoint)
	if err != nil {
		return nil, fmt.Sprintf("Stalwart DNS API unreachable (%s) — DKIM record skipped", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Sprintf("Stalwart has no DNS records for %s — generate a DKIM key first", domain)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Sprintf("Stalwart DNS API returned HTTP %d for %s — DKIM record skipped", resp.StatusCode, domain)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Sprintf("read Stalwart response: %s", err)
	}

	var records []stalwartDNSRecord
	if err := json.Unmarshal(body, &records); err != nil {
		return nil, fmt.Sprintf("parse Stalwart response: %s", err)
	}

	for _, rec := range records {
		ltype := strings.ToUpper(rec.Type)
		if ltype != "TXT" {
			continue
		}
		lower := strings.ToLower(rec.Content)
		if strings.Contains(lower, "v=dkim1") || strings.Contains(lower, "p=") {
			name := rec.Name
			if name == "" {
				// Stalwart may omit the FQDN; derive it from the domain.
				name = "default._domainkey." + domain
			}
			return &desiredRecord{
				Type:    "TXT",
				Name:    name,
				Content: rec.Content,
			}, ""
		}
	}

	return nil, fmt.Sprintf("Stalwart returned records for %s but no DKIM TXT found — generate a DKIM key first", domain)
}

// --- Helper: reconcile -------------------------------------------------------

// reconcileRecord creates or updates a single DNS record in Cloudflare to
// match want. It returns a RecordAction describing what happened.
func (s *Service) reconcileRecord(
	zoneID string,
	want desiredRecord,
	existing []CFRecord,
) (RecordAction, error) {
	action := RecordAction{
		Type:     want.Type,
		Name:     want.Name,
		NewValue: want.Content,
	}

	match := findExisting(existing, want)

	switch {
	case match == nil:
		// Record does not exist — create it.
		req := CreateRecordRequest{
			Type:     want.Type,
			Name:     want.Name,
			Content:  want.Content,
			TTL:      dnsRecordTTL,
			Proxied:  want.Proxied,
			Priority: want.Priority,
		}
		if _, err := s.CreateRecord(zoneID, req); err != nil {
			return action, err
		}
		action.Action = "created"

	case recordMatches(*match, want):
		// Record already has the correct value — nothing to do.
		action.Action = "skipped"

	default:
		// Record exists but value or proxy status differs — update it.
		action.OldValue = match.Content
		req := UpdateRecordRequest{
			Type:     want.Type,
			Name:     want.Name,
			Content:  want.Content,
			TTL:      dnsRecordTTL,
			Proxied:  want.Proxied,
			Priority: want.Priority,
		}
		if _, err := s.UpdateRecord(zoneID, match.ID, req); err != nil {
			return action, err
		}
		action.Action = "updated"
	}

	return action, nil
}

// findExisting locates an existing Cloudflare record that matches want by
// type + name. For TXT records at the apex it checks for SPF vs DMARC vs DKIM
// by content prefix so they don't overwrite each other.
func findExisting(existing []CFRecord, want desiredRecord) *CFRecord {
	wantName := strings.ToLower(strings.TrimSuffix(want.Name, "."))
	for i := range existing {
		rec := &existing[i]
		recName := strings.ToLower(strings.TrimSuffix(rec.Name, "."))
		if rec.Type != want.Type || recName != wantName {
			continue
		}
		// For TXT records at the same name (e.g. the apex has both SPF and
		// other TXTs), we need to distinguish between them by content category.
		if want.Type == "TXT" {
			if txtCategory(want.Content) != txtCategory(rec.Content) {
				continue
			}
		}
		return rec
	}
	return nil
}

// txtCategory maps a TXT record content to a canonical category string so we
// can distinguish SPF, DKIM, DMARC, and other TXT records at the same name.
func txtCategory(content string) string {
	lower := strings.ToLower(strings.TrimSpace(content))
	switch {
	case strings.HasPrefix(lower, "v=spf1"):
		return "spf"
	case strings.Contains(lower, "v=dkim1") || strings.Contains(lower, "._domainkey"):
		return "dkim"
	case strings.HasPrefix(lower, "v=dmarc1"):
		return "dmarc"
	default:
		return "other"
	}
}

// recordMatches returns true when the existing Cloudflare record already has
// the correct content and proxy status.
func recordMatches(rec CFRecord, want desiredRecord) bool {
	contentMatch := strings.TrimSpace(rec.Content) == strings.TrimSpace(want.Content)
	proxiedMatch := rec.Proxied == want.Proxied
	return contentMatch && proxiedMatch
}
