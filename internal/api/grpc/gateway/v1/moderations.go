package v1

import (
	"context"

	"connectrpc.com/connect"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
)

// Moderation implements content classification for policy violations.
func (s *Server) Moderation(ctx context.Context, req *connect.Request[gatewaypb.ModerationRequest]) (*connect.Response[gatewaypb.ModerationResponse], error) {
	logger.Debug("gateway: processing moderation request")

	if err := s.EnsureProvidersForRequest(ctx); err != nil {
		logger.WithFields("error", err.Error()).Warn("gateway: failed to ensure providers for moderation request")
	}
	bundle := s.providersFor(ctx)
	if bundle == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gw.ErrNotImplemented("moderation: no providers configured for this tenant"))
	}

	// Find a provider that supports moderation
	provider, providerName, ok := bundle.reg.FindModerationProvider()
	if !ok {
		return nil, connect.NewError(connect.CodeUnimplemented, gw.ErrNotImplemented("moderation: no moderation provider available"))
	}

	logger.WithFields("provider", providerName, "model", req.Msg.Model).Debug("gateway: routing moderation request")

	// Convert proto to gateway request
	gwReq := gw.ModerationRequest{
		Input:    req.Msg.Input,
		Model:    req.Msg.Model,
		Metadata: protoStructToMap(req.Msg.Metadata),
	}

	// Convert inputs if provided
	for _, input := range req.Msg.Inputs {
		gwInput := gw.ModerationInput{
			Type: input.Type,
			Text: input.Text,
		}
		if input.ImageUrl != nil {
			gwInput.ImageURL = &gw.ModerationImageURL{
				URL: input.ImageUrl.Url,
			}
		}
		gwReq.Inputs = append(gwReq.Inputs, gwInput)
	}

	// Apply defaults
	if gwReq.Model == "" {
		gwReq.Model = "omni-moderation-latest"
	}

	// Call provider
	resp, err := provider.Moderate(ctx, gwReq)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("gateway: moderation failed")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Convert to proto response
	protoResp := &gatewaypb.ModerationResponse{
		Id:    resp.ID,
		Model: resp.Model,
	}

	// Convert results
	for _, result := range resp.Results {
		protoResult := &gatewaypb.ModerationResult{
			Flagged: result.Flagged,
			Categories: &gatewaypb.ModerationCategories{
				Hate:                  result.Categories.Hate,
				HateThreatening:       result.Categories.HateThreatening,
				Harassment:            result.Categories.Harassment,
				HarassmentThreatening: result.Categories.HarassmentThreatening,
				Illicit:               result.Categories.Illicit,
				IllicitViolent:        result.Categories.IllicitViolent,
				SelfHarm:              result.Categories.SelfHarm,
				SelfHarmIntent:        result.Categories.SelfHarmIntent,
				SelfHarmInstructions:  result.Categories.SelfHarmInstructions,
				Sexual:                result.Categories.Sexual,
				SexualMinors:          result.Categories.SexualMinors,
				Violence:              result.Categories.Violence,
				ViolenceGraphic:       result.Categories.ViolenceGraphic,
			},
			CategoryScores: &gatewaypb.ModerationCategoryScores{
				Hate:                  result.CategoryScores.Hate,
				HateThreatening:       result.CategoryScores.HateThreatening,
				Harassment:            result.CategoryScores.Harassment,
				HarassmentThreatening: result.CategoryScores.HarassmentThreatening,
				Illicit:               result.CategoryScores.Illicit,
				IllicitViolent:        result.CategoryScores.IllicitViolent,
				SelfHarm:              result.CategoryScores.SelfHarm,
				SelfHarmIntent:        result.CategoryScores.SelfHarmIntent,
				SelfHarmInstructions:  result.CategoryScores.SelfHarmInstructions,
				Sexual:                result.CategoryScores.Sexual,
				SexualMinors:          result.CategoryScores.SexualMinors,
				Violence:              result.CategoryScores.Violence,
				ViolenceGraphic:       result.CategoryScores.ViolenceGraphic,
			},
		}
		protoResp.Results = append(protoResp.Results, protoResult)
	}

	return connect.NewResponse(protoResp), nil
}
