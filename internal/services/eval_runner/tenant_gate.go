package eval_runner

import (
	"context"
	"sync/atomic"
	"time"
)

// TenantActiveFunc reports whether a tenant (in managed Cloud, a managed
// instance id) may run work. It is expected to be cached and to fail open;
// see internal/lib/tenantstatus.Checker.ActiveFn.
type TenantActiveFunc = func(ctx context.Context, tenantID string) bool

// pausedTenantRetryDelay is how long a paused tenant's eval run is pushed
// back before any worker may claim it again. It keeps a paused tenant's
// runs from being claimed and released on every 2s runner tick.
const pausedTenantRetryDelay = time.Minute

// pausedLeaseOwner marks an eval run whose lease is held on behalf of a
// paused tenant rather than a live worker.
const pausedLeaseOwner = "tenant-paused"

// tenantGate holds an optional TenantActiveFunc. The zero value treats every
// tenant as active. Safe for concurrent use.
type tenantGate struct {
	fn atomic.Pointer[TenantActiveFunc]
}

func (g *tenantGate) set(fn TenantActiveFunc) {
	if fn == nil {
		g.fn.Store(nil)
		return
	}
	g.fn.Store(&fn)
}

func (g *tenantGate) active(ctx context.Context, tenantID string) bool {
	p := g.fn.Load()
	if p == nil || *p == nil {
		return true
	}
	return (*p)(ctx, tenantID)
}
