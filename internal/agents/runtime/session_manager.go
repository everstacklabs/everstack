package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/commands/handlers/agents"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/functions/toolloop"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/jmoiron/sqlx"
	"github.com/redis/go-redis/v9"
)

// bgCtxForTenant creates a background context with both tenant keys injected
// so DB operations and request-scoped provider resolution use the same tenant.
func bgCtxForTenant(tenantID string) context.Context {
	return ContextWithTenantIdentity(context.Background(), tenantID)
}

// approvalGate tracks an in-memory approval wait for a single review.
type approvalGate struct {
	sessionID  string
	reviewID   string
	decisionCh chan ApprovalDecision // buffered(1), bridges SubmitReview → loop
}

// userInputGate tracks an in-memory user input wait for a single ask_user call.
type userInputGate struct {
	sessionID  string
	inputID    string
	responseCh chan UserInputResponse // buffered(1), bridges SubmitUserInput → tool
}

// SessionManager is a process-level registry of active session runners.
// It handles lookup, steering, cancellation, graceful shutdown, and
// HITL approval gate coordination.
type SessionManager struct {
	runners    map[string]*SessionRunner
	approvals  map[string]*approvalGate  // keyed by reviewID
	userInputs map[string]*userInputGate // keyed by inputID
	jobQueues  map[string]*LocalJobQueue // keyed by sessionID, persists across turns
	mu         sync.RWMutex
	engine     *Engine
	toolLoop   *toolloop.LoopManager
	sys        *cqrs.System
	db         *sqlx.DB // for direct approval operations (startup sweep, reaper, submit)

	// Plan tier resolver for session hibernation timeouts
	planTierResolver func(tenantID string) string

	reaperCancel context.CancelFunc
	reaperDone   chan struct{}

	// Runtime durability fields
	instanceID    string
	heartbeat     *HeartbeatWriter
	staleDetector *StaleDetector
	router        SessionRouter
	bridge        ApprovalBridge
	bridgeCh      <-chan ApprovalDecision
	bridgeCancel  context.CancelFunc
	bridgeDone    chan struct{}

	// Phase 4: Digest manager (server-level, shared across sessions)
	digestManager *DigestManager

	// Phase 5: Agent session registry — tracks agentID → active sessionID
	// for cross-agent message delivery on this instance.
	agentSessions map[string]string           // agentID → sessionID
	peerChannels  map[string]chan PeerMessage // sessionID → peer message channel
}

// NewSessionManager creates a new session manager.
// db may be nil for environments without Postgres (tests, memory mode).
// redisClient may be nil; when provided it enables cross-instance session
// routing and approval delivery via Redis.
func NewSessionManager(engine *Engine, toolLoop *toolloop.LoopManager, sys *cqrs.System, db *sqlx.DB, redisClient ...*redis.Client) *SessionManager {
	instID := InstanceID()

	m := &SessionManager{
		runners:       make(map[string]*SessionRunner),
		approvals:     make(map[string]*approvalGate),
		userInputs:    make(map[string]*userInputGate),
		jobQueues:     make(map[string]*LocalJobQueue),
		engine:        engine,
		toolLoop:      toolLoop,
		sys:           sys,
		db:            db,
		instanceID:    instID,
		agentSessions: make(map[string]string),
		peerChannels:  make(map[string]chan PeerMessage),
	}

	// Choose router and bridge implementations based on Redis availability.
	var rc *redis.Client
	if len(redisClient) > 0 && redisClient[0] != nil {
		rc = redisClient[0]
	}

	if rc != nil {
		m.router = NewRedisRouter(rc)
		m.bridge = NewRedisBridge(rc)
		logger.WithFields("instance_id", instID).Info("session_manager: Redis router and bridge enabled")
	} else {
		m.router = NewLocalRouter(instID)
		m.bridge = NewLocalBridge()
	}

	// Expire any stale reviews left by a previous crash
	if db != nil {
		m.startupSweep()

		// Fail sessions that were running on this instance before the crash.
		FailOrphanedSessions(db, instID)

		m.startReaper()
		m.heartbeat = NewHeartbeatWriter(db, instID)
		m.staleDetector = NewStaleDetector(db, instID)
	}

	// Start bridge subscriber so cross-instance approvals can be delivered.
	m.startBridgeSubscriber()

	logger.WithFields("instance_id", instID).Info("session_manager: started")

	return m
}

// startupSweep expires stale pending reviews and fails orphaned sessions
// left by a previous process crash. Called once at startup.
func (m *SessionManager) startupSweep() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Expire any reviews still marked pending (previous process died)
	res, err := m.db.ExecContext(ctx,
		`UPDATE agent_approval_reviews
		 SET status = 'expired', resolution_reason = 'process_restart', resolved_at = NOW()
		 WHERE status = 'pending'`)
	if err != nil {
		logger.WithError(err).Warn("session_manager: startup sweep failed to expire stale reviews")
	} else if n, _ := res.RowsAffected(); n > 0 {
		logger.WithFields("expired_reviews", n).Info("session_manager: startup sweep expired stale reviews")
	}

	// Fail any sessions stuck in waiting_for_approval or waiting_for_user_input
	res, err = m.db.ExecContext(ctx,
		`UPDATE agent_sessions
		 SET status = 'failed'
		 WHERE status IN ('waiting_for_approval', 'waiting_for_user_input')`)
	if err != nil {
		logger.WithError(err).Warn("session_manager: startup sweep failed to expire stale sessions")
	} else if n, _ := res.RowsAffected(); n > 0 {
		logger.WithFields("failed_sessions", n).Info("session_manager: startup sweep failed orphaned sessions")
	}
}

