package delivery

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/buildsrc"
	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type nativeRequest struct {
	domain.DeploymentRequest
	Manifest map[string]any `json:"manifest"`
}
type nativePreview struct {
	Revision string   `json:"revision"`
	EnvKeys  []string `json:"envKeys"`
	AppsDir  string   `json:"appsDir"`
	DataDir  string   `json:"dataDir"`
}

// nativeCall uses the existing pinned-host SSH transport. The destination bridge
// reads its broker credential inside its own container, never over the wire.
func (e *Engine) nativeCall(ctx context.Context, target domain.ShipTarget, operation string, in, out any) error {
	if err := application.ValidateTarget(target); err != nil {
		return err
	}
	if target.Mode != "ssh" {
		return fmt.Errorf("automatic project deployment requires an SSH destination")
	}
	payload, err := json.Marshal(in)
	if err != nil {
		return err
	}
	data, err := e.remote(ctx, target, bytes.NewReader(payload), "docker", "compose", "--project-directory", target.InfraDir,
		"-f", filepath.Join(target.InfraDir, "compose.yaml"), "exec", "-T", "manager-storefront", "python3", "/infra/scripts/project-command.py", operation)
	if err != nil {
		return fmt.Errorf("destination manager bridge unavailable; update the destination manager and check SSH")
	}
	var failure struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(data, &failure) != nil {
		return fmt.Errorf("destination manager returned an invalid response")
	}
	if failure.Error != "" {
		return fmt.Errorf("%s", failure.Error)
	}
	return json.Unmarshal(data, out)
}

func (e *Engine) nativeSource(ctx context.Context, source, tag string) (buildsrc.Spec, map[string]any, error) {
	src, err := e.Sources.Resolve(source)
	if err != nil {
		return buildsrc.Spec{}, nil, err
	}
	spec, err := src.LoadSpec()
	if err != nil {
		return spec, nil, err
	}
	path, err := src.ComposePath(spec)
	if err != nil {
		return spec, nil, err
	}
	tagFile, cleanup, err := tempTagFile(tag)
	if err != nil {
		return spec, nil, err
	}
	defer cleanup()
	// Never read a development .env, even if one exists next to Compose.
	cs := composeSource{dir: filepath.Dir(path), file: path, tagFile: tagFile}
	args := cs.args(false)
	args = append(args[:2], append([]string{"--profile", "*"}, args[2:]...)...)
	raw, err := run(ctx, cs.dir, nil, args...)
	if err != nil {
		return spec, nil, fmt.Errorf("project Compose cannot be rendered without local env files")
	}
	var manifest map[string]any
	err = json.Unmarshal(raw, &manifest)
	return spec, manifest, err
}

func (e *Engine) NativeState(ctx context.Context, target domain.ShipTarget, source string) (domain.NativeDeploymentState, error) {
	result := domain.NativeDeploymentState{Services: []string{}}
	if err := e.nativeCall(ctx, target, "catalog", nil, &result.Catalog); err != nil {
		return result, err
	}
	if result.Catalog.AppsDir != target.AppsDir || result.Catalog.DataDir != target.DataDir {
		return result, fmt.Errorf("destination paths do not match the target manager")
	}
	_, manifest, err := e.nativeSource(ctx, source, "manager-preview")
	if err != nil {
		return result, err
	}
	for name := range object(manifest["services"]) {
		result.Services = append(result.Services, name)
	}
	sort.Strings(result.Services)
	return result, nil
}

func nativeInput(p domain.ShipPlan, b bundle, key string) nativeRequest {
	route := p.Request.Deployment
	input := nativeRequest{DeploymentRequest: domain.DeploymentRequest{Name: p.Request.Project, Key: key,
		Revision: route.Revision, Domain: route.Domain, Service: route.Service, Port: route.Port,
		MigrationService: route.MigrationService, Environment: map[string]string{}, RemoveEnv: []string{}}, Manifest: b.Manifest}
	for _, change := range b.EnvChanges {
		if change.Action == "delete" {
			input.RemoveEnv = append(input.RemoveEnv, change.Name)
		} else {
			input.Environment[change.Name] = change.Value
		}
	}
	return input
}

