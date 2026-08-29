package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	mailsvc "github.com/IamYGT/heyserver/internal/services/mail"
)

const (
	// The TUI intentionally keeps mail observations small. Operators can use
	// hserverctl mail for deeper, explicitly bounded queries.
	tuiMailRecentLogLimit   = 50
	tuiMailQueueLimit       = 100
	tuiMailDeliveryLogLimit = 50
)

type tuiMailState struct {
	Local     bool
	Supported bool
	Status    string
	Message   string

	ServiceStatus     mailsvc.ServiceStatus
	Overview          mailServiceOverview
	StatusAvailable   bool
	OverviewAvailable bool

	Logs              []mailsvc.LogEntry
	Queue             []mailsvc.QueueMessage
	Domains           []mailsvc.MailDomain
	Accounts          []mailsvc.MailAccount
	LogsAvailable     bool
	QueueAvailable    bool
	DomainsAvailable  bool
	AccountsAvailable bool
	Warnings          []string
}

type tuiMailMsg struct {
	TargetID string
	State    tuiMailState
	Err      error
}

type tuiMailDeliveryMsg struct {
	TargetID string
	Email    string
	Entries  []mailsvc.LogEntry
	Err      error
}

// tuiMailMutation is deliberately separate from tuiOperation. The generic
// operation dispatcher is owned by the broader TUI control-center contract;
// mail queue mutations remain local-only and use the same two-step choices /
// confirmation interaction without widening managed-node capabilities.
type tuiMailMutation struct {
	Action string
	Target tuiTarget
	Queue  mailsvc.QueueMessage
}

func loadTUIMailCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		state, err := loadTUIMail(ctx, client, target)
		return tuiMailMsg{TargetID: target.ID, State: state, Err: err}
	}
}

func loadTUIMail(ctx context.Context, client *apiClient, target tuiTarget) (tuiMailState, error) {
	state := tuiMailState{Local: target.Local}
	if !target.Local {
		state.Status = "unsupported"
		state.Message = "Mail service, queue, domains, and accounts are panel-host resources; select Local."
		return state, nil
	}

	// Keep both canonical service endpoints in the TUI inventory. Overview is
	// the richer response and remains sufficient when an older panel lacks the
	// separate status route.
	status, statusErr := requestJSON[mailsvc.ServiceStatus](ctx, client.withTimeout(45*time.Second), http.MethodGet, "/api/mail/service/status", nil, true)
	if statusErr == nil {
		state.ServiceStatus = status
		state.StatusAvailable = true
	}
	overview, overviewErr := requestJSON[mailServiceOverview](ctx, client.withTimeout(45*time.Second), http.MethodGet, "/api/mail/service/overview", nil, true)
	if overviewErr == nil {
		state.Overview = overview
		state.OverviewAvailable = true
		if !state.StatusAvailable {
			state.ServiceStatus = overview.Status
		}
	}
	if statusErr != nil && overviewErr != nil {
		state.Warnings = append(state.Warnings, "Mail service status and overview unavailable")
	}

	logs, err := requestJSON[mailLogResponse](ctx, client.withTimeout(45*time.Second), http.MethodGet,
		fmt.Sprintf("/api/mail/logs?lines=%d", tuiMailRecentLogLimit), nil, true)
	if err != nil {
		state.Warnings = append(state.Warnings, "Recent mail logs unavailable")
	} else {
		state.Logs = sanitizeTUIMailLogs(limitTUIMailLogs(logs.Entries, tuiMailRecentLogLimit))
		state.LogsAvailable = true
	}

	queue, err := requestJSON[[]mailsvc.QueueMessage](ctx, client.withTimeout(45*time.Second), http.MethodGet,
		fmt.Sprintf("/api/mail/queue?limit=%d", tuiMailQueueLimit), nil, true)
	if err != nil {
		state.Warnings = append(state.Warnings, "Mail queue unavailable")
	} else {
		if queue == nil {
			queue = []mailsvc.QueueMessage{}
		}
		state.Queue = queue
		state.QueueAvailable = true
	}

	domains, err := requestJSON[[]mailsvc.MailDomain](ctx, client.withTimeout(45*time.Second), http.MethodGet, "/api/mail/domains", nil, true)
	if err != nil {
		state.Warnings = append(state.Warnings, "Mail domain inventory unavailable")
	} else {
		if domains == nil {
			domains = []mailsvc.MailDomain{}
		}
		state.Domains = domains
		state.DomainsAvailable = true
	}

	accounts, err := requestJSON[[]mailsvc.MailAccount](ctx, client.withTimeout(45*time.Second), http.MethodGet, "/api/mail/accounts", nil, true)
	if err != nil {
		state.Warnings = append(state.Warnings, "Mail account inventory unavailable")
	} else {
		if accounts == nil {
			accounts = []mailsvc.MailAccount{}
		}
		for index := range accounts {
			if accounts[index].Aliases == nil {
				accounts[index].Aliases = []string{}
			}
		}
		state.Accounts = accounts
		state.AccountsAvailable = true
	}

	state.Supported = state.StatusAvailable || state.OverviewAvailable || state.LogsAvailable || state.QueueAvailable || state.DomainsAvailable || state.AccountsAvailable
	state.Status = tuiMailIntegrationStatus(state, statusErr, overviewErr)
	switch state.Status {
	case "not_configured":
		state.Message = "Mail integration is not configured on this panel."
	case "unavailable":
		state.Message = "Mail integration is unavailable on this panel."
	}
	return state, nil
}

