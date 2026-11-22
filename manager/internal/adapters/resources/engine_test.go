package resources

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

const fixtureCompose = "name: shop\nservices:\n  shop-api:\n    image: shop-api:test\n    mem_limit: 256m # preserve me\n    cpus: 0.5\n  shop-worker:\n    image: worker:test\n    mem_limit: 64m\n    cpus: 0.1\n"

func resizeRequest() domain.ResourceRequest {
	return domain.ResourceRequest{Project: "shop", Service: "shop-api", MemoryMiB: 512, CPUs: 1, GitMode: "local"}
}
func TestPatchPreservesUnrelatedBytesAndHandlesInheritedLimits(t *testing.T) {
	r := resizeRequest()
	got, _, err := patchCompose([]byte(fixtureCompose), r)
	want := strings.Replace(strings.Replace(fixtureCompose, "256m # preserve me", "512m # preserve me", 1), "cpus: 0.5", "cpus: 1.00", 1)
	if err != nil || string(got) != want {
		t.Fatalf("patch: %s %v", got, err)
	}
	inherited := "x-base: &base\n  mem_limit: 128m\n  cpus: 0.1\nservices:\n  shop-api:\n    <<: *base\n    image: test\n"
	got, _, err = patchCompose([]byte(inherited), r)
	if err != nil || !strings.Contains(string(got), "    mem_limit: 512m") || !strings.Contains(string(got), "    cpus: 1.00") || !strings.HasPrefix(string(got), "x-base: &base\n  mem_limit: 128m\n  cpus: 0.1\n") {
		t.Fatalf("inheritance: %s %v", got, err)
	}
	nested := strings.Replace(fixtureCompose, "    cpus: 0.5", "    cpus: 0.5\n    deploy:\n      resources:\n        limits:\n          memory: 256m\n          cpus: '0.5'", 1)
	got, _, err = patchCompose([]byte(nested), r)
	if err != nil || !strings.Contains(string(got), "          memory: 512m\n          cpus: 1.00") {
		t.Fatalf("deploy consistency: %s %v", got, err)
	}
}
func TestPatchRejectsDangerousOrUnsupportedShapes(t *testing.T) {
	for _, data := range []string{"services:\n  shop-api: &shared\n    image: test\n    mem_limit: 128m\n  worker:\n    <<: *shared\n", "services: {shop-api: {mem_limit: 256m, image: test}}", "services:\n  shop-api:\n    image: test\n    mem_limit: |\n      256m\n", "services:\n  shop-api:\n    image: test\n    mem_limit: 128m\n    mem_limit: 256m\n", "services:\n  other:\n    image: test\n"} {
		if _, _, err := patchCompose([]byte(data), resizeRequest()); err == nil {
			t.Fatalf("accepted unsupported source %q", data)
		}
	}
}

