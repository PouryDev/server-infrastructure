package application

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

// Envs manages project environment variables stored on the destination.
type Envs struct {
	remote EnvRemote
	repo   ShippingRepository
	audit  Repository
	mu     sync.Mutex
}

func NewEnvs(remote EnvRemote, repo ShippingRepository, audit Repository) *Envs {
	return &Envs{remote: remote, repo: repo, audit: audit}
}

func envEvent(actor, project, action, outcome, detail string) domain.Event {
	return domain.Event{At: time.Now().UTC(), Actor: actor, Project: project, Action: action, Outcome: outcome, Detail: detail}
}
func (s *Envs) target(ctx context.Context) (domain.ShipTarget, error) {
	t, err := s.repo.Target(ctx)
	if err != nil {
		return t, err
	}
	return t, ValidateTarget(t)
}

func (s *Envs) Projects(ctx context.Context) ([]domain.EnvProject, error) {
	t, err := s.target(ctx)
	if err != nil {
		return nil, err
	}
	return s.remote.EnvProjects(ctx, t)
}

// State returns the masked store: no value ever appears in a list response.
func (s *Envs) State(ctx context.Context, project string) (domain.EnvState, error) {
	if !domain.ValidProject(project) {
		return domain.EnvState{}, fmt.Errorf("invalid project")
	}
	t, err := s.target(ctx)
	if err != nil {
		return domain.EnvState{}, err
	}
	raw, err := s.remote.EnvState(ctx, t, project)
	if err != nil {
		return domain.EnvState{}, err
	}
	return domain.MaskEnv(raw), nil
}

// Reveal returns one value after an audited intent record. The audit stores only
// the variable name and scope.
func (s *Envs) Reveal(ctx context.Context, actor, project string, req domain.EnvRevealRequest) (string, error) {
	if !domain.ValidProject(project) || !domain.ValidEnvName(req.Name) || (req.Scope != "" && !domain.ValidService(req.Scope)) {
		return "", fmt.Errorf("invalid variable")
	}
	t, err := s.target(ctx)
	if err != nil {
		return "", err
	}
	detail := fmt.Sprintf("scope=%q name=%s", req.Scope, req.Name)
	if err = s.audit.Record(ctx, envEvent(actor, project, "env_reveal", "intent", detail)); err != nil {
		return "", fmt.Errorf("Audit storage unavailable")
	}
	raw, err := s.remote.EnvState(ctx, t, project)
	outcomeText := "success"
	var value string
	found := false
	if err == nil {
		for _, file := range raw.Files {
			if file.Scope != req.Scope {
				continue
			}
			for _, v := range file.Variables {
				if v.Name == req.Name {
					value, found = v.Value, true
				}
			}
		}
		if !found {
			err = fmt.Errorf("variable not found")
		}
	}
	if err != nil {
		outcomeText = "failure"
	}
	if auditErr := s.audit.Record(ctx, envEvent(actor, project, "env_reveal", outcomeText, detail)); auditErr != nil && err == nil {
		return "", fmt.Errorf("Audit storage unavailable")
	}
	return value, err
}

func (s *Envs) activeJob(ctx context.Context) (bool, error) {
	jobs, err := s.repo.ShipJobs(ctx)
	if err != nil {
		return false, err
	}
	for _, j := range jobs {
		if j.Status == "queued" || j.Status == "running" {
			return true, nil
		}
	}
	return false, nil
}

// Write applies reviewed edits. It records the intent (names and actions only)
// in the audit log before touching the destination and the result afterwards.
func (s *Envs) Write(ctx context.Context, actor, project string, req domain.EnvWriteRequest) (domain.EnvState, error) {
	if !domain.ValidProject(project) {
		return domain.EnvState{}, fmt.Errorf("invalid project")
	}
	if len(req.Changes) == 0 || len(req.Revision) != 64 {
		return domain.EnvState{}, fmt.Errorf("review the changes before saving")
	}
	if err := domain.ValidateEnvChanges(req.Changes); err != nil {
		return domain.EnvState{}, err
	}
	t, err := s.target(ctx)
	if err != nil {
		return domain.EnvState{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if active, err := s.activeJob(ctx); err != nil {
		return domain.EnvState{}, err
	} else if active {
		return domain.EnvState{}, fmt.Errorf("a shipment is active; wait for it to finish before editing variables")
	}
	detail := domain.AuditEnvDetail(req.Changes)
	if err = s.audit.Record(ctx, envEvent(actor, project, "env_change", "intent", detail)); err != nil {
		return domain.EnvState{}, fmt.Errorf("Audit storage unavailable")
	}
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	err = s.remote.EnvWrite(work, t, project, req.Revision, req.Changes)
	result := "success"
	if err != nil {
		result = "failure"
	}
	auditErr := s.audit.Record(work, envEvent(actor, project, "env_change", result, detail))
	if err != nil {
		return domain.EnvState{}, err
	}
	if auditErr != nil {
		return domain.EnvState{}, fmt.Errorf("variables were saved but the audit record could not be stored")
	}
	raw, err := s.remote.EnvState(work, t, project)
	if err != nil {
		return domain.EnvState{}, err
	}
	return domain.MaskEnv(raw), nil
}
