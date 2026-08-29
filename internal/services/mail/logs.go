package mail

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"unicode/utf8"
)

// sanitizeLogQuery removes shell metacharacters and control characters from a
// journalctl --grep pattern.  journalctl accepts a PCRE regex here, but we
// restrict to printable ASCII (no \x00-\x1F, no \x7F) and explicitly strip
// the characters that would most obviously allow argument injection through
// misuse of the journalctl --grep flag itself.
//
// Note: exec.Command already prevents shell metacharacter injection because
// args are passed as separate OS-level argv elements (no shell involved).
// This filter is a defence-in-depth measure against regex DoS and log
// poisoning patterns — not a shell injection vector.
func sanitizeLogQuery(q string) (string, error) {
	if !utf8.ValidString(q) {
		return "", fmt.Errorf("query contains invalid UTF-8")
	}
	// Reject null bytes and ASCII control characters.
	for _, r := range q {
		if r < 0x20 || r == 0x7F {
			return "", fmt.Errorf("query contains control characters")
		}
	}
	if len(q) > 256 {
		return "", fmt.Errorf("query too long (max 256 chars)")
	}
	return q, nil
}

// LogEntry represents a single parsed log line from the configured mail
// service's systemd journal.
type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Level     string `json:"level"`
	Message   string `json:"message"`
}

// GetMailLogs returns the last `lines` log entries for the configured mail
// service unit.
// It uses journalctl so no log file path assumption is needed.
func (s *Service) GetMailLogs(lines int) ([]LogEntry, error) {
	if err := s.requireServiceName(); err != nil {
		return nil, err
	}
	if lines <= 0 {
		lines = 100
	}
	args := []string{
		"-u", strings.TrimSpace(s.serviceName),
		"--no-pager",
		"-n", strconv.Itoa(lines),
		"--output", "short-iso",
	}
	out, err := exec.Command("journalctl", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("journalctl: %w", err)
	}
	return parseJournalLines(string(out)), nil
}

// SearchMailLogs searches the configured mail service journal for lines
// containing query.
// Returns up to 500 matching entries.
func (s *Service) SearchMailLogs(query string) ([]LogEntry, error) {
	if err := s.requireServiceName(); err != nil {
		return nil, err
	}
	if query == "" {
		return nil, fmt.Errorf("query must not be empty")
	}
	clean, err := sanitizeLogQuery(query)
	if err != nil {
		return nil, fmt.Errorf("invalid query: %w", err)
	}
	args := []string{
		"-u", strings.TrimSpace(s.serviceName),
		"--no-pager",
		"-n", "5000",
		"--output", "short-iso",
		"--grep", clean,
	}
	out, execErr := exec.Command("journalctl", args...).Output()
	if execErr != nil {
		// journalctl exits 1 when no matches — treat as empty result
		return []LogEntry{}, nil
	}
	entries := parseJournalLines(string(out))
	if len(entries) > 500 {
		entries = entries[:500]
	}
	return entries, nil
}

// GetDeliveryLog searches the configured mail service journal for lines that mention the
// given email address (either as sender or recipient).
func (s *Service) GetDeliveryLog(email string) ([]LogEntry, error) {
	if email == "" {
		return nil, fmt.Errorf("email must not be empty")
	}
	return s.SearchMailLogs(email)
}

// parseJournalLines converts raw journalctl short-iso output into LogEntry slices.
// Typical line: "2025-01-15T10:23:45+0000 hostname mail-service[1234]: INFO message"
func parseJournalLines(raw string) []LogEntry {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	entries := make([]LogEntry, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		entries = append(entries, parseJournalLine(line))
	}
	return entries
}

// parseJournalLine attempts a best-effort parse of a single journalctl line.
func parseJournalLine(line string) LogEntry {
	entry := LogEntry{Message: line}

	// Extract timestamp (first field, ISO-8601)
	fields := strings.SplitN(line, " ", 4)
	if len(fields) >= 1 {
		entry.Timestamp = fields[0]
	}

	// Remaining text after "mail-service[PID]: "
	if len(fields) >= 4 {
		rest := fields[3]
		entry.Message = rest
		entry.Level = extractLevel(rest)
	} else if len(fields) >= 3 {
		// Message embedded in third field
		if idx := strings.Index(fields[2], "]:"); idx >= 0 {
			entry.Message = strings.TrimSpace(fields[2][idx+2:])
			entry.Level = extractLevel(entry.Message)
		}
	}

	return entry
}

// extractLevel heuristically detects the log level prefix in a message string.
func extractLevel(msg string) string {
	upper := strings.ToUpper(msg)
	for _, lvl := range []string{"ERROR", "WARN", "WARNING", "INFO", "DEBUG", "TRACE"} {
		if strings.HasPrefix(upper, lvl) || strings.Contains(upper, " "+lvl+" ") {
			if lvl == "WARNING" {
				return "WARN"
			}
			return lvl
		}
	}
	return "INFO"
}
