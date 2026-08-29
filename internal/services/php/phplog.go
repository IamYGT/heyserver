package php

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// SlowLogEntry represents a single entry from a PHP-FPM slow request log.
type SlowLogEntry struct {
	Timestamp time.Time `json:"timestamp"`
	Script    string    `json:"script"`
	Duration  float64   `json:"duration"` // seconds
	Backtrace []string  `json:"backtrace"`
}

// GetSlowLog returns the last n entries from the FPM slow log for a specific
// pool. Slow logs are only written when pm.request_slowlog_timeout is set in
// the pool config.
//
// Log path: /var/log/php{version}-fpm-{poolName}-slow.log
func (s *Service) GetSlowLog(version, poolName string, lines int) ([]SlowLogEntry, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	if err := validatePoolName(poolName); err != nil {
		return nil, err
	}
	if lines <= 0 {
		lines = 50
	}

	logPath := fmt.Sprintf("/var/log/php%s-fpm-%s-slow.log", version, poolName)
	return parseSlowLog(logPath, lines)
}

// GetErrorLog returns the last n lines from the FPM error log for a given
// PHP version.
//
// Log path: /var/log/php{version}-fpm.log
func (s *Service) GetErrorLog(version string, lines int) ([]string, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	if lines <= 0 {
		lines = 100
	}

	logPath := fmt.Sprintf("/var/log/php%s-fpm.log", version)
	return tailFile(logPath, lines)
}

// GetAccessLog returns the last n lines from the FPM access log for a specific
// pool. Access logs are only written when access.log is set in the pool config.
//
// Log path: /var/log/php{version}-fpm-{poolName}-access.log
func (s *Service) GetAccessLog(version, poolName string, lines int) ([]string, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	if err := validatePoolName(poolName); err != nil {
		return nil, err
	}
	if lines <= 0 {
		lines = 100
	}

	logPath := fmt.Sprintf("/var/log/php%s-fpm-%s-access.log", version, poolName)
	return tailFile(logPath, lines)
}

// ---------- parsers ----------

// parseSlowLog reads a PHP-FPM slow log file and returns parsed entries.
//
// Slow log format (each entry is a multi-line block):
//
//	[02-Jan-2006 15:04:05]  [pool www] pid 1234
//	script_filename = /path/to/script.php
//	[0x00007f...] function() /path/to/script.php:123
//	...
//	[blank line]
func parseSlowLog(logPath string, maxEntries int) ([]SlowLogEntry, error) {
	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []SlowLogEntry{}, nil // no slow log file = no slow requests
		}
		return nil, fmt.Errorf("opening slow log %s: %w", logPath, err)
	}
	defer func() { _ = f.Close() }()

	var all []SlowLogEntry
	var current *SlowLogEntry

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()

		// Entry header: starts with "[DD-Mon-YYYY HH:MM:SS]"
		if strings.HasPrefix(line, "[") && strings.Contains(line, "[pool ") {
			if current != nil {
				all = append(all, *current)
			}
			current = &SlowLogEntry{}
			current.Timestamp = parseSlowLogTimestamp(line)
			current.Duration = parseSlowLogDuration(line)
			continue
		}

		if current == nil {
			continue
		}

		// Script filename line.
		if strings.HasPrefix(line, "script_filename = ") {
			current.Script = strings.TrimPrefix(line, "script_filename = ")
			continue
		}

		// Blank line closes current entry.
		if strings.TrimSpace(line) == "" {
			all = append(all, *current)
			current = nil
			continue
		}

		// Backtrace frame: starts with "[0x" or a frame index.
		if strings.HasPrefix(line, "[0x") || (len(line) > 0 && line[0] == '[') {
			current.Backtrace = append(current.Backtrace, line)
		}
	}

	// Flush any trailing entry.
	if current != nil {
		all = append(all, *current)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning slow log: %w", err)
	}

	// Return the last maxEntries entries.
	if len(all) > maxEntries {
		all = all[len(all)-maxEntries:]
	}
	return all, nil
}

// parseSlowLogTimestamp extracts the timestamp from the slow log header line.
// Format: [02-Jan-2006 15:04:05]  [pool www] pid 1234
func parseSlowLogTimestamp(line string) time.Time {
	if !strings.HasPrefix(line, "[") {
		return time.Time{}
	}
	end := strings.Index(line, "]")
	if end < 0 {
		return time.Time{}
	}
	ts := line[1:end]
	t, err := time.Parse("02-Jan-2006 15:04:05", ts)
	if err != nil {
		return time.Time{}
	}
	return t
}

// parseSlowLogDuration extracts the request duration (in seconds) from a slow
// log header line. FPM writes the duration in the header after the pool name:
//   [DD-Mon-YYYY HH:MM:SS]  [pool www] pid 1234, duration = 5.234 seconds
// If no duration is found, 0 is returned.
func parseSlowLogDuration(line string) float64 {
	const marker = "duration = "
	idx := strings.Index(line, marker)
	if idx < 0 {
		return 0
	}
	rest := line[idx+len(marker):]
	// rest may look like "5.234 seconds" or just "5.234"
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return 0
	}
	d, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0
	}
	return d
}

// tailFile reads the last n lines from a text file efficiently using a
// line-buffer approach.
func tailFile(path string, lines int) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, fmt.Errorf("opening log %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	// Read all lines into a circular buffer of size `lines`.
	ring := make([]string, lines)
	idx := 0
	total := 0

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 128*1024), 128*1024) // 128 KB per line
	for scanner.Scan() {
		ring[idx%lines] = scanner.Text()
		idx++
		total++
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning log %s: %w", path, err)
	}

	if total == 0 {
		return []string{}, nil
	}

	// Reconstruct ordered slice from circular buffer.
	result := make([]string, 0, min(total, lines))
	if total <= lines {
		result = append(result, ring[:total]...)
	} else {
		// Start from oldest entry: index where we last wrote + 1.
		start := idx % lines
		for i := 0; i < lines; i++ {
			result = append(result, ring[(start+i)%lines])
		}
	}
	return result, nil
}

// min returns the smaller of a and b.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
