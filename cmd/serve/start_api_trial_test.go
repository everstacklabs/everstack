package serve

import (
	"context"
	"github.com/everstacklabs/everstack/internal/enterprise"
	"testing"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

func TestIsManagedGatewayRecognizesEveryCloudTopology(t *testing.T) {
	t.Setenv("EVS_PLATFORM_DSN", "")
	if isManagedGateway(false) {
		t.Fatal("standalone gateway was classified as managed")
	}
	if !isManagedGateway(true) {
		t.Fatal("gateway with a shared database was not classified as managed")
	}

	t.Setenv("EVS_PLATFORM_DSN", "postgres://platform.example/everstack")
	if !isManagedGateway(false) {
		t.Fatal("gateway with EVS_PLATFORM_DSN was not classified as managed")
	}
}

func TestGatewayRuntimeContextMarksPlatformDSNTopologyAsShared(t *testing.T) {
	t.Setenv("EVS_PLATFORM_DSN", "postgres://platform.example/everstack")

	ctx, managed := gatewayRuntimeContext(context.Background(), false)
	if !managed {
		t.Fatal("gateway with EVS_PLATFORM_DSN was not classified as managed")
	}
	if shared, _ := ctx.Value(contextkeys.SharedGatewayMode).(bool); !shared {
		t.Fatal("managed gateway context did not enable tenant-scoped provider routing")
	}
}

func TestManagedBillingUsageURLSupportsStandaloneManagedGateway(t *testing.T) {
	t.Setenv("EVS_BILLING_USAGE_URL", "http://billing.internal:8092/api/billing/usage/sandbox-snapshot")
	if got := managedBillingUsageURL(nil); got != "http://billing.internal:8092/api/billing/usage/sandbox-snapshot" {
		t.Fatalf("managed billing usage URL = %q", got)
	}

	defaults := &EmbeddedDefaults{SharedBillingUsageURL: "http://loopback:8092/internal"}
	if got := managedBillingUsageURL(defaults); got != "http://loopback:8092/internal" {
		t.Fatalf("embedded billing usage URL = %q", got)
	}
}

func TestNewTrialManagerSkipsManagedGateway(t *testing.T) {
	manager, err := newTrialManager(context.Background(), true, nil)
	if err != nil {
		t.Fatalf("newTrialManager() error = %v", err)
	}
	if manager != nil {
		t.Fatal("newTrialManager() created a shared trial manager for a managed gateway")
	}
}

func TestNewTrialManagerInitializesSelfHostedGateway(t *testing.T) {
	manager, err := newTrialManager(context.Background(), false, nil)
	if err != nil {
		t.Fatalf("newTrialManager() error = %v", err)
	}
	if manager == nil {
		t.Fatal("newTrialManager() returned nil for a self-hosted gateway")
	}
	if !manager.IsActive() {
		t.Fatal("newTrialManager() returned an inactive self-hosted trial manager")
	}
}

func TestShouldStartSelfHostedCloudLicensingMatchesBuildEdition(t *testing.T) {
	if shouldStartSelfHostedCloudLicensing(true) {
		t.Fatal("managed gateway enabled self-hosted cloud licensing")
	}

	want := enterprise.Edition() != "ce"
	if got := shouldStartSelfHostedCloudLicensing(false); got != want {
		t.Fatalf("shouldStartSelfHostedCloudLicensing(false) = %t, want %t for %s edition", got, want, enterprise.Edition())
	}
}

// The inference usage reporter must post request/token totals to billing's
// usage snapshot endpoint, not the sandbox heartbeat endpoint the sandbox
// reporter is configured with: the sandbox handler decodes no token fields,
// so totals posted there are silently dropped (it returns 200).
func TestManagedInferenceUsageURLTargetsUsageSnapshot(t *testing.T) {
	t.Setenv("EVS_INFERENCE_USAGE_URL", "")
	t.Setenv("EVS_BILLING_USAGE_URL", "http://billing.internal:8092/api/billing/usage/sandbox-snapshot")
	if got, want := managedInferenceUsageURL(nil), "http://billing.internal:8092/api/billing/usage/snapshot"; got != want {
		t.Fatalf("inference usage URL from env = %q, want %q", got, want)
	}

	defaults := &EmbeddedDefaults{SharedBillingUsageURL: "http://127.0.0.1:8092/api/billing/usage/sandbox-snapshot"}
	if got, want := managedInferenceUsageURL(defaults), "http://127.0.0.1:8092/api/billing/usage/snapshot"; got != want {
		t.Fatalf("inference usage URL from embedded defaults = %q, want %q", got, want)
	}

	// An endpoint that is not the sandbox heartbeat cannot be rewritten
	// safely; report nothing rather than post totals somewhere they are lost.
	t.Setenv("EVS_BILLING_USAGE_URL", "http://billing.internal:8092/internal")
	if got := managedInferenceUsageURL(nil); got != "" {
		t.Fatalf("inference usage URL for unknown endpoint shape = %q, want empty", got)
	}

	t.Setenv("EVS_INFERENCE_USAGE_URL", "http://override:8092/api/billing/usage/snapshot")
	if got, want := managedInferenceUsageURL(nil), "http://override:8092/api/billing/usage/snapshot"; got != want {
		t.Fatalf("explicit inference usage URL = %q, want %q", got, want)
	}

	t.Setenv("EVS_INFERENCE_USAGE_URL", "")
	t.Setenv("EVS_BILLING_USAGE_URL", "")
	if got := managedInferenceUsageURL(nil); got != "" {
		t.Fatalf("inference usage URL with no billing endpoint = %q, want empty", got)
	}
}
