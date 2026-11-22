//go:build nativedeployintegration

package delivery

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/builder"
	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/buildsrc"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

// Explicitly opt in to a real SSH destination. The only remote application this
// test creates is manager-native-check; it has no shared DB/broker credentials.
func TestAutomaticBuildSSHDeployAndDestinationLimits(t *testing.T) {
	raw := os.Getenv("MANAGER_NATIVE_TEST_TARGET")
	if raw == "" {
		t.Skip("explicit SSH integration target is required")
	}
	var target domain.ShipTarget
	if err := json.Unmarshal([]byte(raw), &target); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	name := "manager-native-check"
	source := t.TempDir()
	registry := buildsrc.Registry{"fixture": {Root: source}}
	engine := &Engine{Sources: registry, KeysRoot: os.Getenv("MANAGER_NATIVE_TEST_KEYS")}
	build := &builder.Engine{Registry: registry}
	remote := func(args ...string) string {
		t.Helper()
		data, err := engine.remote(ctx, target, nil, args...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(data))
	}
	dir := filepath.Join(target.AppsDir, name)
	remote("sh", "-c", `test ! -e "$1" && mkdir -p "$1" && printf '# isolated manager test\n' > "$1/.env"`, "--", dir)
	defer func() {
		// Every path/name is fixed to the fixture created above.
		engine.remote(context.Background(), target, nil, "sh", "-c", `docker compose -p manager-native-check -f "$1/compose.yaml" down --remove-orphans >/dev/null 2>&1 || true; rm -f "$2/config/nginx/conf.d/manager-native-check.conf"; docker compose --project-directory "$2" -f "$2/compose.yaml" exec -T nginx nginx -s reload >/dev/null 2>&1; rm -rf "$1"; docker image ls --filter 'reference=manager-native-check-api:*' --format '{{.Repository}}:{{.Tag}}' | xargs -r docker image rm >/dev/null 2>&1 || true`, "--", dir, target.InfraDir)
	}()
	write := func(file, data string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(source, file), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("ship.conf", "APP="+name+"\nCOMPOSE_FILE=compose.yaml\nIMAGES=(\"api=.:Dockerfile\")\n")
	write("Dockerfile", `FROM docker.arvancloud.ir/nginx:1.30.6-alpine
ENTRYPOINT ["/bin/sh", "-ec"]
CMD ["mkdir -p /tmp/www; env > /tmp/www/index.html; printf 'pid /tmp/nginx.pid; events {} http { access_log off; error_log /dev/stderr; client_body_temp_path /tmp/client; proxy_temp_path /tmp/proxy; fastcgi_temp_path /tmp/fastcgi; uwsgi_temp_path /tmp/uwsgi; scgi_temp_path /tmp/scgi; server { listen 8080; root /tmp/www; location /healthz { return 200 ok; } } }' > /tmp/nginx.conf; exec nginx -c /tmp/nginx.conf -g 'daemon off;'" ]
`)
	common := map[string]any{"image": name + "-api:${TAG}", "env_file": []string{".env", ".runtime.env"}, "environment": map[string]string{"SMOKE_VALUE": "default"}, "user": "65532:65532", "read_only": true, "tmpfs": []string{"/tmp"}, "cap_drop": []string{"ALL"}, "security_opt": []string{"no-new-privileges:true"}, "networks": []string{"infra"}, "mem_limit": "64m", "cpus": 0.25, "restart": "no", "healthcheck": map[string]any{"test": []string{"CMD", "/usr/bin/wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/healthz"}, "interval": "1s", "timeout": "1s", "retries": 2}}
	model := map[string]any{"name": name, "services": map[string]any{name + "-api": common}, "networks": map[string]any{"infra": map[string]any{"external": true}}}
	encoded, _ := json.Marshal(model)
	write("compose.yaml", string(encoded))
	ship := func(domainName, value string) {
		t.Helper()
		state, err := engine.NativeState(ctx, target, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		revision := ""
		for _, p := range state.Catalog.Projects {
			if p.Name == name {
				revision = p.Revision
			}
		}
		inspection, err := build.Inspect(ctx, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		req := domain.ShipRequest{Mode: "build", Project: name, Source: "fixture", Tag: inspection.Tag,
			Deployment: &domain.DeploymentRoute{Domain: domainName, Service: name + "-api", Port: 8080, Revision: revision},
			Env:        []domain.EnvChange{{Name: "SMOKE_VALUE", Action: "set", Value: value}}}
		plan, err := engine.Prepare(ctx, target, req)
		if err != nil {
			t.Fatal(err)
		}
		plan.ID = inspection.Tag
		public, _ := json.Marshal(plan)
		if strings.Contains(string(public), value) {
			t.Fatal("env value in public preview")
		}
		var vertices, transferred int64
		if _, err = build.Build(ctx, domain.BuildRequest{Source: "fixture", Tag: inspection.Tag, Head: inspection.Head, Dirty: inspection.Dirty, Services: []string{"api"}}, func(p domain.ShipProgress) error {
			if p.Progress != nil && p.Progress.Unit == "vertices" {
				vertices = p.Progress.Current
			}
			t.Log(p.Phase)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if err = engine.Execute(ctx, plan, func(p domain.ShipProgress) error {
			if p.Progress != nil && p.Progress.Unit == "bytes" {
				transferred = p.Progress.Current
			}
			t.Log(p.Phase)
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		if vertices == 0 || transferred == 0 {
			t.Fatalf("missing real build/transfer progress: vertices=%d bytes=%d", vertices, transferred)
		}
		t.Logf("Verified live progress: %d completed vertices, %d bytes streamed", vertices, transferred)
		body := remote("docker", "exec", "infra-nginx-1", "wget", "-qO-", "--header", "Host: "+domainName, "http://127.0.0.1/")
		if !strings.Contains(body, "SMOKE_VALUE="+value) {
			t.Fatal("destination env not applied literally")
		}
	}
	ship("manager-native-check.localhost", "native$first")
	id := remote("docker", "ps", "-q", "--filter", "label=com.docker.compose.project="+name)
	remote("docker", "update", "--memory", "96m", "--memory-swap", "192m", "--cpus", ".6", "--pids-limit", "64", id)
	// Use a new immutable tag, and change the saved domain/env in the same flow.
	time.Sleep(time.Second)
	ship("manager-native-check-new.localhost", "native$second")
	id = remote("docker", "ps", "-q", "--filter", "label=com.docker.compose.project="+name)
	limits := remote("docker", "inspect", id, "--format", "{{.HostConfig.Memory}} {{.HostConfig.NanoCpus}} {{.HostConfig.PidsLimit}}")
	if limits != "100663296 600000000 64" {
		t.Fatalf("destination limits reset: %s", limits)
	}
}

func TestActualApplicationScratchImageTransfer(t *testing.T) {
	raw := os.Getenv("MANAGER_NATIVE_TEST_TARGET")
	if raw == "" {
		t.Skip("explicit SSH target required")
	}
	var target domain.ShipTarget
	if err := json.Unmarshal([]byte(raw), &target); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	engine := &Engine{KeysRoot: os.Getenv("MANAGER_NATIVE_TEST_KEYS")}
	ref := os.Getenv("MANAGER_NATIVE_TEST_IMAGE")
	if ref == "" {
		t.Skip("optional real application image was not selected")
	}
	plan, err := engine.Prepare(ctx, target, domain.ShipRequest{Mode: "image", Image: ref})
	if err != nil {
		t.Fatal(err)
	}
	plan.ID = "application-scratch-image-verification"
	// Also verify an existing equivalent tag across the classic/containerd stores.
	if _, err = engine.remote(ctx, target, nil, "docker", "image", "tag", ref, plan.Images[0].DestinationRef); err != nil {
		t.Fatal(err)
	}
	if err = engine.Execute(ctx, plan, func(p domain.ShipProgress) error { t.Log(p.Phase); return nil }); err != nil {
		t.Fatal(err)
	}
}
