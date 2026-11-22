package domain

import "time"

type ShipTarget struct {
	Revision int64  `json:"revision"`
	Mode     string `json:"mode"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Identity string `json:"identity"`
	InfraDir string `json:"infraDir"`
	AppsDir  string `json:"appsDir"`
	DataDir  string `json:"dataDir"`
}

type LocalImage struct {
	ID        string `json:"id"`
	ContentID string `json:"contentId,omitempty"`
	Ref       string `json:"ref"`
	Size      int64  `json:"size"`
	Platform  string `json:"platform"`
}

type ShipProject struct {
	Name     string   `json:"name"`
	Services []string `json:"services"`
	Error    string   `json:"error,omitempty"`
}
type ShipCatalog struct {
	Projects   []ShipProject `json:"projects"`
	Images     []LocalImage  `json:"images"`
	Identities []string      `json:"identities"`
}

// ShipRequest modes: image (transfer only), deploy (an installed project's
// Compose file and existing local images), build (build a registered local source
// from its working tree, then ship) and apply (recreate services after env edits).
type ShipRequest struct {
	// Native deploy builds here and bootstraps/deploys through the destination
	// manager, including its persisted domain/env and live container limits.
	Deployment *DeploymentRoute `json:"deployment,omitempty"`
	Project    string           `json:"project"`
	Service    string           `json:"service"`
	Image      string           `json:"image"`
	Mode       string           `json:"mode"`
	IncludeEnv bool             `json:"includeEnv"`
	// Source selects a registered local project for build mode. Tag is always
	// computed by the server, never accepted from a browser.
	Source string `json:"source,omitempty"`
	Tag    string `json:"tag,omitempty"`
	// UseComposeLimits applies the Compose CPU/memory values instead of keeping
	// the limits of the running containers (default: keep the server's limits).
	UseComposeLimits bool `json:"useComposeLimits"`
	// Env edits are applied atomically with the deployment. Values are moved into
	// the private plan bundle and are never returned or persisted in the request.
	Env []EnvChange `json:"env,omitempty"`
}
type ShipImage struct {
	LocalImage
	DestinationRef string `json:"destinationRef"`
}
type ShipPlan struct {
	ID          string             `json:"id"`
	CreatedAt   time.Time          `json:"createdAt"`
	Target      ShipTarget         `json:"target"`
	Request     ShipRequest        `json:"request"`
	Project     string             `json:"project"`
	Directory   string             `json:"directory"`
	Services    []string           `json:"services"`
	Images      []ShipImage        `json:"images"`
	Files       []string           `json:"files"`
	Warnings    []string           `json:"warnings"`
	Fingerprint string             `json:"fingerprint"`
	Build       *BuildInfo         `json:"build,omitempty"`
	EnvChanges  []EnvChangeSummary `json:"envChanges"`
	// Limits kept from the destination's running containers (names and values only).
	PreservedLimits []PreservedLimit `json:"preservedLimits"`
	// Services an env apply or deployment is expected to recreate.
	Affected []string `json:"affected"`
	// Only the authenticated internal runner protocol and repository serialize
	// this bundle. Public API responses never expose manifests or env contents.
	Bundle []byte `json:"-"`
}
type ShipJob struct {
	ID        string        `json:"id"`
	PlanID    string        `json:"planId"`
	Actor     string        `json:"actor"`
	Project   string        `json:"project"`
	Status    string        `json:"status"`
	Phase     string        `json:"phase"`
	Message   string        `json:"message"`
	CreatedAt time.Time     `json:"createdAt"`
	UpdatedAt time.Time     `json:"updatedAt"`
	Steps     []ShipStep    `json:"steps,omitempty"`
	Progress  *WorkProgress `json:"progress,omitempty"`
}

// Steps count confirmed pipeline stages, never elapsed time or estimated work.
type ShipStep struct {
	Phase  string `json:"phase"`
	Status string `json:"status"`
}
type WorkProgress struct {
	Current int64  `json:"current"`
	Total   int64  `json:"total,omitempty"`
	Unit    string `json:"unit"`
}
type ShipProgress struct {
	Phase    string        `json:"phase"`
	Message  string        `json:"message"`
	Progress *WorkProgress `json:"progress,omitempty"`
}
type ShipProbe struct {
	Platform string `json:"platform"`
	Docker   string `json:"docker"`
	Compose  string `json:"compose"`
}
