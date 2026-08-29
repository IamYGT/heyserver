package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	bindsvc "github.com/IamYGT/heyserver/internal/services/bind"
)

type tuiDNSZone = bindsvc.Zone
type tuiDNSRecord = bindsvc.Record
type tuiDNSCreateRequest = bindsvc.CreateZoneRequest
type tuiDNSAddRequest = bindsvc.AddRecordRequest
type tuiDNSUpdateRequest = bindsvc.UpdateRecordRequest
type tuiDNSSOAUpdateRequest = bindsvc.UpdateSOARequest
type tuiDNSSOA = bindsvc.SOARecord

type tuiDNSFormKind string

const (
	tuiDNSFormZoneCreate   tuiDNSFormKind = "zone-create"
	tuiDNSFormRecordAdd    tuiDNSFormKind = "record-add"
	tuiDNSFormRecordUpdate tuiDNSFormKind = "record-update"
	tuiDNSFormSOAUpdate    tuiDNSFormKind = "soa-update"
)

type tuiDNSFormField struct {
	Key         string
	Label       string
	Value       string
	Placeholder string
	Maximum     int
}

type tuiDNSForm struct {
	Kind           tuiDNSFormKind
	Domain         string
	Cursor         int
	Fields         []tuiDNSFormField
	OriginalRecord tuiDNSRecord
	OriginalSOA    tuiDNSSOA
	Error          string
}

func (form tuiDNSForm) value(key string) string {
	for _, field := range form.Fields {
		if field.Key == key {
			return strings.TrimSpace(field.Value)
		}
	}
	return ""
}

type tuiDNSState struct {
	Supported bool
	Message   string
	Status    bindsvc.ServiceStatus
	Zones     []tuiDNSZone
	Detail    *bindsvc.ZoneDetail
	Check     *bindsvc.CheckResult
	Warnings  []string
}

type tuiDNSMsg struct {
	TargetID string
	State    tuiDNSState
	Err      error
}

type tuiDNSDetailMsg struct {
	TargetID string
	Detail   *bindsvc.ZoneDetail
	Err      error
}

type tuiDNSCheckMsg struct {
	TargetID string
	Result   bindsvc.CheckResult
	Err      error
}

type tuiDNSSOAMsg struct {
	TargetID string
	Domain   string
	SOA      tuiDNSSOA
	Err      error
}

func loadTUIDNSCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		state, err := loadTUIDNS(ctx, client, target)
		return tuiDNSMsg{TargetID: target.ID, State: state, Err: err}
	}
}

func loadTUIDNS(ctx context.Context, client *apiClient, target tuiTarget) (tuiDNSState, error) {
	if !target.Local {
		return tuiDNSState{
			Supported: false,
			Message:   "Local BIND management belongs to the panel host; managed nodes do not advertise a DNS capability yet.",
		}, nil
	}

	status, err := requestJSON[bindsvc.ServiceStatus](ctx, client, http.MethodGet, "/api/dns/status", nil, true)
	if err != nil {
		return tuiDNSState{}, err
	}
	state := tuiDNSState{Supported: true, Status: status}
	if !status.ConfigAvailable {
		return state, nil
	}
	zones, err := requestJSON[[]bindsvc.Zone](ctx, client, http.MethodGet, "/api/dns/zones", nil, true)
	if err != nil {
		state.Warnings = append(state.Warnings, "Zone inventory unavailable: "+err.Error())
		return state, nil
	}
	state.Zones = zones
	return state, nil
}

func loadTUIDNSDetailCmd(ctx context.Context, client *apiClient, target tuiTarget, domain string) tea.Cmd {
	return func() tea.Msg {
		normalized, err := bindsvc.NormalizeZoneDomain(domain)
		if err != nil {
			return tuiDNSDetailMsg{TargetID: target.ID, Err: err}
		}
		detail, err := requestJSON[*bindsvc.ZoneDetail](ctx, client, http.MethodGet, dnsZonePath(normalized), nil, true)
		return tuiDNSDetailMsg{TargetID: target.ID, Detail: detail, Err: err}
	}
}

