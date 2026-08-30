package policy

import "testing"

func TestPublicModelMetricsPathsBypassAuthentication(t *testing.T) {
	t.Parallel()

	policy := NewDefaultPolicy()
	for _, path := range []string{
		"/api/model-metrics/v1/report",
		"/api/model-metrics/v1/compare",
		"/api/model-metrics/v1/provider-models",
		"/everstack.modelmetrics.v1.PublicModelMetricsService/GetReport",
		"/everstack.modelmetrics.v1.PublicModelMetricsService/Compare",
		"/everstack.modelmetrics.v1.PublicModelMetricsService/GetProviderModelBreakdown",
	} {
		if !policy.ShouldBypassPath(path) {
			t.Fatalf("public model metrics path %q should bypass authentication", path)
		}
	}

	for _, path := range []string{
		"/api/model-metrics/v1/report/private",
		"/api/model-metrics/v1/compare/export",
		"/api/model-metrics/v1/provider-models/export",
		"/everstack.modelmetrics.v1.PublicModelMetricsService/DeleteReport",
	} {
		if policy.ShouldBypassPath(path) {
			t.Fatalf("non-public model metrics path %q must require authentication", path)
		}
	}
}
