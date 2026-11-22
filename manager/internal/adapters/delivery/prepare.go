package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/buildsrc"
	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

func (e *Engine) Prepare(ctx context.Context, target domain.ShipTarget, req domain.ShipRequest) (domain.ShipPlan, error) {
	return e.prepare(ctx, target, req, false)
}

func newPlan(target domain.ShipTarget, req domain.ShipRequest) domain.ShipPlan {
	public := req
	// Values never appear in a plan: they travel only in the private bundle.
	public.Env = nil
	return domain.ShipPlan{Target: target, Request: public, Services: []string{}, Images: []domain.ShipImage{}, Files: []string{}, EnvChanges: []domain.EnvChangeSummary{}, PreservedLimits: []domain.PreservedLimit{}, Affected: []string{},
		Warnings: []string{"Remote data, databases, volumes and queues are never copied or deleted."}}
}

// pinsFor returns the limit pins for one operation. Services that are being
// recreated follow MergeLimits; the others keep their live values as they are.
func pinsFor(limits map[string]domain.LimitSet, live map[string]domain.LimitSet, selected map[string]bool, full, useCompose bool, known map[string]bool) []domain.PreservedLimit {
	chosen, others := map[string]domain.LimitSet{}, map[string]domain.LimitSet{}
	for service, l := range live {
		if !known[service] {
			continue
		}
		if full || selected[service] {
			chosen[service] = l
		} else {
			others[service] = l
		}
	}
	pins := domain.MergeLimits(limits, chosen, useCompose)
	pins = append(pins, domain.LiveLimits(others)...)
	sort.Slice(pins, func(i, j int) bool { return pins[i].Service < pins[j].Service })
	return pins
}

func (e *Engine) prepare(ctx context.Context, target domain.ShipTarget, req domain.ShipRequest, final bool) (domain.ShipPlan, error) {
	if err := application.ValidateTarget(target); err != nil {
		return domain.ShipPlan{}, err
	}
	p := newPlan(target, req)
	b := bundle{Manifest: map[string]any{}, Files: []bundleFile{}, Networks: []string{}, AllowEnv: req.IncludeEnv, EnvChanges: req.Env}
	switch req.Mode {
	case "image":
		image, err := inspectImage(ctx, req.Image)
		if err != nil {
			return p, err
		}
		image.Ref = req.Image
		p.Images = append(p.Images, domain.ShipImage{LocalImage: image, DestinationRef: "manager-shipped/image:" + strings.TrimPrefix(image.ID, "sha256:")[:24]})
		p.Directory = target.InfraDir
	case "deploy", "build":
		var err error
		if req.Deployment != nil {
			err = e.prepareNative(ctx, target, req, final, &p, &b)
		} else {
			err = e.prepareDeploy(ctx, target, req, final, &p, &b)
		}
		if err != nil {
			return p, err
		}
	case "apply":
		if err := e.prepareApply(ctx, target, req, &p, &b); err != nil {
			return p, err
		}
	default:
		return p, fmt.Errorf("invalid shipment mode")
	}
	sort.Slice(b.Files, func(i, j int) bool { return b.Files[i].Path < b.Files[j].Path })
	sort.Strings(b.Networks)
	sort.Slice(p.Images, func(i, j int) bool { return p.Images[i].DestinationRef < p.Images[j].DestinationRef })
	encoded, err := json.Marshal(b)
	if err != nil {
		return p, err
	}
	if len(encoded) > maxBundle {
		return p, fmt.Errorf("configuration bundle exceeds 8 MiB")
	}
	p.Bundle = encoded
	for _, file := range b.Files {
		p.Files = append(p.Files, file.Path)
	}
	sort.Strings(p.Files)
	// A build preview cannot know image IDs yet: its identity is the source tag.
	images := p.Images
	if req.Mode == "build" {
		images = make([]domain.ShipImage, len(p.Images))
		for i, image := range p.Images {
			images[i] = domain.ShipImage{DestinationRef: image.DestinationRef}
		}
	}
	fpReq := p.Request
	fingerprint, _ := json.Marshal(struct {
		Bundle  []byte
		Images  []domain.ShipImage
		Target  domain.ShipTarget
		Request domain.ShipRequest
	}{encoded, images, target, fpReq})
	digest := sha256.Sum256(fingerprint)
	p.Fingerprint = hex.EncodeToString(digest[:])
	return p, nil
}