func loadTUIDNSSOACmd(ctx context.Context, client *apiClient, target tuiTarget, domain string) tea.Cmd {
	return func() tea.Msg {
		normalized, err := bindsvc.NormalizeZoneDomain(domain)
		if err != nil {
			return tuiDNSSOAMsg{TargetID: target.ID, Domain: domain, Err: err}
		}
		soa, err := requestJSON[bindsvc.SOARecord](ctx, client, http.MethodGet, dnsZonePath(normalized)+"/soa", nil, true)
		return tuiDNSSOAMsg{TargetID: target.ID, Domain: normalized, SOA: soa, Err: err}
	}
}

func loadTUIDNSCheckCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		if !target.Local {
			return tuiDNSCheckMsg{TargetID: target.ID, Err: errors.New("local BIND checks run only on the panel host")}
		}
		result, err := requestJSON[bindsvc.CheckResult](ctx, client.withTimeout(2*time.Minute), http.MethodPost, "/api/dns/check", nil, true)
		return tuiDNSCheckMsg{TargetID: target.ID, Result: result, Err: err}
	}
}

func (model tuiModel) loadDNS() (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading local BIND readiness and zones…"
	model.noticeError = false
	return model, loadTUIDNSCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model tuiModel) openDNSZone() (tea.Model, tea.Cmd) {
	if !model.dnsLoaded {
		return model.loadDNS()
	}
	if model.dns.Detail != nil || model.cursor < 0 || model.cursor >= len(model.dns.Zones) {
		return model, nil
	}
	zone := model.dns.Zones[model.cursor]
	model.resourceLoading = true
	model.notice = "Loading records for " + zone.Domain + "…"
	model.noticeError = false
	return model, loadTUIDNSDetailCmd(model.ctx, model.client, model.snapshot.Selected, zone.Domain)
}

