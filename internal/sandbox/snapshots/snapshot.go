// Package snapshots implements named sandbox snapshot management.
//
// A snapshot is a reusable environment template -- it stores an OCI image
// reference and metadata so tenants can create identical sandboxes without
// reinstalling dependencies on every cold start. Snapshots are created from
// a public OCI image or from an existing sandbox's base image.
package snapshots

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// Snapshot states.
const (
	StatePending  = "pending"
	StateBuilding = "building"
	StateActive   = "active"
	StateInactive = "inactive"
	StateError    = "error"
)

// Snapshot is a named, reusable sandbox environment template.
type Snapshot struct {
	ID             string     `db:"id"              json:"id"`
	TenantID       string     `db:"tenant_id"       json:"tenant_id"`
	Name           string     `db:"name"            json:"name"`
	State          string     `db:"state"           json:"state"`
	BaseImage      string     `db:"base_image"      json:"base_image"`
	FromSandboxID  *string    `db:"from_sandbox_id" json:"from_sandbox_id,omitempty"`
	Error          *string    `db:"error"           json:"error,omitempty"`
	SizeBytes      int64      `db:"size_bytes"      json:"size_bytes"`
	CreatedAt      time.Time  `db:"created_at"      json:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"      json:"updated_at"`
	LastUsedAt     *time.Time `db:"last_used_at"    json:"last_used_at,omitempty"`
}

// CreateParams holds the fields needed to create a new snapshot.
type CreateParams struct {
	TenantID       string
	Name           string
	Image          string  // public OCI image ref (e.g. "node:18-slim")
	FromSandboxID  string  // create from existing sandbox (optional)
}

// ErrNotFound is returned when a snapshot row doesn't exist.
var ErrNotFound = errors.New("snapshot not found")

// ErrDuplicateName is returned when a tenant already has a snapshot with the given name.
var ErrDuplicateName = errors.New("snapshot name already exists")

// Repository is the DB layer for snapshots.
type Repository struct {
	db *sqlx.DB
}

// NewRepository binds a Repository to a DB handle.
func NewRepository(db *sqlx.DB) *Repository {
	return &Repository{db: db}
}

// Create inserts a new snapshot. For public-image snapshots the state is
// immediately 'active'; for from-sandbox snapshots it starts 'pending'
// (a background builder would transition it to building → active, but for
// the initial implementation we resolve the base image synchronously).
func (r *Repository) Create(ctx context.Context, p CreateParams) (*Snapshot, error) {
	if strings.TrimSpace(p.Name) == "" {
		return nil, fmt.Errorf("snapshot name is required")
	}

	// Resolve the base image. If from_sandbox_id is supplied, look up that
	// sandbox's image and use it as the base. Otherwise the caller supplies
	// the image directly.
	baseImage := strings.TrimSpace(p.Image)
	var fromSandboxID *string
	if p.FromSandboxID != "" {
		fromSandboxID = &p.FromSandboxID
		if baseImage == "" {
			// Pull image from the sandbox row.
			var img string
			err := r.db.GetContext(ctx, &img,
				`SELECT COALESCE(image, '') FROM sandbox_instances WHERE id = $1`,
				p.FromSandboxID)
			if err != nil {
				return nil, fmt.Errorf("from_sandbox_id %q: %w", p.FromSandboxID, err)
			}
			baseImage = img
		}
	}
	if baseImage == "" {
		return nil, fmt.Errorf("image or from_sandbox_id is required")
	}

	// For public images we go directly to active -- no async build needed.
	// For from-sandbox we also go active immediately (MVP: captures the image ref).
	state := StateActive

	id, err := newID()
	if err != nil {
		return nil, fmt.Errorf("generate id: %w", err)
	}

	const q = `
		INSERT INTO sandbox_snapshots (id, tenant_id, name, state, base_image, from_sandbox_id, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW(), NOW())
		RETURNING id, tenant_id, name, state, base_image, from_sandbox_id, error, size_bytes, created_at, updated_at, last_used_at`
	var snap Snapshot
	if err := r.db.GetContext(ctx, &snap, q,
		id, p.TenantID, strings.TrimSpace(p.Name), state, baseImage, fromSandboxID,
	); err != nil {
		if isDuplicateNameError(err) {
			return nil, ErrDuplicateName
		}
		return nil, fmt.Errorf("insert snapshot: %w", err)
	}
	return &snap, nil
}

