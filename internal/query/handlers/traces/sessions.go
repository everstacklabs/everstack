package traces

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
)

// ============================================================================
// ListSessionsHandler
// ============================================================================

// ListSessionsHandler queries trace_sessions with filtering and pagination
type ListSessionsHandler struct {
	conn clickhouse.Conn
}

func NewListSessionsHandler(conn clickhouse.Conn) *ListSessionsHandler {
	return &ListSessionsHandler{conn: conn}
}

func (h *ListSessionsHandler) QueryType() string {
	return "ListTraceSessions"
}

func (h *ListSessionsHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	listQuery, ok := q.(*ListSessionsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for ListSessionsHandler")
	}

	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		// Fail closed — see metrics_dashboard.go for rationale.
		return SessionListResult{Sessions: nil, TotalCount: 0}, nil
	}

	whereConditions := []string{"tenant_id = ?"}
	args := []interface{}{tenantID}

	if !listQuery.StartTime.IsZero() {
		whereConditions = append(whereConditions, "last_trace_at >= ?")
		args = append(args, listQuery.StartTime)
	}

	if !listQuery.EndTime.IsZero() {
		whereConditions = append(whereConditions, "first_trace_at <= ?")
		args = append(args, listQuery.EndTime)
	}

	if listQuery.UserID != "" {
		whereConditions = append(whereConditions, "user_id = ?")
		args = append(args, listQuery.UserID)
	}

	if listQuery.Search != "" {
		whereConditions = append(whereConditions, "session_id LIKE ?")
		args = append(args, "%"+listQuery.Search+"%")
	}

	// Determine ORDER BY
	orderBy := "last_trace_at DESC"
	switch listQuery.OrderBy {
	case "trace_count":
		orderBy = "trace_count DESC"
	case "total_cost":
		orderBy = "total_cost DESC"
	case "first_trace_at":
		orderBy = "first_trace_at DESC"
	}

	limit := listQuery.Limit
	if limit <= 0 {
		limit = 50
	}
	offset := listQuery.Offset
	if offset < 0 {
		offset = 0
	}

	// Count query
	countSQL := fmt.Sprintf(`
		SELECT count()
		FROM trace_sessions FINAL
		WHERE %s
	`, joinConditions(whereConditions))

	var totalCount uint64
	countRow := h.conn.QueryRow(ctx, countSQL, args...)
	if err := countRow.Scan(&totalCount); err != nil {
		logger.WithFields("error", err.Error()).Error("failed to count sessions")
		// Non-fatal: continue with 0 count
	}

	// Data query
	sqlQuery := fmt.Sprintf(`
		SELECT
			tenant_id,
			session_id,
			user_id,
			first_trace_at,
			last_trace_at,
			trace_count,
			total_duration_ns,
			total_input_tokens,
			total_output_tokens,
			total_cost,
			error_count,
			models,
			tags,
			environment,
			kinds
		FROM trace_sessions FINAL
		WHERE %s
		ORDER BY %s
		LIMIT ?
		OFFSET ?
	`, joinConditions(whereConditions), orderBy)

	dataArgs := append(args, limit, offset)

	rows, err := h.conn.Query(ctx, sqlQuery, dataArgs...)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("failed to list sessions")
		return nil, fmt.Errorf("failed to list sessions: %w", err)
	}
	defer rows.Close()

	sessions := []SessionReadModel{}
	for rows.Next() {
		var s SessionReadModel
		if err := rows.Scan(
			&s.TenantID,
			&s.SessionID,
			&s.UserID,
			&s.FirstTraceAt,
			&s.LastTraceAt,
			&s.TraceCount,
			&s.TotalDurationNs,
			&s.TotalInputTokens,
			&s.TotalOutputTokens,
			&s.TotalCost,
			&s.ErrorCount,
			&s.Models,
			&s.Tags,
			&s.Environment,
			&s.Kinds,
		); err != nil {
			logger.WithFields("error", err.Error()).Error("failed to scan session row")
			continue
		}
		sessions = append(sessions, s)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating sessions: %w", err)
	}

	return SessionListResult{
		Sessions:   sessions,
		TotalCount: totalCount,
	}, nil
}

// ============================================================================
// GetSessionHandler
// ============================================================================

// GetSessionHandler retrieves a single session by ID
type GetSessionHandler struct {
	conn clickhouse.Conn
}

func NewGetSessionHandler(conn clickhouse.Conn) *GetSessionHandler {
	return &GetSessionHandler{conn: conn}
}

func (h *GetSessionHandler) QueryType() string {
	return "GetTraceSession"
}

func (h *GetSessionHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	getQuery, ok := q.(*GetSessionQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for GetSessionHandler")
	}

	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		// Fail closed — return not-found rather than running an
		// unscoped lookup that could match a foreign tenant's row.
		return nil, fmt.Errorf("session not found")
	}

	whereConditions := []string{"session_id = ?", "tenant_id = ?"}
	args := []interface{}{getQuery.SessionID, tenantID}

	sqlQuery := fmt.Sprintf(`
		SELECT
			tenant_id,
			session_id,
			user_id,
			first_trace_at,
			last_trace_at,
			trace_count,
			total_duration_ns,
			total_input_tokens,
			total_output_tokens,
			total_cost,
			error_count,
			models,
			tags,
			environment,
			kinds
		FROM trace_sessions FINAL
		WHERE %s
		LIMIT 1
	`, joinConditions(whereConditions))

	rows, err := h.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		logger.WithFields("session_id", getQuery.SessionID, "error", err.Error()).Error("failed to get session")
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, sql.ErrNoRows
	}

	var s SessionReadModel
	if err := rows.Scan(
		&s.TenantID,
		&s.SessionID,
		&s.UserID,
		&s.FirstTraceAt,
		&s.LastTraceAt,
		&s.TraceCount,
		&s.TotalDurationNs,
		&s.TotalInputTokens,
		&s.TotalOutputTokens,
		&s.TotalCost,
		&s.ErrorCount,
		&s.Models,
		&s.Tags,
		&s.Environment,
		&s.Kinds,
	); err != nil {
		logger.WithFields("error", err.Error()).Error("failed to scan session row")
		return nil, fmt.Errorf("failed to scan session: %w", err)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading session: %w", err)
	}

	return s, nil
}
