package logs

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ── Allowed directories whitelist ────────────────────────────────────────────
// Only files under these directories may be read.  The list is intentionally
// conservative — never allow arbitrary filesystem access.

var staticAllowedDirs = []string{
	"/var/log/nginx",
	"/var/log/php",
	"/var/log/postgresql",
	"/var/log", // syslog, auth.log, kern.log, etc. — restricted further below
}

var versionedPHPLogPathRe = regexp.MustCompile(`^/var/log/php[0-9]+\.[0-9]+-fpm(?:\.log|/[^/]+\.log)$`)

// allowedLogFileSuffixes restricts generic /var/log access to known safe files.
var allowedLogFileSuffixes = []string{
	"syslog", "auth.log", "kern.log", "dpkg.log", "daemon.log",
	"fail2ban.log", "ufw.log", "apt/history.log",
}

const maxDownloadBytes = 50 * 1024 * 1024 // 50 MB

// ── Models ────────────────────────────────────────────────────────────────────

// LogSource describes a discoverable log file.
type LogSource struct {
	Path         string    `json:"path"`
	Category     string    `json:"category"` // nginx, php, app, pm2, postgresql, system
	Label        string    `json:"label"`
	SizeBytes    int64     `json:"sizeBytes"`
	LastModified time.Time `json:"lastModified"`
	Readable     bool      `json:"readable"`
}

// LogLines holds the result of a tail or search operation.
type LogLines struct {
	Path  string   `json:"path"`
	Lines []string `json:"lines"`
	Total int      `json:"total"`
}

// ── Service ───────────────────────────────────────────────────────────────────

// Config binds application and PM2 log access to installation-owned roots.
// PM2User must name the configured unprivileged daemon owner. PM2Home may
// override that account's default ~/.pm2 directory.
type Config struct {
	VhostsRoot string
	PM2User    string
	PM2Home    string
}

// Service provides log-related operations within fixed system locations and
// the installation-owned application/PM2 roots.
type Service struct {
	vhostsRoot string
	pm2User    string
	pm2LogRoot string
}

// New returns a new installation-scoped Service.
func New(config Config) *Service {
	vhostsRoot := cleanAbsoluteRoot(config.VhostsRoot)

	pm2User := strings.TrimSpace(config.PM2User)
	pm2Home := cleanAbsoluteRoot(config.PM2Home)
	if pm2User == "" || pm2User == "root" {
		pm2Home = ""
	} else if pm2Home == "" && strings.TrimSpace(config.PM2Home) == "" {
		if account, err := user.Lookup(pm2User); err == nil {
			pm2Home = cleanAbsoluteRoot(filepath.Join(account.HomeDir, ".pm2"))
		}
	}

	pm2LogRoot := ""
	if pm2Home != "" {
		pm2LogRoot = filepath.Join(pm2Home, "logs")
	}
	return &Service{vhostsRoot: vhostsRoot, pm2User: pm2User, pm2LogRoot: pm2LogRoot}
}

func cleanAbsoluteRoot(path string) string {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return ""
	}
	return filepath.Clean(path)
}

// ── Path security ─────────────────────────────────────────────────────────────

// IsAllowed returns true when the resolved (clean) path is inside one of the
// whitelisted directories and does not escape via symlink traversal.
func (s *Service) IsAllowed(rawPath string) bool {
	clean := filepath.Clean(rawPath)

	// Prevent null bytes and suspicious characters.
	if strings.ContainsAny(clean, "\x00") {
		return false
	}

	// Resolve symlinks to prevent escape via /var/log/nginx/evil.log → /etc/shadow.
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		clean = filepath.Clean(resolved)
	}
	// If EvalSymlinks fails (file doesn't exist yet), continue with the cleaned path.

	// Check against whitelisted prefixes.
	for _, dir := range staticAllowedDirs {
		if !strings.HasPrefix(clean, dir+"/") && clean != dir {
			continue
		}

		// /var/log root is whitelisted only for specific filenames.
		if dir == "/var/log" {
			if hasAllowedLogSuffix(clean) {
				return true
			}
			continue
		}

		return true
	}
	if versionedPHPLogPathRe.MatchString(filepath.ToSlash(clean)) {
		return true
	}

	// Application roots expose only framework storage/logs files.
	if pathWithin(clean, s.vhostsRoot) {
		rel, err := filepath.Rel(s.vhostsRoot, clean)
		if err == nil && strings.Contains(filepath.ToSlash(rel), "/storage/logs/") {
			return true
		}
	}

	// PM2 logs are limited to the single configured unprivileged daemon home.
	if pathWithin(clean, s.pm2LogRoot) {
		return true
	}
	return false
}

