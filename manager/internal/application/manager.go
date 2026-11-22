package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

var ErrNotFound = errors.New("project not found")
var ErrConflict = errors.New("project policy changed; refresh and retry")
var ErrInvalid = errors.New("invalid policy")
var ErrEnvConflict = errors.New("environment changed on the destination; reload before editing")

type Manager struct {
	runtime Runtime
	repo    Repository
	metrics Metrics
	clock   func() time.Time
	// Serialize policy writes and reconciliation, while reads use a separate lock.
	mu     sync.Mutex
	viewMu sync.RWMutex
	view   domain.Snapshot
}

func New(runtime Runtime, repo Repository, metrics Metrics) *Manager {
	return &Manager{runtime: runtime, repo: repo, metrics: metrics, clock: time.Now,
		view: domain.Snapshot{Projects: []domain.Project{}, Infrastructure: []domain.Container{}, SyncIntervalSeconds: 15}}
}

func (m *Manager) Snapshot() domain.Snapshot {
	m.viewMu.RLock()
	defer m.viewMu.RUnlock()
	return m.view // Published slices are immutable.
}

func (m *Manager) Audit(ctx context.Context, project string, before int64, limit int) ([]domain.Event, error) {
	return m.repo.Audit(ctx, project, before, limit)
}

func (m *Manager) Record(ctx context.Context, actor, action, outcome string) error {
	return m.repo.Record(ctx, m.event(actor, "", action, outcome, ""))
}

func (m *Manager) event(actor, project, action, outcome, detail string) domain.Event {
	return domain.Event{At: m.clock().UTC(), Actor: actor, Project: project, Action: action, Outcome: outcome, Detail: detail}
}

// Change persists intent and audit atomically. Docker work happens in Reconcile,
// so a process crash or transient engine failure cannot lose the desired state.
func (m *Manager) Change(ctx context.Context, actor, project, action string, expiry *time.Time, revision int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	containers, err := m.runtime.List(ctx)
	if err != nil {
		return fmt.Errorf("discover projects: %w", err)
	}
	found := false
	for _, c := range containers {
		if c.Project == project && c.Managed {
			found = true
			break
		}
	}
	if !found {
		return ErrNotFound
	}
	policies, err := m.repo.Policies(ctx)
	if err != nil {
		return err
	}
	p := policies[project]
	before := struct {
		ExpiresAt *time.Time `json:"expiresAt"`
		Suspended bool       `json:"suspended"`
	}{p.ExpiresAt, p.Suspended}
	if p.Revision != revision {
		return ErrConflict
	}
	p.Project = project
	if p.Stopped == nil {
		p.Stopped = map[string]string{}
	}
	switch action {
	case "suspend":
		p.Suspended = true
	case "renew":
		if expiry != nil && !expiry.After(m.clock()) {
			return ErrInvalid
		}
		p.Suspended = false
		p.ExpiresAt = expiry
	case "expiration":
		if expiry != nil && !expiry.After(m.clock()) {
			return ErrInvalid
		}
		p.ExpiresAt = expiry
	default:
		return ErrInvalid
	}
	p.Revision++
	after := struct {
		ExpiresAt *time.Time `json:"expiresAt"`
		Suspended bool       `json:"suspended"`
	}{p.ExpiresAt, p.Suspended}
	detail, err := json.Marshal(map[string]any{"before": before, "after": after})
	if err != nil {
		return err
	}
	if err := m.repo.Save(ctx, p, m.event(actor, project, action, "accepted", string(detail))); err != nil {
		return err
	}
	m.viewMu.Lock()
	projects := append([]domain.Project(nil), m.view.Projects...)
	for i := range projects {
		if projects[i].Name == project {
			projects[i].Policy = p
			projects[i].Status = status(projects[i], m.clock())
		}
	}
	m.view.Projects = projects
	m.viewMu.Unlock()
	return nil
}

