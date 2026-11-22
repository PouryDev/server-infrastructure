package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

const projectLabel = "com.docker.compose.project"
const serviceLabel = "com.docker.compose.service"

type Engine struct {
	client             *http.Client
	network, protected string
	statsMu            sync.Mutex
	previousCPU        map[string]cpuStats
}

func New(socket, network, protected string) *Engine {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}, MaxIdleConnsPerHost: 8}
	return &Engine{client: &http.Client{Transport: transport, Timeout: 35 * time.Second}, network: network, protected: protected, previousCPU: map[string]cpuStats{}}
}

func (e *Engine) call(ctx context.Context, method, path string, output any) error {
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+path, nil)
	if err != nil {
		return err
	}
	res, err := e.client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotModified {
		return nil
	}
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("docker %s: status %d", method, res.StatusCode)
	}
	if output != nil {
		return json.NewDecoder(io.LimitReader(res.Body, 16<<20)).Decode(output)
	}
	return nil
}

type rawContainer struct {
	ID              string `json:"Id"`
	Names           []string
	Labels          map[string]string
	State           string
	Status          string
	NetworkSettings struct{ Networks map[string]json.RawMessage }
}

func (e *Engine) eligible(c rawContainer) bool {
	_, connected := c.NetworkSettings.Networks[e.network]
	return connected && c.Labels[projectLabel] != "" && c.Labels[serviceLabel] != ""
}

func (e *Engine) managed(c rawContainer) bool {
	service := strings.ToLower(c.Labels[serviceLabel])
	for _, word := range []string{"migrate", "migration", "seed", "setup", "init"} {
		for _, part := range strings.FieldsFunc(service, func(r rune) bool { return r == '-' || r == '_' }) {
			if part == word {
				return false
			}
		}
	}
	return e.eligible(c) && c.Labels[projectLabel] != e.protected && c.Labels["infra.manager.managed"] != "false" && !strings.EqualFold(c.Labels["com.docker.compose.oneoff"], "true")
}

func (e *Engine) List(ctx context.Context) ([]domain.Container, error) {
	var raw []rawContainer
	if err := e.call(ctx, "GET", "/containers/json?all=1", &raw); err != nil {
		return nil, err
	}
	alive := make(map[string]bool, len(raw))
	for _, c := range raw {
		alive[c.ID] = true
	}
	e.statsMu.Lock()
	for id := range e.previousCPU {
		if !alive[id] {
			delete(e.previousCPU, id)
		}
	}
	e.statsMu.Unlock()
	result := []domain.Container{}
	for _, c := range raw {
		if !e.eligible(c) && !(c.Labels[projectLabel] == e.protected && c.Labels[serviceLabel] != "") {
			continue
		}
		name := c.Labels[serviceLabel]
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		health := ""
		for _, h := range []string{"unhealthy", "healthy", "starting"} {
			if strings.Contains(c.Status, "("+h+")") || strings.Contains(c.Status, "health: "+h) {
				health = h
				break
			}
		}
		result = append(result, domain.Container{ID: c.ID, Name: name, Project: c.Labels[projectLabel], Service: c.Labels[serviceLabel], State: c.State, Health: health, Managed: e.managed(c)})
	}
	// Include shared data/monitoring services as read-only infrastructure, too.
	sort.Slice(result, func(i, j int) bool { return result[i].Project+result[i].Name < result[j].Project+result[j].Name })
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for i := range result {
		if result[i].State != "running" && result[i].State != "paused" {
			continue
		}
		wg.Add(1)
		go func(c *domain.Container) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}
			statsCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			if err := e.stats(statsCtx, c); err != nil {
				c.Error = "Resource sample unavailable"
			}
		}(&result[i])
	}
	wg.Wait()
	return result, nil
}

type cpuStats struct {
	CPUUsage struct {
		TotalUsage float64 `json:"total_usage"`
	} `json:"cpu_usage"`
	System float64 `json:"system_cpu_usage"`
	CPUs   float64 `json:"online_cpus"`
}

func (e *Engine) stats(ctx context.Context, c *domain.Container) error {
	var stats struct {
		CPU      cpuStats `json:"cpu_stats"`
		Previous cpuStats `json:"precpu_stats"`
		Memory   struct {
			Usage, Limit float64
			Stats        map[string]float64
		} `json:"memory_stats"`
		Networks map[string]struct {
			RX float64 `json:"rx_bytes"`
			TX float64 `json:"tx_bytes"`
		}
	}
	if err := e.call(ctx, "GET", "/containers/"+c.ID+"/stats?stream=false&one-shot=true", &stats); err != nil {
		return err
	}
	// One-shot samples avoid waiting for two Docker frames per container. Derive
	// CPU from consecutive actual counter samples; the first sample stays unknown.
	e.statsMu.Lock()
	previous, ok := e.previousCPU[c.ID]
	e.previousCPU[c.ID] = stats.CPU
	e.statsMu.Unlock()
	if !ok {
		previous = stats.Previous
	}
	delta, system := stats.CPU.CPUUsage.TotalUsage-previous.CPUUsage.TotalUsage, stats.CPU.System-previous.System
	if previous.System > 0 && system > 0 && delta >= 0 && stats.CPU.CPUs > 0 {
		cpu := delta / system * stats.CPU.CPUs * 100
		c.CPU = &cpu
	}
	cache := stats.Memory.Stats["inactive_file"]
	if cache == 0 {
		cache = stats.Memory.Stats["total_inactive_file"]
	}
	memory := stats.Memory.Usage
	if memory >= cache {
		memory -= cache
	}
	c.Memory = &memory
	c.MemoryLimit = &stats.Memory.Limit
	if len(stats.Networks) > 0 {
		rx, tx := 0.0, 0.0
		for _, n := range stats.Networks {
			rx += n.RX
			tx += n.TX
		}
		c.NetworkRX = &rx
		c.NetworkTX = &tx
	}
	return nil
}

var containerID = regexp.MustCompile(`^[a-f0-9]{64}$`)

func (e *Engine) mutate(ctx context.Context, id, action string) error {
	if !containerID.MatchString(id) {
		return fmt.Errorf("invalid container id")
	}
	// Re-check identity and scope on every mutation; the browser cannot submit an
	// arbitrary host/container command, nor stop the manager or shared services.
	var raw []rawContainer
	if err := e.call(ctx, "GET", "/containers/json?all=1", &raw); err != nil {
		return err
	}
	for _, c := range raw {
		if c.ID == id {
			if !e.managed(c) {
				return fmt.Errorf("container is protected")
			}
			suffix := ""
			if action == "stop" {
				suffix = "?t=20"
			}
			return e.call(ctx, "POST", "/containers/"+id+"/"+action+suffix, nil)
		}
	}
	return fmt.Errorf("container not found")
}
func (e *Engine) Start(ctx context.Context, id string) error { return e.mutate(ctx, id, "start") }
func (e *Engine) Stop(ctx context.Context, id string) error  { return e.mutate(ctx, id, "stop") }
