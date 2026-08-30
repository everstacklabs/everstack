package scores

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Recorder handles recording scores to ClickHouse
type Recorder struct {
	db *sql.DB
}

// NewRecorder creates a new score recorder
func NewRecorder(db *sql.DB) *Recorder {
	return &Recorder{db: db}
}

// Record saves a score to the database
func (r *Recorder) Record(ctx context.Context, score *Score) error {
	if err := score.Validate(); err != nil {
		return fmt.Errorf("invalid score: %w", err)
	}

	// Generate ID if not set
	if score.ID == "" {
		score.ID = uuid.New().String()
	}

	// Ensure timestamps are set
	now := time.Now()
	if score.Timestamp.IsZero() {
		score.Timestamp = now
	}
	if score.CreatedAt.IsZero() {
		score.CreatedAt = now
	}
	if score.UpdatedAt.IsZero() {
		score.UpdatedAt = now
	}

	// Serialize metadata
	var metadataJSON string
	if len(score.Metadata) > 0 {
		if data, err := json.Marshal(score.Metadata); err == nil {
			metadataJSON = string(data)
		}
	}

	// Insert into ClickHouse
	query := `
		INSERT INTO otel_trace_scores (
			ScoreId, TraceId, ObservationId, Timestamp, CreatedAt, UpdatedAt,
			Name, Source, DataType,
			NumericValue, StringValue, BooleanValue,
			Comment, AuthorUserId, ConfigId, QueueId, Metadata, Environment
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := r.db.ExecContext(ctx, query,
		score.ID,
		score.TraceID,
		score.ObservationID,
		score.Timestamp,
		score.CreatedAt,
		score.UpdatedAt,
		score.Name,
		string(score.Source),
		string(score.DataType),
		score.NumericValue,
		score.StringValue,
		boolToUint8(score.BooleanValue),
		score.Comment,
		score.AuthorUserID,
		score.ConfigID,
		score.QueueID,
		metadataJSON,
		score.Environment,
	)

	if err != nil {
		logger.WithFields(
			"score_id", score.ID,
			"trace_id", score.TraceID,
			"error", err.Error(),
		).Error("failed to record score")
		return fmt.Errorf("failed to insert score: %w", err)
	}

	logger.WithFields(
		"score_id", score.ID,
		"trace_id", score.TraceID,
		"name", score.Name,
		"source", score.Source,
	).Debug("score recorded successfully")

	return nil
}

// RecordBatch records multiple scores in a single transaction
func (r *Recorder) RecordBatch(ctx context.Context, scores []*Score) error {
	if len(scores) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO otel_trace_scores (
			ScoreId, TraceId, ObservationId, Timestamp, CreatedAt, UpdatedAt,
			Name, Source, DataType,
			NumericValue, StringValue, BooleanValue,
			Comment, AuthorUserId, ConfigId, QueueId, Metadata, Environment
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, score := range scores {
		if err := score.Validate(); err != nil {
			return fmt.Errorf("invalid score in batch: %w", err)
		}

		if score.ID == "" {
			score.ID = uuid.New().String()
		}

		now := time.Now()
		if score.Timestamp.IsZero() {
			score.Timestamp = now
		}
		if score.CreatedAt.IsZero() {
			score.CreatedAt = now
		}
		if score.UpdatedAt.IsZero() {
			score.UpdatedAt = now
		}

		var metadataJSON string
		if len(score.Metadata) > 0 {
			if data, err := json.Marshal(score.Metadata); err == nil {
				metadataJSON = string(data)
			}
		}

		_, err = stmt.ExecContext(ctx,
			score.ID,
			score.TraceID,
			score.ObservationID,
			score.Timestamp,
			score.CreatedAt,
			score.UpdatedAt,
			score.Name,
			string(score.Source),
			string(score.DataType),
			score.NumericValue,
			score.StringValue,
			boolToUint8(score.BooleanValue),
			score.Comment,
			score.AuthorUserID,
			score.ConfigID,
			score.QueueID,
			metadataJSON,
			score.Environment,
		)

		if err != nil {
			return fmt.Errorf("failed to insert score %s: %w", score.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	logger.WithFields("count", len(scores)).Debug("batch scores recorded successfully")
	return nil
}

// GetScoresByTrace retrieves all scores for a specific trace
func (r *Recorder) GetScoresByTrace(ctx context.Context, traceID string) ([]*Score, error) {
	query := `
		SELECT 
			ScoreId, TraceId, ObservationId, Timestamp, CreatedAt, UpdatedAt,
			Name, Source, DataType,
			NumericValue, StringValue, BooleanValue,
			Comment, AuthorUserId, ConfigId, QueueId, Metadata, Environment
		FROM otel_trace_scores
		WHERE TraceId = ?
		ORDER BY Timestamp DESC
	`

	rows, err := r.db.QueryContext(ctx, query, traceID)
	if err != nil {
		return nil, fmt.Errorf("failed to query scores: %w", err)
	}
	defer rows.Close()

	var scores []*Score
	for rows.Next() {
		score, err := scanScore(rows)
		if err != nil {
			logger.WithFields("trace_id", traceID, "error", err.Error()).Warn("failed to scan score")
			continue
		}
		scores = append(scores, score)
	}

	return scores, rows.Err()
}

// GetScoresByTraces retrieves scores for several traces at once, keyed by
// TraceId. Used to resolve score-sourced custom columns for a page of traces in
// one query instead of one per trace.
func (r *Recorder) GetScoresByTraces(ctx context.Context, traceIDs []string) (map[string][]*Score, error) {
	out := map[string][]*Score{}
	if len(traceIDs) == 0 {
		return out, nil
	}
	placeholders := make([]string, len(traceIDs))
	args := make([]interface{}, len(traceIDs))
	for i, id := range traceIDs {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf(`
		SELECT
			ScoreId, TraceId, ObservationId, Timestamp, CreatedAt, UpdatedAt,
			Name, Source, DataType,
			NumericValue, StringValue, BooleanValue,
			Comment, AuthorUserId, ConfigId, QueueId, Metadata, Environment
		FROM otel_trace_scores
		WHERE TraceId IN (%s)
		ORDER BY Timestamp DESC
	`, strings.Join(placeholders, ", "))

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to query scores by traces: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		score, err := scanScore(rows)
		if err != nil {
			logger.WithFields("error", err.Error()).Warn("failed to scan score")
			continue
		}
		out[score.TraceID] = append(out[score.TraceID], score)
	}
	return out, rows.Err()
}

// Delete removes a score by ID and TraceID
func (r *Recorder) Delete(ctx context.Context, scoreID, traceID string) error {
	if scoreID == "" {
		return fmt.Errorf("score_id is required")
	}
	if traceID == "" {
		return fmt.Errorf("trace_id is required")
	}

	query := `ALTER TABLE otel_trace_scores DELETE WHERE ScoreId = ? AND TraceId = ?`

	_, err := r.db.ExecContext(ctx, query, scoreID, traceID)
	if err != nil {
		logger.WithFields(
			"score_id", scoreID,
			"trace_id", traceID,
			"error", err.Error(),
		).Error("failed to delete score")
		return fmt.Errorf("failed to delete score: %w", err)
	}

	logger.WithFields(
		"score_id", scoreID,
		"trace_id", traceID,
	).Debug("score deleted successfully")

	return nil
}

// scanScore scans a database row into a Score struct
func scanScore(scanner interface {
	Scan(dest ...interface{}) error
}) (*Score, error) {
	var score Score
	var numericValue sql.NullFloat64
	var stringValue sql.NullString
	var booleanValueUint8 sql.NullByte
	var metadataJSON string

	err := scanner.Scan(
		&score.ID,
		&score.TraceID,
		&score.ObservationID,
		&score.Timestamp,
		&score.CreatedAt,
		&score.UpdatedAt,
		&score.Name,
		&score.Source,
		&score.DataType,
		&numericValue,
		&stringValue,
		&booleanValueUint8,
		&score.Comment,
		&score.AuthorUserID,
		&score.ConfigID,
		&score.QueueID,
		&metadataJSON,
		&score.Environment,
	)

	if err != nil {
		return nil, err
	}

	// Convert nullable types
	if numericValue.Valid {
		score.NumericValue = &numericValue.Float64
	}
	if stringValue.Valid {
		score.StringValue = &stringValue.String
	}
	if booleanValueUint8.Valid {
		boolVal := booleanValueUint8.Byte != 0
		score.BooleanValue = &boolVal
	}

	// Parse metadata JSON
	if metadataJSON != "" {
		if err := json.Unmarshal([]byte(metadataJSON), &score.Metadata); err != nil {
			logger.WithFields("score_id", score.ID, "error", err.Error()).Warn("failed to parse metadata")
		}
	}

	return &score, nil
}

// boolToUint8 converts a bool pointer to uint8 pointer (0 or 1)
func boolToUint8(b *bool) *uint8 {
	if b == nil {
		return nil
	}
	var val uint8
	if *b {
		val = 1
	}
	return &val
}

