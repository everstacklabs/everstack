package everstack

import "context"

// EmbeddingsResource provides embedding operations.
type EmbeddingsResource struct {
	t *Transport
}

// Create creates embeddings for the given input.
func (r *EmbeddingsResource) Create(ctx context.Context, params *EmbeddingsParams) (*EmbeddingsResponse, error) {
	var resp EmbeddingsResponse
	err := r.t.Request(ctx, "POST", "/v1/embeddings", params, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
