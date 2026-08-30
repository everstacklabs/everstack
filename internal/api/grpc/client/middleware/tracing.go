package middleware

import (
	"strings"

	grpc_trace "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel/propagation"
	"google.golang.org/grpc/stats"
)

// tracedMethods lists the gRPC methods that should be traced on the client side.
var tracedMethods = []string{
	"ChatCompletion",
	"Embeddings",
	"RunTurn",
	"RunTurnStream",
	"ProvisionAgent",
}

func DefaultTracingClient() stats.Handler {
	return grpc_trace.NewClientHandler(
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
