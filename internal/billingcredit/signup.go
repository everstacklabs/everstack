package billingcredit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// SignupCreditTTL is how long the signup credit stays spendable after it is
// granted. Purchased credit and every other grant never expire.
const SignupCreditTTL = 30 * 24 * time.Hour

// SignupExpirySettleWindow is how long after a signup grant's expires_at the
// expiry waits before it is written. Debits land after the usage they charge:
// the usage-debit loop ticks every minute and the gateway's settle outbox
// retries a failed inference debit with up to 15 minutes of backoff.
// Inference debits carry their usage time and are attributed by it; for
// debits without one (compute), every debit written before expires_at +
// SignupExpirySettleWindow counts as spend of the signup credit, so a debit
// that lands late for usage before the expiry never draws on purchased
// credit. Compute usage in the window itself is also counted against the
// signup credit, which only ever lowers what expires.
const SignupExpirySettleWindow = 2 * time.Hour

// SignupGrantUserIdempotencyKey is the ledger key of a user's one-time signup
// grant.
func SignupGrantUserIdempotencyKey(userID string) string {
	return "signup-grant:user:" + userID
}

// SignupExpiryIdempotencyKey is the ledger key of the entry that expires the
// unspent part of one signup grant.
func SignupExpiryIdempotencyKey(grantEntryID string) string {
	return "signup-expiry:" + grantEntryID
}

// The signup credit belongs to a person. An organization's user is its
// primary owner: the owner membership that joined first (ties broken by user
// id). A co-owner invited later is not the org's user, so an invitation never
// spends or blocks the invitee's own credit.
//
// Who has received it is recorded in billing.signup_credit_claims, keyed
// org:<org>, user:<user> and email:<canonical email>
// (billing.canonical_email: lowercased, +tag dropped, Gmail dots dropped).
// The claims are written in the grant's transaction, never deleted, and
// carry no foreign keys, so the dedupe does not depend on which orgs the
// user owns today: deleting or handing away the granted org, or signing up
// again with a +alias, never earns a second grant. Grants made before the
// claims table existed were backfilled by its migration.

// signupCandidateSQL returns the user and canonical email that would receive
// the signup grant for organization $1, or no row when it must not be
// granted: the org already holds a signup grant, has no owner, is not the
// earliest-created org its user is primary owner of ("the first org they
// own"), or the user or their canonical email already holds a claim. Grants
// minted by a pre-claims build during a rolling deploy are also caught by the
// ownership check on the user's other primary-owned orgs.
const signupCandidateSQL = `
	WITH owner AS (
		SELECT m.user_id
		FROM everstack.organization_members m
		WHERE m.organization_id = $1::uuid AND m.role = 'owner'
		ORDER BY m.joined_at, m.user_id
		LIMIT 1
	), user_orgs AS (
		SELECT o.id, o.created_at
		FROM everstack.organizations o
		JOIN everstack.organization_members om ON om.organization_id = o.id AND om.role = 'owner'
		JOIN owner ON owner.user_id = om.user_id
		WHERE NOT EXISTS (
			SELECT 1 FROM everstack.organization_members e
			WHERE e.organization_id = o.id AND e.role = 'owner'
			  AND (e.joined_at, e.user_id) < (om.joined_at, om.user_id))
	), cand AS (
		SELECT owner.user_id, 'email:' || billing.canonical_email(u.email) AS email_key
		FROM owner JOIN everstack.users u ON u.id = owner.user_id
	)
	SELECT cand.user_id::text, cand.email_key
	FROM cand
	WHERE NOT EXISTS (
		SELECT 1 FROM billing.credit_ledger l
		WHERE l.organization_id = $1::uuid AND l.entry_type = 'grant' AND l.resource_type = $2)
	  AND NOT EXISTS (
		SELECT 1 FROM user_orgs uo
		JOIN billing.credit_ledger l ON l.organization_id = uo.id
		WHERE l.entry_type = 'grant' AND l.resource_type = $2)
	  AND $1::uuid = (SELECT uo.id FROM user_orgs uo ORDER BY uo.created_at, uo.id LIMIT 1)
	  AND NOT EXISTS (
		SELECT 1 FROM billing.signup_credit_claims c
		WHERE c.claim_key IN ('user:' || cand.user_id::text, cand.email_key, 'org:' || $1::text))`

