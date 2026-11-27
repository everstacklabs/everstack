package billingtrial

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Staff operations (docs/design/free-trial-onboarding.md 4.2). Each change
// writes a billing.model_trial_events row with the state before and after, in
// the same transaction. Authorization is the caller's: the operations service
// admits only audited staff with the grant-credit permission.

// SettingsPatch changes some settings; nil fields are kept.
type SettingsPatch struct {
	Enabled              *bool
	Model                *string
	RequestsPerOrg       *int
	ValidityDays         *int
	MaxOutputTokens      *int
	MaxInputBytes        *int
	MinIntervalMS        *int
	DailyRequests        *int
	BackfillEmailsPerDay *int
}

// Settings maxima, kept near the budget arithmetic of
// docs/design/free-trial-onboarding.md 3.2 ($60/month project, 430 a day at
// a $0.00435 worst case). Raising one past these is a code change and a
// fresh look at the arithmetic, not a console edit.
const (
	MaxDailyRequestsLimit = 5000
	MaxOutputTokensLimit  = 2000
	MaxInputBytesLimit    = 16000
)

// AllowedModels are the models free requests may be served on. The budget
// arithmetic is priced on gpt-5.4-mini; adding a model means redoing it.
var AllowedModels = map[string]struct{}{
	"gpt-5.4-mini": {},
}

// ErrInvalidSettings wraps a settings value the patch may not set.
var ErrInvalidSettings = errors.New("invalid model trial settings")

func (p SettingsPatch) apply(s Settings) (Settings, error) {
	bad := func(msg string) (Settings, error) { return Settings{}, fmt.Errorf("%w: %s", ErrInvalidSettings, msg) }
	if p.Enabled != nil {
		s.Enabled = *p.Enabled
	}
	if p.Model != nil {
		m := strings.TrimSpace(*p.Model)
		if _, ok := AllowedModels[m]; !ok {
			return bad("model is not allowed for free requests")
		}
		s.Model = m
	}
	intField := func(dst *int, v *int, min, max int, name string) error {
		if v == nil {
			return nil
		}
		if *v < min || *v > max {
			return fmt.Errorf("%w: %s must be between %d and %d", ErrInvalidSettings, name, min, max)
		}
		*dst = *v
		return nil
	}
	for _, f := range []struct {
		dst      *int
		v        *int
		min, max int
		name     string
	}{
		// At least 1: a grant made while this was 0 would hold a row with
		// nothing in it for its whole validity. Pause with enabled=false.
		{&s.RequestsPerOrg, p.RequestsPerOrg, 1, MaxGrantRequests, "requests_per_org"},
		{&s.ValidityDays, p.ValidityDays, 1, 365, "validity_days"},
		{&s.MaxOutputTokens, p.MaxOutputTokens, 1, MaxOutputTokensLimit, "max_output_tokens"},
		{&s.MaxInputBytes, p.MaxInputBytes, 1, MaxInputBytesLimit, "max_input_bytes"},
		{&s.MinIntervalMS, p.MinIntervalMS, 0, 3_600_000, "min_interval_ms"},
		{&s.DailyRequests, p.DailyRequests, 0, MaxDailyRequestsLimit, "daily_requests"},
		{&s.BackfillEmailsPerDay, p.BackfillEmailsPerDay, 0, 100_000, "backfill_emails_per_day"},
	} {
		if err := intField(f.dst, f.v, f.min, f.max, f.name); err != nil {
			return Settings{}, err
		}
	}
	return s, nil
}