func (model tuiModel) checkDNS() (tea.Model, tea.Cmd) {
	if !model.snapshot.Selected.Local {
		model.notice, model.noticeError = "Local BIND checks run only on the panel host", true
		return model, nil
	}
	if model.resourceLoading {
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Running named-checkconf and zone checks…"
	model.noticeError = false
	return model, loadTUIDNSCheckCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model *tuiModel) openDNSCreateForm() {
	if !model.snapshot.Selected.Local {
		model.notice, model.noticeError = "Local BIND mutations run only on the panel host", true
		return
	}
	if !model.dnsLoaded || !model.dns.Status.ZoneManagementReady {
		model.notice, model.noticeError = "BIND zone management is unavailable in the current observed state", true
		return
	}
	if model.dns.Detail == nil {
		model.dialog = tuiDialog{
			Mode: tuiDialogDNSForm, Title: "Create local DNS zone",
			DNSForm: tuiDNSForm{Kind: tuiDNSFormZoneCreate, Fields: []tuiDNSFormField{
				{Key: "domain", Label: "Zone domain", Placeholder: "example.com", Maximum: 253},
				{Key: "ip", Label: "Initial IPv4", Placeholder: "192.0.2.10", Maximum: 45},
			}},
		}
		return
	}
	domain := model.dns.Detail.Domain
	model.dialog = tuiDialog{
		Mode: tuiDialogDNSForm, Title: "Add record to " + truncateTUI(domain, 42),
		DNSForm: tuiDNSForm{Kind: tuiDNSFormRecordAdd, Domain: domain, Fields: []tuiDNSFormField{
			{Key: "name", Label: "Owner", Value: "@", Placeholder: "@ or www", Maximum: 253},
			{Key: "type", Label: "Type", Value: "A", Placeholder: "A, AAAA, CNAME, MX, TXT…", Maximum: 16},
			{Key: "value", Label: "Value", Placeholder: "192.0.2.20", Maximum: 4096},
			{Key: "ttl", Label: "TTL", Value: "3600", Placeholder: "3600", Maximum: 10},
			{Key: "priority", Label: "Priority", Value: "0", Placeholder: "MX/SRV only", Maximum: 5},
		}},
	}
}

func (model tuiModel) openDNSEditForm() (tea.Model, tea.Cmd) {
	if !model.snapshot.Selected.Local {
		model.notice, model.noticeError = "Local BIND mutations run only on the panel host", true
		return model, nil
	}
	if !model.dnsLoaded || !model.dns.Status.ZoneManagementReady || model.dns.Detail == nil {
		model.notice, model.noticeError = "Open a manageable local DNS zone before editing records", true
		return model, nil
	}
	if model.cursor < 0 || model.cursor >= len(model.dns.Detail.Records) {
		return model, nil
	}
	record := model.dns.Detail.Records[model.cursor]
	if strings.EqualFold(record.Type, "SOA") {
		if model.resourceLoading {
			return model, nil
		}
		model.resourceLoading = true
		model.notice = "Loading current SOA fields for " + model.dns.Detail.Domain + "…"
		model.noticeError = false
		return model, loadTUIDNSSOACmd(model.ctx, model.client, model.snapshot.Selected, model.dns.Detail.Domain)
	}
	priority := record.Priority
	model.dialog = tuiDialog{
		Mode: tuiDialogDNSForm, Title: fmt.Sprintf("Edit %s %s", record.Type, truncateTUI(record.Name, 32)),
		Body: []string{"Zone: " + model.dns.Detail.Domain, "Owner and type remain stable; replace value, TTL, or MX/SRV priority."},
		DNSForm: tuiDNSForm{
			Kind: tuiDNSFormRecordUpdate, Domain: model.dns.Detail.Domain, OriginalRecord: record,
			Fields: []tuiDNSFormField{
				{Key: "value", Label: "New value", Value: record.Value, Maximum: 4096},
				{Key: "ttl", Label: "New TTL", Value: record.TTL, Placeholder: "preserve parsed TTL", Maximum: 10},
				{Key: "priority", Label: "Priority", Value: strconv.Itoa(priority), Placeholder: "MX/SRV only", Maximum: 5},
			},
		},
	}
	return model, nil
}

func (model *tuiModel) openDNSSOAForm(domain string, soa tuiDNSSOA) {
	model.dialog = tuiDialog{
		Mode: tuiDialogDNSForm, Title: "Edit SOA for " + truncateTUI(domain, 38),
		Body: []string{fmt.Sprintf("Current serial: %d · serial increments server-side after validation", soa.Serial)},
		DNSForm: tuiDNSForm{
			Kind: tuiDNSFormSOAUpdate, Domain: domain, OriginalSOA: soa,
			Fields: []tuiDNSFormField{
				{Key: "primary-ns", Label: "Primary NS", Value: soa.PrimaryNs, Maximum: 253},
				{Key: "hostmaster", Label: "Hostmaster", Value: soa.Hostmaster, Maximum: 253},
				{Key: "refresh", Label: "Refresh", Value: strconv.FormatUint(uint64(soa.Refresh), 10), Maximum: 10},
				{Key: "retry", Label: "Retry", Value: strconv.FormatUint(uint64(soa.Retry), 10), Maximum: 10},
				{Key: "expire", Label: "Expire", Value: strconv.FormatUint(uint64(soa.Expire), 10), Maximum: 10},
				{Key: "minimum", Label: "Minimum", Value: strconv.FormatUint(uint64(soa.Minimum), 10), Maximum: 10},
			},
		},
	}
}

func (model *tuiModel) leaveDNSZone() {
	if model.dns.Detail == nil {
		return
	}
	model.dns.Detail = nil
	model.cursor = 0
	model.notice = "Returned to local DNS zones"
	model.noticeError = false
}

func (model tuiModel) updateDNSFormKey(key string) (tea.Model, tea.Cmd) {
	form := &model.dialog.DNSForm
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
		operation, err := operationFromTUIDNSForm(*form, model.snapshot.Selected)
		if err != nil {
			form.Error = err.Error()
			return model, nil
		}
		model.openConfirmation(operation, confirmationBody(operation))
		return model, nil
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

func operationFromTUIDNSForm(form tuiDNSForm, target tuiTarget) (tuiOperation, error) {
	if !target.Local {
		return tuiOperation{}, errors.New("local BIND mutations run only on the panel host")
	}
	switch form.Kind {
	case tuiDNSFormZoneCreate:
		request, err := bindsvc.ValidateAndNormalizeCreateZone(bindsvc.CreateZoneRequest{
			Domain: form.value("domain"), IP: form.value("ip"),
		})
		if err != nil {
			return tuiOperation{}, err
		}
		return tuiOperation{
			Kind: tuiOperationDNS, Target: target, Action: "zone-create",
			DNSCreate: request, Label: "Create DNS zone " + request.Domain,
		}, nil
	case tuiDNSFormRecordAdd:
		domain, err := bindsvc.NormalizeZoneDomain(form.Domain)
		if err != nil {
			return tuiOperation{}, err
		}
		priority, err := parseTUIDNSInteger("priority", form.value("priority"), 0, 65535)
		if err != nil {
			return tuiOperation{}, err
		}
		request, err := bindsvc.ValidateAndNormalizeAddRecord(bindsvc.AddRecordRequest{
			Name: form.value("name"), Type: form.value("type"), Value: form.value("value"),
			TTL: form.value("ttl"), Priority: priority, AutoReload: true,
		})
		if err != nil {
			return tuiOperation{}, err
		}
		return tuiOperation{
			Kind: tuiOperationDNS, Target: target, Action: "record-add",
			DNSZone: tuiDNSZone{Domain: domain}, DNSAdd: request,
			Label: fmt.Sprintf("Add %s %s", request.Type, request.Name),
		}, nil
	case tuiDNSFormRecordUpdate:
		domain, err := bindsvc.NormalizeZoneDomain(form.Domain)
		if err != nil {
			return tuiOperation{}, err
		}
		priority, err := parseTUIDNSInteger("priority", form.value("priority"), 0, 65535)
		if err != nil {
			return tuiOperation{}, err
		}
		request, err := bindsvc.ValidateAndNormalizeUpdateRecord(bindsvc.UpdateRecordRequest{
			Name: form.OriginalRecord.Name, Type: form.OriginalRecord.Type,
			OldValue: form.OriginalRecord.Value, NewValue: form.value("value"),
			NewTTL: form.value("ttl"), Priority: priority, AutoReload: true,
		})
		if err != nil {
			return tuiOperation{}, err
		}
		return tuiOperation{
			Kind: tuiOperationDNS, Target: target, Action: "record-update",
			DNSZone: tuiDNSZone{Domain: domain}, DNSRecord: form.OriginalRecord, DNSUpdate: request,
			Label: fmt.Sprintf("Update %s %s", request.Type, request.Name),
		}, nil
	case tuiDNSFormSOAUpdate:
		domain, err := bindsvc.NormalizeZoneDomain(form.Domain)
		if err != nil {
			return tuiOperation{}, err
		}
		refresh, err := parseTUIDNSUint32("refresh", form.value("refresh"))
		if err != nil {
			return tuiOperation{}, err
		}
		retry, err := parseTUIDNSUint32("retry", form.value("retry"))
		if err != nil {
			return tuiOperation{}, err
		}
		expire, err := parseTUIDNSUint32("expire", form.value("expire"))
		if err != nil {
			return tuiOperation{}, err
		}
		minimum, err := parseTUIDNSUint32("minimum", form.value("minimum"))
		if err != nil {
			return tuiOperation{}, err
		}
		request, err := bindsvc.ValidateAndNormalizeSOA(bindsvc.UpdateSOARequest{
			PrimaryNs: form.value("primary-ns"), Hostmaster: form.value("hostmaster"),
			Refresh: refresh, Retry: retry, Expire: expire, Minimum: minimum,
		})
		if err != nil {
			return tuiOperation{}, err
		}
		return tuiOperation{
			Kind: tuiOperationDNS, Target: target, Action: "soa-update",
			DNSZone: tuiDNSZone{Domain: domain}, DNSOriginalSOA: form.OriginalSOA, DNSSOAUpdate: request,
			Label: "Update SOA for " + domain,
		}, nil
	default:
		return tuiOperation{}, fmt.Errorf("unsupported DNS form %q", form.Kind)
	}
}

func parseTUIDNSInteger(name, value string, minimum, maximum int) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || parsed < minimum || parsed > maximum {
		return 0, fmt.Errorf("%s must be an integer between %d and %d", name, minimum, maximum)
	}
	return parsed, nil
}

func parseTUIDNSUint32(name, value string) (uint32, error) {
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 31)
	if err != nil || parsed > 2147483647 {
		return 0, fmt.Errorf("%s must be an integer between 0 and 2147483647", name)
	}
	return uint32(parsed), nil
}

