package middleware

import (
	"context"

	"connectrpc.com/connect"
	apilic "github.com/everstacklabs/everstack/internal/api/policy"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	licensemonitor "github.com/everstacklabs/everstack/internal/services/license_monitor"
)

// SpendLimitInterceptor blocks requests when spend limits are exceeded.
// Uses local cached state for zero-latency checks - no network calls.
type SpendLimitInterceptor struct {
	monitor *licensemonitor.Monitor
	policy  *apilic.Policy
}

// NewSpendLimitInterceptor creates a new spend limit interceptor.
// The monitor tracks spend usage and blocking state.
// The policy determines which endpoints are subject to spend limits (metered paths).
func NewSpendLimitInterceptor(monitor *licensemonitor.Monitor, policy *apilic.Policy) *SpendLimitInterceptor {
	return &SpendLimitInterceptor{
		monitor: monitor,
		policy:  policy,
	}
}

// WrapUnary wraps unary RPCs to check spend limits before processing.
func (i *SpendLimitInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := i.checkSpendLimit(req.Spec().Procedure); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

// Client-side wrappers (no-op - spend limits are enforced on the server side)
func (i *SpendLimitInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn { return next(ctx, spec) }
}

// WrapStreamingHandler wraps streaming RPCs to check spend limits before processing.
func (i *SpendLimitInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if err := i.checkSpendLimit(conn.Spec().Procedure); err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

// checkSpendLimit checks local cached state for spend limit enforcement.
// This is a zero-latency check - pure memory read, no network calls.
func (i *SpendLimitInterceptor) checkSpendLimit(procedure string) error {
	if i.monitor == nil {
		return nil // No monitor = no enforcement
	}

	// Only check spend limits for metered paths (AI gateway endpoints)
	if i.policy != nil && !i.policy.ShouldMeterRequest(procedure) {
		return nil // Non-metered endpoints are not subject to spend limits
	}

	// Check local cached blocked state (zero latency - pure memory read)
	if blocked, reason := i.monitor.IsSpendBlocked(); blocked {
		logger.Warnf("spend_limit_interceptor: blocking request %s - %s", procedure, reason)
		return connect.NewError(connect.CodeResourceExhausted, errMsg("Spend limit exceeded: "+reason))
	}

	return nil
}
