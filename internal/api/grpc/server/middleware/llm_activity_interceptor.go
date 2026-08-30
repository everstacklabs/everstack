package middleware

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/grpc/codes"

	"github.com/everstacklabs/everstack/internal/activity"
	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/api/http"
	"github.com/everstacklabs/everstack/internal/api/info"
)

// ActivityInterceptor creates a ConnectRPC interceptor that logs activity for all requests
func LLMInterceptor() connect.UnaryInterceptorFunc {
	return func(handler connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			start := time.Now()

			// Create activity info and add to context
			ai := &info.ActivityInfo{
				Method:        req.Spec().Procedure,
				Path:          req.Spec().Procedure,
				RequestMethod: "POST", // ConnectRPC uses POST for all requests
			}
			ctx = ai.IntoContext(ctx)

			// Call the handler
			resp, err := handler(ctx, req)

			// Update activity info with response details
			if err != nil {
				ai.SetGRPCStatus(codes.Code(connect.CodeOf(err)))
			} else {
				ai.SetGRPCStatus(codes.OK)
			}

			// Calculate duration
			duration := time.Since(start)

			// Log the response
			activity.TriggerLLMResponse(ctx, "connectrpc", req.Spec().Procedure, int(ai.GRPCStatus), duration, "1234", "success")

			return resp, err
		}
	}
}

// LLMActivityInterceptor creates a ConnectRPC interceptor specifically for LLM requests
// Only triggers when a Everstack API key is present in the request
func LLMActivityInterceptor() connect.UnaryInterceptorFunc {
	return func(handler connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			// Only process LLM service requests
			if !isLLMService(req.Spec().Procedure) {
				// Not an LLM service request - just call the handler
				return handler(ctx, req)
			}

			// Check if this is an LLM request by looking for Everstack API key
			apiKey := extractAPIKeyFromConnectRequest(req)
			if apiKey == "" {
				// No API key found, not an LLM request - just call the handler
				return handler(ctx, req)
			}

			start := time.Now()

			// Create activity info and add to context
			ai := &info.ActivityInfo{
				Method:        req.Spec().Procedure,
				Path:          req.Spec().Procedure,
				RequestMethod: "POST", // ConnectRPC uses POST for all requests
			}
			ctx = ai.IntoContext(ctx)

			// Extract client information from ConnectRPC request headers
			clientID := extractClientIDFromConnectRequest(req)
			clientName := extractClientNameFromConnectRequest(req)
			origin := extractOriginFromConnectRequest(req)
			userAgent := extractUserAgentFromConnectRequest(req)

			// Add client context to request context
			ctx = context.WithValue(ctx, "clientID", clientID)
			ctx = context.WithValue(ctx, "clientName", clientName)
			ctx = context.WithValue(ctx, "apiKey", apiKey)
			ctx = context.WithValue(ctx, "origin", origin)
			ctx = context.WithValue(ctx, "userAgent", userAgent)

			// Extract LLM-specific information from request
			model := extractModelFromConnectRequest(req)
			provider := extractProviderFromConnectRequest(req)
			tokens := extractTokensFromConnectRequest(req)
			requestID := extractRequestIDFromConnectRequest(req)

			// Log the LLM request
			activity.TriggerLLMRequest(ctx, model, provider, tokens, requestID)

			// Call the handler
			resp, err := handler(ctx, req)

			// Update activity info with response details
			if err != nil {
				ai.SetGRPCStatus(codes.Code(connect.CodeOf(err)))
			} else {
				ai.SetGRPCStatus(codes.OK)
			}

			// Calculate duration
			duration := time.Since(start)

			// Extract response tokens if available
			responseTokens := extractTokensFromConnectResponse(resp)
			status := "success"
			if err != nil {
				status = "error"
			}

			// Log the LLM activity with specific activity type
			activity.TriggerLLMActivity(ctx, "llm-request-response", model, provider, responseTokens, duration, requestID, status)

			return resp, err
		}
	}
}