func tempTagFile(tag string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "ship-tag-")
	if err != nil {
		return "", func() {}, err
	}
	file := filepath.Join(dir, "tag.env")
	if err = os.WriteFile(file, []byte("TAG="+tag+"\n"), 0600); err != nil {
		os.RemoveAll(dir)
		return "", func() {}, err
	}
	return file, func() { os.RemoveAll(dir) }, nil
}

func (e *Engine) prepareDeploy(ctx context.Context, target domain.ShipTarget, req domain.ShipRequest, final bool, p *domain.ShipPlan, b *bundle) error {
	var (
		dir   string
		cs    composeSource
		spec  buildsrc.Spec
		built = map[string]domain.BuildSpec{}
	)
	if req.Mode == "build" {
		if e.Sources == nil || !buildsrc.ValidTag(req.Tag) {
			return fmt.Errorf("build source is not configured")
		}
		src, err := e.Sources.Resolve(req.Source)
		if err != nil {
			return err
		}
		if spec, err = src.LoadSpec(); err != nil {
			return err
		}
		composePath, err := src.ComposePath(spec)
		if err != nil {
			return err
		}
		tagFile, cleanup, err := tempTagFile(req.Tag)
		if err != nil {
			return err
		}
		defer cleanup()
		cs = composeSource{dir: filepath.Dir(composePath), file: composePath, tagFile: tagFile}
		if _, err = os.Stat(filepath.Join(cs.dir, ".env")); err == nil {
			cs.envFile = filepath.Join(cs.dir, ".env")
		}
		dir = cs.dir
		req.Project = spec.App
		req.IncludeEnv = false
		b.AllowEnv = false
		p.Request.Project = spec.App
		p.Request.IncludeEnv = false
		if !domain.ValidProject(spec.App) {
			return fmt.Errorf("this project name cannot be deployed from the panel")
		}
		for _, image := range spec.Images {
			built[spec.App+"-"+image.Service+":"+req.Tag] = image
		}
	} else {
		var err error
		if dir, err = e.source(req.Project); err != nil {
			return err
		}
		cs = defaultSource(dir)
	}
	if len(req.Env) > 0 && (req.Project == "infra" || !domain.ValidProject(req.Project)) {
		return fmt.Errorf("environment edits are not available for this project")
	}
	if err := domain.ValidateEnvChanges(req.Env); err != nil {
		return err
	}
	raw, err := modelOf(ctx, cs, true)
	if err != nil {
		return err
	}
	resolved, err := modelOf(ctx, cs, false)
	if err != nil {
		return err
	}
	services := object(raw["services"])
	if len(services) == 0 {
		return fmt.Errorf("project has no deployable services")
	}
	if req.Service != "" {
		if _, ok := services[req.Service]; !ok {
			return fmt.Errorf("service does not exist in project")
		}
		p.Services = []string{req.Service}
	} else {
		for name := range services {
			p.Services = append(p.Services, name)
		}
		sort.Strings(p.Services)
	}
	for _, c := range req.Env {
		if _, ok := services[c.Scope]; c.Scope != "" && !ok {
			return fmt.Errorf("service %q does not exist in the project", c.Scope)
		}
	}
	p.Project = req.Project
	p.Directory = filepath.Join(target.AppsDir, req.Project)
	if req.Project == "infra" {
		p.Directory = target.InfraDir
		p.Warnings = append(p.Warnings, "Infrastructure deployment may restart shared services; bootstrap the destination data directories first.")
	}
	if target.Mode == "local" {
		p.Project = composeProject(target, req.Project)
		p.Warnings = append(p.Warnings, "Same-host sandbox: separate project/network/data paths; published ports and host networking are disabled.")
	}
	selected := map[string]bool{}
	for _, name := range p.Services {
		selected[name] = true
	}
	b.Limits = map[string]domain.LimitSet{}
	b.Styles = map[string]domain.LimitStyle{}
	b.Tag = req.Tag
	neededBuilds := map[string]bool{}
	unbuilt := map[string]bool{}
	for name, value := range services {
		svc := object(value)
		delete(svc, "build")
		delete(svc, "pull_policy")
		if target.Mode == "local" {
			delete(svc, "ports")
			delete(svc, "container_name")
			delete(svc, "network_mode")
			delete(svc, "pid")
		}
		limits, style := domain.ComposeLimits(object(object(resolved["services"])[name]))
		b.Limits[name], b.Styles[name] = limits, style
		if selected[name] {
			ref := text(object(object(resolved["services"])[name])["image"])
			switch {
			case req.Mode == "build":
				if _, ok := built[ref]; ok {
					image := domain.LocalImage{Ref: ref, Platform: spec.Platform}
					if final {
						if image, err = inspectImage(ctx, ref); err != nil {
							return fmt.Errorf("%s: %w", name, err)
						}
					}
					svc["image"] = ref
					neededBuilds[ref] = true
					found := false
					for _, i := range p.Images {
						found = found || i.DestinationRef == ref
					}
					if !found {
						p.Images = append(p.Images, domain.ShipImage{LocalImage: image, DestinationRef: ref})
					}
				} else if ref != "" {
					unbuilt[ref] = true
				}
			default:
				if req.Service != "" && req.Image != "" {
					ref = req.Image
				}
				image, err := inspectImage(ctx, ref)
				if err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
				pin := "manager-shipped/" + strings.ToLower(req.Project) + ":" + strings.TrimPrefix(image.ID, "sha256:")[:24]
				svc["image"] = pin
				found := false
				for index, i := range p.Images {
					if i.DestinationRef == pin {
						found = true
						if image.Ref < i.Ref {
							p.Images[index].Ref = image.Ref
						}
					}
				}
				if !found {
					p.Images = append(p.Images, domain.ShipImage{LocalImage: image, DestinationRef: pin})
				}
			}
		}
		if req.Service != "" && !selected[name] {
			continue
		}
		if err = e.bindings(dir, p.Directory, target, svc, object(object(resolved["services"])[name]), req.IncludeEnv, b); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
	}
	raw["name"] = p.Project
	if target.Mode == "local" {
		for name, value := range object(raw["networks"]) {
			n := object(value)
			delete(n, "ipam")
			n["name"] = strings.TrimSuffix(p.Project, "-"+req.Project) + "-" + name
			if n["external"] == true {
				b.Networks = append(b.Networks, text(n["name"]))
			}
		}
		for name, value := range object(raw["volumes"]) {
			v := object(value)
			v["name"] = p.Project + "-" + name
		}
	}
	b.Manifest = raw
	for _, section := range []string{"configs", "secrets"} {
		for _, value := range object(raw[section]) {
			item := object(value)
			file := text(item["file"])
			if file == "" {
				continue
			}
			if !filepath.IsAbs(file) {
				file = filepath.Join(dir, file)
			}
			rel, ok := relativeTo(dir, file)
			if !ok {
				return fmt.Errorf("configuration or secret file escapes project root")
			}
			if section == "secrets" && !req.IncludeEnv {
				item["file"] = filepath.Join(p.Directory, rel)
				continue
			}
			if err = e.addFiles(dir, rel, b); err != nil {
				return err
			}
			item["file"] = releasePrefix + filepath.ToSlash(rel)
		}
	}
	if req.Project == "infra" && (req.Service == "" || req.Service == "manager-shipper") {
		if err = e.addFiles(dir, "config", b); err != nil {
			return err
		}
	}
	if req.IncludeEnv {
		for _, name := range []string{".env", "manager/.env"} {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				if err = e.addFiles(dir, name, b); err != nil {
					return err
				}
			}
		}
		p.Warnings = append(p.Warnings, "Local env files will be transmitted privately in this release; existing destination env files will not be overwritten.")
	} else {
		p.Warnings = append(p.Warnings, "Destination env files are preserved and must already exist when referenced.")
	}
	if req.Mode == "build" {
		if len(neededBuilds) == 0 {
			return fmt.Errorf("the selected service does not use an image built from this source")
		}
		info := &domain.BuildInfo{Source: req.Source, App: spec.App, Tag: req.Tag, Platform: spec.Platform, Images: []domain.BuildSpec{}, BuildArgNames: append([]string{}, spec.ArgNames...), Unbuilt: []string{}}
		for _, image := range spec.Images {
			if neededBuilds[spec.App+"-"+image.Service+":"+req.Tag] {
				info.Images = append(info.Images, image)
				b.Repos = append(b.Repos, spec.App+"-"+image.Service)
			}
		}
		for ref := range unbuilt {
			info.Unbuilt = append(info.Unbuilt, ref)
		}
		sort.Strings(info.Unbuilt)
		sort.Strings(b.Repos)
		if len(info.Unbuilt) > 0 {
			p.Warnings = append(p.Warnings, "Images not built from this source are not transferred and must already exist on the destination: "+strings.Join(info.Unbuilt, ", "))
		}
		b.Keep = spec.KeepTags
		p.Build = info
	}
	p.Affected = append([]string{}, p.Services...)
	// Destination-dependent previews (never part of the fingerprint).
	if len(req.Env) > 0 {
		state, err := e.readState(ctx, target, p.Directory)
		if err != nil {
			return fmt.Errorf("%w; the destination must be reachable to preview env edits and kept limits", err)
		}
		for _, c := range req.Env {
			_, exists := parseEnvDoc(state.files[c.Scope]).lookup(c.Name)
			summary := domain.EnvChangeSummary{Scope: c.Scope, Name: c.Name, Action: c.Action, Exists: exists}
			p.EnvChanges = append(p.EnvChanges, summary)
		}
		sort.Slice(p.EnvChanges, func(i, j int) bool {
			if p.EnvChanges[i].Scope != p.EnvChanges[j].Scope {
				return p.EnvChanges[i].Scope < p.EnvChanges[j].Scope
			}
			return p.EnvChanges[i].Name < p.EnvChanges[j].Name
		})
	}
	if !req.UseComposeLimits {
		live, err := e.readLive(ctx, target, req.Project)
		if err != nil {
			return fmt.Errorf("%w; the destination must be reachable to keep its CPU/memory limits", err)
		}
		known := map[string]bool{}
		for name := range services {
			known[name] = true
		}
		sel := map[string]bool{}
		for _, name := range p.Services {
			sel[name] = true
		}
		for _, pin := range pinsFor(b.Limits, live, sel, req.Service == "", false, known) {
			if sel[pin.Service] {
				p.PreservedLimits = append(p.PreservedLimits, pin)
			}
		}
		if len(p.PreservedLimits) > 0 {
			p.Warnings = append(p.Warnings, "CPU/memory limits changed on the server are kept; tick 'use limits from compose' to reset them.")
		}
	} else {
		p.Warnings = append(p.Warnings, "Limits from the Compose file will replace any limits set manually on the server.")
	}
	return nil
}

