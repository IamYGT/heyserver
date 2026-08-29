package remotenodes

type AgentUpdateStatus struct {
	ReleaseStatus     string `json:"release_status"`
	SignatureStatus   string `json:"signature_status"`
	CurrentVersion    string `json:"current_version"`
	LatestVersion     string `json:"latest_version,omitempty"`
	LatestState       string `json:"latest_version_state,omitempty"`
	UpdateAvailable   bool   `json:"update_available"`
	Platform          string `json:"platform"`
	ReleaseNotesURL   string `json:"release_notes_url,omitempty"`
	ReleaseMessage    string `json:"release_message"`
	ReleaseCheckedAt  string `json:"release_checked_at"`
	Operation         string `json:"operation"`
	OperationStatus   string `json:"operation_status"`
	OperationVersion  string `json:"operation_version,omitempty"`
	OperationDetail   string `json:"operation_detail"`
	OperationUpdated  string `json:"operation_updated_at,omitempty"`
	RollbackAvailable bool   `json:"rollback_available"`
}

type UpgradeAgentRequest struct {
	Version   string `json:"version"`
	Confirmed bool   `json:"confirmed"`
}

type RollbackAgentRequest struct {
	Confirmed bool `json:"confirmed"`
}
