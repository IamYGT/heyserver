package php

import (
	"strings"
	"testing"
)

func TestNewLeavesVhostsRootUnconfigured(t *testing.T) {
	t.Parallel()

	svc := New()
	if svc.vhostsRoot != "" {
		t.Fatalf("New() vhostsRoot = %q, want unconfigured", svc.vhostsRoot)
	}
	if svc.configRoot != defaultPHPConfigRoot {
		t.Fatalf("New() configRoot = %q, want %q", svc.configRoot, defaultPHPConfigRoot)
	}
}

func TestGetPresets(t *testing.T) {
	t.Parallel()

	presets := GetPresets()
	if len(presets) != 5 {
		t.Fatalf("expected 5 provider-neutral presets, got %d", len(presets))
	}
	names := make(map[string]bool)
	for _, p := range presets {
		names[p.Name] = true
		if p.Config.MaxChildren <= 0 {
			t.Fatalf("preset %q has invalid max_children", p.Name)
		}
	}
	for _, required := range []string{"low", "medium", "high", "wordpress", "laravel"} {
		if !names[required] {
			t.Fatalf("missing preset %q", required)
		}
	}
	if names["etp-portal"] {
		t.Fatal("operator-specific etp-portal preset must not be published")
	}
}

func TestGetPreset(t *testing.T) {
	t.Parallel()

	p, err := GetPreset("medium")
	if err != nil {
		t.Fatalf("GetPreset(medium): %v", err)
	}
	if p.Config.PM != "dynamic" || p.Config.MaxChildren != 10 {
		t.Fatalf("unexpected medium preset: %+v", p.Config)
	}

	_, err = GetPreset("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown preset")
	}
}

