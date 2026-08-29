package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/IamYGT/heyserver/internal/services/security"
	"github.com/IamYGT/heyserver/internal/services/settings"
	"github.com/IamYGT/heyserver/internal/store"
)

const uptimeMonitorRequestBodyLimit = 128 << 10

// ── Helpers ──────────────────────────────────────────────────────────────────

// parsePeriodHours converts a period string or raw hour string to an int.
// Supports: "24h"→24, "7d"→168, "30d"→720, "90d"→2160, or plain integers.
func parsePeriodHours(s string, defaultVal int) int {
	if s == "" {
		return defaultVal
	}
	switch s {
	case "24h":
		return 24
	case "7d":
		return 168
	case "30d":
		return 720
	case "90d":
		return 2160
	}
	if n, err := strconv.Atoi(s); err == nil && n > 0 {
		return n
	}
	return defaultVal
}

// validateMonitorTarget performs SSRF validation for a monitor's target
// URL (http type) or hostname (tcp/ping/dns types) before persisting or running.
func validateMonitorTarget(m *store.UptimeMonitor) error {
	switch m.Type {
	case "http":
		if m.URL == "" {
			return fmt.Errorf("url is required for http monitors")
		}
		if err := security.ValidateOutboundURL(m.URL); err != nil {
			return fmt.Errorf("monitor URL rejected: %w", err)
		}
	case "tcp", "ping", "dns":
		if m.Hostname == "" {
			return fmt.Errorf("hostname is required for %s monitors", m.Type)
		}
		if m.Type == "tcp" && (m.Port < 1 || m.Port > 65535) {
			return fmt.Errorf("port must be between 1 and 65535 for tcp monitors")
		}
		// Synthesise a URL so we can reuse the shared validator.
		synthetic := fmt.Sprintf("http://%s", net.JoinHostPort(m.Hostname, "80"))
		if err := security.ValidateOutboundURL(synthetic); err != nil {
			return fmt.Errorf("monitor hostname rejected: %w", err)
		}
	default:
		return fmt.Errorf("unsupported monitor type: %s", m.Type)
	}
	return nil
}

func applyUptimeMonitorDefaults(m *store.UptimeMonitor, settingsServices ...*settings.Service) {
	defaultInterval, defaultTimeout := 60, 30
	if len(settingsServices) > 0 && settingsServices[0] != nil {
		defaultInterval = readUptimeMonitorDefault(settingsServices[0], "uptime_default_interval", defaultInterval, 10, 86400)
		defaultTimeout = readUptimeMonitorDefault(settingsServices[0], "uptime_default_timeout", defaultTimeout, 1, 300)
		if defaultTimeout > defaultInterval {
			defaultInterval, defaultTimeout = 60, 30
		}
	}
	if m.Method == "" {
		m.Method = "GET"
	}
	if m.IntervalSecs <= 0 {
		m.IntervalSecs = defaultInterval
	}
	if m.TimeoutSecs <= 0 {
		m.TimeoutSecs = defaultTimeout
	}
	if m.Retries <= 0 {
		m.Retries = 1
	}
	if m.RetryInterval <= 0 {
		m.RetryInterval = 30
	}
	if m.AcceptedStatusCodes == "" {
		m.AcceptedStatusCodes = `["200-299"]`
	}
	if m.MaxRedirects <= 0 {
		m.MaxRedirects = 5
	}
	if m.TLSExpiryWarnDays <= 0 {
		m.TLSExpiryWarnDays = 14
	}
}

