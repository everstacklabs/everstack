package browserpool

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// TenantLimits is the commercial admission contract for active browser
// leases. Negative values mean unlimited; zero disables that dimension.
type TenantLimits struct {
	MaxConcurrent int
	MaxSession    time.Duration
	// ScopeTenantIDs are the tenant ids whose leases count toward
	// MaxConcurrent: the whole organization on a managed gateway. Empty
	// means the requesting tenant alone.
	ScopeTenantIDs []string
	// ShadowScopeTenantIDs is the organization scope while it is reported,
	// not enforced: admission still counts the tenant alone and only logs
	// what the organization-wide count would decide.
	ShadowScopeTenantIDs []string
}

// ErrConcurrentBrowserLimit marks an admission refused at the plan's
// concurrent browser cap.
var ErrConcurrentBrowserLimit = errors.New("concurrent browser limit reached")

func (l TenantLimits) Validate() error {
	if l.MaxConcurrent < -1 {
		return fmt.Errorf("browser concurrent limit must be -1 or non-negative")
	}
	if l.MaxSession < -1 {
		return fmt.Errorf("browser session limit must be -1, zero, or positive")
	}
	if l.MaxConcurrent != 0 && l.MaxSession == 0 {
		return fmt.Errorf("browser session limit must be positive when browser concurrency is enabled")
	}
	return nil
}

// LimitsResolver resolves the tenant's current plan at admission time. The
// pool owns atomic concurrency enforcement and the session deadline.
type LimitsResolver func(context.Context, string) (TenantLimits, error)

var unlimitedTenantLimits = TenantLimits{
	MaxConcurrent: -1,
	MaxSession:    -1,
}
