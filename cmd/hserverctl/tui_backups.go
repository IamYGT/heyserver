package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/IamYGT/heyserver/internal/agenthub"
	"github.com/IamYGT/heyserver/internal/models"
	"github.com/IamYGT/heyserver/internal/services/remotenodes"
)

type tuiBackupItem struct {
	ID         string
	Name       string
	Type       string
	Status     string
	Size       int64
	CreatedAt  time.Time
	Managed    bool
	Service    string
	Timer      string
	Enabled    string
	LastResult string
	LastRun    string
	NextRun    string
	Verified   bool
	FileCount  int
}

// tuiBackupSchedule is the read-only shape returned by the panel's schedule
// inventory endpoint.  Schedule mutation deliberately remains outside the
// TUI parity slice: RawLine is retained only as an observed identity for
// diagnostics and is never sent back to the server.
type tuiBackupSchedule struct {
	Cron                 string `json:"cron"`
	Frequency            string `json:"frequency"`
	Time                 string `json:"time"`
	Type                 string `json:"type"`
	Database             string `json:"database"`
	RetentionCount       int    `json:"retention_count"`
	RetentionCountLegacy int    `json:"retentionCount"`
	RetentionDaysLegacy  int    `json:"retention_days"`
	RawLine              string `json:"rawLine"`
}

type tuiBackupSchedulesResponse struct {
	Schedules []tuiBackupSchedule `json:"schedules"`
}

const (
	tuiBackupScheduleDisplayPrefix = "Schedule ·"
	tuiBackupScheduleStatePrefix   = "Schedules:"
)

type tuiBackupCreateSpec struct {
	Type      string
	Engine    string
	Retention int
	Vhosts    []string
}

func (spec tuiBackupCreateSpec) label() string {
	switch spec.Type {
	case "full":
		return "full application backup · " + valueOrNA(spec.Engine)
	case "database":
		return "all databases · " + valueOrNA(spec.Engine)
	case "files":
		return "all website files"
	default:
		return "backup"
	}
}

func backupCreateCoverage(spec tuiBackupCreateSpec) string {
	files := "all configured website files"
	if len(spec.Vhosts) > 0 {
		files = strings.Join(spec.Vhosts, ", ")
	}
	switch spec.Type {
	case "full":
		return "all " + valueOrNA(spec.Engine) + " databases + " + files
	case "database":
		return "all " + valueOrNA(spec.Engine) + " databases"
	case "files":
		return files
	default:
		return "unknown"
	}
}

type tuiBackupJob struct {
	ID         string    `json:"id"`
	Type       string    `json:"type"`
	Source     string    `json:"source"`
	Status     string    `json:"status"`
	Phase      string    `json:"phase"`
	Progress   int       `json:"progress"`
	Message    string    `json:"message"`
	Error      string    `json:"error,omitempty"`
	StartedAt  time.Time `json:"startedAt"`
	DoneAt     time.Time `json:"doneAt,omitempty"`
	ETASeconds int       `json:"etaSeconds,omitempty"`
	BytesDone  int64     `json:"bytesDone,omitempty"`
	BytesTotal int64     `json:"bytesTotal,omitempty"`
	OutputFile string    `json:"outputFile,omitempty"`
	Speed      string    `json:"speed,omitempty"`
	Logs       []string  `json:"logs,omitempty"`
}

func (job tuiBackupJob) active() bool {
	status := strings.ToLower(strings.TrimSpace(job.Status))
	return status == "pending" || status == "running"
}

type tuiBackupStorage struct {
	TotalBytes          int64
	CompletedBytes      int64
	InvalidBytes        int64
	OrphanedBytes       int64
	BackupAvailable     uint64
	BackupUsePercentage float64
}

type tuiBackupTargets struct {
	Vhosts            []string `json:"vhosts"`
	MaxSelectedVhosts int      `json:"maxSelectedVhosts"`
	EmptySelection    string   `json:"emptySelection"`
}

type tuiBackupsMsg struct {
	TargetID string
	Items    []tuiBackupItem
	Jobs     []tuiBackupJob
	Storage  tuiBackupStorage
	Targets  tuiBackupTargets
	Warnings []string
	Err      error
}