// startReaper spawns a background goroutine that periodically expires timed-out
// pending reviews. It delivers the configured default_action to in-memory gates
// if still present (the loop's timer is the primary timeout, but the reaper
// catches edge cases like clock skew or missed in-memory delivery).
func (m *SessionManager) startReaper() {
	ctx, cancel := context.WithCancel(context.Background())
	m.reaperCancel = cancel
	m.reaperDone = make(chan struct{})

	go func() {
		defer close(m.reaperDone)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.reapExpiredReviews()
				m.reapIdleSessions()
			}
		}
	}()
}

// reapExpiredReviews expires timed-out pending reviews in the DB and delivers
// the default action to any in-memory gates still waiting.
func (m *SessionManager) reapExpiredReviews() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	type expiredRow struct {
		ID            string `db:"id"`
		SessionID     string `db:"session_id"`
		DefaultAction string `db:"default_action"`
	}

	rows, err := m.db.QueryxContext(ctx,
		`UPDATE agent_approval_reviews
		 SET status = 'expired', resolution_reason = 'timeout', resolved_at = NOW()
		 WHERE status = 'pending' AND expires_at < NOW()
		 RETURNING id, session_id, default_action`)
	if err != nil {
		logger.WithError(err).Warn("session_manager: reaper query failed")
		return
	}
	defer rows.Close()

	var expired []expiredRow
	for rows.Next() {
		var r expiredRow
		if err := rows.StructScan(&r); err != nil {
			logger.WithError(err).Warn("session_manager: reaper scan failed")
			continue
		}
		expired = append(expired, r)
	}

	if len(expired) == 0 {
		return
	}

	logger.WithFields("count", len(expired)).Info("session_manager: reaper expired reviews")

	// Deliver default action to in-memory gates
	m.mu.Lock()
	for _, r := range expired {
		if gate, ok := m.approvals[r.ID]; ok {
			select {
			case gate.decisionCh <- ApprovalDecision{
				ReviewID: r.ID,
				Action:   r.DefaultAction,
				Reason:   "reaper_timeout",
			}:
			default:
				// Channel already has a decision or is full (shouldn't happen)
			}
			delete(m.approvals, r.ID)
		}
	}
	m.mu.Unlock()
}