func envScopeDiff(current, applied map[string][]byte) map[string][]domain.EnvChangeSummary {
	out := map[string][]domain.EnvChangeSummary{}
	scopes := map[string]bool{}
	for scope := range current {
		scopes[scope] = true
	}
	for scope := range applied {
		scopes[scope] = true
	}
	for scope := range scopes {
		cur := map[string]string{}
		for _, v := range parseEnvDoc(current[scope]).variables() {
			cur[v.Name] = v.Value
		}
		old := map[string]string{}
		for _, v := range parseEnvDoc(applied[scope]).variables() {
			old[v.Name] = v.Value
		}
		for name, value := range cur {
			if before, ok := old[name]; !ok {
				out[scope] = append(out[scope], domain.EnvChangeSummary{Scope: scope, Name: name, Action: "set"})
			} else if before != value {
				out[scope] = append(out[scope], domain.EnvChangeSummary{Scope: scope, Name: name, Action: "set", Exists: true})
			}
		}
		for name := range old {
			if _, ok := cur[name]; !ok {
				out[scope] = append(out[scope], domain.EnvChangeSummary{Scope: scope, Name: name, Action: "delete", Exists: true})
			}
		}
		sort.Slice(out[scope], func(i, j int) bool { return out[scope][i].Name < out[scope][j].Name })
	}
	return out
}

