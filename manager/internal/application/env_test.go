package application_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/sqlite"
	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type recordingEnv struct {
	mu     sync.Mutex
	writes int
}

func (r *recordingEnv) EnvProjects(context.Context, domain.ShipTarget) ([]domain.EnvProject, error) {
	return nil, nil
}
func (r *recordingEnv) EnvState(_ context.Context, _ domain.ShipTarget, project string) (domain.EnvStateRaw, error) {
	return domain.EnvStateRaw{Project: project, Revision: strings.Repeat("c", 64), Files: []domain.EnvFileRaw{{Exists: true, Variables: []domain.EnvPair{{Name: "TOKEN", Value: "tok-123456"}}}}}, nil
}
func (r *recordingEnv) EnvWrite(context.Context, domain.ShipTarget, string, string, []domain.EnvChange) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.writes++
	return nil
}
func (r *recordingEnv) BuildArgs(context.Context, domain.ShipPlan) (map[string]string, error) {
	return nil, nil
}

func TestEnvWriteIsBlockedWhileAShipmentIsActiveAndNeverAuditsValues(t *testing.T) {
	ctx := context.Background()
	repo, err := sqlite.Open(filepath.Join(t.TempDir(), "manager.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	remote := &recordingEnv{}
	envs := application.NewEnvs(remote, repo, repo)
	req := domain.EnvWriteRequest{Revision: strings.Repeat("c", 64), Changes: []domain.EnvChange{{Name: "TOKEN", Action: "set", Value: "brand-new-secret"}}}
	state, err := envs.Write(ctx, "admin", "shop", req)
	if err != nil || remote.writes != 1 {
		t.Fatal(err)
	}
	data, _ := json.Marshal(state)
	if strings.Contains(string(data), "tok-123456") || strings.Contains(string(data), "brand-new-secret") {
		t.Fatal("masked state contains a value")
	}
	// Queue a shipment: edits must wait for it.
	s := application.NewShipping(&fakeDelivery{}, repo, repo)
	plan, err := s.Prepare(ctx, "admin", domain.ShipRequest{Project: "shop", Mode: "deploy"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.Commit(ctx, "admin", plan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = envs.Write(ctx, "admin", "shop", req); err == nil || remote.writes != 1 {
		t.Fatal("env edit accepted during an active shipment")
	}
	if _, err = envs.Write(ctx, "admin", "infra", req); err == nil {
		t.Fatal("infrastructure env edit accepted")
	}
	value, err := envs.Reveal(ctx, "admin", "shop", domain.EnvRevealRequest{Name: "TOKEN"})
	if err != nil || value != "tok-123456" {
		t.Fatal(err, value)
	}
	events, _ := repo.Audit(ctx, "shop", 0, 100)
	log, _ := json.Marshal(events)
	for _, secret := range []string{"tok-123456", "brand-new-secret"} {
		if strings.Contains(string(log), secret) {
			t.Fatalf("audit contains %s", secret)
		}
	}
	for _, action := range []string{"env_reveal", "env_change"} {
		if !strings.Contains(string(log), action) {
			t.Fatalf("missing audit action %s", action)
		}
	}
}
