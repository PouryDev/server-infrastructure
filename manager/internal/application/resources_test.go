package application

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type resourceRunner struct {
	called bool
	err    error
	result domain.ResourceResult
}

func (r *resourceRunner) Preview(context.Context, domain.ResourceRequest) (domain.ResourcePreview, error) {
	return domain.ResourcePreview{}, nil
}
func (r *resourceRunner) Apply(context.Context, domain.ResourceRequest) (domain.ResourceResult, error) {
	r.called = true
	return r.result, r.err
}

type resourceAudit struct {
	events []domain.Event
	fail   bool
}

func (a *resourceAudit) Record(_ context.Context, e domain.Event) error {
	if a.fail {
		return errors.New("storage unavailable")
	}
	a.events = append(a.events, e)
	return nil
}
func TestResourcesAuditBeforeApplyAndRecordPartialOutcome(t *testing.T) {
	r := domain.ResourceRequest{Project: "shop", Service: "shop-api", MemoryMiB: 512, CPUs: 1, GitMode: "commit", Revision: strings.Repeat("a", 64)}
	runner := &resourceRunner{result: domain.ResourceResult{Applied: true, Commit: "abc", Warning: "push unavailable"}}
	audit := &resourceAudit{fail: true}
	s := Resources{Runner: runner, Repo: audit}
	if _, err := s.Apply(context.Background(), "owner", r); err == nil || runner.called {
		t.Fatal("mutation escaped failed audit")
	}
	audit.fail = false
	if _, err := s.Apply(context.Background(), "owner", r); err != nil {
		t.Fatal(err)
	}
	if len(audit.events) != 2 || audit.events[0].Outcome != "intent" || audit.events[1].Outcome != "partial" || audit.events[0].Actor != "owner" {
		t.Fatal(audit.events)
	}
}
func TestResourcesInvalidInputNeverReachesRunner(t *testing.T) {
	runner := &resourceRunner{}
	s := Resources{Runner: runner, Repo: &resourceAudit{}}
	if _, err := s.Apply(context.Background(), "owner", domain.ResourceRequest{}); err == nil || runner.called {
		t.Fatal("invalid resource request accepted")
	}
}
