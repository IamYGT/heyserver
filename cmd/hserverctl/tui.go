package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/IamYGT/heyserver/internal/agenthub"
)

type tuiTab int

const (
	tuiTabOverview tuiTab = iota
	tuiTabServers
	tuiTabServices
	tuiTabProcesses
	tuiTabMaintenance
	tuiTabDisk
	tuiTabContainers
	tuiTabLogs
	tuiTabPM2
	tuiTabPHP
	tuiTabWeb
	tuiTabDNS
	tuiTabFirewall
	tuiTabSecurity
	tuiTabCron
	tuiTabDatabases
	tuiTabFiles
	tuiTabBackups
	tuiTabSnapshots
	tuiTabAudit
	tuiTabUpdates
	tuiTabDeploy
	tuiTabAlerts
	tuiTabCloudflare
	tuiTabUsers
	tuiTabMail
)

var tuiTabLabels = []string{"Overview", "Servers", "Services", "Processes", "Maintenance", "Disk", "Containers", "Logs", "PM2", "PHP", "Web Ops", "DNS", "Firewall", "Security", "Cron", "Databases", "Files", "Backups", "Snapshots", "Audit", "Updates", "Deploy", "Alerts", "Cloudflare", "Users", "Mail"}

type tuiDialogMode int

const (
	tuiDialogNone tuiDialogMode = iota
	tuiDialogHelp
	tuiDialogChoices
	tuiDialogConfirm
	tuiDialogLogs
	tuiDialogPalette
	tuiDialogBackupValidation
	tuiDialogBackupVhosts
	tuiDialogSnapshotSelectors
	tuiDialogAuditFilter
	tuiDialogDNSForm
	tuiDialogUserForm
	tuiDialogSecurityAccessForm
)

type tuiDialogOption struct {
	Label     string
	Action    string
	Dangerous bool
}

type tuiDialog struct {
	Mode                    tuiDialogMode
	Title                   string
	Body                    []string
	Options                 []tuiDialogOption
	Cursor                  int
	HelpScroll              int
	Operation               tuiOperation
	LogSource               tuiLogSource
	LogPM2                  tuiPM2Process
	FilePath                string
	PHPVersion              string
	PHPPool                 string
	LogLines                []string
	LogScroll               int
	PaletteItems            []tuiPaletteItem
	PaletteQuery            string
	BackupItem              tuiBackupItem
	BackupValidation        tuiBackupValidation
	BackupJob               tuiBackupJob
	BackupVhosts            []string
	BackupSelected          map[string]bool
	BackupVhostMax          int
	SnapshotSelectors       []tuiDialogOption
	SnapshotSelected        map[string]bool
	SnapshotMax             int
	AuditFilter             string
	DNSForm                 tuiDNSForm
	UserForm                tuiUserForm
	SecurityAccessForm      tuiSecurityAccessForm
	SecurityAccessAction    string
	SecurityAccessOperation *tuiSecurityAccessListOperation
	DeployLog               tuiDeployLogRef
	LogReloadNotice         string
	MailMutation            *tuiMailMutation
	DiskAnalysisStart       *tuiDiskAnalysisStartRequest
	DiskDiagnosticKind      tuiDiskDiagnosticKind
	DiskDiagnosticPath      string
}

type tuiTickMsg time.Time

type tuiModel struct {
	ctx                      context.Context
	client                   *apiClient
	contextName              string
	serverURL                string
	refreshInterval          time.Duration
	width                    int
	height                   int
	tab                      tuiTab
	cursor                   int
	selectedTargetID         string
	snapshot                 tuiSnapshot
	loading                  bool
	operating                bool
	resourceLoading          bool
	dialog                   tuiDialog
	notice                   string
	noticeError              bool
	diskSelected             map[string]bool
	diskDiagnostics          tuiDiskDiagnosticsState
	diskDiagnosticsTarget    string
	containers               []tuiContainer
	containersTarget         string
	containersLoaded         bool
	pm2Processes             []tuiPM2Process
	pm2Target                string
	pm2Loaded                bool
	php                      tuiPHPState
	phpTarget                string
	phpLoaded                bool
	logSources               []tuiLogSource
	logsTarget               string
	logSourcesLoaded         bool
	webResources             []tuiWebResource
	webWarnings              []string
	webTarget                string
	webLoaded                bool
	dns                      tuiDNSState
	dnsTarget                string
	dnsLoaded                bool
	firewall                 tuiFirewallState
	firewallTarget           string
	firewallLoaded           bool
	security                 tuiSecurityState
	securityTarget           string
	securityLoaded           bool
	cron                     tuiCronState
	cronTarget               string
	cronLoaded               bool
	databases                tuiDatabaseState
	databasesTarget          string
	databasesLoaded          bool
	files                    tuiFileState
	filesTarget              string
	filesLoaded              bool
	backups                  []tuiBackupItem
	backupJobs               []tuiBackupJob
	backupStorage            tuiBackupStorage
	backupVhosts             []string
	backupVhostMax           int
	backupWarnings           []string
	backupsTarget            string
	backupsLoaded            bool
	encryptedSnapshots       tuiSnapshotState
	encryptedSnapshotsTarget string
	encryptedSnapshotsLoaded bool
	audit                    tuiAuditState
	auditTarget              string
	auditLoaded              bool
	auditFilter              string
	updates                  tuiUpdateState
	updatesTarget            string
	updatesLoaded            bool
	deploy                   tuiDeployState
	deployTarget             string
	deployLoaded             bool
	alerts                   tuiAlertsState
	alertsTarget             string
	alertsLoaded             bool
	cloudflare               tuiCloudflareState
	cloudflareTarget         string
	cloudflareLoaded         bool
	users                    tuiUsersState
	usersTarget              string
	usersLoaded              bool
	mail                     tuiMailState
	mailTarget               string
	mailLoaded               bool
}

type tuiMaintenanceAction struct {
	ID          string
	Label       string
	Description string
	Dangerous   bool
}

var tuiMaintenanceActions = []tuiMaintenanceAction{
	{ID: "memory-optimize", Label: "Optimize RAM", Description: "Flush writes and release reclaimable filesystem caches"},
	{ID: "swap-reset", Label: "Reset swap", Description: "Safely cycle configured swap after the server checks memory headroom"},
	{ID: "temp-clean", Label: "Clean temporary files", Description: "Apply the host tmpfiles expiry policy"},
	{ID: "reboot", Label: "Schedule reboot", Description: "Schedule a server reboot with a 10-second cancellation window", Dangerous: true},
	{ID: "reboot-cancel", Label: "Cancel HServer reboot", Description: "Cancel a reboot previously scheduled by HServer"},
}

func runUI(ctx context.Context, client *apiClient, args []string, serverURL string, out io.Writer) error {
	return runUIWithPanel(ctx, client, args, "direct", serverURL, out)
}

// runUIWithContext is the identity-aware entry point used by the CLI. The
// context label is already secret-free; the panel URL is taken from the
// parsed client's canonical base URL rather than from a token-bearing input.
func runUIWithContext(ctx context.Context, client *apiClient, args []string, contextName string, out io.Writer) error {
	panelURL := ""
	if client != nil && client.baseURL != nil {
		panelURL = client.baseURL.String()
	}
	return runUIWithPanel(ctx, client, args, contextName, panelURL, out)
}

