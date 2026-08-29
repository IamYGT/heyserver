package php

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// OPcacheStatus holds the OPcache runtime metrics for a given PHP version.
type OPcacheStatus struct {
	Enabled    bool `json:"enabled"`
	CacheFull  bool `json:"cache_full"`

	// Memory statistics
	UsedMemory           int64   `json:"used_memory"`
	FreeMemory           int64   `json:"free_memory"`
	WastedMemory         int64   `json:"wasted_memory"`
	MemoryUsagePercent   float64 `json:"memory_usage_percent"`

	// Script / key statistics
	CachedScripts int   `json:"cached_scripts"`
	CachedKeys    int   `json:"cached_keys"`
	MaxCachedKeys int   `json:"max_cached_keys"`
	Hits          int64 `json:"hits"`
	Misses        int64 `json:"misses"`
	HitRate       float64 `json:"hit_rate"` // percentage

	// Configuration snapshot
	MemoryConsumption int `json:"memory_consumption_mb"`
	MaxAccelFiles     int `json:"max_accelerated_files"`

	// JIT (PHP 8.x only)
	JITEnabled    bool  `json:"jit_enabled"`
	JITBufferSize int64 `json:"jit_buffer_size"`
	JITBufferUsed int64 `json:"jit_buffer_used"`
}

// opcacheRawOutput is the intermediate structure we decode from the PHP script.
type opcacheRawOutput struct {
	Status *opcacheStatusRaw `json:"status"`
	Config *opcacheConfigRaw `json:"config"`
}

type opcacheStatusRaw struct {
	OPcacheEnabled bool `json:"opcache_enabled"`
	CacheFull      bool `json:"cache_full"`
	Memory         struct {
		UsedMemory   float64 `json:"used_memory"`
		FreeMemory   float64 `json:"free_memory"`
		WastedMemory float64 `json:"wasted_memory"`
		CurrentWastedPercentage float64 `json:"current_wasted_percentage"`
	} `json:"memory_usage"`
	Stats struct {
		NumCachedScripts int64   `json:"num_cached_scripts"`
		NumCachedKeys    int64   `json:"num_cached_keys"`
		MaxCachedKeys    int64   `json:"max_cached_keys"`
		Hits             int64   `json:"hits"`
		Misses           int64   `json:"misses"`
		OPcacheHitRate   float64 `json:"opcache_hit_rate"`
	} `json:"opcache_statistics"`
	JIT *struct {
		Enabled    bool  `json:"enabled"`
		BufferSize int64 `json:"buffer_size"`
		BufferFree int64 `json:"buffer_free"`
	} `json:"jit"`
}

type opcacheConfigRaw struct {
	Directives struct {
		MemoryConsumption int `json:"opcache.memory_consumption"`
		MaxAccelFiles     int `json:"opcache.max_accelerated_files"`
	} `json:"directives"`
}

// opcacheScript is the PHP code we run via CLI to collect OPcache data.
// We write it to a temp file, execute it with the target PHP binary, then
// remove it.
const opcacheScript = `<?php
$status = function_exists('opcache_get_status') ? opcache_get_status(false) : null;
$config = function_exists('opcache_get_configuration') ? opcache_get_configuration() : null;

// Patch: PHP CLI opcache is separate from FPM; if disabled in CLI, return minimal info.
if ($status === false) {
    echo json_encode(['status' => null, 'config' => $config]);
    exit(0);
}

echo json_encode(['status' => $status, 'config' => $config]);
`

// GetOPcacheStatus returns the OPcache metrics for the given PHP version by
// running a temporary PHP script via the CLI binary for that version.
//
// Note: PHP CLI and PHP-FPM maintain separate OPcache instances. The CLI
// result reflects the FPM instance only when opcache.enable_cli = 1.
// For accurate FPM data the preferred method is a web request, but CLI gives a
// useful view of the compiled-in limits and the general hit/miss ratio when the
// same php.ini is shared.
func (s *Service) GetOPcacheStatus(version string) (*OPcacheStatus, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}

	binary := fmt.Sprintf("php%s", version)

	// Write temp PHP script.
	tmpFile, err := os.CreateTemp("", "hserver-opcache-*.php")
	if err != nil {
		return nil, fmt.Errorf("creating temp opcache script: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmpFile.WriteString(opcacheScript); err != nil {
		_ = tmpFile.Close()
		return nil, fmt.Errorf("writing opcache script: %w", err)
	}
	_ = tmpFile.Close()

	out, err := runCommand(binary, tmpPath)
	if err != nil {
		return nil, fmt.Errorf("running opcache script via %s: %w", binary, err)
	}

	var raw opcacheRawOutput
	if err := json.Unmarshal([]byte(out), &raw); err != nil {
		return nil, fmt.Errorf("parsing opcache JSON output: %w (raw: %s)", err, truncate(out, 200))
	}

	return buildOPcacheStatus(&raw), nil
}

