package traceoverlays

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	pkgdb "github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/google/uuid"
)

type Recorder struct {
	db *sql.DB
}

func NewRecorder(db *sql.DB) *Recorder {
	return &Recorder{db: db}
}

type Overlay struct {
	TraceID        string
	UpdatedAt      time.Time
	AuthorUserID   string
	DisplayName    *string
	InputOverride  *string
	OutputOverride *string
	Metadata       map[string]string
	Tags           []string
	HiddenSpanIDs  []string
}

type Observation struct {
	ID                  string
	TraceID             string
	ParentObservationID string
	Name                string
	Type                string
	Source              string
	StartTime           time.Time
	EndTime             *time.Time
	Duration            int64
	Level               string
	StatusMessage       string
	Model               string
	InputData           string
	OutputData          string
	InputMimeType       string
	OutputMimeType      string
	InputTokens         *int64
	OutputTokens        *int64
	TotalTokens         *int64
	InputCost           *float64
	OutputCost          *float64
	TotalCost           *float64
	Metadata            map[string]string
	Tags                []string
	AuthorUserID        string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type Annotation struct {
	ID            string
	TraceID       string
	ObservationID string
	Body          string
	Metadata      map[string]string
	AuthorUserID  string
	CreatedAt     time.Time
}

func (r *Recorder) PutOverlay(ctx context.Context, overlay *Overlay) error {
	if overlay == nil {
		return fmt.Errorf("overlay is required")
	}
	if overlay.TraceID == "" {
		return fmt.Errorf("trace_id is required")
	}
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	if overlay.UpdatedAt.IsZero() {
		overlay.UpdatedAt = time.Now()
	}
	if overlay.AuthorUserID == "" {
		overlay.AuthorUserID = contextkeys.GetUserID(ctx)
	}

	metadataJSON := marshalMap(overlay.Metadata)
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO otel_trace_overlays (
			TenantId, TraceId, UpdatedAt, AuthorUserId,
			DisplayName, InputOverride, OutputOverride, Metadata, Tags, HiddenSpanIds
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		tenantID,
		overlay.TraceID,
		overlay.UpdatedAt,
		overlay.AuthorUserID,
		overlay.DisplayName,
		overlay.InputOverride,
		overlay.OutputOverride,
		metadataJSON,
		overlay.Tags,
		overlay.HiddenSpanIDs,
	)
	if err != nil {
		logger.WithFields("trace_id", overlay.TraceID, "error", err.Error()).Error("failed to write trace overlay")
		return fmt.Errorf("failed to insert trace overlay: %w", err)
	}
	return nil
}

func (r *Recorder) GetOverlay(ctx context.Context, traceID string) (*Overlay, error) {
	if traceID == "" {
		return nil, fmt.Errorf("trace_id is required")
	}
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return nil, sql.ErrNoRows
	}

	row := r.db.QueryRowContext(ctx, `
		SELECT
			TraceId, UpdatedAt, AuthorUserId,
			DisplayName, InputOverride, OutputOverride, Metadata, Tags, HiddenSpanIds
		FROM otel_trace_overlays
		WHERE TenantId = ? AND TraceId = ?
		ORDER BY UpdatedAt DESC
		LIMIT 1
	`, tenantID, traceID)

	overlay, err := scanOverlay(row)
	if err != nil {
		return nil, err
	}
	return overlay, nil
}

func (r *Recorder) CreateObservation(ctx context.Context, obs *Observation) error {
	if err := normalizeObservation(ctx, obs); err != nil {
		return err
	}
	return r.insertObservation(ctx, obs)
}

func (r *Recorder) CreateObservations(ctx context.Context, observations []*Observation) error {
	for _, obs := range observations {
		if err := normalizeObservation(ctx, obs); err != nil {
			return err
		}
	}
	for _, obs := range observations {
		if err := r.insertObservation(ctx, obs); err != nil {
			return err
		}
	}
	return nil
}

