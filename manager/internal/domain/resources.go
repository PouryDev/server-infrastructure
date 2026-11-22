package domain

import (
	"fmt"
	"math"
	"regexp"
)

type ResourceRequest struct {
	Project   string  `json:"project"`
	Service   string  `json:"service"`
	MemoryMiB int64   `json:"memoryMiB"`
	CPUs      float64 `json:"cpus"`
	GitMode   string  `json:"gitMode"`
	Revision  string  `json:"revision"`
}

func (r ResourceRequest) Validate() error {
	name := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,99}$`)
	if !name.MatchString(r.Project) || !name.MatchString(r.Service) || r.MemoryMiB < 6 || r.MemoryMiB > 1048576 || math.IsNaN(r.CPUs) || math.IsInf(r.CPUs, 0) || r.CPUs < .01 || r.CPUs > 1024 || math.Abs(r.CPUs*100-math.Round(r.CPUs*100)) > .000001 {
		return fmt.Errorf("invalid resource limits")
	}
	if r.GitMode != "local" && r.GitMode != "commit" && r.GitMode != "push" {
		return fmt.Errorf("invalid Git mode")
	}
	return nil
}

type ResourceContainer struct {
	ID        string  `json:"id"`
	MemoryMiB int64   `json:"memoryMiB"`
	CPUs      float64 `json:"cpus"`
}
type ResourcePreview struct {
	Project     string              `json:"project"`
	Service     string              `json:"service"`
	File        string              `json:"file"`
	Revision    string              `json:"revision"`
	Branch      string              `json:"branch"`
	GitReady    bool                `json:"gitReady"`
	PushEnabled bool                `json:"pushEnabled"`
	Containers  []ResourceContainer `json:"containers"`
	Changes     []string            `json:"changes"`
}
type ResourceResult struct {
	Applied bool   `json:"applied"`
	Commit  string `json:"commit"`
	Pushed  bool   `json:"pushed"`
	Warning string `json:"warning"`
}
