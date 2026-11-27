package billingmeter

import (
	"context"
	"sync"
	"testing"

	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/require"
)

func TestResolveRoleTable(t *testing.T) {
	cases := []struct {
		name       string
		kind       AttemptKind
		last       bool
		succeeded  bool
		clientGone bool
		want       string
	}{
		{"untagged success", KindUnset, false, true, false, RoleServed},
		// An untagged caller says nothing about fallback, so a failure is
		// not claimed to have fallen back.
		{"untagged failure", KindUnset, false, false, false, RoleInterrupted},
		{"untagged client abort", KindUnset, false, false, true, RoleServed},
		{"primary success", KindPrimary, false, true, false, RoleServed},
		{"primary failure with fallback configured", KindPrimary, false, false, false, RoleFailedFallback},
		{"primary failure with nothing configured after it", KindPrimary, true, false, false, RoleInterrupted},
		{"primary client abort", KindPrimary, true, false, true, RoleServed},
		{"fallback success", KindFallback, false, true, false, RoleServed},
		{"fallback failure", KindFallback, false, false, false, RoleFailedFallback},
		{"last fallback failure", KindFallback, true, false, false, RoleInterrupted},
		{"retry success", KindRetry, false, true, false, RoleServed},
		{"retry failure", KindRetry, false, false, false, RoleRetry},
		{"retry client abort", KindRetry, false, false, true, RoleServed},
		{"platform retry success", KindPlatformRetry, false, true, false, RoleServed},
		{"platform retry failure", KindPlatformRetry, false, false, false, RolePlatformRetry},
		{"platform retry client abort", KindPlatformRetry, false, false, true, RolePlatformRetry},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, ResolveRole(c.kind, c.last, c.succeeded, c.clientGone))
		})
	}
}

func TestLastAttemptContext(t *testing.T) {
	ctx := WithAttemptKind(context.Background(), KindPrimary)
	require.False(t, IsLastAttempt(ctx))
	require.True(t, IsLastAttempt(WithLastAttempt(ctx, true)))
	require.False(t, IsLastAttempt(WithLastAttempt(ctx, false)))
}

func TestRoleCharged(t *testing.T) {
	for _, r := range []string{RoleServed, RoleRaceLoser, RoleFailedFallback, RoleRetry, RoleInterrupted} {
		require.True(t, RoleCharged(r), r)
	}
	require.False(t, RoleCharged(RolePlatformRetry))
	require.False(t, RoleCharged(""))
	require.False(t, RoleCharged("bogus"))
}

func TestChargeableByRoleAndProvenance(t *testing.T) {
	unit := func(role, prov string, micros int64) SettleUnit {
		return SettleUnit{ID: "u", OrgID: "o", Micros: micros, Attempt: &AttemptRecord{Role: role, Provenance: prov}}
	}
	require.True(t, unit(RoleServed, ProvenancePlatform, 10).chargeable())
	require.True(t, unit(RoleRaceLoser, ProvenancePlatform, 10).chargeable())
	require.True(t, unit(RoleFailedFallback, ProvenancePlatform, 10).chargeable())
	require.True(t, unit(RoleRetry, ProvenancePlatform, 10).chargeable())
	require.False(t, unit(RolePlatformRetry, ProvenancePlatform, 10).chargeable(), "platform retries are never charged")
	require.False(t, unit(RoleServed, ProvenanceBYOK, 10).chargeable(), "BYOK is never charged")
	require.False(t, unit(RoleServed, ProvenancePlatform, 0).chargeable(), "no tokens, no charge")
	require.True(t, SettleUnit{ID: "u", Micros: 5}.chargeable(), "legacy units without a record keep their meaning")
}

func TestAttemptKindContext(t *testing.T) {
	ctx := context.Background()
	require.Equal(t, KindUnset, AttemptKindFrom(ctx))
	ctx = WithAttemptKind(ctx, KindRetry)
	require.Equal(t, KindRetry, AttemptKindFrom(ctx))
	require.Equal(t, KindPlatformRetry, AttemptKindFrom(WithAttemptKind(ctx, KindPlatformRetry)))
}

type capturedSettles struct {
	mu    sync.Mutex
	units []SettleUnit
}

func (c *capturedSettles) settle(u SettleUnit) {
	c.mu.Lock()
	c.units = append(c.units, u)
	c.mu.Unlock()
}

func (c *capturedSettles) roles() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := map[string]string{}
	for _, u := range c.units {
		out[u.ID] = u.Attempt.Role
	}
	return out
}

