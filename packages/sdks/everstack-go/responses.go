package everstack

import (
	"context"
	"fmt"
	"net/url"
)

// ResponsesResource provides Responses API operations (agentic orchestration).
type ResponsesResource struct {
	t *Transport
}

// Create creates a new response.
func (r *ResponsesResource) Create(ctx context.Context, params *ResponseCreateParams) (*ResponseObject, error) {
	var resp ResponseObject
	err := r.t.Request(ctx, "POST", "/v1/responses", params, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreateStream creates a streaming response.
func (r *ResponsesResource) CreateStream(ctx context.Context, params *ResponseCreateParams) (*Stream[map[string]any], error) {
	params.Stream = true
	return newStream[map[string]any](ctx, r.t, "POST", "/v1/responses", params)
}

// Get retrieves a response by ID.
func (r *ResponsesResource) Get(ctx context.Context, responseID string) (*ResponseObject, error) {
	var resp ResponseObject
	err := r.t.Request(ctx, "GET", fmt.Sprintf("/v1/responses/%s", responseID), nil, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// Cancel cancels an in-progress response.
func (r *ResponsesResource) Cancel(ctx context.Context, responseID string) (*ResponseObject, error) {
	var resp ResponseObject
	err := r.t.Request(ctx, "POST", fmt.Sprintf("/v1/responses/%s/cancel", responseID), nil, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// Delete deletes a response.
func (r *ResponsesResource) Delete(ctx context.Context, responseID string) (*DeleteResponseResult, error) {
	var resp DeleteResponseResult
	err := r.t.Request(ctx, "DELETE", fmt.Sprintf("/v1/responses/%s", responseID), nil, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// List lists responses.
func (r *ResponsesResource) List(ctx context.Context, params *ResponseListParams) (*ListResponsesResult, error) {
	q := url.Values{}
	if params != nil {
		if params.Status != "" {
			q.Set("status", params.Status)
		}
		if params.Limit != nil {
			q.Set("limit", fmt.Sprintf("%d", *params.Limit))
		}
		if params.After != "" {
			q.Set("after", params.After)
		}
		if params.Before != "" {
			q.Set("before", params.Before)
		}
		if params.Order != "" {
			q.Set("order", params.Order)
		}
	}
	var resp ListResponsesResult
	err := r.t.Request(ctx, "GET", "/v1/responses", nil, q, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