func (e *Engine) prepareNative(ctx context.Context, target domain.ShipTarget, req domain.ShipRequest, final bool, p *domain.ShipPlan, b *bundle) error {
	if target.Mode != "ssh" || req.Mode != "build" || req.Service != "" || req.IncludeEnv || req.UseComposeLimits || !buildsrc.ValidTag(req.Tag) {
		return fmt.Errorf("automatic deployment builds the whole project over SSH and always preserves destination limits")
	}
	if err := domain.ValidateEnvChanges(req.Env); err != nil {
		return err
	}
	for _, change := range req.Env {
		if change.Scope != "" {
			return fmt.Errorf("automatic deployment accepts project-wide destination env edits")
		}
	}
	spec, manifest, err := e.nativeSource(ctx, req.Source, req.Tag)
	if err != nil {
		return err
	}
	if spec.App != req.Project || !domain.ValidProject(spec.App) {
		return fmt.Errorf("invalid deployment project")
	}
	p.Project, p.Directory = spec.App, filepath.Join(target.AppsDir, spec.App)
	p.Build = &domain.BuildInfo{Source: req.Source, App: spec.App, Tag: req.Tag, Platform: spec.Platform,
		Images: spec.Images, BuildArgNames: append([]string{}, spec.ArgNames...), Unbuilt: []string{}}
	built := map[string]bool{}
	for _, item := range spec.Images {
		built[spec.App+"-"+item.Service+":"+req.Tag] = true
	}
	images := map[string]bool{}
	limits := map[string]domain.LimitSet{}
	known := map[string]bool{}
	for name, value := range object(manifest["services"]) {
		svc := object(value)
		ref := text(svc["image"])
		if !built[ref] {
			return fmt.Errorf("every project image must be built by this source: %s", name)
		}
		if !images[ref] {
			img := domain.LocalImage{Ref: ref, Platform: spec.Platform}
			if final {
				if img, err = inspectImage(ctx, ref); err != nil {
					return err
				}
			}
			p.Images = append(p.Images, domain.ShipImage{LocalImage: img, DestinationRef: ref})
			images[ref] = true
		}
		p.Services = append(p.Services, name)
		known[name] = true
		limits[name], _ = domain.ComposeLimits(svc)
	}
	b.Manifest, b.Tag = manifest, req.Tag
	var preview nativePreview
	if err = e.nativeCall(ctx, target, "preview", nativeInput(*p, *b, "00000000-0000-0000-0000-000000000000"), &preview); err != nil {
		return err
	}
	if preview.AppsDir != target.AppsDir || preview.DataDir != target.DataDir {
		return fmt.Errorf("destination paths do not match the target manager")
	}
	keys := map[string]bool{}
	for _, key := range preview.EnvKeys {
		keys[key] = true
	}
	for _, change := range req.Env {
		p.EnvChanges = append(p.EnvChanges, domain.EnvChangeSummary{Scope: "", Name: change.Name, Action: change.Action, Exists: keys[change.Name]})
	}
	live, err := e.readLive(ctx, target, req.Project)
	if err != nil {
		return err
	}
	p.PreservedLimits = domain.MergeLimits(limits, live, false)
	p.Affected = append([]string{}, p.Services...)
	sort.Strings(p.Services)
	p.Warnings = append(p.Warnings, "Destination domain and app env are saved by its manager; local env files are never sent.",
		"All current destination container resource limits are preserved, including during rollback.")
	return nil
}

func (e *Engine) executeNative(ctx context.Context, p domain.ShipPlan, b bundle, progress func(domain.ShipProgress) error) error {
	digest := sha256.Sum256([]byte(p.ID))
	hexID := hex.EncodeToString(digest[:16])
	key := fmt.Sprintf("%s-%s-%s-%s-%s", hexID[:8], hexID[8:12], hexID[12:16], hexID[16:20], hexID[20:])
	var job domain.DeploymentJob
	if err := progress(domain.ShipProgress{Phase: "provisioning", Message: "Configuring the destination project, saved domain and app env"}); err != nil {
		return err
	}
	if err := e.nativeCall(ctx, p.Target, "launch", nativeInput(p, b, key), &job); err != nil {
		return err
	}
	last := ""
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if job.Phase != last {
			if err := progress(domain.ShipProgress{Phase: job.Phase, Message: "Destination project: " + job.Phase}); err != nil {
				return err
			}
			last = job.Phase
		}
		switch job.Status {
		case "ready":
			return nil
		case "failed", "interrupted":
			return fmt.Errorf("%s", job.Message)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("destination operation may still be running; inspect its project job before retrying")
		case <-ticker.C:
		}
		if err := e.nativeCall(ctx, p.Target, "job", map[string]string{"id": key}, &job); err != nil {
			return err
		}
	}
}