func runUIWithPanel(ctx context.Context, client *apiClient, args []string, contextName, panelURL string, out io.Writer) error {
	flags := flag.NewFlagSet("ui", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	refresh := flags.Duration("refresh", 5*time.Second, "automatic refresh interval")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if len(flags.Args()) != 0 {
		return errors.New("usage: hserverctl ui [--refresh DURATION]")
	}
	if *refresh < time.Second {
		return errors.New("UI refresh interval must be at least one second")
	}

	model := newTUIModelWithContext(ctx, client, contextName, panelURL, *refresh)
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithOutput(out))
	_, err := program.Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func newTUIModel(ctx context.Context, client *apiClient, serverURL string, refreshInterval time.Duration) tuiModel {
	return newTUIModelWithContext(ctx, client, "direct", serverURL, refreshInterval)
}

func newTUIModelWithContext(ctx context.Context, client *apiClient, contextName, panelURL string, refreshInterval time.Duration) tuiModel {
	if client != nil && client.baseURL != nil {
		panelURL = client.baseURL.String()
	}
	panelURL = strings.TrimSpace(sanitizeTUIText(panelURL))
	targets := initialTUITargets()
	return tuiModel{
		ctx: ctx, client: client, contextName: normalizeTUIContextName(contextName), serverURL: panelURL, refreshInterval: refreshInterval,
		width: 100, height: 30, selectedTargetID: localTargetID,
		snapshot: tuiSnapshot{Targets: targets, Selected: targets[0]},
		loading:  true, diskSelected: map[string]bool{},
	}
}

func normalizeTUIContextName(name string) string {
	name = strings.TrimSpace(sanitizeTUIText(name))
	if name == "direct" || validateContextName(name) == nil {
		if name != "" {
			return name
		}
	}
	return "direct"
}

// setErrorNotice is the single TUI error presentation boundary. Keep the
// typed API error intact for callers, but expose its safe status/state,
// recovery advice, and selected server context in the notice shown to users.
func (model *tuiModel) setErrorNotice(err error) {
	model.notice = clientErrorMessage(err)
	model.noticeError = err != nil
}

func (model tuiModel) Init() tea.Cmd {
	return tea.Batch(
		loadTUISnapshotCmd(model.ctx, model.client, model.selectedTargetID, model.snapshot.Targets, false),
		model.tickCmd(),
	)
}

func (model tuiModel) tickCmd() tea.Cmd {
	return tea.Tick(model.refreshInterval, func(now time.Time) tea.Msg { return tuiTickMsg(now) })
}

func (model tuiModel) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		model.width = msg.Width
		model.height = msg.Height
		if model.dialog.Mode == tuiDialogHelp {
			model.dialog.HelpScroll = minInt(maxInt(0, model.dialog.HelpScroll), tuiHelpScrollLimit(model.height))
		}
		return model, nil
	case tuiTickMsg:
		command := model.tickCmd()
		if !model.loading && !model.operating && model.dialog.Mode == tuiDialogNone {
			model.loading = true
			commands := []tea.Cmd{command, loadTUISnapshotCmd(model.ctx, model.client, model.selectedTargetID, model.snapshot.Targets, false)}
			if model.tab == tuiTabBackups && model.backupsLoaded && model.hasActiveBackupJobs() && !model.resourceLoading {
				model.resourceLoading = true
				commands = append(commands, loadTUIBackupsCmd(model.ctx, model.client, model.snapshot.Selected))
			}
			if model.tab == tuiTabAudit && model.auditLoaded && !model.resourceLoading {
				model.resourceLoading = true
				commands = append(commands, loadTUIAuditCmd(model.ctx, model.client, model.snapshot.Selected))
			}
			if model.tab == tuiTabUpdates && model.updatesLoaded && !model.resourceLoading && model.hasActiveUpdateOperation() {
				model.resourceLoading = true
				commands = append(commands, loadTUIUpdatesCmd(model.ctx, model.client, model.snapshot.Selected))
			}
			if model.tab == tuiTabDeploy && model.deployLoaded && !model.resourceLoading && model.hasActiveDeployJobs() {
				model.resourceLoading = true
				commands = append(commands, loadTUIDeployCmd(model.ctx, model.client, model.snapshot.Selected))
			}
			if model.tab == tuiTabAlerts && model.alertsLoaded && !model.resourceLoading {
				model.resourceLoading = true
				commands = append(commands, loadTUIAlertsCmd(model.ctx, model.client, model.snapshot.Selected))
			}
			if model.tab == tuiTabUsers && model.usersLoaded && !model.resourceLoading {
				model.resourceLoading = true
				commands = append(commands, loadTUIUsersCmd(model.ctx, model.client, model.snapshot.Selected))
			}
			if model.tab == tuiTabMail && model.mailLoaded && !model.resourceLoading {
				model.resourceLoading = true
				commands = append(commands, loadTUIMailCmd(model.ctx, model.client, model.snapshot.Selected))
			}
			return model, tea.Batch(commands...)
		}
		return model, command
	case tuiLoadMsg:
		model.loading = false
		if msg.TargetID != "" && msg.TargetID != model.selectedTargetID {
			// A slower snapshot for a previously selected node must not replace
			// the target the operator is viewing.
			return model, nil
		}
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			return model, nil
		}
		if !msg.Snapshot.CleanupLoaded && msg.Snapshot.Selected.ID == model.snapshot.Selected.ID {
			msg.Snapshot.CleanupLoaded = model.snapshot.CleanupLoaded
			msg.Snapshot.CleanupTargets = model.snapshot.CleanupTargets
		}
		model.snapshot = msg.Snapshot
		model.selectedTargetID = msg.Snapshot.Selected.ID
		model.normalizeCursor()
		if model.tab == tuiTabDisk && (!model.diskDiagnostics.Loaded || model.diskDiagnosticsTarget != model.selectedTargetID) {
			return model.loadDiskDiagnosticsSummary()
		}
		if model.tab == tuiTabContainers && (!model.containersLoaded || model.containersTarget != model.selectedTargetID) {
			return model.loadContainers()
		}
		if model.tab == tuiTabLogs && (!model.logSourcesLoaded || model.logsTarget != model.selectedTargetID) {
			return model.loadLogSources()
		}
		if model.tab == tuiTabPM2 && (!model.pm2Loaded || model.pm2Target != model.selectedTargetID) {
			return model.loadPM2()
		}
		if model.tab == tuiTabPHP && (!model.phpLoaded || model.phpTarget != model.selectedTargetID) {
			return model.loadPHP()
		}
		if model.tab == tuiTabWeb && (!model.webLoaded || model.webTarget != model.selectedTargetID) {
			return model.loadWeb()
		}
		if model.tab == tuiTabDNS && (!model.dnsLoaded || model.dnsTarget != model.selectedTargetID) {
			return model.loadDNS()
		}
		if model.tab == tuiTabFirewall && (!model.firewallLoaded || model.firewallTarget != model.selectedTargetID) {
			return model.loadFirewall()
		}
		if model.tab == tuiTabSecurity && (!model.securityLoaded || model.securityTarget != model.selectedTargetID) {
			return model.loadSecurity()
		}
		if model.tab == tuiTabCron && (!model.cronLoaded || model.cronTarget != model.selectedTargetID) {
			return model.loadCron()
		}
		if model.tab == tuiTabDatabases && (!model.databasesLoaded || model.databasesTarget != model.selectedTargetID) {
			return model.loadDatabases()
		}
		if model.tab == tuiTabFiles && (!model.filesLoaded || model.filesTarget != model.selectedTargetID) {
			return model.loadFiles(model.files.CurrentPath)
		}
		if model.tab == tuiTabBackups && (!model.backupsLoaded || model.backupsTarget != model.selectedTargetID) {
			return model.loadBackups()
		}
		if model.tab == tuiTabSnapshots && (!model.encryptedSnapshotsLoaded || model.encryptedSnapshotsTarget != model.selectedTargetID) {
			return model.loadEncryptedSnapshots()
		}
		if model.tab == tuiTabAudit && (!model.auditLoaded || model.auditTarget != model.selectedTargetID) {
			return model.loadAudit()
		}
		if model.tab == tuiTabUpdates && (!model.updatesLoaded || model.updatesTarget != model.selectedTargetID) {
			return model.loadUpdates()
		}
		if model.tab == tuiTabDeploy && (!model.deployLoaded || model.deployTarget != model.selectedTargetID) {
			return model.loadDeploy()
		}
		if model.tab == tuiTabAlerts && (!model.alertsLoaded || model.alertsTarget != model.selectedTargetID) {
			return model.loadAlerts()
		}
		if model.tab == tuiTabCloudflare && (!model.cloudflareLoaded || model.cloudflareTarget != model.selectedTargetID) {
			return model.loadCloudflare()
		}
		if model.tab == tuiTabUsers && (!model.usersLoaded || model.usersTarget != model.selectedTargetID) {
			return model.loadUsers()
		}
		if model.tab == tuiTabMail && (!model.mailLoaded || model.mailTarget != model.selectedTargetID) {
			return model.loadMail()
		}
		return model, nil
	case tuiOperationMsg:
		model.operating = false
		model.dialog = tuiDialog{}
		if msg.Err != nil {
			if model.tab == tuiTabMail {
				model.notice = "Mail queue mutation unavailable"
				model.noticeError = true
			} else {
				model.setErrorNotice(msg.Err)
			}
		} else {
			model.notice = msg.Message
			model.noticeError = false
			model.diskSelected = map[string]bool{}
		}
		model.loading = true
		if model.tab == tuiTabContainers {
			model.containersLoaded = false
		}
		if model.tab == tuiTabPM2 {
			model.pm2Loaded = false
		}
		if model.tab == tuiTabPHP {
			model.phpLoaded = false
		}
		if model.tab == tuiTabWeb {
			model.webLoaded = false
		}
		if model.tab == tuiTabDNS {
			model.dnsLoaded = false
			model.dns = tuiDNSState{}
		}
		if model.tab == tuiTabFirewall {
			model.firewallLoaded = false
		}
		if model.tab == tuiTabSecurity {
			model.securityLoaded = false
		}
		if model.tab == tuiTabCron {
			model.cronLoaded = false
		}
		if model.tab == tuiTabDatabases {
			model.databasesLoaded = false
		}
		if model.tab == tuiTabFiles {
			model.filesLoaded = false
		}
		if model.tab == tuiTabBackups {
			model.backupsLoaded = false
		}
		if model.tab == tuiTabSnapshots {
			model.encryptedSnapshotsLoaded = false
		}
		model.updatesLoaded = false
		model.updates = tuiUpdateState{}
		model.deployLoaded = false
		model.deploy = tuiDeployState{}
		model.alertsLoaded = false
		model.alerts = tuiAlertsState{}
		model.cloudflareLoaded = false
		model.cloudflare = tuiCloudflareState{}
		model.usersLoaded = false
		model.users = tuiUsersState{}
		model.mailLoaded = false
		model.mail = tuiMailState{}
		model.mailTarget = ""
		includeCleanup := model.tab == tuiTabDisk
		return model, loadTUISnapshotCmd(model.ctx, model.client, model.selectedTargetID, model.snapshot.Targets, includeCleanup)
	case tuiDiskDiagnosticsMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Mutation {
			model.operating = false
			model.dialog = tuiDialog{}
		}
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			return model, nil
		}
		model.diskDiagnosticsTarget = msg.TargetID
		model.diskDiagnostics = model.mergeTUIDiskDiagnostics(msg.Kind, msg.State)
		if !model.diskDiagnostics.Supported {
			model.notice = model.diskDiagnostics.UnsupportedNote
			model.noticeError = false
			return model, nil
		}
		if msg.OpenDialog && model.tab != tuiTabDisk {
			return model, nil
		}
		if msg.OpenDialog {
			model.dialog = model.diskDiagnosticsDialog(msg.Kind)
			model.notice = "Loaded " + tuiDiskDiagnosticLabel(msg.Kind)
			model.noticeError = false
			return model, nil
		}
		if msg.Mutation {
			status := model.diskDiagnostics.Analysis.Status
			if status == "" {
				status = "queued"
			}
			model.notice = "Deep disk analysis " + status
		} else if msg.Kind == tuiDiskDiagnosticSummary {
			model.notice = "Loaded local disk diagnostics"
		}
		model.noticeError = false
		return model, nil
	case tuiContainersMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			model.containersLoaded = false
			return model, nil
		}
		model.containers = msg.Containers
		model.containersTarget = msg.TargetID
		model.containersLoaded = true
		model.notice = fmt.Sprintf("Loaded %d container(s)", len(msg.Containers))
		model.noticeError = false
		model.normalizeCursor()
		return model, nil
	case tuiLogSourcesMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			model.logSourcesLoaded = false
			return model, nil
		}
		model.logSources = msg.Sources
		model.logsTarget = msg.TargetID
		model.logSourcesLoaded = true
		model.notice = fmt.Sprintf("Loaded %d log source(s)", len(msg.Sources))
		model.noticeError = false
		model.normalizeCursor()
		return model, nil
	case tuiLogLinesMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if model.tab != tuiTabLogs {
			return model, nil
		}
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			return model, nil
		}
		model.dialog = tuiDialog{
			Mode: tuiDialogLogs, Title: "Logs · " + msg.Source.Label,
			LogSource: msg.Source, LogLines: msg.Lines, LogScroll: maxInt(0, len(msg.Lines)-1),
		}
		model.notice = fmt.Sprintf("Loaded %d log line(s)", len(msg.Lines))
		model.noticeError = false
		return model, nil
	case tuiPM2Msg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			model.pm2Loaded = false
			return model, nil
		}
		model.pm2Processes = msg.Processes
		model.pm2Target = msg.TargetID
		model.pm2Loaded = true
		model.notice = fmt.Sprintf("Loaded %d PM2 process(es)", len(msg.Processes))
		model.noticeError = false
		model.normalizeCursor()
		return model, nil
	case tuiPM2LogsMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if model.tab != tuiTabPM2 {
			return model, nil
		}
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			return model, nil
		}
		model.dialog = tuiDialog{
			Mode: tuiDialogLogs, Title: "PM2 logs · " + msg.Process.Name,
			LogPM2: msg.Process, LogLines: msg.Lines, LogScroll: maxInt(0, len(msg.Lines)-1),
		}
		model.notice = fmt.Sprintf("Loaded %d PM2 log line(s)", len(msg.Lines))
		model.noticeError = false
		return model, nil
	case tuiPHPMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			model.phpLoaded = false
			return model, nil
		}
		model.php = msg.State
		model.phpTarget = msg.TargetID
		model.phpLoaded = true
		model.notice = fmt.Sprintf("Loaded %d PHP-FPM inventory row(s)", len(msg.State.Items))
		model.noticeError = false
		model.normalizeCursor()
		return model, nil
	case tuiPHPConfigMsg:
		if msg.TargetID != model.selectedTargetID || model.tab != tuiTabPHP {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			return model, nil
		}
		model.dialog = tuiDialog{
			Mode: tuiDialogLogs, Title: "PHP " + msg.Version + " · " + msg.Pool,
			PHPVersion: msg.Version, PHPPool: msg.Pool, FilePath: msg.Path,
			LogLines: msg.Lines, LogScroll: 0,
		}
		model.notice = fmt.Sprintf("Loaded PHP %s pool %s configuration", msg.Version, msg.Pool)
		model.noticeError = false
		return model, nil
	case tuiSecurityMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		model.security = msg.State
		model.securityTarget = msg.TargetID
		model.securityLoaded = true
		model.notice = fmt.Sprintf("Loaded %d security inventory row(s)", len(msg.State.Items))
		if !msg.State.Supported {
			model.notice = msg.State.UnsupportedNote
		}
		model.noticeError = false
		model.normalizeCursor()
		return model, nil
	case tuiWebMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			model.webLoaded = false
			model.webWarnings = msg.Warnings
			return model, nil
		}
		model.webResources = msg.Items
		model.webWarnings = msg.Warnings
		model.webTarget = msg.TargetID
		model.webLoaded = true
		model.notice = fmt.Sprintf("Loaded %d web resource(s)", len(msg.Items))
		model.noticeError = false
		model.normalizeCursor()
		return model, nil
	case tuiDNSMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			model.dnsLoaded = false
			return model, nil
		}
		model.dns = msg.State
		model.dnsTarget = msg.TargetID
		model.dnsLoaded = true
		model.notice = fmt.Sprintf("Loaded %d local DNS zone(s)", len(msg.State.Zones))
		if !msg.State.Supported {
			model.notice = msg.State.Message
		}
		model.noticeError = false
		model.normalizeCursor()
		return model, nil
	case tuiDNSDetailMsg:
		if msg.TargetID != model.selectedTargetID || model.tab != tuiTabDNS {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			return model, nil
		}
		if msg.Detail == nil {
			model.notice, model.noticeError = "DNS zone detail response was empty", true
			return model, nil
		}
		model.dns.Detail = msg.Detail
		model.cursor = 0
		model.notice = fmt.Sprintf("Loaded %d record(s) for %s", len(msg.Detail.Records), msg.Detail.Domain)
		model.noticeError = false
		return model, nil
	case tuiDNSCheckMsg:
		if msg.TargetID != model.selectedTargetID || model.tab != tuiTabDNS {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			return model, nil
		}
		model.dns.Check = &msg.Result
		model.dialog = tuiDialog{
			Mode: tuiDialogLogs, Title: "BIND configuration check",
			LogLines: dnsCheckLines(msg.Result), LogScroll: 0,
		}
		model.notice = "BIND configuration check completed"
		model.noticeError = !msg.Result.OK
		return model, nil
	case tuiDNSSOAMsg:
		if msg.TargetID != model.selectedTargetID || model.tab != tuiTabDNS {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			return model, nil
		}
		model.openDNSSOAForm(msg.Domain, msg.SOA)
		model.notice = "Loaded current SOA fields for " + msg.Domain
		model.noticeError = false
		return model, nil
	case tuiBackupsMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			model.backupsLoaded = false
			return model, nil
		}
		model.backups = msg.Items
		model.backupJobs = msg.Jobs
		model.backupStorage = msg.Storage
		model.backupVhosts = msg.Targets.Vhosts
		model.backupVhostMax = msg.Targets.MaxSelectedVhosts
		model.backupWarnings = msg.Warnings
		model.backupsTarget = msg.TargetID
		model.backupsLoaded = true
		model.notice = fmt.Sprintf("Loaded %d backup artifact(s) and %d recent job(s)", len(msg.Items), len(msg.Jobs))
		model.noticeError = false
		model.normalizeCursor()
		return model, nil
	case tuiSnapshotsMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			model.encryptedSnapshotsLoaded = false
			return model, nil
		}
		model.encryptedSnapshots = msg.State
		model.encryptedSnapshotsTarget = msg.TargetID
		model.encryptedSnapshotsLoaded = true
		model.notice = fmt.Sprintf("Loaded %d encrypted snapshot(s)", len(msg.State.Snapshots))
		if !msg.State.Supported {
			model.notice = msg.State.DestinationMessage
		}
		model.noticeError = false
		model.normalizeCursor()
		return model, nil
	case tuiAuditMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			model.auditLoaded = false
			return model, nil
		}
		model.audit = msg.State
		model.auditTarget = msg.TargetID
		model.auditLoaded = true
		model.notice = fmt.Sprintf("Loaded %d of %d target-scoped audit event(s)", len(msg.State.Entries), msg.State.Total)
		model.noticeError = false
		model.normalizeCursor()
		return model, nil
	case tuiUpdatesMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			model.updatesLoaded = false
			return model, nil
		}
		model.updates = msg.State
		model.updatesTarget = msg.TargetID
		model.updatesLoaded = true
		model.notice = "Loaded release lifecycle state"
		model.noticeError = false
		return model, nil
	case tuiDeployMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			model.deployLoaded = false
			return model, nil
		}
		model.deploy = msg.State
		model.deployTarget = msg.TargetID
		model.deployLoaded = true
		model.notice = fmt.Sprintf("Loaded %d deployment target(s) and %d recent job(s)", deployTargetCount(msg.State), deployJobCount(msg.State))
		model.noticeError = false
		model.normalizeCursor()
		return model, nil
	case tuiDeployDetailMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			return model, nil
		}
		model.openLocalDeployActions(msg.Detail)
		model.notice = "Deployment preflight and revision loaded"
		model.noticeError = false
		return model, nil
	case tuiDeployLogsMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			return model, nil
		}
		model.dialog = tuiDialog{Mode: tuiDialogLogs, Title: msg.Title, LogLines: msg.Lines, LogScroll: maxInt(0, len(msg.Lines)-1), DeployLog: msg.Ref}
		model.notice = fmt.Sprintf("Loaded %d deployment output line(s)", len(msg.Lines))
		model.noticeError = false
		return model, nil
	case tuiAlertsMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			model.alertsLoaded = false
			return model, nil
		}
		model.alerts = msg.State
		model.alertsTarget = msg.TargetID
		model.alertsLoaded = true
		model.notice = fmt.Sprintf("Loaded %d notification/alert item(s)", alertsItemCount(msg.State))
		model.noticeError = false
		model.normalizeCursor()
		return model, nil
	case tuiCloudflareMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			model.cloudflareLoaded = false
			return model, nil
		}
		model.cloudflare = msg.State
		model.cloudflareTarget = msg.TargetID
		model.cloudflareLoaded = true
		model.notice = fmt.Sprintf("Loaded %d Cloudflare zone(s)", len(msg.State.Zones))
		model.noticeError = false
		model.normalizeCursor()
		return model, nil
	case tuiUsersMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			model.usersLoaded = false
			return model, nil
		}
		model.users = msg.State
		model.usersTarget = msg.TargetID
		model.usersLoaded = true
		model.notice = fmt.Sprintf("Loaded %d of %d central panel user(s)", len(msg.State.Users), msg.State.Total)
		if !msg.State.Local {
			model.notice = msg.State.Message
		}
		model.noticeError = false
		model.normalizeCursor()
		return model, nil
	case tuiMailMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.notice = "Mail integration unavailable"
			model.noticeError = true
			model.mailLoaded = false
			return model, nil
		}
		model.mail = msg.State
		model.mailTarget = msg.TargetID
		model.mailLoaded = true
		model.notice = fmt.Sprintf("Loaded mail service state, %d log(s), %d queue message(s), %d domain(s), and %d account(s)", len(msg.State.Logs), len(msg.State.Queue), len(msg.State.Domains), len(msg.State.Accounts))
		if msg.State.Status == "not_configured" {
			model.notice = msg.State.Message
		}
		model.noticeError = false
		model.normalizeCursor()
		return model, nil
	case tuiMailDeliveryMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if model.tab != tuiTabMail {
			return model, nil
		}
		if msg.Err != nil {
			model.notice = "Mail delivery history unavailable"
			model.noticeError = true
			return model, nil
		}
		lines := tuiMailLogLines(msg.Entries)
		model.dialog = tuiDialog{
			Mode: tuiDialogLogs, Title: "Mail delivery · " + truncateTUI(redactAPISecrets(msg.Email), 48),
			LogLines: lines, LogScroll: maxInt(0, len(lines)-1),
			LogReloadNotice: "Refresh the Mail section to reload delivery history",
		}
		model.notice = fmt.Sprintf("Loaded %d delivery log(s) for %s", len(msg.Entries), truncateTUI(redactAPISecrets(msg.Email), 48))
		model.noticeError = false
		return model, nil
	case tuiCloudflareDetailMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			return model, nil
		}
		model.cloudflare.Detail = &msg.Detail
		model.cursor = 0
		model.notice = fmt.Sprintf("Loaded %d Cloudflare DNS record(s)", len(msg.Detail.Records))
		model.noticeError = false
		return model, nil
	case tuiFirewallMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			model.firewallLoaded = false
			return model, nil
		}
		model.firewall = msg.State
		model.firewallTarget = msg.TargetID
		model.firewallLoaded = true
		model.notice = fmt.Sprintf("Loaded %d firewall rule(s)", len(msg.State.Rules))
		model.noticeError = false
		model.normalizeCursor()
		return model, nil
	case tuiCronMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			model.cronLoaded = false
			return model, nil
		}
		model.cron = msg.State
		model.cronTarget = msg.TargetID
		model.cronLoaded = true
		model.notice = fmt.Sprintf("Loaded %d cron job(s)", len(msg.State.Jobs))
		model.noticeError = false
		model.normalizeCursor()
		return model, nil
	case tuiDatabasesMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			model.databasesLoaded = false
			return model, nil
		}
		model.databases = msg.State
		model.databasesTarget = msg.TargetID
		model.databasesLoaded = true
		model.notice = fmt.Sprintf("Loaded %d database inventory row(s)", len(msg.State.Items))
		model.noticeError = false
		model.normalizeCursor()
		return model, nil
	case tuiFilesMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			model.filesLoaded = false
			return model, nil
		}
		model.files = msg.State
		model.filesTarget = msg.TargetID
		model.filesLoaded = true
		model.cursor = 0
		model.notice = fmt.Sprintf("Loaded %d file entry(s) from %s", len(msg.State.Entries), msg.State.CurrentPath)
		model.noticeError = false
		return model, nil
	case tuiFileContentMsg:
		if msg.TargetID != model.selectedTargetID || model.tab != tuiTabFiles {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			return model, nil
		}
		model.dialog = tuiDialog{
			Mode: tuiDialogLogs, Title: "File · " + msg.Path, FilePath: msg.Path,
			LogLines: msg.Lines, LogScroll: 0,
		}
		model.notice = fmt.Sprintf("Loaded %d line(s) from %s", len(msg.Lines), msg.Path)
		model.noticeError = false
		return model, nil
	case tuiBackupValidationMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			return model, nil
		}
		model.dialog = tuiDialog{
			Mode: tuiDialogBackupValidation, Title: "Restore validation · " + msg.Item.Name,
			BackupItem: msg.Item, BackupValidation: msg.Validation,
		}
		model.notice = "Backup artifact validation completed"
		model.noticeError = false
		return model, nil
	case tuiBackupJobMsg:
		if msg.TargetID != model.selectedTargetID {
			return model, nil
		}
		model.resourceLoading = false
		if msg.Err != nil {
			model.setErrorNotice(msg.Err)
			return model, nil
		}
		for index := range model.backupJobs {
			if model.backupJobs[index].ID == msg.Job.ID {
				model.backupJobs[index] = msg.Job
				break
			}
		}
		if model.dialog.Mode == tuiDialogLogs && model.dialog.BackupJob.ID == msg.Job.ID {
			model.dialog.BackupJob = msg.Job
			model.dialog.LogLines = backupJobLogLines(msg.Job)
			model.dialog.LogScroll = maxInt(0, len(model.dialog.LogLines)-1)
		}
		model.notice = "Backup job refreshed"
		model.noticeError = false
		return model, nil
	case tea.KeyPressMsg:
		return model.updateKey(msg.String())
	default:
		return model, nil
	}
}

