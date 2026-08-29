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

	cfservice "github.com/IamYGT/heyserver/internal/services/cloudflare"
)

const (
	tuiCloudflareResourceZone   = "zone"
	tuiCloudflareResourceRecord = "record"
)

type tuiCloudflareDetail struct {
	Zone              cfservice.CFZone
	Records           []cliCloudflareRecord
	EmailRouting      cfservice.CFEmailRouting
	EmailAvailable    bool
	EmailRoutingError string
}

type tuiCloudflareState struct {
	Local     bool
	Supported bool
	Status    string
	Zones     []cfservice.CFZone
	Detail    *tuiCloudflareDetail
	Message   string
}

type tuiCloudflareMsg struct {
	TargetID string
	State    tuiCloudflareState
	Err      error
}

type tuiCloudflareDetailMsg struct {
	TargetID string
	Detail   tuiCloudflareDetail
	Err      error
}

func loadTUICloudflareCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		state, err := loadTUICloudflare(ctx, client, target)
		return tuiCloudflareMsg{TargetID: target.ID, State: state, Err: err}
	}
}

func loadTUICloudflare(ctx context.Context, client *apiClient, target tuiTarget) (tuiCloudflareState, error) {
	state := tuiCloudflareState{Local: target.Local}
	if !target.Local {
		state.Message = "Cloudflare is a central panel integration; select Local."
		return state, nil
	}
	zones, err := requestJSON[[]cfservice.CFZone](ctx, client.withTimeout(30*time.Second), http.MethodGet, "/api/cloudflare/zones", nil, true)
	if err != nil {
		state.Status = "unavailable"
		state.Message = "Cloudflare integration is unavailable: " + err.Error()
		if strings.Contains(strings.ToLower(err.Error()), "not configured") {
			state.Status = "not_configured"
			state.Message = "Cloudflare API token is not configured on this panel."
		}
		return state, nil
	}
	state.Supported = true
	state.Status = "healthy"
	state.Zones = zones
	return state, nil
}

func loadTUICloudflareDetailCmd(ctx context.Context, client *apiClient, target tuiTarget, zone cfservice.CFZone) tea.Cmd {
	return func() tea.Msg {
		detail, err := loadTUICloudflareDetail(ctx, client, target, zone)
		return tuiCloudflareDetailMsg{TargetID: target.ID, Detail: detail, Err: err}
	}
}

func loadTUICloudflareDetail(ctx context.Context, client *apiClient, target tuiTarget, observed cfservice.CFZone) (tuiCloudflareDetail, error) {
	if !target.Local {
		return tuiCloudflareDetail{}, errors.New("Cloudflare detail requires the panel host")
	}
	zoneID, err := validateCloudflareIdentifier("zone ID", observed.ID)
	if err != nil {
		return tuiCloudflareDetail{}, err
	}
	zone, err := requestJSON[cfservice.CFZone](ctx, client.withTimeout(30*time.Second), http.MethodGet, cloudflareZonePath(zoneID), nil, true)
	if err != nil {
		return tuiCloudflareDetail{}, err
	}
	if zone.ID != observed.ID {
		return tuiCloudflareDetail{}, errors.New("Cloudflare returned a mismatched zone identity")
	}
	records, err := requestJSON[[]cliCloudflareRecord](ctx, client.withTimeout(30*time.Second), http.MethodGet, cloudflareZonePath(zoneID)+"/records", nil, true)
	if err != nil {
		return tuiCloudflareDetail{}, err
	}
	detail := tuiCloudflareDetail{Zone: zone, Records: records}
	routing, err := requestJSON[cfservice.CFEmailRouting](ctx, client.withTimeout(30*time.Second), http.MethodGet, cloudflareZonePath(zoneID)+"/email-routing", nil, true)
	if err != nil {
		detail.EmailRoutingError = err.Error()
	} else {
		detail.EmailRouting = routing
		detail.EmailAvailable = true
	}
	return detail, nil
}

func cloudflareItemCount(state tuiCloudflareState) int {
	if state.Detail != nil {
		return 1 + len(state.Detail.Records)
	}
	return len(state.Zones)
}

