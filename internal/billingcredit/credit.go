// Package billingcredit owns the managed-cloud signup-credit policy and the
// compute admission gate. The $5 signup credit is a grant into the fungible
// usage-credit wallet (billing.credit_ledger), NOT a sandbox-only credit: it is
// spendable on sandbox compute, browser runtime and ingested data. It never
// pays for inference (founder decision, docs/design/free-trial-onboarding.md
// section 0 item 3 and 3.4): the platform key pool is closed today, so no
// organization is served on an Everstack key, and the free model trial
// (internal/billingtrial) has its own request allowance and never debits the
// wallet. When prepaid-credits-v2 S5 opens the pool, it must make signup
// grant entries unspendable on inference. This package grants the signup
// credit (idempotently, once per user, to the first organization that user
// owns), expires its unspent remainder 30 days after the grant, and gates
// compute on the wallet balance.
//
// It does NOT debit. Every wallet debit for background meters is produced by
// one place, the billing service's usage-debit loop
// (services/billing/internal/service/usage_debit.go), so there is a single
// charging mechanism with one watermark scheme. A subscription does not
// bypass the wallet: Cloud plans include their limits and a monthly compute
// allowance (internal/billingallowance), and credit pays only for usage beyond
// them (see Resolve). The plan allowance, the wallet, an Enterprise contract
// or the internal organization flag are the only things that decide whether
// compute may be allocated.
package billingcredit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/billingallowance"
	"github.com/everstacklabs/everstack/internal/billingidentity"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/pkg/plans"
	"github.com/jmoiron/sqlx"
)

const (
	// SignupGrantResourceType tags the one-time fungible signup credit in the
	// ledger. Exported so every grant site grants it identically.
	SignupGrantResourceType = "signup"
)

// SignupGrantIdempotencyKey is the ledger key of a signup grant made under the
// old per-organization rule. New grants are keyed per user
// (SignupGrantUserIdempotencyKey); this remains so existing rows stay
// recognizable. Those grants keep their credit and never expire.
func SignupGrantIdempotencyKey(organizationID string) string {
	return "signup-grant:" + organizationID
}

// Status describes an organization's wallet, surfaced to the UI. Remaining is
// the spendable wallet balance (grants and purchases minus every debit and
// hold), not a sandbox-specific figure.
type Status struct {
	AmountMicros    int64
	UsedMicros      int64
	RemainingMicros int64
}

// Access describes whether managed compute (sandboxes, hosted browsers) may be
// allocated. Allowed is the sandbox decision and BrowserAllowed the hosted
// browser decision; each resource has its own plan allowance. BillingConfigured
// and BillingActive describe the legacy subscription row for display only;
// they never decide admission. Exempt marks an internal organization
// (billingidentity.OrganizationExempt), admitted whatever its wallet holds.
// Allowances is the current period's compute allowance per resource, empty
// when it could not be read or for Enterprise.
type Access struct {
	Allowed           bool
	BrowserAllowed    bool
	BillingConfigured bool
	BillingActive     bool
	Credit            Status
	Exempt            bool
	Allowances        []billingallowance.Status
}

