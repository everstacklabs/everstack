package otelstatus

import "testing"

// The stored values seen in production otel_traces, plus the enum names the
// Everstack OTLP HTTP handler writes. A predicate from this package must
// classify every one of them correctly.
var storedStatuses = []string{
	"Ok", "Error", "Unset",
	"STATUS_CODE_OK", "STATUS_CODE_ERROR", "STATUS_CODE_UNSET",
}

// matches evaluates a rendered `lower(col) IN (...)` / `NOT IN (...)`
// predicate the way ClickHouse would, for a single concrete status value.
func matches(t *testing.T, clause, status string) bool {
	t.Helper()
	set, negated := parseMembership(t, clause)
	_, present := set[lower(status)]
	if negated {
		return !present
	}
	return present
}

func TestIsErrorMatchesShortAndEnumSpellings(t *testing.T) {
	clause := IsError(Column)

	// The short form is what every gateway-produced span actually carries.
	// Before this package existed the model-metrics view compared against
	// 'STATUS_CODE_ERROR' only, so "Error" scored zero and every model
	// reported a 1.0 success rate.
	if !matches(t, clause, "Error") {
		t.Errorf("IsError must match the short spelling %q, got clause %q", "Error", clause)
	}
	if !matches(t, clause, "STATUS_CODE_ERROR") {
		t.Errorf("IsError must match the enum spelling %q, got clause %q", "STATUS_CODE_ERROR", clause)
	}
	for _, ok := range []string{"Ok", "Unset", "STATUS_CODE_OK", "STATUS_CODE_UNSET"} {
		if matches(t, clause, ok) {
			t.Errorf("IsError must not match non-error status %q", ok)
		}
	}
}

func TestIsNotErrorIsExactComplementOfIsError(t *testing.T) {
	errClause := IsError(Column)
	notErrClause := IsNotError(Column)
	for _, status := range storedStatuses {
		if matches(t, errClause, status) == matches(t, notErrClause, status) {
			t.Errorf("IsNotError must be the complement of IsError for %q", status)
		}
	}
}

func TestIsOKExcludesUnset(t *testing.T) {
	clause := IsOK(Column)
	for _, want := range []string{"Ok", "STATUS_CODE_OK"} {
		if !matches(t, clause, want) {
			t.Errorf("IsOK must match %q", want)
		}
	}
	for _, notOK := range []string{"Error", "Unset", "STATUS_CODE_ERROR", "STATUS_CODE_UNSET"} {
		if matches(t, clause, notOK) {
			t.Errorf("IsOK must not match %q", notOK)
		}
	}
}

func TestPredicatesLowerCaseTheColumn(t *testing.T) {
	// ClickHouse "=" and IN are case-sensitive. Without the lower() wrapper a
	// span written as "ERROR" by some future exporter would score zero again.
	for _, clause := range []string{IsError(Column), IsNotError(Column), IsOK(Column)} {
		if got := clause[:len("lower(StatusCode)")]; got != "lower(StatusCode)" {
			t.Errorf("predicate must lower-case the column, got %q", clause)
		}
	}
}

func TestPredicatesAcceptAQualifiedColumn(t *testing.T) {
	clause := IsError("t.StatusCode")
	if want := "lower(t.StatusCode) IN ('error', 'status_code_error')"; clause != want {
		t.Errorf("IsError(%q) = %q, want %q", "t.StatusCode", clause, want)
	}
}
