package v1

// Gateway performance attribution toggles (EVS-83 perf work).
//
// These exist to answer one question with measurements instead of guesses:
// how much of observed request latency is gateway overhead (telemetry
// attribute computation, semantic cache lookups) versus upstream provider
// time. Each toggle isolates one suspect so an A/B benchmark run attributes
// the gap. See the "Gateway performance" Linear document run log: one row
// per run, toggles recorded in the notes column.
//
// All toggles default to OFF (zero behavior change). They are read once at
// process startup, so there is no per-request env lookup cost. The quiet-logs
// A/B needs no toggle: log level is already configurable, so benchmark runs
// should set it to warn/error to isolate logging cost.

import (
	"os"
	"strings"
	"sync/atomic"

	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
)

var (
	perfDisableTelemetryAttrs = atomic.Bool{}
	perfDisableSemanticCache  = atomic.Bool{}
)

func init() { ReloadPerfAttributionToggles() }

// ReloadPerfAttributionToggles re-reads the attribution toggles from the
// environment. Called once at startup; exposed for tests.
func ReloadPerfAttributionToggles() {
	perfDisableTelemetryAttrs.Store(perfEnvFlag("EVS_PERF_DISABLE_TELEMETRY_ATTRS"))
	perfDisableSemanticCache.Store(perfDisableSemanticCacheEnv())
}

func perfEnvFlag(key string) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	return v == "1" || v == "true" || v == "yes"
}

// perfDisableSemanticCacheEnv reads the semantic-cache toggle. Split out so
// the key name is documented in exactly one place.
func perfDisableSemanticCacheEnv() bool {
	return perfEnvFlag("EVS_PERF_DISABLE_SEMANTIC_CACHE")
}

// perfTelemetryAttrsDisabled reports whether expensive telemetry attribute
// computation (full-payload JSON serialization, message/response analysis,
// business classification) should be skipped. Spans are still created and
// ended; only the CPU-heavy attribute building is skipped.
func perfTelemetryAttrsDisabled() bool {
	return perfDisableTelemetryAttrs.Load()
}

// perfSemanticCacheDisabled reports whether semantic cache lookup and store
// should be skipped. Exact-match caching is unaffected.
func perfSemanticCacheDisabled() bool {
	return perfDisableSemanticCache.Load()
}

// perfSerializeToJSON serializes v for a span attribute, or returns "" when
// telemetry attribute computation is disabled for attribution. Call sites
// must tolerate the empty string (attributes are observability-only).
//
// Full fidelity: trace payloads are NEVER truncated here. Debugging and evals
// depend on the complete text, so this is plain SerializeToJSON. Cost control
// on the hot path comes from serializing once and sharing the result (see
// normMessagesJSON), and from moving classification work after send — never
// from cutting payload data (EVS-83).
func perfSerializeToJSON(v interface{}) string {
	if perfTelemetryAttrsDisabled() {
		return ""
	}
	return attrs.SerializeToJSON(v)
}
