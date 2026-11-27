package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

type spawnTierStub struct{ tier string }

func (s spawnTierStub) PlanTier(context.Context, string) (string, bool) { return s.tier, true }

func enforcedManagedTier(t *testing.T, tier string) context.Context {
	t.Helper()
	if enterprise.IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	enterprise.SetManagedGateway(true)
	enterprise.SetPlanTierResolver(spawnTierStub{tier: tier})
	enterprise.SetEnforcementPolicy(true, nil)
	enterprise.SetActivityEnforcement(true)
	t.Cleanup(func() {
		enterprise.SetManagedGateway(false)
		enterprise.SetPlanTierResolver(nil)
		enterprise.SetShadowEnforcement(true)
		enterprise.SetActivityEnforcement(false)
	})
	return contextkeys.WithAuthenticatedAPIKey(context.Background(), "tenant-a", "verified-key-hash")
}

// SPAWN_DEPTH caps how deep sub-agents nest on Cloud: the free plan's 1 lets
// the root spawn children that cannot spawn further, and the refusal names
// the plan and the upgrade path.
func TestPlanSpawnConfigClampsDepthToThePlan(t *testing.T) {
	ctx := enforcedManagedTier(t, "free")
	cfg := DefaultSpawnConfig()
	cfg.Enabled = true

	capped := PlanSpawnConfig(ctx, cfg)
	if capped.MaxDepth != 1 || !capped.PlanCapped {
		t.Fatalf("free plan config = depth %d capped %v, want 1 true", capped.MaxDepth, capped.PlanCapped)
	}
	root := NewSpawnTracker("tree", capped)
	if err := root.CanSpawn(); err != nil {
		t.Fatalf("the root may spawn on depth 1: %v", err)
	}
	err := root.ChildTracker("node").CanSpawn()
	if err == nil || !strings.Contains(err.Error(), "your plan allows 1 level") || !strings.Contains(err.Error(), enterprise.UpgradeURL) {
		t.Fatalf("child refusal = %v, want the plan depth message with an upgrade link", err)
	}

	// An agent configured shallower than its plan keeps its own setting and
	// the generic message.
	shallow := cfg
	shallow.MaxDepth = 1
	if got := PlanSpawnConfig(enforcedManagedTier(t, "pro"), shallow); got.MaxDepth != 1 || got.PlanCapped {
		t.Fatalf("pro plan with agent depth 1 = %+v, want the agent's own setting", got)
	}
}

func TestPlanSpawnConfigLeavesSelfHostedAlone(t *testing.T) {
	cfg := DefaultSpawnConfig()
	if got := PlanSpawnConfig(context.Background(), cfg); got.MaxDepth != cfg.MaxDepth || got.PlanCapped {
		t.Fatalf("self-hosted config changed: %+v", got)
	}
}

// SPAWN_DEPTH is an activity cap: while the activity class is in shadow
// (the default), an enforced creation policy leaves spawn depth untouched.
func TestPlanSpawnConfigIsShadowUntilActivityCapsAreEnforced(t *testing.T) {
	ctx := enforcedManagedTier(t, "free")
	enterprise.SetActivityEnforcement(false)
	cfg := DefaultSpawnConfig()
	if got := PlanSpawnConfig(ctx, cfg); got.MaxDepth != cfg.MaxDepth || got.PlanCapped {
		t.Fatalf("activity shadow changed spawn depth: %+v", got)
	}
}
