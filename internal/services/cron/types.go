package cron

// ReadinessState distinguishes a usable cron runtime from a missing client,
// stopped daemon, or an indeterminate host state.
type ReadinessState string

const (
	StateHealthy      ReadinessState = "healthy"
	StateNotInstalled ReadinessState = "not-installed"
	StateStopped      ReadinessState = "stopped"
	StateUnavailable  ReadinessState = "unavailable"
)

// Status describes local cron management and execution readiness.
type Status struct {
	Available   bool           `json:"available"`
	Installed   bool           `json:"installed"`
	Running     bool           `json:"running"`
	State       ReadinessState `json:"state"`
	DaemonState string         `json:"daemonState"`
	Error       string         `json:"error,omitempty"`
}

// Job represents a single crontab entry for a user.
type Job struct {
	ID            string `json:"id"`
	User          string `json:"user"`
	Schedule      string `json:"schedule"`
	Command       string `json:"command"`
	Description   string `json:"description"`
	IsActive      bool   `json:"isActive"`
	HumanSchedule string `json:"humanSchedule"`
}

// SystemFile represents a file inside /etc/cron.d/, /etc/cron.daily/, etc.
type SystemFile struct {
	Path    string   `json:"path"`
	Name    string   `json:"name"`
	Dir     string   `json:"dir"`
	Entries []string `json:"entries"`
	Size    int64    `json:"size"`
}

// CreateRequest is the input for AddJob.
type CreateRequest struct {
	User        string `json:"user"`
	Schedule    string `json:"schedule"`
	Command     string `json:"command"`
	Description string `json:"description"`
}

// UpdateRequest is the input for EditJob.
type UpdateRequest struct {
	Schedule    string `json:"schedule"`
	Command     string `json:"command"`
	Description string `json:"description"`
	IsActive    bool   `json:"isActive"`
}
