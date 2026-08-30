package enterprise

import (
	"context"
	"strings"
	"testing"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/pkg/tenant"
)

// stubMonitor lets tests control the license state; everything else is noop.
type stubMonitor struct {
	noopLicenseMonitor
	state *LicenseState
}

func (s stubMonitor) GetLicenseState() *LicenseState { return s.state }

func managedCtx() context.Context {
	return tenant.WithConfig(context.Background(), &tenant.Config{InstanceID: "inst-cloud"})
}

func TestShouldEnforceCELimitsManagedBypass(t *testing.T) {
	// Managed/cloud tenants never get the CE fallback, in any edition.
	if ShouldEnforceCELimits(managedCtx(), nil) {
		t.Fatal("managed tenant must bypass CE limits (nil monitor)")
	}
	if ShouldEnforceCELimits(managedCtx(), stubMonitor{}) {
		t.Fatal("managed tenant must bypass CE limits (no license state)")
	}
}

func TestShouldEnforceCELimitsSelfHosted(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds never enforce CE limits")
	}
	ctx := context.Background()
	if !ShouldEnforceCELimits(ctx, nil) {
		t.Fatal("self-hosted without monitor must enforce CE limits")
	}
	if !ShouldEnforceCELimits(ctx, stubMonitor{state: &LicenseState{Active: false, Status: "unlicensed", Tier: "free"}}) {
		t.Fatal("self-hosted unlicensed must enforce CE limits")
	}
	if Edition() == "ee" {
		if ShouldEnforceCELimits(ctx, stubMonitor{state: &LicenseState{Active: true, Status: "active", Tier: "pro"}}) {
			t.Fatal("EE with an active license must not enforce CE limits")
		}
	}
}

func TestCheckCELimitZeroMeansUnavailable(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds never enforce CE limits")
	}
	err := CheckCELimit(context.Background(), nil, nil, "SELECT 1", nil, 0, "channel binding")
	if err == nil {
		t.Fatal("limit 0 must mean 'not available', got nil")
	}
	if !strings.Contains(err.Error(), "not available") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCheckCELimitNegativeMeansUnlimited(t *testing.T) {
	if err := CheckCELimit(context.Background(), nil, nil, "SELECT 1", nil, -1, "agent"); err != nil {
		t.Fatalf("limit -1 must mean unlimited, got: %v", err)
	}
}

func TestCheckPlanLimitManagedBypass(t *testing.T) {
	// Even with no monitor and a zero CE limit, managed tenants pass.
	if err := CheckPlanLimit(managedCtx(), nil, nil, UsageTypeChannelBindings, "SELECT 1", nil, 0, "channel binding"); err != nil {
		t.Fatalf("managed tenant must bypass plan limits: %v", err)
	}
}

func TestCheckPlanLimitEmptyLimitsFailsClosed(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds never enforce CE limits")
	}
	// An ACTIVE license whose state carries zero usage limits means the plan
	// data never arrived; the CE cap must still apply (fail closed), and it
	// must apply through the direct path, not the CheckCELimit gate (which
	// would see the active license and skip).
	mon := stubMonitor{state: &LicenseState{Active: true, Status: "active", Tier: "weird-unknown-tier"}}
	err := CheckPlanLimit(context.Background(), nil, mon, UsageTypeChannelBindings, "SELECT 1", nil, 0, "channel binding")
	if err == nil {
		t.Fatal("active license with empty limit data must fail closed to the CE cap")
	}
}

func TestCheckPlanLimitExplicitValues(t *testing.T) {
	ctx := context.Background()

	// Explicit 0: resource not on this plan.
	mon := stubMonitor{state: &LicenseState{
		Active: true, Status: "active", Tier: "basic",
		UsageLimits: []UsageLimit{{Type: UsageTypeChannelBindings, Limit: 0}},
	}}
	if err := CheckPlanLimit(ctx, nil, mon, UsageTypeChannelBindings, "SELECT 1", nil, 3, "channel binding"); err == nil {
		t.Fatal("explicit 0 must block the resource")
	}

	// Explicit -1: unlimited.
	mon = stubMonitor{state: &LicenseState{
		Active: true, Status: "active", Tier: "enterprise",
		UsageLimits: []UsageLimit{{Type: UsageTypeAgents, Limit: -1}},
	}}
	if err := CheckPlanLimit(ctx, nil, mon, UsageTypeAgents, "SELECT 1", nil, 3, "agent"); err != nil {
		t.Fatalf("explicit -1 must mean unlimited: %v", err)
	}

	// Non-empty list omitting the type: intentional, unlimited.
	mon = stubMonitor{state: &LicenseState{
		Active: true, Status: "active", Tier: "pro",
		UsageLimits: []UsageLimit{{Type: UsageTypeRPM, Limit: 6000}},
	}}
	if err := CheckPlanLimit(ctx, nil, mon, UsageTypeAgents, "SELECT 1", nil, 3, "agent"); err != nil {
		t.Fatalf("type omitted from a non-empty list must mean unlimited: %v", err)
	}
}

// The gateway pod never receives tenant config, so an identified cloud tenant
// reached the CE fallback below the config check and paying customers were held
// to free-plan caps. Workflow creation was the live caller
// (internal/api/grpc/workflows/v1/workflows.go). Recognising the verified
// managed principal closes that second route to the same rule.
func TestShouldEnforceCELimitsManagedGatewayWithoutTenantConfig(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds never enforce CE limits")
	}
	SetManagedGateway(true)
	t.Cleanup(func() { SetManagedGateway(false) })

	verified := verifiedTenantCtx("tenant-abc")
	if ShouldEnforceCELimits(verified, nil) {
		t.Fatal("a verified cloud tenant must not be held to CE caps (nil monitor)")
	}
	if ShouldEnforceCELimits(verified, stubMonitor{}) {
		t.Fatal("a verified cloud tenant must not be held to CE caps (no license state)")
	}
}

// The loosening must not extend to a spoofable header, or anyone could shed CE
// caps by setting x-tenant-id.
func TestShouldEnforceCELimitsIgnoresSpoofableTenantHeader(t *testing.T) {
	if IsDev() || Edition() != "ce" {
		t.Skip("needs a CE build to observe the enforcing branch")
	}
	SetManagedGateway(true)
	t.Cleanup(func() { SetManagedGateway(false) })

	spoofed := contextkeys.WithTenantAuthenticated(
		contextkeys.WithTenantID(context.Background(), "tenant-spoofed"),
	)
	if !ShouldEnforceCELimits(spoofed, nil) {
		t.Fatal("a bare tenant header must not shed CE limits")
	}
}
