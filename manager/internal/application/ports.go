package application

import (
	"context"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

// The use cases own these small ports; adapters depend on them, never vice versa.
type Runtime interface {
	List(context.Context) ([]domain.Container, error)
	Stop(context.Context, string) error
	Start(context.Context, string) error
}

type Repository interface {
	Policies(context.Context) (map[string]domain.Policy, error)
	Save(context.Context, domain.Policy, domain.Event) error
	Record(context.Context, domain.Event) error
	Audit(context.Context, string, int64, int) ([]domain.Event, error)
}

type Metrics interface {
	Collect(context.Context) ([]domain.Metric, error)
}

// EnvRemote reads and writes the destination's env store through the shipping
// runner. It is a narrow typed port: names, values and revisions only.
type EnvRemote interface {
	EnvProjects(context.Context, domain.ShipTarget) ([]domain.EnvProject, error)
	EnvState(context.Context, domain.ShipTarget, string) (domain.EnvStateRaw, error)
	EnvWrite(context.Context, domain.ShipTarget, string, string, []domain.EnvChange) error
	// BuildArgs returns build-argument values for a build plan (values never logged).
	BuildArgs(context.Context, domain.ShipPlan) (map[string]string, error)
}

// Builder builds images from owner-registered local working trees.
type Builder interface {
	Sources(context.Context) ([]domain.BuildSource, error)
	Inspect(context.Context, string) (domain.BuildInspection, error)
	Build(context.Context, domain.BuildRequest, func(domain.ShipProgress) error) ([]domain.BuiltImage, error)
}
