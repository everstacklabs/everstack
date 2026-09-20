package server

import (
	"context"
	"net/http"

	"connectrpc.com/connect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/reflect/protoreflect"
)

type Server interface {
	AppName() string
	MethodPrefix() string
	// AuthMethods() authz.MethodMapping
}

type ConnectServer interface {
	Server
	// RegisterConnectServer takes handler options rather than bare
	// interceptors so cross-cutting handler configuration (compression
	// thresholds, read limits) can be applied uniformly by the caller instead
	// of being repeated, or forgotten, in each of thirty services.
	RegisterConnectServer(opts ...connect.HandlerOption) (string, http.Handler)
	FileDescriptor() protoreflect.FileDescriptor
}

// GatewayRegistrar is a function that registers grpc-gateway HTTP handlers for a service.
type GatewayRegistrar func(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error

// GatewayRegistrable can be implemented by services that also expose grpc-gateway handlers.
// Implementations should call the generated Register<Service>HandlerFromEndpoint.
type GatewayRegistrable interface {
	RegisterGateway(ctx context.Context, mux *runtime.ServeMux, endpoint string, opts []grpc.DialOption) error
}
