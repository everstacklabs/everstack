package datasets

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// --- Read Models ---

// DatasetReadModel maps to datasets table.
type DatasetReadModel struct {
	ID          string         `db:"id" json:"id"`
	TenantID    string         `db:"tenant_id" json:"tenant_id"`
	Name        string         `db:"name" json:"name"`
	Description string         `db:"description" json:"description"`
	Metadata    []byte         `db:"metadata" json:"metadata"`
	CreatedAt   string         `db:"created_at" json:"created_at"`
	UpdatedAt   string         `db:"updated_at" json:"updated_at"`
	ArchivedAt  sql.NullString `db:"archived_at" json:"archived_at"`
}

// DatasetItemReadModel maps to dataset_items table.
type DatasetItemReadModel struct {
	ID                  string `db:"id" json:"id"`
	DatasetID           string `db:"dataset_id" json:"dataset_id"`
	TenantID            string `db:"tenant_id" json:"tenant_id"`
	Input               []byte `db:"input" json:"input"`
	ExpectedOutput      []byte `db:"expected_output" json:"expected_output"`
	Metadata            []byte `db:"metadata" json:"metadata"`
	SourceTraceID       string `db:"source_trace_id" json:"source_trace_id"`
	SourceObservationID string `db:"source_observation_id" json:"source_observation_id"`
	Status              string `db:"status" json:"status"`
	CreatedAt           string `db:"created_at" json:"created_at"`
	UpdatedAt           string `db:"updated_at" json:"updated_at"`
}

// ScoreConfigReadModel maps to score_configs table.
type ScoreConfigReadModel struct {
	ID             string          `db:"id" json:"id"`
	TenantID       string          `db:"tenant_id" json:"tenant_id"`
	Name           string          `db:"name" json:"name"`
	DataType       string          `db:"data_type" json:"data_type"`
	Description    string          `db:"description" json:"description"`
	MinValue       sql.NullFloat64 `db:"min_value" json:"min_value"`
	MaxValue       sql.NullFloat64 `db:"max_value" json:"max_value"`
	Categories     []byte          `db:"categories" json:"categories"`
	EvalPrompt     string          `db:"eval_prompt" json:"eval_prompt"`
	EvalModel      string          `db:"eval_model" json:"eval_model"`
	IsArchived     bool            `db:"is_archived" json:"is_archived"`
	ScorerCode     string          `db:"scorer_code" json:"scorer_code"`
	ScorerLanguage string          `db:"scorer_language" json:"scorer_language"`
	UseSandbox     bool            `db:"use_sandbox" json:"use_sandbox"`
	Slug           string          `db:"slug" json:"slug"`
	ScorerType     string          `db:"scorer_type" json:"scorer_type"`
	OutputType     string          `db:"output_type" json:"output_type"`
	Messages       []byte          `db:"messages" json:"messages"`
	ModelParams    []byte          `db:"model_params" json:"model_params"`
	ChoiceScores   []byte          `db:"choice_scores" json:"choice_scores"`
	UseCot         bool            `db:"use_cot" json:"use_cot"`
	PassThreshold  sql.NullFloat64 `db:"pass_threshold" json:"pass_threshold"`
	DagDefinition  []byte          `db:"dag_definition" json:"dag_definition"`
	CreatedAt      string          `db:"created_at" json:"created_at"`
	UpdatedAt      string          `db:"updated_at" json:"updated_at"`
}

// EvalRunReadModel maps to eval_runs table.
type EvalRunReadModel struct {
	ID               string         `db:"id" json:"id"`
	TenantID         string         `db:"tenant_id" json:"tenant_id"`
	DatasetID        string         `db:"dataset_id" json:"dataset_id"`
	DatasetVersionID sql.NullString `db:"dataset_version_id" json:"dataset_version_id"`
	Name             string         `db:"name" json:"name"`
	Description      string         `db:"description" json:"description"`
	Status           string         `db:"status" json:"status"`
	EvalTargetType   string         `db:"eval_target_type" json:"eval_target_type"`
	EvalTargetID     string         `db:"eval_target_id" json:"eval_target_id"`
	EvalConfig       []byte         `db:"eval_config" json:"eval_config"`
	ScorerConfigIDs  pq.StringArray `db:"scorer_config_ids" json:"scorer_config_ids"`
	TotalItems       int32          `db:"total_items" json:"total_items"`
	CompletedItems   int32          `db:"completed_items" json:"completed_items"`
	FailedItems      int32          `db:"failed_items" json:"failed_items"`
	ScoreSummary     []byte         `db:"score_summary" json:"score_summary"`
	StartedAt        sql.NullString `db:"started_at" json:"started_at"`
	CompletedAt      sql.NullString `db:"completed_at" json:"completed_at"`
	CreatedAt        string         `db:"created_at" json:"created_at"`
	UpdatedAt        string         `db:"updated_at" json:"updated_at"`
	IsBaseline       bool           `db:"is_baseline" json:"is_baseline"`
	BaselineRunID    sql.NullString `db:"baseline_run_id" json:"baseline_run_id"`
	RegressionResult []byte         `db:"regression_result" json:"regression_result"`
}

