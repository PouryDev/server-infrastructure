package resources

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	"golang.org/x/sys/unix"
)

type Source struct {
	Root    string `json:"root"`
	Compose string `json:"compose"`
}
type Engine struct {
	InfraRoot, AppsRoot string
	Sources             map[string]Source
	PushEnabled         bool
	mu                  sync.Mutex
	// Tests replace the process boundary. No tests execute git push.
	Run func(context.Context, string, ...string) ([]byte, error)
}

func (e *Engine) run(ctx context.Context, dir string, args ...string) ([]byte, error) {
	if e.Run != nil {
		return e.Run(ctx, dir, args...)
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "HOME=/tmp", "GIT_SSH_COMMAND=ssh -i /keys/id_ed25519 -o IdentitiesOnly=yes -o BatchMode=yes -o StrictHostKeyChecking=yes -o UserKnownHostsFile=/keys/known_hosts")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s command failed", args[0])
	}
	if out.Len() > 8<<20 {
		return nil, fmt.Errorf("command output too large")
	}
	return out.Bytes(), nil
}
func (e *Engine) source(project string) (Source, error) {
	s, ok := e.Sources[project]
	if !ok {
		if project == "infra" {
			s = Source{e.InfraRoot, "compose.yaml"}
		} else {
			s = Source{filepath.Join(e.AppsRoot, project), "compose.yaml"}
		}
	}
	if !filepath.IsAbs(s.Root) || s.Compose == "" || filepath.IsAbs(s.Compose) || strings.HasPrefix(filepath.Clean(s.Compose), "..") {
		return s, fmt.Errorf("resource source is unavailable")
	}
	root, err := filepath.EvalSymlinks(s.Root)
	if err != nil || root != filepath.Clean(s.Root) {
		return s, fmt.Errorf("resource source is unavailable")
	}
	file := filepath.Join(s.Root, s.Compose)
	resolved, err := filepath.EvalSymlinks(file)
	if err != nil || resolved != file {
		return s, fmt.Errorf("Compose source is missing or symlinked")
	}
	stat, err := os.Stat(file)
	if err != nil || !stat.Mode().IsRegular() || stat.Size() > 2<<20 {
		return s, fmt.Errorf("invalid Compose source")
	}
	return s, nil
}

type inspected struct {
	ID              string `json:"Id"`
	Config          struct{ Labels map[string]string }
	HostConfig      struct{ Memory, MemorySwap, NanoCpus, CpuQuota, CpuPeriod int64 }
	NetworkSettings struct{ Networks map[string]json.RawMessage }
}

