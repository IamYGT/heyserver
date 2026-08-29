package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// uptimeStatusPageCLI is deliberately kept local to the CLI.  The API returns
// the same shape for both the inventory and the create/update responses, while
// the CLI only needs the fields that form a complete replacement payload.
type uptimeStatusPageCLI struct {
	ID          int64                        `json:"id"`
	Slug        string                       `json:"slug"`
	Title       string                       `json:"title"`
	Description string                       `json:"description"`
	Theme       string                       `json:"theme"`
	LogoURL     string                       `json:"logo_url"`
	IsPublic    bool                         `json:"is_public"`
	HistoryDays int                          `json:"history_days"`
	Monitors    []uptimeStatusPageMonitorCLI `json:"monitors"`
	CreatedAt   string                       `json:"created_at,omitempty"`
}

type uptimeStatusPageMonitorCLI struct {
	MonitorID   int64  `json:"monitor_id"`
	DisplayName string `json:"display_name,omitempty"`
	SortOrder   int    `json:"sort_order"`
}

type uptimeStatusPageMutationOptions struct {
	Confirm          bool
	Slug             string
	Title            string
	Description      string
	LogoURL          string
	Theme            string
	HistoryDays      int
	Public           bool
	Private          bool
	Visibility       string
	Monitors         uptimeStatusPageMonitorValues
	ClearMonitors    bool
	ClearDescription bool
	ClearLogoURL     bool
	ClearLogo        bool
	visited          map[string]bool
}

// uptimeStatusPageSlugPattern mirrors the API's canonical lowercase slug
// contract.  The API lowercases input before applying this expression; the
// CLI does the same so callers get the same canonical value in the payload.
var uptimeStatusPageSlugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// uptimeStatusPageMonitorValues preserves the order in which repeatable
// --monitor/--monitor-id flags were supplied, including when both aliases are
// mixed in one invocation.
type uptimeStatusPageMonitorValues []string

func (values *uptimeStatusPageMonitorValues) String() string {
	return strings.Join(*values, ",")
}

func (values *uptimeStatusPageMonitorValues) Set(value string) error {
	*values = append(*values, value)
	return nil
}

// runUptimeStatusPage owns the authenticated status-page lifecycle.  The
// normal dispatch passes args beginning with create, update, or delete.  The
// noun aliases below make the function safe to call from a parent dispatcher
// that retains the existing plural inventory spelling.
func runUptimeStatusPage(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) > 0 && (args[0] == "status-page" || args[0] == "status-pages") {
		args = args[1:]
	}
	if len(args) == 0 {
		return errors.New("usage: hserverctl uptime status-page create|update|delete")
	}
	switch args[0] {
	case "create":
		return runUptimeStatusPageCreate(ctx, client, args[1:], out)
	case "update":
		return runUptimeStatusPageUpdate(ctx, client, args[1:], out)
	case "delete":
		return runUptimeStatusPageDelete(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown uptime status-page command %q", args[0])
	}
}

func newUptimeStatusPageMutationFlags(name string) (*flag.FlagSet, *uptimeStatusPageMutationOptions) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	options := &uptimeStatusPageMutationOptions{}
	flags.BoolVar(&options.Confirm, "confirm", false, "confirm the status-page mutation")
	flags.StringVar(&options.Slug, "slug", "", "lowercase public status-page slug")
	flags.StringVar(&options.Title, "title", "", "status-page title")
	flags.StringVar(&options.Description, "description", "", "optional status-page description")
	flags.StringVar(&options.LogoURL, "logo-url", "", "optional absolute HTTP(S) logo URL")
	flags.StringVar(&options.Theme, "theme", "auto", "status-page theme: auto, light, or dark")
	flags.IntVar(&options.HistoryDays, "history-days", 90, "status-page history window in days")
	flags.BoolVar(&options.Public, "public", false, "make the status page public")
	flags.BoolVar(&options.Private, "private", false, "make the status page private")
	flags.StringVar(&options.Visibility, "visibility", "", "status-page visibility: public or private")
	flags.Var(&options.Monitors, "monitor", "monitor ID to expose; repeatable")
	flags.Var(&options.Monitors, "monitor-id", "monitor ID to expose; repeatable alias for --monitor")
	flags.BoolVar(&options.ClearMonitors, "clear-monitors", false, "remove all monitors from the status page")
	flags.BoolVar(&options.ClearDescription, "clear-description", false, "clear the optional description (update only)")
	flags.BoolVar(&options.ClearLogoURL, "clear-logo-url", false, "clear the optional logo URL (update only)")
	flags.BoolVar(&options.ClearLogo, "clear-logo", false, "clear the optional logo URL (update alias)")
	return flags, options
}

