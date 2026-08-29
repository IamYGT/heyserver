package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/remotenodes"
)

const tuiDeployHistoryLimit = 50

type tuiDeployState struct {
	Supported     bool
	Local         bool
	Targets       []models.DeployTarget
	Runs          []models.DeployRun
	RemoteTargets []remotenodes.RemoteDeployTarget
	RemoteJobs    []remotenodes.RemoteDeployJob
	Message       string
	Warnings      []string
}

type tuiDeployMsg struct {
	TargetID string
	State    tuiDeployState
	Err      error
}

type tuiDeployDetail struct {
	Target    models.DeployTarget
	Preflight models.DeployPreflight
	Revision  models.DeployRevisionComparison
}

type tuiDeployDetailMsg struct {
	TargetID string
	Detail   tuiDeployDetail
	Err      error
}

type tuiDeployLogRef struct {
	LocalRunID int64
	RemoteJob  string
}

type tuiDeployLogsMsg struct {
	TargetID string
	Ref      tuiDeployLogRef
	Title    string
	Lines    []string
	Err      error
}

func deployTargetCount(state tuiDeployState) int {
	if state.Local {
		return len(state.Targets)
	}
	return len(state.RemoteTargets)
}

func deployJobCount(state tuiDeployState) int {
	if state.Local {
		return len(state.Runs)
	}
	return len(state.RemoteJobs)
}

func loadTUIDeployCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		state, err := loadTUIDeploy(ctx, client, target)
		return tuiDeployMsg{TargetID: target.ID, State: state, Err: err}
	}
}

func loadTUIDeploy(ctx context.Context, client *apiClient, target tuiTarget) (tuiDeployState, error) {
	state := tuiDeployState{Local: target.Local}
	if target.Local {
		targets, err := requestJSON[[]models.DeployTarget](ctx, client, http.MethodGet, "/api/deploy/targets", nil, true)
		if err != nil {
			return state, err
		}
		state.Supported = true
		state.Targets = targets
		runs, err := requestJSON[[]models.DeployRun](ctx, client, http.MethodGet, "/api/deploy/history?limit="+strconv.Itoa(tuiDeployHistoryLimit), nil, true)
		if err != nil {
			state.Warnings = append(state.Warnings, "Deployment history unavailable: "+err.Error())
		} else {
			state.Runs = runs
		}
		return state, nil
	}

	if !target.Online {
		state.Message = "Managed node is offline; deployment plans cannot be refreshed."
		return state, nil
	}
	if !target.capability(agenthub.CapabilityDeployRead) {
		state.Message = "Managed agent does not advertise deploy.read."
		return state, nil
	}
	endpoint := "/api/nodes/" + url.PathEscape(target.ID) + "/deploy"
	targets, err := requestJSON[[]remotenodes.RemoteDeployTarget](ctx, client.withTimeout(time.Minute), http.MethodGet, endpoint, nil, true)
	if err != nil {
		return state, err
	}
	state.Supported = true
	state.RemoteTargets = targets
	jobs, err := requestJSON[[]remotenodes.RemoteDeployJob](ctx, client, http.MethodGet, endpoint+"/jobs", nil, true)
	if err != nil {
		state.Warnings = append(state.Warnings, "Managed deployment jobs unavailable: "+err.Error())
	} else {
		state.RemoteJobs = jobs
	}
	return state, nil
}

func loadTUIDeployDetailCmd(ctx context.Context, client *apiClient, target tuiTarget, deployment models.DeployTarget) tea.Cmd {
	return func() tea.Msg {
		detail, err := loadTUIDeployDetail(ctx, client, target, deployment)
		return tuiDeployDetailMsg{TargetID: target.ID, Detail: detail, Err: err}
	}
}

