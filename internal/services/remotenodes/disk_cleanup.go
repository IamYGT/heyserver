package remotenodes

// DiskCleanupTarget is a measured, fixed-scope cleanup operation available on
// a managed node. Commands never come from the request; only these IDs can run.
type DiskCleanupTarget struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Size        uint64 `json:"size"`
	Risk        string `json:"risk"`
}

type DiskCleanupResult struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Message   string `json:"message"`
	Reclaimed uint64 `json:"reclaimed"`
}

type DiskCleanupExecution struct {
	Results   []DiskCleanupResult `json:"results"`
	ScanError string              `json:"scan_error,omitempty"`
}