// UpdateSettings applies patch to the settings row and records the change.
func UpdateSettings(ctx context.Context, db *sqlx.DB, patch SettingsPatch, actor string, now time.Time) (Settings, error) {
	if db == nil {
		return Settings{}, errors.New("billing database is not configured")
	}
	actor, err := cleanActor(actor)
	if err != nil {
		return Settings{}, err
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return Settings{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var before Settings
	if err := tx.GetContext(ctx, &before, `SELECT `+settingsColumns+` FROM billing.model_trial_settings WHERE id FOR UPDATE`); err != nil {
		return Settings{}, fmt.Errorf("read model trial settings: %w", err)
	}
	after, err := patch.apply(before)
	if err != nil {
		return Settings{}, err
	}
	after.UpdatedBy = actor
	after.UpdatedAt = now.UTC().Truncate(time.Microsecond)
	if _, err := tx.ExecContext(ctx, `UPDATE billing.model_trial_settings SET
			enabled = $1, model = $2, requests_per_org = $3, validity_days = $4, max_output_tokens = $5,
			max_input_bytes = $6, min_interval_ms = $7, daily_requests = $8, backfill_emails_per_day = $9,
			updated_by = $10, updated_at = $11
		WHERE id`,
		after.Enabled, after.Model, after.RequestsPerOrg, after.ValidityDays, after.MaxOutputTokens,
		after.MaxInputBytes, after.MinIntervalMS, after.DailyRequests, after.BackfillEmailsPerDay,
		after.UpdatedBy, after.UpdatedAt); err != nil {
		return Settings{}, fmt.Errorf("update model trial settings: %w", err)
	}
	if err := writeEvent(ctx, tx, nil, "settings_change", before, after, actor, now.UTC()); err != nil {
		return Settings{}, err
	}
	if err := tx.Commit(); err != nil {
		return Settings{}, fmt.Errorf("commit model trial settings: %w", err)
	}
	return after, nil
}

// GrantInput is one staff grant of free requests.
type GrantInput struct {
	OrganizationID uuid.UUID
	// Requests is how many unused requests the organization holds after the
	// grant, 1..MaxGrantRequests.
	Requests int
	Actor    string
}

// Grant gives orgID Requests fresh free requests, valid for the configured
// validity_days from now. A new row has source 'ops'. On an existing row the
// allowance becomes used + Requests (so the remaining count is exactly
// Requests and the used <= granted CHECK holds), expiry restarts from now,
// and a revocation is lifted. The organization must exist.
func Grant(ctx context.Context, db *sqlx.DB, in GrantInput, now time.Time) (Trial, error) {
	if db == nil {
		return Trial{}, errors.New("billing database is not configured")
	}
	if in.OrganizationID == uuid.Nil {
		return Trial{}, errors.New("an organization is required")
	}
	if in.Requests < 1 || in.Requests > MaxGrantRequests {
		return Trial{}, fmt.Errorf("requests must be between 1 and %d", MaxGrantRequests)
	}
	actor, err := cleanActor(in.Actor)
	if err != nil {
		return Trial{}, err
	}
	now = now.UTC().Truncate(time.Microsecond)
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return Trial{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var exists bool
	if err := tx.GetContext(ctx, &exists, `SELECT EXISTS (SELECT 1 FROM everstack.organizations WHERE id = $1)`, in.OrganizationID); err != nil {
		return Trial{}, fmt.Errorf("read organization: %w", err)
	}
	if !exists {
		return Trial{}, ErrOrganizationNotFound
	}
	var validityDays int
	if err := tx.GetContext(ctx, &validityDays, `SELECT validity_days FROM billing.model_trial_settings WHERE id`); err != nil {
		return Trial{}, fmt.Errorf("read model trial settings: %w", err)
	}
	expires := now.AddDate(0, 0, validityDays)

	// Create the row if absent (a concurrent first take or grant may race
	// us: ON CONFLICT leaves theirs, then we lock and update it).
	res, err := tx.ExecContext(ctx, `INSERT INTO billing.model_trials
			(organization_id, granted_requests, source, created_at, expires_at)
		VALUES ($1, 0, 'ops', $2, $3) ON CONFLICT (organization_id) DO NOTHING`, in.OrganizationID, now, expires)
	if err != nil {
		return Trial{}, fmt.Errorf("create model trial: %w", err)
	}
	created, _ := res.RowsAffected()
	before, err := loadTrial(ctx, tx, in.OrganizationID, true)
	if err != nil {
		return Trial{}, err
	}
	if before == nil {
		return Trial{}, errors.New("model trial row vanished")
	}
	var beforeEvent any = before
	if created == 1 {
		beforeEvent = nil // the row is new with this grant
	}
	if _, err := tx.ExecContext(ctx, `UPDATE billing.model_trials
		SET granted_requests = used_requests + $2, expires_at = $3, revoked_at = NULL
		WHERE organization_id = $1`, in.OrganizationID, in.Requests, expires); err != nil {
		return Trial{}, fmt.Errorf("grant model trial: %w", err)
	}
	after, err := loadTrial(ctx, tx, in.OrganizationID, false)
	if err != nil {
		return Trial{}, err
	}
	orgID := in.OrganizationID
	if err := writeEvent(ctx, tx, &orgID, "grant", beforeEvent, after, actor, now); err != nil {
		return Trial{}, err
	}
	if err := tx.Commit(); err != nil {
		return Trial{}, fmt.Errorf("commit model trial grant: %w", err)
	}
	return *after, nil
}

// ErrOrganizationNotFound is returned by Grant for an unknown organization.
var ErrOrganizationNotFound = errors.New("organization not found")

// Revoke ends orgID's trial now: every later take is refused. Returns false
// when it was already revoked (no event is written then).
func Revoke(ctx context.Context, db *sqlx.DB, orgID uuid.UUID, actor string, now time.Time) (bool, error) {
	if db == nil {
		return false, errors.New("billing database is not configured")
	}
	actor, err := cleanActor(actor)
	if err != nil {
		return false, err
	}
	now = now.UTC().Truncate(time.Microsecond)
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	before, err := loadTrial(ctx, tx, orgID, true)
	if err != nil {
		return false, err
	}
	if before == nil {
		return false, ErrNotFound
	}
	if before.RevokedAt != nil {
		return false, nil
	}
	if _, err := tx.ExecContext(ctx, `UPDATE billing.model_trials SET revoked_at = $2 WHERE organization_id = $1`, orgID, now); err != nil {
		return false, fmt.Errorf("revoke model trial: %w", err)
	}
	after := *before
	after.RevokedAt = &now
	if err := writeEvent(ctx, tx, &orgID, "revoke", before, after, actor, now); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit model trial revoke: %w", err)
	}
	return true, nil
}
