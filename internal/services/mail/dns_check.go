package mail

import (
	"fmt"
	"net"
	"strings"
)

// dkimSelectors is the ordered list of selectors we probe when checking DKIM.
// Stalwart commonly uses "mail", "default", or date-based selectors;
// Microsoft/Google use selector1/selector2.
var dkimSelectors = []string{
	"default",
	"dkim",
	"mail",
	"stalwart",
	"selector1",
	"selector2",
}

// MXRecord holds a single MX entry with its priority.
type MXRecord struct {
	Host     string `json:"host"`
	Priority uint16 `json:"priority"`
}

// DNSCheckResult holds the result of a single DNS check.
type DNSCheckResult struct {
	Found       bool   `json:"found"`
	Valid       bool   `json:"valid"`
	Value       string `json:"value,omitempty"`
	Selector    string `json:"selector,omitempty"` // DKIM only
	Policy      string `json:"policy,omitempty"`   // DMARC only
	Error       string `json:"error,omitempty"`
	Suggestion  string `json:"suggestion,omitempty"`
	MXRecords   []MXRecord `json:"mxRecords,omitempty"` // MX only
}

// DNSHealthReport is the comprehensive mail-DNS health report for a domain.
type DNSHealthReport struct {
	Domain      string         `json:"domain"`
	MX          DNSCheckResult `json:"mx"`
	SPF         DNSCheckResult `json:"spf"`
	DKIM        DNSCheckResult `json:"dkim"`
	DMARC       DNSCheckResult `json:"dmarc"`
	ReverseDNS  DNSCheckResult `json:"reverseDns"`
	Score       int            `json:"score"`       // 0-100
	Suggestions []string       `json:"suggestions"` // ordered list of what to fix
}

// CheckMX looks up MX records for domain and returns structured results.
func CheckMX(domain string) DNSCheckResult {
	mxs, err := net.LookupMX(domain)
	if err != nil {
		return DNSCheckResult{
			Found:      false,
			Error:      err.Error(),
			Suggestion: fmt.Sprintf("Add an MX record pointing to your mail server, e.g.: MX 10 mail.%s", domain),
		}
	}
	if len(mxs) == 0 {
		return DNSCheckResult{
			Found:      false,
			Suggestion: fmt.Sprintf("No MX records found. Add: MX 10 mail.%s", domain),
		}
	}

	records := make([]MXRecord, 0, len(mxs))
	parts := make([]string, 0, len(mxs))
	for _, mx := range mxs {
		host := strings.TrimSuffix(mx.Host, ".")
		records = append(records, MXRecord{Host: host, Priority: mx.Pref})
		parts = append(parts, fmt.Sprintf("%d %s", mx.Pref, host))
	}

	return DNSCheckResult{
		Found:     true,
		Valid:     true,
		Value:     strings.Join(parts, ", "),
		MXRecords: records,
	}
}

// CheckSPF looks up TXT records for domain and finds the SPF record.
func CheckSPF(domain string) DNSCheckResult {
	txts, err := net.LookupTXT(domain)
	if err != nil {
		return DNSCheckResult{
			Found:      false,
			Error:      err.Error(),
			Suggestion: "Add TXT record: v=spf1 mx a ip4:<SERVER_IP> ~all",
		}
	}

	for _, txt := range txts {
		lower := strings.ToLower(txt)
		if strings.HasPrefix(lower, "v=spf1") {
			// Valid SPF ends with an explicit all mechanism.
			valid := strings.Contains(lower, "~all") ||
				strings.Contains(lower, "-all") ||
				strings.Contains(lower, "?all") ||
				strings.Contains(lower, "+all")

			result := DNSCheckResult{
				Found: true,
				Valid: valid,
				Value: txt,
			}
			if !valid {
				result.Suggestion = "SPF record is missing an 'all' mechanism. Recommended: ~all (softfail) or -all (fail)."
			}
			// Warn about too-permissive +all
			if strings.Contains(lower, "+all") {
				result.Valid = false
				result.Suggestion = "SPF uses +all which allows any server to send mail. Change to ~all or -all."
			}
			return result
		}
	}

	return DNSCheckResult{
		Found:      false,
		Suggestion: fmt.Sprintf("Add TXT record on %s: v=spf1 mx a ip4:<SERVER_IP> ~all", domain),
	}
}

