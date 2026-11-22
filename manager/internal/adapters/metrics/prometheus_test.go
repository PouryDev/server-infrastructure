package metrics

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAttributionAndNonFiniteSamples(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"datname":"reels_chi"},"value":[1,"1024"]},{"metric":{"datname":"other"},"value":[1,"NaN"]}]}}`)
	}))
	defer server.Close()
	rows, err := New(server.URL).query(context.Background(), queries[1])
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Project != "reels-chi" || rows[0].Value != 1024 {
		t.Fatalf("bad attribution: %+v", rows)
	}
}
func TestMalformedMonitoringIsUnavailableNotZero(t *testing.T) {
	for _, body := range []string{`{"status":"error"}`, `{"status":"success","data":{"resultType":"vector","result":[{"value":[1]}]}}`, `not-json`} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) }))
			defer server.Close()
			if _, err := New(server.URL).query(context.Background(), queries[0]); err == nil {
				t.Fatal("malformed metric accepted")
			}
		})
	}
}
