package metrics

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

type Prometheus struct {
	URL  string
	HTTP *http.Client
}

func New(url string) *Prometheus {
	return &Prometheus{URL: strings.TrimRight(url, "/"), HTTP: &http.Client{Timeout: 5 * time.Second}}
}

type query struct{ name, unit, label, expression string }

var queries = []query{
	{"postgres_connections", "connections", "datname", `sum by (datname) (pg_stat_database_numbackends{job="postgres"})`},
	{"postgres_storage", "bytes", "datname", `sum by (datname) (pg_database_size_bytes{job="postgres"})`},
	{"postgres_transactions", "ops/s", "datname", `sum by (datname) (rate(pg_stat_database_xact_commit{job="postgres"}[5m]) + rate(pg_stat_database_xact_rollback{job="postgres"}[5m]))`},
	{"rabbitmq_messages", "messages", "vhost", `sum by (vhost) (rabbitmq_queue_messages{vhost!=""})`},
	{"rabbitmq_memory", "bytes", "vhost", `sum by (vhost) (rabbitmq_queue_process_memory_bytes{vhost!=""})`},
	{"redis_operations", "ops/s", "job", `sum by (job) (rate(db_client_operation_duration_seconds_count{db_system_name="redis"}[5m]))`},
	{"requests", "req/s", "job", `sum by (job) (rate(http_server_request_duration_seconds_count[5m]))`},
	{"uptime", "ratio", "project", `min by (project) (probe_success{project!=""})`},
}

func (p *Prometheus) Collect(ctx context.Context) ([]domain.Metric, error) {
	var mu sync.Mutex
	var wg sync.WaitGroup
	var failures []error
	result := []domain.Metric{}
	for _, q := range queries {
		wg.Add(1)
		go func(q query) {
			defer wg.Done()
			metrics, err := p.query(ctx, q)
			mu.Lock()
			defer mu.Unlock()
			result = append(result, metrics...)
			if err != nil {
				failures = append(failures, err)
			}
		}(q)
	}
	wg.Wait()
	return result, errors.Join(failures...)
}

func (p *Prometheus) query(ctx context.Context, q query) ([]domain.Metric, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", p.URL+"/api/v1/query?query="+url.QueryEscape(q.expression), nil)
	if err != nil {
		return nil, err
	}
	res, err := p.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("prometheus status %d", res.StatusCode)
	}
	var body struct {
		Status string
		Data   struct {
			ResultType string
			Result     []struct {
				Metric map[string]string
				Value  []json.RawMessage
			}
		}
	}
	if err = json.NewDecoder(io.LimitReader(res.Body, 2<<20)).Decode(&body); err != nil {
		return nil, err
	}
	if body.Status != "success" || body.Data.ResultType != "vector" {
		return nil, fmt.Errorf("invalid prometheus result")
	}
	result := []domain.Metric{}
	for _, row := range body.Data.Result {
		if len(row.Value) != 2 {
			return nil, fmt.Errorf("invalid prometheus sample")
		}
		var raw string
		if err = json.Unmarshal(row.Value[1], &raw); err != nil {
			return nil, err
		}
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		project := row.Metric[q.label]
		switch q.label {
		case "datname":
			project = strings.ReplaceAll(project, "_", "-")
		case "vhost":
			project = strings.TrimPrefix(project, "/")
		case "job":
			project, _, _ = strings.Cut(project, "/")
		}
		if project != "" {
			result = append(result, domain.Metric{Project: project, Name: q.name, Value: value, Unit: q.unit})
		}
	}
	return result, nil
}