// EvalRunItemReadModel maps to eval_run_items table joined with dataset_items.
type EvalRunItemReadModel struct {
	ID             string  `db:"id" json:"id"`
	EvalRunID      string  `db:"eval_run_id" json:"eval_run_id"`
	DatasetItemID  string  `db:"dataset_item_id" json:"dataset_item_id"`
	TenantID       string  `db:"tenant_id" json:"tenant_id"`
	Status         string  `db:"status" json:"status"`
	Output         []byte  `db:"output" json:"output"`
	TraceID        string  `db:"trace_id" json:"trace_id"`
	LatencyMs      int64   `db:"latency_ms" json:"latency_ms"`
	Cost           float64 `db:"cost" json:"cost"`
	TokenUsage     []byte  `db:"token_usage" json:"token_usage"`
	Error          string  `db:"error" json:"error"`
	Scores         []byte  `db:"scores" json:"scores"`
	CreatedAt      string  `db:"created_at" json:"created_at"`
	UpdatedAt      string  `db:"updated_at" json:"updated_at"`
	Input          []byte  `db:"input" json:"input"`
	ExpectedOutput []byte  `db:"expected_output" json:"expected_output"`
}

// --- Queries ---

// GetDatasetByIDQuery retrieves a dataset by ID.
type GetDatasetByIDQuery struct {
	query.BaseQuery
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
}

func NewGetDatasetByIDQuery(id, tenantID string) *GetDatasetByIDQuery {
	return &GetDatasetByIDQuery{
		BaseQuery: query.BaseQuery{},
		ID:        id,
		TenantID:  tenantID,
	}
}

func (q GetDatasetByIDQuery) QueryType() string { return "GetDatasetByID" }
func (q GetDatasetByIDQuery) Validate() error {
	if q.ID == "" {
		return fmt.Errorf("id cannot be empty")
	}
	return nil
}

// ListDatasetsQuery retrieves all datasets for a tenant.
type ListDatasetsQuery struct {
	query.BaseQuery
	TenantID string `json:"tenant_id"`
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
}

func NewListDatasetsQuery(tenantID string, limit, offset int) *ListDatasetsQuery {
	return &ListDatasetsQuery{
		BaseQuery: query.BaseQuery{},
		TenantID:  tenantID,
		Limit:     limit,
		Offset:    offset,
	}
}

func (q ListDatasetsQuery) QueryType() string { return "ListDatasets" }
func (q ListDatasetsQuery) Validate() error   { return nil }

// ListDatasetItemsQuery retrieves items in a dataset.
type ListDatasetItemsQuery struct {
	query.BaseQuery
	TenantID  string  `json:"tenant_id"`
	DatasetID string  `json:"dataset_id"`
	Status    *string `json:"status,omitempty"`
	Limit     int     `json:"limit,omitempty"`
	Offset    int     `json:"offset,omitempty"`
}

func NewListDatasetItemsQuery(tenantID, datasetID string, status *string, limit, offset int) *ListDatasetItemsQuery {
	return &ListDatasetItemsQuery{
		BaseQuery: query.BaseQuery{},
		TenantID:  tenantID,
		DatasetID: datasetID,
		Status:    status,
		Limit:     limit,
		Offset:    offset,
	}
}

func (q ListDatasetItemsQuery) QueryType() string { return "ListDatasetItems" }
func (q ListDatasetItemsQuery) Validate() error {
	if q.DatasetID == "" {
		return fmt.Errorf("dataset_id cannot be empty")
	}
	return nil
}

// GetScoreConfigQuery retrieves a score config by ID.
type GetScoreConfigQuery struct {
	query.BaseQuery
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
}

func NewGetScoreConfigQuery(id, tenantID string) *GetScoreConfigQuery {
	return &GetScoreConfigQuery{
		BaseQuery: query.BaseQuery{},
		ID:        id,
		TenantID:  tenantID,
	}
}

