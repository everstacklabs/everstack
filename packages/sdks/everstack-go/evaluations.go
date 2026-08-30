package everstack

import (
	"context"
	"fmt"
	"net/url"
)

// EvaluationsResource provides evaluation operations.
type EvaluationsResource struct {
	Runs      *EvalRunsResource
	Schedules *EvalSchedulesResource
}

func newEvaluationsResource(t *Transport) *EvaluationsResource {
	return &EvaluationsResource{
		Runs:      &EvalRunsResource{t: t},
		Schedules: &EvalSchedulesResource{t: t},
	}
}

// --- Runs ---

// EvalRunsResource provides eval run operations.
type EvalRunsResource struct {
	t *Transport
}

// Create creates an eval run.
func (r *EvalRunsResource) Create(ctx context.Context, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/eval-runs", body, nil, &resp)
}

// Get retrieves an eval run by ID.
func (r *EvalRunsResource) Get(ctx context.Context, runID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/eval-runs/%s", runID), nil, nil, &resp)
}

// List lists eval runs.
func (r *EvalRunsResource) List(ctx context.Context, params ...url.Values) (map[string]any, error) {
	var q url.Values
	if len(params) > 0 {
		q = params[0]
	}
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", "/v1/eval-runs", nil, q, &resp)
}

// Cancel cancels a running eval.
func (r *EvalRunsResource) Cancel(ctx context.Context, runID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/eval-runs/%s/cancel", runID), map[string]any{}, nil, &resp)
}

// Delete deletes an eval run.
func (r *EvalRunsResource) Delete(ctx context.Context, runID string) error {
	return r.t.Request(ctx, "DELETE", fmt.Sprintf("/v1/eval-runs/%s", runID), nil, nil, nil)
}

// Retry retries a failed eval run.
func (r *EvalRunsResource) Retry(ctx context.Context, runID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/eval-runs/%s/retry", runID), map[string]any{}, nil, &resp)
}

// GetItems retrieves items for an eval run.
func (r *EvalRunsResource) GetItems(ctx context.Context, evalRunID string, params ...url.Values) (map[string]any, error) {
	var q url.Values
	if len(params) > 0 {
		q = params[0]
	}
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/eval-runs/%s/items", evalRunID), nil, q, &resp)
}

// GetSummary retrieves the summary for an eval run.
func (r *EvalRunsResource) GetSummary(ctx context.Context, runID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/eval-runs/%s/summary", runID), nil, nil, &resp)
}

// Compare compares two eval runs.
func (r *EvalRunsResource) Compare(ctx context.Context, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/eval-runs/compare", body, nil, &resp)
}

// SetBaseline sets a baseline for an eval run.
func (r *EvalRunsResource) SetBaseline(ctx context.Context, evalRunID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/eval-runs/%s/baseline", evalRunID), body, nil, &resp)
}

// --- Schedules ---

// EvalSchedulesResource provides eval schedule operations.
type EvalSchedulesResource struct {
	t *Transport
}

// Create creates an eval schedule.
func (r *EvalSchedulesResource) Create(ctx context.Context, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/eval-schedules", body, nil, &resp)
}

// Get retrieves an eval schedule by ID.
func (r *EvalSchedulesResource) Get(ctx context.Context, scheduleID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/eval-schedules/%s", scheduleID), nil, nil, &resp)
}

// List lists eval schedules.
func (r *EvalSchedulesResource) List(ctx context.Context, params ...url.Values) (map[string]any, error) {
	var q url.Values
	if len(params) > 0 {
		q = params[0]
	}
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", "/v1/eval-schedules", nil, q, &resp)
}

// Update updates an eval schedule.
func (r *EvalSchedulesResource) Update(ctx context.Context, scheduleID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "PATCH", fmt.Sprintf("/v1/eval-schedules/%s", scheduleID), body, nil, &resp)
}

// Delete deletes an eval schedule.
func (r *EvalSchedulesResource) Delete(ctx context.Context, scheduleID string) error {
	return r.t.Request(ctx, "DELETE", fmt.Sprintf("/v1/eval-schedules/%s", scheduleID), nil, nil, nil)
}
