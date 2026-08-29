package mail

// stats.go — mail statistics gathered from Stalwart telemetry API + journald.
//
// Strategy:
//   - Stalwart exposes a snapshot metrics endpoint at /api/telemetry/metrics
//     (non-streaming). We use that when available.
//   - For per-sender / per-recipient breakdowns and delivery history we fall
//     back to parsing the explicitly configured systemd service journal.
//   - Storage per account comes from the Stalwart principal API (quota/used).
//
// All parse functions are resilient: they return zero-values on error so the
// API always returns something useful even if Stalwart is temporarily down.

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ── Types ─────────────────────────────────────────────────────────────────────

// MailStats holds aggregate counts for a recent period (default: today).
type MailStats struct {
	Period           string    `json:"period"`
	MessagesSent     int       `json:"messagesSent"`
	MessagesReceived int       `json:"messagesReceived"`
	SpamBlocked      int       `json:"spamBlocked"`
	DeliveryFailures int       `json:"deliveryFailures"`
	QueueSize        int       `json:"queueSize"`
	GeneratedAt      time.Time `json:"generatedAt"`
}

// SenderStat is one entry in the top-senders list.
type SenderStat struct {
	Address string `json:"address"`
	Count   int    `json:"count"`
}

// RecipientStat is one entry in the top-recipients list.
type RecipientStat struct {
	Address string `json:"address"`
	Count   int    `json:"count"`
}

// VolumePoint represents message count for a single time bucket.
type VolumePoint struct {
	Timestamp time.Time `json:"timestamp"`
	Sent      int       `json:"sent"`
	Received  int       `json:"received"`
}

// AccountStorage holds per-account storage info.
type AccountStorage struct {
	Email       string  `json:"email"`
	UsedBytes   int64   `json:"usedBytes"`
	QuotaBytes  int64   `json:"quotaBytes"`
	UsedPercent float64 `json:"usedPercent"`
}

// Deliverability holds overall delivery success metrics.
type Deliverability struct {
	TotalAttempts  int     `json:"totalAttempts"`
	Delivered      int     `json:"delivered"`
	Failed         int     `json:"failed"`
	SuccessRate    float64 `json:"successRate"`
	Period         string  `json:"period"`
}

// ── Journal log regexps ───────────────────────────────────────────────────────

var (
	// Stalwart log line format:
	// 2025-03-20T21:43:36Z INFO Message delivered (smtp.message-delivered) from=user@a.com to=user@b.com
	reSent = regexp.MustCompile(
		`(?i)(smtp\.message-delivered|smtp\.message-submitted|message.+delivered)\b.*?from=([^\s,]+)`,
	)
	reReceived = regexp.MustCompile(
		`(?i)(smtp\.message-received|message.+received|inbound.+message)\b.*?to=([^\s,]+)`,
	)
	reSpam = regexp.MustCompile(
		`(?i)(spam\.blocked|message-spam|junk|spam.+detected)`,
	)
	reFailure = regexp.MustCompile(
		`(?i)(delivery.+fail|smtp\.delivery-failed|bounce|non-deliverable|error.+deliver)`,
	)
	reTimestamp = regexp.MustCompile(`^(\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2})`)
	reFrom      = regexp.MustCompile(`from=([^\s,>]+)`)
	reTo        = regexp.MustCompile(`to=([^\s,>]+)`)
)

// ── GetMailStats ──────────────────────────────────────────────────────────────

// GetMailStats parses today's journal entries for the stalwart service and
// returns aggregate send / receive / spam / failure counts.
func (s *Service) GetMailStats() MailStats {
	lines := readJournalSince(s.serviceName, "today")
	stats := MailStats{
		Period:      "today",
		GeneratedAt: time.Now().UTC(),
	}

	for _, line := range lines {
		lower := strings.ToLower(line)
		switch {
		case reSent.MatchString(lower):
			stats.MessagesSent++
		case reReceived.MatchString(lower):
			stats.MessagesReceived++
		case reSpam.MatchString(lower):
			stats.SpamBlocked++
		case reFailure.MatchString(lower):
			stats.DeliveryFailures++
		}
	}

	// Enrich with live queue size from the management API.
	if msgs, err := s.GetQueueStatus(); err == nil {
		stats.QueueSize = len(msgs)
	}

	return stats
}

