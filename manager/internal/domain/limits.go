package domain

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// LimitSet holds Docker-native units. Zero means undefined/unlimited.
type LimitSet struct {
	MemoryBytes  int64 `json:"memoryBytes"`
	NanoCPUs     int64 `json:"nanoCpus"`
	MemSwapBytes int64 `json:"memSwapBytes"`
}

func (l LimitSet) IsZero() bool { return l == LimitSet{} }

// PreservedLimit is shown in previews: the value pinned for a service because the
// running container differs from what the local Compose file would apply.
type PreservedLimit struct {
	Service      string `json:"service"`
	MemoryBytes  int64  `json:"memoryBytes"`
	NanoCPUs     int64  `json:"nanoCpus"`
	MemSwapBytes int64  `json:"memSwapBytes"`
	ComposeMem   int64  `json:"composeMemoryBytes"`
	ComposeCPUs  int64  `json:"composeNanoCpus"`
}

const nanoTolerance = 1_000_000

func nearly(a, b int64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= nanoTolerance
}

// PinLimit returns the values that must be pinned so a recreated container keeps
// the live limits. A live "unlimited" value never pins: Compose then applies its
// own value, or nothing when it defines none.
func PinLimit(compose, live LimitSet) LimitSet {
	var pin LimitSet
	if live.MemoryBytes > 0 && live.MemoryBytes != compose.MemoryBytes {
		pin.MemoryBytes = live.MemoryBytes
	}
	if live.NanoCPUs > 0 && !nearly(live.NanoCPUs, compose.NanoCPUs) {
		pin.NanoCPUs = live.NanoCPUs
	}
	// Docker reports memory+swap; its default is twice the memory limit, which
	// is not an owner decision and is never pinned on its own.
	defaultSwap := live.MemoryBytes > 0 && live.MemSwapBytes == 2*live.MemoryBytes
	if live.MemSwapBytes > 0 && live.MemSwapBytes != compose.MemSwapBytes && !(compose.MemSwapBytes == 0 && defaultSwap) {
		pin.MemSwapBytes = live.MemSwapBytes
	}
	memory := compose.MemoryBytes
	if pin.MemoryBytes > 0 {
		memory = pin.MemoryBytes
	}
	swap := compose.MemSwapBytes
	if pin.MemSwapBytes > 0 {
		swap = pin.MemSwapBytes
	}
	// Docker rejects a swap ceiling below the memory ceiling.
	if memory > 0 && swap > 0 && swap < memory {
		pin.MemSwapBytes = memory
	}
	return pin
}

// MergeLimits computes the per-service pins. useCompose disables preservation.
func MergeLimits(compose, live map[string]LimitSet, useCompose bool) []PreservedLimit {
	out := []PreservedLimit{}
	if useCompose {
		return out
	}
	for service, l := range live {
		c := compose[service]
		pin := PinLimit(c, l)
		if pin.IsZero() {
			continue
		}
		out = append(out, PreservedLimit{Service: service, MemoryBytes: pin.MemoryBytes, NanoCPUs: pin.NanoCPUs, MemSwapBytes: pin.MemSwapBytes, ComposeMem: c.MemoryBytes, ComposeCPUs: c.NanoCPUs})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out
}

// LiveLimits keeps every currently limited service; used for rollback overrides.
func LiveLimits(live map[string]LimitSet) []PreservedLimit {
	out := []PreservedLimit{}
	for service, l := range live {
		if l.MemoryBytes > 0 || l.NanoCPUs > 0 || l.MemSwapBytes > 0 {
			swap := l.MemSwapBytes
			if l.MemoryBytes > 0 && swap == 2*l.MemoryBytes {
				swap = 0
			}
			out = append(out, PreservedLimit{Service: service, MemoryBytes: l.MemoryBytes, NanoCPUs: l.NanoCPUs, MemSwapBytes: swap})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out
}

// ParseBytes understands Compose/Docker size strings: 512m, 1g, 256MB, 1048576.
func ParseBytes(value any) (int64, bool) {
	switch v := value.(type) {
	case int:
		return int64(v), v >= 0
	case int64:
		return v, v >= 0
	case float64:
		return int64(v), v >= 0 && !math.IsInf(v, 0) && !math.IsNaN(v)
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		if s == "" || strings.Contains(s, "$") {
			return 0, false
		}
		multiplier := int64(1)
		for _, unit := range []struct {
			suffix string
			factor int64
		}{{"tb", 1 << 40}, {"gb", 1 << 30}, {"mb", 1 << 20}, {"kb", 1 << 10}, {"t", 1 << 40}, {"g", 1 << 30}, {"m", 1 << 20}, {"k", 1 << 10}, {"b", 1}} {
			if strings.HasSuffix(s, unit.suffix) {
				multiplier = unit.factor
				s = strings.TrimSpace(strings.TrimSuffix(s, unit.suffix))
				break
			}
		}
		number, err := strconv.ParseFloat(s, 64)
		if err != nil || number < 0 || math.IsInf(number, 0) || math.IsNaN(number) {
			return 0, false
		}
		return int64(number * float64(multiplier)), true
	}
	return 0, false
}

func parseCPUs(value any) (int64, bool) {
	var f float64
	switch v := value.(type) {
	case int:
		f = float64(v)
	case int64:
		f = float64(v)
	case float64:
		f = v
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, false
		}
		f = parsed
	default:
		return 0, false
	}
	if f < 0 || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return int64(math.Round(f * 1e9)), true
}

// LimitStyle records which Compose fields the base file uses so an override sets
// the same field (Compose rejects conflicting mem_limit and deploy limits).
type LimitStyle struct{ MemoryDeploy, CPUDeploy bool }

func get(m map[string]any, keys ...string) any {
	var current any = m
	for _, k := range keys {
		next, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = next[k]
	}
	return current
}

// ComposeLimits extracts limits from one service of a (resolved) Compose model.
func ComposeLimits(service map[string]any) (LimitSet, LimitStyle) {
	var l LimitSet
	var style LimitStyle
	if v, ok := ParseBytes(service["mem_limit"]); ok {
		l.MemoryBytes = v
	} else if v, ok := ParseBytes(get(service, "deploy", "resources", "limits", "memory")); ok {
		l.MemoryBytes = v
		style.MemoryDeploy = true
	}
	if v, ok := parseCPUs(service["cpus"]); ok {
		l.NanoCPUs = v
	} else if v, ok := parseCPUs(get(service, "deploy", "resources", "limits", "cpus")); ok {
		l.NanoCPUs = v
		style.CPUDeploy = true
	}
	if v, ok := ParseBytes(service["memswap_limit"]); ok {
		l.MemSwapBytes = v
	}
	if service["mem_limit"] == nil && get(service, "deploy", "resources", "limits", "memory") != nil {
		style.MemoryDeploy = true
	}
	if service["cpus"] == nil && get(service, "deploy", "resources", "limits", "cpus") != nil {
		style.CPUDeploy = true
	}
	return l, style
}

// FormatLimit renders a limit for previews, e.g. "512 MiB" / "1.5".
func FormatMemory(b int64) string {
	switch {
	case b%(1<<30) == 0:
		return fmt.Sprintf("%dg", b>>30)
	case b%(1<<20) == 0:
		return fmt.Sprintf("%dm", b>>20)
	default:
		return fmt.Sprintf("%d", b)
	}
}
func FormatCPUs(n int64) string {
	return strconv.FormatFloat(float64(n)/1e9, 'f', -1, 64)
}
