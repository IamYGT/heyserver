package remotenodes

type RemoteDeployContainer struct {
	Name   string `json:"name"`
	Image  string `json:"image"`
	State  string `json:"state"`
	Health string `json:"health,omitempty"`
}

type RemoteDeployTarget struct {
	ID                string                  `json:"id"`
	Name              string                  `json:"name"`
	Description       string                  `json:"description,omitempty"`
	Kind              string                  `json:"kind"`
	Path              string                  `json:"path"`
	Status            string                  `json:"status"`
	Eligible          bool                    `json:"eligible"`
	Reason            string                  `json:"reason,omitempty"`
	Actions           []string                `json:"actions,omitempty"`
	Branch            string                  `json:"branch,omitempty"`
	Head              string                  `json:"head,omitempty"`
	Upstream          string                  `json:"upstream,omitempty"`
	Remote            string                  `json:"remote,omitempty"`
	Dirty             int                     `json:"dirty,omitempty"`
	Ahead             int                     `json:"ahead,omitempty"`
	Behind            int                     `json:"behind,omitempty"`
	Frameworks        []string                `json:"frameworks,omitempty"`
	Containers        []RemoteDeployContainer `json:"containers,omitempty"`
	RollbackAvailable bool                    `json:"rollback_available,omitempty"`
	RollbackCreatedAt string                  `json:"rollback_created_at,omitempty"`
	HostPort          int                     `json:"host_port,omitempty"`
}

type RemoteDeployDomain struct {
	TargetID         string `json:"target_id"`
	Domain           string `json:"domain"`
	HostPort         int    `json:"host_port"`
	DesiredHostPort  int    `json:"desired_host_port"`
	Upstream         string `json:"upstream"`
	Status           string `json:"status"`
	Message          string `json:"message"`
	TLSStatus        string `json:"tls_status"`
	TLSExpiresAt     string `json:"tls_expires_at,omitempty"`
	TLSDaysRemaining int    `json:"tls_days_remaining,omitempty"`
	TLSMessage       string `json:"tls_message"`
	UpdatedAt        string `json:"updated_at,omitempty"`
	Enabled          bool   `json:"enabled"`
	Revision         string `json:"revision"`
}

type RemoteDeployDomainHealth struct {
	Domain     string `json:"domain"`
	Upstream   string `json:"upstream"`
	Status     string `json:"status"`
	StatusCode int    `json:"status_code,omitempty"`
	LatencyMS  int64  `json:"latency_ms"`
	Message    string `json:"message"`
	CheckedAt  string `json:"checked_at"`
}

type CreateRemoteDeployDomainRequest struct {
	Domain string `json:"domain"`
}

type EnsureRemoteDeployDomainRequest struct {
	ExpectedRevision string `json:"expected_revision"`
	Confirmed        bool   `json:"confirmed"`
}

type EnsureRemoteDeployDomainResponse struct {
	Changed     bool               `json:"changed"`
	Observation RemoteDeployDomain `json:"observation"`
}

type EnableRemoteDeployDomainTLSRequest struct {
	Email string `json:"email"`
}
