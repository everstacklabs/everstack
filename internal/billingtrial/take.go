package billingtrial

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// The take-one statements (docs/design/free-trial-onboarding.md 3.3), run in
// one READ COMMITTED transaction in this order: settings, an unlocked read
// of the day (refuse early when full), the organization's row, the day's
// row, then the take record. Takes and refunds never deadlock (see Refund). Under READ COMMITTED a conditional UPDATE (and an
// ON CONFLICT DO UPDATE ... WHERE) re-checks its WHERE against the latest
// committed row after waiting for the row lock, so parallel takes can never
// push either count past its limit.

// takeSettingsSQL reads the live limits. A plain read: settings writes never
// block takes, and a change applies to the next take.
const takeSettingsSQL = `SELECT enabled, daily_requests, min_interval_ms
FROM billing.model_trial_settings WHERE id`

// takeOrgSQL takes one request from the organization: not revoked, not
// expired, below its allowance, and at least min_interval since its last
// request. $1 organization, $2 now, $3 min_interval_ms. An interval of 0
// disables the interval check outright (otherwise equal timestamps under
// contention would refuse).
const takeOrgSQL = `UPDATE billing.model_trials
SET used_requests = used_requests + 1,
    last_request_at = $2::timestamptz,
    first_request_at = COALESCE(first_request_at, $2::timestamptz)
WHERE organization_id = $1
  AND revoked_at IS NULL
  AND expires_at > $2::timestamptz
  AND used_requests < granted_requests
  AND ($3::int = 0 OR last_request_at IS NULL
       OR last_request_at < $2::timestamptz - $3::int * INTERVAL '1 millisecond')
RETURNING used_requests, granted_requests`

// takeDaySQL takes one request from the day's global count. The cap compared
// is the live setting ($2), not the stored cap_requests, so lowering
// daily_requests (to 0 included) applies on the next take. The INSERT branch
// is reached only when $2 > 0 (checked before this runs). $1 day, $2 cap,
// $3 now.
const takeDaySQL = `INSERT INTO billing.model_trial_daily (day, cap_requests, used_requests, capped_at)
VALUES ($1::date, $2::int, 1, CASE WHEN 1 >= $2::int THEN $3::timestamptz END)
ON CONFLICT (day) DO UPDATE
SET used_requests = billing.model_trial_daily.used_requests + 1,
    cap_requests = EXCLUDED.cap_requests,
    capped_at = CASE WHEN billing.model_trial_daily.used_requests + 1 >= EXCLUDED.cap_requests
                     THEN COALESCE(billing.model_trial_daily.capped_at, $3::timestamptz)
                     ELSE billing.model_trial_daily.capped_at END
WHERE billing.model_trial_daily.used_requests < EXCLUDED.cap_requests
RETURNING used_requests`

// dayFullSQL is the unlocked pre-check: is the day already at the live cap?
// A full day is refused here, before the organization's row is touched, so
// a full day costs one read per attempt and no write. The locked
// conditional insert (takeDaySQL) stays the authority; this read only
// short-circuits. $1 day, $2 cap.
const dayFullSQL = `SELECT EXISTS (SELECT 1 FROM billing.model_trial_daily
WHERE day = $1::date AND used_requests >= $2::int)`

// dayRefusedSQL counts a take the locked insert refused: the read above saw
// room, but the day filled before this take got the row lock. Bounded by
// the requests in flight at the moment the day fills, so it is never a hot
// write. Ops metric only; no customer notification (founder decision
// 2026-10-26).
const dayRefusedSQL = `UPDATE billing.model_trial_daily
SET refused = refused + 1, capped_at = COALESCE(capped_at, $2::timestamptz)
WHERE day = $1::date`

// takeRecordSQL records the take, binding a later refund to it.
const takeRecordSQL = `INSERT INTO billing.model_trial_takes (organization_id, day, taken_at)
VALUES ($1, $2::date, $3::timestamptz) RETURNING id`

// TakeResult is a successful take.
type TakeResult struct {
	// ID names the take; Refund takes it.
	ID uuid.UUID
	// Day is the UTC date the request counts against.
	Day     string
	Used    int
	Granted int
	// DayUsed is the day's global count after this take, for the gateway's
	// ops gauge.
	DayUsed int
}

// Remaining is the organization's requests left after this take.
func (r TakeResult) Remaining() int {
	if r.Granted <= r.Used {
		return 0
	}
	return r.Granted - r.Used
}

