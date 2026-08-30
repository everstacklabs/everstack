package redact

import (
	"reflect"
	"strings"
	"testing"
)

func TestRedactSSN(t *testing.T) {
	r := Redact("my ssn is 123-45-6789 ok")
	if strings.Contains(r.Text, "123-45-6789") {
		t.Errorf("SSN not redacted: %q", r.Text)
	}
	if !strings.Contains(r.Text, "[REDACTED_SSN]") {
		t.Errorf("missing SSN placeholder: %q", r.Text)
	}
	if !reflect.DeepEqual(r.Found, []Kind{KindSSN}) {
		t.Errorf("Found = %v, want [ssn]", r.Found)
	}
}

func TestRedactEmail(t *testing.T) {
	r := Redact("reach me at alice.b@example.co.uk please")
	if strings.Contains(r.Text, "alice.b@example.co.uk") {
		t.Errorf("email not redacted: %q", r.Text)
	}
	if !reflect.DeepEqual(r.Found, []Kind{KindEmail}) {
		t.Errorf("Found = %v, want [email]", r.Found)
	}
}

func TestRedactCreditCard(t *testing.T) {
	for _, in := range []string{"card 4111 1111 1111 1111 here", "card 4111111111111111 here"} {
		r := Redact(in)
		if strings.Contains(r.Text, "4111") {
			t.Errorf("CC not redacted in %q: %q", in, r.Text)
		}
		if !reflect.DeepEqual(r.Found, []Kind{KindCreditCard}) {
			t.Errorf("Found = %v, want [credit_card] for %q", r.Found, in)
		}
	}
}

func TestRedactMultipleSortedDeduped(t *testing.T) {
	r := Redact("email a@b.com and a@b.com, ssn 111-22-3333")
	if !reflect.DeepEqual(r.Found, []Kind{KindEmail, KindSSN}) {
		t.Errorf("Found = %v, want sorted+deduped [email ssn]", r.Found)
	}
}

func TestRedactLeavesCleanTextUntouched(t *testing.T) {
	in := "no pii here, just the number 42 and the date 2026-06-21"
	r := Redact(in)
	if r.Text != in {
		t.Errorf("clean text changed: %q -> %q", in, r.Text)
	}
	if len(r.Found) != 0 {
		t.Errorf("found PII in clean text: %v", r.Found)
	}
}

func TestHasPII(t *testing.T) {
	if !HasPII("ssn 123-45-6789") {
		t.Error("HasPII should detect SSN")
	}
	if HasPII("just a normal sentence") {
		t.Error("HasPII false positive")
	}
}