type tuiBackupJobMsg struct {
	TargetID string
	Job      tuiBackupJob
	Err      error
}

type tuiBackupValidation struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Type             string `json:"type"`
	ArtifactBytes    int64  `json:"artifactBytes"`
	IncludesDatabase bool   `json:"includesDatabase"`
	IncludesFiles    bool   `json:"includesFiles"`
	DatabaseEngine   string `json:"databaseEngine,omitempty"`
	DatabaseTarget   string `json:"databaseTarget,omitempty"`
	DatabaseRecovery bool   `json:"databaseRecovery"`
	FilesRollback    bool   `json:"filesRollback"`
}

type tuiBackupValidationMsg struct {
	TargetID   string
	Item       tuiBackupItem
	Validation tuiBackupValidation
	Err        error
}

func loadTUIBackupsCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		items, jobs, storage, warnings, err := loadTUIBackups(ctx, client, target)
		targets := tuiBackupTargets{}
		if err == nil && target.Local {
			var targetErr error
			targets, targetErr = loadTUIBackupTargets(ctx, client)
			if targetErr != nil {
				warnings = append(warnings, "Selective backup targets unavailable: "+targetErr.Error())
			}
		}
		return tuiBackupsMsg{TargetID: target.ID, Items: items, Jobs: jobs, Storage: storage, Targets: targets, Warnings: warnings, Err: err}
	}
}

func loadTUIBackups(ctx context.Context, client *apiClient, target tuiTarget) ([]tuiBackupItem, []tuiBackupJob, tuiBackupStorage, []string, error) {
	if !target.Local {
		if !target.Online {
			return nil, nil, tuiBackupStorage{}, nil, errors.New("managed node is offline")
		}
		if !target.capability(agenthub.CapabilityBackupRead) {
			return nil, nil, tuiBackupStorage{}, nil, errors.New("managed agent does not advertise backup.read")
		}
		endpoint := "/api/nodes/" + url.PathEscape(target.ID) + "/backups"
		plans, err := requestJSON[[]remotenodes.RemoteBackupPlan](ctx, client.withTimeout(3*time.Minute), http.MethodGet, endpoint, nil, true)
		if err != nil {
			return nil, nil, tuiBackupStorage{}, nil, err
		}
		sort.Slice(plans, func(i, j int) bool { return strings.ToLower(plans[i].Name) < strings.ToLower(plans[j].Name) })
		items := make([]tuiBackupItem, 0, len(plans))
		for _, plan := range plans {
			status := plan.LastResult
			if status == "" || status == "unknown" {
				status = plan.Active
			}
			items = append(items, tuiBackupItem{
				ID: plan.ID, Name: plan.Name, Type: "plan", Status: status, Size: plan.TotalSize,
				Managed: true, Service: plan.Service, Timer: plan.Timer, Enabled: plan.Enabled,
				LastResult: plan.LastResult, LastRun: plan.LastRun, NextRun: plan.NextRun,
				Verified: plan.Verified, FileCount: len(plan.Files),
			})
		}
		return items, nil, tuiBackupStorage{}, nil, nil
	}

	response, err := requestJSON[struct {
		Backups []models.BackupInfo `json:"backups"`
		Storage struct {
			TotalBytes             int64   `json:"totalBytes"`
			CompletedBytes         int64   `json:"completedBytes"`
			InvalidBytes           int64   `json:"invalidBytes"`
			OrphanedBytes          int64   `json:"orphanedBytes"`
			BackupVolumeAvailable  uint64  `json:"backupVolumeAvailable"`
			BackupVolumeUsePercent float64 `json:"backupVolumeUsePercent"`
		} `json:"storage"`
	}](ctx, client.withTimeout(3*time.Minute), http.MethodGet, "/api/backups", nil, true)
	if err != nil {
		return nil, nil, tuiBackupStorage{}, nil, err
	}
	sort.Slice(response.Backups, func(i, j int) bool { return response.Backups[i].CreatedAt.After(response.Backups[j].CreatedAt) })
	items := make([]tuiBackupItem, 0, len(response.Backups))
	for _, backup := range response.Backups {
		items = append(items, tuiBackupItem{
			ID: backup.ID, Name: backup.Name, Type: backup.Type, Status: backup.Status,
			Size: backup.Size, CreatedAt: backup.CreatedAt,
		})
	}
	storage := tuiBackupStorage{
		TotalBytes: response.Storage.TotalBytes, CompletedBytes: response.Storage.CompletedBytes,
		InvalidBytes: response.Storage.InvalidBytes, OrphanedBytes: response.Storage.OrphanedBytes,
		BackupAvailable: response.Storage.BackupVolumeAvailable, BackupUsePercentage: response.Storage.BackupVolumeUsePercent,
	}
	jobResponse, jobErr := requestJSON[struct {
		Jobs []tuiBackupJob `json:"jobs"`
	}](ctx, client.withTimeout(3*time.Minute), http.MethodGet, "/api/backups/jobs?hours=24", nil, true)
	warnings := []string(nil)
	if jobErr != nil {
		warnings = append(warnings, "Backup job history unavailable: "+jobErr.Error())
	} else {
		sort.SliceStable(jobResponse.Jobs, func(i, j int) bool {
			return jobResponse.Jobs[i].StartedAt.After(jobResponse.Jobs[j].StartedAt)
		})
	}

	// Schedule inventory is intentionally best-effort.  A schedule reader
	// outage must not turn a healthy artifact/job inventory into an error or
	// cause the TUI to discard the data it already loaded above.
	schedules, scheduleErr := loadTUIBackupSchedules(ctx, client)
	if scheduleErr != nil {
		warnings = append(warnings, formatTUIBackupScheduleError(scheduleErr))
	} else {
		warnings = append(warnings, formatTUIBackupScheduleInventory(schedules)...)
	}
	return items, jobResponse.Jobs, storage, warnings, nil
}

