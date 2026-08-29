package php

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SecurityProfile defines a predefined set of PHP security settings.
type SecurityProfile struct {
	Name                  string   `json:"name"`
	Description           string   `json:"description"`
	Level                 string   `json:"level"` // "strict", "moderate", "permissive"
	DisableFunctions      []string `json:"disable_functions"`
	OpenBasedir           string   `json:"open_basedir"` // template: {DOCROOT}:/tmp:/usr/share/php
	AllowURLFopen         bool     `json:"allow_url_fopen"`
	AllowURLInclude       bool     `json:"allow_url_include"`
	ExposePhp             bool     `json:"expose_php"`
	DisplayErrors         bool     `json:"display_errors"`
	LogErrors             bool     `json:"log_errors"`
	SessionCookieSecure   bool     `json:"session_cookie_secure"`
	SessionCookieHttpOnly bool     `json:"session_cookie_httponly"`
	SessionCookieSameSite string   `json:"session_cookie_samesite"` // Strict, Lax, None
}

// SecurityScore represents a scored evaluation of a domain's PHP security settings.
type SecurityScore struct {
	Score  int             `json:"score"`
	Level  string          `json:"level"` // "strict", "moderate", "permissive", "custom"
	Issues []SecurityIssue `json:"issues"`
}

// SecurityIssue describes a single security finding.
type SecurityIssue struct {
	Severity    string `json:"severity"` // "critical", "warning", "info"
	Setting     string `json:"setting"`
	Current     string `json:"current"`
	Recommended string `json:"recommended"`
	Description string `json:"description"`
}

// SecurityProfiles contains the built-in predefined security profiles.
var SecurityProfiles = map[string]SecurityProfile{
	"strict": {
		Name:        "Strict",
		Description: "Maximum security — suitable for production WordPress/Laravel",
		Level:       "strict",
		DisableFunctions: []string{
			"exec", "system", "passthru", "shell_exec", "proc_open",
			"proc_close", "proc_get_status", "proc_nice", "proc_terminate",
			"popen", "curl_exec", "curl_multi_exec", "parse_ini_file",
			"show_source", "highlight_file", "phpinfo",
		},
		OpenBasedir:           "{DOCROOT}:/tmp:/usr/share/php",
		AllowURLFopen:         false,
		AllowURLInclude:       false,
		ExposePhp:             false,
		DisplayErrors:         false,
		LogErrors:             true,
		SessionCookieSecure:   true,
		SessionCookieHttpOnly: true,
		SessionCookieSameSite: "Strict",
	},
	"moderate": {
		Name:        "Moderate",
		Description: "Balanced security — allows controlled exec for Composer/artisan",
		Level:       "moderate",
		DisableFunctions: []string{
			"system", "passthru", "shell_exec", "proc_open", "popen",
			"show_source", "highlight_file",
		},
		// exec allowed for Composer
		OpenBasedir:           "{DOCROOT}:/tmp:/usr/share/php:/usr/local/bin",
		AllowURLFopen:         true, // needed for Composer
		AllowURLInclude:       false,
		ExposePhp:             false,
		DisplayErrors:         false,
		LogErrors:             true,
		SessionCookieSecure:   true,
		SessionCookieHttpOnly: true,
		SessionCookieSameSite: "Lax",
	},
	"permissive": {
		Name:                  "Permissive",
		Description:           "Development/staging — minimal restrictions",
		Level:                 "permissive",
		DisableFunctions:      []string{},
		OpenBasedir:           "", // no restriction
		AllowURLFopen:         true,
		AllowURLInclude:       false,
		ExposePhp:             true,
		DisplayErrors:         true,
		LogErrors:             true,
		SessionCookieSecure:   false,
		SessionCookieHttpOnly: true,
		SessionCookieSameSite: "Lax",
	},
}

// ListSecurityProfiles returns all predefined security profiles as a slice.
func (s *Service) ListSecurityProfiles() []SecurityProfile {
	result := make([]SecurityProfile, 0, len(SecurityProfiles))
	// Deterministic order: strict, moderate, permissive
	for _, key := range []string{"strict", "moderate", "permissive"} {
		if p, ok := SecurityProfiles[key]; ok {
			result = append(result, p)
		}
	}
	return result
}

// GetDomainSecurityProfile reads the current PHP-FPM pool config for a domain
// and returns the closest matching predefined profile, or nil if the pool is not found.
func (s *Service) GetDomainSecurityProfile(version, domain string) (*SecurityProfile, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	if err := validatePoolName(domain); err != nil {
		return nil, err
	}

	settings, err := s.readPoolSecuritySettings(version, domain)
	if err != nil {
		return nil, err
	}

	matched := matchProfile(settings)
	return matched, nil
}

