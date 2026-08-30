package executors

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

// AuthExecutor validates authentication credentials from the execution metadata.
//
// It supports multiple extensible auth modes for external identity providers.
//
// Config fields (from frontend AuthConfig):
//   - mode: "none" | "api_key" | "jwt" | "webhook" (default: "api_key")
//   - headerName: header/metadata key to extract the credential from (default: "Authorization")
//
// Mode-specific config:
//   - jwt: issuer, audience, jwksUrl (future: JWKS endpoint fetch for full signature validation)
//   - webhook: webhookUrl, webhookMethod (default: POST), webhookHeaders (map)
//
// Handles: "out" on success. Returns an error on auth failure.
type AuthExecutor struct{}

func (e *AuthExecutor) NodeType() string { return "auth" }

func (e *AuthExecutor) Execute(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	mode := node.GetConfigString("mode")
	if mode == "" {
		mode = "api_key"
	}

	switch mode {
	case "none":
		return e.executeNone(ec)
	case "api_key":
		return e.executeAPIKey(node, ec)
	case "jwt":
		return e.executeJWT(node, ec)
	case "webhook":
		return e.executeWebhook(ctx, node, ec)
	default:
		return engine.NodeResult{Error: fmt.Errorf("unknown auth mode: %s", mode)}
	}
}

// executeNone skips authentication entirely.
func (e *AuthExecutor) executeNone(ec *engine.ExecutionContext) engine.NodeResult {
	ec.Authenticated = true
	ec.SetNodeData("mode", "none")
	ec.SetNodeData("authenticated", "true")
	logger.Debug("auth executor: none mode, skipping authentication")
	return engine.NodeResult{NextHandle: "out", Output: map[string]interface{}{
		"authenticated": true,
		"mode":          "none",
	}}
}

// executeAPIKey validates that a non-empty API key is present in the configured header.
// This is user-managed auth — no database lookup is performed.
func (e *AuthExecutor) executeAPIKey(node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	headerName := node.GetConfigString("headerName")
	if headerName == "" {
		headerName = "Authorization"
	}

	token := extractCredential(ec, headerName)
	if token == "" {
		ec.Authenticated = false
		return engine.NodeResult{Error: fmt.Errorf("authentication required: no credential provided in metadata key %q", headerName)}
	}

	ec.Authenticated = true
	ec.AuthToken = token
	ec.SetNodeData("mode", "api_key")
	ec.SetNodeData("authenticated", "true")
	logger.WithFields("mode", "api_key").Debug("auth executor: authentication successful")
	return engine.NodeResult{NextHandle: "out", Output: map[string]interface{}{
		"authenticated": true,
		"mode":          "api_key",
	}}
}

// executeJWT decodes and validates a JWT token's structure, expiry, issuer, and audience.
// Signature validation via JWKS endpoint is not yet implemented.
func (e *AuthExecutor) executeJWT(node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	headerName := node.GetConfigString("headerName")
	if headerName == "" {
		headerName = "Authorization"
	}

	token := extractCredential(ec, headerName)
	if token == "" {
		ec.Authenticated = false
		return engine.NodeResult{Error: fmt.Errorf("authentication required: no JWT token provided")}
	}

	claims, err := decodeJWTClaims(token)
	if err != nil {
		ec.Authenticated = false
		return engine.NodeResult{Error: fmt.Errorf("JWT validation failed: %w", err)}
	}

	// Validate expiry
	if exp, ok := claims["exp"].(float64); ok {
		if time.Unix(int64(exp), 0).Before(time.Now()) {
			ec.Authenticated = false
			return engine.NodeResult{Error: fmt.Errorf("JWT token has expired")}
		}
	}

	// Validate issuer if configured
	if expectedIssuer := node.GetConfigString("issuer"); expectedIssuer != "" {
		if iss, ok := claims["iss"].(string); !ok || iss != expectedIssuer {
			ec.Authenticated = false
			return engine.NodeResult{Error: fmt.Errorf("JWT issuer mismatch: expected %q", expectedIssuer)}
		}
	}

	// Validate audience if configured
	if expectedAudience := node.GetConfigString("audience"); expectedAudience != "" {
		if !jwtAudienceContains(claims, expectedAudience) {
			ec.Authenticated = false
			return engine.NodeResult{Error: fmt.Errorf("JWT audience mismatch: expected %q", expectedAudience)}
		}
	}

	ec.Authenticated = true
	ec.AuthToken = token

	// Store claims in execution variables for downstream nodes
	ec.SetVariable("_auth_claims", claims)
	if sub, ok := claims["sub"].(string); ok {
		ec.SetVariable("_auth_subject", sub)
	}

	ec.SetNodeData("mode", "jwt")
	ec.SetNodeData("authenticated", "true")
	logger.WithFields("mode", "jwt").Debug("auth executor: JWT authentication successful")
	return engine.NodeResult{NextHandle: "out", Output: map[string]interface{}{
		"authenticated": true,
		"mode":          "jwt",
		"claims":        claims,
	}}
}

