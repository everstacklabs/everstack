package deployment

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	agenttools "github.com/everstacklabs/everstack/internal/agents/runtime/tools"
	"github.com/everstacklabs/everstack/internal/commands"
	agentscmd "github.com/everstacklabs/everstack/internal/commands/handlers/agents"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/mcp"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	"github.com/google/uuid"
)

// McpRegistryHydrator restores a tenant's MCP servers into the in-memory
// registry. Mirrors the contract used by the gRPC agents server.
type McpRegistryHydrator interface {
	HydrateRegistryForTenant(ctx context.Context, tenantID string) error
}

// AgentConfig is the subset of agent definition fields we deserialize from the snapshot.
type AgentConfig struct {
	Model               string                 `json:"model"`
	SystemPrompt        string                 `json:"system_prompt"`
	Tools               []string               `json:"tools"`
	MaxTurns            int                    `json:"max_turns"`
	MaxToolCallsPerTurn int                    `json:"max_tool_calls_per_turn"`
	MaxHistoryMessages  int                    `json:"max_history_messages"`
	Config              map[string]interface{} `json:"config"`
}

// Invoker wraps the agent runtime to handle API invocations.
type Invoker struct {
	sessionMgr  *agentrt.SessionManager
	commandBus  commands.CommandBus
	queryBus    query.QueryBus
	mcpRegistry *mcp.Registry
	mcpHydrator McpRegistryHydrator
}

// NewInvoker creates a new deployment invoker.
func NewInvoker(sessionMgr *agentrt.SessionManager, commandBus commands.CommandBus, queryBus query.QueryBus) *Invoker {
	return &Invoker{sessionMgr: sessionMgr, commandBus: commandBus, queryBus: queryBus}
}

// SetMcpRegistry wires the federated MCP registry so deployment invocations
// expose the same MCP tools the agent has in the chat path.
func (inv *Invoker) SetMcpRegistry(r *mcp.Registry) { inv.mcpRegistry = r }

// SetMcpRegistryHydrator wires a hydrator that restores the tenant's enabled
// MCP servers into the in-memory registry on each invocation.
func (inv *Invoker) SetMcpRegistryHydrator(h McpRegistryHydrator) { inv.mcpHydrator = h }

// buildInterceptor mirrors the gRPC chat path's MCP wiring so deployment
// invocations get the same federated MCP tool handlers. Returns nil when
// no handlers were registered — callers must check before assigning to
// LoopInput.Interceptor (a typed-nil pointer would become a non-nil interface).
func (inv *Invoker) buildInterceptor(ctx context.Context, dep *Deployment, agentCfg *AgentConfig) *agenttools.ToolInterceptor {
	if inv.mcpRegistry == nil || inv.sessionMgr == nil {
		return nil
	}
	if inv.mcpHydrator != nil {
		if err := inv.mcpHydrator.HydrateRegistryForTenant(ctx, dep.TenantID); err != nil {
			logger.WithFields(
				"deployment_id", dep.ID,
				"tenant_id", dep.TenantID,
				"error", err.Error(),
			).Warn("deployment: mcp registry hydration failed; continuing")
		}
	}
	explicit := make(map[string]struct{}, len(agentCfg.Tools))
	for _, t := range agentCfg.Tools {
		explicit[t] = struct{}{}
	}
	handlers := agenttools.NewMcpToolHandlers(inv.mcpRegistry, dep.TenantID)
	if len(handlers) == 0 {
		return nil
	}
	interceptor := agenttools.NewToolInterceptor(inv.sessionMgr.GetToolLoop())
	registered := 0
	for _, h := range handlers {
		if _, ok := explicit[h.Name()]; !ok {
			continue
		}
		interceptor.RegisterHandler(h)
		registered++
	}
	logger.WithFields(
		"deployment_id", dep.ID,
		"tenant_id", dep.TenantID,
		"available", len(handlers),
		"registered", registered,
	).Debug("deployment: registered MCP tool handlers")
	if registered == 0 {
		return nil
	}
	return interceptor
}

