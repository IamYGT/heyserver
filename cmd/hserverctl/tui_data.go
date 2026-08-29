package main

import (
	"context"
	"encoding/json"
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
	cfservice "github.com/IamYGT/heyserver/internal/services/cloudflare"
	"github.com/IamYGT/heyserver/internal/services/remotenodes"
)

const localTargetID = "local"

type tuiTarget struct {
	ID           string
	Name         string
	Hostname     string
	Local        bool
	Online       bool
	AgentVersion string
	Capabilities map[string]bool
	Inventory    agenthub.Inventory
	LastSeenAt   *time.Time
}

func (target tuiTarget) label() string {
	if target.Name != "" {
		return target.Name
	}
	if target.Hostname != "" {
		return target.Hostname
	}
	return target.ID
}

func (target tuiTarget) capability(name string) bool {
	return target.Local || target.Capabilities[name]
}

type tuiHostSummary struct {
	Hostname        string
	OS              string
	CPUPercent      float64
	CPUKnown        bool
	Cores           int
	MemoryPercent   float64
	MemoryTotal     uint64
	MemoryUsed      uint64
	MemoryAvailable uint64
	SwapKnown       bool
	SwapTotal       uint64
	SwapUsed        uint64
	DiskTotal       uint64
	DiskUsed        uint64
	DiskPercent     float64
	Load1           float64
	Load5           float64
	Load15          float64
	NetworkRXBytes  uint64
	NetworkTXBytes  uint64
	Uptime          int64
}

type tuiService struct {
	Name   string
	State  string
	Detail string
	PID    int
}

type tuiProcess struct {
	PID       int
	StartTime uint64
	User      string
	CPU       float64
	Memory    float64
	RSS       uint64
	Command   string
}

type tuiCleanupTarget struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Size        uint64 `json:"size"`
	Risk        string `json:"risk"`
}

type tuiContainer struct {
	ID          string
	Name        string
	Image       string
	State       string
	Detail      string
	Ports       string
	CPUPercent  float64
	MemoryUsage uint64
	MemoryLimit uint64
}

type tuiPM2Process struct {
	ID            string
	Name          string
	Status        string
	PID           int
	CPUPercent    float64
	MemoryBytes   uint64
	UptimeSeconds int64
	Restarts      int
	Mode          string
}

type tuiLogSource struct {
	ID       string
	Label    string
	Category string
	Detail   string
	Readable bool
}

type tuiContainersMsg struct {
	TargetID   string
	Containers []tuiContainer
	Err        error
}

type tuiLogSourcesMsg struct {
	TargetID string
	Sources  []tuiLogSource
	Err      error
}

type tuiLogLinesMsg struct {
	TargetID string
	Source   tuiLogSource
	Lines    []string
	Err      error
}

type tuiPM2Msg struct {
	TargetID  string
	Processes []tuiPM2Process
	Err       error
}

type tuiPM2LogsMsg struct {
	TargetID string
	Process  tuiPM2Process
	Lines    []string
	Err      error
}

type tuiSnapshot struct {
	Targets            []tuiTarget
	Selected           tuiTarget
	Host               tuiHostSummary
	HostAvailable      bool
	Services           []tuiService
	ServicesAvailable  bool
	Processes          []tuiProcess
	ProcessesAvailable bool
	CleanupTargets     []tuiCleanupTarget
	CleanupLoaded      bool
	Warnings           []string
	FetchedAt          time.Time
}

type tuiLoadMsg struct {
	TargetID string
	Snapshot tuiSnapshot
	Err      error
}

type tuiOperationKind int

const (
	tuiOperationHost tuiOperationKind = iota
	tuiOperationService
	tuiOperationProcess
	tuiOperationDisk
	tuiOperationContainer
	tuiOperationPM2
	tuiOperationWeb
	tuiOperationBackup
	tuiOperationFirewall
	tuiOperationCron
	tuiOperationDatabase
	tuiOperationFile
	tuiOperationPHP
	tuiOperationSecurity
	tuiOperationSnapshot
	tuiOperationDNS
	tuiOperationUpdate
	tuiOperationDeploy
	tuiOperationAlert
	tuiOperationCloudflare
	tuiOperationUser
)

type tuiOperation struct {
	Kind                tuiOperationKind
	Target              tuiTarget
	Action              string
	Service             string
	Process             tuiProcess
	Container           tuiContainer
	PM2Process          tuiPM2Process
	WebResource         tuiWebResource
	Backup              tuiBackupItem
	BackupValidation    tuiBackupValidation
	BackupCreate        tuiBackupCreateSpec
	FirewallRule        tuiFirewallRule
	FirewallState       tuiFirewallState
	FirewallSpec        tuiFirewallSpec
	CronJob             tuiCronJob
	CronState           tuiCronState
	Database            tuiDatabaseItem
	File                cliFileEntry
	PHP                 tuiPHPItem
	Security            tuiSecurityItem
	EncryptedSnapshot   tuiEncryptedSnapshot
	DNSZone             tuiDNSZone
	DNSRecord           tuiDNSRecord
	DNSCreate           tuiDNSCreateRequest
	DNSAdd              tuiDNSAddRequest
	DNSUpdate           tuiDNSUpdateRequest
	DNSOriginalSOA      tuiDNSSOA
	DNSSOAUpdate        tuiDNSSOAUpdateRequest
	Update              tuiUpdateState
	DeployTarget        models.DeployTarget
	DeployPreflight     models.DeployPreflight
	DeployRevision      models.DeployRevisionComparison
	RemoteDeployTarget  remotenodes.RemoteDeployTarget
	AlertResource       string
	AlertChannel        cliNotificationChannel
	AlertRule           models.AlertRule
	CloudflareResource  string
	CloudflareZone      cfservice.CFZone
	CloudflareRecord    cliCloudflareRecord
	User                models.User
	DesiredUser         models.User
	UserPassword        string
	CurrentUserID       int64
	SnapshotManifestIDs []string
	SnapshotVhosts      []string
	CleanupIDs          []string
	Label               string
	Dangerous           bool
}

