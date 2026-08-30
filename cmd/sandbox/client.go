package sandbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client is a lightweight REST client for sandbox management endpoints.
type Client struct {
	baseURL    string
	apiKey     string
	tenantID   string
	httpClient *http.Client
	initErr    error
}

// APIError wraps non-2xx API responses.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Body       string
}

func (e *APIError) Error() string {
	trimmed := strings.TrimSpace(e.Body)
	if trimmed == "" {
		return fmt.Sprintf("%s %s failed with status %d", e.Method, e.Path, e.StatusCode)
	}
	return fmt.Sprintf("%s %s failed with status %d: %s", e.Method, e.Path, e.StatusCode, trimmed)
}

func NewClient(baseURL, apiKey, tenantID string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		apiKey:   apiKey,
		tenantID: tenantID,
		httpClient: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) doJSON(method, path string, query url.Values, body interface{}, out interface{}) ([]byte, error) {
	return c.doJSONWith(c.httpClient, method, path, query, body, out)
}

func (c *Client) doJSONWith(hc *http.Client, method, path string, query url.Values, body interface{}, out interface{}) ([]byte, error) {
	if c.initErr != nil {
		return nil, c.initErr
	}
	if query == nil {
		query = url.Values{}
	}

	fullURL := c.baseURL + path
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, fullURL, bodyReader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.apiKey != "" {
		req.Header.Set("x-evs-api-key", c.apiKey)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 300 {
		return nil, &APIError{
			Method:     method,
			Path:       path,
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
		}
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return nil, fmt.Errorf("decode response JSON: %w", err)
		}
	}

	return respBody, nil
}

// doConnectRPC calls a ConnectRPC endpoint (POST /<service>/<method>) with JSON body.
func (c *Client) doConnectRPC(method string, body interface{}, out interface{}) ([]byte, error) {
	return c.doConnectRPCWith(c.httpClient, method, body, out)
}

func (c *Client) doConnectRPCWith(hc *http.Client, method string, body interface{}, out interface{}) ([]byte, error) {
	if c.initErr != nil {
		return nil, c.initErr
	}
	fullURL := c.baseURL + "/" + method

	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(http.MethodPost, fullURL, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("x-evs-api-key", c.apiKey)
	}

	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode >= 300 {
		return nil, &APIError{
			Method:     http.MethodPost,
			Path:       "/" + method,
			StatusCode: resp.StatusCode,
			Body:       string(respBody),
		}
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return nil, fmt.Errorf("decode response JSON: %w", err)
		}
	}

	return respBody, nil
}

func (c *Client) streamSSE(ctx context.Context, path string, query url.Values, onEvent func(eventType, data string) error) error {
	if c.initErr != nil {
		return c.initErr
	}
	if query == nil {
		query = url.Values{}
	}
	fullURL := c.baseURL + path
	if len(query) > 0 {
		fullURL += "?" + query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return err
	}
	if c.apiKey != "" {
		req.Header.Set("x-evs-api-key", c.apiKey)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return &APIError{Method: http.MethodGet, Path: path, StatusCode: resp.StatusCode, Body: string(body)}
	}

	decoder := newSSEDecoder(resp.Body)
	for {
		eventType, data, err := decoder.Next()
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		if onEvent != nil {
			if err := onEvent(eventType, data); err != nil {
				return err
			}
		}
	}
}

func (c *Client) withTenant(q url.Values) url.Values {
	if q == nil {
		q = url.Values{}
	}
	if c.tenantID != "" && q.Get("tenant_id") == "" {
		q.Set("tenant_id", c.tenantID)
	}
	return q
}

func (c *Client) CreateSandbox(body map[string]interface{}) (map[string]interface{}, error) {
	if c.tenantID != "" {
		if _, ok := body["tenant_id"]; !ok {
			body["tenant_id"] = c.tenantID
		}
	}
	var out map[string]interface{}
	_, err := c.doJSON(http.MethodPost, "/v1/sandbox", nil, body, &out)
	return out, err
}

