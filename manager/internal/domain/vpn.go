package domain

// VPNStatus deliberately excludes credentials and the original share link.
type VPNStatus struct {
	Configured bool   `json:"configured"`
	Enabled    bool   `json:"enabled"`
	Running    bool   `json:"running"`
	Protocol   string `json:"protocol"`
	Server     string `json:"server"`
	Revision   string `json:"revision"`
}

type VPNChange struct {
	Link     string `json:"link"`
	Enabled  bool   `json:"enabled"`
	Revision string `json:"revision"`
}