func loadTUIDeployDetail(ctx context.Context, client *apiClient, target tuiTarget, deployment models.DeployTarget) (tuiDeployDetail, error) {
	if !target.Local {
		return tuiDeployDetail{}, errors.New("local deployment detail requires the panel host")
	}
	base := "/api/deploy/targets/" + strconv.FormatInt(deployment.ID, 10)
	preflight, err := requestJSON[models.DeployPreflight](ctx, client.withTimeout(2*time.Minute), http.MethodGet, base+"/preflight", nil, true)
	if err != nil {
		return tuiDeployDetail{}, err
	}
	revision, err := requestJSON[models.DeployRevisionComparison](ctx, client, http.MethodGet, base+"/revision", nil, true)
	if err != nil {
		return tuiDeployDetail{}, err
	}
	if preflight.TargetID != deployment.ID || revision.TargetID != deployment.ID {
		return tuiDeployDetail{}, errors.New("deployment detail returned a mismatched target identity")
	}
	return tuiDeployDetail{Target: deployment, Preflight: preflight, Revision: revision}, nil
}

func loadTUIDeployLogsCmd(ctx context.Context, client *apiClient, target tuiTarget, ref tuiDeployLogRef) tea.Cmd {
	return func() tea.Msg {
		title, lines, err := loadTUIDeployLogs(ctx, client, target, ref)
		return tuiDeployLogsMsg{TargetID: target.ID, Ref: ref, Title: title, Lines: lines, Err: err}
	}
}

func loadTUIDeployLogs(ctx context.Context, client *apiClient, target tuiTarget, ref tuiDeployLogRef) (string, []string, error) {
	if target.Local {
		if ref.LocalRunID <= 0 {
			return "", nil, errors.New("deployment run does not have a stable identity")
		}
		response, err := requestJSON[struct {
			Logs string `json:"logs"`
		}](ctx, client, http.MethodGet, "/api/deploy/history/"+strconv.FormatInt(ref.LocalRunID, 10)+"/logs", nil, true)
		if err != nil {
			return "", nil, err
		}
		return fmt.Sprintf("Deployment run %d logs", ref.LocalRunID), deployOutputLines(response.Logs), nil
	}
	if !target.Online {
		return "", nil, errors.New("managed node is offline")
	}
	if !target.capability(agenthub.CapabilityDeployRead) {
		return "", nil, errors.New("managed agent does not advertise deploy.read")
	}
	jobs, err := requestJSON[[]remotenodes.RemoteDeployJob](ctx, client, http.MethodGet, "/api/nodes/"+url.PathEscape(target.ID)+"/deploy/jobs", nil, true)
	if err != nil {
		return "", nil, err
	}
	for _, job := range jobs {
		if job.ID != ref.RemoteJob {
			continue
		}
		lines := []string{
			"Status: " + valueOrNA(job.Status),
			"Action: " + valueOrNA(job.Action),
			"Target: " + valueOrNA(job.TargetID),
			"Message: " + valueOrNA(job.Message),
			"Created: " + valueOrNA(job.CreatedAt),
		}
		if strings.TrimSpace(job.Output) != "" {
			lines = append(lines, "", "Output:")
			lines = append(lines, deployOutputLines(job.Output)...)
		}
		return "Managed deployment job " + job.ID, lines, nil
	}
	return "", nil, errors.New("managed deployment job is no longer present in recent history")
}

func deployOutputLines(output string) []string {
	output = strings.ReplaceAll(output, "\r\n", "\n")
	output = strings.TrimSuffix(output, "\n")
	if output == "" {
		return []string{"No output was recorded."}
	}
	return strings.Split(output, "\n")
}

