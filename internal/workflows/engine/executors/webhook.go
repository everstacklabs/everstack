package executors

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

// WebhookExecutor handles webhook invocation nodes with retry logic.
type WebhookExecutor struct{}

func (e *WebhookExecutor) NodeType() string { return "webhook" }

func (e *WebhookExecutor) Execute(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	url := node.GetConfigString("url")
	if url == "" {
		return engine.NodeResult{Error: fmt.Errorf("webhook node has no URL configured")}
	}

	method := node.GetConfigString("method")
	if method == "" {
		method = "POST"
	}

	bodyTemplate := node.GetConfigString("bodyTemplate")
	bodyTemplate = interpolateVariables(bodyTemplate, ec)

	retries := node.GetConfigInt("retries")
	if retries <= 0 {
		retries = 3
	}

	timeoutMs := node.GetConfigInt("timeoutMs")
	if timeoutMs <= 0 {
		timeoutMs = 10000
	}

	url = interpolateVariables(url, ec)

	var lastErr error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			// Exponential backoff: 1s, 2s, 4s...
			backoff := time.Duration(1<<uint(attempt-1)) * time.Second
			select {
			case <-ctx.Done():
				return engine.NodeResult{Error: ctx.Err()}
			case <-time.After(backoff):
			}
		}

		reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)

		var bodyReader io.Reader
		if bodyTemplate != "" {
			bodyReader = bytes.NewBufferString(bodyTemplate)
		}

		req, err := http.NewRequestWithContext(reqCtx, strings.ToUpper(method), url, bodyReader)
		if err != nil {
			cancel()
			lastErr = fmt.Errorf("failed to create webhook request: %w", err)
			continue
		}

		// Apply headers
		if headersRaw, ok := node.Config["headers"]; ok {
			if headers, ok := headersRaw.(map[string]interface{}); ok {
				for k, v := range headers {
					if sv, ok := v.(string); ok {
						req.Header.Set(k, interpolateVariables(sv, ec))
					}
				}
			}
		}

		if bodyTemplate != "" && req.Header.Get("Content-Type") == "" {
			req.Header.Set("Content-Type", "application/json")
		}

		client := &http.Client{}
		resp, err := client.Do(req)
		cancel()

		if err != nil {
			lastErr = err
			logger.WithFields("attempt", attempt+1, "url", url, "error", err.Error()).
				Warn("webhook executor: request failed, retrying")
			continue
		}

		respBody, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if err != nil {
			lastErr = err
			continue
		}

		ec.SetVariable("webhook_response", string(respBody))
		ec.SetVariable("webhook_status", fmt.Sprintf("%d", resp.StatusCode))

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("webhook returned status %d", resp.StatusCode)
			logger.WithFields("attempt", attempt+1, "status", resp.StatusCode).
				Warn("webhook executor: server error, retrying")
			continue
		}

		// Success (including 4xx which we don't retry)
		ec.SetNodeData("url", url)
		ec.SetNodeData("method", method)
		ec.SetNodeData("status_code", fmt.Sprintf("%d", resp.StatusCode))
		return engine.NodeResult{NextHandle: "out", Output: map[string]interface{}{
			"status_code": resp.StatusCode,
			"url":         url,
			"method":      method,
		}}
	}

	return engine.NodeResult{Error: fmt.Errorf("webhook failed after %d attempts: %w", retries+1, lastErr)}
}