// PrepareSession creates a session runner and returns the emitter so the caller
// can wire event sinks BEFORE the goroutine starts emitting events.
// After adding sinks, the caller MUST call LaunchSession to start execution.
func (m *SessionManager) PrepareSession(
	ctx context.Context,
	sessionID, agentID, tenantID string,
	input *LoopInput,
	config SessionRunnerConfig,
) (*Emitter, error) {
	// If there is an existing runner that is still shutting down (e.g. after
	// InterruptSession), wait for it to finish before acquiring the lock.
	// This prevents "session is already running" rejections when a user
	// force-sends a queued message immediately after stopping the current turn.
	m.mu.RLock()
	existing, hasExisting := m.runners[sessionID]
	m.mu.RUnlock()

	if hasExisting && existing.IsRunning() {
		doneCh := existing.Done()
		waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		select {
		case <-doneCh:
			// Runner finished — proceed
		case <-waitCtx.Done():
			return nil, fmt.Errorf("session %s is still running after interrupt timeout", sessionID)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Re-check after acquiring write lock — another goroutine may have
	// started a new runner while we were waiting.
	if existing, ok := m.runners[sessionID]; ok && existing.IsRunning() {
		return nil, fmt.Errorf("session %s is already running", sessionID)
	}

	// Create emitter
	emitter := NewEmitter()

	// Create loop
	loop := NewLoop(m.engine, m.toolLoop, emitter, config.LoopConfig)

	// Wire checkpoint if CQRS system is available and no custom checkpoint provided
	if config.CheckpointFunc == nil && m.sys != nil {
		cb := NewCheckpointBuilder(m.sys)
		config.CheckpointFunc = cb.BuildFunc(sessionID, input.UserInput)
	}

	// Create runner but do NOT start it yet
	runner := NewSessionRunner(sessionID, agentID, tenantID, loop, emitter, config.InitialState, config)

	// Capture the tenant schema from the request context so background
	// goroutines can create correctly-scoped DB contexts after the
	// original request context is gone.
	runner.tenantSchema = database.TenantSchemaFromContext(ctx)

	// Wire HITL approval channels if configured
	if input.HITLConfig != nil {
		approvalCh := make(chan ApprovalRequest, 1)
		decisionCh := make(chan ApprovalDecision, 1)
		input.ApprovalCh = approvalCh      // loop writes here (send-only view)
		input.DecisionCh = decisionCh      // loop reads here (receive-only view)
		runner.approvalRecvCh = approvalCh // approval handler reads requests
		runner.loopDecisionCh = decisionCh // approval handler forwards decisions
	}

	// Wire user input (ask_user) channels — always created so the ask_user
	// tool handler can be registered dynamically based on interceptor config.
	{
		userInputReqCh := make(chan UserInputRequest, 1)
		userInputRespCh := make(chan UserInputResponse, 1)
		input.UserInputReqCh = userInputReqCh        // ask_user handler sends here
		input.UserInputRespCh = userInputRespCh      // ask_user handler reads here
		runner.userInputRecvCh = userInputReqCh      // user input handler reads requests
		runner.loopUserInputRespCh = userInputRespCh // user input handler forwards responses
	}

	m.runners[sessionID] = runner

	// Tag the session row with this instance and register in the router.
	if m.db != nil {
		if _, err := m.db.ExecContext(bgCtxForTenant(runner.tenantSchema),
			`UPDATE agent_sessions SET instance_id = $1, heartbeat_at = NOW() WHERE id = $2`,
			m.instanceID, sessionID); err != nil {
			logger.WithFields("session_id", sessionID, "error", err.Error()).
				Warn("session_manager: failed to set instance_id on session")
		}
	}
	if err := m.router.Register(context.Background(), sessionID, m.instanceID); err != nil {
		logger.WithFields("session_id", sessionID, "error", err.Error()).
			Warn("session_manager: failed to register session in router")
	}

	return emitter, nil
}

// LaunchSession starts the goroutine for a previously prepared session.
// This must be called after sinks have been wired to the emitter.
func (m *SessionManager) LaunchSession(ctx context.Context, sessionID string, input *LoopInput) error {
	m.mu.RLock()
	runner, ok := m.runners[sessionID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("session %s not found, call PrepareSession first", sessionID)
	}
	if runner.IsRunning() {
		return fmt.Errorf("session %s is already running", sessionID)
	}

	// Detach the runner context from the caller's HTTP request context.
	// The session runner must outlive the SSE stream — if the client
	// disconnects or navigates away the loop should continue running.
	// context.WithoutCancel preserves values (tenant ID, CQRS system, etc.)
	// but does not propagate cancellation from the parent.
	runCtx := context.WithoutCancel(ctx)

	// Spawn approval handler goroutine if HITL is configured
	if input.HITLConfig != nil && input.ApprovalCh != nil {
		go m.approvalHandler(runCtx, sessionID, input)
	}

	// Spawn user input handler goroutine (always — channels are always wired)
	if runner.userInputRecvCh != nil {
		go m.userInputHandler(runCtx, sessionID)
	}

	runner.Start(runCtx, input)

	// Clean up when done
	emitter := runner.emitter
	go func() {
		<-runner.Done()
		m.mu.Lock()
		if existing, ok := m.runners[sessionID]; ok && existing == runner {
			delete(m.runners, sessionID)
		}
		// Phase 5: Clean up peer message channel
		if ch, ok := m.peerChannels[sessionID]; ok {
			close(ch)
			delete(m.peerChannels, sessionID)
		}
		for agentID, sid := range m.agentSessions {
			if sid == sessionID {
				delete(m.agentSessions, agentID)
				break
			}
		}
		m.mu.Unlock()
		m.CleanupJobQueue(sessionID)
		emitter.Close()
		if err := m.router.Unregister(context.Background(), sessionID); err != nil {
			logger.WithFields("session_id", sessionID, "error", err.Error()).
				Debug("session_manager: failed to unregister session from router")
		}

		state := runner.State()

		// Update session status based on finish reason
		if m.db != nil {
			var newStatus string
			switch {
			case runner.config.ForceTerminalStatus:
				// Request/response callers (e.g. deployment invocations)
				// don't have a runner waiting for input — force terminal
				// so the UI doesn't try to live-subscribe.
				if state.FinishReason == "error" {
					newStatus = "failed"
				} else {
					newStatus = "completed"
				}
			case isTerminalFinishReason(state.FinishReason):
				// Terminal states: error, max_iterations, max_tool_calls, timeout, cancelled, etc.
				if state.FinishReason == "error" {
					newStatus = "failed"
				} else {
					newStatus = "completed"
				}
			default:
				// Normal turn completion (stop, end_turn) — session can continue
				newStatus = "waiting_for_input"
			}
			dbCtx, dbCancel := context.WithTimeout(bgCtxForTenant(runner.tenantSchema), 5*time.Second)
			if _, err := m.db.ExecContext(dbCtx,
				`UPDATE agent_sessions SET status = $1, updated_at = NOW() WHERE id = $2`,
				newStatus, sessionID); err != nil {
				logger.WithFields("session_id", sessionID, "status", newStatus, "error", err.Error()).
					Warn("session_manager: failed to update session status after turn")
			} else {
				logger.WithFields("session_id", sessionID, "status", newStatus, "finish_reason", state.FinishReason).
					Debug("session_manager: updated session status after turn")
			}
			dbCancel()
		}

		// Safety net: transition persistent agent lifecycle_status from
		// 'running' back to 'idle' so the sandbox reaper can idle-stop it.
		// The streaming handlers should do this too, but if they miss it
		// (e.g. channel sessions, context cancellation), this ensures the
		// agent doesn't stay stuck in 'running' forever.
		if m.db != nil && runner.agentID != "" {
			idleCtx, idleCancel := context.WithTimeout(bgCtxForTenant(runner.tenantSchema), 5*time.Second)
			if _, err := m.db.ExecContext(idleCtx,
				`UPDATE agent_definitions SET lifecycle_status = 'idle', updated_at = NOW() WHERE id = $1 AND lifecycle_status = 'running'`,
				runner.agentID); err != nil {
				logger.WithFields("agent_id", runner.agentID, "error", err.Error()).
					Warn("session_manager: failed to transition agent to idle after turn")
			}
			idleCancel()
		}

		// Never destroy sandboxes on session end — regardless of finish reason.
		// Sandboxes are independent of sessions: users may access exposed ports,
		// open the shell tab, or start a new session against the same sandbox.
		// The sandbox reaper handles cleanup via idle retention (e.g., 24h for
		// free tier, never for pro/enterprise).
		logger.WithFields("session_id", sessionID, "finish_reason", state.FinishReason).
			Debug("session_manager: session ended, sandbox kept alive for idle retention")
	}()

	return nil
}

// StartSession creates, wires, and starts a session runner in one call.
// This is a convenience wrapper for callers that don't need to add sinks
// before the session starts. For streaming callers that need to wire sinks
// first, use PrepareSession + LaunchSession instead.
func (m *SessionManager) StartSession(
	ctx context.Context,
	sessionID, agentID, tenantID string,
	input *LoopInput,
	config SessionRunnerConfig,
) (*Emitter, error) {
	emitter, err := m.PrepareSession(ctx, sessionID, agentID, tenantID, input, config)
	if err != nil {
		return nil, err
	}
	if err := m.LaunchSession(ctx, sessionID, input); err != nil {
		return nil, err
	}
	return emitter, nil
}

// GetRunner returns the session runner for the given session ID, or nil.
func (m *SessionManager) GetRunner(sessionID string) *SessionRunner {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.runners[sessionID]
}

// SubscribeToSession attaches a new event sink to an already-running session,
// replaying all buffered events so the subscriber catches up on events emitted
// before it connected. Returns a channel that receives events and the runner's
// done channel. Returns nil channels and false if the session is not running.
func (m *SessionManager) SubscribeToSession(sessionID string, bufSize int) (<-chan Event, <-chan struct{}, bool) {
	runner := m.GetRunner(sessionID)
	if runner == nil || !runner.IsRunning() {
		return nil, nil, false
	}

	if bufSize <= 0 {
		bufSize = 128
	}

	eventCh := make(chan Event, bufSize)
	runner.emitter.AddSinkWithReplay(EventSinkFunc(func(e Event) error {
		select {
		case eventCh <- e:
			return nil
		default:
			return fmt.Errorf("subscribe channel full")
		}
	}))

	return eventCh, runner.Done(), true
}

// SteerSession injects a message into a running session.
func (m *SessionManager) SteerSession(sessionID, tenantID string, msg SteerMessage) error {
	m.mu.RLock()
	runner, ok := m.runners[sessionID]
	isRunning := ok && runner.IsRunning()
	m.mu.RUnlock()

	// If the session runner is active, deliver the steer immediately.
	if isRunning {
		if !runner.Steer(msg) {
			return fmt.Errorf("session %s steer buffer full or session ended", sessionID)
		}
		return nil
	}

	// Session is idle (between turns). Persist to DB so it survives
	// both hibernation and process crashes.
	return m.queueSteerToDB(sessionID, tenantID, msg)
}

// DrainPendingSteers returns and clears any queued steer messages for the
// given session. Steers are persisted in the DB (pending_steers JSONB column)
// so they survive hibernation and process crashes.
func (m *SessionManager) DrainPendingSteers(sessionID, tenantID string) []SteerMessage {
	if m.db == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var steersJSON []byte
	err := m.db.QueryRowContext(ctx, `
		WITH old AS (
			SELECT pending_steers FROM agent_sessions WHERE id = $1 AND tenant_id = $2
		)
		UPDATE agent_sessions SET pending_steers = '[]' WHERE id = $1 AND tenant_id = $2
		RETURNING (SELECT pending_steers FROM old)`,
		sessionID, tenantID).Scan(&steersJSON)
	if err != nil || len(steersJSON) == 0 {
		return nil
	}
	var steers []SteerMessage
	if err := json.Unmarshal(steersJSON, &steers); err != nil {
		logger.WithFields("session_id", sessionID, "error", err.Error()).
			Warn("session_manager: failed to unmarshal pending steers from DB")
		return nil
	}
	if len(steers) > 0 {
		logger.WithFields("session_id", sessionID, "count", len(steers)).
			Debug("session_manager: drained pending steers from DB")
	}
	return steers
}

// queueSteerToDB persists a steer message to the pending_steers JSONB column.
// Cap at 16 entries to prevent unbounded growth.
func (m *SessionManager) queueSteerToDB(sessionID, tenantID string, msg SteerMessage) error {
	if m.db == nil {
		return fmt.Errorf("session %s: no database connection for steer persistence", sessionID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	steerJSON, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("session %s: failed to marshal steer: %w", sessionID, err)
	}

	result, err := m.db.ExecContext(ctx, `
		UPDATE agent_sessions
		SET pending_steers = pending_steers || $1::jsonb,
		    updated_at = NOW()
		WHERE id = $2
		  AND tenant_id = $3
		  AND jsonb_array_length(COALESCE(pending_steers, '[]'::jsonb)) < 16`,
		fmt.Sprintf("[%s]", steerJSON), sessionID, tenantID)
	if err != nil {
		return fmt.Errorf("session %s: failed to persist steer: %w", sessionID, err)
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return fmt.Errorf("session %s pending steer queue full or session not found", sessionID)
	}
	logger.WithFields("session_id", sessionID).
		Debug("session_manager: steer persisted to DB")
	return nil
}

// SetPlanTierResolver sets the function used to resolve the plan tier for
// session hibernation idle timeouts.
func (m *SessionManager) SetPlanTierResolver(fn func(tenantID string) string) {
	m.planTierResolver = fn
}

// CancelSession cancels a running session.
func (m *SessionManager) CancelSession(sessionID string) error {
	m.mu.RLock()
	runner, ok := m.runners[sessionID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}

	runner.Cancel()
	return nil
}

// InterruptSession stops only the currently running turn for a session.
// The session remains reusable for subsequent turns.
func (m *SessionManager) InterruptSession(sessionID string) error {
	m.mu.RLock()
	runner, ok := m.runners[sessionID]
	m.mu.RUnlock()

	if !ok || !runner.IsRunning() {
		return fmt.Errorf("session %s is not running", sessionID)
	}

	runner.Interrupt()
	return nil
}

// ActiveCount returns the number of active session runners.
func (m *SessionManager) ActiveCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	count := 0
	for _, r := range m.runners {
		if r.IsRunning() {
			count++
		}
	}
	return count
}

// GracefulShutdown cancels all runners, expires pending reviews, stops the
// reaper, heartbeat, stale detector, and bridge, and waits for runners to finish.
func (m *SessionManager) GracefulShutdown(ctx context.Context) error {
	// Stop the reaper goroutine
	if m.reaperCancel != nil {
		m.reaperCancel()
		<-m.reaperDone
	}

	// Stop heartbeat and stale detector
	if m.heartbeat != nil {
		m.heartbeat.Stop()
	}
	if m.staleDetector != nil {
		m.staleDetector.Stop()
	}

	// Stop bridge subscriber
	if m.bridgeCancel != nil {
		m.bridgeCancel()
		<-m.bridgeDone
	}
	if m.bridge != nil {
		if err := m.bridge.Close(); err != nil {
			logger.WithError(err).Warn("session_manager: failed to close bridge")
		}
	}

	// Stop digest manager
	if m.digestManager != nil {
		if err := m.digestManager.Shutdown(ctx); err != nil {
			logger.WithError(err).Warn("session_manager: failed to shutdown digest manager")
		}
	}

	m.mu.RLock()
	runners := make([]*SessionRunner, 0, len(m.runners))
	for _, r := range m.runners {
		runners = append(runners, r)
	}
	m.mu.RUnlock()

	// Cancel pending reviews in DB
	if m.db != nil {
		expCtx, expCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer expCancel()
		if _, err := m.db.ExecContext(expCtx,
			`UPDATE agent_approval_reviews
			 SET status = 'cancelled', resolution_reason = 'shutdown', resolved_at = NOW()
			 WHERE status = 'pending'`); err != nil {
			logger.WithError(err).Warn("session_manager: failed to cancel pending reviews on shutdown")
		}
	}

	// Sandbox environments are NOT destroyed on shutdown — the sandbox manager's
	// DestroyAll is called separately from the server shutdown path. Sandboxes
	// persist independently of sessions and are reaped via idle retention.

	if len(runners) == 0 {
		return nil
	}

	logger.WithFields("active_sessions", len(runners)).Info("session_manager: graceful shutdown starting")

	// Cancel all runners
	for _, r := range runners {
		r.Cancel()
	}

	// Wait for all to finish or context deadline
	done := make(chan struct{})
	go func() {
		for _, r := range runners {
			r.Wait()
		}
		close(done)
	}()

	select {
	case <-done:
		logger.Info("session_manager: all sessions terminated gracefully")
		return nil
	case <-ctx.Done():
		logger.Warn("session_manager: shutdown deadline exceeded, some sessions may not have checkpointed")
		return ctx.Err()
	}
}

// approvalHandler runs in a goroutine for each HITL-enabled session.
// It receives ApprovalRequest from the loop via runner.approvalRecvCh
// (the receive side of the channel the loop sends on), persists via CQRS,
// and registers an in-memory gate to bridge SubmitReview decisions back.
func (m *SessionManager) approvalHandler(ctx context.Context, sessionID string, input *LoopInput) {
	runner := m.GetRunner(sessionID)
	if runner == nil || runner.approvalRecvCh == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case req, ok := <-runner.approvalRecvCh:
			if !ok {
				return
			}
			m.handleApprovalRequest(ctx, req)
		case <-runner.Done():
			return
		}
	}
}