// insertSignupClaimsSQL records the three claims. It runs without ON
// CONFLICT inside a savepoint: if a concurrent grant took any of the keys,
// the unique violation rolls the savepoint back and nothing is granted.
const insertSignupClaimsSQL = `
	INSERT INTO billing.signup_credit_claims (claim_key, user_id, organization_id, source)
	VALUES ('org:' || $1::text, $2::uuid, $1::uuid, 'grant'),
	       ('user:' || $2::text, $2::uuid, $1::uuid, 'grant'),
	       ($3, $2::uuid, $1::uuid, 'grant')`

// insertSignupGrantSQL appends the grant itself. It expires SignupCreditTTL
// after it is written.
const insertSignupGrantSQL = `
	INSERT INTO billing.credit_ledger (organization_id, entry_type, amount_micros, resource_type, idempotency_key, expires_at, metadata)
	VALUES ($1::uuid, 'grant', $2, $3, $4, $5::timestamptz + make_interval(secs => $6), jsonb_build_object('user_id', $7::text))
	ON CONFLICT (idempotency_key) DO NOTHING
	RETURNING amount_micros`

// expireSignupSQL appends, for organization $1, the expiry entry of every
// signup grant whose expires_at is at least SignupExpirySettleWindow in the
// past and that has not been expired yet. The amount is the grant's unspent
// remainder: spend is drawn from the signup credit first (it is the credit
// that expires soonest), so the remainder is the grant minus the debits and
// refunds for usage before expires_at. An inference debit records when its
// usage happened (metadata occurred_at, kept across settle-outbox replays),
// so it counts by that time however late it lands; a debit without one
// (compute, refunds) counts when it was written before expires_at + the
// settle window. Holds
// are not spend: they limit what can be spent, not what has expired. The
// amount is clamped to the ledger balance so expiry never reaches purchased
// credit or pushes the wallet negative. Grants without expires_at
// (purchases, staff grants, and signup grants made before the 30-day rule)
// are never touched.
const expireSignupSQL = `
	WITH bal AS (
		SELECT COALESCE(SUM(amount_micros), 0)::BIGINT AS micros
		FROM billing.credit_ledger WHERE organization_id = $1::uuid
	), due AS (
		SELECT g.id,
		       g.amount_micros + COALESCE((
				SELECT SUM(l.amount_micros) FROM billing.credit_ledger l
				WHERE l.organization_id = g.organization_id AND l.entry_type IN ('debit', 'refund')
				  AND l.created_at >= g.created_at
				  AND CASE WHEN l.metadata ? 'occurred_at'
				           THEN (l.metadata ->> 'occurred_at')::timestamptz < g.expires_at
				           ELSE l.created_at < g.expires_at + make_interval(secs => $5) END), 0) AS unspent
		FROM billing.credit_ledger g
		WHERE g.organization_id = $1::uuid AND g.entry_type = 'grant' AND g.resource_type = $2
		  AND g.expires_at IS NOT NULL
		  AND g.expires_at + make_interval(secs => $5) <= $3::timestamptz
		  AND NOT EXISTS (
			SELECT 1 FROM billing.credit_ledger e WHERE e.idempotency_key = $4 || g.id::text)
	)
	INSERT INTO billing.credit_ledger (organization_id, entry_type, amount_micros, resource_type, idempotency_key, metadata)
	SELECT $1::uuid, 'expiry', -GREATEST(0, LEAST(due.unspent, bal.micros)),
	       $2, $4 || due.id::text, jsonb_build_object('grant_id', due.id::text)
	FROM due, bal
	ON CONFLICT (idempotency_key) DO NOTHING
	RETURNING amount_micros`

// syncSignupCreditTx expires a due signup grant and then ensures the signup
// grant, for one organization, inside tx. The caller must hold the org's
// advisory lock. It returns the net ledger change it wrote (grant minus
// expiry) so callers that maintain the balance cache incrementally can apply
// it. Expiry runs first so a debit admitted after expiry can never draw on
// expired credit.
func syncSignupCreditTx(ctx context.Context, tx *sqlx.Tx, organizationID string, grantMicros int64, now time.Time) (granted bool, delta int64, err error) {
	expired, err := sumReturned(ctx, tx, expireSignupSQL, organizationID, SignupGrantResourceType, now,
		"signup-expiry:", SignupExpirySettleWindow.Seconds())
	if err != nil {
		return false, 0, fmt.Errorf("expire signup credit: %w", err)
	}
	delta += expired
	if grantMicros <= 0 {
		return false, delta, nil
	}
	added, err := grantSignupTx(ctx, tx, organizationID, grantMicros, now)
	if err != nil {
		return false, 0, fmt.Errorf("grant signup credit: %w", err)
	}
	return added != 0, delta + added, nil
}

