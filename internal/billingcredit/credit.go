// Package billingcredit owns the managed-cloud starter-credit policy. The $5
// signup credit is a grant into the fungible usage-credit wallet (billing.credit_ledger),
// NOT a sandbox-only credit: it is spendable across sandbox compute, ingested
// data, and (later) inference. This package grants it and gates sandbox on the
// wallet balance; sandbox compute cost is debited against the same wallet here.
package billingcredit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/everstacklabs/everstack/pkg/plans"
	"github.com/jmoiron/sqlx"
)

const (
	// sandboxCostMetric is the cumulative sandbox compute cost (micro-dollars)
	// recorded in billing.usage_meter_watermarks; billingcredit debits its delta
	// into the wallet.
	sandboxCostMetric = "sandbox_compute_cost_micros"
	// SignupGrantResourceType tags the one-time fungible signup credit in the
	// ledger. Exported so the usage-debit backstop grants it identically.
	SignupGrantResourceType = "signup"
	sandboxResource         = "sandbox"
)

// SignupGrantIdempotencyKey is the ledger idempotency key for an org's one-time
// fungible signup grant. Shared so every grant site (the sandbox gate here and
// the usage-debit backstop) de-duplicates against the same key.
func SignupGrantIdempotencyKey(organizationID string) string {
	return "signup-grant:" + organizationID
}

// Status describes an organization's fungible signup credit (wallet), surfaced
// to the UI. Remaining is the spendable wallet balance (grants minus every
// debit and hold), not a sandbox-specific figure.
type Status struct {
	AmountMicros    int64
	UsedMicros      int64
	RemainingMicros int64
}

// Access describes whether managed sandbox compute may be allocated.
type Access struct {
	Allowed           bool
	BillingConfigured bool
	BillingActive     bool
	Credit            Status
}

// StarterCreditMicros reads the public signup-credit amount from the canonical
// plans data. A missing or invalid value fails closed instead of granting
// unbounded compute.
func StarterCreditMicros() int64 {
	cfg, err := plans.Embedded()
	if err != nil || cfg == nil || cfg.SandboxComputePricing == nil {
		return 0
	}
	usd := cfg.SandboxComputePricing.StarterCreditUSD
	if usd <= 0 || math.IsNaN(usd) || math.IsInf(usd, 0) {
		return 0
	}
	return int64(math.Round(usd * 1_000_000))
}

// Resolve returns the managed sandbox access policy for one organization.
// Enterprise contracts bypass public billing. A public subscription must be
// active; an organization without one instead spends its fungible signup
// credit, and sandbox is gated on the wallet balance.
func Resolve(ctx context.Context, db *sqlx.DB, organizationID, tier string) (Access, error) {
	if db == nil || strings.TrimSpace(organizationID) == "" {
		return Access{}, fmt.Errorf("sandbox billing database and organization are required")
	}
	if strings.EqualFold(strings.TrimSpace(tier), "enterprise") {
		return Access{Allowed: true}, nil
	}

	var subscription struct {
		Status               string `db:"status"`
		StripeCustomerID     string `db:"stripe_customer_id"`
		StripeSubscriptionID string `db:"stripe_subscription_id"`
	}
	err := db.GetContext(ctx, &subscription, `
		SELECT status, stripe_customer_id, stripe_subscription_id
		FROM billing.subscriptions
		WHERE organization_id = $1`, organizationID)
	if err == nil {
		active := (strings.EqualFold(subscription.Status, "active") ||
			strings.EqualFold(subscription.Status, "trialing")) &&
			strings.TrimSpace(subscription.StripeCustomerID) != "" &&
			strings.TrimSpace(subscription.StripeSubscriptionID) != ""
		return Access{
			Allowed:           active,
			BillingConfigured: true,
			BillingActive:     active,
		}, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Access{}, fmt.Errorf("resolve sandbox billing subscription: %w", err)
	}

	// No subscription: the free tier spends its fungible signup credit, with
	// sandbox cost debited against it below. Known bounded limitation: this
	// subscription check is outside the per-org wallet lock, and subscription
	// creation does not take that lock, so sandbox cost accrued during a
	// free->active upgrade window can be left unreconciled on the wallet (once
	// active, Resolve early-returns above and sandbox bills via Stripe instead).
	// The gap is capped at the signup grant and closes when activation-time
	// wallet reconciliation is wired with the Stripe activation flow.
	credit, err := resolveWalletCredit(ctx, db, organizationID, StarterCreditMicros())
	if err != nil {
		return Access{}, err
	}
	return Access{
		Allowed: credit.RemainingMicros > 0,
		Credit:  credit,
	}, nil
}

