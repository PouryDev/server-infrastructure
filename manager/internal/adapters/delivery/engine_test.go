package delivery

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

func TestRemoteArgumentsAreQuotedAndHostKeysAreMandatory(t *testing.T) {
	value := "a' $(touch /tmp/should-not-exist)"
	quoted := remoteCommand([]string{"printf", "%s", value})
	if !strings.Contains(quoted, `'"'"'`) {
		t.Fatal("single quote not escaped")
	}
	dir := t.TempDir()
	e := &Engine{KeysRoot: dir}
	target := domain.ShipTarget{Mode: "ssh", Host: "127.0.0.1", User: "deploy", Port: 2222, Identity: "id_ed25519", InfraDir: "/srv/infrastructure", AppsDir: "/srv/apps", DataDir: "/srv/data"}
	os.WriteFile(filepath.Join(dir, target.Identity), []byte("test"), 0600)
	if _, err := e.destination(context.Background(), target, "docker", "version"); err == nil {
		t.Fatal("SSH allowed missing known_hosts")
	}
	os.WriteFile(filepath.Join(dir, "known_hosts"), []byte("test"), 0600)
	cmd, err := e.destination(context.Background(), target, "printf", "%s", value)
	if err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cmd.Args, " ")
	if !strings.Contains(args, "StrictHostKeyChecking=yes") || !strings.Contains(args, "BatchMode=yes") || strings.Contains(args, "accept-new") {
		t.Fatal("unsafe SSH options")
	}
}
func TestBundleRejectsSymlinksAndNeverCopiesSSHKeys(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "file"), []byte("config"), 0600)
	os.Symlink("file", filepath.Join(dir, "alias"))
	e := &Engine{}
	if e.addFiles(dir, "alias", &bundle{}) == nil {
		t.Fatal("symlink accepted")
	}
	if e.addFiles(dir, "../escape", &bundle{}) == nil {
		t.Fatal("parent path accepted")
	}
	os.MkdirAll(filepath.Join(dir, "manager/ssh"), 0700)
	os.WriteFile(filepath.Join(dir, "manager/ssh/id_ed25519"), []byte("secret"), 0600)
	if e.addFiles(dir, "manager/ssh", &bundle{}) == nil {
		t.Fatal("private SSH key entered bundle")
	}
	os.WriteFile(filepath.Join(dir, ".env"), []byte("PRIVATE=value"), 0600)
	if e.addFiles(dir, ".env", &bundle{}) == nil {
		t.Fatal("env file shipped without explicit opt-in")
	}
	if err := e.addFiles(dir, ".env", &bundle{AllowEnv: true}); err != nil {
		t.Fatal(err)
	}
}
func TestBindingsSeparateDataAndPreserveDestinationEnv(t *testing.T) {
	dir := t.TempDir()
	e := &Engine{HostDataRoot: "/local/data"}
	target := domain.ShipTarget{InfraDir: "/destination/infra", AppsDir: "/destination/apps", DataDir: "/destination/data"}
	svc := map[string]any{"volumes": []any{map[string]any{"type": "bind", "source": "/local/data/shop/uploads", "target": "/uploads"}}, "env_file": []any{map[string]any{"path": ".env"}}}
	b := bundle{}
	if err := e.bindings(dir, "/destination/apps/shop", target, svc, svc, false, &b); err != nil {
		t.Fatal(err)
	}
	if text(object(list(svc["volumes"])[0])["source"]) != "/destination/data/shop/uploads" {
		t.Fatal("local data path leaked")
	}
	if text(object(list(svc["env_file"])[0])["path"]) != "/destination/apps/shop/.env" || len(b.Files) != 0 {
		t.Fatal("destination env not preserved")
	}
}
