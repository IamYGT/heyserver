package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/IamYGT/heyserver/internal/models"
)

const tuiAlertHistoryLimit = 100

const (
	tuiAlertResourceChannel = "channel"
	tuiAlertResourceRule    = "rule"
)

type tuiAlertHistoryPage struct {
	Items  []models.AlertHistory `json:"items"`
	Total  int                   `json:"total"`
	Limit  int                   `json:"limit"`
	Offset int                   `json:"offset"`
}

type tuiAlertsState struct {
	Local             bool
	Supported         bool
	Status            string
	Channels          []cliNotificationChannel
	Rules             []models.AlertRule
	History           []models.AlertHistory
	HistoryTotal      int
	ChannelsAvailable bool
	RulesAvailable    bool
	HistoryAvailable  bool
	Message           string
	Warnings          []string
}

type tuiAlertsMsg struct {
	TargetID string
	State    tuiAlertsState
	Err      error
}

func loadTUIAlertsCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		state, err := loadTUIAlerts(ctx, client, target)
		return tuiAlertsMsg{TargetID: target.ID, State: state, Err: err}
	}
}

func loadTUIAlerts(ctx context.Context, client *apiClient, target tuiTarget) (tuiAlertsState, error) {
	state := tuiAlertsState{Local: target.Local}
	if !target.Local {
		state.Message = "Alert channels, rules, and history are central panel resources; select Local."
		return state, nil
	}

	channels, channelErr := requestJSON[[]cliNotificationChannel](ctx, client, http.MethodGet, "/api/notify/channels", nil, true)
	if channelErr != nil {
		state.Warnings = append(state.Warnings, "Notification channels unavailable: "+channelErr.Error())
	} else {
		state.Channels = channels
		state.ChannelsAvailable = true
	}

	rules, ruleErr := requestJSON[[]models.AlertRule](ctx, client, http.MethodGet, "/api/notify/rules", nil, true)
	if ruleErr != nil {
		state.Warnings = append(state.Warnings, "Alert rules unavailable: "+ruleErr.Error())
	} else {
		state.Rules = rules
		state.RulesAvailable = true
	}

	history, historyErr := requestJSON[tuiAlertHistoryPage](ctx, client, http.MethodGet,
		"/api/notify/history?limit="+strconv.Itoa(tuiAlertHistoryLimit)+"&offset=0", nil, true)
	if historyErr != nil {
		state.Warnings = append(state.Warnings, "Alert history unavailable: "+historyErr.Error())
	} else {
		state.History = history.Items
		state.HistoryTotal = history.Total
		state.HistoryAvailable = true
	}

	state.Supported = state.ChannelsAvailable || state.RulesAvailable || state.HistoryAvailable
	if !state.Supported {
		state.Status = "unavailable"
		state.Message = "Notification and alert control is unavailable."
		return state, nil
	}
	state.Status = alertIntegrationStatus(state)
	return state, nil
}

func alertIntegrationStatus(state tuiAlertsState) string {
	if !state.ChannelsAvailable {
		return "unavailable"
	}
	if len(state.Channels) == 0 {
		return "not_configured"
	}
	for _, channel := range state.Channels {
		if channel.Enabled {
			if state.RulesAvailable && state.HistoryAvailable {
				return "healthy"
			}
			return "degraded"
		}
	}
	return "configured_disabled"
}

func alertsItemCount(state tuiAlertsState) int {
	return len(state.Channels) + len(state.Rules) + len(state.History)
}

func (model tuiModel) loadAlerts() (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading notification channels, alert rules, and history…"
	model.noticeError = false
	return model, loadTUIAlertsCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model tuiModel) activateAlertItem() (tea.Model, tea.Cmd) {
	if !model.alertsLoaded || model.alertsTarget != model.selectedTargetID {
		return model.loadAlerts()
	}
	state := model.alerts
	if !state.Local || !state.Supported {
		return model, nil
	}
	if model.cursor < len(state.Channels) {
		model.openAlertChannelActions(state.Channels[model.cursor])
		return model, nil
	}
	ruleIndex := model.cursor - len(state.Channels)
	if ruleIndex >= 0 && ruleIndex < len(state.Rules) {
		model.openAlertRuleActions(state.Rules[ruleIndex])
		return model, nil
	}
	historyIndex := ruleIndex - len(state.Rules)
	if historyIndex < 0 || historyIndex >= len(state.History) {
		return model, nil
	}
	event := state.History[historyIndex]
	lines := []string{
		"Rule: " + valueOrNA(event.RuleName),
		"Type: " + valueOrNA(event.Type),
		fmt.Sprintf("Value: %g", event.Value),
		"Fired: " + formatTUIAlertTime(event.FiredAt),
		"Message: " + valueOrNA(event.Message),
	}
	model.dialog = tuiDialog{
		Mode: tuiDialogLogs, Title: "Alert event · " + truncateTUI(event.RuleName, 48),
		LogLines: lines, LogScroll: maxInt(0, len(lines)-1),
		LogReloadNotice: "Refresh the Alerts section to reload alert history",
	}
	return model, nil
}

