package billingmeter

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
)

// SubscriptionPeriod is the part of a billing.subscriptions row that decides
// an organization's current usage window.
type SubscriptionPeriod struct {
	Status             string    `db:"status"`
	BillingPeriod      string    `db:"billing_period"`
	CurrentPeriodStart time.Time `db:"current_period_start"`
	CurrentPeriodEnd   time.Time `db:"current_period_end"`
	CancelAtPeriodEnd  bool      `db:"cancel_at_period_end"`
}

// UsagePeriod is the window billing files flow usage (requests, tokens)
// under for an organization at now. It mirrors resolveCurrentPeriod and
// SubscriptionEnded in services/billing/internal/service/billing_service.go
// exactly: billing assigns the period itself on receipt, so the reporter
// only needs the same window to decide which counters belong in the
// period-cumulative total it sends. A parity test in services/billing
// (usage_period_parity_test.go) pins the two together.
func UsagePeriod(sub *SubscriptionPeriod, now time.Time) (time.Time, time.Time) {
	now = now.UTC()
	if sub == nil || subscriptionEnded(sub, now) || !sub.CurrentPeriodStart.Before(sub.CurrentPeriodEnd) {
		return calendarMonth(now)
	}
	start := sub.CurrentPeriodStart.UTC()
	end := sub.CurrentPeriodEnd.UTC()
	if now.Before(end) {
		return start, end
	}
	// At most one interval of projection, only within periodProjectionWindow
	// of the stored end (see resolveCurrentPeriod).
	if !now.Before(end.Add(periodProjectionWindow)) {
		return calendarMonth(now)
	}
	switch strings.ToLower(strings.TrimSpace(sub.BillingPeriod)) {
	case "monthly", "month":
		return end, end.AddDate(0, 1, 0)
	case "yearly", "annual", "year":
		return end, end.AddDate(1, 0, 0)
	default:
		return calendarMonth(now)
	}
}

// periodProjectionWindow mirrors services/billing's constant of the same name.
const periodProjectionWindow = 72 * time.Hour

func subscriptionEnded(sub *SubscriptionPeriod, now time.Time) bool {
	if sub == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(sub.Status)) {
	// "unpaid" is not ended: Stripe returns it to active once paid.
	case "canceled", "incomplete_expired":
		return true
	}
	return sub.CancelAtPeriodEnd && !sub.CurrentPeriodEnd.IsZero() && !now.Before(sub.CurrentPeriodEnd)
}

func calendarMonth(now time.Time) (time.Time, time.Time) {
	now = now.UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return start, start.AddDate(0, 1, 0)
}

// LoadSubscriptionPeriod reads org's subscription from the platform DB that
// owns billing.*. No row is (nil, nil): the calendar month applies.
func LoadSubscriptionPeriod(ctx context.Context, billingDB *sqlx.DB, org string) (*SubscriptionPeriod, error) {
	if billingDB == nil {
		return nil, nil
	}
	var sub SubscriptionPeriod
	err := billingDB.GetContext(ctx, &sub, `
		SELECT COALESCE(status, '') AS status,
		       COALESCE(billing_period, '') AS billing_period,
		       current_period_start, current_period_end,
		       COALESCE(cancel_at_period_end, false) AS cancel_at_period_end
		FROM billing.subscriptions
		WHERE organization_id = $1::uuid`, org)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sub, nil
}
