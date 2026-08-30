package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"net/url"

	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// integrationRoundTripper wraps an http.RoundTripper and emits one `integration`
// span per outbound request (M1-T9). Inserting it as a client's Transport
// instruments every method that uses the client at a single point.
type integrationRoundTripper struct {
	base     http.RoundTripper
	provider string
}

// NewIntegrationRoundTripper wraps base (nil => http.DefaultTransport) so every
// request through the client emits an integration span tagged with provider
// (github | gitlab | linear | jira ...).
func NewIntegrationRoundTripper(base http.RoundTripper, provider string) http.RoundTripper {
	if base == nil {
		base = http.DefaultTransport
	}
	return &integrationRoundTripper{base: base, provider: provider}
}

func (rt *integrationRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, span := StartIntegrationSpan(req.Context(), rt.provider, req.Method, req.URL)
	defer span.End()

	resp, err := rt.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		RecordError(span, err)
		return resp, err
	}
	span.SetAttributes(attribute.Int(attrs.HTTPStatusCode, resp.StatusCode))
	// Surface server errors as span errors (feeds the Issues bridge). 4xx are
	// left as-is: for connectors a 404/permission check is often expected flow.
	if resp.StatusCode >= 500 {
		RecordError(span, fmt.Errorf("integration %s returned %d", rt.provider, resp.StatusCode))
	}
	return resp, nil
}

// StartIntegrationSpan starts a span for an outbound integration/connector call.
func StartIntegrationSpan(ctx context.Context, provider, method string, u *url.URL) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
		WithObservationType(ObservationTypeIntegration),
		WithObservationLevel(ObservationLevelDefault),
		trace.WithAttributes(
			attribute.String(attrs.IntegrationProvider, provider),
			attribute.String(attrs.HTTPMethod, method),
			attribute.String(attrs.HTTPURL, sanitizeURL(u)),
		),
	}

	tracer := GetGlobalTracerProvider().Tracer("everstack-integrations")
	ctx, span := tracer.Start(ctx, "integration."+provider, allOpts...)

	addCorrelationIDToSpan(ctx, span)

	return ctx, span
}

// sanitizeURL returns scheme://host/path with query and fragment stripped, so
// tokens or secrets that ride in query strings never reach a span attribute.
func sanitizeURL(u *url.URL) string {
	if u == nil {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path}).String()
}