// ── GetTopSenders ─────────────────────────────────────────────────────────────

// GetTopSenders returns the most active sending addresses found in today's
// journal logs, sorted descending by message count.
func (s *Service) GetTopSenders(limit int) []SenderStat {
	if limit <= 0 {
		limit = 10
	}
	lines := readJournalSince(s.serviceName, "today")
	counts := make(map[string]int)

	for _, line := range lines {
		if !reSent.MatchString(strings.ToLower(line)) {
			continue
		}
		if m := reFrom.FindStringSubmatch(line); len(m) == 2 {
			addr := normalizeAddr(m[1])
			if addr != "" {
				counts[addr]++
			}
		}
	}
	return topN(counts, limit, func(a, b SenderStat) bool { return a.Count > b.Count })
}

// ── GetTopRecipients ──────────────────────────────────────────────────────────

// GetTopRecipients returns the most active recipient addresses found in
// today's journal logs, sorted descending by message count.
func (s *Service) GetTopRecipients(limit int) []RecipientStat {
	if limit <= 0 {
		limit = 10
	}
	lines := readJournalSince(s.serviceName, "today")
	counts := make(map[string]int)

	for _, line := range lines {
		lower := strings.ToLower(line)
		// Count both delivered and received lines for recipients
		if !reSent.MatchString(lower) && !reReceived.MatchString(lower) {
			continue
		}
		if m := reTo.FindStringSubmatch(line); len(m) == 2 {
			addr := normalizeAddr(m[1])
			if addr != "" {
				counts[addr]++
			}
		}
	}
	result := make([]RecipientStat, 0, len(counts))
	for addr, n := range counts {
		result = append(result, RecipientStat{Address: addr, Count: n})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Count > result[j].Count })
	if len(result) > limit {
		result = result[:limit]
	}
	return result
}

// ── GetMailVolume ─────────────────────────────────────────────────────────────

// GetMailVolume builds a time-series of sent/received message counts.
//
// period: "24h" (hourly buckets over last 24 h) or "7d" (daily buckets over
// last 7 days). Unknown values default to "24h".
func (s *Service) GetMailVolume(period string) []VolumePoint {
	var since string
	var bucketFmt string
	var bucketDuration time.Duration

	switch period {
	case "7d":
		since = "7 days ago"
		bucketFmt = "2006-01-02"
		bucketDuration = 24 * time.Hour
	default:
		since = "24 hours ago"
		bucketFmt = "2006-01-02T15"
		bucketDuration = time.Hour
	}

	lines := readJournalSince(s.serviceName, since)

	type bucket struct {
		sent     int
		received int
	}
	buckets := make(map[string]*bucket)

	for _, line := range lines {
		m := reTimestamp.FindStringSubmatch(line)
		if len(m) < 2 {
			continue
		}
		t, err := time.Parse("2006-01-02T15:04:05", m[1])
		if err != nil {
			continue
		}
		key := t.UTC().Format(bucketFmt)
		if buckets[key] == nil {
			buckets[key] = &bucket{}
		}
		lower := strings.ToLower(line)
		switch {
		case reSent.MatchString(lower):
			buckets[key].sent++
		case reReceived.MatchString(lower):
			buckets[key].received++
		}
	}

	// Build a complete, sorted timeline including empty buckets.
	now := time.Now().UTC()
	var points []VolumePoint

	switch period {
	case "7d":
		for i := 6; i >= 0; i-- {
			t := now.Add(-time.Duration(i) * bucketDuration).Truncate(24 * time.Hour)
			key := t.Format(bucketFmt)
			b := buckets[key]
			pt := VolumePoint{Timestamp: t}
			if b != nil {
				pt.Sent = b.sent
				pt.Received = b.received
			}
			points = append(points, pt)
		}
	default:
		for i := 23; i >= 0; i-- {
			t := now.Add(-time.Duration(i) * bucketDuration).Truncate(time.Hour)
			key := t.Format(bucketFmt)
			b := buckets[key]
			pt := VolumePoint{Timestamp: t}
			if b != nil {
				pt.Sent = b.sent
				pt.Received = b.received
			}
			points = append(points, pt)
		}
	}
	return points
}

