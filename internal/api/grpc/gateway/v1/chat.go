package v1

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/activity"
	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/commands/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"
)

// ChatCompletion implements the streaming Connect signature.
// It auto-picks model and sampling defaults from gateway config; request values are ignored.
func (s *Server) ChatCompletion(ctx context.Context, req *connect.Request[gatewaypb.ChatCompletionRequest], stream *connect.ServerStream[gatewaypb.ChatCompletionResponse]) (retErr error) {
	start := time.Now()
	activity.TriggerGatewayRequest(ctx, "GatewayService", "ChatCompletion")
	defer func() {
		status := 200
		if retErr != nil {
			status = 500
		}
		activity.TriggerGatewayResponse(ctx, "GatewayService", "ChatCompletion", status, time.Since(start))

		// Emit a guaranteed terminal event for ListLogs so PENDING entries
		// always resolve, even when the downstream provider hung mid-stream
		// or never emitted provider.response.received. command.completed
		// uses CategorySystem and is intentionally not forwarded to OTEL,
		// so it can't be used as the terminal signal. We pick the event
		// name based on outcome so mapEventToStatus resolves status
		// correctly without depending on event ordering vs an upstream
		// provider.error row.
		event := logger.EventGatewayRequestCompleted
		if retErr != nil {
			event = logger.EventGatewayError
		}
		corrID := correlation.GetCorrelationID(ctx)
		entry := logger.WithCategory(logger.CategoryOperational).
			WithLogEvent(event).
			SetFields(
				"correlation_id", corrID,
				"command_type", "ChatCompletion",
				"procedure", "ChatCompletion",
				"status_code", status,
				"duration_ms", time.Since(start).Milliseconds(),
			)
		t := database.TenantSchemaFromContext(ctx)
		if t == "" {
			t = contextkeys.GetTenantID(ctx)
		}
		if t != "" {
			entry = entry.SetFields("tenant_id", t)
		}
		if retErr != nil {
			entry.WithError(retErr).Warn("ChatCompletion failed")
		} else {
			entry.Info("ChatCompletion completed")
		}
	}()

	// Add correlation ID to response headers if present in context
	correlationID := correlation.GetCorrelationID(ctx)

	// Extract user input from the LATEST user message (the turn that
	// triggered this request). The chat client sends the full transcript
	// every turn, so req.Msg.Messages contains every prior user message;
	// the right "input" for the trace/log is the most recent one. Walking
	// from the front (the previous behaviour) recorded the oldest message
	// in history — which is what made the Logs UI show "who is Trump"
	// for a request whose actual latest user turn was "hi".
	userInput := ""
	if req.Msg != nil && len(req.Msg.Messages) > 0 {
		for i := len(req.Msg.Messages) - 1; i >= 0; i-- {
			msg := req.Msg.Messages[i]
			if msg.Role != gatewaypb.Role_ROLE_USER || len(msg.Content) == 0 {
				continue
			}
			var textParts []string
			for _, content := range msg.Content {
				if content.Type == "text" {
					if textContent := content.GetText(); textContent != "" {
						textParts = append(textParts, textContent)
					}
				}
			}
			if len(textParts) > 0 {
				userInput = strings.Join(textParts, " ")
				break
			}
		}
	}

	// Build structured payload for gateway request received
	payload := logger.NewPayload().
		WithRequest(correlationID, "ChatCompletion", "gRPC", 0).
		WithCorrelation(correlationID)

	// Add user input to request section if available
	if userInput != "" {
		payload = payload.WithRequestInput(userInput)
	}

	reqPayload := payload.Build()

	gwLogEntry := logger.WithCategory(logger.CategoryOperational).
		WithLogEvent(logger.EventGatewayRequestReceived).
		WithPayload(reqPayload).
		SetFields(
			"correlation_id", correlationID,
			"command_type", "ChatCompletion",
			"procedure", "ChatCompletion",
			"has_correlation_id", correlationID != "",
		)
	// Fall back to contextkeys.GetTenantID when TenantSchema isn't set —
	// see internal/providers/logging/middleware.go tenantField for the
	// same fallback rationale. Without this, logs from API-key /
	// cookie-auth paths (which historically only set the contextkeys
	// flavor) emitted without tenant_id and never appeared in the UI.
	tid := database.TenantSchemaFromContext(ctx)
	if tid == "" {
		tid = contextkeys.GetTenantID(ctx)
	}
	if tid != "" {
		gwLogEntry = gwLogEntry.SetFields("tenant_id", tid)
	}
	gwLogEntry.Info("ChatCompletion correlation ID check")

	if correlationID != "" {
		stream.ResponseHeader().Set(correlation.CorrelationIDHeader, correlationID)
		logger.WithFields(
			"correlation_id", correlationID,
			"procedure", "ChatCompletion",
		).Trace("Added correlation ID to Connect response headers")
	} else {
		logger.Warn("No correlation ID found in context for ChatCompletion")
	}

	// Seed sticky key from incoming headers per gateway.yaml load_balancer.key_source
	ctx = s.withKeySourceFromHeaders(ctx, req.Header())

	// Get resolved model for CQRS (use omitted model selection if needed)
	resolvedModel := req.Msg.Model
	if resolvedModel == "" {
		resolvedModel = s.selectOmittedModel(ctx)
		if resolvedModel == "" {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("no default model configured - please specify a model in the request or configure a default provider"))
		}
	}

	// NOTE: ************** MOCK PATH **************
	// Baseline mock path: when X-MF-Mock: true is present, short-circuit provider call
	if strings.EqualFold(req.Header().Get("X-MF-Mock"), "true") {
		logger.WithFields(
			"correlation_id", correlationID,
			"procedure", "ChatCompletion",
			"mock", true,
		).Info("Connect mock short-circuit")
		resp := &gatewaypb.ChatCompletionResponse{
			Id:      correlationID,
			Created: time.Now().Unix(),
			Model:   resolvedModel,
			Choices: []*gatewaypb.Choice{
				{Index: 0, Message: &gatewaypb.Message{Role: gatewaypb.Role_ROLE_ASSISTANT, Content: []*gatewaypb.ContentPart{{Type: "text", Data: &gatewaypb.ContentPart_Text{Text: "mock response"}}}}, FinishReason: "stop"},
			},
		}
		return stream.Send(resp)
	}

	// NOTE: CQRS command dispatch happens INSIDE processChatCompletion
	// after model validation succeeds, to avoid creating session.started
	// events for requests that fail early (e.g., model not found)

	// Wrap send callback to add fallback headers if present
	retErr = processChatCompletion(ctx, s, req.Msg, func(r *gatewaypb.ChatCompletionResponse) error {
		// Add fallback headers if fallback was used
		if r.FallbackInfo != nil && r.FallbackInfo.FallbackUsed {
			// Emit canonical x-evs-* and legacy x-mf-* response headers so
			// consumers reading either name keep working during the migration.
			attempts := fmt.Sprintf("%d", r.FallbackInfo.FallbackAttempts)
			rh := stream.ResponseHeader()
			rh.Set(common.EverstackFallbackUsed, "true")
			rh.Set(common.LegacyMFFallbackUsed, "true")
			rh.Set(common.EverstackRequestedModel, r.FallbackInfo.RequestedModel)
			rh.Set(common.LegacyMFRequestedModel, r.FallbackInfo.RequestedModel)
			rh.Set(common.EverstackActualModel, r.FallbackInfo.ActualModel)
			rh.Set(common.LegacyMFActualModel, r.FallbackInfo.ActualModel)
			rh.Set(common.EverstackFallbackReason, r.FallbackInfo.FallbackReason)
			rh.Set(common.LegacyMFFallbackReason, r.FallbackInfo.FallbackReason)
			rh.Set(common.EverstackFallbackAttempt, attempts)
			rh.Set(common.LegacyMFFallbackAttempt, attempts)
			logger.WithFields(
				"requested_model", r.FallbackInfo.RequestedModel,
				"actual_model", r.FallbackInfo.ActualModel,
				"fallback_reason", r.FallbackInfo.FallbackReason,
			).Warn("Response served via fallback routing")
		}
		return stream.Send(r)
	})
	return retErr
}

