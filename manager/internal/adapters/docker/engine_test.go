package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiscoveryProtectsInfrastructureAndJobs(t *testing.T) {
	for _, tc := range []struct {
		project, service, network, oneoff, optout string
		managed                                   bool
	}{
		{"shop", "shop-api", "infra", "", "", true}, {"shop", "shop-web", "infra", "", "", true}, {"shop", "shop-migrate", "infra", "", "", false},
		{"shop", "worker", "infra", "True", "", false}, {"shop", "web", "infra", "", "false", false}, {"infra", "manager", "infra", "", "", false},
		{"unrelated", "api", "other", "", "", false},
	} {
		t.Run(tc.project+"/"+tc.service+tc.oneoff+tc.optout, func(t *testing.T) {
			e := New("unused", "infra", "infra")
			c := rawContainer{Labels: map[string]string{projectLabel: tc.project, serviceLabel: tc.service, "com.docker.compose.oneoff": tc.oneoff, "infra.manager.managed": tc.optout}}
			c.NetworkSettings.Networks = map[string]json.RawMessage{tc.network: nil}
			if e.managed(c) != tc.managed {
				t.Fatal("incorrect management boundary")
			}
		})
	}
}

func TestMutationsAreRecheckedAndCannotDelete(t *testing.T) {
	id := strings.Repeat("a", 64)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			fmt.Fprintf(w, `[{"Id":%q,"Labels":{"com.docker.compose.project":"infra","com.docker.compose.service":"postgres"},"NetworkSettings":{"Networks":{"infra":{}}}}]`, id)
			return
		}
		calls++
		w.WriteHeader(204)
	}))
	defer server.Close()
	e := New("unused", "infra", "infra")
	e.client = &http.Client{Transport: rewrite{URL: server.URL}}
	if e.Stop(context.Background(), id) == nil {
		t.Fatal("infra stop accepted")
	}
	if e.Start(context.Background(), "../postgres") == nil {
		t.Fatal("invalid ID accepted")
	}
	if calls != 0 {
		t.Fatal("protected mutation reached engine")
	}
}

type rewrite struct{ URL string }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.URL.Host = strings.TrimPrefix(r.URL, "http://")
	return http.DefaultTransport.RoundTrip(cloned)
}

func TestStatsUseCPUAndWorkingSetAndNetworkTotals(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"cpu_stats":{"cpu_usage":{"total_usage":250},"system_cpu_usage":2000,"online_cpus":4},"precpu_stats":{"cpu_usage":{"total_usage":200},"system_cpu_usage":1000},"memory_stats":{"usage":1000,"limit":2000,"stats":{"inactive_file":100}},"networks":{"eth0":{"rx_bytes":10,"tx_bytes":20},"eth1":{"rx_bytes":30,"tx_bytes":40}}}`)
	}))
	defer server.Close()
	e := New("unused", "infra", "infra")
	e.client = &http.Client{Transport: rewrite{URL: server.URL}}
	// Exercise the stats decoder with sanitized domain metadata.
	c := safeContainer()
	if err := e.stats(context.Background(), &c); err != nil {
		t.Fatal(err)
	}
	if c.CPU == nil || *c.CPU != 20 || c.Memory == nil || *c.Memory != 900 || *c.NetworkRX != 40 || *c.NetworkTX != 60 {
		t.Fatalf("wrong stats: %+v", c)
	}
}
