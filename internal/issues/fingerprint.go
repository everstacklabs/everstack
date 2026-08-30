// Package issues implements Sentry-style error tracking: recurring failures in
// the trace store grouped into triageable issues. It derives issue groups by
// aggregating error spans in `otel_traces` at query time (no ingest changes),
// keying each group by a normalized error signature, and overlays a mutable
// triage state (resolved/ignored/assignee) kept in Postgres.
package issues

import (
	"regexp"
	"strings"
)

// ─── ClickHouse SQL building blocks ──────────────────────────────────
//
// The canonical signature is computed in ClickHouse so grouping, counts and
// first/last-seen happen server-side without scanning rows into Go. The Go
// mirror below (NormalizeSignature/Classify) exists for unit tests and any
// future host-side use; list and detail queries both compute the fingerprint
// in SQL, so they always agree with each other.

// chErrorMessage is the raw error text for an error span: the span status
// message, falling back to common exception/error attributes.
const chErrorMessage = `coalesce(nullIf(StatusMessage, ''), nullIf(SpanAttributes['exception.message'], ''), nullIf(SpanAttributes['error.message'], ''), 'unknown error')`

// chProvider / chModel resolve the LLM provider and model across the naming
// conventions we ingest (Everstack-native, OTel gen_ai, OpenInference).
const chProvider = `coalesce(nullIf(SpanAttributes['llm.provider'], ''), nullIf(SpanAttributes['gen_ai.system'], ''), '')`
const chModel = `coalesce(nullIf(SpanAttributes['llm.model'], ''), nullIf(SpanAttributes['gen_ai.response.model'], ''), nullIf(SpanAttributes['gen_ai.request.model'], ''), '')`

// chSignature mirrors NormalizeSignature: strip uuids, hex, numbers, paths and
// quoted strings, collapse whitespace, lowercase. Order matters (uuid before
// hex before numbers). Uses RE2 (ClickHouse) syntax.
const chSignature = `lower(replaceRegexpAll(replaceRegexpAll(replaceRegexpAll(replaceRegexpAll(replaceRegexpAll(replaceRegexpAll(replaceRegexpAll(` +
	chErrorMessage + `,
	'[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}', '<uuid>'),
	'0x[0-9a-fA-F]+', '<hex>'),
	'[0-9]+', '<n>'),
	'/[A-Za-z0-9_./-]+', '<path>'),
	'"[^"]*"', '"<str>"'),
	'\x27[^\x27]*\x27', '<str>'),
	'[[:space:]]+', ' '))`

// chFingerprint is the stable cluster key: a hash of the normalized signature.
const chFingerprint = `lower(hex(cityHash64(` + chSignature + `)))`

// chCategory is an LLM-aware classification of the failure, as a lowercase
// string matching the IssueCategory enum suffixes.
const chCategory = `multiIf(` +
	`positionCaseInsensitive(` + chErrorMessage + `, 'rate limit') > 0 OR positionCaseInsensitive(` + chErrorMessage + `, '429') > 0 OR SpanAttributes['error.type'] = 'rate_limit', 'rate_limit', ` +
	`positionCaseInsensitive(` + chErrorMessage + `, 'context length') > 0 OR positionCaseInsensitive(` + chErrorMessage + `, 'context_length') > 0 OR positionCaseInsensitive(` + chErrorMessage + `, 'maximum context') > 0, 'context_length', ` +
	`positionCaseInsensitive(` + chErrorMessage + `, 'guardrail') > 0 OR positionCaseInsensitive(` + chErrorMessage + `, 'content filter') > 0 OR positionCaseInsensitive(` + chErrorMessage + `, 'moderation') > 0, 'guardrail_block', ` +
	`positionCaseInsensitive(` + chErrorMessage + `, 'timeout') > 0 OR positionCaseInsensitive(` + chErrorMessage + `, 'deadline') > 0, 'timeout', ` +
	`positionCaseInsensitive(` + chErrorMessage + `, 'unauthorized') > 0 OR positionCaseInsensitive(` + chErrorMessage + `, '401') > 0 OR positionCaseInsensitive(` + chErrorMessage + `, '403') > 0 OR positionCaseInsensitive(` + chErrorMessage + `, 'api key') > 0 OR positionCaseInsensitive(` + chErrorMessage + `, 'api-key') > 0 OR positionCaseInsensitive(` + chErrorMessage + `, 'authentication') > 0 OR positionCaseInsensitive(` + chErrorMessage + `, 'invalid key') > 0, 'auth', ` +
	`positionCaseInsensitive(` + chErrorMessage + `, 'tool') > 0, 'tool_error', ` +
	`positionCaseInsensitive(` + chErrorMessage + `, 'parse') > 0 OR positionCaseInsensitive(` + chErrorMessage + `, 'json') > 0 OR positionCaseInsensitive(` + chErrorMessage + `, 'unmarshal') > 0, 'parse_error', ` +
	`positionCaseInsensitive(` + chErrorMessage + `, '500') > 0 OR positionCaseInsensitive(` + chErrorMessage + `, '502') > 0 OR positionCaseInsensitive(` + chErrorMessage + `, '503') > 0 OR positionCaseInsensitive(` + chErrorMessage + `, 'internal server') > 0 OR positionCaseInsensitive(` + chErrorMessage + `, 'overloaded') > 0, 'provider_5xx', ` +
	`'other')`