// Classic gRPC ChatCompletion
func (g *GrpcServer) ChatCompletion(req *gatewaypb.ChatCompletionRequest, srv gatewaypb.GatewayService_ChatCompletionServer) error {
	// Prepare metadata for headers (correlation ID + fallback info)
	headerMD := metadata.New(map[string]string{})

	// Add correlation ID to response headers if present in context
	if correlationID := correlation.GetCorrelationID(srv.Context()); correlationID != "" {
		headerMD.Set(correlation.CorrelationIDHeader, correlationID)
	}

	// Set initial headers (will be updated if fallback occurs)
	if len(headerMD) > 0 {
		srv.SetHeader(headerMD)
	}

	// MOCK PATH for grpc-gateway calls: respect the mock header forwarded into
	// metadata (canonical x-evs-mock, falling back to legacy x-mf-mock).
	if md, ok := metadata.FromIncomingContext(srv.Context()); ok {
		vals := md.Get("x-evs-mock")
		if len(vals) == 0 {
			vals = md.Get("x-mf-mock")
		}
		if len(vals) > 0 && strings.EqualFold(vals[0], "true") {
			cid := correlation.GetCorrelationID(srv.Context())
			logger.WithFields(
				"correlation_id", cid,
				"procedure", "ChatCompletion",
				"mock", true,
			).Info("gRPC mock short-circuit")
			model := req.GetModel()
			if model == "" {
				model = g.base.selectOmittedModel(srv.Context())
				if model == "" {
					return fmt.Errorf("no default model configured - please specify a model in the request or configure a default provider")
				}
			}
			resp := &gatewaypb.ChatCompletionResponse{
				Id:      cid,
				Created: time.Now().Unix(),
				Model:   model,
				Choices: []*gatewaypb.Choice{
					{Index: 0, Message: &gatewaypb.Message{Role: gatewaypb.Role_ROLE_ASSISTANT, Content: []*gatewaypb.ContentPart{{Type: "text", Data: &gatewaypb.ContentPart_Text{Text: "mock response"}}}}, FinishReason: "stop"},
				},
			}
			return srv.Send(resp)
		}
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

		// NOTE: CQRS command dispatch happens INSIDE processChatCompletion
		// after model validation succeeds, to avoid creating session.started
		// events for requests that fail early (e.g., model not found)

		// Wrap send callback to add fallback headers if present
		return processChatCompletion(ctx, g.base, req, func(r *gatewaypb.ChatCompletionResponse) error {
			// Add fallback headers if fallback was used
			if r.FallbackInfo != nil && r.FallbackInfo.FallbackUsed {
				fallbackMD := metadata.New(map[string]string{
					// canonical x-evs-* and legacy x-mf-* (emit both during migration)
					common.EverstackFallbackUsed:    "true",
					common.EverstackRequestedModel:  r.FallbackInfo.RequestedModel,
					common.EverstackActualModel:     r.FallbackInfo.ActualModel,
					common.EverstackFallbackReason:  r.FallbackInfo.FallbackReason,
					common.EverstackFallbackAttempt: fmt.Sprintf("%d", r.FallbackInfo.FallbackAttempts),
					"x-mf-fallback-used":            "true",
					"x-mf-requested-model":          r.FallbackInfo.RequestedModel,
					"x-mf-actual-model":             r.FallbackInfo.ActualModel,
					"x-mf-fallback-reason":          r.FallbackInfo.FallbackReason,
					"x-mf-fallback-attempts":        fmt.Sprintf("%d", r.FallbackInfo.FallbackAttempts),
				})
				srv.SetHeader(fallbackMD)
				logger.WithFields(
					"requested_model", r.FallbackInfo.RequestedModel,
					"actual_model", r.FallbackInfo.ActualModel,
					"fallback_reason", r.FallbackInfo.FallbackReason,
				).Warn("Response served via fallback routing (gRPC)")
			}
			return srv.Send(r)
		})
	}

	// NOTE: CQRS command dispatch happens INSIDE processChatCompletion
	// after model validation succeeds, to avoid creating session.started
	// events for requests that fail early (e.g., model not found)

	// Wrap send callback to add fallback headers if present
	return processChatCompletion(srv.Context(), g.base, req, func(r *gatewaypb.ChatCompletionResponse) error {
		// Add fallback headers if fallback was used
		if r.FallbackInfo != nil && r.FallbackInfo.FallbackUsed {
			fallbackMD := metadata.New(map[string]string{
				// canonical x-evs-* and legacy x-mf-* (emit both during migration)
				common.EverstackFallbackUsed:    "true",
				common.EverstackRequestedModel:  r.FallbackInfo.RequestedModel,
				common.EverstackActualModel:     r.FallbackInfo.ActualModel,
				common.EverstackFallbackReason:  r.FallbackInfo.FallbackReason,
				common.EverstackFallbackAttempt: fmt.Sprintf("%d", r.FallbackInfo.FallbackAttempts),
				"x-mf-fallback-used":            "true",
				"x-mf-requested-model":          r.FallbackInfo.RequestedModel,
				"x-mf-actual-model":             r.FallbackInfo.ActualModel,
				"x-mf-fallback-reason":          r.FallbackInfo.FallbackReason,
				"x-mf-fallback-attempts":        fmt.Sprintf("%d", r.FallbackInfo.FallbackAttempts),
			})
			srv.SetHeader(fallbackMD)
			logger.WithFields(
				"requested_model", r.FallbackInfo.RequestedModel,
				"actual_model", r.FallbackInfo.ActualModel,
				"fallback_reason", r.FallbackInfo.FallbackReason,
			).Warn("Response served via fallback routing (gRPC)")
		}
		return srv.Send(r)
	})
}

