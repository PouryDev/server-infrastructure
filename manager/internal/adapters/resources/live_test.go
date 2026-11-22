//go:build resourceintegration

package resources

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

// Only uniquely named disposable containers and temporary Git repositories are
// mutated. No remote is configured; this test never invokes git push or fetch.
func TestLiveResourceResizeAndCommit(t *testing.T) {
	for _, mode := range []string{"nano", "quota", "unlimited", "json"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			dir := t.TempDir()
			project := fmt.Sprintf("manager-resources-it-%d", time.Now().UnixNano())
			name := project + "-api"
			e := &Engine{Sources: map[string]Source{project: {Root: dir, Compose: "compose.yaml"}}}
			data := "services:\n  api:\n    image: infra-manager-shipper:local\n    mem_limit: 64m\n    cpus: 0.25\n"
			if mode == "json" {
				data = `{"services":{"api":{"image":"infra-manager-shipper:local","mem_limit":67108864,"memswap_limit":134217728,"cpus":0.25,"environment":{"TITLE":"تست دیدچی"}}}}`
			}
			if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(data), 0640); err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{{"git", "init", "-b", "main"}, {"git", "add", "compose.yaml"}, {"git", "-c", "user.name=Resource Test", "-c", "user.email=test@localhost", "commit", "-m", "fixture"}} {
				if _, err := e.run(ctx, dir, args...); err != nil {
					t.Fatal(err)
				}
			}
			args := []string{"docker", "run", "-d", "--name", name, "--network", "infra", "--label", "com.docker.compose.project=" + project, "--label", "com.docker.compose.service=api", "--memory", "64m"}
			if mode == "nano" {
				args = append(args, "--cpus", "0.25")
			} else if mode == "quota" {
				args = append(args, "--cpu-period", "100000", "--cpu-quota", "25000")
			}
			args = append(args, "--entrypoint", "sh", "infra-manager-shipper:local", "-c", "sleep 300")
			if _, err := e.run(ctx, "", args...); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				cleanup, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				e.run(cleanup, "", "docker", "rm", "-f", name)
			})
			r := domain.ResourceRequest{Project: project, Service: "api", MemoryMiB: 128, CPUs: .5, GitMode: "commit"}
			p, err := e.Preview(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			r.Revision = p.Revision
			out, err := e.Apply(ctx, r)
			if err != nil || !out.Applied || len(out.Commit) != 40 || out.Pushed {
				t.Fatalf("apply=%+v err=%v", out, err)
			}
			actual, err := e.Preview(ctx, r)
			if err != nil || len(actual.Containers) != 1 || actual.Containers[0].MemoryMiB != 128 || actual.Containers[0].CPUs != .5 {
				t.Fatalf("actual=%+v err=%v", actual, err)
			}
			if !actual.GitReady {
				t.Fatal("automatic commit left dirty repository")
			}
			// Fail the next commit after actual Docker updates, then verify real rollback.
			base := &Engine{}
			e.Run = func(ctx context.Context, dir string, args ...string) ([]byte, error) {
				if args[0] == "git" && strings.Contains(strings.Join(args, " "), "commit --only") {
					return nil, fmt.Errorf("simulated commit failure")
				}
				return base.run(ctx, dir, args...)
			}
			r.MemoryMiB = 192
			r.CPUs = .75
			p, err = e.Preview(ctx, r)
			if err != nil {
				t.Fatal(err)
			}
			r.Revision = p.Revision
			if _, err = e.Apply(ctx, r); err == nil || !strings.Contains(err.Error(), "previous limits restored") {
				t.Fatal(err)
			}
			actual, err = e.Preview(ctx, r)
			if err != nil || actual.Containers[0].MemoryMiB != 128 || actual.Containers[0].CPUs != .5 || !actual.GitReady {
				t.Fatalf("rollback actual=%+v err=%v", actual, err)
			}
		})
	}
}
