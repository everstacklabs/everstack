package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/agents/policy"
	"github.com/everstacklabs/everstack/internal/functions/toolloop"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/handlers/gateway/cacheexec"
	fpmetrics "github.com/everstacklabs/everstack/internal/lib/handlers/gateway/metrics"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/retrypolicy"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"github.com/everstacklabs/everstack/internal/telemetry/autoscorer"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// LoopConfig controls the agent loop behavior.
type LoopConfig struct {
	MaxIterations       int32         // Max LLM->tool cycles per user message (default: 200)
	MaxToolCallsPerTurn int32         // Max tool calls across all iterations (0 = unlimited)
	MaxHistoryMessages  int32         // Max conversation messages to include in LLM context (0 = unlimited, default: 100)
	EnableStreaming     bool          // Stream text deltas to EventSink (default: true)
	TurnTimeout         time.Duration // Per-turn timeout including all iterations (default: 30m)
	SessionTokenBudget  int64         // Maximum total tokens per session (0 = unlimited)
}

const maxToolArgSize = 64 * 1024 // 64KB per tool argument
const maxRepeatedToolSignatures = 3

type syntheticToolExecutor interface {
	ExecuteSyntheticTool(ctx context.Context, name string, argsJSON string) (string, error)
}

func executeTracedSyntheticTool(
	ctx context.Context,
	interceptor syntheticToolExecutor,
	toolCallID string,
	toolName string,
	argsJSON string,
) (result string, err error, durationMs int64) {
	startedAt := time.Now()
	toolCtx, span := telemetry.StartAgentToolCallSpan(ctx, toolCallID, toolName)
	defer func() {
		durationMs = time.Since(startedAt).Milliseconds()
		attrs.SetAgentToolCallResult(span, err == nil, durationMs, len(result))
		if err != nil {
			telemetry.RecordError(span, err)
		}
		span.End()
	}()

	result, err = interceptor.ExecuteSyntheticTool(toolCtx, toolName, argsJSON)
	return
}

// DefaultLoopConfig returns sensible defaults.
func DefaultLoopConfig() LoopConfig {
	return LoopConfig{
		MaxIterations:       200,
		MaxToolCallsPerTurn: 0, // 0 = unlimited
		MaxHistoryMessages:  100,
		EnableStreaming:     true,
		TurnTimeout:         30 * time.Minute,
	}
}

// ToolResultMeta stores tool call result metadata for persistence.
type ToolResultMeta struct {
	Result                  string `json:"result"`
	Success                 bool   `json:"success"`
	DurationMs              int64  `json:"duration_ms"`
	SandboxParentDurationMs int64  `json:"sandbox_parent_duration_ms,omitempty"`
}

// LoopState tracks the state of the agent loop across iterations.
type LoopState struct {
	Messages           []gw.Message // Full conversation including tool results
	TurnNumber         int32
	IterationCount     int32 // Iterations within current turn
	TotalToolCalls     int32
	TurnUsage          UsageDelta
	CumulativeUsage    UsageDelta
	PriorSessionTokens int // Total tokens from all prior turns in this session
	FinishReason       string
	LastAssistantText  string
	Done               bool
	ToolResults        map[string]ToolResultMeta // Tool call results keyed by tool_call_id (reset per turn)
	// TurnStartIndex marks where the current turn's model output begins in
	// Messages (set just after the turn's user input is appended). The
	// checkpoint slices Messages[TurnStartIndex:] to build the per-turn
	// timeline without pulling in earlier turns' conversation.
	TurnStartIndex int
}

// SteerMessage allows injecting messages between loop iterations.
type SteerMessage struct {
	Role    string `json:"role"` // "user" or "system"
	Content string `json:"content"`
}

// LoopInput contains everything needed for a loop run.
type LoopInput struct {
	TenantID           string
	AgentID            string
	TrooperID          string // Deprecated: kept for backward compat; Phase 6 will remove.
	SessionID          string
	Model              string
	SystemPrompt       string
	Tools              []string
	Sampling           gw.SamplingParams
	UserInput          string
	UserID             *string // End-user ID for user-scoped memory
	AgentMode          string
	TaskPermissionMode string
	WorkingDirectory   string
	MaxSteps           int32
	ExecutionMode      string
	PersistenceMode    string
	SandboxEnabled     bool
	GitRepoConfigured  bool
	TemplateConfigured bool
	SteerCh            <-chan SteerMessage // For message injection between iterations

	// HITL approval gate (nil = disabled)
	HITLConfig *HITLConfig
	ApprovalCh chan<- ApprovalRequest  // loop sends requests to SessionManager
	DecisionCh <-chan ApprovalDecision // loop receives decisions from SessionManager

	// Model fallback configuration (nil = disabled)
	FallbackConfig *FallbackConfig
	// API type: "chat_completions" or "responses"
	APIType string

	// User input (ask_user) channels — nil if ask_user is not wired
	UserInputReqCh  chan<- UserInputRequest  // ask_user handler sends requests to SessionManager
	UserInputRespCh <-chan UserInputResponse // ask_user handler receives responses from SessionManager

	// Synthetic tool interceptor (nil = no synthetic tools)
	Interceptor interface {
		IsSyntheticTool(name string) bool
		ExecuteSyntheticTool(ctx context.Context, name string, argsJSON string) (string, error)
		CollectExposedURLs() []string
	}

	// Persistent agent memory provider (nil = disabled).
	// When set, memories are auto-retrieved into the system prompt and
	// facts/instructions are auto-extracted at turn end.
	MemoryProvider AgentMemoryProvider

	// Task planner (nil = disabled). Set when planning_mode is "on".
	Planner         *TaskPlanner
	PlannerConfig   PlannerConfig
	AvailableAgents []AgentCatalogEntry // Agent definitions available for planning
	ToolNames       []string            // Tool names available for planning
	SpawnPlan       *SpawnPlan          // Set after planner runs (before loop)
	PlanContext     string              // Injected into system prompt when plan exists
	SpawnConfig     *SpawnConfig        // Mutable spawn config adjusted by planner

	// Phase 1: Async Job Queue — completed job results injected between iterations
	JobResultCh <-chan JobResult

	// Phase 2: Fork — fork conclusions injected between iterations
	ForkResultCh <-chan ForkResult

	// Phase 3: Monitor — compaction requests applied at top of iteration
	CompactCh <-chan CompactRequest
	Monitor   *Monitor

	// Phase 4: Digest — bulletin injected into system prompt
	DigestBulletin string

	// Phase 5: Peer Messages — cross-agent messages injected between iterations
	PeerMessageCh <-chan PeerMessage

	// Installed skills — injected into system prompt
	Skills []SkillEntry

	// Auto-scoring pipeline (nil = disabled).
	// When set, heuristic scorers run asynchronously at turn end.
	AutoScorer *autoscorer.Pipeline

	// Policy evaluator (nil = disabled).
	// When set, declarative policies are evaluated at pre-turn, post-tool, and post-turn.
	PolicyEvaluator *policy.Evaluator
}

// PeerMessage is a message sent from one agent to another.
type PeerMessage struct {
	ID            string    `json:"id"`
	SenderAgentID string    `json:"sender_agent_id"`
	SenderName    string    `json:"sender_name"`
	ThreadID      string    `json:"thread_id,omitempty"`
	Content       string    `json:"content"`
	MessageType   string    `json:"message_type"` // "message" | "job_result" | "delegation"
	SentAt        time.Time `json:"sent_at"`
}

// SkillEntry holds a resolved skill's metadata for manifest injection and sandbox provisioning.
type SkillEntry struct {
	Name        string
	Description string
	Content     string
}

// AgentMemoryProvider is the interface for persistent agent memory.
type AgentMemoryProvider interface {
	RetrieveContext(ctx context.Context, agentID, tenantID, userInput string, userID *string) (string, []string, error)
	ExtractFromTurn(ctx context.Context, agentID, tenantID, sessionID, userInput, assistantOutput string, turnNumber int32, userID *string) error
	SummarizeSession(ctx context.Context, agentID, tenantID, sessionID string, messages []gw.Message) error
	IncrementAccess(ctx context.Context, tenantID string, memoryIDs []string) error
}

// Loop orchestrates the agentic loop: LLM call -> detect tool calls -> execute tools -> repeat.
type Loop struct {
	engine   *Engine
	toolLoop *toolloop.LoopManager
	emitter  *Emitter
	config   LoopConfig
}

// NewLoop creates a new agent loop.
func NewLoop(engine *Engine, toolLoop *toolloop.LoopManager, emitter *Emitter, config LoopConfig) *Loop {
	if config.MaxIterations <= 0 {
		config.MaxIterations = 200
	}
	// MaxToolCallsPerTurn: 0 = unlimited (no cap on tool calls per turn)
	if config.MaxHistoryMessages <= 0 {
		config.MaxHistoryMessages = 100
	}
	if config.TurnTimeout <= 0 {
		config.TurnTimeout = 30 * time.Minute
	}
	return &Loop{
		engine:   engine,
		toolLoop: toolLoop,
		emitter:  emitter,
		config:   config,
	}
}