func (model tuiModel) updateKey(key string) (tea.Model, tea.Cmd) {
	if key == "ctrl+c" {
		return model, tea.Quit
	}
	if model.operating {
		return model, nil
	}
	if model.dialog.Mode != tuiDialogNone {
		return model.updateDialogKey(key)
	}

	switch key {
	case "q":
		return model, tea.Quit
	case "?":
		model.dialog = tuiDialog{Mode: tuiDialogHelp, Title: "Keyboard help"}
		return model, nil
	case "ctrl+k":
		model.openPalette()
		return model, nil
	case "left", "h", "shift+tab":
		model.moveTab(-1)
		return model.maybeLoadTabResource()
	case "right", "l", "tab":
		model.moveTab(1)
		return model.maybeLoadTabResource()
	case "1", "2", "3", "4", "5", "6", "7", "8", "9":
		model.tab = tuiTab(int(key[0] - '1'))
		model.cursor = 0
		return model.maybeLoadTabResource()
	case "0":
		model.tab = tuiTabWeb
		model.cursor = 0
		return model.maybeLoadTabResource()
	case "b":
		model.tab = tuiTabBackups
		model.cursor = 0
		return model.maybeLoadTabResource()
	case "N", "shift+n":
		model.tab = tuiTabSnapshots
		model.cursor = 0
		return model.maybeLoadTabResource()
	case "A", "shift+a":
		model.tab = tuiTabAudit
		model.cursor = 0
		return model.maybeLoadTabResource()
	case "U", "shift+u":
		model.tab = tuiTabUpdates
		model.cursor = 0
		return model.maybeLoadTabResource()
	case "G", "shift+g":
		model.tab = tuiTabDeploy
		model.cursor = 0
		return model.maybeLoadTabResource()
	case "L", "shift+l":
		model.tab = tuiTabAlerts
		model.cursor = 0
		return model.maybeLoadTabResource()
	case "O", "shift+o":
		model.tab = tuiTabCloudflare
		model.cursor = 0
		return model.maybeLoadTabResource()
	case "I", "shift+i":
		model.tab = tuiTabUsers
		model.cursor = 0
		return model.maybeLoadTabResource()
	case "M", "shift+m":
		model.tab = tuiTabMail
		model.cursor = 0
		return model.maybeLoadTabResource()
	case "f":
		model.tab = tuiTabFirewall
		model.cursor = 0
		return model.maybeLoadTabResource()
	case "S", "shift+s":
		model.tab = tuiTabSecurity
		model.cursor = 0
		return model.maybeLoadTabResource()
	case "C", "shift+c":
		model.tab = tuiTabCron
		model.cursor = 0
		return model.maybeLoadTabResource()
	case "D", "shift+d":
		model.tab = tuiTabDatabases
		model.cursor = 0
		return model.maybeLoadTabResource()
	case "E", "shift+e":
		model.tab = tuiTabFiles
		model.cursor = 0
		return model.maybeLoadTabResource()
	case "P", "shift+p":
		model.tab = tuiTabPHP
		model.cursor = 0
		return model.maybeLoadTabResource()
	case "Z", "shift+z":
		model.tab = tuiTabDNS
		model.cursor = 0
		return model.maybeLoadTabResource()
	case "up", "k":
		model.moveCursor(-1)
		return model, nil
	case "down", "j":
		model.moveCursor(1)
		return model, nil
	case "[":
		return model.selectRelativeTarget(-1)
	case "]":
		return model.selectRelativeTarget(1)
	case "r":
		if model.tab == tuiTabDisk {
			model.diskDiagnostics = tuiDiskDiagnosticsState{}
			model.diskDiagnosticsTarget = ""
			model.loading = true
			model.notice = "Refreshing disk cleanup and diagnostics…"
			model.noticeError = false
			return model, loadTUISnapshotCmd(model.ctx, model.client, model.selectedTargetID, model.snapshot.Targets, true)
		}
		if model.tab == tuiTabContainers {
			return model.loadContainers()
		}
		if model.tab == tuiTabLogs {
			return model.loadLogSources()
		}
		if model.tab == tuiTabPM2 {
			return model.loadPM2()
		}
		if model.tab == tuiTabPHP {
			return model.loadPHP()
		}
		if model.tab == tuiTabWeb {
			return model.loadWeb()
		}
		if model.tab == tuiTabDNS {
			return model.loadDNS()
		}
		if model.tab == tuiTabFirewall {
			return model.loadFirewall()
		}
		if model.tab == tuiTabSecurity {
			return model.loadSecurity()
		}
		if model.tab == tuiTabCron {
			return model.loadCron()
		}
		if model.tab == tuiTabDatabases {
			return model.loadDatabases()
		}
		if model.tab == tuiTabFiles {
			return model.loadFiles(model.files.CurrentPath)
		}
		if model.tab == tuiTabBackups {
			return model.loadBackups()
		}
		if model.tab == tuiTabSnapshots {
			return model.loadEncryptedSnapshots()
		}
		if model.tab == tuiTabAudit {
			return model.loadAudit()
		}
		if model.tab == tuiTabUpdates {
			return model.loadUpdates()
		}
		if model.tab == tuiTabDeploy {
			return model.loadDeploy()
		}
		if model.tab == tuiTabAlerts {
			return model.loadAlerts()
		}
		if model.tab == tuiTabCloudflare {
			return model.reloadCloudflare()
		}
		if model.tab == tuiTabUsers {
			return model.loadUsers()
		}
		if model.tab == tuiTabMail {
			return model.loadMail()
		}
		model.loading = true
		model.notice = "Refreshing server state…"
		model.noticeError = false
		return model, loadTUISnapshotCmd(model.ctx, model.client, model.selectedTargetID, model.snapshot.Targets, model.tab == tuiTabDisk)
	case "s":
		if model.tab == tuiTabDisk {
			return model.scanDisk()
		} else if model.tab == tuiTabUpdates && model.snapshot.Selected.Local {
			model.openUpdateConfirmation("stage")
		}
		return model, nil
	case "i":
		if model.tab == tuiTabDisk {
			return model.openDiskDiagnostic(tuiDiskDiagnosticIO)
		}
		if model.tab == tuiTabUpdates && model.snapshot.Selected.Local {
			model.openUpdateConfirmation("install")
		}
		return model, nil
	case "m":
		if model.tab == tuiTabDisk {
			return model.openDiskDiagnostic(tuiDiskDiagnosticMounts)
		}
		return model, nil
	case "p":
		if model.tab == tuiTabDisk {
			return model.openDiskDiagnostic(tuiDiskDiagnosticSMART)
		}
		return model, nil
	case "w":
		if model.tab == tuiTabDisk {
			return model.openDiskDiagnostic(tuiDiskDiagnosticLargest)
		}
		return model, nil
	case " ":
		if model.tab == tuiTabDisk {
			model.toggleDiskTarget()
		}
		return model, nil
	case "x":
		if model.tab == tuiTabDisk {
			model.openDiskConfirmation()
		} else if model.tab == tuiTabFiles {
			model.openFileDeleteConfirmation()
		} else if model.tab == tuiTabDNS {
			model.openDNSDeleteConfirmation()
		}
		return model, nil
	case "u":
		if model.tab == tuiTabDisk {
			return model.openDiskDiagnostic(tuiDiskDiagnosticUsage)
		}
		if model.tab == tuiTabUpdates && !model.snapshot.Selected.Local {
			model.openUpdateConfirmation("upgrade")
			return model, nil
		}
		if model.tab == tuiTabFiles {
			return model.openFileParent()
		}
		return model, nil
	case "backspace":
		if model.tab == tuiTabCloudflare && model.cloudflare.Detail != nil {
			return model.closeCloudflareDetail()
		} else if model.tab == tuiTabFiles {
			return model.openFileParent()
		} else if model.tab == tuiTabDNS {
			model.leaveDNSZone()
		}
		return model, nil
	case "o":
		if model.tab == tuiTabUpdates && !model.snapshot.Selected.Local {
			model.openUpdateConfirmation("rollback")
		}
		return model, nil
	case ",":
		if model.tab == tuiTabFiles {
			return model.switchFileRoot(-1)
		}
		return model, nil
	case ".":
		if model.tab == tuiTabFiles {
			return model.switchFileRoot(1)
		}
		return model, nil
	case "v":
		if model.tab == tuiTabPM2 {
			return model.openPM2LogViewer()
		}
		return model, nil
	case "c":
		if model.tab == tuiTabBackups {
			model.openBackupCreateWizard()
		} else if model.tab == tuiTabSnapshots {
			model.openEncryptedSnapshotCreate()
		} else if model.tab == tuiTabDNS {
			return model.checkDNS()
		}
		return model, nil
	case "d":
		if model.tab == tuiTabDisk {
			model.openDiskAnalysisConfirmation()
		} else if model.tab == tuiTabSnapshots {
			model.openEncryptedSnapshotDestination()
		}
		return model, nil
	case "/":
		if model.tab == tuiTabAudit {
			model.openAuditFilter()
		}
		return model, nil
	case "a":
		if model.tab == tuiTabDisk {
			return model.openDiskDiagnostic(tuiDiskDiagnosticAnalysis)
		}
		if model.tab == tuiTabFirewall {
			model.openFirewallAddProfiles()
		} else if model.tab == tuiTabDNS {
			model.openDNSCreateForm()
		} else if model.tab == tuiTabUsers {
			model.openUserCreateForm()
		} else if model.tab == tuiTabSecurity {
			model.openSecurityAccessAddChoices()
		}
		return model, nil
	case "e":
		if model.tab == tuiTabDNS {
			return model.openDNSEditForm()
		}
		return model, nil
	case "t":
		if model.tab == tuiTabFirewall {
			model.openFirewallToggle()
		} else if model.tab == tuiTabDNS {
			model.openDNSReloadConfirmation()
		}
		return model, nil
	case "enter":
		return model.activateCurrent()
	}
	return model, nil
}

