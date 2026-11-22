package application

import (
	"context"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	"testing"
)

type builderRunner struct{ called bool }

func (b *builderRunner) Catalog(context.Context) (domain.StorefrontCatalog, error) {
	return domain.StorefrontCatalog{Images: []string{"storefront-api:sha"}, Themes: []domain.StorefrontTheme{{ID: "atelier"}}, Modules: []domain.StorefrontModule{{ID: "blog"}}}, nil
}
func (b *builderRunner) Create(_ context.Context, in domain.StorefrontSpec) (domain.StorefrontJob, error) {
	b.called = true
	return domain.StorefrontJob{ID: in.Key, Name: in.Name, Status: "queued"}, nil
}

type builderAudit struct {
	Repository
	events []domain.Event
}

func (a *builderAudit) Record(_ context.Context, e domain.Event) error {
	a.events = append(a.events, e)
	return nil
}
func TestStorefrontValidationAndAudit(t *testing.T) {
	runner := &builderRunner{}
	audit := &builderAudit{}
	s := Storefront{Runner: runner, Repo: audit}
	good := domain.StorefrontSpec{Key: "12345678-1234-1234-1234-123456789012", Name: "myshop", Domain: "myshop.localhost", Title: "Shop", Theme: "atelier", OwnerPhone: "+989121234567", AdminPhone: "+989121234568", Image: "storefront-api:sha", Demo: true, Modules: []string{"blog"}}
	for _, mutate := range []func(*domain.StorefrontSpec){func(s *domain.StorefrontSpec) { s.Name = "../escape" }, func(s *domain.StorefrontSpec) { s.Domain = "shop.example.com" }, func(s *domain.StorefrontSpec) { s.AdminPhone = s.OwnerPhone }, func(s *domain.StorefrontSpec) { s.Image = "arbitrary:latest" }, func(s *domain.StorefrontSpec) { s.Modules = []string{"unknown"} }, func(s *domain.StorefrontSpec) { s.Demo = false }} {
		in := good
		mutate(&in)
		if _, e := s.Create(context.Background(), "developer", in); e == nil {
			t.Fatal(in)
		}
		if runner.called {
			t.Fatal("invalid request reached runner")
		}
	}
	job, e := s.Create(context.Background(), "developer", good)
	if e != nil || job.Status != "queued" || len(audit.events) != 1 {
		t.Fatal(job, e, audit.events)
	}
}