func TestValidatePoolConfig(t *testing.T) {
	t.Parallel()

	valid := &PoolConfig{
		Domain:           "example.org",
		Version:          "8.4",
		PM:               "dynamic",
		MaxChildren:      10,
		MaxRequests:      500,
		MaxExecutionTime: 60,
		MemoryLimit:      "256M",
		UploadMaxSize:    "32M",
		PostMaxSize:      "32M",
	}
	if err := validatePoolConfig(valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	tests := []struct {
		name string
		cfg  *PoolConfig
	}{
		{"nil", nil},
		{"empty_domain", &PoolConfig{Domain: "", Version: "8.4"}},
		{"bad_version", &PoolConfig{Domain: "example.org", Version: "9.9"}},
		{"bad_pm", &PoolConfig{Domain: "example.org", Version: "8.4", PM: "invalid"}},
		{"max_children_high", &PoolConfig{Domain: "example.org", Version: "8.4", MaxChildren: 501}},
		{"max_children_negative", &PoolConfig{Domain: "example.org", Version: "8.4", MaxChildren: -1}},
		{"max_requests_high", &PoolConfig{Domain: "example.org", Version: "8.4", MaxRequests: 100001}},
		{"execution_time_high", &PoolConfig{Domain: "example.org", Version: "8.4", MaxExecutionTime: 3601}},
		{"negative_slowlog", &PoolConfig{Domain: "example.org", Version: "8.4", SlowlogTimeout: -1}},
		{"bad_memory", &PoolConfig{Domain: "example.org", Version: "8.4", MemoryLimit: "abc"}},
		{"bad_upload", &PoolConfig{Domain: "example.org", Version: "8.4", UploadMaxSize: "0M"}},
		{"bad_post", &PoolConfig{Domain: "example.org", Version: "8.4", PostMaxSize: "2X"}},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := validatePoolConfig(tc.cfg); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestValidateMemoryValue(t *testing.T) {
	t.Parallel()

	valid := []string{"128M", "1G", "512K", "256"}
	for _, v := range valid {
		if err := validateMemoryValue(v); err != nil {
			t.Fatalf("validateMemoryValue(%q) unexpected error: %v", v, err)
		}
	}

	invalid := []string{"", "abc", "0M", "-5M", "10X"}
	for _, v := range invalid {
		if err := validateMemoryValue(v); err == nil {
			t.Fatalf("validateMemoryValue(%q) expected error", v)
		}
	}
}

func TestParsePoolConfig(t *testing.T) {
	t.Parallel()

	content := `[example.org]
user = deploy
group = deployers
listen.owner = www-data
listen.group = www-data
pm = dynamic
pm.max_children = 12
pm.start_servers = 3
pm.min_spare_servers = 2
pm.max_spare_servers = 4
pm.max_requests = 800
pm.process_idle_timeout = 15s
request_slowlog_timeout = 5s
access.log = /var/log/php8.4-fpm-example.org-access.log
php_admin_value[open_basedir] = /var/www/example.org:/tmp
php_admin_value[disable_functions] = exec
php_admin_value[memory_limit] = 512M
php_admin_value[max_execution_time] = 120
php_admin_value[upload_max_filesize] = 64M
php_admin_value[post_max_size] = 64M
`
	cfg, err := parsePoolConfig("8.4", "example.org", content)
	if err != nil {
		t.Fatalf("parsePoolConfig: %v", err)
	}

	tests := map[string]interface{}{
		"User":               "deploy",
		"Group":              "deployers",
		"PM":                 "dynamic",
		"MaxChildren":        12,
		"StartServers":       3,
		"MinSpareServers":    2,
		"MaxSpareServers":    4,
		"MaxRequests":        800,
		"ProcessIdleTimeout": 15,
		"SlowlogTimeout":     5,
		"OpenBasedir":        "/var/www/example.org:/tmp",
		"DisableFunctions":   "exec",
		"MemoryLimit":        "512M",
		"MaxExecutionTime":   120,
		"UploadMaxSize":      "64M",
		"PostMaxSize":        "64M",
	}

	if cfg.AccessLog != true {
		t.Fatal("AccessLog should be true")
	}
	if cfg.User != tests["User"] {
		t.Fatalf("User = %q", cfg.User)
	}
	if cfg.MaxChildren != tests["MaxChildren"] {
		t.Fatalf("MaxChildren = %d", cfg.MaxChildren)
	}
	if cfg.OpenBasedir != tests["OpenBasedir"] {
		t.Fatalf("OpenBasedir = %q", cfg.OpenBasedir)
	}
}

func TestParseTimeoutSeconds(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want int
	}{
		{"10s", 10},
		{"30", 30},
		{"2m", 120},
		{"", 0},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := parseTimeoutSeconds(tc.in); got != tc.want {
				t.Fatalf("parseTimeoutSeconds(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

func TestExtractBracketKey(t *testing.T) {
	t.Parallel()

	key, ok := extractBracketKey("php_admin_value[memory_limit]")
	if !ok || key != "memory_limit" {
		t.Fatalf("extractBracketKey = %q, %v", key, ok)
	}
	_, ok = extractBracketKey("pm.max_children")
	if ok {
		t.Fatal("expected false for non-bracket key")
	}
}

func TestApplyPoolConfigDefaults(t *testing.T) {
	t.Parallel()

	cfg := &PoolConfig{Domain: "example.org", Version: "8.4"}
	applyPoolConfigDefaults(cfg)

	if cfg.PM != "dynamic" || cfg.MaxChildren != 10 || cfg.MemoryLimit != "256M" {
		t.Fatalf("defaults not applied: %+v", cfg)
	}
	if cfg.Group != "www-data" || cfg.ListenOwner != "www-data" || cfg.ListenGroup != "www-data" {
		t.Fatalf("identity defaults wrong: %+v", cfg)
	}
}

func TestBuildPoolConfigDefaultsToUbuntuWebIdentity(t *testing.T) {
	t.Parallel()

	out := BuildPoolConfig(CreatePoolRequest{
		Name:    "example.org",
		Version: "8.4",
		User:    "www-data",
	})
	for _, want := range []string{
		"user = www-data",
		"group = www-data",
		"listen.owner = www-data",
		"listen.group = www-data",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("BuildPoolConfig missing %q:\n%s", want, out)
		}
	}
}

func TestGeneratePoolConfig(t *testing.T) {
	t.Parallel()

	svc := NewWithConfig(ServiceConfig{VhostsRoot: "/srv/hserver/sites"})
	cfg := &PoolConfig{
		Domain:             "example.org",
		Version:            "8.4",
		PM:                 "ondemand",
		MaxChildren:        8,
		ProcessIdleTimeout: 10,
		MemoryLimit:        "256M",
		MaxExecutionTime:   60,
		SlowlogTimeout:     5,
		AccessLog:          true,
	}
	applyPoolConfigDefaults(cfg)
	out, err := svc.generatePoolConfig(cfg)
	if err != nil {
		t.Fatalf("generatePoolConfig: %v", err)
	}

	checks := []string{
		"[example.org]",
		"user = www-data",
		"group = www-data",
		"pm = ondemand",
		"pm.max_children = 8",
		"pm.process_idle_timeout = 10s",
		"php_admin_value[open_basedir]",
		"/srv/hserver/sites/example.org:/tmp/",
		"php_admin_value[disable_functions]",
		"request_slowlog_timeout = 5s",
		"access.log =",
	}
	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Fatalf("generatePoolConfig missing %q:\n%s", want, out)
		}
	}
}

func TestGeneratePoolConfigRejectsUnavailableVhostsRoot(t *testing.T) {
	t.Parallel()

	svc := NewWithConfig(ServiceConfig{VhostsRoot: "relative/sites"})
	_, err := svc.generatePoolConfig(&PoolConfig{Domain: "example.org", Version: "8.4"})
	if err == nil || !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("generatePoolConfig error = %v, want absolute path error", err)
	}
}

func TestValidatePoolName(t *testing.T) {
	t.Parallel()

	if err := validatePoolName("example.org"); err != nil {
		t.Fatalf("valid name rejected: %v", err)
	}
	if err := validatePoolName(""); err == nil {
		t.Fatal("empty name should fail")
	}
	if err := validatePoolName("bad name!"); err == nil {
		t.Fatal("invalid chars should fail")
	}
}

func TestValidateVersion(t *testing.T) {
	t.Parallel()

	if err := validateVersion("8.4"); err != nil {
		t.Fatalf("8.4 should be valid: %v", err)
	}
	if err := validateVersion("9.0"); err == nil {
		t.Fatal("9.0 should be invalid")
	}
}
