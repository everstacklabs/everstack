package everstack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Transport handles HTTP communication with the Everstack API.
type Transport struct {
	baseURL    string
	apiKey     string
	provider   string
	orgID      string
	userID     string
	headers    map[string]string
	httpClient *http.Client
}

func newTransport(cfg *config) *Transport {
	client := cfg.httpClient
	if client == nil {
		client = &http.Client{}
	}
	if cfg.timeout > 0 {
		client.Timeout = time.Duration(cfg.timeout) * time.Second
	}
	return &Transport{
		baseURL:    strings.TrimRight(cfg.baseURL, "/"),
		apiKey:     cfg.apiKey,
		provider:   cfg.provider,
		orgID:      cfg.orgID,
		userID:     cfg.userID,
		headers:    cfg.headers,
		httpClient: client,
	}
}

func (t *Transport) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if t.apiKey != "" {
		req.Header.Set("x-evs-api-key", t.apiKey)
	}
	if t.provider != "" {
		req.Header.Set("x-evs-provider", t.provider)
	}
	if t.orgID != "" {
		req.Header.Set("x-evs-org-id", t.orgID)
	}
	if t.userID != "" {
		req.Header.Set("x-evs-user-id", t.userID)
	}
	for k, v := range t.headers {
		req.Header.Set(k, v)
	}
}

// Request sends an HTTP request and decodes the JSON response into result.
func (t *Transport) Request(ctx context.Context, method, path string, body any, params url.Values, result any) error {
	u := t.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("everstack: marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return fmt.Errorf("everstack: create request: %w", err)
	}
	t.setHeaders(req)

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return &ConnectionError{Err: err}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("everstack: read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return parseAPIError(resp.StatusCode, respBody)
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("everstack: decode response: %w", err)
		}
	}
	return nil
}

// StreamRaw sends an HTTP request and returns the raw response body for SSE streaming.
// The caller is responsible for closing the returned io.ReadCloser.
func (t *Transport) StreamRaw(ctx context.Context, method, path string, body any) (io.ReadCloser, error) {
	u := t.baseURL + path

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("everstack: marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("everstack: create request: %w", err)
	}
	t.setHeaders(req)
	req.Header.Set("Accept", "text/event-stream")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, &ConnectionError{Err: err}
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		respBody, _ := io.ReadAll(resp.Body)
		return nil, parseAPIError(resp.StatusCode, respBody)
	}

	return resp.Body, nil
}
