package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PouryDev/server-infrastructure/manager/internal/application"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type resourceAPIRunner struct{ calls int }

func (r *resourceAPIRunner) Preview(context.Context, domain.ResourceRequest) (domain.ResourcePreview, error) {
	r.calls++
	return domain.ResourcePreview{Revision: strings.Repeat("a", 64)}, nil
}
func (r *resourceAPIRunner) Apply(context.Context, domain.ResourceRequest) (domain.ResourceResult, error) {
	r.calls++
	return domain.ResourceResult{Applied: true}, nil
}

type resourceAPIAudit struct{}

func (resourceAPIAudit) Record(context.Context, domain.Event) error { return nil }
func TestResourceAPIRequiresSessionOriginCSRFAndReviewedRevision(t *testing.T) {
	runner := &resourceAPIRunner{}
	handler, _ := newServer(t, &application.Resources{Runner: runner, Repo: resourceAPIAudit{}})
	body := `{"project":"shop","service":"api","memoryMiB":512,"cpus":1,"gitMode":"local","revision":"` + strings.Repeat("a", 64) + `"}`
	if got := call(handler, "POST", "/api/resources/apply", body, nil, "", "http://manager.localhost").Code; got != 401 {
		t.Fatal(got)
	}
	login := call(handler, "POST", "/api/login", `{"username":"admin","password":"test-password"}`, nil, "", "http://manager.localhost")
	cookie := login.Result().Cookies()[0]
	var sess map[string]string
	json.Unmarshal(login.Body.Bytes(), &sess)
	if got := call(handler, "POST", "/api/resources/apply", body, cookie, "", "http://manager.localhost").Code; got != 403 {
		t.Fatal(got)
	}
	if got := call(handler, "POST", "/api/resources/apply", body, cookie, sess["csrf"], "https://other.example").Code; got != 403 {
		t.Fatal(got)
	}
	if got := call(handler, "POST", "/api/resources/apply", strings.Replace(body, strings.Repeat("a", 64), "", 1), cookie, sess["csrf"], "http://manager.localhost").Code; got != 422 {
		t.Fatal(got)
	}
	if runner.calls != 0 {
		t.Fatal("unauthorized request reached runner")
	}
	if got := call(handler, "POST", "/api/resources/apply", body, cookie, sess["csrf"], "http://manager.localhost").Code; got != 200 || runner.calls != 1 {
		t.Fatal(got, runner.calls)
	}
}