// executeChatCommandWithModel creates and dispatches a CQRS command for chat completion.
func (s *Server) executeChatCommandWithModel(ctx context.Context, req *gatewaypb.ChatCompletionRequest, resolvedModel, resolvedProvider, correlationID string) error {
	// Get CQRS system from server context first, then try request context
	var cqrsSystem *cqrs.System
	var err error

	if s.ctx != nil {
		cqrsSystem, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil || cqrsSystem == nil {
		cqrsSystem, err = cqrs.GetSystemFromContext(ctx)
	}
	if err != nil || cqrsSystem == nil {
		// CQRS is optional, so just log and continue
		logger.WithFields("correlation_id", correlationID).Debug("CQRS system not available in context")
		return nil
	}

	// Extract user information from headers or context
	userID := s.extractUserID(ctx)
	apiKey := s.extractAPIKey(ctx)

	// Convert protobuf messages to command format
	var messages []gateway.ChatMessage
	for _, msg := range req.Messages {
		var content []gateway.MessageContent
		for _, c := range msg.Content {
			// Map protobuf fields to command fields based on oneof structure
			var text, url string

			// Handle the oneof data field
			switch data := c.Data.(type) {
			case *gatewaypb.ContentPart_Text:
				text = data.Text
			case *gatewaypb.ContentPart_ImageUrl:
				url = data.ImageUrl
			}

			content = append(content, gateway.MessageContent{
				Type: c.Type,
				Text: text,
				URL:  url,
			})
		}

		messages = append(messages, gateway.ChatMessage{
			Role:    msg.Role.String(),
			Content: content,
			Name:    "", // Name field might not exist in protobuf
		})
	}

	// Handle stream field (might be a pointer)
	stream := false
	if req.Stream != nil {
		stream = *req.Stream
	}

	// Create and dispatch the command with the provider resolved on the request
	// path. This handler runs in a background goroutine whose context carries no
	// tenant, so re-resolving here via getProviderForModel would fall back to ""
	// and the session/log provider would be blank. Only re-resolve as a fallback.
	provider := resolvedProvider
	if provider == "" {
		provider = s.getProviderForModel(ctx, resolvedModel)
	}

	chatCmd := gateway.NewChatCompletionCommand(
		resolvedModel,
		provider,
		messages,
		stream,
		userID,
		apiKey,
		correlationID,
	)

	return cqrsSystem.CommandBus.Dispatch(ctx, chatCmd)
}

// getProviderForModel returns the provider name for a given model using the
// caller's tenant router. Takes ctx so multi-tenant deployments resolve the
// right router; in single-tenant mode the default bundle is used.
func (s *Server) getProviderForModel(ctx context.Context, model string) string {
	router := s.routerFor(ctx)
	if router == nil {
		// Return empty, not an "unknown" sentinel: a literal "unknown" is a
		// non-empty value that pollutes the trace provider aggregation
		// (anyIf(provider != '')) and surfaces as "Unknown" in the UI.
		return ""
	}
	_, route, err := router.Resolve(model)
	if err != nil {
		return ""
	}
	return route.ProviderName
}

// extractUserID extracts user ID from request context or headers.
// Priority: x-user-id header > x-everstack-user-id > JWT claims > API key hash fallback
func (s *Server) extractUserID(ctx context.Context) string {
	apiKeyHash := s.extractAPIKeyHash(ctx)
	return contextkeys.ExtractUserID(ctx, apiKeyHash)
}

// extractSessionID returns a caller-supplied observability session id, from the
// x-session-id / x-mf-session-id headers or a `session_id` field in per-call
// request metadata. Returns "" when the caller supplied none, so the gateway
// can fall back to the correlation id. This lets API callers group related
// gateway traces under one session the same way agents/workflows do.
func (s *Server) extractSessionID(ctx context.Context, meta *structpb.Struct) string {
	if sid := contextkeys.ExtractSessionID(ctx); sid != "" {
		return sid
	}
	if meta != nil {
		if f, ok := meta.GetFields()["session_id"]; ok {
			if sv := f.GetStringValue(); sv != "" {
				return strings.TrimSpace(sv)
			}
		}
	}
	return ""
}

// extractThreadID returns a caller-supplied conversational thread id, from the
// x-thread-id / x-mf-thread-id headers or a `thread_id` field in per-call
// request metadata. Returns "" when none was supplied. A thread groups a
// multi-turn conversation independently of the session grouping.
func (s *Server) extractThreadID(ctx context.Context, meta *structpb.Struct) string {
	if tid := contextkeys.ExtractThreadID(ctx); tid != "" {
		return tid
	}
	if meta != nil {
		if f, ok := meta.GetFields()["thread_id"]; ok {
			if sv := f.GetStringValue(); sv != "" {
				return strings.TrimSpace(sv)
			}
		}
	}
	return ""
}

// extractAPIKeyHash extracts and hashes the API key from context or headers.
func (s *Server) extractAPIKeyHash(ctx context.Context) string {
	// First check if already stored in context
	if hash := contextkeys.GetAPIKeyHash(ctx); hash != "" {
		return hash
	}

	// Extract from context using the utility
	return contextkeys.ExtractAPIKeyHash(ctx)
}

// extractTenantID extracts tenant/organization ID from context or headers.
func (s *Server) extractTenantID(ctx context.Context) string {
	return contextkeys.ExtractTenantID(ctx)
}

// extractAPIKey extracts the raw API key from context or headers.
// Deprecated: Use extractAPIKeyHash() instead for security.
// This method is kept for backwards compatibility where the raw key is needed.
func (s *Server) extractAPIKey(ctx context.Context) string {
	// Try to get API key from gRPC metadata headers
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		apiKeyHeaders := []string{common.EverstackAPIKey}
		for _, header := range apiKeyHeaders {
			if values := md.Get(header); len(values) > 0 && values[0] != "" {
				return values[0]
			}
		}
	}
	return ""
}