func (model tuiModel) updateDialogKey(key string) (tea.Model, tea.Cmd) {
	if model.dialog.Mode == tuiDialogPalette {
		return model.updatePaletteKey(key)
	}
	if model.dialog.Mode == tuiDialogAuditFilter {
		return model.updateAuditFilterKey(key)
	}
	if model.dialog.Mode == tuiDialogDNSForm {
		return model.updateDNSFormKey(key)
	}
	if model.dialog.Mode == tuiDialogUserForm {
		return model.updateUserFormKey(key)
	}
	if model.dialog.Mode == tuiDialogSecurityAccessForm {
		return model.updateSecurityAccessFormKey(key)
	}
	if model.dialog.Mode == tuiDialogSnapshotSelectors {
		if len(model.dialog.SnapshotSelectors) == 0 {
			model.dialog = tuiDialog{}
			model.notice, model.noticeError = "Encrypted snapshot selector inventory is empty", true
			return model, nil
		}
		switch strings.ToLower(key) {
		case "esc", "q":
			model.dialog = tuiDialog{}
		case "up", "k":
			model.dialog.Cursor = wrapIndex(model.dialog.Cursor-1, len(model.dialog.SnapshotSelectors))
		case "down", "j":
			model.dialog.Cursor = wrapIndex(model.dialog.Cursor+1, len(model.dialog.SnapshotSelectors))
		case " ":
			selector := model.dialog.SnapshotSelectors[model.dialog.Cursor].Action
			if model.dialog.SnapshotSelected[selector] {
				delete(model.dialog.SnapshotSelected, selector)
			} else if len(model.dialog.SnapshotSelected) >= model.dialog.SnapshotMax {
				model.notice = fmt.Sprintf("At most %d encrypted snapshot selectors may be chosen", model.dialog.SnapshotMax)
				model.noticeError = true
			} else {
				model.dialog.SnapshotSelected[selector] = true
				model.notice = ""
				model.noticeError = false
			}
		case "a":
			if len(model.dialog.SnapshotSelectors) > model.dialog.SnapshotMax {
				model.notice = fmt.Sprintf("Select all is unavailable: at most %d encrypted snapshot selectors may be chosen", model.dialog.SnapshotMax)
				model.noticeError = true
			} else {
				model.dialog.SnapshotSelected = make(map[string]bool, len(model.dialog.SnapshotSelectors))
				for _, selector := range model.dialog.SnapshotSelectors {
					model.dialog.SnapshotSelected[selector.Action] = true
				}
				model.notice = ""
				model.noticeError = false
			}
		case "enter":
			if len(model.dialog.SnapshotSelected) == 0 {
				model.notice = "Select at least one encrypted snapshot restore scope"
				model.noticeError = true
				return model, nil
			}
			operation := model.dialog.Operation
			selected := make([]string, 0, len(model.dialog.SnapshotSelected))
			for _, selector := range model.dialog.SnapshotSelectors {
				if model.dialog.SnapshotSelected[selector.Action] {
					selected = append(selected, selector.Action)
				}
			}
			if operation.Action == "restore-manifest" {
				operation.SnapshotManifestIDs = selected
			} else {
				operation.SnapshotVhosts = selected
			}
			operation.Action = "restore-selected"
			operation.Label = "Restore selected encrypted snapshot scope " + operation.EncryptedSnapshot.ID
			operation.Dangerous = true
			model.openConfirmation(operation, confirmationBody(operation))
		}
		return model, nil
	}
	if model.dialog.Mode == tuiDialogBackupVhosts {
		switch strings.ToLower(key) {
		case "esc", "q":
			model.dialog = tuiDialog{}
		case "up", "k":
			model.dialog.Cursor = wrapIndex(model.dialog.Cursor-1, len(model.dialog.BackupVhosts))
		case "down", "j":
			model.dialog.Cursor = wrapIndex(model.dialog.Cursor+1, len(model.dialog.BackupVhosts))
		case " ":
			name := model.dialog.BackupVhosts[model.dialog.Cursor]
			if model.dialog.BackupSelected[name] {
				delete(model.dialog.BackupSelected, name)
			} else if len(model.dialog.BackupSelected) >= model.dialog.BackupVhostMax {
				model.notice = fmt.Sprintf("At most %d website folders may be selected", model.dialog.BackupVhostMax)
				model.noticeError = true
			} else {
				model.dialog.BackupSelected[name] = true
				model.notice = ""
				model.noticeError = false
			}
		case "a":
			if len(model.dialog.BackupVhosts) > model.dialog.BackupVhostMax {
				model.notice = fmt.Sprintf("Select all is unavailable: at most %d website folders may be selected", model.dialog.BackupVhostMax)
				model.noticeError = true
			} else {
				model.dialog.BackupSelected = make(map[string]bool, len(model.dialog.BackupVhosts))
				for _, name := range model.dialog.BackupVhosts {
					model.dialog.BackupSelected[name] = true
				}
				model.notice = ""
				model.noticeError = false
			}
		case "enter":
			if len(model.dialog.BackupSelected) == 0 {
				model.notice = "Select at least one website folder or return and choose all configured files"
				model.noticeError = true
				return model, nil
			}
			operation := model.dialog.Operation
			operation.BackupCreate.Vhosts = operation.BackupCreate.Vhosts[:0]
			for _, name := range model.dialog.BackupVhosts {
				if model.dialog.BackupSelected[name] {
					operation.BackupCreate.Vhosts = append(operation.BackupCreate.Vhosts, name)
				}
			}
			model.advanceBackupCreate(operation)
		}
		return model, nil
	}
	if model.dialog.Mode == tuiDialogBackupValidation {
		switch strings.ToLower(key) {
		case "q", "esc":
			model.dialog = tuiDialog{}
		case "r":
			item, validation := model.dialog.BackupItem, model.dialog.BackupValidation
			operation := tuiOperation{
				Kind: tuiOperationBackup, Target: model.snapshot.Selected, Action: "restore",
				Backup: item, BackupValidation: validation, Label: "Restore " + item.Name, Dangerous: true,
			}
			model.openConfirmation(operation, confirmationBody(operation))
		}
		return model, nil
	}
	if model.dialog.Mode == tuiDialogLogs {
		switch key {
		case "esc", "q":
			model.dialog = tuiDialog{}
		case "up", "k":
			model.dialog.LogScroll = maxInt(0, model.dialog.LogScroll-1)
		case "down", "j":
			model.dialog.LogScroll = minInt(maxInt(0, len(model.dialog.LogLines)-1), model.dialog.LogScroll+1)
		case "pgup", "ctrl+u":
			model.dialog.LogScroll = maxInt(0, model.dialog.LogScroll-10)
		case "pgdown", "ctrl+d":
			model.dialog.LogScroll = minInt(maxInt(0, len(model.dialog.LogLines)-1), model.dialog.LogScroll+10)
		case "g", "home":
			model.dialog.LogScroll = 0
		case "G", "shift+g", "end":
			model.dialog.LogScroll = maxInt(0, len(model.dialog.LogLines)-1)
		case "r":
			if !model.resourceLoading {
				if model.dialog.DiskDiagnosticKind != "" {
					model.resourceLoading = true
					return model, loadTUIDiskDiagnosticCmd(
						model.ctx, model.client, model.snapshot.Selected,
						model.dialog.DiskDiagnosticKind, model.dialog.DiskDiagnosticPath, true,
					)
				}
				if model.dialog.LogReloadNotice != "" {
					model.notice, model.noticeError = model.dialog.LogReloadNotice, false
					return model, nil
				}
				model.resourceLoading = true
				if model.dialog.DeployLog.LocalRunID > 0 || model.dialog.DeployLog.RemoteJob != "" {
					return model, loadTUIDeployLogsCmd(model.ctx, model.client, model.snapshot.Selected, model.dialog.DeployLog)
				}
				if model.dialog.PHPVersion != "" && model.dialog.PHPPool != "" {
					item, ok := findTUIPHPPool(model.php.Items, model.dialog.PHPVersion, model.dialog.PHPPool)
					if !ok {
						model.resourceLoading = false
						model.notice, model.noticeError = "PHP-FPM pool is no longer present in the current inventory", true
						return model, nil
					}
					return model, loadTUIPHPConfigCmd(model.ctx, model.client, model.snapshot.Selected, item)
				}
				if model.dialog.FilePath != "" {
					return model, loadTUIFileContentCmd(model.ctx, model.client, model.snapshot.Selected, model.dialog.FilePath)
				}
				if model.dialog.BackupJob.ID != "" {
					return model, loadTUIBackupJobCmd(model.ctx, model.client, model.snapshot.Selected, model.dialog.BackupJob)
				}
				if model.dialog.LogPM2.Name != "" {
					return model, loadTUIPM2LogsCmd(model.ctx, model.client, model.snapshot.Selected, model.dialog.LogPM2)
				}
				return model, loadTUILogLinesCmd(model.ctx, model.client, model.snapshot.Selected, model.dialog.LogSource)
			}
		}
		return model, nil
	}
	if model.dialog.Mode == tuiDialogHelp {
		switch key {
		case "up", "k":
			model.dialog.HelpScroll = maxInt(0, model.dialog.HelpScroll-1)
		case "down", "j":
			model.dialog.HelpScroll = minInt(tuiHelpScrollLimit(model.height), model.dialog.HelpScroll+1)
		case "pgup", "ctrl+u":
			model.dialog.HelpScroll = maxInt(0, model.dialog.HelpScroll-tuiHelpPageSize(model.height))
		case "pgdown", "ctrl+d":
			model.dialog.HelpScroll = minInt(tuiHelpScrollLimit(model.height), model.dialog.HelpScroll+tuiHelpPageSize(model.height))
		case "g", "home":
			model.dialog.HelpScroll = 0
		case "G", "shift+g", "end":
			model.dialog.HelpScroll = tuiHelpScrollLimit(model.height)
		case "q", "?", "esc", "enter":
			model.dialog = tuiDialog{}
		}
		return model, nil
	}
	if model.dialog.Mode == tuiDialogConfirm {
		switch strings.ToLower(key) {
		case "y":
			if model.dialog.DiskAnalysisStart != nil {
				request := *model.dialog.DiskAnalysisStart
				model.operating = true
				model.notice = "Queueing deep disk analysis…"
				model.noticeError = false
				return model, runTUIDiskAnalysisStartCmd(model.ctx, model.client, request.Target)
			}
			if model.dialog.MailMutation != nil {
				mutation := *model.dialog.MailMutation
				model.operating = true
				model.notice = "Running " + strings.ToUpper(mutation.Action) + " queued mail mutation…"
				model.noticeError = false
				return model, runTUIMailMutationCmd(model.ctx, model.client, mutation)
			}
			if model.dialog.SecurityAccessOperation != nil {
				model.dialog.SecurityAccessOperation.Confirmed = true
				operation := *model.dialog.SecurityAccessOperation
				model.operating = true
				model.notice = "Running " + tuiSecurityAccessDisplayText(operation.Label, 128) + "…"
				model.noticeError = false
				return model, runTUISecurityAccessListOperationCmd(model.ctx, model.client, operation)
			}
			operation := model.dialog.Operation
			model.operating = true
			model.notice = "Running " + operation.Label + "…"
			model.noticeError = false
			return model, runTUIOperationCmd(model.ctx, model.client, operation)
		case "n", "esc", "q":
			model.dialog = tuiDialog{}
			model.notice = "Operation cancelled"
			model.noticeError = false
		}
		return model, nil
	}
	if model.dialog.Mode == tuiDialogChoices {
		switch key {
		case "up", "k":
			model.dialog.Cursor = wrapIndex(model.dialog.Cursor-1, len(model.dialog.Options))
		case "down", "j":
			model.dialog.Cursor = wrapIndex(model.dialog.Cursor+1, len(model.dialog.Options))
		case "esc", "q":
			model.dialog = tuiDialog{}
		case "enter":
			option := model.dialog.Options[model.dialog.Cursor]
			if model.dialog.MailMutation != nil {
				mutation := *model.dialog.MailMutation
				if option.Action != "retry" && option.Action != "delete" {
					model.notice = "Unsupported mail queue action"
					model.noticeError = true
					return model, nil
				}
				mutation.Action = option.Action
				model.openMailQueueConfirmation(mutation)
				return model, nil
			}
			if model.dialog.SecurityAccessAction == "add" {
				model.openSecurityAccessForm(option.Action)
				return model, nil
			}
			operation := model.dialog.Operation
			if operation.Kind == tuiOperationUser {
				switch option.Action {
				case "edit-profile":
					model.openUserProfileForm(operation.User)
					return model, nil
				case "replace-password":
					model.openUserPasswordForm(operation.User)
					return model, nil
				}
			}
			if operation.Kind == tuiOperationCloudflare && option.Action == "view-record" {
				lines := cloudflareRecordLines(operation.CloudflareZone, operation.CloudflareRecord)
				model.dialog = tuiDialog{
					Mode: tuiDialogLogs, Title: "Cloudflare DNS record · " + operation.CloudflareRecord.Name,
					LogLines: lines, LogScroll: maxInt(0, len(lines)-1),
					LogReloadNotice: "Reopen the Cloudflare zone to refresh this DNS record",
				}
				return model, nil
			}
			if operation.Kind == tuiOperationDeploy && option.Action == "view-preflight" {
				detail := tuiDeployDetail{Target: operation.DeployTarget, Preflight: operation.DeployPreflight, Revision: operation.DeployRevision}
				lines := deployDetailLines(detail)
				model.dialog = tuiDialog{Mode: tuiDialogLogs, Title: "Deployment readiness · " + operation.DeployTarget.Name, LogLines: lines, LogScroll: maxInt(0, len(lines)-1), LogReloadNotice: "Reinspect the deployment target to refresh readiness"}
				return model, nil
			}
			if operation.Kind == tuiOperationSnapshot {
				switch option.Action {
				case "restore-all":
					operation.Action = "restore-all"
					operation.Label = "Restore complete encrypted snapshot " + operation.EncryptedSnapshot.ID
					operation.Dangerous = true
					model.openConfirmation(operation, confirmationBody(operation))
					return model, nil
				case "restore-manifest", "restore-vhosts":
					operation.Action = option.Action
					model.openEncryptedSnapshotSelectors(operation)
					return model, nil
				}
			}
			if operation.Kind == tuiOperationBackup && option.Action == "validate" {
				model.dialog = tuiDialog{}
				model.resourceLoading = true
				model.notice = "Reading and validating the complete backup artifact…"
				model.noticeError = false
				return model, loadTUIBackupValidationCmd(model.ctx, model.client, operation.Target, operation.Backup)
			}
			if operation.Kind == tuiOperationBackup {
				if spec, ok := backupCreateSpecForAction(option.Action); ok {
					operation.Action = "create"
					operation.BackupCreate = spec
					operation.Label = "Create " + spec.label()
					if spec.Type == "full" || spec.Type == "files" {
						model.openBackupScopeChoices(operation)
					} else {
						model.openConfirmation(operation, confirmationBody(operation))
					}
					return model, nil
				}
				if option.Action == "scope-all" {
					operation.BackupCreate.Vhosts = nil
					model.advanceBackupCreate(operation)
					return model, nil
				}
				if option.Action == "scope-select" {
					model.openBackupVhostChoices(operation)
					return model, nil
				}
				if retention, ok := backupRetentionForAction(option.Action); ok {
					operation.BackupCreate.Retention = retention
					operation.Dangerous = retention > 0
					operation.Label = "Create " + operation.BackupCreate.label()
					model.openConfirmation(operation, confirmationBody(operation))
					return model, nil
				}
			}
			if operation.Kind == tuiOperationFirewall {
				if spec, ok := firewallSpecForAction(option.Action); ok {
					operation.Action = "add"
					operation.FirewallSpec = spec
					operation.Label = fmt.Sprintf("Allow %s on %s/%d", spec.Comment, spec.Protocol, spec.Port)
					model.openConfirmation(operation, confirmationBody(operation))
					return model, nil
				}
			}
			operation.Action = option.Action
			operation.Dangerous = option.Dangerous
			operation.Label = option.Label + " " + operation.Label
			model.openConfirmation(operation, confirmationBody(operation))
		}
	}
	return model, nil
}

func (model *tuiModel) moveTab(delta int) {
	model.tab = tuiTab(wrapIndex(int(model.tab)+delta, len(tuiTabLabels)))
	model.cursor = 0
	model.notice = ""
}

