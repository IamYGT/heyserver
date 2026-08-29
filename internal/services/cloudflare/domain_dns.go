package cloudflare

import (
	"fmt"
	"net"
	"strings"
)

// DomainDNSResult describes the address record reconciled during domain
// provisioning. Provider details stay inside the Cloudflare integration.
type DomainDNSResult struct {
	Domain     string       `json:"domain"`
	ZoneID     string       `json:"zoneId"`
	RecordType string       `json:"recordType"`
	Change     RecordAction `json:"change"`
}

// ReconcileDomainAddress creates or updates the A/AAAA record for a domain.
// The origin is always installation-owned configuration; the application has
// no maintainer-specific IP fallback.
func (s *Service) ReconcileDomainAddress(domain, origin string, proxied bool) (*DomainDNSResult, error) {
	domain = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(domain), "."))
	origin = strings.TrimSpace(origin)
	if !validDNSHostname(domain) {
		return nil, fmt.Errorf("domain must be a valid hostname")
	}
	ip := net.ParseIP(origin)
	if ip == nil {
		return nil, fmt.Errorf("domain DNS origin must be a valid IP address")
	}

	recordType := "A"
	if ip.To4() == nil {
		recordType = "AAAA"
	}
	zoneID, err := s.findZoneForDomain(domain)
	if err != nil {
		return nil, fmt.Errorf("find zone for %s: %w", domain, err)
	}
	existing, err := s.ListRecords(zoneID, recordType, domain)
	if err != nil {
		return nil, fmt.Errorf("list %s record for %s: %w", recordType, domain, err)
	}
	want := desiredRecord{
		Type:    recordType,
		Name:    domain,
		Content: origin,
		Proxied: proxied,
	}
	change, err := s.reconcileRecord(zoneID, want, existing)
	if err != nil {
		return nil, fmt.Errorf("reconcile %s record for %s: %w", recordType, domain, err)
	}
	return &DomainDNSResult{
		Domain:     domain,
		ZoneID:     zoneID,
		RecordType: recordType,
		Change:     change,
	}, nil
}
