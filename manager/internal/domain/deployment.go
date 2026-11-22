package domain

type DeploymentRoute struct {
	Domain           string `json:"domain"`
	Service          string `json:"service"`
	Port             int    `json:"port"`
	MigrationService string `json:"migrationService"`
	Revision         string `json:"revision"`
}
type NativeDeploymentState struct {
	Catalog  DeploymentCatalog `json:"catalog"`
	Services []string          `json:"services"`
}

// Deployment settings belong to the destination host, independently of any image tag.
type DeploymentSettings struct {
	Name             string   `json:"name"`
	Domain           string   `json:"domain"`
	Service          string   `json:"service"`
	Port             int      `json:"port"`
	MigrationService string   `json:"migrationService"`
	PackageID        string   `json:"packageID"`
	Revision         string   `json:"revision"`
	Services         []string `json:"services"`
	Images           []string `json:"images"`
	EnvKeys          []string `json:"envKeys"`
	Provisioned      bool     `json:"provisioned"`
	Configured       bool     `json:"configured"`
	Deployed         bool     `json:"deployed"`
}
type DeploymentRequest struct {
	Name             string            `json:"name"`
	Key              string            `json:"key"`
	Revision         string            `json:"revision"`
	Domain           string            `json:"domain,omitempty"`
	Service          string            `json:"service,omitempty"`
	Port             int               `json:"port,omitempty"`
	MigrationService string            `json:"migrationService,omitempty"`
	PackageID        string            `json:"packageID,omitempty"`
	Environment      map[string]string `json:"environment,omitempty"`
	RemoveEnv        []string          `json:"removeEnv,omitempty"`
}
type DeploymentJob struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Action    string `json:"action"`
	Status    string `json:"status"`
	Phase     string `json:"phase"`
	Message   string `json:"message,omitempty"`
	CreatedAt int64  `json:"createdAt"`
}
type DeploymentCatalog struct {
	EdgePort       int                  `json:"edgePort"`
	Projects       []DeploymentSettings `json:"projects"`
	Jobs           []DeploymentJob      `json:"jobs"`
	AppsDir        string               `json:"appsDir"`
	DataDir        string               `json:"dataDir"`
	MaxUploadBytes int64                `json:"maxUploadBytes"`
}
type DeploymentPackage struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Services []string `json:"services"`
	Images   []string `json:"images"`
}
