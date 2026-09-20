package v1

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/query"
)

// A failed request must not read as a success.
//
// Verified live 2026-09-07: a chat completion that returned HTTP 500 produced
// a trace reporting status "success" with errorCount 1, because the provider
// failure lands on a child span while the gateway leaves its own root span
// Unset. Measured across a week of spans, 54 of 57 traces containing a failed
// child had an Unset root, so roughly 90% of failures were reported green.
func TestTraceStatusReflectsChildSpanErrors(t *testing.T) {
	cases := []struct {
		name       string
		rootStatus string
		errorCount uint64
		want       string
	}{
		// The reported bug: provider failed on a child, root left Unset.
		{name: "unset root with a failed child", rootStatus: "STATUS_CODE_UNSET", errorCount: 1, want: "error"},
		{name: "short-form unset root with a failed child", rootStatus: "Unset", errorCount: 1, want: "error"},
		{name: "empty root status with a failed child", rootStatus: "", errorCount: 1, want: "error"},

		// The second bug: the enum spelling was never matched. 79 of 90 error
		// spans carry STATUS_CODE_ERROR, so this was the common case.
		{name: "enum-spelled error root", rootStatus: "STATUS_CODE_ERROR", errorCount: 0, want: "error"},
		{name: "short-form error root", rootStatus: "Error", errorCount: 0, want: "error"},
		{name: "lowercased error root", rootStatus: "error", errorCount: 0, want: "error"},

		// Genuine successes must stay successes.
		{name: "ok root, no errors", rootStatus: "STATUS_CODE_OK", errorCount: 0, want: "success"},
		{name: "short-form ok root, no errors", rootStatus: "Ok", errorCount: 0, want: "success"},
		{name: "unset root, no errors", rootStatus: "STATUS_CODE_UNSET", errorCount: 0, want: "success"},
		{name: "in-flight trace with no root yet", rootStatus: "", errorCount: 0, want: "success"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := toProtoTrace(&query.TraceReadModel{
				TraceID:    "trace-1",
				RootStatus: testCase.rootStatus,
				ErrorCount: testCase.errorCount,
			})
			if got.GetStatus() != testCase.want {
				t.Fatalf("status = %q, want %q (root=%q errorCount=%d)",
					got.GetStatus(), testCase.want, testCase.rootStatus, testCase.errorCount)
			}
		})
	}
}

// The error count itself must still be reported faithfully alongside status,
// since the two disagreeing is what made the original bug visible.
func TestTraceErrorCountIsPreserved(t *testing.T) {
	got := toProtoTrace(&query.TraceReadModel{
		TraceID: "trace-2", RootStatus: "STATUS_CODE_UNSET", ErrorCount: 3,
	})
	if got.GetErrorCount() != 3 {
		t.Fatalf("errorCount = %d, want 3", got.GetErrorCount())
	}
	if got.GetStatus() != "error" {
		t.Fatalf("status = %q, want error", got.GetStatus())
	}
}
