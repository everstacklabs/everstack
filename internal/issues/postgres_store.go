package issues

import (
	"context"
	"database/sql"
	"time"

	"github.com/jmoiron/sqlx"
)

// Triage status values persisted in Postgres. REGRESSED is derived at read time
// (a resolved issue that recurred), never stored.
const (
	StatusUnresolved = "unresolved"
	StatusResolved   = "resolved"
	StatusIgnored    = "ignored"
)

// IssueState is the mutable triage overlay for an issue, keyed by fingerprint.
type IssueState struct {
	TenantID    string     `db:"tenant_id"`
	Fingerprint string     `db:"fingerprint"`
	Status      string     `db:"status"`
	Assignee    *string    `db:"assignee"`
	SnoozeUntil *time.Time `db:"snooze_until"`
	ResolvedAt  *time.Time `db:"resolved_at"`
	Signature   string     `db:"signature"`
	Title       string     `db:"title"`
	CreatedAt   time.Time  `db:"created_at"`
	UpdatedAt   time.Time  `db:"updated_at"`
}

// PGStore persists issue triage state.
type PGStore struct {
	db *sqlx.DB
}

func NewPGStore(db *sqlx.DB) *PGStore {
	return &PGStore{db: db}
}

// GetStates batch-loads triage state for a set of fingerprints, keyed by
// fingerprint. Always tenant-scoped.
func (s *PGStore) GetStates(ctx context.Context, tenantID string, fingerprints []string) (map[string]IssueState, error) {
	out := make(map[string]IssueState)
	if len(fingerprints) == 0 {
		return out, nil
	}
	query, args, err := sqlx.In(
		`SELECT * FROM issue_states WHERE tenant_id = ? AND fingerprint IN (?)`,
		tenantID, fingerprints,
	)
	if err != nil {
		return nil, err
	}
	query = s.db.Rebind(query)
	var rows []IssueState
	if err := s.db.SelectContext(ctx, &rows, query, args...); err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.Fingerprint] = r
	}
	return out, nil
}

// GetState returns the triage state for one fingerprint, or nil if untracked.
func (s *PGStore) GetState(ctx context.Context, tenantID, fingerprint string) (*IssueState, error) {
	var st IssueState
	err := s.db.GetContext(ctx, &st,
		`SELECT * FROM issue_states WHERE tenant_id = $1 AND fingerprint = $2`, tenantID, fingerprint)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &st, nil
}

// UpsertState writes the triage state for an issue. resolved_at is set when the
// status transitions to resolved and cleared otherwise.
func (s *PGStore) UpsertState(ctx context.Context, st *IssueState) error {
	var resolvedAt interface{}
	if st.Status == StatusResolved {
		now := time.Now().UTC()
		if st.ResolvedAt != nil {
			now = *st.ResolvedAt
		}
		resolvedAt = now
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO issue_states (tenant_id, fingerprint, status, assignee, snooze_until, resolved_at, signature, title)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (tenant_id, fingerprint) DO UPDATE SET
			status = EXCLUDED.status,
			assignee = EXCLUDED.assignee,
			snooze_until = EXCLUDED.snooze_until,
			resolved_at = EXCLUDED.resolved_at,
			signature = COALESCE(NULLIF(EXCLUDED.signature, ''), issue_states.signature),
			title = COALESCE(NULLIF(EXCLUDED.title, ''), issue_states.title),
			updated_at = NOW()`,
		st.TenantID, st.Fingerprint, st.Status, st.Assignee, st.SnoozeUntil, resolvedAt, st.Signature, st.Title,
	)
	return err
}

// ─── Triage activity log ─────────────────────────────────────────────

// IssueActivity is one entry in an issue's triage history.
type IssueActivity struct {
	Actor      string    `db:"actor"`
	Action     string    `db:"action"`
	FromStatus string    `db:"from_status"`
	ToStatus   string    `db:"to_status"`
	Note       string    `db:"note"`
	CreatedAt  time.Time `db:"created_at"`
}

// InsertActivity records a triage event (status change / assignment).
func (s *PGStore) InsertActivity(ctx context.Context, tenantID, fingerprint string, a IssueActivity) error {
	const q = `
		INSERT INTO issue_activity (tenant_id, fingerprint, actor, action, from_status, to_status, note)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err := s.db.ExecContext(ctx, q, tenantID, fingerprint, a.Actor, a.Action, a.FromStatus, a.ToStatus, a.Note)
	return err
}

// ListActivity returns an issue's triage history, newest first.
func (s *PGStore) ListActivity(ctx context.Context, tenantID, fingerprint string, limit int) ([]IssueActivity, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	const q = `
		SELECT actor, action, from_status, to_status, note, created_at
		FROM issue_activity
		WHERE tenant_id = $1 AND fingerprint = $2
		ORDER BY created_at DESC
		LIMIT $3`
	var out []IssueActivity
	if err := s.db.SelectContext(ctx, &out, q, tenantID, fingerprint, limit); err != nil {
		return nil, err
	}
	return out, nil
}
