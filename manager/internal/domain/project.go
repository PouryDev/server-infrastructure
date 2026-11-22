package domain

import "time"

// Container contains only operational metadata. Environment variables and mounts
// from Docker inspect must never cross the runtime adapter boundary.
type Container struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Project     string   `json:"project"`
	Service     string   `json:"service"`
	State       string   `json:"state"`
	Health      string   `json:"health"`
	Managed     bool     `json:"managed"`
	CPU         *float64 `json:"cpu"`
	Memory      *float64 `json:"memory"`
	MemoryLimit *float64 `json:"memoryLimit"`
	NetworkRX   *float64 `json:"networkRX"`
	NetworkTX   *float64 `json:"networkTX"`
	Error       string   `json:"error,omitempty"`
}

type Policy struct {
	Project   string     `json:"project"`
	Suspended bool       `json:"suspended"`
	ExpiresAt *time.Time `json:"expiresAt"`
	Revision  int64      `json:"revision"`
	// Stop intents are durable before calling Docker. IDs prevent restarting
	// services that were already stopped by their owner.
	Stopped map[string]string `json:"stopped"`
}

func (p Policy) MustStop(now time.Time) bool {
	return p.Suspended || (p.ExpiresAt != nil && !now.Before(*p.ExpiresAt))
}

type Event struct {
	ID      int64     `json:"id"`
	At      time.Time `json:"at"`
	Actor   string    `json:"actor"`
	Project string    `json:"project"`
	Action  string    `json:"action"`
	Outcome string    `json:"outcome"`
	Detail  string    `json:"detail"`
}

type Metric struct {
	Project string  `json:"project"`
	Name    string  `json:"name"`
	Value   float64 `json:"value"`
	Unit    string  `json:"unit"`
}

type Project struct {
	Name       string      `json:"name"`
	Status     string      `json:"status"`
	Policy     Policy      `json:"policy"`
	Containers []Container `json:"containers"`
	Metrics    []Metric    `json:"metrics"`
}

type Snapshot struct {
	Projects            []Project   `json:"projects"`
	Infrastructure      []Container `json:"infrastructure"`
	SyncedAt            *time.Time  `json:"syncedAt"`
	MetricsAt           *time.Time  `json:"metricsAt"`
	Error               string      `json:"error"`
	MetricsError        string      `json:"metricsError"`
	SyncIntervalSeconds int         `json:"syncIntervalSeconds"`
}
