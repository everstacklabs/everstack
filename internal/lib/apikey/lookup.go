package apikey

import (
	"context"
	"errors"
	"sync"
	"time"

	pdb "github.com/everstacklabs/everstack/pkg/database"
	"golang.org/x/sync/singleflight"
)

// ErrLookupUnavailable means a key could not be looked up: the database failed
// or a failure for the same key is too recent to ask again. The key may well
// be valid; callers answer it as retryable and never cache it as invalid.
var ErrLookupUnavailable = errors.New("API key validation is temporarily unavailable")

const (
	// lookupTimeout bounds one database lookup of a key.
	lookupTimeout = 5 * time.Second
	// DefaultFailureWindow is how long a failed lookup of a key is
	// remembered before the database is asked about that key again.
	DefaultFailureWindow = 2 * time.Second
	// defaultMaxFailures bounds the failures remembered at once. Past it,
	// new failures are not remembered (and those keys are asked about
	// again), so a flood of distinct keys during an outage cannot grow it.
	defaultMaxFailures = 10000
)

// LookupGuard protects the database behind API-key validation when lookups
// are failing or slow, without remembering anything about a key itself:
//
//   - Concurrent lookups of the same key share one query, so a cold cache
//     under load, or a database that hangs, costs one query per key rather
//     than one per request.
//   - A lookup that failed is remembered for a short window, during which
//     further requests with that key are answered ErrLookupUnavailable
//     without querying. Once the window passes the database is asked again.
//
// Only failure is remembered here; a successful answer is cached, if at all,
// by the caller. Lookups are keyed by the key hash and any tenant identifier
// on the context, so callers with different tenant contexts never share one.
type LookupGuard struct {
	group  singleflight.Group
	window time.Duration
	max    int
	now    func() time.Time

	mu     sync.Mutex
	failed map[string]time.Time // lookup key -> when its failure stops being remembered
}

// NewLookupGuard returns a guard that remembers a failed lookup for window.
func NewLookupGuard(window time.Duration) *LookupGuard {
	return &LookupGuard{window: window, max: defaultMaxFailures, now: time.Now, failed: make(map[string]time.Time)}
}

// Do returns fn's answer for hash. fn runs with a context of its own, bounded
// by the lookup timeout and detached from the caller's cancellation, because
// its answer is shared by every caller waiting on the same key; a caller whose
// own context ends stops waiting and gets its context's error.
//
// A nil guard runs fn directly, with the lookup timeout.
func (g *LookupGuard) Do(ctx context.Context, hash string, fn func(context.Context) (interface{}, error)) (interface{}, error) {
	if g == nil {
		lctx, cancel := context.WithTimeout(ctx, lookupTimeout)
		defer cancel()
		return fn(lctx)
	}
	key := lookupKey(ctx, hash)
	if g.recentlyFailed(key) {
		return nil, ErrLookupUnavailable
	}
	ch := g.group.DoChan(key, func() (interface{}, error) {
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), lookupTimeout)
		defer cancel()
		v, err := fn(lctx)
		if err != nil {
			g.rememberFailure(key)
		}
		return v, err
	})
	select {
	case r := <-ch:
		return r.Val, r.Err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func lookupKey(ctx context.Context, hash string) string {
	// internal/database's tenant helpers read the same context value.
	return hash + "\x00" + pdb.TenantSchemaFromContext(ctx)
}

func (g *LookupGuard) recentlyFailed(key string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	until, ok := g.failed[key]
	if !ok {
		return false
	}
	if g.now().Before(until) {
		return true
	}
	delete(g.failed, key)
	return false
}

func (g *LookupGuard) rememberFailure(key string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if len(g.failed) >= g.max {
		for k, until := range g.failed {
			if !now.Before(until) {
				delete(g.failed, k)
			}
		}
		if len(g.failed) >= g.max {
			return
		}
	}
	g.failed[key] = now.Add(g.window)
}