// handleApprovalRequest persists a review via CQRS and registers an in-memory gate.
func (m *SessionManager) handleApprovalRequest(ctx context.Context, req ApprovalRequest) {
	// Serialize tool calls to JSON for CQRS command
	toolCallsJSON, err := json.Marshal(req.ToolCalls)
	if err != nil {
		logger.WithError(err).Error("session_manager: failed to marshal tool calls for approval")
		toolCallsJSON = []byte("[]")
	}

	// Persist via CQRS command
	if m.sys != nil {
		cmd := agents.NewRequestApprovalCommand(
			req.ReviewID,
			req.SessionID,
			req.TenantID,
			req.AgentID,
			req.TurnNumber,
			req.Iteration,
			toolCallsJSON,
			req.Config.DefaultAction,
			req.ExpiresAt,
		)
		if err := m.sys.CommandBus.Dispatch(ctx, cmd); err != nil {
			logger.WithFields(
				"review_id", req.ReviewID,
				"session_id", req.SessionID,
				"error", err.Error(),
			).Error("session_manager: failed to persist approval request via CQRS")
		}
	}

	// Update session status to waiting_for_approval
	if m.db != nil {
		if _, err := m.db.ExecContext(ctx,
			`UPDATE agent_sessions SET status = 'waiting_for_approval' WHERE id = $1`,
			req.SessionID); err != nil {
			logger.WithFields(
				"session_id", req.SessionID,
				"error", err.Error(),
			).Warn("session_manager: failed to update session status to waiting_for_approval")
		}
	}

	// Register in-memory gate so SubmitReview can deliver the decision
	decisionCh := make(chan ApprovalDecision, 1)
	m.mu.Lock()
	m.approvals[req.ReviewID] = &approvalGate{
		sessionID:  req.SessionID,
		reviewID:   req.ReviewID,
		decisionCh: decisionCh,
	}
	m.mu.Unlock()

	// Bridge: wait for decision on gate.decisionCh, then forward to the loop's DecisionCh
	runner := m.GetRunner(req.SessionID)
	if runner == nil || runner.loopDecisionCh == nil {
		return
	}

	go func() {
		select {
		case decision := <-decisionCh:
			// Forward to the loop
			select {
			case runner.loopDecisionCh <- decision:
			default:
			}
			// Clean up gate
			m.mu.Lock()
			delete(m.approvals, req.ReviewID)
			m.mu.Unlock()

			// Update session status back to running
			if m.db != nil {
				dbCtx, dbCancel := context.WithTimeout(bgCtxForTenant(runner.tenantSchema), 5*time.Second)
				defer dbCancel()
				if _, err := m.db.ExecContext(dbCtx,
					`UPDATE agent_sessions SET status = 'running' WHERE id = $1 AND status = 'waiting_for_approval'`,
					req.SessionID); err != nil {
					logger.WithFields("session_id", req.SessionID, "error", err.Error()).
						Warn("session_manager: failed to update session status back to running")
				}
			}
		case <-runner.Done():
			// Session ended while waiting; clean up gate
			m.mu.Lock()
			delete(m.approvals, req.ReviewID)
			m.mu.Unlock()
		}
	}()
}

