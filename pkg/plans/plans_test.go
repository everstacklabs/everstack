package plans_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/everstacklabs/everstack/pkg/plans"
)

func TestEmbeddedReturnsCachedCanonicalPlans(t *testing.T) {
	first, err := plans.Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}
	second, err := plans.Embedded()
	if err != nil {
		t.Fatalf("second Embedded() error = %v", err)
	}
	if first != second {
		t.Fatal("Embedded() did not return the cached configuration")
	}

	free, ok := first.GetPlan("free")
	if !ok {
		t.Fatal("embedded plans have no free tier")
	}
	if free.SeatLimit != 1 || free.InstanceLimit != 1 {
		t.Fatalf("free limits = seats %d, instances %d; want 1, 1", free.SeatLimit, free.InstanceLimit)
	}
	if first.SandboxComputePricing == nil {
		t.Fatal("embedded plans have no sandbox_compute_pricing")
	}
}

func TestGetPlanLimitsIncludesEveryUsageLimit(t *testing.T) {
	config, err := plans.Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}
	free, ok := config.GetPlan("free")
	if !ok {
		t.Fatal("embedded plans have no free tier")
	}

	limits := config.GetPlanLimits("free")
	if limits == nil {
		t.Fatal("GetPlanLimits(\"free\") = nil")
	}
	if limits.SeatLimit != free.SeatLimit || limits.InstanceLimit != free.InstanceLimit {
		t.Fatalf("plan limits omit top-level limits: got seats %d, instances %d", limits.SeatLimit, limits.InstanceLimit)
	}
	if len(limits.UsageLimits) != len(free.UsageLimits) {
		t.Fatalf("GetPlanLimits returned %d usage types; want %d", len(limits.UsageLimits), len(free.UsageLimits))
	}
	for _, want := range free.UsageLimits {
		got, ok := limits.GetUsageLimit(want.Type)
		if !ok {
			t.Errorf("GetPlanLimits omitted %q", want.Type)
			continue
		}
		if got != want.Value {
			t.Errorf("usage limit %q = %d; want %d", want.Type, got, want.Value)
		}
	}
	if _, ok := limits.GetUsageLimit("NOT_DEFINED"); ok {
		t.Fatal("GetUsageLimit reported a missing type as present")
	}
	if got := config.GetPlanLimits("not-a-tier"); got != nil {
		t.Fatalf("GetPlanLimits for missing tier = %#v; want nil", got)
	}
}

func TestLoadUsesFileOverrideAndFallsBackToEmbedded(t *testing.T) {
	dir := t.TempDir()
	overridePath := filepath.Join(dir, "plans.json")
	const override = `{"plans":{"test":{"tier":"test","name":"Test","seat_limit":7,"instance_limit":9,"usage_limits":[{"type":"CUSTOM","value":42}]}}}`
	if err := os.WriteFile(overridePath, []byte(override), 0o600); err != nil {
		t.Fatalf("write override: %v", err)
	}

	loaded, err := plans.Load(overridePath)
	if err != nil {
		t.Fatalf("Load(override) error = %v", err)
	}
	testPlan, ok := loaded.GetPlan("test")
	if !ok {
		t.Fatal("Load(override) did not load the override tier")
	}
	if testPlan.SeatLimit != 7 || testPlan.InstanceLimit != 9 {
		t.Fatalf("override limits = seats %d, instances %d; want 7, 9", testPlan.SeatLimit, testPlan.InstanceLimit)
	}

	embedded, err := plans.Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}
	for _, path := range []string{"", filepath.Join(dir, "missing.json")} {
		fallback, err := plans.Load(path)
		if err != nil {
			t.Fatalf("Load(%q) error = %v", path, err)
		}
		if fallback != embedded {
			t.Errorf("Load(%q) did not return the embedded configuration", path)
		}
	}
}

func TestGetAllPlansKeepsStableTierOrder(t *testing.T) {
	config, err := plans.Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}
	got := config.GetAllPlans()
	want := []string{"free", "basic", "pro", "enterprise"}
	if len(got) < len(want) {
		t.Fatalf("GetAllPlans returned %d plans; want at least %d", len(got), len(want))
	}
	for i, tier := range want {
		if got[i].Tier != tier {
			t.Errorf("GetAllPlans()[%d].Tier = %q; want %q", i, got[i].Tier, tier)
		}
	}
}

