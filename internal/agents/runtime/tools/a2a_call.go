package tools

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	a2aclient "github.com/everstacklabs/everstack/internal/a2a/client"
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
	return ""
}

// A2ACallHandler is a synthetic tool that lets an Everstack agent call a remote
// A2A agent using the legacy 0.2/0.3 JSON-RPC message/send text subset.
// It does not implement A2A 1.0 or continuation of asynchronous tasks.
//
// The tool speaks A2A's JSON-RPC message/send over HTTP. It is opt-in: it only
// appears to an agent whose configured tools include "call_external_agent".
type A2ACallHandler struct {
	Runs ExternalRuns
	// HTTPClient is a trusted transport injection for tests/embedders. Production
	// leaves it nil to enforce connect-time SSRF protection. Deadlines and the
	// no-redirect policy apply even to injected clients.
	HTTPClient *http.Client
	// Remotes optionally resolves a saved remote agent by name (tenant-scoped),
	// so the caller can pass `remote` instead of a raw `endpoint`.
	Remotes RemoteResolver
	// PrivateEndpoints is an operator-only list of exact saved endpoint URLs
	// allowed to dial RFC1918/ULA addresses. Raw endpoint calls never use it.
	// Metadata, loopback and link-local destinations remain blocked.
	PrivateEndpoints []string
}

// RemoteResolver resolves a saved external A2A agent by name to its endpoint and
// optional bearer token. found=false means no such saved remote.
type RemoteResolver interface {
	Resolve(ctx context.Context, name string) (endpoint, authToken string, found bool, err error)
}

func (h *A2ACallHandler) Name() string { return "call_external_agent" }

func (h *A2ACallHandler) Definition() gw.ToolDefinition {
	if h.Runs != nil {
		return durableCallDefinition()
	}
	return gw.ToolDefinition{
		Type: "function",
		Function: gw.ToolFunctionDef{
			Name:        "call_external_agent",
			Description: "Call a remote agent using the legacy A2A 0.2/0.3 JSON-RPC message/send text subset. Use a saved remote name, or an endpoint and optional token. Returns completed text only; streaming, task continuation and A2A 1.0 are unsupported. A timeout may mean work was accepted: do not automatically resend.",
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
						"description": "Optional bearer token for an explicit HTTPS endpoint. Cannot be combined with 'remote'; saved remotes use server-side credentials.",
					},
				},
				"required": []string{"message"},
			},
		},
	}
}

func (h *A2ACallHandler) Execute(ctx context.Context, args map[string]interface{}) (out string, callErr error) {
	if h.Runs != nil {
		remote, _ := args["remote"].(string)
		message, _ := args["message"].(string)
		key, _ := args["idempotency_key"].(string)
		if _, ok := args["endpoint"]; ok {
			return "", errors.New("use a saved remote; endpoint overrides are not allowed")
		}
		if _, ok := args["auth_token"]; ok {
			return "", errors.New("use the saved remote credential; token overrides are not allowed")
		}
		if strings.TrimSpace(remote) == "" || len(key) < 8 || len(key) > 128 {
			return "", errors.New("a saved remote and idempotency_key of 8–128 characters are required; reuse the same key on retry")
		}
		return h.Runs.Start(ctx, remote, message, key)
	}
	ctx, cancel := context.WithTimeout(ctx, a2aclient.Timeout)
	defer cancel()
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
	defer func() {
		// Raw bearer arguments remain a legacy API; never newly echo a known
		// credential from a remote answer or task identifier into tool output.
		if authToken != "" {
			out = strings.ReplaceAll(out, authToken, "[redacted]")
			var taskErr *A2ATaskError
			if errors.As(callErr, &taskErr) {
				taskErr.TaskID = strings.ReplaceAll(taskErr.TaskID, authToken, "[redacted]")
				taskErr.ContextID = strings.ReplaceAll(taskErr.ContextID, authToken, "[redacted]")
				taskErr.PartialText = strings.ReplaceAll(taskErr.PartialText, authToken, "[redacted]")
			}
		}
		if callErr != nil {
			// Remote IDs/partial output belong to the tool outcome, not spans.
			telemetry.RecordError(span, errors.New("a2a_call: external call did not complete successfully"))
		}
	}()

	if strings.TrimSpace(message) == "" {
		return "", errors.New("a2a_call: a nonempty message is required")
	}
	// A saved remote is authoritative: model-supplied values cannot replace
	// its endpoint or credentials, including an intentionally empty credential.
	if remote != "" {
		if endpoint != "" || authToken != "" {
			return "", errors.New("a2a_call: remote cannot be combined with endpoint or auth_token")
		}
		if h.Remotes == nil {
			return "", errors.New("a2a_call: saved remote lookup is unavailable")
		}
		ep, tok, found, err := h.Remotes.Resolve(ctx, remote)
		if err != nil {
			return "", errors.New("a2a_call: could not resolve saved remote")
		}
		if !found {
			return "", errors.New("a2a_call: saved remote not found")
		}
		endpoint = strings.TrimSpace(ep)
		authToken = tok
	}

	var privateEndpoints []string
	if remote != "" {
		privateEndpoints = h.PrivateEndpoints
	}
	result, err := (&a2aclient.Client{HTTPClient: h.HTTPClient, PrivateEndpoints: privateEndpoints}).Send(ctx,
		a2aclient.Connection{Endpoint: endpoint, Protocol: a2aclient.Legacy, Token: authToken}, message, randID())
	if err != nil {
		return "", a2aCallError(ctx, err)
	}
	if result.State == "completed" {
		return result.Text, nil
	}
	return "", &A2ATaskError{TaskID: result.TaskID, ContextID: result.ContextID, State: result.State, PartialText: result.Text}
}

func a2aCallError(ctx context.Context, err error) error {
	code := a2aclient.ErrorCode(err)
	switch code {
	case "transport_error", "read_failed":
		if ctx.Err() != nil {
			return fmt.Errorf("a2a_call: remote outcome unknown; do not automatically resend: %w", ctx.Err())
		}
		return errors.New("a2a_call: request failed or destination blocked; remote outcome unknown; do not automatically resend")
	case "http_401", "http_403":
		return fmt.Errorf("a2a_call: remote rejected authentication or access (HTTP %s)", strings.TrimPrefix(code, "http_"))
	case "response_too_large":
		return errors.New("a2a_call: response exceeds 8 MiB; no completed result; do not automatically resend")
	case "invalid_content_type":
		return errors.New("a2a_call: expected application/json; no completed result; do not automatically resend")
	case "invalid_endpoint":
		return errors.New("a2a_call: endpoint must be an absolute HTTP(S) URL without userinfo or fragment")
	case "https_required":
		return errors.New("a2a_call: bearer authentication requires an HTTPS endpoint")
	}
	if rpc, ok := strings.CutPrefix(code, "rpc_"); ok {
		if a2aclient.Rejected(err) {
			return fmt.Errorf("a2a_call: remote refused the request (JSON-RPC error %s) before doing any work", rpc)
		}
		return fmt.Errorf("a2a_call: remote returned JSON-RPC error %s; remote outcome unknown; do not automatically resend", rpc)
	}
	if strings.HasPrefix(code, "http_") {
		if status, err := strconv.Atoi(strings.TrimPrefix(code, "http_")); err == nil {
			return fmt.Errorf("a2a_call: remote returned HTTP %d; no completed result; do not automatically resend", status)
		}
	}
	return fmt.Errorf("a2a_call: invalid remote response (%s); no completed result; do not automatically resend", code)
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
