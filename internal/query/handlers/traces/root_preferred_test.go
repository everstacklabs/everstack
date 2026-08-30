package traces

import (
	"strings"
	"testing"
)

// The generated fragment must prefer the root span's value and only then fall
// back, otherwise a child span could shadow the authoritative one.
func TestRootPreferredChecksRootFirst(t *testing.T) {
	got := rootPreferred("SpanAttributes['session.id']")

	rootIdx := strings.Index(got, "ParentSpanId = ''")
	if rootIdx == -1 {
		t.Fatalf("no root-scoped aggregate in %q", got)
	}
	fallbackIdx := strings.Index(got, "!= ''")
	if fallbackIdx == -1 {
		t.Fatalf("no non-empty fallback in %q", got)
	}
	if rootIdx > fallbackIdx {
		t.Fatalf("fallback is evaluated before the root value in %q", got)
	}
	if !strings.HasSuffix(got, ", '')") {
		t.Fatalf("fragment must end in an empty-string default, got %q", got)
	}
}

// rootPreferred inlines its argument several times. A bound placeholder would
// therefore be duplicated and silently shift every later parameter, which is a
// whole class of hard-to-spot query corruption. Every caller passes a fragment
// from this package, all of which inline their attribute names.
func TestRootPreferredCallersPassPlaceholderFreeFragments(t *testing.T) {
	for name, frag := range map[string]string{
		"session": sessionSQL(),
		"user":    userSQL(),
		"input":   traceInputSQL(),
		"output":  traceOutputSQL(),
	} {
		if strings.Contains(frag, "?") {
			t.Fatalf("%s fragment contains a bound placeholder and is unsafe to inline: %q", name, frag)
		}
		if strings.Contains(rootPreferred(frag), "?") {
			t.Fatalf("%s produced a placeholder after wrapping", name)
		}
	}
}

// Tenant semantic mappings widen the fragment. The wrapped form must still be
// placeholder-free, since mappings are the one caller-influenced input here.
func TestRootPreferredSafeWithSemanticMappings(t *testing.T) {
	frag := sessionSQL("my_tenant.conversation", "another.key")
	got := rootPreferred(frag)

	if strings.Contains(got, "?") {
		t.Fatalf("semantic mapping introduced a placeholder: %q", got)
	}
	if !strings.Contains(got, "my_tenant.conversation") {
		t.Fatalf("tenant attribute key was dropped: %q", got)
	}
}
