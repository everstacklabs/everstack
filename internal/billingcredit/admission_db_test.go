package billingcredit

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/everstacklabs/everstack/internal/billingallowance"
	"github.com/everstacklabs/everstack/pkg/plans"
)

// Real-Postgres tests for the compute admission decision (Resolve), the gate
// every managed sandbox create, revive and hosted-browser lease goes through.
//
// Every public plan includes monthly compute allowances
// (docs/design/cloud-plan-allowances.md): Starter 40 vCPU-hours, 40
// GiB-hours and 5 browser-hours, each counted on its own meter. An
// organization with room in every allowance a resource draws on is admitted
// to it whatever its wallet holds; once one is used up it needs credit,
// unless it is internal.

const (
	vcpuHour    = int64(50_400) // $0.0504
	gibHour     = int64(16_200) // $0.0162
	browserHour = int64(50_000) // $0.05

	starterCPU     = 40 * vcpuHour
	starterMemory  = 40 * gibHour
	starterBrowser = 5 * browserHour
)

func (e *signupEnv) exempt(org uuid.UUID) {
	e.db.MustExec(`UPDATE everstack.organizations SET plan_limit_exempt = TRUE WHERE id = $1`, org)
}

func (e *signupEnv) setTier(org uuid.UUID, tier string) {
	e.db.MustExec(`UPDATE everstack.organizations SET plan_tier = $2 WHERE id = $1`, org, tier)
}

// heartbeat adds usage the way the billing heartbeat does: under the org
// lock, opening allowance segments around the meter writes. The sandbox cost
// is the sum of its CPU and memory parts.
func (e *signupEnv) heartbeat(org uuid.UUID, now time.Time, cpu, memory, browser int64) {
	e.heartbeatFrom(org, "shared-sandbox-plane", now, cpu, memory, browser)
}

func (e *signupEnv) heartbeatFrom(org uuid.UUID, source string, now time.Time, cpu, memory, browser int64) {
	e.t.Helper()
	cfg, err := plans.Embedded()
	if err != nil {
		e.t.Fatal(err)
	}
	tx := e.db.MustBeginTx(e.ctx, nil)
	defer func() { _ = tx.Rollback() }()
	tx.MustExec(`SELECT pg_advisory_xact_lock(hashtext($1))`, org.String())
	if err := billingallowance.MarkCountersSeenTx(e.ctx, tx, org.String(), source, now); err != nil {
		e.t.Fatal(err)
	}
	hb, err := billingallowance.BeginHeartbeatTx(e.ctx, tx, org.String())
	if err != nil {
		e.t.Fatal(err)
	}
	for metric, v := range map[string]int64{
		billingallowance.MetricSandboxCostMicros:       cpu + memory,
		billingallowance.MetricSandboxCPUCostMicros:    cpu,
		billingallowance.MetricSandboxMemoryCostMicros: memory,
		billingallowance.MetricBrowserCostMicros:       browser,
	} {
		tx.MustExec(`
			INSERT INTO billing.usage_meter_watermarks (organization_id, instance_id, metric_type, total_value)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (organization_id, instance_id, metric_type)
			DO UPDATE SET total_value = billing.usage_meter_watermarks.total_value + EXCLUDED.total_value`,
			org, source, metric, v)
	}
	reported := map[string]bool{}
	for _, c := range billingallowance.Counters {
		reported[c.Metric] = true
	}
	if err := hb.FinishTx(e.ctx, tx, cfg, now, reported); err != nil {
		e.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		e.t.Fatal(err)
	}
}

func (e *signupEnv) resolve(org uuid.UUID, tier string) Access {
	e.t.Helper()
	return e.resolveAt(org, tier, time.Now())
}

func (e *signupEnv) resolveAt(org uuid.UUID, tier string, now time.Time) Access {
	e.t.Helper()
	access, err := ResolveAt(e.ctx, e.db, org.String(), tier, now)
	if err != nil {
		e.t.Fatalf("Resolve(%s, %s): %v", org, tier, err)
	}
	return access
}

