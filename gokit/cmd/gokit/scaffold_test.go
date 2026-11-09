package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeKit(t *testing.T) string {
	t.Helper()
	kit := t.TempDir()
	for name, body := range map[string]string{
		"go.mod":                "module example.com/kit\n\ngo 1.26\n",
		"apperr/apperr.go":      "package apperr\n",
		"apperr/apperr_test.go": "package apperr\n",
		"cmd/gokit/main.go":     "package main\n",
		"README.md":             "ignored",
	} {
		p := filepath.Join(kit, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return kit
}

func TestGenerateLocalKit(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "shop")
	o := &Options{Dir: dir, Module: "example.com/shop", KitMode: "local", KitSrc: writeKit(t),
		GoImage: "go:img", DBImage: "pg:img", LintImage: "lint:img"}
	if err := Generate(o); err != nil {
		t.Fatal(err)
	}

	mustExist := []string{
		"go.mod", "Makefile", "Dockerfile", "compose.dev.yaml", "deploy/compose.yaml", ".env.example", ".gitignore",
		"cmd/api/main.go", "cmd/migrate/main.go", "internal/domain/user/user.go", "internal/usecase/account/service.go",
		"internal/adapter/postgres/user_repository.go", "internal/adapter/postgres/models.go", "internal/adapter/httpapi/router.go", "internal/bootstrap/app.go",
		"internal/architecture_test.go", "migrations/embed.go", "migrations/20260101000000_create_users.sql",
		".kit/go.mod", ".kit/apperr/apperr.go", "ship.conf", "README.md",
	}
	for _, f := range mustExist {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	for _, f := range []string{".kit/cmd/gokit/main.go", ".kit/apperr/apperr_test.go", ".kit/README.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err == nil {
			t.Errorf("%s must not be copied into the project", f)
		}
	}

	gomod, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	if !strings.Contains(string(gomod), "module example.com/shop") || !strings.Contains(string(gomod), "replace example.com/kit => ./.kit") {
		t.Fatalf("go.mod:\n%s", gomod)
	}

	// Every template must be fully rendered.
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.Contains(p, ".kit") {
			return err
		}
		b, _ := os.ReadFile(p)
		for _, bad := range []string{"<%", "%>", "<no value>"} {
			if strings.Contains(string(b), bad) {
				t.Errorf("%s still contains %q", p, bad)
			}
		}
		return nil
	})
	if b, _ := os.ReadFile(filepath.Join(dir, "cmd/api/main.go")); !strings.Contains(string(b), `"example.com/kit/httpx"`) ||
		!strings.Contains(string(b), `"example.com/shop/internal/bootstrap"`) {
		t.Errorf("imports not rendered:\n%s", b)
	}
}

func TestGenerateValidation(t *testing.T) {
	kit := writeKit(t)
	base := t.TempDir()

	for name, o := range map[string]Options{
		"bad name":        {Dir: filepath.Join(base, "Bad_Name"), KitMode: "remote"},
		"local w/o src":   {Dir: filepath.Join(base, "a1"), KitMode: "local"},
		"bad mode":        {Dir: filepath.Join(base, "a2"), KitMode: "x"},
		"bad module path": {Dir: filepath.Join(base, "a3"), KitMode: "remote", Module: "bad module;rm"},
	} {
		o := o
		if err := Generate(&o); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}

	// refuses a non-empty directory
	dir := filepath.Join(base, "full")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "x"), nil, 0o644)
	if err := Generate(&Options{Dir: dir, KitMode: "local", KitSrc: kit}); err == nil {
		t.Error("non-empty directory must be refused")
	}

	// remote mode needs no kit source
	o := &Options{Dir: filepath.Join(base, "remote-app"), KitMode: "remote", KitVersion: "v0.2.0", GoImage: "g", DBImage: "d", LintImage: "l"}
	if err := Generate(o); err != nil {
		t.Fatal(err)
	}
	gomod, _ := os.ReadFile(filepath.Join(o.Dir, "go.mod"))
	if !strings.Contains(string(gomod), defaultKitModule+" v0.2.0") || strings.Contains(string(gomod), "replace") {
		t.Fatalf("go.mod:\n%s", gomod)
	}
	if _, err := os.Stat(filepath.Join(o.Dir, ".kit")); err == nil {
		t.Fatal("remote mode must not vendor the kit")
	}
}
