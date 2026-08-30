package middleware

import (
	"strings"

	grpc_trace "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc/stats"
)

// Methods that should be traced (gateway + agent runtime operations).
// All other gRPC methods (CRUD, admin, config) are excluded.
var tracedMethods = []string{
	"ChatCompletion",
	"Embeddings",
	"RunTurn",
	"RunTurnStream",
	"ProvisionAgent",
}

func DefaultTracingServer() stats.Handler {
	return grpc_trace.NewServerHandler(
		grpc_trace.WithFilter(
			func(info *stats.RPCTagInfo) bool {
				for _, m := range tracedMethods {
					if strings.Contains(info.FullMethodName, m) {
						return true
					}
				}
				return false
			},
		),
		grpc_trace.WithPropagators(
			propagation.NewCompositeTextMapPropagator(
				propagation.TraceContext{},
				propagation.Baggage{},
			),
		),
	)
}
