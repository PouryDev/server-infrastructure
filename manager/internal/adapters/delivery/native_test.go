package delivery

import (
	"context"
	"encoding/json"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	"strings"
	"testing"
)

func TestNativeDeploymentKeepsSecretValuesOnlyInPrivateInput(t *testing.T) {
	plan := newPlan(domain.ShipTarget{}, domain.ShipRequest{Mode: "build", Project: "myapp", Deployment: &domain.DeploymentRoute{Domain: "app.example.com", Service: "myapp-api", Port: 8080, Revision: "server-revision"}})
	bundle := bundle{Manifest: map[string]any{"name": "myapp"}, EnvChanges: []domain.EnvChange{{Name: "PASSWORD", Action: "set", Value: "private-value"}, {Name: "OLD_KEY", Action: "delete"}}}
	in := nativeInput(plan, bundle, "test-key")
	if in.Domain != "app.example.com" || in.Revision != "server-revision" || in.Environment["PASSWORD"] != "private-value" || len(in.RemoveEnv) != 1 {
		t.Fatal("destination settings or env edits lost")
	}
	public, err := json.Marshal(plan)
	if err != nil || strings.Contains(string(public), "private-value") {
		t.Fatal("secret exposed in public plan")
	}
}

func TestAutomaticDeploymentCannotResetLimitsOrShipLocalEnv(t *testing.T) {
	engine := &Engine{}
	for _, req := range []domain.ShipRequest{
		{Mode: "build", Tag: "safe-tag", UseComposeLimits: true},
		{Mode: "build", Tag: "safe-tag", IncludeEnv: true},
		{Mode: "build", Tag: "safe-tag", Service: "myapp-api"},
	} {
		var plan domain.ShipPlan
		var bundle bundle
		if err := engine.prepareNative(context.Background(), domain.ShipTarget{Mode: "ssh"}, req, false, &plan, &bundle); err == nil {
			t.Fatal("unsafe automatic deployment accepted")
		}
	}
}
