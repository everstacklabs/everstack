package everstack

import "context"

// ModelsResource provides model listing operations.
type ModelsResource struct {
	t *Transport
}

// List lists all available models.
func (r *ModelsResource) List(ctx context.Context) (*ModelsListResponse, error) {
	var resp ModelsListResponse
	err := r.t.Request(ctx, "GET", "/v1/models", nil, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
