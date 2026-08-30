package traces

import (
	"context"
	"fmt"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/google/uuid"

	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
)

// GetTraceLogsQuery retrieves the OTLP log records correlated to a trace.
type GetTraceLogsQuery struct {
	query.BaseQuery
	TraceID   string `json:"trace_id"`
	SessionID string `json:"session_id"`
}

// NewGetTraceLogsQuery builds a GetTraceLogsQuery. sessionID is optional and
// only used as a correlation fallback for log records that carry no trace id.
func NewGetTraceLogsQuery(traceID, sessionID string) *GetTraceLogsQuery {
	return &GetTraceLogsQuery{
		BaseQuery: query.BaseQuery{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
		},
		TraceID:   traceID,
		SessionID: sessionID,
	}
}

func (q GetTraceLogsQuery) QueryType() string { return "GetTraceLogs" }

func (q GetTraceLogsQuery) Validate() error {
	if q.TraceID == "" {
		return fmt.Errorf("trace_id cannot be empty")
	}
	return nil
}

// TraceLogsHandler answers GetTraceLogsQuery from the otel_logs table. It is a
// generic, per-record view (unlike the gateway-specific LogsQueryHandler), so it
// works for any OTLP log source — including coding agents like Claude Code whose
// log events carry no correlation_id and aren't tagged log_category=operational.
type TraceLogsHandler struct {
	conn clickhouse.Conn
}

func NewTraceLogsHandler(conn clickhouse.Conn) *TraceLogsHandler {
	return &TraceLogsHandler{conn: conn}
}

func (h *TraceLogsHandler) QueryType() string { return "GetTraceLogs" }

// traceLogsLimit caps records returned for a single trace. A long agent session
// can emit thousands of log events; this keeps the response bounded.
const traceLogsLimit = 2000

func (h *TraceLogsHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	lq, ok := q.(*GetTraceLogsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for TraceLogsHandler")
	}

	// Logs are tenant-private. Fail closed: without a resolved tenant we return
	// nothing rather than risk a cross-tenant read via trace-id collision. The
	// OTLP logs receiver stamps the tenant onto LogAttributes['tenant.id'] (dot),
	// so that — not the gateway logger's 'tenant_id' (underscore) — is the key.
	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		return []query.TraceLogReadModel{}, nil
	}

	// Primary correlation: the receiver copies the OTLP record's TraceId
	// verbatim, so any SDK that propagates trace context onto its log records
	// (gateway, OTel GenAI) matches directly.
	where := "LogAttributes['tenant.id'] = ? AND TraceId = ?"
	args := []interface{}{tenantID, lq.TraceID}

	// Fallback for SDKs that emit logs without trace context: match the trace's
	// session id, bounded to the trace's time window so we don't pull the whole
	// session's logs across unrelated traces.
	if lq.SessionID != "" {
		if from, to, ok := h.traceWindow(ctx, lq.TraceID, tenantID); ok {
			where = "LogAttributes['tenant.id'] = ? AND (" +
				"TraceId = ? OR (" +
				"(LogAttributes['session.id'] = ? OR LogAttributes['session_id'] = ? OR LogAttributes['gen_ai.conversation.id'] = ?)" +
				" AND Timestamp BETWEEN ? AND ?))"
			args = []interface{}{tenantID, lq.TraceID, lq.SessionID, lq.SessionID, lq.SessionID, from, to}
		}
	}

	sqlQuery := fmt.Sprintf(`
		SELECT Timestamp, SeverityText, SeverityNumber, Body, SpanId, ScopeName, ServiceName, LogAttributes
		FROM otel_logs
		WHERE %s
		ORDER BY Timestamp ASC
		LIMIT %d
	`, where, traceLogsLimit)

	rows, err := h.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		logger.WithFields("trace_id", lq.TraceID, "error", err.Error()).Error("failed to query trace logs")
		return nil, fmt.Errorf("failed to query trace logs: %w", err)
	}
	defer rows.Close()

	out := make([]query.TraceLogReadModel, 0)
	for rows.Next() {
		var (
			m      query.TraceLogReadModel
			sevNum uint8 // SeverityNumber is UInt8 in otel_logs
		)
		if err := rows.Scan(
			&m.Timestamp,
			&m.SeverityText,
			&sevNum,
			&m.Body,
			&m.SpanID,
			&m.ScopeName,
			&m.ServiceName,
			&m.Attributes,
		); err != nil {
			logger.WithFields("error", err.Error()).Warn("failed to scan trace log row")
			continue
		}
		m.SeverityNumber = int32(sevNum)
		out = append(out, m)
	}

	return out, nil
}

// traceWindow returns the trace's span time range, padded by a minute on each
// side, to bound the session-id log fallback. ok is false when the trace has no
// spans for this tenant (in which case we keep the strict TraceId-only match).
func (h *TraceLogsHandler) traceWindow(ctx context.Context, traceID, tenantID string) (from, to time.Time, ok bool) {
	clause, targs := tenantBridgeFilter(tenantID)
	sqlQuery := fmt.Sprintf("SELECT min(Timestamp), max(Timestamp) FROM otel_traces WHERE TraceId = ? AND %s", clause)
	args := append([]interface{}{traceID}, targs...)

	row := h.conn.QueryRow(ctx, sqlQuery, args...)
	if err := row.Scan(&from, &to); err != nil {
		return time.Time{}, time.Time{}, false
	}
	if from.IsZero() || to.IsZero() {
		return time.Time{}, time.Time{}, false
	}
	return from.Add(-time.Minute), to.Add(time.Minute), true
}
