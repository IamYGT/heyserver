package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

type uptimeMonitorCreateOptions struct {
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
	TLSCheck            bool
	TLSExpiryWarnDays   int
	DNSRecordType       string
	DNSExpectedValue    string
	Description         string
	MaxRedirects        int
	AlertReminderMins   int
	AlertChannelIDs     stringValues
	visited             map[string]bool
}

func runUptime(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl uptime summary|monitors|monitor|incidents|status-pages|status-page|settings|import-domains")
	}
	switch args[0] {
	case "summary":
		if len(args) != 1 {
			return errors.New("usage: hserverctl uptime summary")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/uptime/monitors/summary", nil, true)
	case "monitors":
		if len(args) != 1 {
			return errors.New("usage: hserverctl uptime monitors")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/uptime/monitors", nil, true)
	case "monitor":
		return runUptimeMonitor(ctx, client, args[1:], out)
	case "incidents":
		return runUptimeIncidents(ctx, client, args[1:], out)
	case "status-pages":
		if len(args) != 1 {
			return errors.New("usage: hserverctl uptime status-pages")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/uptime/status-pages", nil, true)
	case "status-page":
		return runUptimeStatusPage(ctx, client, args[1:], out)
	case "settings":
		return runUptimeSettings(ctx, client, args[1:], out)
	case "import-domains":
		flags := flag.NewFlagSet("uptime import-domains", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		confirmed := flags.Bool("confirm", false, "confirm creating monitors for local domains")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 0 {
			return errors.New("usage: hserverctl uptime import-domains --confirm")
		}
		if !*confirmed {
			return errors.New("uptime domain import requires explicit --confirm")
		}
		return printRequest(ctx, client, out, http.MethodPost, "/api/uptime/monitors/bulk-from-domains", nil, true)
	default:
		return fmt.Errorf("unknown uptime command %q", args[0])
	}
}

func runUptimeMonitor(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl uptime monitor get|create|update|heartbeats|stats|check|pause|resume|delete")
	}
	switch args[0] {
	case "get":
		if len(args) != 2 {
			return errors.New("usage: hserverctl uptime monitor get ID")
		}
		id, err := positiveUptimeID("monitor", args[1])
		if err != nil {
			return err
		}
		return printRequest(ctx, client, out, http.MethodGet, uptimeMonitorEndpoint(id), nil, true)
	case "create":
		return runUptimeMonitorCreate(ctx, client, args[1:], out)
	case "update":
		return runUptimeMonitorUpdate(ctx, client, args[1:], out)
	case "heartbeats":
		return runUptimeMonitorHeartbeats(ctx, client, args[1:], out)
	case "stats":
		if len(args) != 2 {
			return errors.New("usage: hserverctl uptime monitor stats ID")
		}
		id, err := positiveUptimeID("monitor", args[1])
		if err != nil {
			return err
		}
		return printRequest(ctx, client, out, http.MethodGet, uptimeMonitorEndpoint(id)+"/uptime", nil, true)
	case "check", "pause", "resume", "delete":
		return runUptimeMonitorAction(ctx, client, args[0], args[1:], out)
	default:
		return fmt.Errorf("unknown uptime monitor command %q", args[0])
	}
}

func runUptimeMonitorCreate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags, options := newUptimeMonitorCreateFlags()
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl uptime monitor create --confirm --name NAME --type http|tcp|ping|dns [OPTIONS]")
	}
	options.visited = make(map[string]bool)
	flags.Visit(func(item *flag.Flag) { options.visited[item.Name] = true })
	if !options.Confirm {
		return errors.New("uptime monitor creation requires explicit --confirm")
	}
	payload, err := buildUptimeMonitorCreatePayload(options)
	if err != nil {
		return err
	}
	return printRequest(ctx, client, out, http.MethodPost, "/api/uptime/monitors", payload, true)
}