// sessionHistory holds loaded session state for continuing a conversation.
type sessionHistory struct {
	turnNumber   int32
	totalTokens  int
	messages     []gw.Message
	isNewSession bool
}

// loadOrCreateSession loads an existing session's history or prepares a new one.
// When a session_id is provided and the session exists in the DB, prior turns are
// loaded and trimmed to the history budget. Otherwise a new session is created via CQRS.
func (inv *Invoker) loadOrCreateSession(ctx context.Context, dep *Deployment, agentCfg *AgentConfig, sessionID string, isExplicitSession bool) (*sessionHistory, string, error) {
	// Try to load existing session if a session_id was provided and we have a query bus
	if isExplicitSession && inv.queryBus != nil {
		swt, err := inv.loadSessionWithRetry(ctx, sessionID, dep.TenantID)
		if err == nil && swt != nil {
			// Session exists — build history from prior turns
			hist := inv.buildHistoryFromTurns(agentCfg, swt)
			return hist, sessionID, nil
		}
		// Session doesn't exist yet — fall through to create it
		logger.WithFields("session_id", sessionID, "error", fmt.Sprintf("%v", err)).Debug("deployment: session not found, creating new")
	}

	// Generate session ID if not provided
	if sessionID == "" {
		sessionID = uuid.New().String()
	}

	// Register new session in CQRS so projections create the agent_sessions row.
	// This is required for the agent_session_turns FK constraint.
	if inv.commandBus != nil && dep.TrackSessions {
		cmd := agentscmd.NewCreateSessionCommand(dep.TenantID, dep.AgentID, map[string]interface{}{
			"source":        "deployment",
			"deployment_id": dep.ID,
		}, "", "")
		cmd.ID = sessionID
		if err := inv.commandBus.Dispatch(ctx, cmd); err != nil {
			logger.WithFields("error", err.Error(), "session_id", sessionID).Warn("deployment: failed to register session in CQRS")
		}
		// Small delay for eventual consistency — the projection needs to process the event
		time.Sleep(50 * time.Millisecond)
	}

	return &sessionHistory{isNewSession: true}, sessionID, nil
}

// loadSessionWithRetry queries the CQRS read model with retries for eventual consistency.
func (inv *Invoker) loadSessionWithRetry(ctx context.Context, sessionID, tenantID string) (*agentsquery.SessionWithTurns, error) {
	q := agentsquery.NewGetSessionByIDQuery(sessionID, tenantID)
	for attempt := 0; attempt < 3; attempt++ {
		res, err := inv.queryBus.Execute(ctx, q)
		if err != nil {
			if attempt < 2 {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			return nil, err
		}
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}
		if swt, ok := data.(*agentsquery.SessionWithTurns); ok {
			return swt, nil
		}
		if attempt < 2 {
			time.Sleep(100 * time.Millisecond)
		}
	}
	return nil, fmt.Errorf("session %s not found", sessionID)
}

// buildHistoryFromTurns converts stored session turns into LoopState messages,
// following the same pattern as RunTurnStream in the gRPC agents handler.
func (inv *Invoker) buildHistoryFromTurns(agentCfg *AgentConfig, swt *agentsquery.SessionWithTurns) *sessionHistory {
	hasSystemPrompt := agentCfg.SystemPrompt != ""

	// Trim turns to history budget (reserve 1 slot for the new user message)
	maxHistory := int32(agentCfg.MaxHistoryMessages)
	if maxHistory <= 0 {
		maxHistory = 100 // default
	}
	turns := trimTurnsToHistoryBudget(swt.Turns, maxHistory, hasSystemPrompt, 1)

	// Build messages slice (system prompt + prior user/assistant pairs)
	var messages []gw.Message
	if hasSystemPrompt {
		messages = append(messages, gw.Message{
			Role:    gw.RoleSystem,
			Content: []gw.ContentPart{{Type: "text", Text: strPtr(agentCfg.SystemPrompt)}},
		})
	}
	for _, t := range turns {
		if t.UserInput.Valid && t.UserInput.String != "" {
			messages = append(messages, gw.Message{
				Role:    gw.RoleUser,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr(t.UserInput.String)}},
			})
		}
		if t.AssistantOutput.Valid && t.AssistantOutput.String != "" {
			messages = append(messages, gw.Message{
				Role:    gw.RoleAssistant,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr(t.AssistantOutput.String)}},
			})
		}
	}

	return &sessionHistory{
		turnNumber:  int32(len(swt.Turns)),
		totalTokens: int(swt.Session.TotalTokens),
		messages:    messages,
	}
}