func loadTUIPM2Cmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		processes, err := loadTUIPM2(ctx, client, target)
		return tuiPM2Msg{TargetID: target.ID, Processes: processes, Err: err}
	}
}

func loadTUIPM2(ctx context.Context, client *apiClient, target tuiTarget) ([]tuiPM2Process, error) {
	if !target.Local {
		if !target.Online {
			return nil, errors.New("managed node is offline")
		}
		if !target.capability(agenthub.CapabilityPM2Read) {
			return nil, errors.New("managed agent does not advertise pm2.read")
		}
		endpoint := "/api/nodes/" + url.PathEscape(target.ID) + "/pm2"
		remote, err := requestJSON[[]struct {
			ID       int     `json:"id"`
			Name     string  `json:"name"`
			Status   string  `json:"status"`
			PID      int     `json:"pid"`
			CPU      float64 `json:"cpu"`
			Memory   int64   `json:"memory"`
			Uptime   int64   `json:"uptime"`
			Restarts int     `json:"restarts"`
			Mode     string  `json:"mode"`
		}](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
		if err != nil {
			return nil, err
		}
		processes := make([]tuiPM2Process, 0, len(remote))
		for _, process := range remote {
			processes = append(processes, tuiPM2Process{
				ID: fmt.Sprint(process.ID), Name: process.Name, Status: process.Status, PID: process.PID,
				CPUPercent: process.CPU, MemoryBytes: uint64(maxInt64(process.Memory, 0)),
				UptimeSeconds: normalizePM2Uptime(process.Uptime, time.Now()), Restarts: process.Restarts, Mode: process.Mode,
			})
		}
		return processes, nil
	}

	local, err := requestJSON[[]struct {
		ID       int     `json:"id"`
		Name     string  `json:"name"`
		Status   string  `json:"status"`
		PID      int     `json:"pid"`
		CPU      float64 `json:"cpu"`
		Memory   float64 `json:"memory"`
		Uptime   int64   `json:"uptime"`
		Restarts int     `json:"restarts"`
		Mode     string  `json:"mode"`
	}](ctx, client, http.MethodGet, "/api/pm2/processes", nil, true)
	if err != nil {
		return nil, err
	}
	processes := make([]tuiPM2Process, 0, len(local))
	for _, process := range local {
		memoryBytes := uint64(0)
		if process.Memory > 0 {
			memoryBytes = uint64(process.Memory * 1024 * 1024)
		}
		processes = append(processes, tuiPM2Process{
			ID: fmt.Sprint(process.ID), Name: process.Name, Status: process.Status, PID: process.PID,
			CPUPercent: process.CPU, MemoryBytes: memoryBytes, UptimeSeconds: process.Uptime,
			Restarts: process.Restarts, Mode: process.Mode,
		})
	}
	return processes, nil
}

func loadTUIPM2LogsCmd(ctx context.Context, client *apiClient, target tuiTarget, process tuiPM2Process) tea.Cmd {
	return func() tea.Msg {
		lines, err := loadTUIPM2Logs(ctx, client, target, process)
		return tuiPM2LogsMsg{TargetID: target.ID, Process: process, Lines: lines, Err: err}
	}
}

func loadTUIPM2Logs(ctx context.Context, client *apiClient, target tuiTarget, process tuiPM2Process) ([]string, error) {
	identity := process.ID
	endpoint := "/api/pm2/processes/" + url.PathEscape(identity) + "/logs?lines=200"
	if !target.Local {
		if !target.Online {
			return nil, errors.New("managed node is offline")
		}
		if !target.capability(agenthub.CapabilityPM2Read) {
			return nil, errors.New("managed agent does not advertise pm2.read")
		}
		identity = process.Name
		endpoint = "/api/nodes/" + url.PathEscape(target.ID) + "/pm2/" + url.PathEscape(identity) + "/logs?lines=200"
		response, err := requestJSON[struct {
			Logs string `json:"logs"`
		}](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
		if err != nil {
			return nil, err
		}
		return splitPM2LogLines(response.Logs), nil
	}
	response, err := requestJSON[struct {
		Output []string `json:"output"`
	}](ctx, client, http.MethodGet, endpoint, nil, true)
	if err != nil {
		return nil, err
	}
	return response.Output, nil
}

func splitPM2LogLines(output string) []string {
	output = strings.TrimRight(output, "\r\n")
	if output == "" {
		return []string{}
	}
	return strings.Split(output, "\n")
}

func normalizePM2Uptime(value int64, now time.Time) int64 {
	if value <= 0 {
		return 0
	}
	if value >= 1_000_000_000_000 {
		seconds := now.Sub(time.UnixMilli(value)).Seconds()
		if seconds <= 0 {
			return 0
		}
		return int64(seconds)
	}
	return value
}

func loadTUIContainersCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		containers, err := loadTUIContainers(ctx, client, target)
		return tuiContainersMsg{TargetID: target.ID, Containers: containers, Err: err}
	}
}

