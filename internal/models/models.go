package models

import "time"

type Role string

const (
	RoleAdmin   Role = "admin"
	RoleManager Role = "manager"
	RoleViewer  Role = "viewer"
)

type User struct {
	ID          int64     `json:"id" db:"id"`
	Email       string    `json:"email" db:"email"`
	Name        string    `json:"name" db:"name"`
	Password    string    `json:"-" db:"password"`
	Role        Role      `json:"role" db:"role"`
	TOTPSecret  string    `json:"-" db:"totp_secret"`
	TOTPEnabled bool      `json:"totp_enabled" db:"totp_enabled"`
	CreatedAt   time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt   time.Time `json:"updatedAt" db:"updated_at"`
}

type Domain struct {
	ID         string    `json:"id" db:"id"`
	Name       string    `json:"name" db:"name"`
	Type       string    `json:"type" db:"type"`
	Root       string    `json:"root" db:"root"`
	PHPVersion string    `json:"phpVersion,omitempty" db:"php_version"`
	ProxyPort  int       `json:"proxyPort,omitempty" db:"proxy_port"`
	SSLEnabled bool      `json:"sslEnabled" db:"ssl_enabled"`
	IsActive   bool      `json:"isActive" db:"is_active"`
	CreatedAt  time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt  time.Time `json:"updatedAt" db:"updated_at"`
}

type NginxConfig struct {
	Filename  string `json:"filename"`
	Domain    string `json:"domain"`
	Type      string `json:"type"`
	IsEnabled bool   `json:"isEnabled"`
	Content   string `json:"content,omitempty"`
}

type PHPPool struct {
	Name        string `json:"name"`
	PHPVersion  string `json:"phpVersion"`
	User        string `json:"user"`
	Group       string `json:"group"`
	SocketPath  string `json:"socketPath"`
	MaxChildren int    `json:"maxChildren"`
	IsActive    bool   `json:"isActive"`
}

type SSLCertificate struct {
	Domain        string    `json:"domain"`
	Issuer        string    `json:"issuer"`
	Subject       string    `json:"subject"`
	Serial        string    `json:"serial"`
	NotBefore     time.Time `json:"notBefore"`
	NotAfter      time.Time `json:"notAfter"`
	ExpiresAt     time.Time `json:"expiresAt"`
	IsWildcard    bool      `json:"isWildcard"`
	AutoRenew     bool      `json:"autoRenew"`
	DaysRemaining int       `json:"daysRemaining"`
	CertPath      string    `json:"certPath"`
	KeyPath       string    `json:"keyPath"`
	SANs          []string  `json:"sans"`
}

type PM2Process struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	CPU      float64 `json:"cpu"`
	Memory   float64 `json:"memory"`
	Uptime   int64   `json:"uptime"`
	Restarts int     `json:"restarts"`
	Mode     string  `json:"mode"`
	PID      int     `json:"pid"`
}

type FirewallRule struct {
	Number    int    `json:"number"`
	To        string `json:"to"`
	Action    string `json:"action"`
	From      string `json:"from"`
	Protocol  string `json:"protocol"`
	Comment   string `json:"comment,omitempty"`
	Direction string `json:"direction"`
}

type DatabaseInfo struct {
	Name   string `json:"name"`
	Engine string `json:"engine"`
	Owner  string `json:"owner"`
	Size   string `json:"size"`
	Tables int    `json:"tables"`
}

type BackupInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Path      string    `json:"path"`
	Size      int64     `json:"size"`
	DiskSize  int64     `json:"diskSize,omitempty"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"createdAt"`
}

type SystemStats struct {
	CPU    CPUStats    `json:"cpu"`
	Memory MemoryStats `json:"memory"`
	Disk   []DiskStats `json:"disk"`
	Load   [3]float64  `json:"load"`
	Uptime int64       `json:"uptime"`
	Host   string      `json:"hostname"`
	OS     string      `json:"os"`
	Net    []NetStats  `json:"network"`
}

type CPUStats struct {
	Usage float64 `json:"usage"`
	Cores int     `json:"cores"`
	Model string  `json:"model"`
}