func pathWithin(path, root string) bool {
	if root == "" {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func hasAllowedLogSuffix(path string) bool {
	for _, suffix := range allowedLogFileSuffixes {
		if strings.HasSuffix(path, "/"+suffix) || path == "/var/log/"+suffix {
			return true
		}
	}
	return false
}

// ── Discovery ─────────────────────────────────────────────────────────────────

// ListSources discovers all readable log files from well-known locations.
func (s *Service) ListSources() ([]LogSource, error) {
	var sources []LogSource

	// Nginx logs
	sources = append(sources, s.glob("/var/log/nginx", "*.log", "nginx")...)

	// PHP-FPM logs (multiple versions)
	sources = append(sources, s.glob("/var/log/php", "*.log", "php")...)
	for _, v := range []string{"7.4", "8.0", "8.1", "8.2", "8.3", "8.4", "8.5"} {
		sources = append(sources, s.glob(fmt.Sprintf("/var/log/php%s-fpm", v), "*.log", "php")...)
		// Ondrej PPA path variant
		sources = append(sources, s.singleFile(
			fmt.Sprintf("/var/log/php%s-fpm.log", v), "php",
			fmt.Sprintf("PHP-FPM %s", v),
		)...)
	}

	// PostgreSQL
	sources = append(sources, s.glob("/var/log/postgresql", "*.log", "postgresql")...)

	// System logs
	for _, name := range allowedLogFileSuffixes {
		sources = append(sources, s.singleFile(
			filepath.Join("/var/log", name), "system", "")...)
	}

	// Laravel app logs under the installation-owned vhost root.
	if s.vhostsRoot != "" {
		appLogs, _ := filepath.Glob(filepath.Join(s.vhostsRoot, "*", "storage", "logs", "*.log"))
		for _, p := range appLogs {
			sources = append(sources, s.buildSource(p, "app", ""))
		}
	}

	// PM2 logs from the single configured unprivileged daemon owner.
	if s.pm2LogRoot != "" {
		sources = append(sources, s.glob(s.pm2LogRoot, "*.log", "pm2")...)
	}

	// Deduplicate (same path may appear via multiple globs)
	seen := make(map[string]struct{}, len(sources))
	unique := sources[:0]
	for _, src := range sources {
		if _, ok := seen[src.Path]; !ok {
			seen[src.Path] = struct{}{}
			unique = append(unique, src)
		}
	}

	return unique, nil
}

func (s *Service) glob(dir, pattern, category string) []LogSource {
	matches, _ := filepath.Glob(filepath.Join(dir, pattern))
	var out []LogSource
	for _, p := range matches {
		out = append(out, s.buildSource(p, category, ""))
	}
	return out
}

func (s *Service) singleFile(path, category, label string) []LogSource {
	src := s.buildSource(path, category, label)
	if !src.Readable {
		return nil
	}
	return []LogSource{src}
}

func (s *Service) buildSource(path, category, label string) LogSource {
	src := LogSource{
		Path:     path,
		Category: category,
		Label:    s.labelFor(path, category, label),
	}
	info, err := os.Stat(path)
	if err != nil {
		return src
	}
	src.SizeBytes = info.Size()
	src.LastModified = info.ModTime()
	src.Readable = true
	return src
}

func (s *Service) labelFor(path, category, override string) string {
	if override != "" {
		return override
	}
	base := filepath.Base(path)
	// Strip .log suffix for display
	label := strings.TrimSuffix(base, ".log")

	if category == "app" && pathWithin(filepath.Clean(path), s.vhostsRoot) {
		if rel, err := filepath.Rel(s.vhostsRoot, path); err == nil {
			parts := strings.Split(filepath.ToSlash(rel), "/")
			if len(parts) > 0 && parts[0] != "." {
				return fmt.Sprintf("App: %s / %s", parts[0], label)
			}
		}
	}
	if category == "pm2" && s.pm2User != "" {
		return fmt.Sprintf("PM2(%s): %s", s.pm2User, label)
	}
	return label
}

// ── Tail (last N lines) ───────────────────────────────────────────────────────

// TailLines returns the last n lines of the file at path.
// Uses a reverse-read approach to avoid loading the entire file.
func (s *Service) TailLines(path string, n int) (*LogLines, error) {
	if !s.IsAllowed(path) {
		return nil, fmt.Errorf("access denied: path not in allowed list")
	}
	if n <= 0 {
		n = 100
	}
	if n > 5000 {
		n = 5000
	}

	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("cannot open log file: %w", err)
	}
	defer func() { _ = f.Close() }()

	lines, err := tailN(f, n)
	if err != nil {
		return nil, err
	}
	return &LogLines{Path: path, Lines: lines, Total: len(lines)}, nil
}

// tailN reads the last n lines from r using a chunked reverse-scan.
func tailN(r io.ReadSeeker, n int) ([]string, error) {
	// Seek to end to get total size.
	size, err := r.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}

	const chunkSize = int64(4096)
	var (
		lines     []string
		remaining = size
		partial   = ""
	)

	for len(lines) < n && remaining > 0 {
		readSize := chunkSize
		if remaining < chunkSize {
			readSize = remaining
		}
		remaining -= readSize

		if _, err := r.Seek(remaining, io.SeekStart); err != nil {
			return nil, err
		}

		buf := make([]byte, readSize)
		if _, err := io.ReadFull(r, buf); err != nil {
			return nil, err
		}

		// Prepend partial line from previous iteration.
		chunk := string(buf) + partial
		parts := strings.Split(chunk, "\n")

		// The first element may be incomplete — carry it forward.
		partial = parts[0]

		// Collect complete lines (in reverse) until we have enough.
		for i := len(parts) - 1; i >= 1; i-- {
			if parts[i] == "" {
				continue
			}
			lines = append([]string{parts[i]}, lines...)
			if len(lines) >= n {
				break
			}
		}
	}

	// Flush leftover partial line (the very first line of the file).
	if partial != "" && len(lines) < n {
		lines = append([]string{partial}, lines...)
	}

	// Trim to exactly n lines if we overshot.
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	return lines, nil
}

