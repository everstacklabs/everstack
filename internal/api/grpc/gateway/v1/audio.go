package v1

import (
	"context"

	"connectrpc.com/connect"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// Speech implements text-to-speech generation.
func (s *Server) Speech(ctx context.Context, req *connect.Request[gatewaypb.SpeechRequest]) (*connect.Response[gatewaypb.SpeechResponse], error) {
	logger.Debug("gateway: processing speech request")

	// Ensure providers are loaded for this tenant
	if err := s.EnsureProvidersForRequest(ctx); err != nil {
		logger.WithFields("error", err.Error()).Warn("gateway: failed to ensure providers for speech request")
	}
	bundle := s.providersFor(ctx)
	if bundle == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gw.ErrNotImplemented("speech: no providers configured for this tenant"))
	}

	// Route to the correct audio provider based on the requested model.
	// 1. Try model-based routing via the standard chat router.
	// 2. Try finding an audio-capable provider that supports the model.
	// 3. Fall back to any audio-capable provider.
	var provider gw.AudioProvider
	var providerName string
	if req.Msg.Model != "" && bundle.router != nil {
		if p, route, err := bundle.router.ResolveWithContext(ctx, req.Msg.Model); err == nil {
			if ap, ok := gw.UnwrapAudioProvider(p); ok {
				provider = ap
				providerName = route.ProviderName
			}
		}
	}
	if provider == nil && req.Msg.Model != "" {
		var ok bool
		provider, providerName, ok = bundle.reg.FindAudioProviderForModel(req.Msg.Model)
		if ok {
			logger.WithFields("provider", providerName, "model", req.Msg.Model).Debug("gateway: matched audio provider by model support")
		}
	}
	if provider == nil {
		var ok bool
		provider, providerName, ok = bundle.reg.FindAudioProvider()
		if !ok {
			return nil, connect.NewError(connect.CodeUnimplemented, gw.ErrNotImplemented("speech: no audio provider available"))
		}
	}

	logger.WithFields("provider", providerName, "model", req.Msg.Model).Debug("gateway: routing speech request")

	// Convert proto to gateway request
	gwReq := gw.SpeechRequest{
		Model:               req.Msg.Model,
		Input:               req.Msg.Input,
		Voice:               req.Msg.Voice,
		ResponseFormat:      req.Msg.ResponseFormat,
		Speed:               float64(req.Msg.Speed),
		Metadata:            protoStructToMap(req.Msg.Metadata),
		ReferenceAudio:      req.Msg.ReferenceAudio,
		ReferenceText:       req.Msg.ReferenceText,
		VoiceCloneProfileID: req.Msg.VoiceCloneProfileId,
	}

	// Apply defaults
	if gwReq.ResponseFormat == "" {
		gwReq.ResponseFormat = "mp3"
	}
	if gwReq.Speed == 0 {
		gwReq.Speed = 1.0
	}

	// Resolve voice clone profile ID → actual provider voice ID.
	// The profile stores the DashScope-returned voice ID; we inject it so
	// the provider receives the real voice name instead of our app UUID.
	if gwReq.VoiceCloneProfileID != "" && s.voiceCloneRepo != nil {
		// Tenant-scope the lookup. Without this a TTS request bearing a
		// foreign profile id would resolve and synthesize using
		// another tenant's enrolled voice.
		profileTenant := contextkeys.GetTenantID(ctx)
		profile, err := s.voiceCloneRepo.GetByID(ctx, gwReq.VoiceCloneProfileID, profileTenant)
		if err != nil {
			logger.WithFields("error", err.Error(), "profile_id", gwReq.VoiceCloneProfileID).
				Warn("gateway: failed to resolve voice clone profile")
		} else if profile != nil && profile.ProviderVoiceID != "" {
			logger.WithFields("profile_id", gwReq.VoiceCloneProfileID, "provider_voice_id", profile.ProviderVoiceID).
				Debug("gateway: resolved voice clone profile to provider voice ID")
			gwReq.VoiceCloneProfileID = profile.ProviderVoiceID
		}
	}

	// Call provider
	resp, err := provider.Speech(ctx, gwReq)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("gateway: speech generation failed")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Convert to proto response
	return connect.NewResponse(&gatewaypb.SpeechResponse{
		Audio:           resp.Audio,
		Format:          resp.Format,
		ContentType:     resp.ContentType,
		DurationSeconds: float32(resp.DurationSeconds),
		InputCharacters: int32(resp.InputCharacters),
	}), nil
}