func (model tuiModel) loadCloudflare() (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading Cloudflare zones…"
	model.noticeError = false
	return model, loadTUICloudflareCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model tuiModel) reloadCloudflare() (tea.Model, tea.Cmd) {
	if model.cloudflare.Detail == nil {
		return model.loadCloudflare()
	}
	if model.resourceLoading {
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Reloading Cloudflare zone and DNS records…"
	model.noticeError = false
	return model, loadTUICloudflareDetailCmd(model.ctx, model.client, model.snapshot.Selected, model.cloudflare.Detail.Zone)
}

func (model tuiModel) activateCloudflareItem() (tea.Model, tea.Cmd) {
	if !model.cloudflareLoaded || model.cloudflareTarget != model.selectedTargetID {
		return model.loadCloudflare()
	}
	state := model.cloudflare
	if !state.Local || !state.Supported {
		return model, nil
	}
	if state.Detail == nil {
		if model.cursor < 0 || model.cursor >= len(state.Zones) {
			return model, nil
		}
		model.resourceLoading = true
		model.notice = "Loading Cloudflare zone records and email routing…"
		model.noticeError = false
		return model, loadTUICloudflareDetailCmd(model.ctx, model.client, model.snapshot.Selected, state.Zones[model.cursor])
	}
	if model.cursor == 0 {
		model.openCloudflareZoneActions(state.Detail.Zone)
		return model, nil
	}
	recordIndex := model.cursor - 1
	if recordIndex < 0 || recordIndex >= len(state.Detail.Records) {
		return model, nil
	}
	model.openCloudflareRecordActions(state.Detail.Zone, state.Detail.Records[recordIndex])
	return model, nil
}

func (model tuiModel) closeCloudflareDetail() (tea.Model, tea.Cmd) {
	if model.tab != tuiTabCloudflare || model.cloudflare.Detail == nil {
		return model, nil
	}
	model.cloudflare.Detail = nil
	model.cursor = 0
	model.notice = "Returned to Cloudflare zones"
	model.noticeError = false
	return model, nil
}

func (model *tuiModel) openCloudflareZoneActions(zone cfservice.CFZone) {
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Manage Cloudflare zone · " + truncateTUI(zone.Name, 38),
		Body: []string{"Zone ID: " + zone.ID, "Choose a bounded provider action. A separate confirmation follows."},
		Options: []tuiDialogOption{
			{Label: "Purge complete zone cache", Action: "purge", Dangerous: true},
			{Label: "Reconcile installation-owned mail DNS", Action: "mail-autofix", Dangerous: true},
		},
		Operation: tuiOperation{
			Kind: tuiOperationCloudflare, Target: model.snapshot.Selected, CloudflareResource: tuiCloudflareResourceZone,
			CloudflareZone: zone, Label: zone.Name,
		},
	}
}

func (model *tuiModel) openCloudflareRecordActions(zone cfservice.CFZone, record cliCloudflareRecord) {
	options := []tuiDialogOption{{Label: "View record details", Action: "view-record"}}
	if cloudflareRecordSupportsProxy(record) {
		label := "Enable Cloudflare proxy"
		if record.Proxied {
			label = "Disable Cloudflare proxy"
		}
		options = append(options, tuiDialogOption{Label: label, Action: "toggle-proxy", Dangerous: record.Proxied})
	}
	options = append(options, tuiDialogOption{Label: "Delete DNS record", Action: "delete", Dangerous: true})
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Manage Cloudflare record · " + truncateTUI(record.Name, 38),
		Body:    []string{fmt.Sprintf("%s · TTL %d · proxied=%t", record.Type, record.TTL, record.Proxied), "Choose a bounded action. A separate confirmation follows."},
		Options: options,
		Operation: tuiOperation{
			Kind: tuiOperationCloudflare, Target: model.snapshot.Selected, CloudflareResource: tuiCloudflareResourceRecord,
			CloudflareZone: zone, CloudflareRecord: record, Label: record.Name,
		},
	}
}

func cloudflareRecordSupportsProxy(record cliCloudflareRecord) bool {
	return record.Type == "A" || record.Type == "AAAA" || record.Type == "CNAME"
}

func cloudflareRecordLines(zone cfservice.CFZone, record cliCloudflareRecord) []string {
	return []string{
		"Zone: " + zone.Name,
		"Record ID: " + record.ID,
		"Type: " + record.Type,
		"Name: " + record.Name,
		"Content: " + record.Content,
		fmt.Sprintf("TTL: %d", record.TTL),
		fmt.Sprintf("Priority: %d", record.Priority),
		fmt.Sprintf("Proxied: %t", record.Proxied),
	}
}

func runTUICloudflareOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	if !operation.Target.Local {
		return "", errors.New("Cloudflare control requires the panel host")
	}
	switch operation.CloudflareResource {
	case tuiCloudflareResourceZone:
		return runTUICloudflareZoneOperation(ctx, client, operation)
	case tuiCloudflareResourceRecord:
		return runTUICloudflareRecordOperation(ctx, client, operation)
	default:
		return "", errors.New("unsupported Cloudflare resource")
	}
}

func runTUICloudflareZoneOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	if operation.Action != "purge" && operation.Action != "mail-autofix" {
		return "", fmt.Errorf("unsupported Cloudflare zone action %q", operation.Action)
	}
	zoneID, err := validateCloudflareIdentifier("zone ID", operation.CloudflareZone.ID)
	if err != nil {
		return "", err
	}
	fresh, err := requestJSON[cfservice.CFZone](ctx, client.withTimeout(30*time.Second), http.MethodGet, cloudflareZonePath(zoneID), nil, true)
	if err != nil {
		return "", err
	}
	if !sameTUICloudflareZone(operation.CloudflareZone, fresh) {
		return "", errors.New("Cloudflare zone changed; refresh before mutation")
	}
	if operation.Action == "purge" {
		receipt, err := requestJSON[struct {
			Status string `json:"status"`
		}](ctx, client.withTimeout(30*time.Second), http.MethodPost, cloudflareZonePath(zoneID)+"/purge", nil, true)
		if err != nil {
			return "", err
		}
		if receipt.Status != "purged" {
			return "", errors.New("panel returned an invalid Cloudflare cache purge receipt")
		}
		return "Purged Cloudflare cache for " + fresh.Name, nil
	}
	domain, err := validateCloudflareDomain(fresh.Name)
	if err != nil {
		return "", err
	}
	receipt, err := requestJSON[cfservice.AutoFixResult](ctx, client.withTimeout(2*time.Minute), http.MethodPost, "/api/cloudflare/mail-autofix/"+url.PathEscape(domain), nil, true)
	if err != nil {
		return "", err
	}
	if receipt.Domain != domain || receipt.ZoneID != fresh.ID {
		return "", errors.New("panel returned an invalid Cloudflare mail DNS receipt")
	}
	return fmt.Sprintf("Reconciled %d mail DNS record(s) for %s", len(receipt.Changes), domain), nil
}

func runTUICloudflareRecordOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	if operation.Action != "toggle-proxy" && operation.Action != "delete" {
		return "", fmt.Errorf("unsupported Cloudflare record action %q", operation.Action)
	}
	zoneID, err := validateCloudflareIdentifier("zone ID", operation.CloudflareZone.ID)
	if err != nil {
		return "", err
	}
	recordID, err := validateCloudflareIdentifier("record ID", operation.CloudflareRecord.ID)
	if err != nil {
		return "", err
	}
	freshZone, err := requestJSON[cfservice.CFZone](ctx, client.withTimeout(30*time.Second), http.MethodGet, cloudflareZonePath(zoneID), nil, true)
	if err != nil {
		return "", err
	}
	if !sameTUICloudflareZone(operation.CloudflareZone, freshZone) {
		return "", errors.New("Cloudflare zone changed; refresh before mutation")
	}
	records, err := requestJSON[[]cliCloudflareRecord](ctx, client.withTimeout(30*time.Second), http.MethodGet, cloudflareZonePath(zoneID)+"/records", nil, true)
	if err != nil {
		return "", err
	}
	fresh, ok := findTUICloudflareRecord(records, recordID)
	if !ok || fresh != operation.CloudflareRecord {
		return "", errors.New("Cloudflare DNS record changed; refresh before mutation")
	}
	endpoint := cloudflareRecordPath(zoneID, recordID)
	if operation.Action == "delete" {
		body, err := client.withTimeout(30*time.Second).request(ctx, http.MethodDelete, endpoint, nil, true)
		if err != nil {
			return "", err
		}
		if len(strings.TrimSpace(string(body))) != 0 {
			return "", errors.New("panel returned an invalid Cloudflare deletion receipt")
		}
		return "Deleted Cloudflare DNS record " + fresh.Name, nil
	}
	if !cloudflareRecordSupportsProxy(fresh) {
		return "", errors.New("Cloudflare proxy can be changed only for A, AAAA, or CNAME records")
	}
	desired := !fresh.Proxied
	updated, err := requestJSON[cliCloudflareRecord](ctx, client.withTimeout(30*time.Second), http.MethodPut, endpoint+"/proxy", map[string]bool{"proxied": desired}, true)
	if err != nil {
		return "", err
	}
	expected := fresh
	expected.Proxied = desired
	if updated != expected {
		return "", errors.New("panel returned an invalid Cloudflare proxy receipt")
	}
	state := "DNS-only"
	if desired {
		state = "proxied"
	}
	return fmt.Sprintf("Cloudflare record %s is now %s", fresh.Name, state), nil
}