func parseUptimeStatusPageMutationFlags(name string, args []string) (*uptimeStatusPageMutationOptions, []string, error) {
	flags, options := newUptimeStatusPageMutationFlags(name)
	if err := flags.Parse(args); err != nil {
		return nil, nil, err
	}
	options.visited = make(map[string]bool)
	flags.Visit(func(item *flag.Flag) { options.visited[item.Name] = true })
	return options, flags.Args(), nil
}

func runUptimeStatusPageCreate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	options, positional, err := parseUptimeStatusPageMutationFlags("uptime status-page create", args)
	if err != nil {
		return err
	}
	if len(positional) != 0 {
		return errors.New("usage: hserverctl uptime status-page create --confirm --slug SLUG --title TITLE [OPTIONS]")
	}
	if !options.Confirm {
		return errors.New("status page creation requires explicit --confirm")
	}
	if !options.visited["slug"] || !options.visited["title"] {
		return errors.New("status page creation requires --slug and --title")
	}
	if err := validateUptimeStatusPageMutationFlags(options, true); err != nil {
		return err
	}

	monitors, err := uptimeStatusPageMonitorEntries(options.Monitors)
	if err != nil {
		return err
	}
	isPublic, err := uptimeStatusPageVisibility(options, true, true)
	if err != nil {
		return err
	}
	page, err := normalizeUptimeStatusPageCLI(uptimeStatusPageCLI{
		Slug:        options.Slug,
		Title:       options.Title,
		Description: options.Description,
		LogoURL:     options.LogoURL,
		Theme:       options.Theme,
		IsPublic:    isPublic,
		HistoryDays: options.HistoryDays,
		Monitors:    monitors,
	}, false)
	if err != nil {
		return err
	}
	return printRequest(ctx, client, out, http.MethodPost, "/api/uptime/status-pages", uptimeStatusPagePayload(page), true)
}

func runUptimeStatusPageUpdate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	options, positional, err := parseUptimeStatusPageMutationFlags("uptime status-page update", args)
	if err != nil {
		return err
	}
	if len(positional) != 1 {
		return errors.New("usage: hserverctl uptime status-page update --confirm [OPTIONS] ID")
	}
	if !options.Confirm {
		return errors.New("status page update requires explicit --confirm")
	}
	if !uptimeStatusPageHasUpdate(options) {
		return errors.New("status page update requires at least one changed field")
	}
	if err := validateUptimeStatusPageMutationFlags(options, false); err != nil {
		return err
	}
	id, err := positiveUptimeID("status page", positional[0])
	if err != nil {
		return err
	}

	pages, err := requestJSON[[]uptimeStatusPageCLI](ctx, client, http.MethodGet, "/api/uptime/status-pages", nil, true)
	if err != nil {
		return err
	}
	var page *uptimeStatusPageCLI
	for index := range pages {
		if pages[index].ID == id {
			candidate := pages[index]
			page = &candidate
			break
		}
	}
	if page == nil {
		return fmt.Errorf("status page %d was not found in status-page inventory", id)
	}

	if err := applyUptimeStatusPageUpdate(page, options); err != nil {
		return err
	}
	normalized, err := normalizeUptimeStatusPageCLI(*page, true)
	if err != nil {
		return fmt.Errorf("invalid status page update: %w", err)
	}
	return printRequest(ctx, client, out, http.MethodPut, uptimeStatusPageEndpoint(id), uptimeStatusPagePayload(normalized), true)
}