// ResetOPcache resets the OPcache for the given PHP version by running a
// temporary PHP script that calls opcache_reset().
func (s *Service) ResetOPcache(version string) error {
	if err := validateVersion(version); err != nil {
		return err
	}

	const resetScript = `<?php
if (!function_exists('opcache_reset')) {
    fwrite(STDERR, "opcache_reset not available\n");
    exit(1);
}
$ok = opcache_reset();
echo $ok ? "ok" : "failed";
`
	binary := fmt.Sprintf("php%s", version)

	tmpFile, err := os.CreateTemp("", "hserver-opcache-reset-*.php")
	if err != nil {
		return fmt.Errorf("creating temp reset script: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmpFile.WriteString(resetScript); err != nil {
		_ = tmpFile.Close()
		return fmt.Errorf("writing reset script: %w", err)
	}
	_ = tmpFile.Close()

	out, err := runCommand(binary, tmpPath)
	if err != nil {
		return fmt.Errorf("running opcache reset via %s: %w", binary, err)
	}
	if out != "ok" {
		return fmt.Errorf("opcache_reset returned unexpected output: %s", out)
	}
	return nil
}

// GetOPcacheStatusBySocket fetches live FPM OPcache stats by writing a
// temporary PHP script to /tmp, then executing it via a CGI-style web request
// to the pool's Unix socket. This gives accurate per-FPM-process data instead
// of CLI data.
//
// The pool must have pm.status_path configured (or any reachable path) and the
// php.ini for that pool must have opcache.enable = 1.
func (s *Service) GetOPcacheStatusBySocket(version, poolName string) (*OPcacheStatus, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	if err := validatePoolName(poolName); err != nil {
		return nil, err
	}

	pool, err := s.resolvePoolByName(version, poolName)
	if err != nil {
		return nil, fmt.Errorf("resolving pool %s@php%s: %w", poolName, version, err)
	}

	socketPath := pool.Listen
	if !strings.HasPrefix(socketPath, "/") {
		return nil, fmt.Errorf("pool %s uses a TCP listener; only Unix sockets are supported", poolName)
	}

	// Write a temp PHP file that the FPM worker will execute.
	tmpDir := "/tmp"
	tmpFile, err := os.CreateTemp(tmpDir, "hserver-opcache-fpm-*.php")
	if err != nil {
		return nil, fmt.Errorf("creating temp fpm opcache script: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmpFile.WriteString(opcacheScript); err != nil {
		_ = tmpFile.Close()
		return nil, err
	}
	_ = tmpFile.Close()
	// FPM worker needs to read the file.
	if err := os.Chmod(tmpPath, 0644); err != nil {
		return nil, err
	}

	raw, err := fcgiQuery(socketPath, tmpPath, "", 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("fcgi opcache query: %w", err)
	}

	var rawOut opcacheRawOutput
	if err := json.Unmarshal(raw, &rawOut); err != nil {
		return nil, fmt.Errorf("parsing fpm opcache JSON: %w", err)
	}
	return buildOPcacheStatus(&rawOut), nil
}

// ---------- helpers ----------

// runCommand executes a binary with the given arguments and returns trimmed stdout.
func runCommand(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func buildOPcacheStatus(raw *opcacheRawOutput) *OPcacheStatus {
	out := &OPcacheStatus{}

	if raw.Status == nil {
		// OPcache disabled or not available.
		out.Enabled = false
		if raw.Config != nil {
			out.MemoryConsumption = raw.Config.Directives.MemoryConsumption
			out.MaxAccelFiles = raw.Config.Directives.MaxAccelFiles
		}
		return out
	}

	st := raw.Status
	out.Enabled = st.OPcacheEnabled
	out.CacheFull = st.CacheFull

	out.UsedMemory = int64(st.Memory.UsedMemory)
	out.FreeMemory = int64(st.Memory.FreeMemory)
	out.WastedMemory = int64(st.Memory.WastedMemory)

	total := out.UsedMemory + out.FreeMemory
	if total > 0 {
		out.MemoryUsagePercent = float64(out.UsedMemory) / float64(total) * 100
	}

	out.CachedScripts = int(st.Stats.NumCachedScripts)
	out.CachedKeys = int(st.Stats.NumCachedKeys)
	out.MaxCachedKeys = int(st.Stats.MaxCachedKeys)
	out.Hits = st.Stats.Hits
	out.Misses = st.Stats.Misses
	out.HitRate = st.Stats.OPcacheHitRate

	if raw.Config != nil {
		out.MemoryConsumption = raw.Config.Directives.MemoryConsumption
		out.MaxAccelFiles = raw.Config.Directives.MaxAccelFiles
	}

	if st.JIT != nil {
		out.JITEnabled = st.JIT.Enabled
		out.JITBufferSize = st.JIT.BufferSize
		// buffer_free gives us free space; used = total - free
		if st.JIT.BufferSize > 0 {
			out.JITBufferUsed = st.JIT.BufferSize - st.JIT.BufferFree
		}
	}

	return out
}

// truncate returns the first n bytes of s.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