func sameTUICloudflareZone(left, right cfservice.CFZone) bool {
	return left.ID == right.ID && left.Name == right.Name && left.Status == right.Status && left.Plan == right.Plan && slices.Equal(left.NS, right.NS)
}

func findTUICloudflareRecord(records []cliCloudflareRecord, id string) (cliCloudflareRecord, bool) {
	for _, record := range records {
		if record.ID == id {
			return record, true
		}
	}
	return cliCloudflareRecord{}, false
}

func (model tuiModel) renderCloudflare(width, height int) string {
	rows := []string{tuiTitleStyle.Render("Cloudflare") + tuiMutedStyle.Render("  O jump · Enter inspect/action · Backspace zones · R reload")}
	if !model.cloudflareLoaded {
		message := "Cloudflare inventory has not been loaded."
		if model.resourceLoading {
			message = "Loading Cloudflare provider state…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	state := model.cloudflare
	if !state.Local || !state.Supported {
		color := tuiAmber
		if state.Status == "unavailable" {
			color = tuiRed
		}
		rows = append(rows, lipgloss.NewStyle().Bold(true).Foreground(color).Render(strings.ToUpper(valueOrNA(state.Status))))
		rows = append(rows, tuiDimStyle.Render(valueOrNA(state.Message)))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	if state.Detail == nil {
		rows = append(rows, tuiDimStyle.Render(fmt.Sprintf("HEALTHY · %d accessible zone(s) · token remains on the panel", len(state.Zones))))
		visible := maxInt(4, height-6)
		start, end := visibleRange(model.cursor, len(state.Zones), visible)
		for index := start; index < end; index++ {
			zone := state.Zones[index]
			row := fmt.Sprintf("ZONE %-28s %-10s %-18s nameservers=%d", truncateTUI(zone.Name, 28), zone.Status, truncateTUI(zone.Plan.Name, 18), len(zone.NS))
			rows = append(rows, renderSelectableRow(truncateTUI(row, width-3), index == model.cursor, width-2))
		}
		if len(state.Zones) == 0 {
			rows = append(rows, tuiDimStyle.Render("Cloudflare is healthy but the token exposes no zones."))
		}
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	detail := state.Detail
	email := "email routing unavailable"
	if detail.EmailAvailable {
		email = fmt.Sprintf("email routing %s · enabled=%t", valueOrNA(detail.EmailRouting.Status), detail.EmailRouting.Enabled)
	}
	rows = append(rows, tuiDimStyle.Render(fmt.Sprintf("%s · %s · %d DNS record(s) · %s", detail.Zone.Name, detail.Zone.Status, len(detail.Records), email)))
	if detail.EmailRoutingError != "" {
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! "+truncateTUI(detail.EmailRoutingError, width-6)))
	}
	visible := maxInt(4, height-7)
	total := 1 + len(detail.Records)
	start, end := visibleRange(model.cursor, total, visible)
	for index := start; index < end; index++ {
		row := "ZONE ACTIONS  purge cache · reconcile mail DNS"
		if index > 0 {
			record := detail.Records[index-1]
			proxy := "DNS-only"
			if record.Proxied {
				proxy = "proxied"
			}
			row = fmt.Sprintf("%-6s %-28s %-28s TTL=%-6d %s", record.Type, truncateTUI(record.Name, 28), truncateTUI(record.Content, 28), record.TTL, proxy)
		}
		rows = append(rows, renderSelectableRow(truncateTUI(row, width-3), index == model.cursor, width-2))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}
