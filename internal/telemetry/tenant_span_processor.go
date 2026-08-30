package telemetry

import (
	"context"

	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"github.com/everstacklabs/everstack/pkg/ctxkeys"
	pkgdb "github.com/everstacklabs/everstack/pkg/database"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// tenantSpanProcessor stamps tenant.id onto every span at OnStart.
//
// Without this, only manually-instrumented spans (gateway.chat.completion,
// provider.*, agent.turn, a few sandbox/studio entries) carry the attribute,
// and every read-side handler under internal/query/handlers/traces filters
// with `WHERE SpanAttributes['tenant.id'] = ?`. That pruned every
// auto-instrumented child span (otelhttp/otelgrpc/db/MCP/sandbox) from the
// trace tree, listings, metrics aggregation, and dashboards — the symptom
// being chased in c894c48b ("trace tree missing provider span") was an
// incomplete subset of the same root cause.
//
// The fallback chain reads the request-scoped tenant first — correct for
// shared/cloud gateway processes that serve many tenants from one OS
// process — and only falls back to the resource-level tenant for spans
// started outside any request (background workers, schedulers). In
// single-tenant deployments those collapse to the same value.
type tenantSpanProcessor struct {
	fallbackTenantID string
}

func newTenantSpanProcessor(fallbackTenantID string) *tenantSpanProcessor {
	return &tenantSpanProcessor{fallbackTenantID: fallbackTenantID}
}

func (p *tenantSpanProcessor) OnStart(parent context.Context, s sdktrace.ReadWriteSpan) {
	tid := ctxkeys.TenantIDFromContext(parent)
	if tid == "" {
		tid = pkgdb.TenantSchemaFromContext(parent)
	}
	if tid == "" {
		tid = p.fallbackTenantID
	}
	if tid == "" {
		return
	}
	s.SetAttributes(attribute.String(attrs.TenantID, tid))
}

func (p *tenantSpanProcessor) OnEnd(sdktrace.ReadOnlySpan)             {}
func (p *tenantSpanProcessor) Shutdown(context.Context) error          { return nil }
func (p *tenantSpanProcessor) ForceFlush(context.Context) error        { return nil }
