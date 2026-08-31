package models

import "time"

// ---------------------------------------------------------------------------
// Deploy models
// ---------------------------------------------------------------------------

// DeployStatus represents the lifecycle state of a deployment run.
type DeployStatus string

// DeployKind selects the installation-owned deployment executor. Script keeps
// the existing explicit shell workflow; compose uses fixed Docker Compose
// commands assembled by Heyserver rather than accepting an arbitrary command.
type DeployKind string

// DeployWebhookProvider selects the provider-specific signed delivery
// contract used by a deployment target.
type DeployWebhookProvider string

// DeployWebhookStatus reports whether the selected provider integration has a
// usable installation-owned signing secret.
type DeployWebhookStatus string

// DeployTemplateStatus distinguishes an absent local template directory from
// a readable inventory and a partially or wholly invalid inventory.
type DeployTemplateStatus string

// DeployTargetEnvironment distinguishes the canonical production target from
// a staging target derived through the isolated staging lifecycle.
type DeployTargetEnvironment string

const (
	DeployStatusPending DeployStatus = "pending"
	DeployStatusRunning DeployStatus = "running"
	DeployStatusSuccess DeployStatus = "success"
	DeployStatusFailed  DeployStatus = "failed"

	DeployKindScript  DeployKind = "script"
	DeployKindCompose DeployKind = "compose"

	DeployWebhookGitHub DeployWebhookProvider = "github"
	DeployWebhookGitLab DeployWebhookProvider = "gitlab"

	DeployWebhookNotConfigured DeployWebhookStatus = "not_configured"
	DeployWebhookHealthy       DeployWebhookStatus = "healthy"
	DeployWebhookUnavailable   DeployWebhookStatus = "unavailable"

	DeployTemplatesNotConfigured DeployTemplateStatus = "not_configured"
	DeployTemplatesHealthy       DeployTemplateStatus = "healthy"
	DeployTemplatesUnavailable   DeployTemplateStatus = "unavailable"

	DeployEnvironmentProduction DeployTargetEnvironment = "production"
	DeployEnvironmentStaging    DeployTargetEnvironment = "staging"
)

// DeployTemplate is an installation-owned reusable executor preset. It never
// contains a repository credential, webhook secret, or project directory.
type DeployTemplate struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	Description  string     `json:"description"`
	Branch       string     `json:"branch"`
	DeployKind   DeployKind `json:"deploymentKind"`
	ComposeFile  string     `json:"composeFile"`
	DeployScript string     `json:"deployScript"`
}

// DeployTemplateIssue reports one invalid local file by basename only.
type DeployTemplateIssue struct {
	File    string `json:"file"`
	Message string `json:"message"`
}

// DeployTemplateInventory is the observed state of the installation-owned
// deployment template directory.
type DeployTemplateInventory struct {
	Status    DeployTemplateStatus  `json:"status"`
	Directory string                `json:"directory"`
	Templates []DeployTemplate      `json:"templates"`
	Issues    []DeployTemplateIssue `json:"issues"`
}

// DeployTarget is a registered deployment target (domain → git repo mapping).
type DeployTarget struct {
	ID              int64                   `json:"id" db:"id"`
	Name            string                  `json:"name" db:"name"`
	RepoURL         string                  `json:"repoUrl" db:"repo_url"`
	Branch          string                  `json:"branch" db:"branch"`
	ProjectDir      string                  `json:"projectDir" db:"project_dir"`
	Environment     DeployTargetEnvironment `json:"environment" db:"environment"`
	SourceTargetID  *int64                  `json:"sourceTargetId,omitempty" db:"source_target_id"`
	DeployKind      DeployKind              `json:"deploymentKind" db:"deployment_kind"`
	ComposeFile     string                  `json:"composeFile" db:"compose_file"`
	DeployScript    string                  `json:"deployScript" db:"deploy_script"`
	WebhookProvider DeployWebhookProvider   `json:"webhookProvider" db:"webhook_provider"`
	WebhookStatus   DeployWebhookStatus     `json:"webhookStatus"`
	WebhookToken    string                  `json:"webhookToken" db:"webhook_token"`
	AutoDeploy      bool                    `json:"autoDeploy" db:"auto_deploy"`
	IsActive        bool                    `json:"isActive" db:"is_active"`
	CreatedAt       time.Time               `json:"createdAt" db:"created_at"`
	UpdatedAt       time.Time               `json:"updatedAt" db:"updated_at"`
}