// Allowance returns the access's allowance status for one counter.
func (a Access) Allowance(c billingallowance.Counter) (billingallowance.Status, bool) {
	for _, s := range a.Allowances {
		if s.Metric == c.Metric {
			return s, true
		}
	}
	return billingallowance.Status{}, false
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

// Resolve returns the managed compute access policy for one organization.
// Enterprise contracts bypass public billing. Everyone else is gated on the
// plan allowance first and the wallet second: a due signup grant is expired
// and the user's one-time signup grant is ensured (so a user's first
// organization is never refused on an empty wallet it was owed; a later org of
// the same user starts empty), then each resource is admitted while its plan
// allowance for the period has room or the spendable balance is positive.
// Compute cost is debited by the billing service's usage-debit loop, never
// here; that loop charges only usage beyond the allowance, reading the
// allowance through the same package (internal/billingallowance) and the same
// columns, so an organization admitted on its allowance is not charged for it.
// Hard caps such as CONCURRENT_SANDBOXES and SANDBOX_MEMORY_MB are enforced
// elsewhere and credit cannot buy past them.
//
// When the allowance runs out mid-run and there is no credit, the next
// decision refuses, and the shared reporter stops running compute exactly as
// it does when credit runs out. A failed allowance read is logged and treated
// as no allowance, so an outage falls back to the wallet decision.
//
// Internal organizations (the organization's internal flag, settable only by
// migration or ops tooling) are admitted without credit. Their wallet is
// still read and reported, and their compute is still debited from it.
func Resolve(ctx context.Context, db *sqlx.DB, organizationID, tier string) (Access, error) {
	return ResolveAt(ctx, db, organizationID, tier, time.Now())
}

// ResolveAt is Resolve with the instant that places the organization in an
// allowance period.
func ResolveAt(ctx context.Context, db *sqlx.DB, organizationID, tier string, now time.Time) (Access, error) {
	if db == nil || strings.TrimSpace(organizationID) == "" {
		return Access{}, fmt.Errorf("sandbox billing database and organization are required")
	}
	if strings.EqualFold(strings.TrimSpace(tier), "enterprise") {
		return Access{Allowed: true, BrowserAllowed: true}, nil
	}

	// The subscription row is display-only here (the Cloud UI shows whether
	// Stripe billing is configured and live). It does not gate compute
	// directly: a paid plan's included credit is granted into the wallet for
	// each period once that period's invoice is paid (by the billing
	// service's invoice.paid handler, with the hourly grant loop as the
	// backstop; never on a subscription status alone), and top-ups add
	// to the same wallet, so the balance already reflects what a subscriber
	// paid for. A subscribed organization's compute is debited from that
	// wallet like everyone else's.
	var subscription struct {
		Status               string `db:"status"`
		StripeCustomerID     string `db:"stripe_customer_id"`
		StripeSubscriptionID string `db:"stripe_subscription_id"`
	}
	access := Access{}
	err := db.GetContext(ctx, &subscription, `
		SELECT status, stripe_customer_id, stripe_subscription_id
		FROM billing.subscriptions
		WHERE organization_id = $1`, organizationID)
	switch {
	case err == nil:
		access.BillingConfigured = true
		access.BillingActive = (strings.EqualFold(subscription.Status, "active") ||
			strings.EqualFold(subscription.Status, "trialing")) &&
			strings.TrimSpace(subscription.StripeCustomerID) != "" &&
			strings.TrimSpace(subscription.StripeSubscriptionID) != ""
	case errors.Is(err, sql.ErrNoRows):
		// No subscription row: nothing to display.
	default:
		return Access{}, fmt.Errorf("resolve sandbox billing subscription: %w", err)
	}

	credit, err := ensureWalletCredit(ctx, db, organizationID, StarterCreditMicros())
	if err != nil {
		return Access{}, err
	}
	access.Credit = credit
	access.Exempt = organizationExempt(ctx, db, organizationID)
	access.Allowances = allowanceStatus(ctx, db, organizationID, now)
	hasCredit := credit.RemainingMicros > 0
	access.Allowed = access.Exempt || hasCredit || allowanceLeft(access, billingallowance.Sandbox)
	access.BrowserAllowed = access.Exempt || hasCredit || allowanceLeft(access, billingallowance.Browser)
	return access, nil
}

// allowanceStatus reads the current period's compute allowances. A failed
// read is logged and yields none, so admission falls back to the wallet.
func allowanceStatus(ctx context.Context, db *sqlx.DB, organizationID string, now time.Time) []billingallowance.Status {
	statuses, err := billingallowance.ReadStatus(ctx, db, organizationID, now)
	if err != nil {
		logger.WithFields("organization_id", organizationID, "error", err.Error()).
			Warn("billingcredit: compute allowance lookup failed; deciding compute admission on the wallet")
		return nil
	}
	return statuses
}

// allowanceLeft reports whether every allowance that resource draws on has
// room: a sandbox draws on both its vCPU-hour and GiB-hour allowances, so it
// is admitted on allowance only while neither is used up.
func allowanceLeft(access Access, r billingallowance.Resource) bool {
	found := false
	for _, c := range billingallowance.Counters {
		if c.Resource != r {
			continue
		}
		s, ok := access.Allowance(c)
		if !ok || s.Exhausted() {
			return false
		}
		found = true
	}
	return found
}

// organizationExempt reads the internal-organization flag. A failed read is
// logged and treated as not exempt, so an outage falls back to the wallet
// decision rather than admitting or refusing anyone on its own.
func organizationExempt(ctx context.Context, db *sqlx.DB, organizationID string) bool {
	exempt, err := billingidentity.OrganizationExempt(ctx, db, organizationID)
	if err != nil {
		logger.WithFields("organization_id", organizationID, "error", err.Error()).
			Warn("billingcredit: internal organization lookup failed; deciding compute admission on the wallet")
		return false
	}
	return exempt
}

// ensureWalletCredit expires a due signup grant, ensures the one-time signup
// grant (per user, see grantSignupSQL), and returns the wallet status, in one transaction under the org advisory lock
// (the same lock the billing service's wallet writes take), so a concurrent
// debit or grant cannot race the balance. It never debits.
func ensureWalletCredit(ctx context.Context, db *sqlx.DB, organizationID string, grantMicros int64) (Status, error) {
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

	// 1) Expire a due signup grant, then ensure the one-time signup grant
	// (once per user, to that user's first owned org). The balance cache is
	// recomputed absolutely below, so the returned delta is not needed.
	if _, _, err := syncSignupCreditTx(ctx, tx, organizationID, grantMicros, time.Now()); err != nil {
		return Status{}, err
	}

	// 2) Recompute the balance cache from the ledger (absolute, under the lock).
	// "granted" is the signup credit alone: expired subscription grants must
	// not inflate the starter-credit figure the UI shows.
	var granted, balance int64
	if err := tx.GetContext(ctx, &granted, `
		SELECT COALESCE(SUM(amount_micros) FILTER (WHERE entry_type = 'grant' AND resource_type = $2), 0)::BIGINT
		FROM billing.credit_ledger WHERE organization_id = $1::uuid`, organizationID, SignupGrantResourceType); err != nil {
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

	// 3) Available = balance minus credit currently held.
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
