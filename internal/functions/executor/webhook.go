package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// WebhookExecutor executes functions by calling customer webhook endpoints.
type WebhookExecutor struct {
	client *http.Client
}

// NewWebhookExecutor creates a new webhook executor.
func NewWebhookExecutor() *WebhookExecutor {
	return &WebhookExecutor{
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
func (w *WebhookExecutor) Mode() ExecutionMode {
	return ModeWebhook
}

// Execute calls the webhook endpoint with the tool arguments.
func (w *WebhookExecutor) Execute(ctx context.Context, execCtx *ExecutionContext, config *FunctionConfig, args map[string]interface{}) (*ToolResult, error) {
	if config.WebhookURL == "" {
		return &ToolResult{
			Success: false,
			Error:   "webhook URL is not configured",
		}, fmt.Errorf("webhook URL is not configured")
	}

	// Build request payload
	payload := WebhookPayload{
		RequestID:     execCtx.RequestID,
		TenantID:      execCtx.TenantID,
		FunctionID:    config.ID,
		FunctionName:  config.Name,
		Arguments:     args,
		CorrelationID: execCtx.CorrelationID,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
	}

	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return &ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to marshal payload: %v", err),
		}, err
	}

	// Determine HTTP method
	method := config.WebhookMethod
	if method == "" {
		method = http.MethodPost
	}

	// Rewrite localhost URLs to host.docker.internal when running inside Docker,
	// so webhooks running on the host are reachable from the container.
	webhookURL := resolveWebhookURL(config.WebhookURL)

	// Create request
	req, err := http.NewRequestWithContext(ctx, method, webhookURL, bytes.NewReader(payloadBytes))
	if err != nil {
		return &ToolResult{
			Success: false,
			Error:   fmt.Sprintf("failed to create request: %v", err),
		}, err
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Request-ID", execCtx.RequestID)
	req.Header.Set("X-Correlation-ID", execCtx.CorrelationID)
	req.Header.Set("X-Tenant-ID", execCtx.TenantID)
	req.Header.Set("X-Function-ID", config.ID)
	req.Header.Set("X-Function-Name", config.Name)

	// Add custom headers from config
	for key, value := range config.WebhookHeaders {
		req.Header.Set(key, value)
	}

	// Set timeout from config if specified
	timeout := 30 * time.Second
	if config.WebhookTimeoutMs > 0 {
		timeout = time.Duration(config.WebhookTimeoutMs) * time.Millisecond
	} else if config.TimeoutMs > 0 {
		timeout = time.Duration(config.TimeoutMs) * time.Millisecond
	}

	// Create a client with the specific timeout for this request
	client := &http.Client{
		Timeout:   timeout,
		Transport: w.client.Transport,
	}

	logger.WithFields(
		"function_id", config.ID,
		"function_name", config.Name,
		"webhook_url", config.WebhookURL,
		"method", method,
		"timeout_ms", timeout.Milliseconds(),
		"correlation_id", execCtx.CorrelationID,
	).Debug("executing webhook function")

	// Execute request
	resp, err := client.Do(req)
	if err != nil {
		logger.WithFields(
			"function_id", config.ID,
			"error", err.Error(),
			"correlation_id", execCtx.CorrelationID,
		).Error("webhook execution failed")

		return &ToolResult{
			Success: false,
			Error:   fmt.Sprintf("webhook request failed: %v", err),
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
		).Warn("webhook returned non-2xx status")

		return &ToolResult{
			Success: false,
			Error:   fmt.Sprintf("webhook returned status %d: %s", resp.StatusCode, string(body)),
		}, fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}

	// Parse response
	var result interface{}
	if len(body) > 0 {
		// Try to parse as JSON
		if err := json.Unmarshal(body, &result); err != nil {
			// If not JSON, use raw string
			result = string(body)
		}
	}

	logger.WithFields(
		"function_id", config.ID,
		"function_name", config.Name,
		"status_code", resp.StatusCode,
		"correlation_id", execCtx.CorrelationID,
	).Debug("webhook execution completed")

	return &ToolResult{
		Success: true,
		Content: result,
	}, nil
}

// WebhookPayload is the payload sent to webhook endpoints.
type WebhookPayload struct {
	RequestID     string                 `json:"request_id"`
	TenantID      string                 `json:"tenant_id"`
	FunctionID    string                 `json:"function_id"`
	FunctionName  string                 `json:"function_name"`
	Arguments     map[string]interface{} `json:"arguments"`
	CorrelationID string                 `json:"correlation_id"`
	Timestamp     string                 `json:"timestamp"`
}

// resolveWebhookURL rewrites localhost URLs to host.docker.internal when running
// inside a Docker container, so that webhooks on the host machine are reachable.
func resolveWebhookURL(url string) string {
	if !isRunningInDocker() {
		return url
	}
	// Replace localhost and 127.0.0.1 with host.docker.internal
	url = strings.Replace(url, "://localhost:", "://host.docker.internal:", 1)
	url = strings.Replace(url, "://localhost/", "://host.docker.internal/", 1)
	url = strings.Replace(url, "://127.0.0.1:", "://host.docker.internal:", 1)
	url = strings.Replace(url, "://127.0.0.1/", "://host.docker.internal/", 1)
	return url
}

// isRunningInDocker checks if the process is running inside a Docker container.
func isRunningInDocker() bool {
	// Check for /.dockerenv file (present in most Docker containers)
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}
	return false
}
