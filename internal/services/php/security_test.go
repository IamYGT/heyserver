package php

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizePHPKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want string
	}{
		{"php_admin_value[disable_functions]", "disable_functions"},
		{"php_admin_flag[display_errors]", "display_errors"},
		{"php_value[open_basedir]", "open_basedir"},
		{"php_flag[expose_php]", "expose_php"},
		{"plain_key", "plain_key"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			if got := normalizePHPKey(tc.in); got != tc.want {
				t.Fatalf("normalizePHPKey(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestPhpBoolVal(t *testing.T) {
	t.Parallel()

	truthy := []string{"1", "on", "ON", "true", "True", "yes", " YES "}
	for _, v := range truthy {
		if !phpBoolVal(v) {
			t.Fatalf("phpBoolVal(%q) = false, want true", v)
		}
	}
	falsy := []string{"0", "off", "false", "no", "", "maybe"}
	for _, v := range falsy {
		if phpBoolVal(v) {
			t.Fatalf("phpBoolVal(%q) = true, want false", v)
		}
	}
}

func TestComputeSecurityScore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		settings  poolSecuritySettings
		wantScore int
		wantLevel string
		wantIssue int
	}{
		{
			name: "strict_like",
			settings: poolSecuritySettings{
				DisableFunctions:      "exec,system",
				OpenBasedir:           "/var/www:/tmp",
				AllowURLInclude:       "Off",
				DisplayErrors:         "Off",
				ExposePhp:             "Off",
				SessionCookieSecure:   "On",
				SessionCookieHttpOnly: "On",
			},
			wantScore: 100,
			wantLevel: "strict",
			wantIssue: 0,
		},
		{
			name: "permissive_like",
			settings: poolSecuritySettings{
				DisableFunctions:      "",
				OpenBasedir:           "",
				AllowURLInclude:       "On",
				DisplayErrors:         "On",
				ExposePhp:             "On",
				SessionCookieSecure:   "Off",
				SessionCookieHttpOnly: "Off",
			},
			wantScore: 0,
			wantLevel: "custom",
			wantIssue: 7,
		},
		{
			name: "moderate_partial",
			settings: poolSecuritySettings{
				DisableFunctions:      "system,passthru",
				OpenBasedir:           "/var/www:/tmp",
				AllowURLInclude:       "Off",
				DisplayErrors:         "Off",
				ExposePhp:             "On",
				SessionCookieSecure:   "On",
				SessionCookieHttpOnly: "On",
			},
			wantScore: 90,
			wantLevel: "strict",
			wantIssue: 1,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := computeScore(tc.settings, "")
			if got.Score != tc.wantScore {
				t.Fatalf("Score = %d, want %d", got.Score, tc.wantScore)
			}
			if got.Level != tc.wantLevel {
				t.Fatalf("Level = %q, want %q", got.Level, tc.wantLevel)
			}
			if len(got.Issues) != tc.wantIssue {
				t.Fatalf("Issues len = %d, want %d (%+v)", len(got.Issues), tc.wantIssue, got.Issues)
			}
		})
	}
}

func TestComputeSecurityScoreUsesConfiguredRecommendation(t *testing.T) {
	t.Parallel()

	want := "/srv/hserver/sites/example.org/httpdocs:/tmp:/usr/share/php"
	score := computeScore(poolSecuritySettings{}, want)
	for _, issue := range score.Issues {
		if issue.Setting == "open_basedir" {
			if issue.Recommended != want {
				t.Fatalf("recommended open_basedir = %q, want %q", issue.Recommended, want)
			}
			return
		}
	}
	t.Fatal("open_basedir issue not found")
}

func TestResolveDocrootUsesConfiguredVhostsRoot(t *testing.T) {
	t.Parallel()

	svc := NewWithConfig(ServiceConfig{VhostsRoot: "/srv/hserver/sites"})
	got, err := svc.resolveDocroot(filepath.Join(t.TempDir(), "missing.conf"), "example.org")
	if err != nil {
		t.Fatalf("resolveDocroot: %v", err)
	}
	if got != "/srv/hserver/sites/example.org/httpdocs" {
		t.Fatalf("resolveDocroot = %q", got)
	}
}

func TestClassifyLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		score int
		want  string
	}{
		{100, "strict"},
		{90, "strict"},
		{75, "moderate"},
		{60, "moderate"},
		{45, "permissive"},
		{20, "custom"},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			if got := classifyLevel(tc.score); got != tc.want {
				t.Fatalf("classifyLevel(%d) = %q, want %q", tc.score, got, tc.want)
			}
		})
	}
}

