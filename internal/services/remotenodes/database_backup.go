package remotenodes

type RemoteDatabase struct {
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	Connections int    `json:"connections"`
	Objects     int    `json:"objects"`
}

type RemoteDatabaseSession struct {
	ID       string `json:"id"`
	User     string `json:"user"`
	Database string `json:"database,omitempty"`
	State    string `json:"state"`
	Age      int    `json:"age_seconds"`
	Query    string `json:"query,omitempty"`
}

type RemoteDatabaseEngine struct {
	ID        string                  `json:"id"`
	Name      string                  `json:"name"`
	Version   string                  `json:"version"`
	Unit      string                  `json:"unit"`
	Active    string                  `json:"active"`
	DataSize  int64                   `json:"data_size"`
	Databases []RemoteDatabase        `json:"databases"`
	Sessions  []RemoteDatabaseSession `json:"sessions"`
}

type RemoteBackupFile struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
}

type RemoteBackupPlan struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	Service     string             `json:"service"`
	Timer       string             `json:"timer"`
	Active      string             `json:"active"`
	Enabled     string             `json:"enabled"`
	LastResult  string             `json:"last_result"`
	LastRun     string             `json:"last_run"`
	NextRun     string             `json:"next_run"`
	CompletedAt string             `json:"completed_at,omitempty"`
	Verified    bool               `json:"verified"`
	TotalSize   int64              `json:"total_size"`
	Files       []RemoteBackupFile `json:"files"`
}
