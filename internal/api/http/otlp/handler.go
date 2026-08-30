// Package otlp implements an OTLP/HTTP receiver compatible with the OpenTelemetry
// Protocol HTTP spec: https://opentelemetry.io/docs/specs/otlp/#otlphttp.
//
// We accept POST /api/public/otel/v1/traces with either application/x-protobuf
// or application/json content. The API-key middleware resolves the tenant from
// the bearer token / X-Api-Key header; we stamp it onto every received span's
// resource attributes before writing to ClickHouse.
//
// This is the customer-facing ingestion path. The gateway itself still ships
// spans via OTLP/gRPC to a sidecar collector (see internal/telemetry/otel.go);
// this handler is what lets a customer's app POST traces directly with no
// Everstack-specific SDK.
package otlp

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

const (
	// MaxBodyBytes caps an OTLP/HTTP request body. Langfuse uses ~4MB; we
	// allow 10MB to support large multi-trace batches without surprising
	// customers. Tier-based caps live in retention policy (Phase 2).
	MaxBodyBytes = 10 * 1024 * 1024
)

// TracesHandler ingests OTLP/HTTP trace exports into ClickHouse.
type TracesHandler struct {
	conn        clickhouse.Conn
	recordBytes func(tenantID string, byteCount int)
}

// NewTracesHandler builds a TracesHandler. The ClickHouse connection must be
// non-nil; if no ClickHouse is configured the route should not be mounted at
// all (see start_api.go).
func NewTracesHandler(conn clickhouse.Conn) *TracesHandler {
	return &TracesHandler{conn: conn}
}

// SetByteRecorder wires a per-tenant processed-bytes meter. When set, each
// ingest reports its decompressed body size for usage billing. Optional; nil
// leaves metering off.
func (h *TracesHandler) SetByteRecorder(fn func(tenantID string, byteCount int)) {
	h.recordBytes = fn
}

