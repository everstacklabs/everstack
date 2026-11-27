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

// State is the organization-facing trial state (GetModelTrial).
type State string

const (
	StateActive      State = "active"
	StateExhausted   State = "exhausted"
	StateExpired     State = "expired"
	StatePaused      State = "paused"
	StateNotEligible State = "not_eligible"
)

// Status is what GetModelTrial reports to an organization member. It carries
// no other organization's data: the day's ceiling contributes only whether
// free requests are paused and until when.
type Status struct {
	Eligible  bool
	Granted   int
	Used      int
	Remaining int
	State     State
	// ExpiresAt is nil for an eligible organization whose allowance has not
	// been granted yet (the 30 days start at the grant).
	ExpiresAt *time.Time
	// ResumesAt is set when today's ceiling is reached: the next UTC
	// midnight. Nil when paused by ops (no known resume time).
	ResumesAt       *time.Time
	Model           string
	MaxOutputTokens int
}

// GetStatus reports orgID's trial at now. A pure read: it never grants. An
// organization with no trial row is eligible when it holds a signup credit
// claim; the row itself is created on its first trial request (S3) or by
// ops. orgID must come from an authenticated, membership-checked caller.
func GetStatus(ctx context.Context, db *sqlx.DB, orgID uuid.UUID, now time.Time) (Status, error) {
	if db == nil {
		return Status{}, errors.New("billing database is not configured")
	}
	now = now.UTC().Truncate(time.Microsecond)
	settings, err := LoadSettings(ctx, db)
	if err != nil {
		return Status{}, err
	}
	st := Status{Model: settings.Model, MaxOutputTokens: settings.MaxOutputTokens}

	t, err := loadTrial(ctx, db, orgID, false)
	if err != nil {
		return Status{}, err
	}
	switch {
	case t == nil:
		var claimed bool
		err := db.GetContext(ctx, &claimed, `SELECT TRUE FROM billing.signup_credit_claims
			WHERE claim_key = 'org:' || $1::text`, orgID.String())
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Status{}, fmt.Errorf("read signup claim: %w", err)
		}
		if !claimed {
			st.State = StateNotEligible
			return st, nil
		}
		st.Eligible = true
		st.Granted = settings.RequestsPerOrg
		st.Remaining = settings.RequestsPerOrg
	case t.RevokedAt != nil:
		st.State = StateNotEligible
		return st, nil
	default:
		st.Eligible = true
		st.Granted = t.GrantedRequests
		st.Used = t.UsedRequests
		if t.GrantedRequests > t.UsedRequests {
			st.Remaining = t.GrantedRequests - t.UsedRequests
		}
		exp := t.ExpiresAt.UTC()
		st.ExpiresAt = &exp
		if !t.ExpiresAt.After(now) {
			st.State = StateExpired
			return st, nil
		}
		if st.Remaining == 0 {
			st.State = StateExhausted
			return st, nil
		}
	}

	if !settings.Enabled || settings.DailyRequests <= 0 {
		st.State = StatePaused
		return st, nil
	}
	day, err := LoadDaily(ctx, db, now)
	if err != nil {
		return Status{}, err
	}
	if day.UsedRequests >= settings.DailyRequests {
		resumes := nextUTCMidnight(now)
		st.State = StatePaused
		st.ResumesAt = &resumes
		return st, nil
	}
	st.State = StateActive
	return st, nil
}