// ── Search ────────────────────────────────────────────────────────────────────

// SearchLog returns lines containing query (case-insensitive literal match).
// Returns at most maxResults lines.
func (s *Service) SearchLog(path, query string, maxResults int) (*LogLines, error) {
	if !s.IsAllowed(path) {
		return nil, fmt.Errorf("access denied: path not in allowed list")
	}
	if query == "" {
		return nil, fmt.Errorf("query must not be empty")
	}
	if maxResults <= 0 || maxResults > 2000 {
		maxResults = 500
	}

	// Sanitize: only printable ASCII, strip potential shell metacharacters.
	query = sanitizeQuery(query)
	if query == "" {
		return nil, fmt.Errorf("query contains only disallowed characters")
	}

	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return nil, fmt.Errorf("cannot open log file: %w", err)
	}
	defer func() { _ = f.Close() }()

	lower := strings.ToLower(query)
	var matched []string

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 256*1024), 256*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(strings.ToLower(line), lower) {
			matched = append(matched, line)
			if len(matched) >= maxResults {
				break
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading log file: %w", err)
	}

	return &LogLines{Path: path, Lines: matched, Total: len(matched)}, nil
}

// sanitizeQuery strips characters that could be used for injection.
// We perform a simple allowlist: printable ASCII minus shell-special chars.
func sanitizeQuery(q string) string {
	var b strings.Builder
	for _, r := range q {
		// Allow printable ASCII except: $ ` | & ; < > ( ) { } \
		if r >= 32 && r <= 126 &&
			!strings.ContainsRune("$`|&;<>(){}\\", r) {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

// ── File metadata ─────────────────────────────────────────────────────────────

// Stat returns size and last-modified for a log file.
func (s *Service) Stat(path string) (LogSource, error) {
	if !s.IsAllowed(path) {
		return LogSource{}, fmt.Errorf("access denied")
	}
	return s.buildSource(path, "", ""), nil
}

// ── Download size guard ───────────────────────────────────────────────────────

// CheckDownloadable returns an error when the file exceeds the 50 MB limit.
func (s *Service) CheckDownloadable(path string) error {
	if !s.IsAllowed(path) {
		return fmt.Errorf("access denied")
	}
	info, err := os.Stat(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("file not found: %w", err)
	}
	if info.Size() > maxDownloadBytes {
		return fmt.Errorf("file too large for download (%.1f MB > 50 MB)",
			float64(info.Size())/1024/1024)
	}
	return nil
}

// ── Real-time streaming ───────────────────────────────────────────────────────

// StreamNewLines sends newly appended lines to ch until ctx is cancelled.
// It seeks to the end of the file on start and then polls every pollInterval
// for new data.  A heartbeat comment is sent every 15 seconds to keep the
// SSE connection alive through proxies.
//
// ch is closed when streaming ends.
func (s *Service) StreamNewLines(ctx context.Context, path string, ch chan<- string) error {
	if !s.IsAllowed(path) {
		return fmt.Errorf("access denied")
	}

	f, err := os.Open(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("cannot open log file: %w", err)
	}

	// Seek to end — we only want NEW lines.
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		_ = f.Close()
		return fmt.Errorf("seek failed: %w", err)
	}

	const pollInterval = 250 * time.Millisecond
	const heartbeatInterval = 15 * time.Second

	ticker := time.NewTicker(pollInterval)
	heartbeat := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	defer heartbeat.Stop()

	go func() {
		defer func() { _ = f.Close() }()
		defer close(ch)

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 256*1024), 256*1024)

		for {
			// Drain all currently available lines.
			for scanner.Scan() {
				line := scanner.Text()
				select {
				case ch <- line:
				case <-ctx.Done():
					return
				}
			}

			select {
			case <-ctx.Done():
				return
			case <-heartbeat.C:
				// Send SSE comment as heartbeat — callers must handle ": ping"
				select {
				case ch <- ": ping":
				case <-ctx.Done():
					return
				}
			case <-ticker.C:
				// Continue loop to re-scan the file.
			}
		}
	}()

	return nil
}