func (model *tuiModel) openAlertChannelActions(channel cliNotificationChannel) {
	statusAction := "enable"
	statusLabel := "Enable channel"
	if channel.Enabled {
		statusAction = "disable"
		statusLabel = "Disable channel"
	}
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Manage notification channel · " + truncateTUI(channel.Name, 38),
		Body: []string{"Type: " + valueOrNA(channel.Type), "Choose a bounded action. A separate confirmation follows."},
		Options: []tuiDialogOption{
			{Label: "Send test notification", Action: "test"},
			{Label: statusLabel, Action: statusAction, Dangerous: statusAction == "disable"},
			{Label: "Delete channel", Action: "delete", Dangerous: true},
		},
		Operation: tuiOperation{
			Kind: tuiOperationAlert, Target: model.snapshot.Selected, AlertResource: tuiAlertResourceChannel,
			AlertChannel: channel, Label: channel.Name,
		},
	}
}

func (model *tuiModel) openAlertRuleActions(rule models.AlertRule) {
	statusAction := "enable"
	statusLabel := "Enable alert rule"
	if rule.Enabled {
		statusAction = "disable"
		statusLabel = "Disable alert rule"
	}
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Manage alert rule · " + truncateTUI(rule.Name, 44),
		Body: []string{alertRuleSummary(rule), "Choose a bounded action. A separate confirmation follows."},
		Options: []tuiDialogOption{
			{Label: statusLabel, Action: statusAction, Dangerous: statusAction == "disable"},
			{Label: "Delete alert rule", Action: "delete", Dangerous: true},
		},
		Operation: tuiOperation{
			Kind: tuiOperationAlert, Target: model.snapshot.Selected, AlertResource: tuiAlertResourceRule,
			AlertRule: rule, Label: rule.Name,
		},
	}
}

func alertRuleSummary(rule models.AlertRule) string {
	target := ""
	if strings.TrimSpace(rule.Target) != "" {
		target = " · target=" + rule.Target
	}
	return fmt.Sprintf("%s · threshold=%g · duration=%dm · cooldown=%dm%s", valueOrNA(rule.Type), rule.Threshold, rule.DurationMins, rule.CooldownMins, target)
}

func runTUIAlertOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	if !operation.Target.Local {
		return "", errors.New("notification and alert control requires the panel host")
	}
	switch operation.AlertResource {
	case tuiAlertResourceChannel:
		return runTUIAlertChannelOperation(ctx, client, operation)
	case tuiAlertResourceRule:
		return runTUIAlertRuleOperation(ctx, client, operation)
	default:
		return "", errors.New("unsupported alert resource")
	}
}

func runTUIAlertChannelOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	if operation.Action != "test" && operation.Action != "enable" && operation.Action != "disable" && operation.Action != "delete" {
		return "", fmt.Errorf("unsupported notification channel action %q", operation.Action)
	}
	if operation.AlertChannel.ID <= 0 {
		return "", errors.New("notification channel does not have a stable identity")
	}
	endpoint := notifyChannelPath(operation.AlertChannel.ID)
	fresh, err := requestJSON[cliNotificationChannel](ctx, client, http.MethodGet, endpoint, nil, true)
	if err != nil {
		return "", err
	}
	if fresh != operation.AlertChannel {
		return "", errors.New("notification channel changed; refresh before mutation")
	}

	switch operation.Action {
	case "test":
		receipt, err := requestJSON[struct {
			Status string `json:"status"`
		}](ctx, client.withTimeout(30*time.Second), http.MethodPost, endpoint+"/test", nil, true)
		if err != nil {
			return "", err
		}
		if receipt.Status != "sent" {
			return "", errors.New("panel returned an invalid notification test receipt")
		}
		return "Test notification sent through " + fresh.Name, nil
	case "delete":
		receipt, err := requestJSON[struct {
			Status string `json:"status"`
		}](ctx, client, http.MethodDelete, endpoint, nil, true)
		if err != nil {
			return "", err
		}
		if receipt.Status != "deleted" {
			return "", errors.New("panel returned an invalid notification deletion receipt")
		}
		return "Deleted notification channel " + fresh.Name, nil
	default:
		enabled := operation.Action == "enable"
		updated, err := requestJSON[cliNotificationChannel](ctx, client, http.MethodPut, endpoint,
			map[string]any{"name": fresh.Name, "enabled": enabled}, true)
		if err != nil {
			return "", err
		}
		if updated.ID != fresh.ID || updated.Name != fresh.Name || updated.Type != fresh.Type || updated.Config != fresh.Config || updated.Enabled != enabled {
			return "", errors.New("panel returned an invalid notification update receipt")
		}
		return fmt.Sprintf("Notification channel %s is now %s", fresh.Name, operation.Action+"d"), nil
	}
}

func runTUIAlertRuleOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	if operation.Action != "enable" && operation.Action != "disable" && operation.Action != "delete" {
		return "", fmt.Errorf("unsupported alert rule action %q", operation.Action)
	}
	if operation.AlertRule.ID <= 0 {
		return "", errors.New("alert rule does not have a stable identity")
	}
	endpoint := notifyRulePath(operation.AlertRule.ID)
	fresh, err := requestJSON[models.AlertRule](ctx, client, http.MethodGet, endpoint, nil, true)
	if err != nil {
		return "", err
	}
	if fresh != operation.AlertRule {
		return "", errors.New("alert rule changed; refresh before mutation")
	}
	if operation.Action == "delete" {
		receipt, err := requestJSON[struct {
			Status string `json:"status"`
		}](ctx, client, http.MethodDelete, endpoint, nil, true)
		if err != nil {
			return "", err
		}
		if receipt.Status != "deleted" {
			return "", errors.New("panel returned an invalid alert rule deletion receipt")
		}
		return "Deleted alert rule " + fresh.Name, nil
	}
	enabled := operation.Action == "enable"
	updated, err := requestJSON[models.AlertRule](ctx, client, http.MethodPut, endpoint, map[string]any{"enabled": enabled}, true)
	if err != nil {
		return "", err
	}
	expected := fresh
	expected.Enabled = enabled
	if updated.UpdatedAt.Before(fresh.UpdatedAt) {
		return "", errors.New("panel returned an invalid alert rule update timestamp")
	}
	expected.UpdatedAt = updated.UpdatedAt
	if updated != expected {
		return "", errors.New("panel returned an invalid alert rule update receipt")
	}
	return fmt.Sprintf("Alert rule %s is now %s", fresh.Name, operation.Action+"d"), nil
}

func formatTUIAlertTime(value time.Time) string {
	if value.IsZero() {
		return "n/a"
	}
	return value.UTC().Format("2006-01-02 15:04:05 UTC")
}

func (model tuiModel) renderAlerts(width, height int) string {
	rows := []string{tuiTitleStyle.Render("Alerts") + tuiMutedStyle.Render("  L jump · Enter inspect/action · R reload")}
	if !model.alertsLoaded {
		message := "Notification and alert inventory has not been loaded."
		if model.resourceLoading {
			message = "Loading notification channels, alert rules, and history…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	state := model.alerts
	if !state.Local || !state.Supported {
		color := tuiAmber
		if state.Status == "unavailable" {
			color = tuiRed
		}
		rows = append(rows, lipgloss.NewStyle().Foreground(color).Render(valueOrNA(state.Message)))
		for _, warning := range state.Warnings {
			rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! "+truncateTUI(warning, width-6)))
		}
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	statusStyle := lipgloss.NewStyle().Bold(true).Foreground(tuiGreen)
	if state.Status != "healthy" {
		statusStyle = lipgloss.NewStyle().Bold(true).Foreground(tuiAmber)
	}
	rows = append(rows, tuiDimStyle.Render("Integration: ")+statusStyle.Render(strings.ToUpper(state.Status))+tuiDimStyle.Render(fmt.Sprintf(" · %d channel(s) · %d rule(s) · %d/%d recent event(s)", len(state.Channels), len(state.Rules), len(state.History), state.HistoryTotal)))
	visible := maxInt(4, height-6)
	total := alertsItemCount(state)
	start, end := visibleRange(model.cursor, total, visible)
	for index := start; index < end; index++ {
		row := ""
		if index < len(state.Channels) {
			channel := state.Channels[index]
			status := "disabled"
			if channel.Enabled {
				status = "enabled"
			}
			row = fmt.Sprintf("CHANNEL #%-5d %-24s %-10s %s", channel.ID, truncateTUI(channel.Name, 24), channel.Type, status)
		} else if index < len(state.Channels)+len(state.Rules) {
			rule := state.Rules[index-len(state.Channels)]
			status := "disabled"
			if rule.Enabled {
				status = "enabled"
			}
			row = fmt.Sprintf("RULE    #%-5d %-24s %-16s %-8s threshold=%g", rule.ID, truncateTUI(rule.Name, 24), rule.Type, status, rule.Threshold)
		} else {
			event := state.History[index-len(state.Channels)-len(state.Rules)]
			row = fmt.Sprintf("EVENT   %-19s %-22s %s", formatTUIAlertTime(event.FiredAt), truncateTUI(event.RuleName, 22), event.Message)
		}
		rows = append(rows, renderSelectableRow(truncateTUI(row, width-3), index == model.cursor, width-2))
	}
	if total == 0 {
		rows = append(rows, tuiDimStyle.Render("No notification channel, alert rule, or alert event is configured."))
	}
	for _, warning := range state.Warnings {
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! "+truncateTUI(warning, width-6)))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}
