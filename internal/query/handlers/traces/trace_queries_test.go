package traces

import (
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/telemetry/otelstatus"
)

func listTracesQueryWithStatus(status string) *ListTracesQuery {
	now := time.Unix(1_700_000_000, 0)
	q := NewListTracesQuery("tenant-1", now.Add(-time.Hour), now, "", "", status, "", "", "")
	q.Limit = 50
	return q
}

// "OK", "ERROR" and "UNSET" are the values callers already send. Widening the
// accepted set must not drop any of them.
func TestListTracesQueryValidateKeepsExistingStatusVocabulary(t *testing.T) {
	for _, status := range []string{"", "OK", "ERROR", "UNSET"} {
		if err := listTracesQueryWithStatus(status).Validate(); err != nil {
			t.Errorf("status_code %q must stay valid for existing callers, got %v", status, err)
		}
	}
}

// otel_traces stores "Ok"/"Error"/"Unset", and the Everstack OTLP HTTP handler
// writes the protobuf enum names. A caller echoing a status straight back off a
// span used to be rejected outright.
func TestListTracesQueryValidateAcceptsStoredAndEnumSpellings(t *testing.T) {
	for _, status := range []string{
		"Ok", "Error", "Unset",
		"STATUS_CODE_OK", "STATUS_CODE_ERROR", "STATUS_CODE_UNSET",
	} {
		if err := listTracesQueryWithStatus(status).Validate(); err != nil {
			t.Errorf("status_code %q must validate, got %v", status, err)
		}
	}
}

func TestListTracesQueryValidateRejectsUnknownStatus(t *testing.T) {
	for _, status := range []string{"FAILED", "200", "okay"} {
		if err := listTracesQueryWithStatus(status).Validate(); err == nil {
			t.Errorf("status_code %q must be rejected", status)
		}
	}
}

// The filter used to bind the caller's value into `StatusCode = ?`. Since the
// stored spellings are "Ok"/"Error"/"Unset", every value a caller was allowed
// to send matched zero rows and the trace list came back empty with no error.
func TestListTracesStatusConditionMatchesStoredSpellings(t *testing.T) {
	cases := []struct {
		status string
		want   string
	}{
		{"", ""},
		{"OK", otelstatus.IsOK(otelstatus.Column)},
		{"ERROR", otelstatus.IsError(otelstatus.Column)},
		{"UNSET", otelstatus.IsUnset(otelstatus.Column)},
		{"Ok", otelstatus.IsOK(otelstatus.Column)},
		{"Error", otelstatus.IsError(otelstatus.Column)},
		{"STATUS_CODE_ERROR", otelstatus.IsError(otelstatus.Column)},
	}
	for _, tc := range cases {
		got, err := listTracesStatusCondition(tc.status)
		if err != nil {
			t.Errorf("listTracesStatusCondition(%q) returned %v", tc.status, err)
			continue
		}
		if got != tc.want {
			t.Errorf("listTracesStatusCondition(%q) = %q, want %q", tc.status, got, tc.want)
		}
		if tc.status != "" && got == "StatusCode = ?" {
			t.Errorf("listTracesStatusCondition(%q) still binds a literal comparison that matches no stored row", tc.status)
		}
	}
}

func TestListTracesStatusConditionRejectsUnknownStatus(t *testing.T) {
	if _, err := listTracesStatusCondition("FAILED"); err == nil {
		t.Fatal("an unrecognised status must error rather than silently return every trace or none")
	}
}