// trimTurnsToHistoryBudget selects the newest turns that fit within the budget.
// Mirrors the logic in internal/api/grpc/agents/v1/agents.go.
func trimTurnsToHistoryBudget(
	turns []agentsquery.AgentSessionTurnReadModel,
	maxHistoryMessages int32,
	hasSystemPrompt bool,
	reservedNewMessages int,
) []agentsquery.AgentSessionTurnReadModel {
	if maxHistoryMessages <= 0 || len(turns) == 0 {
		return turns
	}

	available := int(maxHistoryMessages) - reservedNewMessages
	if hasSystemPrompt {
		available--
	}
	if available <= 0 {
		return nil
	}

	total := 0
	start := len(turns)
	for i := len(turns) - 1; i >= 0; i-- {
		msgCount := 0
		if turns[i].UserInput.Valid && turns[i].UserInput.String != "" {
			msgCount++
		}
		if turns[i].AssistantOutput.Valid && turns[i].AssistantOutput.String != "" {
			msgCount++
		}
		if msgCount == 0 {
			start = i
			continue
		}
		if total+msgCount > available {
			break
		}
		total += msgCount
		start = i
	}

	if start >= len(turns) {
		return nil
	}
	return turns[start:]
}

func strPtr(s string) *string { return &s }

// resolveConfig extracts common config from deployment + request (max turns, timeout).
type invokeConfig struct {
	maxTurns int
	timeout  time.Duration
}

