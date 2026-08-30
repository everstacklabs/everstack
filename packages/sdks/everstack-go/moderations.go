package everstack

import "context"

// ModerationsResource provides content moderation operations.
type ModerationsResource struct {
	t *Transport
}

// Create classifies content for policy compliance.
func (r *ModerationsResource) Create(ctx context.Context, params *ModerationParams) (*ModerationResponse, error) {
	var resp ModerationResponse
	err := r.t.Request(ctx, "POST", "/v1/moderations", params, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
