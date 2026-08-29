package bind

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const maximumDNSTTL = 2147483647

// NormalizeZoneDomain returns the canonical form used for newly managed BIND
// zones. Relative, reverse-DNS, and internal single-label zones are supported;
// wildcard and underscore labels are record names rather than zone identities.
func NormalizeZoneDomain(value string) (string, error) {
	return normalizeDNSName("zone domain", value, false, false, false)
}

// NormalizeLookupDomain validates one DNS lookup name. Underscore labels are
// accepted for SRV and other service-discovery records.
func NormalizeLookupDomain(value string) (string, error) {
	return normalizeDNSName("lookup domain", value, false, true, false)
}

// NormalizeRecordName validates a zone-relative or absolute owner name.
func NormalizeRecordName(value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || trimmed == "@" {
		return "@", nil
	}
	return normalizeDNSName("record name", trimmed, true, true, true)
}

// NormalizeRecordType canonicalizes one BIND record type. This intentionally
// does not freeze the product to a finite list of record types.
func NormalizeRecordType(value string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	if len(normalized) < 1 || len(normalized) > 16 {
		return "", errors.New("record type must contain 1 to 16 letters")
	}
	for _, character := range normalized {
		if character < 'A' || character > 'Z' {
			return "", errors.New("record type must contain only letters")
		}
	}
	return normalized, nil
}

// ValidateAndNormalizeCreateZone validates a local zone creation payload.
func ValidateAndNormalizeCreateZone(request CreateZoneRequest) (CreateZoneRequest, error) {
	var err error
	request.Domain, err = NormalizeZoneDomain(request.Domain)
	if err != nil {
		return request, err
	}
	ip := net.ParseIP(strings.TrimSpace(request.IP))
	if ip == nil || ip.To4() == nil {
		return request, errors.New("ip must be a valid IPv4 address")
	}
	request.IP = ip.To4().String()
	return request, nil
}

// ValidateAndNormalizeAddRecord validates one record append payload.
func ValidateAndNormalizeAddRecord(request AddRecordRequest) (AddRecordRequest, error) {
	var err error
	request.Name, err = NormalizeRecordName(request.Name)
	if err != nil {
		return request, err
	}
	request.Type, err = NormalizeRecordType(request.Type)
	if err != nil {
		return request, err
	}
	request.Value, err = normalizeRecordValue(request.Type, request.Value)
	if err != nil {
		return request, err
	}
	request.TTL, err = normalizeTTL(request.TTL, "3600")
	if err != nil {
		return request, err
	}
	request.Priority, err = normalizePriority(request.Type, request.Priority)
	return request, err
}

// ValidateAndNormalizeUpdateRecord validates one full record replacement.
func ValidateAndNormalizeUpdateRecord(request UpdateRecordRequest) (UpdateRecordRequest, error) {
	var err error
	request.Name, err = NormalizeRecordName(request.Name)
	if err != nil {
		return request, err
	}
	request.Type, err = NormalizeRecordType(request.Type)
	if err != nil {
		return request, err
	}
	request.OldValue, err = normalizeRecordValue(request.Type, request.OldValue)
	if err != nil {
		return request, fmt.Errorf("old value: %w", err)
	}
	request.NewValue, err = normalizeRecordValue(request.Type, request.NewValue)
	if err != nil {
		return request, fmt.Errorf("new value: %w", err)
	}
	request.NewTTL, err = normalizeTTL(request.NewTTL, "")
	if err != nil {
		return request, err
	}
	request.Priority, err = normalizePriority(request.Type, request.Priority)
	return request, err
}

// ValidateAndNormalizeDeleteRecord validates one exact record identity.
func ValidateAndNormalizeDeleteRecord(request DeleteRecordRequest) (DeleteRecordRequest, error) {
	var err error
	request.Name, err = NormalizeRecordName(request.Name)
	if err != nil {
		return request, err
	}
	request.Type, err = NormalizeRecordType(request.Type)
	if err != nil {
		return request, err
	}
	request.Value, err = normalizeRecordValue(request.Type, request.Value)
	return request, err
}

