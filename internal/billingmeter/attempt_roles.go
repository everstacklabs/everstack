package billingmeter

import (
	"context"
	"sync"
)

// How a provider attempt gets its role. The metering middleware sits at
// provider dispatch and sees one attempt at a time; only the handler that
// dispatched it knows why it was made (the first try, a fallback, a retry the
// routing configuration asked for, a retry the platform made for its own
// reasons, a leg of a parallel race). The handler states that in the
// attempt's context (WithAttemptKind, Race.Leg) and the middleware derives
// the role from it and from how the attempt ended (ResolveRole, Race).
//
// Role vocabulary, as recorded:
//   - served: its output was returned to the caller (including a stream the
//     client walked away from part way: the output it received was served).
//   - race_loser: a leg of a parallel race that did not win.
//   - failed_fallback: failed, and the routing has a later target configured
//     to take over.
//   - interrupted: failed (a stream that broke part way) with nothing
//     configured after it, or from a caller that does not say. Charged for
//     the tokens it generated.
//   - retry: a repeat of the same target, made because the customer's routing
//     configuration allows several attempts, that failed.
//   - platform_retry: an attempt the platform made for its own reasons (key
//     rotation after the previous key was rejected) that failed. Never
//     charged. A platform retry that serves is the served attempt: the
//     customer pays for the response they got, once.

// AttemptKind is why the handler dispatched an attempt.
type AttemptKind int

const (
	// KindUnset is an attempt from a caller that does not say (agent
	// runtime, workflows, memory). Treated as a first try.
	KindUnset AttemptKind = iota
	// KindPrimary is the first try of a request (and each tool-loop turn).
	KindPrimary
	// KindFallback is the first try of a fallback target.
	KindFallback
	// KindRetry repeats a target because the routing configuration allows
	// several attempts.
	KindRetry
	// KindPlatformRetry repeats a target for platform reasons (key rotation).
	KindPlatformRetry
)

type attemptKindKey struct{}

// WithAttemptKind tags the attempts dispatched under ctx.
func WithAttemptKind(ctx context.Context, kind AttemptKind) context.Context {
	return context.WithValue(ctx, attemptKindKey{}, kind)
}

type lastAttemptKey struct{}

// WithLastAttempt marks whether nothing is configured to run after the
// attempts dispatched under ctx (no fallback target, no further repeat), so
// a failure is reported as interrupted rather than as a fallback.
func WithLastAttempt(ctx context.Context, last bool) context.Context {
	return context.WithValue(ctx, lastAttemptKey{}, last)
}

// IsLastAttempt reads the mark; false when there is none.
func IsLastAttempt(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	last, _ := ctx.Value(lastAttemptKey{}).(bool)
	return last
}

// AttemptKindFrom reads the tag; KindUnset when there is none.
func AttemptKindFrom(ctx context.Context) AttemptKind {
	if ctx == nil {
		return KindUnset
	}
	k, _ := ctx.Value(attemptKindKey{}).(AttemptKind)
	return k
}

// ResolveRole is the role of a non-race attempt of kind that ended as
// described. last means nothing is configured to run after it. clientGone
// means the caller cancelled the request while the attempt ran: whatever it
// streamed so far was served.
func ResolveRole(kind AttemptKind, last, succeeded, clientGone bool) string {
	if succeeded {
		return RoleServed
	}
	if kind == KindPlatformRetry {
		return RolePlatformRetry
	}
	if clientGone {
		return RoleServed
	}
	switch {
	case kind == KindRetry:
		return RoleRetry
	case kind == KindUnset || last:
		return RoleInterrupted
	default:
		return RoleFailedFallback
	}
}

// Race arbitrates one parallel-race step. The handler decides the winner (the
// first leg to succeed claims it), but each leg's metering finishes before
// the handler sees its result, so a leg that finishes while the race is open
// is parked and settled when the race is decided: the winner as served,
// every other leg as a race loser. Served and race losers are both charged,
// so only the label waits; the amount is fixed when the leg finishes.
//
// Every unit settles exactly once. The handler must Close the race on every
// exit (defer), so a leg can never stay parked.
type Race struct {
	mu      sync.Mutex
	decided bool
	winner  int
	parked  []parkedLeg
}

type parkedLeg struct {
	idx    int
	unit   SettleUnit
	settle func(SettleUnit)
}

// NewRace opens a race with no winner.
func NewRace() *Race { return &Race{winner: -1} }

type raceLegKey struct{}

type raceLeg struct {
	race *Race
	idx  int
}

// Leg tags ctx as leg idx of r.
func (r *Race) Leg(ctx context.Context, idx int) context.Context {
	return context.WithValue(ctx, raceLegKey{}, raceLeg{race: r, idx: idx})
}

// RaceLegFrom returns the race and leg index ctx belongs to.
func RaceLegFrom(ctx context.Context) (*Race, int, bool) {
	if ctx == nil {
		return nil, 0, false
	}
	l, ok := ctx.Value(raceLegKey{}).(raceLeg)
	if !ok || l.race == nil {
		return nil, 0, false
	}
	return l.race, l.idx, true
}

func raceRole(won bool) string {
	if won {
		return RoleServed
	}
	return RoleRaceLoser
}

// Finish settles leg idx's unit through settle, or parks it until the race is
// decided. The unit's role is set here.
func (r *Race) Finish(idx int, u SettleUnit, settle func(SettleUnit)) {
	r.mu.Lock()
	if !r.decided {
		r.parked = append(r.parked, parkedLeg{idx: idx, unit: u, settle: settle})
		r.mu.Unlock()
		return
	}
	won := r.winner == idx
	r.mu.Unlock()
	settle(withRole(u, raceRole(won)))
}

// Claim makes leg idx the winner if the race is still open. It returns true
// for the winner only; the handler serves a leg's result only when its claim
// succeeds, so the served label and the served response always agree. A nil
// race imposes nothing.
func (r *Race) Claim(idx int) bool {
	if r == nil {
		return true
	}
	r.mu.Lock()
	if r.decided {
		won := r.winner == idx
		r.mu.Unlock()
		return won
	}
	r.decided = true
	r.winner = idx
	parked := r.parked
	r.parked = nil
	r.mu.Unlock()
	settleParkedLegs(parked, idx)
	return true
}

// Close ends the race. Without a winner (timeout, every leg failed) every
// parked leg, and every leg that finishes later, is a race loser. It reports
// whether the race ended with no winner: false means a leg had already
// claimed it, and the handler must serve that leg. Idempotent.
func (r *Race) Close() (noWinner bool) {
	if r == nil {
		return true
	}
	r.mu.Lock()
	if r.decided {
		noWinner = r.winner < 0
		r.mu.Unlock()
		return noWinner
	}
	r.decided = true
	parked := r.parked
	r.parked = nil
	r.mu.Unlock()
	settleParkedLegs(parked, -1)
	return true
}

func settleParkedLegs(parked []parkedLeg, winner int) {
	for _, p := range parked {
		p.settle(withRole(p.unit, raceRole(p.idx == winner)))
	}
}

func withRole(u SettleUnit, role string) SettleUnit {
	if u.Attempt != nil {
		a := *u.Attempt
		a.Role = role
		u.Attempt = &a
	}
	return u
}
