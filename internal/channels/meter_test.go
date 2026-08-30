package channels

import (
	"context"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/enterprise"
)

// TestMessageCeiling pins what the MESSAGES_MONTHLY allowance does to traffic
// past it. Getting this wrong in either direction is expensive: too permissive
// and the free tier is unmetered, too aggressive and a paying customer's Slack
// bot goes silent mid-conversation with no overage path to rescue it.
func TestMessageCeiling(t *testing.T) {
	limits := func(v int64) map[enterprise.UsageType]int64 {
		return map[enterprise.UsageType]int64{enterprise.UsageTypeMessagesMonthly: v}
	}

	cases := []struct {
		name      string
		ent       enterprise.Entitlements
		wantLimit int64
		want      overageAction
	}{
		{
			name:      "hosted free plan is the only tier that blocks",
			ent:       enterprise.Entitlements{Tier: "free", Source: "license", Limits: limits(1_000)},
			wantLimit: 1_000,
			want:      meterBlock,
		},
		{
			name:      "paid tier warns but keeps delivering",
			ent:       enterprise.Entitlements{Tier: "basic", Source: "license", Limits: limits(15_000)},
			wantLimit: 15_000,
			want:      meterWarn,
		},
		{
			name:      "managed plan on a paid tier warns too",
			ent:       enterprise.Entitlements{Tier: "pro", Source: "managed-plan", Limits: limits(100_000)},
			wantLimit: 100_000,
			want:      meterWarn,
		},
		{
			name: "self-hosted CE is silent even if the map carries a limit",
			ent:  enterprise.Entitlements{Tier: "free", Source: "ce", Limits: limits(1_000)},
			want: meterOff,
		},
		{
			name: "unlimited plan has no allowance to exceed",
			ent:  enterprise.Entitlements{Tier: "enterprise", Source: "license", Limits: limits(-1)},
			want: meterOff,
		},
		{
			name: "dev build resolves no limits at all",
			ent:  enterprise.Entitlements{Tier: "dev", Source: "dev"},
			want: meterOff,
		},
		{
			name: "managed bypass has no limit map",
			ent:  enterprise.Entitlements{Source: "managed-bypass"},
			want: meterOff,
		},
		{
			name: "a zero ceiling is treated as unset, not as a total block",
			ent:  enterprise.Entitlements{Tier: "free", Source: "license", Limits: limits(0)},
			want: meterOff,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limit, got := messageCeiling(tc.ent)
			if got != tc.want {
				t.Errorf("action = %v, want %v", got, tc.want)
			}
			if tc.wantLimit != 0 && limit != tc.wantLimit {
				t.Errorf("limit = %d, want %d", limit, tc.wantLimit)
			}
		})
	}
}

// TestMeterMessageWithoutTenantDoesNothing proves the meter cannot be reached
// with an unattributable message. The nil store would panic if the code got
// past the guard, so returning true here is the proof.
func TestMeterMessageWithoutTenant(t *testing.T) {
	m := &ChannelManager{}
	if !m.meterMessage(context.Background(), InboundMessage{ChannelConfigID: "c1"}) {
		t.Fatal("a message with no tenant must pass through unmetered, not be dropped")
	}
}

// TestChannelMessageMeterTenantGuards extends the empty-tenant guards in
// postgres_store_test.go to the metering methods: a nil *sqlx.DB means any
// path that reached SQL would panic, so returning an error proves the guard.
func TestChannelMessageMeterTenantGuards(t *testing.T) {
	s := &PostgresStore{db: nil}
	ctx := context.Background()

	if err := s.RecordChannelMessage(ctx, &ChannelMessageRecord{ChannelConfigID: "c1"}); err == nil {
		t.Error("RecordChannelMessage with no tenant must error before SQL")
	}
	if err := s.RecordChannelMessage(ctx, &ChannelMessageRecord{TenantID: "t1"}); err == nil {
		t.Error("RecordChannelMessage with no channel config must error before SQL")
	}
	if err := s.RecordChannelMessage(ctx, nil); err == nil {
		t.Error("RecordChannelMessage(nil) must error before SQL")
	}
	if _, err := s.CountChannelMessagesThisMonth(ctx, ""); err == nil {
		t.Error("CountChannelMessagesThisMonth with no tenant must error before SQL")
	}
}

// countingStore records how often the meter actually hits the database.
type countingStore struct {
	ChannelStore
	calls int
	count int64
}

func (s *countingStore) CountChannelMessagesThisMonth(context.Context, string) (int64, error) {
	s.calls++
	return s.count, nil
}

// TestMonthlyMessageCountCache pins the read-load behaviour of the meter. The
// naive version runs a COUNT over the tenant's month on every inbound message,
// which at the top of the paid tiers is a six-figure row scan per message.
func TestMonthlyMessageCountCache(t *testing.T) {
	store := &countingStore{count: 5}
	m := &ChannelManager{store: store, messageCounts: make(map[string]*cachedMessageCount)}
	ctx := context.Background()

	got, err := m.monthlyMessageCount(ctx, "t1")
	if err != nil || got != 5 {
		t.Fatalf("first read = %d, %v; want 5, nil", got, err)
	}
	if got, _ = m.monthlyMessageCount(ctx, "t1"); got != 5 || store.calls != 1 {
		t.Fatalf("second read hit the database again: calls=%d", store.calls)
	}

	// A burst inside one TTL window must still move the count, or a tenant
	// could blow past the allowance a minute at a time without tripping it.
	m.bumpMessageCount("t1")
	m.bumpMessageCount("t1")
	if got, _ = m.monthlyMessageCount(ctx, "t1"); got != 7 {
		t.Errorf("count after two bumps = %d, want 7", got)
	}
	if store.calls != 1 {
		t.Errorf("bumps should not requery, calls=%d", store.calls)
	}

	// A stale entry refreshes from the store.
	m.messageCounts["t1"].fetchedAt = time.Now().Add(-2 * messageCountTTL)
	store.count = 99
	if got, _ = m.monthlyMessageCount(ctx, "t1"); got != 99 || store.calls != 2 {
		t.Errorf("stale entry did not refresh: got %d, calls=%d", got, store.calls)
	}

	// A month rollover must not carry the previous month's total forward.
	m.messageCounts["t1"].month = "1999-01"
	store.count = 3
	if got, _ = m.monthlyMessageCount(ctx, "t1"); got != 3 || store.calls != 3 {
		t.Errorf("month rollover did not invalidate: got %d, calls=%d", got, store.calls)
	}

	// Tenants must not share a count.
	store.count = 42
	if got, _ = m.monthlyMessageCount(ctx, "t2"); got != 42 {
		t.Errorf("second tenant read t1's cached count: %d", got)
	}
}
