package php

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// iniPath constants
const (
	globalINIPath = "/etc/php/%s/fpm/php.ini"
)

// INIDirective holds metadata and current value for a single php.ini directive.
type INIDirective struct {
	Key          string `json:"key"`
	Value        string `json:"value"`
	DefaultValue string `json:"default_value"`
	Type         string `json:"type"`        // "string" | "integer" | "boolean" | "bytes"
	Section      string `json:"section"`     // e.g. "Resource Limits", "File Uploads"
	Description  string `json:"description"`
	Changeable   string `json:"changeable"` // "PHP_INI_SYSTEM" | "PHP_INI_PERDIR" | "PHP_INI_ALL" | "PHP_INI_USER"
}

// INIDiff represents a single php.ini setting that differs from the PHP default.
type INIDiff struct {
	Key     string `json:"key"`
	Current string `json:"current"`
	Default string `json:"default"`
	Source  string `json:"source"` // "global" or "domain"
}

// accessToChangeable converts PHP's ini_get_all access bitmask to a human-readable constant.
// PHP constants: PHP_INI_USER=1, PHP_INI_PERDIR=2, PHP_INI_SYSTEM=4, PHP_INI_ALL=7
func accessToChangeable(access int) string {
	switch access {
	case 1:
		return "PHP_INI_USER"
	case 2:
		return "PHP_INI_PERDIR"
	case 4:
		return "PHP_INI_SYSTEM"
	case 6:
		return "PHP_INI_PERDIR" // PERDIR | SYSTEM
	case 7:
		return "PHP_INI_ALL"
	default:
		return "PHP_INI_SYSTEM"
	}
}

// iniAccessEntry is used to unmarshal ini_get_all JSON output.
type iniAccessEntry struct {
	GlobalValue string `json:"global_value"`
	LocalValue  string `json:"local_value"`
	Access      int    `json:"access"`
}

// runPHPEval runs a PHP one-liner with the given binary and returns stdout.
func runPHPEval(version, code string) ([]byte, error) {
	binary := fmt.Sprintf("php%s", version)
	out, err := exec.Command(binary, "-r", code).Output()
	if err != nil {
		return nil, fmt.Errorf("php%s eval failed: %w", version, err)
	}
	return out, nil
}

// fetchINIAll runs ini_get_all on the live FPM config and returns a map of key → entry.
func fetchINIAll(version string) (map[string]iniAccessEntry, error) {
	code := `echo json_encode(ini_get_all(null, true));`
	out, err := runPHPEval(version, code)
	if err != nil {
		return nil, err
	}
	var result map[string]iniAccessEntry
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, fmt.Errorf("parsing ini_get_all output: %w", err)
	}
	return result, nil
}

// fetchINIDefaults runs php -n (no config) to get compiled-in defaults.
// For PHP 7.x, -n disables the json extension, so we use -d to load only json.
func fetchINIDefaults(version string) (map[string]string, error) {
	binary := fmt.Sprintf("php%s", version)
	code := `echo json_encode(ini_get_all(null, false));`

	// PHP 7.x: -n strips json extension; use -d extension=json.so as fallback
	out, err := exec.Command(binary, "-n", "-r", code).Output()
	if err != nil {
		// Retry with json extension explicitly loaded (PHP 7.x compat)
		out, err = exec.Command(binary, "-n", "-d", "extension=json.so", "-r", code).Output()
		if err != nil {
			// Last resort: return empty defaults (non-fatal)
			return make(map[string]string), nil
		}
	}
	var result map[string]string
	if err := json.Unmarshal(out, &result); err != nil {
		return make(map[string]string), nil
	}
	return result, nil
}

// ---------- Global php.ini management ----------

// GetGlobalINI reads the global FPM php.ini and returns all active key=value pairs.
// Comments and empty lines are excluded.
func (s *Service) GetGlobalINI(version string) (map[string]string, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	path := fmt.Sprintf(globalINIPath, version)
	return parseINIFile(path)
}