func (model tuiModel) loadDeploy() (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading deployment targets and recent jobs…"
	model.noticeError = false
	return model, loadTUIDeployCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model tuiModel) activateDeployItem() (tea.Model, tea.Cmd) {
	if !model.deployLoaded || model.deployTarget != model.selectedTargetID {
		return model.loadDeploy()
	}
	state := model.deploy
	if state.Local {
		if model.cursor < len(state.Runs) {
			return model.openDeployLogs(tuiDeployLogRef{LocalRunID: state.Runs[model.cursor].ID})
		}
		index := model.cursor - len(state.Runs)
		if index < 0 || index >= len(state.Targets) {
			return model, nil
		}
		model.resourceLoading = true
		model.notice = "Running deployment preflight and revision inspection…"
		model.noticeError = false
		return model, loadTUIDeployDetailCmd(model.ctx, model.client, model.snapshot.Selected, state.Targets[index])
	}
	if model.cursor < len(state.RemoteJobs) {
		return model.openDeployLogs(tuiDeployLogRef{RemoteJob: state.RemoteJobs[model.cursor].ID})
	}
	index := model.cursor - len(state.RemoteJobs)
	if index < 0 || index >= len(state.RemoteTargets) {
		return model, nil
	}
	model.openRemoteDeployActions(state.RemoteTargets[index])
	return model, nil
}

func (model tuiModel) openDeployLogs(ref tuiDeployLogRef) (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading deployment output…"
	model.noticeError = false
	return model, loadTUIDeployLogsCmd(model.ctx, model.client, model.snapshot.Selected, ref)
}

func (model *tuiModel) openLocalDeployActions(detail tuiDeployDetail) {
	options := []tuiDialogOption{{Label: "View preflight and revision", Action: "view-preflight"}}
	if detail.Target.IsActive && detail.Preflight.Eligible {
		options = append(options, tuiDialogOption{Label: "Deploy", Action: "deploy"})
	}
	if detail.Target.IsActive && detail.Preflight.Eligible && detail.Revision.RollbackAvailable {
		options = append(options, tuiDialogOption{Label: "Rollback", Action: "rollback", Dangerous: true})
	}
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Deploy " + truncateTUI(detail.Target.Name, 48),
		Body: []string{"Choose an observed target action. Mutations require a separate confirmation."}, Options: options,
		Operation: tuiOperation{
			Kind: tuiOperationDeploy, Target: model.snapshot.Selected, DeployTarget: detail.Target,
			DeployPreflight: detail.Preflight, DeployRevision: detail.Revision, Label: detail.Target.Name,
		},
	}
}

func (model *tuiModel) openRemoteDeployActions(target remotenodes.RemoteDeployTarget) {
	if reason := model.unavailableReason(agenthub.CapabilityDeployAction); reason != "" {
		model.notice, model.noticeError = reason, true
		return
	}
	if !target.Eligible {
		model.notice, model.noticeError = valueOrNA(target.Reason), true
		return
	}
	options := make([]tuiDialogOption, 0, len(target.Actions))
	for _, action := range target.Actions {
		if !validTUIRemoteDeployAction(action) {
			continue
		}
		options = append(options, tuiDialogOption{
			Label: strings.ToUpper(action[:1]) + action[1:], Action: action,
			Dangerous: action == "rollback" || action == "restart",
		})
	}
	if len(options) == 0 {
		model.notice, model.noticeError = "Managed deployment plan advertises no supported action", true
		return
	}
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Deploy " + truncateTUI(target.Name, 48),
		Body: []string{"Only actions advertised by this managed agent are available. A confirmation follows."}, Options: options,
		Operation: tuiOperation{
			Kind: tuiOperationDeploy, Target: model.snapshot.Selected, RemoteDeployTarget: target, Label: target.Name,
		},
	}
}

func validTUIRemoteDeployAction(action string) bool {
	return action == "preflight" || action == "deploy" || action == "restart" || action == "rollback"
}

