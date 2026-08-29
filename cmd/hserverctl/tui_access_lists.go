package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
)

// The access-list API is deliberately panel-local.  Keep the endpoint names
// in one place so a future TUI integration cannot accidentally turn these
// operations into a managed-node request.
const (
	tuiSecurityAccessBlacklistEndpoint = "/api/security/ip-blacklist"
	tuiSecurityAccessWhitelistEndpoint = "/api/security/ip-whitelist"

	// A large API response is still bounded by apiClient, but an interactive
	// terminal must not try to render every row it receives.
	tuiSecurityAccessDefaultMaxRows  = 200
	tuiSecurityAccessMaxRenderWidth  = 240
	tuiSecurityAccessMaxCommentRunes = 512
)

// TUISecurityAccessBlacklistEndpoint and
// TUISecurityAccessWhitelistEndpoint are the canonical panel API resources.
// They are exported as integration touchpoints while the implementation stays
// in the command package.
const (
	TUISecurityAccessBlacklistEndpoint = tuiSecurityAccessBlacklistEndpoint
	TUISecurityAccessWhitelistEndpoint = tuiSecurityAccessWhitelistEndpoint
)

type tuiSecurityAccessListType string

const (
	tuiSecurityAccessBlacklist tuiSecurityAccessListType = "blacklist"
	tuiSecurityAccessWhitelist tuiSecurityAccessListType = "whitelist"
)

const (
	TUISecurityAccessBlacklist = string(tuiSecurityAccessBlacklist)
	TUISecurityAccessWhitelist = string(tuiSecurityAccessWhitelist)
)

// tuiSecurityAccessListsState is intentionally separate from tuiSecurityState:
// the existing security tab owns score and Fail2Ban observations, while this
// state carries the two persistent panel-local access lists.  Each list has
// its own loaded bit so a temporary failure in one endpoint does not erase the
// other list.
type tuiSecurityAccessListsState struct {
	Supported       bool
	Blacklist       []cliSecurityIPEntry
	Whitelist       []cliSecurityIPEntry
	BlacklistLoaded bool
	WhitelistLoaded bool
	Warnings        []string
	UnsupportedNote string
}

// TUISecurityAccessListsState is an integration alias for callers wiring the
// access-list state into the existing TUI model.
type TUISecurityAccessListsState = tuiSecurityAccessListsState

type tuiSecurityAccessListsMsg struct {
	TargetID string
	State    tuiSecurityAccessListsState
}

type tuiSecurityAccessListMsg struct {
	TargetID string
	ListType tuiSecurityAccessListType
	Entries  []cliSecurityIPEntry
	Err      error
}

// loadTUISecurityAccessListsCmd loads both independent panel-local resources.
// It returns a message even when one resource is unavailable; the state keeps
// the successful half usable and records a safe warning for the other half.
func loadTUISecurityAccessListsCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		return tuiSecurityAccessListsMsg{
			TargetID: target.ID,
			State:    loadTUISecurityAccessLists(ctx, client, target),
		}
	}
}

// loadTUISecurityAccessLists keeps the local-only boundary explicit before it
// invokes either endpoint.  A managed target therefore causes zero requests,
// even when it is online.
func loadTUISecurityAccessLists(ctx context.Context, client *apiClient, target tuiTarget) tuiSecurityAccessListsState {
	if !target.Local {
		return tuiSecurityAccessListsState{
			UnsupportedNote: "Panel-local IP blacklist and whitelist management is not an advertised managed-node capability.",
		}
	}

	state := tuiSecurityAccessListsState{Supported: true}
	blacklist, err := loadTUISecurityAccessList(ctx, client, target, string(tuiSecurityAccessBlacklist))
	if err != nil {
		state.Warnings = append(state.Warnings, "IP blacklist unavailable: "+safeTUISecurityAccessError(err))
	} else {
		state.BlacklistLoaded = true
		state.Blacklist = blacklist
	}

	whitelist, err := loadTUISecurityAccessList(ctx, client, target, string(tuiSecurityAccessWhitelist))
	if err != nil {
		state.Warnings = append(state.Warnings, "IP whitelist unavailable: "+safeTUISecurityAccessError(err))
	} else {
		state.WhitelistLoaded = true
		state.Whitelist = whitelist
	}
	return state
}

