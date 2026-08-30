package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/telemetry"
)

// a2aTargetHost returns the endpoint host for the a2a.target span attribute,
// stripping any path/query so credentials never reach the span.
func a2aTargetHost(endpoint string) string {
	if endpoint == "" {
		return ""
	}
	if u, err := url.Parse(endpoint); err == nil && u.Host != "" {
		return u.Host
	}
	return endpoint
}

// A2ACallHandler is a synthetic tool that lets an Everstack agent call a remote
// A2A (Agent2Agent) agent — including agents hosted by Google ADK or any other
// A2A-compliant server — and use its response. It is the client counterpart to
// the inbound A2A server (internal/a2a/server).
//
// The tool speaks A2A's JSON-RPC message/send over HTTP. It is opt-in: it only
// appears to an agent whose configured tools include "call_external_agent".
type A2ACallHandler struct {
	HTTPClient *http.Client
	// Remotes optionally resolves a saved remote agent by name (tenant-scoped),
	// so the caller can pass `remote` instead of a raw `endpoint`.
	Remotes RemoteResolver
}

// RemoteResolver resolves a saved external A2A agent by name to its endpoint and
// optional bearer token. found=false means no such saved remote.
type RemoteResolver interface {
	Resolve(ctx context.Context, name string) (endpoint, authToken string, found bool, err error)
}

func (h *A2ACallHandler) Name() string { return "call_external_agent" }

func (h *A2ACallHandler) Definition() gw.ToolDefinition {
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "call_external_agent",
			Description: "Call an external agent over the A2A (Agent2Agent) protocol and return its response. Provide the agent's A2A endpoint URL and a message. Works with Google ADK agents and any A2A-compliant server.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"remote": map[string]interface{}{
						"type":        "string",
						"description": "Name of a saved external agent to call. Use this instead of 'endpoint' when available.",
					},
					"endpoint": map[string]interface{}{
						"type":        "string",
						"description": "The remote A2A agent's service endpoint URL (the 'url' from its Agent Card). Required if 'remote' is not given.",
					},
					"message": map[string]interface{}{
						"type":        "string",
						"description": "The message to send to the remote agent.",
					},
					"auth_token": map[string]interface{}{
						"type":        "string",
						"description": "Optional bearer token to authenticate with the remote agent (ignored when 'remote' supplies one).",
					},
				},
				"required": []string{"message"},
			},
		},
	}
}

func (h *A2ACallHandler) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	endpoint, _ := args["endpoint"].(string)
	message, _ := args["message"].(string)
	authToken, _ := args["auth_token"].(string)
	remote, _ := args["remote"].(string)
	endpoint = strings.TrimSpace(endpoint)
	remote = strings.TrimSpace(remote)

	target := remote
	if target == "" {
		target = a2aTargetHost(endpoint)
	}
	ctx, span := telemetry.StartA2ACallSpan(ctx, target)
	defer span.End()

	// Resolve a saved remote by name when given (and no explicit endpoint).
	if endpoint == "" && remote != "" && h.Remotes != nil {
		ep, tok, found, err := h.Remotes.Resolve(ctx, remote)
		if err != nil {
			return "", fmt.Errorf("could not resolve remote agent %q: %w", remote, err)
		}
		if !found {
			return "", fmt.Errorf("no saved remote agent named %q", remote)
		}
		endpoint = strings.TrimSpace(ep)
		if authToken == "" {
			authToken = tok
		}
	}

	if endpoint == "" || strings.TrimSpace(message) == "" {
		return "", fmt.Errorf("a message and either a 'remote' name or an 'endpoint' URL are required")
	}
	if !strings.HasPrefix(endpoint, "http://") && !strings.HasPrefix(endpoint, "https://") {
		return "", fmt.Errorf("endpoint must be an absolute http(s) URL")
	}

	client := h.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}

	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      randID(),
		"method":  "message/send",
		"params": map[string]interface{}{
			"message": map[string]interface{}{
				"role":      "user",
				"messageId": randID(),
				"kind":      "message",
				"parts":     []map[string]interface{}{{"kind": "text", "text": message}},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("a2a_call: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("a2a_call: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if authToken != "" {
		req.Header.Set("Authorization", "Bearer "+authToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		telemetry.RecordError(span, err)
		return "", fmt.Errorf("a2a_call: request failed: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		authErr := fmt.Errorf("a2a_call: remote agent rejected authentication (401); provide a valid auth_token")
		telemetry.RecordError(span, authErr)
		return "", authErr
	}
	if resp.StatusCode/100 != 2 {
		httpErr := fmt.Errorf("a2a_call: remote returned HTTP %d: %s", resp.StatusCode, truncate(string(raw), 300))
		telemetry.RecordError(span, httpErr)
		return "", httpErr
	}

	return parseA2AResult(raw)
}

// a2aResult mirrors the relevant slice of an A2A message/send JSON-RPC response.
// The result may be a Task (with status + artifacts) or a Message (with parts).
type a2aResult struct {
	Error *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Result struct {
		// Task shape
		Status struct {
			State   string `json:"state"`
			Message struct {
				Parts []struct {
					Kind string `json:"kind"`
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"message"`
		} `json:"status"`
		Artifacts []struct {
			Parts []struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"artifacts"`
		// Message shape
		Parts []struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"result"`
}

func parseA2AResult(raw []byte) (string, error) {
	var r a2aResult
	if err := json.Unmarshal(raw, &r); err != nil {
		return "", fmt.Errorf("a2a_call: could not parse A2A response: %w", err)
	}
	if r.Error != nil {
		return "", fmt.Errorf("a2a_call: remote error %d: %s", r.Error.Code, r.Error.Message)
	}

	// Prefer artifacts, then the status message, then a direct Message result.
	var parts []string
	for _, a := range r.Result.Artifacts {
		for _, p := range a.Parts {
			if p.Text != "" {
				parts = append(parts, p.Text)
			}
		}
	}
	if len(parts) == 0 {
		for _, p := range r.Result.Status.Message.Parts {
			if p.Text != "" {
				parts = append(parts, p.Text)
			}
		}
	}
	if len(parts) == 0 {
		for _, p := range r.Result.Parts {
			if p.Text != "" {
				parts = append(parts, p.Text)
			}
		}
	}

	out := strings.Join(parts, "\n")
	if r.Result.Status.State == "failed" {
		if out == "" {
			out = "remote agent task failed"
		}
		return "", fmt.Errorf("a2a_call: remote agent task failed: %s", out)
	}
	if out == "" {
		return "(remote agent returned no text content)", nil
	}
	return out, nil
}

func randID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "id-0000000000000000"
	}
	return hex.EncodeToString(b)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