func (m *Manager) Reconcile(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	containers, err := m.runtime.List(ctx)
	if err != nil {
		m.setError("Docker discovery unavailable; last snapshot retained")
		return err
	}
	policies, err := m.repo.Policies(ctx)
	if err != nil {
		m.setError("Policy storage unavailable; lifecycle actions blocked")
		return err
	}
	var failures []error
	for i := range containers {
		c := &containers[i]
		if !c.Managed {
			continue
		}
		p, ok := policies[c.Project]
		if !ok {
			continue
		}
		if p.Stopped == nil {
			p.Stopped = map[string]string{}
		}
		mustStop := p.MustStop(m.clock())
		if mustStop && (c.State == "running" || c.State == "restarting" || c.State == "paused") {
			if _, exists := p.Stopped[c.ID]; !exists {
				p.Stopped[c.ID] = c.Service
				if err = m.repo.Save(ctx, p, m.event("reconciler", c.Project, "stop", "intent", c.Name)); err != nil {
					delete(p.Stopped, c.ID)
					failures = append(failures, err)
					continue
				}
			} else if e := m.repo.Record(ctx, m.event("reconciler", c.Project, "stop", "intent", c.Name)); e != nil {
				failures = append(failures, e)
				continue
			}
			if err = m.runtime.Stop(ctx, c.ID); err != nil {
				c.Error = "Stop failed; retrying automatically"
				failures = append(failures, err)
			} else {
				c.State = "exited"
				c.Health = ""
				c.CPU = nil
				c.Memory = nil
			}
			if e := m.repo.Record(ctx, m.event("reconciler", c.Project, "stop", outcome(err), c.Name)); e != nil {
				failures = append(failures, e)
			}
		} else if !mustStop {
			_, tracked := p.Stopped[c.ID]
			// A replaced container is owned by its deployer, so do not implicitly
			// start a new ID; an expired replacement is discovered and stopped above.
			if tracked {
				if c.State == "exited" || c.State == "created" {
					if e := m.repo.Record(ctx, m.event("reconciler", c.Project, "start", "intent", c.Name)); e != nil {
						failures = append(failures, e)
						continue
					}
					err = m.runtime.Start(ctx, c.ID)
				} else if c.State == "running" {
					err = nil
				} else {
					continue
				}
				if err != nil {
					c.Error = "Start failed; retrying automatically"
					failures = append(failures, err)
				} else {
					delete(p.Stopped, c.ID)
					if e := m.repo.Save(ctx, p, m.event("reconciler", c.Project, "start", "success", c.Name)); e != nil {
						failures = append(failures, e)
					}
					c.State = "running"
					c.Health = "starting"
				}
				if err != nil {
					if e := m.repo.Record(ctx, m.event("reconciler", c.Project, "start", "failure", c.Name)); e != nil {
						failures = append(failures, e)
					}
				}
			}
		}
		policies[c.Project] = p
	}
	groups := map[string]*domain.Project{}
	infra := []domain.Container{}
	for _, c := range containers {
		if c.Project == "infra" {
			infra = append(infra, c)
			continue
		}
		p := groups[c.Project]
		if p == nil {
			policy := policies[c.Project]
			policy.Project = c.Project
			p = &domain.Project{Name: c.Project, Policy: policy, Containers: []domain.Container{}, Metrics: []domain.Metric{}}
			groups[c.Project] = p
		}
		p.Containers = append(p.Containers, c)
	}
	// Keep configured projects visible even if Docker containers have been removed.
	for name, policy := range policies {
		if groups[name] == nil {
			groups[name] = &domain.Project{Name: name, Policy: policy, Containers: []domain.Container{}, Metrics: []domain.Metric{}}
		}
	}
	projects := []domain.Project{}
	for _, p := range groups {
		p.Status = status(*p, m.clock())
		projects = append(projects, *p)
	}
	sort.Slice(projects, func(i, j int) bool { return projects[i].Name < projects[j].Name })
	now := m.clock().UTC()
	m.viewMu.Lock()
	// Metric collection runs separately: a monitoring outage never delays expiry.
	old := m.view
	for i := range projects {
		for _, p := range old.Projects {
			if p.Name == projects[i].Name {
				projects[i].Metrics = p.Metrics
			}
		}
	}
	m.view = domain.Snapshot{Projects: projects, Infrastructure: infra, SyncedAt: &now, MetricsAt: old.MetricsAt, MetricsError: old.MetricsError, SyncIntervalSeconds: 15}
	if len(failures) > 0 {
		m.view.Error = "Some lifecycle actions failed; retrying automatically. See audit log."
	}
	m.viewMu.Unlock()
	return errors.Join(failures...)
}

func outcome(err error) string {
	if err != nil {
		return "failure"
	}
	return "success"
}

func status(p domain.Project, now time.Time) string {
	running, active, total, healthy := 0, 0, 0, true
	for _, c := range p.Containers {
		if !c.Managed {
			continue
		}
		total++
		if c.State == "running" {
			running++
		}
		if c.State == "running" || c.State == "restarting" || c.State == "paused" {
			active++
		}
		if c.Health == "unhealthy" || c.Health == "starting" || c.Error != "" {
			healthy = false
		}
	}
	if p.Policy.MustStop(now) {
		if active > 0 {
			return "stopping"
		}
		if p.Policy.Suspended {
			return "suspended"
		}
		return "expired"
	}
	if total == 0 {
		return "missing"
	}
	if active == 0 {
		return "offline"
	}
	if running != total || !healthy {
		return "degraded"
	}
	return "online"
}

func (m *Manager) setError(s string) { m.viewMu.Lock(); defer m.viewMu.Unlock(); m.view.Error = s }

func (m *Manager) CollectMetrics(ctx context.Context) error {
	metrics, err := m.metrics.Collect(ctx)
	m.viewMu.Lock()
	defer m.viewMu.Unlock()
	if err != nil {
		m.view.MetricsError = "Some monitoring data is unavailable; missing metrics are not zero."
	} else {
		m.view.MetricsError = ""
	}
	// Copy slices to preserve immutable snapshots held by concurrent handlers.
	projects := append([]domain.Project(nil), m.view.Projects...)
	for i := range projects {
		projects[i].Metrics = []domain.Metric{}
		for _, metric := range metrics {
			if metric.Project == projects[i].Name {
				projects[i].Metrics = append(projects[i].Metrics, metric)
			}
		}
	}
	m.view.Projects = projects
	now := m.clock().UTC()
	m.view.MetricsAt = &now
	return err
}