func mustAllowance(t *testing.T, a Access, c billingallowance.Counter) billingallowance.Status {
	t.Helper()
	s, ok := a.Allowance(c)
	if !ok {
		t.Fatalf("no %s allowance in %+v", c.LimitType, a)
	}
	return s
}

// newOrg is a user's second organization (the signup credit is per user, so
// it starts at $0), with the zero baselines the gateway reports before it
// allocates anything.
func (e *signupEnv) newOrg() uuid.UUID {
	u := e.user()
	_, org := e.org(u), e.org(u)
	e.heartbeat(org, time.Now(), 0, 0, 0)
	return org
}

// A $0 organization within its plan's allowances is admitted on every
// public tier, and no credit is granted for it.
func TestAdmissionAdmitsZeroCreditOrgWithinAllowance(t *testing.T) {
	e := newSignupEnv(t)
	org := e.newOrg()
	for tier, cpu := range map[string]int64{"free": starterCPU, "basic": 150 * vcpuHour, "pro": 750 * vcpuHour} {
		e.setTier(org, tier)
		access := e.resolve(org, tier)
		if !access.Allowed || !access.BrowserAllowed || access.Exempt || access.Credit.RemainingMicros != 0 {
			t.Fatalf("org on %s = %+v, want admitted at $0 within its allowance", tier, access)
		}
		if s := mustAllowance(t, access, billingallowance.SandboxCPU); s.IncludedMicros != cpu || s.Exhausted() {
			t.Fatalf("%s vCPU allowance = %+v, want %d included", tier, s, cpu)
		}
	}
	if e.signupGrants(org) != 0 {
		t.Fatal("admission granted a second signup credit")
	}
}

// With an allowance used up and no credit, the resource is refused. A
// sandbox draws on both its vCPU-hour and GiB-hour allowances, so either one
// running out refuses it; browsers have their own.
func TestAdmissionRefusesWhenAllowanceUsedUpAndNoCredit(t *testing.T) {
	e := newSignupEnv(t)
	now := time.Now()
	org := e.newOrg()

	e.heartbeat(org, now, starterCPU-1, starterMemory/2, 0)
	if access := e.resolve(org, "free"); !access.Allowed {
		t.Fatalf("one micro-dollar of vCPU allowance left = %+v, want admitted", access)
	}
	e.heartbeat(org, now, 1, 0, 0)
	access := e.resolve(org, "free")
	if access.Allowed || !access.BrowserAllowed {
		t.Fatalf("vCPU-hours used up at $0 = %+v, want sandboxes refused, browsers admitted", access)
	}
	if s := mustAllowance(t, access, billingallowance.SandboxCPU); s.UsedSeconds() != 40*3600 || s.IncludedSeconds() != 40*3600 {
		t.Fatalf("vCPU status = %+v, want 40 of 40 hours", s)
	}
	if s := mustAllowance(t, access, billingallowance.SandboxMemory); s.UsedSeconds() != 20*3600 || s.Exhausted() {
		t.Fatalf("memory status = %+v, want 20 of 40 hours", s)
	}

	// Memory alone running out refuses a sandbox too.
	other := e.newOrg()
	e.heartbeat(other, now, starterCPU/4, starterMemory, 0)
	if access := e.resolve(other, "free"); access.Allowed {
		t.Fatalf("GiB-hours used up at $0 = %+v, want refused", access)
	}

	e.heartbeat(org, now, 0, 0, starterBrowser)
	if access := e.resolve(org, "free"); access.Allowed || access.BrowserAllowed {
		t.Fatalf("all allowances used up at $0 = %+v, want both refused", access)
	}
	e.ledger(org, "purchase", 10_000_000)
	if access := e.resolve(org, "free"); !access.Allowed || !access.BrowserAllowed {
		t.Fatalf("allowances used up with credit = %+v, want admitted", access)
	}
}

