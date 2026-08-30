// Package logcolumns persists tenant-scoped custom columns for the logs table:
// a label + a LogAttributes key, so a tenant surfaces the log fields THEY care
// about. Mirrors internal/customcolumns but for logs (attribute-source only).
package logcolumns

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

// colKeyPattern restricts a column key to a safe identifier; it is used as a map
// key and inlined into SQL, so nothing that could escape a literal is allowed.
var colKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9_]{1,64}$`)

// attrKeyPattern bounds a LogAttributes key (bound as a param, so this is a
// sanity bound).
var attrKeyPattern = regexp.MustCompile(`^[a-zA-Z0-9_.:/\-]{1,128}$`)

// ValidateKey reports whether a column key is a safe identifier.
func ValidateKey(key string) error {
	if !colKeyPattern.MatchString(key) {
		return fmt.Errorf("column key must match %s", colKeyPattern.String())
	}
	return nil
}

// ValidateAttrKey reports whether a LogAttributes key is acceptable.
func ValidateAttrKey(attrKey string) error {
	if !attrKeyPattern.MatchString(attrKey) {
		return fmt.Errorf("attribute key must match %s", attrKeyPattern.String())
	}
	return nil
}

// Column is a persisted custom log-column definition.
type Column struct {
	Key       string
	Label     string
	AttrKey   string
	Position  int32
	UpdatedAt time.Time
}

// Store persists log custom columns in ClickHouse, append-only read-latest.
type Store struct {
	db *sql.DB
}

// NewStore creates a log-column store over the given ClickHouse handle.
func NewStore(db *sql.DB) *Store { return &Store{db: db} }

func tenantIDFromContext(ctx context.Context) string {
	if tid := contextkeys.GetTenantID(ctx); tid != "" {
		return tid
	}
	return pkgdb.TenantSchemaFromContext(ctx)
}

// Put inserts or updates a column (append-only; latest per ColKey wins).
func (r *Store) Put(ctx context.Context, c *Column) error {
	if c == nil {
		return fmt.Errorf("column is required")
	}
	if err := ValidateKey(c.Key); err != nil {
		return err
	}
	if err := ValidateAttrKey(c.AttrKey); err != nil {
		return err
	}
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	c.UpdatedAt = time.Now()
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO otel_log_custom_columns (TenantId, ColKey, Label, AttrKey, Position, IsActive, UpdatedAt)
		VALUES (?, ?, ?, ?, ?, 1, ?)
	`, tenantID, c.Key, c.Label, c.AttrKey, c.Position, c.UpdatedAt)
	if err != nil {
		logger.WithFields("col_key", c.Key, "error", err.Error()).Error("failed to write log custom column")
		return fmt.Errorf("failed to insert log custom column: %w", err)
	}
	return nil
}

// Delete soft-deletes a column by inserting a tombstone (IsActive = 0).
func (r *Store) Delete(ctx context.Context, key string) error {
	if err := ValidateKey(key); err != nil {
		return err
	}
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO otel_log_custom_columns (TenantId, ColKey, Label, AttrKey, Position, IsActive, UpdatedAt)
		VALUES (?, ?, '', '', 0, 0, ?)
	`, tenantID, key, time.Now())
	if err != nil {
		logger.WithFields("col_key", key, "error", err.Error()).Error("failed to delete log custom column")
		return fmt.Errorf("failed to delete log custom column: %w", err)
	}
	return nil
}

// List returns the tenant's active columns, latest per ColKey, ordered by
// Position then Key. Invalid rows are filtered defensively (the key is inlined).
func (r *Store) List(ctx context.Context) ([]Column, error) {
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return []Column{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT ColKey, Label, AttrKey, Position, IsActive, LatestUpdatedAt
		FROM (
			SELECT
				ColKey,
				argMax(Label, UpdatedAt) AS Label,
				argMax(AttrKey, UpdatedAt) AS AttrKey,
				argMax(Position, UpdatedAt) AS Position,
				argMax(IsActive, UpdatedAt) AS IsActive,
				max(UpdatedAt) AS LatestUpdatedAt
			FROM otel_log_custom_columns
			WHERE TenantId = ?
			GROUP BY ColKey
		)
		WHERE IsActive = 1
		ORDER BY Position ASC, ColKey ASC
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query log custom columns: %w", err)
	}
	defer rows.Close()

	var cols []Column
	for rows.Next() {
		var (
			c        Column
			isActive uint8
		)
		if err := rows.Scan(&c.Key, &c.Label, &c.AttrKey, &c.Position, &isActive, &c.UpdatedAt); err != nil {
			logger.WithFields("error", err.Error()).Warn("failed to scan log custom column")
			continue
		}
		if ValidateKey(c.Key) != nil || ValidateAttrKey(c.AttrKey) != nil {
			continue
		}
		cols = append(cols, c)
	}
	return cols, rows.Err()
}