func loadTUIContainers(ctx context.Context, client *apiClient, target tuiTarget) ([]tuiContainer, error) {
	if !target.Local {
		if !target.Online {
			return nil, errors.New("managed node is offline")
		}
		if !target.capability(agenthub.CapabilityContainerRead) {
			return nil, errors.New("managed agent does not advertise container.read")
		}
		endpoint := "/api/nodes/" + url.PathEscape(target.ID) + "/containers"
		remote, err := requestJSON[[]struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Image  string `json:"image"`
			State  string `json:"state"`
			Status string `json:"status"`
			Ports  string `json:"ports"`
		}](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
		if err != nil {
			return nil, err
		}
		containers := make([]tuiContainer, 0, len(remote))
		for _, container := range remote {
			containers = append(containers, tuiContainer{
				ID: container.ID, Name: container.Name, Image: container.Image,
				State: container.State, Detail: container.Status, Ports: container.Ports,
			})
		}
		return containers, nil
	}

	status, err := requestJSON[struct {
		Installed bool   `json:"installed"`
		Running   bool   `json:"running"`
		Error     string `json:"error"`
	}](ctx, client, http.MethodGet, "/api/docker/status", nil, true)
	if err != nil {
		return nil, err
	}
	if !status.Installed || !status.Running {
		message := strings.TrimSpace(status.Error)
		if message == "" && !status.Installed {
			message = "Docker CLI is not installed"
		} else if message == "" {
			message = "Docker daemon is unavailable"
		}
		return nil, errors.New(message)
	}
	local, err := requestJSON[[]struct {
		ID          string   `json:"id"`
		Name        string   `json:"name"`
		Image       string   `json:"image"`
		Status      string   `json:"status"`
		Detail      string   `json:"detail"`
		Ports       []string `json:"ports"`
		CPUPercent  float64  `json:"cpuPercent"`
		MemoryUsage uint64   `json:"memoryUsage"`
		MemoryLimit uint64   `json:"memoryLimit"`
	}](ctx, client, http.MethodGet, "/api/docker/containers", nil, true)
	if err != nil {
		return nil, err
	}
	containers := make([]tuiContainer, 0, len(local))
	for _, container := range local {
		containers = append(containers, tuiContainer{
			ID: container.ID, Name: container.Name, Image: container.Image,
			State: container.Status, Detail: container.Detail, Ports: strings.Join(container.Ports, ", "),
			CPUPercent: container.CPUPercent, MemoryUsage: container.MemoryUsage, MemoryLimit: container.MemoryLimit,
		})
	}
	return containers, nil
}

func loadTUILogSourcesCmd(ctx context.Context, client *apiClient, target tuiTarget) tea.Cmd {
	return func() tea.Msg {
		sources, err := loadTUILogSources(ctx, client, target)
		return tuiLogSourcesMsg{TargetID: target.ID, Sources: sources, Err: err}
	}
}

func loadTUILogSources(ctx context.Context, client *apiClient, target tuiTarget) ([]tuiLogSource, error) {
	if !target.Local {
		if !target.Online {
			return nil, errors.New("managed node is offline")
		}
		if !target.capability(agenthub.CapabilityLogsRead) {
			return nil, errors.New("managed agent does not advertise logs.read")
		}
		sources := make([]tuiLogSource, 0, len(target.Inventory.LogSources))
		for _, source := range target.Inventory.LogSources {
			sources = append(sources, tuiLogSource{ID: source, Label: logSourceLabel(source), Category: "agent", Readable: true})
		}
		return sources, nil
	}
	response, err := requestJSON[struct {
		Sources []struct {
			Path      string `json:"path"`
			Category  string `json:"category"`
			Label     string `json:"label"`
			SizeBytes int64  `json:"sizeBytes"`
			Readable  bool   `json:"readable"`
		} `json:"sources"`
	}](ctx, client, http.MethodGet, "/api/logs/sources", nil, true)
	if err != nil {
		return nil, err
	}
	sources := make([]tuiLogSource, 0, len(response.Sources))
	for _, source := range response.Sources {
		if !source.Readable {
			continue
		}
		sources = append(sources, tuiLogSource{
			ID: source.Path, Label: source.Label, Category: source.Category,
			Detail: formatTUIBytes(uint64(maxInt64(source.SizeBytes, 0))), Readable: true,
		})
	}
	return sources, nil
}

func loadTUILogLinesCmd(ctx context.Context, client *apiClient, target tuiTarget, source tuiLogSource) tea.Cmd {
	return func() tea.Msg {
		lines, err := loadTUILogLines(ctx, client, target, source)
		return tuiLogLinesMsg{TargetID: target.ID, Source: source, Lines: lines, Err: err}
	}
}

