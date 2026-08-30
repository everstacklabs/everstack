package v1

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/everstacklabs/everstack/internal/api/internalauth"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

func generateChatCompletionContent(ctx context.Context, tenantID string, payload map[string]interface{}) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	url := scorerGenGatewayURL()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if key := os.Getenv("MF_EVAL_RUNNER_API_KEY"); key != "" {
		httpReq.Header.Set("x-evs-api-key", key)
		httpReq.Header.Set("x-mf-api-key", key) // legacy alias (rolling-deploy safe)
	} else {
		internalauth.SetHeader(httpReq.Header)
	}
	httpReq.Header.Set("x-tenant-id", tenantID)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("chat-completions request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("chat-completions failed (%d): %s", resp.StatusCode, string(respBody))
	}

	content := extractScorerContent(respBody)
	if content == "" {
		return "", fmt.Errorf("empty response from model")
	}
	return content, nil
}

func stripJSONCodeFence(content string) string {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		content = strings.TrimPrefix(content, "```json")
		content = strings.TrimPrefix(content, "```JSON")
		content = strings.TrimPrefix(content, "```")
		content = strings.TrimSpace(content)
		content = strings.TrimSuffix(content, "```")
	}
	return strings.TrimSpace(content)
}
