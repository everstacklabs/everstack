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

// HTTPRequestExecutor handles HTTP request nodes with template variable interpolation.
type HTTPRequestExecutor struct{}

func (e *HTTPRequestExecutor) NodeType() string { return "httpRequest" }

func (e *HTTPRequestExecutor) Execute(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	method := node.GetConfigString("method")
	if method == "" {
		method = "GET"
	}

	url := node.GetConfigString("url")
	if url == "" {
		return engine.NodeResult{Error: fmt.Errorf("httpRequest node has no URL configured")}
	}

	// Interpolate template variables in URL and body
	url = interpolateVariables(url, ec)

	body := node.GetConfigString("body")
	body = interpolateVariables(body, ec)

	timeoutMs := node.GetConfigInt("timeoutMs")
	if timeoutMs <= 0 {
		timeoutMs = 30000
	}

	responseVariable := node.GetConfigString("responseVariable")
	if responseVariable == "" {
		responseVariable = "response"
	}

	// Build request
	var bodyReader io.Reader
	if body != "" {
		bodyReader = bytes.NewBufferString(body)
	}

	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, strings.ToUpper(method), url, bodyReader)
	if err != nil {
		return engine.NodeResult{Error: fmt.Errorf("failed to create HTTP request: %w", err)}
	}

	// Apply headers from config
	if headersRaw, ok := node.Config["headers"]; ok {
		if headers, ok := headersRaw.(map[string]interface{}); ok {
			for k, v := range headers {
				if sv, ok := v.(string); ok {
					req.Header.Set(k, interpolateVariables(sv, ec))
				}
			}
		}
	}

	if body != "" && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	logger.WithFields("method", method, "url", url).Debug("httpRequest executor: sending request")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return engine.NodeResult{Error: fmt.Errorf("HTTP request failed: %w", err)}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return engine.NodeResult{Error: fmt.Errorf("failed to read HTTP response: %w", err)}
	}

	// Store response in execution context
	ec.SetVariable(responseVariable, string(respBody))
	ec.SetVariable(responseVariable+"_status", fmt.Sprintf("%d", resp.StatusCode))

	ec.SetNodeData("method", method)
	ec.SetNodeData("url", url)
	ec.SetNodeData("status_code", fmt.Sprintf("%d", resp.StatusCode))

	if resp.StatusCode >= 400 {
		logger.WithFields("status", resp.StatusCode, "url", url).Warn("httpRequest executor: non-success status")
	}

	output := map[string]interface{}{
		"body":        string(respBody),
		"status_code": resp.StatusCode,
		"method":      method,
		"url":         url,
	}

	return engine.NodeResult{NextHandle: "out", Output: output}
}

// interpolateVariables replaces {{expression}} patterns with values from the
// execution context. Supports ledger expressions ({{$prev.content}},
// {{$node.X.field}}, etc.) as well as legacy variable and metadata references.
func interpolateVariables(template string, ec *engine.ExecutionContext) string {
	// Use the ledger-aware interpolation if available
	if ec.Ledger != nil {
		return ec.Ledger.InterpolateTemplate(template, ec)
	}

	// Fallback: legacy interpolation
	result := template
	for name, value := range ec.Variables {
		placeholder := fmt.Sprintf("{{%s}}", name)
		if sv, ok := value.(string); ok {
			result = strings.ReplaceAll(result, placeholder, sv)
		}
	}
	// Also interpolate metadata
	for name, value := range ec.Metadata {
		placeholder := fmt.Sprintf("{{meta.%s}}", name)
		result = strings.ReplaceAll(result, placeholder, value)
	}
	return result
}