func (q GetScoreConfigQuery) QueryType() string { return "GetScoreConfig" }
func (q GetScoreConfigQuery) Validate() error {
	if q.ID == "" {
		return fmt.Errorf("id cannot be empty")
	}
	return nil
}

// ListScoreConfigsQuery retrieves all score configs for a tenant.
type ListScoreConfigsQuery struct {
	query.BaseQuery
	TenantID string `json:"tenant_id"`
	Limit    int    `json:"limit,omitempty"`
	Offset   int    `json:"offset,omitempty"`
}

func NewListScoreConfigsQuery(tenantID string, limit, offset int) *ListScoreConfigsQuery {
	return &ListScoreConfigsQuery{
		BaseQuery: query.BaseQuery{},
		TenantID:  tenantID,
		Limit:     limit,
		Offset:    offset,
	}
}

func (q ListScoreConfigsQuery) QueryType() string { return "ListScoreConfigs" }
func (q ListScoreConfigsQuery) Validate() error   { return nil }

// GetEvalRunQuery retrieves an eval run by ID.
type GetEvalRunQuery struct {
	query.BaseQuery
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
}

func NewGetEvalRunQuery(id, tenantID string) *GetEvalRunQuery {
	return &GetEvalRunQuery{
		BaseQuery: query.BaseQuery{},
		ID:        id,
		TenantID:  tenantID,
	}
}

func (q GetEvalRunQuery) QueryType() string { return "GetEvalRun" }
func (q GetEvalRunQuery) Validate() error {
	if q.ID == "" {
		return fmt.Errorf("id cannot be empty")
	}
	return nil
}

// ListEvalRunsQuery retrieves eval runs for a tenant.
type ListEvalRunsQuery struct {
	query.BaseQuery
	TenantID  string  `json:"tenant_id"`
	DatasetID *string `json:"dataset_id,omitempty"`
	Status    *string `json:"status,omitempty"`
	Limit     int     `json:"limit,omitempty"`
	Offset    int     `json:"offset,omitempty"`
}

func NewListEvalRunsQuery(tenantID string, datasetID, status *string, limit, offset int) *ListEvalRunsQuery {
	return &ListEvalRunsQuery{
		BaseQuery: query.BaseQuery{},
		TenantID:  tenantID,
		DatasetID: datasetID,
		Status:    status,
		Limit:     limit,
		Offset:    offset,
	}
}

func (q ListEvalRunsQuery) QueryType() string { return "ListEvalRuns" }
func (q ListEvalRunsQuery) Validate() error   { return nil }

// ListEvalRunItemsQuery retrieves items for an eval run.
type ListEvalRunItemsQuery struct {
	query.BaseQuery
	TenantID  string  `json:"tenant_id"`
	EvalRunID string  `json:"eval_run_id"`
	Status    *string `json:"status,omitempty"`
	Limit     int     `json:"limit,omitempty"`
	Offset    int     `json:"offset,omitempty"`
}

func NewListEvalRunItemsQuery(tenantID, evalRunID string, status *string, limit, offset int) *ListEvalRunItemsQuery {
	return &ListEvalRunItemsQuery{
		BaseQuery: query.BaseQuery{},
		TenantID:  tenantID,
		EvalRunID: evalRunID,
		Status:    status,
		Limit:     limit,
		Offset:    offset,
	}
}

func (q ListEvalRunItemsQuery) QueryType() string { return "ListEvalRunItems" }
func (q ListEvalRunItemsQuery) Validate() error {
	if q.EvalRunID == "" {
		return fmt.Errorf("eval_run_id cannot be empty")
	}
	return nil
}

// --- Query Handlers ---

// GetDatasetByIDQueryHandler handles GetDatasetByID queries.
type GetDatasetByIDQueryHandler struct {
	db *sqlx.DB
}

func NewGetDatasetByIDQueryHandler(db *sqlx.DB) *GetDatasetByIDQueryHandler {
	return &GetDatasetByIDQueryHandler{db: db}
}

func (h *GetDatasetByIDQueryHandler) QueryType() string { return "GetDatasetByID" }

func (h *GetDatasetByIDQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetDatasetByIDQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetDatasetByIDQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"id", qry.ID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing get dataset by id query")

	if qry.TenantID == "" {
		return nil, nil
	}

	var out DatasetReadModel
	err := h.db.GetContext(ctx, &out, `SELECT * FROM datasets WHERE id = $1 AND tenant_id = $2`, qry.ID, qry.TenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute get dataset by id query")
		return nil, fmt.Errorf("failed to get dataset: %w", err)
	}
	return &out, nil
}

