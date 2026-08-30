package otlp

// OTLP/HTTP metrics receiver. Mirrors handler.go (traces) but writes OTLP
// metric data points into the otel_metrics ClickHouse table (one row per
// data point, the flattened layout from build/clickhouse-init.sql).
//
// Scope: Gauge and Sum (NumberDataPoint) metrics — which covers the counters
// and gauges emitted by clients like Claude Code (token/cost/session counts).
// Histogram, ExponentialHistogram and Summary data points are not flattenable
// into the single Value column, so they're skipped and reported as rejected
// via OTLP partial-success rather than silently dropped.
//
// The api-key middleware resolves the tenant; we stamp it onto every data
// point's resource + data-point attributes (clients cannot impersonate other
// tenants — see feedback_no_tenant_leaks.md).

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// MetricsHandler ingests OTLP/HTTP metric exports into ClickHouse.
type MetricsHandler struct {
	conn        clickhouse.Conn
	recordBytes func(tenantID string, byteCount int)
}

// NewMetricsHandler builds a MetricsHandler. The ClickHouse connection must be
// non-nil; if no ClickHouse is configured the route should not be mounted.
func NewMetricsHandler(conn clickhouse.Conn) *MetricsHandler {
	return &MetricsHandler{conn: conn}
}

// SetByteRecorder wires a per-tenant processed-bytes meter (optional).
func (h *MetricsHandler) SetByteRecorder(fn func(tenantID string, byteCount int)) {
	h.recordBytes = fn
}

// ServeHTTP implements http.Handler.
func (h *MetricsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	tenantID := contextkeys.GetTenantID(r.Context())
	if tenantID == "" {
		tenantID = database.TenantSchemaFromContext(r.Context())
	}
	if tenantID == "" {
		http.Error(w, "tenant not resolved", http.StatusUnauthorized)
		return
	}

	body, err := readBody(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if h.recordBytes != nil {
		h.recordBytes(tenantID, len(body))
	}

	req := &colmetricspb.ExportMetricsServiceRequest{}
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

	rejected, accepted, err := h.insertMetrics(r.Context(), tenantID, req.ResourceMetrics)
	if err != nil {
		logger.WithError(err).WithFields(map[string]interface{}{"tenant_id": tenantID}).Error("otlp/http: failed to insert metrics")
		http.Error(w, "failed to persist metrics", http.StatusInternalServerError)
		return
	}

	resp := &colmetricspb.ExportMetricsServiceResponse{}
	if rejected > 0 {
		resp.PartialSuccess = &colmetricspb.ExportMetricsPartialSuccess{
			RejectedDataPoints: int64(rejected),
			ErrorMessage:       "non-NumberDataPoint metrics (histogram/summary) are not stored",
		}
	}
	logger.WithFields("tenant_id", tenantID, "accepted", accepted, "rejected", rejected).Debug("otlp/http: metrics ingest complete")

	if contentType == "application/json" {
		out, _ := protojson.Marshal(resp)
		_, _ = w.Write(out)
	} else {
		out, _ := proto.Marshal(resp)
		_, _ = w.Write(out)
	}
}

// metricBatch accumulates flattened data-point rows for one INSERT.
type metricBatch struct {
	batch    driver.Batch
	tenantID string
	rejected int
	accepted int
}

// insertMetrics flattens ResourceMetrics into otel_metrics rows.
func (h *MetricsHandler) insertMetrics(ctx context.Context, tenantID string, rms []*metricspb.ResourceMetrics) (rejected, accepted int, err error) {
	if len(rms) == 0 {
		return 0, 0, nil
	}

	batch, err := h.conn.PrepareBatch(ctx, `INSERT INTO otel_metrics (
		ResourceAttributes, ResourceSchemaUrl,
		ScopeName, ScopeVersion, ScopeAttributes, ScopeDroppedAttrCount, ScopeSchemaUrl,
		MetricName, MetricDescription, MetricUnit,
		Attributes, StartTimeUnix, TimeUnix, Value, Flags,
		AggTemp, IsMonotonic
	)`)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare batch: %w", err)
	}

	mb := &metricBatch{batch: batch, tenantID: tenantID}

	for _, rm := range rms {
		resAttrs := attrsToMap(rm.GetResource().GetAttributes())
		resAttrs["tenant.id"] = tenantID
		resourceSchemaURL := rm.GetSchemaUrl()

		for _, sm := range rm.GetScopeMetrics() {
			sc := scopeRow{
				name:              sm.GetScope().GetName(),
				version:           sm.GetScope().GetVersion(),
				attrs:             attrsToMap(sm.GetScope().GetAttributes()),
				droppedAttrCount:  sm.GetScope().GetDroppedAttributesCount(),
				schemaURL:         sm.GetSchemaUrl(),
				resAttrs:          resAttrs,
				resourceSchemaURL: resourceSchemaURL,
			}

			for _, m := range sm.GetMetrics() {
				switch data := m.GetData().(type) {
				case *metricspb.Metric_Gauge:
					mb.appendNumberPoints(m, sc, data.Gauge.GetDataPoints(), 0, false)
				case *metricspb.Metric_Sum:
					mb.appendNumberPoints(m, sc, data.Sum.GetDataPoints(), int32(data.Sum.GetAggregationTemporality()), data.Sum.GetIsMonotonic())
				default:
					// Histogram / ExponentialHistogram / Summary: not flattenable
					// into a single Value column. Count their points as rejected.
					mb.rejected += countOtherDataPoints(m)
				}
			}
		}
	}

	if mb.accepted == 0 {
		if abortErr := batch.Abort(); abortErr != nil {
			logger.WithError(abortErr).Debug("otlp/http: metrics batch abort failed")
		}
		return mb.rejected, 0, nil
	}

	if err := batch.Send(); err != nil {
		return mb.rejected, mb.accepted, fmt.Errorf("send batch: %w", err)
	}
	return mb.rejected, mb.accepted, nil
}

