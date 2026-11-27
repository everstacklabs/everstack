package enterprise

import (
	"context"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/billingidentity"
	"github.com/jmoiron/sqlx"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// The refusal is read by customers: it must say what was refused, the cap and
// their count, and where to upgrade, in plain text with no em dash.
func TestLimitErrorMessagesAreCustomerFacing(t *testing.T) {
	cases := []struct {
		name string
		err  *LimitError
		want string
	}{
		{
			name: "at cap on a plan",
			err:  &LimitError{Type: UsageTypeAgents, Resource: "agent", Limit: 3, Used: 3, Requested: 1, Tier: "free"},
			want: "Agent limit reached: your plan includes 3 and you have 3. Upgrade your plan to add more agents: https://everstack.ai/pricing",
		},
		{
			name: "grandfathered over cap",
			err:  &LimitError{Type: UsageTypeAgents, Resource: "agent", Limit: 3, Used: 7, Requested: 1, Tier: "free"},
			want: "Agent limit reached: your plan includes 3 and you have 7. Upgrade your plan to add more agents: https://everstack.ai/pricing",
		},
		{
			name: "batch that would not fit",
			err:  &LimitError{Type: UsageTypeDatasetItems, Resource: "dataset item", Limit: 1000, Used: 998, Requested: 5, Tier: "free"},
			want: "Dataset item limit reached: your plan includes 1000 and you have 998, so 5 more would not fit. Upgrade your plan to add more dataset items: https://everstack.ai/pricing",
		},
		{
			name: "not included in the plan",
			err:  &LimitError{Type: UsageTypeChannels, Resource: "channel", Limit: 0, Requested: 1, Tier: "free"},
			want: "Channels are not included in your current plan. Upgrade your plan to use them: https://everstack.ai/pricing",
		},
		{
			name: "community edition",
			err:  &LimitError{Type: UsageTypeAgents, Resource: "agent", Limit: 3, Used: 3, Requested: 1, Tier: "free", CommunityEdition: true},
			want: "Agent limit reached: the Community Edition includes 3 and you have 3. Upgrade for higher limits: https://everstack.ai/pricing",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.err.Error(); got != tc.want {
				t.Fatalf("message\n got: %q\nwant: %q", got, tc.want)
			}
			if strings.ContainsRune(tc.err.Error(), '\u2014') {
				t.Fatal("message carries an em dash")
			}
			if !errors.Is(tc.err, ErrResourceLimit) {
				t.Fatal("a LimitError must match ErrResourceLimit")
			}
		})
	}
}

// Clients recognise a cap structurally, not by parsing the message.
func TestLimitErrorInfoCarriesTheCap(t *testing.T) {
	info := (&LimitError{Type: UsageTypeAgents, Resource: "agent", Limit: 3, Used: 7, Requested: 1, Tier: "free"}).ErrorInfo()
	if info.GetReason() != PlanLimitReason || info.GetDomain() != "everstack.ai" {
		t.Fatalf("reason/domain = %q/%q", info.GetReason(), info.GetDomain())
	}
	md := info.GetMetadata()
	for k, want := range map[string]string{
		"limit_type": "AGENTS", "limit": "3", "used": "7", "requested": "1", "tier": "free",
		"upgrade_url": UpgradeURL, "resource": "agent",
	} {
		if md[k] != want {
			t.Errorf("metadata[%q] = %q, want %q", k, md[k], want)
		}
	}
}

// Token, request and storage volume are display only on Cloud: an enforced
// managed plan must not carry them, so no existing STORAGE_BYTES check starts
// refusing writes when enforcement turns on.
func TestEnforcedManagedPlanOmitsDisplayOnlyUsageTypes(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	withManagedGateway(t)
	withResolver(t, &stubResolver{tier: "free", known: true})
	SetEnforcementPolicy(true, nil)
	t.Cleanup(func() { SetShadowEnforcement(true) })

	ent := ResolveEntitlements(verifiedTenantCtx("tenant-abc"), nil)
	if ent.Source != "managed-plan" {
		t.Fatalf("Source = %q, want managed-plan", ent.Source)
	}
	for _, ut := range []UsageType{UsageTypeTokens, UsageTypeRequests, UsageTypeStorageBytes} {
		if _, capped := ent.Limit(ut); capped {
			t.Errorf("%s is capped on an enforced managed plan; it must stay display only", ut)
		}
	}
	for _, ut := range []UsageType{UsageTypeAgents, UsageTypeHostedSites} {
		if _, capped := ent.Limit(ut); !capped {
			t.Errorf("%s must be capped on an enforced free plan", ut)
		}
	}
}

