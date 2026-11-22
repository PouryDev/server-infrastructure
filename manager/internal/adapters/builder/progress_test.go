package builder

import (
	"encoding/json"
	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	"strings"
	"testing"
)

func TestBuildProgressCountsRealVerticesAndDoesNotExposeOutput(t *testing.T) {
	var events []domain.ShipProgress
	o := buildOutput{report: func(p domain.ShipProgress) error { events = append(events, p); return nil }}
	for _, chunk := range []string{`{"vertexes":[{"digest":"a","name":"RUN secret-value",`, `"completed":"now"},{"digest":"b"}]}` + "\n", `{"vertexes":[{"digest":"c"},{"digest":"b","completed":"now"}]}` + "\n"} {
		if _, err := o.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := o.flush(); err != nil {
		t.Fatal(err)
	}
	last := events[len(events)-1]
	if last.Progress.Current != 2 || last.Progress.Total != 3 {
		t.Fatal(last)
	}
	data, _ := json.Marshal(events)
	if strings.Contains(string(data), "secret-value") {
		t.Fatal("build commands exposed in progress")
	}
}
