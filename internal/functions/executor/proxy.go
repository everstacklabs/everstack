package executor

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

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// ProxyExecutor executes functions by proxying to HTTP APIs with parameter mapping.
type ProxyExecutor struct {
	client *http.Client
}

// NewProxyExecutor creates a new proxy executor.
func NewProxyExecutor() *ProxyExecutor {
	return &ProxyExecutor{
		client: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// Mode returns the execution mode.
func (p *ProxyExecutor) Mode() ExecutionMode {
	return ModeProxy
}

// Execute calls the proxy endpoint with mapped parameters.
func (p *ProxyExecutor) Execute(ctx context.Context, execCtx *ExecutionContext, config *FunctionConfig, args map[string]interface{}) (*ToolResult, error) {
	if config.ProxyBaseURL == "" {
		return &ToolResult{
			Success: false,
			Error:   "proxy base URL is not configured",
		}, fmt.Errorf("proxy base URL is not configured")
	}

	// Build URL
	targetURL, err := p.buildURL(config, args)
	if err != nil {
		return &ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to build URL: %v", err),
		}, err
	}

	// Determine HTTP method
	method := config.ProxyMethod
	if method == "" {
		method = http.MethodGet
	}

	// Build request body if needed
	var bodyReader io.Reader
	if method != http.MethodGet && method != http.MethodHead {
		body, err := p.buildBody(config, args)
		if err != nil {
			return &ToolResult{
				Success: false,
				Error:   fmt.Sprintf("failed to build body: %v", err),
			}, err
		}
		if body != nil {
			bodyBytes, _ := json.Marshal(body)
			bodyReader = bytes.NewReader(bodyBytes)
		}
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, method, targetURL, bodyReader)
	if err != nil {
		return &ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to create request: %v", err),
		}, err
	}

	// Set headers
	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("X-Request-ID", execCtx.RequestID)
	req.Header.Set("X-Correlation-ID", execCtx.CorrelationID)

	// Apply header mapping
	p.applyHeaderMapping(req, config, args)

	// Set timeout
	timeout := 30 * time.Second
	if config.TimeoutMs > 0 {
		timeout = time.Duration(config.TimeoutMs) * time.Millisecond
	}

	client := &http.Client{
		Timeout:   timeout,
		Transport: p.client.Transport,
	}

	logger.WithFields(
		"function_id", config.ID,
		"function_name", config.Name,
		"proxy_url", targetURL,
		"method", method,
		"timeout_ms", timeout.Milliseconds(),
		"correlation_id", execCtx.CorrelationID,
	).Debug("executing proxy function")

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		logger.WithFields(
			"function_id", config.ID,
			"error", err.Error(),
			"correlation_id", execCtx.CorrelationID,
		).Error("proxy execution failed")

		return &ToolResult{
			Success: false,
			Error:   fmt.Sprintf("proxy request failed: %v", err),
		}, err
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024)) // 10MB limit
	if err != nil {
		return &ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to read response: %v", err),
		}, err
	}

	// Check status code
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		logger.WithFields(
			"function_id", config.ID,
			"status_code", resp.StatusCode,
			"response_body", string(body),
			"correlation_id", execCtx.CorrelationID,
		).Warn("proxy returned non-2xx status")

		return &ToolResult{
			Success: false,
			Error:   fmt.Sprintf("proxy returned status %d: %s", resp.StatusCode, string(body)),
		}, fmt.Errorf("proxy returned status %d", resp.StatusCode)
	}

	// Parse and transform response
	result, err := p.transformResponse(config, body)
	if err != nil {
		return &ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to transform response: %v", err),
		}, err
	}

	logger.WithFields(
		"function_id", config.ID,
		"function_name", config.Name,
		"status_code", resp.StatusCode,
		"correlation_id", execCtx.CorrelationID,
	).Debug("proxy execution completed")

	return &ToolResult{
		Success: true,
		Content: result,
	}, nil
}