// SetGlobalINI updates a single directive in the global FPM php.ini.
// If the key already exists it is updated in-place; otherwise it is appended.
// Validates the key name to prevent injection.
func (s *Service) SetGlobalINI(version, key, value string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	if err := validateINIKey(key); err != nil {
		return err
	}
	path := fmt.Sprintf(globalINIPath, version)
	return setINIFileValue(path, key, value)
}

// GetINIDiff returns all directives where the current global value differs from
// the PHP compiled-in default. This helps identify intentional customizations.
func (s *Service) GetINIDiff(version string) ([]INIDiff, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}

	current, err := fetchINIAll(version)
	if err != nil {
		return nil, err
	}
	defaults, err := fetchINIDefaults(version)
	if err != nil {
		return nil, err
	}

	var diffs []INIDiff
	for key, entry := range current {
		def, hasDefault := defaults[key]
		if !hasDefault {
			def = ""
		}
		if entry.GlobalValue != def {
			diffs = append(diffs, INIDiff{
				Key:     key,
				Current: entry.GlobalValue,
				Default: def,
				Source:  "global",
			})
		}
	}
	return diffs, nil
}

// ---------- Per-domain INI overrides (via pool config) ----------

// resolvePoolConfPath finds the actual config file for a pool name.
// Pool names (e.g., "app-tunex") don't always match file names (e.g., "app.tune-x.com.conf").
// Falls back to scanning the pool directory for a matching [section] header.
func (s *Service) resolvePoolConfPath(version, poolName string) (string, error) {
	// Try direct match first
	direct := poolConfPath(version, poolName)
	if _, err := os.Stat(direct); err == nil {
		return direct, nil
	}

	// Scan pool directory for matching [section] header
	poolDir := fmt.Sprintf(poolDirPattern, version)
	files, err := filepath.Glob(filepath.Join(poolDir, "*.conf"))
	if err != nil {
		return "", fmt.Errorf("scanning pool dir: %w", err)
	}

	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		// Check if file contains [poolName] section
		needle := "[" + poolName + "]"
		if strings.Contains(string(data), needle) {
			return f, nil
		}
	}

	return "", fmt.Errorf("pool %s not found for php%s", poolName, version)
}

// GetDomainINI reads php_admin_value and php_value directives from the pool config
// for the given domain and returns them as a flat key→value map.
func (s *Service) GetDomainINI(version, domain string) (map[string]string, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	if err := validatePoolName(domain); err != nil {
		return nil, err
	}

	confPath, err := s.resolvePoolConfPath(version, domain)
	if err != nil {
		return nil, err
	}

	return parseDomainINI(confPath)
}

// SetDomainINI sets a single php_admin_value or php_value in the pool config.
// For security-critical keys (disable_functions, open_basedir, etc.) it uses
// php_admin_value; for performance/runtime keys it uses php_value.
func (s *Service) SetDomainINI(version, domain, key, value string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	if err := validatePoolName(domain); err != nil {
		return err
	}
	if err := validateINIKey(key); err != nil {
		return err
	}

	confPath, err := s.resolvePoolConfPath(version, domain)
	if err != nil {
		return err
	}

	directive := phpPoolDirective(key)
	return setPoolINIValue(confPath, directive, key, value)
}

// ResetDomainINI removes a php_admin_value/php_value override from the pool config,
// falling back to the global php.ini value.
func (s *Service) ResetDomainINI(version, domain, key string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	if err := validatePoolName(domain); err != nil {
		return err
	}
	if err := validateINIKey(key); err != nil {
		return err
	}

	confPath, err := s.resolvePoolConfPath(version, domain)
	if err != nil {
		return err
	}

	return removePoolINIValue(confPath, key)
}

