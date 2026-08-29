package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/services/releaseupdates"
	"github.com/IamYGT/heyserver/internal/services/remotenodes"
)

type tuiUpdateState struct {
	Supported         bool
	Local             bool
	ReleaseStatus     string
	SignatureStatus   string
	CurrentVersion    string
	LatestVersion     string
	LatestState       string
	UpdateAvailable   bool
	Platform          string
	ReleaseNotesURL   string
	ReleaseMessage    string
	ReleaseCheckedAt  string
	Stage             *releaseupdates.Stage
	Operation         string
	OperationStatus   string
	OperationVersion  string
	OperationDetail   string
	OperationUpdated  string
	RollbackAvailable bool
	Warnings          []string
}

type tuiUpdatesMsg struct {
	TargetID string
	State    tuiUpdateState
	Err      error
}

func loadTUIUpdatesCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		state, err := loadTUIUpdates(ctx, client, target)
		return tuiUpdatesMsg{TargetID: target.ID, State: state, Err: err}
	}
}

func loadTUIUpdates(ctx context.Context, client *apiClient, target tuiTarget) (tuiUpdateState, error) {
	state := tuiUpdateState{Local: target.Local}
	if target.Local {
		release, err := requestJSON[releaseupdates.Result](ctx, client, http.MethodGet, "/api/system/update", nil, true)
		if err != nil {
			return state, err
		}
		state.Supported = true
		state.ReleaseStatus = release.Status
		state.SignatureStatus = release.SignatureStatus
		state.CurrentVersion = release.CurrentVersion
		state.LatestVersion = release.LatestVersion
		state.LatestState = string(release.LatestVersionState)
		state.UpdateAvailable = release.UpdateAvailable
		state.Platform = release.Platform
		state.ReleaseNotesURL = release.ReleaseNotesURL
		state.ReleaseMessage = release.Message
		if !release.CheckedAt.IsZero() {
			state.ReleaseCheckedAt = release.CheckedAt.UTC().Format(time.RFC3339)
		}

		stage, err := requestJSON[struct {
			Stage *releaseupdates.Stage `json:"stage"`
		}](ctx, client, http.MethodGet, "/api/system/update/stage", nil, true)
		if err != nil {
			state.Warnings = append(state.Warnings, "Release stage unavailable: "+err.Error())
			return state, nil
		}
		state.Stage = stage.Stage
		return state, nil
	}

	state.ReleaseStatus = releaseupdates.StatusUnavailable
	if !target.Online {
		state.ReleaseMessage = "Managed node is offline; agent release status cannot be refreshed."
		return state, nil
	}
	if !target.capability(agenthub.CapabilityAgentUpdateRead) {
		state.ReleaseMessage = "Managed agent does not advertise agent.update.read."
		return state, nil
	}

	status, err := loadManagedAgentUpdateStatus(ctx, client, target)
	if err != nil {
		return state, err
	}
	state.Supported = true
	state.ReleaseStatus = status.ReleaseStatus
	state.SignatureStatus = status.SignatureStatus
	state.CurrentVersion = status.CurrentVersion
	state.LatestVersion = status.LatestVersion
	state.LatestState = status.LatestState
	state.UpdateAvailable = status.UpdateAvailable
	state.Platform = status.Platform
	state.ReleaseNotesURL = status.ReleaseNotesURL
	state.ReleaseMessage = status.ReleaseMessage
	state.ReleaseCheckedAt = status.ReleaseCheckedAt
	state.Operation = status.Operation
	state.OperationStatus = status.OperationStatus
	state.OperationVersion = status.OperationVersion
	state.OperationDetail = status.OperationDetail
	state.OperationUpdated = status.OperationUpdated
	state.RollbackAvailable = status.RollbackAvailable
	return state, nil
}

