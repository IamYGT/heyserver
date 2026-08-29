package php

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const poolDirPattern = "/etc/php/%s/fpm/pool.d"

const defaultPHPConfigRoot = "/etc/php"

const defaultPHPBinaryRoot = "/usr/sbin"

var (
	// ErrFPMConfigInvalid identifies a failed full PHP-FPM configuration test.
	ErrFPMConfigInvalid = errors.New("PHP-FPM configuration validation failed")
	// ErrFPMLifecycleAction identifies a systemd reload or restart failure after validation.
	ErrFPMLifecycleAction = errors.New("PHP-FPM lifecycle action failed")
)

// PHPVersion represents an installed PHP version with its pools.
type PHPVersion struct {
	Version   string `json:"version"`
	Active    bool   `json:"active"`
	Info      string `json:"info"`
	PoolDir   string `json:"pool_dir"`
	PoolCount int    `json:"pool_count"`
}

// Pool represents a single PHP-FPM pool configuration.
type Pool struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	ConfigFile   string            `json:"config_file"`
	User         string            `json:"user"`
	Group        string            `json:"group"`
	Listen       string            `json:"listen"`
	PM           string            `json:"pm"`
	PMSettings   PMSettings        `json:"pm_settings"`
	OpenBasedir  string            `json:"open_basedir"`
	SocketExists bool              `json:"socket_exists"`
	RawLines     map[string]string `json:"raw,omitempty"`
}

// PMSettings holds process manager settings.
type PMSettings struct {
	MaxChildren        int    `json:"max_children"`
	StartServers       int    `json:"start_servers"`
	MinSpareServers    int    `json:"min_spare_servers"`
	MaxSpareServers    int    `json:"max_spare_servers"`
	ProcessIdleTimeout string `json:"process_idle_timeout,omitempty"`
	MaxRequests        int    `json:"max_requests"`
}

// CreatePoolRequest holds the data needed to create a new pool.
type CreatePoolRequest struct {
	Name       string     `json:"name"`
	Version    string     `json:"version"`
	User       string     `json:"user"`
	Group      string     `json:"group"`
	DomainRoot string     `json:"domain_root"`
	PM         string     `json:"pm"`
	PMSettings PMSettings `json:"pm_settings"`
}

// UpdatePoolRequest holds the data for updating a pool.
type UpdatePoolRequest struct {
	User        string     `json:"user"`
	Group       string     `json:"group"`
	PM          string     `json:"pm"`
	PMSettings  PMSettings `json:"pm_settings"`
	OpenBasedir string     `json:"open_basedir"`
}

// ServiceConfig binds PHP project and generated pool paths to one installation.
type ServiceConfig struct {
	VhostsRoot string
	ConfigRoot string
	BinaryRoot string
}

// Service provides PHP-FPM management operations for one installation.
type Service struct {
	vhostsRoot        string
	configRoot        string
	binaryRoot        string
	runCommand        func(name string, args ...string) ([]byte, error)
	runCommandContext func(ctx context.Context, name string, args ...string) ([]byte, error)
	mu                *sync.Mutex
}

// New creates a new PHP Service instance.
func New() *Service {
	return NewWithConfig(ServiceConfig{
		ConfigRoot: defaultPHPConfigRoot,
		BinaryRoot: defaultPHPBinaryRoot,
	})
}

// NewWithConfig creates an installation-scoped PHP service. Empty or relative
// roots remain unavailable instead of falling back to another installation's
// filesystem layout.
func NewWithConfig(config ServiceConfig) *Service {
	root := strings.TrimSpace(config.VhostsRoot)
	if root == "" || !filepath.IsAbs(root) {
		root = ""
	} else {
		root = filepath.Clean(root)
	}
	configRoot := cleanAbsoluteRoot(config.ConfigRoot, defaultPHPConfigRoot)
	binaryRoot := cleanAbsoluteRoot(config.BinaryRoot, defaultPHPBinaryRoot)
	return &Service{
		vhostsRoot:        root,
		configRoot:        configRoot,
		binaryRoot:        binaryRoot,
		runCommand:        defaultPHPCommandRunner,
		runCommandContext: defaultPHPCommandContextRunner,
		mu:                &sync.Mutex{},
	}
}

