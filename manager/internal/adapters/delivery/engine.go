package delivery

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
	"strings"

	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/buildsrc"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type Engine struct {
	InfraRoot, AppsRoot, KeysRoot             string
	HostInfraRoot, HostAppsRoot, HostDataRoot string
	// Sources are the owner-registered local projects that build mode may read.
	Sources buildsrc.Registry
}
type bundleFile struct {
	Path string
	Data []byte
	Mode int64
}
type bundle struct {
	Manifest map[string]any
	Files    []bundleFile
	Networks []string
	AllowEnv bool `json:"-"`
	// Private deployment inputs. Env values live only here, never in the public plan.
	EnvChanges []domain.EnvChange
	Limits     map[string]domain.LimitSet
	Styles     map[string]domain.LimitStyle
	Repos      []string
	Tag        string
	Keep       int
	Restore    []string
	Services   []string
	Revision   string
}

const releasePrefix = "__MANAGER_RELEASE__/"
const maxBundle = 8 << 20

func command(ctx context.Context, dir string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "DOCKER_HOST=unix:///var/run/docker.sock", "DOCKER_CONTEXT=default")
	if dir != "" {
		cmd.Env = append(cmd.Env, "PWD="+dir)
	}
	return cmd
}

type cappedBuffer struct{ bytes.Buffer }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxBundle {
		return 0, fmt.Errorf("command output exceeds limit")
	}
	return b.Buffer.Write(p)
}
func run(ctx context.Context, dir string, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := command(ctx, dir, args...)
	cmd.Stdin = stdin
	var output cappedBuffer
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s command failed (check prerequisites and configuration)", filepath.Base(args[0]))
	}
	return output.Bytes(), nil
}
func (e *Engine) source(project string) (string, error) {
	if project == "infra" {
		return e.InfraRoot, nil
	}
	if !safeName(project) {
		return "", fmt.Errorf("invalid source project")
	}
	dir := filepath.Join(e.AppsRoot, project)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil || resolved != dir {
		return "", fmt.Errorf("project source is missing or symlinked")
	}
	return dir, nil
}
func safeName(s string) bool {
	if s == "" || len(s) > 100 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return s != "." && s != ".." && !strings.HasPrefix(s, "-")
}

// composeSource locates a Compose file and the env files used to render it locally.
type composeSource struct{ dir, file, envFile, tagFile string }

func defaultSource(dir string) composeSource {
	c := composeSource{dir: dir, file: filepath.Join(dir, "compose.yaml")}
	if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
		c.envFile = filepath.Join(dir, ".env")
	}
	if _, err := os.Stat(filepath.Join(dir, ".tag.next")); err == nil {
		c.tagFile = filepath.Join(dir, ".tag.next")
	}
	return c
}
func (c composeSource) args(raw bool) []string {
	args := []string{"docker", "compose", "--project-directory", c.dir}
	for _, f := range []string{c.envFile, c.tagFile} {
		if f != "" {
			args = append(args, "--env-file", f)
		}
	}
	args = append(args, "-f", c.file, "config", "--format", "json", "--no-env-resolution", "--no-path-resolution")
	if raw {
		args = append(args, "--no-interpolate")
	}
	return args
}
func modelOf(ctx context.Context, c composeSource, raw bool) (map[string]any, error) {
	data, err := run(ctx, c.dir, nil, c.args(raw)...)
	if err != nil {
		return nil, fmt.Errorf("compose source cannot be rendered; check local env and compose files")
	}
	var result map[string]any
	if err = json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}
func model(ctx context.Context, dir string, raw bool) (map[string]any, error) {
	return modelOf(ctx, defaultSource(dir), raw)
}
func object(value any) map[string]any {
	if m, ok := value.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}
func list(value any) []any {
	if v, ok := value.([]any); ok {
		return v
	}
	return nil
}
func text(value any) string { s, _ := value.(string); return s }

