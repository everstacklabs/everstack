package traces

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/telemetry/scores"
)

// RichTraceQueryHandler handles rich trace queries with Everstack compatibility
type RichTraceQueryHandler struct {
	db            *sql.DB
	scoreRecorder *scores.Recorder
}

// NewRichTraceQueryHandler creates a new handler
func NewRichTraceQueryHandler(db *sql.DB) *RichTraceQueryHandler {
	return &RichTraceQueryHandler{
		db:            db,
		scoreRecorder: scores.NewRecorder(db),
	}
}

// GetTraceByID retrieves a single trace with all observations and scores
func (h *RichTraceQueryHandler) GetTraceByID(ctx context.Context, traceID string) (*EverstackTrace, error) {
	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		return nil, fmt.Errorf("trace not found: %s", traceID)
	}
	tenantClause, tenantArgs := tenantBridgeFilter(tenantID)
	tenantFilter := "AND " + tenantClause

	// Query all spans for this trace
	query := fmt.Sprintf(`
		SELECT
			Timestamp, TraceId, SpanId, ParentSpanId, SpanName, SpanKind,
			ServiceName, toInt64(Duration) as Duration, StatusCode, StatusMessage,
			SpanAttributes, ResourceAttributes
		FROM otel_traces
		WHERE TraceId = ? %s
		ORDER BY Timestamp ASC
	`, tenantFilter)

	queryArgs := append([]interface{}{traceID}, tenantArgs...)

	rows, err := h.db.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return nil, fmt.Errorf("failed to query traces: %w", err)
	}
	defer rows.Close()

	var spans []ClickHouseTrace
	for rows.Next() {
		span, err := h.scanTrace(rows)
		if err != nil {
			logger.WithFields("trace_id", traceID, "error", err.Error()).Warn("failed to scan span")
			continue
		}
		spans = append(spans, span)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating traces: %w", err)
	}

	if len(spans) == 0 {
		return nil, fmt.Errorf("trace not found: %s", traceID)
	}

	// Get scores for this trace
	traceScores, err := h.scoreRecorder.GetScoresByTrace(ctx, traceID)
	if err != nil {
		logger.WithFields("trace_id", traceID, "error", err.Error()).Warn("failed to get scores")
		traceScores = []*scores.Score{}
	}

	// Convert scores to Everstack format
	everstackScores := make([]EverstackScore, 0, len(traceScores))
	for _, s := range traceScores {
		everstackScores = append(everstackScores, convertScore(s))
	}

	// Transform to Everstack format
	scoreMap := map[string][]EverstackScore{
		traceID: everstackScores,
	}

	everstackTraces := TransformTrace(spans, scoreMap)
	if len(everstackTraces) == 0 {
		return nil, fmt.Errorf("failed to transform trace")
	}

	return &everstackTraces[0], nil
}

