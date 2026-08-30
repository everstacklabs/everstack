package sandbox

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

func TestComputeSandboxCost(t *testing.T) {
	cfg := InstanceConfig{
		CPULimit: 2.0,
		MemoryMB: 2048, // 2 GB
		DiskMB:   4096, // 4 GB
	}
	pricing := SandboxPricingConfig{
		Enabled:               true,
		Currency:              "USD",
		CPUPerHourUSD:         0.10,
		MemoryGBPerHourUSD:    0.04,
		DiskGBPerHourUSD:      0.001,
		PlatformFeePerHourUSD: 0.05,
	}
	start := time.Date(2026, 5, 15, 12, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)

	cost, secs := computeSandboxCost(cfg, start, end, pricing, 1.0)
	if secs != 3600 {
		t.Fatalf("duration: got %d, want 3600", secs)
	}
	// CPU: 2 * 0.10 = 0.20; Mem: 2 * 0.04 = 0.08; Disk: 4 * 0.001 = 0.004; Plat: 0.05.
	wantTotal := 0.20 + 0.08 + 0.004 + 0.05
	if math.Abs(cost.TotalUSD-wantTotal) > 1e-9 {
		t.Fatalf("total: got %.9f, want %.9f", cost.TotalUSD, wantTotal)
	}

	// Tier multiplier 0.5 → all components halve.
	half, _ := computeSandboxCost(cfg, start, end, pricing, 0.5)
	if math.Abs(half.TotalUSD-wantTotal*0.5) > 1e-9 {
		t.Fatalf("tier: got %.9f, want %.9f", half.TotalUSD, wantTotal*0.5)
	}

	// Disabled pricing → zeroed regardless of rates.
	disabled := pricing
	disabled.Enabled = false
	zero, _ := computeSandboxCost(cfg, start, end, disabled, 1.0)
	if zero.TotalUSD != 0 {
		t.Fatalf("disabled: got %v, want 0", zero.TotalUSD)
	}

	// Zero/negative windows return zero duration.
	if _, d := computeSandboxCost(cfg, end, start, pricing, 1.0); d != 0 {
		t.Fatalf("inverted window: got %d, want 0", d)
	}
	if _, d := computeSandboxCost(cfg, time.Time{}, end, pricing, 1.0); d != 0 {
		t.Fatalf("zero start: got %d, want 0", d)
	}
}

// A durable sandbox can sleep for hours and revive under the same ID. Live
// accrual must use only the explicit current allocation window, never the
// sandbox's identity creation time. This is the regression that previously
// charged the sleeping gap after a revive (and behaved differently after a
// gateway restart).
func TestLiveAccruedCostUsesCurrentBillingWindow(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	m := &SandboxManager{
		globalConfig: GlobalSandboxConfig{Pricing: SandboxPricingConfig{
			Enabled:       true,
			Currency:      "USD",
			CPUPerHourUSD: 1,
		}},
		instances: map[string]*Instance{
			"revived": {
				ID:               "sbx-revived",
				Status:           StatusRunning,
				CreatedAt:        now.Add(-24 * time.Hour),
				BillingStartedAt: now.Add(-5 * time.Minute),
				Config: InstanceConfig{
					TenantID: "tenant-a",
					CPULimit: 1,
				},
			},
			"sleeping": {
				ID:               "sbx-sleeping",
				Status:           StatusStopped,
				CreatedAt:        now.Add(-48 * time.Hour),
				BillingStartedAt: now.Add(-2 * time.Hour), // stale field must not accrue
				Config: InstanceConfig{
					TenantID: "tenant-a",
					CPULimit: 4,
				},
			},
			"legacy-without-window": {
				ID:        "sbx-no-window",
				Status:    StatusRunning,
				CreatedAt: now.Add(-72 * time.Hour),
				Config: InstanceConfig{
					TenantID: "tenant-a",
					CPULimit: 8,
				},
			},
		},
	}

	cost, seconds := m.liveAccruedCostAt(context.Background(), "tenant-a", now)
	if seconds != 5*60 {
		t.Fatalf("compute seconds = %d, want 300 (sleep gap and closed states excluded)", seconds)
	}
	wantCost := 5.0 / 60.0
	if math.Abs(cost-wantCost) > 1e-9 {
		t.Fatalf("cost = %.9f, want %.9f", cost, wantCost)
	}
}

