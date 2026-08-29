package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	defaultLogLines = 200
	maxLogLines     = 500
)

var journalUnits = map[string]string{
	"system":     "",
	"nginx":      "nginx.service",
	"php":        "php*-fpm.service",
	"mariadb":    "mariadb.service",
	"postgresql": "postgresql*.service",
	"pm2":        "pm2-*.service",
	"docker":     "docker.service",
}

type journalEntry struct {
	Timestamp string `json:"timestamp"`
	Unit      string `json:"unit"`
	Priority  int    `json:"priority"`
	Message   string `json:"message"`
}

type journalReader struct {
	runner  commandRunner
	allowed map[string]struct{}
}

func newJournalReader(runner commandRunner, allowed map[string]struct{}) journalReader {
	return journalReader{runner: runner, allowed: allowed}
}

func (r journalReader) Read(ctx context.Context, source string, lines int) ([]journalEntry, error) {
	unit, known := journalUnits[source]
	_, allowed := r.allowed[source]
	if !known || !allowed {
		return nil, errors.New("log source is not in the local allowlist")
	}
	if lines < 1 {
		lines = defaultLogLines
	}
	if lines > maxLogLines {
		return nil, fmt.Errorf("log line count exceeds %d", maxLogLines)
	}
	args := []string{"--no-pager", "-o", "json", "--lines=" + strconv.Itoa(lines)}
	if unit != "" {
		args = append(args, "-u", unit)
	}
	commandCtx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	output, err := r.runner.run(commandCtx, "journalctl", args...)
	if err != nil {
		return nil, fmt.Errorf("journalctl read failed: %w", err)
	}
	return parseJournalEntries(output, lines)
}

func parseJournalEntries(output []byte, limit int) ([]journalEntry, error) {
	entries := make([]journalEntry, 0, limit)
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	scanner.Buffer(make([]byte, 64*1024), maxCommandOutputBytes)
	for scanner.Scan() {
		var row struct {
			Timestamp  string `json:"__REALTIME_TIMESTAMP"`
			Unit       string `json:"_SYSTEMD_UNIT"`
			Identifier string `json:"SYSLOG_IDENTIFIER"`
			Priority   string `json:"PRIORITY"`
			Message    string `json:"MESSAGE"`
		}
		if json.Unmarshal(scanner.Bytes(), &row) != nil || row.Message == "" {
			continue
		}
		priority, _ := strconv.Atoi(row.Priority)
		entryUnit := row.Unit
		if entryUnit == "" {
			entryUnit = row.Identifier
		}
		timestamp := row.Timestamp
		if micros, err := strconv.ParseInt(row.Timestamp, 10, 64); err == nil {
			timestamp = time.UnixMicro(micros).UTC().Format(time.RFC3339Nano)
		}
		entries = append(entries, journalEntry{Timestamp: timestamp, Unit: entryUnit, Priority: priority, Message: row.Message})
		if len(entries) == limit {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("parse journal output: %w", err)
	}
	return entries, nil
}
