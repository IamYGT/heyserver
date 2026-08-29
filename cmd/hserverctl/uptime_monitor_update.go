package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"unicode"
	"unicode/utf8"
)

// uptimeMonitorUpdateOptions contains only values that the operator may change
// through the partial monitor PUT contract. Every field is accompanied by the
// FlagSet's visited map so false, zero, and empty string values remain
// distinguishable from omitted options.
type uptimeMonitorUpdateOptions struct {
	Confirm             bool
	Name                string
	Type                string
	URL                 string
	Hostname            string
	Port                int
	Method              string
	IntervalSecs        int
	TimeoutSecs         int
	Retries             int
	RetryInterval       int
	AcceptedStatusCodes string
	Keyword             string
	KeywordInvert       bool
	ReqHeaders          string
	ReqHeadersFile      string
	ReqBody             string
	ReqBodyFile         string
	TLSCheck            bool
	TLSExpiryWarnDays   int
	DNSRecordType       string
	DNSExpectedValue    string
	Description         string
	MaxRedirects        int
	AlertReminderMins   int
	AlertChannelIDs     stringValues
	ClearAlertChannels  bool
	ClearURL            bool
	ClearHostname       bool
	ClearKeyword        bool
	ClearReqHeaders     bool
	ClearReqBody        bool
	ClearDescription    bool
	visited             map[string]bool
}

// runUptimeMonitorUpdate applies one explicitly selected set of monitor fields.
// It deliberately sends no preliminary GET: the API owns the partial-update
// merge and validates the resulting monitor atomically.
func runUptimeMonitorUpdate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags, options := newUptimeMonitorUpdateFlags()
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl uptime monitor update --confirm [OPTIONS] ID")
	}
	options.visited = make(map[string]bool)
	flags.Visit(func(item *flag.Flag) { options.visited[item.Name] = true })
	normalizeUptimeMonitorUpdateVisited(options.visited)
	if !options.Confirm {
		return errors.New("uptime monitor update requires explicit --confirm")
	}
	if !uptimeMonitorUpdateHasChange(options.visited) {
		return errors.New("uptime monitor update requires at least one changed field")
	}
	id, err := positiveUptimeID("monitor", flags.Args()[0])
	if err != nil {
		return err
	}
	payload, err := buildUptimeMonitorUpdatePayload(options)
	if err != nil {
		return err
	}
	return printRequest(ctx, client, out, http.MethodPut, uptimeMonitorEndpoint(id), payload, true)
}

