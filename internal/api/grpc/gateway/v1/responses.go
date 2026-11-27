package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/billingmeter"
	"github.com/everstacklabs/everstack/internal/functions/toolloop"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cacheexec"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"github.com/everstacklabs/everstack/internal/telemetry/metrics"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"github.com/jmoiron/sqlx"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// responseStore is an in-memory store for responses, partitioned by the
// owning tenant. Every read and write names the owner, so a response stored
// by one tenant is indistinguishable from a nonexistent one for any other
// tenant: Get/Delete miss and List never returns it.
//
// The store is per-process and not persisted, so there are no pre-existing
// unowned rows to migrate: a deploy restarts the process with an empty map.
// An entry expires responseStoreTTL after it was last written; expired
// entries read as missing and are swept out on a later write.
type responseStore struct {
	mu        sync.RWMutex
	responses map[string]map[string]*storedResponse // owner -> id -> entry
	// now is the store's clock (time.Now outside tests).
	now       func() time.Time
	lastSweep time.Time
}

// storedResponse is one stored response. history is, for a response made by
// the chat-based fallback, the conversation the model was given before that
// response's output (the earlier turns and the request's input, without
// instructions), so a request chained to it with previous_response_id
// replays the question along with the answer. refused, when set, marks a
// response that was too large to keep: only the reason is held, and a chain
// to it fails with that reason instead of silently losing the conversation.
type storedResponse struct {
	resp     *gw.CreateResponseResponse
	history  []gw.Message
	refused  error
	storedAt time.Time
}

// responseStoreTTL is how long a stored response stays retrievable and
// chainable after its last write. OpenAI keeps responses for 30 days; this
// store is in process memory and lost on every deploy anyway, so it keeps
// them for a day: long enough for any tool round trip or conversation in
// progress, short enough that memory tracks recent traffic.
const responseStoreTTL = 24 * time.Hour

// responseSweepInterval bounds how often a write scans for expired entries.
const responseSweepInterval = time.Minute

// Per-response storage caps, checked when a conversation is written. A
// response over a cap is not kept (see storeConversation). Variables so
// tests can lower them.
var (
	// maxStoredToolOutputBytes caps one tool output in the stored
	// conversation or output.
	maxStoredToolOutputBytes = 1 << 20 // 1 MiB
	// maxStoredConversationBytes caps the stored conversation and output of
	// one response, as JSON.
	maxStoredConversationBytes = 8 << 20 // 8 MiB
)

var globalResponseStore = newResponseStore()

func newResponseStore() *responseStore {
	return &responseStore{
		responses: make(map[string]map[string]*storedResponse),
		now:       time.Now,
	}
}

// put writes entry under owner/id, stamping it and sweeping expired
// entries when one is due. Caller holds rs.mu.
func (rs *responseStore) put(owner, id string, entry *storedResponse) {
	now := rs.now()
	if now.Sub(rs.lastSweep) >= responseSweepInterval {
		rs.sweep(now)
	}
	entry.storedAt = now
	bucket, ok := rs.responses[owner]
	if !ok {
		bucket = make(map[string]*storedResponse)
		rs.responses[owner] = bucket
	}
	bucket[id] = entry
}

// sweep deletes every expired entry. Caller holds rs.mu.
func (rs *responseStore) sweep(now time.Time) {
	rs.lastSweep = now
	for owner, bucket := range rs.responses {
		for id, entry := range bucket {
			if rs.expired(entry, now) {
				delete(bucket, id)
			}
		}
		if len(bucket) == 0 {
			delete(rs.responses, owner)
		}
	}
}

func (rs *responseStore) expired(entry *storedResponse, now time.Time) bool {
	return now.Sub(entry.storedAt) >= responseStoreTTL
}

// lookup returns the live entry for owner/id, refused or not. Caller holds
// rs.mu.
func (rs *responseStore) lookup(owner, id string) (*storedResponse, bool) {
	entry, ok := rs.responses[owner][id]
	if !ok || rs.expired(entry, rs.now()) {
		return nil, false
	}
	return entry, true
}

// Set stores resp, keeping the conversation already recorded for id.
func (rs *responseStore) Set(owner, id string, resp *gw.CreateResponseResponse) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	entry := &storedResponse{resp: resp}
	if prev, ok := rs.lookup(owner, id); ok && prev.refused == nil {
		entry.history = prev.history
	}
	rs.put(owner, id, entry)
}

// SetConversation stores resp with the conversation that preceded its
// output.
func (rs *responseStore) SetConversation(owner, id string, resp *gw.CreateResponseResponse, history []gw.Message) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.put(owner, id, &storedResponse{resp: resp, history: history})
}

// Refuse records that response id was not kept, and why. Anything stored
// for it before is dropped.
func (rs *responseStore) Refuse(owner, id string, reason error) {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	rs.put(owner, id, &storedResponse{refused: reason})
}

// Get returns a stored response. A refused response reads as missing.
func (rs *responseStore) Get(owner, id string) (*gw.CreateResponseResponse, bool) {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	entry, ok := rs.lookup(owner, id)
	if !ok || entry.refused != nil {
		return nil, false
	}
	return entry.resp, true
}

// Previous returns what a request chained to id needs: the response, the
// conversation before its output, and, for a refused response, the reason.
func (rs *responseStore) Previous(owner, id string) (resp *gw.CreateResponseResponse, history []gw.Message, refused error, ok bool) {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	entry, ok := rs.lookup(owner, id)
	if !ok {
		return nil, nil, nil, false
	}
	return entry.resp, entry.history, entry.refused, true
}

func (rs *responseStore) Delete(owner, id string) bool {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	entry, ok := rs.lookup(owner, id)
	if !ok {
		return false
	}
	bucket := rs.responses[owner]
	delete(bucket, id)
	if len(bucket) == 0 {
		delete(rs.responses, owner)
	}
	return entry.refused == nil
}

func (rs *responseStore) List(owner string, limit int, after, before string) []*gw.CreateResponseResponse {
	rs.mu.RLock()
	defer rs.mu.RUnlock()

	now := rs.now()
	var results []*gw.CreateResponseResponse
	for _, entry := range rs.responses[owner] {
		if entry.refused != nil || rs.expired(entry, now) {
			continue
		}
		results = append(results, entry.resp)
		if limit > 0 && len(results) >= limit {
			break
		}
	}
	return results
}

// singleTenantResponseOwner is the one bucket a non-shared (self-hosted)
// gateway uses. Such a gateway serves a single tenant, and its callers do not
// carry one uniform tenant id (an API key can carry the instance id while the
// operator session carries the organization id), so partitioning there would
// only hide a tenant's own responses from itself.
const singleTenantResponseOwner = ""

// responseOwnerCache memoizes tenant id -> owning organization id on the
// shared gateway. The mapping (an organization is its own owner; a managed
// instance belongs to exactly one organization) does not change, so only
// successful resolutions are cached.
var responseOwnerCache sync.Map

// responseOwner returns the owner bucket for this request. On a non-shared
// gateway it is always the single-tenant bucket. On the shared gateway it is
// the caller's organization: API-key callers can carry a managed instance id
// or an organization id, session callers the organization id, so both are
// resolved to the owning organization so the same tenant reads what it wrote.
// ok is false when the shared gateway cannot attribute the request to an
// authenticated tenant principal (API key or user session); such a request
// must neither store nor see any response.
func (s *Server) responseOwner(ctx context.Context) (owner string, ok bool) {
	if !s.isSharedMode() {
		return singleTenantResponseOwner, true
	}
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		tenantID = tenantKeyFromContext(ctx)
	}
	if tenantID == "" || !contextkeys.HasVerifiedTenantPrincipal(ctx) {
		return "", false
	}
	return s.canonicalResponseOwner(ctx, tenantID), true
}

