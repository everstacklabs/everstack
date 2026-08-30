package middleware

import (
	"context"

	"connectrpc.com/connect"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/api/tenancy"
)

// LocalTenantInterceptor injects a stable tenant id for standalone self-hosted
// gateways so tenant-scoped handlers work without cloud tenant_config or a
// license activation. It is a no-op in shared/cloud mode and when auth already
// resolved a tenant.
type LocalTenantInterceptor struct {
	resolver *tenancy.LocalScopeResolver
}

func NewLocalTenantInterceptor(db *sqlx.DB, sharedMode bool) *LocalTenantInterceptor {
	return &LocalTenantInterceptor{resolver: tenancy.NewLocalScopeResolver(db, sharedMode)}
}

func (i *LocalTenantInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if i != nil && i.resolver != nil {
			ctx = i.resolver.Inject(ctx)
		}
		return next(ctx, req)
	}
}

func (i *LocalTenantInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn { return next(ctx, spec) }
}

func (i *LocalTenantInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		if i != nil && i.resolver != nil {
			ctx = i.resolver.Inject(ctx)
		}
		return next(ctx, conn)
	}
}