// ApplySecurityProfile applies a named predefined security profile to a PHP-FPM
// pool config file for the given domain. The {DOCROOT} placeholder in OpenBasedir
// is replaced with the existing open_basedir first path component when available.
func (s *Service) ApplySecurityProfile(version, domain, profileName string) error {
	if err := validateVersion(version); err != nil {
		return err
	}
	if err := validatePoolName(domain); err != nil {
		return err
	}

	profile, ok := SecurityProfiles[profileName]
	if !ok {
		return fmt.Errorf("unknown security profile: %s", profileName)
	}

	pool, err := s.resolvePoolByName(version, domain)
	if err != nil {
		return err
	}
	confPath := pool.ConfigFile

	// Determine docroot from existing open_basedir or derive from domain name
	docroot, err := s.resolveDocroot(confPath, domain)
	if err != nil {
		return err
	}

	// Resolve {DOCROOT} placeholder
	openBasedir := strings.ReplaceAll(profile.OpenBasedir, "{DOCROOT}", docroot)

	return patchPoolSecuritySettings(confPath, profile, openBasedir)
}

// GetSecurityScore calculates a 0-100 security score for the given domain's PHP pool.
func (s *Service) GetSecurityScore(version, domain string) (*SecurityScore, error) {
	if err := validateVersion(version); err != nil {
		return nil, err
	}
	if err := validatePoolName(domain); err != nil {
		return nil, err
	}

	settings, err := s.readPoolSecuritySettings(version, domain)
	if err != nil {
		return nil, err
	}

	recommended := ""
	if s.vhostsRoot != "" {
		recommended = filepath.Join(s.vhostsRoot, domain, "httpdocs") + ":/tmp:/usr/share/php"
	}
	return computeScore(settings, recommended), nil
}

// ---------- internal helpers ----------

// poolSecuritySettings holds raw security-relevant values parsed from a pool config.
type poolSecuritySettings struct {
	DisableFunctions      string
	OpenBasedir           string
	AllowURLFopen         string
	AllowURLInclude       string
	ExposePhp             string
	DisplayErrors         string
	LogErrors             string
	SessionCookieSecure   string
	SessionCookieHttpOnly string
	SessionCookieSameSite string
}

func (svc *Service) readPoolSecuritySettings(version, domain string) (poolSecuritySettings, error) {
	pool, err := svc.resolvePoolByName(version, domain)
	if err != nil {
		return poolSecuritySettings{}, err
	}
	confPath := pool.ConfigFile
	f, err := os.Open(confPath)
	if err != nil {
		if os.IsNotExist(err) {
			return poolSecuritySettings{}, fmt.Errorf("pool %s not found for php%s", domain, version)
		}
		return poolSecuritySettings{}, fmt.Errorf("opening pool config: %w", err)
	}
	defer func() { _ = f.Close() }()

	var s poolSecuritySettings
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
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

		// PHP-FPM pool directives use php_admin_value[key] or php_value[key] syntax.
		// We normalise to the bare key name for matching.
		normalized := normalizePHPKey(key)

		switch normalized {
		case "disable_functions":
			s.DisableFunctions = val
		case "open_basedir":
			s.OpenBasedir = val
		case "allow_url_fopen":
			s.AllowURLFopen = val
		case "allow_url_include":
			s.AllowURLInclude = val
		case "expose_php":
			s.ExposePhp = val
		case "display_errors":
			s.DisplayErrors = val
		case "log_errors":
			s.LogErrors = val
		case "session.cookie_secure":
			s.SessionCookieSecure = val
		case "session.cookie_httponly":
			s.SessionCookieHttpOnly = val
		case "session.cookie_samesite":
			s.SessionCookieSameSite = val
		}
	}
	if err := scanner.Err(); err != nil {
		return poolSecuritySettings{}, fmt.Errorf("reading pool config: %w", err)
	}
	return s, nil
}

// normalizePHPKey strips the php_admin_value[...] / php_value[...] wrapper
// and returns the bare PHP ini key, e.g. "disable_functions".
func normalizePHPKey(key string) string {
	for _, prefix := range []string{"php_admin_value[", "php_admin_flag[", "php_value[", "php_flag["} {
		if strings.HasPrefix(key, prefix) && strings.HasSuffix(key, "]") {
			return key[len(prefix) : len(key)-1]
		}
	}
	return key
}

// phpBoolVal normalizes common PHP boolean representations to true/false.
func phpBoolVal(v string) bool {
	lower := strings.ToLower(strings.TrimSpace(v))
	return lower == "1" || lower == "on" || lower == "true" || lower == "yes"
}

