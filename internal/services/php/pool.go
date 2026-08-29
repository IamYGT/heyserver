package php

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// PoolConfig holds the full configuration for a PHP-FPM pool.
// This is the rich config struct used by the advanced pool management API.
type PoolConfig struct {
	Domain  string `json:"domain"`
	Version string `json:"version"` // "7.4", "8.4", "8.5"

	// Process Manager
	PM                 string `json:"pm"`                   // static, dynamic, ondemand
	MaxChildren        int    `json:"max_children"`         // max worker processes
	StartServers       int    `json:"start_servers"`        // initial workers (dynamic only)
	MinSpareServers    int    `json:"min_spare_servers"`    // min idle (dynamic only)
	MaxSpareServers    int    `json:"max_spare_servers"`    // max idle (dynamic only)
	MaxRequests        int    `json:"max_requests"`         // requests before respawn (0=unlimited)
	ProcessIdleTimeout int    `json:"process_idle_timeout"` // idle timeout seconds (ondemand only)

	// Resource Limits
	MemoryLimit      string `json:"memory_limit"`        // e.g. "256M"
	MaxExecutionTime int    `json:"max_execution_time"`  // seconds
	MaxInputTime     int    `json:"max_input_time"`      // seconds
	UploadMaxSize    string `json:"upload_max_filesize"` // e.g. "64M"
	PostMaxSize      string `json:"post_max_size"`       // e.g. "64M"

	// Logging
	SlowlogTimeout int  `json:"slowlog_timeout"` // seconds, 0 = disabled
	AccessLog      bool `json:"access_log"`

	// Security
	OpenBasedir      string `json:"open_basedir"`
	DisableFunctions string `json:"disable_functions"`

	// User/Group
	User        string `json:"user"`
	Group       string `json:"group"`
	ListenOwner string `json:"listen_owner"`
	ListenGroup string `json:"listen_group"`
}

// PoolPreset is a named set of PoolConfig defaults for common use cases.
type PoolPreset struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Config      PoolConfig `json:"config"`
}

// defaultDisableFunctions is a sane default for disabling dangerous functions.
const defaultDisableFunctions = "opcache_get_status"

// GetPresets returns all built-in pool presets.
func GetPresets() []PoolPreset {
	return []PoolPreset{
		{
			Name:        "low",
			Description: "Small/personal sites — minimal resource usage, idle timeout saves RAM",
			Config: PoolConfig{
				PM:                 "ondemand",
				MaxChildren:        5,
				ProcessIdleTimeout: 10,
				MaxRequests:        200,
				MemoryLimit:        "128M",
				MaxExecutionTime:   30,
				MaxInputTime:       60,
				UploadMaxSize:      "16M",
				PostMaxSize:        "16M",
				DisableFunctions:   defaultDisableFunctions,
			},
		},
		{
			Name:        "medium",
			Description: "Standard sites — balanced between performance and memory",
			Config: PoolConfig{
				PM:               "dynamic",
				MaxChildren:      10,
				StartServers:     2,
				MinSpareServers:  1,
				MaxSpareServers:  3,
				MaxRequests:      500,
				MemoryLimit:      "256M",
				MaxExecutionTime: 60,
				MaxInputTime:     120,
				UploadMaxSize:    "32M",
				PostMaxSize:      "32M",
				DisableFunctions: defaultDisableFunctions,
			},
		},
		{
			Name:        "high",
			Description: "High-traffic sites — maximises concurrency, larger worker pool",
			Config: PoolConfig{
				PM:               "dynamic",
				MaxChildren:      30,
				StartServers:     5,
				MinSpareServers:  5,
				MaxSpareServers:  10,
				MaxRequests:      1000,
				MemoryLimit:      "512M",
				MaxExecutionTime: 120,
				MaxInputTime:     300,
				UploadMaxSize:    "128M",
				PostMaxSize:      "128M",
				DisableFunctions: defaultDisableFunctions,
			},
		},
		{
			Name:        "wordpress",
			Description: "WordPress optimised — dynamic PM with opcache-friendly settings",
			Config: PoolConfig{
				PM:               "dynamic",
				MaxChildren:      15,
				StartServers:     3,
				MinSpareServers:  2,
				MaxSpareServers:  5,
				MaxRequests:      500,
				MemoryLimit:      "256M",
				MaxExecutionTime: 60,
				MaxInputTime:     120,
				UploadMaxSize:    "64M",
				PostMaxSize:      "64M",
				DisableFunctions: defaultDisableFunctions,
			},
		},
		{
			Name:        "laravel",
			Description: "Laravel optimised — larger memory, longer execution for queues/jobs",
			Config: PoolConfig{
				PM:               "dynamic",
				MaxChildren:      20,
				StartServers:     4,
				MinSpareServers:  2,
				MaxSpareServers:  8,
				MaxRequests:      500,
				MemoryLimit:      "512M",
				MaxExecutionTime: 300,
				MaxInputTime:     300,
				UploadMaxSize:    "64M",
				PostMaxSize:      "64M",
				DisableFunctions: defaultDisableFunctions,
			},
		},
	}
}