func runUptimeStatusPageDelete(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("uptime status-page delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm status-page deletion")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl uptime status-page delete --confirm ID")
	}
	if !*confirmed {
		return errors.New("status page deletion requires explicit --confirm")
	}
	id, err := positiveUptimeID("status page", flags.Args()[0])
	if err != nil {
		return err
	}
	return printRequest(ctx, client, out, http.MethodDelete, uptimeStatusPageEndpoint(id), nil, true)
}

func validateUptimeStatusPageMutationFlags(options *uptimeStatusPageMutationOptions, creating bool) error {
	if options.visited["public"] && options.visited["private"] {
		return errors.New("status page visibility flags --public and --private are mutually exclusive")
	}
	if options.visited["visibility"] && (options.visited["public"] || options.visited["private"]) {
		return errors.New("use either --visibility or --public/--private, not both")
	}
	if options.visited["visibility"] {
		visibility := strings.ToLower(strings.TrimSpace(options.Visibility))
		if visibility != "public" && visibility != "private" {
			return errors.New("status page visibility must be public or private")
		}
	}
	if options.visited["clear-monitors"] && uptimeStatusPageMonitorsVisited(options) {
		return errors.New("--clear-monitors cannot be combined with --monitor")
	}
	if options.visited["clear-monitors"] {
		if !options.ClearMonitors {
			return errors.New("--clear-monitors must be enabled to clear status-page monitors")
		}
		if creating {
			return errors.New("--clear-monitors is only valid for status page update")
		}
	}
	if options.visited["clear-description"] && options.visited["description"] {
		return errors.New("--clear-description cannot be combined with --description")
	}
	if (options.visited["clear-logo-url"] || options.visited["clear-logo"]) && options.visited["logo-url"] {
		return errors.New("--clear-logo-url cannot be combined with --logo-url")
	}
	if creating && (options.visited["clear-description"] || options.visited["clear-logo-url"] || options.visited["clear-logo"]) {
		return errors.New("status page clear flags are only valid for update")
	}
	if options.visited["clear-description"] && !options.ClearDescription {
		return errors.New("--clear-description must be enabled to clear the status-page description")
	}
	if options.visited["clear-logo-url"] && !options.ClearLogoURL {
		return errors.New("--clear-logo-url must be enabled to clear the status-page logo URL")
	}
	if options.visited["clear-logo"] && !options.ClearLogo {
		return errors.New("--clear-logo must be enabled to clear the status-page logo URL")
	}
	if options.visited["history-days"] && (options.HistoryDays < 1 || options.HistoryDays > 3650) {
		return errors.New("status page history_days must be between 1 and 3650")
	}
	if creating && options.HistoryDays < 1 {
		return errors.New("status page history_days must be between 1 and 3650")
	}
	if options.visited["slug"] {
		if _, err := normalizeUptimeStatusPageSlug(options.Slug); err != nil {
			return err
		}
	}
	if options.visited["title"] {
		if _, err := boundedUptimeStatusPageCLIText("status page title", options.Title, 128, true); err != nil {
			return err
		}
	}
	if options.visited["description"] {
		if _, err := boundedUptimeStatusPageCLIText("status page description", options.Description, 2048, false); err != nil {
			return err
		}
	}
	if options.visited["logo-url"] {
		if _, err := validateUptimeStatusPageLogoURL(options.LogoURL); err != nil {
			return err
		}
	}
	if options.visited["theme"] {
		theme := strings.ToLower(strings.TrimSpace(options.Theme))
		if theme != "auto" && theme != "light" && theme != "dark" {
			return errors.New("status page theme must be auto, light, or dark")
		}
	}
	if _, err := uptimeStatusPageMonitorEntries(options.Monitors); err != nil {
		return err
	}
	return nil
}