// canonicalResponseOwner maps a tenant id (organization or managed instance)
// to its owning organization id. When the mapping cannot be resolved (no
// platform DB, unknown id, lookup error) it falls back to the tenant id
// itself: still that tenant's own identity, so isolation holds, only the
// instance/organization aliasing is lost.
func (s *Server) canonicalResponseOwner(ctx context.Context, tenantID string) string {
	if cached, ok := responseOwnerCache.Load(tenantID); ok {
		return cached.(string)
	}
	// The platform DB (organizations, managed_instances). On the shared
	// gateway PrimaryDB is a per-tenant gateway DB without those tables.
	db, _ := s.ctx.Value(contextkeys.BillingDB).(*sqlx.DB)
	if db == nil {
		return tenantID
	}
	org, found, err := billingmeter.ResolveOrg(ctx, db, tenantID)
	if err != nil {
		logger.WithFields("error", err.Error()).Warn("gateway: responses owner resolution failed; using tenant id")
		return tenantID
	}
	if !found || org == "" {
		return tenantID
	}
	responseOwnerCache.Store(tenantID, org)
	return org
}

// storeResponse records resp under the caller's tenant. It is a no-op when
// the caller has no tenant on the shared gateway.
func (s *Server) storeResponse(ctx context.Context, id string, resp *gw.CreateResponseResponse) {
	if id == "" || resp == nil {
		return
	}
	owner, ok := s.responseOwner(ctx)
	if !ok {
		return
	}
	globalResponseStore.Set(owner, id, resp)
}

// lookupResponse returns the caller's stored response. Another tenant's id
// is reported exactly like a nonexistent one.
func (s *Server) lookupResponse(ctx context.Context, id string) (*gw.CreateResponseResponse, bool) {
	owner, ok := s.responseOwner(ctx)
	if !ok {
		return nil, false
	}
	return globalResponseStore.Get(owner, id)
}

// storeConversation records resp, with the conversation that preceded its
// output, under the caller's tenant (a no-op without one on the shared
// gateway, like storeResponse). A response whose tool outputs or whole
// conversation exceed the storage caps is not kept: the request that made
// it has already been answered, so it is not failed; instead a marker under
// the same owner makes a later chain to it fail with the reason, rather
// than continue with the conversation silently missing.
func (s *Server) storeConversation(ctx context.Context, id string, resp *gw.CreateResponseResponse, history []gw.Message) {
	if id == "" || resp == nil {
		return
	}
	owner, ok := s.responseOwner(ctx)
	if !ok {
		return
	}
	if reason := conversationOverCap(history, resp.Output); reason != "" {
		logger.WithFields("response_id", id, "reason", reason).Warn("gateway: response not stored: over the storage cap")
		globalResponseStore.Refuse(owner, id, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf(
			"previous response %s was not stored: %s; send the conversation as input items instead of previous_response_id", id, reason)))
		return
	}
	globalResponseStore.SetConversation(owner, id, resp, append([]gw.Message(nil), history...))
}

// conversationOverCap reports why a conversation and output are too large
// to store, or "" when they fit.
func conversationOverCap(history []gw.Message, output []gw.ResponseOutputItem) string {
	toolOutputOver := func(n int) string {
		return fmt.Sprintf("a tool output is %d bytes, over the %d-byte limit", n, maxStoredToolOutputBytes)
	}
	for _, m := range history {
		if m.Role != gw.RoleTool {
			continue
		}
		n := 0
		for _, c := range m.Content {
			if c.Text != nil {
				n += len(*c.Text)
			}
		}
		if n > maxStoredToolOutputBytes {
			return toolOutputOver(n)
		}
	}
	for _, item := range output {
		if item.Type == "function_call_output" && len(item.Output) > maxStoredToolOutputBytes {
			return toolOutputOver(len(item.Output))
		}
	}
	h, _ := json.Marshal(history)
	o, _ := json.Marshal(output)
	if n := len(h) + len(o); n > maxStoredConversationBytes {
		return fmt.Sprintf("its conversation is %d bytes, over the %d-byte limit", n, maxStoredConversationBytes)
	}
	return ""
}

// previousResponse returns the caller's response id and the conversation
// before its output, for a request chained to it. A missing, expired or
// foreign id is errPreviousResponseNotFound; a response that was too large
// to store fails with the reason it was not kept.
func (s *Server) previousResponse(ctx context.Context, id string) (*gw.CreateResponseResponse, []gw.Message, error) {
	owner, ok := s.responseOwner(ctx)
	if !ok {
		return nil, nil, errPreviousResponseNotFound()
	}
	resp, history, refused, ok := globalResponseStore.Previous(owner, id)
	if !ok {
		return nil, nil, errPreviousResponseNotFound()
	}
	if refused != nil {
		return nil, nil, refused
	}
	return resp, history, nil
}

// shouldStoreResponse reports whether a response is kept for retrieval and
// chaining: unless the caller sent store:false, as in OpenAI's API.
func shouldStoreResponse(req gw.CreateResponseRequest) bool {
	return !req.StoreConfigured || req.Store
}

// errPreviousResponseNotFound is returned when previous_response_id does not
// name a response the caller owns. A foreign id and a nonexistent id both
// take this path, so the two are indistinguishable to the caller.
func errPreviousResponseNotFound() error {
	return connect.NewError(connect.CodeNotFound, gw.ErrNotImplemented("previous response not found"))
}

// authorizePreviousResponse gates previous_response_id chaining. On the
// shared gateway the upstream provider account can be shared across tenants
// (platform-managed keys), so forwarding a foreign provider response id would
// let the upstream hydrate another tenant's conversation. The id must name a
// response this tenant stored. Single-tenant gateways keep the previous
// behaviour for the native path (the upstream account is theirs, and chaining
// must keep working across a restart of the in-memory store).
func (s *Server) authorizePreviousResponse(ctx context.Context, previousResponseID string) error {
	if previousResponseID == "" || !s.isSharedMode() {
		return nil
	}
	_, _, err := s.previousResponse(ctx, previousResponseID)
	return err
}

// CreateResponse implements the Responses API create endpoint.
func (s *Server) CreateResponse(ctx context.Context, req *connect.Request[gatewaypb.CreateResponseRequest], stream *connect.ServerStream[gatewaypb.CreateResponseResponse]) error {
	logger.Debug("gateway: processing create response request")

	// Convert proto to gateway request
	gwReq := convertProtoToResponseRequest(req.Msg)

	// previous_response_id must name a response this tenant owns; a foreign
	// id fails exactly like a nonexistent one, before any provider sees it.
	if err := s.authorizePreviousResponse(ctx, gwReq.PreviousResponseID); err != nil {
		return err
	}

	if err := s.EnsureProvidersForRequest(ctx); err != nil {
		logger.WithFields("error", err.Error()).Warn("gateway: failed to ensure providers for response request")
	}
	bundle := s.providersFor(ctx)
	if bundle == nil {
		return connect.NewError(connect.CodeFailedPrecondition, gw.ErrNotImplemented("response: no providers configured for this tenant"))
	}
	automaticallyRouted := gwReq.Model == "auto"
	if automaticallyRouted {
		messages := s.convertInputsToMessages(gwReq)
		if gwReq.Instructions != "" {
			text := gwReq.Instructions
			messages = append([]gw.Message{{Role: gw.RoleSystem, Content: []gw.ContentPart{{Type: "text", Text: &text}}}}, messages...)
		}
		hasReferences := gwReq.PreviousResponseID != ""
		for _, input := range gwReq.Input {
			if input.Type != "message" {
				hasReferences = true
			}
		}
		state := decisionChatState(messages, len(gwReq.Tools) > 0 || len(gwReq.BuiltinTools) > 0 || hasReferences)
		var err error
		gwReq.Model, err = s.selectDecisionRoute(ctx, gwReq.Model, state)
		if err != nil {
			return err
		}
	}
	s.requestDefaultsForModel(ctx, gwReq.Model).applyResponse(&gwReq)

	// Find a provider that supports the Responses API
	provider, providerName, ok := bundle.reg.FindResponsesProvider()
	if automaticallyRouted {
		resolved, model, err := resolveDecisionModel(bundle.reg, gwReq.Model)
		if err != nil {
			return connect.NewError(connect.CodeFailedPrecondition, err)
		}
		provider, ok = resolved.(gw.ResponsesProvider)
		providerName = resolved.Name()
		if ok {
			gwReq.Model = model
		}
	}
	if ok {
		logger.WithFields("provider", providerName, "model", req.Msg.Model).Debug("gateway: routing response request to native provider")

		// Stamp the resolved provider on the trace span so Responses-API traces
		// attribute a provider instead of rendering as "N/A".
		if sp := trace.SpanFromContext(ctx); sp != nil {
			sp.SetAttributes(attribute.String("provider", providerName))
		}

		// Use native Responses API if provider supports it
		if gwReq.Stream {
			return s.handleStreamingResponse(ctx, provider, providerName, gwReq, stream)
		}
		return s.handleUnaryResponse(ctx, provider, providerName, gwReq, stream)
	}

	// Fallback: Implement agentic loop using Chat Completions API
	logger.WithFields("model", req.Msg.Model).Debug("gateway: using chat-based agentic loop for response")
	return s.handleAgenticResponse(ctx, gwReq, stream)
}

