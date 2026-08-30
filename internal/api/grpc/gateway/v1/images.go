package v1

import (
	"context"

	"connectrpc.com/connect"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
)

// ImageGeneration implements image generation from text prompts.
func (s *Server) ImageGeneration(ctx context.Context, req *connect.Request[gatewaypb.ImageGenerationRequest]) (*connect.Response[gatewaypb.ImageGenerationResponse], error) {
	logger.Debug("gateway: processing image generation request")

	if err := s.EnsureProvidersForRequest(ctx); err != nil {
		logger.WithFields("error", err.Error()).Warn("gateway: failed to ensure providers for image request")
	}
	bundle := s.providersFor(ctx)
	if bundle == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gw.ErrNotImplemented("image: no providers configured for this tenant"))
	}

	// Find a provider that supports images
	provider, providerName, ok := bundle.reg.FindImageProvider()
	if !ok {
		return nil, connect.NewError(connect.CodeUnimplemented, gw.ErrNotImplemented("image_generation: no image provider available"))
	}

	logger.WithFields("provider", providerName, "model", req.Msg.Model).Debug("gateway: routing image generation request")

	// Convert proto to gateway request
	gwReq := gw.ImageGenerationRequest{
		Prompt:         req.Msg.Prompt,
		Model:          req.Msg.Model,
		N:              int(req.Msg.N),
		Quality:        req.Msg.Quality,
		ResponseFormat: req.Msg.ResponseFormat,
		Size:           req.Msg.Size,
		Style:          req.Msg.Style,
		User:           req.Msg.User,
		Background:     req.Msg.Background,
		OutputFormat:   req.Msg.OutputFormat,
		Moderation:     req.Msg.Moderation,
		Metadata:       protoStructToMap(req.Msg.Metadata),
	}

	// Apply defaults
	if gwReq.Model == "" {
		gwReq.Model = "dall-e-3"
	}
	if gwReq.N == 0 {
		gwReq.N = 1
	}
	if gwReq.ResponseFormat == "" {
		gwReq.ResponseFormat = "url"
	}
	if gwReq.Size == "" {
		gwReq.Size = "1024x1024"
	}

	// Call provider
	resp, err := provider.GenerateImage(ctx, gwReq)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("gateway: image generation failed")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Convert to proto response
	protoResp := &gatewaypb.ImageGenerationResponse{
		Created: resp.Created,
		Model:   resp.Model,
	}

	// Convert image data
	for _, img := range resp.Data {
		protoResp.Data = append(protoResp.Data, &gatewaypb.ImageData{
			B64Json:       img.B64JSON,
			Url:           img.URL,
			RevisedPrompt: img.RevisedPrompt,
		})
	}

	// Convert usage if present
	if resp.Usage != nil {
		protoResp.Usage = &gatewaypb.ImageUsage{
			InputTokens:  int32(resp.Usage.InputTokens),
			OutputTokens: int32(resp.Usage.OutputTokens),
			TotalTokens:  int32(resp.Usage.TotalTokens),
		}
		if resp.Usage.InputTokensDetails != nil {
			protoResp.Usage.InputTokensDetails = &gatewaypb.ImageTokenDetails{
				TextTokens:  int32(resp.Usage.InputTokensDetails.TextTokens),
				ImageTokens: int32(resp.Usage.InputTokensDetails.ImageTokens),
			}
		}
		if resp.Usage.OutputTokensDetails != nil {
			protoResp.Usage.OutputTokensDetails = &gatewaypb.ImageTokenDetails{
				TextTokens:  int32(resp.Usage.OutputTokensDetails.TextTokens),
				ImageTokens: int32(resp.Usage.OutputTokensDetails.ImageTokens),
			}
		}
	}

	return connect.NewResponse(protoResp), nil
}