func uptimeStatusPageHasUpdate(options *uptimeStatusPageMutationOptions) bool {
	for name := range options.visited {
		if name != "confirm" {
			return true
		}
	}
	return false
}

func uptimeStatusPageMonitorsVisited(options *uptimeStatusPageMutationOptions) bool {
	return options.visited["monitor"] || options.visited["monitor-id"]
}

func uptimeStatusPageVisibility(options *uptimeStatusPageMutationOptions, creating, current bool) (bool, error) {
	if options.visited["visibility"] {
		switch strings.ToLower(strings.TrimSpace(options.Visibility)) {
		case "public":
			return true, nil
		case "private":
			return false, nil
		default:
			return false, errors.New("status page visibility must be public or private")
		}
	}
	if options.visited["public"] {
		return options.Public, nil
	}
	if options.visited["private"] {
		return !options.Private, nil
	}
	if creating {
		return true, nil
	}
	return current, nil
}

func applyUptimeStatusPageUpdate(page *uptimeStatusPageCLI, options *uptimeStatusPageMutationOptions) error {
	if options.visited["slug"] {
		page.Slug = options.Slug
	}
	if options.visited["title"] {
		page.Title = options.Title
	}
	if options.visited["description"] {
		page.Description = options.Description
	}
	if options.visited["clear-description"] && options.ClearDescription {
		page.Description = ""
	}
	if options.visited["logo-url"] {
		page.LogoURL = options.LogoURL
	}
	if (options.visited["clear-logo-url"] && options.ClearLogoURL) || (options.visited["clear-logo"] && options.ClearLogo) {
		page.LogoURL = ""
	}
	if options.visited["theme"] {
		page.Theme = options.Theme
	}
	if options.visited["history-days"] {
		page.HistoryDays = options.HistoryDays
	}
	if options.visited["public"] || options.visited["private"] || options.visited["visibility"] {
		isPublic, err := uptimeStatusPageVisibility(options, false, page.IsPublic)
		if err != nil {
			return err
		}
		page.IsPublic = isPublic
	}
	if options.visited["clear-monitors"] && options.ClearMonitors {
		page.Monitors = []uptimeStatusPageMonitorCLI{}
	}
	if len(options.Monitors) > 0 {
		monitors, err := uptimeStatusPageMonitorEntries(options.Monitors)
		if err != nil {
			return err
		}
		page.Monitors = monitors
	}
	return nil
}

func normalizeUptimeStatusPageCLI(page uptimeStatusPageCLI, replacement bool) (uptimeStatusPageCLI, error) {
	slug, err := normalizeUptimeStatusPageSlug(page.Slug)
	if err != nil {
		return uptimeStatusPageCLI{}, err
	}
	title, err := boundedUptimeStatusPageCLIText("status page title", page.Title, 128, true)
	if err != nil {
		return uptimeStatusPageCLI{}, err
	}
	description, err := boundedUptimeStatusPageCLIText("status page description", page.Description, 2048, false)
	if err != nil {
		return uptimeStatusPageCLI{}, err
	}
	theme := strings.ToLower(strings.TrimSpace(page.Theme))
	if theme == "" && !replacement {
		theme = "auto"
	}
	if theme != "auto" && theme != "light" && theme != "dark" {
		return uptimeStatusPageCLI{}, errors.New("status page theme must be auto, light, or dark")
	}
	if page.HistoryDays < 1 || page.HistoryDays > 3650 {
		return uptimeStatusPageCLI{}, errors.New("status page history_days must be between 1 and 3650")
	}
	logoURL, err := validateUptimeStatusPageLogoURL(page.LogoURL)
	if err != nil {
		return uptimeStatusPageCLI{}, err
	}
	monitors, err := normalizeUptimeStatusPageMonitorEntries(page.Monitors)
	if err != nil {
		return uptimeStatusPageCLI{}, err
	}
	page.Slug = slug
	page.Title = title
	page.Description = description
	page.Theme = theme
	page.LogoURL = logoURL
	page.Monitors = monitors
	return page, nil
}