// handleStreamingResponse handles streaming Responses API using native provider.
func (s *Server) handleStreamingResponse(ctx context.Context, provider gw.ResponsesProvider, providerName string, req gw.CreateResponseRequest, stream *connect.ServerStream[gatewaypb.CreateResponseResponse]) error {
	var streamUsage *gw.ResponseUsage
	var streamModel string
	var streamID string
	var lastSnapshot gw.CreateResponseResponse
	err := provider.CreateResponseStream(responsesAttempt(ctx), req, func(resp gw.CreateResponseResponse) error {
		if resp.ID != "" {
			lastSnapshot = resp
		}
		if resp.Usage != nil {
			streamUsage = resp.Usage
		}
		if resp.Model != "" {
			streamModel = resp.Model
		}
		if resp.ID != "" {
			streamID = resp.ID
		}
		protoResp := convertResponseToProto(&resp)
		if err := stream.Send(protoResp); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return responsesConnectError(err)
	}

	// Record ownership of the streamed response so the tenant can retrieve
	// it and chain from it (previous_response_id is gated on ownership on the
	// shared gateway).
	if streamID != "" && shouldStoreResponse(req) {
		lastSnapshot.ID = streamID
		s.storeResponse(ctx, streamID, &lastSnapshot)
	}

	// Record usage metrics from the final streaming chunk. Use the provider that
	// actually served this Responses request (chosen by capability), not a
	// model->provider re-resolve.
	if streamUsage != nil {
		costDetails := metrics.CalculateCostWithCacheTokens(
			providerName,
			streamModel,
			streamUsage.InputTokens,
			streamUsage.OutputTokens,
			streamUsage.CacheReadCount(),
			streamUsage.CacheWriteCount(),
		)
		s.recordUsageMetrics(
			int64(streamUsage.InputTokens),
			int64(streamUsage.OutputTokens),
			costDetails.EstimatedUSD,
			0, false,
		)
	}
	return nil
}

// handleUnaryResponse handles non-streaming Responses API.
func (s *Server) handleUnaryResponse(ctx context.Context, provider gw.ResponsesProvider, providerName string, req gw.CreateResponseRequest, stream *connect.ServerStream[gatewaypb.CreateResponseResponse]) error {
	resp, cacheOutcome, err := cacheexec.ExecuteResponseWithCacheOutcome(ctx, req,
		func(callCtx context.Context, callReq gw.CreateResponseRequest) (gw.CreateResponseResponse, error) {
			return provider.CreateResponse(responsesAttempt(callCtx), callReq)
		},
	)
	annotateResponsesCacheTrace(ctx, cacheOutcome, req.Model)
	if err != nil {
		return responsesConnectError(err)
	}
	cacheType := "miss"
	if cacheOutcome.Hit {
		cacheType = cacheOutcome.HitType
	}
	logger.WithFields(
		"model", resp.Model,
		"cache_hit", cacheOutcome.Hit,
		"cache_type", cacheType,
		"cache_stored", cacheOutcome.Stored,
		"semantic_stored", cacheOutcome.SemanticStored,
	).Debug("gateway: responses unary cache outcome")

	// Record usage metrics. Use the provider that actually served this Responses
	// request (chosen by capability), not a model->provider re-resolve.
	if resp.Usage != nil {
		costDetails := metrics.CalculateCostWithCacheTokens(
			providerName,
			resp.Model,
			resp.Usage.InputTokens,
			resp.Usage.OutputTokens,
			resp.Usage.CacheReadCount(),
			resp.Usage.CacheWriteCount(),
		)
		estimatedCost := costDetails.EstimatedUSD
		cacheSavings := 0.0
		if cacheOutcome.Hit {
			estimatedCost = 0
			cacheSavings = costDetails.EstimatedUSD
		}
		s.recordUsageMetrics(
			int64(resp.Usage.InputTokens),
			int64(resp.Usage.OutputTokens),
			estimatedCost,
			cacheSavings, cacheOutcome.Hit,
		)
	}

	// Store the response, unless the caller asked not to.
	if shouldStoreResponse(req) {
		s.storeResponse(ctx, resp.ID, &resp)
	}

	// Send the response
	protoResp := convertResponseToProto(&resp)
	return stream.Send(protoResp)
}

// handleAgenticResponse implements the agentic loop using Chat Completions API.
func (s *Server) handleAgenticResponse(ctx context.Context, req gw.CreateResponseRequest, stream *connect.ServerStream[gatewaypb.CreateResponseResponse]) error {
	// Generate a unique response ID
	responseID := generateResponseID()
	createdAt := time.Now().Unix()

	// If previous_response_id is provided, hydrate the previous response's
	// conversation (what it was given, then its output, tool calls and tool
	// outputs included) so the fallback loop continues the same thread; then
	// convert this request's inputs to chat messages after it.
	// A previous response that is not stored (unknown, expired, another
	// tenant's, or made with store:false) cannot be continued: the thread
	// it names is not here to hydrate.
	var messages []gw.Message
	if req.PreviousResponseID != "" {
		prev, history, err := s.previousResponse(ctx, req.PreviousResponseID)
		if err != nil {
			return err
		}
		messages = append(messages, history...)
		messages = appendResponseOutputMessages(messages, prev.Output)
	}
	messages = appendResponseInputMessages(messages, req.Input)

	// save records the response with the conversation so far, so a request
	// chained to it replays that ahead of its output. With store:false
	// nothing is kept.
	history := messages
	persist := shouldStoreResponse(req)

	// Add system instructions if provided
	if req.Instructions != "" {
		systemMsg := gw.Message{
			Role:    gw.RoleSystem,
			Content: []gw.ContentPart{{Type: "text", Text: stringPtr(req.Instructions)}},
		}
		messages = append([]gw.Message{systemMsg}, messages...)
	}

	// Convert builtin tools and custom tools to chat tools
	tools := s.convertToToolDefinitions(req)

	// Prepare chat request
	chatReq := gw.ChatCompletionRequest{
		Model:    req.Model,
		Messages: messages,
		Tools:    tools,
		Sampling: gw.SamplingParams{
			Temperature:           req.Temperature,
			TemperatureConfigured: req.TemperatureConfigured,
			TopP:                  req.TopP,
			TopPConfigured:        req.TopPConfigured,
			MaxTokens:             req.MaxOutputTokens,
			ReasoningEffort:       reasoningEffort(req.Reasoning),
		},
		// Each turn is a unary provider.Chat call, or a provider stream when
		// the caller asked for one (streamAgenticTurn sets Stream itself).
		Stream:     false,
		ToolChoice: req.ToolChoice,
	}

	// Apply configurable conversation truncation for the fallback agentic loop.
	// This prevents unbounded context growth when tools append extra messages.
	maxHistoryMessages := resolveResponseHistoryLimit(req.Truncation)

	// Create initial response
	response := &gw.CreateResponseResponse{
		ID:                 responseID,
		Object:             "response",
		CreatedAt:          createdAt,
		Status:             "in_progress",
		Model:              req.Model,
		Output:             []gw.ResponseOutputItem{},
		Metadata:           req.Metadata,
		Temperature:        req.Temperature,
		TopP:               req.TopP,
		MaxOutputTokens:    req.MaxOutputTokens,
		PreviousResponseID: req.PreviousResponseID,
		Truncation:         req.Truncation,
		Reasoning:          req.Reasoning,
	}

	save := func() {
		if persist {
			s.storeConversation(ctx, responseID, response, history)
		}
	}

	// Send initial in_progress event
	if err := stream.Send(convertResponseToProto(response)); err != nil {
		return err
	}

	// Execute the agentic loop
	maxIterations := 10 // Prevent infinite loops
	for i := 0; i < maxIterations; i++ {
		if maxHistoryMessages > 0 {
			chatReq.Messages = truncateChatHistory(chatReq.Messages, maxHistoryMessages)
		}

		// Call the chat completion. A streamed turn adds its output items to
		// the response as they are generated and sends each change; a unary
		// turn's items are added below once it returns.
		var chatResp gw.ChatCompletionResponse
		var cacheHit bool
		var err error
		// metered is the turn's usage as the metering seam settled it, for
		// the license-side record.
		var metered turnUsage
		if req.Stream {
			chatResp, metered, err = s.streamAgenticTurn(ctx, chatReq, response, responseStreamSender{
				snapshot: func() error { return stream.Send(convertResponseToProto(response)) },
				delta: func(d *gatewaypb.ResponseDelta) error {
					return stream.Send(&gatewaypb.CreateResponseResponse{Id: response.ID, Object: "response", Delta: d})
				},
			})
		} else {
			chatResp, cacheHit, err = s.processChatCompletionInternal(ctx, chatReq)
			metered = turnUsage{model: chatResp.Model, usage: chatResp.Usage}
		}
		// Record usage metrics for this agentic loop iteration. A turn that
		// broke off (client gone, upstream error) was still metered, on its
		// reported usage or its estimate, and is recorded the same way.
		s.recordTurnUsage(ctx, metered, cacheHit)
		if err != nil {
			response.Status = "failed"
			response.Error = &gw.ResponseError{
				Code:    "chat_error",
				Message: err.Error(),
			}
			save()
			return stream.Send(convertResponseToProto(response))
		}

		// Check if we have tool calls to execute
		if len(chatResp.Choices) > 0 {
			choice := chatResp.Choices[0]

			// Add the assistant message to the output (a streamed turn
			// already has). A turn that only calls tools has no message
			// item, as in OpenAI's output.
			if !req.Stream && (len(choice.Message.Content) > 0 || len(choice.Message.ToolCalls) == 0) {
				outputItem := gw.ResponseOutputItem{
					ID:      generateItemID(),
					Type:    "message",
					Status:  "completed",
					Role:    gw.RoleAssistant,
					Content: choice.Message.Content,
				}
				response.Output = append(response.Output, outputItem)
			}

			// Check for tool calls
			if len(choice.Message.ToolCalls) > 0 {
				// Add tool call items to output (a streamed turn already has)
				if !req.Stream {
					for _, tc := range choice.Message.ToolCalls {
						toolCallItem := gw.ResponseOutputItem{
							ID:        tc.ID,
							Type:      "function_call",
							Status:    "completed",
							CallID:    tc.ID,
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						}
						response.Output = append(response.Output, toolCallItem)
					}
				}

				// Execute tool calls using the tool loop manager
				var toolMessages []gw.Message
				if s.toolLoop != nil && s.toolLoop.IsEnabled() {
					var err error
					toolMessages, err = s.toolLoop.ExecuteToolLoop(ctx, &toolloop.ExecutionContext{
						RequestID:     responseID,
						TenantID:      s.GetOrganizationID(),
						CorrelationID: responseID,
					}, &chatResp)

					if err != nil {
						logger.WithFields("error", err.Error()).Warn("gateway: tool execution failed")
					}
				}
				if len(toolMessages) > 0 {
					// ExecuteToolLoop returns messages in correct order:
					// [0] = assistant message with tool_calls
					// [1..n] = tool result messages
					// Add all of them to the chat history
					for _, tm := range toolMessages {
						chatReq.Messages = append(chatReq.Messages, tm)

						// Add tool output to response (only for tool messages, not assistant)
						if tm.Role == gw.RoleTool && tm.ToolCallID != "" {
							for _, content := range tm.Content {
								if content.Text != nil {
									toolOutputItem := gw.ResponseOutputItem{
										ID:     generateItemID(),
										Type:   "function_call_output",
										Status: "completed",
										CallID: tm.ToolCallID,
										Output: *content.Text,
									}
									response.Output = append(response.Output, toolOutputItem)
								}
							}
						}
					}

					// A streaming caller sees the tool outputs now, not
					// after the next turn's first token.
					if req.Stream {
						if err := stream.Send(convertResponseToProto(response)); err != nil {
							return err
						}
					}

					// Continue the loop to get the model's response to the tool results
					continue
				}
				// The tool loop is off, or ran nothing: the caller runs the
				// tools. The response ends here with its function_call items,
				// as OpenAI's does; calling the model again without the tool
				// results would only make it repeat the calls.
			}

			// No tool calls, or tool calls left to the caller: we're done.
			response.Status = "completed"
			response.Usage = responseUsageFromChat(&chatResp.Usage)
			save()
			return stream.Send(convertResponseToProto(response))
		}
	}

	// Max iterations reached
	response.Status = "incomplete"
	response.IncompleteDetails = &gw.IncompleteDetails{
		Reason: "max_iterations_reached",
	}
	save()
	return stream.Send(convertResponseToProto(response))
}

// turnUsage is one turn's usage and the model it is priced for.
type turnUsage struct {
	model string
	usage gw.Usage
}

// recordTurnUsage makes the license-side usage record of one turn of the
// chat-based loop (license monitor, trial tokens, and through the monitor's
// syncer the instance's usage totals). A turn that used nothing is not
// recorded.
func (s *Server) recordTurnUsage(ctx context.Context, t turnUsage, cacheHit bool) {
	u := t.usage
	if u.PromptTokens <= 0 && u.CompletionTokens <= 0 {
		return
	}
	cost := metrics.CalculateCostWithCacheTokens(
		s.getProviderForModel(ctx, t.model),
		t.model,
		u.PromptTokens,
		u.CompletionTokens,
		u.CacheReadCount(),
		u.CacheWriteCount(),
	)
	estimatedCost := cost.EstimatedUSD
	cacheSavings := 0.0
	if cacheHit {
		estimatedCost = 0
		cacheSavings = cost.EstimatedUSD
	}
	s.recordUsageMetrics(int64(u.PromptTokens), int64(u.CompletionTokens), estimatedCost, cacheSavings, cacheHit)
}

// responsesAttempt tags a provider attempt of the Responses path for the
// metering seam, as the chat path does: each one (every tool-loop turn
// included) is a first try, and nothing is configured to take over, since
// this path has no fallback. Without the tag an attempt that fails before
// generating anything (a client gone before the first token) leaves no
// billing.inference_attempts record.
func responsesAttempt(ctx context.Context) context.Context {
	return billingmeter.WithLastAttempt(billingmeter.WithAttemptKind(ctx, billingmeter.KindPrimary), true)
}

// responseStreamSender sends the two kinds of CreateResponse stream frame:
// a full snapshot of the response, and a delta frame carrying only what one
// item grew by.
type responseStreamSender struct {
	snapshot func() error
	delta    func(*gatewaypb.ResponseDelta) error
}

// streamAgenticTurn runs one turn of the chat-based fallback loop as a
// provider stream, for a Responses request with stream:true. The turn's
// output items are added to response.Output as they appear, append-only so
// every item keeps its output index: the assistant message first (where the
// unary loop puts it), then one function_call item per tool call.
//
// What reaches the stream is proportional to what the model generates: a
// chunk that only grows existing text or arguments is sent as delta frames
// (the new fragment plus its item id and indexes), and a full snapshot is
// sent only when the structure changes (a new item or content part, a tool
// name), so a reply of N chunks costs O(N) bytes, not O(N^2). The SSE layer
// turns delta frames into output_text and function_call_arguments delta
// events and keeps the running text for the done events; response.Output
// here keeps the full turn for the final snapshot, the store and the next
// turn.
//
// The provider is resolved and called exactly as a unary turn is (through
// the router, so billing and tracing wrap the call once), but without the
// response cache, as chat streaming skips it. A send error (the client went
// away) is returned from the chunk callback, which ends the provider stream
// and cancels the upstream request.
//
// On success the turn's items are marked completed and the turn is returned
// accumulated as a ChatCompletionResponse, so the rest of the loop (usage,
// tool execution, history) is the same for both paths. On error, items left
// in progress are marked incomplete; an item nobody was sent is dropped.
func (s *Server) streamAgenticTurn(ctx context.Context, chatReq gw.ChatCompletionRequest, response *gw.CreateResponseResponse, send responseStreamSender) (gw.ChatCompletionResponse, turnUsage, error) {
	router := s.routerFor(ctx)
	if router == nil {
		return gw.ChatCompletionResponse{}, turnUsage{}, gw.ErrNotImplemented("responses stream: no providers configured for this tenant")
	}
	provider, route, err := router.ResolveWithContext(ctx, chatReq.Model)
	if err != nil {
		return gw.ChatCompletionResponse{}, turnUsage{}, err
	}
	if route.ModelName != "" {
		chatReq.Model = route.ModelName
	}
	chatReq.Stream = true

	start := len(response.Output)
	msgIdx := start
	response.Output = append(response.Output, gw.ResponseOutputItem{
		ID:     generateItemID(),
		Type:   "message",
		Status: "in_progress",
		Role:   gw.RoleAssistant,
	})
	sent := false
	var acc gw.ChatCompletionResponse
	var toolCalls []gw.ToolCall
	var toolItems []int // output index of each entry in toolCalls
	finishReason := ""

	// The metering seam reports what it settled, which for a stream that
	// broke off before its usage chunk is its estimate. Settling happens
	// before ChatStream returns, on this goroutine.
	var settled *billingmeter.SettledUsage
	attemptCtx := billingmeter.WithSettleObserver(responsesAttempt(ctx), func(u billingmeter.SettledUsage) { settled = &u })
	err = provider.ChatStream(attemptCtx, chatReq, func(chunk gw.ChatResponseChunk) error {
		if chunk.Model != "" {
			acc.Model = chunk.Model
		}
		if chunk.ID != "" {
			acc.ID = chunk.ID
		}
		if chunk.Usage != nil {
			acc.Usage = *chunk.Usage
		}
		// structural: the chunk added an item or part (or renamed a tool),
		// which only a snapshot can announce. Otherwise its growth goes out
		// as deltas.
		structural := false
		var deltas []*gatewaypb.ResponseDelta
		msg := &response.Output[msgIdx]
		for _, delta := range chunk.Choices {
			if delta.FinishReason != "" {
				finishReason = delta.FinishReason
			}
			for _, part := range delta.Delta.Content {
				grown, newPart := appendStreamedPart(msg, part)
				switch {
				case newPart:
					structural = true
				case grown != "":
					deltas = append(deltas, &gatewaypb.ResponseDelta{
						ItemId:       msg.ID,
						OutputIndex:  int32(msgIdx),
						ContentIndex: int32(len(msg.Content) - 1),
						ContentDelta: grown,
					})
				}
			}
			for _, tc := range delta.Delta.ToolCalls {
				i := -1
				if tc.ID != "" {
					for j := range toolCalls {
						if toolCalls[j].ID == tc.ID {
							i = j
							break
						}
					}
					if i < 0 {
						toolCalls = append(toolCalls, gw.ToolCall{ID: tc.ID, Type: tc.Type})
						toolItems = append(toolItems, len(response.Output))
						response.Output = append(response.Output, gw.ResponseOutputItem{
							ID:     tc.ID,
							Type:   "function_call",
							Status: "in_progress",
							CallID: tc.ID,
						})
						i = len(toolCalls) - 1
						structural = true
					}
				} else if len(toolCalls) > 0 {
					// Argument fragments after the first carry no id: they
					// continue the most recent call.
					i = len(toolCalls) - 1
				} else {
					continue
				}
				if tc.Type != "" {
					toolCalls[i].Type = tc.Type
				}
				item := &response.Output[toolItems[i]]
				if tc.Function.Name != "" {
					toolCalls[i].Function.Name += tc.Function.Name
					item.Name = toolCalls[i].Function.Name
					structural = true
				}
				if tc.Function.Arguments != "" {
					toolCalls[i].Function.Arguments += tc.Function.Arguments
					item.Arguments = toolCalls[i].Function.Arguments
					deltas = append(deltas, &gatewaypb.ResponseDelta{
						ItemId:         item.ID,
						OutputIndex:    int32(toolItems[i]),
						ArgumentsDelta: tc.Function.Arguments,
					})
				}
			}
		}
		if structural {
			// The snapshot already holds this chunk's growth.
			sent = true
			return send.snapshot()
		}
		for _, d := range deltas {
			if err := send.delta(d); err != nil {
				return err
			}
		}
		return nil
	})
	if acc.Model == "" {
		acc.Model = chatReq.Model
	}
	// Unmetered providers (self-hosted) settle nothing: their reported
	// usage, if any arrived, is the turn's usage.
	metered := turnUsage{model: acc.Model, usage: acc.Usage}
	if settled != nil {
		metered = turnUsage{model: settled.Model, usage: gw.Usage{
			PromptTokens:     settled.InputTokens,
			CompletionTokens: settled.OutputTokens,
			TotalTokens:      settled.InputTokens + settled.OutputTokens,
		}}
		if settled.CacheReadTokens > 0 || settled.CacheWriteTokens > 0 {
			metered.usage.PromptDetails = &gw.TokenDetails{CacheReadTokens: settled.CacheReadTokens, CacheWriteTokens: settled.CacheWriteTokens}
		}
	}
	if err != nil {
		if !sent {
			response.Output = response.Output[:start]
		} else {
			for i := start; i < len(response.Output); i++ {
				response.Output[i].Status = "incomplete"
			}
		}
		return gw.ChatCompletionResponse{}, metered, err
	}

	for i := start; i < len(response.Output); i++ {
		response.Output[i].Status = "completed"
	}
	msg := gw.Message{Role: gw.RoleAssistant, ToolCalls: toolCalls}
	if content := response.Output[msgIdx].Content; len(content) > 0 {
		msg.Content = append([]gw.ContentPart(nil), content...)
	}
	acc.Choices = []gw.Choice{{Message: msg, FinishReason: finishReason}}
	return acc, metered, nil
}

// appendStreamedPart adds one streamed content part to a message item: text
// extends the trailing text part (one output_text part grows, so its content
// index is stable), anything else (a provider-native thinking block) is
// appended as its own part. It returns the text the trailing part grew by,
// or newPart when the item gained a part (text or not), which a snapshot
// must announce.
func appendStreamedPart(item *gw.ResponseOutputItem, part gw.ContentPart) (grown string, newPart bool) {
	isText := part.Text != nil && part.ProviderJSON == nil && (part.Type == "" || part.Type == "text" || part.Type == "output_text")
	if !isText {
		item.Content = append(item.Content, part)
		return "", true
	}
	if *part.Text == "" {
		return "", false
	}
	if n := len(item.Content); n > 0 {
		last := &item.Content[n-1]
		if last.Type == "text" && last.Text != nil && last.ProviderJSON == nil {
			joined := *last.Text + *part.Text
			last.Text = &joined
			return *part.Text, false
		}
	}
	text := *part.Text
	item.Content = append(item.Content, gw.ContentPart{Type: "text", Text: &text})
	return "", true
}

func reasoningEffort(reasoning *gw.ReasoningConfig) string {
	if reasoning == nil {
		return ""
	}
	return reasoning.Effort
}

func responseUsageFromChat(usage *gw.Usage) *gw.ResponseUsage {
	if usage == nil {
		return nil
	}
	responseUsage := &gw.ResponseUsage{
		InputTokens:  usage.PromptTokens,
		OutputTokens: usage.CompletionTokens,
		TotalTokens:  usage.TotalTokens,
	}
	if details := usage.PromptDetails; details != nil {
		responseUsage.InputTokensDetails = &gw.ResponseUsageDetails{
			CachedTokens:     usage.CacheReadCount(),
			CacheWriteTokens: usage.CacheWriteCount(),
			TextTokens:       details.TextTokens,
			AudioTokens:      details.AudioTokens,
			ImageTokens:      details.ImageTokens,
			ReasoningTokens:  details.ReasoningTokens,
		}
	}
	if details := usage.CompletionDetails; details != nil {
		responseUsage.OutputTokensDetails = &gw.ResponseUsageDetails{
			CachedTokens:     details.CachedTokens,
			CacheWriteTokens: details.CacheWriteTokens,
			TextTokens:       details.TextTokens,
			AudioTokens:      details.AudioTokens,
			ImageTokens:      details.ImageTokens,
			ReasoningTokens:  details.ReasoningTokens,
		}
	}
	return responseUsage
}

// processChatCompletionInternal is a wrapper to call the internal chat completion.
func (s *Server) processChatCompletionInternal(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, bool, error) {
	resp, cacheOutcome, err := cacheexec.ExecuteChatWithCacheOutcome(ctx, req,
		func(callCtx context.Context, callReq gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
			router := s.routerFor(callCtx)
			if router == nil {
				return gw.ChatCompletionResponse{}, gw.ErrNotImplemented("processChatCompletionInternal: no providers configured for this tenant")
			}
			provider, route, resolveErr := router.ResolveWithContext(callCtx, callReq.Model)
			if resolveErr != nil {
				return gw.ChatCompletionResponse{}, resolveErr
			}

			// Update model to actual model if aliased.
			if route.ModelName != "" {
				callReq.Model = route.ModelName
			}

			return provider.Chat(responsesAttempt(callCtx), callReq)
		},
	)
	annotateResponsesCacheTrace(ctx, cacheOutcome, req.Model)
	if err != nil {
		return gw.ChatCompletionResponse{}, false, err
	}
	cacheType := "miss"
	if cacheOutcome.Hit {
		cacheType = cacheOutcome.HitType
	}
	logger.WithFields(
		"model", resp.Model,
		"cache_hit", cacheOutcome.Hit,
		"cache_type", cacheType,
		"cache_stored", cacheOutcome.Stored,
		"semantic_stored", cacheOutcome.SemanticStored,
	).Debug("gateway: responses fallback chat cache outcome")
	return resp, cacheOutcome.Hit, nil
}

func annotateResponsesCacheTrace(ctx context.Context, outcome cacheexec.CacheOutcome, model string) {
	span := trace.SpanFromContext(ctx)
	if span == nil || !span.SpanContext().IsValid() {
		return
	}

	telemetry.AddSpanEvent(span, attrs.EventCacheLookupStart,
		attribute.String(attrs.LLMRequestModel, model),
	)

	cacheType := "none"
	if outcome.Hit {
		cacheType = outcome.HitType
	}
	attrs.SetCacheMetadata(span, outcome.Hit, "")
	attrs.SetCacheLookup(span, cacheType, outcome.Hit, 0, 0)

	if outcome.Hit {
		telemetry.AddSpanEvent(span, attrs.EventCacheLookupHit,
			attribute.String(attrs.CacheType, cacheType),
			attribute.Bool(attrs.CacheHit, true),
		)
	} else {
		telemetry.AddSpanEvent(span, attrs.EventCacheLookupMiss,
			attribute.Bool(attrs.CacheHit, false),
		)
	}

	if outcome.Stored {
		telemetry.AddSpanEvent(span, attrs.EventCacheStoreStart,
			attribute.String(attrs.CacheType, "exact"),
		)
		attrs.SetCacheStore(span, "exact", 0, 0, 0)
		telemetry.AddSpanEvent(span, attrs.EventCacheStoreComplete,
			attribute.String(attrs.CacheType, "exact"),
		)
		if outcome.SemanticStored {
			telemetry.AddSpanEvent(span, attrs.EventCacheStoreStart,
				attribute.String(attrs.CacheType, "semantic"),
			)
			telemetry.AddSpanEvent(span, attrs.EventCacheStoreComplete,
				attribute.String(attrs.CacheType, "semantic"),
			)
		}
	}
}

func resolveResponseHistoryLimit(truncation *gw.TruncationStrategy) int {
	// Default guard for fallback loop.
	limit := 100
	if truncation == nil {
		return limit
	}

	if truncation.Type == "disabled" {
		return 0
	}
	if truncation.LastMessages > 0 {
		return truncation.LastMessages
	}
	return limit
}

// truncateChatHistory keeps the last maxMessages messages (the system
// message, if first, counts as one and is always kept). The cut never splits
// a tool call from its results: when it would land on tool results, the
// rest of that call/result group is dropped too, so the kept history does
// not start with results whose call is gone. Dropping from the front never
// separates a kept call from its later results. If dropping the group would
// leave nothing after the system message, the whole group is kept instead,
// over the cap.
func truncateChatHistory(messages []gw.Message, maxMessages int) []gw.Message {
	if maxMessages <= 0 || len(messages) <= maxMessages {
		return messages
	}

	floor := 0 // first droppable index
	start := len(messages) - maxMessages
	if messages[0].Role == gw.RoleSystem {
		floor = 1
		start++
	}
	cut := start
	for cut < len(messages) && messages[cut].Role == gw.RoleTool {
		cut++
	}
	if cut == len(messages) && start < len(messages) {
		// Nothing but tool results would remain: back up to their call.
		cut = start
		for cut > floor && messages[cut].Role == gw.RoleTool {
			cut--
		}
	}

	if floor == 1 {
		truncated := make([]gw.Message, 0, 1+len(messages)-cut)
		truncated = append(truncated, messages[0])
		truncated = append(truncated, messages[cut:]...)
		return truncated
	}
	return messages[cut:]
}

// appendResponseOutputMessages appends a response's output items to msgs as
// chat messages: message items, function_call items as assistant tool
// calls, and function_call_output items as tool messages, in order.
func appendResponseOutputMessages(msgs []gw.Message, items []gw.ResponseOutputItem) []gw.Message {
	for _, item := range items {
		switch item.Type {
		case "message":
			if len(item.Content) == 0 {
				continue
			}
			role := item.Role
			if role == "" {
				role = gw.RoleAssistant
			}
			msgs = append(msgs, gw.Message{
				Role:    role,
				Content: item.Content,
			})
		case "function_call":
			msgs = appendFunctionCall(msgs, item.CallID, item.Name, item.Arguments)
		case "function_call_output":
			msgs = appendFunctionCallOutput(msgs, item.CallID, item.Output)
		}
	}
	return msgs
}

// convertInputsToMessages converts ResponseInputs to chat Messages.
func (s *Server) convertInputsToMessages(req gw.CreateResponseRequest) []gw.Message {
	return appendResponseInputMessages(nil, req.Input)
}

// appendResponseInputMessages appends Responses input items to msgs as chat
// messages, with the same mapping as appendResponseOutputMessages.
func appendResponseInputMessages(msgs []gw.Message, inputs []gw.ResponseInput) []gw.Message {
	for _, input := range inputs {
		switch input.Type {
		case "message":
			msgs = append(msgs, gw.Message{
				Role:    input.Role,
				Content: input.Content,
			})
		case "function_call":
			msgs = appendFunctionCall(msgs, input.CallID, input.Name, input.Arguments)
		case "function_call_output":
			msgs = appendFunctionCallOutput(msgs, input.CallID, input.Output)
		}
	}
	return msgs
}

// appendFunctionCall adds a function_call item to msgs as a tool call of an
// assistant message. A call that directly follows an assistant message
// (another call, or the turn's text) joins that message, so one model turn
// stays one assistant message with all its tool calls, as in Chat
// Completions.
func appendFunctionCall(msgs []gw.Message, callID, name, arguments string) []gw.Message {
	tc := gw.ToolCall{ID: callID, Type: "function", Function: gw.ToolCallFunction{Name: name, Arguments: arguments}}
	if n := len(msgs); n > 0 && msgs[n-1].Role == gw.RoleAssistant {
		last := msgs[n-1].ToolCalls
		// Copy: the message may share its tool calls with a stored response.
		msgs[n-1].ToolCalls = append(last[:len(last):len(last)], tc)
		return msgs
	}
	return append(msgs, gw.Message{Role: gw.RoleAssistant, ToolCalls: []gw.ToolCall{tc}})
}

// appendFunctionCallOutput adds a function_call_output item to msgs as the
// tool message answering call callID.
func appendFunctionCallOutput(msgs []gw.Message, callID, output string) []gw.Message {
	return append(msgs, gw.Message{
		Role:       gw.RoleTool,
		Content:    []gw.ContentPart{gw.Text(output)},
		ToolCallID: callID,
	})
}

// convertToToolDefinitions converts builtin and custom tools to ToolDefinitions.
func (s *Server) convertToToolDefinitions(req gw.CreateResponseRequest) []gw.ToolDefinition {
	var tools []gw.ToolDefinition

	// Add custom function tools
	tools = append(tools, req.Tools...)

	// Builtin tools are handled by the tool loop manager
	// They don't need to be sent to the model as tool definitions

	return tools
}

// GetResponse retrieves a response by ID.
func (s *Server) GetResponse(ctx context.Context, req *connect.Request[gatewaypb.GetResponseRequest]) (*connect.Response[gatewaypb.GetResponseResponse], error) {
	resp, ok := s.lookupResponse(ctx, req.Msg.ResponseId)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, gw.ErrNotImplemented("response not found"))
	}

	protoResp := &gatewaypb.GetResponseResponse{
		Response: convertResponseToProto(resp),
	}

	return connect.NewResponse(protoResp), nil
}

