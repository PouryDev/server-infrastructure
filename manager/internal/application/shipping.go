package application

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type Delivery interface {
	Catalog(context.Context) (domain.ShipCatalog, error)
	Prepare(context.Context, domain.ShipTarget, domain.ShipRequest) (domain.ShipPlan, error)
	Execute(context.Context, domain.ShipPlan, func(domain.ShipProgress) error) error
	Probe(context.Context, domain.ShipTarget) (domain.ShipProbe, error)
}
type NativeDeployment interface {
	NativeState(context.Context, domain.ShipTarget, string) (domain.NativeDeploymentState, error)
}

func (s *Shipping) NativeState(ctx context.Context, source string) (domain.NativeDeploymentState, error) {
	native, ok := s.delivery.(NativeDeployment)
	if !ok || !sourceName.MatchString(source) {
		return domain.NativeDeploymentState{}, fmt.Errorf("automatic project deployment is unavailable")
	}
	target, err := s.repo.Target(ctx)
	if err != nil {
		return domain.NativeDeploymentState{}, err
	}
	return native.NativeState(ctx, target, source)
}

type ShippingRepository interface {
	Target(context.Context) (domain.ShipTarget, error)
	SaveTarget(context.Context, domain.ShipTarget, domain.Event) error
	SavePlan(context.Context, domain.ShipPlan, domain.Event) error
	Plan(context.Context, string) (domain.ShipPlan, error)
	QueueShip(context.Context, domain.ShipJob, domain.Event) error
	SaveShip(context.Context, domain.ShipJob, domain.Event) error
	ShipJobs(context.Context) ([]domain.ShipJob, error)
}

type Shipping struct {
	delivery Delivery
	repo     ShippingRepository
	audit    Repository
	builder  Builder
	env      EnvRemote
	mu       sync.Mutex
	wake     chan struct{}
}

// WithBuilder enables build mode (local working-tree builds) and env sources.
func (s *Shipping) WithBuilder(b Builder) *Shipping { s.builder = b; return s }

// WithEnv lets build jobs read build-argument values from the destination env store.
func (s *Shipping) WithEnv(e EnvRemote) *Shipping { s.env = e; return s }

// BuildSources lists the owner-registered local projects.
func (s *Shipping) BuildSources(ctx context.Context) ([]domain.BuildSource, error) {
	if s.builder == nil {
		return []domain.BuildSource{}, nil
	}
	return s.builder.Sources(ctx)
}

func NewShipping(delivery Delivery, repo ShippingRepository, audit Repository) *Shipping {
	return &Shipping{delivery: delivery, repo: repo, audit: audit, wake: make(chan struct{}, 1)}
}

var shipName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,80}$`)
var shipHost = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.:-]{0,252}$`)
var sourceName = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
var shipPath = regexp.MustCompile(`^/[a-zA-Z0-9_./-]+$`)