// resolveWalletCredit ensures the one-time fungible signup grant exists, debits
// the sandbox compute cost consumed since it was last billed, and returns the
// wallet status — all in one transaction under the org advisory lock (the same
// lock the billing service's wallet writes take), so a concurrent debit/grant
// can't race the balance.
func resolveWalletCredit(ctx context.Context, db *sqlx.DB, organizationID string, grantMicros int64) (Status, error) {
	if grantMicros <= 0 {
		return Status{}, fmt.Errorf("signup credit is not configured")
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return Status{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, organizationID); err != nil {
		return Status{}, err
	}

	// 1) One-time fungible signup grant (idempotent).
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO billing.credit_ledger (organization_id, entry_type, amount_micros, resource_type, idempotency_key)
		VALUES ($1::uuid, 'grant', $2, $3, $4)
		ON CONFLICT (idempotency_key) DO NOTHING`, organizationID, grantMicros, SignupGrantResourceType, SignupGrantIdempotencyKey(organizationID)); err != nil {
		return Status{}, fmt.Errorf("grant signup credit: %w", err)
	}

	// 2) Debit the sandbox compute cost consumed since the last billed cumulative.
	var cumulativeSandboxCost int64
	if err := tx.GetContext(ctx, &cumulativeSandboxCost, `
		SELECT COALESCE(SUM(total_value), 0)::BIGINT
		FROM billing.usage_meter_watermarks
		WHERE organization_id = $1::uuid AND metric_type = $2`, organizationID, sandboxCostMetric); err != nil {
		return Status{}, fmt.Errorf("read sandbox lifetime cost: %w", err)
	}
	var billed int64
	err = tx.GetContext(ctx, &billed, `
		SELECT billed_quantity::BIGINT FROM billing.usage_debit_watermark
		WHERE organization_id = $1::uuid AND metric_type = $2 FOR UPDATE`, organizationID, sandboxCostMetric)
	if errors.Is(err, sql.ErrNoRows) {
		// First debit for this org: seed the watermark at the enrollment baseline
		// so sandbox cost accrued BEFORE the credit existed is never retroactively
		// charged (bill-from-zero-at-enrollment). Orgs migrating off the legacy
		// sandbox-only credit carry that baseline in billing.sandbox_starter_credits;
		// brand-new orgs bill from the current cumulative (~0 at signup, since the
		// gate runs before a VM is allocated). The legacy table is guaranteed
		// present here (its migration precedes the wallet migrations) — a future
		// migration that drops it must update this baseline seed.
		if err := tx.GetContext(ctx, &billed, `
			SELECT COALESCE(
				(SELECT baseline_cost_micros FROM billing.sandbox_starter_credits WHERE organization_id = $1::uuid),
				$2
			)::BIGINT`, organizationID, cumulativeSandboxCost); err != nil {
			return Status{}, fmt.Errorf("seed sandbox debit baseline: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO billing.usage_debit_watermark (organization_id, metric_type, billed_quantity)
			VALUES ($1::uuid, $2, $3)
			ON CONFLICT (organization_id, metric_type) DO NOTHING`,
			organizationID, sandboxCostMetric, billed); err != nil {
			return Status{}, fmt.Errorf("persist sandbox debit baseline: %w", err)
		}
	} else if err != nil {
		return Status{}, fmt.Errorf("read sandbox debit watermark: %w", err)
	}
	if delta := cumulativeSandboxCost - billed; delta > 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO billing.credit_ledger (organization_id, entry_type, amount_micros, resource_type, metric_type, idempotency_key)
			VALUES ($1::uuid, 'debit', $2, $3, $4, 'sandbox-debit:' || $1 || ':' || $5::text)
			ON CONFLICT (idempotency_key) DO NOTHING`,
			organizationID, -delta, sandboxResource, sandboxCostMetric, cumulativeSandboxCost); err != nil {
			return Status{}, fmt.Errorf("debit sandbox cost: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO billing.usage_debit_watermark (organization_id, metric_type, billed_quantity)
			VALUES ($1::uuid, $2, $3)
			ON CONFLICT (organization_id, metric_type) DO UPDATE SET billed_quantity = EXCLUDED.billed_quantity, updated_at = NOW()`,
			organizationID, sandboxCostMetric, cumulativeSandboxCost); err != nil {
			return Status{}, fmt.Errorf("advance sandbox debit watermark: %w", err)
		}
	}

	// 3) Recompute the balance cache from the ledger (absolute, under the lock).
	var granted, balance int64
	if err := tx.GetContext(ctx, &granted, `
		SELECT COALESCE(SUM(amount_micros) FILTER (WHERE entry_type = 'grant'), 0)::BIGINT
		FROM billing.credit_ledger WHERE organization_id = $1::uuid`, organizationID); err != nil {
		return Status{}, err
	}
	if err := tx.GetContext(ctx, &balance, `
		SELECT COALESCE(SUM(amount_micros), 0)::BIGINT
		FROM billing.credit_ledger WHERE organization_id = $1::uuid`, organizationID); err != nil {
		return Status{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO billing.credit_balances (organization_id, balance_micros)
		VALUES ($1::uuid, $2)
		ON CONFLICT (organization_id) DO UPDATE SET balance_micros = EXCLUDED.balance_micros, updated_at = NOW()`,
		organizationID, balance); err != nil {
		return Status{}, err
	}

	// 4) Available = balance minus credit currently held.
	var held int64
	if err := tx.GetContext(ctx, &held, `
		SELECT COALESCE(SUM(amount_micros), 0)::BIGINT FROM billing.credit_holds
		WHERE organization_id = $1::uuid AND state = 'held'`, organizationID); err != nil {
		return Status{}, err
	}
	available := balance - held

	if err := tx.Commit(); err != nil {
		return Status{}, err
	}
	committed = true

	used := granted - available
	if used < 0 {
		used = 0
	}
	return Status{AmountMicros: granted, UsedMicros: used, RemainingMicros: available}, nil
}
