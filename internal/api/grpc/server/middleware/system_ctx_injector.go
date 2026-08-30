package middleware

import (
	"context"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/cqrs"
)

// SystemContextInjector ensures the CQRS system is present in the request context
// before other interceptors execute (e.g., API key validation that uses DB).
type SystemContextInjector struct {
	system *cqrs.System
}

func NewSystemContextInjector(system *cqrs.System) *SystemContextInjector {
	return &SystemContextInjector{system: system}
}

func (i *SystemContextInjector) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if i.system != nil {
			ctx = cqrs.WithSystem(ctx, i.system)
		}
		return next(ctx, req)
	}
}

func (i *SystemContextInjector) WrapStreaming(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if i.system != nil {
			ctx = cqrs.WithSystem(ctx, i.system)
		}
		return next(ctx, conn)
	}
}

func (i *SystemContextInjector) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		if i.system != nil {
			ctx = cqrs.WithSystem(ctx, i.system)
		}
		return next(ctx, spec)
	}
}

func (i *SystemContextInjector) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if i.system != nil {
			ctx = cqrs.WithSystem(ctx, i.system)
		}
		return next(ctx, conn)
	}
}