func (model *tuiModel) openDNSReloadConfirmation() {
	if !model.snapshot.Selected.Local {
		model.notice, model.noticeError = "Local BIND reload runs only on the panel host", true
		return
	}
	if !model.dnsLoaded || !model.dns.Status.ReloadAvailable {
		model.notice, model.noticeError = "BIND reload is unavailable in the current observed state", true
		return
	}
	operation := tuiOperation{
		Kind: tuiOperationDNS, Target: model.snapshot.Selected, Action: "reload",
		Label: "Reload BIND configuration",
	}
	model.openConfirmation(operation, confirmationBody(operation))
}

func (model *tuiModel) openDNSDeleteConfirmation() {
	if !model.snapshot.Selected.Local {
		model.notice, model.noticeError = "Local BIND mutations run only on the panel host", true
		return
	}
	if !model.dnsLoaded || !model.dns.Status.ZoneManagementReady {
		model.notice, model.noticeError = "BIND zone management is unavailable in the current observed state", true
		return
	}
	if model.dns.Detail == nil {
		if model.cursor < 0 || model.cursor >= len(model.dns.Zones) {
			return
		}
		zone := model.dns.Zones[model.cursor]
		operation := tuiOperation{
			Kind: tuiOperationDNS, Target: model.snapshot.Selected, Action: "zone-delete",
			DNSZone: zone, Label: "Delete DNS zone " + zone.Domain, Dangerous: true,
		}
		model.openConfirmation(operation, confirmationBody(operation))
		return
	}
	if model.cursor < 0 || model.cursor >= len(model.dns.Detail.Records) {
		return
	}
	record := model.dns.Detail.Records[model.cursor]
	if strings.EqualFold(record.Type, "SOA") {
		model.notice, model.noticeError = "The zone SOA cannot be deleted from the control center; use SOA update instead", true
		return
	}
	operation := tuiOperation{
		Kind: tuiOperationDNS, Target: model.snapshot.Selected, Action: "record-delete",
		DNSZone: model.dns.Detail.Zone, DNSRecord: record,
		Label: fmt.Sprintf("Delete %s %s", record.Type, record.Name), Dangerous: true,
	}
	model.openConfirmation(operation, confirmationBody(operation))
}

func runTUIDNSOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	if !operation.Target.Local {
		return "", errors.New("local BIND mutations run only on the panel host")
	}
	switch operation.Action {
	case "zone-create":
		request, err := bindsvc.ValidateAndNormalizeCreateZone(operation.DNSCreate)
		if err != nil {
			return "", err
		}
		zone, err := requestJSON[bindsvc.Zone](ctx, client.withTimeout(2*time.Minute), http.MethodPost, "/api/dns/zones", request, true)
		if err != nil {
			return "", err
		}
		return "DNS zone " + zone.Domain + " created", nil
	case "record-add":
		domain, err := bindsvc.NormalizeZoneDomain(operation.DNSZone.Domain)
		if err != nil {
			return "", err
		}
		request, err := bindsvc.ValidateAndNormalizeAddRecord(operation.DNSAdd)
		if err != nil {
			return "", err
		}
		response, err := requestJSON[map[string]string](ctx, client.withTimeout(2*time.Minute), http.MethodPost, dnsZonePath(domain)+"/records", request, true)
		return dnsOperationMessage(response, fmt.Sprintf("DNS record %s %s added", request.Type, request.Name), err)
	case "record-update":
		domain, err := bindsvc.NormalizeZoneDomain(operation.DNSZone.Domain)
		if err != nil {
			return "", err
		}
		request, err := bindsvc.ValidateAndNormalizeUpdateRecord(operation.DNSUpdate)
		if err != nil {
			return "", err
		}
		observed, err := requestJSON[*bindsvc.ZoneDetail](ctx, client, http.MethodGet, dnsZonePath(domain), nil, true)
		if err != nil {
			return "", fmt.Errorf("re-observe DNS record before update: %w", err)
		}
		if observed == nil || !containsTUIDNSRecord(observed.Records, operation.DNSRecord) {
			return "", errors.New("DNS record changed since it was loaded; refresh before updating it")
		}
		response, err := requestJSON[map[string]string](ctx, client.withTimeout(2*time.Minute), http.MethodPut, dnsZonePath(domain)+"/records", request, true)
		return dnsOperationMessage(response, fmt.Sprintf("DNS record %s %s updated", request.Type, request.Name), err)
	case "soa-update":
		domain, err := bindsvc.NormalizeZoneDomain(operation.DNSZone.Domain)
		if err != nil {
			return "", err
		}
		request, err := bindsvc.ValidateAndNormalizeSOA(operation.DNSSOAUpdate)
		if err != nil {
			return "", err
		}
		observed, err := requestJSON[bindsvc.SOARecord](ctx, client, http.MethodGet, dnsZonePath(domain)+"/soa", nil, true)
		if err != nil {
			return "", fmt.Errorf("re-observe SOA before update: %w", err)
		}
		if observed != operation.DNSOriginalSOA {
			return "", errors.New("DNS SOA changed since it was loaded; refresh before updating it")
		}
		response, err := requestJSON[map[string]string](ctx, client.withTimeout(2*time.Minute), http.MethodPut, dnsZonePath(domain)+"/soa", request, true)
		return dnsOperationMessage(response, "SOA updated", err)
	case "reload":
		response, err := requestJSON[map[string]string](ctx, client.withTimeout(2*time.Minute), http.MethodPost, "/api/dns/reload", nil, true)
		return dnsOperationMessage(response, "BIND configuration reloaded", err)
	case "zone-delete":
		domain, err := bindsvc.NormalizeZoneDomain(operation.DNSZone.Domain)
		if err != nil {
			return "", err
		}
		observed, err := requestJSON[*bindsvc.ZoneDetail](ctx, client, http.MethodGet, dnsZonePath(domain), nil, true)
		if err != nil {
			return "", fmt.Errorf("re-observe DNS zone before deletion: %w", err)
		}
		if observed == nil || observed.Serial != operation.DNSZone.Serial || observed.File != operation.DNSZone.File {
			return "", errors.New("DNS zone changed since it was loaded; refresh before deleting it")
		}
		response, err := requestJSON[map[string]string](ctx, client.withTimeout(2*time.Minute), http.MethodDelete, dnsZonePath(domain), nil, true)
		return dnsOperationMessage(response, "DNS zone "+domain+" deleted", err)
	case "record-delete":
		domain, err := bindsvc.NormalizeZoneDomain(operation.DNSZone.Domain)
		if err != nil {
			return "", err
		}
		request, err := bindsvc.ValidateAndNormalizeDeleteRecord(bindsvc.DeleteRecordRequest{
			Name: operation.DNSRecord.Name, Type: operation.DNSRecord.Type,
			Value: operation.DNSRecord.Value, AutoReload: true,
		})
		if err != nil {
			return "", err
		}
		observed, err := requestJSON[*bindsvc.ZoneDetail](ctx, client, http.MethodGet, dnsZonePath(domain), nil, true)
		if err != nil {
			return "", fmt.Errorf("re-observe DNS record before deletion: %w", err)
		}
		if observed == nil || !containsTUIDNSRecord(observed.Records, operation.DNSRecord) {
			return "", errors.New("DNS record changed since it was loaded; refresh before deleting it")
		}
		response, err := requestJSON[map[string]string](ctx, client.withTimeout(2*time.Minute), http.MethodDelete, dnsZonePath(domain)+"/records", request, true)
		return dnsOperationMessage(response, fmt.Sprintf("DNS record %s %s deleted", request.Type, request.Name), err)
	default:
		return "", fmt.Errorf("unsupported DNS TUI action %q", operation.Action)
	}
}

