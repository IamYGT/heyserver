package uptime

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/IamYGT/heyserver/internal/store"
)

func checkHTTP(m *store.UptimeMonitor) CheckResult {
	result := CheckResult{MonitorID: m.ID, CheckedAt: time.Now()}

	// NOTE: SSRF validation is done at create/update time in the handler, not here.
	// The monitoring engine needs to check localhost/internal services (PostgreSQL, health endpoints, etc.).

	client := &http.Client{
		Timeout: time.Duration(m.TimeoutSecs) * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= m.MaxRedirects {
				return fmt.Errorf("too many redirects (%d)", m.MaxRedirects)
			}
			return nil
		},
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: !m.TLSCheck},
		},
	}

	method := m.Method
	if method == "" {
		method = "GET"
	}

	var bodyReader io.Reader
	if m.ReqBody != "" {
		bodyReader = strings.NewReader(m.ReqBody)
	}

	req, err := http.NewRequest(method, m.URL, bodyReader)
	if err != nil {
		result.Status = StatusDown
		result.Msg = fmt.Sprintf("request build error: %v", err)
		return result
	}

	// Parse custom headers before any network activity. Both the JSON object
	// format used by the UI and the legacy newline-delimited format are valid.
	if err := applyHTTPHeaders(req, m.ReqHeaders); err != nil {
		result.Status = StatusDown
		result.Msg = fmt.Sprintf("request headers error: %v", err)
		return result
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "HServer-Uptime/1.0")
	}

	start := time.Now()
	resp, err := client.Do(req)
	result.PingMs = float64(time.Since(start).Milliseconds())

	if err != nil {
		result.Status = StatusDown
		result.Msg = fmt.Sprintf("connection error: %v", err)
		return result
	}
	defer func() { _ = resp.Body.Close() }()

	result.StatusCode = resp.StatusCode

	// Check TLS expiry
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		cert := resp.TLS.PeerCertificates[0]
		result.TLSExpiry = cert.NotAfter.Format(time.RFC3339)
		daysLeft := int(time.Until(cert.NotAfter).Hours() / 24)
		if daysLeft <= m.TLSExpiryWarnDays && daysLeft > 0 {
			result.Status = StatusTLSWarn
			result.Msg = fmt.Sprintf("TLS certificate expires in %d days", daysLeft)
			return result
		}
		if daysLeft <= 0 {
			result.Status = StatusDown
			result.Msg = "TLS certificate expired"
			return result
		}
	}

	// Check status code
	if !isAcceptedStatus(resp.StatusCode, m.AcceptedStatusCodes) {
		result.Status = StatusDown
		result.Msg = fmt.Sprintf("unexpected status code: %d", resp.StatusCode)
		return result
	}

	// Check keyword
	if m.Keyword != "" {
		bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
		if err != nil {
			result.Status = StatusDown
			result.Msg = fmt.Sprintf("body read error: %v", err)
			return result
		}
		body := string(bodyBytes)
		found := strings.Contains(body, m.Keyword)
		if m.KeywordInvert {
			found = !found
		}
		if !found {
			result.Status = StatusDown
			if m.KeywordInvert {
				result.Msg = fmt.Sprintf("keyword '%s' found (should be absent)", m.Keyword)
			} else {
				result.Msg = fmt.Sprintf("keyword '%s' not found in response", m.Keyword)
			}
			return result
		}
	}

	result.Status = StatusUp
	result.Msg = fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
	return result
}

type httpHeader struct {
	name  string
	value string
}

// applyHTTPHeaders validates and applies custom headers to req. Header names
// and values are intentionally never included in returned errors because
// values may contain credentials.
func applyHTTPHeaders(req *http.Request, raw string) error {
	headers, err := parseHTTPHeaders(raw)
	if err != nil {
		return err
	}

	for _, header := range headers {
		// Go requires Host to be set via req.Host, not req.Header.
		if strings.EqualFold(header.name, "Host") {
			req.Host = header.value
		} else {
			req.Header.Set(header.name, header.value)
		}
	}
	return nil
}

// parseHTTPHeaders accepts either a JSON object with string values or the
// legacy newline-delimited "Name: value" format. Blank lines remain ignored
// for compatibility with the previous parser; non-empty malformed lines are
// rejected instead of being silently dropped.
func parseHTTPHeaders(raw string) ([]httpHeader, error) {
	if raw == "" {
		return nil, nil
	}
	if strings.ContainsAny(raw, "\r\x00") {
		return nil, fmt.Errorf("invalid HTTP header input")
	}

	trimmed := strings.Trim(raw, " \t\n")
	if trimmed == "" {
		return nil, nil
	}
	if strings.HasPrefix(trimmed, "{") {
		return parseJSONHTTPHeaders(trimmed)
	}

	lines := strings.Split(raw, "\n")
	headers := make([]httpHeader, 0, len(lines))
	for lineNumber, line := range lines {
		lineNumber++
		line = strings.Trim(line, " \t")
		if line == "" {
			continue
		}

		separator := strings.IndexByte(line, ':')
		if separator <= 0 {
			return nil, fmt.Errorf("invalid HTTP header on line %d", lineNumber)
		}
		name := strings.Trim(line[:separator], " \t")
		value := strings.Trim(line[separator+1:], " \t")
		if !validHTTPHeaderName(name) {
			return nil, fmt.Errorf("invalid HTTP header name on line %d", lineNumber)
		}
		if !validHTTPHeaderValue(value) {
			return nil, fmt.Errorf("invalid HTTP header value on line %d", lineNumber)
		}
		headers = append(headers, httpHeader{name: name, value: value})
	}
	return headers, nil
}

func parseJSONHTTPHeaders(raw string) ([]httpHeader, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &object); err != nil || object == nil {
		return nil, fmt.Errorf("invalid JSON HTTP headers object")
	}

	headers := make([]httpHeader, 0, len(object))
	for name, rawValue := range object {
		if !validHTTPHeaderName(name) {
			return nil, fmt.Errorf("invalid JSON HTTP header name")
		}
		var value *string
		if err := json.Unmarshal(rawValue, &value); err != nil || value == nil {
			return nil, fmt.Errorf("JSON HTTP header values must be strings")
		}
		if !validHTTPHeaderValue(*value) {
			return nil, fmt.Errorf("invalid JSON HTTP header value")
		}
		headers = append(headers, httpHeader{name: name, value: *value})
	}
	return headers, nil
}

func validHTTPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9') {
			continue
		}
		switch c {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}

func validHTTPHeaderValue(value string) bool {
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c < 0x20 && c != '\t') || c == 0x7f {
			return false
		}
	}
	return true
}

// isAcceptedStatus checks if code matches accepted ranges like ["200-299", "301"].
func isAcceptedStatus(code int, accepted string) bool {
	if accepted == "" {
		accepted = `["200-299"]`
	}
	// Simple parser for JSON array of strings
	accepted = strings.Trim(accepted, "[] ")
	for _, part := range strings.Split(accepted, ",") {
		part = strings.Trim(part, `" `)
		if part == "" {
			continue
		}
		if strings.Contains(part, "-") {
			bounds := strings.SplitN(part, "-", 2)
			lo, _ := strconv.Atoi(strings.TrimSpace(bounds[0]))
			hi, _ := strconv.Atoi(strings.TrimSpace(bounds[1]))
			if code >= lo && code <= hi {
				return true
			}
		} else {
			exact, _ := strconv.Atoi(part)
			if code == exact {
				return true
			}
		}
	}
	return false
}