func loadManagedAgentUpdateStatus(ctx context.Context, client *apiClient, target tuiTarget) (remotenodes.AgentUpdateStatus, error) {
	if target.Local {
		return remotenodes.AgentUpdateStatus{}, errors.New("managed agent update status requires a managed node")
	}
	if !target.Online {
		return remotenodes.AgentUpdateStatus{}, errors.New("managed node is offline")
	}
	if !target.capability(agenthub.CapabilityAgentUpdateRead) {
		return remotenodes.AgentUpdateStatus{}, errors.New("managed agent does not advertise agent.update.read")
	}
	return requestJSON[remotenodes.AgentUpdateStatus](ctx, client.withTimeout(time.Minute), http.MethodGet, agentUpdateEndpoint(target.ID, ""), nil, true)
}

func (model tuiModel) loadUpdates() (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading release lifecycle state…"
	model.noticeError = false
	return model, loadTUIUpdatesCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (state tuiUpdateState) canStage() bool {
	if !state.Local || !state.Supported || state.SignatureStatus != releaseupdates.SignatureVerified || state.ReleaseStatus != releaseupdates.StatusHealthy || !state.UpdateAvailable || !stableUpdateVersion(state.LatestVersion) {
		return false
	}
	return state.Stage == nil || !updateOperationActive(state.Stage.Status)
}

func (state tuiUpdateState) canInstall() bool {
	return state.Local && state.Supported && state.SignatureStatus == releaseupdates.SignatureVerified && validTUIUpdateStage(state.Stage) && (state.Stage.Status == releaseupdates.StageStaged || state.Stage.Status == releaseupdates.StageFailed)
}

func (state tuiUpdateState) canUpgradeAgent(target tuiTarget) bool {
	return !state.Local && state.Supported && target.capability(agenthub.CapabilityAgentUpdateAction) &&
		state.SignatureStatus == releaseupdates.SignatureVerified && state.ReleaseStatus == releaseupdates.StatusHealthy && state.UpdateAvailable && stableUpdateVersion(state.LatestVersion) && !updateOperationActive(state.OperationStatus)
}

func (state tuiUpdateState) canRollbackAgent(target tuiTarget) bool {
	return !state.Local && state.Supported && target.capability(agenthub.CapabilityAgentUpdateAction) && state.RollbackAvailable && !updateOperationActive(state.OperationStatus)
}

func (model tuiModel) hasActiveUpdateOperation() bool {
	if !model.updatesLoaded {
		return false
	}
	if model.updates.Local {
		return model.updates.Stage != nil && updateOperationActive(model.updates.Stage.Status)
	}
	return updateOperationActive(model.updates.OperationStatus)
}

func validTUIUpdateStage(stage *releaseupdates.Stage) bool {
	return stage != nil && strings.TrimSpace(stage.ID) != "" && stableUpdateVersion(stage.Version)
}

func (state tuiUpdateState) signedManifestReason() string {
	if !state.Supported || state.SignatureStatus == releaseupdates.SignatureVerified {
		return ""
	}
	return signedManifestRequiredMessage
}

func updateActionUnavailableReason(state tuiUpdateState, fallback string) string {
	if reason := state.signedManifestReason(); reason != "" {
		return reason
	}
	return fallback
}

func (model *tuiModel) openUpdateConfirmation(action string) {
	if !model.updatesLoaded || model.updatesTarget != model.selectedTargetID {
		model.notice, model.noticeError = "Load current release lifecycle state first", true
		return
	}
	state := model.updates
	target := model.snapshot.Selected
	operation := tuiOperation{Kind: tuiOperationUpdate, Target: target, Action: action, Update: state}
	switch action {
	case "stage":
		if !state.canStage() {
			model.notice, model.noticeError = updateActionUnavailableReason(state, "No newer stable panel release is ready to stage"), true
			return
		}
		operation.Label = "Stage panel release " + state.LatestVersion
	case "install":
		if !state.canInstall() {
			model.notice, model.noticeError = updateActionUnavailableReason(state, "No verified panel release stage is ready to install"), true
			return
		}
		operation.Label = "Install panel release " + state.Stage.Version
		operation.Dangerous = true
	case "upgrade":
		if !state.canUpgradeAgent(target) {
			model.notice, model.noticeError = updateActionUnavailableReason(state, "Managed agent has no verified newer stable release ready to install"), true
			return
		}
		operation.Label = "Upgrade managed agent to " + state.LatestVersion
		operation.Dangerous = true
	case "rollback":
		if !state.canRollbackAgent(target) {
			model.notice, model.noticeError = "Managed agent has no verified rollback snapshot ready", true
			return
		}
		operation.Label = "Rollback managed agent"
		operation.Dangerous = true
	default:
		model.notice, model.noticeError = "Unsupported release lifecycle action", true
		return
	}
	model.openConfirmation(operation, confirmationBody(operation))
}

func runTUIUpdateOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	state := operation.Update
	switch operation.Action {
	case "stage":
		if !operation.Target.Local || !state.canStage() {
			return "", errors.New(updateActionUnavailableReason(state, "panel release stage observation is no longer actionable"))
		}
		fresh, err := requestJSON[releaseupdates.Result](ctx, client, http.MethodGet, "/api/system/update", nil, true)
		if err != nil {
			return "", err
		}
		if fresh.SignatureStatus != releaseupdates.SignatureVerified {
			return "", errors.New(signedManifestRequiredMessage)
		}
		if fresh.Status != releaseupdates.StatusHealthy || !fresh.UpdateAvailable || fresh.LatestVersion != state.LatestVersion || !stableUpdateVersion(fresh.LatestVersion) {
			return "", errors.New("panel release state changed; refresh before staging")
		}
		stage, err := requestJSON[releaseupdates.Stage](ctx, client.withTimeout(16*time.Minute), http.MethodPost, "/api/system/update/stage", nil, true)
		if err != nil {
			return "", err
		}
		if !validTUIUpdateStage(&stage) || stage.Version != fresh.LatestVersion || stage.Status != releaseupdates.StageStaged {
			return "", errors.New("panel returned an invalid release stage receipt")
		}
		return "Release " + stage.Version + " staged and verified", nil
	case "install":
		if !operation.Target.Local || !state.canInstall() {
			return "", errors.New(updateActionUnavailableReason(state, "panel release install observation is no longer actionable"))
		}
		freshRelease, err := requestJSON[releaseupdates.Result](ctx, client, http.MethodGet, "/api/system/update", nil, true)
		if err != nil {
			return "", err
		}
		if freshRelease.SignatureStatus != releaseupdates.SignatureVerified {
			return "", errors.New(signedManifestRequiredMessage)
		}
		fresh, err := requestJSON[struct {
			Stage *releaseupdates.Stage `json:"stage"`
		}](ctx, client, http.MethodGet, "/api/system/update/stage", nil, true)
		if err != nil {
			return "", err
		}
		observed := state.Stage
		if !validTUIUpdateStage(fresh.Stage) || fresh.Stage.ID != observed.ID || fresh.Stage.Version != observed.Version || fresh.Stage.Status != observed.Status ||
			(fresh.Stage.Status != releaseupdates.StageStaged && fresh.Stage.Status != releaseupdates.StageFailed) {
			return "", errors.New("panel release stage changed; refresh before installation")
		}
		result, err := requestJSON[releaseupdates.Stage](ctx, client.withTimeout(2*time.Minute), http.MethodPost, "/api/system/update/install", map[string]any{
			"stage_id": fresh.Stage.ID, "version": fresh.Stage.Version, "confirmed": true,
		}, true)
		if err != nil {
			return "", err
		}
		if result.ID != fresh.Stage.ID || result.Version != fresh.Stage.Version || result.Status != releaseupdates.StageScheduled {
			return "", errors.New("panel returned an invalid release installation receipt")
		}
		return "Release " + result.Version + " installation scheduled with automatic rollback", nil
	case "upgrade":
		if operation.Target.Local || !state.canUpgradeAgent(operation.Target) {
			return "", errors.New(updateActionUnavailableReason(state, "managed agent upgrade observation is no longer actionable"))
		}
		fresh, err := loadManagedAgentUpdateStatus(ctx, client, operation.Target)
		if err != nil {
			return "", err
		}
		if fresh.SignatureStatus != releaseupdates.SignatureVerified {
			return "", errors.New(signedManifestRequiredMessage)
		}
		if fresh.ReleaseStatus != releaseupdates.StatusHealthy || !fresh.UpdateAvailable || fresh.LatestVersion != state.LatestVersion ||
			!stableUpdateVersion(fresh.LatestVersion) || updateOperationActive(fresh.OperationStatus) {
			return "", errors.New("managed agent release state changed; refresh before upgrade")
		}
		result, err := requestJSON[remotenodes.AgentUpdateStatus](ctx, client.withTimeout(12*time.Minute), http.MethodPost, agentUpdateEndpoint(operation.Target.ID, "upgrade"), map[string]any{
			"version": fresh.LatestVersion, "confirmed": true,
		}, true)
		if err != nil {
			return "", err
		}
		if result.Operation != "upgrade" || result.OperationStatus != "scheduled" || result.OperationVersion != fresh.LatestVersion {
			return "", errors.New("managed agent returned an invalid upgrade receipt")
		}
		return "Managed agent upgrade to " + result.OperationVersion + " scheduled", nil
	case "rollback":
		if operation.Target.Local || !state.canRollbackAgent(operation.Target) {
			return "", errors.New("managed agent rollback observation is no longer actionable")
		}
		fresh, err := loadManagedAgentUpdateStatus(ctx, client, operation.Target)
		if err != nil {
			return "", err
		}
		if !fresh.RollbackAvailable || fresh.CurrentVersion != state.CurrentVersion || updateOperationActive(fresh.OperationStatus) {
			return "", errors.New("managed agent rollback state changed; refresh before rollback")
		}
		result, err := requestJSON[remotenodes.AgentUpdateStatus](ctx, client.withTimeout(2*time.Minute), http.MethodPost, agentUpdateEndpoint(operation.Target.ID, "rollback"), map[string]any{"confirmed": true}, true)
		if err != nil {
			return "", err
		}
		if result.Operation != "rollback" || result.OperationStatus != "scheduled" {
			return "", errors.New("managed agent returned an invalid rollback receipt")
		}
		return "Managed agent rollback scheduled", nil
	default:
		return "", fmt.Errorf("unsupported update TUI action %q", operation.Action)
	}
}