// CancelResponse cancels an in-progress response.
func (s *Server) CancelResponse(ctx context.Context, req *connect.Request[gatewaypb.CancelResponseRequest]) (*connect.Response[gatewaypb.CancelResponseResponse], error) {
	resp, ok := s.lookupResponse(ctx, req.Msg.ResponseId)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, gw.ErrNotImplemented("response not found"))
	}

	// Update status to cancelled
	resp.Status = "cancelled"
	s.storeResponse(ctx, req.Msg.ResponseId, resp)

	return connect.NewResponse(&gatewaypb.CancelResponseResponse{
		Success:  true,
		Response: convertResponseToProto(resp),
	}), nil
}

// DeleteResponse deletes a response.
func (s *Server) DeleteResponse(ctx context.Context, req *connect.Request[gatewaypb.DeleteResponseRequest]) (*connect.Response[gatewaypb.DeleteResponseResponse], error) {
	deleted := false
	if owner, ok := s.responseOwner(ctx); ok {
		deleted = globalResponseStore.Delete(owner, req.Msg.ResponseId)
	}

	return connect.NewResponse(&gatewaypb.DeleteResponseResponse{
		Id:      req.Msg.ResponseId,
		Object:  "response.deleted",
		Deleted: deleted,
	}), nil
}

// ListResponses lists stored responses.
func (s *Server) ListResponses(ctx context.Context, req *connect.Request[gatewaypb.ListResponsesRequest]) (*connect.Response[gatewaypb.ListResponsesResponse], error) {
	limit := int(req.Msg.Limit)
	if limit == 0 {
		limit = 20
	}

	var responses []*gw.CreateResponseResponse
	if owner, ok := s.responseOwner(ctx); ok {
		responses = globalResponseStore.List(owner, limit, req.Msg.After, req.Msg.Before)
	}

	protoResp := &gatewaypb.ListResponsesResponse{
		Object: "list",
	}

	for _, resp := range responses {
		protoResp.Data = append(protoResp.Data, convertResponseToProto(resp))
	}

	if len(protoResp.Data) > 0 {
		protoResp.FirstId = protoResp.Data[0].Id
		protoResp.LastId = protoResp.Data[len(protoResp.Data)-1].Id
	}
	protoResp.HasMore = false

	return connect.NewResponse(protoResp), nil
}