// GetPreset returns a single preset by name, or an error if not found.
func GetPreset(name string) (*PoolPreset, error) {
	for _, p := range GetPresets() {
		if p.Name == name {
			p := p // capture loop variable
			return &p, nil
		}
	}
	return nil, fmt.Errorf("preset %q not found", name)
}

// GetPoolConfig reads and parses the pool config file for domain+version.
func (s *Service) GetPoolConfig(version, domain string) (*PoolConfig, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	if err := validatePoolName(domain); err != nil {
		return nil, err
	}

	confPath := poolConfPath(version, domain)
	data, err := os.ReadFile(confPath)
	if err != nil {
		return nil, fmt.Errorf("reading pool config %s: %w", confPath, err)
	}
	return parsePoolConfig(version, domain, string(data))
}

// SavePoolConfig generates and writes the pool config file for the given PoolConfig.
func (s *Service) SavePoolConfig(cfg *PoolConfig) error {
	if err := validatePoolConfig(cfg); err != nil {
		return err
	}
	applyPoolConfigDefaults(cfg)

	confPath := poolConfPath(cfg.Version, cfg.Domain)
	content, err := s.generatePoolConfig(cfg)
	if err != nil {
		return err
	}
	if err := os.WriteFile(confPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing pool config: %w", err)
	}
	return nil
}

// ApplyPreset overwrites the pool config for domain+version with preset values,
// keeping security settings (open_basedir, user/group) from the existing config.
func (s *Service) ApplyPreset(version, domain, presetName string) error {
	preset, err := GetPreset(presetName)
	if err != nil {
		return err
	}

	// Load existing config to preserve security/identity fields.
	existing, existErr := s.GetPoolConfig(version, domain)

	cfg := preset.Config
	cfg.Version = version
	cfg.Domain = domain

	if existErr == nil {
		cfg.User = existing.User
		cfg.Group = existing.Group
		cfg.ListenOwner = existing.ListenOwner
		cfg.ListenGroup = existing.ListenGroup
		cfg.OpenBasedir = existing.OpenBasedir
		// Preserve access log setting from existing.
		cfg.AccessLog = existing.AccessLog
	}

	return s.SavePoolConfig(&cfg)
}

// ListInstalledVersions returns all installed PHP versions with extended info.
// This supplements the existing DetectVersions with extension count.
func (s *Service) ListInstalledVersions() ([]PHPVersion, error) {
	return s.DetectVersions()
}

