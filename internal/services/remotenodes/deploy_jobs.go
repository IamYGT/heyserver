package remotenodes

type RemoteDeployJob struct {
	ID         string `json:"id"`
	TargetID   string `json:"target_id"`
	Action     string `json:"action"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	CreatedAt  string `json:"created_at"`
	StartedAt  string `json:"started_at,omitempty"`
	FinishedAt string `json:"finished_at,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	Output     string `json:"output,omitempty"`
}
