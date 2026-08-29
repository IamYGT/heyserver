package php

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"
)

// ErrStatusNotConfigured is returned when a pool has no pm.status_path set.
var ErrStatusNotConfigured = errors.New("pool has no pm.status_path configured; add pm.status_path = /fpm-status to the pool conf")

// readFileString reads a file and returns its content as a string.
func readFileString(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	return string(data), nil
}

// writeFileString writes content string to a file with 0644 permissions.
func writeFileString(path, content string) error {
	return os.WriteFile(path, []byte(content), 0644)
}

// PoolStatus holds runtime metrics for a single PHP-FPM pool.
type PoolStatus struct {
	Domain    string `json:"domain"`
	Version   string `json:"version"`
	Pool      string `json:"pool"`
	PM        string `json:"process_manager"`
	StartTime int64  `json:"start_time"`

	AcceptedConn       int64 `json:"accepted_conn"`
	ListenQueue        int   `json:"listen_queue"`
	MaxListenQueue     int   `json:"max_listen_queue"`
	IdleProcesses      int   `json:"idle_processes"`
	ActiveProcesses    int   `json:"active_processes"`
	TotalProcesses     int   `json:"total_processes"`
	MaxActiveProcesses int   `json:"max_active_processes"`
	MaxChildrenReached int   `json:"max_children_reached"`
	SlowRequests       int   `json:"slow_requests"`

	// Health indicators (calculated from above fields)
	HealthScore int      `json:"health_score"`
	Warnings    []string `json:"warnings"`

	// Per-process details (populated when ?full is used)
	Processes []ProcessInfo `json:"processes,omitempty"`
}

// ProcessInfo holds per-process details from the FPM status ?full output.
type ProcessInfo struct {
	PID               int    `json:"pid"`
	State             string `json:"state"`
	StartTime         int64  `json:"start_time"`
	Requests          int    `json:"requests"`
	RequestDuration   int    `json:"request_duration"`
	RequestMethod     string `json:"request_method"`
	RequestURI        string `json:"request_uri"`
	ContentLength     int    `json:"content_length"`
	User              string `json:"user"`
	Script            string `json:"script"`
	LastRequestMemory int    `json:"last_request_memory"`
}

// fpmStatusJSON is the raw structure returned by FPM's JSON status endpoint.
type fpmStatusJSON struct {
	Pool               string `json:"pool"`
	ProcessManager     string `json:"process manager"`
	StartTime          int64  `json:"start time"`
	AcceptedConn       int64  `json:"accepted conn"`
	ListenQueue        int    `json:"listen queue"`
	MaxListenQueue     int    `json:"max listen queue"`
	IdleProcesses      int    `json:"idle processes"`
	ActiveProcesses    int    `json:"active processes"`
	TotalProcesses     int    `json:"total processes"`
	MaxActiveProcesses int    `json:"max active processes"`
	MaxChildrenReached int    `json:"max children reached"`
	SlowRequests       int    `json:"slow requests"`
	Processes          []struct {
		PID               int    `json:"pid"`
		State             string `json:"state"`
		StartTime         int64  `json:"start time"`
		Requests          int    `json:"requests"`
		RequestDuration   int    `json:"request duration"`
		RequestMethod     string `json:"request method"`
		RequestURI        string `json:"request uri"`
		ContentLength     int    `json:"content length"`
		User              string `json:"user"`
		Script            string `json:"script"`
		LastRequestMemory int    `json:"last request memory"`
	} `json:"processes"`
}

// GetPoolStatus queries the FPM status endpoint for a specific pool via its
// Unix socket. The poolName parameter must match the pool's [section] name in
// the conf file (e.g. "turbo24", not the domain filename).
//
// The pool must have pm.status_path = /fpm-status in its configuration.
// If it does not, ErrStatusNotConfigured is returned.
func (s *Service) GetPoolStatus(version, poolName string) (*PoolStatus, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	if err := validatePoolName(poolName); err != nil {
		return nil, err
	}

	// Resolve pool config to get socket path and verify status_path is set.
	pool, err := s.resolvePoolByName(version, poolName)
	if err != nil {
		return nil, fmt.Errorf("resolving pool %s@php%s: %w", poolName, version, err)
	}

	statusConfigured := false
	if v, ok := pool.RawLines["pm.status_path"]; ok && strings.TrimSpace(v) != "" {
		statusConfigured = true
	}
	if !statusConfigured {
		return nil, ErrStatusNotConfigured
	}

	socketPath := pool.Listen
	if !strings.HasPrefix(socketPath, "/") {
		return nil, fmt.Errorf("pool %s uses a TCP listener (%s); only Unix sockets are supported", poolName, socketPath)
	}

	raw, err := fcgiQuery(socketPath, "/fpm-status", "json&full", 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("fcgi query to %s: %w", socketPath, err)
	}

	var fpmData fpmStatusJSON
	if err := json.Unmarshal(raw, &fpmData); err != nil {
		return nil, fmt.Errorf("parsing fpm status JSON: %w", err)
	}

	status := buildPoolStatus(version, poolName, &fpmData)
	return status, nil
}

