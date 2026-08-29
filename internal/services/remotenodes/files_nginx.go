package remotenodes

// FileEntry is the provider-neutral response shape returned by the remote
// agent's bounded file browser.
type FileEntry struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Type       string `json:"type"`
	Size       int64  `json:"size"`
	Mode       string `json:"mode"`
	ModifiedAt string `json:"modified_at"`
}

// FileContent is shared by the remote file, Nginx, and PHP configuration
// handlers when decoding an agent task result.
type FileContent struct {
	Path       string `json:"path"`
	Content    string `json:"content"`
	Checksum   string `json:"checksum"`
	Size       int64  `json:"size"`
	Mode       string `json:"mode"`
	ModifiedAt string `json:"modified_at"`
}

type NginxConfig struct {
	Name       string `json:"name"`
	Enabled    bool   `json:"enabled"`
	Size       int64  `json:"size"`
	ModifiedAt string `json:"modified_at"`
	Content    string `json:"content,omitempty"`
	Checksum   string `json:"checksum,omitempty"`
}
