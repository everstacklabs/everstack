package otlp

// OTLP/HTTP logs receiver. Mirrors handler.go (traces) but writes OTLP log
// records into the otel_logs ClickHouse table. The api-key middleware resolves
// the tenant; we stamp it onto every record's resource + log attributes before
// insert, overriding anything the client sent (clients cannot impersonate other
// tenants — see feedback_no_tenant_leaks.md).

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
)

// LogsHandler ingests OTLP/HTTP log exports into ClickHouse.
type LogsHandler struct {
	conn        clickhouse.Conn
	recordBytes func(tenantID string, byteCount int)
}

// NewLogsHandler builds a LogsHandler. The ClickHouse connection must be
// non-nil; if no ClickHouse is configured the route should not be mounted.
func NewLogsHandler(conn clickhouse.Conn) *LogsHandler {
	return &LogsHandler{conn: conn}
}

// SetByteRecorder wires a per-tenant processed-bytes meter (optional).
func (h *LogsHandler) SetByteRecorder(fn func(tenantID string, byteCount int)) {
	h.recordBytes = fn
}

// ServeHTTP implements http.Handler.
func (h *LogsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

	req := &collogspb.ExportLogsServiceRequest{}
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

	rejected, accepted, err := h.insertLogs(r.Context(), tenantID, req.ResourceLogs)
	if err != nil {
		logger.WithError(err).WithFields(map[string]interface{}{"tenant_id": tenantID}).Error("otlp/http: failed to insert logs")
		http.Error(w, "failed to persist logs", http.StatusInternalServerError)
		return
	}

	resp := &collogspb.ExportLogsServiceResponse{}
	if rejected > 0 {
		resp.PartialSuccess = &collogspb.ExportLogsPartialSuccess{
			RejectedLogRecords: int64(rejected),
			ErrorMessage:       "one or more log records rejected; see server logs",
		}
	}
	logger.WithFields("tenant_id", tenantID, "accepted", accepted, "rejected", rejected).Debug("otlp/http: logs ingest complete")

	if contentType == "application/json" {
		out, _ := protojson.Marshal(resp)
		_, _ = w.Write(out)
	} else {
		out, _ := proto.Marshal(resp)
		_, _ = w.Write(out)
	}
}

// insertLogs flattens ResourceLogs into otel_logs rows, stamping the
// authenticated tenant onto resource + log attributes.
func (h *LogsHandler) insertLogs(ctx context.Context, tenantID string, rls []*logspb.ResourceLogs) (rejected, accepted int, err error) {
	if len(rls) == 0 {
		return 0, 0, nil
	}

	batch, err := h.conn.PrepareBatch(ctx, `INSERT INTO otel_logs (
		Timestamp, TraceId, SpanId, TraceFlags,
		SeverityText, SeverityNumber, ServiceName, Body,
		ResourceSchemaUrl, ResourceAttributes,
		ScopeSchemaUrl, ScopeName, ScopeVersion, ScopeAttributes,
		LogAttributes
	)`)
	if err != nil {
		return 0, 0, fmt.Errorf("prepare batch: %w", err)
	}

	for _, rl := range rls {
		resAttrs := attrsToMap(rl.GetResource().GetAttributes())
		resAttrs["tenant.id"] = tenantID
		serviceName := resAttrs["service.name"]
		resourceSchemaURL := rl.GetSchemaUrl()

		for _, sl := range rl.GetScopeLogs() {
			scopeName := sl.GetScope().GetName()
			scopeVersion := sl.GetScope().GetVersion()
			scopeAttrs := attrsToMap(sl.GetScope().GetAttributes())
			scopeSchemaURL := sl.GetSchemaUrl()

			for _, lr := range sl.GetLogRecords() {
				logAttrs := attrsToMap(lr.GetAttributes())
				logAttrs["tenant.id"] = tenantID

				tsNs := int64(lr.GetTimeUnixNano())
				if tsNs == 0 {
					tsNs = int64(lr.GetObservedTimeUnixNano())
				}

				if err := batch.Append(
					time.Unix(0, tsNs),
					bytesToHex(lr.GetTraceId()),
					bytesToHex(lr.GetSpanId()),
					uint8(lr.GetFlags()),
					lr.GetSeverityText(),
					uint8(lr.GetSeverityNumber()),
					serviceName,
					anyValueToString(lr.GetBody()),
					resourceSchemaURL,
					resAttrs,
					scopeSchemaURL,
					scopeName,
					scopeVersion,
					scopeAttrs,
					logAttrs,
				); err != nil {
					logger.WithError(err).WithFields(map[string]interface{}{"tenant_id": tenantID}).Warn("otlp/http: failed to append log record to batch")
					rejected++
					continue
				}
				accepted++
			}
		}
	}

	if accepted == 0 {
		if abortErr := batch.Abort(); abortErr != nil {
			logger.WithError(abortErr).Debug("otlp/http: logs batch abort failed")
		}
		return rejected, 0, nil
	}

	if err := batch.Send(); err != nil {
		return rejected, accepted, fmt.Errorf("send batch: %w", err)
	}
	return rejected, accepted, nil
}