func loadTUIBackupSchedules(ctx context.Context, client *apiClient) ([]tuiBackupSchedule, error) {
	response, err := requestJSON[tuiBackupSchedulesResponse](ctx, client.withTimeout(30*time.Second), http.MethodGet, "/api/backups/schedules", nil, true)
	if err != nil {
		return nil, err
	}
	if response.Schedules == nil {
		response.Schedules = []tuiBackupSchedule{}
	}
	sort.SliceStable(response.Schedules, func(i, j int) bool {
		left, right := response.Schedules[i], response.Schedules[j]
		leftKey := strings.ToLower(strings.Join([]string{left.Frequency, left.Time, left.Type, left.Database, left.Cron}, "\x00"))
		rightKey := strings.ToLower(strings.Join([]string{right.Frequency, right.Time, right.Type, right.Database, right.Cron}, "\x00"))
		return leftKey < rightKey
	})
	return response.Schedules, nil
}

func (schedule tuiBackupSchedule) retentionCount() int {
	if schedule.RetentionCount > 0 {
		return schedule.RetentionCount
	}
	if schedule.RetentionCountLegacy > 0 {
		return schedule.RetentionCountLegacy
	}
	return schedule.RetentionDaysLegacy
}

func (schedule tuiBackupSchedule) databaseLabel() string {
	database := strings.TrimSpace(schedule.Database)
	if database != "" {
		return database
	}
	switch strings.ToLower(strings.TrimSpace(schedule.Type)) {
	case "files", "snapshot":
		return "not applicable"
	default:
		return "all"
	}
}

func formatTUIBackupSchedule(schedule tuiBackupSchedule) string {
	frequency := strings.TrimSpace(schedule.Frequency)
	timeValue := strings.TrimSpace(schedule.Time)
	cron := strings.TrimSpace(schedule.Cron)
	if frequency == "" && cron != "" {
		frequency = "custom"
		// A custom cron expression has no honest HH:MM preset representation.
		timeValue = "not represented"
	}
	if frequency == "" {
		frequency = "not reported"
	}
	if timeValue == "" {
		timeValue = "not reported"
	}
	typeValue := strings.TrimSpace(schedule.Type)
	if typeValue == "" {
		typeValue = "not reported"
	}
	retention := "not reported"
	if count := schedule.retentionCount(); count > 0 {
		retention = fmt.Sprintf("%d", count)
	}
	line := fmt.Sprintf("%s frequency=%s · time=%s · type=%s · database=%s · retention=%s", tuiBackupScheduleDisplayPrefix, frequency, timeValue, typeValue, schedule.databaseLabel(), retention)
	if frequency == "custom" {
		line += " · cron=" + cron
	}
	return line
}

