package agenthub

import "time"

const (
	ProtocolVersion = "v1"

	TaskServiceStatus         = "service.status"
	TaskServiceAction         = "service.action"
	TaskHostAction            = "host.action"
	TaskProcessSignal         = "process.signal"
	TaskDiskCleanupScan       = "disk.cleanup.scan"
	TaskDiskCleanupExecute    = "disk.cleanup.execute"
	TaskLogsRead              = "logs.read"
	TaskContainerList         = "container.list"
	TaskContainerAction       = "container.action"
	TaskNginxAction           = "nginx.action"
	TaskNginxConfigList       = "nginx.config.list"
	TaskNginxConfigRead       = "nginx.config.read"
	TaskNginxConfigWrite      = "nginx.config.write"
	TaskPHPInventory          = "php.inventory"
	TaskPHPConfigRead         = "php.config.read"
	TaskPHPConfigWrite        = "php.config.write"
	TaskPHPAction             = "php.action"
	TaskPM2List               = "pm2.list"
	TaskPM2Logs               = "pm2.logs"
	TaskPM2Action             = "pm2.action"
	TaskCronInventory         = "cron.inventory"
	TaskCronCreate            = "cron.create"
	TaskCronUpdate            = "cron.update"
	TaskCronDelete            = "cron.delete"
	TaskCronRun               = "cron.run"
	TaskFirewallInventory     = "firewall.inventory"
	TaskFirewallAdd           = "firewall.add"
	TaskFirewallDelete        = "firewall.delete"
	TaskDomainInventory       = "domain.inventory"
	TaskDomainAction          = "domain.action"
	TaskSSLInventory          = "ssl.inventory"
	TaskSSLAction             = "ssl.action"
	TaskDatabaseInventory     = "database.inventory"
	TaskDatabaseAction        = "database.action"
	TaskBackupInventory       = "backup.inventory"
	TaskBackupRun             = "backup.run"
	TaskFilesBrowse           = "files.browse"
	TaskFilesRead             = "files.read"
	TaskFilesWrite            = "files.write"
	TaskDeployInventory       = "deploy.inventory"
	TaskDeployAction          = "deploy.action"
	TaskDeployDomainInventory = "deploy.domain.inventory"
	TaskDeployDomainHealth    = "deploy.domain.health"
	TaskDeployDomainAction    = "deploy.domain.action"
	TaskAgentUpdateStatus     = "agent.update.status"
	TaskAgentUpdateAction     = "agent.update.action"
	TaskIntegrationStatus     = "integration.status"
	TaskMetricsRead           = "metrics.read"
	TaskProfileApply          = "agent.profile.apply"

	CapabilityInventory          = "inventory"
	CapabilityServiceStatus      = "service.status"
	CapabilityServiceAction      = "service.action"
	CapabilityHostAction         = "host.action"
	CapabilityProcessRead        = "process.read"
	CapabilityProcessSignal      = "process.signal"
	CapabilityTerminal           = "terminal"
	CapabilityDiskCleanup        = "disk.cleanup"
	CapabilityLogsRead           = "logs.read"
	CapabilityContainerRead      = "container.read"
	CapabilityContainerAction    = "container.action"
	CapabilityNginxAction        = "nginx.action"
	CapabilityNginxConfigRead    = "nginx.config.read"
	CapabilityNginxConfigWrite   = "nginx.config.write"
	CapabilityPHPRead            = "php.read"
	CapabilityPHPWrite           = "php.write"
	CapabilityPHPAction          = "php.action"
	CapabilityPM2Read            = "pm2.read"
	CapabilityPM2Action          = "pm2.action"
	CapabilityCronRead           = "cron.read"
	CapabilityCronWrite          = "cron.write"
	CapabilityCronRun            = "cron.run"
	CapabilityFirewallRead       = "firewall.read"
	CapabilityFirewallWrite      = "firewall.write"
	CapabilityDomainRead         = "domain.read"
	CapabilityDomainAction       = "domain.action"
	CapabilitySSLRead            = "ssl.read"
	CapabilitySSLAction          = "ssl.action"
	CapabilityDatabaseRead       = "database.read"
	CapabilityDatabaseAction     = "database.action"
	CapabilityBackupRead         = "backup.read"
	CapabilityBackupRun          = "backup.run"
	CapabilityFilesRead          = "files.read"
	CapabilityFilesWrite         = "files.write"
	CapabilityDeployRead         = "deploy.read"
	CapabilityDeployAction       = "deploy.action"
	CapabilityDeployDomainRead   = "deploy.domain.read"
	CapabilityDeployDomainAction = "deploy.domain.action"
	CapabilityAgentUpdateRead    = "agent.update.read"
	CapabilityAgentUpdateAction  = "agent.update.action"
	CapabilityIntegrationStatus  = "integration.status"
	CapabilityMetricsRead        = "metrics.read"
	CapabilityProfileApply       = "agent.profile.apply"
)

// MetricsSnapshot is the strict provider-neutral live host observation
// returned by the fixed metrics.read task. It intentionally contains no
// caller-selected paths, interfaces, commands, or provider data.
type MetricsSnapshot struct {
	ObservedAt time.Time         `json:"observed_at"`
	CPU        MetricsCPU        `json:"cpu"`
	Load       MetricsLoad       `json:"load"`
	Memory     MetricsMemory     `json:"memory"`
	Network    MetricsNetwork    `json:"network"`
	RootDisk   MetricsFilesystem `json:"root_disk"`
}

type MetricsCPU struct {
	UsagePercent float64 `json:"usage_percent"`
	CoreCount    int     `json:"core_count"`
}