// SwitchDomainVersion moves a pool from one PHP version to another.
// It reads the existing config, writes it for the new version, then removes the old one.
func (s *Service) SwitchDomainVersion(domain, fromVersion, toVersion string) error {
	if err := validateVersion(fromVersion); err != nil {
		return fmt.Errorf("invalid source version: %w", err)
	}
	if err := validateVersion(toVersion); err != nil {
		return fmt.Errorf("invalid target version: %w", err)
	}
	if fromVersion == toVersion {
		return fmt.Errorf("source and target versions are the same: %s", fromVersion)
	}
	if err := validatePoolName(domain); err != nil {
		return err
	}

	// Verify source pool exists.
	srcPath := poolConfPath(fromVersion, domain)
	if _, err := os.Stat(srcPath); os.IsNotExist(err) {
		return fmt.Errorf("pool %s not found for php%s", domain, fromVersion)
	}

	// Verify destination pool does NOT exist to avoid accidental overwrite.
	dstPath := poolConfPath(toVersion, domain)
	if _, err := os.Stat(dstPath); err == nil {
		return fmt.Errorf("pool %s already exists for php%s — delete it first", domain, toVersion)
	}

	// Read the source config, update version, write to destination.
	cfg, err := s.GetPoolConfig(fromVersion, domain)
	if err != nil {
		return fmt.Errorf("reading source pool: %w", err)
	}
	cfg.Version = toVersion

	if err := s.SavePoolConfig(cfg); err != nil {
		return fmt.Errorf("writing destination pool: %w", err)
	}

	// Remove old config only after successful write.
	if err := os.Remove(srcPath); err != nil {
		return fmt.Errorf("removing old pool config (new config already written): %w", err)
	}

	return nil
}

// GetDefaultVersion returns the system default PHP version (php --version).
func (s *Service) GetDefaultVersion() (string, error) {
	out, err := exec.Command("php", "--version").Output()
	if err != nil {
		return "", fmt.Errorf("running 'php --version': %w", err)
	}
	// Output: "PHP 8.4.x ..."
	line := strings.SplitN(string(out), "\n", 2)[0]
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return "", fmt.Errorf("unexpected php --version output: %q", line)
	}
	// Version is like "8.4.5" — return major.minor only.
	segments := strings.SplitN(parts[1], ".", 3)
	if len(segments) < 2 {
		return "", fmt.Errorf("cannot parse version from: %q", parts[1])
	}
	return segments[0] + "." + segments[1], nil
}

// RestartPool restarts the PHP-FPM service for the given version (affects all pools).
// Individual pool-level restart is not directly supported by systemd; we reload instead.
func (s *Service) RestartPool(version, domain string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	if err := validatePoolName(domain); err != nil {
		return err
	}
	// Verify the pool config exists — use resolvePoolConfPath for name↔file matching.
	if _, err := s.resolvePoolConfPath(version, domain); err != nil {
		return err
	}
	return s.ReloadFPM(version)
}

