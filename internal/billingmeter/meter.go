// Package billingmeter records platform-key inference cost against the fungible
// usage-credit wallet from the gateway. It is the gateway-side counterpart to the
// billing service's debit loop: the gateway can reach the billing.* schema (via
// the BillingDB pool) but cannot import services/billing internals, so the wallet
// writes are self-contained raw SQL under the same per-org advisory lock the
// billing service uses. Metering phase only: this records spend (the wallet may
// go negative); it never holds or blocks. Enforcement is a later phase.
package billingmeter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// inferenceResource is the ledger resource_type for every inference modality, so
// the inference-period ledger CHECK and (later) the inference accounting apply
// uniformly; the modality is carried in metric_type.
const inferenceResource = "inference"

// Modality metric_type values, pinned so the ledger vocabulary cannot drift
// across call sites.
const (
	MetricChat        = "chat"
	MetricEmbeddings  = "embeddings"
	MetricResponses   = "responses"
	MetricImage       = "image"
	MetricAudioInput  = "audio_input"
	MetricAudioOutput = "audio_output"
	MetricModeration  = "moderation"
	MetricRerank      = "rerank"
)

// ResolveOrg maps a request tenant id to its billable organization id. The tenant
// is either an organization itself or a managed instance whose owning org lives
// in everstack.managed_instances. Returns ("", false) when the tenant maps to no
// org (usage is then not billed). Mirrors the billing service's resolver so both
// sides bill the same organization.
func ResolveOrg(ctx context.Context, db *sqlx.DB, tenantID string) (string, bool, error) {
	tenantID = strings.TrimSpace(tenantID)
	if db == nil || tenantID == "" {
		return "", false, nil
	}
	var orgID string
	err := db.GetContext(ctx, &orgID, `SELECT id::text FROM everstack.organizations WHERE id::text = $1`, tenantID)
	if err == nil {
		return orgID, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	err = db.GetContext(ctx, &orgID, `SELECT organization_id::text FROM everstack.managed_instances WHERE id::text = $1`, tenantID)
	if err == nil {
		return orgID, true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return "", false, err
}

// DebitInference records the cost of one platform-key inference request against
// the org's fungible wallet. It is idempotent on requestID, so a durable/retried
// settle debits exactly once. costMicros <= 0 is a no-op. The debit carries the
// current calendar-month period (the inference-period ledger CHECK requires one;
// exact subscription-period alignment is an enforcement-phase concern). Runs in a
// single transaction under the per-org advisory lock the billing service also
// takes, and recomputes the balance cache from the ledger under that lock.
//
// The caller owns durability: settle on a detached context (context.WithoutCancel)
// and retry on error, since this is a one-shot transaction.
func DebitInference(ctx context.Context, db *sqlx.DB, orgID, requestID, modality string, costMicros int64, now time.Time) error {
	orgID = strings.TrimSpace(orgID)
	requestID = strings.TrimSpace(requestID)
	if orgID == "" || requestID == "" {
		return fmt.Errorf("billingmeter: organization and request id are required")
	}
	if costMicros <= 0 {
		return nil
	}
	if db == nil {
		return fmt.Errorf("billingmeter: nil billing database")
	}
	if strings.TrimSpace(modality) == "" {
		modality = MetricChat
	}
	periodStart := time.Date(now.UTC().Year(), now.UTC().Month(), 1, 0, 0, 0, 0, time.UTC)
	periodEnd := periodStart.AddDate(0, 1, 0)

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, orgID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO billing.credit_ledger (organization_id, entry_type, amount_micros, resource_type, metric_type, idempotency_key, period_start, period_end)
		VALUES ($1::uuid, 'debit', $2, $3, $4, $5, $6, $7)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		orgID, -costMicros, inferenceResource, modality, "inference:"+requestID, periodStart, periodEnd); err != nil {
		return fmt.Errorf("debit inference: %w", err)
	}

	// Recompute the balance cache from the ledger (absolute, under the lock) so it
	// stays consistent with the billing service's writes, which take the same lock.
	var balance int64
	if err := tx.GetContext(ctx, &balance, `
		SELECT COALESCE(SUM(amount_micros), 0)::BIGINT FROM billing.credit_ledger WHERE organization_id = $1::uuid`, orgID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO billing.credit_balances (organization_id, balance_micros)
		VALUES ($1::uuid, $2)
		ON CONFLICT (organization_id) DO UPDATE SET balance_micros = EXCLUDED.balance_micros, updated_at = NOW()`,
		orgID, balance); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
