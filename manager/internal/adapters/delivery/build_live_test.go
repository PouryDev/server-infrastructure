//go:build shippingintegration

package delivery

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/builder"
	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/buildsrc"
	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

const secretValue = "super-secret-value-9f3a"

type fixture struct {
	t       *testing.T
	root    string
	src     string
	app     string
	engine  *Engine
	builder *builder.Engine
	target  domain.ShipTarget
	seq     int
	log     []string
}

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := run(context.Background(), dir, nil, args...)
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}
func (f *fixture) write(rel, content string) {
	f.t.Helper()
	path := filepath.Join(f.src, rel)
	os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}
func (f *fixture) project() string { return composeProject(f.target, f.app) }
func (f *fixture) dest(rel string) string {
	return filepath.Join(f.target.AppsDir, f.app, rel)
}
func (f *fixture) container(service string) string {
	return sh(f.t, "", "docker", "ps", "-aq", "--filter", "label=com.docker.compose.project="+f.project(), "--filter", "label=com.docker.compose.service="+service)
}
func (f *fixture) inspect(service, format string) string {
	return sh(f.t, "", "docker", "inspect", f.container(service), "--format", format)
}
func (f *fixture) limits(service string) (int64, int64) {
	var mem, nano int64
	fmt.Sscan(f.inspect(service, "{{.HostConfig.Memory}} {{.HostConfig.NanoCpus}}"), &mem, &nano)
	return mem, nano
}
func (f *fixture) env(service, name string) (string, bool) {
	out, err := run(context.Background(), "", nil, "docker", "exec", f.container(service), "printenv", name)
	return strings.TrimSpace(string(out)), err == nil
}
func (f *fixture) progress(p domain.ShipProgress) error {
	f.log = append(f.log, p.Phase+": "+p.Message)
	return nil
}

// ship runs what the application job runner does: inspect, plan, build, execute.
func (f *fixture) ship(req domain.ShipRequest) (domain.ShipPlan, error) {
	f.t.Helper()
	ctx := context.Background()
	f.seq++
	inspection, err := f.builder.Inspect(ctx, "proj")
	if err != nil {
		f.t.Fatal(err)
	}
	req.Mode, req.Source, req.Tag, req.Project = "build", "proj", inspection.Tag, inspection.Source.App
	plan, err := f.engine.Prepare(ctx, f.target, req)
	if err != nil {
		return plan, err
	}
	plan.ID = fmt.Sprintf("build-%d", f.seq)
	public, _ := json.Marshal(plan)
	if strings.Contains(string(public), secretValue) {
		f.t.Fatal("env value leaked into the public plan")
	}
	plan.Build.Head, plan.Build.Dirty = inspection.Head, inspection.Dirty
	plan.Request.Env = nil
	args, err := f.engine.BuildArgs(ctx, plan)
	if err != nil {
		f.t.Fatal(err)
	}
	var services []string
	for _, image := range plan.Build.Images {
		services = append(services, image.Service)
	}
	if _, err = f.builder.Build(ctx, domain.BuildRequest{Source: "proj", Tag: plan.Build.Tag, Head: inspection.Head, Dirty: inspection.Dirty, Services: services, Args: args}, f.progress); err != nil {
		f.t.Fatal(err)
	}
	execErr := f.engine.Execute(ctx, plan, f.progress)
	if execErr != nil {
		if data, err := os.ReadFile(filepath.Join(f.dest(".manager/releases"), plan.ID, "preflight.err")); err == nil {
			f.t.Logf("preflight.err: %s", data)
		}
	}
	return plan, execErr
}
func (f *fixture) apply(service string) (domain.ShipPlan, error) {
	f.t.Helper()
	f.seq++
	plan, err := f.engine.Prepare(context.Background(), f.target, domain.ShipRequest{Mode: "apply", Project: f.app, Service: service})
	if err != nil {
		f.t.Fatal(err)
	}
	plan.ID = fmt.Sprintf("apply-%d", f.seq)
	return plan, f.engine.Execute(context.Background(), plan, f.progress)
}
func (f *fixture) envState() (domain.EnvStateRaw, string) {
	f.t.Helper()
	raw, err := f.engine.EnvState(context.Background(), f.target, f.app)
	if err != nil {
		f.t.Fatal(err)
	}
	return raw, raw.Revision
}
func set(scope, name, value string) domain.EnvChange {
	return domain.EnvChange{Scope: scope, Name: name, Action: "set", Value: value}
}

