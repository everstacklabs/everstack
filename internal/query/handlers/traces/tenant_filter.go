package traces

// tenantBridgeFilter returns a WHERE fragment + the args slice it consumes
// for tenant-scoping a query against `otel_traces`. It matches a span by
// SpanAttribute (the canonical, OnStart-stamped location — see
// internal/telemetry/tenant_span_processor.go) AND falls back to
// ResourceAttribute when the SpanAttribute is empty, so historical spans
// emitted before the SpanProcessor was wired in are still visible.
//
// Why the empty-string guard on SpanAttributes is mandatory:
// in shared/cloud gateway mode one process serves many tenants but the
// resource carries the gateway's home tenant. Without `SpanAttributes
// ['tenant.id'] = ''` constraining the OR, a span deliberately stamped
// for tenant A would also match a query for tenant B whose resource
// happens to be B — i.e. cross-tenant leak. The guard scopes the
// resource fallback to spans that have no per-span tenant at all.
//
// This is a transitional bridge. Once historical data without the
// SpanAttribute has aged out (typically TTL on otel_traces is 30d),
// callers can switch back to a single `SpanAttributes['tenant.id'] = ?`
// predicate and remove this helper. Grep `tenantBridgeFilter` to find
// all callers in one shot.
func tenantBridgeFilter(tenantID string) (clause string, args []interface{}) {
	return "(SpanAttributes['tenant.id'] = ? OR (SpanAttributes['tenant.id'] = '' AND ResourceAttributes['tenant.id'] = ?))",
		[]interface{}{tenantID, tenantID}
}
