package v1

import (
	"context"

	"connectrpc.com/connect"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
)

// Rerank implements document reranking by relevance to a query.
func (s *Server) Rerank(ctx context.Context, req *connect.Request[gatewaypb.RerankRequest]) (*connect.Response[gatewaypb.RerankResponse], error) {
	logger.Debug("gateway: processing rerank request")

	if err := s.EnsureProvidersForRequest(ctx); err != nil {
		logger.WithFields("error", err.Error()).Warn("gateway: failed to ensure providers for rerank request")
	}
	bundle := s.providersFor(ctx)
	if bundle == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gw.ErrNotImplemented("rerank: no providers configured for this tenant"))
	}

	// Find a provider that supports reranking
	provider, providerName, ok := bundle.reg.FindRerankProvider()
	if !ok {
		return nil, connect.NewError(connect.CodeUnimplemented, gw.ErrNotImplemented("rerank: no rerank provider available"))
	}

	logger.WithFields("provider", providerName, "model", req.Msg.Model).Debug("gateway: routing rerank request")

	// Convert proto to gateway request
	gwReq := gw.RerankRequest{
		Model:           req.Msg.Model,
		Query:           req.Msg.Query,
		Documents:       req.Msg.Documents,
		TopN:            int(req.Msg.TopN),
		ReturnDocuments: req.Msg.ReturnDocuments,
		MaxTokensPerDoc: int(req.Msg.MaxTokensPerDoc),
		Metadata:        protoStructToMap(req.Msg.Metadata),
	}

	// Convert document objects if provided
	for _, doc := range req.Msg.DocumentObjects {
		gwReq.DocumentObjects = append(gwReq.DocumentObjects, gw.RerankDocument{
			Text: doc.Text,
		})
	}

	// Apply defaults
	if gwReq.Model == "" {
		gwReq.Model = "rerank-v3.5"
	}

	// Call provider
	resp, err := provider.Rerank(ctx, gwReq)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("gateway: rerank failed")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Convert to proto response
	protoResp := &gatewaypb.RerankResponse{
		Id:    resp.ID,
		Model: resp.Model,
	}

	// Convert results
	for _, result := range resp.Results {
		protoResp.Results = append(protoResp.Results, &gatewaypb.RerankResult{
			Index:          int32(result.Index),
			RelevanceScore: result.RelevanceScore,
			Document:       result.Document,
		})
	}

	// Convert meta if present
	if resp.Meta != nil {
		protoResp.Meta = &gatewaypb.RerankApiVersion{
			Version:    resp.Meta.Version,
			IsBillable: resp.Meta.IsBillable,
		}
		if resp.Meta.BilledUnits != nil {
			protoResp.Meta.BilledUnits = &gatewaypb.RerankBilledUnits{
				SearchUnits:  int32(resp.Meta.BilledUnits.SearchUnits),
				InputTokens:  int32(resp.Meta.BilledUnits.InputTokens),
				OutputTokens: int32(resp.Meta.BilledUnits.OutputTokens),
			}
		}
		if resp.Meta.Tokens != nil {
			protoResp.Meta.Tokens = &gatewaypb.RerankTokens{
				InputTokens:  int32(resp.Meta.Tokens.InputTokens),
				OutputTokens: int32(resp.Meta.Tokens.OutputTokens),
			}
		}
	}

	return connect.NewResponse(protoResp), nil
}