// GetAllPoolsStatus queries all pools for a given PHP version and returns
// their statuses. Pools without pm.status_path are skipped (not an error).
func (s *Service) GetAllPoolsStatus(version string) ([]PoolStatus, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}

	pools, err := s.ListPools(version)
	if err != nil {
		return nil, fmt.Errorf("listing pools for php%s: %w", version, err)
	}

	var results []PoolStatus
	for _, pool := range pools {
		status, err := s.GetPoolStatus(version, pool.Name)
		if err != nil {
			if errors.Is(err, ErrStatusNotConfigured) {
				continue // silently skip unconfigured pools
			}
			// Non-fatal: include a degraded entry so callers can see the error.
			results = append(results, PoolStatus{
				Domain:      pool.Name,
				Version:     version,
				Pool:        pool.Name,
				HealthScore: 0,
				Warnings:    []string{err.Error()},
			})
			continue
		}
		results = append(results, *status)
	}
	return results, nil
}

// EnablePoolStatus adds pm.status_path = /fpm-status and
// pm.status_listen_allowed_clients = 127.0.0.1 to a pool config file so that
// GetPoolStatus can subsequently be called. The FPM service must be reloaded
// after calling this.
func (s *Service) EnablePoolStatus(version, poolName string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	if err := validatePoolName(poolName); err != nil {
		return err
	}

	pool, err := s.resolvePoolByName(version, poolName)
	if err != nil {
		return err
	}

	if _, ok := pool.RawLines["pm.status_path"]; ok {
		return nil // already configured
	}

	confPath := pool.ConfigFile
	content, err := readFileString(confPath)
	if err != nil {
		return fmt.Errorf("reading pool config: %w", err)
	}

	// Append the status directive before the first blank-line section break or
	// at the end of the file.
	addition := "\n; FPM status endpoint\npm.status_path = /fpm-status\n"
	content += addition

	return writeFileString(confPath, content)
}

// ---------- internal helpers ----------

// resolvePoolByName finds the pool conf file for a given version+poolName
// pair by scanning all pool files and matching the [section] header name.
func (s *Service) resolvePoolByName(version, poolName string) (*Pool, error) {
	pools, err := s.ListPools(version)
	if err != nil {
		return nil, err
	}
	for i := range pools {
		if pools[i].Name == poolName {
			return &pools[i], nil
		}
	}
	return nil, fmt.Errorf("pool [%s] not found in php%s pool configs", poolName, version)
}

