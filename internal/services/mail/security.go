package mail

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// TLSConfig holds the current TLS configuration reported by Stalwart.
type TLSConfig struct {
	MinVersion   string   `json:"minVersion"`
	Ciphers      []string `json:"ciphers"`
	ImplicitTLS  bool     `json:"implicitTls"`
	STARTTLS     bool     `json:"starttls"`
}

// RateLimits holds SMTP inbound rate-limiting settings.
type RateLimits struct {
	// Connections per IP per minute on port 25
	InboundConnPerMinute int `json:"inboundConnPerMinute"`
	// Messages per SMTP session
	MessagesPerSession int `json:"messagesPerSession"`
	// Recipients per message
	RecipientsPerMessage int `json:"recipientsPerMessage"`
	// Outbound messages per hour per sender
	OutboundPerHour int `json:"outboundPerHour"`
}

// UpdateRateLimitsRequest carries writable rate-limit fields.
type UpdateRateLimitsRequest struct {
	InboundConnPerMinute *int `json:"inboundConnPerMinute,omitempty"`
	MessagesPerSession   *int `json:"messagesPerSession,omitempty"`
	RecipientsPerMessage *int `json:"recipientsPerMessage,omitempty"`
	OutboundPerHour      *int `json:"outboundPerHour,omitempty"`
}

// FailedLogin represents a single failed authentication attempt from the logs.
type FailedLogin struct {
	Timestamp string `json:"timestamp"`
	IP        string `json:"ip"`
	Account   string `json:"account,omitempty"`
	Reason    string `json:"reason"`
}

// ConnectionStats holds counts of active SMTP/IMAP connections.
type ConnectionStats struct {
	TotalConnections int            `json:"totalConnections"`
	ByProtocol       map[string]int `json:"byProtocol"`
	Timestamp        time.Time      `json:"timestamp"`
}

// stalwartMetricsResponse is a subset of GET /api/telemetry/metrics response.
type stalwartMetricsResponse struct {
	Metrics []stalwartMetric `json:"metrics"`
}

type stalwartMetric struct {
	Name   string            `json:"name"`
	Value  interface{}       `json:"value"`
	Labels map[string]string `json:"labels"`
}

// ── TLS ──────────────────────────────────────────────────────────────────────

// GetTLSConfig reads TLS settings from Stalwart's configuration store.
func (s *Service) GetTLSConfig() (*TLSConfig, error) {
	var raw stalwartSettingsResponse
	if err := s.get("/api/settings/list?prefix=server.tls", &raw); err != nil {
		return nil, fmt.Errorf("get tls config: %w", err)
	}

	cfg := &TLSConfig{
		MinVersion: "TLSv1.2",
		Ciphers:    []string{},
		ImplicitTLS: true,
		STARTTLS:   true,
	}

	for _, item := range raw.Items {
		switch {
		case item.Key == "server.tls.version.min":
			if item.Value != "" {
				cfg.MinVersion = item.Value
			}
		case strings.HasPrefix(item.Key, "server.tls.ciphers."):
			if item.Value != "" {
				cfg.Ciphers = append(cfg.Ciphers, item.Value)
			}
		case item.Key == "server.tls.implicit":
			cfg.ImplicitTLS = parseBool(item.Value)
		case item.Key == "server.tls.starttls":
			cfg.STARTTLS = parseBool(item.Value)
		}
	}

	return cfg, nil
}

// ── Rate Limits ──────────────────────────────────────────────────────────────

// GetRateLimits reads SMTP rate-limiting settings from Stalwart's configuration.
func (s *Service) GetRateLimits() (*RateLimits, error) {
	var raw stalwartSettingsResponse
	if err := s.get("/api/settings/list?prefix=session.throttle", &raw); err != nil {
		return nil, fmt.Errorf("get rate limits: %w", err)
	}

	limits := &RateLimits{
		InboundConnPerMinute: 100,
		MessagesPerSession:   10,
		RecipientsPerMessage: 25,
		OutboundPerHour:      500,
	}

	for _, item := range raw.Items {
		switch item.Key {
		case "session.throttle.inbound.connection.rate":
			limits.InboundConnPerMinute = parseInt(item.Value, 100)
		case "session.throttle.inbound.messages.rate":
			limits.MessagesPerSession = parseInt(item.Value, 10)
		case "session.throttle.inbound.recipients.max":
			limits.RecipientsPerMessage = parseInt(item.Value, 25)
		case "session.throttle.outbound.rate":
			limits.OutboundPerHour = parseInt(item.Value, 500)
		}
	}

	return limits, nil
}