// Take takes one free request for orgID at now, from the organization's
// allowance and from the day's global ceiling, atomically: either both counts
// move or neither does. A refusal is a *RefusedError naming the reason; any
// other error means the database could not be asked, and the caller must
// refuse the request (fail closed).
//
// orgID must come from the authenticated context (the gateway's resolved
// billing org), never from request data.
func Take(ctx context.Context, db *sqlx.DB, orgID uuid.UUID, now time.Time) (TakeResult, error) {
	if db == nil {
		return TakeResult{}, errors.New("billing database is not configured")
	}
	if orgID == uuid.Nil {
		return TakeResult{}, &RefusedError{Reason: ReasonNotEligible}
	}
	now = now.UTC().Truncate(time.Microsecond)
	day := DayOf(now)

	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return TakeResult{}, err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	var enabled bool
	var dailyCap, minIntervalMS int
	if err := tx.QueryRowxContext(ctx, takeSettingsSQL).Scan(&enabled, &dailyCap, &minIntervalMS); err != nil {
		return TakeResult{}, fmt.Errorf("read model trial settings: %w", err)
	}
	if !enabled || dailyCap <= 0 {
		return TakeResult{}, &RefusedError{Reason: ReasonPaused}
	}

	var full bool
	if err := tx.QueryRowxContext(ctx, dayFullSQL, day, dailyCap).Scan(&full); err != nil {
		return TakeResult{}, fmt.Errorf("read model trial day: %w", err)
	}
	if full {
		return TakeResult{}, &RefusedError{Reason: ReasonPaused, ResumesAt: nextUTCMidnight(now)}
	}

	var res TakeResult
	err = tx.QueryRowxContext(ctx, takeOrgSQL, orgID, now, minIntervalMS).Scan(&res.Used, &res.Granted)
	if err != nil {
		if !isNoRows(err) {
			return TakeResult{}, fmt.Errorf("take model trial request: %w", err)
		}
		_ = tx.Rollback()
		committed = true
		return TakeResult{}, classifyOrgMiss(ctx, db, orgID, now, minIntervalMS)
	}

	var dayUsed int
	err = tx.QueryRowxContext(ctx, takeDaySQL, day, dailyCap, now).Scan(&dayUsed)
	if err != nil {
		if !isNoRows(err) {
			return TakeResult{}, fmt.Errorf("take model trial daily request: %w", err)
		}
		// The ceiling refused: roll back the organization's take, then
		// count the refusal for ops.
		_ = tx.Rollback()
		committed = true
		_, _ = db.ExecContext(ctx, dayRefusedSQL, day, now)
		return TakeResult{}, &RefusedError{Reason: ReasonPaused, ResumesAt: nextUTCMidnight(now)}
	}

	if err := tx.QueryRowxContext(ctx, takeRecordSQL, orgID, day, now).Scan(&res.ID); err != nil {
		return TakeResult{}, fmt.Errorf("record model trial take: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return TakeResult{}, fmt.Errorf("commit model trial take: %w", err)
	}
	committed = true
	res.Day = day
	res.DayUsed = dayUsed
	return res, nil
}

// classifyOrgMiss reads why the organization's take matched no row. It runs
// after the rollback, so it may see a later state than the take did; it
// only names the refusal, nothing was taken either way.
func classifyOrgMiss(ctx context.Context, db *sqlx.DB, orgID uuid.UUID, now time.Time, minIntervalMS int) error {
	t, err := loadTrial(ctx, db, orgID, false)
	if err != nil {
		return err
	}
	return classifyTrial(t, now, minIntervalMS)
}

func classifyTrial(t *Trial, now time.Time, minIntervalMS int) *RefusedError {
	switch {
	case t == nil:
		return &RefusedError{Reason: ReasonNotEligible}
	case t.RevokedAt != nil:
		return &RefusedError{Reason: ReasonRevoked}
	case !t.ExpiresAt.After(now):
		return &RefusedError{Reason: ReasonExpired}
	case t.UsedRequests >= t.GrantedRequests:
		return &RefusedError{Reason: ReasonExhausted}
	}
	interval := time.Duration(minIntervalMS) * time.Millisecond
	retry := time.Second
	if t.LastRequestAt != nil {
		if wait := t.LastRequestAt.Add(interval).Sub(now); wait > 0 {
			retry = wait
		}
	}
	return &RefusedError{Reason: ReasonRateLimited, RetryAfter: retry}
}

// ErrTakeNotFound is returned by Refund for a take id that does not exist
// or belongs to another organization.
var ErrTakeNotFound = errors.New("model trial take not found")

// Refund returns the request taken by takeID to orgID and to the day it
// counted against. The caller refunds only when the provider answered with
// an error before any output (docs/design/free-trial-onboarding.md 3.3),
// inline; a failed refund is logged by the caller and the request stays
// counted, which fails safe.
//
// It is bound to the take: a take of another organization is refused
// (ErrTakeNotFound), and refunding a take twice is a no-op that returns
// false. Both counters move together or neither does: if the organization
// row or the day row cannot be decremented, the whole refund rolls back
// with an error. Lock order is take row, organization row, day row; Take
// locks organization then day and only inserts a new take row, so the two
// never deadlock. last_request_at is kept, so forced errors cannot bypass
// the per-org interval.
func Refund(ctx context.Context, db *sqlx.DB, orgID, takeID uuid.UUID, now time.Time) (bool, error) {
	if db == nil {
		return false, errors.New("billing database is not configured")
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

	var day string
	err = tx.QueryRowxContext(ctx, `UPDATE billing.model_trial_takes SET refunded_at = $3::timestamptz
		WHERE id = $1 AND organization_id = $2 AND refunded_at IS NULL
		RETURNING to_char(day, 'YYYY-MM-DD')`, takeID, orgID, now.UTC().Truncate(time.Microsecond)).Scan(&day)
	if isNoRows(err) {
		var refunded bool
		err := tx.GetContext(ctx, &refunded, `SELECT TRUE FROM billing.model_trial_takes
			WHERE id = $1 AND organization_id = $2`, takeID, orgID)
		if isNoRows(err) {
			return false, ErrTakeNotFound
		}
		if err != nil {
			return false, fmt.Errorf("read model trial take: %w", err)
		}
		return false, nil // already refunded
	}
	if err != nil {
		return false, fmt.Errorf("refund model trial take: %w", err)
	}

	org, err := tx.ExecContext(ctx, `UPDATE billing.model_trials SET used_requests = used_requests - 1
		WHERE organization_id = $1 AND used_requests > 0`, orgID)
	if err != nil {
		return false, fmt.Errorf("refund model trial request: %w", err)
	}
	if n, err := org.RowsAffected(); err != nil || n != 1 {
		return false, errors.New("refund model trial: organization count out of step, nothing refunded")
	}
	dayRes, err := tx.ExecContext(ctx, `UPDATE billing.model_trial_daily SET used_requests = used_requests - 1
		WHERE day = $1::date AND used_requests > 0`, day)
	if err != nil {
		return false, fmt.Errorf("refund model trial daily request: %w", err)
	}
	if n, err := dayRes.RowsAffected(); err != nil || n != 1 {
		return false, errors.New("refund model trial: daily count out of step, nothing refunded")
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit model trial refund: %w", err)
	}
	committed = true
	return true, nil
}

// GrantFromSignupClaim creates orgID's trial row when the organization holds
// a person's signup credit claim (org:<id>, any source), the eligibility
// rule of docs/design/free-trial-onboarding.md 3.3: one trial per user, per
// canonical email, on the person's first owned organization. The caller
// (the gateway trial path, S3) runs billingcredit.SyncSignupCredit first so
// the claims exist. Idempotent: an existing row is left untouched.
// Returns whether the organization has a trial row afterwards.
func GrantFromSignupClaim(ctx context.Context, db *sqlx.DB, orgID uuid.UUID, now time.Time) (bool, error) {
	if db == nil {
		return false, errors.New("billing database is not configured")
	}
	_, err := db.ExecContext(ctx, `INSERT INTO billing.model_trials
			(organization_id, user_id, granted_requests, source, created_at, expires_at)
		SELECT c.organization_id, c.user_id, s.requests_per_org, 'signup', $2::timestamptz,
		       $2::timestamptz + s.validity_days * INTERVAL '1 day'
		FROM billing.signup_credit_claims c, billing.model_trial_settings s
		WHERE c.claim_key = 'org:' || $1::text AND c.organization_id = $3::uuid AND s.id
		ON CONFLICT (organization_id) DO NOTHING`, orgID.String(), now.UTC(), orgID)
	if err != nil {
		return false, fmt.Errorf("grant model trial from signup claim: %w", err)
	}
	t, err := loadTrial(ctx, db, orgID, false)
	if err != nil {
		return false, err
	}
	return t != nil, nil
}

func isNoRows(err error) bool {
	return err != nil && errors.Is(err, sql.ErrNoRows)
}