// CreateDeployStagingRequest contains only staging-owned identity and storage
// choices. Executor configuration is inherited from the production source;
// secrets, domains, DNS state, and environment values are never copied.
type CreateDeployStagingRequest struct {
	Name       string `json:"name"`
	Branch     string `json:"branch"`
	ProjectDir string `json:"projectDir"`
}

// DeployStagingReceipt makes the isolation boundary explicit to API and CLI
// clients instead of implying that production state was cloned wholesale.
type DeployStagingReceipt struct {
	Target                  DeployTarget `json:"target"`
	StorageBoundary         string       `json:"storageBoundary"`
	EnvironmentValuesCopied bool         `json:"environmentValuesCopied"`
	WebhookSecretCopied     bool         `json:"webhookSecretCopied"`
	DomainsCopied           bool         `json:"domainsCopied"`
	DNSConfigured           bool         `json:"dnsConfigured"`
}

// DeployRun is a single deployment execution record.
type DeployRun struct {
	ID         int64        `json:"id" db:"id"`
	TargetID   int64        `json:"targetId" db:"target_id"`
	Trigger    string       `json:"trigger" db:"trigger"` // "webhook" | "manual" | "rollback"
	Branch     string       `json:"branch" db:"branch"`
	Commit     string       `json:"commit" db:"commit"`          // HEAD SHA after pull
	PrevCommit string       `json:"prevCommit" db:"prev_commit"` // SHA before pull (rollback ref)
	Status     DeployStatus `json:"status" db:"status"`
	Logs       string       `json:"logs,omitempty" db:"logs"`
	StartedAt  time.Time    `json:"startedAt" db:"started_at"`
	FinishedAt *time.Time   `json:"finishedAt,omitempty" db:"finished_at"`
	DurationMs int64        `json:"durationMs,omitempty" db:"duration_ms"`
}

// DeployPreflightCheck is one non-mutating readiness check for a target.
type DeployPreflightCheck struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// DeployPreflight reports whether a configured target can be deployed without
// claiming that a future Git pull or container start will necessarily succeed.
type DeployPreflight struct {
	TargetID       int64                  `json:"targetId"`
	DeploymentKind DeployKind             `json:"deploymentKind"`
	Eligible       bool                   `json:"eligible"`
	Checks         []DeployPreflightCheck `json:"checks"`
}

// DeployRevisionComparison describes the exact local checkout revision and its
// relationship to the latest successful deployment and rollback candidate.
// It is deliberately local-only: reading this report never fetches a remote or
// changes the checkout.
type DeployRevisionComparison struct {
	TargetID              int64     `json:"targetId"`
	State                 string    `json:"state"`
	Branch                string    `json:"branch"`
	CurrentCommit         string    `json:"currentCommit,omitempty"`
	DeployedCommit        string    `json:"deployedCommit,omitempty"`
	RollbackCommit        string    `json:"rollbackCommit,omitempty"`
	TrackedChanges        bool      `json:"trackedChanges"`
	MatchesDeployed       bool      `json:"matchesDeployed"`
	RollbackAvailable     bool      `json:"rollbackAvailable"`
	CommitsAheadRollback  int       `json:"commitsAheadRollback"`
	CommitsBehindRollback int       `json:"commitsBehindRollback"`
	FilesChanged          int       `json:"filesChanged"`
	Insertions            int       `json:"insertions"`
	Deletions             int       `json:"deletions"`
	Message               string    `json:"message"`
	CheckedAt             time.Time `json:"checkedAt"`
}

// ComposeService is one observed container belonging to a Compose deploy
// target. Service is the stable Compose service key; Container identifies a
// replica when a service is scaled beyond one container.
type ComposeService struct {
	Service   string   `json:"service"`
	Container string   `json:"container"`
	Image     string   `json:"image"`
	State     string   `json:"state"`
	Health    string   `json:"health,omitempty"`
	ExitCode  int      `json:"exitCode"`
	Ports     []string `json:"ports"`
}

// ComposeServiceLogs is a bounded log response for one Compose service.
type ComposeServiceLogs struct {
	Logs      string `json:"logs"`
	Tail      int    `json:"tail"`
	Truncated bool   `json:"truncated"`
}