// SubmitReview resolves a pending approval review. Returns an error if the
// review is not pending or not on this instance.
// The DB update (WHERE status='pending') is the linearization point, preventing
// TOCTOU races and ensuring idempotency.
//
// tenantID is mandatory and included in the UPDATE / lookup WHERE
// clauses. Pre-fix the queries ran by review id alone — any caller
// could approve / deny / read the session for another tenant's
// review by guessing or harvesting a review id.
func (m *SessionManager) SubmitReview(ctx context.Context, reviewID, tenantID string, decision ApprovalDecision) error {
	if m.db == nil {
		return fmt.Errorf("approval reviews require a database connection")
	}
	if tenantID == "" {
		return fmt.Errorf("review %s: tenant context required", reviewID)
	}

	// Step 1: DB gate — atomic update acts as linearization point
	statusVal := "approved"
	if decision.Action == "deny" {
		statusVal = "denied"
	}

	var decisionsJSON []byte
	if len(decision.Decisions) > 0 {
		decisionsJSON = MarshalDecisions(decision.Decisions)
	}

	res, err := m.db.ExecContext(ctx,
		`UPDATE agent_approval_reviews
		 SET status = $2, decisions = $3, resolved_at = NOW(),
		     resolved_by = $4, resolution_reason = $5
		 WHERE id = $1 AND tenant_id = $6 AND status = 'pending'`,
		reviewID, statusVal, sql.NullString{String: string(decisionsJSON), Valid: len(decisionsJSON) > 0},
		decision.ResolvedBy, decision.Reason, tenantID)
	if err != nil {
		return fmt.Errorf("failed to update review: %w", err)
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		return fmt.Errorf("review %s is not pending (already resolved, expired, or not found)", reviewID)
	}

	// Step 2: Deliver to in-memory gate (fast path — same instance)
	m.mu.RLock()
	gate, ok := m.approvals[reviewID]
	m.mu.RUnlock()

	if ok {
		select {
		case gate.decisionCh <- decision:
			logger.WithFields(
				"review_id", reviewID,
				"action", decision.Action,
			).Info("session_manager: approval decision delivered locally")
		default:
			logger.WithFields("review_id", reviewID).Warn("session_manager: decision channel full (defensive)")
		}
		return nil
	}

	// Step 3: Cross-instance delivery via bridge.
	// Look up which instance owns this session and publish to it.
	// Tenant predicate prevents the lookup from returning a session id
	// owned by a different tenant.
	var sessionID string
	if err := m.db.QueryRowContext(ctx,
		`SELECT session_id FROM agent_approval_reviews WHERE id = $1 AND tenant_id = $2`, reviewID, tenantID).Scan(&sessionID); err != nil {
		return fmt.Errorf("review %s: failed to look up session: %w", reviewID, err)
	}

	ownerInstance, err := m.router.Lookup(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("review %s: session %s not found in router: %w", reviewID, sessionID, err)
	}

	decision.TargetInstanceID = ownerInstance
	decision.TenantID = tenantID
	if err := m.bridge.Publish(ctx, decision); err != nil {
		return fmt.Errorf("review %s: failed to publish to instance %s: %w", reviewID, ownerInstance, err)
	}

	logger.WithFields(
		"review_id", reviewID,
		"target_instance", ownerInstance,
		"action", decision.Action,
	).Info("session_manager: approval decision published to remote instance")

	return nil
}

