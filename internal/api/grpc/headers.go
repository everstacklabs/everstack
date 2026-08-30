package grpc

import (
	"context"

	"github.com/grpc-ecosystem/go-grpc-middleware/util/metautils"
	"github.com/grpc-ecosystem/grpc-gateway/runtime"
	"github.com/everstacklabs/everstack/internal/api/common"
)

func RetrieveHeader(ctx context.Context, headerKey string) string {
	return metautils.ExtractIncoming(ctx).Get(headerKey)
}

func RetrieveGatewayHeader(ctx context.Context, headerKey string) string {
	return RetrieveHeader(ctx, runtime.MetadataPrefix+headerKey)
}

func RetrieveAuthorizationHeader(ctx context.Context) string {
	return RetrieveHeader(ctx, common.Authorization)
}