func TestEmbeddedPlanNamesAndPrices(t *testing.T) {
	config, err := plans.Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}

	tests := []struct {
		tier    string
		name    string
		monthly string
		yearly  string
	}{
		{tier: "free", name: "Starter", monthly: "$0", yearly: "$0"},
		{tier: "basic", name: "Build", monthly: "$20", yearly: "$200"},
		{tier: "pro", name: "Scale", monthly: "$100", yearly: "$1,000"},
	}

	for _, test := range tests {
		plan, ok := config.GetPlan(test.tier)
		if !ok {
			t.Fatalf("embedded plans have no %s tier", test.tier)
		}
		if plan.Name != test.name {
			t.Errorf("%s name = %q; want %q", test.tier, plan.Name, test.name)
		}
		if plan.Pricing.Monthly != test.monthly || plan.Pricing.Yearly != test.yearly {
			t.Errorf(
				"%s pricing = %s monthly, %s yearly; want %s monthly, %s yearly",
				test.tier,
				plan.Pricing.Monthly,
				plan.Pricing.Yearly,
				test.monthly,
				test.yearly,
			)
		}
	}
}

func TestCreditRateCardIsMachineReadable(t *testing.T) {
	config, err := plans.Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}

	rc := config.GetCreditPricing()
	if rc == nil {
		t.Fatalf("GetCreditPricing() = nil; want the credit rate card")
	}
	if rc.RetentionPerGiBMonth != 0.5 {
		t.Errorf("RetentionPerGiBMonth = %v; want 0.5", rc.RetentionPerGiBMonth)
	}
	if rc.ProcessedDataPerGiB != 3.0 || rc.ProcessedDataPerGiBFree != 4.0 {
		t.Errorf("ProcessedDataPerGiB = %v/%v; want 3.0/4.0 (paid/free)", rc.ProcessedDataPerGiB, rc.ProcessedDataPerGiBFree)
	}
	if rc.ScoresPer1k != 1.5 || rc.ScoresPer1kFree != 2.5 {
		t.Errorf("ScoresPer1k = %v/%v; want 1.5/2.5 (paid/free)", rc.ScoresPer1k, rc.ScoresPer1kFree)
	}

	// Per-plan grants: recurring credit starts at Basic; Free's grant is the
	// one-time StarterCreditUSD, not a recurring grant.
	wantGrant := map[string]float64{"free": 0, "basic": 20, "pro": 50}
	wantCap := map[string]float64{"free": 5, "basic": 10, "pro": 25}
	for tier, grant := range wantGrant {
		plan, ok := config.GetPlan(tier)
		if !ok {
			t.Fatalf("GetPlan(%q) missing", tier)
		}
		if plan.CreditGrantUSD != grant {
			t.Errorf("%s CreditGrantUSD = %v; want %v", tier, plan.CreditGrantUSD, grant)
		}
		if plan.InferenceCapUSD != wantCap[tier] {
			t.Errorf("%s InferenceCapUSD = %v; want %v", tier, plan.InferenceCapUSD, wantCap[tier])
		}
		if plan.InferenceCapUSD > 0 && grant > 0 && plan.InferenceCapUSD > grant {
			t.Errorf("%s inference cap %v exceeds grant %v", tier, plan.InferenceCapUSD, grant)
		}
	}
}

// TestChannelLimitsPerTier pins the channel entitlement matrix: every tier
// reaches every platform, Free connects three channels, and paid tiers are
// unlimited because MESSAGES_MONTHLY is the meter that actually prices
// channel usage. These limits are read by the CreateChannel gate
// (internal/api/grpc/channels/v1) and mirrored in
// internal/enterprise/ce_defaults.go, so a silent edit here changes what
// paying users can do.
func TestChannelLimitsPerTier(t *testing.T) {
	config, err := plans.Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}

	want := map[string]struct{ channels, bindings int64 }{
		"free":       {3, 3},
		"basic":      {-1, -1},
		"pro":        {-1, -1},
		"enterprise": {-1, -1},
	}

	for tier, expect := range want {
		limits := config.GetPlanLimits(tier)
		if limits == nil {
			t.Fatalf("GetPlanLimits(%q) = nil", tier)
		}
		for limitType, wantValue := range map[string]int64{
			"CHANNELS":         expect.channels,
			"CHANNEL_BINDINGS": expect.bindings,
		} {
			got, ok := limits.GetUsageLimit(limitType)
			if !ok {
				t.Errorf("%s tier is missing %s", tier, limitType)
				continue
			}
			if got != wantValue {
				t.Errorf("%s %s = %d; want %d", tier, limitType, got, wantValue)
			}
		}
	}

	// A tier that sells channels must also advertise the feature, or the
	// pricing table and the in-product plan card disagree with the gate.
	for _, tier := range []string{"free", "basic", "pro", "enterprise"} {
		plan, ok := config.GetPlan(tier)
		if !ok {
			t.Fatalf("GetPlan(%q) missing", tier)
		}
		enabled := false
		for _, f := range plan.Features {
			if f.Name == "channel_bindings" {
				enabled = f.Enabled
			}
		}
		if !enabled {
			t.Errorf("%s tier grants channels but does not enable the channel_bindings feature", tier)
		}
	}
}

