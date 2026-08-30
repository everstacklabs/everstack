package runtime

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
)

// SessionRunnerConfig configures a session runner.
type SessionRunnerConfig struct {
	LoopConfig      LoopConfig
	CheckpointFunc  func(ctx context.Context, state *LoopState) error
	SteerBufferSize int        // default: 8
	InitialState    *LoopState // optional; if nil a zero-value state is used

	// ForceTerminalStatus forces the post-Done status update to a terminal
	// state ("completed" / "failed") regardless of the loop's finish reason.
	// Used by request/response callers (e.g. deployment invocations) where
	// each run is a discrete request — without this the SessionManager
	// leaves the session in "waiting_for_input" because finish_reason is
	// "stop", and the UI then opens an SSE subscribe expecting a live
	// runner that no longer exists, getting 204 in a retry loop.
	ForceTerminalStatus bool
}

// SessionRunner manages the goroutine lifecycle for a single session.
// It is decoupled from the HTTP request lifecycle -- the goroutine outlives the request.
type SessionRunner struct {
	sessionID  string
	agentID    string
	tenantID   string
	loop       *Loop
	emitter    *Emitter
	state      *LoopState
	config     SessionRunnerConfig
	steerCh    chan SteerMessage
	cancelFunc context.CancelCauseFunc
	doneCh     chan struct{}
	running    atomic.Bool
	lastErr    atomic.Value // stores error

	// HITL approval channels (nil when HITL not configured)
	approvalRecvCh <-chan ApprovalRequest // approval handler reads from this
	loopDecisionCh chan ApprovalDecision  // approval handler writes to this, loop reads

	// User input (ask_user) channels — always wired when ask_user tool is available
	userInputRecvCh     <-chan UserInputRequest // user input handler reads from this
	loopUserInputRespCh chan UserInputResponse  // user input handler writes to this, tool reads

	// tenantSchema is the Postgres schema name (e.g. "inst_xxx") captured from
	// the launch context. Used by background goroutines that need to issue DB
	// operations after the original request context is gone.
	tenantSchema string
}

// NewSessionRunner creates a new session runner.
func NewSessionRunner(
	sessionID, agentID, tenantID string,
	loop *Loop,
	emitter *Emitter,
	initialState *LoopState,
	config SessionRunnerConfig,
) *SessionRunner {
	bufSize := config.SteerBufferSize
	if bufSize <= 0 {
		bufSize = 8
	}
	if initialState == nil {
		initialState = &LoopState{}
	}
	return &SessionRunner{
		sessionID: sessionID,
		agentID:   agentID,
		tenantID:  tenantID,
		loop:      loop,
		emitter:   emitter,
		state:     initialState,
		config:    config,
		steerCh:   make(chan SteerMessage, bufSize),
		doneCh:    make(chan struct{}),
	}
}