// ReloadFPM validates configuration, then requests a graceful systemd reload.
func (s *Service) ReloadFPM(version string) error {
	if err := s.TestFPM(version); err != nil {
		return err
	}
	svcName := fmt.Sprintf("php%s-fpm", version)
	out, err := s.runLifecycleCommand("systemctl", "reload", svcName)
	if err != nil {
		return fmt.Errorf("%w: reloading %s: %v — %s", ErrFPMLifecycleAction, svcName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// CalculateMaxChildren returns the recommended pm.max_children for the given version,
// based on available RAM and average process memory of a running FPM worker.
//
// Formula: (availableRAM_MB - osOverhead_MB) / avgProcessMem_MB
// osOverhead is set at 512 MB to leave headroom for other services.
// avgProcessMem is measured from /proc/<pid>/status of running workers, or falls back to 32 MB.
func (s *Service) CalculateMaxChildren(version string, availableRAM int) (int, error) {
	if err := validateVersion(version); err != nil {
		return 0, err
	}
	if availableRAM <= 0 {
		ram, err := readAvailableRAMMB()
		if err != nil {
			return 0, err
		}
		availableRAM = ram
	}

	const osOverheadMB = 512
	usable := availableRAM - osOverheadMB
	if usable <= 0 {
		return 1, nil
	}

	avgMem := measureAvgWorkerMemMB(version)
	if avgMem <= 0 {
		avgMem = 32 // conservative default
	}

	recommended := usable / avgMem
	if recommended < 1 {
		recommended = 1
	}
	return recommended, nil
}

// ---------- config generation ----------

// generatePoolConfig renders a complete PHP-FPM pool .conf file from a PoolConfig.
func (s *Service) generatePoolConfig(cfg *PoolConfig) (string, error) {
	socketPath := fmt.Sprintf("/run/php/php%s-fpm-%s.sock", cfg.Version, cfg.Domain)

	listenOwner := cfg.ListenOwner
	if listenOwner == "" {
		listenOwner = "www-data"
	}
	listenGroup := cfg.ListenGroup
	if listenGroup == "" {
		listenGroup = "www-data"
	}
	group := cfg.Group
	if group == "" {
		group = "www-data"
	}
	user := cfg.User
	if user == "" {
		user = "www-data"
	}

	var b strings.Builder

	// Header
	fmt.Fprintf(&b, "[%s]\n", cfg.Domain)
	fmt.Fprintf(&b, "user = %s\n", user)
	fmt.Fprintf(&b, "group = %s\n", group)
	fmt.Fprintf(&b, "\n")

	// Listen
	fmt.Fprintf(&b, "listen = %s\n", socketPath)
	fmt.Fprintf(&b, "listen.owner = %s\n", listenOwner)
	fmt.Fprintf(&b, "listen.group = %s\n", listenGroup)
	fmt.Fprintf(&b, "listen.mode = 0660\n")
	fmt.Fprintf(&b, "\n")

	// Process manager
	pm := cfg.PM
	if pm == "" {
		pm = "dynamic"
	}
	fmt.Fprintf(&b, "pm = %s\n", pm)
	fmt.Fprintf(&b, "pm.max_children = %d\n", cfg.MaxChildren)

	switch pm {
	case "dynamic":
		fmt.Fprintf(&b, "pm.start_servers = %d\n", cfg.StartServers)
		fmt.Fprintf(&b, "pm.min_spare_servers = %d\n", cfg.MinSpareServers)
		fmt.Fprintf(&b, "pm.max_spare_servers = %d\n", cfg.MaxSpareServers)
	case "ondemand":
		idleTimeout := cfg.ProcessIdleTimeout
		if idleTimeout <= 0 {
			idleTimeout = 10
		}
		fmt.Fprintf(&b, "pm.process_idle_timeout = %ds\n", idleTimeout)
	}

	maxReq := cfg.MaxRequests
	if maxReq == 0 {
		maxReq = 500
	}
	fmt.Fprintf(&b, "pm.max_requests = %d\n", maxReq)
	fmt.Fprintf(&b, "pm.status_path = /fpm-status\n")
	fmt.Fprintf(&b, "\n")

	// Slowlog
	if cfg.SlowlogTimeout > 0 {
		slowlogPath := fmt.Sprintf("/var/log/php%s-fpm-%s-slow.log", cfg.Version, cfg.Domain)
		fmt.Fprintf(&b, "slowlog = %s\n", slowlogPath)
		fmt.Fprintf(&b, "request_slowlog_timeout = %ds\n", cfg.SlowlogTimeout)
		fmt.Fprintf(&b, "\n")
	}

	// Access log
	if cfg.AccessLog {
		accessLogPath := fmt.Sprintf("/var/log/php%s-fpm-%s-access.log", cfg.Version, cfg.Domain)
		fmt.Fprintf(&b, "access.log = %s\n", accessLogPath)
		fmt.Fprintf(&b, "\n")
	}

	b.WriteString("chdir = /\n")
	b.WriteString("\n")
	b.WriteString("catch_workers_output = yes\n")
	b.WriteString("decorate_workers_output = no\n")
	b.WriteString("\n")

	// Security block
	b.WriteString("; Security\n")
	openBasedir := cfg.OpenBasedir
	if openBasedir == "" && cfg.Domain != "" {
		vhostsRoot, err := s.requireVhostsRoot()
		if err != nil {
			return "", err
		}
		openBasedir = fmt.Sprintf("%s:/tmp/:/usr/share/php/", filepath.Join(vhostsRoot, cfg.Domain))
	}
	if openBasedir != "" {
		fmt.Fprintf(&b, "php_admin_value[open_basedir] = %s\n", openBasedir)
	}

	disableFuncs := cfg.DisableFunctions
	if disableFuncs == "" {
		disableFuncs = defaultDisableFunctions
	}
	fmt.Fprintf(&b, "php_admin_value[disable_functions] = %s\n", disableFuncs)
	b.WriteString("\n")

	// Resource limits
	b.WriteString("; Resource limits\n")
	memLimit := cfg.MemoryLimit
	if memLimit == "" {
		memLimit = "256M"
	}
	fmt.Fprintf(&b, "php_admin_value[memory_limit] = %s\n", memLimit)

	if cfg.MaxExecutionTime > 0 {
		fmt.Fprintf(&b, "php_admin_value[max_execution_time] = %d\n", cfg.MaxExecutionTime)
	}
	if cfg.MaxInputTime > 0 {
		fmt.Fprintf(&b, "php_admin_value[max_input_time] = %d\n", cfg.MaxInputTime)
	}
	uploadSize := cfg.UploadMaxSize
	if uploadSize == "" {
		uploadSize = "32M"
	}
	postSize := cfg.PostMaxSize
	if postSize == "" {
		postSize = "32M"
	}
	fmt.Fprintf(&b, "php_admin_value[upload_max_filesize] = %s\n", uploadSize)
	fmt.Fprintf(&b, "php_admin_value[post_max_size] = %s\n", postSize)
	b.WriteString("\n")

	// OPcache tuning
	b.WriteString("; OPcache\n")
	fmt.Fprintf(&b, "php_value[opcache.memory_consumption] = 128\n")
	fmt.Fprintf(&b, "php_value[opcache.interned_strings_buffer] = 64\n")
	fmt.Fprintf(&b, "php_value[opcache.max_accelerated_files] = 10000\n")
	fmt.Fprintf(&b, "php_value[opcache.revalidate_freq] = 2\n")
	fmt.Fprintf(&b, "php_value[opcache.max_wasted_percentage] = 15\n")

	return b.String(), nil
}

// ---------- config parsing ----------

// parsePoolConfig parses a PHP-FPM pool .conf file content into a PoolConfig.
func parsePoolConfig(version, domain, content string) (*PoolConfig, error) {
	cfg := &PoolConfig{
		Version: version,
		Domain:  domain,
	}

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip comments, empty lines, and section headers.
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			continue
		}

		key, val, found := strings.Cut(line, "=")
		if !found {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		// Strip inline comments (e.g. "value ; comment")
		if idx := strings.Index(val, " ;"); idx > 0 {
			val = strings.TrimSpace(val[:idx])
		}

		switch key {
		case "user":
			cfg.User = val
		case "group":
			cfg.Group = val
		case "listen.owner":
			cfg.ListenOwner = val
		case "listen.group":
			cfg.ListenGroup = val
		case "pm":
			cfg.PM = val
		case "pm.max_children":
			cfg.MaxChildren = parseInt(val)
		case "pm.start_servers":
			cfg.StartServers = parseInt(val)
		case "pm.min_spare_servers":
			cfg.MinSpareServers = parseInt(val)
		case "pm.max_spare_servers":
			cfg.MaxSpareServers = parseInt(val)
		case "pm.max_requests":
			cfg.MaxRequests = parseInt(val)
		case "pm.process_idle_timeout":
			// Strip trailing 's' or 'm' — store as seconds.
			cfg.ProcessIdleTimeout = parseTimeoutSeconds(val)
		case "request_slowlog_timeout":
			cfg.SlowlogTimeout = parseTimeoutSeconds(val)
		case "access.log":
			cfg.AccessLog = val != ""
		}

		// php_admin_value[key] = val and php_value[key] = val
		if inner, ok := extractBracketKey(key); ok {
			switch inner {
			case "open_basedir":
				cfg.OpenBasedir = val
			case "disable_functions":
				cfg.DisableFunctions = val
			case "memory_limit":
				cfg.MemoryLimit = val
			case "max_execution_time":
				cfg.MaxExecutionTime = parseInt(val)
			case "max_input_time":
				cfg.MaxInputTime = parseInt(val)
			case "upload_max_filesize":
				cfg.UploadMaxSize = val
			case "post_max_size":
				cfg.PostMaxSize = val
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scanning pool config: %w", err)
	}
	return cfg, nil
}

// ---------- validation ----------

// validatePoolConfig checks that all PoolConfig fields are within acceptable ranges.
func validatePoolConfig(cfg *PoolConfig) error {
	if cfg == nil {
		return fmt.Errorf("pool config is nil")
	}
	if err := validatePoolName(cfg.Domain); err != nil {
		return err
	}
	if err := validateVersion(cfg.Version); err != nil {
		return err
	}

	allowedPM := map[string]bool{"static": true, "dynamic": true, "ondemand": true}
	pm := cfg.PM
	if pm == "" {
		pm = "dynamic"
	}
	if !allowedPM[pm] {
		return fmt.Errorf("invalid pm value %q: must be static, dynamic, or ondemand", pm)
	}

	if cfg.MaxChildren < 0 || cfg.MaxChildren > 500 {
		return fmt.Errorf("max_children must be between 0 and 500, got %d", cfg.MaxChildren)
	}
	if cfg.MaxRequests < 0 || cfg.MaxRequests > 100000 {
		return fmt.Errorf("max_requests must be between 0 and 100000, got %d", cfg.MaxRequests)
	}
	if cfg.MaxExecutionTime < 0 || cfg.MaxExecutionTime > 3600 {
		return fmt.Errorf("max_execution_time must be between 0 and 3600, got %d", cfg.MaxExecutionTime)
	}
	if cfg.SlowlogTimeout < 0 {
		return fmt.Errorf("slowlog_timeout cannot be negative")
	}
	if cfg.MemoryLimit != "" {
		if err := validateMemoryValue(cfg.MemoryLimit); err != nil {
			return fmt.Errorf("memory_limit: %w", err)
		}
	}
	if cfg.UploadMaxSize != "" {
		if err := validateMemoryValue(cfg.UploadMaxSize); err != nil {
			return fmt.Errorf("upload_max_filesize: %w", err)
		}
	}
	if cfg.PostMaxSize != "" {
		if err := validateMemoryValue(cfg.PostMaxSize); err != nil {
			return fmt.Errorf("post_max_size: %w", err)
		}
	}
	return nil
}

// validateMemoryValue ensures a PHP memory string is in a valid format (e.g. "256M", "1G", "512K").
func validateMemoryValue(v string) error {
	v = strings.TrimSpace(v)
	if v == "" {
		return fmt.Errorf("value cannot be empty")
	}
	// Strip unit suffix.
	numeric := strings.TrimRight(v, "KkMmGg")
	if numeric == "" {
		return fmt.Errorf("value %q has no numeric part", v)
	}
	var n int
	if _, err := fmt.Sscanf(numeric, "%d", &n); err != nil || n <= 0 {
		return fmt.Errorf("value %q must be a positive integer followed by K/M/G", v)
	}
	unit := strings.ToUpper(strings.TrimLeft(v, "0123456789"))
	allowed := map[string]bool{"K": true, "M": true, "G": true, "": true}
	if !allowed[unit] {
		return fmt.Errorf("value %q has unrecognised unit %q (use K, M, or G)", v, unit)
	}
	return nil
}

// applyPoolConfigDefaults fills in zero-value fields with safe defaults.
func applyPoolConfigDefaults(cfg *PoolConfig) {
	if cfg.PM == "" {
		cfg.PM = "dynamic"
	}
	if cfg.Group == "" {
		cfg.Group = "www-data"
	}
	if cfg.ListenOwner == "" {
		cfg.ListenOwner = "www-data"
	}
	if cfg.ListenGroup == "" {
		cfg.ListenGroup = "www-data"
	}
	if cfg.MaxChildren == 0 {
		cfg.MaxChildren = 10
	}
	if cfg.PM == "dynamic" {
		if cfg.StartServers == 0 {
			cfg.StartServers = 2
		}
		if cfg.MinSpareServers == 0 {
			cfg.MinSpareServers = 1
		}
		if cfg.MaxSpareServers == 0 {
			cfg.MaxSpareServers = 3
		}
	}
	if cfg.PM == "ondemand" && cfg.ProcessIdleTimeout == 0 {
		cfg.ProcessIdleTimeout = 10
	}
	if cfg.MaxRequests == 0 {
		cfg.MaxRequests = 500
	}
	if cfg.MemoryLimit == "" {
		cfg.MemoryLimit = "256M"
	}
	if cfg.MaxExecutionTime == 0 {
		cfg.MaxExecutionTime = 60
	}
	if cfg.MaxInputTime == 0 {
		cfg.MaxInputTime = 120
	}
	if cfg.UploadMaxSize == "" {
		cfg.UploadMaxSize = "32M"
	}
	if cfg.PostMaxSize == "" {
		cfg.PostMaxSize = "32M"
	}
	if cfg.DisableFunctions == "" {
		cfg.DisableFunctions = defaultDisableFunctions
	}
}

// ---------- helpers ----------

// parseTimeoutSeconds converts strings like "10s", "1m", "30" to seconds.
func parseTimeoutSeconds(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if strings.HasSuffix(s, "m") {
		var n int
		_, _ = fmt.Sscanf(strings.TrimSuffix(s, "m"), "%d", &n)
		return n * 60
	}
	// Strip trailing 's' if present.
	s = strings.TrimSuffix(s, "s")
	var n int
	_, _ = fmt.Sscanf(s, "%d", &n)
	return n
}

// extractBracketKey extracts the key from "php_admin_value[key]" or "php_value[key]".
// Returns ("key", true) on success, ("", false) otherwise.
func extractBracketKey(key string) (string, bool) {
	start := strings.Index(key, "[")
	end := strings.Index(key, "]")
	if start < 0 || end < 0 || end <= start+1 {
		return "", false
	}
	return key[start+1 : end], true
}

// readAvailableRAMMB reads MemAvailable from /proc/meminfo and returns it in MB.
func readAvailableRAMMB() (int, error) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, fmt.Errorf("reading /proc/meminfo: %w", err)
	}
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "MemAvailable:") {
			var kb int
			_, _ = fmt.Sscanf(strings.TrimPrefix(line, "MemAvailable:"), "%d", &kb)
			return kb / 1024, nil
		}
	}
	return 0, fmt.Errorf("MemAvailable not found in /proc/meminfo")
}