// LoadTUISecurityAccessLists is the exported integration alias for the
// aggregate loader.  The command package remains the owner of apiClient and
// tuiTarget, so this is primarily a stable root-wiring name.
func LoadTUISecurityAccessLists(ctx context.Context, client *apiClient, target tuiTarget) tuiSecurityAccessListsState {
	return loadTUISecurityAccessLists(ctx, client, target)
}

// loadTUISecurityAccessListCmd is useful when a refresh action only needs one
// side of the access-list view.
func loadTUISecurityAccessListCmd(ctx context.Context, client *apiClient, target tuiTarget, listType string) tea.Cmd {
	return func() tea.Msg {
		normalized, err := normalizeTUISecurityAccessListType(listType)
		if err != nil {
			return tuiSecurityAccessListMsg{TargetID: target.ID, ListType: tuiSecurityAccessListType(listType), Err: err}
		}
		entries, err := loadTUISecurityAccessList(ctx, client, target, string(normalized))
		return tuiSecurityAccessListMsg{TargetID: target.ID, ListType: normalized, Entries: entries, Err: err}
	}
}

// loadTUISecurityAccessList loads one canonical access-list endpoint.  The
// endpoint is selected from a fixed allow-list; a caller cannot inject a
// managed-node path or an arbitrary URL segment.
func loadTUISecurityAccessList(ctx context.Context, client *apiClient, target tuiTarget, listType string) ([]cliSecurityIPEntry, error) {
	if !target.Local {
		return nil, errors.New("security IP access lists are available only on the panel host")
	}
	endpoint, err := tuiSecurityAccessEndpoint(listType)
	if err != nil {
		return nil, err
	}
	if client == nil {
		return nil, errors.New("security IP access-list client is not configured")
	}
	entries, err := requestJSON[[]cliSecurityIPEntry](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
	if err != nil {
		return nil, err
	}
	if entries == nil {
		entries = []cliSecurityIPEntry{}
	}
	return append([]cliSecurityIPEntry(nil), entries...), nil
}

func loadTUISecurityAccessBlacklist(ctx context.Context, client *apiClient, target tuiTarget) ([]cliSecurityIPEntry, error) {
	return loadTUISecurityAccessList(ctx, client, target, string(tuiSecurityAccessBlacklist))
}

func loadTUISecurityAccessWhitelist(ctx context.Context, client *apiClient, target tuiTarget) ([]cliSecurityIPEntry, error) {
	return loadTUISecurityAccessList(ctx, client, target, string(tuiSecurityAccessWhitelist))
}

func normalizeTUISecurityAccessListType(value string) (tuiSecurityAccessListType, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "blacklist", "ip-blacklist":
		return tuiSecurityAccessBlacklist, nil
	case "whitelist", "ip-whitelist":
		return tuiSecurityAccessWhitelist, nil
	default:
		return "", errors.New("unsupported security IP access-list type")
	}
}

func tuiSecurityAccessEndpoint(listType string) (string, error) {
	normalized, err := normalizeTUISecurityAccessListType(listType)
	if err != nil {
		return "", err
	}
	if normalized == tuiSecurityAccessBlacklist {
		return tuiSecurityAccessBlacklistEndpoint, nil
	}
	return tuiSecurityAccessWhitelistEndpoint, nil
}

// tuiSecurityAccessEntryView is a display-only projection.  It intentionally
// does not expose the original comment or IP string to renderers: every text
// field has already had terminal controls removed and has a finite width.
type tuiSecurityAccessEntryView struct {
	ID        int64
	IP        string
	ListType  string
	Comment   string
	CreatedAt string
	ExpiresAt string
}

type TUISecurityAccessEntryView = tuiSecurityAccessEntryView

// projectTUISecurityAccessEntries converts canonical API entries into a
// bounded, terminal-safe projection.  API order is preserved because the
// canonical list endpoints already return newest entries first.
func projectTUISecurityAccessEntries(entries []cliSecurityIPEntry, maxRows int) []tuiSecurityAccessEntryView {
	limit := tuiSecurityAccessRowLimit(maxRows)
	if len(entries) > limit {
		entries = entries[:limit]
	}
	projected := make([]tuiSecurityAccessEntryView, 0, len(entries))
	for _, entry := range entries {
		projected = append(projected, tuiSecurityAccessEntryView{
			ID:        entry.ID,
			IP:        tuiSecurityAccessDisplayText(entry.IP, 128),
			ListType:  tuiSecurityAccessDisplayText(entry.ListType, 32),
			Comment:   tuiSecurityAccessDisplayText(entry.Comment, tuiSecurityAccessMaxCommentRunes),
			CreatedAt: securityIPEntryTimeText(entry.CreatedAt, "N/A"),
			ExpiresAt: securityIPEntryExpiryText(entry.ExpiresAt),
		})
	}
	return projected
}

