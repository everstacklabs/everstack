package browserpool

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// PostgresUsageMeter owns the durable hosted-browser billing windows. A row is
// open only while a session owns a pool lease; tenant idle pods do not appear.
type PostgresUsageMeter struct {
	db      *sqlx.DB
	pricing UsagePricing
}

type UsageTotals struct {
	RuntimeSeconds int64 `db:"runtime_seconds"`
	CostMicros     int64 `db:"cost_micros"`
}

func NewPostgresUsageMeter(db *sqlx.DB, pricing UsagePricing) (*PostgresUsageMeter, error) {
	if db == nil {
		return nil, fmt.Errorf("browser usage database is required")
	}
	if err := pricing.Validate(); err != nil {
		return nil, err
	}
	return &PostgresUsageMeter{db: db, pricing: pricing}, nil
}

func (m *PostgresUsageMeter) Start(ctx context.Context, lease *Lease, startedAt time.Time) error {
	if m == nil || m.db == nil {
		return fmt.Errorf("browser usage meter is not configured")
	}
	if lease == nil || lease.SessionID == "" || lease.TenantID == "" {
		return fmt.Errorf("browser usage lease identity is required")
	}
	if startedAt.IsZero() {
		startedAt = time.Now()
	}

	var tenantID string
	err := m.db.GetContext(ctx, &tenantID, `
		INSERT INTO browser_usage_windows (
			id, tenant_id, session_id, pod_name,
			started_at, last_heartbeat_at, created_at
		) VALUES ($1, $2, $3, $4, $5, $5, NOW())
		ON CONFLICT (session_id) WHERE ended_at IS NULL
		DO UPDATE SET
			last_heartbeat_at = GREATEST(browser_usage_windows.last_heartbeat_at, EXCLUDED.last_heartbeat_at),
			pod_name = EXCLUDED.pod_name
		WHERE browser_usage_windows.tenant_id = EXCLUDED.tenant_id
		RETURNING tenant_id`,
		uuid.New(), lease.TenantID, lease.SessionID, lease.PodName, startedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("browser session %s is already metered by another tenant", lease.SessionID)
	}
	if err != nil {
		return fmt.Errorf("start browser usage window: %w", err)
	}
	if tenantID != lease.TenantID {
		return fmt.Errorf("browser usage tenant mismatch")
	}
	return nil
}

func (m *PostgresUsageMeter) Heartbeat(ctx context.Context, sessionID string, at time.Time) error {
	if m == nil || m.db == nil || sessionID == "" {
		return nil
	}
	if at.IsZero() {
		at = time.Now()
	}
	_, err := m.db.ExecContext(ctx, `
		UPDATE browser_usage_windows
		SET last_heartbeat_at = GREATEST(last_heartbeat_at, $2)
		WHERE session_id = $1 AND ended_at IS NULL`,
		sessionID, at)
	if err != nil {
		return fmt.Errorf("heartbeat browser usage window: %w", err)
	}
	return nil
}

func (m *PostgresUsageMeter) Finish(ctx context.Context, sessionID string, endedAt time.Time, reason string) error {
	if m == nil || m.db == nil || sessionID == "" {
		return nil
	}
	if endedAt.IsZero() {
		endedAt = time.Now()
	}
	return m.finish(ctx, sessionID, endedAt, reason, nil)
}

func (m *PostgresUsageMeter) finish(
	ctx context.Context,
	sessionID string,
	endedAt time.Time,
	reason string,
	staleBefore *time.Time,
) error {
	tx, err := m.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin browser usage finish: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var window struct {
		StartedAt       time.Time `db:"started_at"`
		LastHeartbeatAt time.Time `db:"last_heartbeat_at"`
	}
	err = tx.GetContext(ctx, &window, `
		SELECT started_at, last_heartbeat_at
		FROM browser_usage_windows
		WHERE session_id = $1 AND ended_at IS NULL
		FOR UPDATE`, sessionID)
	if errors.Is(err, sql.ErrNoRows) {
		return tx.Commit()
	}
	if err != nil {
		return fmt.Errorf("lock browser usage window: %w", err)
	}
	if staleBefore != nil && !window.LastHeartbeatAt.Before(*staleBefore) {
		return tx.Commit()
	}
	if !endedAt.After(window.StartedAt) {
		endedAt = window.StartedAt.Add(time.Nanosecond)
	}

	durationSeconds, billableSeconds, costMicros := m.pricing.PriceWindow(window.StartedAt, endedAt)
	_, err = tx.ExecContext(ctx, `
		UPDATE browser_usage_windows
		SET ended_at = $2,
			duration_seconds = $3,
			billable_seconds = $4,
			cost_micros = $5,
			end_reason = $6,
			updated_at = NOW()
		WHERE session_id = $1 AND ended_at IS NULL`,
		sessionID, endedAt, durationSeconds, billableSeconds, costMicros, reason)
	if err != nil {
		return fmt.Errorf("finish browser usage window: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit browser usage finish: %w", err)
	}
	return nil
}

