package cloudflare

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	minimumRecordTTL = 30
	maximumRecordTTL = 86400
)

// NormalizeRecordType canonicalizes one provider record type without freezing
// the API to a finite type list that Cloudflare may extend.
func NormalizeRecordType(value string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(value))
	if len(normalized) < 1 || len(normalized) > 16 {
		return "", errors.New("DNS record type must contain 1 to 16 letters")
	}
	for _, character := range normalized {
		if character < 'A' || character > 'Z' {
			return "", errors.New("DNS record type must contain only letters")
		}
	}
	return normalized, nil
}

// NormalizeRecordName validates one exact provider DNS record-name filter.
func NormalizeRecordName(value string) (string, error) {
	return normalizeRecordText("DNS record name", value, 253)
}

// ValidateAndNormalizeRecordRequest enforces the common create and full-update
// payload contract before a request is sent to Cloudflare.
func ValidateAndNormalizeRecordRequest(request CreateRecordRequest) (CreateRecordRequest, error) {
	var err error
	request.Type, err = NormalizeRecordType(request.Type)
	if err != nil {
		return request, err
	}
	request.Name, err = NormalizeRecordName(request.Name)
	if err != nil {
		return request, err
	}
	request.Content, err = normalizeRecordText("DNS record content", request.Content, 4096)
	if err != nil {
		return request, err
	}
	if request.TTL != 1 && (request.TTL < minimumRecordTTL || request.TTL > maximumRecordTTL) {
		return request, fmt.Errorf("DNS record TTL must be 1 (automatic) or between %d and %d seconds", minimumRecordTTL, maximumRecordTTL)
	}
	if request.Priority < 0 || request.Priority > 65535 {
		return request, errors.New("DNS record priority must be between 0 and 65535")
	}
	if request.Proxied && request.Type != "A" && request.Type != "AAAA" && request.Type != "CNAME" {
		return request, errors.New("DNS proxy can be enabled only for A, AAAA, or CNAME records")
	}
	return request, nil
}

// NormalizeDomain returns a lower-case DNS name without a trailing root dot.
func NormalizeDomain(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	if normalized == "" || len(normalized) > 253 || !strings.Contains(normalized, ".") {
		return "", errors.New("domain must be a DNS name containing at least one dot")
	}
	for _, label := range strings.Split(normalized, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", errors.New("domain contains an invalid DNS label")
		}
		for _, character := range label {
			if !((character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || character == '-') {
				return "", errors.New("domain contains an invalid DNS label")
			}
		}
	}
	return normalized, nil
}

func normalizeRecordText(name, value string, maximum int) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("%s is required", name)
	}
	if !utf8.ValidString(trimmed) || utf8.RuneCountInString(trimmed) > maximum {
		return "", fmt.Errorf("%s must contain at most %d valid UTF-8 characters", name, maximum)
	}
	for _, character := range trimmed {
		if unicode.IsControl(character) {
			return "", fmt.Errorf("%s must not contain control characters", name)
		}
	}
	return trimmed, nil
}
