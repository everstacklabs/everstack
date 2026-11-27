// Package billingtrial owns the model trial: a fixed allowance of free model
// requests per organization under a global daily request ceiling
// (docs/design/free-trial-onboarding.md 3.2, 3.3, 4). In code it is the
// "model trial"; in product copy it is "free requests". It is not a plan and
// never touches plan_tier, the credit wallet or the $5 signup credit.
//
// The state lives in the billing schema (billing.model_trials,
// billing.model_trial_daily, billing.model_trial_settings,
// billing.model_trial_events). Like internal/billingcredit, this package is
// shared: the gateway's trial path (slice S3) calls Take and Refund on the
// billing database, the billing service reads Status for GetModelTrial, and
// the operations service calls UpdateSettings, Grant and Revoke for staff.
//
// Tenant isolation: every per-organization function takes the organization
// from its caller, which must have resolved it from the authenticated
// context (the gateway's billing org for the API key, the billing customer
// auth interceptor's membership check, or an audited staff action). Nothing
// here derives an organization from request data.
//
// Founder decision (2026-10-26): customers are never emailed or notified
// because the trial is capped, used up, expired or paused. Nothing in this
// package queues a notification; the ceiling is visible to ops only, through
// model_trial_daily.refused and capped_at.
package billingtrial

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

// Reason is why a take was refused.
type Reason string

const (
	// ReasonPaused: the trial is disabled in settings, the daily ceiling is
	// zero, or today's ceiling is reached.
	ReasonPaused Reason = "paused"
	// ReasonNotEligible: the organization has no trial row.
	ReasonNotEligible Reason = "not_eligible"
	// ReasonRevoked: ops revoked the organization's trial.
	ReasonRevoked Reason = "revoked"
	// ReasonExpired: the allowance is past its expiry.
	ReasonExpired Reason = "expired"
	// ReasonExhausted: every granted request is used.
	ReasonExhausted Reason = "exhausted"
	// ReasonRateLimited: the last request was less than the per-org interval
	// ago.
	ReasonRateLimited Reason = "rate_limited"
)

// RefusedError is returned by Take when nothing was taken.
type RefusedError struct {
	Reason Reason
	// RetryAfter is set for ReasonRateLimited: the time until the interval
	// ends.
	RetryAfter time.Duration
	// ResumesAt is set when the daily ceiling refused the take: the next UTC
	// midnight.
	ResumesAt time.Time
}

func (e *RefusedError) Error() string {
	return "model trial refused: " + string(e.Reason)
}

// ErrNotFound is returned by Revoke for an organization with no trial row.
var ErrNotFound = errors.New("model trial not found")

// MaxGrantRequests bounds a single ops grant so a typo cannot hand out a
// fortune of free requests.
const MaxGrantRequests = 1000

// Settings is the single billing.model_trial_settings row.
type Settings struct {
	Enabled              bool      `json:"enabled" db:"enabled"`
	Model                string    `json:"model" db:"model"`
	RequestsPerOrg       int       `json:"requests_per_org" db:"requests_per_org"`
	ValidityDays         int       `json:"validity_days" db:"validity_days"`
	MaxOutputTokens      int       `json:"max_output_tokens" db:"max_output_tokens"`
	MaxInputBytes        int       `json:"max_input_bytes" db:"max_input_bytes"`
	MinIntervalMS        int       `json:"min_interval_ms" db:"min_interval_ms"`
	DailyRequests        int       `json:"daily_requests" db:"daily_requests"`
	BackfillEmailsPerDay int       `json:"backfill_emails_per_day" db:"backfill_emails_per_day"`
	UpdatedBy            string    `json:"updated_by" db:"updated_by"`
	UpdatedAt            time.Time `json:"updated_at" db:"updated_at"`
}

const settingsColumns = `enabled, model, requests_per_org, validity_days, max_output_tokens,
	max_input_bytes, min_interval_ms, daily_requests, backfill_emails_per_day, updated_by, updated_at`

// LoadSettings reads the settings row. A missing row is an error: callers
// fail closed on any error.
func LoadSettings(ctx context.Context, q sqlx.QueryerContext) (Settings, error) {
	var s Settings
	if err := sqlx.GetContext(ctx, q, &s, `SELECT `+settingsColumns+` FROM billing.model_trial_settings WHERE id`); err != nil {
		return Settings{}, fmt.Errorf("read model trial settings: %w", err)
	}
	return s, nil
}