func resolveInvokeConfig(dep *Deployment, agentCfg *AgentConfig, req *InvokeRequest) invokeConfig {
	maxTurns := agentCfg.MaxTurns
	if dep.MaxTurnsPerSession != nil && *dep.MaxTurnsPerSession > 0 {
		maxTurns = *dep.MaxTurnsPerSession
	}
	if req.MaxTurns != nil && *req.MaxTurns > 0 {
		maxTurns = *req.MaxTurns
	}
	if maxTurns <= 0 {
		maxTurns = 10
	}

	timeout := time.Duration(dep.SessionTimeoutSeconds) * time.Second
	if req.TimeoutSeconds != nil && *req.TimeoutSeconds > 0 {
		timeout = time.Duration(*req.TimeoutSeconds) * time.Second
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	return invokeConfig{maxTurns: maxTurns, timeout: timeout}
}

// InvokeSync creates or continues a session, runs it to completion, and returns the final output.
func (inv *Invoker) InvokeSync(ctx context.Context, dep *Deployment, req *InvokeRequest) (*InvokeResponse, error) {
	start := time.Now()

	// Deserialize agent config from snapshot
	var agentCfg AgentConfig
	if err := json.Unmarshal(dep.AgentConfigSnapshot, &agentCfg); err != nil {
		return nil, fmt.Errorf("invalid agent config snapshot: %w", err)
	}

	cfg := resolveInvokeConfig(dep, &agentCfg, req)

	// Load existing session or create a new one
	isExplicitSession := req.SessionID != ""
	hist, sessionID, err := inv.loadOrCreateSession(ctx, dep, &agentCfg, req.SessionID, isExplicitSession)
	if err != nil {
		return nil, fmt.Errorf("session setup: %w", err)
	}

	// Build loop input
	loopInput := &agentrt.LoopInput{
		TenantID:     dep.TenantID,
		AgentID:      dep.AgentID,
		SessionID:    sessionID,
		Model:        agentCfg.Model,
		SystemPrompt: agentCfg.SystemPrompt,
		Tools:        agentCfg.Tools,
		UserInput:    req.Message,
	}

	// Wire MCP tool handlers so the agent has the same federated tools here
	// as it does in the chat path. Without this the LLM is called with no
	// tool definitions and reports "I don't have access to MCP".
	if interceptor := inv.buildInterceptor(ctx, dep, &agentCfg); interceptor != nil {
		loopInput.Interceptor = interceptor
	}

	// Build session runner config with conversation history. Force terminal
	// status so the SessionManager marks the session "completed" after the
	// run — deployment invocations are discrete requests with no live
	// runner waiting between them, and leaving the status at
	// "waiting_for_input" causes the UI to open SSE subscribes that loop
	// on 204 until the retry budget is exhausted.
	runnerConfig := agentrt.SessionRunnerConfig{
		LoopConfig: agentrt.LoopConfig{
			MaxIterations: int32(cfg.maxTurns),
		},
		ForceTerminalStatus: true,
	}

	// For continuing sessions, inject prior conversation as InitialState
	if !hist.isNewSession && len(hist.messages) > 0 {
		runnerConfig.InitialState = &agentrt.LoopState{
			TurnNumber:         hist.turnNumber,
			Messages:           hist.messages,
			PriorSessionTokens: hist.totalTokens,
		}
	}

	// Prepare and launch session
	emitter, err := inv.sessionMgr.PrepareSession(ctx, sessionID, dep.AgentID, dep.TenantID, loopInput, runnerConfig)
	if err != nil {
		return nil, fmt.Errorf("prepare session: %w", err)
	}

	// Collect events via a channel sink
	eventCh := make(chan agentrt.Event, 256)
	emitter.AddSink(agentrt.EventSinkFunc(func(e agentrt.Event) error {
		select {
		case eventCh <- e:
		default:
		}
		return nil
	}))

	// Create a cancellable context for the timeout
	runCtx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()

	if err := inv.sessionMgr.LaunchSession(runCtx, sessionID, loopInput); err != nil {
		return nil, fmt.Errorf("launch session: %w", err)
	}

	// Collect results
	var (
		finalOutput      string
		turns            int
		promptTokens     int
		completionTokens int
		status           = string(InvocationCompleted)
		errMsg           string
	)

	for {
		select {
		case evt, ok := <-eventCh:
			if !ok {
				goto done
			}
			switch evt.Type {
			case agentrt.EventTurnEnd:
				turns++
				if text, ok := evt.Data["assistant_text"].(string); ok && text != "" {
					finalOutput = text
				}
				if evt.Usage != nil {
					promptTokens += evt.Usage.PromptTokens
					completionTokens += evt.Usage.CompletionTokens
				}
			case agentrt.EventSessionEnd:
				goto done
			case agentrt.EventSessionError:
				status = string(InvocationFailed)
				if msg, ok := evt.Data["error"].(string); ok {
					errMsg = msg
				}
				goto done
			}
		case <-runCtx.Done():
			status = string(InvocationTimeout)
			errMsg = "invocation timed out"
			goto done
		}
	}

done:
	duration := int(time.Since(start).Milliseconds())

	return &InvokeResponse{
		SessionID:  sessionID,
		Status:     status,
		Output:     finalOutput,
		Turns:      turns,
		Tokens:     InvokeTokens{Prompt: promptTokens, Completion: completionTokens},
		DurationMs: duration,
		Error:      errMsg,
	}, nil
}

// InvokeStream creates or continues a session and streams SSE events to the
// ResponseWriter. Returns a summary of the run (status, turns, tokens, output
// preview) so the caller can complete the invocation record — without it,
// streamed invocations would be stuck in 'running'.
func (inv *Invoker) InvokeStream(ctx context.Context, dep *Deployment, req *InvokeRequest, w http.ResponseWriter) (*InvokeResponse, error) {
	start := time.Now()
	flusher, ok := w.(http.Flusher)
	if !ok {
		return nil, fmt.Errorf("streaming not supported")
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	// Deserialize agent config
	var agentCfg AgentConfig
	if err := json.Unmarshal(dep.AgentConfigSnapshot, &agentCfg); err != nil {
		return nil, fmt.Errorf("invalid agent config snapshot: %w", err)
	}

	cfg := resolveInvokeConfig(dep, &agentCfg, req)

	// Load existing session or create a new one
	isExplicitSession := req.SessionID != ""
	hist, sessionID, err := inv.loadOrCreateSession(ctx, dep, &agentCfg, req.SessionID, isExplicitSession)
	if err != nil {
		return nil, fmt.Errorf("session setup: %w", err)
	}

	// Build loop input
	loopInput := &agentrt.LoopInput{
		TenantID:     dep.TenantID,
		AgentID:      dep.AgentID,
		SessionID:    sessionID,
		Model:        agentCfg.Model,
		SystemPrompt: agentCfg.SystemPrompt,
		Tools:        agentCfg.Tools,
		UserInput:    req.Message,
	}

	if interceptor := inv.buildInterceptor(ctx, dep, &agentCfg); interceptor != nil {
		loopInput.Interceptor = interceptor
	}

	// Build session runner config with conversation history. Force terminal
	// status so the SessionManager marks the session "completed" after the
	// run — deployment invocations are discrete requests with no live
	// runner waiting between them, and leaving the status at
	// "waiting_for_input" causes the UI to open SSE subscribes that loop
	// on 204 until the retry budget is exhausted.
	runnerConfig := agentrt.SessionRunnerConfig{
		LoopConfig: agentrt.LoopConfig{
			MaxIterations: int32(cfg.maxTurns),
		},
		ForceTerminalStatus: true,
	}

	if !hist.isNewSession && len(hist.messages) > 0 {
		runnerConfig.InitialState = &agentrt.LoopState{
			TurnNumber:         hist.turnNumber,
			Messages:           hist.messages,
			PriorSessionTokens: hist.totalTokens,
		}
	}

	emitter, err := inv.sessionMgr.PrepareSession(ctx, sessionID, dep.AgentID, dep.TenantID, loopInput, runnerConfig)
	if err != nil {
		return nil, fmt.Errorf("prepare session: %w", err)
	}

	// Track summary stats while streaming so the handler can complete the
	// invocation record after the stream finishes.
	summary := &InvokeResponse{SessionID: sessionID, Status: string(InvocationCompleted)}
	var summaryMu sync.Mutex

	doneCh := make(chan struct{})
	emitter.AddSink(agentrt.EventSinkFunc(func(e agentrt.Event) error {
		data, err := json.Marshal(e)
		if err != nil {
			logger.WithFields("error", err.Error()).Warn("deployment: failed to marshal SSE event")
			return nil
		}
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, data)
		flusher.Flush()

		summaryMu.Lock()
		switch e.Type {
		case agentrt.EventTurnEnd:
			summary.Turns++
			if text, ok := e.Data["assistant_text"].(string); ok && text != "" {
				summary.Output = text
			}
			if e.Usage != nil {
				summary.Tokens.Prompt += e.Usage.PromptTokens
				summary.Tokens.Completion += e.Usage.CompletionTokens
			}
		case agentrt.EventSessionError:
			summary.Status = string(InvocationFailed)
			if msg, ok := e.Data["error"].(string); ok {
				summary.Error = msg
			}
		}
		summaryMu.Unlock()

		if e.Type == agentrt.EventSessionEnd || e.Type == agentrt.EventSessionError {
			select {
			case <-doneCh:
			default:
				close(doneCh)
			}
		}
		return nil
	}))

	runCtx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()

	if err := inv.sessionMgr.LaunchSession(runCtx, sessionID, loopInput); err != nil {
		return nil, fmt.Errorf("launch session: %w", err)
	}

	// Wait for completion or timeout
	select {
	case <-doneCh:
	case <-runCtx.Done():
		fmt.Fprintf(w, "event: error\ndata: {\"error\":\"timeout\"}\n\n")
		flusher.Flush()
		summaryMu.Lock()
		summary.Status = string(InvocationTimeout)
		summary.Error = "invocation timed out"
		summaryMu.Unlock()
	}

	summaryMu.Lock()
	summary.DurationMs = int(time.Since(start).Milliseconds())
	out := *summary
	summaryMu.Unlock()
	return &out, nil
}