func (e *Engine) prepareApply(ctx context.Context, target domain.ShipTarget, req domain.ShipRequest, p *domain.ShipPlan, b *bundle) error {
	if !domain.ValidProject(req.Project) || (req.Service != "" && !domain.ValidService(req.Service)) {
		return fmt.Errorf("invalid project or service")
	}
	if len(req.Env) > 0 || req.IncludeEnv || req.Image != "" {
		return fmt.Errorf("an env apply cannot carry edits or images")
	}
	dir := e.projectDir(target, req.Project)
	state, err := e.readState(ctx, target, dir)
	if err != nil {
		return err
	}
	if len(state.compose) == 0 {
		return fmt.Errorf("project is not deployed on the destination")
	}
	infos := state.serviceInfo()
	targets := []string{}
	for _, info := range infos {
		if req.Service != "" && info.Name != req.Service {
			continue
		}
		if info.OneShot {
			if req.Service != "" {
				return fmt.Errorf("one-shot services cannot be recreated by an env apply")
			}
			continue
		}
		targets = append(targets, info.Name)
	}
	if len(targets) == 0 {
		return fmt.Errorf("no eligible service to recreate")
	}
	p.Project = composeProject(target, req.Project)
	p.Directory = dir
	p.Services = targets
	diff := envScopeDiff(state.files, state.applied)
	affected := map[string]bool{}
	for _, info := range infos {
		for _, t := range targets {
			if t != info.Name {
				continue
			}
			if !state.hasApply || len(diff[info.Name]) > 0 || (info.UsesProjectEnv && len(diff[""]) > 0) {
				affected[t] = true
			}
		}
	}
	if state.hasApply {
		for _, items := range diff {
			p.EnvChanges = append(p.EnvChanges, items...)
		}
	}
	if !state.hasApply {
		for scope, content := range state.files {
			for _, v := range parseEnvDoc(content).variables() {
				p.EnvChanges = append(p.EnvChanges, domain.EnvChangeSummary{Scope: scope, Name: v.Name, Action: "set", Exists: true})
			}
		}
		p.Warnings = append(p.Warnings, "No previous apply record exists; every variable is listed because changes cannot be told apart.")
	}
	sort.Slice(p.EnvChanges, func(i, j int) bool {
		if p.EnvChanges[i].Scope != p.EnvChanges[j].Scope {
			return p.EnvChanges[i].Scope < p.EnvChanges[j].Scope
		}
		return p.EnvChanges[i].Name < p.EnvChanges[j].Name
	})
	for _, t := range targets {
		if affected[t] {
			p.Affected = append(p.Affected, t)
		}
	}
	if len(p.Affected) == 0 {
		p.Warnings = append(p.Warnings, "No pending variable changes were found for the selected services; Compose will recreate only containers whose configuration changed.")
	}
	p.Warnings = append(p.Warnings, "Compose recreates only containers whose configuration actually changed; if the apply fails the previous variable files are restored.")
	b.Limits = map[string]domain.LimitSet{}
	b.Styles = map[string]domain.LimitStyle{}
	known := map[string]bool{}
	for name, svc := range state.services {
		known[name] = true
		b.Limits[name], b.Styles[name] = domain.ComposeLimits(svc)
	}
	live, err := e.readLive(ctx, target, req.Project)
	if err != nil {
		return err
	}
	sel := map[string]bool{}
	for _, t := range targets {
		sel[t] = true
	}
	for _, pin := range pinsFor(b.Limits, live, sel, false, false, known) {
		if sel[pin.Service] {
			p.PreservedLimits = append(p.PreservedLimits, pin)
		}
	}
	b.Services = targets
	b.Revision = state.revision()
	for _, info := range infos {
		if sel[info.Name] {
			if info.UsesProjectEnv {
				b.Restore = append(b.Restore, ".env")
			}
			if info.HasOverride {
				b.Restore = append(b.Restore, relOfScope(info.Name))
			}
		}
	}
	b.Restore = uniqueSorted(b.Restore)
	return nil
}

func uniqueSorted(in []string) []string {
	set := map[string]bool{}
	for _, s := range in {
		set[s] = true
	}
	out := make([]string, 0, len(set))
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
