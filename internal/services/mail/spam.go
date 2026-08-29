package mail

import (
	"fmt"
	"strings"
)

// SpamConfig holds the Stalwart spam filter configuration.
type SpamConfig struct {
	Enabled        bool    `json:"enabled"`
	ScoreSpam      float64 `json:"scoreSpam"`
	ScoreDiscard   float64 `json:"scoreDiscard"`
	ScoreReject    float64 `json:"scoreReject"`
	HeaderStatus   bool    `json:"headerStatus"`
	HeaderName     string  `json:"headerName"`
	CardIsHam      bool    `json:"cardIsHam"`
	AutoUpdate     bool    `json:"autoUpdate"`
}

// UpdateSpamConfigRequest carries writable spam filter fields.
type UpdateSpamConfigRequest struct {
	Enabled      *bool    `json:"enabled,omitempty"`
	ScoreSpam    *float64 `json:"scoreSpam,omitempty"`
	ScoreDiscard *float64 `json:"scoreDiscard,omitempty"`
	ScoreReject  *float64 `json:"scoreReject,omitempty"`
	HeaderStatus *bool    `json:"headerStatus,omitempty"`
	HeaderName   *string  `json:"headerName,omitempty"`
	CardIsHam    *bool    `json:"cardIsHam,omitempty"`
	AutoUpdate   *bool    `json:"autoUpdate,omitempty"`
}

// SenderPattern represents a single entry in the blocklist or allowlist.
type SenderPattern struct {
	Pattern string `json:"pattern"`
	Kind    string `json:"kind"` // "email" or "domain"
}

// stalwartSettingsResponse is the JSON shape returned by GET /api/settings/list.
type stalwartSettingsResponse struct {
	Items []stalwartSettingItem `json:"items"`
	Total int                   `json:"total"`
}

type stalwartSettingItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// stalwartSettingsUpdate is the payload sent to POST /api/settings.
type stalwartSettingsUpdate struct {
	Keys map[string]interface{} `json:"keys"`
}

// ── Spam Config ──────────────────────────────────────────────────────────────

// GetSpamConfig reads spam filter settings from Stalwart's configuration store.
func (s *Service) GetSpamConfig() (*SpamConfig, error) {
	var raw stalwartSettingsResponse
	if err := s.get("/api/settings/list?prefix=spam-filter", &raw); err != nil {
		return nil, fmt.Errorf("get spam config: %w", err)
	}

	cfg := &SpamConfig{
		Enabled:      true,
		ScoreSpam:    5.0,
		ScoreDiscard: 0.0,
		ScoreReject:  0.0,
		HeaderStatus: true,
		HeaderName:   "X-Spam-Status",
		CardIsHam:    true,
		AutoUpdate:   true,
	}

	for _, item := range raw.Items {
		switch item.Key {
		case "spam-filter.enable":
			cfg.Enabled = parseBool(item.Value)
		case "spam-filter.score.spam":
			cfg.ScoreSpam = parseFloat(item.Value, 5.0)
		case "spam-filter.score.discard":
			cfg.ScoreDiscard = parseFloat(item.Value, 0.0)
		case "spam-filter.score.reject":
			cfg.ScoreReject = parseFloat(item.Value, 0.0)
		case "spam-filter.header.status.enable":
			cfg.HeaderStatus = parseBool(item.Value)
		case "spam-filter.header.status.name":
			if item.Value != "" {
				cfg.HeaderName = item.Value
			}
		case "spam-filter.card-is-ham":
			cfg.CardIsHam = parseBool(item.Value)
		case "spam-filter.auto-update":
			cfg.AutoUpdate = parseBool(item.Value)
		}
	}

	return cfg, nil
}

// UpdateSpamConfig writes spam filter settings back to Stalwart.
func (s *Service) UpdateSpamConfig(req UpdateSpamConfigRequest) error {
	keys := make(map[string]interface{})

	if req.Enabled != nil {
		keys["spam-filter.enable"] = boolStr(*req.Enabled)
	}
	if req.ScoreSpam != nil {
		keys["spam-filter.score.spam"] = fmt.Sprintf("%.1f", *req.ScoreSpam)
	}
	if req.ScoreDiscard != nil {
		keys["spam-filter.score.discard"] = fmt.Sprintf("%.1f", *req.ScoreDiscard)
	}
	if req.ScoreReject != nil {
		keys["spam-filter.score.reject"] = fmt.Sprintf("%.1f", *req.ScoreReject)
	}
	if req.HeaderStatus != nil {
		keys["spam-filter.header.status.enable"] = boolStr(*req.HeaderStatus)
	}
	if req.HeaderName != nil {
		keys["spam-filter.header.status.name"] = *req.HeaderName
	}
	if req.CardIsHam != nil {
		keys["spam-filter.card-is-ham"] = boolStr(*req.CardIsHam)
	}
	if req.AutoUpdate != nil {
		keys["spam-filter.auto-update"] = boolStr(*req.AutoUpdate)
	}

	if len(keys) == 0 {
		return nil
	}

	if err := s.post("/api/settings", stalwartSettingsUpdate{Keys: keys}, nil); err != nil {
		return fmt.Errorf("update spam config: %w", err)
	}
	return nil
}

