package v1

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/everstacklabs/everstack/internal/billingmeter"
)

// raceLegs runs one parallel fallback step: legs 0..n-1 run concurrently, the
// first to succeed claims race and is returned. It returns as soon as one of
// three things happens:
//
//   - a leg succeeds and claims the race: its result, nil error;
//   - every leg has failed: a raceError wrapping each leg's error;
//   - ctx ends (step timeout or client gone) with no winner: a raceError
//     wrapping ctx.Err() and whatever legs had failed by then.
//
// The legs run under a context cancelled when raceLegs returns, so losers
// still in flight are stopped rather than left running. Each leg receives
// race.Leg(ctx, leg) so metering attributes its attempt to the race.
//
// Before this, the step waited only for a winner or for ctx: with no step
// timeout and every leg failed, nothing ever woke it and the request hung
// until the client disconnected.
func raceLegs[T any](ctx context.Context, race *billingmeter.Race, n int, run func(ctx context.Context, leg int) (T, error)) (T, error) {
	var zero T
	if n <= 0 {
		return zero, errors.New("parallel race has no legs")
	}
	legCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Both channels are buffered for every leg, so no leg ever blocks on a
	// send after raceLegs has returned.
	type legErr struct {
		leg int
		err error
	}
	resCh := make(chan T, 1)
	errCh := make(chan legErr, n)
	for i := 0; i < n; i++ {
		leg := i
		go func() {
			v, err := run(race.Leg(legCtx, leg), leg)
			if err != nil {
				errCh <- legErr{leg: leg, err: err}
				return
			}
			// Only the leg whose claim wins is served, so the served attempt
			// record and the served response always agree. A success that
			// loses the claim is a race loser and counts toward nothing: the
			// winner's result is already on resCh.
			if race.Claim(leg) {
				resCh <- v
			}
		}()
	}

	errs := make([]error, n)
	failed := 0
	for {
		select {
		case v := <-resCh:
			return v, nil
		case le := <-errCh:
			errs[le.leg] = le.err
			failed++
			if failed == n {
				// Nobody can claim the race any more; close it so parked
				// legs settle as losers now rather than when the handler
				// returns.
				race.Close()
				return zero, &raceError{legs: errs}
			}
		case <-ctx.Done():
			// A leg that claimed the race just before is still served (its
			// result is already on its way); otherwise the race closes with
			// no winner.
			if !race.Close() {
				return <-resCh, nil
			}
			return zero, &raceError{legs: errs, ctxErr: ctx.Err()}
		}
	}
}

// raceError is the outcome of a parallel step with no winner. It unwraps to
// every leg error and, when the step ended early, the context error.
type raceError struct {
	legs   []error // indexed by leg; nil for a leg still in flight
	ctxErr error
}

func (e *raceError) Error() string {
	parts := make([]string, 0, len(e.legs)+1)
	failed := 0
	for i, err := range e.legs {
		if err != nil {
			failed++
			parts = append(parts, fmt.Sprintf("leg %d: %v", i, err))
		}
	}
	head := fmt.Sprintf("all %d parallel fallback legs failed", len(e.legs))
	if e.ctxErr != nil {
		head = fmt.Sprintf("parallel fallback ended with no winner (%v); %d of %d legs failed", e.ctxErr, failed, len(e.legs))
	}
	if len(parts) == 0 {
		return head
	}
	return head + ": " + strings.Join(parts, "; ")
}

func (e *raceError) Unwrap() []error {
	out := make([]error, 0, len(e.legs)+1)
	if e.ctxErr != nil {
		out = append(out, e.ctxErr)
	}
	for _, err := range e.legs {
		if err != nil {
			out = append(out, err)
		}
	}
	return out
}

// withRaceFailure appends a failed parallel step's leg errors to the error
// the request will return, keeping err as the only wrapped error so the
// status the client receives is still decided by the original failure (a
// leg's own timeout must not turn a 503 into a 504).
func withRaceFailure(err error, step string, raceErr error) error {
	if raceErr == nil {
		return err
	}
	if err == nil {
		return raceErr
	}
	return fmt.Errorf("%w; parallel fallback %q: %s", err, step, raceErr.Error())
}
