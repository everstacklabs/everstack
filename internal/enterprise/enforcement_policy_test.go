package enterprise

import (
	"testing"

	dto "github.com/prometheus/client_model/go"
)

// Per-tier rollout policy (EVS-6 stage 0 slice 2). The policy is process
// global; every test restores all-shadow on cleanup so order doesn't matter.
func resetPolicy(t *testing.T) {
	t.Helper()
	SetShadowEnforcement(true)
	t.Cleanup(func() { SetShadowEnforcement(true) })
}

func TestParseEnforcementPolicy(t *testing.T) {
	def, over, err := ParseEnforcementPolicy("")
	if err != nil || def || len(over) != 0 {
		t.Fatalf("empty = (%v,%v,%v), want (false,nil,nil)", def, over, err)
	}
	def, over, err = ParseEnforcementPolicy("shadow")
	if err != nil || def || len(over) != 0 {
		t.Fatalf("shadow = (%v,%v,%v), want (false,nil,nil)", def, over, err)
	}
	def, over, err = ParseEnforcementPolicy("enforce")
	if err != nil || !def || len(over) != 0 {
		t.Fatalf("enforce = (%v,%v,%v), want (true,nil,nil)", def, over, err)
	}
	def, over, err = ParseEnforcementPolicy("free=enforce, basic=enforce, pro=shadow")
	if err != nil || def {
		t.Fatalf("per-tier = (%v,%v,%v), want default false", def, over, err)
	}
	if !over["free"] || !over["basic"] || over["pro"] {
		t.Fatalf("per-tier overrides = %v, want free+basic enforce, pro shadow", over)
	}
	for _, bad := range []string{"free", "free=yes", "gold=enforce", "free=enforce,bogus", "enforce,free=shadow"} {
		if _, _, err := ParseEnforcementPolicy(bad); err == nil {
			t.Fatalf("policy %q: want error, got nil", bad)
		}
	}
}

func TestTierEnforcedDefaultsToShadow(t *testing.T) {
	resetPolicy(t)
	for _, tier := range []string{"free", "basic", "pro", "enterprise", "", "gold"} {
		if TierEnforced(tier) {
			t.Fatalf("tier %q enforced by default, want shadow", tier)
		}
	}
	// Case-insensitive known tiers still resolve once enabled, not by default.
	if TierEnforced("PRO") {
		t.Fatal("PRO enforced by default, want shadow")
	}
}

func TestTierEnforcementRolloutAndKillSwitch(t *testing.T) {
	resetPolicy(t)

	if err := SetTierEnforcement("free", true); err != nil {
		t.Fatalf("enable free: %v", err)
	}
	if !TierEnforced("free") || !TierEnforced("FREE") {
		t.Fatal("free should enforce (case-insensitive)")
	}
	if TierEnforced("basic") {
		t.Fatal("basic must stay shadow while only free enforces")
	}
	if ShadowEnforcement() {
		t.Fatal("ShadowEnforcement must be false once any tier enforces")
	}

	// Per-tier kill switch: back to shadow without touching other tiers.
	if err := SetTierEnforcement("free", false); err != nil {
		t.Fatalf("kill free: %v", err)
	}
	if TierEnforced("free") {
		t.Fatal("free should be shadow after kill switch")
	}
	if !ShadowEnforcement() {
		t.Fatal("fully shadow again, want ShadowEnforcement true")
	}

	if err := SetTierEnforcement("gold", true); err == nil {
		t.Fatal("unknown tier must be rejected, not guessed")
	}
}

func TestLegacyShadowEnforcementCompat(t *testing.T) {
	resetPolicy(t)

	SetShadowEnforcement(false) // legacy global flip
	for _, tier := range []string{"free", "basic", "pro", "enterprise"} {
		if !TierEnforced(tier) {
			t.Fatalf("tier %s should enforce after legacy global flip", tier)
		}
	}
	if ShadowEnforcement() {
		t.Fatal("ShadowEnforcement must be false after legacy global flip")
	}

	SetShadowEnforcement(true)
	for _, tier := range []string{"free", "basic", "pro", "enterprise"} {
		if TierEnforced(tier) {
			t.Fatalf("tier %s should shadow after reset", tier)
		}
	}
}

func TestEnforcementGaugeMirrorsPolicy(t *testing.T) {
	resetPolicy(t)
	gauge := func(tier string) float64 {
		t.Helper()
		m := &dto.Metric{}
		if err := entitlementTierEnforced.WithLabelValues(tier).Write(m); err != nil {
			t.Fatalf("gauge write: %v", err)
		}
		return m.GetGauge().GetValue()
	}

	SetEnforcementPolicy(false, map[string]bool{"pro": true})
	if gauge("pro") != 1 || gauge("free") != 0 {
		t.Fatalf("gauge pro=%v free=%v, want 1/0", gauge("pro"), gauge("free"))
	}
	SetEnforcementPolicy(true, map[string]bool{"pro": false})
	if gauge("pro") != 0 || gauge("free") != 1 {
		t.Fatalf("gauge pro=%v free=%v, want 0/1", gauge("pro"), gauge("free"))
	}
}

// Cloud enforces plan caps by default (owner decision 2026-10-06). Unset
// means enforce every tier; "shadow" is the kill switch; per-tier entries
// layer on top of enforcement, so "pro=shadow" exempts only pro.
func TestManagedEnforcementPolicyDefaultsOn(t *testing.T) {
	cases := []struct {
		raw       string
		wantDef   bool
		wantOver  map[string]bool
		wantError bool
	}{
		{raw: "", wantDef: true},
		{raw: "  ", wantDef: true},
		{raw: "enforce", wantDef: true},
		{raw: "shadow", wantDef: false},
		{raw: "SHADOW", wantDef: false},
		{raw: "pro=shadow", wantDef: true, wantOver: map[string]bool{"pro": false}},
		{raw: "free=enforce,basic=shadow", wantDef: true, wantOver: map[string]bool{"free": true, "basic": false}},
		{raw: "free=maybe", wantError: true},
	}
	for _, tc := range cases {
		def, over, err := ManagedEnforcementPolicy(tc.raw)
		if tc.wantError {
			if err == nil {
				t.Fatalf("%q: want an error", tc.raw)
			}
			continue
		}
		if err != nil || def != tc.wantDef || len(over) != len(tc.wantOver) {
			t.Fatalf("%q = (%v, %v, %v), want (%v, %v)", tc.raw, def, over, err, tc.wantDef, tc.wantOver)
		}
		for tier, want := range tc.wantOver {
			if over[tier] != want {
				t.Fatalf("%q: override %s = %v, want %v", tc.raw, tier, over[tier], want)
			}
		}
	}

	// Applied, the unset default enforces every catalog tier.
	resetPolicy(t)
	def, over, _ := ManagedEnforcementPolicy("")
	SetEnforcementPolicy(def, over)
	for _, tier := range []string{"free", "basic", "pro", "enterprise"} {
		if !TierEnforced(tier) {
			t.Fatalf("tier %s in shadow under the Cloud default", tier)
		}
	}
}