// Run executes the complete agentic loop for one user message.
// It iterates: LLM call -> check for tool calls -> execute tools -> append results -> repeat
// until the LLM produces a final response or a termination condition is hit.
func (l *Loop) Run(ctx context.Context, state *LoopState, input *LoopInput) (*LoopState, error) {
	// Apply turn timeout
	turnCtx, cancel := context.WithTimeout(ctx, l.config.TurnTimeout)
	defer cancel()

	// Resolve provider ONCE for all iterations
	chatProvider, resolvedModel, err := l.engine.ResolveProvider(turnCtx, input.Model)
	if err != nil {
		l.emitter.Emit(Event{
			Type:      EventSessionError,
			SessionID: input.SessionID,
			Timestamp: time.Now(),
			Error:     err.Error(),
		})
		return state, err
	}
	if resolvedModel == "" {
		resolvedModel = input.Model
	}

	// Build tool definitions ONCE from DB.
	// If an interceptor is attached, use its BuildToolDefinitions which
	// includes both regular DB-backed tools and synthetic tools.
	var toolDefs []gw.ToolDefinition
	type toolDefBuilder interface {
		BuildToolDefinitions(ctx context.Context, tenantID string, functionNames []string) ([]gw.ToolDefinition, error)
	}
	if input.Interceptor != nil {
		if builder, ok := input.Interceptor.(toolDefBuilder); ok {
			toolDefs, err = builder.BuildToolDefinitions(turnCtx, input.TenantID, input.Tools)
			if err != nil {
				logger.WithFields(
					"agent_id", input.AgentID,
					"session_id", input.SessionID,
					"error", err.Error(),
				).Warn("failed to build tool definitions via interceptor, continuing without tools")
			}
		}
	}
	if len(toolDefs) == 0 && l.toolLoop != nil && l.toolLoop.IsEnabled() && len(input.Tools) > 0 {
		toolDefs, err = l.toolLoop.BuildToolDefinitions(turnCtx, input.TenantID, input.Tools)
		if err != nil {
			logger.WithFields(
				"agent_id", input.AgentID,
				"session_id", input.SessionID,
				"error", err.Error(),
			).Warn("failed to build tool definitions, continuing without tools")
		}
	}
	hasTools := len(toolDefs) > 0
	baseSystemPrompt := input.SystemPrompt
	digestPrompt := ""
	skillsManifest := ""
	memoryBlock := ""

	// Append system prompt if not already present
	if input.SystemPrompt != "" && len(state.Messages) == 0 {
		systemPrompt := baseSystemPrompt

		// Phase 4: Inject digest bulletin into system prompt
		if input.DigestBulletin != "" {
			digestPrompt = "## Agent Knowledge Bulletin\n" + input.DigestBulletin
			systemPrompt = systemPrompt + "\n\n" + digestPrompt
		}

		// Inject skills manifest into system prompt (skills content is loaded
		// dynamically via the use_skill tool from the sandbox filesystem).
		if len(input.Skills) > 0 {
			var sb strings.Builder
			sb.WriteString("## Available Skills\n")
			sb.WriteString("When a request matches one of these skills, call `use_skill` to load its full instructions before proceeding.\n")
			for _, s := range input.Skills {
				desc := s.Description
				if desc == "" {
					desc = s.Name
				}
				sb.WriteString(fmt.Sprintf("- %s: %s\n", s.Name, desc))
			}
			sb.WriteString("Use `use_skill` instead of reading skill files directly with other tools.")
			skillsManifest = sb.String()
			systemPrompt += "\n\n" + skillsManifest
		}

		// Auto-retrieve persistent memories and append to system prompt
		if input.MemoryProvider != nil {
			memCtx, memCancel := context.WithTimeout(ctx, 5*time.Second)
			retrievedMemoryBlock, memoryIDs, memErr := input.MemoryProvider.RetrieveContext(
				memCtx, input.AgentID, input.TenantID, input.UserInput, input.UserID,
			)
			memCancel()
			if memErr != nil {
				logger.WithFields(
					"agent_id", input.AgentID,
					"session_id", input.SessionID,
					"error", memErr.Error(),
				).Warn("agent loop: memory retrieval failed, continuing without memory")
			} else if retrievedMemoryBlock != "" {
				memoryBlock = retrievedMemoryBlock
				systemPrompt = systemPrompt + "\n" + memoryBlock
				// Track access in background
				if len(memoryIDs) > 0 {
					go func() {
						accessCtx, accessCancel := context.WithTimeout(context.Background(), 5*time.Second)
						defer accessCancel()
						_ = input.MemoryProvider.IncrementAccess(accessCtx, input.TenantID, memoryIDs)
					}()
				}
			}
		}

		state.Messages = append(state.Messages, gw.Message{
			Role:    gw.RoleSystem,
			Content: []gw.ContentPart{{Type: "text", Text: strPtr(systemPrompt)}},
		})
	}

	// Append current user input (skip for auto-continue turns where the
	// continuation prompt is injected as a system message instead)
	if input.UserInput != "" {
		if hasPriorUserTurn(state.Messages) {
			state.Messages = append(state.Messages, gw.Message{
				Role: gw.RoleSystem,
				Content: []gw.ContentPart{{
					Type: "text",
					Text: strPtr("Treat the most recent user message as the active objective. Do not continue unrelated earlier requests unless the user explicitly asks you to return to them. If you already completed the latest request, report that result clearly instead of summarizing older unfinished tasks."),
				}},
			})
		}
		state.Messages = append(state.Messages, gw.Message{
			Role:    gw.RoleUser,
			Content: []gw.ContentPart{{Type: "text", Text: strPtr(input.UserInput)}},
		})
	}

	state.TurnNumber++
	state.IterationCount = 0
	state.ToolResults = nil // Reset per turn; populated during tool execution
	state.TurnUsage = UsageDelta{}
	// Everything appended from here on belongs to this turn — record the
	// boundary so the checkpoint can reconstruct the turn's ordered timeline.
	state.TurnStartIndex = len(state.Messages)
	lastToolSignature := ""
	repeatedToolSignatureCount := 0
	var sandboxParentStart time.Time
	forceRetryAfterToolFailure := false
	pendingExposedURLs := make(map[string]struct{})
	urlAnnouncementAttempted := false
	turnToolCalls := 0
	turnSandboxToolCalls := 0
	turnToolErrors := 0
	emptyResponseRetries := 0
	const maxEmptyResponseRetries = 3
	askUserBackstopRetries := 0
	const maxAskUserBackstopRetries = 2
	awaitingPostAskUserAction := false
	askUserContinuationRetries := 0
	const maxAskUserContinuationRetries = 2
	lengthRetries := 0
	const maxLengthRetries = 3

	// Start OTEL turn span
	turnCtxSpan, turnSpan := telemetry.StartAgentTurnSpan(turnCtx, input.SessionID, int(state.TurnNumber))

	// Set tenant.id, model on turn span for metrics aggregation
	if input.TenantID != "" {
		turnSpan.SetAttributes(attribute.String(attrs.TenantID, input.TenantID))
	}
	if input.Model != "" {
		turnSpan.SetAttributes(attribute.String(attrs.ModelRequested, input.Model))
	}

	// Capture the conversation-bytes-at-decision before this turn appends
	// any tool results; this is the input-side context size the LLM saw.
	turnContextBytesAtStart := approxMessagesBytes(state.Messages)

	defer func() {
		attrs.SetAgentTurnMetrics(turnSpan,
			int(state.IterationCount),
			int(state.TotalToolCalls),
			int64(state.TurnUsage.PromptTokens),
			int64(state.TurnUsage.CompletionTokens),
			int64(state.TurnUsage.TotalTokens),
			0,
		)
		attrs.SetAgentTurnToolSummary(
			turnSpan,
			turnToolCalls,
			turnSandboxToolCalls,
			turnToolCalls-turnSandboxToolCalls,
			turnToolErrors,
		)
		attrs.SetAgentTurnSnapshot(turnSpan, attrs.AgentTurnSnapshot{
			ContextBytesAtStart: turnContextBytesAtStart,
			ToolResultSummary:   summarizeToolResults(state.ToolResults),
			// PromptTemplateID, PromptVersion, ReasoningTextHash intentionally
			// left blank — populated by a follow-up PR when prompt-template
			// resolution + reasoning-text capture land. Empty values are
			// skipped by the setter.
		})
		turnSpan.End()
	}()
	// Replace turnCtx with span context so child spans are nested under the turn
	turnCtx = turnCtxSpan

	turnStartData := map[string]interface{}{
		"model": input.Model,
	}
	if input.UserInput != "" {
		turnStartData["user_input"] = input.UserInput
	}
	l.emitter.Emit(Event{
		Type:       EventTurnStart,
		SessionID:  input.SessionID,
		TurnNumber: state.TurnNumber,
		Timestamp:  time.Now(),
		Data:       turnStartData,
	})

	// Pre-turn policy check: evaluate budget, rate, and input policies before any LLM call
	if input.PolicyEvaluator != nil {
		preTurnDecisions := input.PolicyEvaluator.EvaluatePhase(policy.PhasePRETURN, &policy.EvalContext{
			AgentID:            input.AgentID,
			SessionID:          input.SessionID,
			TenantID:           input.TenantID,
			TurnNumber:         state.TurnNumber,
			UserInput:          input.UserInput,
			SessionTotalTokens: int64(state.PriorSessionTokens) + int64(state.CumulativeUsage.TotalTokens),
		})
		for _, d := range preTurnDecisions {
			l.emitter.Emit(Event{
				Type:       EventPolicyDecision,
				SessionID:  input.SessionID,
				TurnNumber: state.TurnNumber,
				Timestamp:  time.Now(),
				Data: map[string]interface{}{
					"policy":   d.PolicyName,
					"action":   string(d.Action),
					"severity": d.Severity,
					"details":  d.Details,
					"phase":    "pre_turn",
				},
			})
		}
		if policy.HasBlockingDecision(preTurnDecisions) {
			state.FinishReason = "policy_blocked"
			state.Done = true
			var blockedBy string
			for _, d := range preTurnDecisions {
				if d.Action == policy.ActionBLOCK {
					blockedBy = d.PolicyName
					break
				}
			}
			state.LastAssistantText = fmt.Sprintf("Turn blocked by policy: %s", blockedBy)
			logger.WithFields(
				"session_id", input.SessionID,
				"policy", blockedBy,
			).Warn("loop: turn blocked by pre-turn policy")
			return state, nil
		}
	}

	// Tracks partial streamed text across chunks so we can preserve it on cancellation/error
	var partialStreamedText strings.Builder

	// Iteration loop
	for {
		// Check context cancellation
		if err := turnCtx.Err(); err != nil {
			state.FinishReason = "timeout"
			state.Done = true
			l.emitter.Emit(Event{
				Type:       EventTermination,
				SessionID:  input.SessionID,
				TurnNumber: state.TurnNumber,
				Timestamp:  time.Now(),
				Reason:     "timeout",
			})
			break
		}

		// Check max iterations (typed max_steps uses a distinct finish reason).
		if state.IterationCount >= l.config.MaxIterations {
			terminationReason := "max_iterations"
			if input.MaxSteps > 0 && l.config.MaxIterations == input.MaxSteps {
				terminationReason = "max_steps"
			}
			state.FinishReason = terminationReason
			state.Done = true
			l.emitter.Emit(Event{
				Type:       EventTermination,
				SessionID:  input.SessionID,
				TurnNumber: state.TurnNumber,
				Timestamp:  time.Now(),
				Reason:     terminationReason,
			})
			break
		}

		// Check max tool calls (0 = unlimited)
		if l.config.MaxToolCallsPerTurn > 0 && state.TotalToolCalls >= l.config.MaxToolCallsPerTurn {
			state.FinishReason = "max_tool_calls"
			state.Done = true
			l.emitter.Emit(Event{
				Type:       EventTermination,
				SessionID:  input.SessionID,
				TurnNumber: state.TurnNumber,
				Timestamp:  time.Now(),
				Reason:     "max_tool_calls",
			})
			break
		}

		// Check session-level token budget
		if l.config.SessionTokenBudget > 0 && int64(state.PriorSessionTokens)+int64(state.CumulativeUsage.TotalTokens) >= l.config.SessionTokenBudget {
			state.FinishReason = "token_budget_exhausted"
			state.Done = true
			l.emitter.Emit(Event{
				Type:       EventTermination,
				SessionID:  input.SessionID,
				TurnNumber: state.TurnNumber,
				Timestamp:  time.Now(),
				Reason:     "token_budget_exhausted",
			})
			break
		}

		state.IterationCount++
		partialStreamedText.Reset()

		// Phase 3: Drain CompactCh — apply any pending compaction requests
		if input.CompactCh != nil {
			for {
				select {
				case req, ok := <-input.CompactCh:
					if !ok {
						break
					}
					if req.ReplaceEnd <= len(state.Messages) && req.ReplaceStart >= 0 && req.ReplaceStart < req.ReplaceEnd {
						// Replace the message range with the summary
						newMessages := make([]gw.Message, 0, len(state.Messages)-req.ReplaceEnd+req.ReplaceStart+1)
						newMessages = append(newMessages, state.Messages[:req.ReplaceStart]...)
						newMessages = append(newMessages, req.SummaryMessage)
						newMessages = append(newMessages, state.Messages[req.ReplaceEnd:]...)
						state.Messages = newMessages

						l.emitter.Emit(Event{
							Type:       EventCompactionApplied,
							SessionID:  input.SessionID,
							TurnNumber: state.TurnNumber,
							Timestamp:  time.Now(),
							Data: map[string]interface{}{
								"tier":         string(req.Tier),
								"freed_tokens": req.FreedTokens,
								"new_count":    len(state.Messages),
							},
						})

						logger.WithFields(
							"session_id", input.SessionID,
							"tier", string(req.Tier),
							"freed_tokens", req.FreedTokens,
						).Info("loop: applied context compaction")
					}
				default:
					goto compactDone
				}
			}
		compactDone:
		}

		// Compact verbose tool results from prior iterations. The LLM has already
		// seen and acted on these results — keeping the full text wastes context.
		// We truncate tool result content older than the last 6 messages to a
		// short prefix, preserving the most recent results in full.
		compactToolResults(state.Messages, 6)

		// Truncate conversation history to fit within the configured limit.
		// Always preserves the system prompt (first message) and the most recent
		// messages to stay within LLM context window limits.
		messagesToSend := state.Messages
		if l.config.MaxHistoryMessages > 0 && int32(len(messagesToSend)) > l.config.MaxHistoryMessages {
			if len(messagesToSend) > 0 && messagesToSend[0].Role == gw.RoleSystem {
				// Keep system prompt + the newest maxHistoryMessages-1 messages.
				tail := messagesToSend[int32(len(messagesToSend))-l.config.MaxHistoryMessages+1:]
				truncated := make([]gw.Message, 0, l.config.MaxHistoryMessages)
				truncated = append(truncated, messagesToSend[0])
				truncated = append(truncated, tail...)
				messagesToSend = truncated
			} else {
				// Keep the newest maxHistoryMessages messages.
				messagesToSend = messagesToSend[int32(len(messagesToSend))-l.config.MaxHistoryMessages:]
			}
		}

		// Build request
		req := l.engine.BuildChatRequest(resolvedModel, messagesToSend, toolDefs, input.Sampling)
		// Only enable the Responses API when the resolved provider actually
		// supports it (i.e. OpenAI). Stale config from a previous model
		// selection should not route calls to OpenAI's endpoint.
		if input.APIType == "responses" {
			if named, ok := chatProvider.(interface{ Name() string }); ok && named.Name() == "openai" {
				req.UseResponsesAPI = true
			}
		}

		promptBreakdown := buildPromptTokenBreakdown(messagesToSend, input.UserInput, baseSystemPrompt, digestPrompt, skillsManifest, memoryBlock)

		l.emitter.Emit(Event{
			Type:       EventLLMStart,
			SessionID:  input.SessionID,
			TurnNumber: state.TurnNumber,
			Timestamp:  time.Now(),
			Data: map[string]interface{}{
				"iteration":               state.IterationCount,
				"message_count":           len(state.Messages),
				"estimated_prompt_tokens": promptBreakdown["estimated_total_tokens"],
				"prompt_breakdown":        promptBreakdown,
			},
		})
		logger.WithFields(
			"session_id", input.SessionID,
			"iteration", state.IterationCount,
			"prompt_breakdown", promptBreakdown,
		).Debug("agent loop: prompt token breakdown")

		// Execute LLM call (with optional fallback)
		llmStart := time.Now()
		var resp *gw.ChatCompletionResponse

		onChunk := func(textDelta string) error {
			partialStreamedText.WriteString(textDelta)
			l.emitter.Emit(Event{
				Type:       EventLLMChunk,
				SessionID:  input.SessionID,
				TurnNumber: state.TurnNumber,
				Timestamp:  time.Now(),
				TextDelta:  textDelta,
			})
			return nil
		}

		var usedProvider gw.ChatProvider

		// Retry loop for rate-limit errors: wait and retry before terminating.
		const maxRateLimitRetries = 3
		var rateLimitBackoffs = [3]time.Duration{15 * time.Second, 30 * time.Second, 60 * time.Second}
		for rateLimitAttempt := 0; ; rateLimitAttempt++ {
			resp, usedProvider, err = l.callWithFallback(turnCtx, chatProvider, input.Model, req, input, state, onChunk)
			if usedProvider != nil {
				chatProvider = usedProvider
			}
			if err == nil || !retrypolicy.IsRateLimitError(err) || rateLimitAttempt >= maxRateLimitRetries {
				break
			}
			// Rate-limited: wait and retry
			backoff := rateLimitBackoffs[rateLimitAttempt]
			logger.WithFields(
				"session_id", input.SessionID,
				"attempt", rateLimitAttempt+1,
				"backoff_seconds", int(backoff.Seconds()),
				"error", err.Error(),
			).Warn("rate limit hit, waiting before retry")
			l.emitter.Emit(Event{
				Type:       EventLLMEnd,
				SessionID:  input.SessionID,
				TurnNumber: state.TurnNumber,
				Timestamp:  time.Now(),
				Error:      fmt.Sprintf("rate limited, retrying in %ds (attempt %d/%d)", int(backoff.Seconds()), rateLimitAttempt+1, maxRateLimitRetries),
			})
			select {
			case <-turnCtx.Done():
				err = turnCtx.Err()
			case <-time.After(backoff):
			}
			if turnCtx.Err() != nil {
				break
			}
		}

		llmDuration := time.Since(llmStart).Milliseconds()

		if err != nil {
			// Capture any partial streamed text before returning
			if partial := partialStreamedText.String(); partial != "" {
				state.LastAssistantText = partial
				state.Messages = append(state.Messages, gw.Message{
					Role:    gw.RoleAssistant,
					Content: []gw.ContentPart{{Type: "text", Text: strPtr(partial)}},
				})
			}

			if turnCtx.Err() != nil {
				state.FinishReason = finishReasonFromContext(turnCtx)
				state.Done = true
				return state, nil
			}
			l.emitter.Emit(Event{
				Type:       EventLLMEnd,
				SessionID:  input.SessionID,
				TurnNumber: state.TurnNumber,
				Timestamp:  time.Now(),
				Error:      err.Error(),
			})
			state.FinishReason = "error"
			state.Done = true
			return state, fmt.Errorf("LLM call failed: %w", err)
		}

		// Accumulate usage. Cache buckets (read/write) live on PromptDetails
		// and are accumulated separately so the turn-level breakdown survives
		// multi-call turns.
		var callCacheRead, callCacheWrite int
		if resp.Usage.PromptDetails != nil {
			callCacheRead = resp.Usage.PromptDetails.CacheReadTokens
			callCacheWrite = resp.Usage.PromptDetails.CacheWriteTokens
		}
		state.TurnUsage.PromptTokens += resp.Usage.PromptTokens
		state.TurnUsage.CompletionTokens += resp.Usage.CompletionTokens
		state.TurnUsage.TotalTokens += resp.Usage.TotalTokens
		state.TurnUsage.CacheReadTokens += callCacheRead
		state.TurnUsage.CacheWriteTokens += callCacheWrite
		state.CumulativeUsage.PromptTokens += resp.Usage.PromptTokens
		state.CumulativeUsage.CompletionTokens += resp.Usage.CompletionTokens
		state.CumulativeUsage.TotalTokens += resp.Usage.TotalTokens
		state.CumulativeUsage.CacheReadTokens += callCacheRead
		state.CumulativeUsage.CacheWriteTokens += callCacheWrite

		// Mirror provider response onto the agent turn span so the trace
		// detail panel can render Output / response model / finish reason
		// for the turn even when no provider span exists (provider hangs,
		// tracing config not loaded, sampler dropped the child span, etc.).
		// Without this, trace-summary.ts's per-span aggregation reads zeros
		// from the agent turn span.
		if turnSpan != nil && resp != nil {
			attrs.SetLLMResponse(turnSpan, resp.Model, resp.ID, attrs.AnalyzeResponse(*resp))
			attrs.SetLLMResponsePayload(turnSpan, resp.Choices)
			attrs.SetLLMTokens(turnSpan,
				int64(resp.Usage.PromptTokens),
				int64(resp.Usage.CompletionTokens),
				int64(resp.Usage.TotalTokens),
			)
			// Stamp model.served + provider so trace_metrics_hourly's
			// (tenant, period, model, provider, ...) sort key on agent
			// turn rows matches the provider span's row. Without this,
			// agent traffic shows requests under provider='' and tokens
			// under provider='openai' as two separate rows — when the
			// metrics dashboard groups by provider it filters out empty
			// providers, dropping the request count entirely.
			if resp.Model != "" {
				turnSpan.SetAttributes(attribute.String(attrs.ModelServed, resp.Model))
			}
			if named, ok := chatProvider.(interface{ Name() string }); ok {
				if name := named.Name(); name != "" {
					turnSpan.SetAttributes(attribute.String(attrs.Provider, name))
				}
			}
			if resp.Usage.PromptDetails != nil || resp.Usage.CompletionDetails != nil {
				var pd, cd *attrs.TokenBreakdown
				if resp.Usage.PromptDetails != nil {
					pd = &attrs.TokenBreakdown{
						CachedTokens:     int64(resp.Usage.PromptDetails.CachedTokens),
						CacheReadTokens:  int64(resp.Usage.PromptDetails.CacheReadTokens),
						CacheWriteTokens: int64(resp.Usage.PromptDetails.CacheWriteTokens),
						AudioTokens:      int64(resp.Usage.PromptDetails.AudioTokens),
					}
				}
				if resp.Usage.CompletionDetails != nil {
					cd = &attrs.TokenBreakdown{
						ReasoningTokens: int64(resp.Usage.CompletionDetails.ReasoningTokens),
						AudioTokens:     int64(resp.Usage.CompletionDetails.AudioTokens),
					}
				}
				attrs.SetLLMTokenBreakdown(turnSpan, pd, cd, nil)
			}
		}

		// Phase 3: Notify Monitor of token usage (non-blocking, runs in background)
		if input.Monitor != nil {
			go input.Monitor.ObserveTurnEnd(turnCtx, state.Messages, resp.Usage.PromptTokens)
		}

		l.emitter.Emit(Event{
			Type:       EventLLMEnd,
			SessionID:  input.SessionID,
			TurnNumber: state.TurnNumber,
			Timestamp:  time.Now(),
			Usage: &UsageDelta{
				PromptTokens:     resp.Usage.PromptTokens,
				CompletionTokens: resp.Usage.CompletionTokens,
				TotalTokens:      resp.Usage.TotalTokens,
				CacheReadTokens:  callCacheRead,
				CacheWriteTokens: callCacheWrite,
			},
			Data: map[string]interface{}{
				"latency_ms": llmDuration,
			},
		})

		// Check if tool execution is needed.
		// Enter the tool loop when:
		// 1. Regular tools are configured and toolLoop indicates tool calls, OR
		// 2. Synthetic tools exist (via interceptor) and response has tool calls
		// Without this guard, a model that returns finish_reason "tool_calls"
		// (e.g. via streaming artefacts) will spin for max_iterations doing nothing.
		hasRegularTools := l.toolLoop != nil && l.toolLoop.IsEnabled()
		regularToolsNeeded := hasRegularTools && l.toolLoop.ShouldExecuteToolLoop(resp)
		responseHasToolCalls := len(resp.Choices) > 0 && len(resp.Choices[0].Message.ToolCalls) > 0
		syntheticToolsNeeded := input.Interceptor != nil && responseHasToolCalls

		shouldLoop := hasTools && (regularToolsNeeded || syntheticToolsNeeded)

		// Handle finish_reason "length" — the LLM output was truncated by max_tokens.
		// Tool call JSON will be malformed. Discard the truncated response and retry
		// with a nudge to produce shorter output.
		if len(resp.Choices) > 0 && resp.Choices[0].FinishReason == "length" && responseHasToolCalls {
			lengthRetries++
			if lengthRetries <= maxLengthRetries {
				logger.WithFields(
					"session_id", input.SessionID,
					"iteration", state.IterationCount,
					"length_retry", lengthRetries,
					"tool_call_count", len(resp.Choices[0].Message.ToolCalls),
				).Warn("loop: LLM output truncated (finish_reason=length), retrying with shorter output hint")

				state.Messages = append(state.Messages, gw.Message{
					Role: gw.RoleUser,
					Content: []gw.ContentPart{{Type: "text", Text: strPtr(
						"Your previous response was truncated because it exceeded the output token limit. " +
							"Your tool call arguments were cut off and could not be parsed. " +
							"Please retry with shorter content. If you are writing a file, break it into smaller chunks using multiple tool calls.",
					)}},
				})
				continue
			}
			logger.WithFields(
				"session_id", input.SessionID,
				"length_retry", lengthRetries,
			).Warn("loop: max length retries exceeded, attempting to proceed with truncated tool calls")
		}

		// Diagnostic: log the tool loop decision so we can trace hangs
		if len(resp.Choices) > 0 {
			var toolNames []string
			for _, tc := range resp.Choices[0].Message.ToolCalls {
				toolNames = append(toolNames, tc.Function.Name)
			}
			logger.WithFields(
				"session_id", input.SessionID,
				"iteration", state.IterationCount,
				"finish_reason", resp.Choices[0].FinishReason,
				"tool_call_count", len(resp.Choices[0].Message.ToolCalls),
				"tool_names", strings.Join(toolNames, ","),
				"should_loop", shouldLoop,
				"has_tools", hasTools,
				"regular_tools_needed", regularToolsNeeded,
				"synthetic_tools_needed", syntheticToolsNeeded,
				"has_interceptor", input.Interceptor != nil,
			).Info("loop: tool loop decision")
		}

		if !shouldLoop {
			// Extract final assistant text
			hasText := false
			state.LastAssistantText = ""
			if len(resp.Choices) > 0 {
				state.FinishReason = resp.Choices[0].FinishReason
				state.LastAssistantText = messageText(resp.Choices[0].Message)
				hasText = strings.TrimSpace(state.LastAssistantText) != ""

				// Empty response guard: if the LLM returned finish_reason=stop
				// with no text and no tool calls, it's a degenerate response.
				// Nudge the LLM to keep working instead of terminating.
				if !hasText && len(resp.Choices[0].Message.ToolCalls) == 0 && hasTools && emptyResponseRetries < maxEmptyResponseRetries {
					emptyResponseRetries++
					logger.WithFields(
						"session_id", input.SessionID,
						"iteration", state.IterationCount,
						"finish_reason", state.FinishReason,
						"empty_retry", emptyResponseRetries,
					).Warn("loop: LLM returned empty response, nudging to continue")

					state.Messages = append(state.Messages, gw.Message{
						Role:    gw.RoleUser,
						Content: []gw.ContentPart{{Type: "text", Text: strPtr("Your previous response was empty. Please continue working on the task. Use your available tools to make progress, or provide a status update if you're done.")}},
					})
					continue
				}

				if hasText && len(resp.Choices[0].Message.ToolCalls) == 0 && askUserBackstopRetries < maxAskUserBackstopRetries && shouldBackstopAskUser(input, state.LastAssistantText) {
					askUserBackstopRetries++
					retryMsg := "You asked the user a blocking question in plain assistant text. Do not ask clarifying questions directly in assistant output. Re-issue the question as an ask_user tool call instead. When there are a few clear choices, include them in the options array."
					logger.WithFields(
						"session_id", input.SessionID,
						"iteration", state.IterationCount,
						"ask_user_backstop_retry", askUserBackstopRetries,
					).Warn("loop: assistant asked for user input in plain text, retrying with ask_user backstop")

					state.Messages = append(state.Messages, gw.Message{
						Role:    gw.RoleSystem,
						Content: []gw.ContentPart{{Type: "text", Text: strPtr(retryMsg)}},
					})
					l.emitter.Emit(Event{
						Type:       EventSteerReceived,
						SessionID:  input.SessionID,
						TurnNumber: state.TurnNumber,
						Timestamp:  time.Now(),
						Data: map[string]interface{}{
							"role":    "system",
							"content": retryMsg,
						},
					})
					continue
				}

				if len(resp.Choices[0].Message.ToolCalls) == 0 && awaitingPostAskUserAction && askUserContinuationRetries < maxAskUserContinuationRetries {
					askUserContinuationRetries++
					retryMsg := "You now have the user's answer. Continue the task by taking the next concrete action immediately. If a scheduling or automation tool is available, use it now (for example create_trigger, schedule_cron, or a workflow-related tool). Do not stop after collecting user input unless you are truly blocked and must explain the blocker."
					logger.WithFields(
						"session_id", input.SessionID,
						"iteration", state.IterationCount,
						"ask_user_continuation_retry", askUserContinuationRetries,
						"finish_reason", state.FinishReason,
					).Warn("loop: model stopped after ask_user response, nudging to continue with next action")

					state.Messages = append(state.Messages, gw.Message{
						Role:    gw.RoleSystem,
						Content: []gw.ContentPart{{Type: "text", Text: strPtr(retryMsg)}},
					})
					l.emitter.Emit(Event{
						Type:       EventSteerReceived,
						SessionID:  input.SessionID,
						TurnNumber: state.TurnNumber,
						Timestamp:  time.Now(),
						Data: map[string]interface{}{
							"role":    "system",
							"content": retryMsg,
							"source":  "ask_user_continuation_backstop",
						},
					})
					continue
				}

				logger.WithFields(
					"session_id", input.SessionID,
					"iteration", state.IterationCount,
					"finish_reason", state.FinishReason,
					"has_text", hasText,
					"text_len", len(state.LastAssistantText),
				).Info("loop: breaking, no tool execution needed")
				// Append assistant message to history
				state.Messages = append(state.Messages, resp.Choices[0].Message)
			}
			state.Done = true
			break
		}

		// Tool execution phase - extract tool calls from response
		var toolCalls []toolloop.ToolCallMessage
		if l.toolLoop != nil {
			toolCalls = l.toolLoop.ExtractToolCalls(resp)
		} else if len(resp.Choices) > 0 && len(resp.Choices[0].Message.ToolCalls) > 0 {
			// Fallback for synthetic-only tools: extract directly from response
			for _, tc := range resp.Choices[0].Message.ToolCalls {
				toolCalls = append(toolCalls, toolloop.ToolCallMessage{
					ID:   tc.ID,
					Type: tc.Type,
					Function: toolloop.ToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				})
			}
		}

		// Defensive guard: if finish_reason indicated tool calls but none could
		// be extracted (e.g. streaming parser did not assemble ToolCalls), treat
		// the response as a final text reply to avoid an infinite empty loop.
		if len(toolCalls) == 0 {
			currentAssistantText := messageText(resp.Choices[0].Message)
			if len(pendingExposedURLs) > 0 {
				urls := sortedURLs(pendingExposedURLs)
				if messageMentionsAnyURL(currentAssistantText, urls) {
					pendingExposedURLs = make(map[string]struct{})
					urlAnnouncementAttempted = false
				} else if hasTools && !urlAnnouncementAttempted {
					urlAnnouncementAttempted = true
					retryMsg := "Before ending this turn, provide a concise user-facing update listing these available sandbox URL(s):\n" + strings.Join(urls, "\n")
					state.Messages = append(state.Messages, gw.Message{
						Role:    gw.RoleSystem,
						Content: []gw.ContentPart{{Type: "text", Text: strPtr(retryMsg)}},
					})
					l.emitter.Emit(Event{
						Type:       EventSteerReceived,
						SessionID:  input.SessionID,
						TurnNumber: state.TurnNumber,
						Timestamp:  time.Now(),
						Data: map[string]interface{}{
							"role":    "system",
							"content": retryMsg,
						},
					})
					continue
				}
			}

			if forceRetryAfterToolFailure && hasTools {
				forceRetryAfterToolFailure = false
				retryMsg := "Previous tool execution failed. Continue troubleshooting with additional tool calls before ending this turn. If truly blocked, explain the blocker and ask for user input."
				state.Messages = append(state.Messages, gw.Message{
					Role:    gw.RoleSystem,
					Content: []gw.ContentPart{{Type: "text", Text: strPtr(retryMsg)}},
				})
				l.emitter.Emit(Event{
					Type:       EventSteerReceived,
					SessionID:  input.SessionID,
					TurnNumber: state.TurnNumber,
					Timestamp:  time.Now(),
					Data: map[string]interface{}{
						"role":    "system",
						"content": retryMsg,
					},
				})
				continue
			}

			logger.WithFields(
				"session_id", input.SessionID,
				"iteration", state.IterationCount,
				"finish_reason", resp.Choices[0].FinishReason,
			).Warn("tool loop: finish_reason indicates tool_calls but none could be extracted, breaking")

			if len(resp.Choices) > 0 {
				state.FinishReason = resp.Choices[0].FinishReason
				state.LastAssistantText = messageText(resp.Choices[0].Message)
				state.Messages = append(state.Messages, resp.Choices[0].Message)
			}
			state.Done = true
			break
		}

		// Guard: if the model repeats the exact same tool-call set for multiple
		// iterations, stop early to avoid wasting tokens/time in a tight loop.
		currentToolSignature := toolCallSignature(toolCalls)
		if currentToolSignature != "" {
			if currentToolSignature == lastToolSignature {
				repeatedToolSignatureCount++
			} else {
				lastToolSignature = currentToolSignature
				repeatedToolSignatureCount = 1
			}
		}
		if repeatedToolSignatureCount >= maxRepeatedToolSignatures {
			msg := fmt.Sprintf("Tool loop stopped: identical tool calls were repeated %d times without progress.", repeatedToolSignatureCount)
			logger.WithFields(
				"session_id", input.SessionID,
				"iteration", state.IterationCount,
				"repeats", repeatedToolSignatureCount,
			).Warn("tool loop: repeated identical tool calls, terminating iteration loop")

			state.FinishReason = "tool_loop_stalled"
			state.LastAssistantText = msg
			state.Messages = append(state.Messages, gw.Message{
				Role:    gw.RoleAssistant,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr(msg)}},
			})
			state.Done = true
			break
		}

		state.TotalToolCalls += int32(len(toolCalls))

		// Validate tool argument sizes
		for i, tc := range toolCalls {
			if len(tc.Function.Arguments) > maxToolArgSize {
				logger.WithFields(
					"session_id", input.SessionID,
					"tool_name", tc.Function.Name,
					"arg_size", len(tc.Function.Arguments),
				).Warn("tool argument exceeds size limit, truncating")
				// Replace with error result instead of executing
				errText := fmt.Sprintf("Tool call rejected: arguments exceed maximum size of %d bytes", maxToolArgSize)
				state.Messages = append(state.Messages, resp.Choices[0].Message)
				state.Messages = append(state.Messages, gw.Message{
					Role:       gw.RoleTool,
					ToolCallID: tc.ID,
					Content:    []gw.ContentPart{{Type: "text", Text: strPtr(errText)}},
				})
				// Remove the oversized tool call so it doesn't get executed
				toolCalls = append(toolCalls[:i], toolCalls[i+1:]...)
				if len(toolCalls) == 0 {
					continue
				}
			}
		}

		// ================================================================
		// HITL Approval Gate: check if any tool calls need human approval
		// ================================================================
		if input.HITLConfig != nil && input.ApprovalCh != nil && input.DecisionCh != nil {
			// Convert toolloop.ToolCallMessage to gw.ToolCall for the approval filter.
			gwToolCalls := make([]gw.ToolCall, len(toolCalls))
			for i, tc := range toolCalls {
				gwToolCalls[i] = gw.ToolCall{
					ID:   tc.ID,
					Type: tc.Type,
					Function: gw.ToolCallFunction{
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					},
				}
			}
			needsApproval := FilterToolCallsNeedingApproval(gwToolCalls, input.HITLConfig)
			if len(needsApproval) > 0 {
				reviewID := uuid.New().String()
				approvalTimeout := time.Duration(input.HITLConfig.TimeoutSeconds) * time.Second
				expiresAt := time.Now().Add(approvalTimeout)

				// Emit approval.requested event (reaches client via stream)
				l.emitter.Emit(Event{
					Type:       EventApprovalRequested,
					SessionID:  input.SessionID,
					TurnNumber: state.TurnNumber,
					Timestamp:  time.Now(),
					ReviewID:   reviewID,
					Data: map[string]interface{}{
						"tool_calls":      needsApproval,
						"timeout_seconds": input.HITLConfig.TimeoutSeconds,
						"default_action":  input.HITLConfig.DefaultAction,
					},
				})

				// Send to SessionManager for DB persistence (blocking, cancellable)
				select {
				case input.ApprovalCh <- ApprovalRequest{
					ReviewID:   reviewID,
					SessionID:  input.SessionID,
					TenantID:   input.TenantID,
					AgentID:    input.AgentID,
					TurnNumber: state.TurnNumber,
					Iteration:  state.IterationCount,
					ToolCalls:  needsApproval,
					Config:     input.HITLConfig,
					ExpiresAt:  expiresAt,
				}:
				case <-turnCtx.Done():
					state.FinishReason = finishReasonFromContext(turnCtx)
					state.Done = true
					return state, nil
				}

				// Block waiting for human decision, timeout, or cancellation
				approvalTimer := time.NewTimer(approvalTimeout)
				heartbeatTicker := time.NewTicker(15 * time.Second)
				var decision ApprovalDecision
			waitLoop:
				for {
					select {
					case <-turnCtx.Done():
						approvalTimer.Stop()
						heartbeatTicker.Stop()
						l.emitter.Emit(Event{
							Type:       EventApprovalCancelled,
							SessionID:  input.SessionID,
							TurnNumber: state.TurnNumber,
							Timestamp:  time.Now(),
							ReviewID:   reviewID,
							Reason:     "context_cancelled",
						})
						state.FinishReason = finishReasonFromContext(turnCtx)
						state.Done = true
						return state, nil
					case decision = <-input.DecisionCh:
						break waitLoop
					case <-approvalTimer.C:
						decision = ApprovalDecision{
							ReviewID: reviewID,
							Action:   input.HITLConfig.DefaultAction,
							Reason:   "approval_timeout",
						}
						break waitLoop
					case <-heartbeatTicker.C:
						l.emitter.Emit(Event{
							Type:       EventApprovalHeartbeat,
							SessionID:  input.SessionID,
							TurnNumber: state.TurnNumber,
							Timestamp:  time.Now(),
							ReviewID:   reviewID,
						})
					}
				}
				approvalTimer.Stop()
				heartbeatTicker.Stop()

				// Emit approval.resolved event
				l.emitter.Emit(Event{
					Type:       EventApprovalResolved,
					SessionID:  input.SessionID,
					TurnNumber: state.TurnNumber,
					Timestamp:  time.Now(),
					ReviewID:   reviewID,
					Reason:     decision.Action,
					Data: map[string]interface{}{
						"action":      decision.Action,
						"reason":      decision.Reason,
						"resolved_by": decision.ResolvedBy,
					},
				})

				if decision.Action == "deny" {
					// Append the assistant message with tool_calls so the LLM sees them
					state.Messages = append(state.Messages, resp.Choices[0].Message)
					// Append synthetic denial tool results
					for _, tc := range needsApproval {
						denialText := "Tool call denied by human reviewer."
						if decision.Reason != "" {
							denialText += " Reason: " + decision.Reason
						}
						state.Messages = append(state.Messages, gw.Message{
							Role:       gw.RoleTool,
							ToolCallID: tc.ID,
							Content:    []gw.ContentPart{{Type: "text", Text: strPtr(denialText)}},
						})
					}
					continue // skip tool execution, LLM sees denial
				}
				// approved: fall through to existing tool execution
			}
		}

		// Intercept synthetic tool calls (spawn_agent, etc.) before gateway
		var syntheticResults []gw.Message
		var regularToolCalls []toolloop.ToolCallMessage
		iterationHadToolFailure := false
		seenSyntheticCalls := make(map[string]struct{})
		if input.Interceptor != nil {
			for _, tc := range toolCalls {
				if input.Interceptor.IsSyntheticTool(tc.Function.Name) {
					// Guard against duplicated synthetic tool calls with identical
					// arguments in the same LLM response (common with ask_user).
					syntheticKey := tc.Function.Name + ":" + tc.Function.Arguments
					if _, duplicated := seenSyntheticCalls[syntheticKey]; duplicated {
						result := fmt.Sprintf("Skipped duplicate synthetic tool call %q in the same iteration.", tc.Function.Name)
						startTime := time.Now()
						turnToolCalls++
						if isSandboxToolName(tc.Function.Name) {
							turnSandboxToolCalls++
						}
						l.emitter.Emit(Event{
							Type:       EventToolCallStart,
							SessionID:  input.SessionID,
							TurnNumber: state.TurnNumber,
							Timestamp:  startTime,
							ToolCallID: tc.ID,
							ToolName:   tc.Function.Name,
							ToolArgs:   tc.Function.Arguments,
						})
						endTime := time.Now()
						l.emitter.Emit(Event{
							Type:         EventToolCallEnd,
							SessionID:    input.SessionID,
							TurnNumber:   state.TurnNumber,
							Timestamp:    endTime,
							ToolCallID:   tc.ID,
							ToolName:     tc.Function.Name,
							ToolResult:   result,
							ToolSuccess:  true,
							ToolDuration: 0,
						})
						if state.ToolResults == nil {
							state.ToolResults = make(map[string]ToolResultMeta)
						}
						state.ToolResults[tc.ID] = ToolResultMeta{
							Result:     result,
							Success:    true,
							DurationMs: 0,
						}
						syntheticResults = append(syntheticResults, gw.Message{
							Role:       gw.RoleTool,
							ToolCallID: tc.ID,
							Content:    []gw.ContentPart{{Type: "text", Text: strPtr(result)}},
						})
						continue
					}
					seenSyntheticCalls[syntheticKey] = struct{}{}

					startTime := time.Now()
					if isSandboxToolName(tc.Function.Name) && sandboxParentStart.IsZero() {
						sandboxParentStart = startTime
					}
					turnToolCalls++
					if isSandboxToolName(tc.Function.Name) {
						turnSandboxToolCalls++
					}
					l.emitter.Emit(Event{
						Type:       EventToolCallStart,
						SessionID:  input.SessionID,
						TurnNumber: state.TurnNumber,
						Timestamp:  startTime,
						ToolCallID: tc.ID,
						ToolName:   tc.Function.Name,
						ToolArgs:   tc.Function.Arguments,
					})
					result, synthErr, synthDuration := executeTracedSyntheticTool(
						turnCtx,
						input.Interceptor,
						tc.ID,
						tc.Function.Name,
						tc.Function.Arguments,
					)
					success := synthErr == nil
					if tc.Function.Name == "ask_user" {
						if success {
							awaitingPostAskUserAction = true
							askUserContinuationRetries = 0
						} else {
							awaitingPostAskUserAction = false
						}
					} else if success {
						awaitingPostAskUserAction = false
					}
					if synthErr != nil {
						iterationHadToolFailure = true
						turnToolErrors++
						result = fmt.Sprintf("Error: %s", synthErr.Error())
					}
					endTime := time.Now()
					var sandboxParentDuration int64
					var eventData map[string]interface{}
					if isSandboxToolName(tc.Function.Name) {
						if sandboxParentStart.IsZero() {
							sandboxParentStart = endTime
						}
						sandboxParentDuration = endTime.Sub(sandboxParentStart).Milliseconds()
						eventData = map[string]interface{}{
							"sandbox_parent_duration_ms": sandboxParentDuration,
						}
					}
					l.emitter.Emit(Event{
						Type:         EventToolCallEnd,
						SessionID:    input.SessionID,
						TurnNumber:   state.TurnNumber,
						Timestamp:    endTime,
						ToolCallID:   tc.ID,
						ToolName:     tc.Function.Name,
						ToolResult:   result,
						ToolSuccess:  success,
						ToolDuration: synthDuration,
						Data:         eventData,
					})
					// Track result for persistence
					if state.ToolResults == nil {
						state.ToolResults = make(map[string]ToolResultMeta)
					}
					state.ToolResults[tc.ID] = ToolResultMeta{
						Result:                  result,
						Success:                 success,
						DurationMs:              synthDuration,
						SandboxParentDurationMs: sandboxParentDuration,
					}
					syntheticResults = append(syntheticResults, gw.Message{
						Role:       gw.RoleTool,
						ToolCallID: tc.ID,
						Content:    []gw.ContentPart{{Type: "text", Text: strPtr(result)}},
					})
				} else {
					awaitingPostAskUserAction = false
					regularToolCalls = append(regularToolCalls, tc)
				}
			}
		} else {
			regularToolCalls = toolCalls
		}

		// Start OTEL spans for each regular tool call and emit start events
		type toolSpanEntry struct {
			span  trace.Span
			start time.Time
		}
		toolSpans := make(map[string]toolSpanEntry, len(regularToolCalls))
		for _, tc := range regularToolCalls {
			startTime := time.Now()
			if isSandboxToolName(tc.Function.Name) && sandboxParentStart.IsZero() {
				sandboxParentStart = startTime
			}
			turnToolCalls++
			if isSandboxToolName(tc.Function.Name) {
				turnSandboxToolCalls++
			}
			_, tcSpan := telemetry.StartAgentToolCallSpan(turnCtx, tc.ID, tc.Function.Name)
			toolSpans[tc.ID] = toolSpanEntry{span: tcSpan, start: startTime}

			l.emitter.Emit(Event{
				Type:       EventToolCallStart,
				SessionID:  input.SessionID,
				TurnNumber: state.TurnNumber,
				Timestamp:  startTime,
				ToolCallID: tc.ID,
				ToolName:   tc.Function.Name,
				ToolArgs:   tc.Function.Arguments,
			})
		}

		// Execute regular tool calls via the tool loop manager
		var resultMessages []gw.Message
		var toolErr error
		execCtx := &toolloop.ExecutionContext{
			RequestID:     uuid.New().String(),
			TenantID:      input.TenantID,
			CorrelationID: input.SessionID,
		}

		if len(regularToolCalls) > 0 {
			resultMessages, toolErr = l.toolLoop.ExecuteToolLoop(turnCtx, execCtx, resp)
		}

		// Merge synthetic results with regular results
		if len(syntheticResults) > 0 {
			// The assistant message with tool_calls must be appended first
			state.Messages = append(state.Messages, resp.Choices[0].Message)
			state.Messages = append(state.Messages, syntheticResults...)
			// Append regular tool results (skip the first message if it's the
			// assistant message, because ExecuteToolLoop prepends the same
			// assistant message and we already added it above).
			if len(resultMessages) > 0 && resultMessages[0].Role == gw.RoleAssistant {
				state.Messages = append(state.Messages, resultMessages[1:]...)
			} else {
				state.Messages = append(state.Messages, resultMessages...)
			}
		}

		if toolErr != nil {
			logger.WithFields(
				"session_id", input.SessionID,
				"error", toolErr.Error(),
			).Warn("tool execution had errors")
		}

		// Emit tool call end events and close OTEL spans for regular tools
		for _, tc := range regularToolCalls {
			// Find result for this tool call
			var resultContent string
			var success bool
			for _, msg := range resultMessages {
				if msg.ToolCallID == tc.ID {
					for _, part := range msg.Content {
						if part.Text != nil {
							resultContent = *part.Text
						}
					}
					success = true
					break
				}
			}

			eventType := EventToolCallEnd
			if !success && toolErr != nil {
				eventType = EventToolCallError
			}
			if !success {
				iterationHadToolFailure = true
				turnToolErrors++
			}

			// Compute per-tool duration and end OTEL span
			var perToolDuration int64
			if entry, ok := toolSpans[tc.ID]; ok {
				perToolDuration = time.Since(entry.start).Milliseconds()
				attrs.SetAgentToolCallResult(entry.span, success, perToolDuration, len(resultContent))
				if !success && toolErr != nil {
					telemetry.RecordError(entry.span, toolErr)
				}
				entry.span.End()
			}
			endTime := time.Now()
			var sandboxParentDuration int64
			var eventData map[string]interface{}
			if isSandboxToolName(tc.Function.Name) {
				if sandboxParentStart.IsZero() {
					sandboxParentStart = endTime
				}
				sandboxParentDuration = endTime.Sub(sandboxParentStart).Milliseconds()
				eventData = map[string]interface{}{
					"sandbox_parent_duration_ms": sandboxParentDuration,
				}
			}

			l.emitter.Emit(Event{
				Type:         eventType,
				SessionID:    input.SessionID,
				TurnNumber:   state.TurnNumber,
				Timestamp:    endTime,
				ToolCallID:   tc.ID,
				ToolName:     tc.Function.Name,
				ToolResult:   resultContent,
				ToolSuccess:  success,
				ToolDuration: perToolDuration,
				Data:         eventData,
			})

			// Track result for persistence
			if state.ToolResults == nil {
				state.ToolResults = make(map[string]ToolResultMeta)
			}
			state.ToolResults[tc.ID] = ToolResultMeta{
				Result:                  resultContent,
				Success:                 success,
				DurationMs:              perToolDuration,
				SandboxParentDurationMs: sandboxParentDuration,
			}
		}

		// Append tool messages to conversation (skip if synthetic results already appended)
		if len(syntheticResults) == 0 {
			state.Messages = append(state.Messages, resultMessages...)
		}

		// Collect exposed sandbox port URLs from structured tool state
		// (not regex-matched from tool output, which would catch unrelated
		// URLs from npm, web_search, etc.).
		if input.Interceptor != nil {
			for _, url := range input.Interceptor.CollectExposedURLs() {
				pendingExposedURLs[url] = struct{}{}
			}
		}

		if iterationHadToolFailure {
			forceRetryAfterToolFailure = true
		} else {
			forceRetryAfterToolFailure = false
		}

		// Post-tool policy check
		if input.PolicyEvaluator != nil {
			postToolDecisions := input.PolicyEvaluator.EvaluatePhase(policy.PhasePOSTTOOL, &policy.EvalContext{
				AgentID:            input.AgentID,
				SessionID:          input.SessionID,
				TurnNumber:         state.TurnNumber,
				IterationCount:     state.IterationCount,
				ToolCalls:          turnToolCalls,
				SandboxToolCalls:   turnSandboxToolCalls,
				ToolErrors:         turnToolErrors,
				SessionTotalTokens: int64(state.PriorSessionTokens) + int64(state.CumulativeUsage.TotalTokens),
			})
			for _, d := range postToolDecisions {
				l.emitter.Emit(Event{
					Type:       EventPolicyDecision,
					SessionID:  input.SessionID,
					TurnNumber: state.TurnNumber,
					Timestamp:  time.Now(),
					Data: map[string]interface{}{
						"policy":   d.PolicyName,
						"action":   string(d.Action),
						"severity": d.Severity,
						"details":  d.Details,
						"phase":    "post_tool",
					},
				})
			}
			if policy.HasBlockingDecision(postToolDecisions) {
				var blockedBy string
				for _, d := range postToolDecisions {
					if d.Action == policy.ActionBLOCK {
						blockedBy = d.PolicyName
						break
					}
				}
				state.FinishReason = "policy_blocked"
				msg := fmt.Sprintf("Turn stopped by policy: %s", blockedBy)
				state.LastAssistantText = msg
				state.Messages = append(state.Messages, gw.Message{
					Role:    gw.RoleAssistant,
					Content: []gw.ContentPart{{Type: "text", Text: strPtr(msg)}},
				})
				state.Done = true
				break
			}
		}

		// Guard: if we entered tool execution but produced no tool results, the
		// loop cannot make forward progress and will likely spin.
		if len(syntheticResults) == 0 && len(resultMessages) == 0 && len(toolCalls) > 0 {
			msg := "Tool loop stopped: no tool results were produced for the requested tool calls."
			logger.WithFields(
				"session_id", input.SessionID,
				"iteration", state.IterationCount,
			).Warn("tool loop: no tool results produced, terminating iteration loop")
			state.FinishReason = "tool_loop_no_results"
			state.LastAssistantText = msg
			state.Messages = append(state.Messages, gw.Message{
				Role:    gw.RoleAssistant,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr(msg)}},
			})
			state.Done = true
			break
		}

		// Check steer channel (non-blocking)
		if input.SteerCh != nil {
			select {
			case steerMsg, ok := <-input.SteerCh:
				if ok {
					role := gw.RoleUser
					if steerMsg.Role == "system" {
						role = gw.RoleSystem
					}
					state.Messages = append(state.Messages, gw.Message{
						Role:    role,
						Content: []gw.ContentPart{{Type: "text", Text: strPtr(steerMsg.Content)}},
					})
					l.emitter.Emit(Event{
						Type:       EventSteerReceived,
						SessionID:  input.SessionID,
						TurnNumber: state.TurnNumber,
						Timestamp:  time.Now(),
						Data: map[string]interface{}{
							"role":    steerMsg.Role,
							"content": steerMsg.Content,
						},
					})
				}
			default:
				// No steer message available, continue
			}
		}

		// Phase 1: Drain JobResultCh — inject completed job results
		if input.JobResultCh != nil {
			for {
				select {
				case result, ok := <-input.JobResultCh:
					if !ok {
						goto jobDrainDone
					}
					var content string
					switch result.Status {
					case JobStatusCompleted:
						content = fmt.Sprintf("[Background job %s completed]\nResult: %s", result.JobID, result.Result)
					case JobStatusFailed:
						content = fmt.Sprintf("[Background job %s failed]\nError: %s", result.JobID, result.Error)
					case JobStatusCancelled:
						content = fmt.Sprintf("[Background job %s was cancelled]", result.JobID)
					default:
						continue
					}
					state.Messages = append(state.Messages, gw.Message{
						Role:    gw.RoleSystem,
						Content: []gw.ContentPart{{Type: "text", Text: strPtr(content)}},
					})
					l.emitter.Emit(Event{
						Type:       EventSteerReceived,
						SessionID:  input.SessionID,
						TurnNumber: state.TurnNumber,
						Timestamp:  time.Now(),
						Data: map[string]interface{}{
							"role":    "system",
							"content": content,
							"source":  "job_result",
							"job_id":  result.JobID,
						},
					})
				default:
					goto jobDrainDone
				}
			}
		jobDrainDone:
		}

		// Phase 2: Drain ForkResultCh — inject fork conclusions
		if input.ForkResultCh != nil {
			for {
				select {
				case result, ok := <-input.ForkResultCh:
					if !ok {
						goto forkDrainDone
					}
					var content string
					switch result.Status {
					case "completed":
						content = fmt.Sprintf("[Fork conclusion (re: %s)]\n%s", result.Instruction, result.Conclusion)
					case "failed":
						content = fmt.Sprintf("[Fork failed (re: %s)]\nError: %s", result.Instruction, result.Error)
					case "cancelled":
						content = fmt.Sprintf("[Fork cancelled (re: %s)]", result.Instruction)
					default:
						continue
					}
					state.Messages = append(state.Messages, gw.Message{
						Role:    gw.RoleSystem,
						Content: []gw.ContentPart{{Type: "text", Text: strPtr(content)}},
					})
					l.emitter.Emit(Event{
						Type:       EventSteerReceived,
						SessionID:  input.SessionID,
						TurnNumber: state.TurnNumber,
						Timestamp:  time.Now(),
						Data: map[string]interface{}{
							"role":    "system",
							"content": content,
							"source":  "fork_result",
							"fork_id": result.ForkID,
						},
					})
				default:
					goto forkDrainDone
				}
			}
		forkDrainDone:
		}

		// Phase 5: Drain PeerMessageCh — inject cross-agent messages
		if input.PeerMessageCh != nil {
			for {
				select {
				case msg, ok := <-input.PeerMessageCh:
					if !ok {
						goto peerDrainDone
					}
					var content string
					switch msg.MessageType {
					case "delegation":
						content = fmt.Sprintf("[Job delegation from @%s]: %s", msg.SenderName, msg.Content)
					case "job_result":
						content = fmt.Sprintf("[Job result from @%s]: %s", msg.SenderName, msg.Content)
					default:
						content = fmt.Sprintf("[Message from @%s]: %s", msg.SenderName, msg.Content)
					}
					state.Messages = append(state.Messages, gw.Message{
						Role:    gw.RoleSystem,
						Content: []gw.ContentPart{{Type: "text", Text: strPtr(content)}},
					})
					l.emitter.Emit(Event{
						Type:       EventSteerReceived,
						SessionID:  input.SessionID,
						TurnNumber: state.TurnNumber,
						Timestamp:  time.Now(),
						Data: map[string]interface{}{
							"role":            "system",
							"content":         content,
							"source":          "peer_message",
							"sender_agent_id": msg.SenderAgentID,
							"sender_name":     msg.SenderName,
							"message_type":    msg.MessageType,
						},
					})
				default:
					goto peerDrainDone
				}
			}
		peerDrainDone:
		}
	}

	// Build final tool calls JSON for persistence
	var toolCallsJSON json.RawMessage
	if len(state.Messages) > 0 {
		lastMsg := state.Messages[len(state.Messages)-1]
		if len(lastMsg.ToolCalls) > 0 {
			if data, err := json.Marshal(lastMsg.ToolCalls); err == nil {
				toolCallsJSON = data
			}
		}
	}

	if len(pendingExposedURLs) > 0 {
		urls := sortedURLs(pendingExposedURLs)
		if !messageMentionsAnyURL(state.LastAssistantText, urls) {
			fallback := "Available service URL(s):\n" + strings.Join(urls, "\n")
			state.LastAssistantText = strings.TrimSpace(strings.TrimSpace(state.LastAssistantText) + "\n\n" + fallback)
			state.Messages = append(state.Messages, gw.Message{
				Role:    gw.RoleAssistant,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr(fallback)}},
			})
		}
	}

	// Post-turn policy check: evaluate output patterns, finish reasons, retry policies
	if input.PolicyEvaluator != nil && state.FinishReason != "policy_blocked" {
		postTurnDecisions := input.PolicyEvaluator.EvaluatePhase(policy.PhasePOSTTURN, &policy.EvalContext{
			AgentID:            input.AgentID,
			SessionID:          input.SessionID,
			TurnNumber:         state.TurnNumber,
			IterationCount:     state.IterationCount,
			AssistantText:      state.LastAssistantText,
			FinishReason:       state.FinishReason,
			ToolCalls:          turnToolCalls,
			SandboxToolCalls:   turnSandboxToolCalls,
			ToolErrors:         turnToolErrors,
			SessionTotalTokens: int64(state.PriorSessionTokens) + int64(state.CumulativeUsage.TotalTokens),
		})
		for _, d := range postTurnDecisions {
			l.emitter.Emit(Event{
				Type:       EventPolicyDecision,
				SessionID:  input.SessionID,
				TurnNumber: state.TurnNumber,
				Timestamp:  time.Now(),
				Data: map[string]interface{}{
					"policy":   d.PolicyName,
					"action":   string(d.Action),
					"severity": d.Severity,
					"details":  d.Details,
					"phase":    "post_turn",
				},
			})
		}
		if policy.HasBlockingDecision(postTurnDecisions) {
			state.FinishReason = "policy_blocked"
			for _, d := range postTurnDecisions {
				if d.Action == policy.ActionBLOCK {
					logger.WithFields(
						"session_id", input.SessionID,
						"policy", d.PolicyName,
					).Warn("loop: turn output blocked by post-turn policy")
					break
				}
			}
		}
	}

	l.emitter.Emit(Event{
		Type:       EventTurnEnd,
		SessionID:  input.SessionID,
		TurnNumber: state.TurnNumber,
		Timestamp:  time.Now(),
		Reason:     state.FinishReason,
		Usage: &UsageDelta{
			PromptTokens:     state.TurnUsage.PromptTokens,
			CompletionTokens: state.TurnUsage.CompletionTokens,
			TotalTokens:      state.TurnUsage.TotalTokens,
		},
		Data: map[string]interface{}{
			"model":                   input.Model,
			"assistant_text":          state.LastAssistantText,
			"iterations":              state.IterationCount,
			"tool_calls":              state.TotalToolCalls,
			"tool_calls_json":         string(toolCallsJSON),
			"turn_tool_calls":         turnToolCalls,
			"turn_sandbox_tool_calls": turnSandboxToolCalls,
			"turn_non_sandbox_tools":  turnToolCalls - turnSandboxToolCalls,
			"turn_tool_errors":        turnToolErrors,
		},
	})

	// Auto-extract facts/instructions from this turn (non-blocking)
	if input.MemoryProvider != nil && state.LastAssistantText != "" {
		go func() {
			extractCtx, extractCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer extractCancel()
			if extractErr := input.MemoryProvider.ExtractFromTurn(
				extractCtx, input.AgentID, input.TenantID, input.SessionID,
				input.UserInput, state.LastAssistantText, state.TurnNumber, input.UserID,
			); extractErr != nil {
				logger.WithFields(
					"agent_id", input.AgentID,
					"session_id", input.SessionID,
					"turn", state.TurnNumber,
					"error", extractErr.Error(),
				).Warn("agent loop: memory extraction failed")
			}
		}()
	}

	// Auto-score this turn (non-blocking)
	if input.AutoScorer != nil {
		// Extract trace ID from the turn span
		traceID := trace.SpanFromContext(turnCtxSpan).SpanContext().TraceID().String()

		// Build per-tool details from state.ToolResults
		toolDetails := make(map[string]autoscorer.ToolResult, len(state.ToolResults))
		for id, tr := range state.ToolResults {
			toolDetails[id] = autoscorer.ToolResult{
				Result:     tr.Result,
				Success:    tr.Success,
				DurationMs: tr.DurationMs,
			}
		}

		input.AutoScorer.ScoreTurnAsync(&autoscorer.TurnContext{
			TraceID:          traceID,
			SessionID:        input.SessionID,
			AgentID:          input.AgentID,
			TenantID:         input.TenantID,
			TurnNumber:       state.TurnNumber,
			UserInput:        input.UserInput,
			AssistantText:    state.LastAssistantText,
			FinishReason:     state.FinishReason,
			ToolCalls:        turnToolCalls,
			SandboxToolCalls: turnSandboxToolCalls,
			ToolErrors:       turnToolErrors,
			IterationCount:   state.IterationCount,
			PromptTokens:     state.TurnUsage.PromptTokens,
			CompletionTokens: state.TurnUsage.CompletionTokens,
			TotalTokens:      state.TurnUsage.TotalTokens,
			ToolResults:      toolDetails,
		})
	}

	return state, nil
}

