package security

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// blockedCIDRs lists address ranges that must never be contacted by
// user-supplied URLs (monitor targets, webhook destinations, etc.).
// This prevents SSRF attacks where an authenticated user could pivot into
// internal services (Stalwart admin, metadata APIs, loopback services …).
var blockedCIDRs []*net.IPNet

func init() {
	// RFC 1122 loopback
	// RFC 1918 private ranges
	// RFC 3927 link-local (AWS/GCP metadata: 169.254.169.254)
	// RFC 4193 IPv6 unique-local
	// IPv6 loopback
	// IPv6 link-local
	blocked := []string{
		"127.0.0.0/8",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"169.254.0.0/16",
		"100.64.0.0/10", // RFC 6598 carrier-grade NAT / Tailscale
		"0.0.0.0/8",     // RFC 1122 "this" network
		"::1/128",       // IPv6 loopback
		"fc00::/7",      // IPv6 unique-local
		"fe80::/10",     // IPv6 link-local
		"::ffff:0:0/96", // IPv4-mapped IPv6 (covers ::ffff:127.0.0.1 etc.)
	}
	for _, cidr := range blocked {
		_, network, err := net.ParseCIDR(cidr)
		if err == nil {
			blockedCIDRs = append(blockedCIDRs, network)
		}
	}
}

// isBlockedIP returns true if ip falls within any of the blocked CIDR ranges.
func isBlockedIP(ip net.IP) bool {
	// Plain IPv4: only match IPv4 CIDRs. Matching against ::ffff:0:0/96 would
	// incorrectly block every public IPv4 literal (Go maps v4 into that range).
	if v4 := ip.To4(); v4 != nil {
		for _, network := range blockedCIDRs {
			if len(network.IP) == net.IPv4len && network.Contains(v4) {
				return true
			}
		}
		return false
	}
	for _, network := range blockedCIDRs {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// ValidateOutboundURL checks whether rawURL is safe to contact as an
// outbound request target.  It rejects:
//   - non-http/https schemes
//   - hostnames that resolve to private / loopback / link-local IPs
//   - literal private IP addresses in the URL
//
// The function performs DNS resolution at validation time; it does NOT
// guarantee safety if DNS changes between validation and the actual request
// (TOCTOU).  Callers should also set a short request timeout.
func ValidateOutboundURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("ssrf: URL must not be empty")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("ssrf: invalid URL: %w", err)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("ssrf: scheme %q is not allowed (use http or https)", u.Scheme)
	}

	hostname := u.Hostname()
	if hostname == "" {
		return fmt.Errorf("ssrf: URL has no hostname")
	}

	// If the hostname is already a literal IP address, check it directly.
	if ip := net.ParseIP(hostname); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("ssrf: IP address %s is in a blocked range", hostname)
		}
		return nil
	}

	// Resolve the hostname and check every returned IP.
	addrs, err := net.LookupHost(hostname)
	if err != nil {
		// Treat unresolvable as unsafe — could be a split-horizon DNS attack.
		return fmt.Errorf("ssrf: cannot resolve hostname %q: %w", hostname, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("ssrf: hostname %q resolved to no addresses", hostname)
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		if isBlockedIP(ip) {
			return fmt.Errorf("ssrf: hostname %q resolves to blocked IP %s", hostname, addr)
		}
	}

	return nil
}

// ValidateWebhookURL is an alias for ValidateOutboundURL that adds a more
// descriptive error prefix suited for webhook configuration contexts.
func ValidateWebhookURL(rawURL string) error {
	if err := ValidateOutboundURL(rawURL); err != nil {
		return fmt.Errorf("webhook URL validation failed: %w", err)
	}
	return nil
}
