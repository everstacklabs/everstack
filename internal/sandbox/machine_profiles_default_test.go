package sandbox

import "testing"

// The managed-compute policy demands an exact (CPU, MemoryMB, DiskMB) match
// against ManagedMachineProfiles. Anything that supplies sizing for a managed
// sandbox therefore has to land on a profile, and the default config is what
// every unset code path falls back to.
//
// This is a regression test with a real incident behind it: the persistent
// agent reprovision path carried its own hand-picked fallback of
// 1.0 / 512 / 2048, which matches no profile. Every persistent agent whose
// sizing columns were unset failed reprovisioning with
// ErrUnsupportedSandboxSize, permanently, and the error blamed the caller for
// choosing a custom size they had never chosen.
func TestDefaultSandboxConfigIsAValidManagedProfile(t *testing.T) {
	cfg := DefaultSandboxConfig()

	profile, ok := MatchManagedMachineProfile(cfg)
	if !ok {
		t.Fatalf(
			"DefaultSandboxConfig() = %.1f CPU / %d MiB / %d MiB disk, which matches no managed machine profile.\n"+
				"Every managed sandbox created without explicit sizing falls back to this, so it must be a profile.",
			cfg.CPULimit, cfg.MemoryMB, cfg.DiskMB,
		)
	}
	t.Logf("default resolves to the %q profile", profile.ID)
}

// The default also has to be usable on the smallest plan, or agents on free
// tier get a "not available on your plan" error for a size they never picked.
func TestDefaultSandboxConfigFitsTheSmallestTier(t *testing.T) {
	if err := ValidateManagedMachineProfile(DefaultSandboxConfig(), "free"); err != nil {
		t.Fatalf("the default config must validate on the free tier, got: %v", err)
	}
}

// An unknown tier falls back to the free-tier cap, so it must behave the same.
func TestDefaultSandboxConfigValidatesForUnknownTier(t *testing.T) {
	for _, tier := range []string{"", "  ", "not-a-real-tier"} {
		if err := ValidateManagedMachineProfile(DefaultSandboxConfig(), tier); err != nil {
			t.Fatalf("default config rejected for tier %q: %v", tier, err)
		}
	}
}

// Pins the shape of the failure the incident produced, so that if someone
// reintroduces a non-profile fallback the test names what went wrong.
func TestNonProfileSizingIsRejected(t *testing.T) {
	cases := []struct {
		name string
		cfg  SandboxConfig
	}{
		{"the old reprovision fallback", SandboxConfig{CPULimit: 1.0, MemoryMB: 512, DiskMB: 2048}},
		{"legacy stored sizing", SandboxConfig{CPULimit: 1.0, MemoryMB: 512, DiskMB: 1024}},
		{"zero values", SandboxConfig{}},
		{"right cpu and memory, wrong disk", SandboxConfig{CPULimit: 1, MemoryMB: 1024, DiskMB: 2048}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := MatchManagedMachineProfile(tc.cfg); ok {
				t.Fatal("expected no profile match")
			}
		})
	}
}