// ProjectTUISecurityAccessEntries is the exported display-projection alias.
func ProjectTUISecurityAccessEntries(entries []cliSecurityIPEntry, maxRows int) []tuiSecurityAccessEntryView {
	return projectTUISecurityAccessEntries(entries, maxRows)
}

func tuiSecurityAccessDisplayText(value string, maximum int) string {
	value = strings.TrimSpace(strings.Join(strings.Fields(sanitizeTUIText(value)), " "))
	if value == "" {
		return "N/A"
	}
	return truncateTUIWidth(value, maximum)
}

// renderTUISecurityAccessListRows returns at most maxRows lines.  When the
// source contains more entries, the final line is a bounded omission marker.
// Width is capped so a malformed caller cannot create an unbounded terminal
// cell even if the selected terminal reports an implausibly large width.
func renderTUISecurityAccessListRows(entries []cliSecurityIPEntry, width int, maxRows ...int) []string {
	width = tuiSecurityAccessRenderWidth(width)
	limit := tuiSecurityAccessDefaultMaxRows
	if len(maxRows) > 0 {
		limit = tuiSecurityAccessRowLimit(maxRows[0])
	}

	omitted := 0
	projectLimit := limit
	if len(entries) > limit {
		// Reserve one line for the omission marker, keeping the total bounded.
		projectLimit = maxInt(0, limit-1)
		omitted = len(entries) - projectLimit
	}
	projectInput := entries
	if projectLimit == 0 {
		// projectTUISecurityAccessEntries treats zero as the default limit for
		// direct callers.  Passing an empty input here retains the one-line
		// omission-marker contract when maxRows is one.
		projectInput = nil
		projectLimit = 1
	}
	projected := projectTUISecurityAccessEntries(projectInput, projectLimit)
	rows := make([]string, 0, len(projected)+1)
	for _, entry := range projected {
		comment := entry.Comment
		if comment == "N/A" {
			comment = "—"
		}
		row := fmt.Sprintf("%-45s  %-32s  added %s  expires %s", entry.IP, comment, entry.CreatedAt, entry.ExpiresAt)
		rows = append(rows, truncateTUIWidth(row, width))
	}
	if omitted > 0 {
		rows = append(rows, truncateTUIWidth(fmt.Sprintf("… %d additional access-list entries not shown", omitted), width))
	}
	if len(rows) == 0 {
		rows = append(rows, truncateTUIWidth("No entries.", width))
	}
	return rows
}

// RenderTUISecurityAccessListRows is the exported row-rendering alias.
func RenderTUISecurityAccessListRows(entries []cliSecurityIPEntry, width int, maxRows ...int) []string {
	return renderTUISecurityAccessListRows(entries, width, maxRows...)
}

func renderTUISecurityAccessList(entries []cliSecurityIPEntry, width int, maxRows ...int) string {
	return strings.Join(renderTUISecurityAccessListRows(entries, width, maxRows...), "\n")
}

func RenderTUISecurityAccessList(entries []cliSecurityIPEntry, width int, maxRows ...int) string {
	return renderTUISecurityAccessList(entries, width, maxRows...)
}

func tuiSecurityAccessRenderWidth(width int) int {
	if width <= 0 {
		return 80
	}
	if width > tuiSecurityAccessMaxRenderWidth {
		return tuiSecurityAccessMaxRenderWidth
	}
	return width
}

func tuiSecurityAccessRowLimit(limit int) int {
	if limit <= 0 {
		return tuiSecurityAccessDefaultMaxRows
	}
	if limit > tuiSecurityAccessDefaultMaxRows {
		return tuiSecurityAccessDefaultMaxRows
	}
	return limit
}

// tuiSecurityAccessListOperation carries the complete mutation intent,
// including confirmation state.  Keeping Confirmed in operation data avoids
// a future integration accidentally treating a menu selection as consent.
type tuiSecurityAccessListOperation struct {
	Target           tuiTarget
	Action           string
	ListType         string
	IP               string
	Comment          string
	ExpiresInMinutes *int
	Confirmed        bool
	Label            string
}