// computeScore deducts points for each security issue and returns a SecurityScore.
func computeScore(s poolSecuritySettings, recommendedOpenBasedir string) *SecurityScore {
	score := 100
	var issues []SecurityIssue

	if strings.TrimSpace(s.DisableFunctions) == "" {
		score -= 30
		issues = append(issues, SecurityIssue{
			Severity:    "critical",
			Setting:     "disable_functions",
			Current:     "(empty)",
			Recommended: "exec,system,passthru,shell_exec,proc_open,...",
			Description: "No functions are disabled; remote code execution risk is high.",
		})
	}

	if phpBoolVal(s.DisplayErrors) {
		score -= 20
		issues = append(issues, SecurityIssue{
			Severity:    "critical",
			Setting:     "display_errors",
			Current:     s.DisplayErrors,
			Recommended: "Off",
			Description: "Error details are shown to end users, leaking internal paths and logic.",
		})
	}

	if phpBoolVal(s.ExposePhp) {
		score -= 10
		issues = append(issues, SecurityIssue{
			Severity:    "warning",
			Setting:     "expose_php",
			Current:     s.ExposePhp,
			Recommended: "Off",
			Description: "PHP version is disclosed in the X-Powered-By header.",
		})
	}

	if phpBoolVal(s.AllowURLInclude) {
		score -= 25
		issues = append(issues, SecurityIssue{
			Severity:    "critical",
			Setting:     "allow_url_include",
			Current:     s.AllowURLInclude,
			Recommended: "Off",
			Description: "Remote file inclusion is enabled; allows including arbitrary remote PHP code.",
		})
	}

	if strings.TrimSpace(s.OpenBasedir) == "" {
		if recommendedOpenBasedir == "" {
			recommendedOpenBasedir = "{VHOSTS_ROOT}/domain/httpdocs:/tmp:/usr/share/php"
		}
		score -= 15
		issues = append(issues, SecurityIssue{
			Severity:    "warning",
			Setting:     "open_basedir",
			Current:     "(none)",
			Recommended: recommendedOpenBasedir,
			Description: "No open_basedir restriction; PHP can access any file on the filesystem.",
		})
	}

	if !phpBoolVal(s.SessionCookieSecure) {
		score -= 10
		issues = append(issues, SecurityIssue{
			Severity:    "warning",
			Setting:     "session.cookie_secure",
			Current:     coalesceEmpty(s.SessionCookieSecure, "Off"),
			Recommended: "On",
			Description: "Session cookies are sent over plain HTTP; susceptible to interception.",
		})
	}

	if !phpBoolVal(s.SessionCookieHttpOnly) {
		score -= 10
		issues = append(issues, SecurityIssue{
			Severity:    "warning",
			Setting:     "session.cookie_httponly",
			Current:     coalesceEmpty(s.SessionCookieHttpOnly, "Off"),
			Recommended: "On",
			Description: "Session cookies are accessible via JavaScript; XSS can steal sessions.",
		})
	}

	if score < 0 {
		score = 0
	}

	level := classifyLevel(score)
	return &SecurityScore{
		Score:  score,
		Level:  level,
		Issues: issues,
	}
}

func classifyLevel(score int) string {
	switch {
	case score >= 90:
		return "strict"
	case score >= 60:
		return "moderate"
	case score >= 30:
		return "permissive"
	default:
		return "custom"
	}
}

func coalesceEmpty(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

// matchProfile returns the predefined profile that best matches the current settings.
// It checks in order: strict → moderate → permissive.
func matchProfile(s poolSecuritySettings) *SecurityProfile {
	score := computeScore(s, "")
	p, ok := SecurityProfiles[score.Level]
	if !ok {
		// Default to moderate for "custom"
		mod := SecurityProfiles["moderate"]
		return &mod
	}
	result := p
	return &result
}

// resolveDocroot returns the document root for a domain's pool config.
// It first tries to read the existing open_basedir first component,
// then falls back to the standard vhost path.
func (s *Service) resolveDocroot(confPath, domain string) (string, error) {
	f, err := os.Open(confPath)
	if err != nil {
		root, rootErr := s.requireVhostsRoot()
		if rootErr != nil {
			return "", rootErr
		}
		return filepath.Join(root, domain, "httpdocs"), nil
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		normalized := normalizePHPKey(extractKey(line))
		if normalized == "open_basedir" {
			_, val, found := strings.Cut(line, "=")
			if found {
				val = strings.TrimSpace(val)
				parts := strings.SplitN(val, ":", 2)
				if parts[0] != "" {
					return parts[0], nil
				}
			}
		}
	}
	root, err := s.requireVhostsRoot()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, domain, "httpdocs"), nil
}