// scopeRow carries the per-scope/per-resource columns shared by every data
// point under that scope.
type scopeRow struct {
	name              string
	version           string
	attrs             map[string]string
	droppedAttrCount  uint32
	schemaURL         string
	resAttrs          map[string]string
	resourceSchemaURL string
}

func (mb *metricBatch) appendNumberPoints(m *metricspb.Metric, sc scopeRow, dps []*metricspb.NumberDataPoint, aggTemp int32, isMonotonic bool) {
	for _, dp := range dps {
		dpAttrs := attrsToMap(dp.GetAttributes())
		dpAttrs["tenant.id"] = mb.tenantID

		var value float64
		switch dp.GetValue().(type) {
		case *metricspb.NumberDataPoint_AsInt:
			value = float64(dp.GetAsInt())
		case *metricspb.NumberDataPoint_AsDouble:
			value = dp.GetAsDouble()
		}

		if err := mb.batch.Append(
			sc.resAttrs,
			sc.resourceSchemaURL,
			sc.name,
			sc.version,
			sc.attrs,
			sc.droppedAttrCount,
			sc.schemaURL,
			m.GetName(),
			m.GetDescription(),
			m.GetUnit(),
			dpAttrs,
			time.Unix(0, int64(dp.GetStartTimeUnixNano())),
			time.Unix(0, int64(dp.GetTimeUnixNano())),
			value,
			dp.GetFlags(),
			aggTemp,
			isMonotonic,
		); err != nil {
			logger.WithError(err).WithFields(map[string]interface{}{"tenant_id": mb.tenantID, "metric": m.GetName()}).Warn("otlp/http: failed to append metric point to batch")
			mb.rejected++
			continue
		}
		mb.accepted++
	}
}

// countOtherDataPoints returns the number of data points carried by metric
// types we don't persist, so partial-success reporting is accurate.
func countOtherDataPoints(m *metricspb.Metric) int {
	switch data := m.GetData().(type) {
	case *metricspb.Metric_Histogram:
		return len(data.Histogram.GetDataPoints())
	case *metricspb.Metric_ExponentialHistogram:
		return len(data.ExponentialHistogram.GetDataPoints())
	case *metricspb.Metric_Summary:
		return len(data.Summary.GetDataPoints())
	}
	return 0
}
