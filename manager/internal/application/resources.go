package application

import (
	"context"
	"fmt"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type ResourceRunner interface {
	Preview(context.Context, domain.ResourceRequest) (domain.ResourcePreview, error)
	Apply(context.Context, domain.ResourceRequest) (domain.ResourceResult, error)
}
type Resources struct {
	Runner ResourceRunner
	Repo   StorefrontAudit
}

func (s *Resources) Preview(ctx context.Context, r domain.ResourceRequest) (domain.ResourcePreview, error) {
	if err := r.Validate(); err != nil {
		return domain.ResourcePreview{}, err
	}
	return s.Runner.Preview(ctx, r)
}
func (s *Resources) Apply(ctx context.Context, actor string, r domain.ResourceRequest) (domain.ResourceResult, error) {
	if err := r.Validate(); err != nil {
		return domain.ResourceResult{}, err
	}
	if len(r.Revision) != 64 {
		return domain.ResourceResult{}, fmt.Errorf("resource preview is stale; preview again")
	}
	event := domain.Event{At: time.Now().UTC(), Actor: actor, Project: r.Project, Action: "resources.apply", Outcome: "intent", Detail: fmt.Sprintf("service=%s memoryMiB=%d cpus=%.2f git=%s", r.Service, r.MemoryMiB, r.CPUs, r.GitMode)}
	if err := s.Repo.Record(ctx, event); err != nil {
		return domain.ResourceResult{}, fmt.Errorf("Audit storage unavailable")
	}
	// Complete/rollback independently of a browser disconnect, bounded by the runner.
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Minute)
	defer cancel()
	out, err := s.Runner.Apply(work, r)
	event.At = time.Now().UTC()
	event.Outcome = "success"
	if err != nil {
		event.Outcome = "failure"
		event.Detail += "; " + err.Error()
	} else {
		event.Detail += fmt.Sprintf("; commit=%s pushed=%t", out.Commit, out.Pushed)
		if out.Warning != "" {
			event.Outcome = "partial"
			event.Detail += "; " + out.Warning
		}
	}
	if auditErr := s.Repo.Record(work, event); auditErr != nil {
		out.Warning += " Audit storage unavailable; inspect service state before retrying"
	}
	return out, err
}
