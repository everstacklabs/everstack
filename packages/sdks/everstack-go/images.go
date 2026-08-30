package everstack

import "context"

// ImagesResource provides image operations.
type ImagesResource struct {
	t *Transport
}

// Generate generates images from a text prompt.
func (r *ImagesResource) Generate(ctx context.Context, params *ImageGenerateParams) (*ImageResponse, error) {
	var resp ImageResponse
	err := r.t.Request(ctx, "POST", "/v1/images/generations", params, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// Edit edits an image based on a prompt.
func (r *ImagesResource) Edit(ctx context.Context, params *ImageEditParams) (*ImageResponse, error) {
	var resp ImageResponse
	err := r.t.Request(ctx, "POST", "/v1/images/edits", params, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreateVariation creates a variation of an image.
func (r *ImagesResource) CreateVariation(ctx context.Context, params *ImageVariationParams) (*ImageResponse, error) {
	var resp ImageResponse
	err := r.t.Request(ctx, "POST", "/v1/images/variations", params, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}