func finishReasonFromContext(ctx context.Context) string {
	if errors.Is(context.Cause(ctx), ErrTurnInterrupted) {
		return "interrupted"
	}
	return "cancelled"
}

func isSandboxToolName(name string) bool {
	return strings.HasPrefix(name, "sandbox_")
}

// approxMessagesBytes returns a cheap upper bound on the bytes of context the
// LLM sees this turn. We only count message text — not media attachments or
// tool schemas — because the goal is to slice outcome rates by "did the agent
// have too much context to decide well?", and text dominates that signal in
// practice. Used by SetAgentTurnSnapshot for verdict-rate breakdowns.
func approxMessagesBytes(msgs []gw.Message) int64 {
	var total int64
	for _, m := range msgs {
		total += int64(len(m.Role))
		for _, part := range m.Content {
			if part.Text != nil {
				total += int64(len(*part.Text))
			}
		}
	}
	return total
}

// summarizeToolResults builds a compact "tool_name=ok:N,err:M" comma-joined
// string from the turn's tool-result map. Cardinality is bounded by the per-
// turn tool name set (typically <10), so the resulting attribute stays
// queryable in ClickHouse without blowing up the high-cardinality budget.
func summarizeToolResults(results map[string]ToolResultMeta) string {
	if len(results) == 0 {
		return ""
	}
	type counter struct{ ok, err int }
	counts := make(map[string]*counter)
	for _, r := range results {
		// Tool name lives on the originating tool call, not the result;
		// callers typically include it in Result. Fall back to "tool" if
		// upstream code doesn't tag the result with a name.
		name := r.Result
		if idx := strings.IndexByte(name, ':'); idx > 0 {
			name = name[:idx]
		}
		if name == "" {
			name = "tool"
		}
		c, ok := counts[name]
		if !ok {
			c = &counter{}
			counts[name] = c
		}
		if r.Success {
			c.ok++
		} else {
			c.err++
		}
	}
	names := make([]string, 0, len(counts))
	for n := range counts {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, n := range names {
		c := counts[n]
		parts = append(parts, fmt.Sprintf("%s=ok:%d,err:%d", n, c.ok, c.err))
	}
	return strings.Join(parts, ",")
}

func sortedURLs(set map[string]struct{}) []string {
	urls := make([]string, 0, len(set))
	for u := range set {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	return urls
}

func messageMentionsAnyURL(text string, urls []string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	for _, u := range urls {
		if strings.Contains(text, u) {
			return true
		}
	}
	return false
}

func messageText(msg gw.Message) string {
	var text strings.Builder
	for _, part := range msg.Content {
		if part.Text != nil {
			text.WriteString(*part.Text)
		}
	}
	return text.String()
}

func hasPriorUserTurn(messages []gw.Message) bool {
	for _, msg := range messages {
		if msg.Role == gw.RoleUser {
			return true
		}
	}
	return false
}

func shouldBackstopAskUser(input *LoopInput, assistantText string) bool {
	if strings.TrimSpace(assistantText) == "" {
		return false
	}
	if !toolNameAllowed(input.Tools, "ask_user") {
		return false
	}
	return looksLikeBlockingUserQuestion(assistantText)
}

func toolNameAllowed(names []string, target string) bool {
	for _, name := range names {
		if name == target {
			return true
		}
	}
	return false
}

func looksLikeBlockingUserQuestion(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	if normalized == "" {
		return false
	}

	blockingPhrases := []string{
		"reply yes",
		"do you approve",
		"would you like me to proceed",
		"let me know your",
		"tell me which",
		"tell me your preferred",
		"just reply with",
		"pick your",
		"choose as many as you like",
		"which topics do you want",
		"what time would you like",
		"what timezone are you in",
	}
	for _, phrase := range blockingPhrases {
		if strings.Contains(normalized, phrase) {
			return true
		}
	}

	if !strings.Contains(normalized, "?") {
		return false
	}

	questionPrefixes := []string{
		"what ",
		"which ",
		"when ",
		"where ",
		"do you ",
		"would you ",
		"could you ",
		"can you ",
	}
	for _, prefix := range questionPrefixes {
		if strings.HasPrefix(normalized, prefix) || strings.Contains(normalized, "\n"+prefix) {
			return true
		}
	}

	return false
}

// callWithFallback attempts the primary LLM call and, on retriable failure,
// tries each fallback model in order. Returns the response, the provider that
// succeeded (so callers can reuse it), and any error.
func (l *Loop) callWithFallback(
	ctx context.Context,
	primaryProvider gw.ChatProvider,
	primaryModel string,
	req gw.ChatCompletionRequest,
	input *LoopInput,
	state *LoopState,
	onChunk func(string) error,
) (*gw.ChatCompletionResponse, gw.ChatProvider, error) {
	// Try the primary model first
	resp, err := l.executeLLMCall(ctx, primaryProvider, req, onChunk, input.SessionID, state.TurnNumber)
	if err == nil {
		return resp, primaryProvider, nil
	}

	// If no fallback configured or error is not retriable, return immediately
	if input.FallbackConfig == nil || !retrypolicy.IsRetryable(err) {
		return nil, nil, err
	}

	primaryErr := err
	logger.WithFields(
		"session_id", input.SessionID,
		"primary_model", primaryModel,
		"error", primaryErr.Error(),
	).Warn("primary model failed, attempting fallback")

	// Try each fallback model in order
	for _, fbModel := range input.FallbackConfig.Models {
		for attempt := 1; attempt <= input.FallbackConfig.MaxAttempts; attempt++ {
			l.emitter.Emit(Event{
				Type:              EventFallbackTriggered,
				SessionID:         input.SessionID,
				TurnNumber:        state.TurnNumber,
				Timestamp:         time.Now(),
				FallbackFromModel: primaryModel,
				FallbackToModel:   fbModel,
				FallbackAttempt:   int32(attempt),
			})

			// Resolve the fallback provider
			fbProvider, resolvedFallbackModel, resolveErr := l.engine.ResolveProvider(ctx, fbModel)
			if resolveErr != nil {
				logger.WithFields(
					"session_id", input.SessionID,
					"fallback_model", fbModel,
					"error", resolveErr.Error(),
				).Warn("failed to resolve fallback model, trying next")
				break // Skip to next model
			}
			if resolvedFallbackModel == "" {
				resolvedFallbackModel = fbModel
			}

			// Update the model in the request
			fbReq := req
			fbReq.Model = resolvedFallbackModel

			// Backoff before retry (skip on first attempt)
			if attempt > 1 && input.FallbackConfig.BackoffMs > 0 {
				select {
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				case <-time.After(time.Duration(input.FallbackConfig.BackoffMs) * time.Millisecond):
				}
			}

			resp, err = l.executeLLMCall(ctx, fbProvider, fbReq, onChunk, input.SessionID, state.TurnNumber)
			if err == nil {
				l.emitter.Emit(Event{
					Type:              EventFallbackSucceeded,
					SessionID:         input.SessionID,
					TurnNumber:        state.TurnNumber,
					Timestamp:         time.Now(),
					FallbackFromModel: primaryModel,
					FallbackToModel:   fbModel,
					FallbackAttempt:   int32(attempt),
				})
				return resp, fbProvider, nil
			}

			if !retrypolicy.IsRetryable(err) {
				break // Non-retriable error, try next model
			}
		}
	}

	// All fallbacks exhausted
	l.emitter.Emit(Event{
		Type:              EventFallbackFailed,
		SessionID:         input.SessionID,
		TurnNumber:        state.TurnNumber,
		Timestamp:         time.Now(),
		FallbackFromModel: primaryModel,
		Error:             err.Error(),
	})
	return nil, nil, fmt.Errorf("all fallback models exhausted, last error: %w", err)
}

func toolCallSignature(calls []toolloop.ToolCallMessage) string {
	if len(calls) == 0 {
		return ""
	}
	var b strings.Builder
	for _, tc := range calls {
		b.WriteString(tc.Function.Name)
		b.WriteString(":")
		b.WriteString(tc.Function.Arguments)
		b.WriteString("|")
	}
	return b.String()
}

// executeLLMCall runs a single LLM call in streaming or unary mode.
func (l *Loop) executeLLMCall(
	ctx context.Context,
	provider gw.ChatProvider,
	req gw.ChatCompletionRequest,
	onChunk func(string) error,
	sessionID string,
	turnNumber int32,
) (*gw.ChatCompletionResponse, error) {
	chatResp, cacheOutcome, err := cacheexec.ExecuteChatWithCacheOutcome(ctx, req,
		func(callCtx context.Context, callReq gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
			if l.config.EnableStreaming {
				streamResp, streamErr := l.engine.ExecuteTurnStream(callCtx, provider, callReq, onChunk)
				if streamErr != nil {
					return gw.ChatCompletionResponse{}, streamErr
				}
				return *streamResp, nil
			}
			return provider.Chat(callCtx, callReq)
		},
	)
	annotateCacheTrace(ctx, cacheOutcome, req.Model)

	eventType := EventCacheMiss
	data := map[string]interface{}{
		"model": req.Model,
	}
	if cacheOutcome.Hit {
		fpmetrics.RecordAgentCacheHit(cacheOutcome.HitType)
		eventType = EventCacheHit
		data["cache_type"] = cacheOutcome.HitType
	} else {
		fpmetrics.RecordAgentCacheMiss()
	}
	l.emitter.Emit(Event{
		Type:       eventType,
		SessionID:  sessionID,
		TurnNumber: turnNumber,
		Timestamp:  time.Now(),
		Data:       data,
	})

	if err != nil {
		return nil, err
	}
	if cacheOutcome.Stored {
		fpmetrics.RecordAgentCacheStore(cacheOutcome.SemanticStored)
		l.emitter.Emit(Event{
			Type:       EventCacheStore,
			SessionID:  sessionID,
			TurnNumber: turnNumber,
			Timestamp:  time.Now(),
			Data: map[string]interface{}{
				"model":           req.Model,
				"semantic_stored": cacheOutcome.SemanticStored,
			},
		})
	}
	if cacheOutcome.Hit && l.config.EnableStreaming && onChunk != nil && len(chatResp.Choices) > 0 {
		if text := messageText(chatResp.Choices[0].Message); text != "" {
			if chunkErr := onChunk(text); chunkErr != nil {
				return nil, chunkErr
			}
		}
	}
	return &chatResp, nil
}

// compactToolResults truncates verbose tool result content from older messages
// in-place. Tool results that the LLM has already processed don't need their
// full text retained — a short prefix is enough for the LLM to recall context.
// The most recent `keepRecent` messages are left intact.
//
// This runs at the top of each iteration, effectively compacting the output of
// the previous iteration's tool calls before they're sent in the next prompt.
const toolResultCompactThreshold = 300 // chars — results shorter than this are kept in full
const skillResultSummaryLength = 280

func compactToolResults(messages []gw.Message, keepRecent int) {
	if len(messages) <= keepRecent {
		return
	}
	cutoff := len(messages) - keepRecent
	for i := 0; i < cutoff; i++ {
		msg := &messages[i]
		if msg.Role != gw.RoleTool || msg.ToolCallID == "" {
			continue
		}
		for j := range msg.Content {
			if msg.Content[j].Text == nil {
				continue
			}
			text := *msg.Content[j].Text
			if len(text) <= toolResultCompactThreshold {
				continue
			}
			truncated := compactToolResultText(text)
			msg.Content[j].Text = &truncated
		}
	}
}

func compactToolResultText(text string) string {
	if strings.HasPrefix(text, "## Skill:") {
		return compactSkillResultText(text)
	}
	keep := 200
	if len(text) < keep {
		keep = len(text)
	}
	return text[:keep] + "\n...[truncated — " + fmt.Sprintf("%d", len(text)) + " chars total]"
}

func compactSkillResultText(text string) string {
	lines := strings.Split(text, "\n")
	header := "[Skill instructions were loaded earlier in this session.]"
	if len(lines) > 0 && strings.TrimSpace(lines[0]) != "" {
		header = "[Previously loaded " + strings.TrimSpace(strings.TrimPrefix(lines[0], "## ")) + "]"
	}
	body := strings.TrimSpace(strings.Join(lines[1:], "\n"))
	body = strings.Join(strings.Fields(body), " ")
	if len(body) > skillResultSummaryLength {
		body = body[:skillResultSummaryLength] + "..."
	}
	if body == "" {
		return header
	}
	return header + "\nRetain and follow the loaded skill guidance for this task.\nSummary: " + body
}

func buildPromptTokenBreakdown(messages []gw.Message, currentUserInput, baseSystemPrompt, digestPrompt, skillsManifest, memoryBlock string) map[string]interface{} {
	breakdown := map[string]interface{}{
		"system_prompt_tokens":   EstimateTokensForText(baseSystemPrompt),
		"digest_tokens":          EstimateTokensForText(digestPrompt),
		"skills_manifest_tokens": EstimateTokensForText(skillsManifest),
		"memory_tokens":          EstimateTokensForText(memoryBlock),
		"current_user_tokens":    EstimateTokensForText(currentUserInput),
	}

	var toolResultTokens int
	var historyTokens int
	var loadedSkillTokens int
	for _, msg := range messages {
		switch msg.Role {
		case gw.RoleTool:
			toks := EstimateTokens([]gw.Message{msg})
			toolResultTokens += toks
			for _, part := range msg.Content {
				if part.Text != nil && strings.HasPrefix(*part.Text, "## Skill:") {
					loadedSkillTokens += EstimateTokensForText(*part.Text)
				}
			}
		case gw.RoleSystem:
			continue
		default:
			historyTokens += EstimateTokens([]gw.Message{msg})
		}
	}

	breakdown["history_tokens"] = historyTokens
	breakdown["tool_result_tokens"] = toolResultTokens
	breakdown["loaded_skill_tokens"] = loadedSkillTokens
	breakdown["estimated_total_tokens"] = EstimateTokens(messages)
	return breakdown
}

func annotateCacheTrace(ctx context.Context, outcome cacheexec.CacheOutcome, model string) {
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