// GetByID returns a snapshot by ID, scoped to tenantID.
func (r *Repository) GetByID(ctx context.Context, tenantID, id string) (*Snapshot, error) {
	const q = `SELECT * FROM sandbox_snapshots WHERE id = $1 AND tenant_id = $2`
	var snap Snapshot
	if err := r.db.GetContext(ctx, &snap, q, id, tenantID); err != nil {
		if isNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get snapshot: %w", err)
	}
	return &snap, nil
}

// ListByTenant returns all snapshots for a tenant, most recently updated first.
func (r *Repository) ListByTenant(ctx context.Context, tenantID string) ([]Snapshot, error) {
	const q = `
		SELECT * FROM sandbox_snapshots
		WHERE tenant_id = $1
		ORDER BY updated_at DESC
		LIMIT 500`
	var snaps []Snapshot
	if err := r.db.SelectContext(ctx, &snaps, q, tenantID); err != nil {
		return nil, fmt.Errorf("list snapshots: %w", err)
	}
	if snaps == nil {
		snaps = []Snapshot{}
	}
	return snaps, nil
}

// Delete removes a snapshot. Returns ErrNotFound if it doesn't exist for the tenant.
func (r *Repository) Delete(ctx context.Context, tenantID, id string) error {
	const q = `DELETE FROM sandbox_snapshots WHERE id = $1 AND tenant_id = $2`
	res, err := r.db.ExecContext(ctx, q, id, tenantID)
	if err != nil {
		return fmt.Errorf("delete snapshot: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecordUsage bumps last_used_at and reactivates inactive snapshots.
// Called when a sandbox is created from this snapshot.
func (r *Repository) RecordUsage(ctx context.Context, tenantID, id string) error {
	const q = `
		UPDATE sandbox_snapshots
		SET last_used_at = NOW(),
		    state        = CASE WHEN state = 'inactive' THEN 'active' ELSE state END,
		    updated_at   = NOW()
		WHERE id = $1 AND tenant_id = $2`
	_, err := r.db.ExecContext(ctx, q, id, tenantID)
	return err
}

// DeactivateUnused marks snapshots as inactive when they haven't been used
// for thresholdDays. Safe to run concurrently (advisory lock not needed --
// the UPDATE is idempotent and the cost of concurrent writes is a no-op).
func (r *Repository) DeactivateUnused(ctx context.Context, thresholdDays int) (int, error) {
	const q = `
		UPDATE sandbox_snapshots
		SET state = 'inactive', updated_at = NOW()
		WHERE state = 'active'
		  AND (last_used_at IS NULL OR last_used_at < NOW() - make_interval(days => $1))
		  AND created_at < NOW() - make_interval(days => $1)`
	res, err := r.db.ExecContext(ctx, q, thresholdDays)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// ImageForSnapshot returns the base_image for a snapshot, reactivating it if
// it was inactive. Returns "" if the snapshot doesn't exist or is in error state.
func (r *Repository) ImageForSnapshot(ctx context.Context, tenantID, snapshotID string) (string, error) {
	snap, err := r.GetByID(ctx, tenantID, snapshotID)
	if err != nil {
		return "", err
	}
	if snap.State == StateError {
		return "", fmt.Errorf("snapshot %q is in error state: %s", snapshotID, deref(snap.Error))
	}
	// Reactivate if inactive.
	if snap.State == StateInactive {
		_ = r.RecordUsage(ctx, tenantID, snapshotID)
	}
	return snap.BaseImage, nil
}

func newID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "snap_" + hex.EncodeToString(b), nil
}

func isNotFound(err error) bool {
	return err != nil && (err.Error() == "sql: no rows in result set" ||
		strings.Contains(err.Error(), "no rows"))
}

func isDuplicateNameError(err error) bool {
	return err != nil && strings.Contains(err.Error(), "duplicate key") &&
		strings.Contains(err.Error(), "tenant_id, name")
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