// userInputHandler runs in a goroutine for each session. It receives
// UserInputRequest from the ask_user tool handler, registers an in-memory
// gate, and bridges the response back to the tool handler.
func (m *SessionManager) userInputHandler(ctx context.Context, sessionID string) {
	runner := m.GetRunner(sessionID)
	if runner == nil || runner.userInputRecvCh == nil {
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case req, ok := <-runner.userInputRecvCh:
			if !ok {
				return
			}
			m.handleUserInputRequest(ctx, sessionID, req, runner)
		case <-runner.Done():
			return
		}
	}
}

// handleUserInputRequest registers an in-memory gate and bridges the response
// to the ask_user tool handler when SubmitUserInput is called.
func (m *SessionManager) handleUserInputRequest(ctx context.Context, sessionID string, req UserInputRequest, runner *SessionRunner) {
	// Update session status to waiting_for_input
	if m.db != nil {
		if _, err := m.db.ExecContext(ctx,
			`UPDATE agent_sessions SET status = 'waiting_for_user_input' WHERE id = $1`,
			sessionID); err != nil {
			logger.WithFields(
				"session_id", sessionID,
				"error", err.Error(),
			).Warn("session_manager: failed to update session status to waiting_for_user_input")
		}
	}

	// Register in-memory gate so SubmitUserInput can deliver the response
	responseCh := make(chan UserInputResponse, 1)
	m.mu.Lock()
	m.userInputs[req.InputID] = &userInputGate{
		sessionID:  sessionID,
		inputID:    req.InputID,
		responseCh: responseCh,
	}
	m.mu.Unlock()

	// Bridge: wait for response on gate.responseCh, then forward to the runner
	go func() {
		select {
		case resp := <-responseCh:
			// Forward to the ask_user tool handler
			select {
			case runner.loopUserInputRespCh <- resp:
			default:
			}
			// Clean up gate
			m.mu.Lock()
			delete(m.userInputs, req.InputID)
			m.mu.Unlock()

			// Update session status back to running
			if m.db != nil {
				dbCtx, dbCancel := context.WithTimeout(bgCtxForTenant(runner.tenantSchema), 5*time.Second)
				defer dbCancel()
				if _, err := m.db.ExecContext(dbCtx,
					`UPDATE agent_sessions SET status = 'running' WHERE id = $1 AND status = 'waiting_for_user_input'`,
					sessionID); err != nil {
					logger.WithFields("session_id", sessionID, "error", err.Error()).
						Warn("session_manager: failed to update session status back to running after user input")
				}
			}
		case <-runner.Done():
			// Session ended while waiting; clean up gate
			m.mu.Lock()
			delete(m.userInputs, req.InputID)
			m.mu.Unlock()
		}
	}()
}