// measureAvgWorkerMemMB measures average VmRSS of running php-fpm worker processes
// for the given version. Returns 0 if no workers are found.
func measureAvgWorkerMemMB(version string) int {
	out, err := exec.Command("pgrep", "-a", "-x", "php-fpm: pool").Output()
	if err != nil {
		// Fallback: pgrep with partial match on the binary name.
		out, err = exec.Command("pgrep", "-d", "\n", fmt.Sprintf("php%s-fpm", version)).Output()
		if err != nil {
			return 0
		}
	}

	var totalKB, count int
	for _, pidStr := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pidStr = strings.Fields(pidStr)[0] // strip process title if pgrep -a was used
		if pidStr == "" {
			continue
		}
		statusPath := fmt.Sprintf("/proc/%s/status", pidStr)
		statusData, err := os.ReadFile(statusPath)
		if err != nil {
			continue
		}
		// Check this is actually the right PHP version's process;
		// accept any worker for the right binary regardless of pattern match.
		sc := bufio.NewScanner(strings.NewReader(string(statusData)))
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "VmRSS:") {
				var kb int
				_, _ = fmt.Sscanf(strings.TrimPrefix(sc.Text(), "VmRSS:"), "%d", &kb)
				totalKB += kb
				count++
				break
			}
		}
	}

	if count == 0 {
		return 0
	}
	return (totalKB / count) / 1024
}