// ValidateAndNormalizeSOA validates the authoritative timing and DNS-name
// fields before a zone file is staged.
func ValidateAndNormalizeSOA(request UpdateSOARequest) (UpdateSOARequest, error) {
	var err error
	request.PrimaryNs, err = normalizeDNSName("primary nameserver", request.PrimaryNs, false, false, true)
	if err != nil {
		return request, err
	}
	request.Hostmaster, err = normalizeDNSName("hostmaster", request.Hostmaster, false, false, true)
	if err != nil {
		return request, err
	}
	for name, value := range map[string]uint32{
		"refresh": request.Refresh,
		"retry":   request.Retry,
		"expire":  request.Expire,
		"minimum": request.Minimum,
	} {
		if value > maximumDNSTTL {
			return request, fmt.Errorf("%s must be between 0 and %d seconds", name, maximumDNSTTL)
		}
	}
	return request, nil
}

func normalizeDNSName(field, value string, allowWildcard, allowUnderscore, preserveRootDot bool) (string, error) {
	trimmed := strings.TrimSpace(value)
	rooted := strings.HasSuffix(trimmed, ".")
	trimmed = strings.TrimSuffix(trimmed, ".")
	if trimmed == "" || len(trimmed) > 253 {
		return "", fmt.Errorf("%s must contain 1 to 253 characters", field)
	}
	labels := strings.Split(strings.ToLower(trimmed), ".")
	for index, label := range labels {
		if label == "" || len(label) > 63 {
			return "", fmt.Errorf("%s contains an invalid DNS label", field)
		}
		if label == "*" && allowWildcard && index == 0 {
			continue
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return "", fmt.Errorf("%s contains an invalid DNS label", field)
		}
		for _, character := range label {
			valid := character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-'
			if allowUnderscore && character == '_' {
				valid = true
			}
			if !valid {
				return "", fmt.Errorf("%s contains an invalid DNS label", field)
			}
		}
	}
	normalized := strings.Join(labels, ".")
	if preserveRootDot && rooted {
		normalized += "."
	}
	return normalized, nil
}

func normalizeTTL(value, defaultValue string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return defaultValue, nil
	}
	seconds, err := strconv.ParseUint(trimmed, 10, 31)
	if err != nil || seconds > maximumDNSTTL {
		return "", fmt.Errorf("TTL must be an integer between 0 and %d seconds", maximumDNSTTL)
	}
	return strconv.FormatUint(seconds, 10), nil
}

func normalizePriority(recordType string, value int) (int, error) {
	if value < 0 || value > 65535 {
		return 0, errors.New("record priority must be between 0 and 65535")
	}
	if recordType != "MX" && recordType != "SRV" && value != 0 {
		return 0, errors.New("record priority is supported only for MX and SRV records")
	}
	if (recordType == "MX" || recordType == "SRV") && value == 0 {
		return 10, nil
	}
	return value, nil
}

func normalizeRecordValue(recordType, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New("record value is required")
	}
	if !utf8.ValidString(trimmed) || utf8.RuneCountInString(trimmed) > 4096 {
		return "", errors.New("record value must contain at most 4096 valid UTF-8 characters")
	}
	for _, character := range trimmed {
		if unicode.IsControl(character) {
			return "", errors.New("record value must not contain control characters")
		}
	}
	switch recordType {
	case "A":
		ip := net.ParseIP(trimmed)
		if ip == nil || ip.To4() == nil {
			return "", errors.New("A record value must be a valid IPv4 address")
		}
		return ip.To4().String(), nil
	case "AAAA":
		ip := net.ParseIP(trimmed)
		if ip == nil || ip.To4() != nil {
			return "", errors.New("AAAA record value must be a valid IPv6 address")
		}
		return ip.String(), nil
	}
	return trimmed, nil
}