func extractKey(line string) string {
	key, _, found := strings.Cut(line, "=")
	if !found {
		return ""
	}
	return strings.TrimSpace(key)
}

// patchPoolSecuritySettings rewrites the security-relevant directives in a pool
// config file, preserving all other lines.
func patchPoolSecuritySettings(confPath string, profile SecurityProfile, openBasedir string) error {
	// Read current content
	data, err := os.ReadFile(confPath)
	if err != nil {
		return fmt.Errorf("reading pool config: %w", err)
	}

	lines := strings.Split(string(data), "\n")
	var output []string

	// Track which security keys we have already emitted so we can append missing ones.
	emitted := map[string]bool{}

	// Keys we manage — keyed by their bare PHP ini name.
	securityKeys := map[string]bool{
		"disable_functions":       true,
		"open_basedir":            true,
		"allow_url_fopen":         true,
		"allow_url_include":       true,
		"expose_php":              true,
		"display_errors":          true,
		"log_errors":              true,
		"session.cookie_secure":   true,
		"session.cookie_httponly": true,
		"session.cookie_samesite": true,
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "#") {
			output = append(output, line)
			continue
		}

		key, _, found := strings.Cut(trimmed, "=")
		if !found {
			output = append(output, line)
			continue
		}

		bare := normalizePHPKey(strings.TrimSpace(key))
		if !securityKeys[bare] {
			output = append(output, line)
			continue
		}

		// Replace this line with the profile value
		newLine := buildSecurityLine(bare, profile, openBasedir)
		if newLine != "" {
			output = append(output, newLine)
		}
		// Mark as emitted (even if newLine was empty, we consumed this directive)
		emitted[bare] = true
	}

	// Append any directives not already present in the file
	missing := buildMissingSecurityLines(profile, openBasedir, emitted)
	if len(missing) > 0 {
		output = append(output, "")
		output = append(output, "; Security profile: "+profile.Name)
		output = append(output, missing...)
	}

	result := strings.Join(output, "\n")
	return os.WriteFile(confPath, []byte(result), 0644)
}

// buildSecurityLine returns the php_admin_value / php_admin_flag line for a given bare key.
func buildSecurityLine(bare string, profile SecurityProfile, openBasedir string) string {
	switch bare {
	case "disable_functions":
		return fmt.Sprintf("php_admin_value[disable_functions] = %s", strings.Join(profile.DisableFunctions, ","))
	case "open_basedir":
		if openBasedir == "" {
			return "" // no restriction — remove the directive entirely
		}
		return fmt.Sprintf("php_admin_value[open_basedir] = %s", openBasedir)
	case "allow_url_fopen":
		return fmt.Sprintf("php_admin_flag[allow_url_fopen] = %s", boolToOnOff(profile.AllowURLFopen))
	case "allow_url_include":
		return fmt.Sprintf("php_admin_flag[allow_url_include] = %s", boolToOnOff(profile.AllowURLInclude))
	case "expose_php":
		return fmt.Sprintf("php_admin_flag[expose_php] = %s", boolToOnOff(profile.ExposePhp))
	case "display_errors":
		return fmt.Sprintf("php_admin_flag[display_errors] = %s", boolToOnOff(profile.DisplayErrors))
	case "log_errors":
		return fmt.Sprintf("php_admin_flag[log_errors] = %s", boolToOnOff(profile.LogErrors))
	case "session.cookie_secure":
		return fmt.Sprintf("php_admin_flag[session.cookie_secure] = %s", boolToOnOff(profile.SessionCookieSecure))
	case "session.cookie_httponly":
		return fmt.Sprintf("php_admin_flag[session.cookie_httponly] = %s", boolToOnOff(profile.SessionCookieHttpOnly))
	case "session.cookie_samesite":
		return fmt.Sprintf("php_admin_value[session.cookie_samesite] = %s", profile.SessionCookieSameSite)
	}
	return ""
}

// buildMissingSecurityLines returns lines for any security directives not yet emitted.
func buildMissingSecurityLines(profile SecurityProfile, openBasedir string, emitted map[string]bool) []string {
	type entry struct {
		key string
	}
	order := []entry{
		{"disable_functions"},
		{"open_basedir"},
		{"allow_url_fopen"},
		{"allow_url_include"},
		{"expose_php"},
		{"display_errors"},
		{"log_errors"},
		{"session.cookie_secure"},
		{"session.cookie_httponly"},
		{"session.cookie_samesite"},
	}

	var lines []string
	for _, e := range order {
		if emitted[e.key] {
			continue
		}
		line := buildSecurityLine(e.key, profile, openBasedir)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

func boolToOnOff(b bool) string {
	if b {
		return "On"
	}
	return "Off"
}