func (c *Client) ListSandboxInstances(status string, limit, offset int) ([]map[string]interface{}, int, error) {
	q := c.withTenant(nil)
	if status != "" {
		q.Set("status", sandboxStatusToProtoEnum(status))
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if offset > 0 {
		q.Set("offset", fmt.Sprintf("%d", offset))
	}

	var out map[string]interface{}
	_, err := c.doJSON(http.MethodGet, "/v1/sandbox/instances", q, nil, &out)
	if err != nil {
		return nil, 0, err
	}

	instances := mapSliceField(out, "instances")
	total := int(numberField(out, "total"))
	if total == 0 {
		total = len(instances)
	}
	return instances, total, nil
}

func (c *Client) GetSandboxInstance(sandboxID string) (map[string]interface{}, error) {
	var out map[string]interface{}
	_, err := c.doJSON(http.MethodGet, "/v1/sandbox/instances/"+url.PathEscape(sandboxID), c.withTenant(nil), nil, &out)
	if err != nil {
		return nil, err
	}
	if nested := mapField(out, "instance"); nested != nil {
		return nested, nil
	}
	return out, nil
}

func (c *Client) GetSandboxOverview() (map[string]interface{}, error) {
	var out map[string]interface{}
	_, err := c.doJSON(http.MethodGet, "/v1/sandbox/overview", c.withTenant(nil), nil, &out)
	if err != nil {
		return nil, err
	}
	if nested := mapField(out, "overview"); nested != nil {
		return nested, nil
	}
	return out, nil
}

func (c *Client) ResolveSessionID(sandboxID string) (string, error) {
	inst, err := c.GetSandboxInstance(sandboxID)
	if err != nil {
		return "", err
	}
	sessionID := stringField(inst, "session_id", "sessionId")
	if sessionID == "" {
		return "", fmt.Errorf("sandbox %s does not include session_id in response", sandboxID)
	}
	return sessionID, nil
}

func (c *Client) DestroySandboxBySession(sessionID string) (map[string]interface{}, error) {
	var out map[string]interface{}
	_, err := c.doJSON(http.MethodDelete, "/v1/sandbox/"+url.PathEscape(sessionID), c.withTenant(nil), nil, &out)
	return out, err
}

func (c *Client) RecreateSandbox(sandboxID, sessionID string) (map[string]interface{}, error) {
	body := map[string]interface{}{
		"sandbox_id": sandboxID,
	}
	if c.tenantID != "" {
		body["tenant_id"] = c.tenantID
	}
	if sessionID != "" {
		body["session_id"] = sessionID
	}
	var out map[string]interface{}
	_, err := c.doJSON(http.MethodPost, "/v1/sandbox/recreate", nil, body, &out)
	return out, err
}

func (c *Client) ListSandboxEvents(sandboxID, eventType string, limit, offset int) ([]map[string]interface{}, int, error) {
	q := c.withTenant(nil)
	if eventType != "" {
		q.Set("event_type", eventType)
	}
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if offset > 0 {
		q.Set("offset", fmt.Sprintf("%d", offset))
	}

	var out map[string]interface{}
	_, err := c.doJSON(http.MethodGet, "/v1/sandbox/"+url.PathEscape(sandboxID)+"/events", q, nil, &out)
	if err != nil {
		return nil, 0, err
	}
	events := mapSliceField(out, "events")
	total := int(numberField(out, "total"))
	if total == 0 {
		total = len(events)
	}
	return events, total, nil
}

func (c *Client) ListSandboxExecutions(sandboxID string, limit, offset int) ([]map[string]interface{}, int, error) {
	q := c.withTenant(nil)
	if limit > 0 {
		q.Set("limit", fmt.Sprintf("%d", limit))
	}
	if offset > 0 {
		q.Set("offset", fmt.Sprintf("%d", offset))
	}

	var out map[string]interface{}
	_, err := c.doJSON(http.MethodGet, "/v1/sandbox/"+url.PathEscape(sandboxID)+"/executions", q, nil, &out)
	if err != nil {
		return nil, 0, err
	}
	execs := mapSliceField(out, "executions")
	total := int(numberField(out, "total"))
	if total == 0 {
		total = len(execs)
	}
	return execs, total, nil
}

func (c *Client) GetSandboxStats(sessionID string) (map[string]interface{}, error) {
	var out map[string]interface{}
	_, err := c.doJSON(http.MethodGet, "/v1/sandbox/"+url.PathEscape(sessionID)+"/stats", c.withTenant(nil), nil, &out)
	if err != nil {
		return nil, err
	}
	if nested := mapField(out, "stats"); nested != nil {
		return nested, nil
	}
	return out, nil
}

func (c *Client) ExposePort(sessionID string, port int, protocol string) (map[string]interface{}, error) {
	body := map[string]interface{}{
		"session_id": sessionID,
		"port":       port,
	}
	if c.tenantID != "" {
		body["tenant_id"] = c.tenantID
	}
	if protocol != "" {
		body["protocol"] = protocol
	}
	var out map[string]interface{}
	_, err := c.doJSON(http.MethodPost, "/v1/sandbox/"+url.PathEscape(sessionID)+"/ports", nil, body, &out)
	if err != nil {
		return nil, err
	}
	if nested := mapField(out, "mapping"); nested != nil {
		return nested, nil
	}
	return out, nil
}

func (c *Client) UnexposePort(sessionID string, port int) (map[string]interface{}, error) {
	var out map[string]interface{}
	_, err := c.doJSON(http.MethodDelete, "/v1/sandbox/"+url.PathEscape(sessionID)+"/ports/"+url.PathEscape(fmt.Sprintf("%d", port)), c.withTenant(nil), nil, &out)
	return out, err
}

func (c *Client) ListExposedPorts(sessionID string) ([]map[string]interface{}, error) {
	var out map[string]interface{}
	_, err := c.doJSON(http.MethodGet, "/v1/sandbox/"+url.PathEscape(sessionID)+"/ports", c.withTenant(nil), nil, &out)
	if err != nil {
		return nil, err
	}
	return mapSliceField(out, "ports"), nil
}

func (c *Client) DetectListeningPorts(sessionID string) ([]map[string]interface{}, error) {
	var out map[string]interface{}
	_, err := c.doJSON(http.MethodGet, "/v1/sandbox/"+url.PathEscape(sessionID)+"/ports/detect", c.withTenant(nil), nil, &out)
	if err != nil {
		return nil, err
	}
	return mapSliceField(out, "ports"), nil
}

// --- Phase 0+ scaffolding endpoints ---

func (c *Client) ExecSandboxCommand(sandboxID string, body map[string]interface{}) (map[string]interface{}, error) {
	if c.tenantID != "" {
		if _, ok := body["tenant_id"]; !ok {
			body["tenant_id"] = c.tenantID
		}
	}
	var out map[string]interface{}
	_, err := c.doJSON(http.MethodPost, "/v1/sandbox/instances/"+url.PathEscape(sandboxID)+"/exec", nil, body, &out)
	return out, err
}

func (c *Client) StopSandbox(sandboxID string) (map[string]interface{}, error) {
	body := map[string]interface{}{}
	if c.tenantID != "" {
		body["tenant_id"] = c.tenantID
	}
	var out map[string]interface{}
	_, err := c.doJSON(http.MethodPost, "/v1/sandbox/instances/"+url.PathEscape(sandboxID)+"/stop", nil, body, &out)
	return out, err
}

func (c *Client) ReviveSandbox(sandboxID string) (map[string]interface{}, error) {
	body := map[string]interface{}{}
	if c.tenantID != "" {
		body["tenant_id"] = c.tenantID
	}
	var out map[string]interface{}
	_, err := c.doJSON(http.MethodPost, "/v1/sandbox/instances/"+url.PathEscape(sandboxID)+"/revive", nil, body, &out)
	return out, err
}

func (c *Client) TerminateSandbox(sandboxID string) (map[string]interface{}, error) {
	body := map[string]interface{}{}
	if c.tenantID != "" {
		body["tenant_id"] = c.tenantID
	}
	var out map[string]interface{}
	_, err := c.doJSON(http.MethodPost, "/v1/sandbox/instances/"+url.PathEscape(sandboxID)+"/terminate", nil, body, &out)
	return out, err
}

func (c *Client) GetSandboxSSHInfo(sandboxID string) (map[string]interface{}, error) {
	// Use a short timeout — SSH info is a lightweight in-memory lookup.
	shortClient := *c.httpClient
	shortClient.Timeout = 10 * time.Second

	// Try the REST endpoint first (registered on gorilla mux, bypasses middleware).
	var out map[string]interface{}
	restPath := "/v1/sandbox/instances/" + url.PathEscape(sandboxID) + "/ssh-info"
	if _, err := c.doJSONWith(&shortClient, http.MethodGet, restPath, c.withTenant(nil), nil, &out); err == nil {
		return out, nil
	}

	// Fallback to ConnectRPC (goes through middleware, may require API key).
	rpcBody := map[string]interface{}{
		"sandboxId": sandboxID,
	}
	if c.tenantID != "" {
		rpcBody["tenantId"] = c.tenantID
	}
	out = nil
	_, err := c.doConnectRPCWith(&shortClient, "everstack.agents.v1.AgentsService/GetSandboxSSHInfo", rpcBody, &out)
	return out, err
}

func (c *Client) ListSSHKeys() ([]map[string]interface{}, error) {
	var out map[string]interface{}
	_, err := c.doJSON(http.MethodGet, "/v1/settings/ssh-keys", c.withTenant(nil), nil, &out)
	if err != nil {
		return nil, err
	}
	return mapSliceField(out, "keys"), nil
}

func (c *Client) AddSSHKey(name, publicKey string) (map[string]interface{}, error) {
	body := map[string]interface{}{
		"name":       name,
		"public_key": publicKey,
	}
	if c.tenantID != "" {
		body["tenant_id"] = c.tenantID
	}
	var out map[string]interface{}
	_, err := c.doJSON(http.MethodPost, "/v1/settings/ssh-keys", nil, body, &out)
	return out, err
}

func (c *Client) DeleteSSHKey(keyID string) (map[string]interface{}, error) {
	var out map[string]interface{}
	_, err := c.doJSON(http.MethodDelete, "/v1/settings/ssh-keys/"+url.PathEscape(keyID), c.withTenant(nil), nil, &out)
	return out, err
}

func isEndpointMissing(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusNotFound || apiErr.StatusCode == http.StatusMethodNotAllowed || apiErr.StatusCode == http.StatusNotImplemented
	}
	return false
}