// Transcription implements speech-to-text transcription.
func (s *Server) Transcription(ctx context.Context, req *connect.Request[gatewaypb.TranscriptionRequest]) (*connect.Response[gatewaypb.TranscriptionResponse], error) {
	logger.Debug("gateway: processing transcription request")

	if err := s.EnsureProvidersForRequest(ctx); err != nil {
		logger.WithFields("error", err.Error()).Warn("gateway: failed to ensure providers for transcription request")
	}
	bundle := s.providersFor(ctx)
	if bundle == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gw.ErrNotImplemented("transcription: no providers configured for this tenant"))
	}

	// Route to the correct audio provider based on the requested model
	var provider gw.AudioProvider
	var providerName string
	if req.Msg.Model != "" && bundle.router != nil {
		if p, route, err := bundle.router.ResolveWithContext(ctx, req.Msg.Model); err == nil {
			if ap, ok := gw.UnwrapAudioProvider(p); ok {
				provider = ap
				providerName = route.ProviderName
			}
		}
	}
	if provider == nil {
		var ok bool
		provider, providerName, ok = bundle.reg.FindAudioProvider()
		if !ok {
			return nil, connect.NewError(connect.CodeUnimplemented, gw.ErrNotImplemented("transcription: no audio provider available"))
		}
	}

	logger.WithFields("provider", providerName, "model", req.Msg.Model).Debug("gateway: routing transcription request")

	// Convert proto to gateway request
	gwReq := gw.TranscriptionRequest{
		File:                   req.Msg.File,
		Model:                  req.Msg.Model,
		Language:               req.Msg.Language,
		Prompt:                 req.Msg.Prompt,
		ResponseFormat:         req.Msg.ResponseFormat,
		Temperature:            float64(req.Msg.Temperature),
		TimestampGranularities: req.Msg.TimestampGranularities,
		Filename:               req.Msg.Filename,
		Metadata:               protoStructToMap(req.Msg.Metadata),
	}

	// Apply defaults
	if gwReq.ResponseFormat == "" {
		gwReq.ResponseFormat = "json"
	}

	// Call provider
	resp, err := provider.Transcribe(ctx, gwReq)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("gateway: transcription failed")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Convert to proto response
	protoResp := &gatewaypb.TranscriptionResponse{
		Text:     resp.Text,
		Task:     resp.Task,
		Language: resp.Language,
		Duration: float32(resp.Duration),
	}

	// Convert words
	for _, w := range resp.Words {
		protoResp.Words = append(protoResp.Words, &gatewaypb.TranscriptionWord{
			Word:  w.Word,
			Start: float32(w.Start),
			End:   float32(w.End),
		})
	}

	// Convert segments
	for _, seg := range resp.Segments {
		tokens := make([]int32, len(seg.Tokens))
		for i, t := range seg.Tokens {
			tokens[i] = int32(t)
		}
		protoResp.Segments = append(protoResp.Segments, &gatewaypb.TranscriptionSegment{
			Id:               int32(seg.ID),
			Seek:             int32(seg.Seek),
			Start:            float32(seg.Start),
			End:              float32(seg.End),
			Text:             seg.Text,
			Tokens:           tokens,
			Temperature:      float32(seg.Temperature),
			AvgLogprob:       float32(seg.AvgLogprob),
			CompressionRatio: float32(seg.CompressionRatio),
			NoSpeechProb:     float32(seg.NoSpeechProb),
		})
	}

	return connect.NewResponse(protoResp), nil
}

// Translation implements audio translation to English.
func (s *Server) Translation(ctx context.Context, req *connect.Request[gatewaypb.TranslationRequest]) (*connect.Response[gatewaypb.TranslationResponse], error) {
	logger.Debug("gateway: processing translation request")

	if err := s.EnsureProvidersForRequest(ctx); err != nil {
		logger.WithFields("error", err.Error()).Warn("gateway: failed to ensure providers for translation request")
	}
	bundle := s.providersFor(ctx)
	if bundle == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, gw.ErrNotImplemented("translation: no providers configured for this tenant"))
	}

	// Route to the correct audio provider based on the requested model
	var provider gw.AudioProvider
	var providerName string
	if req.Msg.Model != "" && bundle.router != nil {
		if p, route, err := bundle.router.ResolveWithContext(ctx, req.Msg.Model); err == nil {
			if ap, ok := gw.UnwrapAudioProvider(p); ok {
				provider = ap
				providerName = route.ProviderName
			}
		}
	}
	if provider == nil {
		var ok bool
		provider, providerName, ok = bundle.reg.FindAudioProvider()
		if !ok {
			return nil, connect.NewError(connect.CodeUnimplemented, gw.ErrNotImplemented("translation: no audio provider available"))
		}
	}

	logger.WithFields("provider", providerName, "model", req.Msg.Model).Debug("gateway: routing translation request")

	// Convert proto to gateway request
	gwReq := gw.TranslationRequest{
		File:           req.Msg.File,
		Model:          req.Msg.Model,
		Prompt:         req.Msg.Prompt,
		ResponseFormat: req.Msg.ResponseFormat,
		Temperature:    float64(req.Msg.Temperature),
		Filename:       req.Msg.Filename,
		Metadata:       protoStructToMap(req.Msg.Metadata),
	}

	// Apply defaults
	if gwReq.ResponseFormat == "" {
		gwReq.ResponseFormat = "json"
	}

	// Call provider
	resp, err := provider.Translate(ctx, gwReq)
	if err != nil {
		logger.WithFields("error", err.Error()).Error("gateway: translation failed")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Convert to proto response
	protoResp := &gatewaypb.TranslationResponse{
		Text:     resp.Text,
		Task:     resp.Task,
		Language: resp.Language,
		Duration: float32(resp.Duration),
	}

	// Convert segments
	for _, seg := range resp.Segments {
		tokens := make([]int32, len(seg.Tokens))
		for i, t := range seg.Tokens {
			tokens[i] = int32(t)
		}
		protoResp.Segments = append(protoResp.Segments, &gatewaypb.TranscriptionSegment{
			Id:               int32(seg.ID),
			Seek:             int32(seg.Seek),
			Start:            float32(seg.Start),
			End:              float32(seg.End),
			Text:             seg.Text,
			Tokens:           tokens,
			Temperature:      float32(seg.Temperature),
			AvgLogprob:       float32(seg.AvgLogprob),
			CompressionRatio: float32(seg.CompressionRatio),
			NoSpeechProb:     float32(seg.NoSpeechProb),
		})
	}

	return connect.NewResponse(protoResp), nil
}

// protoStructToMap converts a protobuf Struct to a map.
func protoStructToMap(s *structpb.Struct) map[string]interface{} {
	if s == nil {
		return nil
	}
	return s.AsMap()
}
