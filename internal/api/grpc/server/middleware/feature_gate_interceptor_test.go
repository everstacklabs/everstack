package middleware

import (
	"context"
	"testing"

	"connectrpc.com/connect"
)

// TestFeatureDeniedErrorIsNotAuthShaped guards the contract that a feature-gate
// denial must NOT use an auth-shaped Connect code. The admin FE transport
// bounces the browser to the cloud /login on Unauthenticated (16) or
// PermissionDenied (7) — see apps/admin/src/lib/auth-redirect.ts. Returning
// either here logged the user out when they visited a gated page
// (/observability/alerts, /deployments/voice) instead of surfacing the upgrade
// prompt. FailedPrecondition (9) keeps the denial in-app.
//
// We assert on featureDeniedError directly because the checkFeatureAccess
// block path is unreachable under default build tags: enterprise.Edition()
// defaults to "dev", so enterprise.IsDev() short-circuits to allow.
func TestFeatureDeniedErrorIsNotAuthShaped(t *testing.T) {
	err := featureDeniedError("Alerts requires a higher plan.")
	if err == nil {
		t.Fatal("featureDeniedError returned nil")
	}

	switch code := connect.CodeOf(err); code {
	case connect.CodePermissionDenied, connect.CodeUnauthenticated:
		t.Fatalf("feature-gate denial used auth-shaped code %v — this logs the user out on the FE", code)
	case connect.CodeFailedPrecondition:
		// expected
	default:
		t.Fatalf("expected CodeFailedPrecondition, got %v", code)
	}
}

// TestFeatureGateDevBypass confirms dev builds never block (belt-and-suspenders
// for the default-tags path).
func TestFeatureGateDevBypass(t *testing.T) {
	i := NewFeatureGateInterceptor(nil, "alerts", "Alerts", "dev")
	if err := i.checkFeatureAccess(context.Background(), "/everstack.alerts.v1.AlertsService/ListAlertRules"); err != nil {
		t.Fatalf("dev build must not block gated features, got %v", err)
	}
}