func newUptimeMonitorUpdateFlags() (*flag.FlagSet, *uptimeMonitorUpdateOptions) {
	flags := flag.NewFlagSet("uptime monitor update", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := &uptimeMonitorUpdateOptions{}
	flags.BoolVar(&options.Confirm, "confirm", false, "confirm uptime monitor update")
	flags.StringVar(&options.Name, "name", "", "replacement monitor display name")
	flags.StringVar(&options.Type, "type", "", "replacement monitor type: http, tcp, ping, or dns")
	flags.StringVar(&options.URL, "url", "", "replacement HTTP or HTTPS target URL; an empty value clears it")
	flags.StringVar(&options.Hostname, "hostname", "", "replacement TCP, ping, or DNS hostname; an empty value clears it")
	flags.IntVar(&options.Port, "port", 0, "replacement TCP target port")
	flags.StringVar(&options.Method, "method", "", "replacement HTTP method")
	flags.IntVar(&options.IntervalSecs, "interval-secs", 0, "replacement seconds between checks")
	flags.IntVar(&options.TimeoutSecs, "timeout-secs", 0, "replacement per-check timeout in seconds")
	flags.IntVar(&options.Retries, "retries", 0, "replacement consecutive failure count")
	flags.IntVar(&options.RetryInterval, "retry-interval-secs", 0, "replacement seconds between retry checks")
	flags.StringVar(&options.AcceptedStatusCodes, "accepted-statuscodes", "", "replacement comma-separated HTTP codes or ranges")
	flags.StringVar(&options.Keyword, "keyword", "", "replacement HTTP response keyword; an empty value clears it")
	flags.BoolVar(&options.KeywordInvert, "keyword-invert", false, "replacement HTTP keyword absence requirement")
	flags.StringVar(&options.ReqHeaders, "req-headers", "", "replacement HTTP request headers, one Header: Value per line")
	flags.StringVar(&options.ReqHeadersFile, "req-headers-file", "", "protected file containing replacement HTTP request headers")
	flags.StringVar(&options.ReqBody, "req-body", "", "replacement HTTP request body; an empty value clears it")
	flags.StringVar(&options.ReqBodyFile, "req-body-file", "", "protected file containing replacement HTTP request body")
	// Keep short aliases for operators who refer to the HTTP concepts rather
	// than the persisted req_* field names. They normalize to the canonical
	// visited keys before payload construction.
	flags.StringVar(&options.ReqHeaders, "headers", "", "alias for --req-headers")
	flags.StringVar(&options.ReqHeadersFile, "headers-file", "", "alias for --req-headers-file")
	flags.StringVar(&options.ReqBody, "body", "", "alias for --req-body")
	flags.StringVar(&options.ReqBodyFile, "body-file", "", "alias for --req-body-file")
	flags.BoolVar(&options.TLSCheck, "tls-check", false, "replacement HTTP TLS certificate verification state")
	flags.IntVar(&options.TLSExpiryWarnDays, "tls-expiry-warn-days", 0, "replacement TLS expiry warning threshold in days")
	flags.StringVar(&options.DNSRecordType, "dns-record-type", "", "replacement DNS record type: A, AAAA, MX, or CNAME")
	flags.StringVar(&options.DNSExpectedValue, "dns-expected", "", "replacement exact DNS record value; an empty value clears it")
	flags.StringVar(&options.Description, "description", "", "replacement monitor description; an empty value clears it")
	flags.IntVar(&options.MaxRedirects, "max-redirects", 0, "replacement maximum HTTP redirects")
	flags.IntVar(&options.AlertReminderMins, "alert-reminder-mins", 0, "replacement minutes between repeated down alerts")
	flags.Var(&options.AlertChannelIDs, "alert-channel", "replacement notification channel ID; repeatable")
	flags.Var(&options.AlertChannelIDs, "alert-channel-id", "alias for --alert-channel; repeatable")
	flags.BoolVar(&options.ClearAlertChannels, "clear-alert-channels", false, "clear all monitor-specific notification channels")
	flags.BoolVar(&options.ClearAlertChannels, "clear-alert-channel", false, "alias for --clear-alert-channels")
	flags.BoolVar(&options.ClearURL, "clear-url", false, "clear the stored monitor URL")
	flags.BoolVar(&options.ClearHostname, "clear-hostname", false, "clear the stored monitor hostname")
	flags.BoolVar(&options.ClearKeyword, "clear-keyword", false, "clear the stored HTTP keyword")
	flags.BoolVar(&options.ClearReqHeaders, "clear-req-headers", false, "clear stored HTTP request headers")
	flags.BoolVar(&options.ClearReqHeaders, "clear-headers", false, "alias for --clear-req-headers")
	flags.BoolVar(&options.ClearReqBody, "clear-req-body", false, "clear stored HTTP request body")
	flags.BoolVar(&options.ClearReqBody, "clear-body", false, "alias for --clear-req-body")
	flags.BoolVar(&options.ClearDescription, "clear-description", false, "clear the stored monitor description")
	return flags, options
}

func normalizeUptimeMonitorUpdateVisited(visited map[string]bool) {
	for alias, canonical := range map[string]string{
		"headers":             "req-headers",
		"headers-file":        "req-headers-file",
		"body":                "req-body",
		"body-file":           "req-body-file",
		"clear-headers":       "clear-req-headers",
		"clear-body":          "clear-req-body",
		"alert-channel-id":    "alert-channel",
		"clear-alert-channel": "clear-alert-channels",
	} {
		if visited[alias] {
			visited[canonical] = true
		}
	}
}

func uptimeMonitorUpdateHasChange(visited map[string]bool) bool {
	for name, selected := range visited {
		if selected && name != "confirm" {
			return true
		}
	}
	return false
}

func buildUptimeMonitorUpdatePayload(options *uptimeMonitorUpdateOptions) (map[string]any, error) {
	visited := options.visited
	payload := make(map[string]any)

	if visited["name"] {
		name, err := boundedUptimeText("monitor name", options.Name, 128, true)
		if err != nil {
			return nil, err
		}
		payload["name"] = name
	}

	monitorType := ""
	if visited["type"] {
		monitorType = strings.ToLower(strings.TrimSpace(options.Type))
		switch monitorType {
		case "http", "tcp", "ping", "dns":
		default:
			return nil, errors.New("uptime monitor type must be http, tcp, ping, or dns")
		}
		payload["type"] = monitorType
	}

	if visited["url"] {
		value := strings.TrimSpace(options.URL)
		if value == "" {
			// An explicit empty string is a supported clear operation. The API
			// still decides whether the resulting monitor remains valid.
			payload["url"] = ""
		} else {
			normalized, err := validateUptimeHTTPURL(value)
			if err != nil {
				return nil, err
			}
			payload["url"] = normalized
		}
	}
	if visited["clear-url"] {
		if !options.ClearURL {
			return nil, errors.New("--clear-url must be enabled to clear the monitor URL")
		}
		if visited["url"] {
			return nil, errors.New("--url and --clear-url cannot be combined")
		}
		payload["url"] = ""
	}

	if visited["hostname"] {
		value := strings.TrimSpace(options.Hostname)
		if value == "" {
			payload["hostname"] = ""
		} else {
			normalized, err := validateUptimeHostname(value)
			if err != nil {
				return nil, err
			}
			payload["hostname"] = normalized
		}
	}
	if visited["clear-hostname"] {
		if !options.ClearHostname {
			return nil, errors.New("--clear-hostname must be enabled to clear the monitor hostname")
		}
		if visited["hostname"] {
			return nil, errors.New("--hostname and --clear-hostname cannot be combined")
		}
		payload["hostname"] = ""
	}

	if visited["port"] {
		if options.Port < 1 || options.Port > 65535 {
			return nil, errors.New("uptime TCP monitor port must be between 1 and 65535")
		}
		payload["port"] = options.Port
	}
	if visited["method"] {
		method := strings.ToUpper(strings.TrimSpace(options.Method))
		switch method {
		case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		default:
			return nil, errors.New("uptime HTTP method must be GET, HEAD, POST, PUT, PATCH, DELETE, or OPTIONS")
		}
		payload["method"] = method
	}
	if visited["interval-secs"] {
		if options.IntervalSecs < 10 || options.IntervalSecs > 86400 {
			return nil, errors.New("uptime monitor interval must be between 10 and 86400 seconds")
		}
		payload["interval_secs"] = options.IntervalSecs
	}
	if visited["timeout-secs"] {
		if options.TimeoutSecs < 1 || options.TimeoutSecs > 300 {
			return nil, errors.New("uptime monitor timeout must be between 1 and 300 seconds")
		}
		payload["timeout_secs"] = options.TimeoutSecs
	}
	if visited["retries"] {
		if options.Retries < 1 || options.Retries > 10 {
			return nil, errors.New("uptime monitor retries must be between 1 and 10")
		}
		payload["retries"] = options.Retries
	}
	if visited["retry-interval-secs"] {
		if options.RetryInterval < 1 || options.RetryInterval > 3600 {
			return nil, errors.New("uptime monitor retry interval must be between 1 and 3600 seconds")
		}
		payload["retry_interval"] = options.RetryInterval
	}
	if visited["accepted-statuscodes"] {
		accepted, err := normalizeUptimeStatusCodes(options.AcceptedStatusCodes)
		if err != nil {
			return nil, err
		}
		payload["accepted_statuscodes"] = accepted
	}
	if visited["keyword"] {
		if visited["clear-keyword"] {
			return nil, errors.New("--keyword and --clear-keyword cannot be combined")
		}
		keyword, err := boundedUptimeText("monitor keyword", options.Keyword, 512, false)
		if err != nil {
			return nil, err
		}
		payload["keyword"] = keyword
	}
	if visited["clear-keyword"] {
		if !options.ClearKeyword {
			return nil, errors.New("--clear-keyword must be enabled to clear the monitor keyword")
		}
		payload["keyword"] = ""
	}
	if visited["keyword-invert"] {
		payload["keyword_invert"] = options.KeywordInvert
	}

	if visited["req-headers"] && (visited["req-headers-file"] || visited["clear-req-headers"]) {
		return nil, errors.New("--req-headers, --req-headers-file, and --clear-req-headers cannot be combined")
	}
	if visited["req-headers-file"] && visited["clear-req-headers"] {
		return nil, errors.New("--req-headers-file and --clear-req-headers cannot be combined")
	}
	if visited["req-headers"] {
		value, err := validateUptimeRequestHeaders(options.ReqHeaders)
		if err != nil {
			return nil, err
		}
		payload["req_headers"] = value
	}
	if visited["req-headers-file"] {
		value, err := readUptimeMonitorInputFile(options.ReqHeadersFile, "request headers", 32<<10)
		if err != nil {
			return nil, err
		}
		value, err = validateUptimeRequestHeaders(value)
		if err != nil {
			return nil, err
		}
		payload["req_headers"] = value
	}
	if visited["clear-req-headers"] {
		if !options.ClearReqHeaders {
			return nil, errors.New("--clear-req-headers must be enabled to clear request headers")
		}
		payload["req_headers"] = ""
	}

	if visited["req-body"] && (visited["req-body-file"] || visited["clear-req-body"]) {
		return nil, errors.New("--req-body, --req-body-file, and --clear-req-body cannot be combined")
	}
	if visited["req-body-file"] && visited["clear-req-body"] {
		return nil, errors.New("--req-body-file and --clear-req-body cannot be combined")
	}
	if visited["req-body"] {
		value, err := validateUptimeRequestBody(options.ReqBody)
		if err != nil {
			return nil, err
		}
		payload["req_body"] = value
	}
	if visited["req-body-file"] {
		value, err := readUptimeMonitorInputFile(options.ReqBodyFile, "request body", 64<<10)
		if err != nil {
			return nil, err
		}
		value, err = validateUptimeRequestBody(value)
		if err != nil {
			return nil, err
		}
		payload["req_body"] = value
	}
	if visited["clear-req-body"] {
		if !options.ClearReqBody {
			return nil, errors.New("--clear-req-body must be enabled to clear request body")
		}
		payload["req_body"] = ""
	}

	if visited["tls-check"] {
		payload["tls_check"] = options.TLSCheck
	}
	if visited["tls-expiry-warn-days"] {
		if options.TLSExpiryWarnDays < 1 || options.TLSExpiryWarnDays > 365 {
			return nil, errors.New("uptime monitor TLS warning must be between 1 and 365 days")
		}
		payload["tls_expiry_warn_days"] = options.TLSExpiryWarnDays
	}
	if visited["max-redirects"] {
		if options.MaxRedirects < 1 || options.MaxRedirects > 20 {
			return nil, errors.New("uptime monitor redirects must be between 1 and 20")
		}
		payload["max_redirects"] = options.MaxRedirects
	}

	if visited["dns-record-type"] {
		if visited["keyword"] || visited["clear-keyword"] {
			return nil, errors.New("--dns-record-type cannot be combined with --keyword or --clear-keyword")
		}
		recordType := strings.ToUpper(strings.TrimSpace(options.DNSRecordType))
		switch recordType {
		case "A", "AAAA", "MX", "CNAME":
		default:
			return nil, errors.New("uptime DNS record type must be A, AAAA, MX, or CNAME")
		}
		payload["keyword"] = recordType
	}
	if visited["dns-expected"] {
		if visited["req-body"] || visited["req-body-file"] || visited["clear-req-body"] {
			return nil, errors.New("--dns-expected cannot be combined with HTTP request-body options")
		}
		expected, err := boundedUptimeText("expected DNS value", options.DNSExpectedValue, 253, false)
		if err != nil {
			return nil, err
		}
		payload["req_body"] = expected
	}

	if visited["description"] {
		if visited["clear-description"] {
			return nil, errors.New("--description and --clear-description cannot be combined")
		}
		description, err := boundedUptimeText("monitor description", options.Description, 2048, false)
		if err != nil {
			return nil, err
		}
		payload["description"] = description
	}
	if visited["clear-description"] {
		if !options.ClearDescription {
			return nil, errors.New("--clear-description must be enabled to clear the monitor description")
		}
		payload["description"] = ""
	}
	if visited["alert-reminder-mins"] {
		if options.AlertReminderMins < 0 || options.AlertReminderMins > 10080 {
			return nil, errors.New("uptime monitor alert reminder must be between 0 and 10080 minutes")
		}
		payload["alert_reminder_mins"] = options.AlertReminderMins
	}
	if visited["alert-channel"] && visited["clear-alert-channels"] {
		return nil, errors.New("--alert-channel and --clear-alert-channels cannot be combined")
	}
	if visited["alert-channel"] {
		channels, err := parseUptimeChannelIDs(options.AlertChannelIDs)
		if err != nil {
			return nil, err
		}
		payload["alert_channel_ids"] = channels
	}
	if visited["clear-alert-channels"] {
		if !options.ClearAlertChannels {
			return nil, errors.New("--clear-alert-channels must be enabled to clear alert channels")
		}
		payload["alert_channel_ids"] = []int64{}
	}

	if err := validateUptimeMonitorUpdateCompatibility(options, monitorType); err != nil {
		return nil, err
	}
	return payload, nil
}

func validateUptimeMonitorUpdateCompatibility(options *uptimeMonitorUpdateOptions, monitorType string) error {
	visited := options.visited
	if monitorType == "" {
		// Without a type replacement, the API has the existing type available for
		// its merge validation. We can still reject an unambiguous mixed target.
		if visited["url"] && strings.TrimSpace(options.URL) != "" && visited["hostname"] && strings.TrimSpace(options.Hostname) != "" {
			return errors.New("uptime monitor update cannot set both URL and hostname")
		}
		return nil
	}

	httpOnly := []string{"method", "accepted-statuscodes", "keyword", "clear-keyword", "keyword-invert", "req-headers", "req-headers-file", "clear-req-headers", "req-body", "req-body-file", "clear-req-body", "tls-check", "tls-expiry-warn-days", "max-redirects"}
	for _, name := range httpOnly {
		if visited[name] && !uptimeMonitorUpdateOptionIsEmptyClear(options, name) && monitorType != "http" {
			return fmt.Errorf("--%s is only valid for HTTP monitors", name)
		}
	}
	if monitorType == "http" {
		if visited["hostname"] && strings.TrimSpace(options.Hostname) != "" {
			return errors.New("--hostname is not valid for HTTP monitors")
		}
		if visited["port"] {
			return errors.New("--port is not valid for HTTP monitors")
		}
		if visited["dns-record-type"] || visited["dns-expected"] {
			return errors.New("DNS options are not valid for HTTP monitors")
		}
		return nil
	}

	if visited["url"] && strings.TrimSpace(options.URL) != "" {
		return fmt.Errorf("--url is only valid for HTTP monitors")
	}
	if visited["clear-url"] && options.ClearURL {
		// Clearing an obsolete URL is harmless during a type transition.
	}
	if visited["req-headers"] || visited["req-headers-file"] || visited["clear-req-headers"] {
		if !uptimeMonitorUpdateOptionIsEmptyClear(options, "req-headers") && !uptimeMonitorUpdateOptionIsEmptyClear(options, "req-headers-file") && !uptimeMonitorUpdateOptionIsEmptyClear(options, "clear-req-headers") {
			return fmt.Errorf("--req-headers options are only valid for HTTP monitors")
		}
	}
	if visited["req-body"] || visited["req-body-file"] || visited["clear-req-body"] {
		if !uptimeMonitorUpdateOptionIsEmptyClear(options, "req-body") && !uptimeMonitorUpdateOptionIsEmptyClear(options, "req-body-file") && !uptimeMonitorUpdateOptionIsEmptyClear(options, "clear-req-body") {
			return fmt.Errorf("--req-body options are only valid for HTTP monitors")
		}
	}
	if visited["dns-record-type"] || visited["dns-expected"] {
		if monitorType != "dns" {
			return errors.New("DNS options are only valid for DNS monitors")
		}
	}
	if monitorType == "tcp" {
		if visited["keyword"] || visited["clear-keyword"] {
			return errors.New("--keyword is not valid for TCP monitors; use --dns-record-type for DNS monitors")
		}
		if visited["dns-record-type"] || visited["dns-expected"] {
			return errors.New("DNS options are not valid for TCP monitors")
		}
	}
	if monitorType == "ping" {
		if visited["port"] {
			return errors.New("--port is only valid for TCP monitors")
		}
		if visited["keyword"] || visited["clear-keyword"] || visited["dns-record-type"] || visited["dns-expected"] {
			return errors.New("keyword and DNS options are not valid for ping monitors")
		}
	}
	if monitorType == "dns" {
		if visited["port"] {
			return errors.New("--port is only valid for TCP monitors")
		}
		if visited["keyword"] || visited["clear-keyword"] {
			return errors.New("--keyword is only valid for HTTP monitors; use --dns-record-type for DNS monitors")
		}
	}
	return nil
}

func uptimeMonitorUpdateOptionIsEmptyClear(options *uptimeMonitorUpdateOptions, name string) bool {
	switch name {
	case "url":
		return options.visited["url"] && strings.TrimSpace(options.URL) == ""
	case "hostname":
		return options.visited["hostname"] && strings.TrimSpace(options.Hostname) == ""
	case "req-headers":
		return options.visited["req-headers"] && options.ReqHeaders == ""
	case "req-headers-file":
		return false
	case "clear-req-headers":
		return options.visited["clear-req-headers"] && options.ClearReqHeaders
	case "req-body":
		return options.visited["req-body"] && options.ReqBody == ""
	case "req-body-file":
		return false
	case "clear-req-body":
		return options.visited["clear-req-body"] && options.ClearReqBody
	case "keyword", "clear-keyword":
		return (options.visited["keyword"] && options.Keyword == "") || (options.visited["clear-keyword"] && options.ClearKeyword)
	default:
		return false
	}
}

func validateUptimeRequestHeaders(value string) (string, error) {
	if !utf8.ValidString(value) || len(value) > 32<<10 || strings.ContainsAny(value, "\r\x00") {
		return "", errors.New("uptime monitor request headers must be valid UTF-8 of at most 32768 bytes without CR or NUL")
	}
	trimmed := strings.TrimSpace(value)
	if strings.HasPrefix(trimmed, "{") {
		var object map[string]json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &object); err != nil || object == nil {
			return "", errors.New("uptime monitor request headers must be a JSON object or contain Header: Value lines")
		}
		for name, rawValue := range object {
			if !validUptimeHeaderName(name) {
				return "", errors.New("uptime monitor request headers contain an invalid header name")
			}
			var headerValue *string
			if err := json.Unmarshal(rawValue, &headerValue); err != nil || headerValue == nil {
				return "", errors.New("uptime monitor JSON request header values must be strings")
			}
			if _, err := validateUptimeHeaderValue(*headerValue); err != nil {
				return "", err
			}
		}
		return value, nil
	}
	for _, line := range strings.Split(value, "\n") {
		if line == "" {
			continue
		}
		name, headerValue, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "" || !validUptimeHeaderName(name) {
			return "", errors.New("uptime monitor request headers must contain Header: Value lines")
		}
		if _, err := validateUptimeHeaderValue(headerValue); err != nil {
			return "", err
		}
	}
	return value, nil
}

