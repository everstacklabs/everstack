package features

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client is a REST client for the feature management API.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// NewClient creates a new feature management API client.
func NewClient(baseURL string) *Client {
	return &Client{
		baseURL:    baseURL + "/api/v1/features",
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// FeatureDefinition represents a feature flag definition.
type FeatureDefinition struct {
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Status      string   `json:"status"`
	Enabled     bool     `json:"enabled"`
	Categories  []string `json:"categories"`
	MinTier     *string  `json:"min_tier"`
	CreatedAt   string   `json:"created_at,omitempty"`
	UpdatedAt   string   `json:"updated_at,omitempty"`
}

// FeatureOverride represents a per-tenant feature override.
type FeatureOverride struct {
	ID         string  `json:"id"`
	TenantID   string  `json:"tenant_id"`
	FeatureKey string  `json:"feature_key"`
	Enabled    bool    `json:"enabled"`
	ExpiresAt  *string `json:"expires_at"`
	Reason     *string `json:"reason"`
	CreatedBy  *string `json:"created_by"`
	CreatedAt  string  `json:"created_at,omitempty"`
	UpdatedAt  string  `json:"updated_at,omitempty"`
}

func (c *Client) do(method, path string, body interface{}) ([]byte, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("API error %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// ListFeatures lists all feature definitions.
func (c *Client) ListFeatures() ([]FeatureDefinition, error) {
	data, err := c.do(http.MethodGet, "", nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Features []FeatureDefinition `json:"features"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return resp.Features, nil
}

// GetFeature gets a single feature by key.
func (c *Client) GetFeature(key string) (*FeatureDefinition, error) {
	data, err := c.do(http.MethodGet, "/"+url.PathEscape(key), nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Feature FeatureDefinition `json:"feature"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &resp.Feature, nil
}

// CreateFeature creates a new feature definition.
func (c *Client) CreateFeature(def *FeatureDefinition) (*FeatureDefinition, error) {
	data, err := c.do(http.MethodPost, "", def)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Feature FeatureDefinition `json:"feature"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &resp.Feature, nil
}

// UpdateFeature updates a feature definition.
func (c *Client) UpdateFeature(key string, def *FeatureDefinition) (*FeatureDefinition, error) {
	data, err := c.do(http.MethodPut, "/"+url.PathEscape(key), def)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Feature FeatureDefinition `json:"feature"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &resp.Feature, nil
}

// DeleteFeature deletes a feature by key.
func (c *Client) DeleteFeature(key string) error {
	_, err := c.do(http.MethodDelete, "/"+url.PathEscape(key), nil)
	return err
}

// ListOverrides lists overrides for a tenant.
func (c *Client) ListOverrides(tenantID string) ([]FeatureOverride, error) {
	data, err := c.do(http.MethodGet, "/overrides?tenant_id="+url.QueryEscape(tenantID), nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Overrides []FeatureOverride `json:"overrides"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return resp.Overrides, nil
}

// SetOverride creates or updates a per-tenant override.
func (c *Client) SetOverride(tenantID string, override *FeatureOverride) error {
	body := map[string]interface{}{
		"feature_key": override.FeatureKey,
		"enabled":     override.Enabled,
	}
	if override.ExpiresAt != nil {
		body["expires_at"] = *override.ExpiresAt
	}
	if override.Reason != nil {
		body["reason"] = *override.Reason
	}
	if override.CreatedBy != nil {
		body["created_by"] = *override.CreatedBy
	}
	_, err := c.do(http.MethodPost, "/overrides?tenant_id="+url.QueryEscape(tenantID), body)
	return err
}

// DeleteOverride deletes a per-tenant override.
func (c *Client) DeleteOverride(tenantID, featureKey string) error {
	_, err := c.do(http.MethodDelete, "/overrides/"+url.PathEscape(featureKey)+"?tenant_id="+url.QueryEscape(tenantID), nil)
	return err
}

// Publish triggers manifest publishing.
func (c *Client) Publish(tenantID string) error {
	path := "/publish"
	if tenantID != "" {
		path += "?tenant_id=" + url.QueryEscape(tenantID)
	}
	_, err := c.do(http.MethodPost, path, nil)
	return err
}
