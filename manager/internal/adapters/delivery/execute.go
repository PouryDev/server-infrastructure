package delivery

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	"gopkg.in/yaml.v3"
)

func (e *Engine) Execute(ctx context.Context, p domain.ShipPlan, progress func(domain.ShipProgress) error) error {
	step := func(phase, message string) error {
		return progress(domain.ShipProgress{Phase: phase, Message: message})
	}
	if err := step("preflight", "Verifying local image IDs and configuration fingerprint"); err != nil {
		return err
	}
	// Env values live only in the private bundle; recover them for the re-render.
	request := p.Request
	if len(p.Bundle) > 0 {
		var original bundle
		if err := json.Unmarshal(p.Bundle, &original); err != nil {
			return err
		}
		request.Env = original.EnvChanges
	}
	current, err := e.prepare(ctx, p.Target, request, true)
	if err != nil {
		return err
	}
	if current.Fingerprint != p.Fingerprint {
		return fmt.Errorf("local images or configuration changed; prepare a new preview")
	}
	// Only recomputed, fingerprint-verified local configuration is executable.
	// The wire protocol cannot substitute a destination directory or manifest.
	current.ID = p.ID
	p = current
	if !safeName(p.ID) {
		return fmt.Errorf("invalid release identity")
	}
	probe, err := e.Probe(ctx, p.Target)
	if err != nil {
		return err
	}
	for _, image := range p.Images {
		if image.Platform != probe.Platform {
			return fmt.Errorf("image platform %s does not match destination %s", image.Platform, probe.Platform)
		}
	}
	if len(p.Images) > 0 {
		for _, image := range p.Images {
			ids, err := e.remote(ctx, p.Target, nil, "docker", "image", "ls", "--no-trunc", "--filter", "reference="+image.DestinationRef, "--format", "{{.ID}}")
			if err != nil {
				return fmt.Errorf("destination image catalog unavailable; no images were transferred")
			}
			if strings.TrimSpace(string(ids)) == "" {
				continue
			}
			data, err := e.remote(ctx, p.Target, nil, "docker", "image", "inspect", image.DestinationRef, "--format", "{{json .}}")
			existing, parseErr := parseImage(data, image.DestinationRef)
			if err != nil || parseErr != nil {
				return fmt.Errorf("existing destination image could not be verified")
			}
			if existing.ID != image.ID && existing.ContentID != image.ContentID {
				return fmt.Errorf("destination image tag already has different contents; prepare a new build with a fresh tag")
			}
		}
		if err = step("transfer", "Streaming existing local images to destination Docker (no registry)"); err != nil {
			return err
		}
		refs := []string{}
		for _, image := range p.Images {
			if image.DestinationRef != image.Ref {
				if _, err = run(ctx, "", nil, "docker", "image", "tag", image.ID, image.DestinationRef); err != nil {
					return err
				}
			}
			refs = append(refs, image.DestinationRef)
		}
		save := command(ctx, "", append([]string{"docker", "image", "save"}, refs...)...)
		pipe, err := save.StdoutPipe()
		if err != nil {
			return err
		}
		save.Stderr = io.Discard
		if err = save.Start(); err != nil {
			return fmt.Errorf("local image export failed")
		}
		counted := &transferReader{source: pipe, report: func(n int64) error {
			return progress(domain.ShipProgress{Phase: "transfer", Message: "Streaming existing local images to destination Docker (no registry)", Progress: &domain.WorkProgress{Current: n, Unit: "bytes"}})
		}}
		_, loadErr := e.remote(ctx, p.Target, counted, "docker", "image", "load", "--quiet")
		pipe.Close()
		saveErr := save.Wait()
		if loadErr != nil || saveErr != nil {
			return fmt.Errorf("image transfer failed; no deployment was applied")
		}
		for _, image := range p.Images {
			data, err := e.remote(ctx, p.Target, nil, "docker", "image", "inspect", image.DestinationRef, "--format", "{{json .}}")
			imported, parseErr := parseImage(data, image.DestinationRef)
			if err != nil || parseErr != nil || (imported.ID != image.ID && imported.ContentID != image.ContentID) {
				return fmt.Errorf("destination image verification failed; deployment was not applied")
			}
		}
		if err = step("verified", "Destination image content matches the reviewed local images"); err != nil {
			return err
		}
	}
	if p.Request.Mode == "image" {
		return nil
	}
	var b bundle
	if err = json.Unmarshal(p.Bundle, &b); err != nil {
		return err
	}
	if p.Request.Deployment != nil {
		return e.executeNative(ctx, p, b, progress)
	}
	kind := "deploy"
	if p.Request.Mode == "apply" {
		kind = "apply"
	}
	release := filepath.Join(p.Directory, ".manager", "releases", p.ID)
	state, err := e.readState(ctx, p.Target, p.Directory)
	if err != nil {
		return err
	}
	var manifestData []byte
	manifestServices := map[string]bool{}
	if kind == "deploy" {
		manifest := object(replaceRelease(b.Manifest, release))
		// A service-only deployment replaces only that service definition in the
		// destination manifest; other services keep their destination configuration.
		if p.Request.Service != "" {
			if len(bytes.TrimSpace(state.compose)) == 0 {
				return fmt.Errorf("service-only deployment needs an existing destination project; deploy the whole project first")
			}
			var previous map[string]any
			if err = yaml.Unmarshal(state.compose, &previous); err != nil {
				return fmt.Errorf("destination compose file could not be read")
			}
			services := object(previous["services"])
			svc, ok := object(manifest["services"])[p.Request.Service]
			if !ok {
				return fmt.Errorf("selected service disappeared")
			}
			services[p.Request.Service] = svc
			previous["services"] = services
			previous["name"] = p.Project
			for _, section := range []string{"networks", "volumes", "configs", "secrets"} {
				dest := object(previous[section])
				for key, value := range object(manifest[section]) {
					if _, ok := dest[key]; !ok {
						dest[key] = value
					}
				}
				if len(dest) > 0 {
					previous[section] = dest
				}
			}
			manifest = previous
		}
		for name := range object(manifest["services"]) {
			manifestServices[name] = true
		}
		if manifestData, err = json.Marshal(manifest); err != nil {
			return err
		}
	} else {
		for name := range state.services {
			manifestServices[name] = true
		}
	}
	// Env edits become complete files plus the generated override.
	stage := &envStage{files: map[string][]byte{}, expect: map[string]string{}}
	if kind == "deploy" && len(b.EnvChanges) > 0 {
		if stage, err = buildEnvStage(state, b.EnvChanges, manifestServices); err != nil {
			return err
		}
	}
	live, err := e.readLive(ctx, p.Target, p.Request.Project)
	if err != nil {
		return err
	}
	selected := map[string]bool{}
	for _, name := range p.Services {
		selected[name] = true
	}
	styles := map[string]domain.LimitStyle{}
	for name, svc := range state.services {
		_, styles[name] = domain.ComposeLimits(svc)
	}
	for name, style := range b.Styles {
		styles[name] = style
	}
	limits := b.Limits
	if kind == "apply" {
		// An apply recreates from the destination manifest, never local files.
		limits = map[string]domain.LimitSet{}
		for name, svc := range state.services {
			limits[name], _ = domain.ComposeLimits(svc)
		}
	}
	useCompose := kind == "deploy" && p.Request.UseComposeLimits
	full := kind == "deploy" && p.Request.Service == ""
	pins := pinsFor(limits, live, selected, full, useCompose, manifestServices)
	override, err := renderOverride(overrideInput{Dir: p.Directory, EnvServices: state.scopesAfter(stage, manifestServices), Pins: pins, Styles: styles})
	if err != nil {
		return err
	}
	// The rollback override keeps the previous env.d wiring and pins every
	// currently limited container at its live value.
	previousKnown := map[string]bool{}
	for name := range state.services {
		previousKnown[name] = true
	}
	var rollbackPins []domain.PreservedLimit
	for _, pin := range domain.LiveLimits(live) {
		if previousKnown[pin.Service] {
			rollbackPins = append(rollbackPins, pin)
		}
	}
	previous, err := renderOverride(overrideInput{Dir: p.Directory, EnvServices: state.scopesAfter(nil, previousKnown), Pins: rollbackPins, Styles: styles})
	if err != nil {
		return err
	}
	if kind == "deploy" {
		b.Files = append(b.Files, bundleFile{Path: "compose.yaml", Data: manifestData, Mode: 0600})
	}
	b.Files = append(b.Files, bundleFile{Path: "apply.sh", Data: []byte(applyScript), Mode: 0700},
		bundleFile{Path: overrideFile, Data: override, Mode: 0600},
		bundleFile{Path: "manager.override.prev.yaml", Data: previous, Mode: 0600},
		bundleFile{Path: "repos", Data: []byte(strings.Join(b.Repos, "\n") + "\n"), Mode: 0600},
		bundleFile{Path: "services", Data: []byte(strings.Join(b.Services, "\n") + "\n"), Mode: 0600},
		bundleFile{Path: "restore.list", Data: []byte(strings.Join(b.Restore, "\n") + "\n"), Mode: 0600})
	if len(stage.files) > 0 {
		for _, entry := range stage.entries() {
			b.Files = append(b.Files, bundleFile{Path: "envstage/" + entry.name, Data: entry.data, Mode: 0600})
		}
	}
	if p.Target.Mode == "local" {
		if err = checkSandbox(p.Directory); err != nil {
			return err
		}
	}
	if _, err = e.remote(ctx, p.Target, nil, "mkdir", "-p", release); err != nil {
		return err
	}
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	for _, file := range b.Files {
		if filepath.IsAbs(file.Path) || strings.Contains(file.Path, "..") {
			return fmt.Errorf("invalid bundle path")
		}
		if err = writer.WriteHeader(&tar.Header{Name: file.Path, Mode: file.Mode, Size: int64(len(file.Data)), Typeflag: tar.TypeReg}); err != nil {
			return err
		}
		if _, err = writer.Write(file.Data); err != nil {
			return err
		}
	}
	if err = writer.Close(); err != nil {
		return err
	}
	if _, err = e.remote(ctx, p.Target, &archive, "tar", "-xf", "-", "-C", release); err != nil {
		return err
	}
	for _, network := range b.Networks {
		if !strings.HasPrefix(network, "manager-test-") {
			return fmt.Errorf("unexpected sandbox network")
		}
		if _, err = e.remote(ctx, p.Target, nil, "sh", "-c", `docker network inspect "$1" >/dev/null 2>&1 || docker network create "$1" >/dev/null`, "--", network); err != nil {
			return err
		}
	}
	if err = step("deploy", "Applying Compose configuration and waiting for selected services"); err != nil {
		return err
	}
	tag, keep := "-", "0"
	if p.Request.Mode == "build" {
		tag = b.Tag
		if p.Target.Mode != "local" {
			keep = strconv.Itoa(b.Keep)
		}
	}
	service := p.Request.Service
	if kind == "apply" {
		service = ""
	}
	cmd, err := e.destination(ctx, p.Target, "flock", "-n", filepath.Join(p.Directory, ".manager", "deploy.lock"), "sh", filepath.Join(release, "apply.sh"), p.Directory, release, p.Project, service, p.Target.DataDir, p.Target.AppsDir, kind, tag, keep)
	if err != nil {
		return err
	}
	var output cappedBuffer
	cmd.Stdout = &output
	cmd.Stderr = io.Discard
	if err = cmd.Run(); err != nil {
		switch {
		case strings.Contains(output.String(), "MANAGER_ROLLED_BACK"):
			if kind == "apply" {
				return fmt.Errorf("apply failed health checks; previous variable files were restored and services rolled back")
			}
			return fmt.Errorf("deployment failed health checks and was rolled back to the previous manifest")
		case strings.Contains(output.String(), "MANAGER_ROLLBACK_FAILED"):
			return fmt.Errorf("deployment failed and rollback failed; inspect destination services")
		case strings.Contains(output.String(), "MANAGER_PREFLIGHT_FAILED"):
			return fmt.Errorf("destination Compose validation failed; no containers were changed (check env files and paths)")
		case strings.Contains(output.String(), "MANAGER_ENV_CONFLICT"):
			return fmt.Errorf("env files changed on the destination during the preview; prepare again")
		case strings.Contains(output.String(), "MANAGER_NO_PREVIOUS_RELEASE"):
			return fmt.Errorf("deployment did not become healthy and there was no previous release to roll back to; inspect the destination")
		case strings.TrimSpace(output.String()) == "":
			return fmt.Errorf("the destination did not run the deployment (another operation may hold the project lock); retry shortly")
		}
		return fmt.Errorf("deployment did not complete; inspect destination state before retrying")
	}
	return step("healthy", "Compose deployment completed; destination manifest activated")
}

func checkSandbox(dir string) error {
	for current := dir; current != "/"; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("sandbox destination contains a symlink or inaccessible directory")
		}
	}
	return nil
}