func tuiMailIntegrationStatus(state tuiMailState, statusErr, overviewErr error) string {
	// The overview source map is authoritative for optional sub-sources. Only
	// inspect known keys; unknown provider fields never reach the terminal.
	for _, key := range []string{"status", "version", "listeners", "storage"} {
		source, ok := state.Overview.Sources[key]
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(source.State), "not_configured") {
			return "not_configured"
		}
		if !source.Available {
			return "unavailable"
		}
	}

	serviceStatus := strings.ToLower(strings.TrimSpace(state.ServiceStatus.Status))
	switch serviceStatus {
	case "not_configured":
		return "not_configured"
	case "unknown", "failed":
		return "unavailable"
	case "running", "active", "healthy":
		return "healthy"
	case "stopped", "inactive":
		return "stopped"
	}
	if statusErr != nil && overviewErr != nil {
		return tuiMailErrorState(statusErr, overviewErr)
	}
	return "unavailable"
}

func tuiMailErrorState(errs ...error) string {
	for _, err := range errs {
		if err == nil {
			continue
		}
		var apiErr *apiError
		if errors.As(err, &apiErr) {
			if strings.EqualFold(strings.TrimSpace(apiErr.State), "not_configured") || apiErr.StatusCode == http.StatusServiceUnavailable && strings.Contains(strings.ToLower(apiErr.Message), "not configured") {
				return "not_configured"
			}
		}
		if strings.Contains(strings.ToLower(err.Error()), "not configured") {
			return "not_configured"
		}
	}
	return "unavailable"
}

func limitTUIMailLogs(entries []mailsvc.LogEntry, limit int) []mailsvc.LogEntry {
	if limit <= 0 || len(entries) <= limit {
		return entries
	}
	return entries[len(entries)-limit:]
}

func sanitizeTUIMailLogs(entries []mailsvc.LogEntry) []mailsvc.LogEntry {
	if entries == nil {
		return []mailsvc.LogEntry{}
	}
	result := make([]mailsvc.LogEntry, len(entries))
	for index, entry := range entries {
		result[index] = mailsvc.LogEntry{
			Timestamp: sanitizeTUILogLine(entry.Timestamp),
			Level:     sanitizeTUILogLine(entry.Level),
			Message:   sanitizeTUILogLine(redactAPISecrets(entry.Message)),
		}
	}
	return result
}

func loadTUIMailDeliveryCmd(ctx context.Context, client *apiClient, target tuiTarget, email string) tea.Cmd {
	return func() tea.Msg {
		entries, err := loadTUIMailDelivery(ctx, client, target, email)
		return tuiMailDeliveryMsg{TargetID: target.ID, Email: email, Entries: entries, Err: err}
	}
}