// CheckDKIM probes common selectors and returns the first matching DKIM record.
func CheckDKIM(domain string) DNSCheckResult {
	for _, sel := range dkimSelectors {
		target := fmt.Sprintf("%s._domainkey.%s", sel, domain)
		txts, err := net.LookupTXT(target)
		if err != nil || len(txts) == 0 {
			continue
		}
		// Multiple strings in a single TXT record are concatenated.
		full := strings.Join(txts, "")
		lower := strings.ToLower(full)

		// A valid DKIM record must contain a public key (p=...) and start with v=DKIM1.
		hasVersion := strings.Contains(lower, "v=dkim1")
		hasKey := strings.Contains(lower, "p=")

		result := DNSCheckResult{
			Found:    true,
			Valid:    hasVersion && hasKey,
			Value:    full,
			Selector: sel,
		}
		if !result.Valid {
			result.Suggestion = fmt.Sprintf(
				"DKIM record at %s exists but appears malformed (missing v=DKIM1 or p= key).",
				target,
			)
		}
		return result
	}

	return DNSCheckResult{
		Found: false,
		Suggestion: fmt.Sprintf(
			"No DKIM record found (tried selectors: %s). Generate a DKIM key in Stalwart admin, "+
				"then publish the TXT record at: <selector>._domainkey.%s",
			strings.Join(dkimSelectors, ", "),
			domain,
		),
	}
}

// CheckDMARC looks up the _dmarc TXT record and parses the p= policy tag.
func CheckDMARC(domain string) DNSCheckResult {
	target := "_dmarc." + domain
	txts, err := net.LookupTXT(target)
	if err != nil {
		return DNSCheckResult{
			Found:      false,
			Error:      err.Error(),
			Suggestion: fmt.Sprintf("Add TXT record on %s: v=DMARC1; p=quarantine; rua=mailto:dmarc@%s", target, domain),
		}
	}

	for _, txt := range txts {
		lower := strings.ToLower(txt)
		if !strings.HasPrefix(lower, "v=dmarc1") {
			continue
		}

		policy := parseDMARCPolicy(txt)
		valid := policy == "none" || policy == "quarantine" || policy == "reject"

		result := DNSCheckResult{
			Found:  true,
			Valid:  valid,
			Value:  txt,
			Policy: policy,
		}

		if policy == "none" {
			result.Suggestion = "DMARC policy is p=none (monitoring only). Consider upgrading to p=quarantine or p=reject once you verify all mail sources are aligned."
		} else if !valid {
			result.Valid = false
			result.Suggestion = "DMARC record is missing or has an invalid p= policy tag."
		}
		return result
	}

	return DNSCheckResult{
		Found:      false,
		Suggestion: fmt.Sprintf("Add TXT record on %s: v=DMARC1; p=quarantine; rua=mailto:dmarc@%s", target, domain),
	}
}

// parseDMARCPolicy extracts the value of the p= tag from a raw DMARC record.
func parseDMARCPolicy(record string) string {
	for _, part := range strings.Split(record, ";") {
		part = strings.TrimSpace(part)
		lower := strings.ToLower(part)
		if strings.HasPrefix(lower, "p=") {
			return strings.TrimPrefix(lower, "p=")
		}
	}
	return ""
}

// CheckReverseDNS does a PTR lookup for ip and verifies the returned hostname
// resolves back to the same IP (forward-confirmed rDNS).
func CheckReverseDNS(ip string) DNSCheckResult {
	if ip == "" {
		return DNSCheckResult{
			Found:      false,
			Error:      "no IP address provided",
			Suggestion: "Provide the server's public IP address to check rDNS (PTR record).",
		}
	}

	hostnames, err := net.LookupAddr(ip)
	if err != nil || len(hostnames) == 0 {
		return DNSCheckResult{
			Found: false,
			Error: func() string {
				if err != nil {
					return err.Error()
				}
				return "no PTR record found"
			}(),
			Suggestion: fmt.Sprintf(
				"Set a PTR record for %s pointing to your mail server hostname (e.g. mail.yourdomain.com). "+
					"This is usually configured at your hosting provider / VPS control panel.",
				ip,
			),
		}
	}

	ptr := strings.TrimSuffix(hostnames[0], ".")

	// Forward-confirm: the PTR hostname must resolve back to the queried IP.
	addrs, lookupErr := net.LookupHost(ptr)
	forwardOK := false
	if lookupErr == nil {
		for _, a := range addrs {
			if a == ip {
				forwardOK = true
				break
			}
		}
	}

	result := DNSCheckResult{
		Found: true,
		Valid: forwardOK,
		Value: ptr,
	}
	if !forwardOK {
		result.Suggestion = fmt.Sprintf(
			"PTR record for %s points to %s, but %s does not resolve back to %s "+
				"(forward-confirmed rDNS mismatch). Ensure an A record exists for %s → %s.",
			ip, ptr, ptr, ip, ptr, ip,
		)
	}
	return result
}