func inspectImage(ctx context.Context, ref string) (domain.LocalImage, error) {
	data, err := run(ctx, "", nil, "docker", "image", "inspect", "--format", "{{json .}}", "--", ref)
	if err != nil {
		return domain.LocalImage{}, fmt.Errorf("selected image is not available locally")
	}
	return parseImage(data, ref)
}

// Docker's classic store reports a config digest as .Id; containerd reports an
// OCI manifest/index digest. The portable identity covers runtime configuration,
// platform and every filesystem layer, independently of that storage layout.
func parseImage(data []byte, ref string) (domain.LocalImage, error) {
	var image struct {
		ID           string `json:"Id"`
		OS           string `json:"Os"`
		Architecture string
		Variant      string
		Config       map[string]any
		RootFS       map[string]any
		Size         int64
	}
	if err := json.Unmarshal(data, &image); err != nil {
		return domain.LocalImage{}, err
	}
	if !strings.HasPrefix(image.ID, "sha256:") || len(image.ID) != 71 {
		return domain.LocalImage{}, fmt.Errorf("invalid local image identity")
	}
	if image.Config == nil || image.RootFS == nil {
		return domain.LocalImage{}, fmt.Errorf("image content metadata is missing")
	}
	// Classic Docker materializes optional OCI config fields as JSON null,
	// while containerd omits them (notably on scratch images). They mean the
	// same unset value; every non-null configuration value remains verified.
	for key, value := range image.Config {
		if value == nil {
			delete(image.Config, key)
		}
	}
	content, err := json.Marshal(struct {
		OS, Architecture, Variant string
		Config, RootFS            map[string]any
	}{image.OS, image.Architecture, image.Variant, image.Config, image.RootFS})
	if err != nil {
		return domain.LocalImage{}, err
	}
	digest := sha256.Sum256(content)
	return domain.LocalImage{ID: image.ID, ContentID: hex.EncodeToString(digest[:]), Ref: ref, Size: image.Size, Platform: image.OS + "/" + image.Architecture}, nil
}
func (e *Engine) Catalog(ctx context.Context) (domain.ShipCatalog, error) {
	result := domain.ShipCatalog{Projects: []domain.ShipProject{}, Images: []domain.LocalImage{}, Identities: []string{}}
	projects := []string{"infra"}
	entries, _ := os.ReadDir(e.AppsRoot)
	for _, entry := range entries {
		if entry.IsDir() && safeName(entry.Name()) {
			if _, err := os.Stat(filepath.Join(e.AppsRoot, entry.Name(), "compose.yaml")); err == nil {
				projects = append(projects, entry.Name())
			}
		}
	}
	for _, project := range projects {
		p := domain.ShipProject{Name: project, Services: []string{}}
		dir, err := e.source(project)
		if err == nil {
			var m map[string]any
			m, err = model(ctx, dir, true)
			if err == nil {
				for name := range object(m["services"]) {
					p.Services = append(p.Services, name)
				}
				sort.Strings(p.Services)
			}
		}
		if err != nil {
			p.Error = "Compose source unavailable"
		}
		result.Projects = append(result.Projects, p)
	}
	data, err := run(ctx, "", nil, "docker", "image", "ls", "--no-trunc", "--format", "{{.Repository}}:{{.Tag}} {{.ID}}")
	if err != nil {
		return result, err
	}
	seen := map[string]domain.LocalImage{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || strings.Contains(fields[0], "<none>") {
			continue
		}
		image, ok := seen[fields[1]]
		if !ok {
			image, err = inspectImage(ctx, fields[1])
			if err != nil {
				continue
			}
			seen[fields[1]] = image
		}
		image.Ref = fields[0]
		result.Images = append(result.Images, image)
	}
	entries, _ = os.ReadDir(e.KeysRoot)
	for _, entry := range entries {
		if !entry.IsDir() && safeName(entry.Name()) && entry.Name() != "known_hosts" && !strings.HasSuffix(entry.Name(), ".pub") {
			result.Identities = append(result.Identities, entry.Name())
		}
	}
	sort.Slice(result.Images, func(i, j int) bool { return result.Images[i].Ref < result.Images[j].Ref })
	return result, nil
}