// BulkSetDomainINI applies multiple INI overrides to a pool config in a single write.
// Reads the file once, applies all changes in memory, writes once — avoids multiple I/O.
func (s *Service) BulkSetDomainINI(version, domain string, settings map[string]string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	if err := validatePoolName(domain); err != nil {
		return err
	}

	confPath, err := s.resolvePoolConfPath(version, domain)
	if err != nil {
		return err
	}

	// Validate all keys before touching the file
	for key := range settings {
		if err := validateINIKey(key); err != nil {
			return fmt.Errorf("invalid key %q: %w", key, err)
		}
	}

	content, err := os.ReadFile(confPath)
	if err != nil {
		return fmt.Errorf("reading pool config: %w", err)
	}

	lines := strings.Split(string(content), "\n")

	for key, value := range settings {
		directive := phpPoolDirective(key)
		pattern := fmt.Sprintf(`%s\[%s\]`, regexp.QuoteMeta(directive), regexp.QuoteMeta(key))
		re := regexp.MustCompile(pattern)

		found := false
		newLine := fmt.Sprintf("%s[%s] = %s", directive, key, value)
		for i, line := range lines {
			if re.MatchString(strings.TrimSpace(line)) {
				lines[i] = newLine
				found = true
				break
			}
		}
		if !found {
			lines = append(lines, newLine)
		}
	}

	return os.WriteFile(confPath, []byte(strings.Join(lines, "\n")), 0644)
}

// ---------- INI directive browser ----------

// ListINIDirectives returns all ini directives for the given PHP version with
// metadata (type, section, description, changeable) and both current and default values.
func (s *Service) ListINIDirectives(version string) ([]INIDirective, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}

	current, err := fetchINIAll(version)
	if err != nil {
		return nil, err
	}
	defaults, err := fetchINIDefaults(version)
	if err != nil {
		return nil, err
	}

	meta := iniDirectiveMeta()
	var directives []INIDirective

	for key, entry := range current {
		def := defaults[key]
		m := meta[key]

		directive := INIDirective{
			Key:          key,
			Value:        entry.GlobalValue,
			DefaultValue: def,
			Type:         m.Type,
			Section:      m.Section,
			Description:  m.Description,
			Changeable:   accessToChangeable(entry.Access),
		}

		// Fill in sensible defaults for unknown directives
		if directive.Type == "" {
			directive.Type = inferINIType(entry.GlobalValue)
		}
		if directive.Section == "" {
			directive.Section = "Other"
		}

		directives = append(directives, directive)
	}

	return directives, nil
}

// ---------- Helpers ----------

// parseINIFile reads a php.ini file and returns active key=value pairs.
// Skips comments (;) and section headers ([...]).
func parseINIFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	result := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			continue
		}
		key, val, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		// Strip inline comments
		if idx := strings.Index(val, " ;"); idx > 0 {
			val = strings.TrimSpace(val[:idx])
		}
		result[key] = val
	}
	return result, scanner.Err()
}

// setINIFileValue updates or appends a key=value pair in a php.ini file.
// Updates in-place if the key exists (even if commented out), appends otherwise.
func setINIFileValue(path, key, value string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	lines := strings.Split(string(content), "\n")
	newLine := fmt.Sprintf("%s = %s", key, value)

	// Match the active line (not commented)
	keyRe := regexp.MustCompile(fmt.Sprintf(`(?i)^[ \t]*%s[ \t]*=`, regexp.QuoteMeta(key)))
	found := false
	for i, line := range lines {
		if keyRe.MatchString(line) {
			lines[i] = newLine
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, newLine)
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0644)
}

// parseDomainINI reads php_admin_value[key] and php_value[key] from a pool config.
func parseDomainINI(confPath string) (map[string]string, error) {
	f, err := os.Open(confPath)
	if err != nil {
		return nil, fmt.Errorf("opening %s: %w", confPath, err)
	}
	defer func() { _ = f.Close() }()

	// Pattern: php_admin_value[key] = value  OR  php_value[key] = value
	re := regexp.MustCompile(`^php(?:_admin)?_value\[([^\]]+)\]\s*=\s*(.*)$`)
	result := make(map[string]string)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if m := re.FindStringSubmatch(line); m != nil {
			key := strings.TrimSpace(m[1])
			val := strings.TrimSpace(m[2])
			// Strip inline comments
			if idx := strings.Index(val, " ;"); idx > 0 {
				val = strings.TrimSpace(val[:idx])
			}
			result[key] = val
		}
	}
	return result, scanner.Err()
}