// ── GetStorageUsage ───────────────────────────────────────────────────────────

// GetStorageUsage returns per-account storage from the Stalwart principal API.
// Accounts with no quota set are included with QuotaBytes=0.
func (s *Service) GetStorageUsage() ([]AccountStorage, error) {
	accounts, err := s.ListAccounts("")
	if err != nil {
		return nil, fmt.Errorf("storage usage: %w", err)
	}
	result := make([]AccountStorage, 0, len(accounts))
	for _, acc := range accounts {
		pct := 0.0
		if acc.Quota > 0 {
			pct = float64(acc.UsedStorage) / float64(acc.Quota) * 100
		}
		result = append(result, AccountStorage{
			Email:       acc.Email,
			UsedBytes:   acc.UsedStorage,
			QuotaBytes:  acc.Quota,
			UsedPercent: pct,
		})
	}
	// Sort by usage descending
	sort.Slice(result, func(i, j int) bool {
		return result[i].UsedBytes > result[j].UsedBytes
	})
	return result, nil
}

// ── GetDeliverability ─────────────────────────────────────────────────────────

// GetDeliverability computes a delivery success rate from today's journal.
func (s *Service) GetDeliverability() Deliverability {
	lines := readJournalSince(s.serviceName, "today")
	var delivered, failed int

	for _, line := range lines {
		lower := strings.ToLower(line)
		switch {
		case reSent.MatchString(lower):
			delivered++
		case reFailure.MatchString(lower):
			failed++
		}
	}
	total := delivered + failed
	rate := 0.0
	if total > 0 {
		rate = float64(delivered) / float64(total) * 100
	}
	return Deliverability{
		TotalAttempts: total,
		Delivered:     delivered,
		Failed:        failed,
		SuccessRate:   rate,
		Period:        "today",
	}
}

// ── Journal helpers ───────────────────────────────────────────────────────────

// readJournalSince returns journal lines for an explicitly configured service
// using an arbitrary --since value (e.g. "24 hours ago" or "today").
func readJournalSince(serviceName, since string) []string {
	serviceName = strings.TrimSpace(serviceName)
	if serviceName == "" {
		return nil
	}
	args := []string{
		"-u", serviceName,
		"--no-pager",
		"--output=short-iso",
		"--since", since,
	}
	out, err := exec.Command("journalctl", args...).Output()
	if err != nil {
		// journalctl not available or service not found — return empty
		return nil
	}
	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(out))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

// ── Utility helpers ───────────────────────────────────────────────────────────

// normalizeAddr cleans angle-bracket quoting and trailing punctuation from an
// address token extracted by regex.
func normalizeAddr(raw string) string {
	raw = strings.Trim(raw, "<>\"',;")
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "@") {
		return ""
	}
	return strings.ToLower(raw)
}

// topN converts a count map to a sorted []SenderStat slice limited to n items.
func topN(counts map[string]int, n int, less func(a, b SenderStat) bool) []SenderStat {
	result := make([]SenderStat, 0, len(counts))
	for addr, cnt := range counts {
		result = append(result, SenderStat{Address: addr, Count: cnt})
	}
	sort.Slice(result, func(i, j int) bool { return less(result[i], result[j]) })
	if len(result) > n {
		result = result[:n]
	}
	return result
}