func cleanAbsoluteRoot(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return filepath.Clean(fallback)
	}
	if !filepath.IsAbs(value) {
		return ""
	}
	return filepath.Clean(value)
}

func defaultPHPCommandRunner(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

func defaultPHPCommandContextRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		return nil
	}
	cmd.WaitDelay = 250 * time.Millisecond
	var output boundedPHPCommandOutput
	cmd.Stdout = &output
	cmd.Stderr = &output
	runErr := cmd.Run()
	return output.Bytes(), runErr
}

type boundedPHPCommandOutput struct {
	mu   sync.Mutex
	data []byte
}

func (o *boundedPHPCommandOutput) Write(data []byte) (int, error) {
	const maxBytes = 64 * 1024
	originalLen := len(data)
	o.mu.Lock()
	defer o.mu.Unlock()
	if len(o.data) < maxBytes {
		remaining := maxBytes - len(o.data)
		if len(data) > remaining {
			data = data[:remaining]
		}
		o.data = append(o.data, data...)
	}
	return originalLen, nil
}

func (o *boundedPHPCommandOutput) Bytes() []byte {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]byte(nil), o.data...)
}

func (s *Service) runLifecycleCommand(name string, args ...string) ([]byte, error) {
	if s != nil && s.runCommand != nil {
		return s.runCommand(name, args...)
	}
	return defaultPHPCommandRunner(name, args...)
}

func (s *Service) runLifecycleCommandContext(ctx context.Context, name string, args ...string) ([]byte, error) {
	if s != nil && s.runCommandContext != nil {
		return s.runCommandContext(ctx, name, args...)
	}
	return defaultPHPCommandContextRunner(ctx, name, args...)
}

func (s *Service) requireVhostsRoot() (string, error) {
	if s == nil || s.vhostsRoot == "" {
		return "", fmt.Errorf("PHP vhosts root must be configured as an absolute path")
	}
	return s.vhostsRoot, nil
}

// DetectVersions scans /etc/php/*/fpm/ and returns installed versions.
func (s *Service) DetectVersions() ([]PHPVersion, error) {
	matches, err := filepath.Glob("/etc/php/*/fpm")
	if err != nil {
		return nil, fmt.Errorf("scanning php fpm dirs: %w", err)
	}

	var versions []PHPVersion
	for _, match := range matches {
		// Extract version from path, e.g. "/etc/php/8.4/fpm" → "8.4"
		parts := strings.Split(match, "/")
		if len(parts) < 4 {
			continue
		}
		ver := parts[3]

		poolDir := fmt.Sprintf(poolDirPattern, ver)
		pools, _ := listPoolFiles(poolDir)

		active := isServiceActive(ver)
		info := getPHPInfo(ver)

		versions = append(versions, PHPVersion{
			Version:   ver,
			Active:    active,
			Info:      info,
			PoolDir:   poolDir,
			PoolCount: len(pools),
		})
	}
	return versions, nil
}

// ListPools returns all pools for a specific PHP version.
func (s *Service) ListPools(version string) ([]Pool, error) {
	poolDir := fmt.Sprintf(poolDirPattern, version)
	files, err := listPoolFiles(poolDir)
	if err != nil {
		return nil, fmt.Errorf("listing pools for php%s: %w", version, err)
	}

	var pools []Pool
	for _, f := range files {
		pool, err := parsePoolFile(version, f)
		if err != nil {
			// Skip unparseable files, log via caller
			continue
		}
		pools = append(pools, pool)
	}
	return pools, nil
}

// ListAllPools returns all pools across all installed PHP versions.
func (s *Service) ListAllPools() ([]Pool, error) {
	versions, err := s.DetectVersions()
	if err != nil {
		return nil, err
	}

	var all []Pool
	for _, v := range versions {
		pools, err := s.ListPools(v.Version)
		if err != nil {
			continue
		}
		all = append(all, pools...)
	}
	return all, nil
}

// GetPool returns the configuration for a specific pool.
func (s *Service) GetPool(version, poolName string) (*Pool, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	if err := validatePoolName(poolName); err != nil {
		return nil, err
	}
	confPath := poolConfPath(version, poolName)
	pool, err := parsePoolFile(version, confPath)
	if err != nil {
		return nil, fmt.Errorf("reading pool %s@%s: %w", poolName, version, err)
	}
	return &pool, nil
}