type TUISecurityAccessListOperation = tuiSecurityAccessListOperation

// tuiSecurityAccessFormField mirrors the bounded text-field contract used by
// the existing DNS and Users dialogs without coupling access-list input to
// either domain.  The form only assembles an operation; no request is made
// until the separate confirmation dialog receives Y.
type tuiSecurityAccessFormField struct {
	Key         string
	Label       string
	Value       string
	Placeholder string
	Maximum     int
}

type tuiSecurityAccessForm struct {
	ListType string
	Cursor   int
	Fields   []tuiSecurityAccessFormField
	Error    string
}

func (form tuiSecurityAccessForm) rawValue(key string) string {
	for _, field := range form.Fields {
		if field.Key == key {
			return field.Value
		}
	}
	return ""
}

// openSecurityAccessAddChoices starts the add flow only for the selected
// panel-local target.  The list choice is intentionally explicit so adding a
// value can never silently select the wrong persistent list.
func (model *tuiModel) openSecurityAccessAddChoices() {
	if !model.snapshot.Selected.Local {
		model.notice, model.noticeError = "Security IP blacklist/whitelist mutations run only on the panel host", true
		return
	}
	if !model.securityLoaded || !model.security.Supported {
		model.notice, model.noticeError = "Load the local Security section before changing an IP access list", true
		return
	}
	model.dialog = tuiDialog{
		Mode:  tuiDialogChoices,
		Title: "Add persistent local IP access entry",
		Body: []string{
			"Choose the panel-local list, then enter an IP/CIDR and optional bounded metadata.",
			"A separate confirmation is required before the API request.",
		},
		Options: []tuiDialogOption{
			{Label: "Add to blacklist", Action: string(tuiSecurityAccessBlacklist)},
			{Label: "Add to whitelist", Action: string(tuiSecurityAccessWhitelist)},
		},
		SecurityAccessAction: "add",
	}
}

func (model *tuiModel) openSecurityAccessForm(listType string) {
	normalized, err := normalizeTUISecurityAccessListType(listType)
	if err != nil {
		model.notice, model.noticeError = safeTUISecurityAccessFormError(err), true
		return
	}
	model.dialog = tuiDialog{
		Mode:  tuiDialogSecurityAccessForm,
		Title: "Add local " + string(normalized) + " entry",
		Body: []string{
			"IP and CIDR input uses the canonical security validator.",
			"Comment and expiry are optional; expiry is measured in minutes (zero means no expiry).",
		},
		SecurityAccessForm: tuiSecurityAccessForm{
			ListType: string(normalized),
			Fields: []tuiSecurityAccessFormField{
				{Key: "ip", Label: "IP / CIDR", Placeholder: "198.51.100.20 or 2001:db8::/64", Maximum: 128},
				{Key: "comment", Label: "Comment", Placeholder: "optional reason", Maximum: tuiSecurityAccessMaxCommentRunes},
				{Key: "expires", Label: "Expiry min", Placeholder: "optional; 0 = none", Maximum: 10},
			},
		},
	}
}

func (model tuiModel) updateSecurityAccessFormKey(key string) (tea.Model, tea.Cmd) {
	form := &model.dialog.SecurityAccessForm
	if len(form.Fields) == 0 {
		model.dialog = tuiDialog{}
		return model, nil
	}
	switch key {
	case "esc":
		model.dialog = tuiDialog{}
		return model, nil
	case "tab", "down":
		form.Cursor = wrapIndex(form.Cursor+1, len(form.Fields))
		form.Error = ""
		return model, nil
	case "shift+tab", "up":
		form.Cursor = wrapIndex(form.Cursor-1, len(form.Fields))
		form.Error = ""
		return model, nil
	case "backspace", "ctrl+h":
		runes := []rune(form.Fields[form.Cursor].Value)
		if len(runes) > 0 {
			form.Fields[form.Cursor].Value = string(runes[:len(runes)-1])
		}
		form.Error = ""
		return model, nil
	case "ctrl+u":
		form.Fields[form.Cursor].Value = ""
		form.Error = ""
		return model, nil
	case "enter":
		operation, err := operationFromTUISecurityAccessForm(*form, model.snapshot.Selected)
		if err != nil {
			form.Error = safeTUISecurityAccessFormError(err)
			return model, nil
		}
		model.openSecurityAccessConfirmation(operation)
		return model, nil
	}
	if key == "space" {
		key = " "
	}
	if utf8.RuneCountInString(key) == 1 {
		character, _ := utf8.DecodeRuneInString(key)
		field := &form.Fields[form.Cursor]
		if !unicode.IsControl(character) && utf8.RuneCountInString(field.Value) < field.Maximum {
			field.Value += key
			form.Error = ""
		}
	}
	return model, nil
}

