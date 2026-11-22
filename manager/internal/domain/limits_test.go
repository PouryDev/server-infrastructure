package domain

import "testing"

const mib = 1 << 20

func TestPinKeepsLiveLimitsThatDifferFromCompose(t *testing.T) {
	compose := map[string]LimitSet{"api": {MemoryBytes: 256 * mib, NanoCPUs: 500_000_000}, "web": {MemoryBytes: 128 * mib}, "same": {MemoryBytes: 64 * mib}}
	live := map[string]LimitSet{"api": {MemoryBytes: 512 * mib, NanoCPUs: 500_000_000}, "web": {MemoryBytes: 128 * mib}, "same": {MemoryBytes: 64 * mib}, "free": {}}
	pins := MergeLimits(compose, live, false)
	if len(pins) != 1 || pins[0].Service != "api" || pins[0].MemoryBytes != 512*mib || pins[0].NanoCPUs != 0 {
		t.Fatalf("%+v", pins)
	}
}
func TestUnlimitedLiveValueNeverPinsAndComposeApplies(t *testing.T) {
	// The owner removed the limit on the server; compose defines one: compose applies.
	if pin := PinLimit(LimitSet{MemoryBytes: 256 * mib}, LimitSet{}); !pin.IsZero() {
		t.Fatalf("unlimited live value pinned: %+v", pin)
	}
	// Both unlimited: nothing to pin, container stays unlimited.
	if pin := PinLimit(LimitSet{}, LimitSet{}); !pin.IsZero() {
		t.Fatal("pinned nothing-to-nothing")
	}
	// Live limit where compose defines none is preserved.
	if pin := PinLimit(LimitSet{}, LimitSet{NanoCPUs: 2e9}); pin.NanoCPUs != 2e9 {
		t.Fatalf("%+v", pin)
	}
}
func TestCPUComparisonToleratesNanoRounding(t *testing.T) {
	if pin := PinLimit(LimitSet{NanoCPUs: 100_000_000}, LimitSet{NanoCPUs: 100_000_001}); !pin.IsZero() {
		t.Fatalf("rounding noise pinned: %+v", pin)
	}
}
func TestSwapDefaultIsNotAnOwnerDecisionButExplicitSwapIsKept(t *testing.T) {
	if pin := PinLimit(LimitSet{MemoryBytes: 256 * mib}, LimitSet{MemoryBytes: 256 * mib, MemSwapBytes: 512 * mib}); !pin.IsZero() {
		t.Fatalf("docker's default swap pinned: %+v", pin)
	}
	pin := PinLimit(LimitSet{MemoryBytes: 256 * mib}, LimitSet{MemoryBytes: 256 * mib, MemSwapBytes: 1024 * mib})
	if pin.MemSwapBytes != 1024*mib || pin.MemoryBytes != 0 {
		t.Fatalf("%+v", pin)
	}
	// Raising memory above a compose swap ceiling must raise the swap ceiling.
	pin = PinLimit(LimitSet{MemoryBytes: 128 * mib, MemSwapBytes: 256 * mib}, LimitSet{MemoryBytes: 512 * mib})
	if pin.MemoryBytes != 512*mib || pin.MemSwapBytes != 512*mib {
		t.Fatalf("%+v", pin)
	}
}
func TestUseComposeLimitsDisablesPreservation(t *testing.T) {
	if got := MergeLimits(map[string]LimitSet{"a": {}}, map[string]LimitSet{"a": {MemoryBytes: mib}}, true); len(got) != 0 {
		t.Fatal(got)
	}
}
func TestLiveLimitsForRollbackSkipDefaultSwap(t *testing.T) {
	got := LiveLimits(map[string]LimitSet{"a": {MemoryBytes: 64 * mib, MemSwapBytes: 128 * mib}, "b": {}, "c": {NanoCPUs: 1e9}})
	if len(got) != 2 || got[0].Service != "a" || got[0].MemSwapBytes != 0 || got[1].NanoCPUs != 1e9 {
		t.Fatalf("%+v", got)
	}
}
func TestComposeLimitsReadsBothFieldStylesAndBytes(t *testing.T) {
	l, style := ComposeLimits(map[string]any{"mem_limit": "256m", "cpus": 0.5, "memswap_limit": "1g"})
	if l.MemoryBytes != 256*mib || l.NanoCPUs != 500_000_000 || l.MemSwapBytes != 1<<30 || style.MemoryDeploy || style.CPUDeploy {
		t.Fatalf("%+v %+v", l, style)
	}
	l, style = ComposeLimits(map[string]any{"deploy": map[string]any{"resources": map[string]any{"limits": map[string]any{"memory": "268435456", "cpus": "0.25"}}}})
	if l.MemoryBytes != 256*mib || l.NanoCPUs != 250_000_000 || !style.MemoryDeploy || !style.CPUDeploy {
		t.Fatalf("%+v %+v", l, style)
	}
	if l, _ = ComposeLimits(map[string]any{"mem_limit": "${MEM:-256m}"}); l.MemoryBytes != 0 {
		t.Fatal("unresolved interpolation parsed")
	}
	if v, ok := ParseBytes("1.5g"); !ok || v != 3<<29 {
		t.Fatal(v)
	}
	if FormatMemory(512*mib) != "512m" || FormatMemory(2<<30) != "2g" || FormatCPUs(1_500_000_000) != "1.5" {
		t.Fatal("format")
	}
}
