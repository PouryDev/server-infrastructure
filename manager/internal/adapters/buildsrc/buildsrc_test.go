package buildsrc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const gapchi = `# comment
APP=gapchi
SSH_HOST=${BODYAI_SSH_HOST:-deploy@CHANGE-ME}
REMOTE_DIR=$HOME/infra-local/apps/gapchi
COMPOSE_FILE=deploy/compose.yaml
IMAGES=("api=." "web=./client" "ssr=./client:./client/Dockerfile.ssr")
BUILD_ARGS=("--network" "host" "--build-arg" "VITE_A" "--build-arg" "MODE=prod")
`

func TestParseSpecReadsShipConfWithoutExecutingShell(t *testing.T) {
	spec, err := ParseSpec(gapchi)
	if err != nil {
		t.Fatal(err)
	}
	if spec.App != "gapchi" || spec.ComposeFile != "deploy/compose.yaml" || spec.Platform != "linux/amd64" || spec.KeepTags != 3 {
		t.Fatalf("%+v", spec)
	}
	if len(spec.Images) != 3 || spec.Images[2].Service != "ssr" || spec.Images[2].Context != "client" || spec.Images[2].Dockerfile != "client/Dockerfile.ssr" {
		t.Fatalf("%+v", spec.Images)
	}
	if spec.Network != "host" || len(spec.ArgNames) != 1 || spec.ArgNames[0] != "VITE_A" || spec.Literals["MODE"] != "prod" {
		t.Fatalf("%+v", spec)
	}
}
func TestParseSpecRejectsUnsafeInput(t *testing.T) {
	for name, src := range map[string]string{
		"expansion in needed key": "APP=$(whoami)\nIMAGES=(\"a=.\")",
		"traversal":               "APP=shop\nIMAGES=(\"a=../x\")",
		"absolute compose":        "APP=shop\nCOMPOSE_FILE=/etc/passwd\nIMAGES=(\"a=.\")",
		"bad option":              "APP=shop\nIMAGES=(\"a=.\")\nBUILD_ARGS=(\"--privileged\")",
		"bad network":             "APP=shop\nIMAGES=(\"a=.\")\nBUILD_ARGS=(\"--network\" \"container:x\")",
		"missing images":          "APP=shop",
		"bad app":                 "APP=Shop\nIMAGES=(\"a=.\")",
		"duplicate service":       "APP=shop\nIMAGES=(\"a=.\" \"a=./x\")",
	} {
		if _, err := ParseSpec(src); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
func TestRegistryAllowlistAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "api"), 0o755)
	os.WriteFile(filepath.Join(root, "api", "ship.conf"), []byte(gapchi), 0o644)
	outside := t.TempDir()
	os.Symlink(outside, filepath.Join(root, "link"))
	reg, err := Load(`{"bodyai":{"root":"` + root + `","subdir":"api"},"evil":{"root":"` + root + `","subdir":"link"}}`)
	if err != nil {
		t.Fatal(err)
	}
	src, err := reg.Resolve("bodyai")
	if err != nil {
		t.Fatal(err)
	}
	spec, err := src.LoadSpec()
	if err != nil || spec.App != "gapchi" {
		t.Fatal(err)
	}
	if _, err = reg.Resolve("evil"); err == nil {
		t.Fatal("symlinked subdirectory escaped the root")
	}
	if _, err = reg.Resolve("../etc"); err == nil {
		t.Fatal("unregistered source resolved")
	}
	for _, bad := range []string{`{"x":{"root":"relative"}}`, `{"Bad":{"root":"/a"}}`, `{"ok":{"root":"/a","subdir":"../b"}}`} {
		if _, err = Load(bad); err == nil {
			t.Errorf("accepted %s", bad)
		}
	}
	if _, err = src.ContextPath("../x"); err == nil {
		t.Fatal("context escape accepted")
	}
}
func TestTagFollowsShipScript(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	if MakeTag("abc1234567", false, now) != "abc1234567" || MakeTag("abc1234567", true, now) != "abc1234567-dirty1700000000" || !strings.HasPrefix(MakeTag("", false, now), "2023") {
		t.Fatal("tag format")
	}
	if !ValidTag("abc-dirty1") || ValidTag("a b") || ValidTag("-a") {
		t.Fatal("tag validation")
	}
}
