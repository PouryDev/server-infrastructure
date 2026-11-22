package resources

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestPatchDeployedJSONPreservesEveryUnrelatedByte(t *testing.T) {
	for _, data := range []string{
		`{"name":"shop","services":{"shop-api":{"environment":{"TITLE":"دیدچی","LITERAL":"a$\\b\"c"},"mem_limit":268435456,"cpus":0.5,"healthcheck":{"test":["CMD","/api","healthcheck"]}},"shop-worker":{"mem_limit":67108864,"cpus":0.1}},"networks":{"infra":{"external":true}}}`,
		"{\n  \"services\": {\n    \"shop-api\": {\n      \"mem_limit\": 268435456,\n      \"cpus\": 0.5,\n      \"image\": \"shop-api:test\"\n    }\n  }\n}\n",
	} {
		got, changes, err := patchCompose([]byte(data), resizeRequest())
		want := strings.Replace(strings.Replace(data, `"mem_limit":268435456`, `"mem_limit":"512m"`, 1), `"cpus":0.5`, `"cpus":1.00`, 1)
		want = strings.Replace(strings.Replace(want, `"mem_limit": 268435456`, `"mem_limit": "512m"`, 1), `"cpus": 0.5`, `"cpus": 1.00`, 1)
		if err != nil || string(got) != want || len(changes) != 2 {
			t.Fatalf("patch=%s changes=%v error=%v", got, changes, err)
		}
		// A preview after successful application must not propose another file edit.
		again, changes, err := patchCompose(got, resizeRequest())
		if err != nil || !bytes.Equal(again, got) || len(changes) != 0 {
			t.Fatalf("not idempotent: %s %v %v", again, changes, err)
		}
	}
}

func TestPatchJSONInsertsMissingLimitsAndUpdatesDeployLimits(t *testing.T) {
	for _, data := range []string{
		`{"services":{"shop-api":{}}}`,
		`{"services":{"shop-api":{"image":"shop-api:test","deploy":{"resources":{"limits":{"memory":"128m","cpus":"0.25","pids":64}}}}}}`,
	} {
		got, _, err := patchCompose([]byte(data), resizeRequest())
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Services map[string]struct {
				Memory string  `json:"mem_limit"`
				CPUs   float64 `json:"cpus"`
				Deploy struct {
					Resources struct {
						Limits struct {
							Memory string  `json:"memory"`
							CPUs   float64 `json:"cpus"`
							Pids   int     `json:"pids"`
						} `json:"limits"`
					} `json:"resources"`
				} `json:"deploy"`
			} `json:"services"`
		}
		if err = json.Unmarshal(got, &doc); err != nil {
			t.Fatal(err)
		}
		s := doc.Services["shop-api"]
		if s.Memory != "512m" || s.CPUs != 1 {
			t.Fatal(string(got))
		}
		if s.Deploy.Resources.Limits.Pids != 0 && (s.Deploy.Resources.Limits.Memory != "512m" || s.Deploy.Resources.Limits.CPUs != 1 || s.Deploy.Resources.Limits.Pids != 64) {
			t.Fatal(string(got))
		}
	}
	for _, data := range []string{
		`{"services":{"shop-api":{"mem_limit":128,"mem_limit":256}}}`,
		`{"services":{"shop-api":{"mem_limit":{}}}}`,
		`{"services":{"shop-api":[]}}`,
		`{"services":{"other":{}}}`,
	} {
		if _, _, err := patchCompose([]byte(data), resizeRequest()); err == nil {
			t.Fatal("unsupported JSON accepted", data)
		}
	}
}

func TestJSONResourceApplyAndRollback(t *testing.T) {
	for _, fail := range []bool{false, true} {
		f := newFixture(t)
		data := []byte(`{"services":{"shop-api":{"image":"shop-api:test","mem_limit":268435456,"cpus":0.5}}}`)
		if err := os.WriteFile(f.file, data, 0640); err != nil {
			t.Fatal(err)
		}
		f.validationFail = fail
		r := resizeRequest()
		p, err := f.engine.Preview(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}
		r.Revision = p.Revision
		result, err := f.engine.Apply(context.Background(), r)
		after, _ := os.ReadFile(f.file)
		if fail {
			if err == nil || !bytes.Equal(data, after) {
				t.Fatalf("JSON rollback failed: %s %v", after, err)
			}
		} else if err != nil || !result.Applied || bytes.Equal(data, after) {
			t.Fatalf("JSON apply failed: %+v %v", result, err)
		}
	}
}

func TestJSONResourceResizeKeepsSavedSwapCeilingValid(t *testing.T) {
	for _, swap := range []string{`134217728`, `"128m"`, `-1`, `0`, `1073741824`} {
		data := `{"services":{"shop-api":{"mem_limit":67108864,"cpus":0.5,"memswap_limit":` + swap + `}}}`
		got, _, err := patchCompose([]byte(data), resizeRequest())
		if err != nil {
			t.Fatal(err)
		}
		want := swap
		if swap == `134217728` || swap == `"128m"` {
			want = `"512m"`
		}
		if !strings.Contains(string(got), `"memswap_limit":`+want) {
			t.Fatal(string(got))
		}
	}
}