func loadTUIMailDelivery(ctx context.Context, client *apiClient, target tuiTarget, email string) ([]mailsvc.LogEntry, error) {
	if !target.Local {
		return nil, errors.New("mail delivery logs require the panel host")
	}
	selected, err := normalizeMailPathValue("mail delivery email", email)
	if err != nil {
		return nil, err
	}
	response, err := requestJSON[mailDeliveryLogResponse](ctx, client.withTimeout(45*time.Second), http.MethodGet,
		"/api/mail/logs/delivery?email="+url.QueryEscape(selected), nil, true)
	if err != nil {
		return nil, err
	}
	return sanitizeTUIMailLogs(limitTUIMailLogs(response.Entries, tuiMailDeliveryLogLimit)), nil
}

func tuiMailLogLines(entries []mailsvc.LogEntry) []string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		timestamp := valueOrNA(entry.Timestamp)
		level := strings.ToUpper(valueOrNA(entry.Level))
		message := valueOrNA(sanitizeTUILogLine(redactAPISecrets(entry.Message)))
		lines = append(lines, fmt.Sprintf("%s  %-5s  %s", timestamp, truncateTUI(level, 5), message))
	}
	return lines
}

func (model tuiModel) loadMail() (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading mail service, queue, logs, domains, and accounts…"
	model.noticeError = false
	return model, loadTUIMailCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model tuiModel) activateMailItem() (tea.Model, tea.Cmd) {
	if !model.mailLoaded || model.mailTarget != model.selectedTargetID {
		return model.loadMail()
	}
	state := model.mail
	if !state.Local {
		model.notice = valueOrNA(state.Message)
		model.noticeError = true
		return model, nil
	}
	if model.cursor < 0 || model.cursor >= mailTUIItemCount(state) {
		return model, nil
	}
	cursor := model.cursor
	if cursor < len(state.Queue) {
		model.openMailQueueActions(state.Queue[cursor])
		return model, nil
	}
	cursor -= len(state.Queue)
	if cursor < len(state.Domains) {
		domain := state.Domains[cursor]
		lines := []string{"Domain: " + tuiMailDisplayValue(domain.Name), "Description: " + tuiMailDisplayValue(domain.Description)}
		model.dialog = tuiDialog{
			Mode: tuiDialogLogs, Title: "Mail domain · " + truncateTUI(redactAPISecrets(domain.Name), 48),
			LogLines: lines, LogScroll: maxInt(0, len(lines)-1),
			LogReloadNotice: "Refresh the Mail section to reload domain inventory",
		}
		return model, nil
	}
	cursor -= len(state.Domains)
	if cursor < len(state.Accounts) {
		account := state.Accounts[cursor]
		if strings.TrimSpace(account.Email) == "" {
			model.notice = "Selected mail account has no stable email identity"
			model.noticeError = true
			return model, nil
		}
		model.resourceLoading = true
		model.notice = "Loading delivery history for " + truncateTUI(redactAPISecrets(account.Email), 48) + "…"
		model.noticeError = false
		return model, loadTUIMailDeliveryCmd(model.ctx, model.client, model.snapshot.Selected, account.Email)
	}
	return model, nil
}

func mailTUIItemCount(state tuiMailState) int {
	return len(state.Queue) + len(state.Domains) + len(state.Accounts)
}

func (model *tuiModel) openMailQueueActions(message mailsvc.QueueMessage) {
	if strings.TrimSpace(message.ID) == "" {
		model.notice = "Queued message has no stable identity; controls are unavailable"
		model.noticeError = true
		return
	}
	mutation := &tuiMailMutation{Target: model.snapshot.Selected, Queue: message}
	model.dialog = tuiDialog{
		Mode:  tuiDialogChoices,
		Title: "Manage queued mail · " + truncateTUI(redactAPISecrets(message.ID), 42),
		Body: []string{
			"Sender: " + tuiMailDisplayValue(message.Sender),
			fmt.Sprintf("Recipients: %d · retries: %d", len(message.Recipients), message.Retries),
			"Choose an action. A separate explicit confirmation follows.",
		},
		Options: []tuiDialogOption{
			{Label: "Retry delivery now", Action: "retry"},
			{Label: "Delete queued message", Action: "delete", Dangerous: true},
		},
		MailMutation: mutation,
	}
}

