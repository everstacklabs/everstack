package middleware

import (
	"context"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/api/call"
)

func CallDurationHandler() connect.UnaryInterceptorFunc {
	return func(handler connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			ctx = call.WithTimestamp(ctx)
			return handler(ctx, req)
		}
	}
}