// Owner decision 2026-10-06: creation caps are enforced, activity caps stay
// shadow (logged only) behind their own switch, so they can be turned on
// later without touching the creation caps.
func TestActivityCapsHaveTheirOwnSwitch(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	withManagedGateway(t)
	withResolver(t, &stubResolver{tier: "free", known: true})
	SetEnforcementPolicy(true, nil)
	t.Cleanup(func() { SetShadowEnforcement(true); SetActivityEnforcement(false) })

	creation := []UsageType{UsageTypeAgents, UsageTypePersistentAgents, UsageTypeDatasetItems,
		UsageTypeEvalRunsMonthly, UsageTypeAnnotationQueues, UsageTypeChannels, UsageTypeChannelBindings,
		UsageTypeHostedSites, UsageTypeHostingStorageBytes}
	activity := []UsageType{UsageTypeMessagesMonthly, UsageTypeConcurrentRunning,
		UsageTypeConcurrentSandboxes, UsageTypeConcurrentBrowsers, UsageTypeSpawnDepth}

	ent := ResolveEntitlements(verifiedTenantCtx("tenant-activity"), nil)
	for _, ut := range creation {
		if _, capped := ent.Limit(ut); !capped {
			t.Errorf("creation cap %s must be enforced", ut)
		}
	}
	for _, ut := range activity {
		if _, capped := ent.Limit(ut); capped {
			t.Errorf("activity cap %s must stay shadow by default", ut)
		}
	}

	SetActivityEnforcement(true)
	ent = ResolveEntitlements(verifiedTenantCtx("tenant-activity"), nil)
	for _, ut := range append(creation, activity...) {
		if _, capped := ent.Limit(ut); !capped {
			t.Errorf("%s must be enforced once the activity switch is on", ut)
		}
	}

	for raw, want := range map[string]bool{"": false, "shadow": false, " ENFORCE ": true} {
		if got, err := ParseActivityEnforcement(raw); err != nil || got != want {
			t.Errorf("ParseActivityEnforcement(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	if _, err := ParseActivityEnforcement("on"); err == nil {
		t.Error("an unknown activity mode must be an error")
	}
}

// An internal (exempt) organization skips plan caps entirely: creation and
// activity, in the request path and in hosting. The flag comes only from the
// organization lookup.
func TestExemptOrganizationSkipsPlanCaps(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	withManagedGateway(t)
	withResolver(t, exemptStub{exempt: true})
	SetEnforcementPolicy(true, nil)
	SetActivityEnforcement(true)
	t.Cleanup(func() { SetShadowEnforcement(true); SetActivityEnforcement(false) })

	ent := ResolveEntitlements(verifiedTenantCtx("tenant-internal"), nil)
	if ent.Source != "managed-exempt" || ent.Limits != nil {
		t.Fatalf("exempt org resolved %q with %v, want managed-exempt and no limits", ent.Source, ent.Limits)
	}
	if err := CheckLimit(verifiedTenantCtx("tenant-internal"), nil, nil, AgentsLimit("tenant-internal")); err != nil {
		t.Fatalf("exempt org refused: %v", err)
	}
	if _, _, ok, _ := EnforcedManagedPlan(context.Background(), "tenant-internal"); ok {
		t.Fatal("hosting must not apply a quota to an exempt org")
	}

	withResolver(t, exemptStub{exempt: false})
	if ent := ResolveEntitlements(verifiedTenantCtx("tenant-customer"), nil); ent.Source != "managed-plan" {
		t.Fatalf("non-exempt org resolved %q, want managed-plan", ent.Source)
	}
}

type exemptStub struct{ exempt bool }

func (s exemptStub) PlanTier(context.Context, string) (string, bool) { return "free", true }

func (s exemptStub) ResolveOrgScope(_ context.Context, tenantID string) (OrgScope, error) {
	return OrgScope{OrganizationID: "org-" + tenantID, Tier: "free", Aliases: []string{tenantID}, Exempt: s.exempt}, nil
}

// The server-resolved mark is not a bare tenant header: it only counts while
// the context still carries the tenant it was set for, and only on a managed
// gateway.
func TestServerResolvedTenantMark(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	withManagedGateway(t)
	withResolver(t, &stubResolver{tier: "free", known: true})
	SetEnforcementPolicy(true, nil)
	t.Cleanup(func() { SetShadowEnforcement(true) })

	marked := WithServerResolvedTenant(context.Background(), "tenant-abc")
	if got := contextkeys.GetTenantID(marked); got != "tenant-abc" {
		t.Fatalf("tenant = %q, want tenant-abc", got)
	}
	if ent := ResolveEntitlements(marked, nil); ent.Source != "managed-plan" {
		t.Fatalf("marked tenant resolved %q, want managed-plan", ent.Source)
	}

	// Rebinding a different tenant on top must not inherit the mark.
	rebound := contextkeys.WithTenantID(marked, "tenant-other")
	if ent := ResolveEntitlements(rebound, nil); ent.Source == "managed-plan" {
		t.Fatal("a rebound tenant inherited the server-resolved mark")
	}

	// A tenant header marked authenticated but with no verified principal
	// keeps the strict path.
	spoofed := contextkeys.WithTenantAuthenticated(contextkeys.WithTenantID(context.Background(), "tenant-abc"))
	if ent := ResolveEntitlements(spoofed, nil); ent.Source == "managed-plan" {
		t.Fatal("a bare tenant header resolved a managed plan")
	}
	if got := WithServerResolvedTenant(context.Background(), "  "); contextkeys.GetTenantID(got) != "" {
		t.Fatal("an empty tenant must not be bound")
	}
}

// Self-hosted and CE keep counting the request's tenant alone, so their
// behaviour is unchanged by organization scoping.
func TestCountScopeOffManagedGatewayIsTheTenantAlone(t *testing.T) {
	withResolver(t, &stubResolver{tier: "free", known: true})
	scope := ResolveCountScope(context.Background(), "tenant-abc")
	if scope.Key != "tenant-abc" || len(scope.Aliases) != 1 || scope.Aliases[0] != "tenant-abc" {
		t.Fatalf("scope = %+v, want the tenant alone", scope)
	}
	textArg := scope.Arg(TenantText).(*pq.StringArray)
	if len(*textArg) != 1 || (*textArg)[0] != "tenant-abc" {
		t.Fatalf("text array = %v", *textArg)
	}
	// A non-UUID tenant can never match a uuid column; it is dropped rather
	// than failing the cast.
	uuidArg := scope.Arg(TenantUUID).(*pq.StringArray)
	if len(*uuidArg) != 0 {
		t.Fatalf("uuid array = %v, want empty", *uuidArg)
	}
}

// New checks are Cloud-only: off a managed gateway they never run, so a
// self-hosted or CE install keeps its behaviour even where CE defaults carry
// the same cap.
func TestManagedOnlyChecksSkipSelfHosted(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	saved := CEUsageLimits[UsageTypePersistentAgents]
	CEUsageLimits[UsageTypePersistentAgents] = 0
	t.Cleanup(func() { CEUsageLimits[UsageTypePersistentAgents] = saved })

	ctx := contextkeys.WithTenantID(context.Background(), "tenant-abc")
	if err := CheckLimit(ctx, nil, nil, PersistentAgentsLimit("tenant-abc").Managed()); err != nil {
		t.Fatalf("managed-only check ran on a self-hosted gateway: %v", err)
	}
	if slot, err := ReserveLimitSlot(ctx, nil, nil, PersistentAgentsLimit("tenant-abc").Managed()); err != nil || slot == nil {
		t.Fatalf("managed-only slot = %v, %v; want a no-op slot", slot, err)
	}
	// The same check without the flag still applies the CE cap.
	if err := CheckLimit(ctx, nil, nil, PersistentAgentsLimit("tenant-abc")); !errors.Is(err, ErrResourceLimit) {
		t.Fatalf("unflagged check err = %v, want the CE refusal", err)
	}
}

// EnforcedManagedPlan answers only for an enforced managed plan; shadow and
// self-hosted get ok=false, which hosting turns into "no quota".
func TestEnforcedManagedPlan(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	if _, _, ok, _ := EnforcedManagedPlan(context.Background(), "tenant-abc"); ok {
		t.Fatal("self-hosted resolved a managed plan")
	}
	withManagedGateway(t)
	withResolver(t, scopedStub{tier: "free", org: "org-1", aliases: []string{"org-1", "tenant-abc"}})
	withShadow(t, true)
	if _, _, ok, _ := EnforcedManagedPlan(context.Background(), "tenant-abc"); ok {
		t.Fatal("a shadow tier must not enforce")
	}
	SetEnforcementPolicy(true, nil)
	ent, scope, ok, _ := EnforcedManagedPlan(context.Background(), "tenant-abc")
	if !ok || ent.Tier != "free" || scope.Key != "org:org-1" || len(scope.Aliases) != 2 {
		t.Fatalf("enforced plan = %+v %+v %v", ent, scope, ok)
	}
	if limit, capped := ent.Limit(UsageTypeHostedSites); !capped || limit != 3 {
		t.Fatalf("HOSTED_SITES = %d %v, want 3", limit, capped)
	}
}

type scopedStub struct {
	tier    string
	org     string
	aliases []string
}

func (s scopedStub) PlanTier(context.Context, string) (string, bool) { return s.tier, true }

func (s scopedStub) ResolveOrgScope(context.Context, string) (OrgScope, error) {
	return OrgScope{OrganizationID: s.org, Tier: s.tier, Aliases: s.aliases}, nil
}

// A failed organization lookup on Cloud must not fail open: the request gets
// Starter caps, and the error is not cached, so the next request retries.
func TestTransientLookupErrorGetsStarterCapsAndIsNotCached(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	withManagedGateway(t)
	withResolver(t, NewDBPlanTierResolver(sqlx.NewDb(rawDB, "sqlmock"), time.Minute))
	SetEnforcementPolicy(true, nil)
	t.Cleanup(func() { SetShadowEnforcement(true) })

	mock.ExpectQuery(`SELECT o.id::text`).WillReturnError(errors.New("connection refused"))
	ent := ResolveEntitlements(verifiedTenantCtx("tenant-flaky"), nil)
	if ent.Source != "managed-unresolved" {
		t.Fatalf("lookup error resolved %q, want managed-unresolved (Starter caps)", ent.Source)
	}
	if _, capped := ent.Limit(UsageTypeAgents); !capped {
		t.Fatal("a lookup error must not resolve unlimited on Cloud")
	}

	// Not cached: the next request queries again and gets the real plan.
	mock.ExpectQuery(`SELECT o.id::text`).WillReturnRows(
		sqlmock.NewRows([]string{"organization_id", "plan_tier"}).AddRow("org-1", "pro"))
	mock.ExpectQuery(`WITH claims`).WillReturnRows(sqlmock.NewRows([]string{"alias"}).AddRow("tenant-flaky"))
	mock.ExpectQuery(`WITH claims`).WillReturnRows(sqlmock.NewRows([]string{"alias"})) // deleted instances
	if ent := ResolveEntitlements(verifiedTenantCtx("tenant-flaky"), nil); ent.Tier != "pro" {
		t.Fatalf("after recovery tier = %q (%s), want pro: the error must not be cached", ent.Tier, ent.Source)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Before plan enforcement, a detached server path (channel session) on a
// managed gateway resolved Community Edition limits: agent storage uploads
// were capped at 500 MB and channel-started runs at one concurrent agent.
// Resolving the organization's plan must never make those looser.
func TestDetachedPathsKeepTheirPreviousCaps(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	withManagedGateway(t)
	withResolver(t, &stubResolver{tier: "pro", known: true})

	marked := WithServerResolvedTenant(context.Background(), "tenant-abc")
	for _, policy := range []bool{false, true} { // shadow, then enforced
		SetEnforcementPolicy(policy, nil)
		ent := ResolveEntitlements(marked, nil)
		if limit, capped := ent.Limit(UsageTypeStorageBytes); !capped || limit != CEUsageLimits[UsageTypeStorageBytes] {
			t.Fatalf("enforce=%v: STORAGE_BYTES = %d %v, want the previous CE cap %d", policy, limit, capped, CEUsageLimits[UsageTypeStorageBytes])
		}
		if limit, capped := ent.Limit(UsageTypeConcurrentRunning); !capped || limit != CEUsageLimits[UsageTypeConcurrentRunning] {
			t.Fatalf("enforce=%v: CONCURRENT_RUNNING = %d %v, want the previous CE cap", policy, limit, capped)
		}
	}
	SetShadowEnforcement(true)

	// The floor is for detached paths only: a verified request is unchanged.
	if _, capped := ResolveEntitlements(verifiedTenantCtx("tenant-abc"), nil).Limit(UsageTypeStorageBytes); capped {
		t.Fatal("a verified request must not pick up the detached-path floor")
	}
	// And CheckCELimit keeps applying CE caps on detached paths, as before.
	if !ShouldEnforceCELimits(marked, nil) {
		t.Fatal("CE limits must still apply on detached paths")
	}
}

// Hosting follows the same rule as every other cap: a tenant that resolves no
// live organization (or whose lookup failed) gets the Starter quota on its
// own sites, never unlimited.
func TestEnforcedManagedPlanGivesUnresolvedTenantsStarterQuota(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	withManagedGateway(t)
	withResolver(t, failingScopeResolver{})
	SetEnforcementPolicy(true, nil)
	t.Cleanup(func() { SetShadowEnforcement(true) })

	ent, scope, ok, _ := EnforcedManagedPlan(context.Background(), "tenant-gone")
	if !ok || ent.Tier != "free" || ent.Source != "managed-unresolved" {
		t.Fatalf("unresolved hosting plan = %+v, %v; want Starter (managed-unresolved)", ent, ok)
	}
	if limit, capped := ent.Limit(UsageTypeHostedSites); !capped || limit != 3 {
		t.Fatalf("HOSTED_SITES = %d %v, want the Starter cap 3", limit, capped)
	}
	if scope.Key != "tenant-gone" || len(scope.Aliases) != 1 {
		t.Fatalf("scope = %+v, want the tenant alone", scope)
	}
}

type failingScopeResolver struct{}

func (failingScopeResolver) PlanTier(context.Context, string) (string, bool) { return "", false }

func (failingScopeResolver) ResolveOrgScope(context.Context, string) (OrgScope, error) {
	return OrgScope{}, billingidentity.ErrNoOrganization
}

// A platform DB outage must not drop a paying org to Starter caps: the last
// known good answer for that tenant is served for up to 24 hours. Errors are
// still never cached, and with no prior answer the tenant gets Starter.
func TestLookupErrorServesLastKnownGoodTierFor24Hours(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	base := time.Now()
	now := base
	timeNow = func() time.Time { return now }
	t.Cleanup(func() { timeNow = time.Now })

	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	withManagedGateway(t)
	withResolver(t, NewDBPlanTierResolver(sqlx.NewDb(rawDB, "sqlmock"), time.Minute))
	SetEnforcementPolicy(true, nil)
	t.Cleanup(func() { SetShadowEnforcement(true) })

	mock.ExpectQuery(`SELECT o.id::text`).WillReturnRows(
		sqlmock.NewRows([]string{"organization_id", "plan_tier"}).AddRow("org-1", "pro"))
	mock.ExpectQuery(`WITH claims`).WillReturnRows(sqlmock.NewRows([]string{"alias"}).AddRow("tenant-paid").AddRow("org-1"))
	mock.ExpectQuery(`WITH claims`).WillReturnRows(sqlmock.NewRows([]string{"alias"})) // deleted instances
	if ent := ResolveEntitlements(verifiedTenantCtx("tenant-paid"), nil); ent.Tier != "pro" {
		t.Fatalf("tier = %q, want pro", ent.Tier)
	}

	// The cache entry expires and the platform DB is down: still pro, with
	// the organization's full scope, and the error itself is not cached.
	now = base.Add(2 * time.Hour)
	for i := 0; i < 2; i++ {
		mock.ExpectQuery(`SELECT o.id::text`).WillReturnError(errors.New("connection refused"))
		if ent := ResolveEntitlements(verifiedTenantCtx("tenant-paid"), nil); ent.Tier != "pro" || ent.Source != "managed-plan" {
			t.Fatalf("outage %d: %q/%q, want the last known pro plan", i, ent.Tier, ent.Source)
		}
	}
	mock.ExpectQuery(`SELECT o.id::text`).WillReturnError(errors.New("connection refused"))
	if scope := ResolveCountScope(verifiedTenantCtx("tenant-paid"), "tenant-paid"); scope.Key != "org:org-1" {
		t.Fatalf("outage scope = %+v, want the last known organization scope", scope)
	}

	// Past 24 hours the stale answer is no longer trusted: Starter.
	now = base.Add(25 * time.Hour)
	mock.ExpectQuery(`SELECT o.id::text`).WillReturnError(errors.New("connection refused"))
	if ent := ResolveEntitlements(verifiedTenantCtx("tenant-paid"), nil); ent.Source != "managed-unresolved" {
		t.Fatalf("after 24h: %q/%q, want Starter (managed-unresolved)", ent.Tier, ent.Source)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// If the organization-wide count cannot be resolved, a create must not fall
// back to counting only its own instance's rows (an under-count): it is
// refused with a retryable "temporarily unavailable" error.
func TestUnresolvableOrgScopeFailsClosed(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	withManagedGateway(t)
	withResolver(t, NewDBPlanTierResolver(sqlx.NewDb(rawDB, "sqlmock"), time.Minute))
	SetEnforcementPolicy(true, nil)
	t.Cleanup(func() { SetShadowEnforcement(true) })

	// Tier lookup and scope lookup both fail; there is no prior answer.
	mock.ExpectQuery(`SELECT o.id::text`).WillReturnError(errors.New("connection refused"))
	mock.ExpectQuery(`SELECT o.id::text`).WillReturnError(errors.New("connection refused"))
	err = CheckLimit(verifiedTenantCtx("tenant-x"), sqlx.NewDb(rawDB, "sqlmock"), nil, AgentsLimit("tenant-x"))
	if !errors.Is(err, ErrLimitCheckUnavailable) || errors.Is(err, ErrResourceLimit) {
		t.Fatalf("err = %v, want ErrLimitCheckUnavailable (never a partial count)", err)
	}
	if !strings.Contains(err.Error(), "try again") {
		t.Fatalf("message %q must say the refusal is temporary", err.Error())
	}
}

// Hosting fails closed like every other cap: when the organization cannot be
// resolved because the lookup failed (no last known good answer), the quota
// is unavailable rather than counted over the tenant's own sites only.
func TestEnforcedManagedPlanFailsClosedOnLookupError(t *testing.T) {
	if IsDev() {
		t.Skip("dev builds resolve everything unlimited")
	}
	withManagedGateway(t)
	withResolver(t, transientScopeResolver{})
	SetEnforcementPolicy(true, nil)
	t.Cleanup(func() { SetShadowEnforcement(true) })

	if _, _, ok, err := EnforcedManagedPlan(context.Background(), "tenant-x"); ok || !errors.Is(err, ErrLimitCheckUnavailable) {
		t.Fatalf("ok=%v err=%v, want ErrLimitCheckUnavailable", ok, err)
	}
	SetShadowEnforcement(true)
	if _, _, ok, err := EnforcedManagedPlan(context.Background(), "tenant-x"); ok || err != nil {
		t.Fatalf("shadow: ok=%v err=%v, want no quota and no error", ok, err)
	}
}

type transientScopeResolver struct{}

func (transientScopeResolver) PlanTier(context.Context, string) (string, bool) { return "", false }

func (transientScopeResolver) ResolveOrgScope(context.Context, string) (OrgScope, error) {
	return OrgScope{}, errors.New("connection refused")
}