func (r *Recorder) insertObservation(ctx context.Context, obs *Observation) error {
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO otel_custom_observations (
			TenantId, ObservationId, TraceId, ParentObservationId,
			Name, Type, Source, StartTime, EndTime, Duration, Level,
			StatusMessage, Model, InputData, OutputData, InputMimeType, OutputMimeType,
			InputTokens, OutputTokens, TotalTokens, InputCost, OutputCost, TotalCost,
			Metadata, Tags, AuthorUserId, CreatedAt, UpdatedAt
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		tenantID,
		obs.ID,
		obs.TraceID,
		obs.ParentObservationID,
		obs.Name,
		obs.Type,
		obs.Source,
		obs.StartTime,
		obs.EndTime,
		obs.Duration,
		obs.Level,
		obs.StatusMessage,
		obs.Model,
		obs.InputData,
		obs.OutputData,
		obs.InputMimeType,
		obs.OutputMimeType,
		obs.InputTokens,
		obs.OutputTokens,
		obs.TotalTokens,
		obs.InputCost,
		obs.OutputCost,
		obs.TotalCost,
		marshalMap(obs.Metadata),
		obs.Tags,
		obs.AuthorUserID,
		obs.CreatedAt,
		obs.UpdatedAt,
	)
	if err != nil {
		logger.WithFields("trace_id", obs.TraceID, "observation_id", obs.ID, "error", err.Error()).Error("failed to write custom observation")
		return fmt.Errorf("failed to insert custom observation: %w", err)
	}
	return nil
}

func (r *Recorder) ListObservations(ctx context.Context, traceID string) ([]*Observation, error) {
	if traceID == "" {
		return nil, fmt.Errorf("trace_id is required")
	}
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return []*Observation{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT
			ObservationId, TraceId, ParentObservationId,
			Name, Type, Source, StartTime, EndTime, Duration, Level,
			StatusMessage, Model, InputData, OutputData, InputMimeType, OutputMimeType,
			InputTokens, OutputTokens, TotalTokens, InputCost, OutputCost, TotalCost,
			Metadata, Tags, AuthorUserId, CreatedAt, UpdatedAt
		FROM otel_custom_observations
		WHERE TenantId = ? AND TraceId = ?
		ORDER BY StartTime ASC, CreatedAt ASC
	`, tenantID, traceID)
	if err != nil {
		return nil, fmt.Errorf("failed to query custom observations: %w", err)
	}
	defer rows.Close()

	var observations []*Observation
	for rows.Next() {
		obs, err := scanObservation(rows)
		if err != nil {
			logger.WithFields("trace_id", traceID, "error", err.Error()).Warn("failed to scan custom observation")
			continue
		}
		observations = append(observations, obs)
	}
	return observations, rows.Err()
}

func (r *Recorder) ListObservationsByID(ctx context.Context, ids []string) ([]*Observation, error) {
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" || len(ids) == 0 {
		return []*Observation{}, nil
	}
	placeholders := make([]string, 0, len(ids))
	args := []interface{}{tenantID}
	for _, id := range ids {
		if id == "" {
			continue
		}
		placeholders = append(placeholders, "?")
		args = append(args, id)
	}
	if len(placeholders) == 0 {
		return []*Observation{}, nil
	}

	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(`
		SELECT
			ObservationId, TraceId, ParentObservationId,
			Name, Type, Source, StartTime, EndTime, Duration, Level,
			StatusMessage, Model, InputData, OutputData, InputMimeType, OutputMimeType,
			InputTokens, OutputTokens, TotalTokens, InputCost, OutputCost, TotalCost,
			Metadata, Tags, AuthorUserId, CreatedAt, UpdatedAt
		FROM otel_custom_observations
		WHERE TenantId = ? AND ObservationId IN (%s)
		ORDER BY StartTime ASC, CreatedAt ASC
	`, strings.Join(placeholders, ",")), args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query custom observations by id: %w", err)
	}
	defer rows.Close()

	var observations []*Observation
	for rows.Next() {
		obs, err := scanObservation(rows)
		if err != nil {
			logger.WithFields("error", err.Error()).Warn("failed to scan custom observation")
			continue
		}
		observations = append(observations, obs)
	}
	return observations, rows.Err()
}

func (r *Recorder) CreateAnnotation(ctx context.Context, annotation *Annotation) error {
	if annotation == nil {
		return fmt.Errorf("annotation is required")
	}
	if annotation.TraceID == "" {
		return fmt.Errorf("trace_id is required")
	}
	if strings.TrimSpace(annotation.Body) == "" {
		return fmt.Errorf("body is required")
	}
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	if annotation.ID == "" {
		annotation.ID = uuid.New().String()
	}
	if annotation.CreatedAt.IsZero() {
		annotation.CreatedAt = time.Now()
	}
	if annotation.AuthorUserID == "" {
		annotation.AuthorUserID = contextkeys.GetUserID(ctx)
	}

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO otel_trace_annotations (
			TenantId, AnnotationId, TraceId, ObservationId,
			Body, Metadata, AuthorUserId, CreatedAt
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`,
		tenantID,
		annotation.ID,
		annotation.TraceID,
		annotation.ObservationID,
		annotation.Body,
		marshalMap(annotation.Metadata),
		annotation.AuthorUserID,
		annotation.CreatedAt,
	)
	if err != nil {
		logger.WithFields("trace_id", annotation.TraceID, "annotation_id", annotation.ID, "error", err.Error()).Error("failed to write trace annotation")
		return fmt.Errorf("failed to insert trace annotation: %w", err)
	}
	return nil
}