// ─── Go mirror (for tests + host-side use) ───────────────────────────

var (
	reUUID  = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	reHex   = regexp.MustCompile(`0x[0-9a-fA-F]+`)
	reNum   = regexp.MustCompile(`[0-9]+`)
	rePath  = regexp.MustCompile(`/[A-Za-z0-9_./-]+`)
	reDQ    = regexp.MustCompile(`"[^"]*"`)
	reSQ    = regexp.MustCompile(`'[^']*'`)
	reSpace = regexp.MustCompile(`\s+`)
)

// NormalizeSignature reduces an error message to a stable fingerprint that
// ignores volatile bits. Mirrors the ClickHouse chSignature expression and the
// frontend's normalizeErrorSignature.
func NormalizeSignature(msg string) string {
	s := msg
	s = reUUID.ReplaceAllString(s, "<uuid>")
	s = reHex.ReplaceAllString(s, "<hex>")
	s = reNum.ReplaceAllString(s, "<n>")
	s = rePath.ReplaceAllString(s, "<path>")
	s = reDQ.ReplaceAllString(s, `"<str>"`)
	s = reSQ.ReplaceAllString(s, "<str>")
	s = reSpace.ReplaceAllString(s, " ")
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// Classify returns the LLM-aware category for an error message, mirroring the
// chCategory SQL. Returned values match the IssueCategory enum suffixes.
func Classify(msg string) string {
	m := strings.ToLower(msg)
	contains := func(needle string) bool { return strings.Contains(m, needle) }
	switch {
	case contains("rate limit") || contains("429"):
		return "rate_limit"
	case contains("context length") || contains("context_length") || contains("maximum context"):
		return "context_length"
	case contains("guardrail") || contains("content filter") || contains("moderation"):
		return "guardrail_block"
	case contains("timeout") || contains("deadline"):
		return "timeout"
	case contains("unauthorized") || contains("401") || contains("403") || contains("api key") ||
		contains("api-key") || contains("authentication") || contains("invalid key"):
		return "auth"
	case contains("tool"):
		return "tool_error"
	case contains("parse") || contains("json") || contains("unmarshal"):
		return "parse_error"
	case contains("500") || contains("502") || contains("503") || contains("internal server") || contains("overloaded"):
		return "provider_5xx"
	default:
		return "other"
	}
}

// tenantBridgeFilter scopes a query against otel_traces to a tenant. It mirrors
// the helper in internal/query/handlers/traces/tenant_filter.go: match the
// per-span tenant attribute, falling back to the resource attribute only when
// the span has no per-span tenant (the empty-string guard prevents cross-tenant
// leaks in shared gateway mode).
func tenantBridgeFilter(tenantID string) (string, []interface{}) {
	return "(SpanAttributes['tenant.id'] = ? OR (SpanAttributes['tenant.id'] = '' AND ResourceAttributes['tenant.id'] = ?))",
		[]interface{}{tenantID, tenantID}
}