func deployDetailLines(detail tuiDeployDetail) []string {
	lines := []string{
		"Target: " + detail.Target.Name,
		fmt.Sprintf("Preflight: eligible=%t · kind=%s", detail.Preflight.Eligible, detail.Preflight.DeploymentKind),
	}
	for _, check := range detail.Preflight.Checks {
		lines = append(lines, fmt.Sprintf("[%s] %s — %s", strings.ToUpper(check.Status), check.ID, check.Message))
	}
	lines = append(lines, "",
		"Revision state: "+valueOrNA(detail.Revision.State),
		"Current: "+shortDeployRevision(detail.Revision.CurrentCommit),
		"Deployed: "+shortDeployRevision(detail.Revision.DeployedCommit),
		"Rollback: "+shortDeployRevision(detail.Revision.RollbackCommit),
		fmt.Sprintf("Tracked changes: %t · matches deployed: %t · rollback available: %t", detail.Revision.TrackedChanges, detail.Revision.MatchesDeployed, detail.Revision.RollbackAvailable),
		fmt.Sprintf("Diff: %d files · +%d/-%d", detail.Revision.FilesChanged, detail.Revision.Insertions, detail.Revision.Deletions),
		detail.Revision.Message,
	)
	return lines
}

func shortDeployRevision(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 12 {
		return value[:12]
	}
	return valueOrNA(value)
}

func runTUIDeployOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	if operation.Target.Local {
		if operation.Action != "deploy" && operation.Action != "rollback" {
			return "", fmt.Errorf("unsupported local deployment action %q", operation.Action)
		}
		freshTarget, err := observeLocalDeployTarget(ctx, client, operation.DeployTarget.ID)
		if err != nil {
			return "", err
		}
		if !sameLocalDeployTarget(operation.DeployTarget, freshTarget) {
			return "", errors.New("deployment target changed; refresh before mutation")
		}
		if !freshTarget.IsActive {
			return "", errors.New("deployment target is inactive")
		}
		base := "/api/deploy/targets/" + strconv.FormatInt(freshTarget.ID, 10)
		preflight, err := requestJSON[models.DeployPreflight](ctx, client.withTimeout(2*time.Minute), http.MethodGet, base+"/preflight", nil, true)
		if err != nil {
			return "", err
		}
		if !preflight.Eligible {
			return "", errors.New("deployment preflight is no longer eligible")
		}
		if operation.DeployPreflight.TargetID > 0 && !sameDeployPreflight(operation.DeployPreflight, preflight) {
			return "", errors.New("deployment preflight changed; refresh before mutation")
		}
		revision, err := requestJSON[models.DeployRevisionComparison](ctx, client, http.MethodGet, base+"/revision", nil, true)
		if err != nil {
			return "", err
		}
		if operation.DeployRevision.TargetID > 0 && !sameDeployRevision(operation.DeployRevision, revision) {
			return "", errors.New("deployment revision changed; refresh before mutation")
		}
		endpoint := "/api/deploy/manual/" + strconv.FormatInt(freshTarget.ID, 10)
		if operation.Action == "rollback" {
			if !revision.RollbackAvailable || operation.DeployRevision.TargetID <= 0 {
				return "", errors.New("deployment rollback revision changed; refresh before mutation")
			}
			endpoint = "/api/deploy/rollback/" + strconv.FormatInt(freshTarget.ID, 10)
		}
		receipt, err := requestJSON[struct {
			Message string `json:"message"`
			RunID   int64  `json:"runId"`
		}](ctx, client.withTimeout(2*time.Minute), http.MethodPost, endpoint, nil, true)
		if err != nil {
			return "", err
		}
		if receipt.RunID <= 0 || strings.TrimSpace(receipt.Message) == "" {
			return "", errors.New("panel returned an invalid deployment receipt")
		}
		return fmt.Sprintf("%s; run %d", strings.TrimSpace(receipt.Message), receipt.RunID), nil
	}

	if !operation.Target.capability(agenthub.CapabilityDeployRead) || !operation.Target.capability(agenthub.CapabilityDeployAction) {
		return "", errors.New("managed agent does not advertise deploy.read and deploy.action")
	}
	if !validTUIRemoteDeployAction(operation.Action) || !slices.Contains(operation.RemoteDeployTarget.Actions, operation.Action) {
		return "", errors.New("managed deployment action was not observed")
	}
	freshTargets, err := requestJSON[[]remotenodes.RemoteDeployTarget](ctx, client.withTimeout(time.Minute), http.MethodGet, "/api/nodes/"+url.PathEscape(operation.Target.ID)+"/deploy", nil, true)
	if err != nil {
		return "", err
	}
	fresh, ok := findRemoteDeployTarget(freshTargets, operation.RemoteDeployTarget.ID)
	if !ok || !sameRemoteDeployTarget(operation.RemoteDeployTarget, fresh) || !fresh.Eligible || !slices.Contains(fresh.Actions, operation.Action) {
		return "", errors.New("managed deployment plan changed; refresh before mutation")
	}
	job, err := requestJSON[remotenodes.RemoteDeployJob](ctx, client.withTimeout(2*time.Minute), http.MethodPost,
		"/api/nodes/"+url.PathEscape(operation.Target.ID)+"/deploy/"+url.PathEscape(fresh.ID)+"/actions/"+url.PathEscape(operation.Action), nil, true)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(job.ID) == "" || job.TargetID != fresh.ID || job.Action != operation.Action || (job.Status != agenthub.TaskStatusQueued && job.Status != agenthub.TaskStatusRunning) {
		return "", errors.New("managed agent returned an invalid deployment job receipt")
	}
	return fmt.Sprintf("Managed %s queued as %s", operation.Action, job.ID), nil
}