func operationFromTUISecurityAccessForm(form tuiSecurityAccessForm, target tuiTarget) (tuiSecurityAccessListOperation, error) {
	if !target.Local {
		return tuiSecurityAccessListOperation{}, errors.New("security IP blacklist/whitelist mutations run only on the panel host")
	}
	listType, err := normalizeTUISecurityAccessListType(form.ListType)
	if err != nil {
		return tuiSecurityAccessListOperation{}, err
	}
	ip, err := validateTUISecurityAccessIPOrCIDR(form.rawValue("ip"))
	if err != nil {
		return tuiSecurityAccessListOperation{}, err
	}
	comment, err := validateTUISecurityAccessComment(form.rawValue("comment"))
	if err != nil {
		return tuiSecurityAccessListOperation{}, err
	}
	expiresText := strings.TrimSpace(form.rawValue("expires"))
	var expires *int
	if expiresText != "" {
		value, parseErr := strconv.Atoi(expiresText)
		if parseErr != nil {
			return tuiSecurityAccessListOperation{}, errors.New("security IP-list expiration must be a whole number of minutes")
		}
		expires = &value
	}
	expires, err = validateTUISecurityAccessExpiry(expires)
	if err != nil {
		return tuiSecurityAccessListOperation{}, err
	}
	return tuiSecurityAccessListOperation{
		Target:           target,
		Action:           "add",
		ListType:         string(listType),
		IP:               ip,
		Comment:          comment,
		ExpiresInMinutes: expires,
		Label:            fmt.Sprintf("Add %s to %s", ip, string(listType)),
	}, nil
}

func safeTUISecurityAccessFormError(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(sanitizeTUIText(err.Error()))
}

func (model *tuiModel) openSecurityAccessConfirmation(operation tuiSecurityAccessListOperation) {
	operation.Label = tuiSecurityAccessDisplayText(operation.Label, 128)
	body := []string{
		"Target: " + tuiSecurityAccessDisplayText(operation.Target.label(), 96),
		"The selected panel-local list will be re-observed after this request.",
	}
	if operation.Comment != "" {
		body = append(body, "Comment: "+tuiSecurityAccessDisplayText(operation.Comment, 96))
	}
	if operation.ExpiresInMinutes == nil || *operation.ExpiresInMinutes == 0 {
		body = append(body, "Expiry: none")
	} else {
		body = append(body, fmt.Sprintf("Expiry: %d minute(s)", *operation.ExpiresInMinutes))
	}
	model.dialog = tuiDialog{
		Mode:                    tuiDialogConfirm,
		Title:                   "Confirm local security access-list mutation",
		Body:                    body,
		SecurityAccessOperation: &operation,
	}
}

// tuiSecurityAccessListMutation is the exact optional POST shape accepted by
// the API.  A nil expiration is omitted; a non-nil zero is sent as zero and
// has the API's documented "no expiration" meaning.
type tuiSecurityAccessListMutation struct {
	IP               string
	Comment          string
	ExpiresInMinutes *int
}

type TUISecurityAccessListMutation = tuiSecurityAccessListMutation