// RecoverStale closes windows whose owning pool replica stopped heartbeating.
// The Kubernetes lease TTL is used only to prove that the owner is gone. Cost
// stops at the last durable heartbeat: an orphaned pod is not reachable
// through the dead gateway and must never become customer-billed idle time.
func (m *PostgresUsageMeter) RecoverStale(ctx context.Context, now time.Time, leaseTTL time.Duration) error {
	if m == nil || m.db == nil {
		return nil
	}
	if now.IsZero() {
		now = time.Now()
	}
	if leaseTTL <= 0 {
		return fmt.Errorf("browser lease TTL must be positive")
	}
	staleBefore := now.Add(-leaseTTL)

	var windows []struct {
		SessionID       string    `db:"session_id"`
		LastHeartbeatAt time.Time `db:"last_heartbeat_at"`
	}
	if err := m.db.SelectContext(ctx, &windows, `
		SELECT session_id, last_heartbeat_at
		FROM browser_usage_windows
		WHERE ended_at IS NULL AND last_heartbeat_at < $1`,
		staleBefore); err != nil {
		return fmt.Errorf("list stale browser usage windows: %w", err)
	}

	var joined error
	for _, window := range windows {
		if err := m.finish(ctx, window.SessionID, window.LastHeartbeatAt, "lease_expired", &staleBefore); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

// Totals returns a lifetime-monotonic source meter. Central billing assigns
// positive deltas to billing periods using its durable watermark.
func (m *PostgresUsageMeter) Totals(ctx context.Context, now time.Time) (UsageTotals, error) {
	return m.totals(ctx, "", now)
}

// TotalsForTenant is used by the shared cloud reporter so each organization
// advances only its own central-billing watermark.
func (m *PostgresUsageMeter) TotalsForTenant(ctx context.Context, tenantID string, now time.Time) (UsageTotals, error) {
	if tenantID == "" {
		return UsageTotals{}, fmt.Errorf("browser usage tenant ID is required")
	}
	return m.totals(ctx, tenantID, now)
}

func (m *PostgresUsageMeter) totals(ctx context.Context, tenantID string, now time.Time) (UsageTotals, error) {
	if m == nil || m.db == nil {
		return UsageTotals{}, nil
	}
	if now.IsZero() {
		now = time.Now()
	}

	var totals UsageTotals
	if err := m.db.GetContext(ctx, &totals, `
		SELECT
			COALESCE(SUM(duration_seconds), 0) AS runtime_seconds,
			COALESCE(SUM(cost_micros), 0) AS cost_micros
		FROM browser_usage_windows
		WHERE ended_at IS NOT NULL
		  AND ($1 = '' OR tenant_id = $1)`, tenantID); err != nil {
		return UsageTotals{}, fmt.Errorf("sum finalized browser usage: %w", err)
	}

	var open []struct {
		StartedAt time.Time `db:"started_at"`
	}
	if err := m.db.SelectContext(ctx, &open, `
		SELECT started_at
		FROM browser_usage_windows
		WHERE ended_at IS NULL
		  AND ($1 = '' OR tenant_id = $1)`, tenantID); err != nil {
		return UsageTotals{}, fmt.Errorf("list open browser usage: %w", err)
	}
	for _, window := range open {
		durationSeconds, _, costMicros := m.pricing.PriceWindow(window.StartedAt, now)
		totals.RuntimeSeconds += durationSeconds
		totals.CostMicros += costMicros
	}
	return totals, nil
}
