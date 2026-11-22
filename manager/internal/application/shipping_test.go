package application_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/sqlite"
	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type fakeDelivery struct{ runs int }

func (*fakeDelivery) Catalog(context.Context) (domain.ShipCatalog, error) {
	return domain.ShipCatalog{}, nil
}
func (*fakeDelivery) Probe(context.Context, domain.ShipTarget) (domain.ShipProbe, error) {
	return domain.ShipProbe{Platform: "linux/amd64"}, nil
}
func (*fakeDelivery) Prepare(_ context.Context, t domain.ShipTarget, r domain.ShipRequest) (domain.ShipPlan, error) {
	return domain.ShipPlan{Target: t, Request: r, Project: r.Project, Fingerprint: "fingerprint", Bundle: []byte("private-env-value"), Services: []string{"api"}}, nil
}
func (d *fakeDelivery) Execute(_ context.Context, _ domain.ShipPlan, progress func(domain.ShipProgress) error) error {
	d.runs++
	return progress(domain.ShipProgress{Phase: "verified", Message: "Verified"})
}

func TestShippingTargetValidationAndPreviewInvalidation(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	s := application.NewShipping(&fakeDelivery{}, repo, repo)
	target, err := s.Target(ctx)
	if err != nil {
		t.Fatal(err)
	}
	unsafe := target
	unsafe.DataDir = "/srv/data"
	if application.ValidateTarget(unsafe) == nil {
		t.Fatal("same-host data escape accepted")
	}
	unsafe = target
	unsafe.AppsDir = target.InfraDir
	if application.ValidateTarget(unsafe) == nil {
		t.Fatal("overlapping destination paths accepted")
	}
	p, err := s.Prepare(ctx, "admin", domain.ShipRequest{Project: "shop", Mode: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(p)
	if strings.Contains(string(data), "private-env-value") {
		t.Fatal("public preview contains private bundle")
	}
	if err = s.SaveTarget(ctx, "admin", target); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Commit(ctx, "admin", p.ID); err == nil {
		t.Fatal("stale target preview committed")
	}
	if !errors.Is(s.SaveTarget(ctx, "admin", target), application.ErrConflict) {
		t.Fatal("stale target settings overwrote a new revision")
	}
}
func TestShippingJobIsDurableSingleUseAndAudited(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "manager.db")
	repo, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	delivery := &fakeDelivery{}
	s := application.NewShipping(delivery, repo, repo)
	p, err := s.Prepare(ctx, "admin", domain.ShipRequest{Project: "shop", Mode: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.Commit(ctx, "admin", p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Commit(ctx, "admin", p.ID); err == nil {
		t.Fatal("preview replay accepted")
	}
	repo.Close()
	repo, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	s = application.NewShipping(delivery, repo, repo)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); s.Run(runCtx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		jobs, err := s.Jobs(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(jobs) == 1 && jobs[0].Status == "succeeded" {
			if jobs[0].ID != job.ID {
				t.Fatal("wrong job")
			}
			events, err := repo.Audit(ctx, "shop", 0, 200)
			if err != nil || len(events) < 5 {
				t.Fatal("shipping audit missing")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("queued shipment was not resumed")
}
func TestSuspendedProjectCannotBeDeployed(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	if err = repo.Save(ctx, domain.Policy{Project: "shop", Suspended: true}, domain.Event{At: time.Now(), Action: "suspend"}); err != nil {
		t.Fatal(err)
	}
	s := application.NewShipping(&fakeDelivery{}, repo, repo)
	if _, err = s.Prepare(ctx, "admin", domain.ShipRequest{Project: "shop", Mode: "deploy"}); err == nil {
		t.Fatal("suspended project could be reactivated by deployment")
	}
}

type fakeBuilder struct {
	built []domain.BuildRequest
	order *[]string
}

func (*fakeBuilder) Sources(context.Context) ([]domain.BuildSource, error) {
	return []domain.BuildSource{{ID: "shop"}}, nil
}
func (*fakeBuilder) Inspect(_ context.Context, id string) (domain.BuildInspection, error) {
	return domain.BuildInspection{Source: domain.BuildSource{ID: id, App: "shop"}, Head: "abc1234567", Dirty: true, Tag: "abc1234567-dirty1"}, nil
}
func (b *fakeBuilder) Build(_ context.Context, r domain.BuildRequest, p func(domain.ShipProgress) error) ([]domain.BuiltImage, error) {
	b.built = append(b.built, r)
	*b.order = append(*b.order, "build")
	return nil, p(domain.ShipProgress{Phase: "build", Message: "Building image web (1/1)"})
}

type fakeEnvRemote struct{}

func (fakeEnvRemote) EnvProjects(context.Context, domain.ShipTarget) ([]domain.EnvProject, error) {
	return nil, nil
}
func (fakeEnvRemote) EnvState(context.Context, domain.ShipTarget, string) (domain.EnvStateRaw, error) {
	return domain.EnvStateRaw{}, nil
}
func (fakeEnvRemote) EnvWrite(context.Context, domain.ShipTarget, string, string, []domain.EnvChange) error {
	return nil
}
func (fakeEnvRemote) BuildArgs(context.Context, domain.ShipPlan) (map[string]string, error) {
	return map[string]string{"VITE_A": "from-env-store"}, nil
}

type buildDelivery struct {
	fakeDelivery
	order *[]string
	seen  domain.ShipRequest
}

func (d *buildDelivery) Prepare(_ context.Context, t domain.ShipTarget, r domain.ShipRequest) (domain.ShipPlan, error) {
	d.seen = r
	plan := domain.ShipPlan{Target: t, Request: r, Project: r.Project, Fingerprint: "fp", Bundle: []byte("private-env-value"), Services: []string{"web"}, Build: &domain.BuildInfo{Source: r.Source, App: r.Project, Tag: r.Tag, Images: []domain.BuildSpec{{Service: "web"}}}}
	return plan, nil
}
func (d *buildDelivery) Execute(ctx context.Context, p domain.ShipPlan, progress func(domain.ShipProgress) error) error {
	*d.order = append(*d.order, "ship")
	return d.fakeDelivery.Execute(ctx, p, progress)
}

func TestBuildPlanComputesTagServerSideAndKeepsEnvValuesOutOfThePlan(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	order := []string{}
	delivery := &buildDelivery{order: &order}
	builder := &fakeBuilder{order: &order}
	s := application.NewShipping(delivery, repo, repo).WithBuilder(builder).WithEnv(fakeEnvRemote{})
	req := domain.ShipRequest{Mode: "build", Source: "shop", Tag: "browser-chosen", Project: "other", IncludeEnv: true, Env: []domain.EnvChange{{Name: "API_KEY", Action: "set", Value: "super-secret-123"}}}
	plan, err := s.Prepare(ctx, "admin", req)
	if err != nil {
		t.Fatal(err)
	}
	if delivery.seen.Tag != "abc1234567-dirty1" || delivery.seen.Project != "shop" || delivery.seen.IncludeEnv {
		t.Fatalf("browser-controlled fields reached the runner: %+v", delivery.seen)
	}
	if plan.Build.Head != "abc1234567" || !plan.Build.Dirty {
		t.Fatalf("%+v", plan.Build)
	}
	public, _ := json.Marshal(plan)
	if strings.Contains(string(public), "super-secret-123") || len(plan.Request.Env) != 0 {
		t.Fatal("env value in the public plan")
	}
	events, _ := repo.Audit(ctx, "shop", 0, 50)
	log, _ := json.Marshal(events)
	if strings.Contains(string(log), "super-secret-123") || !strings.Contains(string(log), "API_KEY") {
		t.Fatalf("audit must list names but never values: %s", log)
	}
	if _, err = s.Commit(ctx, "admin", plan.ID); err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); s.Run(runCtx) }()
	defer func() { cancel(); <-done }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if jobs, _ := s.Jobs(ctx); len(jobs) == 1 && jobs[0].Status == "succeeded" {
			if strings.Join(order, ",") != "build,ship" {
				t.Fatalf("a build must finish before shipping: %v", order)
			}
			if got := builder.built[0]; got.Tag != "abc1234567-dirty1" || got.Args["VITE_A"] != "from-env-store" || got.Services[0] != "web" || !got.Dirty {
				t.Fatalf("%+v", got)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("build job did not finish")
}
func TestEnvApplyAndBuildRulesAreEnforcedBeforeTheRunner(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	order := []string{}
	delivery := &buildDelivery{order: &order}
	s := application.NewShipping(delivery, repo, repo).WithBuilder(&fakeBuilder{order: &order}).WithEnv(fakeEnvRemote{})
	if _, err = s.Prepare(ctx, "admin", domain.ShipRequest{Mode: "apply", Project: "infra"}); err == nil {
		t.Fatal("infrastructure may never be an env apply target")
	}
	if _, err = s.Prepare(ctx, "admin", domain.ShipRequest{Mode: "apply", Project: "shop", Env: []domain.EnvChange{{Name: "A", Action: "set"}}}); err == nil {
		t.Fatal("an apply cannot carry edits")
	}
	if _, err = s.Prepare(ctx, "admin", domain.ShipRequest{Mode: "build", Source: "../etc"}); err == nil {
		t.Fatal("source id not validated")
	}
	if _, err = s.Prepare(ctx, "admin", domain.ShipRequest{Mode: "deploy", Project: "shop", Env: []domain.EnvChange{{Name: "A", Action: "set"}}}); err == nil {
		t.Fatal("env edits are only part of Build & Ship")
	}
	if _, err = s.Prepare(ctx, "admin", domain.ShipRequest{Mode: "build", Source: "shop", Env: []domain.EnvChange{{Name: "lower", Action: "set"}}}); err == nil {
		t.Fatal("invalid variable name accepted")
	}
	if err = repo.Save(ctx, domain.Policy{Project: "shop", Suspended: true}, domain.Event{At: time.Now(), Action: "suspend"}); err != nil {
		t.Fatal(err)
	}
	for _, req := range []domain.ShipRequest{{Mode: "apply", Project: "shop"}, {Mode: "build", Source: "shop"}} {
		if _, err = s.Prepare(ctx, "admin", req); err == nil {
			t.Fatalf("suspended project accepted %s", req.Mode)
		}
	}
	if delivery.seen.Mode != "" {
		t.Fatal("a rejected request reached the runner")
	}
	noBuilder := application.NewShipping(delivery, repo, repo)
	if _, err = noBuilder.Prepare(ctx, "admin", domain.ShipRequest{Mode: "build", Source: "shop"}); err == nil {
		t.Fatal("build accepted without a configured builder")
	}
}

func TestAutomaticPreviewsNeverReuseRollbackImageTags(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	order := []string{}
	delivery := &buildDelivery{order: &order}
	shipping := application.NewShipping(delivery, repo, repo).WithBuilder(&fakeBuilder{order: &order})
	req := domain.ShipRequest{Mode: "build", Source: "shop", Deployment: &domain.DeploymentRoute{Domain: "shop.example.com", Service: "shop-api", Port: 8080}}
	first, err := shipping.Prepare(ctx, "admin", req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := shipping.Prepare(ctx, "admin", req)
	if err != nil {
		t.Fatal(err)
	}
	if first.Build.Tag == second.Build.Tag || !strings.HasPrefix(first.Build.Tag, "abc1234567-dirty1-r") {
		t.Fatal("automatic deployments could replace a rollback tag")
	}
}
