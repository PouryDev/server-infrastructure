package resources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	"gopkg.in/yaml.v3"
)

func isJSONCompose(data []byte) bool { return json.Valid(data) }

type jsonSpan struct{ start, end int }

// Decoder offsets count bytes, so edits stay correct even for compact JSON
// containing Persian text or escaped strings before a resource field.
func jsonMember(data []byte, key string) (jsonSpan, bool, error) {
	d := json.NewDecoder(bytes.NewReader(data))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return jsonSpan{}, false, fmt.Errorf("invalid Compose mapping")
	}
	for d.More() {
		token, err = d.Token()
		if err != nil {
			return jsonSpan{}, false, err
		}
		var raw json.RawMessage
		if err = d.Decode(&raw); err != nil {
			return jsonSpan{}, false, err
		}
		if token == key {
			end := int(d.InputOffset())
			return jsonSpan{end - len(raw), end}, true, nil
		}
	}
	return jsonSpan{}, false, nil
}

func patchJSONCompose(data []byte, r domain.ResourceRequest) ([]byte, []string, error) {
	// JSON allows duplicate keys syntactically; reject them just like block YAML
	// rather than editing a different value from the one Compose would consume.
	var unique map[string]any
	if yaml.Unmarshal(data, &unique) != nil {
		return nil, nil, fmt.Errorf("invalid Compose source")
	}
	locate := func(parent jsonSpan, key string) (jsonSpan, bool, error) {
		s, ok, err := jsonMember(data[parent.start:parent.end], key)
		return jsonSpan{parent.start + s.start, parent.start + s.end}, ok, err
	}
	services, ok, err := locate(jsonSpan{0, len(data)}, "services")
	if err != nil || !ok {
		return nil, nil, fmt.Errorf("invalid Compose source")
	}
	svc, ok, err := locate(services, r.Service)
	if err != nil || !ok {
		return nil, nil, fmt.Errorf("service requires a Compose definition")
	}
	// Require an object even if both fields need to be inserted.
	if _, _, err = jsonMember(data[svc.start:svc.end], "mem_limit"); err != nil {
		return nil, nil, err
	}
	type edit struct {
		jsonSpan
		value string
	}
	var edits []edit
	var additions []string
	changes := []string{}
	set := func(parent jsonSpan, key, value string, insert bool) error {
		s, found, err := locate(parent, key)
		if err != nil {
			return err
		}
		if !found {
			if insert {
				additions = append(additions, fmt.Sprintf("%q:%s", key, value))
				changes = append(changes, key+": (inherited/unset) → "+value)
			}
			return nil
		}
		old := data[s.start:s.end]
		if len(old) == 0 || old[0] == '{' || old[0] == '[' {
			return fmt.Errorf("resource limits must be scalar values")
		}
		if !bytes.Equal(old, []byte(value)) {
			edits = append(edits, edit{s, value})
			changes = append(changes, key+": "+string(old)+" → "+value)
		}
		return nil
	}
	memory := fmt.Sprintf("\"%dm\"", r.MemoryMiB)
	cpu := fmt.Sprintf("%.2f", r.CPUs)
	if err = set(svc, "mem_limit", memory, true); err != nil {
		return nil, nil, err
	}
	if err = set(svc, "cpus", cpu, true); err != nil {
		return nil, nil, err
	}
	// Deployed manifests pin Docker's memory+swap ceiling. Raising RAM above
	// that ceiling must keep the saved Compose usable too, as docker update does.
	if span, found, err := locate(svc, "memswap_limit"); err != nil {
		return nil, nil, err
	} else if found {
		var value any
		if json.Unmarshal(data[span.start:span.end], &value) != nil {
			return nil, nil, fmt.Errorf("invalid memory+swap limit")
		}
		if swap, ok := domain.ParseBytes(value); ok && swap > 0 && swap < r.MemoryMiB*(1<<20) {
			if err = set(svc, "memswap_limit", memory, false); err != nil {
				return nil, nil, err
			}
		}
	}
	parent := svc
	for _, key := range []string{"deploy", "resources", "limits"} {
		next, found, err := locate(parent, key)
		if err != nil {
			return nil, nil, err
		}
		if !found {
			parent = jsonSpan{}
			break
		}
		parent = next
	}
	if parent.end > parent.start {
		if err = set(parent, "memory", memory, false); err != nil {
			return nil, nil, err
		}
		if err = set(parent, "cpus", cpu, false); err != nil {
			return nil, nil, err
		}
	}
	if len(additions) > 0 {
		pos := svc.end - 1
		prefix := ""
		if len(bytes.TrimSpace(data[svc.start+1:pos])) > 0 {
			prefix = ","
		}
		edits = append(edits, edit{jsonSpan{pos, pos}, prefix + strings.Join(additions, ",")})
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	out := append([]byte(nil), data...)
	for _, e := range edits {
		out = append(out[:e.start], append([]byte(e.value), out[e.end:]...)...)
	}
	if !json.Valid(out) {
		return nil, nil, fmt.Errorf("Compose edit is invalid")
	}
	return out, changes, nil
}
