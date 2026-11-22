//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/docker"
	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/gateway"
	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/sqlite"
	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type noMetrics struct{}

func (noMetrics) Collect(context.Context) ([]domain.Metric, error) { return nil, nil }

func TestRealDockerLifecycle(t *testing.T) {
	ctx := context.Background()
	httpClient := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", "/var/run/docker.sock")
	}}}
	call := func(method, path string, body any) []byte {
		t.Helper()
		var data []byte
		if body != nil {
			data, _ = json.Marshal(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		res, err := httpClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err = io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			t.Fatalf("fixture %s %s: %d %s", method, path, res.StatusCode, data)
		}
		return data
	}
	project := "manager-it-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	create := func(service string, start bool) string {
		t.Helper()
		var result struct {
			ID string `json:"Id"`
		}
		json.Unmarshal(call("POST", "/containers/create?name="+project+"-"+service, map[string]any{"Image": "alpine:3", "Cmd": []string{"sh", "-c", "echo preserved > /marker; trap 'exit 0' TERM INT; while :; do sleep 3600 & wait $!; done"}, "Labels": map[string]string{"com.docker.compose.project": project, "com.docker.compose.service": service}, "HostConfig": map[string]any{"NetworkMode": "infra", "Memory": 16 << 20}}), &result)
		if result.ID == "" {
			t.Fatal("fixture returned no ID")
		}
		// IDs originate solely from this create response; no name/glob cleanup.
		t.Cleanup(func() {
			req, _ := http.NewRequest("DELETE", "http://docker/containers/"+result.ID+"?force=true", nil)
			res, err := httpClient.Do(req)
			if err != nil {
				t.Error(err)
			} else {
				res.Body.Close()
				if res.StatusCode != 204 {
					t.Errorf("fixture cleanup: %d", res.StatusCode)
				}
			}
		})
		if start {
			call("POST", "/containers/"+result.ID+"/start", nil)
		}
		return result.ID
	}
	api, web, owner := create("api", true), create("web", true), create("owner-stopped", false)
	paused := create("paused-runtime", true)
	call("POST", "/containers/"+paused+"/pause", nil)
	engine := docker.New("/var/run/docker.sock", "infra", "infra")
	const token = "integration-test-token-01234567890123456789"
	broker := httptest.NewServer(gateway.Handler(engine, token))
	defer broker.Close()
	runtime := gateway.New(broker.URL, token)
	path := filepath.Join(t.TempDir(), "manager.db")
	repo, err := sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	m := application.New(runtime, repo, noMetrics{})
	future := time.Now().Add(time.Hour)
	if err = m.Change(ctx, "integration", project, "expiration", &future, 0); err != nil {
		t.Fatal(err)
	}
	policies, err := repo.Policies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	p := policies[project]
	past := time.Now().Add(-time.Second)
	p.ExpiresAt = &past
	if err = repo.Save(ctx, p, domain.Event{At: time.Now(), Project: project, Actor: "integration", Action: "test-clock", Outcome: "accepted"}); err != nil {
		t.Fatal(err)
	}
	if err = m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	inspectState := func(id string) string {
		var state struct{ State struct{ Status string } }
		json.Unmarshal(call("GET", "/containers/"+id+"/json", nil), &state)
		return state.State.Status
	}
	if inspectState(api) != "exited" || inspectState(web) != "exited" || inspectState(paused) != "exited" || inspectState(owner) != "created" {
		t.Fatal("expiration did not stop just running services")
	}
	// Real manager process/storage replacement preserves enforcement and history.
	repo.Close()
	repo, err = sqlite.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer repo.Close()
	m = application.New(runtime, repo, noMetrics{})
	worker := create("replacement-worker", true)
	if err = m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if inspectState(worker) != "exited" {
		t.Fatal("expired redeployment remained running")
	}
	if err = m.Change(ctx, "integration", project, "renew", nil, 1); err != nil {
		t.Fatal(err)
	}
	if err = m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{api, web, worker} {
		if inspectState(id) != "running" {
			t.Fatal("renew did not resume owned container")
		}
		marker := call("GET", "/containers/"+id+"/archive?path=/marker", nil)
		if !bytes.Contains(marker, []byte("preserved")) {
			t.Fatal("container data lost")
		}
	}
	if inspectState(owner) != "created" {
		t.Fatal("owner-stopped service was started")
	}
	if err = m.Change(ctx, "integration", project, "suspend", nil, 2); err != nil {
		t.Fatal(err)
	}
	if err = m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if inspectState(api) != "exited" {
		t.Fatal("manual suspension failed")
	}
	if err = m.Change(ctx, "integration", project, "renew", nil, 3); err != nil {
		t.Fatal(err)
	}
	if err = m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	events, err := repo.Audit(ctx, project, 0, 200)
	if err != nil || len(events) < 15 {
		t.Fatalf("audit not durable: %v events=%d", err, len(events))
	}
	t.Log(fmt.Sprintf("verified expiry, redeploy, restart, renew, manual stop, retained IDs/data and %d audit events", len(events)))
}