// Credit held for in-flight usage is not available: with an allowance used up
// and all credit held, the org is refused.
func TestAdmissionAdmitsOrgWithAvailableCredit(t *testing.T) {
	e := newSignupEnv(t)
	org := e.newOrg()
	e.heartbeat(org, time.Now(), starterCPU, starterMemory, 0)
	e.ledger(org, "purchase", 10_000_000)
	access := e.resolve(org, "free")
	if !access.Allowed || access.Credit.RemainingMicros != 10_000_000 {
		t.Fatalf("funded org = %+v, want admitted with $10 available", access)
	}
	e.db.MustExec(`
		INSERT INTO billing.credit_holds (organization_id, amount_micros, state, request_id, expires_at)
		VALUES ($1, 10000000, 'held', $2, NOW() + INTERVAL '1 hour')`, org, "test-hold:"+uuid.NewString())
	if access := e.resolve(org, "free"); access.Allowed {
		t.Fatalf("org with allowance used up and all credit held = %+v, want refused", access)
	}
}

// The allowance resets each period: last month's usage never counts.
func TestAdmissionAllowanceResetsEachPeriod(t *testing.T) {
	e := newSignupEnv(t)
	org := e.newOrg()
	oct := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	nov := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	e.heartbeat(org, oct, 5*starterCPU, 5*starterMemory, 0)
	if access := e.resolveAt(org, "free", oct); access.Allowed {
		t.Fatalf("October used up at $0 = %+v, want refused", access)
	}
	e.heartbeat(org, nov, 0, 0, 0) // the gateway reports every few seconds
	access := e.resolveAt(org, "free", nov)
	s := mustAllowance(t, access, billingallowance.SandboxCPU)
	if !access.Allowed || s.UsedMicros != 0 || !s.PeriodStart.Equal(time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("November before use = %+v (%+v), want a fresh allowance", access, s)
	}
	e.heartbeat(org, nov, 20*vcpuHour, 20*gibHour, 0)
	if s := mustAllowance(t, e.resolveAt(org, "free", nov), billingallowance.SandboxCPU); s.UsedSeconds() != 20*3600 {
		t.Fatalf("November = %+v, want 20 hours used", s)
	}
}

// A paid plan's allowance follows its subscription period, not the calendar.
func TestAdmissionPaidPlanUsesSubscriptionPeriod(t *testing.T) {
	e := newSignupEnv(t)
	org := e.newOrg()
	e.setTier(org, "basic")
	start := time.Now().UTC().Add(-72 * time.Hour).Truncate(time.Second)
	end := start.AddDate(0, 1, 0)
	e.db.MustExec(`INSERT INTO billing.subscriptions
		(organization_id, stripe_subscription_id, stripe_price_id, stripe_customer_id, plan_tier, billing_period, status,
		 current_period_start, current_period_end, deployment_type)
		VALUES ($1, 'sub_x', 'price_x', 'cus_x', 'basic', 'monthly', 'active', $2, $3, 'cloud')`, org, start, end)
	e.heartbeat(org, time.Now(), 0, 0, 0) // opens Build's segment
	e.heartbeat(org, time.Now(), vcpuHour, gibHour, 0)
	s := mustAllowance(t, e.resolve(org, "basic"), billingallowance.SandboxMemory)
	if !s.PeriodStart.Equal(start) || !s.PeriodEnd.Equal(end) || s.IncludedMicros != 150*gibHour || s.UsedMicros != gibHour {
		t.Fatalf("paid allowance = %+v, want Build's over %s..%s with an hour used", s, start, end)
	}
}

// Internal organizations (plan_limit_exempt) are admitted without credit,
// even past their allowances with the wallet below zero. Their wallet is
// still reported, and their first org still receives its grant.
func TestAdmissionAdmitsExemptOrgWithoutCredit(t *testing.T) {
	e := newSignupEnv(t)
	internal := e.newOrg()
	e.exempt(internal)
	access := e.resolve(internal, "free")
	if !access.Allowed || !access.BrowserAllowed || !access.Exempt || access.Credit.RemainingMicros != 0 {
		t.Fatalf("exempt org at $0 = %+v, want admitted", access)
	}
	e.heartbeat(internal, time.Now(), 10*starterCPU, 10*starterMemory, 10*starterBrowser)
	e.ledger(internal, "debit", -2_000_000)
	access = e.resolve(internal, "pro")
	if !access.Allowed || !access.BrowserAllowed || access.Credit.RemainingMicros != -2_000_000 {
		t.Fatalf("exempt org below zero past its allowance = %+v, want admitted", access)
	}
	if e.signupGrants(internal) != 0 {
		t.Fatal("exemption granted a signup credit")
	}
	owner := e.user()
	exemptFirst := e.org(owner)
	e.exempt(exemptFirst)
	access = e.resolve(exemptFirst, "free")
	if !access.Allowed || access.Credit.RemainingMicros != fiveDollars || e.signupGrants(exemptFirst) != 1 {
		t.Fatalf("exempt first org = %+v grants=%d", access, e.signupGrants(exemptFirst))
	}
}

