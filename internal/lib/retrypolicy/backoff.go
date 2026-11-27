package retrypolicy

import (
	"context"
	"math/rand/v2"
	"time"
)

// Backoff waits about d before the next attempt, or until ctx is done,
// whichever comes first. The wait is jittered uniformly over [d/2, 3d/2) so
// that requests which failed together (one upstream 429 burst) do not retry
// in lockstep; the mean stays d. It returns ctx's error when ctx ended the
// wait, nil otherwise. A non-positive d returns immediately (still reporting
// an already-cancelled ctx).
func Backoff(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(Jitter(d))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Jitter returns a duration drawn uniformly from [d/2, 3d/2).
func Jitter(d time.Duration) time.Duration {
	if d <= 1 {
		return d
	}
	return d/2 + time.Duration(rand.Int64N(int64(d)))
}