func formatTUIBackupScheduleInventory(schedules []tuiBackupSchedule) []string {
	if len(schedules) == 0 {
		return []string{tuiBackupScheduleStatePrefix + " empty — not configured; no backup schedules configured."}
	}
	warnings := make([]string, 0, len(schedules))
	for _, schedule := range schedules {
		warnings = append(warnings, formatTUIBackupSchedule(schedule))
	}
	return warnings
}

func formatTUIBackupScheduleError(err error) string {
	state := "unavailable"
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	var apiErr *apiError
	if errors.As(err, &apiErr) && strings.EqualFold(strings.TrimSpace(apiErr.State), "not_configured") {
		state = "not configured"
	}
	if strings.Contains(message, "not configured") || strings.Contains(message, "not installed") {
		state = "not configured"
	}
	return fmt.Sprintf("%s %s — %s", tuiBackupScheduleStatePrefix, state, err.Error())
}

func loadTUIBackupTargets(ctx context.Context, client *apiClient) (tuiBackupTargets, error) {
	targets, err := requestJSON[tuiBackupTargets](ctx, client.withTimeout(3*time.Minute), http.MethodGet, "/api/backups/targets", nil, true)
	if err != nil {
		return tuiBackupTargets{}, err
	}
	return normalizeTUIBackupTargets(targets)
}

func normalizeTUIBackupTargets(targets tuiBackupTargets) (tuiBackupTargets, error) {
	if targets.EmptySelection != "all-configured-vhosts" {
		return tuiBackupTargets{}, errors.New("server returned an unsupported empty-selection contract")
	}
	if targets.MaxSelectedVhosts < 1 {
		return tuiBackupTargets{}, errors.New("server returned an invalid selection limit")
	}
	if len(targets.Vhosts) > 4096 {
		return tuiBackupTargets{}, errors.New("server returned too many observed vhost identities")
	}
	seen := make(map[string]struct{}, len(targets.Vhosts))
	for _, value := range targets.Vhosts {
		normalized, err := validateBackupIdentity("vhost", value)
		if err != nil || normalized != value {
			return tuiBackupTargets{}, errors.New("server returned an invalid observed vhost identity")
		}
		if _, exists := seen[normalized]; exists {
			return tuiBackupTargets{}, fmt.Errorf("server returned duplicate vhost identity %q", normalized)
		}
		seen[normalized] = struct{}{}
	}
	sort.SliceStable(targets.Vhosts, func(i, j int) bool {
		return strings.ToLower(targets.Vhosts[i]) < strings.ToLower(targets.Vhosts[j])
	})
	targets.MaxSelectedVhosts = minInt(targets.MaxSelectedVhosts, 16)
	return targets, nil
}

func loadTUIBackupJobCmd(ctx context.Context, client *apiClient, target tuiTarget, job tuiBackupJob) tea.Cmd {
	return func() tea.Msg {
		if !target.Local {
			return tuiBackupJobMsg{TargetID: target.ID, Err: errors.New("backup job history is local to the panel host")}
		}
		jobID, err := validateBackupIdentity("backup job", job.ID)
		if err != nil {
			return tuiBackupJobMsg{TargetID: target.ID, Err: err}
		}
		current, err := requestJSON[tuiBackupJob](ctx, client.withTimeout(3*time.Minute), http.MethodGet,
			"/api/backups/jobs/"+url.PathEscape(jobID), nil, true)
		return tuiBackupJobMsg{TargetID: target.ID, Job: current, Err: err}
	}
}