func observeLocalDeployTarget(ctx context.Context, client *apiClient, targetID int64) (models.DeployTarget, error) {
	targets, err := requestJSON[[]models.DeployTarget](ctx, client, http.MethodGet, "/api/deploy/targets", nil, true)
	if err != nil {
		return models.DeployTarget{}, err
	}
	for _, target := range targets {
		if target.ID == targetID {
			return target, nil
		}
	}
	return models.DeployTarget{}, errors.New("deployment target is no longer present")
}

func sameLocalDeployTarget(left, right models.DeployTarget) bool {
	return left.ID == right.ID && left.Name == right.Name && left.RepoURL == right.RepoURL && left.Branch == right.Branch &&
		left.ProjectDir == right.ProjectDir && left.Environment == right.Environment && equalOptionalInt64(left.SourceTargetID, right.SourceTargetID) &&
		left.DeployKind == right.DeployKind && left.ComposeFile == right.ComposeFile && left.DeployScript == right.DeployScript &&
		left.WebhookProvider == right.WebhookProvider && left.WebhookStatus == right.WebhookStatus && left.AutoDeploy == right.AutoDeploy && left.IsActive == right.IsActive
}

func equalOptionalInt64(left, right *int64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func sameDeployPreflight(left, right models.DeployPreflight) bool {
	if left.TargetID != right.TargetID || left.DeploymentKind != right.DeploymentKind || left.Eligible != right.Eligible || len(left.Checks) != len(right.Checks) {
		return false
	}
	for index := range left.Checks {
		if left.Checks[index] != right.Checks[index] {
			return false
		}
	}
	return true
}

func sameDeployRevision(left, right models.DeployRevisionComparison) bool {
	return left.TargetID == right.TargetID && left.State == right.State && left.Branch == right.Branch &&
		left.CurrentCommit == right.CurrentCommit && left.DeployedCommit == right.DeployedCommit && left.RollbackCommit == right.RollbackCommit &&
		left.TrackedChanges == right.TrackedChanges && left.MatchesDeployed == right.MatchesDeployed && left.RollbackAvailable == right.RollbackAvailable &&
		left.CommitsAheadRollback == right.CommitsAheadRollback && left.CommitsBehindRollback == right.CommitsBehindRollback &&
		left.FilesChanged == right.FilesChanged && left.Insertions == right.Insertions && left.Deletions == right.Deletions
}

func findRemoteDeployTarget(targets []remotenodes.RemoteDeployTarget, id string) (remotenodes.RemoteDeployTarget, bool) {
	for _, target := range targets {
		if target.ID == id {
			return target, true
		}
	}
	return remotenodes.RemoteDeployTarget{}, false
}

func sameRemoteDeployTarget(left, right remotenodes.RemoteDeployTarget) bool {
	return left.ID == right.ID && left.Name == right.Name && left.Description == right.Description && left.Kind == right.Kind &&
		left.Path == right.Path && left.Status == right.Status && left.Eligible == right.Eligible && left.Reason == right.Reason &&
		slices.Equal(left.Actions, right.Actions) && left.Branch == right.Branch && left.Head == right.Head && left.Upstream == right.Upstream &&
		left.Remote == right.Remote && left.Dirty == right.Dirty && left.Ahead == right.Ahead && left.Behind == right.Behind &&
		left.RollbackAvailable == right.RollbackAvailable && left.RollbackCreatedAt == right.RollbackCreatedAt && left.HostPort == right.HostPort
}

func (model tuiModel) hasActiveDeployJobs() bool {
	for _, run := range model.deploy.Runs {
		if run.Status == models.DeployStatusPending || run.Status == models.DeployStatusRunning {
			return true
		}
	}
	for _, job := range model.deploy.RemoteJobs {
		if job.Status == agenthub.TaskStatusQueued || job.Status == agenthub.TaskStatusRunning {
			return true
		}
	}
	return false
}

func (model tuiModel) renderDeploy(width, height int) string {
	rows := []string{tuiTitleStyle.Render("Deployments") + tuiMutedStyle.Render("  G jump · Enter inspect/action · R reload")}
	if !model.deployLoaded {
		message := "Deployment inventory has not been loaded."
		if model.resourceLoading {
			message = "Loading deployment targets and recent jobs…"
		}
		rows = append(rows, tuiDimStyle.Render(message))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	state := model.deploy
	if !state.Supported {
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render(valueOrNA(state.Message)))
		return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
	}
	jobCount := len(state.Runs)
	targetCount := len(state.Targets)
	if !state.Local {
		jobCount = len(state.RemoteJobs)
		targetCount = len(state.RemoteTargets)
	}
	rows = append(rows, tuiDimStyle.Render(fmt.Sprintf("%d target(s) · %d recent job(s) · %s", targetCount, jobCount, model.snapshot.Selected.label())))
	visible := maxInt(4, height-6)
	total := jobCount + targetCount
	start, end := visibleRange(model.cursor, total, visible)
	for index := start; index < end; index++ {
		row := ""
		if index < jobCount {
			if state.Local {
				run := state.Runs[index]
				row = fmt.Sprintf("JOB  #%-6d %-9s %-10s target=%d · %s", run.ID, run.Status, run.Trigger, run.TargetID, shortDeployRevision(run.Commit))
			} else {
				job := state.RemoteJobs[index]
				row = fmt.Sprintf("JOB  %-12s %-10s %-10s %s · %s", truncateTUI(job.ID, 12), job.Status, job.Action, job.TargetID, job.Message)
			}
		} else if state.Local {
			target := state.Targets[index-jobCount]
			active := "inactive"
			if target.IsActive {
				active = "active"
			}
			row = fmt.Sprintf("APP  #%-6d %-24s %-10s %-8s %s@%s", target.ID, truncateTUI(target.Name, 24), target.Environment, active, target.DeployKind, valueOrNA(target.Branch))
		} else {
			target := state.RemoteTargets[index-jobCount]
			eligible := "blocked"
			if target.Eligible {
				eligible = "ready"
			}
			row = fmt.Sprintf("PLAN %-18s %-24s %-10s %-10s %s", truncateTUI(target.ID, 18), truncateTUI(target.Name, 24), target.Kind, eligible, strings.Join(target.Actions, ","))
		}
		rows = append(rows, renderSelectableRow(truncateTUI(row, width-3), index == model.cursor, width-2))
	}
	if total == 0 {
		rows = append(rows, tuiDimStyle.Render("No deployment target or recent job was reported."))
	}
	for _, warning := range state.Warnings {
		rows = append(rows, lipgloss.NewStyle().Foreground(tuiAmber).Render("! "+truncateTUI(warning, width-6)))
	}
	return tuiPanelStyle.Width(width - 2).Render(strings.Join(rows, "\n"))
}