func newUptimeMonitorCreateFlags() (*flag.FlagSet, *uptimeMonitorCreateOptions) {
	flags := flag.NewFlagSet("uptime monitor create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := &uptimeMonitorCreateOptions{}
	flags.BoolVar(&options.Confirm, "confirm", false, "confirm uptime monitor creation")
	flags.StringVar(&options.Name, "name", "", "monitor display name")
	flags.StringVar(&options.Type, "type", "", "http, tcp, ping, or dns")
	flags.StringVar(&options.URL, "url", "", "HTTP or HTTPS target URL")
	flags.StringVar(&options.Hostname, "hostname", "", "TCP, ping, or DNS hostname")
	flags.IntVar(&options.Port, "port", 0, "TCP target port")
	flags.StringVar(&options.Method, "method", "GET", "HTTP method")
	flags.IntVar(&options.IntervalSecs, "interval-secs", 60, "seconds between checks")
	flags.IntVar(&options.TimeoutSecs, "timeout-secs", 30, "per-check timeout in seconds")
	flags.IntVar(&options.Retries, "retries", 1, "consecutive failures before down state")
	flags.IntVar(&options.RetryInterval, "retry-interval-secs", 30, "seconds between retry checks")
	flags.StringVar(&options.AcceptedStatusCodes, "accepted-statuscodes", "200-299", "comma-separated HTTP codes or ranges")
	flags.StringVar(&options.Keyword, "keyword", "", "HTTP response keyword")
	flags.BoolVar(&options.KeywordInvert, "keyword-invert", false, "require the HTTP keyword to be absent")
	flags.BoolVar(&options.TLSCheck, "tls-check", false, "verify the HTTP TLS certificate")
	flags.IntVar(&options.TLSExpiryWarnDays, "tls-expiry-warn-days", 14, "TLS expiry warning threshold")
	flags.StringVar(&options.DNSRecordType, "dns-record-type", "A", "DNS record type: A, AAAA, MX, or CNAME")
	flags.StringVar(&options.DNSExpectedValue, "dns-expected", "", "optional exact DNS record value")
	flags.StringVar(&options.Description, "description", "", "monitor description")
	flags.IntVar(&options.MaxRedirects, "max-redirects", 5, "maximum HTTP redirects")
	flags.IntVar(&options.AlertReminderMins, "alert-reminder-mins", 0, "minutes between repeated down alerts")
	flags.Var(&options.AlertChannelIDs, "alert-channel", "notification channel ID; repeatable")
	return flags, options
}

func buildUptimeMonitorCreatePayload(options *uptimeMonitorCreateOptions) (map[string]any, error) {
	name, err := boundedUptimeText("monitor name", options.Name, 128, true)
	if err != nil {
		return nil, err
	}
	monitorType := strings.ToLower(strings.TrimSpace(options.Type))
	switch monitorType {
	case "http", "tcp", "ping", "dns":
	default:
		return nil, errors.New("uptime monitor type must be http, tcp, ping, or dns")
	}
	if options.IntervalSecs < 10 || options.IntervalSecs > 86400 {
		return nil, errors.New("uptime monitor interval must be between 10 and 86400 seconds")
	}
	if options.TimeoutSecs < 1 || options.TimeoutSecs > 300 {
		return nil, errors.New("uptime monitor timeout must be between 1 and 300 seconds")
	}
	if options.Retries < 1 || options.Retries > 10 {
		return nil, errors.New("uptime monitor retries must be between 1 and 10")
	}
	if options.RetryInterval < 1 || options.RetryInterval > 3600 {
		return nil, errors.New("uptime monitor retry interval must be between 1 and 3600 seconds")
	}
	if options.AlertReminderMins < 0 || options.AlertReminderMins > 10080 {
		return nil, errors.New("uptime monitor alert reminder must be between 0 and 10080 minutes")
	}
	description, err := boundedUptimeText("monitor description", options.Description, 1024, false)
	if err != nil {
		return nil, err
	}
	channelIDs, err := parseUptimeChannelIDs(options.AlertChannelIDs)
	if err != nil {
		return nil, err
	}
	payload := map[string]any{
		"name":                name,
		"type":                monitorType,
		"interval_secs":       options.IntervalSecs,
		"timeout_secs":        options.TimeoutSecs,
		"retries":             options.Retries,
		"retry_interval":      options.RetryInterval,
		"description":         description,
		"alert_channel_ids":   channelIDs,
		"alert_reminder_mins": options.AlertReminderMins,
	}

	if monitorType == "http" {
		if options.visited["hostname"] || options.visited["port"] || options.visited["dns-record-type"] || options.visited["dns-expected"] {
			return nil, errors.New("--hostname, --port, and DNS options are not valid for HTTP monitors")
		}
		target, err := validateUptimeHTTPURL(options.URL)
		if err != nil {
			return nil, err
		}
		method := strings.ToUpper(strings.TrimSpace(options.Method))
		switch method {
		case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		default:
			return nil, errors.New("uptime HTTP method must be GET, HEAD, POST, PUT, PATCH, DELETE, or OPTIONS")
		}
		accepted, err := normalizeUptimeStatusCodes(options.AcceptedStatusCodes)
		if err != nil {
			return nil, err
		}
		keyword, err := boundedUptimeText("monitor keyword", options.Keyword, 512, false)
		if err != nil {
			return nil, err
		}
		if options.MaxRedirects < 1 || options.MaxRedirects > 20 {
			return nil, errors.New("uptime monitor redirects must be between 1 and 20")
		}
		if options.TLSExpiryWarnDays < 1 || options.TLSExpiryWarnDays > 365 {
			return nil, errors.New("uptime monitor TLS warning must be between 1 and 365 days")
		}
		payload["url"] = target
		payload["method"] = method
		payload["accepted_statuscodes"] = accepted
		payload["keyword"] = keyword
		payload["keyword_invert"] = options.KeywordInvert
		payload["tls_check"] = options.TLSCheck
		payload["tls_expiry_warn_days"] = options.TLSExpiryWarnDays
		payload["max_redirects"] = options.MaxRedirects
		return payload, nil
	}

	for _, option := range []string{"url", "method", "accepted-statuscodes", "keyword-invert", "tls-check", "tls-expiry-warn-days", "max-redirects"} {
		if options.visited[option] {
			return nil, fmt.Errorf("--%s is only valid for HTTP monitors", option)
		}
	}
	hostname, err := validateUptimeHostname(options.Hostname)
	if err != nil {
		return nil, err
	}
	payload["hostname"] = hostname
	if monitorType == "tcp" {
		if options.Port < 1 || options.Port > 65535 {
			return nil, errors.New("uptime TCP monitor port must be between 1 and 65535")
		}
		if options.visited["dns-record-type"] || options.visited["dns-expected"] || options.visited["keyword"] {
			return nil, errors.New("DNS and keyword options are not valid for TCP monitors")
		}
		payload["port"] = options.Port
		return payload, nil
	}
	if options.visited["port"] {
		return nil, errors.New("--port is only valid for TCP monitors")
	}
	if monitorType == "ping" {
		if options.visited["dns-record-type"] || options.visited["dns-expected"] || options.visited["keyword"] {
			return nil, errors.New("DNS and keyword options are not valid for ping monitors")
		}
		return payload, nil
	}
	if options.visited["keyword"] {
		return nil, errors.New("--keyword is only valid for HTTP monitors; use --dns-record-type for DNS monitors")
	}
	recordType := strings.ToUpper(strings.TrimSpace(options.DNSRecordType))
	switch recordType {
	case "A", "AAAA", "MX", "CNAME":
	default:
		return nil, errors.New("uptime DNS record type must be A, AAAA, MX, or CNAME")
	}
	expected, err := boundedUptimeText("expected DNS value", options.DNSExpectedValue, 253, false)
	if err != nil {
		return nil, err
	}
	payload["keyword"] = recordType
	payload["req_body"] = expected
	return payload, nil
}

func runUptimeMonitorHeartbeats(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("uptime monitor heartbeats", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	hours := flags.Int("hours", 24, "history window in hours")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl uptime monitor heartbeats [--hours N] ID")
	}
	if *hours < 1 || *hours > 2160 {
		return errors.New("uptime heartbeat hours must be between 1 and 2160")
	}
	id, err := positiveUptimeID("monitor", flags.Args()[0])
	if err != nil {
		return err
	}
	endpoint := uptimeMonitorEndpoint(id) + "/heartbeats?hours=" + strconv.Itoa(*hours)
	return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
}

func runUptimeIncidents(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("uptime incidents", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	monitor := flags.Int64("monitor", 0, "optional monitor ID")
	limit := flags.Int("limit", 200, "maximum incident rows")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl uptime incidents [--monitor ID] [--limit N]")
	}
	monitorProvided := false
	flags.Visit(func(item *flag.Flag) {
		if item.Name == "monitor" {
			monitorProvided = true
		}
	})
	if *monitor < 0 || (monitorProvided && *monitor == 0) {
		return errors.New("uptime monitor ID must be a positive integer")
	}
	if *limit < 1 || *limit > 1000 {
		return errors.New("uptime incident limit must be between 1 and 1000")
	}
	query := url.Values{"limit": []string{strconv.Itoa(*limit)}}
	if *monitor > 0 {
		query.Set("monitor_id", strconv.FormatInt(*monitor, 10))
	}
	return printRequest(ctx, client, out, http.MethodGet, "/api/uptime/incidents?"+query.Encode(), nil, true)
}

func runUptimeMonitorAction(ctx context.Context, client *apiClient, action string, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("uptime monitor "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm uptime monitor mutation")
	waitDefault := 30 * time.Second
	if action == "check" {
		waitDefault = 2 * time.Minute
	}
	wait := flags.Duration("wait", waitDefault, "maximum operation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return fmt.Errorf("usage: hserverctl uptime monitor %s --confirm [--wait DURATION] ID", action)
	}
	if !*confirmed {
		return fmt.Errorf("uptime monitor %s requires explicit --confirm", action)
	}
	if *wait <= 0 {
		return errors.New("uptime monitor action wait must be greater than zero")
	}
	id, err := positiveUptimeID("monitor", flags.Args()[0])
	if err != nil {
		return err
	}
	method := http.MethodPost
	endpoint := uptimeMonitorEndpoint(id) + "/" + action
	switch action {
	case "check":
		endpoint = uptimeMonitorEndpoint(id) + "/check-now"
	case "pause", "resume":
	case "delete":
		method = http.MethodDelete
		endpoint = uptimeMonitorEndpoint(id)
	default:
		return fmt.Errorf("unsupported uptime monitor action %q", action)
	}
	return printRequest(ctx, client.withTimeout(*wait), out, method, endpoint, nil, true)
}

func uptimeMonitorEndpoint(id int64) string {
	return "/api/uptime/monitors/" + strconv.FormatInt(id, 10)
}

func positiveUptimeID(label, value string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("uptime %s ID must be a positive integer", label)
	}
	return id, nil
}

func validateUptimeHTTPURL(value string) (string, error) {
	clean := strings.TrimSpace(value)
	parsed, err := url.Parse(clean)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("uptime HTTP target must be an absolute http:// or https:// URL without credentials or fragments")
	}
	if parsed.Hostname() == "" || strings.ContainsAny(clean, "\r\n\x00") {
		return "", errors.New("uptime HTTP target contains an invalid hostname")
	}
	return parsed.String(), nil
}