// Helper functions

func convertProtoToResponseRequest(msg *gatewaypb.CreateResponseRequest) gw.CreateResponseRequest {
	req := gw.CreateResponseRequest{
		Model:                 msg.Model,
		Instructions:          msg.Instructions,
		ParallelToolCalls:     msg.ParallelToolCalls,
		MaxOutputTokens:       int(msg.GetMaxOutputTokens()),
		MaxOutputConfigured:   msg.MaxOutputTokens != nil,
		Temperature:           float64(msg.GetTemperature()),
		TemperatureConfigured: msg.Temperature != nil,
		TopP:                  float64(msg.GetTopP()),
		TopPConfigured:        msg.TopP != nil,
		Store:                 msg.GetStore(),
		StoreConfigured:       msg.Store != nil,
		PreviousResponseID:    msg.PreviousResponseId,
		Stream:                msg.Stream,
		Metadata:              msg.Metadata,
	}

	// Convert inputs
	for _, input := range msg.Input {
		gwInput := gw.ResponseInput{
			Type:      input.Type,
			Role:      convertProtoRole(input.Role),
			ItemID:    input.ItemId,
			CallID:    input.CallId,
			Output:    input.Output,
			Name:      input.Name,
			Arguments: input.Arguments,
		}
		for _, content := range input.Content {
			gwInput.Content = append(gwInput.Content, convertProtoContentPart(content))
		}
		req.Input = append(req.Input, gwInput)
	}

	// Convert tools
	for _, tool := range msg.Tools {
		req.Tools = append(req.Tools, gw.ToolDefinition{
			Type: tool.Type,
			Function: gw.ToolFunctionDef{
				Name:        tool.Function.Name,
				Description: tool.Function.Description,
				Parameters:  protoStructToMap(tool.Function.Parameters),
			},
		})
	}

	// Convert builtin tools
	for _, bt := range msg.BuiltinTools {
		gwBT := gw.BuiltInToolConfig{
			Type: bt.Type,
		}
		if bt.WebSearch != nil {
			gwBT.WebSearch = &gw.WebSearchConfig{
				MaxResults:        int(bt.WebSearch.MaxResults),
				SearchContextSize: bt.WebSearch.SearchContextSize,
			}
			if bt.WebSearch.UserLocation != nil {
				gwBT.WebSearch.UserLocation = &gw.WebSearchLocation{
					Type:     bt.WebSearch.UserLocation.Type,
					City:     bt.WebSearch.UserLocation.City,
					Region:   bt.WebSearch.UserLocation.Region,
					Country:  bt.WebSearch.UserLocation.Country,
					Timezone: bt.WebSearch.UserLocation.Timezone,
				}
			}
		}
		if bt.FileSearch != nil {
			gwBT.FileSearch = &gw.FileSearchConfig{
				VectorStoreIDs: bt.FileSearch.VectorStoreIds,
				MaxNumResults:  int(bt.FileSearch.MaxNumResults),
			}
			if bt.FileSearch.RankingOptions != nil {
				gwBT.FileSearch.RankingOptions = &gw.FileSearchRankingOptions{
					Ranker:         bt.FileSearch.RankingOptions.Ranker,
					ScoreThreshold: float64(bt.FileSearch.RankingOptions.ScoreThreshold),
				}
			}
		}
		if bt.CodeInterpreter != nil && bt.CodeInterpreter.Container != nil {
			gwBT.CodeInterpreter = &gw.CodeInterpreterConfig{
				Container: &gw.CodeInterpreterContainer{
					Type:    bt.CodeInterpreter.Container.Type,
					FileIDs: bt.CodeInterpreter.Container.FileIds,
				},
			}
		}
		if bt.ComputerUse != nil {
			gwBT.ComputerUse = &gw.ComputerUseConfig{
				Environment:   bt.ComputerUse.Environment,
				DisplayWidth:  int(bt.ComputerUse.DisplayWidth),
				DisplayHeight: int(bt.ComputerUse.DisplayHeight),
			}
		}
		if bt.Mcp != nil {
			gwBT.MCP = &gw.McpServerConfig{
				URL:             bt.Mcp.Url,
				Name:            bt.Mcp.Name,
				RequireApproval: bt.Mcp.RequireApproval,
				AllowedTools:    bt.Mcp.AllowedTools,
				Headers:         bt.Mcp.Headers,
			}
			if bt.Mcp.ToolFilter != nil {
				gwBT.MCP.ToolFilter = &gw.McpToolFilter{
					Type:  bt.Mcp.ToolFilter.Type,
					Tools: bt.Mcp.ToolFilter.Tools,
				}
			}
		}
		req.BuiltinTools = append(req.BuiltinTools, gwBT)
	}

	// Convert text config
	if msg.Text != nil && msg.Text.Format != nil {
		req.Text = &gw.ResponseTextConfig{
			Format: &gw.ResponseFormatConfig{
				Type:       msg.Text.Format.Type,
				JSONSchema: protoStructToMap(msg.Text.Format.JsonSchema),
			},
		}
	}

	// Convert reasoning config
	if msg.Reasoning != nil {
		req.Reasoning = &gw.ReasoningConfig{
			Effort:          msg.Reasoning.Effort,
			GenerateSummary: msg.Reasoning.GenerateSummary,
		}
	}

	// Convert truncation
	if msg.Truncation != nil {
		req.Truncation = &gw.TruncationStrategy{
			Type:         msg.Truncation.Type,
			LastMessages: int(msg.Truncation.LastMessages),
		}
	}

	return req
}

