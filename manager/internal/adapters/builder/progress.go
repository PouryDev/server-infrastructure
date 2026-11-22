package builder

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
)

// BuildKit discovers its graph during the build. Only actual vertices are
// counted; command names and output never enter public progress events.
type buildOutput struct {
	pending  []byte
	vertices map[string]bool
	last     time.Time
	tail     tail
	report   func(domain.ShipProgress) error
}

func (o *buildOutput) Write(p []byte) (int, error) {
	o.pending = append(o.pending, p...)
	for {
		i := bytes.IndexByte(o.pending, '\n')
		if i < 0 {
			break
		}
		o.line(o.pending[:i])
		o.pending = o.pending[i+1:]
	}
	// Bound malformed/non-JSON CLI output too.
	if len(o.pending) > 1<<20 {
		o.line(o.pending)
		o.pending = nil
	}
	if o.last.IsZero() || time.Since(o.last) >= time.Second {
		if err := o.flush(); err != nil {
			return len(p), err
		}
	}
	return len(p), nil
}

func (o *buildOutput) line(line []byte) {
	var event struct {
		Vertices []struct {
			Digest    string  `json:"digest"`
			Completed *string `json:"completed"`
			Error     string  `json:"error"`
		} `json:"vertexes"`
		Logs []struct {
			Data []byte `json:"data"`
		} `json:"logs"`
		Error string `json:"error"`
	}
	if json.Unmarshal(line, &event) != nil {
		o.tail.Write(line)
		return
	}
	if o.vertices == nil {
		o.vertices = map[string]bool{}
	}
	for _, v := range event.Vertices {
		if v.Digest != "" {
			o.vertices[v.Digest] = v.Completed != nil && v.Error == ""
		}
		if v.Error != "" {
			o.tail.Write([]byte(v.Error))
		}
	}
	for _, log := range event.Logs {
		o.tail.Write(log.Data)
	}
	if event.Error != "" {
		o.tail.Write([]byte(event.Error))
	}
}

func (o *buildOutput) flush() error {
	if len(o.vertices) == 0 {
		return nil
	}
	var complete int64
	for _, done := range o.vertices {
		if done {
			complete++
		}
	}
	o.last = time.Now()
	return o.report(domain.ShipProgress{Phase: "build", Message: "Building local image", Progress: &domain.WorkProgress{Current: complete, Total: int64(len(o.vertices)), Unit: "vertices"}})
}