// buildPoolStatus converts raw FPM JSON data into a PoolStatus with health
// score and warnings computed.
func buildPoolStatus(version, poolName string, d *fpmStatusJSON) *PoolStatus {
	ps := &PoolStatus{
		Domain:             poolName,
		Version:            version,
		Pool:               d.Pool,
		PM:                 d.ProcessManager,
		StartTime:          d.StartTime,
		AcceptedConn:       d.AcceptedConn,
		ListenQueue:        d.ListenQueue,
		MaxListenQueue:     d.MaxListenQueue,
		IdleProcesses:      d.IdleProcesses,
		ActiveProcesses:    d.ActiveProcesses,
		TotalProcesses:     d.TotalProcesses,
		MaxActiveProcesses: d.MaxActiveProcesses,
		MaxChildrenReached: d.MaxChildrenReached,
		SlowRequests:       d.SlowRequests,
	}

	// Compute health score.
	score := 100
	var warnings []string

	if d.ListenQueue > 0 {
		score -= 30
		warnings = append(warnings, fmt.Sprintf("listen queue is %d (pool exhausted, requests are queuing)", d.ListenQueue))
	}
	if d.MaxChildrenReached > 0 {
		score -= 20
		warnings = append(warnings, fmt.Sprintf("max_children_reached = %d (process limit was hit)", d.MaxChildrenReached))
	}
	if d.TotalProcesses > 0 {
		utilization := float64(d.ActiveProcesses) / float64(d.TotalProcesses)
		if utilization >= 0.8 {
			score -= 15
			warnings = append(warnings, fmt.Sprintf("pool near capacity: %d/%d processes active (%.0f%%)",
				d.ActiveProcesses, d.TotalProcesses, utilization*100))
		}
	}
	if d.SlowRequests > 10 {
		score -= 10
		warnings = append(warnings, fmt.Sprintf("slow requests: %d", d.SlowRequests))
	}
	if score < 0 {
		score = 0
	}

	ps.HealthScore = score
	ps.Warnings = warnings

	// Map process details.
	for _, p := range d.Processes {
		ps.Processes = append(ps.Processes, ProcessInfo{
			PID:               p.PID,
			State:             p.State,
			StartTime:         p.StartTime,
			Requests:          p.Requests,
			RequestDuration:   p.RequestDuration,
			RequestMethod:     p.RequestMethod,
			RequestURI:        p.RequestURI,
			ContentLength:     p.ContentLength,
			User:              p.User,
			Script:            p.Script,
			LastRequestMemory: p.LastRequestMemory,
		})
	}

	return ps
}

// ---------- Minimal FastCGI client ----------
//
// PHP-FPM's status page is served over the FastCGI protocol. Go's standard
// library does not include an FCGI *client*, only an FCGI *server* (net/http/fcgi).
// Rather than pulling in an external dependency we implement just enough of the
// FastCGI protocol (RFC 3875 + FastCGI spec v1.0) to send a single GET-style
// request and read back the response body.
//
// Message layout (per FastCGI spec §3.3):
//   - 1 FCGI_BEGIN_REQUEST record  (role = RESPONDER)
//   - 1 FCGI_PARAMS record         (SCRIPT_NAME, QUERY_STRING, REQUEST_METHOD …)
//   - 1 FCGI_PARAMS record         (empty — signals end of params)
//   - 1 FCGI_STDIN  record         (empty — GET has no body)
//
// We then read FCGI_STDOUT records until we receive an FCGI_END_REQUEST or EOF.

const (
	fcgiVersion       = 1
	fcgiBeginRequest  = 1
	fcgiAbortRequest  = 2
	fcgiEndRequest    = 3
	fcgiParams        = 4
	fcgiStdin         = 5
	fcgiStdout        = 6
	fcgiStderr        = 7
	fcgiRoleResponder = 1
)

// fcgiHeader is an 8-byte FastCGI record header.
type fcgiHeader struct {
	Version       uint8
	Type          uint8
	RequestIDHigh uint8
	RequestIDLow  uint8
	ContentLenH   uint8
	ContentLenL   uint8
	PaddingLen    uint8
	Reserved      uint8
}

func (h fcgiHeader) contentLength() int {
	return int(h.ContentLenH)<<8 | int(h.ContentLenL)
}