// CreatePool writes a new pool config file.
func (s *Service) CreatePool(req CreatePoolRequest) (*Pool, error) {
	if err := validatePoolName(req.Name); err != nil {
		return nil, err
	}
	if err := validateVersion(req.Version); err != nil {
		return nil, err
	}

	confPath := poolConfPath(req.Version, req.Name)
	if _, err := os.Stat(confPath); err == nil {
		return nil, fmt.Errorf("pool %s already exists for php%s", req.Name, req.Version)
	}

	// Apply defaults if not set
	req = applyPMDefaults(req)

	content := BuildPoolConfig(req)
	if err := os.WriteFile(confPath, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("writing pool config: %w", err)
	}

	return s.GetPool(req.Version, req.Name)
}

// UpdatePool updates an existing pool config file.
func (s *Service) UpdatePool(version, poolName string, req UpdatePoolRequest) (*Pool, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	if err := validatePoolName(poolName); err != nil {
		return nil, err
	}
	confPath := poolConfPath(version, poolName)
	if _, err := os.Stat(confPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("pool %s not found for php%s", poolName, version)
	}

	existing, err := s.GetPool(version, poolName)
	if err != nil {
		return nil, err
	}

	// Merge update into existing (preserve name/version)
	createReq := CreatePoolRequest{
		Name:       poolName,
		Version:    version,
		User:       req.User,
		Group:      req.Group,
		PM:         req.PM,
		PMSettings: req.PMSettings,
	}
	// Derive domain root from open_basedir if provided
	if req.OpenBasedir != "" {
		// Take the first path component as domain root
		parts := strings.SplitN(req.OpenBasedir, ":", 2)
		createReq.DomainRoot = parts[0]
	} else if existing.OpenBasedir != "" {
		parts := strings.SplitN(existing.OpenBasedir, ":", 2)
		createReq.DomainRoot = parts[0]
	}
	createReq = applyPMDefaults(createReq)

	content := BuildPoolConfig(createReq)
	if err := os.WriteFile(confPath, []byte(content), 0644); err != nil {
		return nil, fmt.Errorf("updating pool config: %w", err)
	}

	return s.GetPool(version, poolName)
}

// DeletePool removes a pool config file.
func (s *Service) DeletePool(version, poolName string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	if err := validatePoolName(poolName); err != nil {
		return err
	}
	confPath := poolConfPath(version, poolName)
	if _, err := os.Stat(confPath); os.IsNotExist(err) {
		return fmt.Errorf("pool %s not found for php%s", poolName, version)
	}
	return os.Remove(confPath)
}