func convertResponseToProto(resp *gw.CreateResponseResponse) *gatewaypb.CreateResponseResponse {
	protoResp := &gatewaypb.CreateResponseResponse{
		Id:                 resp.ID,
		Object:             resp.Object,
		CreatedAt:          resp.CreatedAt,
		Status:             resp.Status,
		Model:              resp.Model,
		Metadata:           resp.Metadata,
		Temperature:        float32(resp.Temperature),
		TopP:               float32(resp.TopP),
		MaxOutputTokens:    int32(resp.MaxOutputTokens),
		PreviousResponseId: resp.PreviousResponseID,
	}

	// Convert error
	if resp.Error != nil {
		protoResp.Error = &gatewaypb.ResponseError{
			Code:    resp.Error.Code,
			Message: resp.Error.Message,
		}
	}

	// Convert incomplete details
	if resp.IncompleteDetails != nil {
		protoResp.IncompleteDetails = &gatewaypb.IncompleteDetails{
			Reason: resp.IncompleteDetails.Reason,
		}
	}

	// Convert output items
	for _, item := range resp.Output {
		protoItem := &gatewaypb.ResponseOutputItem{
			Id:        item.ID,
			Type:      item.Type,
			Status:    item.Status,
			Role:      convertRoleToProto(item.Role),
			CallId:    item.CallID,
			Name:      item.Name,
			Arguments: item.Arguments,
			Output:    item.Output,
		}
		for _, content := range item.Content {
			protoItem.Content = append(protoItem.Content, convertContentPartToProto(content))
		}
		if item.WebSearch != nil {
			protoItem.WebSearchResults = &gatewaypb.WebSearchResults{
				Query: item.WebSearch.Query,
			}
			for _, r := range item.WebSearch.Results {
				protoItem.WebSearchResults.Results = append(protoItem.WebSearchResults.Results, &gatewaypb.WebSearchResult{
					Title:   r.Title,
					Url:     r.URL,
					Snippet: r.Snippet,
				})
			}
		}
		if item.FileSearch != nil {
			protoItem.FileSearchResults = &gatewaypb.FileSearchResults{
				Query: item.FileSearch.Query,
			}
			for _, r := range item.FileSearch.Results {
				protoItem.FileSearchResults.Results = append(protoItem.FileSearchResults.Results, &gatewaypb.FileSearchResult{
					FileId:   r.FileID,
					FileName: r.FileName,
					Content:  r.Content,
					Score:    float32(r.Score),
				})
			}
		}
		protoResp.Output = append(protoResp.Output, protoItem)
	}

	// Convert usage
	if resp.Usage != nil {
		protoResp.Usage = &gatewaypb.ResponseUsage{
			InputTokens:  int32(resp.Usage.InputTokens),
			OutputTokens: int32(resp.Usage.OutputTokens),
			TotalTokens:  int32(resp.Usage.TotalTokens),
		}
		if resp.Usage.InputTokensDetails != nil {
			protoResp.Usage.InputTokensDetails = &gatewaypb.ResponseUsageDetails{
				CachedTokens:     int32(resp.Usage.InputTokensDetails.CachedTokens),
				CacheWriteTokens: int32(resp.Usage.InputTokensDetails.CacheWriteTokens),
				TextTokens:       int32(resp.Usage.InputTokensDetails.TextTokens),
				AudioTokens:      int32(resp.Usage.InputTokensDetails.AudioTokens),
				ImageTokens:      int32(resp.Usage.InputTokensDetails.ImageTokens),
				ReasoningTokens:  int32(resp.Usage.InputTokensDetails.ReasoningTokens),
			}
		}
		if resp.Usage.OutputTokensDetails != nil {
			protoResp.Usage.OutputTokensDetails = &gatewaypb.ResponseUsageDetails{
				CachedTokens:     int32(resp.Usage.OutputTokensDetails.CachedTokens),
				CacheWriteTokens: int32(resp.Usage.OutputTokensDetails.CacheWriteTokens),
				TextTokens:       int32(resp.Usage.OutputTokensDetails.TextTokens),
				AudioTokens:      int32(resp.Usage.OutputTokensDetails.AudioTokens),
				ImageTokens:      int32(resp.Usage.OutputTokensDetails.ImageTokens),
				ReasoningTokens:  int32(resp.Usage.OutputTokensDetails.ReasoningTokens),
			}
		}
	}

	// Convert reasoning config
	if resp.Reasoning != nil {
		protoResp.Reasoning = &gatewaypb.ReasoningConfig{
			Effort:          resp.Reasoning.Effort,
			GenerateSummary: resp.Reasoning.GenerateSummary,
		}
	}

	// Convert truncation
	if resp.Truncation != nil {
		protoResp.Truncation = &gatewaypb.TruncationStrategy{
			Type:         resp.Truncation.Type,
			LastMessages: int32(resp.Truncation.LastMessages),
		}
	}

	return protoResp
}

