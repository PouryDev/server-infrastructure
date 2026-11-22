package delivery

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	"gopkg.in/yaml.v3"
)

// EnvConflictMessage is also matched by the protocol client to restore ErrConflict.
var EnvConflictMessage = application.ErrEnvConflict.Error()

var (
	envEntry      = regexp.MustCompile(`^env\.d/([A-Za-z0-9][A-Za-z0-9_.-]{0,99})\.env$`)
	appliedEntry  = regexp.MustCompile(`^\.manager/applied/(\.env|env\.d/[A-Za-z0-9][A-Za-z0-9_.-]{0,99}\.env)$`)
	liveLinesOnly = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
)

// destState is the destination's env store as read through the runner.
type destState struct {
	dir      string
	compose  []byte
	override []byte
	files    map[string][]byte // scope -> content ("" is the project .env)
	applied  map[string][]byte // same keys, last successfully applied copy
	hasApply bool
	services map[string]map[string]any
}

func scopeOfRel(rel string) (string, bool) {
	if rel == ".env" {
		return "", true
	}
	if m := envEntry.FindStringSubmatch(rel); m != nil {
		return m[1], true
	}
	return "", false
}
func relOfScope(scope string) string {
	if scope == "" {
		return ".env"
	}
	return "env.d/" + scope + ".env"
}
func sha(data []byte) string { s := sha256.Sum256(data); return hex.EncodeToString(s[:]) }

func (e *Engine) projectDir(t domain.ShipTarget, project string) string {
	return filepath.Join(t.AppsDir, project)
}

// composeProject mirrors the sandbox naming used by deployments.
func composeProject(t domain.ShipTarget, project string) string {
	if t.Mode == "local" {
		hash := sha256.Sum256([]byte(t.InfraDir))
		return "manager-test-" + hex.EncodeToString(hash[:4]) + "-" + project
	}
	return project
}

func readTar(data []byte) (map[string][]byte, error) {
	out := map[string][]byte{}
	reader := tar.NewReader(bytes.NewReader(data))
	total := 0
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return nil, fmt.Errorf("destination env data is unreadable")
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		name := strings.TrimPrefix(path.Clean(header.Name), "./")
		if header.Size > 1<<20 {
			return nil, fmt.Errorf("destination file is too large")
		}
		content, err := io.ReadAll(io.LimitReader(reader, 1<<20))
		if err != nil {
			return nil, fmt.Errorf("destination env data is unreadable")
		}
		if total += len(content); total > maxBundle {
			return nil, fmt.Errorf("destination env data is too large")
		}
		out[name] = content
	}
}

func (e *Engine) readState(ctx context.Context, t domain.ShipTarget, dir string) (*destState, error) {
	data, err := e.remote(ctx, t, nil, "sh", "-c", readScript, "--", dir)
	if err != nil {
		return nil, err
	}
	entries, err := readTar(data)
	if err != nil {
		return nil, err
	}
	s := &destState{dir: dir, files: map[string][]byte{}, applied: map[string][]byte{}, services: map[string]map[string]any{}}
	for name, content := range entries {
		switch {
		case name == "compose.yaml":
			s.compose = content
		case name == overrideFile:
			s.override = content
		case appliedEntry.MatchString(name):
			s.hasApply = true
			if scope, ok := scopeOfRel(strings.TrimPrefix(name, ".manager/applied/")); ok {
				s.applied[scope] = content
			}
		default:
			if scope, ok := scopeOfRel(name); ok {
				s.files[scope] = content
			}
		}
	}
	if len(s.compose) > 0 {
		var doc map[string]any
		if err = yaml.Unmarshal(s.compose, &doc); err != nil {
			return nil, fmt.Errorf("destination compose file could not be read")
		}
		for name, value := range object(doc["services"]) {
			s.services[name] = object(value)
		}
	}
	return s, nil
}