func raceUnit(id string) SettleUnit {
	return SettleUnit{ID: id, OrgID: "o", Micros: 10, Attempt: &AttemptRecord{Provenance: ProvenancePlatform}}
}

// Legs that finish before the handler picks a winner are parked; the claim
// settles them, the winner as served and the rest as race losers.
func TestRaceParksUntilClaim(t *testing.T) {
	var c capturedSettles
	r := NewRace()
	r.Finish(0, raceUnit("a"), c.settle)
	r.Finish(1, raceUnit("b"), c.settle)
	require.Empty(t, c.units, "nothing settles before a winner is known")
	require.True(t, r.Claim(1))
	require.False(t, r.Claim(0), "only the first claim wins")
	require.Equal(t, map[string]string{"a": RoleRaceLoser, "b": RoleServed}, c.roles())

	// A leg finishing after the decision settles at once.
	r.Finish(2, raceUnit("c"), c.settle)
	require.Equal(t, RoleRaceLoser, c.roles()["c"])
}

// A race that ends with no winner (timeout, every leg failed) settles every
// parked leg as a race loser, and later legs too.
func TestRaceCloseWithoutWinner(t *testing.T) {
	var c capturedSettles
	r := NewRace()
	r.Finish(0, raceUnit("a"), c.settle)
	require.True(t, r.Close(), "closed with no winner")
	require.Equal(t, RoleRaceLoser, c.roles()["a"])
	require.False(t, r.Claim(1), "a closed race has no winner")
	r.Finish(1, raceUnit("b"), c.settle)
	require.Equal(t, RoleRaceLoser, c.roles()["b"])
	require.True(t, r.Close(), "idempotent")
	require.Len(t, c.units, 2)

	// Closing a race a leg already claimed reports the winner.
	won := NewRace()
	require.True(t, won.Claim(2))
	require.False(t, won.Close(), "a claimed race has a winner to serve")
}

// Each unit settles exactly once however legs, claims and closes interleave.
func TestRaceConcurrentExactlyOnce(t *testing.T) {
	for i := 0; i < 50; i++ {
		var c capturedSettles
		r := NewRace()
		var wg sync.WaitGroup
		for leg := 0; leg < 4; leg++ {
			wg.Add(1)
			go func(leg int) {
				defer wg.Done()
				r.Finish(leg, raceUnit(string(rune('a'+leg))), c.settle)
				r.Claim(leg)
			}(leg)
		}
		wg.Wait()
		r.Close()
		roles := c.roles()
		require.Len(t, c.units, 4)
		served := 0
		for _, role := range roles {
			if role == RoleServed {
				served++
			}
		}
		require.Equal(t, 1, served)
	}
}

func TestRaceLegContext(t *testing.T) {
	r := NewRace()
	ctx := r.Leg(context.Background(), 3)
	got, idx, ok := RaceLegFrom(ctx)
	require.True(t, ok)
	require.Same(t, r, got)
	require.Equal(t, 3, idx)
	_, _, ok = RaceLegFrom(context.Background())
	require.False(t, ok)
	var nilRace *Race
	require.True(t, nilRace.Claim(0), "a nil race imposes no arbitration on the handler")
	nilRace.Close()
}

// Under a saturated settle queue a charged unit is debited inline (never
// lost), while a record-only unit is dropped and counted instead of taking a
// database write on the request goroutine: it carries no money.
func TestSaturatedQueueDropsOnlyRecordOnlyUnits(t *testing.T) {
	fake := &fakeDebit{}
	m := &Meter{billingDB: &sqlx.DB{}}
	m.worker = newSettleWorker(nil, 1, 0, fake.fn, nil)
	defer m.worker.stop()
	require.True(t, m.worker.enqueue(SettleUnit{OrgID: "o", ID: "filler", Micros: 1}))

	before := counterValue(attemptsDroppedSaturated)
	recordOnly := SettleUnit{ID: "r", OrgID: "o", Micros: 0, Attempt: &AttemptRecord{Role: RolePlatformRetry, Provenance: ProvenancePlatform}}
	m.Settle(context.Background(), recordOnly)
	require.Zero(t, fake.calls.Load(), "a record-only unit must not be written inline")
	require.Equal(t, before+1, counterValue(attemptsDroppedSaturated))

	charged := SettleUnit{ID: "c", OrgID: "o", Micros: 10, Attempt: &AttemptRecord{Role: RoleServed, Provenance: ProvenancePlatform}}
	m.Settle(context.Background(), charged)
	require.Equal(t, int64(1), fake.calls.Load(), "a charged unit falls back to an inline debit")
}