func containsTUIDNSRecord(records []bindsvc.Record, expected bindsvc.Record) bool {
	for _, record := range records {
		if record.Name == expected.Name && strings.EqualFold(record.Type, expected.Type) && record.Value == expected.Value &&
			record.TTL == expected.TTL && record.Priority == expected.Priority {
			return true
		}
	}
	return false
}

func dnsOperationMessage(response map[string]string, fallback string, err error) (string, error) {
	if err != nil {
		return "", err
	}
	if message := strings.TrimSpace(response["message"]); message != "" {
		return message, nil
	}
	return fallback, nil
}

func dnsCheckLines(result bindsvc.CheckResult) []string {
	status := "PASSED"
	if !result.OK {
		status = "FAILED"
	}
	lines := []string{"Configuration check: " + status}
	if output := strings.TrimSpace(result.Output); output != "" {
		lines = append(lines, strings.Split(output, "\n")...)
	}
	for _, zone := range result.ZoneChecks {
		zoneStatus := "PASSED"
		if !zone.OK {
			zoneStatus = "FAILED"
		}
		lines = append(lines, "", fmt.Sprintf("Zone %s: %s", zone.Domain, zoneStatus))
		if output := strings.TrimSpace(zone.Output); output != "" {
			lines = append(lines, strings.Split(output, "\n")...)
		}
	}
	return lines
}
