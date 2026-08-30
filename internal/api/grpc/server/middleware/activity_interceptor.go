package middleware

import (
	"context"
	"net/http"
	"slices"
	"strings"

	"connectrpc.com/connect"

	"github.com/everstacklabs/everstack/internal/activity"
	"github.com/everstacklabs/everstack/internal/api/grpc/mferrors"
	http_util "github.com/everstacklabs/everstack/internal/api/http"
	ainfo "github.com/everstacklabs/everstack/internal/api/info"
)

func ActivityInterceptor() connect.UnaryInterceptorFunc {
	return func(handler connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			ctx = activityInfoFromGateway(ctx, req.Header()).SetMethod(req.Spec().Procedure).IntoContext(ctx)
			resp, err := handler(ctx, req)
			if isResourceAPI(req.Spec().Procedure) && !isLLMService(req.Spec().Procedure) {
				code, _, _, _ := mferrors.ExtractMFError(err)
				ctx = ainfo.ActivityInfoFromContext(ctx).SetGRPCStatus(code).IntoContext(ctx)
				activity.TriggerGRPCWithContext(ctx, activity.ResourceAPI)
			}
			return resp, err
		}
	}
}

var resourcePrefixes = []string{
	"/everstack.health.v1.HealthService/",
	"/everstack.management.v1.ManagementService/",
	"/everstack.admin.v1.AdminService/",
	"/everstack.settings.v1.SettingsService/",
	"/everstack.auth.v1.AuthService/",
	// "/everstack.gateway.v1.GatewayService/",
}

func isResourceAPI(method string) bool {
	return slices.ContainsFunc(resourcePrefixes, func(prefix string) bool {
		return strings.HasPrefix(method, prefix)
	})
}

func isLLMService(method string) bool {
	return strings.HasPrefix(method, "/everstack.llm.v1.LLMService/")
}

func hasAPIKey(req connect.AnyRequest) bool {
	// Check for Everstack API key
	if req.Header().Get(http_util.EverstackApiKey) != "" {
		return true
	}

	// Check for Authorization header with Bearer token
	if auth := req.Header().Get(http_util.Authorization); auth != "" && strings.HasPrefix(auth, "Bearer ") {
		return true
	}

	return false
}

func activityInfoFromGateway(ctx context.Context, headers http.Header) *ainfo.ActivityInfo {
	info := ainfo.ActivityInfoFromContext(ctx)
	path := headers.Get(activity.PathKey)
	if path == "" {
		// grpc-gateway sends metadata as HTTP headers with "Grpc-Metadata-" prefix
		path = headers.Get(http_util.GrpcMetadataPrefix + activity.PathKey)
	}
	requestMethod := headers.Get(activity.RequestMethodKey)
	if requestMethod == "" {
		requestMethod = headers.Get(http_util.GrpcMetadataPrefix + activity.RequestMethodKey)
	}
	return info.SetPath(path).SetRequestMethod(requestMethod)
}