// ListDatasetsQueryHandler handles ListDatasets queries.
type ListDatasetsQueryHandler struct {
	db *sqlx.DB
}

func NewListDatasetsQueryHandler(db *sqlx.DB) *ListDatasetsQueryHandler {
	return &ListDatasetsQueryHandler{db: db}
}

func (h *ListDatasetsQueryHandler) QueryType() string { return "ListDatasets" }

func (h *ListDatasetsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListDatasetsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListDatasetsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list datasets query")

	if qry.TenantID == "" {
		return []DatasetReadModel{}, nil
	}

	queryStr := `SELECT * FROM datasets WHERE tenant_id = $1 AND archived_at IS NULL`
	args := []interface{}{qry.TenantID}
	argIndex := 2

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

	var out []DatasetReadModel
	err := h.db.SelectContext(ctx, &out, queryStr, args...)
	if err != nil {
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute list datasets query")
		return nil, fmt.Errorf("failed to list datasets: %w", err)
	}

	logger.WithFields("count", len(out)).Info("datasets: list query completed")
	return out, nil
}

// ListDatasetItemsQueryHandler handles ListDatasetItems queries.
type ListDatasetItemsQueryHandler struct {
	db *sqlx.DB
}

func NewListDatasetItemsQueryHandler(db *sqlx.DB) *ListDatasetItemsQueryHandler {
	return &ListDatasetItemsQueryHandler{db: db}
}

func (h *ListDatasetItemsQueryHandler) QueryType() string { return "ListDatasetItems" }

func (h *ListDatasetItemsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListDatasetItemsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListDatasetItemsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"dataset_id", qry.DatasetID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list dataset items query")

	if qry.TenantID == "" {
		return []DatasetItemReadModel{}, nil
	}

	queryStr := `SELECT * FROM dataset_items WHERE dataset_id = $1 AND tenant_id = $2`
	args := []interface{}{qry.DatasetID, qry.TenantID}
	argIndex := 3

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

	var out []DatasetItemReadModel
	err := h.db.SelectContext(ctx, &out, queryStr, args...)
	if err != nil {
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute list dataset items query")
		return nil, fmt.Errorf("failed to list dataset items: %w", err)
	}

	logger.WithFields("count", len(out)).Info("dataset_items: list query completed")
	return out, nil
}

// GetScoreConfigQueryHandler handles GetScoreConfig queries.
type GetScoreConfigQueryHandler struct {
	db *sqlx.DB
}

func NewGetScoreConfigQueryHandler(db *sqlx.DB) *GetScoreConfigQueryHandler {
	return &GetScoreConfigQueryHandler{db: db}
}

func (h *GetScoreConfigQueryHandler) QueryType() string { return "GetScoreConfig" }

func (h *GetScoreConfigQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetScoreConfigQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetScoreConfigQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"id", qry.ID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing get score config query")

	if qry.TenantID == "" {
		return nil, nil
	}

	var out ScoreConfigReadModel
	err := h.db.GetContext(ctx, &out, `SELECT * FROM score_configs WHERE id = $1 AND tenant_id = $2`, qry.ID, qry.TenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute get score config query")
		return nil, fmt.Errorf("failed to get score config: %w", err)
	}
	return &out, nil
}

// ListScoreConfigsQueryHandler handles ListScoreConfigs queries.
type ListScoreConfigsQueryHandler struct {
	db *sqlx.DB
}

func NewListScoreConfigsQueryHandler(db *sqlx.DB) *ListScoreConfigsQueryHandler {
	return &ListScoreConfigsQueryHandler{db: db}
}

func (h *ListScoreConfigsQueryHandler) QueryType() string { return "ListScoreConfigs" }

func (h *ListScoreConfigsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListScoreConfigsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListScoreConfigsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list score configs query")

	if qry.TenantID == "" {
		return []ScoreConfigReadModel{}, nil
	}

	queryStr := `SELECT * FROM score_configs WHERE tenant_id = $1 AND is_archived = false`
	args := []interface{}{qry.TenantID}
	argIndex := 2

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

	var out []ScoreConfigReadModel
	err := h.db.SelectContext(ctx, &out, queryStr, args...)
	if err != nil {
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute list score configs query")
		return nil, fmt.Errorf("failed to list score configs: %w", err)
	}

	logger.WithFields("count", len(out)).Info("score_configs: list query completed")
	return out, nil
}

// GetEvalRunQueryHandler handles GetEvalRun queries.
type GetEvalRunQueryHandler struct {
	db *sqlx.DB
}

