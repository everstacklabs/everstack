package api

import (
	api_keyv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/api_key/v1"
	eventsv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/events/v1"
	gatewayv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	healthv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/health/v1"
	"google.golang.org/grpc"
)

// RegisterServices registers all gRPC services
func RegisterServices(s *grpc.Server, healthService healthv1.HealthServiceServer, gatewayService gatewayv1.GatewayServiceServer, apiKeyService api_keyv1.ApiKeyServiceServer, eventsService eventsv1.EventsServiceServer) {
	healthv1.RegisterHealthServiceServer(s, healthService)
	gatewayv1.RegisterGatewayServiceServer(s, gatewayService)
	api_keyv1.RegisterApiKeyServiceServer(s, apiKeyService)
	eventsv1.RegisterEventsServiceServer(s, eventsService)
}

// Available methods for HealthService:
// - Health
// - GetSystemInfo