func loadTUIBackupValidationCmd(ctx context.Context, client *apiClient, target tuiTarget, item tuiBackupItem) tea.Cmd {
	return func() tea.Msg {
		validation, err := requestJSON[tuiBackupValidation](ctx, client.withTimeout(20*time.Minute), http.MethodGet,
			"/api/backups/restore/"+url.PathEscape(item.ID)+"/validate", nil, true)
		return tuiBackupValidationMsg{TargetID: target.ID, Item: item, Validation: validation, Err: err}
	}
}

func formatBackupSchedule(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "unknown" {
		return "not reported"
	}
	return value
}

func formatBackupCreatedAt(value time.Time) string {
	if value.IsZero() {
		return "time not reported"
	}
	return value.UTC().Format("2006-01-02 15:04Z")
}

func backupValidationSummary(validation tuiBackupValidation) string {
	parts := []string{strings.ToUpper(valueOrNA(validation.Type)), formatTUIBytes(uint64(maxInt64(validation.ArtifactBytes, 0)))}
	if validation.IncludesDatabase {
		parts = append(parts, "database "+valueOrNA(validation.DatabaseEngine)+"/"+valueOrNA(validation.DatabaseTarget))
	}
	if validation.IncludesFiles {
		parts = append(parts, "files")
	}
	return strings.Join(parts, " · ")
}

func backupJobLogLines(job tuiBackupJob) []string {
	progress := minInt(100, maxInt(0, job.Progress))
	lines := []string{
		fmt.Sprintf("Status: %s · phase: %s · progress: %d%%", valueOrNA(job.Status), valueOrNA(job.Phase), progress),
		fmt.Sprintf("Type: %s · source: %s · started: %s", valueOrNA(job.Type), valueOrNA(job.Source), formatBackupCreatedAt(job.StartedAt)),
	}
	if job.BytesTotal > 0 || job.BytesDone > 0 {
		lines = append(lines, fmt.Sprintf("Transferred: %s / %s · speed: %s", formatTUIBytes(uint64(maxInt64(job.BytesDone, 0))), formatTUIBytes(uint64(maxInt64(job.BytesTotal, 0))), valueOrNA(job.Speed)))
	}
	if job.ETASeconds > 0 && job.active() {
		lines = append(lines, "ETA: "+compactDuration(time.Duration(job.ETASeconds)*time.Second))
	}
	if strings.TrimSpace(job.Message) != "" {
		lines = append(lines, "Message: "+job.Message)
	}
	if strings.TrimSpace(job.Error) != "" {
		lines = append(lines, "Error: "+job.Error)
	}
	if strings.TrimSpace(job.OutputFile) != "" {
		lines = append(lines, "Output: "+job.OutputFile)
	}
	if len(job.Logs) > 0 {
		lines = append(lines, "", "Job log:")
		lines = append(lines, job.Logs...)
	}
	return lines
}

func backupCreateSpecForAction(action string) (tuiBackupCreateSpec, bool) {
	switch action {
	case "create-full-postgresql":
		return tuiBackupCreateSpec{Type: "full", Engine: "postgresql"}, true
	case "create-full-mariadb":
		return tuiBackupCreateSpec{Type: "full", Engine: "mariadb"}, true
	case "create-database-postgresql":
		return tuiBackupCreateSpec{Type: "database", Engine: "postgresql"}, true
	case "create-database-mariadb":
		return tuiBackupCreateSpec{Type: "database", Engine: "mariadb"}, true
	case "create-files":
		return tuiBackupCreateSpec{Type: "files", Engine: "postgresql"}, true
	default:
		return tuiBackupCreateSpec{}, false
	}
}

func backupRetentionForAction(action string) (int, bool) {
	switch action {
	case "retention-0":
		return 0, true
	case "retention-7":
		return 7, true
	case "retention-14":
		return 14, true
	case "retention-30":
		return 30, true
	default:
		return 0, false
	}
}

func validateTUIBackupCreateSpec(spec tuiBackupCreateSpec) error {
	if spec.Type != "full" && spec.Type != "database" && spec.Type != "files" {
		return errors.New("backup type must be full, database, or files")
	}
	if spec.Engine != "postgresql" && spec.Engine != "mariadb" {
		return errors.New("backup engine must be postgresql or mariadb")
	}
	if spec.Retention < 0 || spec.Retention > 365 {
		return errors.New("backup retention must be between 0 and 365")
	}
	if spec.Type != "full" && spec.Retention != 0 {
		return errors.New("retention cleanup is available only for full backups")
	}
	if err := validateUniqueBackupIdentities("vhost", spec.Vhosts, 16); err != nil {
		return err
	}
	return nil
}

