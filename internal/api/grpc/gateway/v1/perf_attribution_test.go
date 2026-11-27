package v1

import (
	"strings"
	"testing"

	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
)

// resetPerfToggles restores defaults after each test. The toggles are
// process-wide, so tests must not leak state.
func resetPerfToggles(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		t.Setenv("EVS_PERF_DISABLE_TELEMETRY_ATTRS", "")
		t.Setenv("EVS_PERF_DISABLE_SEMANTIC_CACHE", "")
		ReloadPerfAttributionToggles()
	})
}

func TestPerfTogglesDefaultOff(t *testing.T) {
	resetPerfToggles(t)
	ReloadPerfAttributionToggles()

	if perfTelemetryAttrsDisabled() {
		t.Error("telemetry attrs toggle should default to off")
	}
	if perfSemanticCacheDisabled() {
		t.Error("semantic cache toggle should default to off")
	}
}

func TestPerfTogglesEnvParsing(t *testing.T) {
	resetPerfToggles(t)

	for _, v := range []string{"1", "true", "TRUE", "yes", " Yes "} {
		t.Setenv("EVS_PERF_DISABLE_TELEMETRY_ATTRS", v)
		t.Setenv("EVS_PERF_DISABLE_SEMANTIC_CACHE", v)
		ReloadPerfAttributionToggles()
		if !perfTelemetryAttrsDisabled() {
			t.Errorf("telemetry attrs toggle should be on for %q", v)
		}
		if !perfSemanticCacheDisabled() {
			t.Errorf("semantic cache toggle should be on for %q", v)
		}
	}

	for _, v := range []string{"", "0", "false", "no", "banana"} {
		t.Setenv("EVS_PERF_DISABLE_TELEMETRY_ATTRS", v)
		t.Setenv("EVS_PERF_DISABLE_SEMANTIC_CACHE", v)
		ReloadPerfAttributionToggles()
		if perfTelemetryAttrsDisabled() {
			t.Errorf("telemetry attrs toggle should be off for %q", v)
		}
		if perfSemanticCacheDisabled() {
			t.Errorf("semantic cache toggle should be off for %q", v)
		}
	}
}

func TestPerfSerializeToJSONRespectsToggle(t *testing.T) {
	resetPerfToggles(t)

	payload := map[string]string{"hello": "world"}

	// Default: full-fidelity serialization, never empty for non-empty input.
	got := perfSerializeToJSON(payload)
	if got == "" {
		t.Fatal("expected non-empty serialization with toggles off")
	}

	// Toggle on: empty string (call sites must tolerate it).
	t.Setenv("EVS_PERF_DISABLE_TELEMETRY_ATTRS", "1")
	ReloadPerfAttributionToggles()
	if got := perfSerializeToJSON(payload); got != "" {
		t.Errorf("expected empty string with toggle on, got %q", got)
	}
}

func TestPerfSerializeToJSONPreservesFullPayload(t *testing.T) {
	resetPerfToggles(t)

	// Trace payloads must NEVER be truncated: debugging and evals depend on
	// the complete text. A 100KB payload must round-trip in full.
	big := map[string]string{"prompt": strings.Repeat("a", 100*1024)}
	got := perfSerializeToJSON(big)
	want := attrs.SerializeToJSON(big)
	if got != want {
		t.Errorf("expected full-fidelity serialization (%d chars), got %d chars", len(want), len(got))
	}
	if len(got) == 0 {
		t.Fatal("expected non-empty output")
	}
}
