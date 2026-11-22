//go:build shippingintegration

package delivery

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

func TestLocalImageShippingAndSelectiveDeploy(t *testing.T) {
	ctx := context.Background()
	root, err := os.MkdirTemp("/tmp/infra-manager-target", "it-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	project := "ship-it-" + filepath.Base(root)
	source := filepath.Join(root, "source", project)
	os.MkdirAll(filepath.Join(source, "config"), 0700)
	os.WriteFile(filepath.Join(source, ".env"), []byte("PRIVATE_VALUE=private-integration-secret\n"), 0600)
	os.WriteFile(filepath.Join(source, "config/message"), []byte("config-preserved"), 0600)
	write := func(marker string, broken bool) {
		t.Helper()
		cmd := `echo "$MARKER" > /marker; trap 'exit 0' TERM INT; while :; do sleep 3600 & wait $!; done`
		if broken {
			cmd = "exit 1"
		}
		m := map[string]any{"services": map[string]any{"api": map[string]any{"image": "alpine:3", "env_file": []string{".env"}, "environment": map[string]string{"MARKER": marker}, "command": []string{"sh", "-c", strings.ReplaceAll(cmd, "$", "$$")}, "healthcheck": map[string]any{"test": []string{"CMD", "test", "-f", "/marker"}, "interval": "1s", "timeout": "1s", "retries": 2}}, "web": map[string]any{"image": "alpine:3", "command": []string{"sh", "-c", `trap 'exit 0' TERM INT; while :; do sleep 3600 & wait $$!; done`}, "environment": map[string]string{"KEEP_REMOTE": marker}, "volumes": []string{"./config/message:/config-message:ro"}, "ports": []string{"127.0.0.1:58999:80"}}}}
		data, _ := json.Marshal(m)
		if err := os.WriteFile(filepath.Join(source, "compose.yaml"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("initial", false)
	target := domain.ShipTarget{Mode: "local", Port: 22, Identity: "id_ed25519", InfraDir: filepath.Join(root, "destination", "infra"), AppsDir: filepath.Join(root, "destination", "apps"), DataDir: filepath.Join(root, "destination", "data")}
	e := &Engine{AppsRoot: filepath.Join(root, "source"), KeysRoot: os.Getenv("MANAGER_TEST_SSH_KEYS")}
	progress := func(p domain.ShipProgress) error { t.Log(p.Phase); return nil }
	req := domain.ShipRequest{Project: project, Mode: "deploy", IncludeEnv: true}
	p, err := e.Prepare(ctx, target, req)
	if err != nil {
		t.Fatal(err)
	}
	p.ID = "first"
	cleanupProject := p.Project
	t.Cleanup(func() {
		ids, _ := run(context.Background(), "", nil, "docker", "ps", "-aq", "--filter", "label=com.docker.compose.project="+cleanupProject)
		for _, id := range strings.Fields(string(ids)) {
			run(context.Background(), "", nil, "docker", "rm", "-f", id)
		}
		for _, image := range p.Images {
			run(context.Background(), "", nil, "docker", "image", "rm", image.DestinationRef)
		}
	})
	public, _ := json.Marshal(p)
	if strings.Contains(string(public), "private-integration-secret") {
		t.Fatal("secret in public preview")
	}
	if err = e.Execute(ctx, p, progress); err != nil {
		t.Fatal(err)
	}
	id := func(svc string) string {
		data, err := run(ctx, "", nil, "docker", "ps", "-aq", "--filter", "label=com.docker.compose.project="+p.Project, "--filter", "label=com.docker.compose.service="+svc)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(data))
	}
	webID := id("web")
	apiID := id("api")
	if webID == "" || apiID == "" {
		t.Fatal("destination services missing")
	}
	data, err := run(ctx, "", nil, "docker", "exec", apiID, "cat", "/marker")
	if err != nil || strings.TrimSpace(string(data)) != "initial" {
		t.Fatal("command interpolation or env handling failed")
	}
	data, err = run(ctx, "", nil, "docker", "exec", webID, "cat", "/config-message")
	if err != nil || strings.TrimSpace(string(data)) != "config-preserved" {
		t.Fatal("bind configuration did not arrive")
	}
	data, _ = run(ctx, "", nil, "docker", "inspect", webID, "--format", "{{json .HostConfig.PortBindings}}")
	if strings.Contains(string(data), "58999") {
		t.Fatal("sandbox published a host port")
	}
	write("updated", false)
	req.Service = "api"
	p2, err := e.Prepare(ctx, target, req)
	if err != nil {
		t.Fatal(err)
	}
	p2.ID = "second"
	if err = e.Execute(ctx, p2, progress); err != nil {
		t.Fatal(err)
	}
	if id("web") != webID {
		t.Fatal("unselected container was recreated")
	}
	data, _ = run(ctx, "", nil, "docker", "inspect", webID, "--format", "{{json .Config.Env}}")
	if strings.Contains(string(data), "KEEP_REMOTE=updated") {
		t.Fatal("unselected destination service was changed")
	}
	data, _ = run(ctx, "", nil, "docker", "exec", id("api"), "cat", "/marker")
	if strings.TrimSpace(string(data)) != "updated" {
		t.Fatal("selected service was not updated")
	}
	write("broken", true)
	bad, err := e.Prepare(ctx, target, req)
	if err != nil {
		t.Fatal(err)
	}
	bad.ID = "broken"
	if err = e.Execute(ctx, bad, progress); err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("missing rollback: %v", err)
	}
	data, _ = run(ctx, "", nil, "docker", "exec", id("api"), "cat", "/marker")
	if strings.TrimSpace(string(data)) != "updated" {
		t.Fatal("rollback did not restore previous configuration")
	}
	image, err := e.Prepare(ctx, target, domain.ShipRequest{Mode: "image", Image: "alpine:3"})
	if err != nil {
		t.Fatal(err)
	}
	image.ID = "image-only"
	_, existingPinErr := run(ctx, "", nil, "docker", "image", "inspect", image.Images[0].DestinationRef)
	if err = e.Execute(ctx, image, progress); err != nil {
		t.Fatal(err)
	}
	if existingPinErr != nil {
		t.Cleanup(func() { run(context.Background(), "", nil, "docker", "image", "rm", image.Images[0].DestinationRef) })
	}
	if id("web") != webID {
		t.Fatal("image-only upload changed containers")
	}
	if os.Getenv("MANAGER_TEST_SSH_PORT") != "" {
		testSSHTransfer(t, e, target, progress)
	}
}

func testSSHTransfer(t *testing.T, e *Engine, target domain.ShipTarget, progress func(domain.ShipProgress) error) {
	t.Helper()
	var port int
	json.Unmarshal([]byte(os.Getenv("MANAGER_TEST_SSH_PORT")), &port)
	target.Mode = "ssh"
	target.Host = "127.0.0.1"
	target.Port = port
	target.User = "root"
	target.Identity = "id_ed25519"
	p, err := e.Prepare(context.Background(), target, domain.ShipRequest{Mode: "image", Image: "alpine:3"})
	if err != nil {
		t.Fatal(err)
	}
	p.ID = "ssh-self-test"
	if err = e.Execute(context.Background(), p, progress); err != nil {
		t.Fatal(err)
	}
	t.Log("Real SSH image transfer verified against loopback-only disposable SSH target")
}
