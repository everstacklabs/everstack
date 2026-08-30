package everstack

import "context"

// RerankResource provides document reranking operations.
type RerankResource struct {
	t *Transport
}

// Create reranks documents by relevance to a query.
func (r *RerankResource) Create(ctx context.Context, params *RerankParams) (*RerankResponse, error) {
	var resp RerankResponse
	err := r.t.Request(ctx, "POST", "/v1/rerank", params, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