// fcgiQuery sends a minimal FCGI GET request to socketPath for scriptName with
// queryString, returning the response body (headers stripped).
func fcgiQuery(socketPath, scriptName, queryString string, timeout time.Duration) ([]byte, error) {
	conn, err := net.DialTimeout("unix", socketPath, timeout)
	if err != nil {
		return nil, fmt.Errorf("dial unix socket: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, err
	}

	const requestID = 1

	// Build FCGI_BEGIN_REQUEST (role=RESPONDER, flags=0 means close-on-done).
	beginBody := [8]byte{
		0, fcgiRoleResponder, // role
		0,                   // flags (FCGI_KEEP_CONN = 0)
		0, 0, 0, 0, 0,       // reserved
	}
	if err := writeFCGIRecord(conn, fcgiBeginRequest, requestID, beginBody[:]); err != nil {
		return nil, fmt.Errorf("write BEGIN_REQUEST: %w", err)
	}

	// Build FCGI_PARAMS.
	params := encodeFCGIParams(map[string]string{
		"REQUEST_METHOD":  "GET",
		"SCRIPT_NAME":     scriptName,
		"SCRIPT_FILENAME": scriptName,
		"QUERY_STRING":    queryString,
		"SERVER_SOFTWARE": "hserver-panel/fcgi-client",
		"REMOTE_ADDR":     "127.0.0.1",
		"SERVER_NAME":     "localhost",
		"SERVER_PORT":     "80",
		"SERVER_PROTOCOL": "HTTP/1.1",
	})
	if err := writeFCGIRecord(conn, fcgiParams, requestID, params); err != nil {
		return nil, fmt.Errorf("write PARAMS: %w", err)
	}
	// Empty PARAMS record signals end of parameters.
	if err := writeFCGIRecord(conn, fcgiParams, requestID, nil); err != nil {
		return nil, fmt.Errorf("write PARAMS end: %w", err)
	}
	// Empty STDIN record signals end of input (GET request has no body).
	if err := writeFCGIRecord(conn, fcgiStdin, requestID, nil); err != nil {
		return nil, fmt.Errorf("write STDIN: %w", err)
	}

	// Read response records until END_REQUEST.
	var stdout []byte
	buf := make([]byte, 8)
	for {
		if _, err := io.ReadFull(conn, buf); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("read record header: %w", err)
		}

		hdr := fcgiHeader{
			Version:       buf[0],
			Type:          buf[1],
			RequestIDHigh: buf[2],
			RequestIDLow:  buf[3],
			ContentLenH:   buf[4],
			ContentLenL:   buf[5],
			PaddingLen:    buf[6],
		}

		contentLen := hdr.contentLength()
		totalRead := contentLen + int(hdr.PaddingLen)
		body := make([]byte, totalRead)
		if totalRead > 0 {
			if _, err := io.ReadFull(conn, body); err != nil {
				return nil, fmt.Errorf("read record body: %w", err)
			}
		}

		switch hdr.Type {
		case fcgiStdout:
			if contentLen > 0 {
				stdout = append(stdout, body[:contentLen]...)
			}
		case fcgiStderr:
			// FPM errors — surface as an error if content is non-empty.
			if contentLen > 0 {
				return nil, fmt.Errorf("fpm returned stderr: %s", strings.TrimSpace(string(body[:contentLen])))
			}
		case fcgiEndRequest:
			goto done
		}
	}
done:
	// stdout begins with HTTP headers followed by \r\n\r\n and then the body.
	return stripHTTPHeaders(stdout), nil
}

// writeFCGIRecord serialises a single FastCGI record to w.
func writeFCGIRecord(w io.Writer, recordType uint8, requestID int, content []byte) error {
	contentLen := len(content)
	if contentLen > 65535 {
		return fmt.Errorf("FCGI record content too large: %d bytes", contentLen)
	}

	hdr := [8]byte{
		fcgiVersion,
		recordType,
		uint8(requestID >> 8),
		uint8(requestID),
		uint8(contentLen >> 8),
		uint8(contentLen),
		0, // padding length
		0, // reserved
	}

	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if len(content) > 0 {
		if _, err := w.Write(content); err != nil {
			return err
		}
	}
	return nil
}

// encodeFCGIParams encodes a map of name-value pairs using FastCGI's
// length-prefixed encoding (§3.4 of the FastCGI spec).
func encodeFCGIParams(params map[string]string) []byte {
	var buf []byte
	for k, v := range params {
		buf = append(buf, encodeFCGILength(len(k))...)
		buf = append(buf, encodeFCGILength(len(v))...)
		buf = append(buf, k...)
		buf = append(buf, v...)
	}
	return buf
}

// encodeFCGILength encodes a FastCGI name/value length:
//   - values < 128 are encoded as 1 byte
//   - values >= 128 are encoded as 4 bytes with the high bit of byte[0] set
func encodeFCGILength(n int) []byte {
	if n < 128 {
		return []byte{uint8(n)}
	}
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(n)|0x80000000)
	return b
}

// stripHTTPHeaders removes the HTTP header section (up to and including the
// blank line) from an FCGI STDOUT payload, returning only the body.
func stripHTTPHeaders(data []byte) []byte {
	// Look for \r\n\r\n (standard) or \n\n (some FPM builds).
	if idx := strings.Index(string(data), "\r\n\r\n"); idx >= 0 {
		return data[idx+4:]
	}
	if idx := strings.Index(string(data), "\n\n"); idx >= 0 {
		return data[idx+2:]
	}
	return data
}