type MetricsLoad struct {
	One     float64 `json:"one"`
	Five    float64 `json:"five"`
	Fifteen float64 `json:"fifteen"`
}

type MetricsMemory struct {
	TotalBytes     uint64  `json:"total_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsagePercent   float64 `json:"usage_percent"`
}

type MetricsNetwork struct {
	RXBytes uint64 `json:"rx_bytes"`
	TXBytes uint64 `json:"tx_bytes"`
}

type MetricsFilesystem struct {
	TotalBytes     uint64  `json:"total_bytes"`
	UsedBytes      uint64  `json:"used_bytes"`
	AvailableBytes uint64  `json:"available_bytes"`
	UsagePercent   float64 `json:"usage_percent"`
}

// ServiceState is an agent-observed systemd unit state.
type ServiceState struct {
	Name   string `json:"name"`
	Active string `json:"active"`
	Sub    string `json:"sub,omitempty"`
}

// DiskMount is a bounded df-compatible filesystem snapshot observed locally by
// the agent. It deliberately excludes tmpfs, container overlay, and squashfs.
type DiskMount struct {
	Filesystem string `json:"filesystem"`
	Size       uint64 `json:"size"`
	Used       uint64 `json:"used"`
	Available  uint64 `json:"available"`
	UsePercent int    `json:"use_percent"`
	Mountpoint string `json:"mountpoint"`
}

// Process is a bounded stable-identity process snapshot reported by an agent.
type Process struct {
	PID       int     `json:"pid"`
	StartTime uint64  `json:"startTime"`
	User      string  `json:"user"`
	CPU       float64 `json:"cpu"`
	Memory    float64 `json:"memory"`
	RSS       uint64  `json:"rss"`
	Command   string  `json:"command"`
}

// Inventory is the bounded host state accepted by the hub. It includes a
// capped process command snapshot but excludes arbitrary files, environment
// values, and secrets.
type Inventory struct {
	OS                string         `json:"os"`
	Arch              string         `json:"arch,omitempty"`
	Kernel            string         `json:"kernel"`
	BootID            string         `json:"boot_id"`
	UptimeSeconds     int64          `json:"uptime_seconds"`
	Load1             float64        `json:"load_1"`
	MemoryTotal       uint64         `json:"memory_total_bytes"`
	MemoryAvailable   uint64         `json:"memory_available_bytes"`
	SwapTotal         uint64         `json:"swap_total_bytes"`
	SwapUsed          uint64         `json:"swap_used_bytes"`
	SwapFree          uint64         `json:"swap_free_bytes"`
	SwapResetEligible bool           `json:"swap_reset_eligible"`
	SwapResetReason   string         `json:"swap_reset_reason,omitempty"`
	DiskTotal         uint64         `json:"disk_total_bytes"`
	DiskUsed          uint64         `json:"disk_used_bytes"`
	DiskAvailable     uint64         `json:"disk_available_bytes"`
	DiskUsePercent    float64        `json:"disk_use_percent"`
	DiskMounts        []DiskMount    `json:"disk_mounts,omitempty"`
	PleskPresent      bool           `json:"plesk_present"`
	Services          []ServiceState `json:"services"`
	Processes         []Process      `json:"processes"`
	LogSources        []string       `json:"log_sources,omitempty"`
	FileReadRoots     []string       `json:"file_read_roots,omitempty"`
	FileWriteRoots    []string       `json:"file_write_roots,omitempty"`
}

type HeartbeatRequest struct {
	ProtocolVersion string    `json:"protocol_version"`
	NodeID          string    `json:"node_id"`
	AgentVersion    string    `json:"agent_version"`
	Capabilities    []string  `json:"capabilities"`
	Hostname        string    `json:"hostname"`
	SentAt          time.Time `json:"sent_at"`
	Inventory       Inventory `json:"inventory"`
	// Profile is an optional observation from agents that advertise the
	// agent.profile.apply capability. It is additive so older agents can keep
	// sending the existing heartbeat shape.
	Profile *AgentProfileObservation `json:"profile,omitempty"`
}

type HeartbeatResponse struct {
	Accepted bool      `json:"accepted"`
	ServerAt time.Time `json:"server_at"`
}

type RegisterNodeRequest struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// RegisterNodeResponse returns Token only once. The database stores its hash.
type RegisterNodeResponse struct {
	Node  Node   `json:"node"`
	Token string `json:"token"`
}

type Node struct {
	ID              string     `json:"id"`
	Name            string     `json:"name"`
	Hostname        string     `json:"hostname"`
	AgentVersion    string     `json:"agent_version"`
	ProtocolVersion string     `json:"protocol_version"`
	Capabilities    []string   `json:"capabilities"`
	Inventory       Inventory  `json:"inventory"`
	LastSeenAt      *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type TaskRequest struct {
	Kind    string            `json:"kind"`
	Payload map[string]string `json:"payload"`
}

type Task struct {
	ID          int64             `json:"id"`
	NodeID      string            `json:"node_id"`
	Kind        string            `json:"kind"`
	Payload     map[string]string `json:"payload"`
	Status      string            `json:"status"`
	Result      map[string]string `json:"result,omitempty"`
	Error       string            `json:"error,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	StartedAt   *time.Time        `json:"started_at,omitempty"`
	CompletedAt *time.Time        `json:"completed_at,omitempty"`
}

type TaskPollResponse struct {
	Task *Task `json:"task,omitempty"`
}

type TaskResultRequest struct {
	Status string            `json:"status"`
	Result map[string]string `json:"result,omitempty"`
	Error  string            `json:"error,omitempty"`
}