func (model *tuiModel) openMailQueueConfirmation(mutation tuiMailMutation) {
	action := strings.ToUpper(mutation.Action)
	label := action + " queued mail message " + truncateTUI(redactAPISecrets(mutation.Queue.ID), 64)
	body := []string{
		label,
		"Sender: " + tuiMailDisplayValue(mutation.Queue.Sender),
		fmt.Sprintf("Recipients: %d · retries: %d", len(mutation.Queue.Recipients), mutation.Queue.Retries),
		"The exact queued-message identity is re-observed before the panel mutation.",
		"Target: " + tuiMailDisplayValue(mutation.Target.label()),
	}
	model.dialog = tuiDialog{
		Mode: tuiDialogConfirm, Title: "Confirm mail queue mutation", Body: body,
		Operation:    tuiOperation{Label: label, Dangerous: mutation.Action == "delete"},
		MailMutation: &mutation,
	}
}

func runTUIMailMutationCmd(ctx context.Context, client *apiClient, mutation tuiMailMutation) tea.Cmd {
	return func() tea.Msg {
		message, err := runTUIMailMutation(ctx, client, mutation)
		return tuiOperationMsg{Message: message, Err: err}
	}
}

func runTUIMailMutation(ctx context.Context, client *apiClient, mutation tuiMailMutation) (string, error) {
	if !mutation.Target.Local {
		return "", errors.New("mail queue controls require the panel host")
	}
	if mutation.Action != "retry" && mutation.Action != "delete" {
		return "", fmt.Errorf("unsupported mail queue action %q", mutation.Action)
	}
	id, err := normalizeMailPathValue("mail queue message ID", mutation.Queue.ID)
	if err != nil {
		return "", err
	}
	queue, err := requestJSON[[]mailsvc.QueueMessage](ctx, client.withTimeout(45*time.Second), http.MethodGet,
		fmt.Sprintf("/api/mail/queue?limit=%d", maxMailQueueLimit), nil, true)
	if err != nil {
		return "", err
	}
	var fresh *mailsvc.QueueMessage
	for index := range queue {
		if queue[index].ID == id {
			fresh = &queue[index]
			break
		}
	}
	if fresh == nil || !sameTUIMailQueueMessage(mutation.Queue, *fresh) {
		return "", errors.New("mail queue message changed or is no longer queued; refresh before mutation")
	}

	endpoint := "/api/mail/queue/" + url.PathEscape(id)
	method := http.MethodDelete
	expected := "deleted"
	verb := "Deleted"
	if mutation.Action == "retry" {
		endpoint += "/retry"
		method = http.MethodPost
		expected = "retrying"
		verb = "Retried"
	}
	receipt, err := requestJSON[mailMutationResponse](ctx, client.withTimeout(45*time.Second), method, endpoint, nil, true)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(receipt.Status) == "" {
		return "", errors.New("panel returned an invalid mail queue mutation receipt")
	}
	if receipt.Status != expected && receipt.Status != "ok" {
		return "", fmt.Errorf("panel returned an invalid mail queue mutation receipt")
	}
	return verb + " queued mail message " + truncateTUI(redactAPISecrets(fresh.ID), 64), nil
}

func sameTUIMailQueueMessage(left, right mailsvc.QueueMessage) bool {
	return left.ID == right.ID && left.Sender == right.Sender && slices.Equal(left.Recipients, right.Recipients) && left.CreatedAt.Equal(right.CreatedAt) && left.NextRetry.Equal(right.NextRetry) && left.Retries == right.Retries
}

