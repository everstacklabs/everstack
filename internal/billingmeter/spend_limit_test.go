package billingmeter

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
)

func TestEvaluateSpendLimits(t *testing.T) {
	monthly := SpendLimit{Period: "monthly", LimitMicros: 50_000_000}
	daily := SpendLimit{Period: "daily", LimitMicros: 5_000_000}
	at := func(l SpendLimit, spent int64) SpendLimit { l.SpentMicros = spent; return l }

	for _, tc := range []struct {
		name    string
		state   SpendState
		outcome string
		over    string
	}{
		{"no limit", SpendState{}, SpendOutcomeNoLimit, ""},
		{"under the limit", SpendState{Limits: []SpendLimit{at(monthly, 49_999_999)}}, SpendOutcomeAdmit, ""},
		{"exactly at the limit is over", SpendState{Limits: []SpendLimit{at(monthly, 50_000_000)}}, SpendOutcomeOver, "monthly"},
		{"past the limit", SpendState{Limits: []SpendLimit{at(monthly, 60_000_000)}}, SpendOutcomeOver, "monthly"},
		{"any one limit over blocks", SpendState{Limits: []SpendLimit{at(monthly, 10), at(daily, 5_000_000)}}, SpendOutcomeOver, "daily"},
		{"exempt org is never over", SpendState{Exempt: true, Limits: []SpendLimit{at(monthly, 60_000_000)}}, SpendOutcomeExempt, ""},
		{"a zero limit is not a block-everything switch", SpendState{Limits: []SpendLimit{at(SpendLimit{Period: "monthly"}, 0)}}, SpendOutcomeAdmit, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outcome, over := EvaluateSpendLimits(tc.state)
			require.Equal(t, tc.outcome, outcome)
			if tc.over == "" {
				require.Nil(t, over)
			} else {
				require.NotNil(t, over)
				require.Equal(t, tc.over, over.Period)
			}
		})
	}
}

func TestParseSpendLimitEnforcement(t *testing.T) {
	for _, in := range []string{"", "shadow", " Shadow "} {
		enforce, err := ParseSpendLimitEnforcement(in)
		require.NoError(t, err, in)
		require.False(t, enforce, in)
	}
	enforce, err := ParseSpendLimitEnforcement("enforce")
	require.NoError(t, err)
	require.True(t, enforce)
	// Anything else is a misconfiguration and stays in shadow.
	enforce, err = ParseSpendLimitEnforcement("on")
	require.Error(t, err)
	require.False(t, enforce)
}

func TestSpendLimitEnforcementDefaultsToShadow(t *testing.T) {
	require.False(t, SpendLimitEnforced(), "the switch must default to shadow")
}

// The refusal a customer sees: the limit, the period and when it resets,
// with no internal ids, and a status that is neither retried nor treated as
// a lost session.
func TestSpendLimitExceededError(t *testing.T) {
	now := time.Date(2026, 10, 10, 15, 0, 0, 0, time.UTC)
	monthly := SpendLimitError(SpendLimit{Period: "monthly", LimitMicros: 50_000_000, SpentMicros: 50_100_000}, now)
	msg := monthly.Error()
	require.Contains(t, msg, "$50.00")
	require.Contains(t, msg, "monthly spend limit")
	require.Contains(t, msg, "1 November 2026")
	require.NotContains(t, msg, "\u2014", "no em dashes in customer copy")
	require.NotContains(t, msg, "micros")

	daily := SpendLimitError(SpendLimit{Period: "daily", LimitMicros: 2_500_000}, now).Error()
	require.Contains(t, daily, "$2.50")
	require.Contains(t, daily, "daily spend limit")
	require.Contains(t, daily, "00:00 UTC")

	var typed *SpendLimitExceededError
	require.True(t, errors.As(monthly, &typed))
	require.Equal(t, ReasonSpendLimitExceeded, typed.Reason())
	require.Equal(t, http.StatusPaymentRequired, typed.HTTPStatusCode())

	var ce *connect.Error
	require.True(t, errors.As(monthly, &ce))
	require.Equal(t, connect.CodeResourceExhausted, ce.Code())
	require.False(t, strings.Contains(ce.Message(), "organization_id"))

	// The reason travels as a google.rpc.ErrorInfo detail: it is the only
	// part that survives the gRPC hop to grpc-gateway, where the HTTP layer
	// turns it into a 402.
	var reasons []string
	for _, d := range ce.Details() {
		v, err := d.Value()
		require.NoError(t, err)
		if info, ok := v.(*errdetails.ErrorInfo); ok {
			reasons = append(reasons, info.GetReason())
		}
	}
	require.Equal(t, []string{ReasonSpendLimitExceeded}, reasons)
}

