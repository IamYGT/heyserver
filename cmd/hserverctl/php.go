package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

var (
	cliPHPVersionPattern = regexp.MustCompile(`^[0-9]{1,2}\.[0-9]{1,2}$`)
	cliPHPPoolPattern    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

const (
	maxPHPErrorLogLines = 5000
	maxPHPSlowLogLines  = 5000
	maxPHPProjectBytes  = 4096
)

type localPHPVersion struct {
	Version   string `json:"version"`
	Active    bool   `json:"active"`
	Info      string `json:"info"`
	PoolDir   string `json:"pool_dir"`
	PoolCount int    `json:"pool_count"`
}

type cliPHPPool struct {
	Name         string         `json:"name"`
	Version      string         `json:"version"`
	ConfigFile   string         `json:"config_file"`
	User         string         `json:"user"`
	Group        string         `json:"group"`
	Listen       string         `json:"listen"`
	PM           string         `json:"pm"`
	PMSettings   map[string]any `json:"pm_settings,omitempty"`
	OpenBasedir  string         `json:"open_basedir,omitempty"`
	SocketExists bool           `json:"socket_exists"`
}

type remotePHPVersion struct {
	Version string          `json:"version"`
	Unit    string          `json:"unit"`
	Active  string          `json:"active"`
	Enabled string          `json:"enabled"`
	Masked  bool            `json:"masked"`
	Binary  string          `json:"binary,omitempty"`
	Pools   []remotePHPPool `json:"pools"`
}

type remotePHPPool struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	User        string `json:"user,omitempty"`
	Group       string `json:"group,omitempty"`
	Listen      string `json:"listen,omitempty"`
	PM          string `json:"pm,omitempty"`
	MaxChildren int    `json:"max_children,omitempty"`
}