// CheckAll runs all DNS health checks for domain (with optional serverIP for rDNS)
// and returns a comprehensive DNSHealthReport including a score and suggestions.
func CheckAll(domain string, serverIP ...string) DNSHealthReport {
	report := DNSHealthReport{
		Domain: domain,
		MX:     CheckMX(domain),
		SPF:    CheckSPF(domain),
		DKIM:   CheckDKIM(domain),
		DMARC:  CheckDMARC(domain),
	}

	ip := ""
	if len(serverIP) > 0 {
		ip = serverIP[0]
	}
	report.ReverseDNS = CheckReverseDNS(ip)

	report.Score, report.Suggestions = computeScore(&report)
	return report
}

// computeScore calculates a 0-100 health score and collects actionable suggestions.
//
// Weights:
//   - MX    : 20 pts (mail delivery impossible without it)
//   - SPF   : 20 pts
//   - DKIM  : 25 pts (strongly weighted — modern spam filters rely on it)
//   - DMARC : 25 pts
//   - rDNS  : 10 pts
func computeScore(r *DNSHealthReport) (int, []string) {
	score := 0
	var suggestions []string

	// MX — 20 pts
	if r.MX.Found && r.MX.Valid {
		score += 20
	} else {
		if r.MX.Suggestion != "" {
			suggestions = append(suggestions, "MX: "+r.MX.Suggestion)
		} else {
			suggestions = append(suggestions, fmt.Sprintf("MX: Add an MX record for %s.", r.Domain))
		}
	}

	// SPF — 20 pts (10 for found, 10 for valid)
	if r.SPF.Found {
		score += 10
		if r.SPF.Valid {
			score += 10
		} else if r.SPF.Suggestion != "" {
			suggestions = append(suggestions, "SPF: "+r.SPF.Suggestion)
		}
	} else {
		if r.SPF.Suggestion != "" {
			suggestions = append(suggestions, "SPF: "+r.SPF.Suggestion)
		} else {
			suggestions = append(suggestions, fmt.Sprintf("SPF: Add a TXT record on %s: v=spf1 mx ~all", r.Domain))
		}
	}

	// DKIM — 25 pts (10 for found, 15 for valid)
	if r.DKIM.Found {
		score += 10
		if r.DKIM.Valid {
			score += 15
		} else if r.DKIM.Suggestion != "" {
			suggestions = append(suggestions, "DKIM: "+r.DKIM.Suggestion)
		}
	} else {
		if r.DKIM.Suggestion != "" {
			suggestions = append(suggestions, "DKIM: "+r.DKIM.Suggestion)
		}
	}

	// DMARC — 25 pts (10 for found, 15 for reject/quarantine policy)
	if r.DMARC.Found {
		score += 10
		if r.DMARC.Valid {
			if r.DMARC.Policy == "quarantine" || r.DMARC.Policy == "reject" {
				score += 15
			} else {
				// p=none — partial credit
				score += 5
				if r.DMARC.Suggestion != "" {
					suggestions = append(suggestions, "DMARC: "+r.DMARC.Suggestion)
				}
			}
		} else if r.DMARC.Suggestion != "" {
			suggestions = append(suggestions, "DMARC: "+r.DMARC.Suggestion)
		}
	} else {
		if r.DMARC.Suggestion != "" {
			suggestions = append(suggestions, "DMARC: "+r.DMARC.Suggestion)
		}
	}

	// rDNS — 10 pts
	if r.ReverseDNS.Found && r.ReverseDNS.Valid {
		score += 10
	} else if r.ReverseDNS.Found && !r.ReverseDNS.Valid {
		score += 3 // partial: PTR exists but forward-confirm failed
		if r.ReverseDNS.Suggestion != "" {
			suggestions = append(suggestions, "rDNS: "+r.ReverseDNS.Suggestion)
		}
	} else {
		if r.ReverseDNS.Suggestion != "" {
			suggestions = append(suggestions, "rDNS: "+r.ReverseDNS.Suggestion)
		}
	}

	if score > 100 {
		score = 100
	}
	return score, suggestions
}
