package telemetry

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// fakeRT returns a canned response with the given status.
type fakeRT struct{ status int }

func (f *fakeRT) RoundTrip(*http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: f.status,
		Status:     http.StatusText(f.status),
		Body:       io.NopCloser(strings.NewReader("{}")),
		Header:     make(http.Header),
	}, nil
}

func TestIntegrationRoundTripper(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	prev := GetGlobalTracerProvider()
	SetGlobalTracerProvider(tp)
	defer SetGlobalTracerProvider(prev)

	rt := NewIntegrationRoundTripper(&fakeRT{status: 200}, "github")
	req, _ := http.NewRequest(http.MethodGet, "https://api.github.com/repos?token=secret&page=2", nil)
	resp, err := rt.RoundTrip(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("roundtrip: status=%d err=%v", resp.StatusCode, err)
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 || spans[0].Name != "integration.github" {
		t.Fatalf("expected integration.github span, got %+v", spans)
	}
	got := map[string]string{}
	geti := map[string]int64{}
	for _, a := range spans[0].Attributes {
		got[string(a.Key)] = a.Value.AsString()
		geti[string(a.Key)] = a.Value.AsInt64()
	}
	if got[attrs.ObservationType] != string(ObservationTypeIntegration) {
		t.Errorf("observation.type = %q, want INTEGRATION", got[attrs.ObservationType])
	}
	if got[attrs.IntegrationProvider] != "github" || got[attrs.HTTPMethod] != "GET" {
		t.Errorf("provider/method wrong: %+v", got)
	}
	// Query string (including the token) must be stripped.
	if got[attrs.HTTPURL] != "https://api.github.com/repos" {
		t.Errorf("url not sanitized: %q", got[attrs.HTTPURL])
	}
	if geti[attrs.HTTPStatusCode] != 200 {
		t.Errorf("status code attr = %d, want 200", geti[attrs.HTTPStatusCode])
	}
}

func TestSanitizeURL(t *testing.T) {
	u, _ := url.Parse("https://h.io/p/x?secret=1#frag")
	if got := sanitizeURL(u); got != "https://h.io/p/x" {
		t.Errorf("sanitizeURL = %q", got)
	}
	if got := sanitizeURL(nil); got != "" {
		t.Errorf("sanitizeURL(nil) = %q, want empty", got)
	}
}
