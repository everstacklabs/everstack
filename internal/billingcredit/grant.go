package billingcredit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ErrOrganizationNotFound is returned when a grant names no organization.
var ErrOrganizationNotFound = errors.New("organization not found")

// GrantIdempotencyKey is the ledger key of a staff grant
// (docs/design/prepaid-credits-v2.md section 5: grant:<ops_grant_id>).
func GrantIdempotencyKey(grantID uuid.UUID) string {
	return "grant:" + grantID.String()
}

// GrantInput is one staff-issued credit grant.
type GrantInput struct {
	OrganizationID uuid.UUID
	GrantID        uuid.UUID
	AmountMicros   int64
	Reason         string
	// Actor is the staff identity that issued the grant.
	Actor string
	// ExpiresAt must be nil: the only expiry sweep for granted credit is the
	// signup credit's (signup.go), so an expiring staff grant would silently
	// never expire. Rejected until staff grants get a sweep of their own.
	ExpiresAt *time.Time
}

// ErrExpiringGrantUnsupported is returned for a grant with an expiry.
var ErrExpiringGrantUnsupported = errors.New("expiring grants are not supported yet")

// GrantResult reports what Grant did.
type GrantResult struct {
	// Granted is false when GrantID had already been applied.
	Granted       bool
	LedgerEntryID uuid.UUID
	BalanceMicros int64
}

// Grant appends a `grant` ledger entry for a staff credit grant, keyed
// grant:<grant_id> so a retried grant lands once, and keeps the balance cache
// in step, under the same per-organization advisory lock every wallet write
// takes. Issuance is separate from payment capture: a grant is never a
// purchase. This is the seam a future invoiced-enterprise path uses.
func Grant(ctx context.Context, db *sqlx.DB, in GrantInput) (GrantResult, error) {
	var res GrantResult
	if db == nil {
		return res, errors.New("billing database is not configured")
	}
	if in.OrganizationID == uuid.Nil || in.GrantID == uuid.Nil {
		return res, errors.New("organization and grant id are required")
	}
	if in.AmountMicros <= 0 {
		return res, errors.New("grant amount must be positive")
	}
	reason := strings.TrimSpace(in.Reason)
	if reason == "" {
		return res, errors.New("a reason is required")
	}
	if in.ExpiresAt != nil {
		return res, ErrExpiringGrantUnsupported
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return res, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	var exists bool
	if err := tx.GetContext(ctx, &exists, `SELECT EXISTS (SELECT 1 FROM everstack.organizations WHERE id = $1)`, in.OrganizationID); err != nil {
		return res, err
	}
	if !exists {
		return res, ErrOrganizationNotFound
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, in.OrganizationID.String()); err != nil {
		return res, err
	}
	meta, _ := json.Marshal(map[string]any{"reason": reason, "actor": in.Actor, "source": "operations"})
	key := GrantIdempotencyKey(in.GrantID)
	err = tx.GetContext(ctx, &res.LedgerEntryID, `
		INSERT INTO billing.credit_ledger (organization_id, entry_type, amount_micros, resource_type, idempotency_key, expires_at, metadata)
		VALUES ($1, 'grant', $2, 'ops_grant', $3, NULL, $4)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING id`, in.OrganizationID, in.AmountMicros, key, meta)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// Already applied: report the existing entry, but only for the same
		// organization (a grant id can never move credit to another org).
		var owner uuid.UUID
		if err := tx.QueryRowxContext(ctx, `SELECT id, organization_id FROM billing.credit_ledger WHERE idempotency_key = $1`, key).
			Scan(&res.LedgerEntryID, &owner); err != nil {
			return res, err
		}
		if owner != in.OrganizationID {
			return res, fmt.Errorf("grant id %s was already used for another organization", in.GrantID)
		}
	case err != nil:
		return res, fmt.Errorf("append grant: %w", err)
	default:
		res.Granted = true
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO billing.credit_balances (organization_id, balance_micros, updated_at)
			VALUES ($1, $2, NOW())
			ON CONFLICT (organization_id) DO UPDATE
			SET balance_micros = billing.credit_balances.balance_micros + EXCLUDED.balance_micros, updated_at = NOW()`,
			in.OrganizationID, in.AmountMicros); err != nil {
			return res, err
		}
	}
	if err := tx.GetContext(ctx, &res.BalanceMicros, `
		SELECT COALESCE((SELECT balance_micros FROM billing.credit_balances WHERE organization_id = $1), 0)`, in.OrganizationID); err != nil {
		return res, err
	}
	if err := tx.Commit(); err != nil {
		return res, err
	}
	committed = true
	return res, nil
}