func TestCoalesceEmpty(t *testing.T) {
	t.Parallel()

	if got := coalesceEmpty("", "Off"); got != "Off" {
		t.Fatalf("coalesceEmpty empty = %q", got)
	}
	if got := coalesceEmpty("On", "Off"); got != "On" {
		t.Fatalf("coalesceEmpty value = %q", got)
	}
}

func TestMatchProfile(t *testing.T) {
	t.Parallel()

	strictSettings := poolSecuritySettings{
		DisableFunctions:      strings.Join(SecurityProfiles["strict"].DisableFunctions, ","),
		OpenBasedir:           "/var/www:/tmp",
		AllowURLInclude:       "Off",
		DisplayErrors:         "Off",
		ExposePhp:             "Off",
		SessionCookieSecure:   "On",
		SessionCookieHttpOnly: "On",
	}
	p := matchProfile(strictSettings)
	if p == nil || p.Level != "strict" {
		t.Fatalf("matchProfile strict: %+v", p)
	}
}

func TestBuildSecurityLine(t *testing.T) {
	t.Parallel()

	profile := SecurityProfiles["strict"]
	openBasedir := "/var/www/example.org/httpdocs:/tmp:/usr/share/php"

	line := buildSecurityLine("disable_functions", profile, openBasedir)
	if !strings.Contains(line, "php_admin_value[disable_functions]") {
		t.Fatalf("disable_functions line = %q", line)
	}
	if !strings.Contains(line, "exec") {
		t.Fatalf("expected exec in disable_functions: %q", line)
	}

	flagLine := buildSecurityLine("display_errors", profile, openBasedir)
	if flagLine != "php_admin_flag[display_errors] = Off" {
		t.Fatalf("display_errors line = %q", flagLine)
	}

	emptyOB := buildSecurityLine("open_basedir", SecurityProfiles["permissive"], "")
	if emptyOB != "" {
		t.Fatalf("permissive open_basedir should be empty, got %q", emptyOB)
	}
}

func TestBuildMissingSecurityLines(t *testing.T) {
	t.Parallel()

	profile := SecurityProfiles["moderate"]
	emitted := map[string]bool{"disable_functions": true}
	lines := buildMissingSecurityLines(profile, "/var/www:/tmp", emitted)
	if len(lines) == 0 {
		t.Fatal("expected missing security lines")
	}
	for _, line := range lines {
		if strings.Contains(line, "disable_functions") {
			t.Fatalf("should not re-emit disable_functions: %q", line)
		}
	}
}

func TestBoolToOnOff(t *testing.T) {
	t.Parallel()

	if boolToOnOff(true) != "On" || boolToOnOff(false) != "Off" {
		t.Fatal("boolToOnOff mismatch")
	}
}

func TestPatchPoolSecuritySettings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	conf := filepath.Join(dir, "example.org.conf")
	initial := `[example.org]
php_admin_value[disable_functions] = old_func
php_admin_flag[display_errors] = On
; keep this comment
user = www-data
`
	if err := os.WriteFile(conf, []byte(initial), 0644); err != nil {
		t.Fatalf("write conf: %v", err)
	}

	profile := SecurityProfiles["strict"]
	openBasedir := "/var/www/example.org/httpdocs:/tmp:/usr/share/php"
	if err := patchPoolSecuritySettings(conf, profile, openBasedir); err != nil {
		t.Fatalf("patchPoolSecuritySettings: %v", err)
	}

	updated, err := os.ReadFile(conf)
	if err != nil {
		t.Fatalf("read conf: %v", err)
	}
	body := string(updated)
	if strings.Contains(body, "old_func") {
		t.Fatalf("old disable_functions still present: %s", body)
	}
	if !strings.Contains(body, "php_admin_flag[display_errors] = Off") {
		t.Fatalf("display_errors not patched: %s", body)
	}
	if !strings.Contains(body, "user = www-data") {
		t.Fatal("non-security line should be preserved")
	}
	if !strings.Contains(body, "; keep this comment") {
		t.Fatal("comment should be preserved")
	}
}

func TestListSecurityProfiles(t *testing.T) {
	t.Parallel()

	svc := &Service{}
	profiles := svc.ListSecurityProfiles()
	if len(profiles) != 3 {
		t.Fatalf("len = %d, want 3", len(profiles))
	}
	if profiles[0].Level != "strict" || profiles[1].Level != "moderate" || profiles[2].Level != "permissive" {
		t.Fatalf("unexpected order: %+v", profiles)
	}
}