func ValidateTarget(t domain.ShipTarget) error {
	if t.Mode != "local" && t.Mode != "ssh" {
		return fmt.Errorf("choose local or ssh target")
	}
	if t.Port < 1 || t.Port > 65535 {
		return fmt.Errorf("invalid SSH port")
	}
	if t.Mode == "ssh" && (!shipHost.MatchString(t.Host) || !shipName.MatchString(t.User) || !shipName.MatchString(t.Identity)) {
		return fmt.Errorf("invalid SSH host, user or identity")
	}
	for _, p := range []string{t.InfraDir, t.AppsDir, t.DataDir} {
		if !shipPath.MatchString(p) || path.Clean(p) != p || p == "/" || len(p) < 5 || strings.Contains(p, "/../") {
			return fmt.Errorf("use distinct absolute destination paths")
		}
	}
	for i, a := range []string{t.InfraDir, t.AppsDir, t.DataDir} {
		for j, b := range []string{t.InfraDir, t.AppsDir, t.DataDir} {
			if i != j && (a == b || strings.HasPrefix(a, b+"/")) {
				return fmt.Errorf("destination directories must not overlap")
			}
		}
	}
	// Same-host deployment is deliberately confined to the runner's sandbox.
	if t.Mode == "local" {
		for _, p := range []string{t.InfraDir, t.AppsDir, t.DataDir} {
			if !strings.HasPrefix(p, "/tmp/infra-manager-target/") {
				return fmt.Errorf("local targets must stay below /tmp/infra-manager-target/")
			}
		}
	}
	return nil
}
func shippingEvent(actor, project, action, outcome, detail string) domain.Event {
	return domain.Event{At: time.Now().UTC(), Actor: actor, Project: project, Action: action, Outcome: outcome, Detail: detail}
}
func (s *Shipping) Target(ctx context.Context) (domain.ShipTarget, error) { return s.repo.Target(ctx) }
func (s *Shipping) Catalog(ctx context.Context) (domain.ShipCatalog, error) {
	return s.delivery.Catalog(ctx)
}
func (s *Shipping) SaveTarget(ctx context.Context, actor string, t domain.ShipTarget) error {
	if err := ValidateTarget(t); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, err := s.repo.Target(ctx)
	if err != nil {
		return err
	}
	if old.Revision != t.Revision {
		return ErrConflict
	}
	t.Revision++
	return s.repo.SaveTarget(ctx, t, shippingEvent(actor, "", "ship_target", "success", fmt.Sprintf("mode=%s destination=%s@%s revision=%d", t.Mode, t.User, t.Host, t.Revision)))
}
func (s *Shipping) Probe(ctx context.Context, actor string) (domain.ShipProbe, error) {
	t, err := s.repo.Target(ctx)
	if err != nil {
		return domain.ShipProbe{}, err
	}
	if err = ValidateTarget(t); err != nil {
		return domain.ShipProbe{}, err
	}
	if err = s.audit.Record(ctx, shippingEvent(actor, "", "ship_probe", "intent", t.Mode)); err != nil {
		return domain.ShipProbe{}, err
	}
	result, err := s.delivery.Probe(ctx, t)
	recordErr := s.audit.Record(ctx, shippingEvent(actor, "", "ship_probe", outcome(err), t.Mode))
	return result, errors.Join(err, recordErr)
}
func (s *Shipping) Prepare(ctx context.Context, actor string, req domain.ShipRequest) (domain.ShipPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, err := s.repo.Target(ctx)
	if err != nil {
		return domain.ShipPlan{}, err
	}
	if err = ValidateTarget(t); err != nil {
		return domain.ShipPlan{}, err
	}
	// The tag is always computed here, never taken from a browser.
	req.Tag = ""
	if req.Deployment != nil && req.Mode != "build" {
		return domain.ShipPlan{}, fmt.Errorf("automatic project deployment requires build mode")
	}
	var inspection domain.BuildInspection
	switch req.Mode {
	case "image":
		if len(req.Env) > 0 || req.Source != "" {
			return domain.ShipPlan{}, fmt.Errorf("invalid shipment mode")
		}
	case "deploy", "apply":
		if !shipName.MatchString(req.Project) || (req.Service != "" && !shipName.MatchString(req.Service)) || req.Source != "" {
			return domain.ShipPlan{}, fmt.Errorf("invalid project or service")
		}
		if req.Mode == "apply" && (!domain.ValidProject(req.Project) || len(req.Env) > 0 || req.Image != "" || req.IncludeEnv) {
			return domain.ShipPlan{}, fmt.Errorf("invalid project or service")
		}
		if req.Mode == "deploy" && len(req.Env) > 0 {
			return domain.ShipPlan{}, fmt.Errorf("environment edits belong to Build & Ship")
		}
	case "build":
		if s.builder == nil {
			return domain.ShipPlan{}, fmt.Errorf("no local project sources are configured")
		}
		if !sourceName.MatchString(req.Source) || (req.Service != "" && !shipName.MatchString(req.Service)) || req.Image != "" {
			return domain.ShipPlan{}, fmt.Errorf("invalid project source")
		}
		if err = domain.ValidateEnvChanges(req.Env); err != nil {
			return domain.ShipPlan{}, err
		}
		if inspection, err = s.builder.Inspect(ctx, req.Source); err != nil {
			return domain.ShipPlan{}, err
		}
		req.Project = inspection.Source.App
		req.Tag = inspection.Tag
		if req.Deployment != nil {
			// Distinct previews can use the same commit but different build args
			// or moving base images. Never replace a tag needed for rollback.
			req.Tag += "-r" + strings.ToLower(rand.Text()[:8])
		}
		req.IncludeEnv = false
		if !domain.ValidProject(req.Project) {
			return domain.ShipPlan{}, fmt.Errorf("this project name cannot be deployed from the panel")
		}
	default:
		return domain.ShipPlan{}, fmt.Errorf("invalid shipment mode")
	}
	if req.Mode != "image" {
		policies, e := s.audit.Policies(ctx)
		if e != nil {
			return domain.ShipPlan{}, e
		}
		if policies[req.Project].MustStop(time.Now()) {
			return domain.ShipPlan{}, fmt.Errorf("renew the suspended project before deployment")
		}
	}
	plan, err := s.delivery.Prepare(ctx, t, req)
	if err != nil {
		return domain.ShipPlan{}, err
	}
	plan.ID = rand.Text()
	plan.CreatedAt = time.Now().UTC()
	plan.Target = t
	plan.Request = req
	plan.Request.Env = nil
	if plan.Build != nil {
		plan.Build.Head, plan.Build.Dirty = inspection.Head, inspection.Dirty
	}
	detail := fmt.Sprintf("plan=%s mode=%s services=%s", plan.ID, req.Mode, strings.Join(plan.Services, ","))
	if plan.Build != nil {
		detail += fmt.Sprintf(" source=%s tag=%s dirty=%t", plan.Build.Source, plan.Build.Tag, plan.Build.Dirty)
	}
	if len(req.Env) > 0 {
		detail += " env: " + domain.AuditEnvDetail(req.Env)
	}
	if req.UseComposeLimits {
		detail += " limits=compose"
	}
	err = s.repo.SavePlan(ctx, plan, shippingEvent(actor, req.Project, "ship_plan", "success", detail))
	return plan, err
}
func (s *Shipping) Commit(ctx context.Context, actor, id string) (domain.ShipJob, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.repo.Plan(ctx, id)
	if err != nil {
		return domain.ShipJob{}, err
	}
	t, err := s.repo.Target(ctx)
	if err != nil {
		return domain.ShipJob{}, err
	}
	if p.Target.Revision != t.Revision || time.Since(p.CreatedAt) > 15*time.Minute {
		return domain.ShipJob{}, fmt.Errorf("deployment preview expired; prepare it again")
	}
	policies, err := s.audit.Policies(ctx)
	if err != nil {
		return domain.ShipJob{}, err
	}
	if p.Request.Mode != "image" && policies[p.Request.Project].MustStop(time.Now()) {
		return domain.ShipJob{}, fmt.Errorf("renew the suspended project before deployment")
	}
	now := time.Now().UTC()
	job := domain.ShipJob{ID: rand.Text(), PlanID: id, Actor: actor, Project: p.Request.Project, Status: "queued", Phase: "queued", Message: "Shipment queued", CreatedAt: now, UpdatedAt: now}
	if err = s.repo.QueueShip(ctx, job, shippingEvent(actor, job.Project, "ship", "accepted", "job="+job.ID)); err != nil {
		return domain.ShipJob{}, err
	}
	select {
	case s.wake <- struct{}{}:
	default:
	}
	return job, nil
}
func (s *Shipping) Jobs(ctx context.Context) ([]domain.ShipJob, error) { return s.repo.ShipJobs(ctx) }
func (s *Shipping) Run(ctx context.Context) {
	// A lost transport may leave a remote command running. Never silently replay
	// an in-flight shipment after restart; its outcome requires inspection.
	jobs, err := s.repo.ShipJobs(ctx)
	if err != nil {
		return
	}
	for _, job := range jobs {
		if job.Status == "running" {
			job.Status = "interrupted"
			job.Phase = "interrupted"
			job.Message = "Manager restarted; inspect destination before retrying"
			job.UpdatedAt = time.Now().UTC()
			if s.repo.SaveShip(ctx, job, shippingEvent("shipper", job.Project, "ship", "interrupted", "job="+job.ID)) != nil {
				return
			}
		}
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		s.next(ctx)
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
	}
}
func (s *Shipping) next(ctx context.Context) {
	jobs, err := s.repo.ShipJobs(ctx)
	if err != nil {
		return
	}
	for _, job := range jobs {
		if job.Status != "queued" {
			continue
		}
		plan, err := s.repo.Plan(ctx, job.PlanID)
		if err != nil {
			return
		}
		job.Status = "running"
		job.Phase = "preflight"
		job.Steps = shippingSteps(plan)
		job.UpdatedAt = time.Now().UTC()
		if s.repo.SaveShip(ctx, job, shippingEvent("shipper", job.Project, "ship", "intent", "job="+job.ID)) != nil {
			return
		}
		policies, policyErr := s.audit.Policies(ctx)
		if policyErr != nil || (plan.Request.Mode != "image" && policies[plan.Request.Project].MustStop(time.Now())) {
			job.Status = "failed"
			job.Phase = "failed"
			job.Message = "Deployment blocked by current project policy or unavailable policy storage"
			job.UpdatedAt = time.Now().UTC()
			_ = s.repo.SaveShip(ctx, job, shippingEvent("shipper", job.Project, "ship", "failure", job.Message))
			return
		}
		runCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		report := func(p domain.ShipProgress) error {
			updateShippingProgress(&job, p)
			job.Phase = p.Phase
			job.Message = p.Message
			job.UpdatedAt = time.Now().UTC()
			return s.repo.SaveShip(runCtx, job, shippingEvent("shipper", job.Project, "ship_progress", p.Phase, p.Message))
		}
		err = s.build(runCtx, plan, report)
		if err == nil {
			err = s.delivery.Execute(runCtx, plan, report)
		}
		cancel()
		job.Status = "succeeded"
		job.Phase = "complete"
		job.Message = "Shipment completed"
		if err != nil {
			job.Status = "failed"
			job.Phase = "failed"
			job.Message = err.Error()
			if strings.Contains(err.Error(), "rolled back") {
				job.Status = "rolled_back"
			}
			if ctx.Err() != nil {
				job.Status = "interrupted"
				job.Message = "Shipment interrupted; inspect destination before retrying"
			}
		}
		job.UpdatedAt = time.Now().UTC()
		saveCtx, cancelSave := context.WithTimeout(context.Background(), 5*time.Second)
		finishShippingProgress(&job, err == nil)
		_ = s.repo.SaveShip(saveCtx, job, shippingEvent("shipper", job.Project, "ship", job.Status, "job="+job.ID+" "+job.Message))
		cancelSave()
		return
	}
}

// build runs the local image build of a build-mode plan before it is shipped
// through the same pipeline as any other deployment. Build-argument values come
// from the destination env store (plus pending edits) and go straight to the
// builder; they are never persisted or reported.
func (s *Shipping) build(ctx context.Context, plan domain.ShipPlan, report func(domain.ShipProgress) error) error {
	if plan.Request.Mode != "build" {
		return nil
	}
	if s.builder == nil || s.env == nil || plan.Build == nil {
		return fmt.Errorf("local builds are not configured")
	}
	if err := report(domain.ShipProgress{Phase: "build", Message: "Preparing local build"}); err != nil {
		return err
	}
	args, err := s.env.BuildArgs(ctx, plan)
	if err != nil {
		return err
	}
	services := make([]string, 0, len(plan.Build.Images))
	for _, image := range plan.Build.Images {
		services = append(services, image.Service)
	}
	_, err = s.builder.Build(ctx, domain.BuildRequest{Source: plan.Build.Source, Tag: plan.Build.Tag, Head: plan.Build.Head, Dirty: plan.Build.Dirty, Services: services, Args: args}, report)
	return err
}
