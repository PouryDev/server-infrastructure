package application

import (
	"context"
	"fmt"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type VPNRunner interface {
	Status(context.Context) (domain.VPNStatus, error)
	Change(context.Context, domain.VPNChange) (domain.VPNStatus, error)
	Test(context.Context) error
}

type VPN struct {
	Runner VPNRunner
	Repo   StorefrontAudit
}

func (v *VPN) Change(ctx context.Context, actor string, change domain.VPNChange) (domain.VPNStatus, error) {
	if len(change.Link) > 16384 || len(change.Revision) != 64 {
		return domain.VPNStatus{}, fmt.Errorf("Invalid VPN request")
	}
	e := domain.Event{At: time.Now().UTC(), Actor: actor, Project: "infra", Action: "vpn.change", Outcome: "intent"}
	if err := v.Repo.Record(ctx, e); err != nil {
		return domain.VPNStatus{}, fmt.Errorf("Audit storage unavailable")
	}
	work, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	out, err := v.Runner.Change(work, change)
	e.At = time.Now().UTC()
	e.Outcome = "success"
	if err != nil {
		e.Outcome = "failure"
	}
	// Never put link, config, endpoint or runner diagnostics in the audit log.
	if auditErr := v.Repo.Record(work, e); auditErr != nil && err == nil {
		err = fmt.Errorf("Audit storage unavailable")
	}
	return out, err
}
