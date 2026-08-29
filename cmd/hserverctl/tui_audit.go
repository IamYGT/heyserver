package main

import (
	"context"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/IamYGT/heyserver/internal/models"
)

const tuiAuditPageSize = 100

type tuiAuditState struct {
	Entries []models.AuditLog `json:"data"`
	Total   int               `json:"total"`
	Limit   int               `json:"limit"`
	Offset  int               `json:"offset"`
}

type tuiAuditMsg struct {
	TargetID string
	State    tuiAuditState
	Err      error
}

func loadTUIAuditCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		state, err := loadTUIAudit(ctx, client, target)
		return tuiAuditMsg{TargetID: target.ID, State: state, Err: err}
	}
}

func loadTUIAudit(ctx context.Context, client *apiClient, target tuiTarget) (tuiAuditState, error) {
	scope := target.ID
	if target.Local {
		scope = "local"
	}
	endpoint, err := buildAuditListPath(auditListOptions{Limit: tuiAuditPageSize, Server: scope})
	if err != nil {
		return tuiAuditState{}, err
	}
	state, err := requestJSON[tuiAuditState](ctx, client, "GET", endpoint, nil, true)
	if err != nil {
		return tuiAuditState{}, err
	}
	if state.Entries == nil {
		state.Entries = []models.AuditLog{}
	}
	if state.Total < len(state.Entries) || state.Limit < 0 || state.Offset < 0 {
		return tuiAuditState{}, fmt.Errorf("audit endpoint returned invalid pagination metadata")
	}
	return state, nil
}

func (model tuiModel) loadAudit() (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading target-scoped audit history…"
	model.noticeError = false
	return model, loadTUIAuditCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model *tuiModel) openAuditFilter() {
	model.dialog = tuiDialog{
		Mode: tuiDialogAuditFilter, Title: "Filter loaded audit history",
		AuditFilter: model.auditFilter,
	}
}

func (model tuiModel) updateAuditFilterKey(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "esc":
		model.dialog = tuiDialog{}
		return model, nil
	case "enter":
		model.auditFilter = strings.TrimSpace(model.dialog.AuditFilter)
		model.dialog = tuiDialog{}
		model.cursor = 0
		model.notice = "Audit filter updated"
		if model.auditFilter == "" {
			model.notice = "Audit filter cleared"
		}
		model.noticeError = false
		return model, nil
	case "backspace", "ctrl+h":
		runes := []rune(model.dialog.AuditFilter)
		if len(runes) > 0 {
			model.dialog.AuditFilter = string(runes[:len(runes)-1])
		}
		return model, nil
	case "ctrl+u":
		model.dialog.AuditFilter = ""
		return model, nil
	}
	if utf8.RuneCountInString(key) == 1 && utf8.RuneCountInString(model.dialog.AuditFilter) < 128 {
		character, _ := utf8.DecodeRuneInString(key)
		if !unicode.IsControl(character) {
			model.dialog.AuditFilter += key
		}
	}
	return model, nil
}

func filteredTUIAuditEntries(entries []models.AuditLog, query string) []models.AuditLog {
	terms := strings.Fields(strings.ToLower(strings.TrimSpace(query)))
	if len(terms) == 0 {
		return entries
	}
	filtered := make([]models.AuditLog, 0, len(entries))
	for _, entry := range entries {
		haystack := strings.ToLower(strings.Join([]string{
			entry.UserName, entry.Action, entry.Resource, entry.Details, entry.IP,
		}, " "))
		matches := true
		for _, term := range terms {
			if !strings.Contains(haystack, term) {
				matches = false
				break
			}
		}
		if matches {
			filtered = append(filtered, entry)
		}
	}
	return filtered
}