// TestPlanLadderNeverGoesBackwards pins the one property a pricing ladder must
// have: paying more never takes something away.
//
// basic shipped with evaluations disabled while free, pro and enterprise all
// enabled it, and while basic itself provisioned fifty eval runs, fifty
// thousand dataset items and five annotation queues. The flag contradicted the
// limits either side of it, and the table is about to be published, so the
// invariant is now pinned rather than left to review.
func TestPlanLadderNeverGoesBackwards(t *testing.T) {
	config, err := plans.Embedded()
	if err != nil {
		t.Fatalf("Embedded() error = %v", err)
	}

	ladder := []string{"free", "basic", "pro", "enterprise"}

	type tierPlan struct {
		tier string
		plan *plans.Plan
	}
	ordered := make([]tierPlan, 0, len(ladder))
	for _, tier := range ladder {
		plan, ok := config.GetPlan(tier)
		if !ok {
			t.Fatalf("embedded plans have no %s tier", tier)
		}
		ordered = append(ordered, tierPlan{tier: tier, plan: plan})
	}

	featuresOf := func(p *plans.Plan) map[string]bool {
		out := make(map[string]bool, len(p.Features))
		for _, f := range p.Features {
			out[f.Name] = f.Enabled
		}
		return out
	}
	limitsOf := func(p *plans.Plan) map[string]int64 {
		out := make(map[string]int64, len(p.UsageLimits))
		for _, l := range p.UsageLimits {
			out[l.Type] = l.Value
		}
		return out
	}

	for i := 1; i < len(ordered); i++ {
		lower, higher := ordered[i-1], ordered[i]

		lowerFeatures, higherFeatures := featuresOf(lower.plan), featuresOf(higher.plan)
		for name, enabled := range lowerFeatures {
			if !enabled {
				continue
			}
			// A feature the higher tier does not mention is not a regression;
			// only an explicit false is.
			if present, ok := higherFeatures[name]; ok && !present {
				t.Errorf(
					"feature %q is enabled on %s but disabled on the more expensive %s tier",
					name, lower.tier, higher.tier,
				)
			}
		}

		lowerLimits, higherLimits := limitsOf(lower.plan), limitsOf(higher.plan)
		for name, lowerValue := range lowerLimits {
			higherValue, ok := higherLimits[name]
			if !ok {
				continue
			}
			// -1 is unlimited, so it may only be followed by unlimited.
			if lowerValue == -1 {
				if higherValue != -1 {
					t.Errorf(
						"limit %s is unlimited on %s but capped at %d on the more expensive %s tier",
						name, lower.tier, higherValue, higher.tier,
					)
				}
				continue
			}
			if higherValue != -1 && higherValue < lowerValue {
				t.Errorf(
					"limit %s drops from %d on %s to %d on the more expensive %s tier",
					name, lowerValue, lower.tier, higherValue, higher.tier,
				)
			}
		}

		if higher.plan.SeatLimit != 0 && higher.plan.SeatLimit < lower.plan.SeatLimit {
			t.Errorf("seat limit drops from %d on %s to %d on %s",
				lower.plan.SeatLimit, lower.tier, higher.plan.SeatLimit, higher.tier)
		}
		if higher.plan.InstanceLimit != 0 && higher.plan.InstanceLimit < lower.plan.InstanceLimit {
			t.Errorf("instance limit drops from %d on %s to %d on %s",
				lower.plan.InstanceLimit, lower.tier, higher.plan.InstanceLimit, higher.tier)
		}
	}
}

// The extra-instance fee is a decided price, not a placeholder
// (docs/design/instance-over-limit.md): $10 per instance per month.
func TestEmbeddedExtraInstancePrice(t *testing.T) {
	cfg, err := plans.Embedded()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CreditPricing == nil || cfg.CreditPricing.ExtraInstancePerMonth != 10.0 {
		t.Fatalf("extra_instance_per_month = %+v, want 10.0", cfg.CreditPricing)
	}
}