func (model tuiModel) renderUpdates(width, height int) string {
	rows := []string{tuiTitleStyle.Render("Release lifecycle") + tuiMutedStyle.Render("  U jump · R reload")}
	if !model.updatesLoaded {
		message := "Release lifecycle state has not been loaded."
		if model.resourceLoading {
			message = "Loading release lifecycle state…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	state := model.updates
	statusStyle := tuiDimStyle
	switch state.ReleaseStatus {
	case releaseupdates.StatusHealthy:
		statusStyle = lipgloss.NewStyle().Foreground(tuiGreen)
	case releaseupdates.StatusUnavailable:
		statusStyle = lipgloss.NewStyle().Foreground(tuiRed)
	case releaseupdates.StatusNotConfigured:
		statusStyle = lipgloss.NewStyle().Foreground(tuiAmber)
	}
	rows = append(rows,
		fmt.Sprintf("%-18s %s", tuiMutedStyle.Render("Discovery"), statusStyle.Render(valueOrNA(state.ReleaseStatus))),
		fmt.Sprintf("%-18s %s", tuiMutedStyle.Render("Signature"), valueOrNA(state.SignatureStatus)),
		fmt.Sprintf("%-18s %s", tuiMutedStyle.Render("Version"), updateVersionRoute(state.CurrentVersion, state.LatestVersion)),
		fmt.Sprintf("%-18s %s", tuiMutedStyle.Render("Platform"), valueOrNA(state.Platform)),
		fmt.Sprintf("%-18s %s", tuiMutedStyle.Render("Checked"), valueOrNA(state.ReleaseCheckedAt)),
	)
	if reason := state.signedManifestReason(); reason != "" {
		rows = append(rows, "", lipgloss.NewStyle().Foreground(tuiAmber).Render(reason))
	}
	if state.ReleaseMessage != "" {
		rows = append(rows, "", truncateTUI(state.ReleaseMessage, width-4))
	}
	if state.Local {
		rows = append(rows, "", tuiTitleStyle.Render("Verified panel stage"))
		if state.Stage == nil {
			rows = append(rows, tuiDimStyle.Render("No staged release is available."))
		} else {
			rows = append(rows,
				fmt.Sprintf("%-18s %s", tuiMutedStyle.Render("Identity"), state.Stage.ID),
				fmt.Sprintf("%-18s %s · %s", tuiMutedStyle.Render("State"), state.Stage.Status, valueOrNA(state.Stage.StatusDetail)),
				fmt.Sprintf("%-18s %s · %s", tuiMutedStyle.Render("Artifact"), formatTUIBytes(uint64(maxInt64(0, state.Stage.SizeBytes))), shortUpdateDigest(state.Stage.SHA256)),
				fmt.Sprintf("%-18s %s", tuiMutedStyle.Render("Updated"), formatTUIUpdateTime(state.Stage.UpdatedAt)),
			)
		}
		actions := []string{}
		if state.canStage() {
			actions = append(actions, "s stage "+state.LatestVersion)
		}
		if state.canInstall() {
			actions = append(actions, "i install "+state.Stage.Version)
		}
		rows = append(rows, "", tuiDimStyle.Render("Available: "+valueOrNA(strings.Join(actions, " · "))))
	} else {
		rows = append(rows, "", tuiTitleStyle.Render("Managed agent operation"))
		rows = append(rows,
			fmt.Sprintf("%-18s %s · %s", tuiMutedStyle.Render("Operation"), valueOrNA(state.Operation), valueOrNA(state.OperationStatus)),
			fmt.Sprintf("%-18s %s", tuiMutedStyle.Render("Operation version"), valueOrNA(state.OperationVersion)),
			fmt.Sprintf("%-18s %s", tuiMutedStyle.Render("Detail"), truncateTUI(valueOrNA(state.OperationDetail), width-24)),
			fmt.Sprintf("%-18s %s", tuiMutedStyle.Render("Rollback"), map[bool]string{true: "available", false: "unavailable"}[state.RollbackAvailable]),
		)
		actions := []string{}
		if state.canUpgradeAgent(model.snapshot.Selected) {
			actions = append(actions, "u upgrade "+state.LatestVersion)
		}
		if state.canRollbackAgent(model.snapshot.Selected) {
			actions = append(actions, "o rollback")
		}
		rows = append(rows, "", tuiDimStyle.Render("Available: "+valueOrNA(strings.Join(actions, " · "))))
	}
	for _, warning := range state.Warnings {
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! "+truncateTUI(warning, width-6)))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}

func updateVersionRoute(current, latest string) string {
	current = valueOrNA(current)
	if strings.TrimSpace(latest) == "" {
		return current
	}
	return current + " → " + latest
}

func shortUpdateDigest(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 16 {
		return "sha256:" + value[:16] + "…"
	}
	return valueOrNA(value)
}

func formatTUIUpdateTime(value time.Time) string {
	if value.IsZero() {
		return "N/A"
	}
	return value.UTC().Format(time.RFC3339)
}