// ClientContextInterceptor adds client context to the request context
func ClientContextInterceptor() connect.UnaryInterceptorFunc {
	return func(handler connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			// Extract client information from ConnectRPC request headers
			clientID := extractClientIDFromConnectRequest(req)
			clientName := extractClientNameFromConnectRequest(req)
			apiKey := extractAPIKeyFromConnectRequest(req)
			origin := extractOriginFromConnectRequest(req)
			userAgent := extractUserAgentFromConnectRequest(req)

			// Add client context to request context
			ctx = context.WithValue(ctx, "clientID", clientID)
			ctx = context.WithValue(ctx, "clientName", clientName)
			ctx = context.WithValue(ctx, "apiKey", apiKey)
			ctx = context.WithValue(ctx, "origin", origin)
			ctx = context.WithValue(ctx, "userAgent", userAgent)

			return handler(ctx, req)
		}
	}
}

// Helper functions to extract client information from ConnectRPC request headers
func extractClientIDFromConnectRequest(req connect.AnyRequest) string {
	// Try to get client ID from various headers using your constants
	if clientID := req.Header().Get(http.XUserID); clientID != "" {
		return clientID
	}
	if clientID := req.Header().Get(http.XOrgID); clientID != "" {
		return clientID
	}
	if clientID := req.Header().Get("x-client-id"); clientID != "" {
		return clientID
	}
	return "Unknown Client"
}

func extractClientNameFromConnectRequest(req connect.AnyRequest) string {
	if clientName := req.Header().Get("x-client-name"); clientName != "" {
		return clientName
	}
	// If no client name, try to derive from client ID
	clientID := extractClientIDFromConnectRequest(req)
	if clientID != "" {
		return "Client-" + clientID
	}
	return "Unknown Client"
}

func extractAPIKeyFromConnectRequest(req connect.AnyRequest) string {
	// Try Everstack API key first using your constants
	if apiKey := req.Header().Get(http.EverstackApiKey); apiKey != "" {
		return apiKey
	}

	// Try authorization header using your constants
	if auth := req.Header().Get(common.Authorization); auth != "" {
		// Extract API key from "Bearer <key>"
		if len(auth) > 7 && auth[:7] == "Bearer " {
			return auth[7:]
		}
		return auth
	}

	// Try license key as fallback using your constants
	if licenseKey := req.Header().Get(http.EverstackLicenseKey); licenseKey != "" {
		return licenseKey
	}

	return ""
}

func extractOriginFromConnectRequest(req connect.AnyRequest) string {
	if origin := req.Header().Get(http.Origin); origin != "" {
		return origin
	}
	if referer := req.Header().Get(http.Referer); referer != "" {
		return referer
	}
	return "Unknown Origin"
}

func extractUserAgentFromConnectRequest(req connect.AnyRequest) string {
	if userAgent := req.Header().Get(http.UserAgent); userAgent != "" {
		return userAgent
	}
	if userAgent := req.Header().Get(http.XUserAgent); userAgent != "" {
		return userAgent
	}
	return ""
}

// Helper functions to extract LLM-specific information from ConnectRPC request
// These are placeholder implementations - you'll need to adapt them to your actual request structures

func extractModelFromConnectRequest(req connect.AnyRequest) string {
	// Implement based on your LLM request structure
	// Example: if req has a Model field, return req.Model
	// You might need to use req.Any() to get the actual request message
	return "unknown"
}

func extractProviderFromConnectRequest(req connect.AnyRequest) string {
	// Implement based on your LLM request structure
	// Example: if req has a Provider field, return req.Provider
	return "unknown"
}

func extractTokensFromConnectRequest(req connect.AnyRequest) int {
	// Implement based on your LLM request structure
	// Example: if req has a Tokens field, return req.Tokens
	return 0
}

func extractRequestIDFromConnectRequest(req connect.AnyRequest) string {
	// Check for request ID in headers first using your constants
	if requestID := req.Header().Get(http.XRequestID); requestID != "" {
		return requestID
	}
	if correlationID := req.Header().Get(http.XCorrelationID); correlationID != "" {
		return correlationID
	}
	// Then check in request body if available
	return ""
}

func extractTokensFromConnectResponse(resp connect.AnyResponse) int {
	// Implement based on your LLM response structure
	// Example: if resp has a Tokens field, return resp.Tokens
	return 0
}
