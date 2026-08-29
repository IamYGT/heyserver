package remotenodes

// This package contains provider-neutral response contracts shared by the API.
// Remote execution belongs exclusively to the versioned HServer agent protocol.

type ActionStatus struct {
	Running   bool   `json:"running"`
	Action    string `json:"action,omitempty"`
	StartedAt string `json:"started_at,omitempty"`
}

type RebootStatus struct {
	Pending          bool   `json:"pending"`
	ScheduledFor     string `json:"scheduled_for,omitempty"`
	RemainingSeconds int64  `json:"remaining_seconds,omitempty"`
}

type ProcessSignalResult struct {
	Message   string `json:"message"`
	Exited    bool   `json:"exited"`
	Confirmed bool   `json:"confirmed"`
}

type DiskMount struct {
	Filesystem string `json:"filesystem"`
	Size       uint64 `json:"size"`
	Used       uint64 `json:"used"`
	Available  uint64 `json:"available"`
	UsePercent int    `json:"use_percent"`
	Mountpoint string `json:"mountpoint"`
}

type MemoryState struct {
	MemoryTotal       uint64 `json:"memory_total_bytes"`
	MemoryAvailable   uint64 `json:"memory_available_bytes"`
	SwapTotal         uint64 `json:"swap_total_bytes"`
	SwapUsed          uint64 `json:"swap_used_bytes"`
	SwapFree          uint64 `json:"swap_free_bytes"`
	SwapResetEligible bool   `json:"swap_reset_eligible"`
	SwapResetReason   string `json:"swap_reset_reason,omitempty"`
}

type LogEntry struct {
	Timestamp string `json:"timestamp"`
	Unit      string `json:"unit"`
	Priority  int    `json:"priority"`
	Message   string `json:"message"`
}

type Container struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Image  string `json:"image"`
	State  string `json:"state"`
	Status string `json:"status"`
	Ports  string `json:"ports"`
}