// SubmitUserInput delivers a user's response to a pending ask_user call.
func (m *SessionManager) SubmitUserInput(ctx context.Context, inputID string, text string) error {
	m.mu.RLock()
	gate, ok := m.userInputs[inputID]
	m.mu.RUnlock()

	if !ok {
		return fmt.Errorf("user input %s is not pending (already resolved, expired, or not found)", inputID)
	}

	select {
	case gate.responseCh <- UserInputResponse{InputID: inputID, Text: text}:
		logger.WithFields("input_id", inputID).Info("session_manager: user input response delivered")
	default:
		logger.WithFields("input_id", inputID).Warn("session_manager: user input response channel full (defensive)")
	}

	return nil
}

// GetOrCreateJobQueue returns the session-scoped job queue for the given
// session, creating one if it doesn't exist yet. This ensures the job queue
// persists across HTTP turns so async job IDs remain valid.
func (m *SessionManager) GetOrCreateJobQueue(sessionID string, maxConcurrent int) *LocalJobQueue {
	m.mu.Lock()
	defer m.mu.Unlock()
	if q, ok := m.jobQueues[sessionID]; ok {
		return q
	}
	q := NewLocalJobQueue(maxConcurrent, nil)
	m.jobQueues[sessionID] = q
	return q
}

// CleanupJobQueue shuts down and removes the job queue for a session.
func (m *SessionManager) CleanupJobQueue(sessionID string) {
	m.mu.Lock()
	q, ok := m.jobQueues[sessionID]
	if ok {
		delete(m.jobQueues, sessionID)
	}
	m.mu.Unlock()
	if ok {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = q.Shutdown(ctx)
	}
}