// ── Blocklist ────────────────────────────────────────────────────────────────

// GetBlockedSenders returns all patterns in the spam-filter blocklist.
func (s *Service) GetBlockedSenders() ([]SenderPattern, error) {
	return s.getSenderList("spam-filter.list.blocked-senders")
}

// AddBlockedSender adds a pattern to the spam blocklist.
func (s *Service) AddBlockedSender(pattern string) error {
	if pattern == "" {
		return fmt.Errorf("pattern is required")
	}
	return s.appendSenderToList("spam-filter.list.blocked-senders", pattern)
}

// RemoveBlockedSender removes a pattern from the spam blocklist.
func (s *Service) RemoveBlockedSender(pattern string) error {
	if pattern == "" {
		return fmt.Errorf("pattern is required")
	}
	return s.removeSenderFromList("spam-filter.list.blocked-senders", pattern)
}

// ── Allowlist ────────────────────────────────────────────────────────────────

// GetAllowedSenders returns all patterns in the spam-filter allowlist.
func (s *Service) GetAllowedSenders() ([]SenderPattern, error) {
	return s.getSenderList("spam-filter.list.allowed-senders")
}

// AddAllowedSender adds a pattern to the spam allowlist.
func (s *Service) AddAllowedSender(pattern string) error {
	if pattern == "" {
		return fmt.Errorf("pattern is required")
	}
	return s.appendSenderToList("spam-filter.list.allowed-senders", pattern)
}

// RemoveAllowedSender removes a pattern from the spam allowlist.
func (s *Service) RemoveAllowedSender(pattern string) error {
	if pattern == "" {
		return fmt.Errorf("pattern is required")
	}
	return s.removeSenderFromList("spam-filter.list.allowed-senders", pattern)
}

// ── internal list helpers ────────────────────────────────────────────────────

func (s *Service) getSenderList(key string) ([]SenderPattern, error) {
	var raw stalwartSettingsResponse
	if err := s.get("/api/settings/list?prefix="+key, &raw); err != nil {
		return nil, fmt.Errorf("get sender list %s: %w", key, err)
	}

	patterns := make([]SenderPattern, 0, len(raw.Items))
	for _, item := range raw.Items {
		// Items are stored as key.NNN = pattern
		if item.Value == "" {
			continue
		}
		patterns = append(patterns, SenderPattern{
			Pattern: item.Value,
			Kind:    inferKind(item.Value),
		})
	}
	return patterns, nil
}

func (s *Service) appendSenderToList(listKey, pattern string) error {
	// Fetch the current highest index to append safely.
	var raw stalwartSettingsResponse
	if err := s.get("/api/settings/list?prefix="+listKey, &raw); err != nil {
		return fmt.Errorf("read list %s: %w", listKey, err)
	}

	// Find next available index.
	maxIdx := -1
	for _, item := range raw.Items {
		// Reject duplicates.
		if item.Value == pattern {
			return nil // already present
		}
		idx := parseSuffixIndex(item.Key, listKey)
		if idx > maxIdx {
			maxIdx = idx
		}
	}

	nextKey := fmt.Sprintf("%s.%d", listKey, maxIdx+1)
	keys := map[string]interface{}{nextKey: pattern}
	if err := s.post("/api/settings", stalwartSettingsUpdate{Keys: keys}, nil); err != nil {
		return fmt.Errorf("add sender to %s: %w", listKey, err)
	}
	return nil
}

func (s *Service) removeSenderFromList(listKey, pattern string) error {
	var raw stalwartSettingsResponse
	if err := s.get("/api/settings/list?prefix="+listKey, &raw); err != nil {
		return fmt.Errorf("read list %s: %w", listKey, err)
	}

	for _, item := range raw.Items {
		if item.Value == pattern {
			// DELETE /api/settings/{key}
			if err := s.delete("/api/settings/" + item.Key); err != nil {
				return fmt.Errorf("remove sender from %s: %w", listKey, err)
			}
			return nil
		}
	}
	return fmt.Errorf("pattern %q not found in list", pattern)
}

// ── small utilities ──────────────────────────────────────────────────────────

func inferKind(pattern string) string {
	if strings.Contains(pattern, "@") {
		return "email"
	}
	return "domain"
}

// parseSuffixIndex extracts the trailing integer from a key like "prefix.3".
func parseSuffixIndex(key, prefix string) int {
	suffix := strings.TrimPrefix(key, prefix+".")
	idx := 0
	for _, c := range suffix {
		if c >= '0' && c <= '9' {
			idx = idx*10 + int(c-'0')
		} else {
			return -1
		}
	}
	return idx
}

func parseBool(s string) bool {
	return strings.EqualFold(s, "true") || s == "1"
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func parseFloat(s string, def float64) float64 {
	var f float64
	if _, err := fmt.Sscanf(s, "%f", &f); err != nil {
		return def
	}
	return f
}
