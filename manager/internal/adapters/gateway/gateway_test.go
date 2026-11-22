package gateway

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type runtime struct{ stopped string }

func (r *runtime) List(context.Context) ([]domain.Container, error) { return []domain.Container{}, nil }
func (r *runtime) Stop(_ context.Context, id string) error          { r.stopped = id; return nil }
func (r *runtime) Start(context.Context, string) error              { return nil }
func TestGatewayRejectsUnauthenticatedAndArbitraryDockerOperations(t *testing.T) {
	token := "01234567890123456789012345678901"
	rt := &runtime{}
	handler := Handler(rt, token)
	for _, tc := range []struct {
		method, path, token string
		code                int
	}{
		{"GET", "/containers", "", 401}, {"GET", "/containers", token, 200}, {"POST", "/containers/a/stop", token, 204},
		{"POST", "/containers/create", token, 404}, {"DELETE", "/containers/a", token, 404}, {"POST", "/containers/a/exec", token, 404},
	} {
		t.Run(tc.method+tc.path+tc.token, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+tc.token)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != tc.code {
				t.Fatalf("%s %s: %d", tc.method, tc.path, response.Code)
			}
		})
	}
	if rt.stopped != "a" {
		t.Fatal("allowed stop not forwarded")
	}
}