// phpPoolDirective decides whether to use php_admin_value or php_value for a key.
// Security-critical keys that must not be overridden per-request use php_admin_value.
func phpPoolDirective(key string) string {
	adminKeys := map[string]bool{
		"open_basedir":       true,
		"disable_functions":  true,
		"disable_classes":    true,
		"allow_url_fopen":    true,
		"allow_url_include":  true,
		"expose_php":         true,
		"safe_mode":          true,
		"max_execution_time": true,
		"max_input_time":     true,
		"memory_limit":       true,
	}
	if adminKeys[key] {
		return "php_admin_value"
	}
	return "php_value"
}

// setPoolINIValue updates or appends a php_admin_value/php_value line in a pool config.
func setPoolINIValue(confPath, directive, key, value string) error {
	content, err := os.ReadFile(confPath)
	if err != nil {
		return fmt.Errorf("reading pool config: %w", err)
	}

	lines := strings.Split(string(content), "\n")
	// Match either php_admin_value[key] or php_value[key]
	pattern := fmt.Sprintf(`php(?:_admin)?_value\[%s\]`, regexp.QuoteMeta(key))
	re := regexp.MustCompile(pattern)
	newLine := fmt.Sprintf("%s[%s] = %s", directive, key, value)

	found := false
	for i, line := range lines {
		if re.MatchString(strings.TrimSpace(line)) {
			lines[i] = newLine
			found = true
			break
		}
	}
	if !found {
		lines = append(lines, newLine)
	}

	return os.WriteFile(confPath, []byte(strings.Join(lines, "\n")), 0644)
}

// removePoolINIValue removes the php_admin_value/php_value line for the given key.
func removePoolINIValue(confPath, key string) error {
	content, err := os.ReadFile(confPath)
	if err != nil {
		return fmt.Errorf("reading pool config: %w", err)
	}

	pattern := fmt.Sprintf(`php(?:_admin)?_value\[%s\]`, regexp.QuoteMeta(key))
	re := regexp.MustCompile(pattern)

	lines := strings.Split(string(content), "\n")
	filtered := lines[:0]
	for _, line := range lines {
		if !re.MatchString(strings.TrimSpace(line)) {
			filtered = append(filtered, line)
		}
	}

	return os.WriteFile(confPath, []byte(strings.Join(filtered, "\n")), 0644)
}

// validateINIKey ensures a key contains only safe characters to prevent config injection.
// Allows: alphanumeric, underscore, dot — no spaces, brackets, or special characters.
func validateINIKey(key string) error {
	if key == "" {
		return fmt.Errorf("ini key cannot be empty")
	}
	re := regexp.MustCompile(`^[a-zA-Z0-9_.]+$`)
	if !re.MatchString(key) {
		return fmt.Errorf("invalid ini key %q: only alphanumeric, underscore, and dot allowed", key)
	}
	return nil
}

// inferINIType guesses the type of an ini value from its content.
func inferINIType(value string) string {
	lower := strings.ToLower(value)
	switch lower {
	case "on", "off", "true", "false", "1", "0", "yes", "no":
		return "boolean"
	}
	// Byte size values: 128M, 2G, 512K
	if regexp.MustCompile(`^\d+[KMGkmg]$`).MatchString(value) {
		return "bytes"
	}
	// Pure integer
	if regexp.MustCompile(`^\d+$`).MatchString(value) {
		return "integer"
	}
	return "string"
}

// iniMetaEntry holds static metadata for known directives.
type iniMetaEntry struct {
	Type        string
	Section     string
	Description string
}

