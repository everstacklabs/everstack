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
// ListUsersHandler
// ============================================================================

// ListUsersHandler queries trace_users with filtering and pagination
type ListUsersHandler struct {
	conn clickhouse.Conn
}

func NewListUsersHandler(conn clickhouse.Conn) *ListUsersHandler {
	return &ListUsersHandler{conn: conn}
}

func (h *ListUsersHandler) QueryType() string {
	return "ListTraceUsers"
}

func (h *ListUsersHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	listQuery, ok := q.(*ListUsersQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for ListUsersHandler")
	}

	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		// Fail closed — see metrics_dashboard.go for rationale.
		return UserListResult{Users: nil, TotalCount: 0}, nil
	}

	whereConditions := []string{"tenant_id = ?"}
	args := []interface{}{tenantID}

	if !listQuery.StartTime.IsZero() {
		whereConditions = append(whereConditions, "last_seen >= ?")
		args = append(args, listQuery.StartTime)
	}

	if !listQuery.EndTime.IsZero() {
		whereConditions = append(whereConditions, "first_seen <= ?")
		args = append(args, listQuery.EndTime)
	}

	if listQuery.Search != "" {
		whereConditions = append(whereConditions, "user_id LIKE ?")
		args = append(args, "%"+listQuery.Search+"%")
	}

	// Determine ORDER BY
	orderBy := "last_seen DESC"
	switch listQuery.OrderBy {
	case "trace_count":
		orderBy = "trace_count DESC"
	case "total_cost":
		orderBy = "total_cost DESC"
	case "first_seen":
		orderBy = "first_seen DESC"
	case "session_count":
		orderBy = "session_count DESC"
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
		FROM trace_users FINAL
		WHERE %s
	`, joinConditions(whereConditions))

	var totalCount uint64
	countRow := h.conn.QueryRow(ctx, countSQL, args...)
	if err := countRow.Scan(&totalCount); err != nil {
		logger.WithFields("error", err.Error()).Error("failed to count users")
	}

	// Data query
	sqlQuery := fmt.Sprintf(`
		SELECT
			tenant_id,
			user_id,
			first_seen,
			last_seen,
			session_count,
			trace_count,
			total_tokens,
			total_cost,
			error_rate,
			avg_latency_ns
		FROM trace_users FINAL
		WHERE %s
		ORDER BY %s
		LIMIT ?
		OFFSET ?
	`, joinConditions(whereConditions), orderBy)

	dataArgs := append(args, limit, offset)

	rows, err := h.conn.Query(ctx, sqlQuery, dataArgs...)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("failed to list users")
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	defer rows.Close()

	users := []UserReadModel{}
	for rows.Next() {
		var u UserReadModel
		if err := rows.Scan(
			&u.TenantID,
			&u.UserID,
			&u.FirstSeen,
			&u.LastSeen,
			&u.SessionCount,
			&u.TraceCount,
			&u.TotalTokens,
			&u.TotalCost,
			&u.ErrorRate,
			&u.AvgLatencyNs,
		); err != nil {
			logger.WithFields("error", err.Error()).Error("failed to scan user row")
			continue
		}
		users = append(users, u)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating users: %w", err)
	}

	return UserListResult{
		Users:      users,
		TotalCount: totalCount,
	}, nil
}

// ============================================================================
// GetUserHandler
// ============================================================================

// GetUserHandler retrieves a single user by ID
type GetUserHandler struct {
	conn clickhouse.Conn
}

func NewGetUserHandler(conn clickhouse.Conn) *GetUserHandler {
	return &GetUserHandler{conn: conn}
}

func (h *GetUserHandler) QueryType() string {
	return "GetTraceUser"
}

func (h *GetUserHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	getQuery, ok := q.(*GetUserQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type for GetUserHandler")
	}

	tenantID := database.TenantSchemaFromContext(ctx)
	if tenantID == "" {
		return nil, fmt.Errorf("user not found")
	}

	whereConditions := []string{"user_id = ?", "tenant_id = ?"}
	args := []interface{}{getQuery.TargetUserID, tenantID}

	sqlQuery := fmt.Sprintf(`
		SELECT
			tenant_id,
			user_id,
			first_seen,
			last_seen,
			session_count,
			trace_count,
			total_tokens,
			total_cost,
			error_rate,
			avg_latency_ns
		FROM trace_users FINAL
		WHERE %s
		LIMIT 1
	`, joinConditions(whereConditions))

	rows, err := h.conn.Query(ctx, sqlQuery, args...)
	if err != nil {
		logger.WithFields("user_id", getQuery.TargetUserID, "error", err.Error()).Error("failed to get user")
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		return nil, sql.ErrNoRows
	}

	var u UserReadModel
	if err := rows.Scan(
		&u.TenantID,
		&u.UserID,
		&u.FirstSeen,
		&u.LastSeen,
		&u.SessionCount,
		&u.TraceCount,
		&u.TotalTokens,
		&u.TotalCost,
		&u.ErrorRate,
		&u.AvgLatencyNs,
	); err != nil {
		logger.WithFields("error", err.Error()).Error("failed to scan user row")
		return nil, fmt.Errorf("failed to scan user: %w", err)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error reading user: %w", err)
	}

	return u, nil
}