// buildURL constructs the target URL with path and query parameters.
func (p *ProxyExecutor) buildURL(config *FunctionConfig, args map[string]interface{}) (string, error) {
	baseURL := strings.TrimSuffix(config.ProxyBaseURL, "/")

	// Build path with substitutions
	path := config.ProxyPath
	if path != "" {
		path = p.substitutePathParams(path, args)
	}

	fullURL := baseURL + path

	// Build query parameters
	if len(config.ProxyQueryMapping) > 0 {
		params := url.Values{}
		for paramName, argPath := range config.ProxyQueryMapping {
			value := p.extractValue(args, argPath)
			if value != "" {
				params.Set(paramName, value)
			}
		}
		if len(params) > 0 {
			if strings.Contains(fullURL, "?") {
				fullURL += "&" + params.Encode()
			} else {
				fullURL += "?" + params.Encode()
			}
		}
	}

	return fullURL, nil
}

// substitutePathParams replaces {param} placeholders in the path.
func (p *ProxyExecutor) substitutePathParams(path string, args map[string]interface{}) string {
	result := path
	for key, value := range args {
		placeholder := "{" + key + "}"
		if strings.Contains(result, placeholder) {
			result = strings.ReplaceAll(result, placeholder, fmt.Sprintf("%v", value))
		}
	}
	return result
}

// buildBody constructs the request body using body mapping.
func (p *ProxyExecutor) buildBody(config *FunctionConfig, args map[string]interface{}) (map[string]interface{}, error) {
	if len(config.ProxyBodyMapping) == 0 {
		// No mapping, pass args directly
		return args, nil
	}

	body := make(map[string]interface{})
	for targetKey, argPath := range config.ProxyBodyMapping {
		value := p.extractValueAny(args, argPath)
		if value != nil {
			p.setNestedValue(body, targetKey, value)
		}
	}

	return body, nil
}

// applyHeaderMapping sets headers based on the header mapping configuration.
func (p *ProxyExecutor) applyHeaderMapping(req *http.Request, config *FunctionConfig, args map[string]interface{}) {
	for headerName, argPath := range config.ProxyHeaderMapping {
		value := p.extractValue(args, argPath)
		if value != "" {
			req.Header.Set(headerName, value)
		}
	}
}

// transformResponse applies response mapping to transform the API response.
func (p *ProxyExecutor) transformResponse(config *FunctionConfig, body []byte) (interface{}, error) {
	// Parse JSON response
	var rawResponse interface{}
	if err := json.Unmarshal(body, &rawResponse); err != nil {
		// If not JSON, return as string
		return string(body), nil
	}

	// If no response mapping, return raw response
	if len(config.ProxyResponseMapping) == 0 {
		return rawResponse, nil
	}

	// Apply response mapping
	responseMap, ok := rawResponse.(map[string]interface{})
	if !ok {
		return rawResponse, nil
	}

	result := make(map[string]interface{})
	for targetKey, sourcePath := range config.ProxyResponseMapping {
		value := p.extractValueAny(responseMap, sourcePath)
		if value != nil {
			p.setNestedValue(result, targetKey, value)
		}
	}

	return result, nil
}

// extractValue extracts a string value from args using a dot-notation path.
func (p *ProxyExecutor) extractValue(args map[string]interface{}, path string) string {
	value := p.extractValueAny(args, path)
	if value == nil {
		return ""
	}
	return fmt.Sprintf("%v", value)
}

// extractValueAny extracts any value from args using a dot-notation path.
func (p *ProxyExecutor) extractValueAny(args map[string]interface{}, path string) interface{} {
	// Handle literal values (prefixed with $)
	if strings.HasPrefix(path, "$") {
		return strings.TrimPrefix(path, "$")
	}

	parts := strings.Split(path, ".")
	var current interface{} = args

	for _, part := range parts {
		switch v := current.(type) {
		case map[string]interface{}:
			current = v[part]
		default:
			return nil
		}
	}

	return current
}

// setNestedValue sets a value in a map using a dot-notation path.
func (p *ProxyExecutor) setNestedValue(target map[string]interface{}, path string, value interface{}) {
	parts := strings.Split(path, ".")
	current := target

	for i, part := range parts {
		if i == len(parts)-1 {
			current[part] = value
		} else {
			if _, ok := current[part]; !ok {
				current[part] = make(map[string]interface{})
			}
			if next, ok := current[part].(map[string]interface{}); ok {
				current = next
			} else {
				return
			}
		}
	}
}
