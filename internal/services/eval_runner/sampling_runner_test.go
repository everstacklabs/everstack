package eval_runner

import (
	"fmt"
	"testing"
)

// TestStableSample_Determinism — same (rule, trace) pair → same decision.
func TestStableSample_Determinism(t *testing.T) {
	cases := []struct{ rule, trace string }{
		{"rule-1", "abc"},
		{"rule-1", "xyz"},
		{"rule-2", "abc"},
	}
	for _, c := range cases {
		a := stableSample(c.rule, c.trace, 0.5)
		b := stableSample(c.rule, c.trace, 0.5)
		if a != b {
			t.Fatalf("stableSample(%q,%q) non-deterministic: %v vs %v", c.rule, c.trace, a, b)
		}
	}
}

// TestStableSample_Bounds — rate=0 always false, rate=1 always true.
func TestStableSample_Bounds(t *testing.T) {
	for i := 0; i < 1000; i++ {
		traceID := fmt.Sprintf("t-%d", i)
		if stableSample("r", traceID, 0) {
			t.Fatalf("rate=0 should never sample, got true for %q", traceID)
		}
		if !stableSample("r", traceID, 1) {
			t.Fatalf("rate=1 should always sample, got false for %q", traceID)
		}
	}
}

// TestStableSample_Distribution — fraction sampled should match rate
// within a tolerance for a reasonable sample size. Catches a swap of
// the comparison or a hash range mistake.
func TestStableSample_Distribution(t *testing.T) {
	const n = 10000
	rates := []float64{0.1, 0.25, 0.5, 0.9}
	for _, rate := range rates {
		sampled := 0
		for i := 0; i < n; i++ {
			traceID := fmt.Sprintf("trace-%d-%f", i, rate)
			if stableSample("rule-A", traceID, rate) {
				sampled++
			}
		}
		got := float64(sampled) / float64(n)
		// Wide tolerance (±5pp) because we're hash-bucketing strings.
		if got < rate-0.05 || got > rate+0.05 {
			t.Errorf("rate=%v: sampled=%v, expected ~%v ±0.05", rate, got, rate)
		}
	}
}

// TestStableSample_RuleIndependence — different rules give independent
// samples for the same trace, so rules with overlapping filters don't
// always score the same trace cohort.
func TestStableSample_RuleIndependence(t *testing.T) {
	const n = 5000
	var bothIn, bothOut, ruleAOnly, ruleBOnly int
	for i := 0; i < n; i++ {
		traceID := fmt.Sprintf("trace-%d", i)
		a := stableSample("rule-A", traceID, 0.5)
		b := stableSample("rule-B", traceID, 0.5)
		switch {
		case a && b:
			bothIn++
		case !a && !b:
			bothOut++
		case a:
			ruleAOnly++
		case b:
			ruleBOnly++
		}
	}
	// If rules were correlated, bothIn + bothOut would be ~5000 and
	// ruleAOnly + ruleBOnly would be ~0. Independent: each quadrant ~25%.
	if ruleAOnly+ruleBOnly < n/3 {
		t.Errorf("rules look correlated: ruleAOnly=%d ruleBOnly=%d (expected ~%d combined)", ruleAOnly, ruleBOnly, n/2)
	}
}