// grantSignupTx writes the claims and the grant for organization, or nothing
// when it is not eligible or a concurrent grant claimed one of its keys.
func grantSignupTx(ctx context.Context, tx *sqlx.Tx, organizationID string, grantMicros int64, now time.Time) (int64, error) {
	var cand struct {
		UserID   string `db:"user_id"`
		EmailKey string `db:"email_key"`
	}
	// The savepoint keeps the caller's transaction usable when the claim
	// step must be abandoned: a concurrent grant took a key, or the claims
	// migration has not run yet on this database.
	if _, err := tx.ExecContext(ctx, `SAVEPOINT signup_claims`); err != nil {
		return 0, err
	}
	abandon := func() (int64, error) {
		if _, err := tx.ExecContext(ctx, `ROLLBACK TO SAVEPOINT signup_claims`); err != nil {
			return 0, err
		}
		return 0, nil
	}
	err := tx.QueryRowxContext(ctx, signupCandidateSQL, organizationID, SignupGrantResourceType).Scan(&cand.UserID, &cand.EmailKey)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT signup_claims`); err != nil {
			return 0, err
		}
		return 0, nil
	case isMissingClaimsSchema(err):
		// A gateway or service running ahead of the services migration that
		// creates billing.signup_credit_claims and billing.canonical_email.
		// Skip only the grant: expiry already ran, and admission must still
		// be decided on the wallet.
		warnMissingClaimsSchema(err)
		return abandon()
	case err != nil:
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, insertSignupClaimsSQL, organizationID, cand.UserID, cand.EmailKey); err != nil {
		if isUniqueViolation(err) {
			return abandon() // a concurrent grant claimed the user, email or org first
		}
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, `RELEASE SAVEPOINT signup_claims`); err != nil {
		return 0, err
	}
	return sumReturned(ctx, tx, insertSignupGrantSQL, organizationID, grantMicros, SignupGrantResourceType,
		SignupGrantUserIdempotencyKey(cand.UserID), now, SignupCreditTTL.Seconds(), cand.UserID)
}

// isMissingClaimsSchema reports undefined_table or undefined_function: the
// claims migration has not run on this database yet.
func isMissingClaimsSchema(err error) bool {
	var coded interface{ SQLState() string }
	if !errors.As(err, &coded) {
		return false
	}
	switch coded.SQLState() {
	case "42P01", "42883":
		return true
	}
	return false
}

var missingClaimsWarned atomic.Bool

// warnMissingClaimsSchema logs the skipped grant once per process.
func warnMissingClaimsSchema(err error) {
	if missingClaimsWarned.CompareAndSwap(false, true) {
		logger.WithError(err).Warn("billingcredit: signup credit claims schema missing; skipping signup grants until the billing migration runs")
	}
}

// isUniqueViolation reports a Postgres unique_violation from either driver
// in use (pgx and lib/pq both expose SQLState).
func isUniqueViolation(err error) bool {
	var coded interface{ SQLState() string }
	return errors.As(err, &coded) && coded.SQLState() == "23505"
}

func sumReturned(ctx context.Context, tx *sqlx.Tx, query string, args ...any) (int64, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var total int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return 0, err
		}
		total += v
	}
	return total, rows.Err()
}

// SyncSignupCredit expires a due signup grant and ensures the signup grant
// for one organization, under the org advisory lock every wallet write takes,
// and keeps the balance cache in step. It returns whether a grant was newly
// written. Safe to call from any grant site, any number of times.
func SyncSignupCredit(ctx context.Context, db *sqlx.DB, organizationID string, now time.Time) (bool, error) {
	if db == nil || strings.TrimSpace(organizationID) == "" {
		return false, errors.New("billing database and organization are required")
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, organizationID); err != nil {
		return false, err
	}
	granted, delta, err := syncSignupCreditTx(ctx, tx, organizationID, StarterCreditMicros(), now)
	if err != nil {
		return false, err
	}
	if delta != 0 {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO billing.credit_balances (organization_id, balance_micros, updated_at)
			VALUES ($1::uuid, $2, NOW())
			ON CONFLICT (organization_id) DO UPDATE
			SET balance_micros = billing.credit_balances.balance_micros + EXCLUDED.balance_micros, updated_at = NOW()`,
			organizationID, delta); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	committed = true
	return granted, nil
}

