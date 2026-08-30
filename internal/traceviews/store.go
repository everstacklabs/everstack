// Package traceviews persists saved views for the traces table: a named bundle
// of visible columns + filters + sort. The view config is an opaque JSON blob
// owned by the frontend; this store is a tenant-scoped CRUD layer over a
// ClickHouse table, append-only with read-latest per (TenantId, ViewId).
package traceviews

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	pkgdb "github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/google/uuid"
)

// View is a saved traces-table view.
type View struct {
	ID           string
	Name         string
	ConfigJSON   string // opaque: { visibleColumns, filters, sort, ... }
	AuthorUserID string
	UpdatedAt    time.Time
}

// Store persists views in ClickHouse, append-only with read-latest.
type Store struct {
	db *sql.DB
}

// NewStore creates a view store over the given ClickHouse handle.
func NewStore(db *sql.DB) *Store {
	return &Store{db: db}
}

func tenantIDFromContext(ctx context.Context) string {
	if tid := contextkeys.GetTenantID(ctx); tid != "" {
		return tid
	}
	return pkgdb.TenantSchemaFromContext(ctx)
}

// Put inserts or updates a view. A new view (empty ID) gets a generated id,
// which is returned on v.ID.
func (r *Store) Put(ctx context.Context, v *View) error {
	if v == nil {
		return fmt.Errorf("view is required")
	}
	if v.Name == "" {
		return fmt.Errorf("name is required")
	}
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	if v.ID == "" {
		v.ID = uuid.NewString()
	}
	if v.AuthorUserID == "" {
		v.AuthorUserID = contextkeys.GetUserID(ctx)
	}
	v.UpdatedAt = time.Now()

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO otel_trace_views (
			TenantId, ViewId, Name, ConfigJson, AuthorUserId, IsActive, UpdatedAt
		) VALUES (?, ?, ?, ?, ?, 1, ?)
	`, tenantID, v.ID, v.Name, v.ConfigJSON, v.AuthorUserID, v.UpdatedAt)
	if err != nil {
		logger.WithFields("view_id", v.ID, "error", err.Error()).Error("failed to write trace view")
		return fmt.Errorf("failed to insert trace view: %w", err)
	}
	return nil
}

// Delete soft-deletes a view by inserting a tombstone (IsActive = 0).
func (r *Store) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is required")
	}
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return fmt.Errorf("tenant_id is required")
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO otel_trace_views (
			TenantId, ViewId, Name, ConfigJson, AuthorUserId, IsActive, UpdatedAt
		) VALUES (?, ?, '', '', '', 0, ?)
	`, tenantID, id, time.Now())
	if err != nil {
		logger.WithFields("view_id", id, "error", err.Error()).Error("failed to delete trace view")
		return fmt.Errorf("failed to delete trace view: %w", err)
	}
	return nil
}

// List returns the active views for the context tenant, latest row per ViewId,
// tombstones dropped, ordered by Name.
func (r *Store) List(ctx context.Context) ([]View, error) {
	tenantID := tenantIDFromContext(ctx)
	if tenantID == "" {
		return []View{}, nil
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT ViewId, Name, ConfigJson, AuthorUserId, IsActive, LatestUpdatedAt
		FROM (
			SELECT
				ViewId,
				argMax(Name, UpdatedAt) AS Name,
				argMax(ConfigJson, UpdatedAt) AS ConfigJson,
				argMax(AuthorUserId, UpdatedAt) AS AuthorUserId,
				argMax(IsActive, UpdatedAt) AS IsActive,
				max(UpdatedAt) AS LatestUpdatedAt
			FROM otel_trace_views
			WHERE TenantId = ?
			GROUP BY ViewId
		)
		WHERE IsActive = 1
		ORDER BY Name ASC
	`, tenantID)
	if err != nil {
		return nil, fmt.Errorf("failed to query trace views: %w", err)
	}
	defer rows.Close()

	var views []View
	for rows.Next() {
		var (
			v        View
			isActive uint8
		)
		if err := rows.Scan(&v.ID, &v.Name, &v.ConfigJSON, &v.AuthorUserID, &isActive, &v.UpdatedAt); err != nil {
			logger.WithFields("error", err.Error()).Warn("failed to scan trace view")
			continue
		}
		views = append(views, v)
	}
	return views, rows.Err()
}
