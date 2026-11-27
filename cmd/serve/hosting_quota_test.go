package serve

import (
	"context"
	"errors"
	"testing"

	"github.com/everstacklabs/everstack/internal/enterprise"
)

type hostingScopeStub struct{}

func (hostingScopeStub) PlanTier(context.Context, string) (string, bool) { return "free", true }

func (hostingScopeStub) ResolveOrgScope(context.Context, string) (enterprise.OrgScope, error) {
	return enterprise.OrgScope{OrganizationID: "org-1", Tier: "free", Aliases: []string{"org-1", "inst-1"}}, nil
}

// Hosting quotas come from the organization plan only once that plan is
// enforced; in shadow (and self-hosted) hosting stays unlimited as before.
func TestManagedHostingQuotaFollowsTheEnforcedPlan(t *testing.T) {
	if enterprise.IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	if q, err := managedHostingQuota(context.Background(), "inst-1"); err != nil || q.MaxSites != -1 || q.MaxStorageBytes != -1 {
		t.Fatalf("self-hosted quota = %+v, %v; want unlimited", q, err)
	}

	enterprise.SetManagedGateway(true)
	enterprise.SetPlanTierResolver(hostingScopeStub{})
	enterprise.SetShadowEnforcement(true)
	t.Cleanup(func() {
		enterprise.SetManagedGateway(false)
		enterprise.SetPlanTierResolver(nil)
		enterprise.SetShadowEnforcement(true)
	})
	if q, _ := managedHostingQuota(context.Background(), "inst-1"); q.MaxSites != -1 {
		t.Fatalf("shadow quota = %+v, want unlimited", q)
	}

	enterprise.SetEnforcementPolicy(true, nil)
	q, err := managedHostingQuota(context.Background(), "inst-1")
	if err != nil || q.MaxSites != 3 || q.MaxStorageBytes != 524288000 || q.ScopeKey != "org:org-1" || len(q.ScopeTenantIDs) != 2 {
		t.Fatalf("enforced free quota = %+v, %v; want 3 sites, 500 MiB, organization scope", q, err)
	}
}

type hostingOutageStub struct{}

func (hostingOutageStub) PlanTier(context.Context, string) (string, bool) { return "", false }

func (hostingOutageStub) ResolveOrgScope(context.Context, string) (enterprise.OrgScope, error) {
	return enterprise.OrgScope{}, errors.New("connection refused")
}

// A failed organization lookup refuses the publish (retryable) rather than
// checking the quota against the tenant's own sites only.
func TestManagedHostingQuotaFailsClosedOnLookupError(t *testing.T) {
	if enterprise.IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	enterprise.SetManagedGateway(true)
	enterprise.SetPlanTierResolver(hostingOutageStub{})
	enterprise.SetEnforcementPolicy(true, nil)
	t.Cleanup(func() {
		enterprise.SetManagedGateway(false)
		enterprise.SetPlanTierResolver(nil)
		enterprise.SetShadowEnforcement(true)
	})
	if _, err := managedHostingQuota(context.Background(), "inst-1"); !errors.Is(err, enterprise.ErrLimitCheckUnavailable) {
		t.Fatalf("err = %v, want ErrLimitCheckUnavailable", err)
	}
}

type hostingDeletedInstanceStub struct{}

func (hostingDeletedInstanceStub) PlanTier(context.Context, string) (string, bool) { return "free", true }

func (hostingDeletedInstanceStub) ResolveOrgScope(context.Context, string) (enterprise.OrgScope, error) {
	return enterprise.OrgScope{OrganizationID: "org-1", Tier: "free",
		Aliases: []string{"org-1", "inst-live"}, DeletedAliases: []string{"inst-deleted"}}, nil
}

// Sites of a deleted instance keep counting toward the organization's hosted
// site quota until the takedown sweep has removed them (a failed takedown
// leaves the site deleting, still counted), so deleting an instance cannot be
// used to publish past the cap.
func TestManagedHostingQuotaCountsDeletedInstancesUntilTakenDown(t *testing.T) {
	if enterprise.IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	enterprise.SetManagedGateway(true)
	enterprise.SetPlanTierResolver(hostingDeletedInstanceStub{})
	enterprise.SetEnforcementPolicy(true, nil)
	t.Cleanup(func() {
		enterprise.SetManagedGateway(false)
		enterprise.SetPlanTierResolver(nil)
		enterprise.SetShadowEnforcement(true)
	})
	q, err := managedHostingQuota(context.Background(), "inst-live")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"org-1": true, "inst-live": true, "inst-deleted": true}
	if len(q.ScopeTenantIDs) != len(want) {
		t.Fatalf("scope = %v, want the live ids plus the deleted instance", q.ScopeTenantIDs)
	}
	for _, id := range q.ScopeTenantIDs {
		if !want[id] {
			t.Fatalf("scope = %v contains %q", q.ScopeTenantIDs, id)
		}
	}
}
