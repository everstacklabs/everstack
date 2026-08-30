package everstack

import (
	"context"
	"fmt"
	"net/url"
)

// DatasetsResource provides dataset operations.
type DatasetsResource struct {
	Items        *DatasetItemsResource
	ScoreConfigs *ScoreConfigsResource
	t            *Transport
}

func newDatasetsResource(t *Transport) *DatasetsResource {
	return &DatasetsResource{
		Items:        &DatasetItemsResource{t: t},
		ScoreConfigs: &ScoreConfigsResource{t: t},
		t:            t,
	}
}

// Create creates a dataset.
func (r *DatasetsResource) Create(ctx context.Context, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/datasets", body, nil, &resp)
}

// Get retrieves a dataset by ID.
func (r *DatasetsResource) Get(ctx context.Context, datasetID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/datasets/%s", datasetID), nil, nil, &resp)
}

// List lists datasets.
func (r *DatasetsResource) List(ctx context.Context, params ...url.Values) (map[string]any, error) {
	var q url.Values
	if len(params) > 0 {
		q = params[0]
	}
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", "/v1/datasets", nil, q, &resp)
}

// Update updates a dataset.
func (r *DatasetsResource) Update(ctx context.Context, datasetID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "PATCH", fmt.Sprintf("/v1/datasets/%s", datasetID), body, nil, &resp)
}

// Delete deletes a dataset.
func (r *DatasetsResource) Delete(ctx context.Context, datasetID string) error {
	return r.t.Request(ctx, "DELETE", fmt.Sprintf("/v1/datasets/%s", datasetID), nil, nil, nil)
}

// ListBuiltinMetrics lists built-in evaluation metrics.
func (r *DatasetsResource) ListBuiltinMetrics(ctx context.Context) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", "/v1/builtin-metrics", nil, nil, &resp)
}

// --- Items ---

// DatasetItemsResource provides dataset item operations.
type DatasetItemsResource struct {
	t *Transport
}

// Create creates a dataset item.
func (r *DatasetItemsResource) Create(ctx context.Context, datasetID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/datasets/%s/items", datasetID), body, nil, &resp)
}

// CreateBatch creates multiple dataset items.
func (r *DatasetItemsResource) CreateBatch(ctx context.Context, datasetID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", fmt.Sprintf("/v1/datasets/%s/items/batch", datasetID), body, nil, &resp)
}

// Get retrieves a dataset item.
func (r *DatasetItemsResource) Get(ctx context.Context, itemID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/datasets/items/%s", itemID), nil, nil, &resp)
}

// List lists items in a dataset.
func (r *DatasetItemsResource) List(ctx context.Context, datasetID string, params ...url.Values) (map[string]any, error) {
	var q url.Values
	if len(params) > 0 {
		q = params[0]
	}
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/datasets/%s/items", datasetID), nil, q, &resp)
}

// Update updates a dataset item.
func (r *DatasetItemsResource) Update(ctx context.Context, itemID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "PATCH", fmt.Sprintf("/v1/datasets/items/%s", itemID), body, nil, &resp)
}

// Delete deletes a dataset item.
func (r *DatasetItemsResource) Delete(ctx context.Context, itemID string) error {
	return r.t.Request(ctx, "DELETE", fmt.Sprintf("/v1/datasets/items/%s", itemID), nil, nil, nil)
}

// --- Score Configs ---

// ScoreConfigsResource provides score configuration operations.
type ScoreConfigsResource struct {
	t *Transport
}

// Create creates a score config.
func (r *ScoreConfigsResource) Create(ctx context.Context, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "POST", "/v1/score-configs", body, nil, &resp)
}

// Get retrieves a score config.
func (r *ScoreConfigsResource) Get(ctx context.Context, configID string) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", fmt.Sprintf("/v1/score-configs/%s", configID), nil, nil, &resp)
}

// List lists score configs.
func (r *ScoreConfigsResource) List(ctx context.Context, params ...url.Values) (map[string]any, error) {
	var q url.Values
	if len(params) > 0 {
		q = params[0]
	}
	var resp map[string]any
	return resp, r.t.Request(ctx, "GET", "/v1/score-configs", nil, q, &resp)
}

// Update updates a score config.
func (r *ScoreConfigsResource) Update(ctx context.Context, configID string, body map[string]any) (map[string]any, error) {
	var resp map[string]any
	return resp, r.t.Request(ctx, "PATCH", fmt.Sprintf("/v1/score-configs/%s", configID), body, nil, &resp)
}

// Delete deletes a score config.
func (r *ScoreConfigsResource) Delete(ctx context.Context, configID string) error {
	return r.t.Request(ctx, "DELETE", fmt.Sprintf("/v1/score-configs/%s", configID), nil, nil, nil)
}
