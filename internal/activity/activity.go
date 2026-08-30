package activity

import (
	"context"
	"strconv"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/everstacklabs/everstack/internal/api/http"
	"github.com/everstacklabs/everstack/internal/api/info"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

const (
	Activity = "activity"

	PathKey          = "everstack-activity-path"
	RequestMethodKey = "everstack-activity-request-method"
)

type TriggerMethod int

const (
	Unspecified TriggerMethod = iota
	ClientConnect
	ClientDisconnect
	ResourceAPI
	LLMRequest
	LLMResponse
	LLMActivity
	GatewayRequest
	GatewayResponse
	RateLimitExceeded
	AuthenticationFailed
)

func (t TriggerMethod) String() string {
	switch t {
	case Unspecified:
		return "unspecified"
	case ClientConnect:
		return "clientConnect"
	case ClientDisconnect:
		return "clientDisconnect"
	case ResourceAPI:
		return "resourceAPI"
	case LLMRequest:
		return "llmRequest"
	case LLMResponse:
		return "llmResponse"
	case LLMActivity:
		return "llmActivity"
	case GatewayRequest:
		return "gatewayRequest"
	case GatewayResponse:
		return "gatewayResponse"
	case RateLimitExceeded:
		return "rateLimitExceeded"
	case AuthenticationFailed:
		return "authenticationFailed"
	default:
		return "unknown"
	}
}

// ClientContext represents client-specific context data
type ClientContext struct {
	ClientID  string
	APIKey    string
	Origin    string
	UserAgent string
}

// GetClientContext extracts client information from context
func GetClientContext(ctx context.Context) *ClientContext {
	lookup := func(keys ...string) string {
		for _, k := range keys {
			if v := ctx.Value(k); v != nil {
				if s, ok := v.(string); ok {
					return s
				}
			}
		}
		return ""
	}

	clientID := lookup("client-id", "clientID")
	apiKey := lookup("api-key", "apiKey")
	origin := lookup("origin", "Origin")
	userAgent := lookup("user-agent", "userAgent", "User-Agent")

	return &ClientContext{
		ClientID:  clientID,
		APIKey:    apiKey,
		Origin:    origin,
		UserAgent: userAgent,
	}
}

// TriggerClientConnect logs when a client connects to the gateway
func TriggerClientConnect(ctx context.Context, clientID, clientName string) {
	clientCtx := GetClientContext(ctx)

	logger.WithFields(
		"clientID", clientID,
		"origin", clientCtx.Origin,
		"userAgent", clientCtx.UserAgent,
		"trigger", ClientConnect.String(),
	).Info(Activity)
}

// TriggerClientDisconnect logs when a client disconnects from the gateway
func TriggerClientDisconnect(ctx context.Context, clientID, clientName string, duration time.Duration) {
	clientCtx := GetClientContext(ctx)

	logger.WithFields(
		"clientID", clientID,
		"origin", clientCtx.Origin,
		"userAgent", clientCtx.UserAgent,
		"duration", duration.Seconds(),
		"trigger", ClientDisconnect.String(),
	).Info(Activity)
}

// TriggerLLMRequest logs LLM request activity for a specific client
func TriggerLLMRequest(ctx context.Context, model, provider string, tokens int, requestID string) {
	clientCtx := GetClientContext(ctx)
	ai := info.ActivityInfoFromContext(ctx)

	logger.WithFields(
		"clientID", clientCtx.ClientID,
		"origin", clientCtx.Origin,
		"trigger", LLMRequest.String(),
		"method", ai.Method,
		"path", ai.Path,
		"requestMethod", ai.RequestMethod,
		"model", model,
		"provider", provider,
		"tokens", tokens,
		"requestID", requestID,
	).Trace(Activity)
}

// TriggerLLMResponse logs LLM response activity for a specific client
func TriggerLLMResponse(ctx context.Context, model, provider string, tokens int, duration time.Duration, requestID string, status string) {
	clientCtx := GetClientContext(ctx)
	ai := info.ActivityInfoFromContext(ctx)

	logger.WithFields(
		"clientID", clientCtx.ClientID,
		"origin", clientCtx.Origin,
		"trigger", LLMResponse.String(),
		"method", ai.Method,
		"path", ai.Path,
		"requestMethod", ai.RequestMethod,
		"model", model,
		"provider", provider,
		"tokens", tokens,
		"duration", duration.Seconds(),
		"requestID", requestID,
		"status", status,
	).Trace(Activity)
}

// TriggerLLMActivity logs LLM activity with a specific activity type
func TriggerLLMActivity(ctx context.Context, activityType string, model, provider string, tokens int, duration time.Duration, requestID string, status string) {
	clientCtx := GetClientContext(ctx)
	ai := info.ActivityInfoFromContext(ctx)

	logger.WithFields(
		"clientID", clientCtx.ClientID,
		"origin", clientCtx.Origin,
		"trigger", LLMActivity.String(),
		"activityType", activityType,
		"method", ai.Method,
		"path", ai.Path,
		"requestMethod", ai.RequestMethod,
		"model", model,
		"provider", provider,
		"tokens", tokens,
		"duration", duration.Seconds(),
		"requestID", requestID,
		"status", status,
	).Trace(Activity)
}

// TriggerSimpleLLMActivity logs simple LLM activity with just the activity type
func TriggerSimpleLLMActivity(ctx context.Context, activityType string) {
	clientCtx := GetClientContext(ctx)
	ai := info.ActivityInfoFromContext(ctx)

	logger.WithFields(
		"clientID", clientCtx.ClientID,
		"origin", clientCtx.Origin,
		"trigger", LLMActivity.String(),
		"activityType", activityType,
		"method", ai.Method,
		"path", ai.Path,
		"requestMethod", ai.RequestMethod,
	).Trace(Activity)
}

// TriggerGatewayRequest logs gateway request activity
func TriggerGatewayRequest(ctx context.Context, service, endpoint string) {
	clientCtx := GetClientContext(ctx)
	ai := info.ActivityInfoFromContext(ctx)

	logger.WithFields(
		"clientID", clientCtx.ClientID,
		"origin", clientCtx.Origin,
		"trigger", GatewayRequest.String(),
		"method", ai.Method,
		"path", ai.Path,
		"requestMethod", ai.RequestMethod,
		"service", service,
		"endpoint", endpoint,
	).Trace(Activity)
}

// TriggerGatewayResponse logs gateway response activity
func TriggerGatewayResponse(ctx context.Context, service, endpoint string, status int, duration time.Duration) {
	clientCtx := GetClientContext(ctx)
	ai := info.ActivityInfoFromContext(ctx)

	logger.WithFields(
		"clientID", clientCtx.ClientID,
		"origin", clientCtx.Origin,
		"trigger", GatewayResponse.String(),
		"method", ai.Method,
		"path", ai.Path,
		"requestMethod", ai.RequestMethod,
		"service", service,
		"endpoint", endpoint,
		"status", status,
		"duration", duration.Seconds(),
	).Trace(Activity)
}

// TriggerRateLimitExceeded logs when a client exceeds rate limits
func TriggerRateLimitExceeded(ctx context.Context, limitType string, limit int, window time.Duration) {
	clientCtx := GetClientContext(ctx)
	ai := info.ActivityInfoFromContext(ctx)

	logger.WithFields(
		"clientID", clientCtx.ClientID,
		"origin", clientCtx.Origin,
		"trigger", RateLimitExceeded.String(),
		"method", ai.Method,
		"path", ai.Path,
		"requestMethod", ai.RequestMethod,
		"limitType", limitType,
		"limit", limit,
		"window", window.Seconds(),
	).Warn(Activity)
}

// TriggerAuthenticationFailed logs authentication failures
func TriggerAuthenticationFailed(ctx context.Context, reason string) {
	clientCtx := GetClientContext(ctx)
	ai := info.ActivityInfoFromContext(ctx)

	logger.WithFields(
		"clientID", clientCtx.ClientID,
		"origin", clientCtx.Origin,
		"trigger", AuthenticationFailed.String(),
		"method", ai.Method,
		"path", ai.Path,
		"requestMethod", ai.RequestMethod,
		"reason", reason,
	).Warn(Activity)
}

// TriggerGRPCWithContext logs gRPC activity with client context
func TriggerGRPCWithContext(ctx context.Context, trigger TriggerMethod) {
	clientCtx := GetClientContext(ctx)
	ai := info.ActivityInfoFromContext(ctx)

	logger.WithFields(
		"clientID", clientCtx.ClientID,
		"origin", http.DomainContext(ctx).Origin(),
		"trigger", trigger.String(),
		"method", ai.Method,
		"path", ai.Path,
		"requestMethod", ai.RequestMethod,
		"grpcStatus", strconv.Itoa(int(ai.GRPCStatus)),
		"httpStatus", strconv.Itoa(runtime.HTTPStatusFromCode(ai.GRPCStatus)),
	).Trace(Activity)
}
