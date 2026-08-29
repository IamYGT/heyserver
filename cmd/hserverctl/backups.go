package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var backupIdentityPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,252}$`)
var snapshotIdentityPattern = regexp.MustCompile(`^[A-Fa-f0-9]{8,64}$`)
var snapshotRepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
var backupScheduleCronPattern = regexp.MustCompile(`^(\*|[0-9]+(?:[,\-/][0-9]+)*)(\s+(\*|[0-9]+(?:[,\-/][0-9]+)*)){4}$`)
var backupScheduleTimePattern = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)

var snapshotRestoreManifest = map[string]struct{}{
	"vhosts": {}, "nginx": {}, "letsencrypt": {}, "postgresql-cfg": {},
	"mysql-cfg": {}, "php": {}, "hserver-data": {}, "cron-d": {}, "systemd": {},
}

func runBackups(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl backups list|create|run|jobs|job|validate|restore|delete|schedule|snapshot")
	}
	switch args[0] {
	case "list":
		node, err := parseOptionalNode("backups list", args[1:])
		if err != nil {
			return err
		}
		endpoint := "/api/backups"
		if node != "" {
			endpoint = "/api/nodes/" + url.PathEscape(node) + "/backups"
		}
		return printRequest(ctx, client.withTimeout(3*time.Minute), out, http.MethodGet, endpoint, nil, true)
	case "create":
		return runBackupCreate(ctx, client, args[1:], out)
	case "run":
		return runManagedBackup(ctx, client, args[1:], out)
	case "jobs":
		return runBackupJobs(ctx, client, args[1:], out)
	case "job":
		if len(args) != 2 {
			return errors.New("usage: hserverctl backups job JOB_ID")
		}
		jobID, err := validateBackupIdentity("job", args[1])
		if err != nil {
			return err
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/backups/jobs/"+url.PathEscape(jobID), nil, true)
	case "validate":
		if len(args) != 2 {
			return errors.New("usage: hserverctl backups validate BACKUP_ID")
		}
		backupID, err := validateBackupIdentity("backup", args[1])
		if err != nil {
			return err
		}
		return printRequest(ctx, client.withTimeout(20*time.Minute), out, http.MethodGet, "/api/backups/restore/"+url.PathEscape(backupID)+"/validate", nil, true)
	case "restore":
		return runBackupRestore(ctx, client, args[1:], out)
	case "delete":
		return runBackupDelete(ctx, client, args[1:], out)
	case "schedule":
		return runBackupSchedule(ctx, client, args[1:], out)
	case "snapshot":
		return runSnapshotBackups(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown backups command %q", args[0])
	}
}

func runBackupSchedule(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl backups schedule list|set|delete")
	}
	switch args[0] {
	case "list":
		if len(args) != 1 {
			return errors.New("usage: hserverctl backups schedule list")
		}
		return printRequest(ctx, client, out, http.MethodGet, "/api/backups/schedules", nil, true)
	case "set":
		return runBackupScheduleSet(ctx, client, args[1:], out)
	case "delete":
		return runBackupScheduleDelete(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown backups schedule command %q", args[0])
	}
}

func runBackupScheduleSet(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("backups schedule set", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm replacement of the local backup schedule")
	cron := flags.String("cron", "", "standard five-field cron expression")
	frequency := flags.String("frequency", "", "preset frequency: daily, weekly, or monthly")
	scheduleTime := flags.String("time", "", "preset schedule time in HH:MM")
	backupType := flags.String("type", "", "backup type: full, database, files, or snapshot")
	database := flags.String("database", "", "optional database identity")
	retentionCount := flags.Int("retention-count", 10, "completed backups to retain, from 1 to 365")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl backups schedule set --confirm (--cron EXPR|--frequency daily|weekly|monthly --time HH:MM) [--type full|database|files|snapshot] [--database NAME] [--retention-count 1-365]")
	}
	if !*confirmed {
		return errors.New("backup schedule replacement requires explicit --confirm")
	}
	if *retentionCount < 1 || *retentionCount > 365 {
		return errors.New("backup schedule retention count must be between 1 and 365")
	}

	cronValue := strings.TrimSpace(*cron)
	frequencyValue := strings.ToLower(strings.TrimSpace(*frequency))
	timeValue := strings.TrimSpace(*scheduleTime)
	if cronValue != "" {
		if frequencyValue != "" || timeValue != "" {
			return errors.New("backup schedule must use either --cron or --frequency with --time, not both")
		}
		if !backupScheduleCronPattern.MatchString(cronValue) {
			return errors.New("backup schedule cron must be a standard five-field expression")
		}
	} else {
		if frequencyValue == "" || timeValue == "" {
			return errors.New("backup schedule requires --cron or both --frequency and --time")
		}
		switch frequencyValue {
		case "daily", "weekly", "monthly":
		default:
			return errors.New("backup schedule frequency must be daily, weekly, or monthly")
		}
		if !backupScheduleTimePattern.MatchString(timeValue) {
			return errors.New("backup schedule time must use HH:MM in the 00:00-23:59 range")
		}
	}

	typeValue := strings.ToLower(strings.TrimSpace(*backupType))
	if typeValue != "" {
		switch typeValue {
		case "full", "database", "files", "snapshot":
		default:
			return errors.New("backup schedule type must be full, database, files, or snapshot")
		}
	}
	databaseValue := strings.TrimSpace(*database)
	if databaseValue != "" {
		var err error
		databaseValue, err = validateBackupIdentity("database", databaseValue)
		if err != nil {
			return err
		}
	}

	payload := map[string]any{
		"retention_count": *retentionCount,
	}
	if cronValue != "" {
		payload["cron"] = cronValue
	} else {
		payload["frequency"] = frequencyValue
		payload["time"] = timeValue
	}
	if typeValue != "" {
		payload["type"] = typeValue
	}
	if databaseValue != "" {
		payload["database"] = databaseValue
	}
	return printRequest(ctx, client, out, http.MethodPost, "/api/backups/schedules", payload, true)
}

func runBackupScheduleDelete(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("backups schedule delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm deletion of the observed local backup schedule")
	rawLine := flags.String("raw-line", "", "exact rawLine returned by backups schedule list")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl backups schedule delete --confirm --raw-line LINE")
	}
	if !*confirmed {
		return errors.New("backup schedule deletion requires explicit --confirm")
	}
	rawLineValue, err := validateBackupScheduleRawLine(*rawLine)
	if err != nil {
		return err
	}
	return printRequest(ctx, client, out, http.MethodDelete, "/api/backups/schedules", map[string]string{"rawLine": rawLineValue}, true)
}

func validateBackupScheduleRawLine(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("backup schedule --raw-line is required")
	}
	if strings.ContainsAny(value, "\r\n\x00") {
		return "", errors.New("backup schedule --raw-line must be a single line")
	}
	if !strings.HasSuffix(value, "# hserver-backup") {
		return "", errors.New("backup schedule --raw-line must be an observed hserver-backup line")
	}
	fields := strings.Fields(value)
	if len(fields) < 6 || !backupScheduleCronPattern.MatchString(strings.Join(fields[:5], " ")) {
		return "", errors.New("backup schedule --raw-line must begin with a standard five-field cron expression")
	}
	return value, nil
}

type cliSnapshotSettings struct {
	Destination          string   `json:"destination"`
	RepoFolder           string   `json:"repoFolder"`
	EnabledPaths         []string `json:"enabledPaths"`
	KeepDaily            int      `json:"keepDaily"`
	KeepWeekly           int      `json:"keepWeekly"`
	KeepMonthly          int      `json:"keepMonthly"`
	PasswordAcknowledged bool     `json:"passwordAcknowledged"`
}

type cliSnapshotStatus struct {
	CanPurgeRepository bool                `json:"canPurgeRepository"`
	Settings           cliSnapshotSettings `json:"settings"`
}

func runSnapshotBackups(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: hserverctl backups snapshot status|list|vhosts|run|restore|destination|purge")
	}
	switch args[0] {
	case "status":
		if len(args) != 1 {
			return errors.New("usage: hserverctl backups snapshot status")
		}
		return printRequest(ctx, client.withTimeout(3*time.Minute), out, http.MethodGet, "/api/backups/snapshot/status", nil, true)
	case "list":
		if len(args) != 1 {
			return errors.New("usage: hserverctl backups snapshot list")
		}
		return printRequest(ctx, client.withTimeout(3*time.Minute), out, http.MethodGet, "/api/backups/snapshot/list", nil, true)
	case "vhosts":
		if len(args) != 1 {
			return errors.New("usage: hserverctl backups snapshot vhosts")
		}
		return printRequest(ctx, client.withTimeout(3*time.Minute), out, http.MethodGet, "/api/backups/snapshot/vhosts", nil, true)
	case "run":
		flags := flag.NewFlagSet("backups snapshot run", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		confirmed := flags.Bool("confirm", false, "confirm creation of an encrypted remote snapshot")
		wait := flags.Duration("wait", 2*time.Minute, "maximum snapshot-start request wait")
		if err := flags.Parse(args[1:]); err != nil {
			return err
		}
		if len(flags.Args()) != 0 {
			return errors.New("usage: hserverctl backups snapshot run --confirm [--wait DURATION]")
		}
		if !*confirmed {
			return errors.New("snapshot creation requires explicit --confirm")
		}
		if *wait <= 0 {
			return errors.New("snapshot start wait must be greater than zero")
		}
		return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, "/api/backups/snapshot/run", nil, true)
	case "restore":
		return runSnapshotRestore(ctx, client, args[1:], out)
	case "destination":
		if len(args) != 2 {
			return errors.New("usage: hserverctl backups snapshot destination gdrive|s3")
		}
		destination := strings.ToLower(strings.TrimSpace(args[1]))
		if destination != "gdrive" && destination != "s3" {
			return errors.New("snapshot destination must be gdrive or s3")
		}
		settings, err := requestJSON[cliSnapshotSettings](ctx, client, http.MethodGet, "/api/backups/snapshot/settings", nil, true)
		if err != nil {
			return err
		}
		settings.Destination = destination
		return printRequest(ctx, client, out, http.MethodPut, "/api/backups/snapshot/settings", settings, true)
	case "purge":
		return runSnapshotPurge(ctx, client, args[1:], out)
	default:
		return fmt.Errorf("unknown backups snapshot command %q", args[0])
	}
}

func runSnapshotRestore(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("backups snapshot restore", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm extraction from the encrypted remote snapshot")
	restoreAll := flags.Bool("all", false, "extract every path represented by the snapshot")
	wait := flags.Duration("wait", 2*time.Minute, "maximum restore-start request wait")
	var manifestIDs stringValues
	var vhosts stringValues
	flags.Var(&manifestIDs, "manifest", "fixed snapshot manifest identity (repeatable)")
	flags.Var(&vhosts, "vhost", "observed portable vhost identity (repeatable, maximum 16)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl backups snapshot restore --confirm (--all|--manifest ID...|--vhost NAME...) [--wait DURATION] SNAPSHOT_ID")
	}
	if !*confirmed {
		return errors.New("snapshot restore requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("snapshot restore wait must be greater than zero")
	}
	snapshotID := strings.TrimSpace(flags.Args()[0])
	if !snapshotIdentityPattern.MatchString(snapshotID) {
		return errors.New("snapshot identity must be an 8-64 character hexadecimal value")
	}
	if *restoreAll && (len(manifestIDs) > 0 || len(vhosts) > 0) {
		return errors.New("snapshot restore --all cannot be combined with --manifest or --vhost")
	}
	if !*restoreAll && len(manifestIDs) == 0 && len(vhosts) == 0 {
		return errors.New("snapshot restore requires --all or at least one --manifest or --vhost selector")
	}
	if len(manifestIDs) > len(snapshotRestoreManifest) {
		return fmt.Errorf("snapshot restore accepts at most %d manifest identities", len(snapshotRestoreManifest))
	}
	seenManifest := make(map[string]struct{}, len(manifestIDs))
	for index, value := range manifestIDs {
		value = strings.TrimSpace(value)
		if _, ok := snapshotRestoreManifest[value]; !ok {
			return fmt.Errorf("snapshot restore manifest identity %q is not selectable", value)
		}
		if _, exists := seenManifest[value]; exists {
			return fmt.Errorf("duplicate snapshot manifest identity %q", value)
		}
		manifestIDs[index] = value
		seenManifest[value] = struct{}{}
	}
	if err := validateUniqueBackupIdentities("vhost", vhosts, 16); err != nil {
		return err
	}
	if _, allVhosts := seenManifest["vhosts"]; allVhosts && len(vhosts) > 0 {
		return errors.New("choose either the vhosts manifest or specific --vhost selectors")
	}
	payload := map[string]any{
		"snapshotId":  snapshotID,
		"manifestIds": []string(manifestIDs),
		"vhosts":      []string(vhosts),
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, "/api/backups/snapshot/restore", payload, true)
}

func runSnapshotPurge(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("backups snapshot purge", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm permanent deletion of the complete remote snapshot repository")
	repository := flags.String("repository", "", "exact currently observed snapshot repository folder")
	wait := flags.Duration("wait", 12*time.Minute, "maximum repository purge wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl backups snapshot purge --confirm --repository REPO_FOLDER [--wait DURATION]")
	}
	if !*confirmed {
		return errors.New("snapshot repository purge requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("snapshot repository purge wait must be greater than zero")
	}
	repositoryValue := strings.TrimSpace(*repository)
	if !snapshotRepositoryPattern.MatchString(repositoryValue) || path.Clean(repositoryValue) != repositoryValue {
		return errors.New("snapshot repository must be the exact portable relative repository folder")
	}
	status, err := requestJSON[cliSnapshotStatus](ctx, client.withTimeout(3*time.Minute), http.MethodGet, "/api/backups/snapshot/status?refresh=1", nil, true)
	if err != nil {
		return err
	}
	if !status.CanPurgeRepository {
		return errors.New("selected snapshot destination does not support repository purge")
	}
	if repositoryValue != status.Settings.RepoFolder {
		return fmt.Errorf("snapshot repository confirmation %q does not match observed repository %q", repositoryValue, status.Settings.RepoFolder)
	}
	payload := map[string]string{
		"repoFolder":   repositoryValue,
		"confirmation": "purge-snapshot-repository",
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, "/api/backups/snapshot/purge-repo", payload, true)
}

func runBackupRestore(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("backups restore", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm replacement of data from one local backup")
	validated := flags.Bool("validated", false, "acknowledge review of the current backups validate receipt")
	wait := flags.Duration("wait", 2*time.Minute, "maximum restore-start request wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl backups restore --confirm --validated [--wait DURATION] BACKUP_ID")
	}
	if !*confirmed {
		return errors.New("backup restore requires explicit --confirm")
	}
	if !*validated {
		return errors.New("backup restore requires backups validate BACKUP_ID and explicit --validated acknowledgement")
	}
	if *wait <= 0 {
		return errors.New("backup restore wait must be greater than zero")
	}
	backupID, err := validateBackupIdentity("backup", flags.Args()[0])
	if err != nil {
		return err
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, "/api/backups/restore/"+url.PathEscape(backupID), nil, true)
}

func runBackupCreate(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("backups create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm creation of a local backup")
	backupType := flags.String("type", "full", "backup type: full, database, or files")
	name := flags.String("name", "", "portable optional backup name")
	engine := flags.String("engine", "postgresql", "database engine: postgresql or mariadb")
	database := flags.String("database", "", "optional database identity; empty means all")
	compression := flags.Int("compression", 6, "gzip compression level from 1 to 9")
	retention := flags.Int("retention", 0, "completed backups to retain; zero disables retention cleanup")
	wait := flags.Duration("wait", 2*time.Minute, "maximum create request wait")
	var vhosts stringValues
	flags.Var(&vhosts, "vhost", "observed portable vhost identity (repeatable, maximum 16)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl backups create --confirm [--type full|database|files] [--name NAME] [--engine postgresql|mariadb] [--database NAME] [--compression 1-9] [--retention 0-365] [--vhost NAME]...")
	}
	if !*confirmed {
		return errors.New("backup creation requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("backup create wait must be greater than zero")
	}
	typeValue := strings.ToLower(strings.TrimSpace(*backupType))
	if typeValue != "full" && typeValue != "database" && typeValue != "files" {
		return errors.New("backup type must be full, database, or files")
	}
	engineValue := strings.ToLower(strings.TrimSpace(*engine))
	if engineValue != "postgresql" && engineValue != "mariadb" {
		return errors.New("backup engine must be postgresql or mariadb")
	}
	if *compression < 1 || *compression > 9 {
		return errors.New("backup compression must be between 1 and 9")
	}
	if *retention < 0 || *retention > 365 {
		return errors.New("backup retention must be between 0 and 365")
	}
	nameValue := strings.TrimSpace(*name)
	if nameValue != "" {
		var err error
		nameValue, err = validateBackupIdentity("backup name", nameValue)
		if err != nil {
			return err
		}
	}
	databaseValue := strings.TrimSpace(*database)
	if databaseValue != "" {
		var err error
		databaseValue, err = validateBackupIdentity("database", databaseValue)
		if err != nil {
			return err
		}
	}
	if typeValue == "database" && len(vhosts) > 0 {
		return errors.New("database-only backups cannot select vhosts")
	}
	if err := validateUniqueBackupIdentities("vhost", vhosts, 16); err != nil {
		return err
	}
	payload := map[string]any{
		"type": typeValue, "name": nameValue, "engine": engineValue, "database": databaseValue,
		"compression": *compression, "retention": *retention, "vhosts": []string(vhosts),
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, "/api/backups", payload, true)
}

func runManagedBackup(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("backups run", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	node := flags.String("node", "", "managed node ID")
	confirmed := flags.Bool("confirm", false, "confirm execution of the managed backup plan")
	wait := flags.Duration("wait", 22*time.Minute, "maximum managed backup wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 || strings.TrimSpace(*node) == "" {
		return errors.New("usage: hserverctl backups run --confirm --node NODE [--wait DURATION] PLAN")
	}
	if !*confirmed {
		return errors.New("managed backup execution requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("managed backup wait must be greater than zero")
	}
	plan, err := validateBackupIdentity("backup plan", flags.Args()[0])
	if err != nil {
		return err
	}
	endpoint := "/api/nodes/" + url.PathEscape(strings.TrimSpace(*node)) + "/backups/" + url.PathEscape(plan) + "/run"
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodPost, endpoint, nil, true)
}

func runBackupJobs(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("backups jobs", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	hours := flags.Float64("hours", 24, "job history window in hours, up to 168")
	active := flags.Bool("active", false, "return active jobs only")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl backups jobs [--hours N] [--active]")
	}
	if *hours <= 0 || *hours > 168 {
		return errors.New("backup job history must be greater than zero and at most 168 hours")
	}
	query := url.Values{"hours": []string{strconv.FormatFloat(*hours, 'f', -1, 64)}}
	if *active {
		query.Set("active", "1")
	}
	return printRequest(ctx, client, out, http.MethodGet, "/api/backups/jobs?"+query.Encode(), nil, true)
}

func runBackupDelete(ctx context.Context, client *apiClient, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("backups delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	confirmed := flags.Bool("confirm", false, "confirm deletion of one local backup artifact")
	wait := flags.Duration("wait", 2*time.Minute, "maximum deletion wait")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 1 {
		return errors.New("usage: hserverctl backups delete --confirm [--wait DURATION] BACKUP_ID")
	}
	if !*confirmed {
		return errors.New("backup deletion requires explicit --confirm")
	}
	if *wait <= 0 {
		return errors.New("backup deletion wait must be greater than zero")
	}
	backupID, err := validateBackupIdentity("backup", flags.Args()[0])
	if err != nil {
		return err
	}
	return printRequest(ctx, client.withTimeout(*wait), out, http.MethodDelete, "/api/backups/"+url.PathEscape(backupID), nil, true)
}

func validateBackupIdentity(kind, value string) (string, error) {
	value = strings.TrimSpace(value)
	if !backupIdentityPattern.MatchString(value) {
		return "", fmt.Errorf("%s identity must be a portable name", kind)
	}
	return value, nil
}

func validateUniqueBackupIdentities(kind string, values []string, maximum int) error {
	if len(values) > maximum {
		return fmt.Errorf("at most %d %s identities may be selected", maximum, kind)
	}
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		normalized, err := validateBackupIdentity(kind, value)
		if err != nil {
			return err
		}
		values[index] = normalized
		if _, exists := seen[normalized]; exists {
			return fmt.Errorf("duplicate %s identity %q", kind, normalized)
		}
		seen[normalized] = struct{}{}
	}
	return nil
}
