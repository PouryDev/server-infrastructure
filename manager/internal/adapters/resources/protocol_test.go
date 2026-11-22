package resources

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type protocolRunner struct{ called bool }

func (p *protocolRunner) Preview(context.Context, domain.ResourceRequest) (domain.ResourcePreview, error) {
	p.called = true
	return domain.ResourcePreview{}, nil
}
func (p *protocolRunner) Apply(context.Context, domain.ResourceRequest) (domain.ResourceResult, error) {
	p.called = true
	return domain.ResourceResult{Applied: true}, nil
}
func TestRunnerProtocolAuthenticationAndStrictInput(t *testing.T) {
	engine := &protocolRunner{}
	token := strings.Repeat("t", 32)
	server := httptest.NewServer(Handler(engine, token))
	defer server.Close()
	body := `{"project":"shop","service":"shop-api","memoryMiB":512,"cpus":1,"gitMode":"local","revision":""}`
	for _, tt := range []struct {
		token, body string
		want        int
	}{{"", body, 401}, {token, body[:len(body)-1] + `,"command":"rm"}`, 400}, {token, body + "{}", 400}, {token, strings.Replace(body, "512", "0", 1), 400}} {
		req, _ := http.NewRequest("POST", server.URL+"/resources/apply", strings.NewReader(tt.body))
		req.Header.Set("Authorization", "Bearer "+tt.token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != tt.want || engine.called {
			t.Fatalf("status=%d called=%t", res.StatusCode, engine.called)
		}
	}
	client := New(server.URL, token)
	if _, err := client.Preview(context.Background(), resizeRequest()); err != nil || !engine.called {
		t.Fatal(err)
	}
}