func (e *Engine) containers(ctx context.Context, r domain.ResourceRequest) ([]inspected, error) {
	data, err := e.run(ctx, "", "docker", "ps", "-a", "--no-trunc", "--filter", "label=com.docker.compose.project="+r.Project, "--filter", "label=com.docker.compose.service="+r.Service, "--format", "{{.ID}}")
	if err != nil {
		return nil, fmt.Errorf("container discovery failed")
	}
	ids := strings.Fields(string(data))
	sort.Strings(ids)
	if len(ids) == 0 || len(ids) > 100 {
		return nil, fmt.Errorf("no eligible service containers")
	}
	var all []inspected
	for _, id := range ids {
		if len(id) != 64 || strings.Trim(id, "0123456789abcdef") != "" {
			return nil, fmt.Errorf("invalid container identity")
		}
		data, err = e.run(ctx, "", "docker", "inspect", id)
		if err != nil {
			return nil, fmt.Errorf("container inspection failed")
		}
		var items []inspected
		if json.Unmarshal(data, &items) != nil || len(items) != 1 {
			return nil, fmt.Errorf("container inspection failed")
		}
		c := items[0]
		labels := c.Config.Labels
		_, network := c.NetworkSettings.Networks["infra"]
		if c.ID != id || labels["com.docker.compose.project"] != r.Project || labels["com.docker.compose.service"] != r.Service || strings.EqualFold(labels["com.docker.compose.oneoff"], "true") || (r.Project != "infra" && !network) {
			return nil, fmt.Errorf("container is outside resource scope")
		}
		for _, part := range strings.FieldsFunc(strings.ToLower(r.Service), func(r rune) bool { return r == '-' || r == '_' }) {
			if part == "migrate" || part == "migration" || part == "seed" || part == "setup" || part == "init" {
				return nil, fmt.Errorf("one-shot services cannot be resized")
			}
		}
		all = append(all, c)
	}
	return all, nil
}
func (e *Engine) git(ctx context.Context, s Source, args ...string) ([]byte, error) {
	base := []string{"git", "-c", "safe.directory=" + s.Root, "-c", "core.hooksPath=/dev/null", "-c", "commit.gpgsign=false"}
	return e.run(ctx, s.Root, append(base, args...)...)
}
func (e *Engine) gitState(ctx context.Context, s Source) (head, branch string, ready bool) {
	root, err := e.git(ctx, s, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(string(root)) != s.Root {
		return
	}
	h, err := e.git(ctx, s, "rev-parse", "HEAD")
	if err != nil {
		return
	}
	head = strings.TrimSpace(string(h))
	b, err := e.git(ctx, s, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return
	}
	branch = strings.TrimSpace(string(b))
	status, err := e.git(ctx, s, "status", "--porcelain", "--untracked-files=normal")
	if err != nil || len(bytes.TrimSpace(status)) != 0 {
		return
	}
	_, err = e.git(ctx, s, "ls-files", "--error-unmatch", "--", s.Compose)
	ready = err == nil
	return
}
func (e *Engine) preview(ctx context.Context, r domain.ResourceRequest) (domain.ResourcePreview, Source, []byte, []byte, []inspected, error) {
	p := domain.ResourcePreview{}
	if err := r.Validate(); err != nil {
		return p, Source{}, nil, nil, nil, err
	}
	s, err := e.source(r.Project)
	if err != nil {
		return p, s, nil, nil, nil, err
	}
	data, err := os.ReadFile(filepath.Join(s.Root, s.Compose))
	if err != nil {
		return p, s, nil, nil, nil, err
	}
	next, changes, err := patchCompose(data, r)
	if err != nil {
		return p, s, nil, nil, nil, err
	}
	containers, err := e.containers(ctx, r)
	if err != nil {
		return p, s, nil, nil, nil, err
	}
	head, branch, ready := e.gitState(ctx, s)
	p = domain.ResourcePreview{Project: r.Project, Service: r.Service, File: s.Compose, Branch: branch, GitReady: ready, PushEnabled: e.PushEnabled, Containers: []domain.ResourceContainer{}, Changes: changes}
	for _, c := range containers {
		cpu := float64(c.HostConfig.NanoCpus) / 1e9
		if cpu == 0 && c.HostConfig.CpuQuota > 0 && c.HostConfig.CpuPeriod > 0 {
			cpu = float64(c.HostConfig.CpuQuota) / float64(c.HostConfig.CpuPeriod)
		}
		p.Containers = append(p.Containers, domain.ResourceContainer{ID: c.ID, MemoryMiB: c.HostConfig.Memory / (1 << 20), CPUs: cpu})
	}
	payload, _ := json.Marshal(struct {
		Data, Next []byte
		Head       string
		Containers []inspected
		Mode       string
		Push       bool
	}{data, next, head, containers, r.GitMode, e.PushEnabled})
	hash := sha256.Sum256(payload)
	p.Revision = hex.EncodeToString(hash[:])
	return p, s, data, next, containers, nil
}
func (e *Engine) Preview(ctx context.Context, r domain.ResourceRequest) (domain.ResourcePreview, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, _, _, _, _, err := e.preview(ctx, r)
	return p, err
}
func atomicWrite(path string, data []byte) error {
	stat, err := os.Stat(path)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".manager-limits-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if owner, ok := stat.Sys().(*syscall.Stat_t); ok {
		if err = f.Chown(int(owner.Uid), int(owner.Gid)); err != nil {
			f.Close()
			return err
		}
	}
	if err = f.Chmod(stat.Mode().Perm()); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
func (e *Engine) Apply(ctx context.Context, r domain.ResourceRequest) (domain.ResourceResult, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := domain.ResourceResult{}
	// Cross-process lock lives in /tmp, never adds a file to the user's repository.
	lockName := sha256.Sum256([]byte(e.InfraRoot + e.AppsRoot))
	lock, err := os.OpenFile(filepath.Join(os.TempDir(), fmt.Sprintf("manager-resources-%x.lock", lockName[:8])), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return out, fmt.Errorf("resource lock unavailable")
	}
	defer lock.Close()
	if unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB) != nil {
		return out, fmt.Errorf("another resource change is active")
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	p, s, previous, next, containers, err := e.preview(ctx, r)
	if err != nil {
		return out, err
	}
	if r.Revision == "" || r.Revision != p.Revision {
		return out, fmt.Errorf("resource preview is stale; preview again")
	}
	if r.GitMode != "local" && !p.GitReady {
		return out, fmt.Errorf("Git requires a clean repository and tracked Compose file")
	}
	if r.GitMode == "push" && !e.PushEnabled {
		return out, fmt.Errorf("Git push is disabled on this server")
	}
	if r.GitMode == "push" {
		// Never publish unrelated local commits, and never force push.
		upstream, er := e.git(ctx, s, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
		if er != nil || strings.TrimSpace(string(upstream)) != "origin/"+p.Branch {
			return out, fmt.Errorf("configure an origin upstream before pushing")
		}
		if _, er = e.git(ctx, s, "fetch", "origin"); er != nil {
			return out, fmt.Errorf("Git fetch failed; nothing changed")
		}
		counts, er := e.git(ctx, s, "rev-list", "--left-right", "--count", "HEAD...@{upstream}")
		if er != nil || strings.Join(strings.Fields(string(counts)), " ") != "0 0" {
			return out, fmt.Errorf("Git branch must match origin before pushing")
		}
	}
	// Refresh after any network wait. Never replace an intervening source edit.
	fresh, _, _, _, _, checkErr := e.preview(ctx, r)
	if checkErr != nil {
		return out, checkErr
	}
	if fresh.Revision != p.Revision {
		return out, fmt.Errorf("resource preview is stale; preview again")
	}
	originalHead, _, _ := e.gitState(ctx, s)
	file := filepath.Join(s.Root, s.Compose)
	if err = atomicWrite(file, next); err != nil {
		return out, fmt.Errorf("Compose source is not writable")
	}
	changed := []inspected{}
	rollback := func(cause string) (domain.ResourceResult, error) {
		recovery, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		now, readErr := os.ReadFile(file)
		failed := readErr != nil || !bytes.Equal(now, next)
		if !failed {
			failed = atomicWrite(file, previous) != nil
		}
		for _, c := range changed {
			h := c.HostConfig
			args := []string{"docker", "update", "--memory", strconv.FormatInt(h.Memory, 10), "--memory-swap", strconv.FormatInt(h.MemorySwap, 10)}
			if h.NanoCpus > 0 {
				args = append(args, "--cpus", strconv.FormatFloat(float64(h.NanoCpus)/1e9, 'f', 9, 64))
			} else {
				args = append(args, "--cpus", "0", "--cpu-period", strconv.FormatInt(h.CpuPeriod, 10), "--cpu-quota", strconv.FormatInt(h.CpuQuota, 10))
			}
			if _, er := e.run(recovery, "", append(args, c.ID)...); er != nil {
				failed = true
			}
		}
		if failed {
			return out, fmt.Errorf("%s; rollback incomplete, inspect Compose and containers", cause)
		}
		return out, fmt.Errorf("%s; previous limits restored", cause)
	}
	args := []string{"docker", "compose", "-f", file}
	seen := map[string]bool{}
	for _, dir := range []string{s.Root, filepath.Dir(file)} {
		for _, name := range []string{".env", ".tag.next"} {
			envFile := filepath.Join(dir, name)
			if _, er := os.Stat(envFile); er == nil && !seen[envFile] {
				args = append(args, "--env-file", envFile)
				seen[envFile] = true
			}
		}
	}
	if _, err = e.run(ctx, s.Root, append(args, "config", "--quiet")...); err != nil {
		return rollback("Compose validation failed")
	}
	for _, c := range containers {
		// Include the failed call in rollback: Docker may have accepted a timed-out request.
		changed = append(changed, c)
		swap := c.HostConfig.MemorySwap
		memory := r.MemoryMiB * (1 << 20)
		if swap >= 0 && swap < memory {
			swap = memory
		}
		// Preserve CPU CFS quota mode if already used; NanoCPUs and CpuQuota conflict.
		update := []string{"docker", "update", "--memory", strconv.FormatInt(memory, 10), "--memory-swap", strconv.FormatInt(swap, 10)}
		if c.HostConfig.CpuQuota > 0 {
			period := c.HostConfig.CpuPeriod
			if period <= 0 {
				period = 100000
			}
			update = append(update, "--cpu-period", strconv.FormatInt(period, 10), "--cpu-quota", strconv.FormatInt(int64(r.CPUs*float64(period)), 10))
		} else {
			update = append(update, "--cpus", fmt.Sprintf("%.2f", r.CPUs))
		}
		if _, err = e.run(ctx, "", append(update, c.ID)...); err != nil {
			return rollback("Container update failed")
		}
	}
	if r.GitMode != "local" && !bytes.Equal(previous, next) {
		now, readErr := os.ReadFile(file)
		if readErr != nil || !bytes.Equal(now, next) {
			return rollback("Compose source changed during apply")
		}
		head, _, _ := e.gitState(ctx, s)
		if head != originalHead {
			return rollback("Git HEAD changed during apply")
		}

		// --only protects the index even if another tool stages an unrelated file.
		if _, err = e.git(ctx, s, "-c", "user.name=Infrastructure Manager", "-c", "user.email=manager@localhost", "commit", "--only", "-m", fmt.Sprintf("chore(resources): resize %s/%s to %dMiB %.2f CPU", r.Project, r.Service, r.MemoryMiB, r.CPUs), "--", s.Compose); err != nil {
			return rollback("Git commit failed")
		}
		hash, er := e.git(ctx, s, "rev-parse", "HEAD")
		if er != nil {
			out.Warning = "Limits applied and committed; commit ID unavailable"
		} else {
			out.Commit = strings.TrimSpace(string(hash))
		}
	}
	out.Applied = true
	if r.GitMode == "push" && out.Commit != "" {
		// Explicit refspec and no force: only the reviewed branch is published.
		if _, err = e.git(ctx, s, "push", "origin", out.Commit+":refs/heads/"+p.Branch); err != nil {
			out.Warning = "Limits applied and committed locally; Git push failed. Retry pushing this commit manually."
		} else {
			out.Pushed = true
		}
	}
	return out, nil
}
