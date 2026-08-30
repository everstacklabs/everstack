package annotations

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// ---------------------------------------------------------------------------
// Read Models
// ---------------------------------------------------------------------------

// QueueReadModel maps to the annotation_queues table.
type QueueReadModel struct {
	ID                 string         `db:"id" json:"id"`
	TenantID           string         `db:"tenant_id" json:"tenant_id"`
	Name               string         `db:"name" json:"name"`
	Description        sql.NullString `db:"description" json:"description"`
	Status             string         `db:"status" json:"status"`
	ScoreConfigIDs     pq.StringArray `db:"score_config_ids" json:"score_config_ids"`
	AssignmentMode     string         `db:"assignment_mode" json:"assignment_mode"`
	Annotators         pq.StringArray `db:"annotators" json:"annotators"`
	AutoPopulateConfig []byte         `db:"auto_populate_config" json:"auto_populate_config"`
	ItemsPending       int32          `db:"items_pending" json:"items_pending"`
	ItemsCompleted     int32          `db:"items_completed" json:"items_completed"`
	CreatedAt          string         `db:"created_at" json:"created_at"`
	UpdatedAt          string         `db:"updated_at" json:"updated_at"`
}

// QueueItemReadModel maps to the annotation_queue_items table.
type QueueItemReadModel struct {
	ID            string         `db:"id" json:"id"`
	QueueID       string         `db:"queue_id" json:"queue_id"`
	TenantID      string         `db:"tenant_id" json:"tenant_id"`
	TraceID       string         `db:"trace_id" json:"trace_id"`
	ObservationID sql.NullString `db:"observation_id" json:"observation_id"`
	AssignedTo    sql.NullString `db:"assigned_to" json:"assigned_to"`
	AssignedAt    sql.NullTime   `db:"assigned_at" json:"assigned_at"`
	Status        string         `db:"status" json:"status"`
	Priority      int32          `db:"priority" json:"priority"`
	CompletedBy   sql.NullString `db:"completed_by" json:"completed_by"`
	CompletedAt   sql.NullTime   `db:"completed_at" json:"completed_at"`
	CreatedAt     string         `db:"created_at" json:"created_at"`
	UpdatedAt     string         `db:"updated_at" json:"updated_at"`
}

// QueueStatsReadModel holds aggregate queue statistics.
type QueueStatsReadModel struct {
	QueueID         string `db:"queue_id" json:"queue_id"`
	TotalItems      int32  `db:"total_items" json:"total_items"`
	PendingItems    int32  `db:"pending_items" json:"pending_items"`
	InProgressItems int32  `db:"in_progress_items" json:"in_progress_items"`
	CompletedItems  int32  `db:"completed_items" json:"completed_items"`
	SkippedItems    int32  `db:"skipped_items" json:"skipped_items"`
}

// ---------------------------------------------------------------------------
// Queries
// ---------------------------------------------------------------------------

// GetQueueByIDQuery retrieves an annotation queue by ID.
type GetQueueByIDQuery struct {
	query.BaseQuery
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
}

func NewGetQueueByIDQuery(id, tenantID string) *GetQueueByIDQuery {
	return &GetQueueByIDQuery{
		BaseQuery: query.BaseQuery{},
		ID:        id,
		TenantID:  tenantID,
	}
}

func (q GetQueueByIDQuery) QueryType() string { return "GetAnnotationQueueByID" }
func (q GetQueueByIDQuery) Validate() error {
	if q.ID == "" {
		return fmt.Errorf("id cannot be empty")
	}
	return nil
}

// ListQueuesQuery retrieves annotation queues for a tenant.
type ListQueuesQuery struct {
	query.BaseQuery
	TenantID string  `json:"tenant_id"`
	Status   *string `json:"status,omitempty"`
	Limit    int     `json:"limit,omitempty"`
	Offset   int     `json:"offset,omitempty"`
}

func NewListQueuesQuery(tenantID string, status *string, limit, offset int) *ListQueuesQuery {
	return &ListQueuesQuery{
		BaseQuery: query.BaseQuery{},
		TenantID:  tenantID,
		Status:    status,
		Limit:     limit,
		Offset:    offset,
	}
}

func (q ListQueuesQuery) QueryType() string { return "ListAnnotationQueues" }
func (q ListQueuesQuery) Validate() error   { return nil }

// ListQueueItemsQuery retrieves items for a given queue.
type ListQueueItemsQuery struct {
	query.BaseQuery
	TenantID   string  `json:"tenant_id"`
	QueueID    string  `json:"queue_id"`
	Status     *string `json:"status,omitempty"`
	AssignedTo *string `json:"assigned_to,omitempty"`
	Limit      int     `json:"limit,omitempty"`
	Offset     int     `json:"offset,omitempty"`
}

func NewListQueueItemsQuery(tenantID, queueID string, status, assignedTo *string, limit, offset int) *ListQueueItemsQuery {
	return &ListQueueItemsQuery{
		BaseQuery:  query.BaseQuery{},
		TenantID:   tenantID,
		QueueID:    queueID,
		Status:     status,
		AssignedTo: assignedTo,
		Limit:      limit,
		Offset:     offset,
	}
}