type fixture struct {
	engine                                                  *Engine
	file                                                    string
	calls                                                   []string
	dirty, commitFail, updateFail, pushFail, validationFail bool
	head                                                    string
	runtime                                                 []inspected
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "compose.yaml")
	if err := os.WriteFile(file, []byte(fixtureCompose), 0640); err != nil {
		t.Fatal(err)
	}
	f := &fixture{file: file, head: strings.Repeat("a", 40)}
	for _, letter := range []string{"b", "c"} {
		var c inspected
		c.ID = strings.Repeat(letter, 64)
		c.Config.Labels = map[string]string{"com.docker.compose.project": "shop", "com.docker.compose.service": "shop-api"}
		c.NetworkSettings.Networks = map[string]json.RawMessage{"infra": json.RawMessage(`{}`)}
		c.HostConfig.Memory = 256 << 20
		c.HostConfig.MemorySwap = 512 << 20
		c.HostConfig.NanoCpus = 500000000
		f.runtime = append(f.runtime, c)
	}
	f.engine = &Engine{InfraRoot: dir, AppsRoot: dir, Sources: map[string]Source{"shop": {dir, "compose.yaml"}}}
	f.engine.Run = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
		call := strings.Join(args, " ")
		f.calls = append(f.calls, call)
		if args[0] == "git" {
			switch {
			case strings.Contains(call, "rev-parse --show-toplevel"):
				return []byte(dir), nil
			case strings.Contains(call, "rev-parse --abbrev-ref --symbolic-full-name"):
				return []byte("origin/main"), nil
			case strings.Contains(call, "rev-parse HEAD"):
				return []byte(f.head), nil
			case strings.Contains(call, "symbolic-ref --short HEAD"):
				return []byte("main"), nil
			case strings.Contains(call, "status --porcelain"):
				if f.dirty {
					return []byte(" M other.txt"), nil
				}
				return nil, nil
			case strings.Contains(call, "ls-files"):
				return []byte("compose.yaml"), nil
			case strings.Contains(call, "commit --only"):
				if f.commitFail {
					return nil, errors.New("commit failed")
				}
				f.head = strings.Repeat("d", 40)
				return nil, nil
			case strings.Contains(call, "fetch origin"):
				return nil, nil
			case strings.Contains(call, "rev-list"):
				return []byte("0\t0\n"), nil
			case strings.Contains(call, "push origin"):
				if f.pushFail {
					return nil, errors.New("push failed")
				}
				return nil, nil
			}
		}
		if len(args) > 1 && args[0] == "docker" {
			switch args[1] {
			case "ps":
				return []byte(f.runtime[0].ID + "\n" + f.runtime[1].ID), nil
			case "inspect":
				for _, c := range f.runtime {
					if c.ID == args[2] {
						return json.Marshal([]inspected{c})
					}
				}
			case "compose":
				if f.validationFail {
					return nil, errors.New("invalid")
				}
				return nil, nil
			case "update":
				if f.updateFail && strings.Contains(call, "--memory 536870912") && args[len(args)-1] == f.runtime[1].ID {
					return nil, errors.New("update failed")
				}
				return nil, nil
			}
		}
		t.Errorf("unexpected command %s", call)
		return nil, errors.New("unexpected")
	}
	return f
}
func previewRequest(t *testing.T, f *fixture, r domain.ResourceRequest) domain.ResourceRequest {
	t.Helper()
	p, err := f.engine.Preview(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}
	r.Revision = p.Revision
	return r
}
func TestLocalResizeUpdatesEveryReplicaWithoutGitMutation(t *testing.T) {
	f := newFixture(t)
	r := previewRequest(t, f, resizeRequest())
	out, err := f.engine.Apply(context.Background(), r)
	if err != nil || !out.Applied || out.Commit != "" || out.Pushed {
		t.Fatalf("apply: %+v %v", out, err)
	}
	updates := 0
	for _, call := range f.calls {
		if strings.Contains(call, "docker update") {
			updates++
		}
		if strings.Contains(call, "commit --only") || strings.Contains(call, "push origin") || strings.Contains(call, "fetch origin") {
			t.Fatal("local operation touched Git")
		}
	}
	if updates != 2 {
		t.Fatalf("updated %d replicas", updates)
	}
	data, _ := os.ReadFile(f.file)
	if !strings.Contains(string(data), "mem_limit: 512m") {
		t.Fatal(string(data))
	}
	stat, _ := os.Stat(f.file)
	if stat.Mode().Perm() != 0640 {
		t.Fatal("file permissions changed")
	}
}
func TestResizeRejectsStalePreviewDirtyGitAndDisabledPush(t *testing.T) {
	for _, scenario := range []string{"file", "runtime", "head", "dirty", "push"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			r := resizeRequest()
			if scenario == "dirty" {
				r.GitMode = "commit"
			}
			if scenario == "push" {
				r.GitMode = "push"
			}
			r = previewRequest(t, f, r)
			switch scenario {
			case "file":
				os.WriteFile(f.file, []byte(fixtureCompose+"# edit\n"), 0640)
			case "runtime":
				f.runtime[0].HostConfig.Memory = 1024 << 20
			case "head":
				f.head = "changed"
			case "dirty":
				f.dirty = true
			}
			if _, err := f.engine.Apply(context.Background(), r); err == nil {
				t.Fatal("unsafe request accepted")
			}
			for _, call := range f.calls {
				if strings.Contains(call, "docker update") || strings.Contains(call, "commit --only") || strings.Contains(call, "push origin") {
					t.Fatal("mutation before validation")
				}
			}
		})
	}
}
func TestFailuresRestoreComposeAndRuntime(t *testing.T) {
	for _, scenario := range []string{"update", "commit", "validation"} {
		t.Run(scenario, func(t *testing.T) {
			f := newFixture(t)
			r := resizeRequest()
			r.GitMode = "commit"
			r = previewRequest(t, f, r)
			f.updateFail = scenario == "update"
			f.commitFail = scenario == "commit"
			f.validationFail = scenario == "validation"
			_, err := f.engine.Apply(context.Background(), r)
			if err == nil || !strings.Contains(err.Error(), "previous limits restored") {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(f.file)
			if string(data) != fixtureCompose {
				t.Fatal("Compose not restored")
			}
			restores := 0
			for _, call := range f.calls {
				if strings.Contains(call, "docker update --memory 268435456") {
					restores++
				}
			}
			if scenario != "validation" && restores != 2 {
				t.Fatalf("not all attempted replicas restored: %d", restores)
			}
		})
	}
}
func TestAutomaticCommitAndSimulatedPushFailureKeepResultHonest(t *testing.T) {
	f := newFixture(t)
	f.engine.PushEnabled = true
	f.pushFail = true
	r := resizeRequest()
	r.GitMode = "push"
	r = previewRequest(t, f, r)
	out, err := f.engine.Apply(context.Background(), r)
	if err != nil || !out.Applied || out.Commit == "" || out.Pushed || out.Warning == "" {
		t.Fatalf("partial success lost: %+v %v", out, err)
	}
	for _, call := range f.calls {
		if strings.Contains(call, "commit --only") && !strings.HasSuffix(call, "-- compose.yaml") {
			t.Fatal("commit scope escaped")
		}
	}
}
func TestResourceScopeRejectsOneOffAndForeignContainers(t *testing.T) {
	for _, kind := range []string{"oneoff", "foreign", "network", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			switch kind {
			case "oneoff":
				f.runtime[0].Config.Labels["com.docker.compose.oneoff"] = "true"
			case "foreign":
				f.runtime[0].Config.Labels["com.docker.compose.project"] = "other"
			case "network":
				f.runtime[0].NetworkSettings.Networks = nil
			case "symlink":
				os.Remove(f.file)
				os.Symlink("/etc/passwd", f.file)
			}
			if _, err := f.engine.Preview(context.Background(), resizeRequest()); err == nil {
				t.Fatal("scope bypass")
			}
		})
	}
}