func convertProtoRole(role gatewaypb.Role) gw.MessageRole {
	switch role {
	case gatewaypb.Role_ROLE_SYSTEM:
		return gw.RoleSystem
	case gatewaypb.Role_ROLE_USER:
		return gw.RoleUser
	case gatewaypb.Role_ROLE_ASSISTANT:
		return gw.RoleAssistant
	case gatewaypb.Role_ROLE_FUNCTION:
		return gw.RoleFunction
	case gatewaypb.Role_ROLE_TOOL:
		return gw.RoleTool
	default:
		return gw.RoleUser
	}
}

func convertRoleToProto(role gw.MessageRole) gatewaypb.Role {
	switch role {
	case gw.RoleSystem:
		return gatewaypb.Role_ROLE_SYSTEM
	case gw.RoleUser:
		return gatewaypb.Role_ROLE_USER
	case gw.RoleAssistant:
		return gatewaypb.Role_ROLE_ASSISTANT
	case gw.RoleFunction:
		return gatewaypb.Role_ROLE_FUNCTION
	case gw.RoleTool:
		return gatewaypb.Role_ROLE_TOOL
	default:
		return gatewaypb.Role_ROLE_UNSPECIFIED
	}
}

func convertProtoContentPart(cp *gatewaypb.ContentPart) gw.ContentPart {
	part := gw.ContentPart{
		Type: cp.Type,
	}
	switch data := cp.Data.(type) {
	case *gatewaypb.ContentPart_Text:
		part.Text = &data.Text
	case *gatewaypb.ContentPart_ImageUrl:
		part.ImageURL = &data.ImageUrl
	case *gatewaypb.ContentPart_FileId:
		part.FileID = &data.FileId
	case *gatewaypb.ContentPart_ToolCallId:
		part.ToolCallID = &data.ToolCallId
	}
	return part
}