// SetSandboxCleanup is a no-op retained for backward compatibility.
// Sandboxes are no longer destroyed on session end — the reaper handles
// cleanup via idle retention (24h free, 7d basic, never for pro/enterprise).
// Deprecated: this method will be removed in a future release.
func (m *SessionManager) SetSandboxCleanup(fn func(ctx context.Context, sessionID string) error) {
	// Intentionally does nothing. Sandbox lifecycle is decoupled from sessions.
}

// SetDigestManager sets the digest manager for this session manager.
func (m *SessionManager) SetDigestManager(dm *DigestManager) {
	m.digestManager = dm
}

// GetDigestManager returns the digest manager, or nil.
func (m *SessionManager) GetDigestManager() *DigestManager {
	return m.digestManager
}

// GetToolLoop returns the session manager's tool loop manager.
func (m *SessionManager) GetToolLoop() *toolloop.LoopManager {
	return m.toolLoop
}

// GetInstanceID returns this server instance's identity.
func (m *SessionManager) GetInstanceID() string {
	return m.instanceID
}

// startBridgeSubscriber runs a goroutine that listens for approval decisions
// published from other instances and delivers them to the local in-memory gate.
func (m *SessionManager) startBridgeSubscriber() {
	ctx, cancel := context.WithCancel(context.Background())
	m.bridgeCancel = cancel
	m.bridgeDone = make(chan struct{})

	ch, err := m.bridge.Subscribe(ctx, m.instanceID)
	if err != nil {
		logger.WithError(err).Warn("session_manager: bridge subscribe failed, cross-instance approvals disabled")
		close(m.bridgeDone)
		return
	}
	m.bridgeCh = ch

	go func() {
		defer close(m.bridgeDone)
		for {
			select {
			case <-ctx.Done():
				return
			case decision, ok := <-ch:
				if !ok {
					return
				}
				m.deliverBridgeDecision(decision)
			}
		}
	}()
}

// deliverBridgeDecision forwards a decision received via the bridge to the
// local in-memory approval gate.
func (m *SessionManager) deliverBridgeDecision(decision ApprovalDecision) {
	m.mu.RLock()
	gate, ok := m.approvals[decision.ReviewID]
	m.mu.RUnlock()

	if !ok {
		logger.WithFields("review_id", decision.ReviewID).
			Debug("session_manager: bridge decision for unknown review (may have expired)")
		return
	}

	select {
	case gate.decisionCh <- decision:
		logger.WithFields(
			"review_id", decision.ReviewID,
			"action", decision.Action,
		).Info("session_manager: bridge decision delivered to local gate")
	default:
		logger.WithFields("review_id", decision.ReviewID).
			Warn("session_manager: bridge decision channel full")
	}
}

// isTerminalFinishReason returns true if the given finish reason indicates
// the session has reached a terminal state and should not continue.
// Sessions ending with "stop" or "end_turn" remain in waiting_for_input
// and can accept more user input.
func isTerminalFinishReason(reason string) bool {
	switch reason {
	case "error", "cancelled", "explicit_complete":
		// Truly terminal: session cannot/should not continue.
		return true
	default:
		// Non-terminal: session stays in waiting_for_input so additional turns
		// can be sent. This includes "stop", "end_turn", "max_iterations",
		// "max_steps", "max_tool_calls", "timeout", "token_budget_exhausted",
		// "interrupted", and "" (empty). The agent hit a per-turn limit but
		// can continue working on the next turn.
		return false
	}
}

// ─── Phase 5: Agent Session Registry ──────────────────────────────────

// RegisterAgentSession maps an agent ID to its active session ID and creates
// the peer message channel for cross-agent communication.
func (m *SessionManager) RegisterAgentSession(agentID, sessionID string) <-chan PeerMessage {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.agentSessions[agentID] = sessionID
	ch := make(chan PeerMessage, 32)
	m.peerChannels[sessionID] = ch
	return ch
}

// UnregisterAgentSession removes the agent→session mapping and closes
// the peer message channel.
func (m *SessionManager) UnregisterAgentSession(agentID, sessionID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.agentSessions[agentID] == sessionID {
		delete(m.agentSessions, agentID)
	}
	if ch, ok := m.peerChannels[sessionID]; ok {
		close(ch)
		delete(m.peerChannels, sessionID)
	}
}

// LookupAgentSession returns the active session ID for the given agent ID.
// Returns ("", false) if the agent has no active session on this instance.
func (m *SessionManager) LookupAgentSession(agentID string) (string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sid, ok := m.agentSessions[agentID]
	return sid, ok
}

// DeliverPeerMessage sends a message to an agent's active session on this instance.
// Returns false if the agent has no active session or the channel is full.
func (m *SessionManager) DeliverPeerMessage(targetAgentID string, msg PeerMessage) bool {
	m.mu.RLock()
	sessionID, ok := m.agentSessions[targetAgentID]
	if !ok {
		m.mu.RUnlock()
		return false
	}
	ch, ok := m.peerChannels[sessionID]
	m.mu.RUnlock()
	if !ok {
		return false
	}

	select {
	case ch <- msg:
		return true
	default:
		// Channel full — drop message (caller should queue to DB)
		return false
	}
}
