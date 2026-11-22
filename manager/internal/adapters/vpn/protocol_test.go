package vpn

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type protocolRunner struct{ changes int }

func (*protocolRunner) Status(context.Context) (domain.VPNStatus, error) {
	return domain.VPNStatus{Configured: true}, nil
}
func (p *protocolRunner) Change(context.Context, domain.VPNChange) (domain.VPNStatus, error) {
	p.changes++
	return domain.VPNStatus{}, nil
}
func (*protocolRunner) Test(context.Context) error { return nil }
func TestBrokerAuthenticationAndStrictRequest(t *testing.T) {
	token := strings.Repeat("a", 32)
	runner := &protocolRunner{}
	server := httptest.NewServer(Handler(runner, token))
	defer server.Close()
	if _, err := New(server.URL, "wrong").Status(context.Background()); err == nil {
		t.Fatal("unauthenticated broker accepted")
	}
	if _, err := New(server.URL, token).Status(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"link":"secret","unknown":true}`, `{} {}`, strings.Repeat("x", 21<<10)} {
		req, _ := http.NewRequest("PUT", server.URL+"/vpn", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 400 {
			t.Fatal(res.StatusCode)
		}
	}
	if runner.changes != 0 {
		t.Fatal("invalid request reached core")
	}
}