func convertContentPartToProto(cp gw.ContentPart) *gatewaypb.ContentPart {
	protoCP := &gatewaypb.ContentPart{
		Type: cp.Type,
	}
	if cp.Text != nil {
		protoCP.Data = &gatewaypb.ContentPart_Text{Text: *cp.Text}
	} else if cp.ImageURL != nil {
		protoCP.Data = &gatewaypb.ContentPart_ImageUrl{ImageUrl: *cp.ImageURL}
	} else if cp.FileID != nil {
		protoCP.Data = &gatewaypb.ContentPart_FileId{FileId: *cp.FileID}
	} else if cp.ToolCallID != nil {
		protoCP.Data = &gatewaypb.ContentPart_ToolCallId{ToolCallId: *cp.ToolCallID}
	}
	return protoCP
}

var responseIDCounter int64
var itemIDCounter int64
var idMutex sync.Mutex

func generateResponseID() string {
	idMutex.Lock()
	defer idMutex.Unlock()
	responseIDCounter++
	return fmt.Sprintf("resp_%s_%d", time.Now().Format("20060102150405"), responseIDCounter)
}

func generateItemID() string {
	idMutex.Lock()
	defer idMutex.Unlock()
	itemIDCounter++
	return fmt.Sprintf("item_%s_%d", time.Now().Format("20060102150405"), itemIDCounter)
}

func stringPtr(s string) *string {
	return &s
}

// responsesConnectError keeps a Connect status the provider chain already
// chose (the spend limit refusal is ResourceExhausted with a reason detail
// the HTTP layer turns into 402); anything else stays Internal.
func responsesConnectError(err error) error {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return err
	}
	return connect.NewError(connect.CodeInternal, err)
}