// Start spawns the session goroutine. It runs the agentic loop and
// checkpoints the result on completion.
func (sr *SessionRunner) Start(ctx context.Context, input *LoopInput) {
	logger.WithFields("session_id", sr.sessionID, "already_running", sr.running.Load()).
		Info("session_runner: Start called")
	if sr.running.Load() {
		logger.WithFields("session_id", sr.sessionID).Warn("session_runner: already running, returning early")
		return
	}
	sr.running.Store(true)

	runCtx, cancel := context.WithCancelCause(ctx)
	sr.cancelFunc = cancel

	// Wire steer channel into input
	input.SteerCh = sr.steerCh

	go func() {
		defer close(sr.doneCh)
		defer sr.running.Store(false)

		logger.WithFields("session_id", sr.sessionID, "user_input", input.UserInput).
			Info("session_runner: goroutine started, about to emit session start")

		// Start OTEL session span
		spanCtx, sessionSpan := telemetry.StartAgentSessionSpan(runCtx, sr.agentID, sr.sessionID, sr.tenantID)
		defer sessionSpan.End()
		attrs.SetAgentSessionContext(
			sessionSpan,
			input.ExecutionMode,
			input.PersistenceMode,
			input.SandboxEnabled,
			input.GitRepoConfigured,
			input.TemplateConfigured,
		)
		attrs.SetAgentPolicyContext(
			sessionSpan,
			input.AgentMode,
			input.TaskPermissionMode,
			input.WorkingDirectory,
			input.MaxSteps,
		)

		sr.emitter.Emit(Event{
			Type:      EventSessionStart,
			SessionID: sr.sessionID,
			Timestamp: time.Now(),
			Data: map[string]interface{}{
				"agent_id":  sr.agentID,
				"tenant_id": sr.tenantID,
			},
		})

		// Run the task planner if configured (before the agentic loop)
		if input.Planner != nil && input.PlannerConfig.PlanningMode == PlanningModeOn {
			plan, planErr := input.Planner.Plan(spanCtx, input.UserInput, input.AvailableAgents, input.ToolNames, input.PlannerConfig, sr.emitter, sr.sessionID)
			if planErr != nil {
				logger.WithFields("session_id", sr.sessionID).WithError(planErr).
					Warn("session_runner: planner failed, proceeding without plan")
			} else if plan != nil && plan.Strategy != "single" {
				input.SpawnPlan = plan
				// Inject plan context into system prompt
				input.PlanContext = formatPlanContext(plan)
				// Adjust spawn config from plan
				if input.SpawnConfig != nil {
					input.SpawnConfig.MaxTotalSpawns = plan.AdjustedConfig.MaxTotalSpawns
					input.SpawnConfig.Enabled = true
				}
			}
		}

		logger.WithFields("session_id", sr.sessionID).Info("session_runner: calling loop.Run")

		// Run the agentic loop with auto-continue. When the loop exits due to
		// a per-turn limit (max_iterations, timeout, etc.) rather than the LLM
		// voluntarily stopping, automatically inject a continuation prompt and
		// re-enter the loop so the agent keeps working on the task.
		const maxAutoContinue = 5
		var err error
		for continueCount := 0; ; continueCount++ {
			newState, loopErr := sr.loop.Run(spanCtx, sr.state, input)
			sr.state = newState
			err = loopErr

			logger.WithFields(
				"session_id", sr.sessionID,
				"finish_reason", newState.FinishReason,
				"turn_number", newState.TurnNumber,
				"iteration_count", newState.IterationCount,
				"has_error", loopErr != nil,
				"auto_continue_count", continueCount,
			).Info("session_runner: loop.Run completed")

			if loopErr != nil {
				sr.lastErr.Store(loopErr)
				telemetry.RecordError(sessionSpan, loopErr)
				logger.WithFields(
					"session_id", sr.sessionID,
					"error", loopErr.Error(),
				).Error("session runner: loop failed")
				break
			}

			// Auto-continue if the loop hit a per-turn limit or the LLM
			// returned an empty stop (degenerate response). This keeps the
			// agent working on complex tasks.
			emptyStop := newState.FinishReason == "stop" && newState.LastAssistantText == ""
			if (!shouldAutoContinue(newState.FinishReason) && !emptyStop) || continueCount >= maxAutoContinue {
				break
			}

			// Check context before continuing
			if spanCtx.Err() != nil {
				break
			}

			logger.WithFields(
				"session_id", sr.sessionID,
				"finish_reason", newState.FinishReason,
				"continue_count", continueCount+1,
			).Info("session_runner: auto-continuing after per-turn limit")

			sr.emitter.Emit(Event{
				Type:      EventTurnEnd,
				SessionID: sr.sessionID,
				Timestamp: time.Now(),
				Reason:    newState.FinishReason,
				Data: map[string]interface{}{
					"auto_continue": true,
					"continue_count": continueCount + 1,
				},
			})

			// Reset per-turn state for the continuation turn but preserve
			// conversation history and cumulative usage.
			sr.state.Done = false
			sr.state.IterationCount = 0
			sr.state.TotalToolCalls = 0
			sr.state.FinishReason = ""

			// Inject a continuation prompt as a system message so the LLM
			// knows to keep going without it appearing as a user message
			// in the session timeline.
			sr.state.Messages = append(sr.state.Messages, gw.Message{
				Role:    gw.RoleSystem,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr("Continue working on the task. You were interrupted because you hit the per-turn iteration limit. Pick up where you left off and keep going until the task is complete.")}},
			})
			input.UserInput = ""
		}

		// Set session-level metrics on the span
		attrs.SetAgentSessionMetrics(sessionSpan,
			int(sr.state.TurnNumber),
			int(sr.state.TotalToolCalls),
			int(sr.state.CumulativeUsage.TotalTokens),
			sr.state.FinishReason,
		)

		// Cancel the run context now that the loop is done, before checkpointing.
		// Checkpoint uses its own independent context so it can persist even after
		// cancellation or timeout of the run context.
		cancel(nil)

		// Checkpoint with an independent context so that turn data is persisted
		// even when the run context was cancelled (user cancel, timeout, shutdown).
		// Use WithoutCancel(runCtx) to preserve tenant/correlation context values
		// required by tenant-aware DB routing in CQRS projections.
		if sr.config.CheckpointFunc != nil {
			cpBase := context.WithoutCancel(runCtx)
			cpCtx, cpCancel := context.WithTimeout(cpBase, 10*time.Second)
			defer cpCancel()
			if cpErr := sr.config.CheckpointFunc(cpCtx, sr.state); cpErr != nil {
				logger.WithFields(
					"session_id", sr.sessionID,
					"error", cpErr.Error(),
				).Error("session runner: checkpoint failed")
			}
		}

		// Auto-summarize session for persistent memory (non-blocking)
		if input.MemoryProvider != nil && len(sr.state.Messages) > 0 {
			go func() {
				sumBase := context.WithoutCancel(runCtx)
				sumCtx, sumCancel := context.WithTimeout(sumBase, 60*time.Second)
				defer sumCancel()
				if sumErr := input.MemoryProvider.SummarizeSession(
					sumCtx, input.AgentID, input.TenantID, sr.sessionID, sr.state.Messages,
				); sumErr != nil {
					logger.WithFields(
						"agent_id", input.AgentID,
						"session_id", sr.sessionID,
						"error", sumErr.Error(),
					).Warn("session runner: memory summarization failed")
				}
			}()
		}

		endEvent := Event{
			Type:      EventSessionEnd,
			SessionID: sr.sessionID,
			Timestamp: time.Now(),
			Reason:    sr.state.FinishReason,
			Usage: &UsageDelta{
				PromptTokens:     sr.state.CumulativeUsage.PromptTokens,
				CompletionTokens: sr.state.CumulativeUsage.CompletionTokens,
				TotalTokens:      sr.state.CumulativeUsage.TotalTokens,
			},
		}
		if err != nil {
			endEvent.Error = err.Error()
			endEvent.Type = EventSessionError
		}
		sr.emitter.Emit(endEvent)
	}()
}