func (model tuiModel) maybeLoadTabResource() (tea.Model, tea.Cmd) {
	switch model.tab {
	case tuiTabDisk:
		if !model.snapshot.CleanupLoaded && !model.loading {
			return model.scanDisk()
		}
		if !model.diskDiagnostics.Loaded && !model.loading && !model.resourceLoading {
			return model.loadDiskDiagnosticsSummary()
		}
	case tuiTabContainers:
		if !model.containersLoaded || model.containersTarget != model.selectedTargetID {
			return model.loadContainers()
		}
	case tuiTabLogs:
		if !model.logSourcesLoaded || model.logsTarget != model.selectedTargetID {
			return model.loadLogSources()
		}
	case tuiTabPM2:
		if !model.pm2Loaded || model.pm2Target != model.selectedTargetID {
			return model.loadPM2()
		}
	case tuiTabPHP:
		if !model.phpLoaded || model.phpTarget != model.selectedTargetID {
			return model.loadPHP()
		}
	case tuiTabWeb:
		if !model.webLoaded || model.webTarget != model.selectedTargetID {
			return model.loadWeb()
		}
	case tuiTabDNS:
		if !model.dnsLoaded || model.dnsTarget != model.selectedTargetID {
			return model.loadDNS()
		}
	case tuiTabFirewall:
		if !model.firewallLoaded || model.firewallTarget != model.selectedTargetID {
			return model.loadFirewall()
		}
	case tuiTabSecurity:
		if !model.securityLoaded || model.securityTarget != model.selectedTargetID {
			return model.loadSecurity()
		}
	case tuiTabCron:
		if !model.cronLoaded || model.cronTarget != model.selectedTargetID {
			return model.loadCron()
		}
	case tuiTabDatabases:
		if !model.databasesLoaded || model.databasesTarget != model.selectedTargetID {
			return model.loadDatabases()
		}
	case tuiTabFiles:
		if !model.filesLoaded || model.filesTarget != model.selectedTargetID {
			return model.loadFiles("")
		}
	case tuiTabBackups:
		if !model.backupsLoaded || model.backupsTarget != model.selectedTargetID {
			return model.loadBackups()
		}
	case tuiTabSnapshots:
		if !model.encryptedSnapshotsLoaded || model.encryptedSnapshotsTarget != model.selectedTargetID {
			return model.loadEncryptedSnapshots()
		}
	case tuiTabAudit:
		if !model.auditLoaded || model.auditTarget != model.selectedTargetID {
			return model.loadAudit()
		}
	case tuiTabUpdates:
		if !model.updatesLoaded || model.updatesTarget != model.selectedTargetID {
			return model.loadUpdates()
		}
	case tuiTabDeploy:
		if !model.deployLoaded || model.deployTarget != model.selectedTargetID {
			return model.loadDeploy()
		}
	case tuiTabAlerts:
		if !model.alertsLoaded || model.alertsTarget != model.selectedTargetID {
			return model.loadAlerts()
		}
	case tuiTabCloudflare:
		if !model.cloudflareLoaded || model.cloudflareTarget != model.selectedTargetID {
			return model.loadCloudflare()
		}
	case tuiTabUsers:
		if !model.usersLoaded || model.usersTarget != model.selectedTargetID {
			return model.loadUsers()
		}
	case tuiTabMail:
		if !model.mailLoaded || model.mailTarget != model.selectedTargetID {
			return model.loadMail()
		}
	}
	return model, nil
}

func (model *tuiModel) moveCursor(delta int) {
	model.cursor = wrapIndex(model.cursor+delta, model.currentItemCount())
}

func (model *tuiModel) normalizeCursor() {
	count := model.currentItemCount()
	if count == 0 {
		model.cursor = 0
		return
	}
	if model.cursor >= count {
		model.cursor = count - 1
	}
}

func (model tuiModel) currentItemCount() int {
	switch model.tab {
	case tuiTabServers:
		return len(model.snapshot.Targets)
	case tuiTabServices:
		return len(model.snapshot.Services)
	case tuiTabProcesses:
		return len(model.snapshot.Processes)
	case tuiTabMaintenance:
		return len(tuiMaintenanceActions)
	case tuiTabDisk:
		return len(model.snapshot.CleanupTargets)
	case tuiTabContainers:
		return len(model.containers)
	case tuiTabLogs:
		return len(model.logSources)
	case tuiTabPM2:
		return len(model.pm2Processes)
	case tuiTabPHP:
		return len(model.php.Items)
	case tuiTabWeb:
		return len(model.webResources)
	case tuiTabDNS:
		if model.dns.Detail != nil {
			return len(model.dns.Detail.Records)
		}
		return len(model.dns.Zones)
	case tuiTabFirewall:
		return len(model.firewall.Rules)
	case tuiTabSecurity:
		return len(model.security.Items)
	case tuiTabCron:
		return len(model.cron.Jobs)
	case tuiTabDatabases:
		return len(model.databases.Items)
	case tuiTabFiles:
		return len(model.files.Entries)
	case tuiTabBackups:
		return len(model.backupJobs) + len(model.backups)
	case tuiTabSnapshots:
		return len(model.encryptedSnapshots.Snapshots)
	case tuiTabAudit:
		return len(filteredTUIAuditEntries(model.audit.Entries, model.auditFilter))
	case tuiTabDeploy:
		return deployTargetCount(model.deploy) + deployJobCount(model.deploy)
	case tuiTabAlerts:
		return alertsItemCount(model.alerts)
	case tuiTabCloudflare:
		return cloudflareItemCount(model.cloudflare)
	case tuiTabUsers:
		return len(model.users.Users)
	case tuiTabMail:
		return mailTUIItemCount(model.mail)
	default:
		return 0
	}
}

func (model tuiModel) selectRelativeTarget(delta int) (tea.Model, tea.Cmd) {
	if len(model.snapshot.Targets) == 0 {
		return model, nil
	}
	index := 0
	for i, target := range model.snapshot.Targets {
		if target.ID == model.selectedTargetID {
			index = i
			break
		}
	}
	return model.selectTarget(model.snapshot.Targets[wrapIndex(index+delta, len(model.snapshot.Targets))].ID)
}

func (model tuiModel) selectTarget(id string) (tea.Model, tea.Cmd) {
	if id == model.selectedTargetID {
		return model, nil
	}
	model.selectedTargetID = id
	model.cursor = 0
	model.loading = true
	if target, ok := findTUITarget(model.snapshot.Targets, id); ok {
		model.snapshot.Selected = target
	} else {
		model.snapshot.Selected = tuiTarget{ID: id, Name: id, Local: id == localTargetID}
	}
	model.snapshot.Host = tuiHostSummary{}
	model.snapshot.HostAvailable = false
	model.snapshot.Services = nil
	model.snapshot.ServicesAvailable = false
	model.snapshot.Processes = nil
	model.snapshot.ProcessesAvailable = false
	model.snapshot.Warnings = nil
	model.snapshot.FetchedAt = time.Time{}
	model.diskSelected = map[string]bool{}
	model.diskDiagnostics = tuiDiskDiagnosticsState{}
	model.diskDiagnosticsTarget = ""
	model.snapshot.CleanupLoaded = false
	model.snapshot.CleanupTargets = nil
	model.containers = nil
	model.containersTarget = ""
	model.containersLoaded = false
	model.pm2Processes = nil
	model.pm2Target = ""
	model.pm2Loaded = false
	model.php = tuiPHPState{}
	model.phpTarget = ""
	model.phpLoaded = false
	model.logSources = nil
	model.logsTarget = ""
	model.logSourcesLoaded = false
	model.webResources = nil
	model.webWarnings = nil
	model.webTarget = ""
	model.webLoaded = false
	model.dns = tuiDNSState{}
	model.dnsTarget = ""
	model.dnsLoaded = false
	model.firewall = tuiFirewallState{}
	model.firewallTarget = ""
	model.firewallLoaded = false
	model.security = tuiSecurityState{}
	model.securityTarget = ""
	model.securityLoaded = false
	model.cron = tuiCronState{}
	model.cronTarget = ""
	model.cronLoaded = false
	model.databases = tuiDatabaseState{}
	model.databasesTarget = ""
	model.databasesLoaded = false
	model.files = tuiFileState{}
	model.filesTarget = ""
	model.filesLoaded = false
	model.backups = nil
	model.backupJobs = nil
	model.backupStorage = tuiBackupStorage{}
	model.backupVhosts = nil
	model.backupVhostMax = 0
	model.backupWarnings = nil
	model.backupsTarget = ""
	model.backupsLoaded = false
	model.encryptedSnapshots = tuiSnapshotState{}
	model.encryptedSnapshotsTarget = ""
	model.encryptedSnapshotsLoaded = false
	model.audit = tuiAuditState{}
	model.auditTarget = ""
	model.auditLoaded = false
	model.auditFilter = ""
	model.updates = tuiUpdateState{}
	model.updatesTarget = ""
	model.updatesLoaded = false
	model.deploy = tuiDeployState{}
	model.deployTarget = ""
	model.deployLoaded = false
	model.alerts = tuiAlertsState{}
	model.alertsTarget = ""
	model.alertsLoaded = false
	model.cloudflare = tuiCloudflareState{}
	model.cloudflareTarget = ""
	model.cloudflareLoaded = false
	model.users = tuiUsersState{}
	model.usersTarget = ""
	model.usersLoaded = false
	model.mail = tuiMailState{}
	model.mailTarget = ""
	model.mailLoaded = false
	model.resourceLoading = false
	model.notice = "Switching server…"
	model.noticeError = false
	return model, loadTUISnapshotCmd(model.ctx, model.client, id, model.snapshot.Targets, false)
}

func (model tuiModel) activateCurrent() (tea.Model, tea.Cmd) {
	switch model.tab {
	case tuiTabServers:
		if model.cursor < len(model.snapshot.Targets) {
			return model.selectTarget(model.snapshot.Targets[model.cursor].ID)
		}
	case tuiTabServices:
		model.openServiceActions()
	case tuiTabProcesses:
		model.openProcessActions()
	case tuiTabMaintenance:
		model.openMaintenanceConfirmation()
	case tuiTabDisk:
		if !model.snapshot.CleanupLoaded {
			return model.scanDisk()
		}
		model.toggleDiskTarget()
	case tuiTabContainers:
		if !model.containersLoaded {
			return model.loadContainers()
		}
		model.openContainerActions()
	case tuiTabLogs:
		if !model.logSourcesLoaded {
			return model.loadLogSources()
		}
		return model.openLogViewer()
	case tuiTabPM2:
		if !model.pm2Loaded {
			return model.loadPM2()
		}
		model.openPM2Actions()
	case tuiTabPHP:
		if !model.phpLoaded {
			return model.loadPHP()
		}
		return model.activatePHPItem()
	case tuiTabWeb:
		if !model.webLoaded {
			return model.loadWeb()
		}
		model.openWebActions()
	case tuiTabDNS:
		return model.openDNSZone()
	case tuiTabFirewall:
		if !model.firewallLoaded {
			return model.loadFirewall()
		}
		model.openFirewallRuleActions()
	case tuiTabSecurity:
		if !model.securityLoaded {
			return model.loadSecurity()
		}
		model.activateSecurityItem()
	case tuiTabCron:
		if !model.cronLoaded {
			return model.loadCron()
		}
		model.openCronJobActions()
	case tuiTabDatabases:
		if !model.databasesLoaded {
			return model.loadDatabases()
		}
		model.openDatabaseActions()
	case tuiTabFiles:
		if !model.filesLoaded {
			return model.loadFiles("")
		}
		return model.activateFileEntry()
	case tuiTabBackups:
		if !model.backupsLoaded {
			return model.loadBackups()
		}
		if model.cursor < len(model.backupJobs) {
			model.openBackupJobViewer()
			return model, nil
		}
		model.openBackupActions()
	case tuiTabSnapshots:
		if !model.encryptedSnapshotsLoaded {
			return model.loadEncryptedSnapshots()
		}
		model.openEncryptedSnapshotRestore()
	case tuiTabAudit:
		if !model.auditLoaded {
			return model.loadAudit()
		}
	case tuiTabDeploy:
		return model.activateDeployItem()
	case tuiTabAlerts:
		return model.activateAlertItem()
	case tuiTabCloudflare:
		return model.activateCloudflareItem()
	case tuiTabUsers:
		return model.activateUserItem()
	case tuiTabMail:
		return model.activateMailItem()
	}
	return model, nil
}

func (model *tuiModel) openServiceActions() {
	if model.cursor >= len(model.snapshot.Services) {
		return
	}
	if reason := model.unavailableReason(agenthub.CapabilityServiceAction); reason != "" {
		model.notice, model.noticeError = reason, true
		return
	}
	service := model.snapshot.Services[model.cursor]
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Manage " + truncateTUI(service.Name, 48),
		Body: []string{"Choose a bounded systemd action. A confirmation follows."},
		Options: []tuiDialogOption{
			{Label: "Start", Action: "start"},
			{Label: "Restart", Action: "restart"},
			{Label: "Stop", Action: "stop", Dangerous: true},
		},
		Operation: tuiOperation{Kind: tuiOperationService, Target: model.snapshot.Selected, Service: service.Name, Label: service.Name},
	}
}

func (model *tuiModel) openProcessActions() {
	if model.cursor >= len(model.snapshot.Processes) {
		return
	}
	if reason := model.unavailableReason(agenthub.CapabilityProcessSignal); reason != "" {
		model.notice, model.noticeError = reason, true
		return
	}
	process := model.snapshot.Processes[model.cursor]
	if process.StartTime == 0 {
		model.notice, model.noticeError = "This process has no stable start-time identity and cannot be signalled", true
		return
	}
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: fmt.Sprintf("Signal PID %d", process.PID),
		Body: []string{truncateTUI(process.Command, 72), "TERM is the safe default; KILL forces immediate termination."},
		Options: []tuiDialogOption{
			{Label: "Send TERM", Action: "term"},
			{Label: "Send KILL", Action: "kill", Dangerous: true},
		},
		Operation: tuiOperation{Kind: tuiOperationProcess, Target: model.snapshot.Selected, Process: process, Label: fmt.Sprintf("PID %d", process.PID)},
	}
}

func (model *tuiModel) openMaintenanceConfirmation() {
	if model.cursor >= len(tuiMaintenanceActions) {
		return
	}
	if reason := model.unavailableReason(agenthub.CapabilityHostAction); reason != "" {
		model.notice, model.noticeError = reason, true
		return
	}
	action := tuiMaintenanceActions[model.cursor]
	operation := tuiOperation{
		Kind: tuiOperationHost, Target: model.snapshot.Selected, Action: action.ID,
		Label: action.Label, Dangerous: action.Dangerous,
	}
	model.openConfirmation(operation, []string{action.Description, "Target: " + model.snapshot.Selected.label()})
}

func (model *tuiModel) openContainerActions() {
	if model.cursor >= len(model.containers) {
		return
	}
	if reason := model.unavailableReason(agenthub.CapabilityContainerAction); reason != "" {
		model.notice, model.noticeError = reason, true
		return
	}
	container := model.containers[model.cursor]
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Manage container " + truncateTUI(container.Name, 40),
		Body: []string{truncateTUI(container.Image, 64), "Choose a fixed lifecycle action. A confirmation follows."},
		Options: []tuiDialogOption{
			{Label: "Start", Action: "start"},
			{Label: "Restart", Action: "restart"},
			{Label: "Stop", Action: "stop", Dangerous: true},
		},
		Operation: tuiOperation{Kind: tuiOperationContainer, Target: model.snapshot.Selected, Container: container, Label: container.Name},
	}
}

func (model *tuiModel) openPM2Actions() {
	if model.cursor >= len(model.pm2Processes) {
		return
	}
	if reason := model.unavailableReason(agenthub.CapabilityPM2Action); reason != "" {
		model.notice, model.noticeError = reason, true
		return
	}
	process := model.pm2Processes[model.cursor]
	options := []tuiDialogOption{
		{Label: "Start", Action: "start"},
		{Label: "Restart", Action: "restart"},
		{Label: "Reload", Action: "reload"},
		{Label: "Stop", Action: "stop", Dangerous: true},
	}
	if model.snapshot.Selected.Local {
		options = append(options,
			tuiDialogOption{Label: "Save process list", Action: "save"},
			tuiDialogOption{Label: "Delete from PM2", Action: "delete", Dangerous: true},
		)
	}
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Manage PM2 process " + truncateTUI(process.Name, 40),
		Body:      []string{fmt.Sprintf("PID %d · %s · %s", process.PID, valueOrNA(process.Status), valueOrNA(process.Mode)), "Press V from the process list to view logs."},
		Options:   options,
		Operation: tuiOperation{Kind: tuiOperationPM2, Target: model.snapshot.Selected, PM2Process: process, Label: process.Name},
	}
}