// Trial is one billing.model_trials row.
type Trial struct {
	OrganizationID  uuid.UUID  `json:"organization_id" db:"organization_id"`
	UserID          *uuid.UUID `json:"user_id,omitempty" db:"user_id"`
	GrantedRequests int        `json:"granted_requests" db:"granted_requests"`
	UsedRequests    int        `json:"used_requests" db:"used_requests"`
	Source          string     `json:"source" db:"source"`
	CreatedAt       time.Time  `json:"created_at" db:"created_at"`
	ExpiresAt       time.Time  `json:"expires_at" db:"expires_at"`
	FirstRequestAt  *time.Time `json:"first_request_at,omitempty" db:"first_request_at"`
	LastRequestAt   *time.Time `json:"last_request_at,omitempty" db:"last_request_at"`
	RevokedAt       *time.Time `json:"revoked_at,omitempty" db:"revoked_at"`
}

const trialColumns = `organization_id, user_id, granted_requests, used_requests, source, created_at,
	expires_at, first_request_at, last_request_at, revoked_at`

func loadTrial(ctx context.Context, q sqlx.QueryerContext, orgID uuid.UUID, forUpdate bool) (*Trial, error) {
	query := `SELECT ` + trialColumns + ` FROM billing.model_trials WHERE organization_id = $1`
	if forUpdate {
		query += ` FOR UPDATE`
	}
	var t Trial
	if err := sqlx.GetContext(ctx, q, &t, query, orgID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("read model trial: %w", err)
	}
	return &t, nil
}

// Daily is one billing.model_trial_daily row.
type Daily struct {
	Day          time.Time  `json:"day" db:"day"`
	CapRequests  int        `json:"cap_requests" db:"cap_requests"`
	UsedRequests int        `json:"used_requests" db:"used_requests"`
	Refused      int        `json:"refused" db:"refused"`
	CappedAt     *time.Time `json:"capped_at,omitempty" db:"capped_at"`
}

// DayOf is the UTC date a take at now counts against, as the DATE literal
// the daily table is keyed on.
func DayOf(now time.Time) string {
	return now.UTC().Format(time.DateOnly)
}

// nextUTCMidnight is when a day's ceiling resets.
func nextUTCMidnight(now time.Time) time.Time {
	u := now.UTC()
	return time.Date(u.Year(), u.Month(), u.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1)
}

// LoadDaily reads the ceiling row for the UTC day containing now. A day with
// no take yet has no row: the zero Daily (with Day set) is returned.
func LoadDaily(ctx context.Context, q sqlx.QueryerContext, now time.Time) (Daily, error) {
	var d Daily
	err := sqlx.GetContext(ctx, q, &d, `SELECT day, cap_requests, used_requests, refused, capped_at
		FROM billing.model_trial_daily WHERE day = $1::date`, DayOf(now))
	if errors.Is(err, sql.ErrNoRows) {
		day, _ := time.Parse(time.DateOnly, DayOf(now))
		return Daily{Day: day}, nil
	}
	if err != nil {
		return Daily{}, fmt.Errorf("read model trial day: %w", err)
	}
	return d, nil
}

func marshalJSON(v any) []byte {
	if v == nil {
		return nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	return b
}

func writeEvent(ctx context.Context, tx *sqlx.Tx, orgID *uuid.UUID, action string, before, after any, actor string, now time.Time) error {
	var b, a []byte
	if before != nil {
		b = marshalJSON(before)
	}
	if after != nil {
		a = marshalJSON(after)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO billing.model_trial_events (organization_id, action, before, after, actor, at)
		VALUES ($1, $2, $3::jsonb, $4::jsonb, $5, $6)`, orgID, action, nullJSON(b), nullJSON(a), actor, now)
	if err != nil {
		return fmt.Errorf("write model trial event: %w", err)
	}
	return nil
}

func nullJSON(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return string(b)
}

func cleanActor(actor string) (string, error) {
	actor = strings.TrimSpace(actor)
	if actor == "" {
		return "", errors.New("an actor is required")
	}
	return actor, nil
}