// TestFPM validates the complete PHP-FPM configuration for a version.
func (s *Service) TestFPM(version string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	binary := "php-fpm" + version
	out, err := s.runLifecycleCommand(binary, "-t")
	if err != nil {
		return fmt.Errorf("%w: testing php%s-fpm configuration: %v — %s", ErrFPMConfigInvalid, version, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RestartFPM validates configuration, then restarts PHP-FPM for a version.
func (s *Service) RestartFPM(version string) error {
	if err := s.TestFPM(version); err != nil {
		return err
	}
	svcName := fmt.Sprintf("php%s-fpm", version)
	out, err := s.runLifecycleCommand("systemctl", "restart", svcName)
	if err != nil {
		return fmt.Errorf("%w: restarting %s: %v — %s", ErrFPMLifecycleAction, svcName, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ---------- helpers ----------

func poolConfPath(version, poolName string) string {
	return filepath.Join(fmt.Sprintf(poolDirPattern, version), poolName+".conf")
}

func listPoolFiles(poolDir string) ([]string, error) {
	return filepath.Glob(filepath.Join(poolDir, "*.conf"))
}

func isServiceActive(version string) bool {
	svcName := fmt.Sprintf("php%s-fpm", version)
	cmd := exec.Command("systemctl", "is-active", "--quiet", svcName)
	return cmd.Run() == nil
}

func getPHPInfo(version string) string {
	binary := fmt.Sprintf("php%s", version)
	out, err := exec.Command(binary, "-v").Output()
	if err != nil {
		return ""
	}
	// Return only the first line
	line, _, _ := strings.Cut(string(out), "\n")
	return strings.TrimSpace(line)
}

// parsePoolFile reads a .conf file and builds a Pool struct.
func parsePoolFile(version, confPath string) (Pool, error) {
	f, err := os.Open(confPath)
	if err != nil {
		return Pool{}, err
	}
	defer func() { _ = f.Close() }()

	pool := Pool{
		Version:    version,
		ConfigFile: confPath,
		RawLines:   make(map[string]string),
	}

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Pool name header: [poolname]
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			pool.Name = line[1 : len(line)-1]
			continue
		}

		// Skip comments and empty lines
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
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

		pool.RawLines[key] = val

		switch key {
		case "user":
			pool.User = val
		case "group":
			pool.Group = val
		case "listen":
			pool.Listen = val
		case "pm":
			pool.PM = val
		case "pm.max_children":
			pool.PMSettings.MaxChildren = parseInt(val)
		case "pm.start_servers":
			pool.PMSettings.StartServers = parseInt(val)
		case "pm.min_spare_servers":
			pool.PMSettings.MinSpareServers = parseInt(val)
		case "pm.max_spare_servers":
			pool.PMSettings.MaxSpareServers = parseInt(val)
		case "pm.process_idle_timeout":
			pool.PMSettings.ProcessIdleTimeout = val
		case "pm.max_requests":
			pool.PMSettings.MaxRequests = parseInt(val)
		}

		// php_admin_value[open_basedir]
		if strings.Contains(key, "open_basedir") {
			pool.OpenBasedir = val
		}
	}

	if err := scanner.Err(); err != nil {
		return Pool{}, err
	}

	// Derive pool name from filename if not found in header
	if pool.Name == "" {
		base := filepath.Base(confPath)
		pool.Name = strings.TrimSuffix(base, ".conf")
	}

	// Check socket existence
	pool.SocketExists = isSocketPresent(pool.Listen)

	return pool, nil
}

func isSocketPresent(listen string) bool {
	if !strings.HasPrefix(listen, "/") {
		return false // TCP socket, not a file
	}
	_, err := os.Stat(listen)
	return err == nil
}

func parseInt(s string) int {
	var n int
	_, _ = fmt.Sscanf(s, "%d", &n)
	return n
}

func validatePoolName(name string) error {
	if name == "" {
		return fmt.Errorf("pool name cannot be empty")
	}
	for _, c := range name {
		if !isAlphaNumDashDot(c) {
			return fmt.Errorf("invalid pool name %q: only alphanumeric, dash, and dot allowed", name)
		}
	}
	return nil
}

func isAlphaNumDashDot(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9') || c == '-' || c == '.' || c == '_'
}

func validateVersion(version string) error {
	allowed := []string{"7.4", "8.0", "8.1", "8.2", "8.3", "8.4", "8.5"}
	for _, v := range allowed {
		if version == v {
			return nil
		}
	}
	return fmt.Errorf("unsupported PHP version: %s", version)
}

// ValidateVersion is the exported form of validateVersion for use by API handlers.
func ValidateVersion(version string) error {
	return validateVersion(version)
}

func applyPMDefaults(req CreatePoolRequest) CreatePoolRequest {
	if req.Group == "" {
		req.Group = "www-data"
	}
	if req.PM == "" {
		req.PM = "dynamic"
	}
	if req.PMSettings.MaxChildren == 0 {
		req.PMSettings.MaxChildren = 5
	}
	if req.PMSettings.StartServers == 0 {
		req.PMSettings.StartServers = 2
	}
	if req.PMSettings.MinSpareServers == 0 {
		req.PMSettings.MinSpareServers = 1
	}
	if req.PMSettings.MaxSpareServers == 0 {
		req.PMSettings.MaxSpareServers = 3
	}
	if req.PMSettings.MaxRequests == 0 {
		req.PMSettings.MaxRequests = 500
	}
	if req.PMSettings.ProcessIdleTimeout == "" {
		req.PMSettings.ProcessIdleTimeout = "10s"
	}
	return req
}