// executeWebhook forwards the credential to an external auth endpoint.
// Expects a JSON response with { "authenticated": bool, "user": {...} } or HTTP 200/401.
func (e *AuthExecutor) executeWebhook(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	webhookURL := node.GetConfigString("webhookUrl")
	if webhookURL == "" {
		return engine.NodeResult{Error: fmt.Errorf("auth webhook: webhookUrl is required")}
	}

	webhookMethod := node.GetConfigString("webhookMethod")
	if webhookMethod == "" {
		webhookMethod = "POST"
	}

	headerName := node.GetConfigString("headerName")
	if headerName == "" {
		headerName = "Authorization"
	}

	token := extractCredential(ec, headerName)
	if token == "" {
		ec.Authenticated = false
		return engine.NodeResult{Error: fmt.Errorf("authentication required: no credential provided")}
	}

	// Build the request payload
	payload, _ := json.Marshal(map[string]string{
		"token": token,
	})

	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, webhookMethod, webhookURL, strings.NewReader(string(payload)))
	if err != nil {
		return engine.NodeResult{Error: fmt.Errorf("auth webhook: failed to create request: %w", err)}
	}

	httpReq.Header.Set("Content-Type", "application/json")

	// Apply custom headers from config
	if node.Config != nil {
		if hdrs, ok := node.Config["webhookHeaders"].(map[string]interface{}); ok {
			for k, v := range hdrs {
				if sv, ok := v.(string); ok {
					httpReq.Header.Set(k, sv)
				}
			}
		}
	}

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return engine.NodeResult{Error: fmt.Errorf("auth webhook: request failed: %w", err)}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return engine.NodeResult{Error: fmt.Errorf("auth webhook: failed to read response: %w", err)}
	}

	// HTTP 401/403 = not authenticated
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		ec.Authenticated = false
		return engine.NodeResult{Error: fmt.Errorf("authentication denied by webhook (HTTP %d)", resp.StatusCode)}
	}

	if resp.StatusCode != http.StatusOK {
		return engine.NodeResult{Error: fmt.Errorf("auth webhook: unexpected status %d", resp.StatusCode)}
	}

	// Parse JSON response
	var webhookResp struct {
		Authenticated bool                   `json:"authenticated"`
		User          map[string]interface{} `json:"user"`
	}
	if err := json.Unmarshal(body, &webhookResp); err != nil {
		// If the response isn't JSON, treat 200 as authenticated
		ec.Authenticated = true
		ec.AuthToken = token
		logger.WithFields("mode", "webhook").Debug("auth executor: webhook returned 200 (non-JSON body)")
		return engine.NodeResult{NextHandle: "out", Output: map[string]interface{}{
			"authenticated": true,
			"mode":          "webhook",
		}}
	}

	if !webhookResp.Authenticated {
		ec.Authenticated = false
		return engine.NodeResult{Error: fmt.Errorf("authentication denied by webhook")}
	}

	ec.Authenticated = true
	ec.AuthToken = token

	// Store user data from webhook response in execution variables
	if webhookResp.User != nil {
		ec.SetVariable("_auth_user", webhookResp.User)
	}

	ec.SetNodeData("mode", "webhook")
	ec.SetNodeData("authenticated", "true")
	logger.WithFields("mode", "webhook").Debug("auth executor: webhook authentication successful")
	output := map[string]interface{}{
		"authenticated": true,
		"mode":          "webhook",
	}
	if webhookResp.User != nil {
		output["user"] = webhookResp.User
	}
	return engine.NodeResult{NextHandle: "out", Output: output}
}

// extractCredential extracts the credential from execution metadata,
// trying the configured header name and common fallbacks.
func extractCredential(ec *engine.ExecutionContext, headerName string) string {
	credential := ec.Metadata[headerName]
	if credential == "" {
		credential = ec.Metadata[strings.ToLower(headerName)]
	}
	if credential == "" {
		credential = ec.Metadata["x-evs-api-key"]
	}
	if credential == "" {
		credential = ec.Metadata["x-mf-api-key"]
	}
	if credential == "" {
		credential = ec.Metadata["x-everstack-api-key"]
	}
	if credential == "" {
		credential = ec.Metadata["api_key"]
	}

	// Strip "Bearer " prefix if present
	if strings.HasPrefix(strings.ToLower(credential), "bearer ") {
		credential = strings.TrimSpace(credential[7:])
	}

	return credential
}

// decodeJWTClaims performs a minimal JWT decode (header.payload.signature base64 split)
// without signature verification. Returns the claims as a map.
func decodeJWTClaims(token string) (map[string]interface{}, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("invalid JWT format: expected 3 parts, got %d", len(parts))
	}

	// Decode the payload (second segment)
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, fmt.Errorf("invalid JWT payload encoding: %w", err)
	}

	var claims map[string]interface{}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, fmt.Errorf("invalid JWT payload JSON: %w", err)
	}

	return claims, nil
}

// jwtAudienceContains checks if the JWT "aud" claim contains the expected audience.
// The "aud" claim can be a string or an array of strings.
func jwtAudienceContains(claims map[string]interface{}, expected string) bool {
	aud, ok := claims["aud"]
	if !ok {
		return false
	}

	switch v := aud.(type) {
	case string:
		return v == expected
	case []interface{}:
		for _, a := range v {
			if s, ok := a.(string); ok && s == expected {
				return true
			}
		}
	}
	return false
}
