package application_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/sqlite"
	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type runtime struct {
	containers       []domain.Container
	stopped, started []string
	failStop         bool
	err              error
}

func (r *runtime) List(context.Context) ([]domain.Container, error) {
	return append([]domain.Container(nil), r.containers...), r.err
}
func (r *runtime) Stop(_ context.Context, id string) error {
	r.stopped = append(r.stopped, id)
	if r.failStop {
		return errors.New("engine unavailable")
	}
	for i := range r.containers {
		if r.containers[i].ID == id {
			r.containers[i].State = "exited"
		}
	}
	return nil
}
func (r *runtime) Start(_ context.Context, id string) error {
	r.started = append(r.started, id)
	for i := range r.containers {
		if r.containers[i].ID == id {
			r.containers[i].State = "running"
		}
	}
	return nil
}

type metrics struct{}

func (metrics) Collect(context.Context) ([]domain.Metric, error) { return nil, nil }
func container(id, state string) domain.Container {
	return domain.Container{ID: id, Project: "shop", Service: id, Name: id, State: state, Managed: true}
}

func TestExpirationSurvivesRestartAndRenewStartsOnlyOwnedContainers(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "manager.db")
	repo, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rt := &runtime{containers: []domain.Container{container("api", "running"), container("web", "running"), container("owner-stopped", "exited")}}
	m := application.New(rt, repo, metrics{})
	past := time.Now().Add(-time.Minute)
	if err = repo.Save(ctx, domain.Policy{Project: "shop", ExpiresAt: &past, Revision: 1}, domain.Event{At: time.Now(), Project: "shop", Action: "expiration"}); err != nil {
		t.Fatal(err)
	}
	if err = m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(rt.stopped) != 2 || m.Snapshot().Projects[0].Status != "expired" {
		t.Fatalf("expiration: %+v, %+v", rt.stopped, m.Snapshot())
	}
	repo.Close()
	repo, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	m = application.New(rt, repo, metrics{})
	// A fresh deploy while expired is also stopped.
	rt.containers = append(rt.containers, container("new-worker", "running"))
	if err = m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(rt.stopped) != 3 {
		t.Fatal("replacement escaped expiration")
	}
	future := time.Now().Add(time.Hour)
	if err = m.Change(ctx, "admin", "shop", "renew", &future, 1); err != nil {
		t.Fatal(err)
	}
	if err = m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(rt.started) != 3 {
		t.Fatalf("unexpected starts: %v", rt.started)
	}
	for _, id := range rt.started {
		if id == "owner-stopped" {
			t.Fatal("started an owner-stopped service")
		}
	}
	if err = m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if len(rt.started) != 3 {
		t.Fatal("reconciliation is not idempotent")
	}
	events, err := repo.Audit(ctx, "shop", 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	starts, stops := 0, 0
	for _, event := range events {
		if event.Outcome == "success" {
			if event.Action == "start" {
				starts++
			}
			if event.Action == "stop" {
				stops++
			}
		}
	}
	if starts != 3 || stops != 3 {
		t.Fatalf("missing lifecycle outcomes: %+v", events)
	}
}

func TestPolicyValidationConcurrencyAndManualSuspension(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	rt := &runtime{containers: []domain.Container{container("api", "running")}}
	m := application.New(rt, repo, metrics{})
	past := time.Now().Add(-time.Minute)
	if !errors.Is(m.Change(ctx, "a", "shop", "expiration", &past, 0), application.ErrInvalid) {
		t.Fatal("past expiration accepted")
	}
	if !errors.Is(m.Change(ctx, "a", "infra", "suspend", nil, 0), application.ErrNotFound) {
		t.Fatal("infra mutation accepted")
	}
	if err = m.Change(ctx, "a", "shop", "suspend", nil, 0); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(m.Change(ctx, "a", "shop", "renew", nil, 0), application.ErrConflict) {
		t.Fatal("stale write accepted")
	}
	if err = m.Change(ctx, "a", "shop", "expiration", nil, 1); err != nil {
		t.Fatal(err)
	}
	if err = m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if m.Snapshot().Projects[0].Status != "suspended" {
		t.Fatal("removing expiry resumed a manual suspension")
	}
}

func TestFailedStopRetainsIntentAndRetries(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	rt := &runtime{containers: []domain.Container{container("api", "running")}, failStop: true}
	m := application.New(rt, repo, metrics{})
	if err = m.Change(ctx, "a", "shop", "suspend", nil, 0); err != nil {
		t.Fatal(err)
	}
	if m.Reconcile(ctx) == nil {
		t.Fatal("stop failure hidden")
	}
	policies, err := repo.Policies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if policies["shop"].Stopped["api"] != "api" {
		t.Fatal("stop intent was lost")
	}
	if m.Snapshot().Projects[0].Status != "stopping" {
		t.Fatal("project reported stopped before engine success")
	}
	rt.failStop = false
	if err = m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	rt.err = errors.New("offline")
	if m.Reconcile(ctx) == nil || m.Snapshot().Error == "" || len(m.Snapshot().Projects) != 1 {
		t.Fatal("outage discarded last snapshot")
	}
}

type readOnlyAudit struct{ application.Repository }

func (r readOnlyAudit) Record(context.Context, domain.Event) error {
	return errors.New("audit disk unavailable")
}

func TestRenewDoesNotMutateDockerWhenAuditCannotBeWritten(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	p := domain.Policy{Project: "shop", Stopped: map[string]string{"api": "api"}, Revision: 1}
	if err := repo.Save(ctx, p, domain.Event{At: time.Now(), Project: "shop", Action: "renew"}); err != nil {
		t.Fatal(err)
	}
	rt := &runtime{containers: []domain.Container{container("api", "exited")}}
	m := application.New(rt, readOnlyAudit{repo}, metrics{})
	if m.Reconcile(ctx) == nil || len(rt.started) != 0 {
		t.Fatal("runtime was mutated without a writable audit log")
	}
}
