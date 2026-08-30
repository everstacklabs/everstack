package middleware

import (
	"context"

	"connectrpc.com/connect"
	rtconfig "github.com/everstacklabs/everstack/internal/domain/runtime_config"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// RuntimeConfigInjector injects the RuntimeConfigService into the request context
// for dynamic configuration access (hot-reload support).
type RuntimeConfigInjector struct {
	service *rtconfig.Service
}

func NewRuntimeConfigInjector(service *rtconfig.Service) *RuntimeConfigInjector {
	return &RuntimeConfigInjector{service: service}
}

func (i *RuntimeConfigInjector) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if i.service != nil {
			ctx = context.WithValue(ctx, contextkeys.RuntimeConfigService, i.service)
		}
		return next(ctx, req)
	}
}

func (i *RuntimeConfigInjector) WrapStreaming(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if i.service != nil {
			ctx = context.WithValue(ctx, contextkeys.RuntimeConfigService, i.service)
		}
		return next(ctx, conn)
	}
}

func (i *RuntimeConfigInjector) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		if i.service != nil {
			ctx = context.WithValue(ctx, contextkeys.RuntimeConfigService, i.service)
		}
		return next(ctx, spec)
	}
}

func (i *RuntimeConfigInjector) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if i.service != nil {
			ctx = context.WithValue(ctx, contextkeys.RuntimeConfigService, i.service)
		}
		return next(ctx, conn)
	}
}
