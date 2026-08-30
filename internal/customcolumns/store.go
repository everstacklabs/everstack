package customcolumns

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"time"

	pkgdb "github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// colKeyPattern restricts a column key to a safe identifier charset. The key is
// our own handle (used as a map key and, later, potentially inlined into SQL),
// so it must never contain anything that could escape a string literal.
var colKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9_]{1,64}$`)

// ValidateKey reports whether a column key is a safe identifier.
func ValidateKey(key string) error {
	if !colKeyPattern.MatchString(key) {
		return fmt.Errorf("column key must match %s", colKeyPattern.String())
	}
	return nil
}

// StoredColumn is a persisted custom-column definition for a tenant.
type StoredColumn struct {
	Key       string
	Label     string
	ValueType ValueType
	Source    Source
	SourceRef string
	Position  int32
	UpdatedAt time.Time
}

// Definition returns the pure resolver Column for this stored definition.
func (s StoredColumn) Definition() Column {
	return Column{
		Key:       s.Key,
		Label:     s.Label,
		ValueType: s.ValueType,
		Source:    s.Source,
		SourceRef: s.SourceRef,
	}
}

// Store persists tenant-scoped custom-column definitions in ClickHouse,
// append-only with read-latest per (TenantId, ColKey). It reuses the same
// ClickHouse handle as the trace-overlay recorder.
type Store struct {
	db *sql.DB
}

// NewStore creates a custom-column store over the given ClickHouse handle.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func tenantIDFromContext(ctx context.Context) string {
	if tid := contextkeys.GetTenantID(ctx); tid != "" {
		return tid
	}
	return pkgdb.TenantSchemaFromContext(ctx)
}

// Put inserts or updates a column definition (append-only). The latest row per
// (TenantId, ColKey) wins on read.
func (r *Store) Put(ctx context.Context, col *StoredColumn) error {
	if col == nil {
		return fmt.Errorf("column is required")
	}
	if err := ValidateKey(col.Key); err != nil {
		return err
	}
	if col.ValueType == "" {
		col.ValueType = TypeString
	}
	if col.Source == "" {
		return fmt.Errorf("source is required")
	}
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	col.UpdatedAt = time.Now()

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO otel_trace_custom_columns (
			TenantId, ColKey, Label, ValueType, Source, SourceRef, Position, IsActive, UpdatedAt
		) VALUES (?, ?, ?, ?, ?, ?, ?, 1, ?)
	`,
		tenantID,
		col.Key,
		col.Label,
		string(col.ValueType),
		string(col.Source),
		col.SourceRef,
		col.Position,
		col.UpdatedAt,
	)
	if err != nil {
		logger.WithFields("col_key", col.Key, "error", err.Error()).Error("failed to write custom column")
		return fmt.Errorf("failed to insert custom column: %w", err)
	}
	return nil
}

// Delete soft-deletes a column by inserting a tombstone row (IsActive = 0).
func (r *Store) Delete(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO otel_trace_custom_columns (
			TenantId, ColKey, Label, ValueType, Source, SourceRef, Position, IsActive, UpdatedAt
		) VALUES (?, ?, '', '', '', '', 0, 0, ?)
	`, tenantID, key, time.Now())
	if err != nil {
		logger.WithFields("col_key", key, "error", err.Error()).Error("failed to delete custom column")
		return fmt.Errorf("failed to delete custom column: %w", err)
	}
	return nil
}

// List returns the active column definitions for the context tenant, ordered by
// Position then Key. Takes the latest row per ColKey and drops tombstones.
func (r *Store) List(ctx context.Context) ([]StoredColumn, error) {
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return []StoredColumn{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT ColKey, Label, ValueType, Source, SourceRef, Position, IsActive, LatestUpdatedAt
		FROM (
			SELECT
				ColKey,
				argMax(Label, UpdatedAt) AS Label,
				argMax(ValueType, UpdatedAt) AS ValueType,
				argMax(Source, UpdatedAt) AS Source,
				argMax(SourceRef, UpdatedAt) AS SourceRef,
				argMax(Position, UpdatedAt) AS Position,
				argMax(IsActive, UpdatedAt) AS IsActive,
				max(UpdatedAt) AS LatestUpdatedAt
			FROM otel_trace_custom_columns
			WHERE TenantId = ?
			GROUP BY ColKey
		)
		WHERE IsActive = 1
		ORDER BY Position ASC, ColKey ASC
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query custom columns: %w", err)
	}
	defer rows.Close()

	var cols []StoredColumn
	for rows.Next() {
		var (
			c        StoredColumn
			vt, src  string
			isActive uint8
		)
		if err := rows.Scan(&c.Key, &c.Label, &vt, &src, &c.SourceRef, &c.Position, &isActive, &c.UpdatedAt); err != nil {
			logger.WithFields("error", err.Error()).Warn("failed to scan custom column")
			continue
		}
		c.ValueType = ValueType(vt)
		c.Source = Source(src)
		cols = append(cols, c)
	}
	return cols, rows.Err()
}
