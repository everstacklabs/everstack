package billingmeter

import (
	"testing"
	"time"
)

// A reporter that starts and never succeeds must still age, so the stall
// alert fires for a reporter wedged from its first tick; a success moves the
// timestamp forward and a later start (a restart) never moves it back.
func TestUsageReportLastSuccessAgesFromStart(t *testing.T) {
	const reporter = "test-ages-from-start"
	start := time.Unix(1_800_000_000, 0)

	UsageReporterStarted(reporter, start)
	if got := UsageReportLastSuccess(reporter); got != float64(start.Unix()) {
		t.Fatalf("after start = %v, want the start time %d", got, start.Unix())
	}

	ok := start.Add(time.Minute)
	UsageReportSucceeded(reporter, ok)
	if got := UsageReportLastSuccess(reporter); got != float64(ok.Unix()) {
		t.Fatalf("after success = %v, want %d", got, ok.Unix())
	}

	UsageReporterStarted(reporter, start)
	if got := UsageReportLastSuccess(reporter); got != float64(ok.Unix()) {
		t.Fatalf("a second start moved last success back to %v, want %d", got, ok.Unix())
	}
}