func (model tuiModel) openPM2LogViewer() (tea.Model, tea.Cmd) {
	if !model.pm2Loaded {
		return model.loadPM2()
	}
	if model.cursor >= len(model.pm2Processes) {
		return model, nil
	}
	if reason := model.unavailableReason(agenthub.CapabilityPM2Read); reason != "" {
		model.notice, model.noticeError = reason, true
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading the latest 200 PM2 log lines…"
	model.noticeError = false
	return model, loadTUIPM2LogsCmd(model.ctx, model.client, model.snapshot.Selected, model.pm2Processes[model.cursor])
}

func (model tuiModel) openLogViewer() (tea.Model, tea.Cmd) {
	if model.cursor >= len(model.logSources) {
		return model, nil
	}
	if reason := model.unavailableReason(agenthub.CapabilityLogsRead); reason != "" {
		model.notice, model.noticeError = reason, true
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading the latest 200 log lines…"
	model.noticeError = false
	return model, loadTUILogLinesCmd(model.ctx, model.client, model.snapshot.Selected, model.logSources[model.cursor])
}

func (model tuiModel) loadContainers() (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	if reason := model.unavailableReason(agenthub.CapabilityContainerRead); reason != "" {
		model.notice, model.noticeError = reason, true
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading container inventory…"
	model.noticeError = false
	return model, loadTUIContainersCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model tuiModel) loadLogSources() (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	if reason := model.unavailableReason(agenthub.CapabilityLogsRead); reason != "" {
		model.notice, model.noticeError = reason, true
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Discovering readable log sources…"
	model.noticeError = false
	return model, loadTUILogSourcesCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model tuiModel) loadPM2() (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	if reason := model.unavailableReason(agenthub.CapabilityPM2Read); reason != "" {
		model.notice, model.noticeError = reason, true
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading PM2 process inventory…"
	model.noticeError = false
	return model, loadTUIPM2Cmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model tuiModel) loadPHP() (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	if reason := model.unavailableReason(agenthub.CapabilityPHPRead); reason != "" {
		model.notice, model.noticeError = reason, true
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading PHP-FPM versions and pool inventory…"
	model.noticeError = false
	return model, loadTUIPHPCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model tuiModel) activatePHPItem() (tea.Model, tea.Cmd) {
	if model.cursor < 0 || model.cursor >= len(model.php.Items) {
		return model, nil
	}
	item := model.php.Items[model.cursor]
	if item.Kind == tuiPHPPoolItem {
		if !model.php.Readable {
			model.notice, model.noticeError = "PHP-FPM pool configuration is unavailable for this target", true
			return model, nil
		}
		model.resourceLoading = true
		model.notice = "Loading observed PHP-FPM pool configuration…"
		model.noticeError = false
		return model, loadTUIPHPConfigCmd(model.ctx, model.client, model.snapshot.Selected, item)
	}
	model.openPHPVersionActions()
	return model, nil
}

func (model *tuiModel) openPHPVersionActions() {
	if model.cursor < 0 || model.cursor >= len(model.php.Items) {
		return
	}
	item := model.php.Items[model.cursor]
	if item.Kind != tuiPHPVersionItem {
		return
	}
	if !model.php.Actionable {
		model.notice, model.noticeError = "PHP-FPM lifecycle actions are unavailable for this target", true
		return
	}
	if item.Masked || !item.Runtime {
		model.notice, model.noticeError = "PHP-FPM runtime is masked or has no executable binary", true
		return
	}
	operation := tuiOperation{Kind: tuiOperationPHP, Target: model.snapshot.Selected, PHP: item, Label: "PHP " + item.Version}
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Manage PHP " + item.Version,
		Body: []string{"Every lifecycle action validates the PHP-FPM configuration first.", "Choose a bounded action. A confirmation follows."},
		Options: []tuiDialogOption{
			{Label: "Test configuration", Action: "test"},
			{Label: "Reload PHP-FPM", Action: "reload"},
			{Label: "Restart PHP-FPM", Action: "restart", Dangerous: true},
		},
		Operation: operation,
	}
}

func (model tuiModel) loadWeb() (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading Nginx, domain, and SSL inventory…"
	model.noticeError = false
	return model, loadTUIWebCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model tuiModel) loadFirewall() (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	if reason := model.unavailableReason(agenthub.CapabilityFirewallRead); reason != "" {
		model.notice, model.noticeError = reason, true
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading observed firewall policy and rules…"
	model.noticeError = false
	return model, loadTUIFirewallCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model tuiModel) loadSecurity() (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading security score and Fail2Ban inventory…"
	model.noticeError = false
	return model, loadTUISecurityCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model *tuiModel) activateSecurityItem() {
	if model.cursor < 0 || model.cursor >= len(model.security.Items) {
		return
	}
	item := model.security.Items[model.cursor]
	if item.Kind == tuiSecurityBlacklistItem || item.Kind == tuiSecurityWhitelistItem {
		if !model.snapshot.Selected.Local || !model.security.Supported {
			model.notice, model.noticeError = "Security IP blacklist/whitelist mutations run only on the panel host", true
			return
		}
		listType := string(tuiSecurityAccessBlacklist)
		if item.Kind == tuiSecurityWhitelistItem {
			listType = string(tuiSecurityAccessWhitelist)
		}
		entryIP := item.AccessEntry.IP
		if entryIP == "" {
			entryIP = item.IP
		}
		displayIP := tuiSecurityAccessDisplayText(entryIP, 128)
		operation := tuiSecurityAccessListOperation{
			Target: model.snapshot.Selected, Action: "delete", ListType: listType,
			IP: entryIP, Label: "Delete " + displayIP + " from " + listType,
		}
		model.openSecurityAccessConfirmation(operation)
		return
	}
	if item.Kind != tuiSecurityBannedIPItem {
		if item.Kind == tuiSecurityJailItem {
			model.notice = "Jail rows are observed inventory; select a banned IP row to unban or use the scripted CLI to ban an IP"
		} else {
			model.notice = "Security checks are read-only observations"
		}
		model.noticeError = false
		return
	}
	if !model.security.Fail2Ban.Available || model.security.Fail2Ban.State != "healthy" {
		model.notice, model.noticeError = "Fail2Ban is not ready for IP mutations", true
		return
	}
	operation := tuiOperation{
		Kind: tuiOperationSecurity, Target: model.snapshot.Selected, Action: "unban", Security: item,
		Label: "Unban " + item.IP + " from " + item.Jail, Dangerous: true,
	}
	model.openConfirmation(operation, confirmationBody(operation))
}

func (model tuiModel) loadBackups() (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	if reason := model.unavailableReason(agenthub.CapabilityBackupRead); reason != "" {
		model.notice, model.noticeError = reason, true
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading backup artifacts and managed plans…"
	model.noticeError = false
	return model, loadTUIBackupsCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model tuiModel) loadEncryptedSnapshots() (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading encrypted snapshot repository state…"
	model.noticeError = false
	return model, loadTUISnapshotsCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model *tuiModel) openEncryptedSnapshotCreate() {
	state := model.encryptedSnapshots
	if !model.encryptedSnapshotsLoaded || !state.Supported {
		model.notice, model.noticeError = "Encrypted snapshots are available only for the Local HServer hub", true
		return
	}
	if !state.ready() {
		message := strings.TrimSpace(state.DestinationMessage)
		if message == "" {
			message = "The selected encrypted snapshot destination is not healthy"
		}
		model.notice, model.noticeError = message, true
		return
	}
	operation := tuiOperation{
		Kind: tuiOperationSnapshot, Target: model.snapshot.Selected, Action: "create",
		Label: "Create encrypted snapshot",
	}
	model.openConfirmation(operation, confirmationBody(operation))
}

func (model *tuiModel) openEncryptedSnapshotDestination() {
	if !model.encryptedSnapshotsLoaded || !model.encryptedSnapshots.Supported {
		model.notice, model.noticeError = "Encrypted snapshot destinations are configured on the Local HServer hub", true
		return
	}
	model.dialog = tuiDialog{
		Mode:  tuiDialogChoices,
		Title: "Select encrypted snapshot destination",
		Body: []string{
			"Only the provider choice changes; the complete observed manifest and retention policy are preserved.",
			"Provider credentials remain in protected installation-owned environment or secret files.",
		},
		Options: []tuiDialogOption{
			{Label: "Google Drive", Action: "destination-gdrive"},
			{Label: "S3-compatible / MinIO", Action: "destination-s3"},
		},
		Operation: tuiOperation{Kind: tuiOperationSnapshot, Target: model.snapshot.Selected},
	}
}

func (model *tuiModel) openEncryptedSnapshotRestore() {
	if !model.encryptedSnapshotsLoaded || !model.encryptedSnapshots.Supported {
		return
	}
	if model.cursor < 0 || model.cursor >= len(model.encryptedSnapshots.Snapshots) {
		return
	}
	if !model.encryptedSnapshots.ready() {
		model.notice, model.noticeError = "The encrypted snapshot destination is not ready for restore", true
		return
	}
	snapshot := model.encryptedSnapshots.Snapshots[model.cursor]
	options := []tuiDialogOption{{Label: "Extract the complete snapshot to staging", Action: "restore-all", Dangerous: true}}
	if len(model.encryptedSnapshotManifestSelectors()) > 0 {
		options = append(options, tuiDialogOption{Label: "Select installation-owned manifest paths", Action: "restore-manifest", Dangerous: true})
	}
	if len(model.encryptedSnapshots.Vhosts) > 0 {
		options = append(options, tuiDialogOption{Label: "Select observed website folders", Action: "restore-vhosts", Dangerous: true})
	}
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Restore encrypted snapshot · " + snapshot.ID,
		Body: []string{
			"Every restore extracts only into HServer's fixed staging directory.",
			"Choose the complete snapshot, installation-owned manifest identities, or currently observed website folders.",
		},
		Options: options,
		Operation: tuiOperation{
			Kind: tuiOperationSnapshot, Target: model.snapshot.Selected, EncryptedSnapshot: snapshot,
		},
	}
}

func (model tuiModel) encryptedSnapshotManifestSelectors() []tuiDialogOption {
	selectors := make([]tuiDialogOption, 0, len(model.encryptedSnapshots.Manifest))
	for _, entry := range model.encryptedSnapshots.Manifest {
		if _, selectable := snapshotRestoreManifest[entry.ID]; !selectable {
			continue
		}
		label := strings.TrimSpace(entry.Label)
		if label == "" {
			label = entry.ID
		}
		selectors = append(selectors, tuiDialogOption{Label: label + " · " + entry.ID, Action: entry.ID})
	}
	return selectors
}

func (model *tuiModel) openEncryptedSnapshotSelectors(operation tuiOperation) {
	selectors := model.encryptedSnapshotManifestSelectors()
	title := "Select snapshot manifest paths"
	maximum := len(snapshotRestoreManifest)
	if operation.Action == "restore-vhosts" {
		selectors = make([]tuiDialogOption, 0, len(model.encryptedSnapshots.Vhosts))
		for _, name := range model.encryptedSnapshots.Vhosts {
			selectors = append(selectors, tuiDialogOption{Label: name, Action: name})
		}
		title = "Select snapshot website folders"
		maximum = 16
	}
	if len(selectors) == 0 {
		model.notice, model.noticeError = "No bounded encrypted snapshot selectors are currently available", true
		model.dialog = tuiDialog{}
		return
	}
	model.dialog = tuiDialog{
		Mode: tuiDialogSnapshotSelectors, Title: title,
		Body: []string{
			"Space toggles · A selects all within the limit · Enter continues to a separate confirmation.",
			"The server resolves every identity below its installation-owned paths and repeats validation.",
		},
		Operation: operation, SnapshotSelectors: selectors,
		SnapshotSelected: make(map[string]bool), SnapshotMax: maximum,
	}
}

func (model tuiModel) loadCron() (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	if reason := model.unavailableReason(agenthub.CapabilityCronRead); reason != "" {
		model.notice, model.noticeError = reason, true
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading scheduled jobs and cron service state…"
	model.noticeError = false
	return model, loadTUICronCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model tuiModel) loadDatabases() (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	if reason := model.unavailableReason(agenthub.CapabilityDatabaseRead); reason != "" {
		model.notice, model.noticeError = reason, true
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading database engines, databases, and connection state…"
	model.noticeError = false
	return model, loadTUIDatabasesCmd(model.ctx, model.client, model.snapshot.Selected)
}

func (model tuiModel) loadFiles(path string) (tea.Model, tea.Cmd) {
	if model.resourceLoading {
		return model, nil
	}
	if reason := model.unavailableReason(agenthub.CapabilityFilesRead); reason != "" {
		model.notice, model.noticeError = reason, true
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading configured file roots and directory inventory…"
	model.noticeError = false
	return model, loadTUIFilesCmd(model.ctx, model.client, model.snapshot.Selected, path)
}

func (model tuiModel) activateFileEntry() (tea.Model, tea.Cmd) {
	if model.cursor < 0 || model.cursor >= len(model.files.Entries) {
		return model, nil
	}
	entry := model.files.Entries[model.cursor]
	if entry.Type == "directory" {
		return model.loadFiles(entry.Path)
	}
	if entry.Type != "file" {
		model.notice, model.noticeError = "Only observed regular text files can be opened in the CLI viewer", true
		return model, nil
	}
	model.resourceLoading = true
	model.notice = "Loading text file…"
	model.noticeError = false
	return model, loadTUIFileContentCmd(model.ctx, model.client, model.snapshot.Selected, entry.Path)
}

func (model tuiModel) openFileParent() (tea.Model, tea.Cmd) {
	if !model.filesLoaded || model.files.CurrentPath == "" || model.files.CurrentRoot == "" {
		return model, nil
	}
	if model.files.CurrentPath == model.files.CurrentRoot {
		model.notice, model.noticeError = "Already at the current file root", false
		return model, nil
	}
	parent := filepath.Dir(model.files.CurrentPath)
	if !pathWithinRoots(parent, []string{model.files.CurrentRoot}) {
		parent = model.files.CurrentRoot
	}
	return model.loadFiles(parent)
}

func (model tuiModel) switchFileRoot(delta int) (tea.Model, tea.Cmd) {
	if !model.filesLoaded || len(model.files.Roots) == 0 {
		return model, nil
	}
	index := 0
	for i, root := range model.files.Roots {
		if root == model.files.CurrentRoot {
			index = i
			break
		}
	}
	return model.loadFiles(model.files.Roots[wrapIndex(index+delta, len(model.files.Roots))])
}

func (model *tuiModel) openFileDeleteConfirmation() {
	if !model.snapshot.Selected.Local || !model.files.Manageable {
		model.notice, model.noticeError = "Managed-node files are read-only in the TUI; use the checksum-protected files save command to edit text", true
		return
	}
	if model.cursor < 0 || model.cursor >= len(model.files.Entries) {
		return
	}
	entry := model.files.Entries[model.cursor]
	if entry.Type == "symlink" {
		model.notice, model.noticeError = "Deleting symlinks is unavailable through hserverctl", true
		return
	}
	operation := tuiOperation{
		Kind: tuiOperationFile, Target: model.snapshot.Selected, Action: "delete", File: entry,
		Label: "Delete " + entry.Name, Dangerous: true,
	}
	model.openConfirmation(operation, confirmationBody(operation))
}

func (model *tuiModel) openDatabaseActions() {
	if model.cursor < 0 || model.cursor >= len(model.databases.Items) {
		return
	}
	item := model.databases.Items[model.cursor]
	operation := tuiOperation{Kind: tuiOperationDatabase, Target: model.snapshot.Selected, Database: item}
	if model.snapshot.Selected.Local {
		if item.Kind != tuiDatabaseRowItem || !model.databases.Manageable {
			model.notice, model.noticeError = "Select a local database row to manage it; engine rows are inventory-only", true
			return
		}
		operation.Action = "drop"
		operation.Label = "Drop " + item.EngineName + " database " + item.Name
		operation.Dangerous = true
		model.openConfirmation(operation, confirmationBody(operation))
		return
	}
	if item.Kind != tuiDatabaseEngineItem {
		model.notice, model.noticeError = "Managed database rows are read-only; select an engine row for restart and health check", true
		return
	}
	if !model.databases.Restartable || !model.snapshot.Selected.capability(agenthub.CapabilityDatabaseAction) {
		model.notice, model.noticeError = "Managed agent does not advertise database.action for this engine", true
		return
	}
	operation.Action = "restart"
	operation.Label = "Restart and health-check " + item.EngineName
	operation.Dangerous = true
	model.openConfirmation(operation, confirmationBody(operation))
}

func (model *tuiModel) openCronJobActions() {
	if model.cursor < 0 || model.cursor >= len(model.cron.Jobs) {
		return
	}
	job := model.cron.Jobs[model.cursor]
	options := make([]tuiDialogOption, 0, 3)
	if model.cron.Manageable && model.snapshot.Selected.capability(agenthub.CapabilityCronWrite) {
		action, label := "enable", "Enable scheduled job"
		if job.Enabled {
			action, label = "disable", "Disable scheduled job"
		}
		options = append(options, tuiDialogOption{Label: label, Action: action})
	}
	if !model.snapshot.Selected.Local && model.cron.Runnable && model.snapshot.Selected.capability(agenthub.CapabilityCronRun) {
		options = append(options, tuiDialogOption{Label: "Run job now", Action: "run", Dangerous: true})
	}
	if model.cron.Manageable && model.snapshot.Selected.capability(agenthub.CapabilityCronWrite) {
		options = append(options, tuiDialogOption{Label: "Delete scheduled job", Action: "delete", Dangerous: true})
	}
	if len(options) == 0 {
		model.notice, model.noticeError = "This cron inventory is read-only for the active server and capability set", true
		return
	}
	operation := tuiOperation{Kind: tuiOperationCron, Target: model.snapshot.Selected, CronJob: job, CronState: model.cron, Label: cronJobLabel(job)}
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Manage cron job · " + truncateTUI(job.ID, 28),
		Body:    []string{truncateTUI(job.Schedule+" · "+job.User+" · "+job.Command, 72), "Every mutation rechecks the observed job identity; a separate confirmation follows."},
		Options: options, Operation: operation,
	}
}

func cronJobLabel(job tuiCronJob) string {
	return fmt.Sprintf("%s · %s · %s", job.ID, job.Schedule, job.User)
}

func (model *tuiModel) openFirewallAddProfiles() {
	if !model.firewallLoaded {
		model.notice, model.noticeError = "Load firewall inventory before adding a rule", true
		return
	}
	if !model.firewall.Manageable {
		model.notice, model.noticeError = "Firewall inventory is read-only on this server", true
		return
	}
	if reason := model.unavailableReason(agenthub.CapabilityFirewallWrite); reason != "" {
		model.notice, model.noticeError = reason, true
		return
	}
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Add common inbound firewall rule",
		Body: []string{
			"Choose a fixed allow profile for any source. Scripted CLI commands support bounded custom source and port values.",
			"The server or agent rechecks local protection and persistence policy before accepting the rule.",
		},
		Options: []tuiDialogOption{
			{Label: "Allow SSH · TCP 22", Action: "firewall-add-ssh"},
			{Label: "Allow HTTP · TCP 80", Action: "firewall-add-http"},
			{Label: "Allow HTTPS · TCP 443", Action: "firewall-add-https"},
			{Label: "Allow DNS · UDP 53", Action: "firewall-add-dns"},
		},
		Operation: tuiOperation{Kind: tuiOperationFirewall, Target: model.snapshot.Selected, FirewallState: model.firewall},
	}
}

func (model *tuiModel) openFirewallToggle() {
	if !model.snapshot.Selected.Local {
		model.notice, model.noticeError = "Managed firewall activation is owned by the agent installation", true
		return
	}
	if !model.firewallLoaded || !model.firewall.Manageable {
		model.notice, model.noticeError = "UFW is not currently manageable on this server", true
		return
	}
	action := "enable"
	label := "Enable UFW firewall"
	dangerous := false
	if model.firewall.Active {
		action = "disable"
		label = "Disable UFW firewall"
		dangerous = true
	}
	operation := tuiOperation{
		Kind: tuiOperationFirewall, Target: model.snapshot.Selected, Action: action,
		FirewallState: model.firewall, Label: label, Dangerous: dangerous,
	}
	model.openConfirmation(operation, confirmationBody(operation))
}

func (model *tuiModel) openFirewallRuleActions() {
	if model.cursor < 0 || model.cursor >= len(model.firewall.Rules) {
		return
	}
	rule := model.firewall.Rules[model.cursor]
	if !model.firewall.Manageable || !rule.Managed {
		model.notice, model.noticeError = "This observed firewall rule is read-only and is not owned by HServer", true
		return
	}
	if reason := model.unavailableReason(agenthub.CapabilityFirewallWrite); reason != "" {
		model.notice, model.noticeError = reason, true
		return
	}
	operation := tuiOperation{
		Kind: tuiOperationFirewall, Target: model.snapshot.Selected, FirewallRule: rule,
		FirewallState: model.firewall, Label: firewallRuleLabel(rule),
	}
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Manage firewall rule · " + truncateTUI(firewallRuleLabel(rule), 42),
		Body:      []string{"Only this currently observed rule identity can be removed. A separate disruptive confirmation follows."},
		Options:   []tuiDialogOption{{Label: "Delete firewall rule", Action: "delete", Dangerous: true}},
		Operation: operation,
	}
}

func firewallRuleLabel(rule tuiFirewallRule) string {
	identity := rule.ID
	if identity == "" && rule.Number > 0 {
		identity = strconv.Itoa(rule.Number)
	}
	return fmt.Sprintf("%s %s/%s from %s · %s", valueOrNA(rule.Action), valueOrNA(rule.Protocol), valueOrNA(rule.Target), valueOrNA(rule.Source), valueOrNA(identity))
}

func (model *tuiModel) openBackupCreateWizard() {
	if !model.snapshot.Selected.Local {
		model.notice, model.noticeError = "Managed nodes run installation-owned plans; select a plan row", true
		return
	}
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Create local backup",
		Body: []string{
			"Choose a fixed server-validated profile. Empty selectors mean all databases or all configured website files.",
			"Compression uses the portable level-6 default; no filesystem path or command can be entered.",
			"Choose an engine configured on this host; unavailable dependencies are reported as failed jobs before an artifact is accepted.",
		},
		Options: []tuiDialogOption{
			{Label: "Full application · PostgreSQL + website files", Action: "create-full-postgresql"},
			{Label: "Full application · MariaDB + website files", Action: "create-full-mariadb"},
			{Label: "All PostgreSQL databases", Action: "create-database-postgresql"},
			{Label: "All MariaDB databases", Action: "create-database-mariadb"},
			{Label: "All configured website files", Action: "create-files"},
		},
		Operation: tuiOperation{Kind: tuiOperationBackup, Target: model.snapshot.Selected},
	}
}

func (model *tuiModel) openBackupScopeChoices(operation tuiOperation) {
	options := []tuiDialogOption{{Label: "All configured website files", Action: "scope-all"}}
	if len(model.backupVhosts) > 0 && model.backupVhostMax > 0 {
		options = append(options, tuiDialogOption{Label: "Select observed website folders", Action: "scope-select"})
	}
	body := []string{
		"All uses the installation-owned vhost root without accepting a filesystem path.",
	}
	if len(model.backupVhosts) > 0 && model.backupVhostMax > 0 {
		body = append(body, fmt.Sprintf("HServer observed %d eligible website folder(s); a selective backup accepts at most %d.", len(model.backupVhosts), model.backupVhostMax))
	} else {
		body = append(body, "Selective targets are unavailable; all configured website files remains available.")
	}
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Backup file scope", Body: body, Options: options, Operation: operation,
	}
}

func (model *tuiModel) openBackupVhostChoices(operation tuiOperation) {
	model.dialog = tuiDialog{
		Mode: tuiDialogBackupVhosts, Title: "Select website folders", Operation: operation,
		BackupVhosts:   append([]string(nil), model.backupVhosts...),
		BackupSelected: make(map[string]bool), BackupVhostMax: model.backupVhostMax,
	}
}

func (model *tuiModel) advanceBackupCreate(operation tuiOperation) {
	operation.Label = "Create " + operation.BackupCreate.label()
	if operation.BackupCreate.Type == "full" {
		model.openBackupRetentionChoices(operation)
		return
	}
	model.openConfirmation(operation, confirmationBody(operation))
}

func (model *tuiModel) openBackupRetentionChoices(operation tuiOperation) {
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Full-backup retention",
		Body: []string{
			"Retention runs only after a successful full backup.",
			"A keep limit removes older completed full-backup artifacts beyond that count.",
		},
		Options: []tuiDialogOption{
			{Label: "Keep all completed full backups", Action: "retention-0"},
			{Label: "Keep latest 7 completed full backups", Action: "retention-7", Dangerous: true},
			{Label: "Keep latest 14 completed full backups", Action: "retention-14", Dangerous: true},
			{Label: "Keep latest 30 completed full backups", Action: "retention-30", Dangerous: true},
		},
		Operation: operation,
	}
}

func (model *tuiModel) openBackupActions() {
	backupIndex := model.cursor - len(model.backupJobs)
	if backupIndex < 0 || backupIndex >= len(model.backups) {
		return
	}
	item := model.backups[backupIndex]
	if item.Managed {
		if reason := model.unavailableReason(agenthub.CapabilityBackupRun); reason != "" {
			model.notice, model.noticeError = reason, true
			return
		}
		model.dialog = tuiDialog{
			Mode: tuiDialogChoices, Title: "Manage backup plan " + truncateTUI(item.Name, 40),
			Body:      []string{truncateTUI(item.Service, 64), "Only the installation-owned service mapped to this plan can run."},
			Options:   []tuiDialogOption{{Label: "Run backup plan", Action: "run"}},
			Operation: tuiOperation{Kind: tuiOperationBackup, Target: model.snapshot.Selected, Backup: item, Label: item.Name},
		}
		return
	}
	options := []tuiDialogOption{{Label: "Delete backup artifact", Action: "delete", Dangerous: true}}
	if item.Status == "completed" {
		options = append([]tuiDialogOption{{Label: "Validate for restore", Action: "validate"}}, options...)
	}
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Manage backup " + truncateTUI(item.Name, 40),
		Body:      []string{strings.ToUpper(valueOrNA(item.Type)) + " · " + formatTUIBytes(uint64(maxInt64(item.Size, 0))), "Validation reads the complete artifact without mutation."},
		Options:   options,
		Operation: tuiOperation{Kind: tuiOperationBackup, Target: model.snapshot.Selected, Backup: item, Label: item.Name},
	}
}

func (model *tuiModel) openBackupJobViewer() {
	if model.cursor < 0 || model.cursor >= len(model.backupJobs) {
		return
	}
	job := model.backupJobs[model.cursor]
	lines := backupJobLogLines(job)
	model.dialog = tuiDialog{
		Mode: tuiDialogLogs, Title: "Backup job · " + truncateTUI(job.Type, 24) + " · " + truncateTUI(job.ID, 32),
		BackupJob: job, LogLines: lines, LogScroll: maxInt(0, len(lines)-1),
	}
}

func (model tuiModel) hasActiveBackupJobs() bool {
	for _, job := range model.backupJobs {
		if job.active() {
			return true
		}
	}
	return false
}

func (model *tuiModel) openWebActions() {
	if model.cursor >= len(model.webResources) {
		return
	}
	resource := model.webResources[model.cursor]
	options := make([]tuiDialogOption, 0, 2)
	capability := ""
	switch resource.Kind {
	case tuiWebNginx:
		capability = agenthub.CapabilityNginxAction
		options = append(options,
			tuiDialogOption{Label: "Test Nginx configuration", Action: "test"},
			tuiDialogOption{Label: "Reload Nginx", Action: "reload"},
		)
	case tuiWebDomain:
		capability = agenthub.CapabilityDomainAction
		if resource.Enabled {
			options = append(options, tuiDialogOption{Label: "Disable domain", Action: "disable", Dangerous: true})
		} else {
			options = append(options, tuiDialogOption{Label: "Enable domain", Action: "enable"})
		}
	case tuiWebSSL:
		capability = agenthub.CapabilitySSLAction
		if !model.snapshot.Selected.Local {
			options = append(options, tuiDialogOption{Label: "Check certificate", Action: "check"})
		}
		options = append(options, tuiDialogOption{Label: "Renew certificate", Action: "renew"})
	default:
		model.notice, model.noticeError = "Unsupported web resource type", true
		return
	}
	if reason := model.unavailableReason(capability); reason != "" {
		model.notice, model.noticeError = reason, true
		return
	}
	model.dialog = tuiDialog{
		Mode: tuiDialogChoices, Title: "Manage " + strings.ToUpper(string(resource.Kind)) + " · " + truncateTUI(resource.Name, 38),
		Body:      []string{truncateTUI(resource.Detail, 72), "Choose a bounded action. A confirmation follows."},
		Options:   options,
		Operation: tuiOperation{Kind: tuiOperationWeb, Target: model.snapshot.Selected, WebResource: resource, Label: resource.Name},
	}
}

func (model *tuiModel) openDiskConfirmation() {
	if !model.snapshot.CleanupLoaded {
		model.notice, model.noticeError = "Scan cleanup targets first", true
		return
	}
	ids := model.selectedCleanupIDs()
	if len(ids) == 0 {
		model.notice, model.noticeError = "Select at least one cleanup target with Space", true
		return
	}
	if reason := model.unavailableReason(agenthub.CapabilityDiskCleanup); reason != "" {
		model.notice, model.noticeError = reason, true
		return
	}
	operation := tuiOperation{
		Kind: tuiOperationDisk, Target: model.snapshot.Selected, CleanupIDs: ids,
		Label: fmt.Sprintf("Clean %d disk target(s)", len(ids)), Dangerous: true,
	}
	model.openConfirmation(operation, []string{
		"Only the fixed cleanup target IDs returned by the server will run.",
		"Targets: " + strings.Join(ids, ", "),
	})
}

func (model *tuiModel) openConfirmation(operation tuiOperation, body []string) {
	title := "Confirm operation"
	if operation.Dangerous {
		title = "Confirm disruptive operation"
	}
	model.dialog = tuiDialog{Mode: tuiDialogConfirm, Title: title, Body: body, Operation: operation}
}

func (model tuiModel) unavailableReason(capability string) string {
	target := model.snapshot.Selected
	if target.Local {
		return ""
	}
	if !target.Online {
		return "Managed node is offline"
	}
	if !target.capability(capability) {
		return "Managed agent does not advertise " + capability
	}
	return ""
}

func (model tuiModel) scanDisk() (tea.Model, tea.Cmd) {
	if reason := model.unavailableReason(agenthub.CapabilityDiskCleanup); reason != "" {
		model.notice, model.noticeError = reason, true
		return model, nil
	}
	model.loading = true
	model.notice = "Scanning fixed cleanup targets…"
	model.noticeError = false
	return model, loadTUISnapshotCmd(model.ctx, model.client, model.selectedTargetID, model.snapshot.Targets, true)
}

func (model *tuiModel) toggleDiskTarget() {
	if model.cursor >= len(model.snapshot.CleanupTargets) {
		return
	}
	id := model.snapshot.CleanupTargets[model.cursor].ID
	if model.diskSelected[id] {
		delete(model.diskSelected, id)
		return
	}
	maximum := 20
	if !model.snapshot.Selected.Local {
		maximum = 4
	}
	if len(model.diskSelected) >= maximum {
		model.notice = fmt.Sprintf("This target accepts at most %d cleanup selections", maximum)
		model.noticeError = true
		return
	}
	model.diskSelected[id] = true
}

func (model tuiModel) selectedCleanupIDs() []string {
	ids := make([]string, 0, len(model.diskSelected))
	for _, target := range model.snapshot.CleanupTargets {
		if model.diskSelected[target.ID] {
			ids = append(ids, target.ID)
		}
	}
	return ids
}

func confirmationBody(operation tuiOperation) []string {
	switch operation.Kind {
	case tuiOperationService:
		return []string{fmt.Sprintf("%s service %s", strings.ToUpper(operation.Action), operation.Service), "Target: " + operation.Target.label()}
	case tuiOperationProcess:
		return []string{fmt.Sprintf("Send %s to PID %d", strings.ToUpper(operation.Action), operation.Process.PID), truncateTUI(operation.Process.Command, 72)}
	case tuiOperationContainer:
		return []string{
			fmt.Sprintf("%s container %s", strings.ToUpper(operation.Action), operation.Container.Name),
			"Target: " + operation.Target.label(),
		}
	case tuiOperationPM2:
		if operation.Action == "save" {
			return []string{
				"Persist the complete local PM2 process list for reboot recovery.",
				"Target: " + operation.Target.label(),
			}
		}
		if operation.Action == "delete" {
			return []string{
				fmt.Sprintf("DELETE PM2 process %s from the active list", operation.PM2Process.Name),
				"Application files are not removed. Save the process list separately to persist this deletion.",
			}
		}
		return []string{
			fmt.Sprintf("%s PM2 process %s", strings.ToUpper(operation.Action), operation.PM2Process.Name),
			"Target: " + operation.Target.label(),
		}
	case tuiOperationPHP:
		body := []string{
			fmt.Sprintf("%s PHP-FPM %s", strings.ToUpper(operation.Action), operation.PHP.Version),
			"Target: " + operation.Target.label(),
		}
		if operation.Action == "test" {
			body = append(body, "Validate the complete PHP-FPM configuration without calling systemd.")
		} else {
			body = append(body, "The current PHP-FPM configuration is tested before the lifecycle action.")
		}
		if operation.Action == "reload" {
			body = append(body, "Reload asks PHP-FPM to replace workers gracefully.")
		}
		if operation.Action == "restart" {
			body = append(body, "Restart can briefly interrupt requests served by this PHP version.")
		}
		return body
	case tuiOperationSecurity:
		return []string{
			"Remove the observed Fail2Ban block for " + operation.Security.IP,
			"Jail: " + operation.Security.Jail,
			"The current healthy jail inventory and banned IP identity are rechecked before mutation.",
		}
	case tuiOperationWeb:
		return []string{
			fmt.Sprintf("%s %s resource %s", strings.ToUpper(operation.Action), strings.ToUpper(string(operation.WebResource.Kind)), operation.WebResource.Name),
			"Target: " + operation.Target.label(),
		}
	case tuiOperationFirewall:
		switch operation.Action {
		case "add":
			spec := operation.FirewallSpec
			return []string{
				fmt.Sprintf("ALLOW inbound %s/%d from any source", strings.ToUpper(spec.Protocol), spec.Port),
				"Target: " + operation.Target.label(),
				"The current server policy and protected management paths are rechecked before mutation.",
			}
		case "delete":
			return []string{
				"DELETE firewall rule " + firewallRuleLabel(operation.FirewallRule),
				"Target: " + operation.Target.label(),
				"Remote deletion repeats inventory observation and optimistic revision checks.",
			}
		case "enable", "disable":
			return []string{
				strings.ToUpper(operation.Action) + " the local UFW firewall",
				"Target: " + operation.Target.label(),
			}
		}
	case tuiOperationCron:
		action := strings.ToUpper(operation.Action)
		body := []string{
			action + " cron job " + operation.CronJob.ID,
			"Schedule: " + operation.CronJob.Schedule + " · user: " + operation.CronJob.User,
			"Command: " + truncateTUI(operation.CronJob.Command, 72),
			"Target: " + operation.Target.label(),
		}
		if operation.Action == "run" {
			body = append(body, "The managed agent executes the exact observed command once and returns bounded output.")
		}
		return body
	case tuiOperationDatabase:
		if operation.Action == "drop" {
			return []string{
				"DROP " + operation.Database.EngineName + " database " + operation.Database.Name,
				"This permanently removes the currently observed database and its data.",
				"Target: " + operation.Target.label(),
			}
		}
		return []string{
			"RESTART " + operation.Database.EngineName + " and run its agent-local health check",
			"Active database connections may be interrupted.",
			"Target: " + operation.Target.label(),
		}
	case tuiOperationFile:
		return []string{
			"DELETE " + operation.File.Path,
			"Directories are removed recursively with all nested contents.",
			"The exact observed name and type are rechecked before deletion.",
			"Target: " + operation.Target.label(),
		}
	case tuiOperationBackup:
		switch operation.Action {
		case "create":
			spec := operation.BackupCreate
			body := []string{
				"Create " + spec.label() + " with portable level-6 compression.",
				"Coverage: " + backupCreateCoverage(spec),
				"Target: " + operation.Target.label(),
			}
			if spec.Retention > 0 {
				body = append(body, fmt.Sprintf("Retention: keep latest %d completed full backups; remove older completed full artifacts after success.", spec.Retention))
			} else if spec.Type == "full" {
				body = append(body, "Retention: keep all completed full backups.")
			}
			return body
		case "run":
			return []string{
				"Run managed backup plan " + operation.Backup.Name,
				"Target: " + operation.Target.label(),
			}
		case "delete":
			return []string{
				"DELETE backup artifact " + operation.Backup.Name,
				"This permanently removes the selected local artifact.",
			}
		case "restore":
			return []string{
				"RESTORE data from " + operation.Backup.Name,
				backupValidationSummary(operation.BackupValidation),
				"The server will recheck the artifact before starting the restore job.",
			}
		}
	case tuiOperationSnapshot:
		switch operation.Action {
		case "create":
			return []string{
				"Create a new client-side encrypted snapshot on the selected repository.",
				"Destination: the currently observed healthy snapshot provider.",
				"The server rechecks restic, password, provider health, manifest, and active-job state.",
			}
		case "restore-all":
			return []string{
				"EXTRACT every represented path from snapshot " + operation.EncryptedSnapshot.ID,
				"The restore writes only to HServer's fixed local staging directory.",
				"Production paths are not replaced automatically.",
			}
		case "restore-selected":
			scope := strings.Join(operation.SnapshotManifestIDs, ", ")
			if len(operation.SnapshotVhosts) > 0 {
				scope = strings.Join(operation.SnapshotVhosts, ", ")
			}
			return []string{
				"EXTRACT selected data from snapshot " + operation.EncryptedSnapshot.ID,
				"Scope: " + scope,
				"The restore writes only to HServer's fixed local staging directory.",
				"Production paths are not replaced automatically.",
			}
		case "destination-gdrive", "destination-s3":
			destination := strings.TrimPrefix(operation.Action, "destination-")
			return []string{
				"Select " + snapshotDestinationLabel(destination) + " for future encrypted snapshot operations.",
				"The latest complete server-observed manifest and retention policy are preserved.",
				"Destination health is probed after this change; configuration presence alone is not healthy.",
			}
		}
	case tuiOperationDNS:
		switch operation.Action {
		case "zone-create":
			return []string{
				"CREATE local DNS zone " + operation.DNSCreate.Domain,
				"Initial apex and www IPv4: " + operation.DNSCreate.IP,
				"The server validates the complete BIND configuration before commit.",
			}
		case "record-add":
			record := operation.DNSAdd
			return []string{
				fmt.Sprintf("ADD %s %s %s to zone %s", record.Name, record.Type, record.Value, operation.DNSZone.Domain),
				fmt.Sprintf("TTL %s · priority %d · reload after validation", valueOrNA(record.TTL), record.Priority),
			}
		case "record-update":
			record := operation.DNSUpdate
			return []string{
				fmt.Sprintf("UPDATE %s %s in zone %s", record.Name, record.Type, operation.DNSZone.Domain),
				"Old value: " + record.OldValue,
				"New value: " + record.NewValue,
				"The exact original record is re-observed before replacement and reload.",
			}
		case "soa-update":
			soa := operation.DNSSOAUpdate
			return []string{
				"UPDATE SOA for zone " + operation.DNSZone.Domain,
				fmt.Sprintf("Primary %s · hostmaster %s", soa.PrimaryNs, soa.Hostmaster),
				fmt.Sprintf("Refresh %d · retry %d · expire %d · minimum %d", soa.Refresh, soa.Retry, soa.Expire, soa.Minimum),
				"The current serial and every original SOA field are re-observed before replacement.",
			}
		case "reload":
			return []string{
				"Reload the currently validated BIND configuration and all configured zones.",
				"Target: " + operation.Target.label(),
			}
		case "zone-delete":
			return []string{
				"DELETE local DNS zone " + operation.DNSZone.Domain,
				"The zone file and its named.conf.local declaration will be removed.",
				"Target: " + operation.Target.label(),
			}
		case "record-delete":
			record := operation.DNSRecord
			return []string{
				fmt.Sprintf("DELETE %s %s %s from zone %s", record.Name, record.Type, record.Value, operation.DNSZone.Domain),
				"The exact observed record identity is revalidated and the zone reloads after mutation.",
				"Target: " + operation.Target.label(),
			}
		}
	case tuiOperationUpdate:
		switch operation.Action {
		case "stage":
			return []string{
				"DOWNLOAD and verify panel release " + operation.Update.LatestVersion,
				"Target: " + operation.Target.label(),
				"The release state is fetched again before the download begins.",
			}
		case "install":
			version := "N/A"
			stageID := "N/A"
			if operation.Update.Stage != nil {
				version = operation.Update.Stage.Version
				stageID = operation.Update.Stage.ID
			}
			return []string{
				"INSTALL panel release " + version,
				"Verified stage: " + stageID,
				"The exact stage is fetched again before scheduling the restart.",
				"The release runner restores the previous version automatically if health checks fail.",
			}
		case "upgrade":
			return []string{
				"UPGRADE managed agent to " + operation.Update.LatestVersion,
				"Target: " + operation.Target.label(),
				"The agent release state and active lifecycle operation are fetched again before mutation.",
			}
		case "rollback":
			return []string{
				"ROLLBACK managed agent " + operation.Target.label(),
				"Current version: " + valueOrNA(operation.Update.CurrentVersion),
				"Rollback availability and active lifecycle operation are fetched again before mutation.",
			}
		}
	case tuiOperationDeploy:
		if operation.Target.Local {
			body := []string{
				strings.ToUpper(operation.Action) + " deployment target " + operation.DeployTarget.Name,
				"Project: " + operation.DeployTarget.ProjectDir,
				"Branch: " + valueOrNA(operation.DeployTarget.Branch) + " · environment: " + valueOrNA(string(operation.DeployTarget.Environment)),
				"The exact target and current preflight are re-observed before queuing.",
			}
			if operation.Action == "rollback" {
				body = append(body, "Rollback revision: "+shortDeployRevision(operation.DeployRevision.RollbackCommit))
			}
			return body
		}
		return []string{
			strings.ToUpper(operation.Action) + " managed deployment plan " + operation.RemoteDeployTarget.Name,
			"Target: " + operation.Target.label(),
			"Plan identity: " + operation.RemoteDeployTarget.ID,
			"The exact eligible plan and advertised action are re-observed before queuing.",
		}
	case tuiOperationAlert:
		if operation.AlertResource == tuiAlertResourceChannel {
			return []string{
				strings.ToUpper(operation.Action) + " notification channel " + operation.AlertChannel.Name,
				"Type: " + valueOrNA(operation.AlertChannel.Type),
				"The exact redacted channel observation is fetched again before mutation.",
			}
		}
		return []string{
			strings.ToUpper(operation.Action) + " alert rule " + operation.AlertRule.Name,
			alertRuleSummary(operation.AlertRule),
			"The exact alert rule is fetched again before mutation.",
		}
	case tuiOperationCloudflare:
		if operation.CloudflareResource == tuiCloudflareResourceZone {
			return []string{
				strings.ToUpper(operation.Action) + " Cloudflare zone " + operation.CloudflareZone.Name,
				"Zone ID: " + operation.CloudflareZone.ID,
				"The exact provider zone is fetched again before mutation.",
			}
		}
		return []string{
			strings.ToUpper(operation.Action) + " Cloudflare DNS record " + operation.CloudflareRecord.Name,
			fmt.Sprintf("%s · %s · proxied=%t", operation.CloudflareRecord.Type, operation.CloudflareRecord.Content, operation.CloudflareRecord.Proxied),
			"The exact zone and DNS record are fetched again before mutation.",
		}
	case tuiOperationUser:
		switch operation.Action {
		case "create":
			return []string{
				"CREATE central panel user " + operation.DesiredUser.Name,
				fmt.Sprintf("%s · role: %s", operation.DesiredUser.Email, operation.DesiredUser.Role),
				"The password remains masked and is never included in this receipt.",
			}
		case "profile":
			return []string{
				"UPDATE central panel-user profile for " + operation.User.Name,
				"Email: " + operation.User.Email + " -> " + operation.DesiredUser.Email,
				"Name: " + operation.User.Name + " -> " + operation.DesiredUser.Name,
				"The exact user is fetched again before mutation.",
			}
		case "password":
			return []string{
				"REPLACE password for central panel user " + operation.User.Name,
				"Email: " + operation.User.Email,
				"The password remains masked and the exact user is fetched again before mutation.",
			}
		case "delete":
			return []string{
				"DELETE central panel user " + operation.User.Name,
				"Email: " + operation.User.Email,
				"The current account cannot be deleted and the final administrator is protected by the server.",
				"The exact user is fetched again before mutation.",
			}
		}
		role, _ := strings.CutPrefix(operation.Action, "role-")
		return []string{
			"CHANGE central panel user role for " + operation.User.Name,
			fmt.Sprintf("%s: %s -> %s", operation.User.Email, operation.User.Role, role),
			"The final administrator is protected by the server.",
			"The exact user is fetched again before mutation.",
		}
	default:
		return []string{"Target: " + operation.Target.label()}
	}
	return []string{"Target: " + operation.Target.label()}
}

func wrapIndex(index, count int) int {
	if count <= 0 {
		return 0
	}
	for index < 0 {
		index += count
	}
	return index % count
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func sortTUIProcesses(processes []tuiProcess) {
	sort.SliceStable(processes, func(i, j int) bool {
		if processes[i].CPU == processes[j].CPU {
			return processes[i].Memory > processes[j].Memory
		}
		return processes[i].CPU > processes[j].CPU
	})
}