func loadTUILogLines(ctx context.Context, client *apiClient, target tuiTarget, source tuiLogSource) ([]string, error) {
	if !target.Local {
		if !target.Online {
			return nil, errors.New("managed node is offline")
		}
		if !target.capability(agenthub.CapabilityLogsRead) {
			return nil, errors.New("managed agent does not advertise logs.read")
		}
		endpoint := "/api/nodes/" + url.PathEscape(target.ID) + "/logs?source=" + url.QueryEscape(source.ID) + "&lines=200"
		entries, err := requestJSON[[]struct {
			Timestamp string `json:"timestamp"`
			Unit      string `json:"unit"`
			Priority  int    `json:"priority"`
			Message   string `json:"message"`
		}](ctx, client.withTimeout(45*time.Second), http.MethodGet, endpoint, nil, true)
		if err != nil {
			return nil, err
		}
		lines := make([]string, 0, len(entries))
		for _, entry := range entries {
			prefix := strings.TrimSpace(entry.Timestamp)
			if entry.Unit != "" {
				prefix += " " + entry.Unit
			}
			lines = append(lines, strings.TrimSpace(prefix+"  "+entry.Message))
		}
		return lines, nil
	}
	endpoint := "/api/logs/read?path=" + url.QueryEscape(source.ID) + "&lines=200"
	response, err := requestJSON[struct {
		Lines []string `json:"lines"`
	}](ctx, client, http.MethodGet, endpoint, nil, true)
	if err != nil {
		return nil, err
	}
	return response.Lines, nil
}

func logSourceLabel(source string) string {
	switch source {
	case "system":
		return "System journal"
	case "php":
		return "PHP-FPM"
	case "pm2":
		return "PM2"
	default:
		if source == "" {
			return "Unknown"
		}
		return strings.ToUpper(source[:1]) + source[1:]
	}
}

type tuiOperationMsg struct {
	Message string
	Err     error
}

type managedNodeEnvelope struct {
	agenthub.Node
	Online bool `json:"online"`
}

func initialTUITargets() []tuiTarget {
	return []tuiTarget{{
		ID: localTargetID, Name: "Panel host", Hostname: "localhost",
		Local: true, Online: true, Capabilities: map[string]bool{},
	}}
}

func loadTUISnapshotCmd(ctx context.Context, client *apiClient, selectedID string, previous []tuiTarget, includeCleanup bool) tea.Cmd {
	return func() tea.Msg {
		snapshot, err := loadTUISnapshot(ctx, client, selectedID, previous, includeCleanup)
		return tuiLoadMsg{TargetID: selectedID, Snapshot: snapshot, Err: err}
	}
}

func loadTUISnapshot(ctx context.Context, client *apiClient, selectedID string, previous []tuiTarget, includeCleanup bool) (tuiSnapshot, error) {
	snapshot := tuiSnapshot{Targets: cloneTargets(previous), FetchedAt: time.Now()}
	if len(snapshot.Targets) == 0 {
		snapshot.Targets = initialTUITargets()
	}

	nodes, nodeErr := requestJSON[[]managedNodeEnvelope](ctx, client, http.MethodGet, "/api/nodes", nil, true)
	if nodeErr == nil {
		snapshot.Targets = targetsFromNodes(nodes)
	} else {
		snapshot.Warnings = append(snapshot.Warnings, "Managed-node list unavailable: "+nodeErr.Error())
	}

	selected, ok := findTUITarget(snapshot.Targets, selectedID)
	if !ok {
		selected = snapshot.Targets[0]
		snapshot.Warnings = append(snapshot.Warnings, "Selected server is no longer available; switched to the panel host")
	}
	snapshot.Selected = selected

	if selected.Local {
		if err := loadLocalTUISnapshot(ctx, client, &snapshot, includeCleanup); err != nil {
			return snapshot, err
		}
	} else {
		loadRemoteTUISnapshot(&snapshot)
		loadRemoteTUIHostMetrics(ctx, client, &snapshot)
		if includeCleanup {
			if !selected.Online {
				snapshot.Warnings = append(snapshot.Warnings, "Disk scan unavailable while the managed node is offline")
			} else if !selected.capability(agenthub.CapabilityDiskCleanup) {
				snapshot.Warnings = append(snapshot.Warnings, "Managed agent does not advertise disk.cleanup")
			} else {
				endpoint := "/api/nodes/" + url.PathEscape(selected.ID) + "/disk/cleanup"
				targets, err := requestJSON[[]tuiCleanupTarget](ctx, client.withTimeout(2*time.Minute), http.MethodGet, endpoint, nil, true)
				if err != nil {
					snapshot.Warnings = append(snapshot.Warnings, "Disk scan failed: "+err.Error())
				} else {
					snapshot.CleanupTargets = targets
					snapshot.CleanupLoaded = true
				}
			}
		}
	}

	return snapshot, nil
}

