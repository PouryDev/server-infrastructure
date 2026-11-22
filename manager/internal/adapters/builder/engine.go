// Package builder builds images from a registered local working tree. It runs only
// in the manager-builder runner, which has the Docker socket and read-only source
// mounts but no SSH keys and no destination access.
package builder

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/buildsrc"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type Engine struct {
	Registry buildsrc.Registry
	// Proxy is passed to builds as HTTP(S)_PROXY. A loopback proxy also selects the
	// host network for the build, like the local ship.conf files do.
	Proxy string
	mu    sync.Mutex
}

func (e *Engine) source(id string) (buildsrc.Source, buildsrc.Spec, error) {
	src, err := e.Registry.Resolve(id)
	if err != nil {
		return src, buildsrc.Spec{}, err
	}
	spec, err := src.LoadSpec()
	return src, spec, err
}
func describe(id string, src buildsrc.Source, spec buildsrc.Spec, subdir string) domain.BuildSource {
	out := domain.BuildSource{ID: id, Subdir: subdir, App: spec.App, ComposeFile: spec.ComposeFile, Platform: spec.Platform, KeepTags: spec.KeepTags, Images: spec.Images, BuildArgNames: append([]string{}, spec.ArgNames...), Services: []string{}}
	for _, i := range spec.Images {
		out.Services = append(out.Services, i.Service)
	}
	sort.Strings(out.BuildArgNames)
	return out
}

func (e *Engine) Sources(context.Context) ([]domain.BuildSource, error) {
	result := []domain.BuildSource{}
	for _, id := range e.Registry.IDs() {
		item := domain.BuildSource{ID: id, Images: []domain.BuildSpec{}, BuildArgNames: []string{}, Services: []string{}}
		src, spec, err := e.source(id)
		if err != nil {
			item.Error = err.Error()
		} else {
			item = describe(id, src, spec, e.Registry[id].Subdir)
		}
		result = append(result, item)
	}
	return result, nil
}

func (e *Engine) Inspect(ctx context.Context, id string) (domain.BuildInspection, error) {
	src, spec, err := e.source(id)
	if err != nil {
		return domain.BuildInspection{}, err
	}
	if _, err = src.ComposePath(spec); err != nil {
		return domain.BuildInspection{}, err
	}
	head, dirty := buildsrc.GitState(ctx, src.Dir)
	return domain.BuildInspection{Source: describe(id, src, spec, e.Registry[id].Subdir), Head: head, Dirty: dirty, Tag: buildsrc.MakeTag(head, dirty, time.Now())}, nil
}

type tail struct {
	lines []string
}

func (t *tail) Write(p []byte) (int, error) {
	for _, line := range strings.Split(string(p), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if len(line) > 300 {
			line = line[:300]
		}
		t.lines = append(t.lines, line)
		if len(t.lines) > 12 {
			t.lines = t.lines[1:]
		}
	}
	return len(p), nil
}

func redact(text string, values map[string]string) string {
	for _, v := range values {
		if len(v) >= 4 {
			text = strings.ReplaceAll(text, v, "***")
		}
	}
	return text
}

func proxyArgs(proxy string) []string {
	if proxy == "" {
		return nil
	}
	var args []string
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy"} {
		args = append(args, "--build-arg", name+"="+proxy)
	}
	return args
}

func (e *Engine) Build(ctx context.Context, req domain.BuildRequest, progress func(domain.ShipProgress) error) ([]domain.BuiltImage, error) {
	if !e.mu.TryLock() {
		return nil, fmt.Errorf("another build is already running")
	}
	defer e.mu.Unlock()
	src, spec, err := e.source(req.Source)
	if err != nil {
		return nil, err
	}
	if !buildsrc.ValidTag(req.Tag) {
		return nil, fmt.Errorf("invalid image tag")
	}
	head, dirty := buildsrc.GitState(ctx, src.Dir)
	if head != req.Head || dirty != req.Dirty {
		return nil, fmt.Errorf("the working tree changed since the preview; prepare a new preview")
	}
	wanted := map[string]bool{}
	for _, s := range req.Services {
		wanted[s] = true
	}
	var specs []domain.BuildSpec
	for _, item := range spec.Images {
		if len(wanted) == 0 || wanted[item.Service] {
			specs = append(specs, item)
			delete(wanted, item.Service)
		}
	}
	if len(wanted) > 0 || len(specs) == 0 {
		return nil, fmt.Errorf("selected service is not built by this project")
	}
	values := map[string]string{}
	for _, name := range spec.ArgNames {
		if v, ok := req.Args[name]; ok {
			values[name] = v
		}
	}
	for name := range req.Args {
		known := false
		for _, n := range spec.ArgNames {
			known = known || n == name
		}
		if !known {
			return nil, fmt.Errorf("unexpected build argument")
		}
	}
	built := []domain.BuiltImage{}
	for index, item := range specs {
		if err = progress(domain.ShipProgress{Phase: "build", Message: fmt.Sprintf("Building image %s (%d/%d)", item.Service, index+1, len(specs))}); err != nil {
			return built, err
		}
		contextDir, err := src.ContextPath(item.Context)
		if err != nil {
			return built, err
		}
		ref := spec.App + "-" + item.Service + ":" + req.Tag
		args := []string{"docker", "build", "--progress=rawjson", "--provenance=false", "--platform", spec.Platform, "--label", "infra.manager.build=true"}
		if item.Dockerfile != "" {
			dockerfile, err := src.ContextPath(item.Dockerfile)
			if err != nil {
				return built, err
			}
			args = append(args, "-f", dockerfile)
		}
		network := spec.Network
		if strings.Contains(e.Proxy, "127.0.0.1") || strings.Contains(e.Proxy, "localhost") {
			network = "host"
		}
		if network == "host" {
			args = append(args, "--network", "host")
		}
		args = append(args, proxyArgs(e.Proxy)...)
		names := make([]string, 0, len(spec.Literals))
		for name := range spec.Literals {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			args = append(args, "--build-arg", name+"="+spec.Literals[name])
		}
		env := append(os.Environ(), "DOCKER_HOST=unix:///var/run/docker.sock", "DOCKER_CONTEXT=default")
		for _, name := range spec.ArgNames {
			if v, ok := values[name]; ok {
				// Passed by name; the value travels only in the process environment.
				args = append(args, "--build-arg", name)
				env = append(env, name+"="+v)
			}
		}
		args = append(args, "-t", ref, contextDir)
		cmd := exec.CommandContext(ctx, args[0], args[1:]...)
		cmd.Dir = src.Dir
		cmd.Env = env
		output := buildOutput{report: progress}
		cmd.Stdout = &output
		cmd.Stderr = &output
		if err = cmd.Run(); err != nil {
			message := fmt.Sprintf("build of %s failed", item.Service)
			if ctx.Err() != nil {
				message = fmt.Sprintf("build of %s was interrupted", item.Service)
			}
			if len(output.tail.lines) > 0 && ctx.Err() == nil {
				message += "; last output: " + redact(strings.Join(output.tail.lines, " | "), values)
			}
			return built, fmt.Errorf("%s", message)
		}
		if err = output.flush(); err != nil {
			return built, err
		}
		var id bytes.Buffer
		inspect := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", ref)
		inspect.Env = env
		inspect.Stdout = &id
		if err = inspect.Run(); err != nil || !strings.HasPrefix(strings.TrimSpace(id.String()), "sha256:") {
			return built, fmt.Errorf("built image %s cannot be inspected", item.Service)
		}
		built = append(built, domain.BuiltImage{Service: item.Service, Ref: ref, ID: strings.TrimSpace(id.String())})
	}
	return built, nil
}
