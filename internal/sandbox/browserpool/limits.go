package browserpool

import (
	"context"
	"fmt"
	"time"
)

// TenantLimits is the commercial admission contract for active browser
// leases. Negative values mean unlimited; zero disables that dimension.
type TenantLimits struct {
	MaxConcurrent int
	MaxSession    time.Duration
}

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
