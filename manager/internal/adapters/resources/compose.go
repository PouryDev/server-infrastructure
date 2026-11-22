package resources

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/PouryDev/server-infrastructure/manager/internal/domain"
	"gopkg.in/yaml.v3"
)

func field(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// Patch only scalar resource lines. Preserve comments, anchors, formatting and
// every unrelated byte; inherited defaults get explicit service overrides.
func patchCompose(data []byte, r domain.ResourceRequest) ([]byte, []string, error) {
	var doc yaml.Node
	if yaml.Unmarshal(data, &doc) != nil || len(doc.Content) != 1 {
		return nil, nil, fmt.Errorf("invalid Compose source")
	}
	if isJSONCompose(data) {
		return patchJSONCompose(data, r)
	}
	svc := field(field(doc.Content[0], "services"), r.Service)
	if svc == nil || svc.Kind != yaml.MappingNode || svc.Anchor != "" || svc.Style&yaml.FlowStyle != 0 || len(svc.Content) == 0 {
		return nil, nil, fmt.Errorf("service requires a block-style Compose definition")
	}
	lines := strings.Split(string(data), "\n")
	type edit struct {
		line   int
		text   string
		insert bool
	}
	var edits []edit
	changes := []string{}
	set := func(parent *yaml.Node, key, value string, allowInsert bool) error {
		n := field(parent, key)
		if n == nil {
			if !allowInsert {
				return nil
			}
			k := parent.Content[0]
			edits = append(edits, edit{k.Line - 1, strings.Repeat(" ", k.Column-1) + key + ": " + value, true})
			changes = append(changes, key+": (inherited/unset) → "+value)
			return nil
		}
		if n.Kind != yaml.ScalarNode || n.Style&yaml.LiteralStyle != 0 || n.Style&yaml.FoldedStyle != 0 || n.Line < 1 {
			return fmt.Errorf("resource limits must be scalar values")
		}
		line := lines[n.Line-1]
		// Inline maps need a proper YAML editor, never overwrite their other fields.
		prefix := line[:n.Column-1]
		if strings.TrimSpace(prefix) != key+":" {
			return fmt.Errorf("resource limits require block-style fields")
		}
		comment := ""
		if at := regexp.MustCompile(`\s+#`).FindStringIndex(line[n.Column-1:]); at != nil {
			comment = line[n.Column-1+at[0]:]
		}
		next := prefix + value + comment
		if next != line {
			edits = append(edits, edit{n.Line - 1, next, false})
			changes = append(changes, key+": "+n.Value+" → "+value)
		}
		return nil
	}
	memory := fmt.Sprintf("%dm", r.MemoryMiB)
	cpu := fmt.Sprintf("%.2f", r.CPUs)
	if err := set(svc, "mem_limit", memory, true); err != nil {
		return nil, nil, err
	}
	if err := set(svc, "cpus", cpu, true); err != nil {
		return nil, nil, err
	}
	deploy := field(svc, "deploy")
	resources := field(deploy, "resources")
	limits := field(resources, "limits")
	if limits != nil {
		if limits.Kind != yaml.MappingNode || limits.Anchor != "" || limits.Style&yaml.FlowStyle != 0 {
			return nil, nil, fmt.Errorf("deploy limits require block-style fields")
		}
		if err := set(limits, "memory", memory, false); err != nil {
			return nil, nil, err
		}
		if err := set(limits, "cpus", cpu, false); err != nil {
			return nil, nil, err
		}
	}
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].line > edits[j].line })
	for _, e := range edits {
		if e.insert {
			lines = append(lines[:e.line], append([]string{e.text}, lines[e.line:]...)...)
		} else {
			lines[e.line] = e.text
		}
	}
	out := []byte(strings.Join(lines, "\n"))
	var check map[string]any
	if yaml.Unmarshal(out, &check) != nil {
		return nil, nil, fmt.Errorf("Compose edit is invalid")
	}
	return out, changes, nil
}