func (q ListQueueItemsQuery) QueryType() string { return "ListAnnotationQueueItems" }
func (q ListQueueItemsQuery) Validate() error {
	if q.QueueID == "" {
		return fmt.Errorf("queue_id cannot be empty")
	}
	return nil
}

// GetNextItemQuery retrieves the next pending item for annotation.
type GetNextItemQuery struct {
	query.BaseQuery
	TenantID    string `json:"tenant_id"`
	QueueID     string `json:"queue_id"`
	AnnotatorID string `json:"annotator_id,omitempty"`
}

func NewGetNextItemQuery(tenantID, queueID, annotatorID string) *GetNextItemQuery {
	return &GetNextItemQuery{
		BaseQuery:   query.BaseQuery{},
		TenantID:    tenantID,
		QueueID:     queueID,
		AnnotatorID: annotatorID,
	}
}

func (q GetNextItemQuery) QueryType() string { return "GetNextAnnotationItem" }
func (q GetNextItemQuery) Validate() error {
	if q.QueueID == "" {
		return fmt.Errorf("queue_id cannot be empty")
	}
	return nil
}

// GetQueueStatsQuery retrieves aggregate statistics for a queue.
type GetQueueStatsQuery struct {
	query.BaseQuery
	TenantID string `json:"tenant_id"`
	QueueID  string `json:"queue_id"`
}

func NewGetQueueStatsQuery(tenantID, queueID string) *GetQueueStatsQuery {
	return &GetQueueStatsQuery{
		BaseQuery: query.BaseQuery{},
		TenantID:  tenantID,
		QueueID:   queueID,
	}
}

func (q GetQueueStatsQuery) QueryType() string { return "GetAnnotationQueueStats" }
func (q GetQueueStatsQuery) Validate() error {
	if q.QueueID == "" {
		return fmt.Errorf("queue_id cannot be empty")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// GetQueueByIDHandler handles GetAnnotationQueueByID queries.
type GetQueueByIDHandler struct {
	db *sqlx.DB
}

func NewGetQueueByIDHandler(db *sqlx.DB) *GetQueueByIDHandler {
	return &GetQueueByIDHandler{db: db}
}

func (h *GetQueueByIDHandler) QueryType() string { return "GetAnnotationQueueByID" }

func (h *GetQueueByIDHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetQueueByIDQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetQueueByIDQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"id", qry.ID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing get annotation queue by id query")

	if qry.TenantID == "" {
		return nil, fmt.Errorf("annotation queue lookup requires tenant id")
	}
	var out QueueReadModel
	err := h.db.GetContext(ctx, &out, `
		SELECT * FROM annotation_queues
		WHERE id = $1 AND tenant_id = $2
	`, qry.ID, qry.TenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute get annotation queue by id query")
		return nil, fmt.Errorf("failed to get annotation queue: %w", err)
	}
	return &out, nil
}

// ListQueuesHandler handles ListAnnotationQueues queries.
type ListQueuesHandler struct {
	db *sqlx.DB
}

func NewListQueuesHandler(db *sqlx.DB) *ListQueuesHandler {
	return &ListQueuesHandler{db: db}
}

func (h *ListQueuesHandler) QueryType() string { return "ListAnnotationQueues" }

func (h *ListQueuesHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListQueuesQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListQueuesQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list annotation queues query")

	if qry.TenantID == "" {
		return nil, fmt.Errorf("annotation queue list requires tenant id")
	}
	queryStr := `SELECT * FROM annotation_queues WHERE tenant_id = $1`
	args := []interface{}{qry.TenantID}
	argIndex := 2

	if qry.Status != nil {
		queryStr += fmt.Sprintf(" AND status = $%d", argIndex)
		args = append(args, *qry.Status)
		argIndex++
	}

	queryStr += " ORDER BY created_at DESC"

	if qry.Limit > 0 {
		queryStr += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, qry.Limit)
		argIndex++
	}

	if qry.Offset > 0 {
		queryStr += fmt.Sprintf(" OFFSET $%d", argIndex)
		args = append(args, qry.Offset)
	}

	var out []QueueReadModel
	err := h.db.SelectContext(ctx, &out, queryStr, args...)
	if err != nil {
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute list annotation queues query")
		return nil, fmt.Errorf("failed to list annotation queues: %w", err)
	}

	logger.WithFields(
		"count", len(out),
	).Info("annotation_queues: list query completed")

	return out, nil
}

// ListQueueItemsHandler handles ListAnnotationQueueItems queries.
type ListQueueItemsHandler struct {
	db *sqlx.DB
}

func NewListQueueItemsHandler(db *sqlx.DB) *ListQueueItemsHandler {
	return &ListQueueItemsHandler{db: db}
}

func (h *ListQueueItemsHandler) QueryType() string { return "ListAnnotationQueueItems" }

func (h *ListQueueItemsHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListQueueItemsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListQueueItemsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"queue_id", qry.QueueID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list annotation queue items query")

	if qry.TenantID == "" {
		return nil, fmt.Errorf("annotation queue items list requires tenant id")
	}
	queryStr := `SELECT * FROM annotation_queue_items WHERE queue_id = $1 AND tenant_id = $2`
	args := []interface{}{qry.QueueID, qry.TenantID}
	argIndex := 3

	if qry.Status != nil {
		queryStr += fmt.Sprintf(" AND status = $%d", argIndex)
		args = append(args, *qry.Status)
		argIndex++
	}

	if qry.AssignedTo != nil {
		queryStr += fmt.Sprintf(" AND assigned_to = $%d", argIndex)
		args = append(args, *qry.AssignedTo)
		argIndex++
	}

	queryStr += " ORDER BY priority DESC, created_at ASC"

	if qry.Limit > 0 {
		queryStr += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, qry.Limit)
		argIndex++
	}

	if qry.Offset > 0 {
		queryStr += fmt.Sprintf(" OFFSET $%d", argIndex)
		args = append(args, qry.Offset)
	}

	var out []QueueItemReadModel
	err := h.db.SelectContext(ctx, &out, queryStr, args...)
	if err != nil {
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute list annotation queue items query")
		return nil, fmt.Errorf("failed to list annotation queue items: %w", err)
	}

	return out, nil
}