func loadLocalTUISnapshot(ctx context.Context, client *apiClient, snapshot *tuiSnapshot, includeCleanup bool) error {
	type statsResult struct {
		value models.SystemStats
		err   error
	}
	type servicesResult struct {
		value []models.ServiceStatus
		err   error
	}
	type processesResult struct {
		value []models.ProcessInfo
		err   error
	}
	statsCh := make(chan statsResult, 1)
	servicesCh := make(chan servicesResult, 1)
	processesCh := make(chan processesResult, 1)

	go func() {
		value, err := requestJSON[models.SystemStats](ctx, client, http.MethodGet, "/api/system/stats", nil, true)
		statsCh <- statsResult{value: value, err: err}
	}()
	go func() {
		value, err := requestJSON[[]models.ServiceStatus](ctx, client, http.MethodGet, "/api/system/services", nil, true)
		servicesCh <- servicesResult{value: value, err: err}
	}()
	go func() {
		value, err := requestJSON[[]models.ProcessInfo](ctx, client, http.MethodGet, "/api/monitoring/processes", nil, true)
		processesCh <- processesResult{value: value, err: err}
	}()

	stats := <-statsCh
	services := <-servicesCh
	processes := <-processesCh
	if stats.err != nil {
		snapshot.Warnings = append(snapshot.Warnings, "Panel-host metrics unavailable: "+stats.err.Error())
	} else {
		snapshot.Host = hostSummaryFromLocal(stats.value)
		snapshot.HostAvailable = true
		snapshot.Selected.Hostname = stats.value.Host
		if len(snapshot.Targets) > 0 {
			snapshot.Targets[0].Hostname = stats.value.Host
		}
	}
	if services.err != nil {
		snapshot.Warnings = append(snapshot.Warnings, "Service status unavailable: "+services.err.Error())
	} else {
		snapshot.ServicesAvailable = true
		for _, service := range services.value {
			snapshot.Services = append(snapshot.Services, tuiService{
				Name: service.Name, State: service.Status, Detail: service.Detail, PID: service.PID,
			})
		}
	}
	if processes.err != nil {
		snapshot.Warnings = append(snapshot.Warnings, "Process list unavailable: "+processes.err.Error())
	} else {
		snapshot.ProcessesAvailable = true
		for _, process := range processes.value {
			snapshot.Processes = append(snapshot.Processes, tuiProcess{
				PID: process.PID, StartTime: process.StartTime, User: process.User,
				CPU: process.CPU, Memory: process.Memory, RSS: process.RSS, Command: process.Command,
			})
		}
		sortTUIProcesses(snapshot.Processes)
	}
	if includeCleanup {
		targets, err := requestJSON[[]tuiCleanupTarget](ctx, client.withTimeout(2*time.Minute), http.MethodGet, "/api/disk/cleanup/scan", nil, true)
		if err != nil {
			snapshot.Warnings = append(snapshot.Warnings, "Disk scan failed: "+err.Error())
		} else {
			snapshot.CleanupTargets = targets
			snapshot.CleanupLoaded = true
		}
	}
	return nil
}

func loadRemoteTUISnapshot(snapshot *tuiSnapshot) {
	target := snapshot.Selected
	inventory := target.Inventory
	snapshot.Host = tuiHostSummary{
		Hostname: target.Hostname, OS: inventory.OS,
	}
	// Heartbeat inventory is intentionally limited to identity and the
	// bounded service/process lists below. Live host metrics must come from
	// GET /api/nodes/{id}/metrics; never surface inventory observations as a
	// fallback for that endpoint.
	snapshot.HostAvailable = false
	snapshot.ServicesAvailable = true
	snapshot.ProcessesAvailable = true
	for _, service := range inventory.Services {
		snapshot.Services = append(snapshot.Services, tuiService{Name: service.Name, State: service.Active, Detail: service.Sub})
	}
	for _, process := range inventory.Processes {
		snapshot.Processes = append(snapshot.Processes, tuiProcess{
			PID: process.PID, StartTime: process.StartTime, User: process.User,
			CPU: process.CPU, Memory: process.Memory, RSS: process.RSS, Command: process.Command,
		})
	}
	sortTUIProcesses(snapshot.Processes)
	if !target.Online {
		snapshot.Warnings = append(snapshot.Warnings, "Managed node is offline; observations are from its last heartbeat")
	}
}

func loadRemoteTUIHostMetrics(ctx context.Context, client *apiClient, snapshot *tuiSnapshot) {
	target := snapshot.Selected
	if !target.Online {
		snapshot.Warnings = append(snapshot.Warnings, "Managed node is offline; live metrics unavailable")
		return
	}
	if !target.capability(agenthub.CapabilityMetricsRead) {
		snapshot.Warnings = append(snapshot.Warnings, "Managed node metrics unavailable; managed agent does not advertise metrics.read")
		return
	}

	value, err := requestManagedMonitoringStats(ctx, client.withTimeout(45*time.Second), target.ID)
	if err != nil {
		snapshot.Warnings = append(snapshot.Warnings, managedTUIHostMetricsWarning(err))
		return
	}
	if err := validateTUIManagedMetricsFreshness(value.ObservedAt, time.Now()); err != nil {
		snapshot.Warnings = append(snapshot.Warnings, "Managed node metrics stale; live metrics withheld")
		return
	}

	snapshot.Host = hostSummaryFromManaged(value, target)
	snapshot.HostAvailable = true
}

func hostSummaryFromManaged(value cliManagedMonitoringStatsResponse, target tuiTarget) tuiHostSummary {
	return tuiHostSummary{
		Hostname: target.Hostname, OS: target.Inventory.OS,
		CPUPercent: value.CPU.UsagePercent, CPUKnown: true, Cores: value.CPU.CoreCount,
		MemoryPercent: value.Memory.UsagePercent,
		MemoryTotal:   value.Memory.TotalBytes, MemoryUsed: value.Memory.UsedBytes, MemoryAvailable: value.Memory.AvailableBytes,
		DiskTotal: value.RootDisk.TotalBytes, DiskUsed: value.RootDisk.UsedBytes, DiskPercent: value.RootDisk.UsagePercent,
		Load1: value.Load.One, Load5: value.Load.Five, Load15: value.Load.Fifteen,
		NetworkRXBytes: value.Network.RXBytes, NetworkTXBytes: value.Network.TXBytes,
	}
}

