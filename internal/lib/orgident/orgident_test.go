package orgident

import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestValidateSlugRejectsAnyIDSpelling(t *testing.T) {
	id := uuid.MustParse("6f1c2a3b-4d5e-4f60-8a7b-9c0d1e2f3a4b")
	s := id.String()
	hex := strings.ReplaceAll(s, "-", "")
	for name, slug := range map[string]string{
		"canonical":        s,
		"hyphenless":       hex,
		"uppercase":        strings.ToUpper(s),
		"uppercase nohyph": strings.ToUpper(hex),
		"braced":           "{" + s + "}",
		"urn":              "urn:uuid:" + s,
		"postgres groups":  hex[:4] + "-" + hex[4:8] + "-" + hex[8:12] + "-" + hex[12:16] + "-" + hex[16:20] + "-" + hex[20:24] + "-" + hex[24:28] + "-" + hex[28:],
		"nil uuid":         uuid.Nil.String(),
	} {
		if err := ValidateSlug(slug); !errors.Is(err, ErrInvalidSlug) {
			t.Errorf("%s: ValidateSlug(%q) = %v, want ErrInvalidSlug", name, slug, err)
		}
	}
}

func TestValidateSlugShape(t *testing.T) {
	for _, ok := range []string{"acme", "acme-co", "a1", "org-6f1c2a3b", strings.Repeat("a", 63), "deadbeef"} {
		if err := ValidateSlug(ok); err != nil {
			t.Errorf("ValidateSlug(%q) = %v, want ok", ok, err)
		}
	}
	for _, bad := range []string{"", "a", "Acme", "-acme", "acme-", "ac me", "a_b", "me@example.com", "a.b", strings.Repeat("a", 64)} {
		if err := ValidateSlug(bad); !errors.Is(err, ErrInvalidSlug) {
			t.Errorf("ValidateSlug(%q) = %v, want ErrInvalidSlug", bad, err)
		}
	}
}

func TestParseClassifiesOnce(t *testing.T) {
	id := uuid.New()
	for _, in := range []string{id.String(), strings.ToUpper(id.String()), strings.ReplaceAll(id.String(), "-", ""), "{" + id.String() + "}", "urn:uuid:" + id.String(), " " + id.String() + " "} {
		got, isID := Parse(in)
		if !isID || got != id.String() {
			t.Errorf("Parse(%q) = %q, %v; want canonical id", in, got, isID)
		}
	}
	for _, in := range []string{"acme", "me@example.com", "org-1"} {
		got, isID := Parse(in)
		if isID || got != in {
			t.Errorf("Parse(%q) = %q, %v; want slug", in, got, isID)
		}
	}
}

func TestFitSlugAlwaysValidates(t *testing.T) {
	id := uuid.NewString()
	for _, base := range []string{id, strings.ReplaceAll(id, "-", ""), "a", "org", strings.Repeat("ab-", 30), "acme"} {
		got := FitSlug(base)
		if err := ValidateSlug(got); err != nil {
			t.Errorf("FitSlug(%q) = %q: %v", base, got, err)
		}
		if err := ValidateSlug(got + "-100"); err != nil {
			t.Errorf("FitSlug(%q) + suffix = %q: %v", base, got+"-100", err)
		}
	}
	if FitSlug("acme") != "acme" {
		t.Errorf("FitSlug changed a valid slug")
	}
}