// iniDirectiveMeta returns a map of well-known PHP ini directives with metadata.
// This covers the most commonly managed directives in a web panel context.
func iniDirectiveMeta() map[string]iniMetaEntry {
	return map[string]iniMetaEntry{
		// Resource Limits
		"memory_limit": {
			Type:        "bytes",
			Section:     "Resource Limits",
			Description: "Maximum amount of memory a script may consume.",
		},
		"max_execution_time": {
			Type:        "integer",
			Section:     "Resource Limits",
			Description: "Maximum time in seconds a script is allowed to run.",
		},
		"max_input_time": {
			Type:        "integer",
			Section:     "Resource Limits",
			Description: "Maximum time in seconds a script is allowed to parse input data.",
		},
		"max_input_vars": {
			Type:        "integer",
			Section:     "Resource Limits",
			Description: "Maximum number of input variables per request.",
		},
		"max_input_nesting_level": {
			Type:        "integer",
			Section:     "Resource Limits",
			Description: "Maximum nesting level of input variables.",
		},

		// File Uploads
		"file_uploads": {
			Type:        "boolean",
			Section:     "File Uploads",
			Description: "Whether to allow HTTP file uploads.",
		},
		"upload_max_filesize": {
			Type:        "bytes",
			Section:     "File Uploads",
			Description: "Maximum allowed size for uploaded files.",
		},
		"post_max_size": {
			Type:        "bytes",
			Section:     "File Uploads",
			Description: "Maximum size of POST data that PHP will accept.",
		},
		"max_file_uploads": {
			Type:        "integer",
			Section:     "File Uploads",
			Description: "Maximum number of files allowed to be uploaded simultaneously.",
		},
		"upload_tmp_dir": {
			Type:        "string",
			Section:     "File Uploads",
			Description: "Temporary directory used for storing file uploads.",
		},

		// Error Handling
		"display_errors": {
			Type:        "boolean",
			Section:     "Error Handling",
			Description: "Whether to print errors to stdout (disable in production).",
		},
		"display_startup_errors": {
			Type:        "boolean",
			Section:     "Error Handling",
			Description: "Whether to display errors during PHP startup.",
		},
		"error_reporting": {
			Type:        "string",
			Section:     "Error Handling",
			Description: "Bitmask controlling which PHP errors are reported.",
		},
		"log_errors": {
			Type:        "boolean",
			Section:     "Error Handling",
			Description: "Whether to log errors to a file.",
		},
		"error_log": {
			Type:        "string",
			Section:     "Error Handling",
			Description: "Name of the file where script errors are logged.",
		},
		"ignore_repeated_errors": {
			Type:        "boolean",
			Section:     "Error Handling",
			Description: "Do not log repeated messages from the same error location.",
		},

		// Session
		"session.save_handler": {
			Type:        "string",
			Section:     "Session",
			Description: "Session storage handler (files, redis, memcached).",
		},
		"session.save_path": {
			Type:        "string",
			Section:     "Session",
			Description: "Path where session data files are stored.",
		},
		"session.gc_maxlifetime": {
			Type:        "integer",
			Section:     "Session",
			Description: "Number of seconds after which session data is garbage collected.",
		},
		"session.cookie_lifetime": {
			Type:        "integer",
			Section:     "Session",
			Description: "Lifetime of the session cookie in seconds (0 = until browser close).",
		},
		"session.cookie_secure": {
			Type:        "boolean",
			Section:     "Session",
			Description: "Whether session cookies should only be sent over HTTPS.",
		},
		"session.cookie_httponly": {
			Type:        "boolean",
			Section:     "Session",
			Description: "Whether session cookies are accessible only via HTTP (not JavaScript).",
		},
		"session.cookie_samesite": {
			Type:        "string",
			Section:     "Session",
			Description: "SameSite attribute for session cookies (Strict, Lax, None).",
		},
		"session.use_strict_mode": {
			Type:        "boolean",
			Section:     "Session",
			Description: "Prevent session ID fixation by rejecting uninitialized IDs.",
		},

		// OPcache
		"opcache.enable": {
			Type:        "boolean",
			Section:     "OPcache",
			Description: "Enable the OPcache bytecode cache.",
		},
		"opcache.enable_cli": {
			Type:        "boolean",
			Section:     "OPcache",
			Description: "Enable the OPcache for CLI PHP.",
		},
		"opcache.memory_consumption": {
			Type:        "integer",
			Section:     "OPcache",
			Description: "Shared memory storage size in megabytes.",
		},
		"opcache.interned_strings_buffer": {
			Type:        "integer",
			Section:     "OPcache",
			Description: "Megabytes of memory used to store interned strings.",
		},
		"opcache.max_accelerated_files": {
			Type:        "integer",
			Section:     "OPcache",
			Description: "Maximum number of files that can be cached.",
		},
		"opcache.revalidate_freq": {
			Type:        "integer",
			Section:     "OPcache",
			Description: "How often (in seconds) to check script timestamps for changes.",
		},
		"opcache.validate_timestamps": {
			Type:        "boolean",
			Section:     "OPcache",
			Description: "When disabled scripts are never checked for changes (production mode).",
		},
		"opcache.max_wasted_percentage": {
			Type:        "integer",
			Section:     "OPcache",
			Description: "Maximum percentage of wasted memory allowed before a restart.",
		},
		"opcache.jit": {
			Type:        "string",
			Section:     "OPcache",
			Description: "JIT mode: off, tracing (1254), function (1205).",
		},
		"opcache.jit_buffer_size": {
			Type:        "bytes",
			Section:     "OPcache",
			Description: "JIT buffer size (e.g. 32M).",
		},

		// Security
		"expose_php": {
			Type:        "boolean",
			Section:     "Security",
			Description: "Whether to expose PHP version in the X-Powered-By header.",
		},
		"allow_url_fopen": {
			Type:        "boolean",
			Section:     "Security",
			Description: "Allow treating URLs as files (fopen, include). Disable for security.",
		},
		"allow_url_include": {
			Type:        "boolean",
			Section:     "Security",
			Description: "Allow include/require with URLs. Should always be Off.",
		},
		"disable_functions": {
			Type:        "string",
			Section:     "Security",
			Description: "Comma-separated list of functions to disable.",
		},
		"disable_classes": {
			Type:        "string",
			Section:     "Security",
			Description: "Comma-separated list of classes to disable.",
		},
		"open_basedir": {
			Type:        "string",
			Section:     "Security",
			Description: "Limits files that can be opened to the specified directory tree.",
		},

		// Database
		"pdo_mysql.default_socket": {
			Type:        "string",
			Section:     "Database",
			Description: "Default socket path for PDO MySQL connections.",
		},
		"mysqli.default_socket": {
			Type:        "string",
			Section:     "Database",
			Description: "Default socket path for MySQLi connections.",
		},
		"mysqli.default_host": {
			Type:        "string",
			Section:     "Database",
			Description: "Default MySQL host for MySQLi.",
		},
		"mysqli.default_port": {
			Type:        "integer",
			Section:     "Database",
			Description: "Default MySQL port for MySQLi.",
		},

		// Mail
		"sendmail_path": {
			Type:        "string",
			Section:     "Mail",
			Description: "Path to the sendmail binary used for mail().",
		},
		"SMTP": {
			Type:        "string",
			Section:     "Mail",
			Description: "SMTP server for mail() on Windows.",
		},
		"smtp_port": {
			Type:        "integer",
			Section:     "Mail",
			Description: "SMTP port for Windows mail().",
		},
		"mail.add_x_header": {
			Type:        "boolean",
			Section:     "Mail",
			Description: "Add X-PHP-Originating-Script header to sent emails.",
		},

		// Output
		"output_buffering": {
			Type:        "integer",
			Section:     "Output",
			Description: "Output buffer size in bytes (4096 is typical). 0 = off.",
		},
		"zlib.output_compression": {
			Type:        "boolean",
			Section:     "Output",
			Description: "Transparently compress output with gzip.",
		},
		"default_charset": {
			Type:        "string",
			Section:     "Output",
			Description: "Default encoding for PHP output (e.g. UTF-8).",
		},
		"default_mimetype": {
			Type:        "string",
			Section:     "Output",
			Description: "Default MIME type for responses.",
		},

		// Date
		"date.timezone": {
			Type:        "string",
			Section:     "Date",
			Description: "Default timezone for date/time functions (e.g. Europe/Istanbul).",
		},
	}
}