func missingEndpointError(cmdName, endpoint string) error {
	return fmt.Errorf("%s is scaffolded, but server endpoint %s is not available yet", cmdName, endpoint)
}

func sandboxStatusToProtoEnum(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending":
		return "SANDBOX_STATUS_PENDING"
	case "running":
		return "SANDBOX_STATUS_RUNNING"
	case "stopped":
		return "SANDBOX_STATUS_STOPPED"
	case "failed":
		return "SANDBOX_STATUS_FAILED"
	default:
		return status
	}
}

func mapField(m map[string]interface{}, key string) map[string]interface{} {
	v, ok := m[key]
	if !ok {
		return nil
	}
	out, ok := v.(map[string]interface{})
	if !ok {
		return nil
	}
	return out
}

func mapSliceField(m map[string]interface{}, key string) []map[string]interface{} {
	v, ok := m[key]
	if !ok {
		return nil
	}
	raw, ok := v.([]interface{})
	if !ok {
		return nil
	}
	out := make([]map[string]interface{}, 0, len(raw))
	for _, item := range raw {
		if mm, ok := item.(map[string]interface{}); ok {
			out = append(out, mm)
		}
	}
	return out
}

func stringField(m map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			switch vv := v.(type) {
			case string:
				return vv
			case fmt.Stringer:
				return vv.String()
			}
		}
	}
	return ""
}