// runTUISecurityAccessListOperation only talks to the panel-local API.  It
// validates all mutation data before the request and never accepts a managed
// target as a local substitute.
func runTUISecurityAccessListOperation(ctx context.Context, client *apiClient, operation tuiSecurityAccessListOperation) (string, error) {
	if !operation.Target.Local {
		return "", errors.New("security IP access-list mutations are available only on the panel host")
	}
	if !operation.Confirmed {
		return "", errors.New("security IP-list mutation requires explicit confirmation")
	}
	listType, err := normalizeTUISecurityAccessListType(operation.ListType)
	if err != nil {
		return "", err
	}
	if client == nil {
		return "", errors.New("security IP access-list client is not configured")
	}

	action := strings.ToLower(strings.TrimSpace(operation.Action))
	if action != "add" && action != "delete" {
		return "", errors.New("unsupported security IP access-list action")
	}
	ip, err := validateTUISecurityAccessIPOrCIDR(operation.IP)
	if err != nil {
		return "", err
	}
	if action == "delete" {
		if strings.TrimSpace(operation.Comment) != "" || operation.ExpiresInMinutes != nil {
			return "", errors.New("security IP-list deletion accepts only an IP or CIDR")
		}
		endpoint, _ := tuiSecurityAccessEndpoint(string(listType))
		endpoint += "/" + url.PathEscape(ip)
		if _, err := client.request(ctx, http.MethodDelete, endpoint, nil, true); err != nil {
			return "", err
		}
		return fmt.Sprintf("Deleted %s from %s", ip, string(listType)), nil
	}

	comment, err := validateTUISecurityAccessComment(operation.Comment)
	if err != nil {
		return "", err
	}
	expiresInMinutes, err := validateTUISecurityAccessExpiry(operation.ExpiresInMinutes)
	if err != nil {
		return "", err
	}
	payload := map[string]any{"ip": ip, "comment": comment}
	if expiresInMinutes != nil {
		payload["expiresInMinutes"] = *expiresInMinutes
	}
	endpoint, _ := tuiSecurityAccessEndpoint(string(listType))
	if _, err := requestJSON[cliSecurityIPEntry](ctx, client.withTimeout(2*time.Minute), http.MethodPost, endpoint, payload, true); err != nil {
		return "", err
	}
	return fmt.Sprintf("Added %s to %s", ip, string(listType)), nil
}

func RunTUISecurityAccessListOperation(ctx context.Context, client *apiClient, operation tuiSecurityAccessListOperation) (string, error) {
	return runTUISecurityAccessListOperation(ctx, client, operation)
}

// runTUISecurityAccessListOperationCmd adapts the standalone runner to the
// existing TUI operation message.  This lets the root model retain its normal
// operating/dialog/error lifecycle without teaching this foundation about
// shared model fields.
func runTUISecurityAccessListOperationCmd(ctx context.Context, client *apiClient, operation tuiSecurityAccessListOperation) tea.Cmd {
	return func() tea.Msg {
		message, err := runTUISecurityAccessListOperation(ctx, client, operation)
		return tuiOperationMsg{Message: message, Err: err}
	}
}

// validateTUISecurityAccessIPOrCIDR deliberately delegates to the canonical
// CLI validator so scripted CLI and TUI input have identical IP/CIDR parity.
// net.ParseIP/net.ParseCIDR reject zones, malformed prefixes, and arbitrary
// terminal/control text before it can reach an endpoint path.
func validateTUISecurityAccessIPOrCIDR(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("security IP-list address or CIDR is required")
	}
	ip, err := validateCLISecurityIPOrCIDR(value)
	if err != nil {
		return "", errors.New(sanitizeTUIText(err.Error()))
	}
	return ip, nil
}

func ValidateTUISecurityAccessIPOrCIDR(value string) (string, error) {
	return validateTUISecurityAccessIPOrCIDR(value)
}

func validateTUISecurityAccessComment(value string) (string, error) {
	value = strings.TrimSpace(value)
	if !utf8.ValidString(value) {
		return "", errors.New("security IP-list comment must be valid UTF-8")
	}
	if utf8.RuneCountInString(value) > tuiSecurityAccessMaxCommentRunes {
		return "", fmt.Errorf("security IP-list comment must be at most %d characters", tuiSecurityAccessMaxCommentRunes)
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return "", errors.New("security IP-list comment must not contain terminal control characters")
		}
	}
	return value, nil
}

func validateTUISecurityAccessExpiry(value *int) (*int, error) {
	if value == nil {
		return nil, nil
	}
	if *value < 0 {
		return nil, errors.New("security IP-list expiration must be zero or greater")
	}
	// The API converts minutes to time.Duration.  Keep the operation inside
	// that representable range while retaining the API's non-negative-minute
	// semantics instead of introducing a product-specific calendar limit.
	const maxDurationMinutes = int64((1<<63 - 1) / int64(time.Minute))
	if int64(*value) > maxDurationMinutes {
		return nil, errors.New("security IP-list expiration is too large")
	}
	copyValue := *value
	return &copyValue, nil
}

func safeTUISecurityAccessError(err error) string {
	if err == nil {
		return ""
	}
	// clientErrorMessage preserves typed status/recovery context while
	// discarding response bodies and terminal controls.  It is the only error
	// text copied into aggregate list warnings.
	return clientErrorMessage(err)
}