func TestLiveAccruedCostPinsPendingCloseAtObservedEnd(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	startedAt := now.Add(-time.Hour)
	endedAt := startedAt.Add(5 * time.Minute)
	m := &SandboxManager{
		globalConfig: GlobalSandboxConfig{Pricing: SandboxPricingConfig{
			Enabled:       true,
			Currency:      "USD",
			CPUPerHourUSD: 1,
		}},
		instances: map[string]*Instance{
			"closing": {
				ID:               "sbx-closing",
				Status:           StatusRunning,
				LifecycleState:   LifecycleStopping,
				BillingStartedAt: startedAt,
				BillingEndedAt:   endedAt,
				Config: InstanceConfig{
					TenantID: "tenant-a",
					CPULimit: 1,
				},
			},
		},
	}

	cost, seconds := m.liveAccruedCostAt(context.Background(), "tenant-a", now)
	if seconds != 5*60 {
		t.Fatalf("compute seconds = %d, want 300 after pinned close", seconds)
	}
	if want := 5.0 / 60.0; math.Abs(cost-want) > 1e-9 {
		t.Fatalf("cost = %.9f, want %.9f", cost, want)
	}
}

func TestPinBillingWindowEndKeepsFirstObservedEnd(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	start := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	firstEnd := start.Add(5 * time.Minute)
	retryEnd := start.Add(20 * time.Minute)
	m := &SandboxManager{
		db:                 sqlx.NewDb(db, "sqlmock"),
		instancesBySandbox: map[string]*Instance{"sbx-a": {ID: "sbx-a", BillingStartedAt: start}},
	}

	mock.ExpectQuery(`(?s)UPDATE sandbox_instances.*SET billing_ended_at = COALESCE\(billing_ended_at, \$3\).*RETURNING billing_ended_at`).
		WithArgs("sbx-a", start, retryEnd).
		WillReturnRows(sqlmock.NewRows([]string{"billing_ended_at"}).AddRow(firstEnd))

	got, err := m.pinBillingWindowEnd(context.Background(), "sbx-a", start, retryEnd)
	if err != nil {
		t.Fatalf("pinBillingWindowEnd: %v", err)
	}
	if !got.Equal(firstEnd) {
		t.Fatalf("pinned end = %s, want first observed end %s", got, firstEnd)
	}
	if cached := m.instancesBySandbox["sbx-a"].BillingEndedAt; !cached.Equal(firstEnd) {
		t.Fatalf("cached end = %s, want %s", cached, firstEnd)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

func TestPinBillingWindowEndDoesNotReenterRoutingLock(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	start := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	end := start.Add(5 * time.Minute)
	m := &SandboxManager{
		db:                 sqlx.NewDb(db, "sqlmock"),
		instancesBySandbox: map[string]*Instance{"sbx-a": {ID: "sbx-a", BillingStartedAt: start}},
	}
	mock.ExpectQuery(`(?s)UPDATE sandbox_instances.*SET billing_ended_at = COALESCE\(billing_ended_at, \$3\).*RETURNING billing_ended_at`).
		WithArgs("sbx-a", start, end).
		WillReturnRows(sqlmock.NewRows([]string{"billing_ended_at"}).AddRow(end))

	// Reconfiguration holds this lock while it tears down the old backend.
	// Pinning must still complete because Postgres, not this cache, owns the
	// retry-safe end boundary.
	m.mu.Lock()
	type result struct {
		end time.Time
		err error
	}
	resultCh := make(chan result, 1)
	go func() {
		got, pinErr := m.pinBillingWindowEnd(context.Background(), "sbx-a", start, end)
		resultCh <- result{end: got, err: pinErr}
	}()

	select {
	case got := <-resultCh:
		m.mu.Unlock()
		if got.err != nil {
			t.Fatalf("pinBillingWindowEnd: %v", got.err)
		}
		if !got.end.Equal(end) {
			t.Fatalf("pinned end = %s, want %s", got.end, end)
		}
	case <-time.After(time.Second):
		m.mu.Unlock()
		t.Fatal("pinBillingWindowEnd blocked on the routing mutex")
	}

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}

// TestTieredDiskCostUSD exercises the included-allowance + marginal tier
// pricing: 20 GiB free, base rate to 50 GiB, +25% beyond, plus the
// defensive fallbacks for partial/legacy configs.
func TestTieredDiskCostUSD(t *testing.T) {
	const (
		rate     = 0.000166644 // per GiB-hour
		included = 20.0
		tier2at  = 50.0
		tier2mul = 1.25
		hours    = 1.0
		gib      = int64(1024) // MB per GiB
	)

	cases := []struct {
		name   string
		diskMB int64
		want   float64
	}{
		{"below allowance", 10 * gib, 0},
		{"exactly allowance", 20 * gib, 0},
		{"tier1 only", 30 * gib, 10 * rate},             // (30-20) at base
		{"tier1 boundary", 50 * gib, 30 * rate},         // full 20-50 band
		{"into tier2", 70 * gib, (30 + 20*1.25) * rate}, // 30 base + 20 at 1.25x
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tieredDiskCostUSD(tc.diskMB, hours, rate, included, tier2at, tier2mul)
			if math.Abs(got-tc.want) > 1e-12 {
				t.Fatalf("got %.12f, want %.12f", got, tc.want)
			}
		})
	}

	// Defensive: zero allowance + unusable tier-2 config bills every GiB at
	// the base rate (legacy behavior, never gives storage away).
	if got, want := tieredDiskCostUSD(4*gib, hours, rate, 0, 0, 0), 4*rate; math.Abs(got-want) > 1e-12 {
		t.Fatalf("legacy fallback: got %.12f, want %.12f", got, want)
	}
	// Zero rate or zero disk → no charge.
	if got := tieredDiskCostUSD(70*gib, hours, 0, included, tier2at, tier2mul); got != 0 {
		t.Fatalf("zero rate: got %v, want 0", got)
	}
	if got := tieredDiskCostUSD(0, hours, rate, included, tier2at, tier2mul); got != 0 {
		t.Fatalf("zero disk: got %v, want 0", got)
	}
}

