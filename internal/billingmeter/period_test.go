package billingmeter

import (
	"testing"
	"time"
)

func TestUsagePeriod(t *testing.T) {
	now := time.Date(2026, 9, 30, 12, 0, 0, 0, time.UTC)
	monthStart := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	monthEnd := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	liveStart := time.Date(2026, 9, 10, 23, 59, 48, 0, time.UTC)
	liveEnd := time.Date(2026, 10, 10, 23, 59, 48, 0, time.UTC)
	cases := []struct {
		name       string
		sub        *SubscriptionPeriod
		start, end time.Time
	}{
		{"no subscription", nil, monthStart, monthEnd},
		{"live", &SubscriptionPeriod{Status: "active", BillingPeriod: "monthly", CurrentPeriodStart: liveStart, CurrentPeriodEnd: liveEnd}, liveStart, liveEnd},
		{"canceled", &SubscriptionPeriod{Status: "canceled", BillingPeriod: "monthly", CurrentPeriodStart: liveStart, CurrentPeriodEnd: liveEnd}, monthStart, monthEnd},
		{"incomplete_expired", &SubscriptionPeriod{Status: "incomplete_expired", BillingPeriod: "monthly", CurrentPeriodStart: liveStart, CurrentPeriodEnd: liveEnd}, monthStart, monthEnd},
		{"unpaid is recoverable, keeps its window", &SubscriptionPeriod{Status: "Unpaid ", BillingPeriod: "monthly", CurrentPeriodStart: liveStart, CurrentPeriodEnd: liveEnd}, liveStart, liveEnd},
		{"cancel at period end, passed", &SubscriptionPeriod{Status: "active", BillingPeriod: "monthly", CancelAtPeriodEnd: true,
			CurrentPeriodStart: liveStart.AddDate(0, -1, 0), CurrentPeriodEnd: liveStart}, monthStart, monthEnd},
		{"stale period is not rolled forward month by month", &SubscriptionPeriod{Status: "active", BillingPeriod: "monthly",
			CurrentPeriodStart: liveStart.AddDate(0, -2, 0), CurrentPeriodEnd: liveStart.AddDate(0, -1, 0)}, monthStart, monthEnd},
		{"delayed renewal projects one period", &SubscriptionPeriod{Status: "active", BillingPeriod: "monthly",
			CurrentPeriodStart: now.Add(-24*time.Hour).AddDate(0, -1, 0), CurrentPeriodEnd: now.Add(-24 * time.Hour)},
			now.Add(-24 * time.Hour), now.Add(-24*time.Hour).AddDate(0, 1, 0)},
		{"projection stops at the window", &SubscriptionPeriod{Status: "active", BillingPeriod: "monthly",
			CurrentPeriodStart: now.Add(-periodProjectionWindow).AddDate(0, -1, 0), CurrentPeriodEnd: now.Add(-periodProjectionWindow)},
			monthStart, monthEnd},
		{"unknown period falls back", &SubscriptionPeriod{Status: "active", BillingPeriod: "weekly",
			CurrentPeriodStart: liveStart.AddDate(0, -2, 0), CurrentPeriodEnd: liveStart.AddDate(0, -1, 0)}, monthStart, monthEnd},
		{"invalid window", &SubscriptionPeriod{Status: "active", BillingPeriod: "monthly", CurrentPeriodStart: liveEnd, CurrentPeriodEnd: liveStart}, monthStart, monthEnd},
	}
	for _, tc := range cases {
		s, e := UsagePeriod(tc.sub, now)
		if !s.Equal(tc.start) || !e.Equal(tc.end) {
			t.Errorf("%s: got [%s, %s), want [%s, %s)", tc.name, s, e, tc.start, tc.end)
		}
	}
}