func managedTUIHostMetricsWarning(err error) string {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		reason := strings.ToLower(strings.TrimSpace(apiErr.Message + " " + apiErr.State))
		switch {
		case apiErr.StatusCode == http.StatusConflict && strings.Contains(reason, "managed_node_offline"):
			return "Managed node is offline; live metrics unavailable"
		case apiErr.StatusCode == http.StatusConflict && strings.Contains(reason, "capability_unavailable"):
			return "Managed node metrics unavailable; managed agent does not advertise metrics.read"
		case apiErr.StatusCode >= http.StatusInternalServerError:
			return "Managed node metrics unavailable; server failed to obtain a fresh observation (" + clientErrorMessage(err) + ")"
		}
	}
	return "Managed node metrics unavailable; live observation was withheld (" + clientErrorMessage(err) + ")"
}

func validateTUIManagedMetricsFreshness(observedAt string, now time.Time) error {
	timestamp, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(observedAt))
	if err != nil {
		return fmt.Errorf("managed metrics observed_at is invalid: %w", err)
	}
	now = now.UTC()
	if timestamp.Before(now.Add(-agenthub.MetricsSnapshotMaxAge)) || timestamp.After(now.Add(agenthub.MetricsSnapshotFutureSkew)) {
		return fmt.Errorf("managed metrics observed_at is outside the current observation window")
	}
	return nil
}

func hostSummaryFromLocal(stats models.SystemStats) tuiHostSummary {
	diskTotal, diskUsed, diskPercent := uint64(0), uint64(0), float64(0)
	if len(stats.Disk) > 0 {
		selected := stats.Disk[0]
		for _, disk := range stats.Disk {
			if disk.Mount == "/" {
				selected = disk
				break
			}
		}
		diskTotal, diskUsed, diskPercent = selected.Total, selected.Used, selected.Percentage
	}
	networkRX, networkTX := uint64(0), uint64(0)
	for _, network := range stats.Net {
		networkRX += network.BytesIn
		networkTX += network.BytesOut
	}
	return tuiHostSummary{
		Hostname: stats.Host, OS: stats.OS, CPUPercent: stats.CPU.Usage, CPUKnown: true, Cores: stats.CPU.Cores,
		MemoryPercent: stats.Memory.Percentage,
		MemoryTotal:   stats.Memory.Total, MemoryUsed: stats.Memory.Used, MemoryAvailable: stats.Memory.Available,
		SwapKnown: true,
		SwapTotal: stats.Memory.SwapTotal, SwapUsed: stats.Memory.SwapUsed,
		DiskTotal: diskTotal, DiskUsed: diskUsed, DiskPercent: diskPercent,
		Load1: stats.Load[0], Load5: stats.Load[1], Load15: stats.Load[2],
		NetworkRXBytes: networkRX, NetworkTXBytes: networkTX, Uptime: stats.Uptime,
	}
}

func targetsFromNodes(nodes []managedNodeEnvelope) []tuiTarget {
	targets := initialTUITargets()
	sort.Slice(nodes, func(i, j int) bool {
		return strings.ToLower(nodes[i].Name) < strings.ToLower(nodes[j].Name)
	})
	for _, node := range nodes {
		capabilities := make(map[string]bool, len(node.Capabilities))
		for _, capability := range node.Capabilities {
			capabilities[capability] = true
		}
		targets = append(targets, tuiTarget{
			ID: node.ID, Name: node.Name, Hostname: node.Hostname, Online: node.Online,
			AgentVersion: node.AgentVersion, Capabilities: capabilities, Inventory: node.Inventory,
			LastSeenAt: node.LastSeenAt,
		})
	}
	return targets
}

func cloneTargets(targets []tuiTarget) []tuiTarget {
	cloned := make([]tuiTarget, len(targets))
	copy(cloned, targets)
	return cloned
}

func findTUITarget(targets []tuiTarget, id string) (tuiTarget, bool) {
	for _, target := range targets {
		if target.ID == id {
			return target, true
		}
	}
	return tuiTarget{}, false
}

func requestJSON[T any](ctx context.Context, client *apiClient, method, endpoint string, payload any, authenticated bool) (T, error) {
	var value T
	raw, err := client.request(ctx, method, endpoint, payload, authenticated)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return value, fmt.Errorf("decode %s: %w", endpoint, err)
	}
	return value, nil
}

func runTUIOperationCmd(ctx context.Context, client *apiClient, operation tuiOperation) tea.Cmd {
	return func() tea.Msg {
		message, err := runTUIOperation(ctx, client, operation)
		return tuiOperationMsg{Message: message, Err: err}
	}
}

func runTUIOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	if !operation.Target.Local && !operation.Target.Online {
		return "", errors.New("managed node is offline")
	}
	longClient := client.withTimeout(7 * time.Minute)
	switch operation.Kind {
	case tuiOperationHost:
		endpoint := "/api/system/actions/" + url.PathEscape(operation.Action)
		if !operation.Target.Local {
			if !operation.Target.capability(agenthub.CapabilityHostAction) {
				return "", errors.New("managed agent does not advertise host.action")
			}
			endpoint = "/api/nodes/" + url.PathEscape(operation.Target.ID) + "/actions/" + url.PathEscape(operation.Action)
		}
		return requestMessage(ctx, longClient, endpoint, nil)
	case tuiOperationService:
		if operation.Target.Local {
			return requestMessage(ctx, longClient, "/api/system/actions/service", map[string]string{
				"service": operation.Service, "action": operation.Action,
			})
		}
		if !operation.Target.capability(agenthub.CapabilityServiceAction) {
			return "", errors.New("managed agent does not advertise service.action")
		}
		return runRemoteServiceAction(ctx, longClient, operation)
	case tuiOperationProcess:
		if operation.Process.PID <= 1 || operation.Process.StartTime == 0 {
			return "", errors.New("process does not have a stable identity")
		}
		endpoint := "/api/system/actions/process"
		if !operation.Target.Local {
			if !operation.Target.capability(agenthub.CapabilityProcessSignal) {
				return "", errors.New("managed agent does not advertise process.signal")
			}
			endpoint = "/api/nodes/" + url.PathEscape(operation.Target.ID) + "/processes/signal"
		}
		return requestMessage(ctx, longClient, endpoint, map[string]any{
			"pid": operation.Process.PID, "startTime": operation.Process.StartTime, "signal": operation.Action,
		})
	case tuiOperationDisk:
		if len(operation.CleanupIDs) == 0 {
			return "", errors.New("select at least one cleanup target")
		}
		endpoint := "/api/disk/cleanup/execute"
		payload := map[string]any{"targets": operation.CleanupIDs}
		if !operation.Target.Local {
			if !operation.Target.capability(agenthub.CapabilityDiskCleanup) {
				return "", errors.New("managed agent does not advertise disk.cleanup")
			}
			endpoint = "/api/nodes/" + url.PathEscape(operation.Target.ID) + "/disk/cleanup"
			payload["confirmed"] = true
		}
		return requestCleanup(ctx, longClient, endpoint, payload)
	case tuiOperationContainer:
		endpoint := "/api/docker/containers/" + url.PathEscape(operation.Container.ID) + "/" + url.PathEscape(operation.Action)
		if !operation.Target.Local {
			if !operation.Target.capability(agenthub.CapabilityContainerAction) {
				return "", errors.New("managed agent does not advertise container.action")
			}
			endpoint = "/api/nodes/" + url.PathEscape(operation.Target.ID) + "/containers/" +
				url.PathEscape(operation.Container.ID) + "/actions/" + url.PathEscape(operation.Action)
		}
		response, err := requestJSON[map[string]any](ctx, longClient, http.MethodPost, endpoint, nil, true)
		if err != nil {
			return "", err
		}
		if message, ok := response["message"].(string); ok && strings.TrimSpace(message) != "" {
			return strings.TrimSpace(message), nil
		}
		return fmt.Sprintf("Container %s completed for %s", operation.Action, operation.Container.Name), nil
	case tuiOperationPM2:
		if operation.Target.Local && operation.Action == "save" {
			if _, err := requestJSON[map[string]any](ctx, longClient, http.MethodPost, "/api/pm2/save", nil, true); err != nil {
				return "", err
			}
			return "PM2 process list saved", nil
		}
		identity := operation.PM2Process.ID
		endpoint := "/api/pm2/processes/" + url.PathEscape(identity) + "/" + url.PathEscape(operation.Action)
		if !operation.Target.Local {
			if !operation.Target.capability(agenthub.CapabilityPM2Action) {
				return "", errors.New("managed agent does not advertise pm2.action")
			}
			identity = operation.PM2Process.Name
			endpoint = "/api/nodes/" + url.PathEscape(operation.Target.ID) + "/pm2/" +
				url.PathEscape(identity) + "/actions/" + url.PathEscape(operation.Action)
		}
		response, err := requestJSON[map[string]any](ctx, longClient, http.MethodPost, endpoint, nil, true)
		if err != nil {
			return "", err
		}
		for _, key := range []string{"message", "output", "status"} {
			if message, ok := response[key].(string); ok && strings.TrimSpace(message) != "" {
				return strings.TrimSpace(message), nil
			}
		}
		return fmt.Sprintf("PM2 %s completed for %s", operation.Action, operation.PM2Process.Name), nil
	case tuiOperationWeb:
		return runTUIWebOperation(ctx, client, operation)
	case tuiOperationBackup:
		return runTUIBackupOperation(ctx, client, operation)
	case tuiOperationFirewall:
		return runTUIFirewallOperation(ctx, client, operation)
	case tuiOperationCron:
		return runTUICronOperation(ctx, client, operation)
	case tuiOperationDatabase:
		return runTUIDatabaseOperation(ctx, client, operation)
	case tuiOperationFile:
		return runTUIFileOperation(ctx, client, operation)
	case tuiOperationPHP:
		return runTUIPHPOperation(ctx, client, operation)
	case tuiOperationSecurity:
		return runTUISecurityOperation(ctx, client, operation)
	case tuiOperationSnapshot:
		return runTUISnapshotOperation(ctx, client, operation)
	case tuiOperationDNS:
		return runTUIDNSOperation(ctx, client, operation)
	case tuiOperationUpdate:
		return runTUIUpdateOperation(ctx, client, operation)
	case tuiOperationDeploy:
		return runTUIDeployOperation(ctx, client, operation)
	case tuiOperationAlert:
		return runTUIAlertOperation(ctx, client, operation)
	case tuiOperationCloudflare:
		return runTUICloudflareOperation(ctx, client, operation)
	case tuiOperationUser:
		return runTUIUserOperation(ctx, client, operation)
	default:
		return "", errors.New("unsupported TUI operation")
	}
}

