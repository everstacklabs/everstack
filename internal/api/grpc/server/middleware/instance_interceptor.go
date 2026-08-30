package middleware

import (
	"context"

	"google.golang.org/grpc"
)

const (
	HTTP1Host = "x-everstack-http1-host"
)

func InstanceInterceptor(externalDomain string, explicitInstanceIdServices ...string) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		ctx = context.WithValue(ctx, HTTP1Host, externalDomain)
		for _, service := range explicitInstanceIdServices {
			ctx = context.WithValue(ctx, service, externalDomain)
		}
		return handler(ctx, req)
	}
}

func SetInstance(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
	// ctx =
	return handler(ctx, req)
}