// DeployEnvironment exposes only variable names. Values are write-only at the
// API boundary and never serialized back to clients.
type DeployEnvironment struct {
	Configured bool                        `json:"configured"`
	Variables  []DeployEnvironmentVariable `json:"variables"`
}

type DeployEnvironmentVariable struct {
	Key string `json:"key"`
}

// DeployDomain binds one public hostname to an explicitly published host port
// of a Compose service. Nginx always reaches the project through loopback; the
// browser never supplies an arbitrary upstream URL.
type DeployDomain struct {
	ID       int64 `json:"id" db:"id"`
	TargetID int64 `json:"targetId" db:"target_id"`
	// RuntimeOwner identifies a non-database deployment owner at the host
	// mutation boundary. Local targets keep using TargetID; managed agents use
	// an installation-local, validated owner token without exposing it in APIs.
	RuntimeOwner     string     `json:"-" db:"-"`
	Domain           string     `json:"domain" db:"domain"`
	Service          string     `json:"service" db:"service"`
	HostPort         int        `json:"hostPort" db:"host_port"`
	Upstream         string     `json:"upstream"`
	TLSEnabled       bool       `json:"-" db:"tls_enabled"`
	TLSStatus        string     `json:"tlsStatus"`
	TLSExpiresAt     *time.Time `json:"tlsExpiresAt,omitempty"`
	TLSDaysRemaining int        `json:"tlsDaysRemaining,omitempty"`
	TLSMessage       string     `json:"tlsMessage"`
	CreatedAt        time.Time  `json:"createdAt" db:"created_at"`
	UpdatedAt        time.Time  `json:"updatedAt" db:"updated_at"`
}

type CreateDeployDomainRequest struct {
	Domain   string `json:"domain"`
	Service  string `json:"service"`
	HostPort int    `json:"hostPort"`
}

type EnableDeployDomainTLSRequest struct {
	Email string `json:"email"`
}

type DeployDomainTLSState struct {
	Status        string     `json:"status"`
	ExpiresAt     *time.Time `json:"expiresAt,omitempty"`
	DaysRemaining int        `json:"daysRemaining,omitempty"`
	Message       string     `json:"message"`
}

// DeployDomainHealth reports an active loopback probe. A configured Nginx
// mapping or a running container alone is never presented as application
// readiness.
type DeployDomainHealth struct {
	Domain     string    `json:"domain"`
	Upstream   string    `json:"upstream"`
	Status     string    `json:"status"`
	StatusCode int       `json:"statusCode,omitempty"`
	LatencyMs  int64     `json:"latencyMs"`
	Message    string    `json:"message"`
	CheckedAt  time.Time `json:"checkedAt"`
}

// CreateDeployTargetRequest is the request body for POST /api/deploy/targets.
type CreateDeployTargetRequest struct {
	Name            string                `json:"name"`
	RepoURL         string                `json:"repoUrl"`
	Branch          string                `json:"branch"`
	ProjectDir      string                `json:"projectDir"`
	DeployKind      DeployKind            `json:"deploymentKind"`
	ComposeFile     string                `json:"composeFile"`
	DeployScript    string                `json:"deployScript"`
	WebhookProvider DeployWebhookProvider `json:"webhookProvider"`
	WebhookToken    string                `json:"webhookToken"`
	AutoDeploy      bool                  `json:"autoDeploy"`
}

// UpdateDeployTargetRequest is the request body for PUT /api/deploy/targets/{id}.
type UpdateDeployTargetRequest struct {
	Name              string                `json:"name"`
	RepoURL           string                `json:"repoUrl"`
	Branch            string                `json:"branch"`
	ProjectDir        string                `json:"projectDir"`
	DeployKind        DeployKind            `json:"deploymentKind"`
	ComposeFile       string                `json:"composeFile"`
	DeployScript      string                `json:"deployScript"`
	WebhookProvider   DeployWebhookProvider `json:"webhookProvider"`
	WebhookToken      string                `json:"webhookToken"`
	ClearWebhookToken bool                  `json:"clearWebhookToken"`
	AutoDeploy        bool                  `json:"autoDeploy"`
	IsActive          bool                  `json:"isActive"`
	ExpectedUpdatedAt time.Time             `json:"expectedUpdatedAt"`
}
