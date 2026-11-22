package buildsrc

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Entry is one allowlisted local project, configured only through the runner's
// environment (an ignored Compose override), never by the browser.
type Entry struct {
	Root   string `json:"root"`
	Subdir string `json:"subdir"`
	Spec   string `json:"spec"`
}
type Registry map[string]Entry

type Source struct {
	ID       string
	Root     string
	Dir      string
	SpecPath string
}

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)

func Load(raw string) (Registry, error) {
	r := Registry{}
	if strings.TrimSpace(raw) == "" {
		return r, nil
	}
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return nil, fmt.Errorf("invalid MANAGER_BUILD_SOURCES")
	}
	for id, e := range r {
		if !idPattern.MatchString(id) || !filepath.IsAbs(e.Root) || filepath.Clean(e.Root) != e.Root || e.Root == "/" {
			return nil, fmt.Errorf("invalid build source %q", id)
		}
		if e.Subdir != "" {
			if _, err := cleanRel(e.Subdir); err != nil {
				return nil, fmt.Errorf("invalid build source subdirectory for %q", id)
			}
		}
		if e.Spec != "" {
			if _, err := cleanRel(e.Spec); err != nil {
				return nil, fmt.Errorf("invalid build source spec for %q", id)
			}
		}
	}
	return r, nil
}

func (r Registry) IDs() []string {
	ids := make([]string, 0, len(r))
	for id := range r {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Resolve returns an allowlisted source whose real path stays below its root.
func (r Registry) Resolve(id string) (Source, error) {
	e, ok := r[id]
	if !ok || !idPattern.MatchString(id) {
		return Source{}, fmt.Errorf("unknown build source")
	}
	root, err := filepath.EvalSymlinks(e.Root)
	if err != nil {
		return Source{}, fmt.Errorf("build source directory is not mounted")
	}
	dir := root
	if e.Subdir != "" {
		dir, err = filepath.EvalSymlinks(filepath.Join(root, e.Subdir))
		if err != nil || (dir != root && !strings.HasPrefix(dir, root+string(filepath.Separator))) {
			return Source{}, fmt.Errorf("build source subdirectory is unavailable")
		}
	}
	spec := e.Spec
	if spec == "" {
		spec = "ship.conf"
	}
	return Source{ID: id, Root: root, Dir: dir, SpecPath: filepath.Join(dir, filepath.FromSlash(spec))}, nil
}

// Load reads and validates the source's spec file.
func (s Source) LoadSpec() (Spec, error) {
	info, err := os.Lstat(s.SpecPath)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return Spec{}, fmt.Errorf("ship.conf is missing, symlinked or too large")
	}
	data, err := os.ReadFile(s.SpecPath)
	if err != nil {
		return Spec{}, fmt.Errorf("ship.conf is unreadable")
	}
	spec, err := ParseSpec(string(data))
	if err != nil {
		return Spec{}, fmt.Errorf("ship.conf: %w", err)
	}
	if _, err = cleanRel(spec.ComposeFile); err != nil {
		return Spec{}, err
	}
	return spec, nil
}

// ComposePath is the Compose file; it must stay inside the source directory.
func (s Source) ComposePath(spec Spec) (string, error) {
	p := filepath.Join(s.Dir, filepath.FromSlash(spec.ComposeFile))
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil || (resolved != s.Dir && !strings.HasPrefix(resolved, s.Dir+string(filepath.Separator))) {
		return "", fmt.Errorf("compose file is missing or outside the project")
	}
	return resolved, nil
}

// ContextPath resolves a build context or Dockerfile inside the source directory.
func (s Source) ContextPath(rel string) (string, error) {
	p, err := filepath.EvalSymlinks(filepath.Join(s.Dir, filepath.FromSlash(rel)))
	if err != nil || (p != s.Dir && !strings.HasPrefix(p, s.Dir+string(filepath.Separator))) {
		return "", fmt.Errorf("build path %q is missing or outside the project", rel)
	}
	return p, nil
}

func gitEnv() []string {
	env := os.Environ()
	return append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=safe.directory", "GIT_CONFIG_VALUE_0=*")
}

// GitState returns the short HEAD and whether the source subtree has uncommitted
// or untracked changes. Missing git/repository yields an empty head.
func GitState(ctx context.Context, dir string) (head string, dirty bool) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "rev-parse", "--short=10", "HEAD")
	cmd.Env = gitEnv()
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	head = strings.TrimSpace(string(out))
	status := exec.CommandContext(ctx, "git", "-C", dir, "--no-optional-locks", "status", "--porcelain", "--", ".")
	status.Env = gitEnv()
	out, err = status.Output()
	// An unreadable status is treated as dirty: never claim a clean tree we could not verify.
	return head, err != nil || len(strings.TrimSpace(string(out))) > 0
}

var tagPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func ValidTag(tag string) bool { return tagPattern.MatchString(tag) }

// MakeTag follows scripts/ship.sh: short git sha, "-dirty<epoch>" when the tree
// has uncommitted changes, or a timestamp when there is no git history.
func MakeTag(head string, dirty bool, now time.Time) string {
	if head == "" {
		return now.UTC().Format("20060102150405")
	}
	if dirty {
		return head + "-dirty" + strconv.FormatInt(now.Unix(), 10)
	}
	return head
}