func validateUptimeHeaderValue(value string) (string, error) {
	if strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("uptime monitor request headers contain an invalid control character")
	}
	for _, character := range value {
		if unicode.IsControl(character) && character != '\t' {
			return "", errors.New("uptime monitor request headers contain an invalid control character")
		}
	}
	return value, nil
}

func validUptimeHeaderName(value string) bool {
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' {
			continue
		}
		switch character {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		default:
			return false
		}
	}
	return true
}

func validateUptimeRequestBody(value string) (string, error) {
	if !utf8.ValidString(value) || len(value) > 64<<10 || strings.ContainsRune(value, '\x00') {
		return "", errors.New("uptime monitor request body must be valid UTF-8 of at most 65536 bytes without NUL")
	}
	return value, nil
}

func readUptimeMonitorInputFile(path, label string, maxBytes int64) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", fmt.Errorf("uptime monitor %s file path must not be empty", label)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("read uptime monitor %s file: %w", label, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("uptime monitor %s file must be a regular file and not a symlink", label)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("uptime monitor %s file must not be accessible by group or others", label)
	}
	if info.Size() > maxBytes {
		return "", fmt.Errorf("uptime monitor %s file exceeds %d bytes", label, maxBytes)
	}
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("read uptime monitor %s file: %w", label, err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return "", fmt.Errorf("read uptime monitor %s file: %w", label, err)
	}
	if int64(len(data)) > maxBytes {
		return "", fmt.Errorf("uptime monitor %s file exceeds %d bytes", label, maxBytes)
	}
	return string(data), nil
}
