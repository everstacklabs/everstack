package otelstatus

import "testing"

// A caller-supplied status filter has to survive the round trip from the
// vocabulary the API exposes to the vocabulary otel_traces stores. These are
// the spellings that reach the trace APIs in practice.
func TestNormalizeAcceptsEverySpellingCallersSend(t *testing.T) {
	cases := map[string]string{
		// What the trace APIs have always documented and what the admin UI sends.
		"OK":    CanonicalOK,
		"ERROR": CanonicalError,
		"UNSET": CanonicalUnset,
		// What otel_traces actually stores (collector exporter). A caller that
		// echoes a status straight back off a span used to be rejected.
		"Ok":    CanonicalOK,
		"Error": CanonicalError,
		"Unset": CanonicalUnset,
		// What the Everstack OTLP HTTP handler writes.
		"STATUS_CODE_OK":    CanonicalOK,
		"STATUS_CODE_ERROR": CanonicalError,
		"STATUS_CODE_UNSET": CanonicalUnset,
		// Whitespace and case are not a caller's problem.
		"  error  ": CanonicalError,
		"oK":        CanonicalOK,
	}
	for value, want := range cases {
		got, ok := Normalize(value)
		if !ok {
			t.Errorf("Normalize(%q) rejected a spelling callers legitimately send", value)
			continue
		}
		if got != want {
			t.Errorf("Normalize(%q) = %q, want %q", value, got, want)
		}
	}
}

func TestNormalizeRejectsUnknownValues(t *testing.T) {
	for _, value := range []string{"", "FAILED", "200", "ok!", "status_code"} {
		if got, ok := Normalize(value); ok {
			t.Errorf("Normalize(%q) = %q, want rejection", value, got)
		}
	}
}

// Equals is the seam that replaces `StatusCode = ?`. Binding a caller's value
// as a parameter compared it to a single stored spelling and therefore matched
// nothing; every accepted value must now compile to a membership test.
func TestEqualsCompilesToAMembershipPredicate(t *testing.T) {
	cases := []struct {
		value string
		want  string
	}{
		{"OK", IsOK(Column)},
		{"Ok", IsOK(Column)},
		{"STATUS_CODE_OK", IsOK(Column)},
		{"ERROR", IsError(Column)},
		{"Error", IsError(Column)},
		{"STATUS_CODE_ERROR", IsError(Column)},
		{"UNSET", IsUnset(Column)},
		{"Unset", IsUnset(Column)},
		{"STATUS_CODE_UNSET", IsUnset(Column)},
	}
	for _, tc := range cases {
		got, err := Equals(Column, tc.value)
		if err != nil {
			t.Errorf("Equals(%q) returned %v, want a predicate", tc.value, err)
			continue
		}
		if got != tc.want {
			t.Errorf("Equals(%q) = %q, want %q", tc.value, got, tc.want)
		}
	}
}

func TestEqualsRejectsUnknownStatus(t *testing.T) {
	if _, err := Equals(Column, "FAILED"); err == nil {
		t.Fatal("Equals must reject an unknown status instead of building a predicate that matches nothing")
	}
}

func TestIsUnsetMatchesBothSpellingsAndNothingElse(t *testing.T) {
	clause := IsUnset(Column)
	for _, want := range []string{"Unset", "STATUS_CODE_UNSET"} {
		if !matches(t, clause, want) {
			t.Errorf("IsUnset must match %q", want)
		}
	}
	for _, notUnset := range []string{"Ok", "Error", "STATUS_CODE_OK", "STATUS_CODE_ERROR"} {
		if matches(t, clause, notUnset) {
			t.Errorf("IsUnset must not match %q", notUnset)
		}
	}
}

func TestIsNotUnsetIsExactComplementOfIsUnset(t *testing.T) {
	unset := IsUnset(Column)
	notUnset := IsNotUnset(Column)
	for _, status := range storedStatuses {
		if matches(t, unset, status) == matches(t, notUnset, status) {
			t.Errorf("IsNotUnset must be the complement of IsUnset for %q", status)
		}
	}
}
