package plans

import (
	"errors"
	"testing"
)

func catalog(t *testing.T) *PlansConfig {
	t.Helper()
	pc, err := Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}
	return pc
}

// The property this whole package exists for. GetPlanLimits returns nil for an
// unknown tier, and a nil limit map reads downstream as "no limits apply" --
// which is how managed tenants came to run uncapped. ResolveEntitlements must
// never do that.
func TestResolveEntitlementsFailsClosedOnUnknownTier(t *testing.T) {
	pc := catalog(t)

	for _, tier := range []string{"nonexistent", "", "  ", "Scale", "enterprise-plus"} {
		ent, err := pc.ResolveEntitlements(tier)

		if err == nil {
			t.Fatalf("tier %q: expected an error", tier)
		}
		if !errors.Is(err, ErrUnknownPlan) {
			t.Fatalf("tier %q: error = %v, want ErrUnknownPlan", tier, err)
		}
		// The load-bearing assertion: usable limits come back ANYWAY, so a
		// caller that ignores the error is still bounded.
		if ent == nil {
			t.Fatalf("tier %q: entitlements are nil; an ignoring caller would be unbounded", tier)
		}
		if !ent.Degraded {
			t.Fatalf("tier %q: Degraded = false, want true", tier)
		}
		if len(ent.UsageLimits) == 0 {
			t.Fatalf("tier %q: no usage limits returned", tier)
		}
		if ent.Tier != SafeTier {
			t.Fatalf("tier %q: fell back to %q, want %q", tier, ent.Tier, SafeTier)
		}
	}
}

// Compare against GetPlanLimits so the difference in behaviour is pinned rather
// than described. If someone "fixes" GetPlanLimits to fail closed, this test
// says so instead of silently passing.
func TestGetPlanLimitsStillReturnsNilForUnknownTier(t *testing.T) {
	pc := catalog(t)

	if got := pc.GetPlanLimits("nonexistent"); got != nil {
		t.Fatalf("GetPlanLimits(unknown) = %#v, want nil; if this changed deliberately, update the resolver's doc comment", got)
	}
	ent, err := pc.ResolveEntitlements("nonexistent")
	if ent == nil || err == nil {
		t.Fatal("ResolveEntitlements must return both limits and an error")
	}
}

func TestResolveEntitlementsReturnsEveryDeclaredLimit(t *testing.T) {
	pc := catalog(t)

	ent, err := pc.ResolveEntitlements("free")
	if err != nil {
		t.Fatalf("ResolveEntitlements(free) error = %v", err)
	}

	plan, _ := pc.GetPlan("free")
	if len(ent.UsageLimits) != len(plan.UsageLimits) {
		t.Fatalf("resolved %d limits, catalog declares %d", len(ent.UsageLimits), len(plan.UsageLimits))
	}

	// Spot-check the expensive ones the CE trial manager does not implement.
	// These are exactly the limits a trial user is currently unbounded on.
	for _, name := range []string{
		"CONCURRENT_SANDBOXES", "CONCURRENT_BROWSERS", "SANDBOX_MEMORY_MB",
		"PERSISTENT_AGENTS", "BROWSER_SESSION_MAX_SECONDS", "STORAGE_BYTES",
	} {
		if _, ok := ent.Limit(name); !ok {
			t.Errorf("free tier is missing %s", name)
		}
	}
}

// -1 means unlimited and an absent key means undeclared. Collapsing the two is
// how "nobody said" becomes "unlimited".
func TestLimitDistinguishesUnlimitedFromUndeclared(t *testing.T) {
	ent := &Entitlements{UsageLimits: map[string]int64{"UNLIMITED_THING": -1}}

	if v, ok := ent.Limit("UNLIMITED_THING"); !ok || v != -1 {
		t.Fatalf("declared unlimited: got (%d, %v), want (-1, true)", v, ok)
	}
	if _, ok := ent.Limit("NEVER_DECLARED"); ok {
		t.Fatal("undeclared limit reported as declared")
	}

	var nilEnt *Entitlements
	if _, ok := nilEnt.Limit("ANY"); ok {
		t.Fatal("nil entitlements reported a declared limit")
	}
}

// Every plan must state its trial policy explicitly. An absent key means nobody
// decided, which is a configuration error rather than a perpetual plan.
func TestEveryPlanDeclaresATrialPolicyExplicitly(t *testing.T) {
	raw, err := plansJSON()
	if err != nil {
		t.Fatalf("read embedded plans: %v", err)
	}
	for _, tier := range []string{"free", "basic", "pro", "enterprise"} {
		if !hasExplicitTrialKey(raw, tier) {
			t.Errorf("plan %q has no explicit \"trial\" key; null is required, absence is ambiguous", tier)
		}
	}

	pc := catalog(t)
	for _, tier := range []string{"free", "basic", "pro", "enterprise"} {
		ent, err := pc.ResolveEntitlements(tier)
		if err != nil {
			t.Fatalf("ResolveEntitlements(%s) error = %v", tier, err)
		}
		if !ent.IsPerpetual() {
			t.Errorf("plan %q declares a trial; no plan offers one today, so this needs a pricing decision recorded first", tier)
		}
	}
}