type normalizedPHPPool struct {
	Version     string `json:"version"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	User        string `json:"user,omitempty"`
	Group       string `json:"group,omitempty"`
	Listen      string `json:"listen,omitempty"`
	PM          string `json:"pm,omitempty"`
	MaxChildren int    `json:"max_children,omitempty"`
}

// The PHP diagnostics endpoints expose host and provider-derived values. Keep
// their CLI projections deliberately narrow so a future API field (or a raw
// provider response) cannot become an accidental output sink.
type cliPHPExtension struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Version string `json:"version"`
	Type    string `json:"type"`
	INIFile string `json:"ini_file"`
}

type cliPHPINIDiff struct {
	Key     string `json:"key"`
	Current string `json:"current"`
	Default string `json:"default"`
	Source  string `json:"source"`
}

type cliPHPINIDirective struct {
	Key          string `json:"key"`
	Value        string `json:"value"`
	DefaultValue string `json:"default_value"`
	Type         string `json:"type"`
	Section      string `json:"section"`
	Description  string `json:"description"`
	Changeable   string `json:"changeable"`
}

type cliPHPSlowLogEntry struct {
	Timestamp string   `json:"timestamp"`
	Script    string   `json:"script"`
	Duration  float64  `json:"duration"`
	Backtrace []string `json:"backtrace"`
}

type cliPHPSecurityProfile struct {
	Name                  string   `json:"name"`
	Description           string   `json:"description"`
	Level                 string   `json:"level"`
	DisableFunctions      []string `json:"disable_functions"`
	OpenBasedir           string   `json:"open_basedir"`
	AllowURLFopen         bool     `json:"allow_url_fopen"`
	AllowURLInclude       bool     `json:"allow_url_include"`
	ExposePhp             bool     `json:"expose_php"`
	DisplayErrors         bool     `json:"display_errors"`
	LogErrors             bool     `json:"log_errors"`
	SessionCookieSecure   bool     `json:"session_cookie_secure"`
	SessionCookieHttpOnly bool     `json:"session_cookie_httponly"`
	SessionCookieSameSite string   `json:"session_cookie_samesite"`
}

type cliPHPSecurityIssue struct {
	Severity    string `json:"severity"`
	Setting     string `json:"setting"`
	Current     string `json:"current"`
	Recommended string `json:"recommended"`
	Description string `json:"description"`
}

type cliPHPSecurityScore struct {
	Score  int                   `json:"score"`
	Level  string                `json:"level"`
	Issues []cliPHPSecurityIssue `json:"issues"`
}

type cliPHPSecurityStatus struct {
	Score   cliPHPSecurityScore    `json:"score"`
	Profile *cliPHPSecurityProfile `json:"profile"`
}

type cliPHPComposerVersion struct {
	Version string `json:"version"`
}

type cliPHPComposerPackage struct {
	Name           string `json:"name"`
	CurrentVersion string `json:"current_version"`
	LatestVersion  string `json:"latest_version"`
	Description    string `json:"description"`
	Abandoned      bool   `json:"abandoned"`
}

func runPHP(ctx context.Context, client *apiClient, args []string, out, errOut io.Writer, getenv func(string) string) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl php versions|pools|pool|config|action|status|opcache|extensions|ini|logs|security|composer")
	}
	switch args[0] {
	case "versions":
		return runPHPVersions(ctx, client, args[1:], out)
	case "pools":
		return runPHPPools(ctx, client, args[1:], out)
	case "pool":
		return runPHPPool(ctx, client, args[1:], out)
	case "config":
		return runPHPConfig(ctx, client, args[1:], out, errOut, getenv)
	case "action":
		return runPHPAction(ctx, client, args[1:], out)
	case "status":
		return runPHPStatus(ctx, client, args[1:], out)
	case "opcache":
		return runPHPOPcache(ctx, client, args[1:], out)
	case "extensions":
		return runPHPExtensions(ctx, client, args[1:], out)
	case "ini":
		return runPHPINI(ctx, client, args[1:], out)
	case "logs":
		return runPHPLogs(ctx, client, args[1:], out)
	case "security":
		return runPHPSecurity(ctx, client, args[1:], out)
	case "composer":
		return runPHPComposer(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown php command %q", args[0])
	}
}

// runPHPExtensions reads the installed and available extension inventory for a
// local PHP-FPM version. There is intentionally no --node form: the API route
// is panel-local and the managed-agent contract does not advertise it.
func runPHPExtensions(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: hserverctl php extensions VERSION")
	}
	version, err := validateCLIPHPVersion(args[0])
	if err != nil {
		return err
	}
	entries, err := requestJSON[[]cliPHPExtension](ctx, client, http.MethodGet,
		"/api/php/extensions/"+url.PathEscape(version), nil, true)
	if err != nil {
		return err
	}
	if entries == nil {
		entries = []cliPHPExtension{}
	}
	for index := range entries {
		entries[index] = sanitizePHPExtension(entries[index])
	}
	return printJSONValue(out, entries)
}

func runPHPINI(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl php ini get|diff|directives VERSION [POOL]")
	}
	switch args[0] {
	case "get":
		return runPHPINIGet(ctx, client, args[1:], out)
	case "diff":
		return runPHPINIDiff(ctx, client, args[1:], out)
	case "directives":
		return runPHPINIDirectives(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown php ini command %q", args[0])
	}
}

func runPHPINIGet(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: hserverctl php ini get VERSION [POOL]")
	}
	version, err := validateCLIPHPVersion(args[0])
	if err != nil {
		return err
	}
	endpoint := "/api/php/ini/" + url.PathEscape(version)
	if len(args) == 1 {
		settings, err := requestJSON[map[string]string](ctx, client, http.MethodGet, endpoint, nil, true)
		if err != nil {
			return err
		}
		return printJSONValue(out, sanitizePHPINIMap(settings))
	}
	pool, err := validateCLIPHPPool(args[1])
	if err != nil {
		return err
	}
	settings, err := requestJSON[map[string]string](ctx, client, http.MethodGet,
		endpoint+"/"+url.PathEscape(pool), nil, true)
	if err != nil {
		return err
	}
	return printJSONValue(out, sanitizePHPINIMap(settings))
}

func runPHPINIDiff(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: hserverctl php ini diff VERSION")
	}
	version, err := validateCLIPHPVersion(args[0])
	if err != nil {
		return err
	}
	entries, err := requestJSON[[]cliPHPINIDiff](ctx, client, http.MethodGet,
		"/api/php/ini/"+url.PathEscape(version)+"/diff", nil, true)
	if err != nil {
		return err
	}
	if entries == nil {
		entries = []cliPHPINIDiff{}
	}
	for index := range entries {
		entries[index] = sanitizePHPINIDiff(entries[index])
	}
	return printJSONValue(out, entries)
}

func runPHPINIDirectives(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) != 1 {
		return errors.New("usage: hserverctl php ini directives VERSION")
	}
	version, err := validateCLIPHPVersion(args[0])
	if err != nil {
		return err
	}
	entries, err := requestJSON[[]cliPHPINIDirective](ctx, client, http.MethodGet,
		"/api/php/ini/"+url.PathEscape(version)+"/directives", nil, true)
	if err != nil {
		return err
	}
	if entries == nil {
		entries = []cliPHPINIDirective{}
	}
	for index := range entries {
		entries[index] = sanitizePHPINIDirective(entries[index])
	}
	return printJSONValue(out, entries)
}

func runPHPLogs(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl php logs error|slow")
	}
	switch args[0] {
	case "error":
		return runPHPErrorLog(ctx, client, args[1:], out)
	case "slow":
		return runPHPSlowLog(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown php logs command %q", args[0])
	}
}

func runPHPErrorLog(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("php logs error", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	lines := flags.Int("lines", 100, "number of latest PHP-FPM error-log lines")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl php logs error [--lines N] VERSION")
	}
	if *lines < 1 || *lines > maxPHPErrorLogLines {
		return fmt.Errorf("PHP error log line count must be between 1 and %d", maxPHPErrorLogLines)
	}
	version, err := validateCLIPHPVersion(flags.Args()[0])
	if err != nil {
		return err
	}
	endpoint := "/api/php/logs/" + url.PathEscape(version) + "/error?lines=" + fmt.Sprint(*lines)
	entries, err := requestJSON[[]string](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
	if err != nil {
		return err
	}
	if entries == nil {
		entries = []string{}
	}
	for index := range entries {
		entries[index] = sanitizePHPField(entries[index])
	}
	return printJSONValue(out, entries)
}

func runPHPSlowLog(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("php logs slow", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	lines := flags.Int("lines", 50, "number of latest PHP-FPM slow-log entries")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 2 {
		return errors.New("usage: hserverctl php logs slow [--lines N] VERSION POOL")
	}
	if *lines < 1 || *lines > maxPHPSlowLogLines {
		return fmt.Errorf("PHP slow log line count must be between 1 and %d", maxPHPSlowLogLines)
	}
	version, err := validateCLIPHPVersion(flags.Args()[0])
	if err != nil {
		return err
	}
	pool, err := validateCLIPHPPool(flags.Args()[1])
	if err != nil {
		return err
	}
	endpoint := "/api/php/logs/" + url.PathEscape(version) + "/" + url.PathEscape(pool) + "/slow?lines=" + fmt.Sprint(*lines)
	entries, err := requestJSON[[]cliPHPSlowLogEntry](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
	if err != nil {
		return err
	}
	if entries == nil {
		entries = []cliPHPSlowLogEntry{}
	}
	for index := range entries {
		entries[index] = sanitizePHPSlowLogEntry(entries[index])
	}
	return printJSONValue(out, entries)
}

func runPHPSecurity(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl php security profiles|status")
	}
	switch args[0] {
	case "profiles":
		if len(args) != 1 {
			return errors.New("usage: hserverctl php security profiles")
		}
		profiles, err := requestJSON[[]cliPHPSecurityProfile](ctx, client, http.MethodGet, "/api/php/security/profiles", nil, true)
		if err != nil {
			return err
		}
		if profiles == nil {
			profiles = []cliPHPSecurityProfile{}
		}
		for index := range profiles {
			profiles[index] = sanitizePHPSecurityProfile(profiles[index])
		}
		return printJSONValue(out, profiles)
	case "status":
		if len(args) != 3 {
			return errors.New("usage: hserverctl php security status VERSION POOL")
		}
		version, err := validateCLIPHPVersion(args[1])
		if err != nil {
			return err
		}
		pool, err := validateCLIPHPPool(args[2])
		if err != nil {
			return err
		}
		status, err := requestJSON[cliPHPSecurityStatus](ctx, client, http.MethodGet,
			"/api/php/security/"+url.PathEscape(version)+"/"+url.PathEscape(pool), nil, true)
		if err != nil {
			return err
		}
		return printJSONValue(out, sanitizePHPSecurityStatus(status))
	default:
		return fmt.Errorf("unknown php security command %q", args[0])
	}
}

func runPHPComposer(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl php composer version|outdated")
	}
	switch args[0] {
	case "version":
		if len(args) != 1 {
			return errors.New("usage: hserverctl php composer version")
		}
		version, err := requestJSON[cliPHPComposerVersion](ctx, client, http.MethodGet, "/api/php/composer/version", nil, true)
		if err != nil {
			return err
		}
		version.Version = sanitizePHPField(version.Version)
		return printJSONValue(out, version)
	case "outdated":
		return runPHPComposerOutdated(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown php composer command %q", args[0])
	}
}

func runPHPComposerOutdated(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("php composer outdated", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	projectDir := flags.String("project-dir", "", "absolute Composer project directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 || strings.TrimSpace(*projectDir) == "" {
		return errors.New("usage: hserverctl php composer outdated --project-dir PATH VERSION")
	}
	version, err := validateCLIPHPVersion(flags.Args()[0])
	if err != nil {
		return err
	}
	project, err := validateCLIPHPProjectDir(*projectDir)
	if err != nil {
		return err
	}
	packages, err := requestJSON[[]cliPHPComposerPackage](ctx, client.withTimeout(6*time.Minute), http.MethodPost,
		"/api/php/composer/"+url.PathEscape(version)+"/outdated", map[string]string{"project_dir": project}, true)
	if err != nil {
		return err
	}
	if packages == nil {
		packages = []cliPHPComposerPackage{}
	}
	for index := range packages {
		packages[index] = sanitizePHPComposerPackage(packages[index])
	}
	return printJSONValue(out, packages)
}

func sanitizePHPExtension(value cliPHPExtension) cliPHPExtension {
	value.Name = sanitizePHPField(value.Name)
	value.Version = sanitizePHPField(value.Version)
	value.Type = sanitizePHPField(value.Type)
	value.INIFile = sanitizePHPField(value.INIFile)
	return value
}

func sanitizePHPINIDiff(value cliPHPINIDiff) cliPHPINIDiff {
	value.Key = sanitizePHPField(value.Key)
	value.Current = sanitizePHPField(value.Current)
	value.Default = sanitizePHPField(value.Default)
	value.Source = sanitizePHPField(value.Source)
	return value
}

func sanitizePHPINIDirective(value cliPHPINIDirective) cliPHPINIDirective {
	value.Key = sanitizePHPField(value.Key)
	value.Value = sanitizePHPField(value.Value)
	value.DefaultValue = sanitizePHPField(value.DefaultValue)
	value.Type = sanitizePHPField(value.Type)
	value.Section = sanitizePHPField(value.Section)
	value.Description = sanitizePHPField(value.Description)
	value.Changeable = sanitizePHPField(value.Changeable)
	return value
}

func sanitizePHPSlowLogEntry(value cliPHPSlowLogEntry) cliPHPSlowLogEntry {
	value.Timestamp = sanitizePHPField(value.Timestamp)
	value.Script = sanitizePHPField(value.Script)
	value.Backtrace = sanitizePHPStringSlice(value.Backtrace)
	return value
}

func sanitizePHPSecurityProfile(value cliPHPSecurityProfile) cliPHPSecurityProfile {
	value.Name = sanitizePHPField(value.Name)
	value.Description = sanitizePHPField(value.Description)
	value.Level = sanitizePHPField(value.Level)
	value.DisableFunctions = sanitizePHPStringSlice(value.DisableFunctions)
	value.OpenBasedir = sanitizePHPField(value.OpenBasedir)
	value.SessionCookieSameSite = sanitizePHPField(value.SessionCookieSameSite)
	return value
}

func sanitizePHPSecurityStatus(value cliPHPSecurityStatus) cliPHPSecurityStatus {
	value.Score.Level = sanitizePHPField(value.Score.Level)
	for index := range value.Score.Issues {
		issue := &value.Score.Issues[index]
		issue.Severity = sanitizePHPField(issue.Severity)
		issue.Setting = sanitizePHPField(issue.Setting)
		issue.Current = sanitizePHPField(issue.Current)
		issue.Recommended = sanitizePHPField(issue.Recommended)
		issue.Description = sanitizePHPField(issue.Description)
	}
	if value.Score.Issues == nil {
		value.Score.Issues = []cliPHPSecurityIssue{}
	}
	if value.Profile != nil {
		profile := sanitizePHPSecurityProfile(*value.Profile)
		value.Profile = &profile
	}
	return value
}

func sanitizePHPComposerPackage(value cliPHPComposerPackage) cliPHPComposerPackage {
	value.Name = sanitizePHPField(value.Name)
	value.CurrentVersion = sanitizePHPField(value.CurrentVersion)
	value.LatestVersion = sanitizePHPField(value.LatestVersion)
	value.Description = sanitizePHPField(value.Description)
	return value
}

func sanitizePHPINIMap(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	sanitized := make(map[string]string, len(values))
	for key, value := range values {
		sanitized[sanitizePHPField(key)] = sanitizePHPField(value)
	}
	return sanitized
}

func sanitizePHPStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	sanitized := make([]string, len(values))
	for index, value := range values {
		sanitized[index] = sanitizePHPField(value)
	}
	return sanitized
}

// sanitizePHPField keeps provider/config/log metadata on one safe JSON line.
// Newlines, tabs, and carriage returns cannot be allowed to change terminal
// layout; other Unicode controls are dropped before the value is rendered.
func sanitizePHPField(value string) string {
	return strings.Map(func(character rune) rune {
		switch character {
		case '\n', '\r', '\t':
			return ' '
		default:
			if unicode.IsControl(character) {
				return -1
			}
			return character
		}
	}, value)
}

// sanitizePHPConfigContent retains the useful line structure of a read-only
// configuration view while dropping terminal controls that are not formatting.
func sanitizePHPConfigContent(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.Map(func(character rune) rune {
		if character == '\n' || character == '\t' {
			return character
		}
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, value)
}

func sanitizePHPFileContent(value cliFileContent) cliFileContent {
	value.Path = sanitizePHPField(value.Path)
	value.Content = sanitizePHPConfigContent(value.Content)
	value.Checksum = sanitizePHPField(value.Checksum)
	value.Mode = sanitizePHPField(value.Mode)
	value.ModifiedAt = sanitizePHPField(value.ModifiedAt)
	return value
}

func printPHPConfigContent(ctx context.Context, client *apiClient, out io.Writer, endpoint string) error {
	value, err := requestJSON[cliFileContent](ctx, client, http.MethodGet, endpoint, nil, true)
	if err != nil {
		return err
	}
	return printJSONValue(out, sanitizePHPFileContent(value))
}

func validateCLIPHPProjectDir(value string) (string, error) {
	if strings.TrimSpace(value) == "" {
		return "", errors.New("Composer project directory is required")
	}
	if len([]byte(value)) > maxPHPProjectBytes || !utf8.ValidString(value) || strings.IndexByte(value, 0) >= 0 {
		return "", fmt.Errorf("Composer project directory must be valid UTF-8 text of at most %d bytes", maxPHPProjectBytes)
	}
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", errors.New("Composer project directory must not contain control characters")
	}
	if !filepath.IsAbs(value) || filepath.Clean(value) != value {
		return "", errors.New("Composer project directory must be a clean absolute path")
	}
	return value, nil
}

func runPHPVersions(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("php versions", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl php versions [--node NODE]")
	}
	selectedNode := strings.TrimSpace(*node)
	if selectedNode == "" {
		return printRequest(ctx, client, out, http.MethodGet, "/api/php/versions", nil, true)
	}
	versions, err := loadRemotePHPVersions(ctx, client, selectedNode, agenthub.CapabilityPHPRead)
	if err != nil {
		return err
	}
	return printJSONValue(out, versions)
}

func runPHPPools(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("php pools", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	version := flags.String("version", "", "optional PHP version filter")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl php pools [--node NODE] [--version VERSION]")
	}
	selectedVersion := strings.TrimSpace(*version)
	if selectedVersion != "" {
		var err error
		selectedVersion, err = validateCLIPHPVersion(selectedVersion)
		if err != nil {
			return err
		}
	}
	selectedNode := strings.TrimSpace(*node)
	if selectedNode == "" {
		endpoint := "/api/php/pools"
		if selectedVersion != "" {
			endpoint += "?version=" + url.QueryEscape(selectedVersion)
		}
		return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
	}
	versions, err := loadRemotePHPVersions(ctx, client, selectedNode, agenthub.CapabilityPHPRead)
	if err != nil {
		return err
	}
	pools := make([]normalizedPHPPool, 0)
	matchedVersion := selectedVersion == ""
	for _, item := range versions {
		if selectedVersion != "" && item.Version != selectedVersion {
			continue
		}
		matchedVersion = true
		for _, pool := range item.Pools {
			pools = append(pools, normalizedPHPPool{
				Version: item.Version, Name: pool.Name, Path: pool.Path, User: pool.User, Group: pool.Group,
				Listen: pool.Listen, PM: pool.PM, MaxChildren: pool.MaxChildren,
			})
		}
	}
	if !matchedVersion {
		return errors.New("PHP version is not present in the current managed-node inventory")
	}
	return printJSONValue(out, pools)
}

func runPHPPool(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) != 3 || args[0] != "get" {
		return errors.New("usage: hserverctl php pool get VERSION POOL")
	}
	version, err := validateCLIPHPVersion(args[1])
	if err != nil {
		return err
	}
	pool, err := validateCLIPHPPool(args[2])
	if err != nil {
		return err
	}
	if _, err := requireLocalPHPPool(ctx, client, version, pool); err != nil {
		return err
	}
	endpoint := "/api/php/pools/" + url.PathEscape(version) + "/" + url.PathEscape(pool)
	return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
}

func runPHPConfig(ctx context.Context, client *apiClient, args []string, out, errOut io.Writer, getenv func(string) string) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl php config get|edit|save")
	}
	switch args[0] {
	case "get":
		flags := flag.NewFlagSet("php config get", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		node := flags.String("node", "", "managed node ID; omit for the local host")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 2 {
			return errors.New("usage: hserverctl php config get [--node NODE] VERSION POOL")
		}
		version, pool, err := validateCLIPHPIdentity(flags.Args()[0], flags.Args()[1])
		if err != nil {
			return err
		}
		selectedNode := strings.TrimSpace(*node)
		if selectedNode == "" {
			if _, err := requireLocalPHPPool(ctx, client, version, pool); err != nil {
				return err
			}
			return printPHPConfigContent(ctx, client.withTimeout(45*time.Second), out, localPHPConfigEndpoint(version, pool))
		}
		if _, _, err := requireRemotePHPPool(ctx, client, selectedNode, version, pool, agenthub.CapabilityPHPRead); err != nil {
			return err
		}
		endpoint := remotePHPConfigEndpoint(selectedNode, version, pool)
		return printPHPConfigContent(ctx, client.withTimeout(45*time.Second), out, endpoint)
	case "edit":
		return runPHPConfigEdit(ctx, client, args[1:], out, errOut, getenv)
	case "save":
		return runPHPConfigSave(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown php config command %q", args[0])
	}
}

func runPHPConfigEdit(ctx context.Context, client *apiClient, args []string, out, errOut io.Writer, getenv func(string) string) error {
	flags := flag.NewFlagSet("php config edit", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	editor := flags.String("editor", "", "editor executable; defaults to HSERVER_EDITOR, VISUAL, or EDITOR")
	reload := flags.Bool("reload", false, "reload PHP-FPM after a successful configuration test")
	confirmed := flags.Bool("confirm", false, "confirm checksum-protected pool replacement after editing")
	wait := flags.Duration("wait", 2*time.Minute, "maximum save and validation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 2 {
		return errors.New("usage: hserverctl php config edit --confirm [--node NODE] [--editor EXECUTABLE] [--reload] [--wait DURATION] VERSION POOL")
	}
	if !*confirmed {
		return errors.New("PHP-FPM configuration edit requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("PHP-FPM edit wait must be greater than zero")
	}
	version, pool, err := validateCLIPHPIdentity(flags.Args()[0], flags.Args()[1])
	if err != nil {
		return err
	}
	selectedNode := strings.TrimSpace(*node)
	endpoint, current, err := loadWritablePHPConfig(ctx, client, selectedNode, version, pool)
	if err != nil {
		return err
	}
	checksum, err := validateCLIFileChecksum(current.Checksum)
	if err != nil {
		return fmt.Errorf("server returned an invalid PHP-FPM pool checksum: %w", err)
	}
	edited, changed, err := editCLIText(ctx, current.Content, *editor, "hserver-php-*.conf", maxCLIManagedFileBytes, errOut, getenv)
	if err != nil {
		return err
	}
	if !changed {
		return printJSONValue(out, map[string]any{
			"changed":  false,
			"checksum": checksum,
			"message":  "PHP-FPM pool was not changed",
			"path":     current.Path,
		})
	}
	payload := map[string]any{"content": edited, "checksum": checksum, "reload": *reload}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPut, endpoint, payload, true)
}

func runPHPConfigSave(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("php config save", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	contentFile := flags.String("content-file", "", "regular UTF-8 replacement file")
	checksum := flags.String("checksum", "", "expected remote SHA-256 checksum")
	reload := flags.Bool("reload", false, "reload PHP-FPM after a successful configuration test")
	confirmed := flags.Bool("confirm", false, "confirm checksum-protected pool replacement")
	wait := flags.Duration("wait", 2*time.Minute, "maximum save and validation wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 2 || strings.TrimSpace(*contentFile) == "" {
		return errors.New("usage: hserverctl php config save --confirm [--node NODE] --checksum SHA256 --content-file PATH [--reload] [--wait DURATION] VERSION POOL")
	}
	if !*confirmed {
		return errors.New("PHP-FPM configuration save requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("PHP-FPM save wait must be greater than zero")
	}
	version, pool, err := validateCLIPHPIdentity(flags.Args()[0], flags.Args()[1])
	if err != nil {
		return err
	}
	expected, err := validateCLIFileChecksum(*checksum)
	if err != nil {
		return err
	}
	content, err := readCLIManagedTextFile(*contentFile)
	if err != nil {
		return err
	}
	selectedNode := strings.TrimSpace(*node)
	endpoint, current, err := loadWritablePHPConfig(ctx, client, selectedNode, version, pool)
	if err != nil {
		return err
	}
	if strings.ToLower(current.Checksum) != expected {
		return errors.New("PHP-FPM pool checksum changed; read the current configuration and retry")
	}
	payload := map[string]any{"content": content, "checksum": expected, "reload": *reload}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPut, endpoint, payload, true)
}

func loadWritablePHPConfig(ctx context.Context, client *apiClient, nodeID, version, pool string) (string, cliFileContent, error) {
	if nodeID == "" {
		observedPool, err := requireLocalPHPPool(ctx, client, version, pool)
		if err != nil {
			return "", cliFileContent{}, err
		}
		endpoint := localPHPConfigEndpoint(version, pool)
		current, err := requestJSON[cliFileContent](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
		if err != nil {
			return "", cliFileContent{}, err
		}
		if current.Path != observedPool.ConfigFile {
			return "", cliFileContent{}, errors.New("local panel resolved the PHP-FPM pool to a different path; refresh inventory and retry")
		}
		return endpoint, current, nil
	}
	_, observedPool, err := requireRemotePHPPool(ctx, client, nodeID, version, pool, agenthub.CapabilityPHPWrite)
	if err != nil {
		return "", cliFileContent{}, err
	}
	endpoint := remotePHPConfigEndpoint(nodeID, version, pool)
	current, err := requestJSON[cliFileContent](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
	if err != nil {
		return "", cliFileContent{}, err
	}
	if current.Path != observedPool.Path {
		return "", cliFileContent{}, errors.New("managed agent resolved the PHP-FPM pool to a different path; refresh inventory and retry")
	}
	return endpoint, current, nil
}

func runPHPAction(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("php action", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID; omit for the local host")
	confirmed := flags.Bool("confirm", false, "confirm the PHP-FPM lifecycle action")
	wait := flags.Duration("wait", 2*time.Minute, "maximum test and lifecycle wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 2 {
		return errors.New("usage: hserverctl php action --confirm [--node NODE] [--wait DURATION] VERSION test|reload|restart")
	}
	if !*confirmed {
		return errors.New("PHP-FPM action requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("PHP-FPM action wait must be greater than zero")
	}
	version, err := validateCLIPHPVersion(flags.Args()[0])
	if err != nil {
		return err
	}
	action := strings.ToLower(strings.TrimSpace(flags.Args()[1]))
	if action != "test" && action != "reload" && action != "restart" {
		return errors.New("PHP-FPM action must be test, reload, or restart")
	}
	selectedNode := strings.TrimSpace(*node)
	if selectedNode == "" {
		if _, err := requireLocalPHPVersion(ctx, client, version); err != nil {
			return err
		}
		endpoint := "/api/php/versions/" + url.PathEscape(version) + "/actions/" + action
		return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, nil, true)
	}
	if _, versionState, err := requireRemotePHPVersion(ctx, client, selectedNode, version, agenthub.CapabilityPHPAction); err != nil {
		return err
	} else if versionState.Masked || versionState.Binary == "" {
		return errors.New("managed PHP-FPM runtime is masked or has no executable binary")
	}
	endpoint := "/api/nodes/" + url.PathEscape(selectedNode) + "/php/" + url.PathEscape(version) + "/actions/" + action
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, nil, true)
}

func runPHPStatus(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("usage: hserverctl php status VERSION [POOL]")
	}
	version, err := validateCLIPHPVersion(args[0])
	if err != nil {
		return err
	}
	if _, err := requireLocalPHPVersion(ctx, client, version); err != nil {
		return err
	}
	endpoint := "/api/php/status/" + url.PathEscape(version)
	if len(args) == 2 {
		pool, err := validateCLIPHPPool(args[1])
		if err != nil {
			return err
		}
		if _, err := requireLocalPHPPool(ctx, client, version, pool); err != nil {
			return err
		}
		endpoint += "/" + url.PathEscape(pool)
	}
	return printRequest(ctx, client, out, http.MethodGet, endpoint, nil, true)
}

func runPHPOPcache(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 2 && args[0] == "get" {
		version, err := validateCLIPHPVersion(args[1])
		if err != nil {
			return err
		}
		if _, err := requireLocalPHPVersion(ctx, client, version); err != nil {
			return err
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/php/opcache/"+url.PathEscape(version), nil, true)
	}
	if len(args) >= 1 && args[0] == "reset" {
		flags := flag.NewFlagSet("php opcache reset", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		confirmed := flags.Bool("confirm", false, "confirm OPcache reset")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 1 {
			return errors.New("usage: hserverctl php opcache reset --confirm VERSION")
		}
		if !*confirmed {
			return errors.New("PHP OPcache reset requires explicit --confirm")
		}
		version, err := validateCLIPHPVersion(flags.Args()[0])
		if err != nil {
			return err
		}
		if _, err := requireLocalPHPVersion(ctx, client, version); err != nil {
			return err
		}
		return printRequest(ctx, client, out, http.MethodPost, "/api/php/opcache/"+url.PathEscape(version)+"/reset", nil, true)
	}
	return errors.New("usage: hserverctl php opcache get VERSION | php opcache reset --confirm VERSION")
}

func loadLocalPHPVersions(ctx context.Context, client *apiClient) ([]localPHPVersion, error) {
	return requestJSON[[]localPHPVersion](ctx, client, http.MethodGet, "/api/php/versions", nil, true)
}

func requireLocalPHPVersion(ctx context.Context, client *apiClient, version string) (localPHPVersion, error) {
	versions, err := loadLocalPHPVersions(ctx, client)
	if err != nil {
		return localPHPVersion{}, err
	}
	for _, item := range versions {
		if item.Version == version {
			return item, nil
		}
	}
	return localPHPVersion{}, errors.New("PHP version is not present in the current local inventory")
}

func requireLocalPHPPool(ctx context.Context, client *apiClient, version, pool string) (cliPHPPool, error) {
	if _, err := requireLocalPHPVersion(ctx, client, version); err != nil {
		return cliPHPPool{}, err
	}
	pools, err := requestJSON[[]cliPHPPool](ctx, client, http.MethodGet, "/api/php/pools?version="+url.QueryEscape(version), nil, true)
	if err != nil {
		return cliPHPPool{}, err
	}
	for _, item := range pools {
		if item.Name == pool && item.Version == version {
			return item, nil
		}
	}
	return cliPHPPool{}, errors.New("PHP-FPM pool is not present in the current local inventory")
}

func loadRemotePHPVersions(ctx context.Context, client *apiClient, nodeID, capability string) ([]remotePHPVersion, error) {
	target, err := loadCLIManagedNode(ctx, client, nodeID)
	if err != nil {
		return nil, err
	}
	if !target.Online {
		return nil, errors.New("managed node is offline")
	}
	if !managedNodeHasCapability(target, capability) {
		return nil, fmt.Errorf("managed agent does not advertise %s", capability)
	}
	endpoint := "/api/nodes/" + url.PathEscape(nodeID) + "/php"
	return requestJSON[[]remotePHPVersion](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
}

func requireRemotePHPVersion(ctx context.Context, client *apiClient, nodeID, version, capability string) (managedNodeEnvelope, remotePHPVersion, error) {
	target, err := loadCLIManagedNode(ctx, client, nodeID)
	if err != nil {
		return managedNodeEnvelope{}, remotePHPVersion{}, err
	}
	if !target.Online {
		return managedNodeEnvelope{}, remotePHPVersion{}, errors.New("managed node is offline")
	}
	if !managedNodeHasCapability(target, capability) {
		return managedNodeEnvelope{}, remotePHPVersion{}, fmt.Errorf("managed agent does not advertise %s", capability)
	}
	if capability != agenthub.CapabilityPHPRead && !managedNodeHasCapability(target, agenthub.CapabilityPHPRead) {
		return managedNodeEnvelope{}, remotePHPVersion{}, errors.New("managed PHP-FPM actions require the php.read observation capability")
	}
	endpoint := "/api/nodes/" + url.PathEscape(nodeID) + "/php"
	versions, err := requestJSON[[]remotePHPVersion](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
	if err != nil {
		return managedNodeEnvelope{}, remotePHPVersion{}, err
	}
	for _, item := range versions {
		if item.Version == version {
			return target, item, nil
		}
	}
	return managedNodeEnvelope{}, remotePHPVersion{}, errors.New("PHP version is not present in the current managed-node inventory")
}

func requireRemotePHPPool(ctx context.Context, client *apiClient, nodeID, version, pool, capability string) (managedNodeEnvelope, remotePHPPool, error) {
	target, versionState, err := requireRemotePHPVersion(ctx, client, nodeID, version, capability)
	if err != nil {
		return managedNodeEnvelope{}, remotePHPPool{}, err
	}
	for _, item := range versionState.Pools {
		if item.Name == pool {
			return target, item, nil
		}
	}
	return managedNodeEnvelope{}, remotePHPPool{}, errors.New("PHP-FPM pool is not present in the current managed-node inventory")
}

func managedNodeHasCapability(target managedNodeEnvelope, capability string) bool {
	for _, item := range target.Capabilities {
		if item == capability {
			return true
		}
	}
	return false
}

func validateCLIPHPIdentity(version, pool string) (string, string, error) {
	validatedVersion, err := validateCLIPHPVersion(version)
	if err != nil {
		return "", "", err
	}
	validatedPool, err := validateCLIPHPPool(pool)
	if err != nil {
		return "", "", err
	}
	return validatedVersion, validatedPool, nil
}

func validateCLIPHPVersion(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !cliPHPVersionPattern.MatchString(value) {
		return "", errors.New("PHP version must use numeric MAJOR.MINOR form")
	}
	return value, nil
}

func validateCLIPHPPool(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !cliPHPPoolPattern.MatchString(value) {
		return "", errors.New("PHP-FPM pool must use 1-128 portable name characters")
	}
	return value, nil
}

func remotePHPConfigEndpoint(nodeID, version, pool string) string {
	return "/api/nodes/" + url.PathEscape(nodeID) + "/php/" + url.PathEscape(version) + "/pools/" + url.PathEscape(pool)
}

func localPHPConfigEndpoint(version, pool string) string {
	return "/api/php/pools/" + url.PathEscape(version) + "/" + url.PathEscape(pool) + "/config"
}