// ListOrganizationsWithDueSignupWork returns the organizations the sweep must
// visit: each user's first primary-owned organization while neither the user
// nor their canonical email holds a claim, and every organization holding a
// signup grant whose expiry (plus the settle window) has passed with no
// expiry entry yet. The per-org statements re-check all of it under the org
// lock; this only narrows what the sweep locks.
func ListOrganizationsWithDueSignupWork(ctx context.Context, db *sqlx.DB, now time.Time) ([]string, error) {
	var ids []string
	err := db.SelectContext(ctx, &ids, `
		WITH po AS (
			SELECT DISTINCT ON (organization_id) organization_id, user_id
			FROM everstack.organization_members
			WHERE role = 'owner'
			ORDER BY organization_id, joined_at, user_id
		), first_org AS (
			SELECT DISTINCT ON (po.user_id) po.user_id, o.id
			FROM po JOIN everstack.organizations o ON o.id = po.organization_id
			ORDER BY po.user_id, o.created_at, o.id
		)
		SELECT f.id::text
		FROM first_org f JOIN everstack.users u ON u.id = f.user_id
		WHERE NOT EXISTS (
			SELECT 1 FROM billing.signup_credit_claims c
			WHERE c.claim_key IN ('user:' || f.user_id::text, 'email:' || billing.canonical_email(u.email)))
		  AND NOT EXISTS (
			SELECT 1 FROM po
			JOIN billing.credit_ledger l ON l.organization_id = po.organization_id
			WHERE po.user_id = f.user_id AND l.entry_type = 'grant' AND l.resource_type = $1)
		UNION
		SELECT g.organization_id::text
		FROM billing.credit_ledger g
		WHERE g.entry_type = 'grant' AND g.resource_type = $1
		  AND g.expires_at IS NOT NULL
		  AND g.expires_at + make_interval(secs => $4) <= $2::timestamptz
		  AND NOT EXISTS (SELECT 1 FROM billing.credit_ledger e WHERE e.idempotency_key = $3 || g.id::text)`,
		SignupGrantResourceType, now, "signup-expiry:", SignupExpirySettleWindow.Seconds())
	return ids, err
}

// reconcileSignupClaimsSQL inserts the claims of every signup grant that has
// none: grants minted by a build without the claims table (a gateway or
// service pod still on the old version during a rollout, which grants per
// org). The same attribution as the claims migration's backfill: the user
// the grant names, else the org's current primary owner; an org that no
// longer exists keeps only its org claim. Idempotent.
const reconcileSignupClaimsSQL = `
	WITH grants AS (
		SELECT g.organization_id, g.created_at,
		       COALESCE(
		           CASE WHEN g.idempotency_key LIKE 'signup-grant:user:%'
		                THEN substring(g.idempotency_key FROM 19) END,
		           NULLIF(g.metadata ->> 'user_id', ''),
		           (SELECT m.user_id::text
		            FROM everstack.organization_members AS m
		            WHERE m.organization_id = g.organization_id AND m.role = 'owner'
		            ORDER BY m.joined_at, m.user_id
		            LIMIT 1)
		       ) AS user_ref
		FROM billing.credit_ledger AS g
		WHERE g.entry_type = 'grant' AND g.resource_type = $1
		  AND NOT EXISTS (
			SELECT 1 FROM billing.signup_credit_claims AS c
			WHERE c.claim_key = 'org:' || g.organization_id::text)
	), resolved AS (
		SELECT gr.organization_id, gr.created_at, u.id AS user_id, u.email
		FROM grants AS gr
		LEFT JOIN everstack.users AS u ON u.id::text = gr.user_ref
	)
	INSERT INTO billing.signup_credit_claims (claim_key, user_id, organization_id, source, created_at)
	SELECT k.claim_key, r.user_id, r.organization_id, 'backfill', r.created_at
	FROM resolved AS r
	CROSS JOIN LATERAL (VALUES
		('org:' || r.organization_id::text),
		(CASE WHEN r.user_id IS NOT NULL THEN 'user:' || r.user_id::text END),
		(CASE WHEN r.email IS NOT NULL THEN 'email:' || billing.canonical_email(r.email) END)
	) AS k(claim_key)
	WHERE k.claim_key IS NOT NULL
	ORDER BY r.created_at
	ON CONFLICT (claim_key) DO NOTHING`

// ReconcileSignupClaims records the claims of every signup grant that lacks
// them and returns how many claims it inserted. The signup sweep runs it
// before granting so a user granted by an old pod during a rollout is never
// granted again.
func ReconcileSignupClaims(ctx context.Context, db *sqlx.DB) (int64, error) {
	if db == nil {
		return 0, errors.New("billing database is required")
	}
	res, err := db.ExecContext(ctx, reconcileSignupClaimsSQL, SignupGrantResourceType)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