type MemoryStats struct {
	Total          uint64  `json:"total"`
	Used           uint64  `json:"used"`
	Free           uint64  `json:"free"`
	Percentage     float64 `json:"percentage"`
	Buffers        uint64  `json:"buffers"`
	Cached         uint64  `json:"cached"`
	Available      uint64  `json:"available"`
	SwapTotal      uint64  `json:"swapTotal"`
	SwapUsed       uint64  `json:"swapUsed"`
	SwapFree       uint64  `json:"swapFree"`
	SwapPercentage float64 `json:"swapPercentage"`
}

type DiskStats struct {
	Mount      string  `json:"mount"`
	Total      uint64  `json:"total"`
	Used       uint64  `json:"used"`
	Free       uint64  `json:"free"`
	Percentage float64 `json:"percentage"`
}

type NetStats struct {
	Interface string `json:"interface"`
	BytesIn   uint64 `json:"bytesIn"`
	BytesOut  uint64 `json:"bytesOut"`
}

type ServiceStatus struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	PID    int    `json:"pid,omitempty"`
	Uptime string `json:"uptime,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type AuditLog struct {
	ID        int64     `json:"id" db:"id"`
	UserID    int64     `json:"userId" db:"user_id"`
	UserName  string    `json:"userName" db:"user_name"`
	Action    string    `json:"action" db:"action"`
	Resource  string    `json:"resource" db:"resource"`
	Details   string    `json:"details" db:"details"`
	IP        string    `json:"ip" db:"ip_address"`
	CreatedAt time.Time `json:"createdAt" db:"created_at"`
}

type FileEntry struct {
	Name        string    `json:"name"`
	Path        string    `json:"path"`
	Type        string    `json:"type"`
	Size        int64     `json:"size"`
	Permissions string    `json:"permissions"`
	Owner       string    `json:"owner"`
	Group       string    `json:"group"`
	Modified    time.Time `json:"modified"`
}

type ProcessInfo struct {
	PID       int     `json:"pid"`
	StartTime uint64  `json:"startTime"`
	User      string  `json:"user"`
	CPU       float64 `json:"cpu"`
	Memory    float64 `json:"memory"`
	VSZ       uint64  `json:"vsz"`
	RSS       uint64  `json:"rss"`
	Stat      string  `json:"stat"`
	Command   string  `json:"command"`
}

// ── Notification ──────────────────────────────────────────────────────────────

const (
	ChannelEmail    = "email"
	ChannelTelegram = "telegram"
	ChannelDiscord  = "discord"
	ChannelSlack    = "slack"
)

const (
	AlertCPUUsage     = "cpu_usage"
	AlertMemoryUsage  = "memory_usage"
	AlertDiskUsage    = "disk_usage"
	AlertSSLExpiry    = "ssl_expiry"
	AlertServiceDown  = "service_down"
	AlertFailedLogins = "failed_logins"
)

type NotificationChannel struct {
	ID             int64     `json:"id"        db:"id"`
	Name           string    `json:"name"      db:"name"`
	Type           string    `json:"type"      db:"type"`
	Config         string    `json:"config"    db:"config"`
	Enabled        bool      `json:"enabled"   db:"enabled"`
	ConfigRevision int64     `json:"-"         db:"config_revision"`
	CreatedAt      time.Time `json:"createdAt" db:"created_at"`
	UpdatedAt      time.Time `json:"updatedAt" db:"updated_at"`
}

// NotificationDeliveryOutcome records whether a provider delivery attempt
// succeeded. The value is deliberately bounded because it is persisted in the
// notification delivery receipt table and used by readiness checks.
type NotificationDeliveryOutcome string

// NotificationDeliverySource identifies the bounded operation that produced a
// delivery receipt. It never carries provider or request details.
type NotificationDeliverySource string

const (
	NotificationDeliveryOutcomeSuccess NotificationDeliveryOutcome = "success"
	NotificationDeliveryOutcomeFailure NotificationDeliveryOutcome = "failure"

	NotificationDeliverySourceManualTest NotificationDeliverySource = "manual_test"
	NotificationDeliverySourceAlert      NotificationDeliverySource = "alert"
	NotificationDeliverySourceUptime     NotificationDeliverySource = "uptime"
	NotificationDeliverySourceBackup     NotificationDeliverySource = "backup"
)

// NotificationDeliveryReceipt is the bounded latest delivery observation for
// one notification channel. ConfigRevision fences a receipt to the channel
// configuration that was active when the attempt ran; no provider payload or
// error data is retained here.
type NotificationDeliveryReceipt struct {
	ChannelID             int64                       `json:"channelId"             db:"channel_id"`
	ChannelConfigRevision int64                       `json:"channelConfigRevision" db:"channel_config_revision"`
	Outcome               NotificationDeliveryOutcome `json:"outcome"               db:"outcome"`
	Source                NotificationDeliverySource  `json:"source"                db:"source"`
	ObservedAt            time.Time                   `json:"observedAt"            db:"observed_at"`
}

type AlertRule struct {
	ID           int64     `json:"id"           db:"id"`
	Name         string    `json:"name"         db:"name"`
	Type         string    `json:"type"         db:"type"`
	Threshold    float64   `json:"threshold"    db:"threshold"`
	DurationMins int       `json:"durationMins" db:"duration_mins"`
	Target       string    `json:"target"       db:"target"`
	Enabled      bool      `json:"enabled"      db:"enabled"`
	CooldownMins int       `json:"cooldownMins" db:"cooldown_mins"`
	CreatedAt    time.Time `json:"createdAt"    db:"created_at"`
	UpdatedAt    time.Time `json:"updatedAt"    db:"updated_at"`
}

type AlertHistory struct {
	ID       int64     `json:"id"       db:"id"`
	RuleID   int64     `json:"ruleId"   db:"rule_id"`
	RuleName string    `json:"ruleName" db:"rule_name"`
	Type     string    `json:"type"     db:"type"`
	Message  string    `json:"message"  db:"message"`
	Value    float64   `json:"value"    db:"value"`
	FiredAt  time.Time `json:"firedAt"  db:"fired_at"`
}

type EmailConfig struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
	To       string `json:"to"`
	TLS      bool   `json:"tls"`
}

type TelegramConfig struct {
	BotToken string `json:"botToken"`
	ChatID   int64  `json:"chatId"`
}

type DiscordConfig struct {
	WebhookURL string `json:"webhookUrl"`
	Username   string `json:"username,omitempty"`
}

type SlackConfig struct {
	WebhookURL string `json:"webhookUrl"`
	Channel    string `json:"channel,omitempty"`
	Username   string `json:"username,omitempty"`
}

// ── Settings ──────────────────────────────────────────────────────────────────

type Setting struct {
	Key       string    `json:"key"       db:"key"`
	Value     string    `json:"value"     db:"value"`
	UpdatedAt time.Time `json:"updatedAt" db:"updated_at"`
}

type NetInterface struct {
	Name  string   `json:"name"`
	Addrs []string `json:"addrs"`
	IsUp  bool     `json:"isUp"`
}

type SystemInfo struct {
	OS           string         `json:"os"`
	Kernel       string         `json:"kernel"`
	BootID       string         `json:"boot_id"`
	Hostname     string         `json:"hostname"`
	Arch         string         `json:"arch"`
	Nginx        string         `json:"nginx"`
	PHP          []string       `json:"php"`
	PostgreSQL   string         `json:"postgresql"`
	Interfaces   []NetInterface `json:"interfaces"`
	GoVersion    string         `json:"go_version"`
	NodeVersion  string         `json:"node_version"`
	BuildCommit  string         `json:"build_commit"`
	BuildDate    string         `json:"build_date"`
	PanelVersion string         `json:"panel_version"`
	ProjectURL   string         `json:"project_url,omitempty"`
}

type HealthStatus struct {
	Status      string `json:"status"`
	Version     string `json:"version"`
	Uptime      int64  `json:"uptime"`
	BuildCommit string `json:"build_commit,omitempty"`
}