func readUptimeMonitorDefault(svc *settings.Service, key string, fallback, minimum, maximum int) int {
	value, err := svc.Get(key, strconv.Itoa(fallback))
	if err != nil {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < minimum || parsed > maximum {
		return fallback
	}
	return parsed
}

func normalizeUptimeMonitor(m *store.UptimeMonitor) error {
	name, err := boundedUptimeAPIText("monitor name", m.Name, 128, true)
	if err != nil {
		return err
	}
	m.Name = name
	m.Type = strings.ToLower(strings.TrimSpace(m.Type))
	m.URL = strings.TrimSpace(m.URL)
	m.Hostname = strings.TrimSpace(m.Hostname)
	if err := validateMonitorTarget(m); err != nil {
		return err
	}
	if m.IntervalSecs < 10 || m.IntervalSecs > 86400 {
		return errors.New("monitor interval_secs must be between 10 and 86400")
	}
	if m.TimeoutSecs < 1 || m.TimeoutSecs > 300 {
		return errors.New("monitor timeout_secs must be between 1 and 300")
	}
	if m.Retries < 1 || m.Retries > 10 {
		return errors.New("monitor retries must be between 1 and 10")
	}
	if m.RetryInterval < 1 || m.RetryInterval > 3600 {
		return errors.New("monitor retry_interval must be between 1 and 3600")
	}
	if m.AlertReminderMins < 0 || m.AlertReminderMins > 10080 {
		return errors.New("monitor alert_reminder_mins must be between 0 and 10080")
	}
	description, err := boundedUptimeAPIText("monitor description", m.Description, 2048, false)
	if err != nil {
		return err
	}
	m.Description = description
	if m.GroupID != nil && *m.GroupID <= 0 {
		return errors.New("monitor group_id must be a positive integer")
	}
	channelIDs, err := normalizeUptimeChannelIDs(m.AlertChannelIDs)
	if err != nil {
		return err
	}
	m.AlertChannelIDs = channelIDs

	if m.Type == "http" {
		m.Method = strings.ToUpper(strings.TrimSpace(m.Method))
		switch m.Method {
		case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		default:
			return errors.New("monitor method must be GET, HEAD, POST, PUT, PATCH, DELETE, or OPTIONS")
		}
		accepted, err := normalizeUptimeAcceptedStatusCodes(m.AcceptedStatusCodes)
		if err != nil {
			return err
		}
		m.AcceptedStatusCodes = accepted
		if m.MaxRedirects < 1 || m.MaxRedirects > 20 {
			return errors.New("monitor max_redirects must be between 1 and 20")
		}
		if m.TLSExpiryWarnDays < 1 || m.TLSExpiryWarnDays > 365 {
			return errors.New("monitor tls_expiry_warn_days must be between 1 and 365")
		}
		keyword, err := boundedUptimeAPIText("monitor keyword", m.Keyword, 512, false)
		if err != nil {
			return err
		}
		m.Keyword = keyword
		if !utf8.ValidString(m.ReqHeaders) || len(m.ReqHeaders) > 32<<10 || strings.ContainsAny(m.ReqHeaders, "\r\x00") {
			return errors.New("monitor req_headers must be valid UTF-8 of at most 32768 bytes without CR or NUL")
		}
		if !utf8.ValidString(m.ReqBody) || len(m.ReqBody) > 64<<10 || strings.ContainsRune(m.ReqBody, '\x00') {
			return errors.New("monitor req_body must be valid UTF-8 of at most 65536 bytes without NUL")
		}
		m.Hostname = ""
		m.Port = 0
		return nil
	}

	m.URL = ""
	m.Method = "GET"
	m.AcceptedStatusCodes = `["200-299"]`
	m.KeywordInvert = false
	m.ReqHeaders = ""
	m.TLSCheck = false
	m.TLSExpiryWarnDays = 14
	m.MaxRedirects = 5
	if m.Type == "tcp" {
		m.Keyword = ""
		m.ReqBody = ""
		return nil
	}
	m.Port = 0
	if m.Type == "ping" {
		m.Keyword = ""
		m.ReqBody = ""
		return nil
	}
	recordType := strings.ToUpper(strings.TrimSpace(m.Keyword))
	if recordType == "" {
		recordType = "A"
	}
	switch recordType {
	case "A", "AAAA", "MX", "CNAME":
	default:
		return errors.New("DNS monitor record type must be A, AAAA, MX, or CNAME")
	}
	expected, err := boundedUptimeAPIText("DNS monitor expected value", m.ReqBody, 253, false)
	if err != nil {
		return err
	}
	m.Keyword = recordType
	m.ReqBody = expected
	return nil
}

func normalizeUptimeChannelIDs(value store.ChannelIDs) (store.ChannelIDs, error) {
	if value == "" {
		return store.ChannelIDs("[]"), nil
	}
	var ids []int64
	if err := json.Unmarshal([]byte(value), &ids); err != nil {
		return "", errors.New("monitor alert_channel_ids must be an array of positive integers")
	}
	if len(ids) > 128 {
		return "", errors.New("monitor accepts at most 128 alert_channel_ids")
	}
	seen := make(map[int64]struct{}, len(ids))
	normalized := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return "", errors.New("monitor alert_channel_ids must contain only positive integers")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		normalized = append(normalized, id)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return store.ChannelIDs(encoded), nil
}

func normalizeUptimeAcceptedStatusCodes(value string) (string, error) {
	var raw []string
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
			return "", errors.New("monitor accepted_statuscodes must be a comma list or JSON string array")
		}
	} else {
		raw = strings.Split(trimmed, ",")
	}
	if len(raw) == 0 || len(raw) > 32 {
		return "", errors.New("monitor accepted_statuscodes must contain between 1 and 32 entries")
	}
	normalized := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, candidate := range raw {
		entry := strings.TrimSpace(candidate)
		if entry == "" {
			return "", errors.New("monitor accepted_statuscodes contains an empty entry")
		}
		if strings.Contains(entry, "-") {
			bounds := strings.Split(entry, "-")
			if len(bounds) != 2 {
				return "", fmt.Errorf("invalid monitor status range %q", entry)
			}
			low, lowErr := strconv.Atoi(bounds[0])
			high, highErr := strconv.Atoi(bounds[1])
			if lowErr != nil || highErr != nil || low < 100 || high > 599 || low > high {
				return "", fmt.Errorf("invalid monitor status range %q", entry)
			}
			entry = strconv.Itoa(low) + "-" + strconv.Itoa(high)
		} else {
			code, err := strconv.Atoi(entry)
			if err != nil || code < 100 || code > 599 {
				return "", fmt.Errorf("invalid monitor status code %q", entry)
			}
			entry = strconv.Itoa(code)
		}
		if _, exists := seen[entry]; exists {
			continue
		}
		seen[entry] = struct{}{}
		normalized = append(normalized, entry)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func boundedUptimeAPIText(label, value string, maxBytes int, required bool) (string, error) {
	clean := strings.TrimSpace(value)
	if required && clean == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	if !utf8.ValidString(clean) || len(clean) > maxBytes {
		return "", fmt.Errorf("%s must be valid UTF-8 of at most %d bytes", label, maxBytes)
	}
	for _, character := range clean {
		if unicode.IsControl(character) && character != '\t' {
			return "", fmt.Errorf("%s contains unsupported control characters", label)
		}
	}
	return clean, nil
}
