package builder

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PouryDev/server-infrastructure/manager/internal/adapters/buildsrc"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

func engineFor(t *testing.T) *Engine {
	t.Helper()
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "ship.conf"), []byte("APP=shop\nIMAGES=(\"api=.\")\nBUILD_ARGS=(\"--build-arg\" \"VITE_A\")\n"), 0o644)
	os.MkdirAll(filepath.Join(root, "deploy"), 0o755)
	os.WriteFile(filepath.Join(root, "deploy", "compose.yaml"), []byte("services: {}\n"), 0o644)
	registry, err := buildsrc.Load(`{"shop":{"root":"` + root + `"}}`)
	if err != nil {
		t.Fatal(err)
	}
	return &Engine{Registry: registry}
}

func TestBuildRejectsUnexpectedArgumentsStaleTreesAndBadTagsBeforeDocker(t *testing.T) {
	e := engineFor(t)
	progress := func(domain.ShipProgress) error { return nil }
	ctx := context.Background()
	if _, err := e.Build(ctx, domain.BuildRequest{Source: "shop", Tag: "abc", Args: map[string]string{"SURPRISE": "x"}}, progress); err == nil || !strings.Contains(err.Error(), "unexpected build argument") {
		t.Fatalf("%v", err)
	}
	if _, err := e.Build(ctx, domain.BuildRequest{Source: "shop", Tag: "bad tag"}, progress); err == nil {
		t.Fatal("invalid tag accepted")
	}
	if _, err := e.Build(ctx, domain.BuildRequest{Source: "shop", Tag: "abc", Head: "deadbeef00"}, progress); err == nil || !strings.Contains(err.Error(), "working tree changed") {
		t.Fatalf("a tree that changed since the preview must be rebuilt from a new preview: %v", err)
	}
	if _, err := e.Build(ctx, domain.BuildRequest{Source: "nope", Tag: "abc"}, progress); err == nil {
		t.Fatal("unregistered source built")
	}
	if _, err := e.Build(ctx, domain.BuildRequest{Source: "shop", Tag: "abc", Services: []string{"ghost"}}, progress); err == nil {
		t.Fatal("service outside the spec built")
	}
}
func TestSourcesDescribeSpecWithoutSecrets(t *testing.T) {
	list, err := engineFor(t).Sources(context.Background())
	if err != nil || len(list) != 1 || list[0].App != "shop" || list[0].BuildArgNames[0] != "VITE_A" || list[0].Platform != "linux/amd64" || list[0].Images[0].Service != "api" {
		t.Fatalf("%+v %v", list, err)
	}
}
func TestBuilderProtocolRequiresBearerToken(t *testing.T) {
	token := strings.Repeat("t", 32)
	h := Handler(engineFor(t), token)
	for _, tc := range []struct {
		method, path, auth string
		want               int
	}{
		{"GET", "/healthz", "", 200},
		{"GET", "/builder/sources", "", 401},
		{"GET", "/builder/sources", "Bearer wrong", 401},
		{"GET", "/builder/sources", "Bearer " + token, 200},
	} {
		req := httptest.NewRequest(tc.method, tc.path, nil)
		if tc.auth != "" {
			req.Header.Set("Authorization", tc.auth)
		}
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)
		if res.Code != tc.want {
			t.Errorf("%s %s auth=%q: %d", tc.method, tc.path, tc.auth, res.Code)
		}
	}
	req := httptest.NewRequest("POST", "/builder/build", strings.NewReader(`{"source":"shop","tag":"x","unknown":1}`))
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("unknown JSON fields accepted: %d", res.Code)
	}
}
func TestRedactionAndProxyHandling(t *testing.T) {
	if got := redact("token=abcd1234 ok", map[string]string{"A": "abcd1234", "B": "x"}); got != "token=*** ok" {
		t.Fatal(got)
	}
	if len(proxyArgs("")) != 0 || len(proxyArgs("http://127.0.0.1:2020")) != 8 {
		t.Fatal("proxy arguments")
	}
}