func (r *Recorder) ListAnnotations(ctx context.Context, traceID string) ([]*Annotation, error) {
	if traceID == "" {
		return nil, fmt.Errorf("trace_id is required")
	}
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return []*Annotation{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT AnnotationId, TraceId, ObservationId, Body, Metadata, AuthorUserId, CreatedAt
		FROM otel_trace_annotations
		WHERE TenantId = ? AND TraceId = ?
		ORDER BY CreatedAt ASC
	`, tenantID, traceID)
	if err != nil {
		return nil, fmt.Errorf("failed to query trace annotations: %w", err)
	}
	defer rows.Close()

	var annotations []*Annotation
	for rows.Next() {
		annotation, err := scanAnnotation(rows)
		if err != nil {
			logger.WithFields("trace_id", traceID, "error", err.Error()).Warn("failed to scan trace annotation")
			continue
		}
		annotations = append(annotations, annotation)
	}
	return annotations, rows.Err()
}

func normalizeObservation(ctx context.Context, obs *Observation) error {
	if obs == nil {
		return fmt.Errorf("observation is required")
	}
	if obs.TraceID == "" {
		return fmt.Errorf("trace_id is required")
	}
	if strings.TrimSpace(obs.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if obs.ID == "" {
		obs.ID = uuid.New().String()
	}
	if obs.Type == "" {
		obs.Type = "SPAN"
	}
	if obs.Source == "" {
		obs.Source = "API"
	}
	if obs.Level == "" {
		obs.Level = "DEFAULT"
	}
	now := time.Now()
	if obs.StartTime.IsZero() {
		obs.StartTime = now
	}
	if obs.EndTime != nil && obs.Duration <= 0 {
		obs.Duration = obs.EndTime.Sub(obs.StartTime).Nanoseconds()
	}
	if obs.Duration < 0 {
		return fmt.Errorf("duration cannot be negative")
	}
	if obs.CreatedAt.IsZero() {
		obs.CreatedAt = now
	}
	if obs.UpdatedAt.IsZero() {
		obs.UpdatedAt = obs.CreatedAt
	}
	if obs.AuthorUserID == "" {
		obs.AuthorUserID = contextkeys.GetUserID(ctx)
	}
	return nil
}

func scanOverlay(scanner interface {
	Scan(dest ...interface{}) error
}) (*Overlay, error) {
	var overlay Overlay
	var displayName, inputOverride, outputOverride sql.NullString
	var metadataJSON string

	if err := scanner.Scan(
		&overlay.TraceID,
		&overlay.UpdatedAt,
		&overlay.AuthorUserID,
		&displayName,
		&inputOverride,
		&outputOverride,
		&metadataJSON,
		&overlay.Tags,
		&overlay.HiddenSpanIDs,
	); err != nil {
		return nil, err
	}
	if displayName.Valid {
		overlay.DisplayName = &displayName.String
	}
	if inputOverride.Valid {
		overlay.InputOverride = &inputOverride.String
	}
	if outputOverride.Valid {
		overlay.OutputOverride = &outputOverride.String
	}
	overlay.Metadata = unmarshalMap(metadataJSON)
	return &overlay, nil
}

func scanObservation(scanner interface {
	Scan(dest ...interface{}) error
}) (*Observation, error) {
	var obs Observation
	var endTime sql.NullTime
	var inputTokens, outputTokens, totalTokens sql.NullInt64
	var inputCost, outputCost, totalCost sql.NullFloat64
	var metadataJSON string

	if err := scanner.Scan(
		&obs.ID,
		&obs.TraceID,
		&obs.ParentObservationID,
		&obs.Name,
		&obs.Type,
		&obs.Source,
		&obs.StartTime,
		&endTime,
		&obs.Duration,
		&obs.Level,
		&obs.StatusMessage,
		&obs.Model,
		&obs.InputData,
		&obs.OutputData,
		&obs.InputMimeType,
		&obs.OutputMimeType,
		&inputTokens,
		&outputTokens,
		&totalTokens,
		&inputCost,
		&outputCost,
		&totalCost,
		&metadataJSON,
		&obs.Tags,
		&obs.AuthorUserID,
		&obs.CreatedAt,
		&obs.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if endTime.Valid {
		obs.EndTime = &endTime.Time
	}
	if inputTokens.Valid {
		obs.InputTokens = &inputTokens.Int64
	}
	if outputTokens.Valid {
		obs.OutputTokens = &outputTokens.Int64
	}
	if totalTokens.Valid {
		obs.TotalTokens = &totalTokens.Int64
	}
	if inputCost.Valid {
		obs.InputCost = &inputCost.Float64
	}
	if outputCost.Valid {
		obs.OutputCost = &outputCost.Float64
	}
	if totalCost.Valid {
		obs.TotalCost = &totalCost.Float64
	}
	obs.Metadata = unmarshalMap(metadataJSON)
	return &obs, nil
}

func scanAnnotation(scanner interface {
	Scan(dest ...interface{}) error
}) (*Annotation, error) {
	var annotation Annotation
	var metadataJSON string
	if err := scanner.Scan(
		&annotation.ID,
		&annotation.TraceID,
		&annotation.ObservationID,
		&annotation.Body,
		&metadataJSON,
		&annotation.AuthorUserID,
		&annotation.CreatedAt,
	); err != nil {
		return nil, err
	}
	annotation.Metadata = unmarshalMap(metadataJSON)
	return &annotation, nil
}

func tenantIDFromContext(ctx context.Context) string {
	if tid := contextkeys.GetTenantID(ctx); tid != "" {
		return tid
	}
	return pkgdb.TenantSchemaFromContext(ctx)
}

func marshalMap(m map[string]string) string {
	if len(m) == 0 {
		return ""
	}
	b, err := json.Marshal(m)
	if err != nil {
		return ""
	}
	return string(b)
}

func unmarshalMap(raw string) map[string]string {
	if raw == "" {
		return map[string]string{}
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return map[string]string{}
	}
	return m
}
