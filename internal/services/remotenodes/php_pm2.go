package remotenodes

type PHPFPMVersion struct {
	Version string       `json:"version"`
	Unit    string       `json:"unit"`
	Active  string       `json:"active"`
	Enabled string       `json:"enabled"`
	Masked  bool         `json:"masked"`
	Binary  string       `json:"binary,omitempty"`
	Pools   []PHPFPMPool `json:"pools"`
}

type PHPFPMPool struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	User        string `json:"user,omitempty"`
	Group       string `json:"group,omitempty"`
	Listen      string `json:"listen,omitempty"`
	PM          string `json:"pm,omitempty"`
	MaxChildren int    `json:"max_children,omitempty"`
}

type PM2Process struct {
	ID       int     `json:"id"`
	Name     string  `json:"name"`
	Status   string  `json:"status"`
	PID      int     `json:"pid"`
	CPU      float64 `json:"cpu"`
	Memory   int64   `json:"memory"`
	Uptime   int64   `json:"uptime"`
	Restarts int     `json:"restarts"`
	Mode     string  `json:"mode"`
	CWD      string  `json:"cwd"`
	Script   string  `json:"script"`
	Version  string  `json:"version"`
}