func NewGetEvalRunQueryHandler(db *sqlx.DB) *GetEvalRunQueryHandler {
	return &GetEvalRunQueryHandler{db: db}
}

func (h *GetEvalRunQueryHandler) QueryType() string { return "GetEvalRun" }

func (h *GetEvalRunQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*GetEvalRunQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected GetEvalRunQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"id", qry.ID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing get eval run query")

	if qry.TenantID == "" {
		return nil, nil
	}

	var out EvalRunReadModel
	err := h.db.GetContext(ctx, &out, `SELECT * FROM eval_runs WHERE id = $1 AND tenant_id = $2`, qry.ID, qry.TenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute get eval run query")
		return nil, fmt.Errorf("failed to get eval run: %w", err)
	}
	return &out, nil
}

// ListEvalRunsQueryHandler handles ListEvalRuns queries.
type ListEvalRunsQueryHandler struct {
	db *sqlx.DB
}

func NewListEvalRunsQueryHandler(db *sqlx.DB) *ListEvalRunsQueryHandler {
	return &ListEvalRunsQueryHandler{db: db}
}

func (h *ListEvalRunsQueryHandler) QueryType() string { return "ListEvalRuns" }

func (h *ListEvalRunsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListEvalRunsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListEvalRunsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list eval runs query")

	if qry.TenantID == "" {
		return []EvalRunReadModel{}, nil
	}

	queryStr := `SELECT * FROM eval_runs WHERE tenant_id = $1`
	args := []interface{}{qry.TenantID}
	argIndex := 2

	if qry.DatasetID != nil {
		queryStr += fmt.Sprintf(" AND dataset_id = $%d", argIndex)
		args = append(args, *qry.DatasetID)
		argIndex++
	}
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

	var out []EvalRunReadModel
	err := h.db.SelectContext(ctx, &out, queryStr, args...)
	if err != nil {
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute list eval runs query")
		return nil, fmt.Errorf("failed to list eval runs: %w", err)
	}

	logger.WithFields("count", len(out)).Info("eval_runs: list query completed")
	return out, nil
}

// ListEvalRunItemsQueryHandler handles ListEvalRunItems queries.
type ListEvalRunItemsQueryHandler struct {
	db *sqlx.DB
}

func NewListEvalRunItemsQueryHandler(db *sqlx.DB) *ListEvalRunItemsQueryHandler {
	return &ListEvalRunItemsQueryHandler{db: db}
}

func (h *ListEvalRunItemsQueryHandler) QueryType() string { return "ListEvalRunItems" }

func (h *ListEvalRunItemsQueryHandler) Handle(ctx context.Context, q query.Query) (interface{}, error) {
	qry, ok := q.(*ListEvalRunItemsQuery)
	if !ok {
		return nil, fmt.Errorf("invalid query type, expected ListEvalRunItemsQuery")
	}

	correlationID := correlation.GetCorrelationID(ctx)
	logger.WithFields(
		"query_type", qry.QueryType(),
		"eval_run_id", qry.EvalRunID,
		"tenant_id", qry.TenantID,
		"correlation_id", correlationID,
	).Debug("executing list eval run items query")

	if qry.TenantID == "" {
		return []EvalRunItemReadModel{}, nil
	}

	queryStr := `SELECT eri.*, di.input, di.expected_output
		FROM eval_run_items eri
		JOIN dataset_items di ON di.id = eri.dataset_item_id
		WHERE eri.eval_run_id = $1 AND eri.tenant_id = $2`
	args := []interface{}{qry.EvalRunID, qry.TenantID}
	argIndex := 3

	if qry.Status != nil {
		queryStr += fmt.Sprintf(" AND eri.status = $%d", argIndex)
		args = append(args, *qry.Status)
		argIndex++
	}

	queryStr += " ORDER BY eri.created_at ASC"

	if qry.Limit > 0 {
		queryStr += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, qry.Limit)
		argIndex++
	}
	if qry.Offset > 0 {
		queryStr += fmt.Sprintf(" OFFSET $%d", argIndex)
		args = append(args, qry.Offset)
	}

	var out []EvalRunItemReadModel
	err := h.db.SelectContext(ctx, &out, queryStr, args...)
	if err != nil {
		logger.WithFields(
			"query_type", qry.QueryType(),
			"error", err.Error(),
			"correlation_id", correlationID,
		).Error("failed to execute list eval run items query")
		return nil, fmt.Errorf("failed to list eval run items: %w", err)
	}

	logger.WithFields("count", len(out)).Info("eval_run_items: list query completed")
	return out, nil
}