// UpdateRateLimits writes rate-limiting settings to Stalwart.
func (s *Service) UpdateRateLimits(req UpdateRateLimitsRequest) error {
	keys := make(map[string]interface{})

	if req.InboundConnPerMinute != nil {
		keys["session.throttle.inbound.connection.rate"] = fmt.Sprintf("%d", *req.InboundConnPerMinute)
	}
	if req.MessagesPerSession != nil {
		keys["session.throttle.inbound.messages.rate"] = fmt.Sprintf("%d", *req.MessagesPerSession)
	}
	if req.RecipientsPerMessage != nil {
		keys["session.throttle.inbound.recipients.max"] = fmt.Sprintf("%d", *req.RecipientsPerMessage)
	}
	if req.OutboundPerHour != nil {
		keys["session.throttle.outbound.rate"] = fmt.Sprintf("%d", *req.OutboundPerHour)
	}

	if len(keys) == 0 {
		return nil
	}

	if err := s.post("/api/settings", stalwartSettingsUpdate{Keys: keys}, nil); err != nil {
		return fmt.Errorf("update rate limits: %w", err)
	}
	return nil
}

// ── Failed Logins ────────────────────────────────────────────────────────────

// GetFailedLogins queries Stalwart's log endpoint for recent authentication failures.
func (s *Service) GetFailedLogins(limit int) ([]FailedLogin, error) {
	if err := s.requireBaseURL(); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}

	// GET /api/logs returns plain-text log lines (newline-delimited).
	req, err := http.NewRequest(http.MethodGet,
		fmt.Sprintf("%s/api/logs?level=warn&limit=%d&filter=authentication", s.baseURL, limit),
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("build logs request: %w", err)
	}
	s.setAuth(req)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("get logs: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("get logs HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	entries := make([]FailedLogin, 0, limit)
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if entry, ok := parseFailedLoginLine(line); ok {
			entries = append(entries, entry)
			if len(entries) >= limit {
				break
			}
		}
	}
	return entries, nil
}

// parseFailedLoginLine tries to extract a FailedLogin from a Stalwart log line.
// Stalwart logs use structured text like:
//
//	2024-01-15T12:34:56Z WARN Authentication failed for user@example.com from 1.2.3.4
func parseFailedLoginLine(line string) (FailedLogin, bool) {
	lower := strings.ToLower(line)
	if !strings.Contains(lower, "authentication failed") &&
		!strings.Contains(lower, "login failed") &&
		!strings.Contains(lower, "auth error") {
		return FailedLogin{}, false
	}

	entry := FailedLogin{Reason: "authentication failed"}

	// Extract timestamp (first field).
	parts := strings.Fields(line)
	if len(parts) > 0 {
		entry.Timestamp = parts[0]
	}

	// Extract IP address.
	for _, part := range parts {
		if isIPLike(part) {
			entry.IP = strings.Trim(part, ",;")
			break
		}
	}

	// Extract account (email pattern).
	for _, part := range parts {
		if strings.Contains(part, "@") {
			entry.Account = strings.Trim(part, ",;\"")
			break
		}
	}

	return entry, true
}

func isIPLike(s string) bool {
	clean := strings.Trim(s, ",;")
	dots := strings.Count(clean, ".")
	colons := strings.Count(clean, ":")
	return (dots == 3) || (colons >= 2) // IPv4 or IPv6
}

// ── Connection Stats ─────────────────────────────────────────────────────────

// GetConnectionStats returns active SMTP/IMAP connection counts from Stalwart metrics.
func (s *Service) GetConnectionStats() (*ConnectionStats, error) {
	var metrics stalwartMetricsResponse
	err := s.get("/api/telemetry/metrics", &metrics)
	if err != nil {
		// Metrics endpoint may not be available in all Stalwart versions;
		// fall back to a minimal response rather than an error.
		return &ConnectionStats{
			TotalConnections: 0,
			ByProtocol:       map[string]int{},
			Timestamp:        time.Now(),
		}, nil
	}

	stats := &ConnectionStats{
		ByProtocol: make(map[string]int),
		Timestamp:  time.Now(),
	}

	for _, m := range metrics.Metrics {
		name := strings.ToLower(m.Name)
		// Stalwart emits metrics like "server.connections" with a "listener" label.
		if !strings.Contains(name, "connection") {
			continue
		}

		count := toInt(m.Value)
		if proto, ok := m.Labels["listener"]; ok && proto != "" {
			proto = strings.ToUpper(proto)
			stats.ByProtocol[proto] += count
		}
		stats.TotalConnections += count
	}

	return stats, nil
}

// ── tiny numeric helpers ─────────────────────────────────────────────────────

func parseInt(s string, def int) int {
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err != nil {
		return def
	}
	return n
}

func toInt(v interface{}) int {
	switch val := v.(type) {
	case float64:
		return int(val)
	case int:
		return val
	case int64:
		return int(val)
	default:
		return 0
	}
}
