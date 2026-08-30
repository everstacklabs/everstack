package serve

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// starCORSHandler stands in for the gateway-wide rs/cors middleware, which
// sets the wildcard headers before the route handler runs.
func starCORSHandler(t *testing.T) http.Handler {
	t.Helper()
	return publicModelMetricsCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

func serveWithWildcardAlreadySet(t *testing.T, origin string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	recorder.Header().Set("Access-Control-Allow-Origin", "*")
	recorder.Header().Set("Access-Control-Allow-Credentials", "true")

	request := httptest.NewRequest(http.MethodGet, "/api/model-metrics/v1/report", nil)
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	starCORSHandler(t).ServeHTTP(recorder, request)
	return recorder
}

func TestPublicModelMetricsCORSAllowsCatalogOrigin(t *testing.T) {
	t.Setenv("EVS_PUBLIC_MODEL_METRICS_ALLOWED_ORIGINS", "")

	recorder := serveWithWildcardAlreadySet(t, DefaultPublicModelMetricsOrigin)

	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != DefaultPublicModelMetricsOrigin {
		t.Fatalf("allow-origin = %q, want %q", got, DefaultPublicModelMetricsOrigin)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("allow-credentials = %q, want it removed", got)
	}
	if got := recorder.Header().Values("Vary"); len(got) == 0 {
		t.Fatal("expected Vary: Origin")
	}
}

func TestPublicModelMetricsCORSRejectsOtherOrigins(t *testing.T) {
	t.Setenv("EVS_PUBLIC_MODEL_METRICS_ALLOWED_ORIGINS", "")

	recorder := serveWithWildcardAlreadySet(t, "https://evil.example")

	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("allow-origin = %q, want empty for a disallowed origin", got)
	}
}

// The wildcard must be stripped even when no Origin header is present, so a
// cached response can never carry `*` back to a browser.
func TestPublicModelMetricsCORSStripsWildcardWithoutOrigin(t *testing.T) {
	t.Setenv("EVS_PUBLIC_MODEL_METRICS_ALLOWED_ORIGINS", "")

	recorder := serveWithWildcardAlreadySet(t, "")

	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("allow-origin = %q, want empty", got)
	}
}

func TestPublicModelMetricsOriginsIgnoresWildcardOverride(t *testing.T) {
	t.Setenv("EVS_PUBLIC_MODEL_METRICS_ALLOWED_ORIGINS", "*")

	origins := publicModelMetricsOrigins()
	if len(origins) != 1 || origins[0] != DefaultPublicModelMetricsOrigin {
		t.Fatalf("origins = %v, want the default only", origins)
	}
}

func TestPublicModelMetricsOriginsAcceptsExplicitList(t *testing.T) {
	t.Setenv(
		"EVS_PUBLIC_MODEL_METRICS_ALLOWED_ORIGINS",
		" https://everstack.ai , https://preview.everstack.ai ",
	)

	origins := publicModelMetricsOrigins()
	if len(origins) != 2 ||
		origins[0] != "https://everstack.ai" ||
		origins[1] != "https://preview.everstack.ai" {
		t.Fatalf("origins = %v", origins)
	}
}

func TestPublicModelMetricsFirstPartyTenantsDefaultsEmpty(t *testing.T) {
	t.Setenv("EVS_PUBLIC_MODEL_METRICS_FIRST_PARTY_TENANTS", "")

	if got := publicModelMetricsFirstPartyTenants(); got != nil {
		t.Fatalf("first-party tenants = %v, want nil so the carve-out stays off", got)
	}
}

func TestPublicModelMetricsFirstPartyTenantsParsesList(t *testing.T) {
	t.Setenv(
		"EVS_PUBLIC_MODEL_METRICS_FIRST_PARTY_TENANTS",
		" 8515093e-16b3-43fe-80eb-e742815391aa , ,e4409d61-0ff4-45bf-b9be-47bb0bd72986 ",
	)

	got := publicModelMetricsFirstPartyTenants()
	if len(got) != 2 ||
		got[0] != "8515093e-16b3-43fe-80eb-e742815391aa" ||
		got[1] != "e4409d61-0ff4-45bf-b9be-47bb0bd72986" {
		t.Fatalf("first-party tenants = %v", got)
	}
}
