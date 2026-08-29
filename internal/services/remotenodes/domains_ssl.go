package remotenodes

type Domain struct {
	Name            string   `json:"name"`
	Aliases         []string `json:"aliases"`
	Config          string   `json:"config"`
	Enabled         bool     `json:"enabled"`
	SSL             bool     `json:"ssl"`
	CertificateName string   `json:"certificate_name,omitempty"`
	Root            string   `json:"root,omitempty"`
	ProxyTarget     string   `json:"proxy_target,omitempty"`
	Kind            string   `json:"kind"`
}

type Certificate struct {
	Name          string   `json:"name"`
	Domains       []string `json:"domains"`
	Issuer        string   `json:"issuer"`
	Serial        string   `json:"serial"`
	NotBefore     string   `json:"not_before"`
	NotAfter      string   `json:"not_after"`
	DaysRemaining int      `json:"days_remaining"`
	AutoRenew     bool     `json:"auto_renew"`
}
