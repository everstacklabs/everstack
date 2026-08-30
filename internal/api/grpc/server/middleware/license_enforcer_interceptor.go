package middleware

import (
	"context"
	"strings"

	"connectrpc.com/connect"
	httpmw "github.com/everstacklabs/everstack/internal/api/http/middleware"
	apilic "github.com/everstacklabs/everstack/internal/api/policy"
	"github.com/everstacklabs/everstack/pkg/tenant"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// LicenseConnectInterceptor enforces that the current instance has an active, valid license via a shared enforcer.
// It bypasses configured services using a policy and delegates state checks to the shared HTTP enforcer.
type LicenseConnectInterceptor struct {
	policy   *apilic.Policy
	enforcer *httpmw.LicenseEnforcer
}

func NewLicenseConnectInterceptor(enforcer *httpmw.LicenseEnforcer, policy *apilic.Policy) *LicenseConnectInterceptor {
	if policy == nil {
		policy = apilic.NewDefaultPolicy()
	}
	return &LicenseConnectInterceptor{policy: policy, enforcer: enforcer}
}

func (i *LicenseConnectInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		procedure := req.Spec().Procedure
		// Managed/shared tenant requests are auto-licensed by control plane.
		if tenant.ConfigFromContext(ctx) != nil {
			return next(ctx, req)
		}
		if contextkeys.IsTenantAuthenticated(ctx) {
			return next(ctx, req)
		}

		// Check if policy exists and log its state
		if i.policy != nil {
			shouldBypass := i.policy.ShouldBypassProcedure(procedure)
			if shouldBypass {
				logger.Debugf("license_enforcer: bypassing procedure: %s", procedure)
				return next(ctx, req)
			}
		} else {
			logger.Warnf("license_enforcer: policy is nil for procedure: %s", procedure)
		}

		if i.enforcer == nil || !i.enforcer.IsEnabled() {
			return next(ctx, req)
		}
		st := i.enforcer.GetCached()
		if err := i.enforceStateWithTrialFallback(ctx, st, procedure); err != nil {
			logger.Warnf("license_enforcer: blocking procedure %s: %v", procedure, err)
			return nil, err
		}
		return next(ctx, req)
	}
}

// Client-side wrappers (no-op)
func (i *LicenseConnectInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn { return next(ctx, spec) }
}

func (i *LicenseConnectInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		// Managed/shared tenant requests are auto-licensed by control plane.
		if tenant.ConfigFromContext(ctx) != nil {
			return next(ctx, conn)
		}
		if contextkeys.IsTenantAuthenticated(ctx) {
			return next(ctx, conn)
		}
		if i.policy != nil && i.policy.ShouldBypassProcedure(conn.Spec().Procedure) {
			return next(ctx, conn)
		}
		if i.enforcer == nil || !i.enforcer.IsEnabled() {
			return next(ctx, conn)
		}
		st := i.enforcer.GetCached()
		if err := i.enforceStateWithTrialFallback(ctx, st, conn.Spec().Procedure); err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

// enforceStateWithTrialFallback mirrors the HTTP middleware's semantics
// (docs/design/editions-and-billing.md, D6/D9) for Connect procedures:
//   - unlicensed / trial-expired / license-expired: NEVER a wall — the
//     instance runs as CE and the creation limits + feature gates do the
//     gating (the grace window keeps full plan entitlements via the monitor).
//   - active trial: metered on AI-gateway procedures.
//   - explicitly disabled/revoked license: writes blocked, reads allowed.
func (i *LicenseConnectInterceptor) enforceStateWithTrialFallback(ctx context.Context, st *httpmw.LicenseState, procedure string) error {
	// Explicitly disabled/revoked licenses keep a harder posture: reads and
	// data export stay available (never brick), writes are blocked.
	// NOTE: the license service does not emit these statuses yet; forward
	// hook for a real vendor kill switch (see the HTTP surface for rationale).
	if st != nil && (st.Status == "disabled" || st.Status == "revoked") {
		if isWriteProcedure(procedure) {
			return connect.NewError(connect.CodePermissionDenied, errMsg("license disabled; contact support or renew"))
		}
		return nil
	}

	// Active trial (optional elevated preview): meter AI-gateway procedures.
	// An expired trial falls through to CE pass-through, never to a wall.
	if st == nil || !st.Active {
		if i.enforcer != nil && i.enforcer.IsInTrialMode() {
			tm := i.enforcer.GetTrialManager()
			if tm != nil && tm.IsActive() && !tm.IsExpired() {
				if i.policy != nil && i.policy.ShouldMeterRequest(procedure) {
					if err := tm.RecordRequest(ctx); err != nil {
						logger.Warnf("trial limit exceeded for procedure %s: %v", procedure, err)
						return connect.NewError(connect.CodeResourceExhausted, errMsg("trial limit exceeded: "+err.Error()))
					}
				}
			}
		}
		// Unlicensed is a terminal, non-blocking CE state.
		return nil
	}

	// Active license (including suspended, expired-in-grace, and
	// expired-degraded): the request proceeds. Suspended AI-gateway traffic
	// is blocked separately by SpendLimitInterceptor; post-grace CE limits
	// apply at the enforcement callsites via the monitor state.
	return nil
}

// isWriteProcedure reports whether a Connect procedure name looks like a
// mutation (used only for the disabled-license write block).
func isWriteProcedure(proc string) bool {
	// Whitelist activation-related procedures so a disabled instance can
	// still re-activate.
	if strings.Contains(proc, "ActivateGatewayInstance") || strings.Contains(proc, "GenerateActivationToken") {
		return false
	}
	writeOps := []string{
		"Create", "Update", "Delete", "Configure", "Toggle",
		"Set", "Add", "Remove", "Activate", "Deactivate", "Refresh",
		"Upsert", "Insert", "Modify", "Save", "Execute", "Run",
	}
	for _, op := range writeOps {
		if strings.Contains(proc, "/"+op) || strings.Contains(proc, "."+op) {
			return true
		}
	}
	return false
}

// errMsg wraps a string as an error type without allocations each call.
type staticErr string

func (e staticErr) Error() string { return string(e) }

func errMsg(s string) error { return staticErr(s) }