func runTUIWebOperation(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	resource := operation.WebResource
	if resource.Kind == tuiWebDomain && operation.Target.Local {
		if _, err := validateLocalDomainName(resource.ID); err != nil {
			return "", err
		}
	} else if _, err := validateDomainResourceIdentity(resource.ID); err != nil {
		return "", err
	}
	endpoint := ""
	var payload any
	timeout := 7 * time.Minute
	switch resource.Kind {
	case tuiWebNginx:
		if operation.Action != "test" && operation.Action != "reload" {
			return "", fmt.Errorf("unsupported Nginx TUI action %q", operation.Action)
		}
		endpoint = "/api/nginx/" + operation.Action
		if !operation.Target.Local {
			if !operation.Target.capability(agenthub.CapabilityNginxAction) {
				return "", errors.New("managed agent does not advertise nginx.action")
			}
			endpoint = "/api/nodes/" + url.PathEscape(operation.Target.ID) + "/nginx/actions/" + operation.Action
		}
	case tuiWebDomain:
		if operation.Action != "enable" && operation.Action != "disable" {
			return "", fmt.Errorf("unsupported domain TUI action %q", operation.Action)
		}
		endpoint = "/api/domains/" + url.PathEscape(resource.ID) + "/toggle"
		payload = map[string]bool{"active": operation.Action == "enable"}
		if !operation.Target.Local {
			if !operation.Target.capability(agenthub.CapabilityDomainAction) {
				return "", errors.New("managed agent does not advertise domain.action")
			}
			endpoint = "/api/nodes/" + url.PathEscape(operation.Target.ID) + "/domains/" +
				url.PathEscape(resource.ID) + "/actions/" + operation.Action
			payload = nil
		}
	case tuiWebSSL:
		if operation.Action != "renew" && operation.Action != "check" {
			return "", fmt.Errorf("unsupported SSL TUI action %q", operation.Action)
		}
		if operation.Target.Local && operation.Action == "check" {
			return "", errors.New("local certificate checks use the inventory endpoint")
		}
		timeout = 20 * time.Minute
		endpoint = "/api/ssl/renew/" + url.PathEscape(resource.ID)
		if !operation.Target.Local {
			if !operation.Target.capability(agenthub.CapabilitySSLAction) {
				return "", errors.New("managed agent does not advertise ssl.action")
			}
			endpoint = "/api/nodes/" + url.PathEscape(operation.Target.ID) + "/certificates/" +
				url.PathEscape(resource.ID) + "/actions/" + operation.Action
		}
	default:
		return "", errors.New("unsupported web resource type")
	}
	response, err := requestJSON[map[string]any](ctx, client.withTimeout(timeout), http.MethodPost, endpoint, payload, true)
	if err != nil {
		return "", err
	}
	for _, key := range []string{"message", "output", "status"} {
		if message, ok := response[key].(string); ok && strings.TrimSpace(message) != "" {
			return strings.TrimSpace(message), nil
		}
	}
	return fmt.Sprintf("%s %s completed for %s", strings.ToUpper(string(resource.Kind)), operation.Action, resource.Name), nil
}

func requestMessage(ctx context.Context, client *apiClient, endpoint string, payload any) (string, error) {
	response, err := requestJSON[map[string]any](ctx, client, http.MethodPost, endpoint, payload, true)
	if err != nil {
		return "", err
	}
	if message, ok := response["message"].(string); ok && strings.TrimSpace(message) != "" {
		return strings.TrimSpace(message), nil
	}
	return "Operation completed", nil
}

func requestCleanup(ctx context.Context, client *apiClient, endpoint string, payload any) (string, error) {
	var response struct {
		Results []struct {
			ID        string `json:"id"`
			Status    string `json:"status"`
			Message   string `json:"message"`
			Reclaimed uint64 `json:"reclaimed"`
		} `json:"results"`
		ScanError string `json:"scan_error"`
	}
	raw, err := client.request(ctx, http.MethodPost, endpoint, payload, true)
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return "", fmt.Errorf("decode cleanup receipt: %w", err)
	}
	var reclaimed uint64
	failed := 0
	for _, result := range response.Results {
		reclaimed += result.Reclaimed
		if result.Status != "ok" {
			failed++
		}
	}
	message := fmt.Sprintf("Disk cleanup completed; %s reclaimed", formatTUIBytes(reclaimed))
	if failed > 0 {
		message += fmt.Sprintf("; %d target(s) failed", failed)
	}
	if strings.TrimSpace(response.ScanError) != "" {
		message += "; post-cleanup scan unavailable"
	}
	return message, nil
}

func runRemoteServiceAction(ctx context.Context, client *apiClient, operation tuiOperation) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	endpoint := "/api/nodes/" + url.PathEscape(operation.Target.ID) + "/tasks"
	task, err := requestJSON[agenthub.Task](ctx, client, http.MethodPost, endpoint, map[string]any{
		"kind":    agenthub.TaskServiceAction,
		"payload": map[string]string{"service": operation.Service, "action": operation.Action},
	}, true)
	if err != nil {
		return "", err
	}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		if task.Status == agenthub.TaskStatusCompleted {
			if message := strings.TrimSpace(task.Result["message"]); message != "" {
				return message, nil
			}
			return "Service action completed", nil
		}
		if task.Status == agenthub.TaskStatusFailed {
			if task.Error == "" {
				return "", errors.New("managed service action failed")
			}
			return "", errors.New(task.Error)
		}
		select {
		case <-ctx.Done():
			return "", fmt.Errorf("managed service task %d did not complete: %w", task.ID, ctx.Err())
		case <-ticker.C:
		}
		taskEndpoint := endpoint + "/" + fmt.Sprint(task.ID)
		task, err = requestJSON[agenthub.Task](ctx, client, http.MethodGet, taskEndpoint, nil, true)
		if err != nil {
			return "", err
		}
	}
}

func maxInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
