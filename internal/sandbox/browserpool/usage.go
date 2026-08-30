package browserpool

import (
	"context"
	"fmt"
	"math"
	"time"
)

// UsageRecorder is the narrow billing seam owned by the browser pool. Idle
// pods never call Start, and a released lease always calls Finish regardless
// of whether the pod is reused or deleted.
type UsageRecorder interface {
	Start(context.Context, *Lease, time.Time) error
	Heartbeat(context.Context, string, time.Time) error
	Finish(context.Context, string, time.Time, string) error
}

// UsagePricing is expressed as integer micro-dollars to keep cumulative
// meters bit-for-bit stable across gateway and billing-service retries.
type UsagePricing struct {
	CostMicrosPerHour       int64
	BillingIncrementSeconds int64
	MinimumSessionSeconds   int64
}

func NewUsagePricing(browserHourUSD float64, billingIncrementSeconds, minimumSessionSeconds int64) (UsagePricing, error) {
	pricing := UsagePricing{
		CostMicrosPerHour:       int64(math.Round(browserHourUSD * 1_000_000)),
		BillingIncrementSeconds: billingIncrementSeconds,
		MinimumSessionSeconds:   minimumSessionSeconds,
	}
	if err := pricing.Validate(); err != nil {
		return UsagePricing{}, err
	}
	return pricing, nil
}

func (p UsagePricing) Validate() error {
	if p.CostMicrosPerHour <= 0 {
		return fmt.Errorf("browser usage cost per hour must be positive")
	}
	if p.BillingIncrementSeconds <= 0 {
		return fmt.Errorf("browser billing increment must be positive")
	}
	if p.MinimumSessionSeconds < p.BillingIncrementSeconds {
		return fmt.Errorf("browser minimum session must be at least one billing increment")
	}
	return nil
}

// PriceWindow applies the public contract: elapsed time rounds up to the next
// billing increment, then to the minimum session, and cost rounds up once at
// the final micro-dollar boundary.
func (p UsagePricing) PriceWindow(startedAt, endedAt time.Time) (durationSeconds, billableSeconds, costMicros int64) {
	if p.Validate() != nil || startedAt.IsZero() || !endedAt.After(startedAt) {
		return 0, 0, 0
	}
	durationSeconds = int64(math.Ceil(endedAt.Sub(startedAt).Seconds()))
	billableSeconds = ceilDiv(durationSeconds, p.BillingIncrementSeconds) * p.BillingIncrementSeconds
	if billableSeconds < p.MinimumSessionSeconds {
		billableSeconds = p.MinimumSessionSeconds
	}
	costMicros = ceilDiv(billableSeconds*p.CostMicrosPerHour, 3600)
	return durationSeconds, billableSeconds, costMicros
}

func ceilDiv(value, divisor int64) int64 {
	if value <= 0 || divisor <= 0 {
		return 0
	}
	return (value + divisor - 1) / divisor
}