func mailIntegrationStateStyle(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

func renderMailIntegrationState(status string) string {
	normalized := mailIntegrationStateStyle(status)
	color := tuiMuted
	label := strings.ToUpper(valueOrNA(normalized))
	switch normalized {
	case "healthy":
		color = tuiGreen
	case "not_configured":
		color = tuiAmber
	case "unavailable":
		color = tuiRed
	case "stopped":
		color = tuiMuted
	case "unsupported":
		color = tuiAmber
	}
	return lipgloss.NewStyle().Bold(true).Foreground(color).Render(truncateTUI(label, 15))
}

func renderMailServiceState(status mailsvc.ServiceStatus) string {
	normalized := strings.ToLower(strings.TrimSpace(status.Status))
	if normalized == "not_configured" || normalized == "unavailable" {
		return renderMailIntegrationState(normalized)
	}
	return renderState(status.Status)
}

func tuiMailDisplayValue(value string) string {
	return valueOrNA(redactAPISecrets(value))
}

func (model tuiModel) renderMail(width, height int) string {
	rows := []string{tuiTitleStyle.Render("Mail") + tuiMutedStyle.Render("  M jump · Enter queue/domain/account · R reload")}
	if !model.mailLoaded {
		message := "Mail service and inventory have not been loaded."
		if model.resourceLoading {
			message = "Loading mail service, recent logs, queue, domains, and accounts…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	if !model.mail.Local {
		rows = append(rows,
			lipgloss.NewStyle().Foreground(tuiAmber).Render("! "+truncateTUI(valueOrNA(model.mail.Message), width-4)),
			tuiDimStyle.Render("Managed-node mail APIs are not advertised; no remote mail request was sent."),
		)
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}

	status := model.mail.ServiceStatus
	integration := renderMailIntegrationState(model.mail.Status)
	service := renderMailServiceState(status)
	serviceLabel := tuiMailDisplayValue(status.Status)
	serviceDetail := ""
	if status.PID != "" {
		serviceDetail += " · PID " + truncateTUI(redactAPISecrets(status.PID), 16)
	}
	if status.Uptime != "" {
		serviceDetail += " · since " + truncateTUI(redactAPISecrets(status.Uptime), 28)
	}
	// Keep the colored state visible without allowing ANSI output to affect the
	// width calculation on narrow terminals.
	plainStatus := "Integration " + strings.ToUpper(valueOrNA(model.mail.Status)) + " · service " + serviceLabel + serviceDetail
	if lipgloss.Width(plainStatus) > width-4 {
		rows = append(rows, tuiDimStyle.Render(truncateTUI(plainStatus, width-4)))
	} else {
		rows = append(rows, tuiDimStyle.Render("Integration ")+integration+tuiDimStyle.Render(" · service ")+service+tuiDimStyle.Render(serviceDetail))
	}

	if model.mail.OverviewAvailable {
		version := model.mail.Overview.Version.Version
		if version == "" {
			version = model.mail.Overview.Version.Raw
		}
		storage := model.mail.Overview.Storage.Backend
		if model.mail.Overview.Storage.SizeBytes > 0 {
			storage += " · " + formatTUIBytes(uint64(model.mail.Overview.Storage.SizeBytes))
		}
		listenerText := fmt.Sprintf("%d listener(s)", len(model.mail.Overview.Listeners))
		rows = append(rows, tuiDimStyle.Render(truncateTUI(redactAPISecrets(fmt.Sprintf("Version %s · %s · %s", tuiMailDisplayValue(version), listenerText, tuiMailDisplayValue(storage))), width-4)))
		rows = append(rows, renderMailSources(model.mail.Overview.Sources, width-4))
	} else {
		rows = append(rows, tuiDimStyle.Render("Service overview unavailable; individual inventory sections may still be readable."))
	}

	logSummary := "logs unavailable"
	if model.mail.LogsAvailable {
		logSummary = fmt.Sprintf("%d recent log(s)", len(model.mail.Logs))
	}
	queueSummary := mailAvailabilitySummary("queue", len(model.mail.Queue), model.mail.QueueAvailable)
	domainSummary := mailAvailabilitySummary("domains", len(model.mail.Domains), model.mail.DomainsAvailable)
	accountSummary := mailAvailabilitySummary("accounts", len(model.mail.Accounts), model.mail.AccountsAvailable)
	rows = append(rows, tuiDimStyle.Render(truncateTUI("Observability · "+logSummary+" · "+queueSummary+" · "+domainSummary+" · "+accountSummary, width-4)))

	if model.mail.LogsAvailable {
		rows = append(rows, tuiMutedStyle.Render("Recent mail logs · latest bounded entries"))
		preview := minInt(len(model.mail.Logs), maxInt(2, minInt(5, height/5)))
		for index := len(model.mail.Logs) - preview; index < len(model.mail.Logs); index++ {
			if index < 0 {
				continue
			}
			entry := model.mail.Logs[index]
			line := fmt.Sprintf("%s  %-5s  %s", valueOrNA(entry.Timestamp), truncateTUI(strings.ToUpper(valueOrNA(entry.Level)), 5), valueOrNA(entry.Message))
			rows = append(rows, tuiDimStyle.Render(truncateTUI(line, width-4)))
		}
	}

	items := mailTUIItemCount(model.mail)
	rows = append(rows, tuiMutedStyle.Render("Inventory · Enter queue actions, domain details, or account delivery history"))
	visible := maxInt(3, height-len(rows)-2)
	start, end := visibleRange(model.cursor, items, visible)
	for index := start; index < end; index++ {
		row := renderMailItemRow(model.mail, index, width-3)
		rows = append(rows, renderSelectableRow(row, index == model.cursor, width-2))
	}
	if items == 0 {
		if model.mail.QueueAvailable && model.mail.DomainsAvailable && model.mail.AccountsAvailable {
			rows = append(rows, tuiDimStyle.Render("Mail queue, domain, and account inventories are empty."))
		} else {
			rows = append(rows, tuiDimStyle.Render("No readable mail inventory rows are available."))
		}
	}
	for _, warning := range model.mail.Warnings {
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! "+truncateTUI(warning, width-4)))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func renderMailSources(sources map[string]mailSource, width int) string {
	parts := make([]string, 0, 4)
	for _, key := range []string{"status", "version", "listeners", "storage"} {
		source, ok := sources[key]
		if !ok {
			continue
		}
		state := strings.TrimSpace(source.State)
		if state == "" {
			if source.Available {
				state = "healthy"
			} else {
				state = "unavailable"
			}
		}
		parts = append(parts, key+"="+state)
	}
	if len(parts) == 0 {
		return tuiDimStyle.Render("Mail overview sources were not reported.")
	}
	return tuiDimStyle.Render(truncateTUI(redactAPISecrets("Sources · "+strings.Join(parts, " · ")), width))
}

func mailAvailabilitySummary(label string, count int, available bool) string {
	if !available {
		return label + " unavailable"
	}
	return fmt.Sprintf("%s %d", label, count)
}

func renderMailItemRow(state tuiMailState, index, width int) string {
	if index < len(state.Queue) {
		item := state.Queue[index]
		created := "—"
		if !item.CreatedAt.IsZero() {
			created = item.CreatedAt.UTC().Format("01-02 15:04")
		}
		row := fmt.Sprintf("QUEUE  %-22s  %-24s  %d recipient(s) · retry %d · %s", truncateTUI(item.ID, 22), truncateTUI(item.Sender, 24), len(item.Recipients), item.Retries, created)
		return truncateTUI(redactAPISecrets(row), width)
	}
	index -= len(state.Queue)
	if index < len(state.Domains) {
		item := state.Domains[index]
		return truncateTUI(redactAPISecrets(fmt.Sprintf("DOMAIN %-28s  %s", item.Name, tuiMailDisplayValue(item.Description))), width)
	}
	index -= len(state.Domains)
	if index < len(state.Accounts) {
		item := state.Accounts[index]
		stateLabel := "disabled"
		if item.IsEnabled {
			stateLabel = "enabled"
		}
		used := formatTUIBytes(uint64(maxInt64(item.UsedStorage, 0)))
		quota := "no quota"
		if item.Quota > 0 {
			quota = formatTUIBytes(uint64(item.Quota))
		}
		return truncateTUI(redactAPISecrets(fmt.Sprintf("ACCOUNT %-30s  %-10s · %s/%s", item.Email, stateLabel, used, quota)), width)
	}
	return ""
}
