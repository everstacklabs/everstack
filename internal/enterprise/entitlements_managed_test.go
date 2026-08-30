package enterprise

import (
	"context"
	"testing"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// TestManagedGatewayNeverFallsBackToCE pins the rule that a shared cloud
// gateway must not resolve an identified tenant through the self-hosted
// licensing path.
//
// The failure this guards against shipped silently: with no tenant config on
// the gateway pod and no instance licence, every tenant on everstack-prod
// resolved Source "ce", so paying customers ran on free-plan limits. The
// branch sits above the edition check on purpose, which is also why this test
// works in an untagged (CE) binary where the licence branch is unreachable.
func TestManagedGatewayNeverFallsBackToCE(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}

	withAPIKey := contextkeys.WithAuthenticatedAPIKey(context.Background(), "tenant-abc", "verified-key-hash")
	withSession := contextkeys.WithTenantID(context.Background(), "tenant-abc")
	withSession = contextkeys.WithCloudUserID(withSession, "user-abc")
	withSession = contextkeys.WithTenantAuthenticated(withSession)
	withSpoofableHeader := contextkeys.WithTenantID(context.Background(), "tenant-abc")
	withSpoofableHeader = contextkeys.WithTenantAuthenticated(withSpoofableHeader)
	anonymous := context.Background()

	t.Run("managed gateway, validated API key, bypasses CE", func(t *testing.T) {
		SetManagedGateway(true)
		t.Cleanup(func() { SetManagedGateway(false) })

		ent := ResolveEntitlements(withAPIKey, nil)
		if ent.Source != "managed-bypass" {
			t.Fatalf("Source = %q, want managed-bypass (a paying tenant must not inherit free-plan limits)", ent.Source)
		}
		if _, capped := ent.Limit(UsageTypeAgents); capped {
			t.Error("managed bypass must not carry caps")
		}
	})

	t.Run("managed gateway, validated user session, bypasses CE", func(t *testing.T) {
		SetManagedGateway(true)
		t.Cleanup(func() { SetManagedGateway(false) })

		ent := ResolveEntitlements(withSession, nil)
		if ent.Source != "managed-bypass" {
			t.Fatalf("Source = %q, want managed-bypass for a validated tenant session", ent.Source)
		}
	})

	t.Run("managed gateway, spoofable tenant header, keeps the stricter path", func(t *testing.T) {
		SetManagedGateway(true)
		t.Cleanup(func() { SetManagedGateway(false) })

		ent := ResolveEntitlements(withSpoofableHeader, nil)
		if ent.Source == "managed-bypass" {
			t.Fatal("tenant header without verified principal evidence inherited the managed bypass")
		}
		if _, capped := ent.Limit(UsageTypeAgents); !capped {
			t.Error("unverified tenant context must still resolve caps")
		}
	})

	t.Run("managed gateway, NO tenant identity, keeps the stricter path", func(t *testing.T) {
		SetManagedGateway(true)
		t.Cleanup(func() { SetManagedGateway(false) })

		// The hole to avoid: "managed mode -> unlimited" would hand unlimited
		// entitlements to exactly the requests that failed tenant resolution.
		ent := ResolveEntitlements(anonymous, nil)
		if ent.Source == "managed-bypass" {
			t.Fatal("a request with no tenant identity must not inherit the managed bypass")
		}
		if _, capped := ent.Limit(UsageTypeAgents); !capped {
			t.Error("unidentified requests must still resolve caps")
		}
	})

	t.Run("self-hosted gateway is unaffected", func(t *testing.T) {
		SetManagedGateway(false)

		// A tenant id on a self-hosted install (LocalScopeResolver injects one)
		// must still resolve CE, or self-hosted enforcement disappears.
		ent := ResolveEntitlements(withAPIKey, nil)
		if ent.Source != "ce" {
			t.Fatalf("Source = %q, want ce on a self-hosted gateway", ent.Source)
		}
	})
}