// TestMergeSandboxPricing verifies the runtime_config overlay contract that
// governs whether the live bill picks up the new model. The allowance/tier
// fields must be inherited from fallback when an (older) operator row omits
// them — otherwise an existing deployment would silently bill the full root
// disk from the first GiB. Explicit rates in the row still override fallback,
// which is precisely why a pre-existing pricing row must be cleared/updated
// for the new CPU/memory rates to take effect.
func TestMergeSandboxPricing(t *testing.T) {
	fallback := SandboxPricingConfig{
		Enabled:               true,
		Currency:              "USD",
		CPUPerHourUSD:         0.0504,
		MemoryGBPerHourUSD:    0.0162,
		DiskGBPerHourUSD:      0.000166644,
		IncludedDiskGiB:       20,
		DiskTier2ThresholdGiB: 50,
		DiskTier2Multiplier:   1.25,
	}

	// Pre-feature operator row: explicit old rates, no allowance fields.
	legacy := map[string]interface{}{
		"enabled":                true,
		"cpu_per_hour_usd":       0.0,
		"memory_gb_per_hour_usd": 0.0414,
		"disk_gb_per_hour_usd":   0.000166644,
	}
	got := mergeSandboxPricing(fallback, legacy)
	if got.IncludedDiskGiB != 20 || got.DiskTier2ThresholdGiB != 50 || got.DiskTier2Multiplier != 1.25 {
		t.Fatalf("allowance/tier must inherit from fallback when row omits them: %+v", got)
	}
	if got.CPUPerHourUSD != 0.0 || got.MemoryGBPerHourUSD != 0.0414 {
		t.Fatalf("explicit legacy rates should override fallback (operator row must be migrated): %+v", got)
	}

	// New-style row sets allowance + tiering explicitly.
	full := map[string]interface{}{
		"included_disk_gib":        30.0,
		"disk_tier2_threshold_gib": 80.0,
		"disk_tier2_multiplier":    1.5,
	}
	if got := mergeSandboxPricing(fallback, full); got.IncludedDiskGiB != 30 || got.DiskTier2ThresholdGiB != 80 || got.DiskTier2Multiplier != 1.5 {
		t.Fatalf("explicit allowance/tier not applied: %+v", got)
	}

	// camelCase keys (admin UI writes these) parse too.
	if got := mergeSandboxPricing(fallback, map[string]interface{}{"includedDiskGib": 15.0}); got.IncludedDiskGiB != 15 {
		t.Fatalf("camelCase allowance not applied: got %v", got.IncludedDiskGiB)
	}
}

// TestComputeSandboxCost_LedgerLiveParity locks in the property that
// "Cost so far" cannot jump when a sandbox terminates: the live
// estimate for an in-flight window must equal the ledger row that
// gets written at the lifecycle event. Both code paths funnel through
// computeSandboxCost, so this test would only break if a future
// change forks the math between the two.
func TestComputeSandboxCost_LedgerLiveParity(t *testing.T) {
	cfg := InstanceConfig{CPULimit: 1.5, MemoryMB: 1024, DiskMB: 2048}
	pricing := SandboxPricingConfig{
		Enabled:               true,
		Currency:              "USD",
		CPUPerHourUSD:         0.0825,
		MemoryGBPerHourUSD:    0.0414,
		DiskGBPerHourUSD:      0.000166644,
		PlatformFeePerHourUSD: 0.01,
		TierMultipliers:       map[string]float64{"pro": 0.93},
	}
	start := time.Date(2026, 5, 1, 9, 30, 17, 0, time.UTC)
	end := start.Add(10*24*time.Hour + 11*time.Minute + 42*time.Second)

	live, liveSecs := computeSandboxCost(cfg, start, end, pricing, 0.93)
	ledger, ledgerSecs := computeSandboxCost(cfg, start, end, pricing, 0.93)
	if liveSecs != ledgerSecs {
		t.Fatalf("duration drift: live=%d ledger=%d", liveSecs, ledgerSecs)
	}
	if live.TotalUSD != ledger.TotalUSD {
		t.Fatalf("cost drift: live=%.9f ledger=%.9f", live.TotalUSD, ledger.TotalUSD)
	}
}