// GetNextItemHandler handles GetNextAnnotationItem queries.
type GetNextItemHandler struct {
	db *sqlx.DB
}

func NewGetNextItemHandler(db *sqlx.DB) *GetNextItemHandler {
	return &GetNextItemHandler{db: db}
}

func (h *GetNextItemHandler) QueryType() string { return "GetNextAnnotationItem" }

func (h *GetNextItemHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetNextItemQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetNextItemQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"queue_id", qry.QueueID,
		"tenant_id", qry.TenantID,
		"annotator_id", qry.AnnotatorID,
		"correlation_id", correlationID,
	).Debug("executing get next annotation item query")

	if qry.TenantID == "" {
		return nil, fmt.Errorf("get next annotation item requires tenant id")
	}
	var out QueueItemReadModel
	err := h.db.GetContext(ctx, &out, `
		SELECT * FROM annotation_queue_items
		WHERE queue_id = $1
			AND tenant_id = $2
			AND status = 'pending'
			AND (assigned_to = $3 OR assigned_to = '')
		ORDER BY priority DESC, created_at ASC
		LIMIT 1
	`, qry.QueueID, qry.TenantID, qry.AnnotatorID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute get next annotation item query")
		return nil, fmt.Errorf("failed to get next annotation item: %w", err)
	}

	// If an annotator is specified, update the assignment (side-effect for convenience)
	if qry.AnnotatorID != "" {
		now := time.Now()
		result, err := h.db.ExecContext(ctx, `
			UPDATE annotation_queue_items
			SET assigned_to = $1, assigned_at = $2, status = 'in_progress', updated_at = $3
			WHERE id = $4
				AND tenant_id = $5
				AND status = 'pending'
				AND (assigned_to = $1 OR assigned_to = '')
		`, qry.AnnotatorID, now, now, out.ID, qry.TenantID)
		if err != nil {
			return nil, fmt.Errorf("failed to assign next annotation item: %w", err)
		}
		rowsAffected, err := result.RowsAffected()
		if err != nil {
			return nil, fmt.Errorf("failed to verify next annotation item assignment: %w", err)
		}
		if rowsAffected == 0 {
			return nil, nil
		}
		out.AssignedTo = sql.NullString{String: qry.AnnotatorID, Valid: true}
		out.AssignedAt = sql.NullTime{Time: now, Valid: true}
		out.Status = "in_progress"
	}

	return &out, nil
}

// GetQueueStatsHandler handles GetAnnotationQueueStats queries.
type GetQueueStatsHandler struct {
	db *sqlx.DB
}

func NewGetQueueStatsHandler(db *sqlx.DB) *GetQueueStatsHandler {
	return &GetQueueStatsHandler{db: db}
}

func (h *GetQueueStatsHandler) QueryType() string { return "GetAnnotationQueueStats" }

func (h *GetQueueStatsHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetQueueStatsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetQueueStatsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"queue_id", qry.QueueID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing get annotation queue stats query")

	if qry.TenantID == "" {
		return nil, fmt.Errorf("annotation queue stats requires tenant id")
	}
	var stats QueueStatsReadModel
	err := h.db.GetContext(ctx, &stats, `
		SELECT
			$1::varchar AS queue_id,
			COUNT(*)::int AS total_items,
			COUNT(*) FILTER (WHERE status = 'pending')::int AS pending_items,
			COUNT(*) FILTER (WHERE status = 'in_progress')::int AS in_progress_items,
			COUNT(*) FILTER (WHERE status = 'completed')::int AS completed_items,
			COUNT(*) FILTER (WHERE status = 'skipped')::int AS skipped_items
		FROM annotation_queue_items
		WHERE queue_id = $1 AND tenant_id = $2
	`, qry.QueueID, qry.TenantID)
	if err != nil {
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute get annotation queue stats query")
		return nil, fmt.Errorf("failed to get annotation queue stats: %w", err)
	}

	return &stats, nil
}