func validateUptimeHostname(value string) (string, error) {
	clean := strings.TrimSpace(value)
	if ip := net.ParseIP(clean); ip != nil {
		return ip.String(), nil
	}
	hostname, err := validateLocalDomainName(clean)
	if err != nil {
		return "", errors.New("uptime target must be a valid ASCII hostname or IP address")
	}
	return hostname, nil
}

func normalizeUptimeStatusCodes(value string) (string, error) {
	parts := strings.Split(value, ",")
	if len(parts) == 0 || len(parts) > 32 {
		return "", errors.New("uptime accepted status codes must contain between 1 and 32 entries")
	}
	normalized := make([]string, 0, len(parts))
	seen := make(map[string]struct{}, len(parts))
	for _, raw := range parts {
		part := strings.TrimSpace(raw)
		if part == "" {
			return "", errors.New("uptime accepted status codes contain an empty entry")
		}
		if strings.Contains(part, "-") {
			bounds := strings.Split(part, "-")
			if len(bounds) != 2 {
				return "", fmt.Errorf("invalid uptime status range %q", part)
			}
			low, lowErr := strconv.Atoi(bounds[0])
			high, highErr := strconv.Atoi(bounds[1])
			if lowErr != nil || highErr != nil || low < 100 || high > 599 || low > high {
				return "", fmt.Errorf("invalid uptime status range %q", part)
			}
			part = strconv.Itoa(low) + "-" + strconv.Itoa(high)
		} else {
			code, err := strconv.Atoi(part)
			if err != nil || code < 100 || code > 599 {
				return "", fmt.Errorf("invalid uptime status code %q", part)
			}
			part = strconv.Itoa(code)
		}
		if _, exists := seen[part]; exists {
			continue
		}
		seen[part] = struct{}{}
		normalized = append(normalized, part)
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func parseUptimeChannelIDs(values []string) ([]int64, error) {
	if len(values) > 128 {
		return nil, errors.New("uptime monitor accepts at most 128 alert channels")
	}
	ids := make([]int64, 0, len(values))
	seen := make(map[int64]struct{}, len(values))
	for _, value := range values {
		id, err := positiveUptimeID("alert channel", value)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

func boundedUptimeText(label, value string, maxBytes int, required bool) (string, error) {
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
