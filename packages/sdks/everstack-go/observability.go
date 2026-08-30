package everstack

import (
	"context"
	"fmt"
)

// ObservabilityResource provides observability operations.
type ObservabilityResource struct {
	Metrics  *ObsMetricsResource
	Sessions *ObsSessionsResource
	Users    *ObsUsersResource
	Outcomes *ObsOutcomesResource
}

func newObservabilityResource(t *Transport) *ObservabilityResource {
	return &ObservabilityResource{
		Metrics:  &ObsMetricsResource{t: t},
		Sessions: &ObsSessionsResource{t: t},
		Users:    &ObsUsersResource{t: t},
		Outcomes: &ObsOutcomesResource{t: t},
	}
}

// --- Metrics ---

// ObsMetricsResource provides observability metrics operations.
type ObsMetricsResource struct {
	t *Transport
}

// GetDashboard retrieves the metrics dashboard.
func (r *ObsMetricsResource) GetDashboard(ctx context.Context, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/observability/metrics/dashboard", body, nil, &resp)
}

// GetTimeSeries retrieves metrics time series data.
func (r *ObsMetricsResource) GetTimeSeries(ctx context.Context, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/observability/metrics/timeseries", body, nil, &resp)
}

// --- Sessions ---

// ObsSessionsResource provides observability session operations.
type ObsSessionsResource struct {
	t *Transport
}

// List lists observability sessions.
func (r *ObsSessionsResource) List(ctx context.Context, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/observability/sessions", body, nil, &resp)
}

// Get retrieves a session by ID.
func (r *ObsSessionsResource) Get(ctx context.Context, sessionID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/observability/sessions/%s", sessionID), nil, nil, &resp)
}

// --- Users ---

// ObsUsersResource provides observability user operations.
type ObsUsersResource struct {
	t *Transport
}

// List lists observability users.
func (r *ObsUsersResource) List(ctx context.Context, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/observability/users", body, nil, &resp)
}

// Get retrieves a user by ID.
func (r *ObsUsersResource) Get(ctx context.Context, userID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/observability/users/%s", userID), nil, nil, &resp)
}

// --- Outcomes ---

// ObsOutcomesResource provides outcome operations.
type ObsOutcomesResource struct {
	t *Transport
}

// GetDashboard retrieves the outcomes dashboard.
func (r *ObsOutcomesResource) GetDashboard(ctx context.Context, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/observability/outcomes/dashboard", body, nil, &resp)
}

// GetTimeSeries retrieves outcomes time series data.
func (r *ObsOutcomesResource) GetTimeSeries(ctx context.Context, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/observability/outcomes/timeseries", body, nil, &resp)
}
