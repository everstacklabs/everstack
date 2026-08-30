package memory

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Client is a REST client for the memory API.
type Client struct {
	baseURL    string
	apiKey     string
	tenantID   string
	httpClient *http.Client
	initErr    error
}

// NewClient creates a new memory API client.
func NewClient(baseURL, apiKey string) *Client {
	return &Client{
		baseURL:    baseURL + "/v1/memory",
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// ── Types ───────────────────────────────────────────────────────────

// CollectionResponse represents a memory collection.
type CollectionResponse struct {
	ID                 string            `json:"id"`
	TenantID           string            `json:"tenant_id"`
	Name               string            `json:"name"`
	Description        string            `json:"description"`
	EmbeddingModel     string            `json:"embedding_model"`
	EmbeddingDimension int               `json:"embedding_dimension"`
	DistanceMetric     string            `json:"distance_metric"`
	DocumentCount      int               `json:"document_count"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	CreatedAt          string            `json:"created_at"`
	UpdatedAt          string            `json:"updated_at"`
}

// DocumentInput represents a document to be added.
type DocumentInput struct {
	Content  string            `json:"content"`
	Metadata map[string]string `json:"metadata,omitempty"`
	Source   string            `json:"source,omitempty"`
}

// SearchResult represents a single query result.
type SearchResult struct {
	DocumentID string            `json:"document_id"`
	ChunkText  string            `json:"chunk_text"`
	ChunkIndex int               `json:"chunk_index"`
	Score      float64           `json:"score"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// AnalyticsBucket represents one time bucket in analytics output.
type AnalyticsBucket struct {
	Timestamp    string  `json:"timestamp"`
	QueryCount   int     `json:"query_count"`
	StoreCount   int     `json:"store_count"`
	DeleteCount  int     `json:"delete_count"`
	ErrorCount   int     `json:"error_count"`
	AvgLatencyMs float64 `json:"avg_latency_ms"`
}

// CollectionStat represents a top-collection entry.
type CollectionStat struct {
	CollectionName string `json:"collection_name"`
	RequestCount   int    `json:"request_count"`
}

// AnalyticsSummary is the full analytics response.
type AnalyticsSummary struct {
	Buckets        []AnalyticsBucket `json:"buckets"`
	TotalRequests  int               `json:"total_requests"`
	TotalErrors    int               `json:"total_errors"`
	AvgLatencyMs   float64           `json:"avg_latency_ms"`
	TopCollections []CollectionStat  `json:"top_collections"`
}

// ── HTTP helper ─────────────────────────────────────────────────────

func (c *Client) do(method, path string, body interface{}) ([]byte, error) {
	if c.initErr != nil {
		return nil, c.initErr
	}
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
	if c.apiKey != "" {
		req.Header.Set("x-evs-api-key", c.apiKey)
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

// ── Collection methods ──────────────────────────────────────────────

// ListCollections lists all collections for a tenant.
func (c *Client) ListCollections(tenantID string) ([]CollectionResponse, error) {
	tenantID = c.resolveTenantID(tenantID)
	path := "/collections"
	if tenantID != "" {
		path += "?tenant_id=" + url.QueryEscape(tenantID)
	}
	data, err := c.do(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Collections []CollectionResponse `json:"collections"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return resp.Collections, nil
}

// GetCollection gets a single collection by name.
func (c *Client) GetCollection(tenantID, name string) (*CollectionResponse, error) {
	tenantID = c.resolveTenantID(tenantID)
	path := "/collections/" + url.PathEscape(name)
	if tenantID != "" {
		path += "?tenant_id=" + url.QueryEscape(tenantID)
	}
	data, err := c.do(http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Collection CollectionResponse `json:"collection"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &resp.Collection, nil
}

// CreateCollectionOpts holds options for creating a collection.
type CreateCollectionOpts struct {
	Description        string            `json:"description,omitempty"`
	EmbeddingModel     string            `json:"embedding_model,omitempty"`
	EmbeddingDimension int               `json:"embedding_dimension,omitempty"`
	DistanceMetric     string            `json:"distance_metric,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
}

// CreateCollection creates a new collection.
func (c *Client) CreateCollection(tenantID, name string, opts CreateCollectionOpts) (*CollectionResponse, error) {
	tenantID = c.resolveTenantID(tenantID)
	body := map[string]interface{}{
		"name": name,
	}
	if tenantID != "" {
		body["tenant_id"] = tenantID
	}
	if opts.Description != "" {
		body["description"] = opts.Description
	}
	if opts.EmbeddingModel != "" {
		body["embedding_model"] = opts.EmbeddingModel
	}
	if opts.EmbeddingDimension > 0 {
		body["embedding_dimension"] = opts.EmbeddingDimension
	}
	if opts.DistanceMetric != "" {
		body["distance_metric"] = opts.DistanceMetric
	}
	if opts.Metadata != nil {
		body["metadata"] = opts.Metadata
	}

	data, err := c.do(http.MethodPost, "/collections", body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Collection CollectionResponse `json:"collection"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &resp.Collection, nil
}

// DeleteCollection deletes a collection by name.
func (c *Client) DeleteCollection(tenantID, name string) error {
	tenantID = c.resolveTenantID(tenantID)
	path := "/collections/" + url.PathEscape(name)
	if tenantID != "" {
		path += "?tenant_id=" + url.QueryEscape(tenantID)
	}
	_, err := c.do(http.MethodDelete, path, nil)
	return err
}

// ── Document methods ────────────────────────────────────────────────

// AddDocuments adds documents to a collection.
func (c *Client) AddDocuments(tenantID, collectionName string, docs []DocumentInput, chunkSize int) ([]string, int, error) {
	tenantID = c.resolveTenantID(tenantID)
	body := map[string]interface{}{
		"documents": docs,
	}
	if tenantID != "" {
		body["tenant_id"] = tenantID
	}
	if chunkSize > 0 {
		body["chunk_size"] = chunkSize
	}

	data, err := c.do(http.MethodPost, "/collections/"+url.PathEscape(collectionName)+"/documents", body)
	if err != nil {
		return nil, 0, err
	}
	var resp struct {
		DocumentIDs   []string `json:"document_ids"`
		ChunksCreated int      `json:"chunks_created"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, 0, fmt.Errorf("decode response: %w", err)
	}
	return resp.DocumentIDs, resp.ChunksCreated, nil
}

// ── Query method ────────────────────────────────────────────────────

// QueryCollection queries a collection.
func (c *Client) QueryCollection(tenantID, collectionName, query string, topK int, minScore float32) ([]SearchResult, error) {
	tenantID = c.resolveTenantID(tenantID)
	body := map[string]interface{}{
		"query": query,
	}
	if tenantID != "" {
		body["tenant_id"] = tenantID
	}
	if topK > 0 {
		body["top_k"] = topK
	}
	if minScore > 0 {
		body["min_score"] = minScore
	}

	data, err := c.do(http.MethodPost, "/collections/"+url.PathEscape(collectionName)+"/query", body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Results []SearchResult `json:"results"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return resp.Results, nil
}

// ── Analytics method ────────────────────────────────────────────────

// GetAnalytics retrieves memory analytics.
func (c *Client) GetAnalytics(tenantID, from, to string) (*AnalyticsSummary, error) {
	tenantID = c.resolveTenantID(tenantID)
	body := map[string]interface{}{}
	if tenantID != "" {
		body["tenant_id"] = tenantID
	}
	if from != "" {
		body["from"] = from
	}
	if to != "" {
		body["to"] = to
	}

	data, err := c.do(http.MethodPost, "/analytics", body)
	if err != nil {
		return nil, err
	}
	var resp AnalyticsSummary
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &resp, nil
}

func (c *Client) resolveTenantID(tenantID string) string {
	if tenantID != "" {
		return tenantID
	}
	return c.tenantID
}

// ── Setup method ────────────────────────────────────────────────────

// SetupPgVector triggers pgvector extension setup.
func (c *Client) SetupPgVector() (string, error) {
	data, err := c.do(http.MethodPost, "/setup", nil)
	if err != nil {
		return "", err
	}
	var resp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("decode response: %w", err)
	}
	return resp.Message, nil
}