func TestBuildShipEnvAndPreservedLimits(t *testing.T) {
	ctx := context.Background()
	root, err := os.MkdirTemp("/tmp/infra-manager-target", "bs-")
	if err != nil {
		t.Fatal(err)
	}
	suffix := make([]byte, 3)
	rand.Read(suffix)
	app := "bsit" + hex.EncodeToString(suffix)
	f := &fixture{t: t, root: root, src: filepath.Join(root, "source"), app: app}
	os.MkdirAll(f.src, 0o755)
	f.target = domain.ShipTarget{Mode: "local", Port: 22, Identity: "id_ed25519", InfraDir: filepath.Join(root, "destination", "infra"), AppsDir: filepath.Join(root, "destination", "apps"), DataDir: filepath.Join(root, "destination", "data")}
	registry, err := buildsrc.Load(`{"proj":{"root":"` + f.src + `","subdir":""}}`)
	if err != nil {
		t.Fatal(err)
	}
	f.engine = &Engine{AppsRoot: filepath.Join(root, "apps"), KeysRoot: os.Getenv("MANAGER_TEST_SSH_KEYS"), Sources: registry}
	f.builder = &builder.Engine{Registry: registry}
	t.Cleanup(func() {
		ids, _ := run(context.Background(), "", nil, "docker", "ps", "-aq", "--filter", "label=com.docker.compose.project="+f.project())
		for _, id := range strings.Fields(string(ids)) {
			run(context.Background(), "", nil, "docker", "rm", "-f", id)
		}
		images, _ := run(context.Background(), "", nil, "docker", "image", "ls", app+"-web", "--format", "{{.Repository}}:{{.Tag}}")
		for _, image := range strings.Fields(string(images)) {
			run(context.Background(), "", nil, "docker", "image", "rm", "-f", image)
		}
		run(context.Background(), "", nil, "docker", "network", "rm", strings.TrimSuffix(f.project(), "-"+app)+"-default")
		os.RemoveAll(root)
	})
	f.write("ship.conf", "APP="+app+"\nCOMPOSE_FILE=deploy/compose.yaml\nIMAGES=(\"web=.\")\nBUILD_ARGS=(\"--build-arg\" \"GREETING\")\n")
	f.write("Dockerfile", "FROM alpine:3\nARG GREETING\nCOPY version.txt /version\nRUN echo \"$GREETING\" > /greeting\n")
	f.write("version.txt", "one\n")
	loop := `["sh", "-c", "trap 'exit 0' TERM INT; while :; do sleep 3600 & wait $$!; done"]`
	health := `{test: ["CMD-SHELL", "test -f /greeting && [ \"$$HEALTH_OK\" != no ]"], interval: 1s, timeout: 1s, retries: 2}`
	f.write("deploy/compose.yaml", "services:\n  web:\n    image: "+app+"-web:${TAG}\n    env_file: [.env]\n    command: "+loop+"\n    mem_limit: 128m\n    cpus: 0.5\n    healthcheck: "+health+"\n  worker:\n    image: "+app+"-web:${TAG}\n    env_file: [.env]\n    command: "+loop+"\n    deploy: {resources: {limits: {memory: 64m}}}\n    healthcheck: "+health+"\n")
	g := func(args ...string) {
		sh(t, f.src, append([]string{"git", "-c", "user.email=t@example.test", "-c", "user.name=t"}, args...)...)
	}
	g("init", "-q")
	g("add", "-A")
	g("commit", "-q", "-m", "initial")

	// 1. First deployment from a CLEAN tree: build args and env edits come from the env store.
	plan, err := f.ship(domain.ShipRequest{Env: []domain.EnvChange{set("", "GREETING", "hello"), set("", "HEALTH_OK", "yes"), set("", "API_KEY", secretValue), set("web", "WEB_ONLY", "w1")}})
	if err != nil {
		t.Fatalf("first build and ship: %v\n%s", err, strings.Join(f.log, "\n"))
	}
	if plan.Build.Dirty || strings.Contains(plan.Build.Tag, "dirty") || len(plan.EnvChanges) != 4 || plan.EnvChanges[0].Exists {
		t.Fatalf("clean tree must produce a clean sha tag: %+v %+v", plan.Build, plan.EnvChanges)
	}
	tag1 := plan.Build.Tag
	for _, line := range f.log {
		if strings.Contains(line, secretValue) || strings.Contains(line, "hello") {
			t.Fatalf("progress leaked a value: %s", line)
		}
	}
	if out, _ := run(ctx, "", nil, "docker", "exec", f.container("web"), "cat", "/greeting"); strings.TrimSpace(string(out)) != "hello" {
		t.Fatalf("build argument from the env store did not reach the build: %q", out)
	}
	if v, _ := f.env("web", "GREETING"); v != "hello" {
		t.Fatal("project env missing")
	}
	if v, ok := f.env("web", "WEB_ONLY"); !ok || v != "w1" {
		t.Fatal("service-scoped variable missing from its service")
	}
	if _, ok := f.env("worker", "WEB_ONLY"); ok {
		t.Fatal("service-scoped variable leaked to another service")
	}
	if mem, nano := f.limits("web"); mem != 128<<20 || nano != 500_000_000 {
		t.Fatal(mem, nano)
	}
	if mem, _ := f.limits("worker"); mem != 64<<20 {
		t.Fatal(mem)
	}
	if got := sh(t, "", "stat", "-c", "%a", f.dest(".env")); got != "600" {
		t.Fatalf(".env mode %s", got)
	}
	if got := sh(t, "", "cat", f.dest(".tag")); got != tag1 {
		t.Fatalf(".tag %s", got)
	}
	if len(strings.Fields(sh(t, "", "docker", "image", "ls", app+"-web", "--format", "{{.Tag}}"))) < 1 {
		t.Fatal("built image missing")
	}

	// 2. The owner changes limits by hand on the server.
	sh(t, "", "docker", "update", "--memory", "200m", "--memory-swap", "400m", "--cpus", "0.8", f.container("web"))
	sh(t, "", "docker", "update", "--memory", "96m", "--memory-swap", "192m", f.container("worker"))
	webBefore, workerBefore := f.container("web"), f.container("worker")

	// 3. A service-only build and ship of web (dirty tree) keeps the manual limits.
	f.write("version.txt", "two\n")
	plan, err = f.ship(domain.ShipRequest{Service: "web"})
	if err != nil {
		t.Fatalf("service-only ship: %v\n%s", err, strings.Join(f.log, "\n"))
	}
	if !plan.Build.Dirty || !strings.Contains(plan.Build.Tag, "-dirty") {
		t.Fatalf("uncommitted changes must be tagged dirty: %s", plan.Build.Tag)
	}
	if len(plan.PreservedLimits) != 1 || plan.PreservedLimits[0].Service != "web" || plan.PreservedLimits[0].MemoryBytes != 200<<20 || plan.PreservedLimits[0].NanoCPUs != 800_000_000 {
		t.Fatalf("preview must list the limits kept from the server: %+v", plan.PreservedLimits)
	}
	if f.container("web") == webBefore {
		t.Fatal("web was not recreated")
	}
	if mem, nano := f.limits("web"); mem != 200<<20 || nano != 800_000_000 {
		t.Fatalf("manual limits were reset by the deployment: %d %d", mem, nano)
	}
	if f.container("worker") != workerBefore {
		t.Fatal("service-only deployment recreated another service")
	}
	if mem, _ := f.limits("worker"); mem != 96<<20 {
		t.Fatalf("unselected service lost its manual limit: %d", mem)
	}
	// The destination manifest itself was never given the pinned numbers.
	if manifest := sh(t, "", "cat", f.dest("compose.yaml")); strings.Contains(manifest, "209715200") {
		t.Fatal("pinned limit leaked into compose.yaml")
	}
	if !strings.Contains(sh(t, "", "cat", f.dest("manager.override.yaml")), "209715200") || !strings.Contains(sh(t, "", "cat", f.dest("manager.override.yaml")), "env.d/web.env") {
		t.Fatal("generated override missing pins or env.d wiring")
	}

	// 4. A full deployment recreates the worker too (deploy-style limits are pinned in kind).
	f.write("version.txt", "three\n")
	if plan, err = f.ship(domain.ShipRequest{}); err != nil {
		t.Fatalf("full ship: %v\n%s", err, strings.Join(f.log, "\n"))
	}
	if mem, _ := f.limits("worker"); mem != 96<<20 {
		t.Fatalf("deploy.resources limit was reset: %d", mem)
	}
	if mem, nano := f.limits("web"); mem != 200<<20 || nano != 800_000_000 {
		t.Fatalf("%d %d", mem, nano)
	}
	if f.container("worker") == workerBefore {
		t.Fatal("worker was not recreated by the full deployment")
	}

	// 5. Opting in to the Compose limits resets them.
	f.write("version.txt", "four\n")
	if plan, err = f.ship(domain.ShipRequest{UseComposeLimits: true}); err != nil {
		t.Fatalf("compose-limits ship: %v\n%s", err, strings.Join(f.log, "\n"))
	}
	if len(plan.PreservedLimits) != 0 {
		t.Fatal("limits were listed as preserved despite the opt-out")
	}
	if mem, nano := f.limits("web"); mem != 128<<20 || nano != 500_000_000 {
		t.Fatalf("compose limits not applied: %d %d", mem, nano)
	}
	if mem, _ := f.limits("worker"); mem != 64<<20 {
		t.Fatalf("%d", mem)
	}
	tag4 := plan.Build.Tag

	// 6. Env edits on the running project: stale revisions are refused, files keep comments and mode, backups are kept.
	appended := filepath.Join(f.target.AppsDir, app, ".env")
	existing, _ := os.ReadFile(appended)
	os.WriteFile(appended, append([]byte("# keep this comment\n"), existing...), 0o600)
	raw, revision := f.envState()
	if _, err = f.engine.EnvState(ctx, f.target, "infra"); err == nil {
		t.Fatal("infrastructure env must never be reachable")
	}
	if err = f.engine.EnvWrite(ctx, f.target, app, strings.Repeat("0", 64), []domain.EnvChange{set("", "API_KEY", "x")}); err == nil || err.Error() != EnvConflictMessage {
		t.Fatalf("stale revision accepted: %v", err)
	}
	if err = f.engine.EnvWrite(ctx, f.target, app, revision, []domain.EnvChange{set("web", "WEB_ONLY", "w2"), {Name: "API_KEY", Action: "delete"}, set("", "NEW_PLAIN", "a b")}); err != nil {
		t.Fatal(err)
	}
	if err = f.engine.EnvWrite(ctx, f.target, app, revision, []domain.EnvChange{set("", "X", "1")}); err == nil {
		t.Fatal("a revision may only be used once")
	}
	if err = f.engine.EnvWrite(ctx, f.target, app, mustRevision(t, f), []domain.EnvChange{set("ghost", "X", "1")}); err == nil {
		t.Fatal("unknown service scope accepted")
	}
	envFile := sh(t, "", "cat", f.dest(".env"))
	if !strings.HasPrefix(envFile, "# keep this comment") || strings.Contains(envFile, "API_KEY") || !strings.Contains(envFile, "NEW_PLAIN='a b'") || !strings.Contains(envFile, "GREETING=hello") {
		t.Fatalf("env file not edited in place:\n%s", envFile)
	}
	if sh(t, "", "stat", "-c", "%a", f.dest(".env")) != "600" || sh(t, "", "stat", "-c", "%a", f.dest("env.d/web.env")) != "600" {
		t.Fatal("env file mode not preserved")
	}
	if got := sh(t, "", "sh", "-c", "ls "+f.dest(".env")+".bak.* | wc -l"); got != "1" {
		t.Fatalf("expected one backup of .env, got %s", got)
	}
	raw, _ = f.envState()
	if len(raw.Files) != 2 || raw.Files[1].Scope != "web" {
		t.Fatalf("%+v", raw.Files)
	}
	masked, _ := json.Marshal(domain.MaskEnv(raw))
	if strings.Contains(string(masked), "w2") || strings.Contains(string(masked), "hello") {
		t.Fatal("masked state contains values")
	}

	// 7. Apply to the running project: only the selected/affected services are recreated, limits survive.
	sh(t, "", "docker", "update", "--memory", "150m", "--memory-swap", "300m", f.container("web"))
	preview, err := f.engine.Prepare(ctx, f.target, domain.ShipRequest{Mode: "apply", Project: app, Service: "web"})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, c := range preview.EnvChanges {
		names = append(names, c.Scope+"/"+c.Name+":"+c.Action)
	}
	if strings.Join(names, ",") != "/API_KEY:delete,/NEW_PLAIN:set,web/WEB_ONLY:set" || strings.Join(preview.Affected, ",") != "web" {
		t.Fatalf("apply preview: %v affected=%v", names, preview.Affected)
	}
	if len(preview.PreservedLimits) != 1 || preview.PreservedLimits[0].MemoryBytes != 150<<20 {
		t.Fatalf("%+v", preview.PreservedLimits)
	}
	webBefore, workerBefore = f.container("web"), f.container("worker")
	if _, err = f.apply("web"); err != nil {
		t.Fatalf("apply web: %v", err)
	}
	if f.container("web") == webBefore || f.container("worker") != workerBefore {
		t.Fatal("apply of one service must recreate only that service")
	}
	if v, _ := f.env("web", "WEB_ONLY"); v != "w2" {
		t.Fatal("new value not applied")
	}
	if _, ok := f.env("web", "API_KEY"); ok {
		t.Fatal("deleted variable still present")
	}
	if mem, _ := f.limits("web"); mem != 150<<20 {
		t.Fatalf("apply reset the manual limit: %d", mem)
	}
	workerBefore = f.container("worker")
	if _, err = f.apply(""); err != nil {
		t.Fatalf("apply all: %v", err)
	}
	if f.container("worker") == workerBefore {
		t.Fatal("apply-all should recreate the worker whose environment changed")
	}
	if _, ok := f.env("worker", "API_KEY"); ok {
		t.Fatal("deleted variable still present in the worker")
	}
	if _, err = f.engine.Prepare(ctx, f.target, domain.ShipRequest{Mode: "apply", Project: "infra"}); err == nil {
		t.Fatal("infrastructure must never be an apply target")
	}
	if _, err = f.engine.Prepare(ctx, f.target, domain.ShipRequest{Mode: "apply", Project: app, Service: "ghost"}); err == nil {
		t.Fatal("unknown service accepted")
	}

	// 8. A bad value that breaks the health check: the env files are restored and the service recovers.
	_, revision = f.envState()
	if err = f.engine.EnvWrite(ctx, f.target, app, revision, []domain.EnvChange{set("web", "HEALTH_OK", "no")}); err != nil {
		t.Fatal(err)
	}
	if _, err = f.apply("web"); err == nil || !strings.Contains(err.Error(), "restored") {
		t.Fatalf("failed apply must restore the previous variables: %v", err)
	}
	if strings.Contains(sh(t, "", "cat", f.dest("env.d/web.env")), "HEALTH_OK") {
		t.Fatal("previous env file was not restored")
	}
	if got := sh(t, "", "sh", "-c", "ls "+f.dest("env.d/web.env")+".bak.* | wc -l"); got == "0" {
		t.Fatal("the failed edit was not kept as a backup")
	}
	if v, _ := f.env("web", "HEALTH_OK"); v != "yes" {
		t.Fatalf("service did not return to the previous configuration: %q", v)
	}
	if status := f.inspect("web", "{{.State.Health.Status}}"); status != "healthy" {
		t.Fatalf("web is %s after the rollback", status)
	}

	// 9. A Build & Ship whose env edit makes the new version unhealthy rolls back image tag AND env files.
	f.write("version.txt", "five\n")
	_, err = f.ship(domain.ShipRequest{Service: "web", Env: []domain.EnvChange{set("", "HEALTH_OK", "no"), set("web", "HEALTH_OK", "no")}})
	if err == nil || !strings.Contains(err.Error(), "rolled back") {
		t.Fatalf("expected rollback, got %v", err)
	}
	if strings.Contains(sh(t, "", "cat", f.dest(".env")), "HEALTH_OK=no") || strings.Contains(sh(t, "", "cat", f.dest("env.d/web.env")), "HEALTH_OK") {
		t.Fatal("env files were not restored together with the image rollback")
	}
	if got := sh(t, "", "cat", f.dest(".tag")); got != tag4 {
		t.Fatalf(".tag moved to a failed release: %s", got)
	}
	if got := f.inspect("web", "{{.Config.Image}}"); got != app+"-web:"+tag4 {
		t.Fatalf("web runs %s after rollback, want the previous tag %s", got, tag4)
	}
	if status := f.inspect("web", "{{.State.Health.Status}}"); status != "healthy" {
		t.Fatalf("web is %s after the rollback", status)
	}
	// Env edits with an unknown service scope never reach the destination.
	if _, err = f.ship(domain.ShipRequest{Env: []domain.EnvChange{set("ghost", "X", "1")}}); err == nil {
		t.Fatal("unknown scope accepted in a deployment")
	}
	_ = errors.Is
	_ = application.ErrEnvConflict
	_ = time.Second
}

func mustRevision(t *testing.T, f *fixture) string {
	_, r := f.envState()
	return r
}
