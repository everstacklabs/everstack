package billingmeter

import "context"

// SettledUsage is the usage the metering seam settled for one attempt: the
// provider's reported usage, or the character estimate of a stream that
// ended without reporting any. It is what the wallet was (or, on BYOK, would
// have been) debited for.
type SettledUsage struct {
	Model            string
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
	// Estimated marks the character estimate.
	Estimated bool
}

type settleObserverKey struct{}

// WithSettleObserver asks the metering seam to report, through fn, the usage
// it settles for each attempt dispatched under ctx. A handler that records
// usage elsewhere (the license monitor) uses it to record what was debited,
// including for a stream that broke off before reporting usage. fn runs
// synchronously, before the provider call returns, and only for attempts the
// seam meters (platform and BYOK providers on a shared gateway).
func WithSettleObserver(ctx context.Context, fn func(SettledUsage)) context.Context {
	return context.WithValue(ctx, settleObserverKey{}, fn)
}

// ObserveSettle reports u to ctx's settle observer, if there is one.
func ObserveSettle(ctx context.Context, u SettledUsage) {
	if ctx == nil {
		return
	}
	if fn, ok := ctx.Value(settleObserverKey{}).(func(SettledUsage)); ok && fn != nil {
		fn(u)
	}
}