func backupOperationMessage(response map[string]any, fallback string) string {
	message := fallback
	if value, ok := response["message"].(string); ok && strings.TrimSpace(value) != "" {
		message = strings.TrimSpace(value)
	}
	if jobID, ok := response["jobId"].(string); ok && strings.TrimSpace(jobID) != "" {
		message += " · job " + strings.TrimSpace(jobID)
	}
	return message
}

func validateTUIBackupItem(item tuiBackupItem) error {
	if _, err := validateBackupIdentity("backup", item.ID); err != nil {
		return err
	}
	if strings.TrimSpace(item.Name) == "" {
		return fmt.Errorf("backup resource name is empty")
	}
	return nil
}

func runTUIBackupOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	item := operation.Backup
	switch operation.Action {
	case "create":
		if !operation.Target.Local {
			return "", errors.New("managed nodes run installation-owned backup plans")
		}
		spec := operation.BackupCreate
		if err := validateTUIBackupCreateSpec(spec); err != nil {
			return "", err
		}
		response, err := requestJSON[map[string]any](ctx, client.withTimeout(2*time.Minute), http.MethodPost, "/api/backups", map[string]any{
			"type": spec.Type, "name": "", "engine": spec.Engine, "database": "",
			"compression": 6, "retention": spec.Retention, "vhosts": append([]string{}, spec.Vhosts...),
		}, true)
		if err != nil {
			return "", err
		}
		return backupOperationMessage(response, "Backup started"), nil
	case "run":
		if operation.Target.Local || !item.Managed {
			return "", errors.New("backup plan execution requires a managed plan")
		}
		if !operation.Target.capability(agenthub.CapabilityBackupRun) {
			return "", errors.New("managed agent does not advertise backup.run")
		}
		if err := validateTUIBackupItem(item); err != nil {
			return "", err
		}
		endpoint := "/api/nodes/" + url.PathEscape(operation.Target.ID) + "/backups/" + url.PathEscape(item.ID) + "/run"
		response, err := requestJSON[map[string]any](ctx, client.withTimeout(22*time.Minute), http.MethodPost, endpoint, nil, true)
		if err != nil {
			return "", err
		}
		return backupOperationMessage(response, "Backup plan completed"), nil
	case "delete":
		if !operation.Target.Local || item.Managed {
			return "", errors.New("managed backup plans cannot be deleted from the control center")
		}
		if err := validateTUIBackupItem(item); err != nil {
			return "", err
		}
		response, err := requestJSON[map[string]any](ctx, client.withTimeout(2*time.Minute), http.MethodDelete,
			"/api/backups/"+url.PathEscape(item.ID), nil, true)
		if err != nil {
			return "", err
		}
		return backupOperationMessage(response, "Backup deleted"), nil
	case "restore":
		if !operation.Target.Local || item.Managed {
			return "", errors.New("managed backup plans cannot be restored from the control center")
		}
		if err := validateTUIBackupItem(item); err != nil {
			return "", err
		}
		validation := operation.BackupValidation
		if validation.ID != item.ID {
			return "", errors.New("backup restore requires the current validation receipt")
		}
		if validation.IncludesDatabase && !validation.DatabaseRecovery {
			return "", errors.New("backup validation does not declare database recovery")
		}
		if validation.IncludesFiles && !validation.FilesRollback {
			return "", errors.New("backup validation does not declare files rollback")
		}
		response, err := requestJSON[map[string]any](ctx, client.withTimeout(2*time.Minute), http.MethodPost,
			"/api/backups/restore/"+url.PathEscape(item.ID), nil, true)
		if err != nil {
			return "", err
		}
		return backupOperationMessage(response, "Restore started"), nil
	default:
		return "", fmt.Errorf("unsupported backup TUI action %q", operation.Action)
	}
}