// ServeHTTP implements http.Handler.
func (h *TracesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tenantID := contextkeys.GetTenantID(r.Context())
	if tenantID == "" {
		tenantID = database.TenantSchemaFromContext(r.Context())
	}
	if tenantID == "" {
		// The api-key middleware is what resolves the tenant. If we got here
		// without one, the route was mounted outside that middleware — refuse
		// rather than silently using a fallback. Same isolation rule as the
		// gRPC read path.
		http.Error(w, "tenant not resolved", http.StatusUnauthorized)
		return
	}

	body, err := readBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	// Meter decompressed ingest bytes per tenant (post-decompression,
	// pre-enrichment) for usage billing. Buffered + flushed by the meter.
	if h.recordBytes != nil {
		h.recordBytes(tenantID, len(body))
	}

	req := &coltracepb.ExportTraceServiceRequest{}
	contentType := strings.ToLower(strings.TrimSpace(strings.Split(r.Header.Get("Content-Type"), ";")[0]))
	switch contentType {
	case "", "application/x-protobuf", "application/protobuf":
		if err := proto.Unmarshal(body, req); err != nil {
			http.Error(w, fmt.Sprintf("invalid protobuf body: %s", err), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	case "application/json":
		if err := protojson.Unmarshal(body, req); err != nil {
			http.Error(w, fmt.Sprintf("invalid json body: %s", err), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
	default:
		http.Error(w, "unsupported content-type, expected application/x-protobuf or application/json", http.StatusUnsupportedMediaType)
		return
	}

	rejected, accepted, err := h.insertSpans(r.Context(), tenantID, req.ResourceSpans)
	if err != nil {
		logger.WithError(err).WithFields(map[string]interface{}{"tenant_id": tenantID}).Error("otlp/http: failed to insert spans")
		http.Error(w, "failed to persist spans", http.StatusInternalServerError)
		return
	}

	resp := &coltracepb.ExportTraceServiceResponse{}
	if rejected > 0 {
		// OTLP allows partial success — the client decides whether to retry.
		resp.PartialSuccess = &coltracepb.ExportTracePartialSuccess{
			RejectedSpans: int64(rejected),
			ErrorMessage:  "one or more spans rejected; see server logs",
		}
	}
	logger.WithFields("tenant_id", tenantID, "accepted", accepted, "rejected", rejected).Debug("otlp/http: ingest complete")

	if contentType == "application/json" {
		out, _ := protojson.Marshal(resp)
		_, _ = w.Write(out)
	} else {
		out, _ := proto.Marshal(resp)
		_, _ = w.Write(out)
	}
}

// readBody handles gzip decompression and size capping.
func readBody(r *http.Request) ([]byte, error) {
	var reader io.Reader = r.Body
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, fmt.Errorf("invalid gzip body: %w", err)
		}
		defer gz.Close()
		reader = gz
	}
	limited := io.LimitReader(reader, MaxBodyBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("failed to read body: %w", err)
	}
	if len(body) > MaxBodyBytes {
		return nil, errors.New("body exceeds max size")
	}
	return body, nil
}

// insertSpans flattens ResourceSpans into otel_traces rows. The caller's
// authenticated tenantID is stamped onto both resource and span attributes,
// overriding anything the client sent (clients cannot impersonate other
// tenants — see feedback_no_tenant_leaks.md).
func (h *TracesHandler) insertSpans(ctx context.Context, tenantID string, rss []*tracepb.ResourceSpans) (rejected, accepted int, err error) {
	if len(rss) == 0 {
		return 0, 0, nil
	}

	batch, err := h.conn.PrepareBatch(ctx, `INSERT INTO otel_traces (
		Timestamp, TraceId, SpanId, ParentSpanId, TraceState,
		SpanName, SpanKind, ServiceName, ResourceAttributes,
		ScopeName, ScopeVersion, SpanAttributes, Duration,
		StatusCode, StatusMessage,
		Events.Timestamp, Events.Name, Events.Attributes,
		Links.TraceId, Links.SpanId, Links.TraceState, Links.Attributes
	)`)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare batch: %w", err)
	}

	for _, rs := range rss {
		resAttrs := attrsToMap(rs.GetResource().GetAttributes())
		// Tenant is always the authenticated caller's tenant — never trust
		// the client-supplied tenant.id.
		resAttrs["tenant.id"] = tenantID
		serviceName := resAttrs["service.name"]

		for _, ss := range rs.GetScopeSpans() {
			scopeName := ss.GetScope().GetName()
			scopeVersion := ss.GetScope().GetVersion()

			for _, span := range ss.GetSpans() {
				if span.GetTraceId() == nil || span.GetSpanId() == nil {
					rejected++
					continue
				}
				spanAttrs := attrsToMap(span.GetAttributes())
				// Same tenant stamp on span attributes — read handlers look at both.
				spanAttrs["tenant.id"] = tenantID
				// Price LLM spans that arrive without a cost (coding agents
				// report tokens but no cost — see enrichSpanCost).
				enrichSpanCost(spanAttrs)

				traceID := bytesToHex(span.GetTraceId())
				spanID := bytesToHex(span.GetSpanId())
				parentID := bytesToHex(span.GetParentSpanId())

				startNs := int64(span.GetStartTimeUnixNano())
				endNs := int64(span.GetEndTimeUnixNano())
				duration := int64(0)
				if endNs > startNs {
					duration = endNs - startNs
				} else if startNs > 0 && endNs == 0 {
					// Logfire-style pending span — the client sent the
					// span at start time and will follow up at end time.
					// Flag it so the read path renders a live progress bar
					// (see trace-waterfall.tsx). Duration left at 0 so the
					// UI can compute it from wall-clock-now minus startNs.
					spanAttrs["span.pending"] = "true"
				}
				start := time.Unix(0, startNs)

				eventsTS, eventsNames, eventsAttrs := flattenEvents(span.GetEvents())
				linksTrace, linksSpan, linksState, linksAttrs := flattenLinks(span.GetLinks())

				if err := batch.Append(
					start,
					traceID,
					spanID,
					parentID,
					span.GetTraceState(),
					span.GetName(),
					spanKindString(span.GetKind()),
					serviceName,
					resAttrs,
					scopeName,
					scopeVersion,
					spanAttrs,
					duration,
					statusCodeString(span.GetStatus().GetCode()),
					span.GetStatus().GetMessage(),
					eventsTS, eventsNames, eventsAttrs,
					linksTrace, linksSpan, linksState, linksAttrs,
				); err != nil {
					logger.WithError(err).WithFields(map[string]interface{}{"tenant_id": tenantID, "trace_id": traceID}).Warn("otlp/http: failed to append span to batch")
					rejected++
					continue
				}
				accepted++
			}
		}
	}

	if accepted == 0 {
		// Nothing to flush; release the batch.
		if abortErr := batch.Abort(); abortErr != nil {
			logger.WithError(abortErr).Debug("otlp/http: batch abort failed")
		}
		return rejected, 0, nil
	}

	if err := batch.Send(); err != nil {
		return rejected, accepted, fmt.Errorf("send batch: %w", err)
	}
	return rejected, accepted, nil
}

// attrsToMap converts OTLP KeyValues to a ClickHouse string map. Non-string
// values are JSON-encoded so the source value is preserved as a string.
func attrsToMap(kvs []*commonpb.KeyValue) map[string]string {
	m := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		if kv == nil || kv.Key == "" {
			continue
		}
		m[kv.Key] = anyValueToString(kv.Value)
	}
	return m
}

