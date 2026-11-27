package v1

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/billingmeter"
)

// The platform key pool must stay closed while inference admission is
// shadow-only: a shadow admission never refuses a request, so an org on the
// allowlist with an empty wallet would be served on platform keys for free.
func TestPlatformKeyPoolClosedWhileAdmissionIsShadow(t *testing.T) {
	got, closed := platformKeyPoolPolicy("org-a,org-b", false, "")
	if len(got) != 0 {
		t.Fatalf("allowlist = %v, want empty while admission is shadow", got)
	}
	if !closed {
		t.Fatal("policy must report that it closed a non-empty allowlist")
	}
}

func TestPlatformKeyPoolOpensOnceAdmissionEnforces(t *testing.T) {
	got, closed := platformKeyPoolPolicy("org-a, ORG-B", true, "")
	if closed || len(got) != 2 {
		t.Fatalf("allowlist = %v closed=%t, want both orgs once admission enforces", got, closed)
	}
	if _, ok := got["org-b"]; !ok {
		t.Fatalf("allowlist = %v, want normalized org-b", got)
	}
}

func TestPlatformKeyPoolShadowOverrideIsExplicit(t *testing.T) {
	for _, v := range []string{"true", "TRUE", " true "} {
		got, closed := platformKeyPoolPolicy("org-a", false, v)
		if closed || len(got) != 1 {
			t.Fatalf("override %q: allowlist = %v closed=%t, want org-a served", v, got, closed)
		}
	}
	for _, v := range []string{"", "1", "yes", "false", "on"} {
		if got, closed := platformKeyPoolPolicy("org-a", false, v); len(got) != 0 || !closed {
			t.Fatalf("override %q must not open the pool: allowlist = %v closed=%t", v, got, closed)
		}
	}
}

func TestPlatformKeyPoolEmptyAllowlistIsNotAnError(t *testing.T) {
	got, closed := platformKeyPoolPolicy("  ", false, "")
	if len(got) != 0 || closed {
		t.Fatalf("empty allowlist: got %v closed=%t, want nothing and no error", got, closed)
	}
}

// The guard keys off one code-level fact. When the enforcing admission PR
// lands it flips billingmeter.InferenceAdmissionEnforced and must update this
// test in the same change.
func TestInferenceAdmissionIsStillShadow(t *testing.T) {
	if billingmeter.InferenceAdmissionEnforced {
		t.Fatal("inference admission is marked enforced; remove the platform key pool shadow guard deliberately, not by accident")
	}
}
