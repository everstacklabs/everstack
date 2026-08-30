package serve

import (
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/modelmetrics"
)

func TestPublicModelMetricsEnabled(t *testing.T) {
	t.Run("defaults on for managed gateway", func(t *testing.T) {
		t.Setenv("EVS_PUBLIC_MODEL_METRICS_ENABLED", "")
		if !publicModelMetricsEnabled(true) {
			t.Fatal("expected managed gateway metrics to default on")
		}
	})

	t.Run("can be disabled for managed gateway", func(t *testing.T) {
		t.Setenv("EVS_PUBLIC_MODEL_METRICS_ENABLED", "false")
		if publicModelMetricsEnabled(true) {
			t.Fatal("expected explicit false to disable metrics")
		}
	})

	t.Run("cannot be enabled for self hosted gateway", func(t *testing.T) {
		t.Setenv("EVS_PUBLIC_MODEL_METRICS_ENABLED", "true")
		if publicModelMetricsEnabled(false) {
			t.Fatal("expected metrics to stay disabled outside managed cloud")
		}
	})
}

func TestPositiveEnvUint64(t *testing.T) {
	t.Run("uses configured positive value", func(t *testing.T) {
		t.Setenv("EVS_PUBLIC_MODEL_METRICS_MIN_TENANTS", "12")
		if got := positiveEnvUint64("EVS_PUBLIC_MODEL_METRICS_MIN_TENANTS", 5); got != 12 {
			t.Fatalf("positiveEnvUint64() = %d, want 12", got)
		}
	})

	for _, value := range []string{"", "0", "-2", "invalid"} {
		t.Run("falls back for "+value, func(t *testing.T) {
			t.Setenv("EVS_PUBLIC_MODEL_METRICS_MIN_TENANTS", value)
			if got := positiveEnvUint64("EVS_PUBLIC_MODEL_METRICS_MIN_TENANTS", 5); got != 5 {
				t.Fatalf("positiveEnvUint64() = %d, want fallback 5", got)
			}
		})
	}
}

func TestEnvUint64AtLeastEnforcesPrivacyFloor(t *testing.T) {
	t.Setenv("EVS_PUBLIC_MODEL_METRICS_MIN_TENANTS", "1")
	if got := envUint64AtLeast("EVS_PUBLIC_MODEL_METRICS_MIN_TENANTS", 5); got != 5 {
		t.Fatalf("envUint64AtLeast() = %d, want floor 5", got)
	}

	t.Setenv("EVS_PUBLIC_MODEL_METRICS_MIN_TENANTS", "12")
	if got := envUint64AtLeast("EVS_PUBLIC_MODEL_METRICS_MIN_TENANTS", 5); got != 12 {
		t.Fatalf("envUint64AtLeast() = %d, want configured value 12", got)
	}
}

func TestPublicModelMetricsConfigAllowsTimeBoundTestingThresholds(t *testing.T) {
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	testingUntil := now.Add(time.Hour)
	t.Setenv(
		"EVS_PUBLIC_MODEL_METRICS_TESTING_UNTIL",
		testingUntil.Format(time.RFC3339),
	)
	t.Setenv("EVS_PUBLIC_MODEL_METRICS_MIN_TENANTS", "1")
	t.Setenv("EVS_PUBLIC_MODEL_METRICS_MIN_REQUESTS", "1")

	config := publicModelMetricsConfig(now)
	if !config.TestingThresholdsUntil.Equal(testingUntil) {
		t.Fatalf(
			"TestingThresholdsUntil = %v, want %v",
			config.TestingThresholdsUntil,
			testingUntil,
		)
	}
	if config.MinimumTenants != 1 || config.MinimumRequests != 1 {
		t.Fatalf(
			"thresholds = %d/%d, want 1/1",
			config.MinimumTenants,
			config.MinimumRequests,
		)
	}
}

func TestPublicModelMetricsConfigRejectsInvalidTestingWindows(t *testing.T) {
	now := time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)
	for name, value := range map[string]string{
		"invalid":  "tomorrow",
		"expired":  now.Add(-time.Second).Format(time.RFC3339),
		"too_long": now.Add(modelmetrics.MaximumTestingThresholdWindow + time.Second).Format(time.RFC3339),
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("EVS_PUBLIC_MODEL_METRICS_TESTING_UNTIL", value)
			t.Setenv("EVS_PUBLIC_MODEL_METRICS_MIN_TENANTS", "1")
			t.Setenv("EVS_PUBLIC_MODEL_METRICS_MIN_REQUESTS", "1")

			config := publicModelMetricsConfig(now)
			if !config.TestingThresholdsUntil.IsZero() {
				t.Fatalf(
					"TestingThresholdsUntil = %v, want rejected window",
					config.TestingThresholdsUntil,
				)
			}
			if config.MinimumTenants != modelmetrics.MinimumPublicTenants ||
				config.MinimumRequests != modelmetrics.MinimumPublicRequests {
				t.Fatalf("thresholds = %#v, want public privacy floors", config)
			}
		})
	}
}