// ListTraces retrieves traces with filtering
func (h *RichTraceQueryHandler) ListTraces(ctx context.Context, filter TraceFilter) ([]EverstackTrace, error) {
	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		// Fail closed — see metrics_dashboard.go for rationale.
		return []EverstackTrace{}, nil
	}

	listTenantClause, listTenantArgs := tenantBridgeFilter(tenantID)
	queryBuilder := strings.Builder{}
	queryBuilder.WriteString(fmt.Sprintf(`
		SELECT
			Timestamp, TraceId, SpanId, ParentSpanId, SpanName, SpanKind,
			ServiceName, toInt64(Duration) as Duration, StatusCode, StatusMessage,
			SpanAttributes, ResourceAttributes
		FROM otel_traces
		WHERE %s
	`, listTenantClause))

	args := append([]interface{}{}, listTenantArgs...)

	// Apply filters
	if len(filter.TraceIDs) > 0 {
		placeholders := make([]string, len(filter.TraceIDs))
		for i, tid := range filter.TraceIDs {
			placeholders[i] = "?"
			args = append(args, tid)
		}
		queryBuilder.WriteString(fmt.Sprintf(" AND TraceId IN (%s)", strings.Join(placeholders, ",")))
	}

	if filter.SessionID != nil {
		queryBuilder.WriteString(" AND SpanAttributes['trace.session_id'] = ?")
		args = append(args, *filter.SessionID)
	}

	if filter.ThreadID != nil {
		queryBuilder.WriteString(" AND SpanAttributes['trace.thread_id'] = ?")
		args = append(args, *filter.ThreadID)
	}

	if filter.UserID != nil {
		queryBuilder.WriteString(" AND SpanAttributes['trace.user_id'] = ?")
		args = append(args, *filter.UserID)
	}

	if filter.Model != nil {
		queryBuilder.WriteString(" AND " + modelSQL() + " = ?")
		args = append(args, *filter.Model)
	}

	if filter.Environment != nil {
		queryBuilder.WriteString(" AND ResourceAttributes['deployment.environment'] = ?")
		args = append(args, *filter.Environment)
	}

	if filter.FromTime != nil {
		queryBuilder.WriteString(" AND Timestamp >= ?")
		args = append(args, *filter.FromTime)
	}

	if filter.ToTime != nil {
		queryBuilder.WriteString(" AND Timestamp <= ?")
		args = append(args, *filter.ToTime)
	}

	queryBuilder.WriteString(" ORDER BY Timestamp DESC")

	if filter.Limit > 0 {
		queryBuilder.WriteString(" LIMIT ?")
		args = append(args, filter.Limit)
	}

	if filter.Offset > 0 {
		queryBuilder.WriteString(" OFFSET ?")
		args = append(args, filter.Offset)
	}

	rows, err := h.db.QueryContext(ctx, queryBuilder.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query traces: %w", err)
	}
	defer rows.Close()

	var spans []ClickHouseTrace
	for rows.Next() {
		span, err := h.scanTrace(rows)
		if err != nil {
			logger.WithFields("error", err.Error()).Warn("failed to scan span")
			continue
		}
		spans = append(spans, span)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating traces: %w", err)
	}

	// Get unique trace IDs
	traceIDs := make(map[string]struct{})
	for _, span := range spans {
		traceIDs[span.TraceID] = struct{}{}
	}

	// Note: For efficiency, we skip loading scores in list view
	// Scores can be loaded when fetching individual traces
	scoreMap := make(map[string][]EverstackScore)

	return TransformTrace(spans, scoreMap), nil
}

// scanTrace scans a database row into a ClickHouseTrace
func (h *RichTraceQueryHandler) scanTrace(scanner interface {
	Scan(dest ...interface{}) error
}) (ClickHouseTrace, error) {
	var trace ClickHouseTrace
	var spanAttrs, resourceAttrs map[string]string

	err := scanner.Scan(
		&trace.Timestamp,
		&trace.TraceID,
		&trace.SpanID,
		&trace.ParentSpanID,
		&trace.SpanName,
		&trace.SpanKind,
		&trace.ServiceName,
		&trace.Duration,
		&trace.StatusCode,
		&trace.StatusMessage,
		&spanAttrs,
		&resourceAttrs,
	)

	if err != nil {
		return ClickHouseTrace{}, err
	}

	trace.SpanAttributes = spanAttrs
	trace.ResourceAttributes = resourceAttrs

	return trace, nil
}

// convertScore converts internal score to Everstack format
func convertScore(s *scores.Score) EverstackScore {
	ls := EverstackScore{
		ID:        s.ID,
		TraceID:   s.TraceID,
		Name:      s.Name,
		Source:    string(s.Source),
		Timestamp: s.Timestamp,
		CreatedAt: s.CreatedAt,
		UpdatedAt: s.UpdatedAt,
		DataType:  string(s.DataType),
	}

	if s.ObservationID != "" {
		ls.ObservationID = &s.ObservationID
	}

	if s.AuthorUserID != "" {
		ls.AuthorUserID = &s.AuthorUserID
	}

	if s.Comment != "" {
		ls.Comment = &s.Comment
	}

	if s.ConfigID != "" {
		ls.ConfigID = &s.ConfigID
	}

	if s.QueueID != "" {
		ls.QueueID = &s.QueueID
	}

	if s.Environment != "" {
		ls.Environment = &s.Environment
	}

	if len(s.Metadata) > 0 {
		ls.Metadata = s.Metadata
	}

	// Set appropriate value based on data type
	if s.NumericValue != nil {
		ls.Value = s.NumericValue
	}

	if s.StringValue != nil {
		ls.StringValue = s.StringValue
	}

	if s.BooleanValue != nil {
		if *s.BooleanValue {
			strVal := "True"
			ls.StringValue = &strVal
		} else {
			strVal := "False"
			ls.StringValue = &strVal
		}
	}

	return ls
}
