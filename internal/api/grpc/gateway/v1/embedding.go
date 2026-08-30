package v1

import (
	"context"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/activity"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"google.golang.org/grpc/metadata"
)

func (s *Server) Embeddings(ctx context.Context, req *connect.Request[gatewaypb.EmbeddingsRequest], stream *connect.ServerStream[gatewaypb.EmbeddingsResponse],
) (retErr error) {
	start := time.Now()
	activity.TriggerGatewayRequest(ctx, "GatewayService", "Embeddings")
	defer func() {
		status := 200
		if retErr != nil {
			status = 500
		}
		activity.TriggerGatewayResponse(ctx, "GatewayService", "Embeddings", status, time.Since(start))
	}()

	// Add correlation ID to response headers if present in context
	correlationID := correlation.GetCorrelationID(ctx)
	if correlationID != "" {
		stream.ResponseHeader().Set(correlation.CorrelationIDHeader, correlationID)
		logger.WithFields(
			"correlation_id", correlationID,
			"procedure", "Embeddings",
		).Trace("Added correlation ID to Connect response headers")
	} else {
		logger.Warn("No correlation ID found in context for Embeddings")
	}

	// Seed sticky key from incoming headers per gateway.yaml load_balancer.key_source
	ctx = s.withKeySourceFromHeaders(ctx, req.Header())

	retErr = processEmbeddings(ctx, s, req.Msg, func(r *gatewaypb.EmbeddingsResponse) error { return stream.Send(r) })
	return retErr
}

// Classic gRPC Embeddings
func (g *GrpcServer) Embeddings(req *gatewaypb.EmbeddingsRequest, srv gatewaypb.GatewayService_EmbeddingsServer) error {
	// Add correlation ID to response headers if present in context
	if correlationID := correlation.GetCorrelationID(srv.Context()); correlationID != "" {
		// For classic gRPC, we need to add metadata to the response
		md := metadata.New(map[string]string{
			correlation.CorrelationIDHeader: correlationID,
		})
		srv.SetHeader(md)
	}

	// Seed sticky key from incoming headers per gateway.yaml load_balancer.key_source
	if md, ok := metadata.FromIncomingContext(srv.Context()); ok {
		hdr := http.Header{}
		for k, vals := range md {
			for _, v := range vals {
				hdr.Add(k, v)
			}
		}
		ctx := g.base.withKeySourceFromHeaders(srv.Context(), hdr)
		return processEmbeddings(ctx, g.base, req, func(r *gatewaypb.EmbeddingsResponse) error { return srv.Send(r) })
	}

	return processEmbeddings(srv.Context(), g.base, req, func(r *gatewaypb.EmbeddingsResponse) error { return srv.Send(r) })
}