func anyValueToString(v *commonpb.AnyValue) string {
	if v == nil {
		return ""
	}
	switch tv := v.GetValue().(type) {
	case *commonpb.AnyValue_StringValue:
		return tv.StringValue
	case *commonpb.AnyValue_BoolValue:
		if tv.BoolValue {
			return "true"
		}
		return "false"
	case *commonpb.AnyValue_IntValue:
		return fmt.Sprintf("%d", tv.IntValue)
	case *commonpb.AnyValue_DoubleValue:
		return fmt.Sprintf("%g", tv.DoubleValue)
	case *commonpb.AnyValue_BytesValue:
		return bytesToHex(tv.BytesValue)
	case *commonpb.AnyValue_ArrayValue, *commonpb.AnyValue_KvlistValue:
		// Round-trip via protojson so consumers still see a meaningful value.
		out, err := protojson.Marshal(v)
		if err != nil {
			return ""
		}
		return string(out)
	}
	return ""
}

func flattenEvents(events []*tracepb.Span_Event) ([]time.Time, []string, []map[string]string) {
	if len(events) == 0 {
		return nil, nil, nil
	}
	ts := make([]time.Time, 0, len(events))
	names := make([]string, 0, len(events))
	attrs := make([]map[string]string, 0, len(events))
	for _, e := range events {
		ts = append(ts, time.Unix(0, int64(e.GetTimeUnixNano())))
		names = append(names, e.GetName())
		attrs = append(attrs, attrsToMap(e.GetAttributes()))
	}
	return ts, names, attrs
}

func flattenLinks(links []*tracepb.Span_Link) ([]string, []string, []string, []map[string]string) {
	if len(links) == 0 {
		return nil, nil, nil, nil
	}
	tIDs := make([]string, 0, len(links))
	sIDs := make([]string, 0, len(links))
	states := make([]string, 0, len(links))
	attrs := make([]map[string]string, 0, len(links))
	for _, l := range links {
		tIDs = append(tIDs, bytesToHex(l.GetTraceId()))
		sIDs = append(sIDs, bytesToHex(l.GetSpanId()))
		states = append(states, l.GetTraceState())
		attrs = append(attrs, attrsToMap(l.GetAttributes()))
	}
	return tIDs, sIDs, states, attrs
}

func bytesToHex(b []byte) string {
	if len(b) == 0 {
		return ""
	}
	const hex = "0123456789abcdef"
	out := make([]byte, len(b)*2)
	for i, v := range b {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&0x0f]
	}
	return string(out)
}

func spanKindString(k tracepb.Span_SpanKind) string {
	switch k {
	case tracepb.Span_SPAN_KIND_INTERNAL:
		return "Internal"
	case tracepb.Span_SPAN_KIND_SERVER:
		return "Server"
	case tracepb.Span_SPAN_KIND_CLIENT:
		return "Client"
	case tracepb.Span_SPAN_KIND_PRODUCER:
		return "Producer"
	case tracepb.Span_SPAN_KIND_CONSUMER:
		return "Consumer"
	}
	return "Unspecified"
}

func statusCodeString(c tracepb.Status_StatusCode) string {
	switch c {
	case tracepb.Status_STATUS_CODE_OK:
		return "STATUS_CODE_OK"
	case tracepb.Status_STATUS_CODE_ERROR:
		return "STATUS_CODE_ERROR"
	}
	return "STATUS_CODE_UNSET"
}
