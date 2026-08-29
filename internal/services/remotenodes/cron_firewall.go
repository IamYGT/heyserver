package remotenodes

type RemoteCronJob struct {
	ID          string `json:"id"`
	Schedule    string `json:"schedule"`
	User        string `json:"user"`
	Command     string `json:"command"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
}

type RemoteCronSource struct {
	Path       string `json:"path"`
	EntryCount int    `json:"entry_count"`
	Managed    bool   `json:"managed"`
}

type RemoteCronInventory struct {
	Service  string             `json:"service"`
	Jobs     []RemoteCronJob    `json:"jobs"`
	Sources  []RemoteCronSource `json:"sources"`
	Revision string             `json:"revision"`
}

type RemoteFirewallRule struct {
	ID       string `json:"id"`
	Action   string `json:"action"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port,omitempty"`
	Source   string `json:"source,omitempty"`
	Comment  string `json:"comment,omitempty"`
	Managed  bool   `json:"managed"`
	Raw      string `json:"raw,omitempty"`
}

type RemoteFirewallInventory struct {
	Backend          string               `json:"backend"`
	Policy           string               `json:"policy"`
	Persistence      string               `json:"persistence"`
	Rules            []RemoteFirewallRule `json:"rules"`
	Revision         string               `json:"revision"`
	ProtectedSources []string             `json:"protected_sources"`
	ProtectedPorts   []int                `json:"protected_ports"`
}