func boolField(m map[string]interface{}, keys ...string) bool {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			switch vv := v.(type) {
			case bool:
				return vv
			case string:
				return strings.EqualFold(vv, "true")
			}
		}
	}
	return false
}

func numberField(m map[string]interface{}, keys ...string) float64 {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			switch vv := v.(type) {
			case float64:
				return vv
			case float32:
				return float64(vv)
			case int:
				return float64(vv)
			case int64:
				return float64(vv)
			case json.Number:
				if f, err := vv.Float64(); err == nil {
					return f
				}
			}
		}
	}
	return 0
}

// sseDecoder parses basic text/event-stream payloads.
type sseDecoder struct {
	r io.Reader
}

func newSSEDecoder(r io.Reader) *sseDecoder {
	return &sseDecoder{r: r}
}

func (d *sseDecoder) Next() (string, string, error) {
	buf := make([]byte, 1)
	var line strings.Builder
	var eventType string
	var dataLines []string

	flushLine := func(l string) {
		if l == "" {
			return
		}
		if strings.HasPrefix(l, "event:") {
			eventType = strings.TrimSpace(strings.TrimPrefix(l, "event:"))
			return
		}
		if strings.HasPrefix(l, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(l, "data:")))
		}
	}

	for {
		n, err := d.r.Read(buf)
		if n > 0 {
			b := buf[0]
			if b == '\n' {
				curr := strings.TrimRight(line.String(), "\r")
				line.Reset()
				if curr == "" {
					if len(dataLines) > 0 || eventType != "" {
						if eventType == "" {
							eventType = "message"
						}
						return eventType, strings.Join(dataLines, "\n"), nil
					}
					continue
				}
				flushLine(curr)
				continue
			}
			line.WriteByte(b)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				curr := strings.TrimRight(line.String(), "\r")
				if curr != "" {
					flushLine(curr)
				}
				if len(dataLines) > 0 || eventType != "" {
					if eventType == "" {
						eventType = "message"
					}
					return eventType, strings.Join(dataLines, "\n"), nil
				}
			}
			return "", "", err
		}
	}
}