// ImageEdit implements image editing.
func (s *Server) ImageEdit(ctx context.Context, req *connect.Request[gatewaypb.ImageEditRequest]) (*connect.Response[gatewaypb.ImageEditResponse], error) {
	logger.Debug("gateway: processing image edit request")

	if err := s.EnsureProvidersForRequest(ctx); err != nil {
		logger.WithFields("error", err.Error()).Warn("gateway: failed to ensure providers for image request")
	}
	bundle := s.providersFor(ctx)
	if bundle == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gw.ErrNotImplemented("image: no providers configured for this tenant"))
	}

	// Find a provider that supports images
	provider, providerName, ok := bundle.reg.FindImageProvider()
	if !ok {
		return nil, connect.NewError(connect.CodeUnimplemented, gw.ErrNotImplemented("image_edit: no image provider available"))
	}

	logger.WithFields("provider", providerName, "model", req.Msg.Model).Debug("gateway: routing image edit request")

	// Convert proto to gateway request
	gwReq := gw.ImageEditRequest{
		Image:          req.Msg.Image,
		Prompt:         req.Msg.Prompt,
		Mask:           req.Msg.Mask,
		Model:          req.Msg.Model,
		N:              int(req.Msg.N),
		Size:           req.Msg.Size,
		ResponseFormat: req.Msg.ResponseFormat,
		User:           req.Msg.User,
		Quality:        req.Msg.Quality,
		Metadata:       protoStructToMap(req.Msg.Metadata),
	}

	// Apply defaults
	if gwReq.Model == "" {
		gwReq.Model = "dall-e-2"
	}
	if gwReq.N == 0 {
		gwReq.N = 1
	}
	if gwReq.ResponseFormat == "" {
		gwReq.ResponseFormat = "url"
	}
	if gwReq.Size == "" {
		gwReq.Size = "1024x1024"
	}

	// Call provider
	resp, err := provider.EditImage(ctx, gwReq)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("gateway: image edit failed")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Convert to proto response
	protoResp := &gatewaypb.ImageEditResponse{
		Created: resp.Created,
		Model:   resp.Model,
	}

	// Convert image data
	for _, img := range resp.Data {
		protoResp.Data = append(protoResp.Data, &gatewaypb.ImageData{
			B64Json:       img.B64JSON,
			Url:           img.URL,
			RevisedPrompt: img.RevisedPrompt,
		})
	}

	// Convert usage if present
	if resp.Usage != nil {
		protoResp.Usage = &gatewaypb.ImageUsage{
			InputTokens:  int32(resp.Usage.InputTokens),
			OutputTokens: int32(resp.Usage.OutputTokens),
			TotalTokens:  int32(resp.Usage.TotalTokens),
		}
	}

	return connect.NewResponse(protoResp), nil
}

// ImageVariation implements image variation creation.
func (s *Server) ImageVariation(ctx context.Context, req *connect.Request[gatewaypb.ImageVariationRequest]) (*connect.Response[gatewaypb.ImageVariationResponse], error) {
	logger.Debug("gateway: processing image variation request")

	if err := s.EnsureProvidersForRequest(ctx); err != nil {
		logger.WithFields("error", err.Error()).Warn("gateway: failed to ensure providers for image request")
	}
	bundle := s.providersFor(ctx)
	if bundle == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gw.ErrNotImplemented("image: no providers configured for this tenant"))
	}

	// Find a provider that supports images
	provider, providerName, ok := bundle.reg.FindImageProvider()
	if !ok {
		return nil, connect.NewError(connect.CodeUnimplemented, gw.ErrNotImplemented("image_variation: no image provider available"))
	}

	logger.WithFields("provider", providerName, "model", req.Msg.Model).Debug("gateway: routing image variation request")

	// Convert proto to gateway request
	gwReq := gw.ImageVariationRequest{
		Image:          req.Msg.Image,
		Model:          req.Msg.Model,
		N:              int(req.Msg.N),
		ResponseFormat: req.Msg.ResponseFormat,
		Size:           req.Msg.Size,
		User:           req.Msg.User,
		Metadata:       protoStructToMap(req.Msg.Metadata),
	}

	// Apply defaults
	if gwReq.Model == "" {
		gwReq.Model = "dall-e-2"
	}
	if gwReq.N == 0 {
		gwReq.N = 1
	}
	if gwReq.ResponseFormat == "" {
		gwReq.ResponseFormat = "url"
	}
	if gwReq.Size == "" {
		gwReq.Size = "1024x1024"
	}

	// Call provider
	resp, err := provider.CreateImageVariation(ctx, gwReq)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("gateway: image variation failed")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Convert to proto response
	protoResp := &gatewaypb.ImageVariationResponse{
		Created: resp.Created,
		Model:   resp.Model,
	}

	// Convert image data
	for _, img := range resp.Data {
		protoResp.Data = append(protoResp.Data, &gatewaypb.ImageData{
			B64Json:       img.B64JSON,
			Url:           img.URL,
			RevisedPrompt: img.RevisedPrompt,
		})
	}

	return connect.NewResponse(protoResp), nil
}