// One organization's exemption, credit, allowance or usage never admits or
// refuses another, resolving one organization never writes to another's
// wallet, and usage from a self-hosted source is never counted.
func TestAdmissionIsolatesOrganizations(t *testing.T) {
	e := newSignupEnv(t)
	now := time.Now()
	orgA, orgB, orgC := e.newOrg(), e.newOrg(), e.newOrg()
	e.exempt(orgA)
	e.ledger(orgC, "purchase", 25_000_000)
	e.heartbeat(orgB, now, starterCPU, starterMemory, 0)

	if access := e.resolve(orgB, "free"); access.Allowed || access.Exempt {
		t.Fatalf("org B = %+v; another org's exemption, credit or allowance admitted it", access)
	}
	if s := mustAllowance(t, e.resolve(orgC, "free"), billingallowance.SandboxCPU); s.UsedMicros != 0 {
		t.Fatalf("org C allowance = %+v; org B's usage counted against it", s)
	}
	if access := e.resolve(orgA, "free"); !access.Allowed || !access.Exempt || access.Credit.RemainingMicros != 0 {
		t.Fatalf("org A = %+v", access)
	}
	if access := e.resolve(orgC, "free"); !access.Allowed || access.Exempt || access.Credit.RemainingMicros != 25_000_000 {
		t.Fatalf("org C = %+v", access)
	}
	if e.balance(orgB) != 0 || e.balance(orgA) != 0 || e.balance(orgC) != 25_000_000 {
		t.Fatalf("balances A=%d B=%d C=%d", e.balance(orgA), e.balance(orgB), e.balance(orgC))
	}
	e.heartbeatFrom(orgC, "self-hosted-gateway-1", now, 0, 0, 0)
	e.heartbeatFrom(orgC, "self-hosted-gateway-1", now, 10*starterCPU, 10*starterMemory, 0)
	if s := mustAllowance(t, e.resolve(orgC, "free"), billingallowance.SandboxCPU); s.UsedMicros != 0 {
		t.Fatalf("org C allowance = %+v; self-hosted usage counted", s)
	}
	stranger := uuid.New()
	if access, err := Resolve(e.ctx, e.db, stranger.String(), "free"); err != nil || access.Allowed || access.BrowserAllowed || access.Exempt || len(access.Allowances) != 0 {
		t.Fatalf("unknown id = %+v, %v", access, err)
	}
}

// Enterprise contracts bypass the wallet and the allowances; unchanged.
func TestAdmissionEnterpriseBypassesWallet(t *testing.T) {
	e := newSignupEnv(t)
	org := e.newOrg()
	if access := e.resolve(org, "enterprise"); !access.Allowed || !access.BrowserAllowed {
		t.Fatalf("enterprise = %+v", access)
	}
}

// Admission promises a sandbox allowance only while a gateway reports the
// CPU and memory counters; after a rollback to gateways without them, sandbox
// cost is charged in full, so a $0 org is refused sandboxes.
func TestAdmissionNeedsFreshSandboxCounters(t *testing.T) {
	e := newSignupEnv(t)
	now := time.Now()
	org := e.newOrg()
	e.heartbeat(org, now, vcpuHour, gibHour, 0)
	if access := e.resolveAt(org, "free", now); !access.Allowed {
		t.Fatalf("fresh counters = %+v, want admitted", access)
	}
	later := now.Add(billingallowance.CounterFreshness + time.Minute)
	if access := e.resolveAt(org, "free", later); access.Allowed || !access.BrowserAllowed {
		t.Fatalf("stale counters at $0 = %+v, want sandboxes refused, browsers admitted", access)
	}
}
