package serve

import (
	"context"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/internal/lib/pgscratch"
)

// The sandbox and browser caps resolve the same organization scope as every
// other plan cap: an instance id widens to its organization's ids, while an
// orphan or self-hosted tenant keeps counting itself alone.
func TestPGManagedSandboxConcurrencyScopeIsTheOrganization(t *testing.T) {
	db := pgscratch.New(t, "sandbox_scope_wiring")
	pgscratch.MustExec(t, db, resourcePlatformSchema,
		`INSERT INTO everstack.organizations (id) VALUES ('`+resOrgA+`')`,
		`INSERT INTO everstack.managed_instances (id, organization_id) VALUES ('`+resInstA+`', '`+resOrgA+`')`,
	)

	if _, ok := managedSandboxConcurrencyScope(context.Background(), resInstA); ok {
		t.Fatal("off a managed gateway the scope must stay per tenant")
	}

	enterprise.SetManagedGateway(true)
	enterprise.SetPlanTierResolver(enterprise.NewDBPlanTierResolver(db, time.Minute))
	t.Cleanup(func() {
		enterprise.SetManagedGateway(false)
		enterprise.SetPlanTierResolver(nil)
	})

	scope, ok := managedSandboxConcurrencyScope(context.Background(), resInstA)
	if !ok || scope.Key != "org:"+resOrgA || !scope.Contains(resOrgA) || !scope.Contains(resInstA) {
		t.Fatalf("instance scope = %+v, %v; want organization %s with both ids", scope, ok, resOrgA)
	}

	// Org-wide counting is an activity cap: shadow (reported only) unless
	// EVS_PLAN_LIMIT_ENFORCEMENT_ACTIVITY=enforce and the tier is enforced.
	if !scope.ShadowOnly {
		t.Fatal("org-wide sandbox counting must be shadow by default")
	}
	t.Setenv("EVS_PLAN_LIMIT_ENFORCEMENT_ACTIVITY", "enforce")
	if scope, _ := managedSandboxConcurrencyScope(context.Background(), resInstA); !scope.ShadowOnly {
		t.Fatal("activity switch on but the tier in shadow: must stay shadow")
	}
	enterprise.SetEnforcementPolicy(true, nil)
	t.Cleanup(func() { enterprise.SetShadowEnforcement(true) })
	if scope, _ := managedSandboxConcurrencyScope(context.Background(), resInstA); scope.ShadowOnly {
		t.Fatal("activity switch on and tier enforced: org-wide counting must be enforced")
	}
	t.Setenv("EVS_PLAN_LIMIT_ENFORCEMENT_ACTIVITY", "shadow")
	if scope, _ := managedSandboxConcurrencyScope(context.Background(), resInstA); !scope.ShadowOnly {
		t.Fatal("activity switch set to shadow: org-wide counting must be shadow")
	}
	if scope.Contains(resOrphan) {
		t.Fatal("scope leaked an unrelated tenant")
	}
	if _, ok := managedSandboxConcurrencyScope(context.Background(), resOrphan); ok {
		t.Fatal("an orphan tenant must keep the per-tenant count")
	}
}
