package license_monitor

import (
	"context"
	"testing"
	"time"
)

func TestCalculateDeltaReportsSandboxMetersWithoutLLMTraffic(t *testing.T) {
	s := &UsageSyncer{
		lastSyncTime: time.Date(2026, 7, 22, 10, 0, 0, 0, time.UTC),
		getCounts: func(context.Context) ResourceCounts {
			return ResourceCounts{
				ConcurrentSandboxes:      4,
				SandboxComputeSeconds:    7_200,
				SandboxComputeCostMicros: 123_456,
				BrowserRuntimeSeconds:    3_600,
				BrowserRuntimeCostMicros: 10_000,
			}
		},
	}

	report := s.calculateDelta(UsageStats{})
	if report == nil {
		t.Fatal("expected resource heartbeat when sandbox compute changes without LLM traffic")
	}
	if got := report.GetCumulativeSandboxComputeSeconds(); got != 7_200 {
		t.Fatalf("sandbox compute seconds = %d, want 7200", got)
	}
	if got := report.GetCumulativeSandboxComputeCostMicros(); got != 123_456 {
		t.Fatalf("sandbox compute cost = %d, want 123456", got)
	}
	if got := report.GetConcurrentSandboxesCount(); got != 4 {
		t.Fatalf("concurrent sandboxes = %d, want 4", got)
	}
	if got := report.GetCumulativeBrowserRuntimeSeconds(); got != 3_600 {
		t.Fatalf("browser runtime seconds = %d, want 3600", got)
	}
	if got := report.GetCumulativeBrowserRuntimeCostMicros(); got != 10_000 {
		t.Fatalf("browser runtime cost = %d, want 10000", got)
	}
}
