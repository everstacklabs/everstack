package middleware

import (
	"context"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

// UnaryServerStatusInterceptor returns a grpc.UnaryServerInterceptor that sets span status based on the gRPC status code
func UnaryServerStatusInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Get current span from context
		span := trace.SpanFromContext(ctx)
		
		// Set initial status to OK - this ensures all spans have a status
		// If an error occurs, it will be overwritten
		span.SetStatus(codes.Ok, "Success")
		
		// Add method information as attributes for better filtering
		span.SetAttributes(
			attribute.String("rpc.method", info.FullMethod),
			attribute.String("rpc.system", "grpc"),
		)
		
		// Call the handler
		resp, err := handler(ctx, req)
		
		// Set span status based on error
		if err != nil {
			// Get gRPC status from error
			st, ok := status.FromError(err)
			if ok {
				// Set span status with gRPC status code and message
				span.SetStatus(codes.Error, st.Message())
				// Add the gRPC status code as an attribute
				span.SetAttributes(
					GRPCStatusCodeKey.Int64(int64(st.Code())),
					attribute.Bool("error", true),
				)
				logger.WithFields(
					"method", info.FullMethod,
					"status_code", st.Code().String(),
					"error", err.Error(),
				).Debug("gRPC request failed")
			} else {
				// Generic error handling
				span.SetStatus(codes.Error, err.Error())
				span.SetAttributes(
					ErrorMessageKey.String(err.Error()),
					attribute.Bool("error", true),
				)
			}
		} else {
			// Explicitly set status to OK for successful requests
			span.SetStatus(codes.Ok, "Success")
			span.SetAttributes(
				GRPCStatusCodeKey.Int64(0), // 0 = OK in gRPC
				attribute.Bool("error", false),
			)
		}
		
		return resp, err
	}
}

// StreamServerStatusInterceptor returns a grpc.StreamServerInterceptor that sets span status based on the gRPC status code
func StreamServerStatusInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// Get current span from context
		span := trace.SpanFromContext(ss.Context())
		
		// Set initial status to OK - this ensures all spans have a status
		// If an error occurs, it will be overwritten
		span.SetStatus(codes.Ok, "Success")
		
		// Add method information as attributes for better filtering
		span.SetAttributes(
			attribute.String("rpc.method", info.FullMethod),
			attribute.String("rpc.system", "grpc"),
		)
		
		// Call the handler
		err := handler(srv, ss)
		
		// Set span status based on error
		if err != nil {
			// Get gRPC status from error
			st, ok := status.FromError(err)
			if ok {
				// Set span status with gRPC status code and message
				span.SetStatus(codes.Error, st.Message())
				// Add the gRPC status code as an attribute
				span.SetAttributes(
					GRPCStatusCodeKey.Int64(int64(st.Code())),
					attribute.Bool("error", true),
				)
				logger.WithFields(
					"method", info.FullMethod,
					"status_code", st.Code().String(),
					"error", err.Error(),
				).Debug("gRPC stream failed")
			} else {
				// Generic error handling
				span.SetStatus(codes.Error, err.Error())
				span.SetAttributes(
					ErrorMessageKey.String(err.Error()),
					attribute.Bool("error", true),
				)
			}
		} else {
			// Explicitly set status to OK for successful streams
			span.SetStatus(codes.Ok, "Success")
			span.SetAttributes(
				GRPCStatusCodeKey.Int64(0), // 0 = OK in gRPC
				attribute.Bool("error", false),
			)
		}
		
		return err
	}
}

// Define OpenTelemetry attribute keys for consistency
var (
	GRPCStatusCodeKey = attribute.Key("rpc.grpc.status_code")
	ErrorMessageKey   = attribute.Key("error.message")
)