func normalizeUptimeStatusPageSlug(value string) (string, error) {
	clean := strings.ToLower(strings.TrimSpace(value))
	if !uptimeStatusPageSlugPattern.MatchString(clean) {
		return "", errors.New("status page slug must use 1-64 lowercase letters, numbers, or interior hyphens")
	}
	return clean, nil
}

func boundedUptimeStatusPageCLIText(label, value string, maxBytes int, required bool) (string, error) {
	clean := strings.TrimSpace(value)
	if required && clean == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	if !utf8.ValidString(clean) || len(clean) > maxBytes {
		return "", fmt.Errorf("%s must be valid UTF-8 of at most %d bytes", label, maxBytes)
	}
	for _, character := range clean {
		if unicode.IsControl(character) && character != '\t' {
			return "", fmt.Errorf("%s contains unsupported control characters", label)
		}
	}
	return clean, nil
}

func validateUptimeStatusPageLogoURL(value string) (string, error) {
	clean := strings.TrimSpace(value)
	if clean == "" {
		return "", nil
	}
	if len(clean) > 2048 || strings.ContainsAny(clean, "\r\n\x00") {
		return "", errors.New("status page logo_url must be at most 2048 bytes")
	}
	parsed, err := url.Parse(clean)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Fragment != "" {
		return "", errors.New("status page logo_url must be an absolute http:// or https:// URL without credentials or fragments")
	}
	return parsed.String(), nil
}

func uptimeStatusPageMonitorEntries(values []string) ([]uptimeStatusPageMonitorCLI, error) {
	if len(values) > 128 {
		return nil, errors.New("status page accepts at most 128 monitors")
	}
	entries := make([]uptimeStatusPageMonitorCLI, 0, len(values))
	seen := make(map[int64]struct{}, len(values))
	for index, value := range values {
		id, err := positiveUptimeID("status page monitor", strings.TrimSpace(value))
		if err != nil {
			return nil, errors.New("status page monitor IDs must be positive integers")
		}
		if _, exists := seen[id]; exists {
			return nil, fmt.Errorf("status page monitor %d is duplicated", id)
		}
		seen[id] = struct{}{}
		entries = append(entries, uptimeStatusPageMonitorCLI{MonitorID: id, SortOrder: index + 1})
	}
	return entries, nil
}

func normalizeUptimeStatusPageMonitorEntries(entries []uptimeStatusPageMonitorCLI) ([]uptimeStatusPageMonitorCLI, error) {
	if len(entries) > 128 {
		return nil, errors.New("status page accepts at most 128 monitors")
	}
	normalized := make([]uptimeStatusPageMonitorCLI, 0, len(entries))
	seen := make(map[int64]struct{}, len(entries))
	for _, entry := range entries {
		if entry.MonitorID <= 0 {
			return nil, errors.New("status page monitor IDs must be positive integers")
		}
		if _, exists := seen[entry.MonitorID]; exists {
			return nil, fmt.Errorf("status page monitor %d is duplicated", entry.MonitorID)
		}
		seen[entry.MonitorID] = struct{}{}
		displayName, err := boundedUptimeStatusPageCLIText("status page monitor display name", entry.DisplayName, 128, false)
		if err != nil {
			return nil, err
		}
		entry.DisplayName = displayName
		normalized = append(normalized, entry)
	}
	return normalized, nil
}

func uptimeStatusPagePayload(page uptimeStatusPageCLI) map[string]any {
	monitors := page.Monitors
	if monitors == nil {
		monitors = []uptimeStatusPageMonitorCLI{}
	}
	return map[string]any{
		"slug":         page.Slug,
		"title":        page.Title,
		"description":  page.Description,
		"logo_url":     page.LogoURL,
		"theme":        page.Theme,
		"history_days": page.HistoryDays,
		"is_public":    page.IsPublic,
		"monitors":     monitors,
	}
}

func uptimeStatusPageEndpoint(id int64) string {
	return "/api/uptime/status-pages/" + strconv.FormatInt(id, 10)
}
