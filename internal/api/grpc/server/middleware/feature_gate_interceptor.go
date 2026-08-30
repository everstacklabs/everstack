package middleware

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/pkg/tenant"
)

// FeatureGateInterceptor blocks requests when a feature is not available at the
// current tier. Reusable for any EE-gated feature.
//
// Decision flow:
//   - CE build (edition == "ce") → always blocked (feature is enterprise-only)
//   - EE build → delegates to LicenseMonitor.IsFeatureEnabled(featureKey)
//
// Denials use connect.CodeFailedPrecondition, NOT CodePermissionDenied. A
// plan/edition gate is a "your account state doesn't permit this yet" signal
// (upgrade to proceed), not an authentication or identity-permission failure.
// The admin FE's transport bounces the browser to the cloud /login on any
// auth-shaped error (Unauthenticated=16 / PermissionDenied=7 — see
// apps/admin/src/lib/auth-redirect.ts). Returning PermissionDenied here made
// visiting a gated page (e.g. /observability/alerts, /deployments/voice) log
// the user out instead of showing the upgrade banner. FailedPrecondition keeps
// the denial in-app.
type FeatureGateInterceptor struct {
	monitor     enterprise.LicenseMonitor
	featureKey  string
	featureName string // human-readable name for error messages
	edition     string // "ce" or "ee"
}

// NewFeatureGateInterceptor creates a new feature gate interceptor.
func NewFeatureGateInterceptor(monitor enterprise.LicenseMonitor, featureKey, featureName, edition string) *FeatureGateInterceptor {
	return &FeatureGateInterceptor{
		monitor:     monitor,
		featureKey:  featureKey,
		featureName: featureName,
		edition:     edition,
	}
}

func (i *FeatureGateInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := i.checkFeatureAccess(ctx, req.Spec().Procedure); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

func (i *FeatureGateInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn { return next(ctx, spec) }
}

func (i *FeatureGateInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if err := i.checkFeatureAccess(ctx, conn.Spec().Procedure); err != nil {
			return err
		}
		return next(ctx, conn)
	}
}

func (i *FeatureGateInterceptor) checkFeatureAccess(ctx context.Context, procedure string) error {
	// Dev build (-tags dev): everything unlocked
	if i.edition == "dev" || enterprise.IsDev() {
		return nil
	}

	// Managed/cloud tenants are provisioned by the control plane; their plan
	// feature gating is enforced by the cloud-side path, not the gateway-local
	// license machinery.
	if tenant.ConfigFromContext(ctx) != nil {
		return nil
	}

	// CE build: block only what the free plan excludes. The free plan grants
	// real limits for datasets/evals/annotations, so gating those off
	// categorically would contradict plans.json (editions-and-billing.md).
	if i.edition == "ce" {
		if enterprise.CEFeatureEnabled(i.featureKey) {
			return nil
		}
		logger.Debugf("feature_gate: blocking %s — %s is not available on the Community Edition", procedure, i.featureName)
		return featureDeniedError(fmt.Sprintf("%s is an Enterprise feature. Upgrade at https://everstack.ai/pricing to access it.", i.featureName))
	}

	// EE build: delegate to license monitor
	if i.monitor == nil {
		return nil // No monitor = no enforcement
	}

	if enabled, reason := i.monitor.IsFeatureEnabled(i.featureKey); !enabled {
		// EE without an active license runs with CE entitlements: fall back
		// to the free plan's feature set instead of blocking everything.
		state := i.monitor.GetLicenseState()
		if (state == nil || !state.Active) && enterprise.CEFeatureEnabled(i.featureKey) {
			return nil
		}
		logger.Debugf("feature_gate: blocking %s — %s not enabled: %s", procedure, i.featureKey, reason)
		return featureDeniedError(fmt.Sprintf("%s requires a higher plan. %s", i.featureName, reason))
	}

	return nil
}

// featureDeniedError builds the error returned when a feature gate blocks a
// request. It MUST use connect.CodeFailedPrecondition (not PermissionDenied /
// Unauthenticated): a plan/edition gate is an "upgrade to proceed" signal, and
// the admin FE transport logs the user out on any auth-shaped error
// (apps/admin/src/lib/auth-redirect.ts). See the type doc for the full story.
func featureDeniedError(msg string) error {
	return connect.NewError(connect.CodeFailedPrecondition, errMsg(msg))
}
