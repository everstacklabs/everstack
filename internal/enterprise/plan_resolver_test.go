package enterprise

import (
	"context"
	"testing"
	"time"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

type stubResolver struct {
	tier  string
	known bool
	calls int
}

func (s *stubResolver) PlanTier(context.Context, string) (string, bool) {
	s.calls++
	return s.tier, s.known
}

func verifiedTenantCtx(tenantID string) context.Context {
	return contextkeys.WithAuthenticatedAPIKey(context.Background(), tenantID, "verified-key-hash")
}

func withManagedGateway(t *testing.T) {
	t.Helper()
	SetManagedGateway(true)
	t.Cleanup(func() { SetManagedGateway(false) })
}

func withResolver(t *testing.T, r PlanTierResolver) {
	t.Helper()
	SetPlanTierResolver(r)
	t.Cleanup(func() { SetPlanTierResolver(nil) })
}

func withShadow(t *testing.T, on bool) {
	t.Helper()
	prev := ShadowEnforcement()
	SetShadowEnforcement(on)
	ResetShadowReportingForTest()
	t.Cleanup(func() { SetShadowEnforcement(prev) })
}

// Shadow mode is the whole safety story for this change: cloud tenants have
// been running uncapped, so resolving a tier must not start denying requests
// until someone reviews what would be denied.
func TestShadowModeResolvesTierButEnforcesNothing(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	withManagedGateway(t)
	withResolver(t, &stubResolver{tier: "free", known: true})
	withShadow(t, true)

	ent := ResolveEntitlements(verifiedTenantCtx("tenant-abc"), nil)

	if ent.Source != "managed-shadow" {
		t.Fatalf("Source = %q, want managed-shadow", ent.Source)
	}
	if ent.Tier != "free" {
		t.Fatalf("Tier = %q, want free (the tier must still be observable)", ent.Tier)
	}
	// The free plan caps agents. Shadow mode must still report unlimited.
	if _, capped := ent.Limit(UsageTypeAgents); capped {
		t.Fatal("shadow mode applied a limit; it must observe only")
	}
}

func TestEnforcementAppliesResolvedPlanLimits(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	withManagedGateway(t)
	withResolver(t, &stubResolver{tier: "free", known: true})
	withShadow(t, false)

	ent := ResolveEntitlements(verifiedTenantCtx("tenant-abc"), nil)

	if ent.Source != "managed-plan" {
		t.Fatalf("Source = %q, want managed-plan", ent.Source)
	}
	limit, capped := ent.Limit(UsageTypeAgents)
	if !capped {
		t.Fatal("enforcement mode did not apply the free plan's agent cap")
	}
	if limit <= 0 {
		t.Fatalf("agent limit = %d, want the positive free-plan cap", limit)
	}
}

// An unknown tenant must keep the old behaviour rather than being guessed into
// a tier, and a resolver failure must never deny a request.
func TestUnknownTenantFallsBackToBypass(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	withManagedGateway(t)
	withResolver(t, &stubResolver{known: false})
	withShadow(t, false)

	ent := ResolveEntitlements(verifiedTenantCtx("tenant-unknown"), nil)

	if ent.Source != "managed-bypass" {
		t.Fatalf("Source = %q, want managed-bypass", ent.Source)
	}
	if _, capped := ent.Limit(UsageTypeAgents); capped {
		t.Fatal("an unresolvable tenant must not be capped")
	}
}

func TestUnknownTierStringFallsBackToBypass(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	withManagedGateway(t)
	withResolver(t, &stubResolver{tier: "platinum", known: true})
	withShadow(t, false)

	ent := ResolveEntitlements(verifiedTenantCtx("tenant-abc"), nil)
	if ent.Source != "managed-bypass" {
		t.Fatalf("Source = %q, want managed-bypass for an unrecognised tier", ent.Source)
	}
}

// A bare tenant header is not authority. The spoofable same-origin path must
// not reach plan resolution at all.
func TestSpoofableTenantHeaderNeverResolvesAPlan(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	withManagedGateway(t)
	stub := &stubResolver{tier: "pro", known: true}
	withResolver(t, stub)
	withShadow(t, false)

	ctx := contextkeys.WithTenantAuthenticated(
		contextkeys.WithTenantID(context.Background(), "tenant-spoofed"),
	)
	ent := ResolveEntitlements(ctx, nil)

	if stub.calls != 0 {
		t.Fatalf("resolver consulted %d times for an unverified principal; want 0", stub.calls)
	}
	if ent.Source == "managed-plan" || ent.Source == "managed-shadow" {
		t.Fatalf("Source = %q: a spoofable header must not select a plan", ent.Source)
	}
}

// Self-hosted installs must be untouched by any of this.
func TestSelfHostedIsUnaffectedByTheResolver(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	stub := &stubResolver{tier: "pro", known: true}
	withResolver(t, stub)
	withShadow(t, false)
	// Deliberately NOT a managed gateway.

	ResolveEntitlements(verifiedTenantCtx("tenant-abc"), nil)
	if stub.calls != 0 {
		t.Fatalf("resolver consulted %d times on a self-hosted gateway; want 0", stub.calls)
	}
}

func TestCacheEntriesExpire(t *testing.T) {
	base := time.Now()
	now := base
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = time.Now })

	r := &dbPlanTierResolver{ttl: time.Minute, cache: map[string]planCacheEntry{}}
	r.store("tenant-abc", "pro", true)

	if _, ok := r.lookup("tenant-abc"); !ok {
		t.Fatal("entry missing immediately after store")
	}
	now = base.Add(2 * time.Minute)
	if _, ok := r.lookup("tenant-abc"); ok {
		t.Fatal("entry survived past its TTL; a plan change would never take effect")
	}
}

// Shadow reporting runs on a per-request path, so it must not log per request.
func TestShadowReportingIsDedupedPerTenantAndLimit(t *testing.T) {
	ResetShadowReportingForTest()
	limits := map[UsageType]int64{UsageTypeAgents: 3}

	reportShadowLimits("tenant-abc", "free", limits)
	if _, seen := shadowSeen.Load("tenant-abc|" + string(UsageTypeAgents)); !seen {
		t.Fatal("first report was not recorded")
	}

	count := 0
	shadowSeen.Range(func(any, any) bool { count++; return true })
	reportShadowLimits("tenant-abc", "free", limits)
	after := 0
	shadowSeen.Range(func(any, any) bool { after++; return true })
	if after != count {
		t.Fatalf("repeat report added entries: %d -> %d", count, after)
	}
}

func TestUnlimitedLimitsAreNotReported(t *testing.T) {
	ResetShadowReportingForTest()
	reportShadowLimits("tenant-abc", "enterprise", map[UsageType]int64{UsageTypeAgents: -1})

	count := 0
	shadowSeen.Range(func(any, any) bool { count++; return true })
	if count != 0 {
		t.Fatalf("unlimited entries were reported as would-apply limits (%d)", count)
	}
}