func TestSpendLimitOutcomeMetric(t *testing.T) {
	before := counterValue(spendLimitEvaluated.WithLabelValues("shadow", SpendOutcomeWouldBlock, SpendScopeOrganization, SpendBasisPlatformCredit))
	reportSpendLimit("org-1", "shadow", SpendOutcomeOver, &SpendLimit{Period: "monthly", LimitMicros: 1, SpentMicros: 2})
	require.Equal(t, before+1, counterValue(spendLimitEvaluated.WithLabelValues("shadow", SpendOutcomeWouldBlock, SpendScopeOrganization, SpendBasisPlatformCredit)))

	before = counterValue(spendLimitEvaluated.WithLabelValues("enforce", SpendOutcomeBlocked, SpendScopeOrganization, SpendBasisPlatformCredit))
	reportSpendLimit("org-1", "enforce", SpendOutcomeOver, &SpendLimit{Period: "monthly", LimitMicros: 1, SpentMicros: 2})
	require.Equal(t, before+1, counterValue(spendLimitEvaluated.WithLabelValues("enforce", SpendOutcomeBlocked, SpendScopeOrganization, SpendBasisPlatformCredit)))
}

// Concurrent cache misses for one organization share a single platform DB
// read; a hit afterwards reads nothing.
func TestSpendCacheSingleflightsConcurrentMisses(t *testing.T) {
	var reads atomic.Int32
	release := make(chan struct{})
	c := &spendCache{read: func(ctx context.Context, _ *sqlx.DB, orgID string, _ time.Time) (SpendState, error) {
		reads.Add(1)
		<-release
		return SpendState{Limits: []SpendLimit{{Period: "monthly", LimitMicros: 1}}}, nil
	}}
	now := time.Now()
	const callers = 20
	var wg sync.WaitGroup
	results := make(chan SpendState, callers)
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := c.get(context.Background(), nil, "org-1", now)
			require.NoError(t, err)
			results <- s
		}()
	}
	// Let every caller reach the miss before the one read returns.
	require.Eventually(t, func() bool { return reads.Load() == 1 }, time.Second, time.Millisecond)
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()
	close(results)
	for s := range results {
		require.Len(t, s.Limits, 1)
	}
	require.Equal(t, int32(1), reads.Load(), "concurrent misses must share one read")
	_, err := c.get(context.Background(), nil, "org-1", now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, int32(1), reads.Load(), "a fresh entry is served from the cache")
}

// A cache miss reads under its own short timeout, so a slow platform DB
// cannot hold a request (whose context has no deadline) for long; the
// caller then fails open.
func TestSpendCacheMissHasItsOwnTimeout(t *testing.T) {
	c := &spendCache{timeout: 50 * time.Millisecond, read: func(ctx context.Context, _ *sqlx.DB, _ string, _ time.Time) (SpendState, error) {
		<-ctx.Done()
		return SpendState{}, ctx.Err()
	}}
	start := time.Now()
	_, err := c.get(context.Background(), nil, "org-1", time.Now())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(start), time.Second)
	require.Equal(t, 2*time.Second, spendReadTimeout, "the production read timeout")
}

// One caller giving up does not cancel the shared read for the others.
func TestSpendCacheSharedReadSurvivesOneCallerCancelling(t *testing.T) {
	release := make(chan struct{})
	c := &spendCache{read: func(ctx context.Context, _ *sqlx.DB, _ string, _ time.Time) (SpendState, error) {
		select {
		case <-release:
			return SpendState{Exempt: true}, nil
		case <-ctx.Done():
			return SpendState{}, ctx.Err()
		}
	}}
	gone, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := c.get(gone, nil, "org-1", time.Now()); done <- err }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	close(release)
	s, err := c.get(context.Background(), nil, "org-1", time.Now())
	require.NoError(t, err)
	require.True(t, s.Exempt)
}
