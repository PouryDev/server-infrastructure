package main

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"text/template"
)

const (
	defaultKitModule  = "github.com/PouryDev/server-infrastructure/gokit"
	defaultGoImage    = "docker.arvancloud.ir/library/golang:1.26-alpine"
	defaultDBImage    = "docker.arvancloud.ir/postgres:18.6-alpine"
	defaultLintImage  = "docker.arvancloud.ir/golangci/golangci-lint:latest-alpine"
	defaultRedisImage = "docker.arvancloud.ir/redis:8.10.3-alpine"
)

//go:embed all:templates
var templatesFS embed.FS

// Options describe the project to create.
type Options struct {
	Dir        string
	Name       string // default: base name of Dir
	Module     string
	KitMode    string // local | remote
	KitSrc     string
	KitModule  string
	KitVersion string
	GoImage    string
	DBImage    string
	LintImage  string
	RedisImage string
}

// templateData is what the .tmpl files see (delimiters are <% and %> so YAML, Makefile and shell
// syntax in the templates stay untouched).
type templateData struct {
	Name, Module, KitModule, KitVersion string
	GoImage, DBImage, LintImage         string
	RedisImage                          string
	GoVersion                           string
	LocalKit                            bool
}

var (
	nameRe   = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)
	moduleRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._~-]*(/[a-zA-Z0-9._~-]+)*$`)
)

// Generate renders the project into o.Dir, which must not exist or be empty.
func Generate(o *Options) error {
	o.Dir = filepath.Clean(o.Dir)
	if o.Name == "" {
		o.Name = filepath.Base(o.Dir)
	}
	if !nameRe.MatchString(o.Name) {
		return fmt.Errorf("project name %q must match %s, the same rule as `make new-project`", o.Name, nameRe)
	}
	if o.Module == "" {
		o.Module = "github.com/PouryDev/" + o.Name
	}
	if !moduleRe.MatchString(o.Module) {
		return fmt.Errorf("invalid module path %q", o.Module)
	}
	if o.RedisImage == "" {
		o.RedisImage = defaultRedisImage
	}
	switch o.KitMode {
	case "local":
		if o.KitSrc == "" {
			return fmt.Errorf("--kit-src is required in local mode")
		}
		mod, err := moduleOf(filepath.Join(o.KitSrc, "go.mod"))
		if err != nil {
			return err
		}
		if o.KitModule == "" {
			o.KitModule = mod
		}
	case "remote":
		if o.KitModule == "" {
			o.KitModule = defaultKitModule
		}
	default:
		return fmt.Errorf("--kit-mode must be local or remote")
	}
	if entries, err := os.ReadDir(o.Dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("%s is not empty", o.Dir)
	}

	data := templateData{
		Name: o.Name, Module: o.Module, KitModule: o.KitModule, KitVersion: o.KitVersion,
		GoImage: o.GoImage, DBImage: o.DBImage, LintImage: o.LintImage, RedisImage: o.RedisImage,
		GoVersion: goMinor(runtime.Version()), LocalKit: o.KitMode == "local",
	}

	if err := os.MkdirAll(o.Dir, 0o755); err != nil {
		return err
	}
	if err := renderTemplates(o.Dir, data); err != nil {
		return err
	}
	if err := writeGoMod(o, data); err != nil {
		return err
	}
	if data.LocalKit {
		if err := copyKit(o.KitSrc, filepath.Join(o.Dir, ".kit")); err != nil {
			return err
		}
	}
	return nil
}

func renderTemplates(dst string, data templateData) error {
	return fs.WalkDir(templatesFS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(p, "templates"), "/")
		if rel == "" {
			return nil
		}
		target := filepath.Join(dst, filepath.FromSlash(strings.TrimSuffix(rel, ".tmpl")))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := templatesFS.ReadFile(p)
		if err != nil {
			return err
		}
		out := raw
		if strings.HasSuffix(rel, ".tmpl") {
			t, err := template.New(path.Base(rel)).Delims("<%", "%>").Option("missingkey=error").Parse(string(raw))
			if err != nil {
				return fmt.Errorf("template %s: %w", rel, err)
			}
			var buf bytes.Buffer
			if err := t.Execute(&buf, data); err != nil {
				return fmt.Errorf("template %s: %w", rel, err)
			}
			out = buf.Bytes()
		}
		if strings.HasSuffix(target, ".go") { // generated code is always gofmt-clean, whatever the template spacing
			if out, err = format.Source(out); err != nil {
				return fmt.Errorf("format %s: %w", rel, err)
			}
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(target, ".sh") {
			mode = 0o755
		}
		return os.WriteFile(target, out, mode)
	})
}

func writeGoMod(o *Options, data templateData) error {
	var b strings.Builder
	fmt.Fprintf(&b, "module %s\n\ngo %s\n\n", o.Module, data.GoVersion)
	if data.LocalKit {
		fmt.Fprintf(&b, "require %s v0.0.0\n\nreplace %s => ./.kit\n", o.KitModule, o.KitModule)
	} else {
		fmt.Fprintf(&b, "require %s %s\n", o.KitModule, o.KitVersion)
	}
	return os.WriteFile(filepath.Join(o.Dir, "go.mod"), []byte(b.String()), 0o644)
}

// copyKit copies the kit's library sources (no tests, no commands) so a project can build without
// the kit being published anywhere.
func copyKit(src, dst string) error {
	if _, err := moduleOf(filepath.Join(src, "go.mod")); err != nil {
		return err
	}
	if err := os.RemoveAll(dst); err != nil {
		return err
	}
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			if rel == "cmd" || strings.HasPrefix(d.Name(), ".") && rel != "." {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		name := d.Name()
		keep := name == "go.mod" || name == "go.sum" || (strings.HasSuffix(name, ".go") && !strings.HasSuffix(name, "_test.go"))
		if !keep {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o644)
	})
}

var moduleLineRe = regexp.MustCompile(`(?m)^module\s+(\S+)\s*$`)

func moduleOf(goMod string) (string, error) {
	b, err := os.ReadFile(goMod)
	if err != nil {
		return "", fmt.Errorf("kit source: %w", err)
	}
	m := moduleLineRe.FindSubmatch(b)
	if m == nil {
		return "", fmt.Errorf("%s has no module line", goMod)
	}
	return string(m[1]), nil
}

func goMinor(v string) string { // go1.26.10 -> 1.26
	v = strings.TrimPrefix(v, "go")
	parts := strings.Split(v, ".")
	if len(parts) >= 2 {
		return parts[0] + "." + parts[1]
	}
	return v
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}