// Steer sends a message to be injected between loop iterations.
// Returns false if the channel is full or the runner is not running.
func (sr *SessionRunner) Steer(msg SteerMessage) bool {
	if !sr.running.Load() {
		return false
	}
	select {
	case sr.steerCh <- msg:
		return true
	default:
		return false
	}
}

// Cancel cancels the session context. The goroutine finishes its current
// tool call then exits.
func (sr *SessionRunner) Cancel() {
	if sr.cancelFunc != nil {
		sr.cancelFunc(ErrSessionCancelled)
	}
}

// Interrupt stops only the active turn so the session can continue.
func (sr *SessionRunner) Interrupt() {
	if sr.cancelFunc != nil {
		sr.cancelFunc(ErrTurnInterrupted)
	}
}

// Wait blocks until the session goroutine exits.
func (sr *SessionRunner) Wait() {
	<-sr.doneCh
}

// Done returns a channel that is closed when the session goroutine exits.
func (sr *SessionRunner) Done() <-chan struct{} {
	return sr.doneCh
}

// IsRunning returns whether the session goroutine is active.
func (sr *SessionRunner) IsRunning() bool {
	return sr.running.Load()
}

// State returns a snapshot of the current loop state.
func (sr *SessionRunner) State() LoopState {
	if sr.state == nil {
		return LoopState{}
	}
	return *sr.state
}

// LastError returns the last error from the loop, if any.
func (sr *SessionRunner) LastError() error {
	v := sr.lastErr.Load()
	if v == nil {
		return nil
	}
	return v.(error)
}

// shouldAutoContinue returns true if the given finish reason indicates the
// loop hit a per-turn limit (not the LLM choosing to stop) and should
// automatically continue with a new turn.
func shouldAutoContinue(reason string) bool {
	switch reason {
	case "max_iterations", "max_steps", "max_tool_calls":
		// The agent was still actively working when the limit was hit.
		return true
	default:
		// "stop", "end_turn" = LLM chose to stop (task done or needs input)
		// "error", "timeout", "cancelled" = unrecoverable
		// "token_budget_exhausted" = cost control
		return false
	}
}

// formatPlanContext generates system prompt additions describing the spawn plan.
func formatPlanContext(plan *SpawnPlan) string {
	if plan == nil || len(plan.SubAgents) == 0 {
		return ""
	}

	ctx := "\n\n## Task Execution Plan\n"
	ctx += "Strategy: " + plan.Strategy + "\n\n"
	ctx += "You have planned the following sub-agents for this task:\n"

	for i, agent := range plan.SubAgents {
		ctx += fmt.Sprintf("%d. **%s**: %s\n", i+1, agent.Role, agent.Task)
		if len(agent.DependsOn) > 0 {
			ctx += "   Depends on: " + fmt.Sprintf("%v", agent.DependsOn) + "\n"
		}
	}

	ctx += "\nUse the spawn_agent tool to spawn each sub-agent by role name. "
	ctx += "Available roles: "
	for i, agent := range plan.SubAgents {
		if i > 0 {
			ctx += ", "
		}
		ctx += agent.Role
	}
	ctx += "\n"

	return ctx
}