func (s *destState) revision() string {
	scopes := make([]string, 0, len(s.files))
	for scope := range s.files {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	var b strings.Builder
	for _, scope := range scopes {
		b.WriteString(relOfScope(scope) + "\x00" + sha(s.files[scope]) + "\n")
	}
	b.WriteString(overrideFile + "\x00" + sha(s.override))
	return sha([]byte(b.String()))
}

func usesProjectEnv(dir string, svc map[string]any) bool {
	check := func(p string) bool {
		return filepath.Base(p) == ".env" && (!filepath.IsAbs(p) || filepath.Clean(p) == filepath.Join(dir, ".env"))
	}
	switch v := svc["env_file"].(type) {
	case string:
		return check(v)
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && check(s) {
				return true
			}
			if m, ok := item.(map[string]any); ok && check(text(m["path"])) {
				return true
			}
		}
	}
	return false
}
func serviceLabels(svc map[string]any) map[string]string {
	labels := map[string]string{}
	switch v := svc["labels"].(type) {
	case map[string]any:
		for k, value := range v {
			labels[k] = fmt.Sprint(value)
		}
	case []any:
		for _, item := range v {
			if k, val, ok := strings.Cut(text(item), "="); ok {
				labels[k] = val
			}
		}
	}
	return labels
}
func (s *destState) serviceInfo() []domain.EnvServiceInfo {
	out := []domain.EnvServiceInfo{}
	for name, svc := range s.services {
		_, has := s.files[name]
		out = append(out, domain.EnvServiceInfo{Name: name, OneShot: domain.OneShotService(name, text(svc["restart"]), serviceLabels(svc)), UsesProjectEnv: usesProjectEnv(s.dir, svc), HasOverride: has})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (s *destState) raw(project string) domain.EnvStateRaw {
	out := domain.EnvStateRaw{Project: project, Revision: s.revision(), Services: s.serviceInfo(), Files: []domain.EnvFileRaw{}}
	scopes := []string{""}
	for scope := range s.files {
		if scope != "" {
			scopes = append(scopes, scope)
		}
	}
	sort.Strings(scopes[1:])
	for _, scope := range scopes {
		content, exists := s.files[scope]
		out.Files = append(out.Files, domain.EnvFileRaw{Scope: scope, Exists: exists, Variables: parseEnvDoc(content).variables()})
	}
	return out
}

func parseLive(output []byte) map[string]domain.LimitSet {
	type candidate struct {
		set     domain.LimitSet
		running bool
	}
	chosen := map[string]candidate{}
	for _, line := range strings.Split(string(output), "\n") {
		f := strings.Fields(line)
		if len(f) < 7 || !liveLinesOnly.MatchString(f[0]) || (len(f) > 7 && strings.EqualFold(f[7], "true")) {
			continue
		}
		num := func(s string) int64 { n, _ := strconv.ParseInt(s, 10, 64); return n }
		set := domain.LimitSet{MemoryBytes: num(f[1]), NanoCPUs: num(f[2]), MemSwapBytes: num(f[3])}
		if set.NanoCPUs == 0 && num(f[4]) > 0 && num(f[5]) > 0 {
			set.NanoCPUs = num(f[4]) * 1_000_000_000 / num(f[5])
		}
		if set.MemoryBytes < 0 {
			set.MemoryBytes = 0
		}
		if set.MemSwapBytes < 0 {
			set.MemSwapBytes = 0
		}
		running := f[6] == "true"
		if old, ok := chosen[f[0]]; !ok || (running && !old.running) {
			chosen[f[0]] = candidate{set, running}
		}
	}
	out := map[string]domain.LimitSet{}
	for name, c := range chosen {
		out[name] = c.set
	}
	return out
}
func (e *Engine) readLive(ctx context.Context, t domain.ShipTarget, project string) (map[string]domain.LimitSet, error) {
	data, err := e.remote(ctx, t, nil, "sh", "-c", liveScript, "--", composeProject(t, project))
	if err != nil {
		return nil, err
	}
	return parseLive(data), nil
}

// envStage holds reviewed changes turned into complete file contents.
type envStage struct {
	files  map[string][]byte // rel -> new content
	expect map[string]string // rel -> sha of the content the change was based on, or "-"
}

func buildEnvStage(state *destState, changes []domain.EnvChange, known map[string]bool) (*envStage, error) {
	groups := map[string][]domain.EnvChange{}
	for _, c := range changes {
		groups[c.Scope] = append(groups[c.Scope], c)
	}
	st := &envStage{files: map[string][]byte{}, expect: map[string]string{}}
	for scope, group := range groups {
		if scope != "" && !known[scope] {
			return nil, fmt.Errorf("service %q does not exist in the project", scope)
		}
		old, exists := state.files[scope]
		doc := parseEnvDoc(old)
		if err := doc.apply(group); err != nil {
			return nil, err
		}
		content := doc.bytes()
		if exists && bytes.Equal(content, old) {
			continue
		}
		if !exists && len(content) == 0 {
			continue
		}
		rel := relOfScope(scope)
		st.files[rel] = content
		if exists {
			st.expect[rel] = sha(old)
		} else {
			st.expect[rel] = "-"
		}
	}
	return st, nil
}

// scopesAfter lists service scopes with an env.d file once the stage is installed.
func (s *destState) scopesAfter(st *envStage, known map[string]bool) []string {
	set := map[string]bool{}
	for scope := range s.files {
		if scope != "" {
			set[scope] = true
		}
	}
	if st != nil {
		for rel := range st.files {
			if m := envEntry.FindStringSubmatch(rel); m != nil {
				set[m[1]] = true
			}
		}
	}
	out := []string{}
	for scope := range set {
		if known[scope] {
			out = append(out, scope)
		}
	}
	sort.Strings(out)
	return out
}

type tarEntry struct {
	name string
	data []byte
	mode int64
}

func makeTar(entries []tarEntry) ([]byte, error) {
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	for _, entry := range entries {
		if strings.HasPrefix(entry.name, "/") || strings.Contains(entry.name, "..") {
			return nil, fmt.Errorf("invalid staged path")
		}
		if err := w.WriteHeader(&tar.Header{Name: entry.name, Mode: entry.mode, Size: int64(len(entry.data)), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0)}); err != nil {
			return nil, err
		}
		if _, err := w.Write(entry.data); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
func (st *envStage) entries(extra ...tarEntry) []tarEntry {
	rels := make([]string, 0, len(st.files))
	for rel := range st.files {
		rels = append(rels, rel)
	}
	sort.Strings(rels)
	var manifest strings.Builder
	var out []tarEntry
	for _, rel := range rels {
		fmt.Fprintf(&manifest, "%s %s\n", rel, st.expect[rel])
		out = append(out, tarEntry{"files/" + rel, st.files[rel], 0600})
	}
	for _, x := range extra {
		out = append(out, x)
	}
	return append([]tarEntry{{"MANIFEST", []byte(manifest.String()), 0600}}, out...)
}

func (e *Engine) listProjects(ctx context.Context, t domain.ShipTarget) ([]domain.EnvProject, error) {
	data, err := e.remote(ctx, t, nil, "sh", "-c", listScript, "--", t.AppsDir)
	if err != nil {
		return nil, err
	}
	out := []domain.EnvProject{}
	for _, name := range strings.Fields(string(data)) {
		if !domain.ValidProject(name) {
			continue
		}
		state, err := e.readState(ctx, t, e.projectDir(t, name))
		if err != nil {
			return nil, err
		}
		item := domain.EnvProject{Name: name, Services: []string{}}
		for _, svc := range state.serviceInfo() {
			item.Services = append(item.Services, svc.Name)
		}
		out = append(out, item)
	}
	return out, nil
}

// EnvProjects lists projects installed on the destination (never the infrastructure).
func (e *Engine) EnvProjects(ctx context.Context, t domain.ShipTarget) ([]domain.EnvProject, error) {
	if err := application.ValidateTarget(t); err != nil {
		return nil, err
	}
	return e.listProjects(ctx, t)
}

func (e *Engine) EnvState(ctx context.Context, t domain.ShipTarget, project string) (domain.EnvStateRaw, error) {
	if err := application.ValidateTarget(t); err != nil {
		return domain.EnvStateRaw{}, err
	}
	if !domain.ValidProject(project) {
		return domain.EnvStateRaw{}, fmt.Errorf("invalid project")
	}
	state, err := e.readState(ctx, t, e.projectDir(t, project))
	if err != nil {
		return domain.EnvStateRaw{}, err
	}
	if len(state.compose) == 0 {
		return domain.EnvStateRaw{}, fmt.Errorf("project is not deployed on the destination")
	}
	return state.raw(project), nil
}

// EnvWrite applies a reviewed change set atomically: backup, temp file, rename,
// mode preserved, comments/order/unknown lines kept, optimistic revision check.
func (e *Engine) EnvWrite(ctx context.Context, t domain.ShipTarget, project, revision string, changes []domain.EnvChange) error {
	if err := application.ValidateTarget(t); err != nil {
		return err
	}
	if !domain.ValidProject(project) || domain.ValidateEnvChanges(changes) != nil || len(changes) == 0 {
		return fmt.Errorf("invalid environment change")
	}
	dir := e.projectDir(t, project)
	state, err := e.readState(ctx, t, dir)
	if err != nil {
		return err
	}
	if len(state.compose) == 0 {
		return fmt.Errorf("project is not deployed on the destination")
	}
	if revision != state.revision() {
		return fmt.Errorf("%s", EnvConflictMessage)
	}
	known := map[string]bool{}
	for name := range state.services {
		known[name] = true
	}
	stage, err := buildEnvStage(state, changes, known)
	if err != nil {
		return err
	}
	if len(stage.files) == 0 {
		return nil
	}
	override, err := renderOverride(overrideInput{Existing: state.override, KeepLimits: true, Dir: dir, EnvServices: state.scopesAfter(stage, known)})
	if err != nil {
		return err
	}
	stage.files[overrideFile] = override
	if len(state.override) > 0 {
		stage.expect[overrideFile] = sha(state.override)
	} else {
		stage.expect[overrideFile] = "-"
	}
	archive, err := makeTar(stage.entries())
	if err != nil {
		return err
	}
	if _, err = e.remote(ctx, t, nil, "mkdir", "-p", filepath.Join(dir, ".manager")); err != nil {
		return err
	}
	cmd, err := e.destination(ctx, t, "flock", "-n", filepath.Join(dir, ".manager", "deploy.lock"), "sh", "-c", writeScript, "--", dir, strconv.FormatInt(time.Now().Unix(), 10))
	if err != nil {
		return err
	}
	var out cappedBuffer
	cmd.Stdin = bytes.NewReader(archive)
	cmd.Stdout = &out
	if err = cmd.Run(); err != nil {
		if strings.Contains(out.String(), "MANAGER_ENV_CONFLICT") {
			return fmt.Errorf("%s", EnvConflictMessage)
		}
		if strings.TrimSpace(out.String()) == "" {
			return fmt.Errorf("another operation holds the project lock; retry shortly")
		}
		return fmt.Errorf("environment write failed; no variables were changed")
	}
	return nil
}

// BuildArgs returns build-time argument values from the destination's project
// .env with the plan's pending project-scope edits layered on top. The values go
// straight to the build runner and are never stored or logged.
func (e *Engine) BuildArgs(ctx context.Context, p domain.ShipPlan) (map[string]string, error) {
	if p.Build == nil || len(p.Build.BuildArgNames) == 0 {
		return map[string]string{}, nil
	}
	if err := application.ValidateTarget(p.Target); err != nil {
		return nil, err
	}
	var b bundle
	if err := json.Unmarshal(p.Bundle, &b); err != nil {
		return nil, err
	}
	project := p.Request.Project
	if !domain.ValidProject(project) {
		return nil, fmt.Errorf("invalid project")
	}
	state, err := e.readState(ctx, p.Target, e.projectDir(p.Target, project))
	if err != nil {
		return nil, err
	}
	doc := parseEnvDoc(state.files[""])
	var pending []domain.EnvChange
	for _, c := range b.EnvChanges {
		if c.Scope == "" {
			pending = append(pending, c)
		}
	}
	if err = doc.apply(pending); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, name := range p.Build.BuildArgNames {
		if v, ok := doc.lookup(name); ok {
			out[name] = v
		}
	}
	return out, nil
}
