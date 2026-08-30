package runtime

import "time"

// EventType identifies the kind of runtime lifecycle event.
type EventType string

const (
	EventSessionStart      EventType = "session.start"
	EventSessionEnd        EventType = "session.end"
	EventSessionError      EventType = "session.error"
	EventTurnStart         EventType = "turn.start"
	EventTurnEnd           EventType = "turn.end"
	EventLLMStart          EventType = "llm.start"
	EventLLMChunk          EventType = "llm.chunk"
	EventLLMEnd            EventType = "llm.end"
	EventCacheHit          EventType = "cache.hit"
	EventCacheMiss         EventType = "cache.miss"
	EventCacheStore        EventType = "cache.store"
	EventToolCallStart     EventType = "tool_call.start"
	EventToolCallEnd       EventType = "tool_call.end"
	EventToolCallError     EventType = "tool_call.error"
	EventSteerReceived     EventType = "steer.received"
	EventCheckpoint        EventType = "checkpoint"
	EventTermination       EventType = "termination"
	EventApprovalRequested EventType = "approval.requested"
	EventApprovalResolved  EventType = "approval.resolved"
	EventApprovalHeartbeat EventType = "approval.heartbeat"
	EventApprovalCancelled EventType = "approval.cancelled"
	EventSpawnStart        EventType = "spawn.start"
	EventSpawnEnd          EventType = "spawn.end"
	EventSpawnError        EventType = "spawn.error"
	EventPolicyDecision    EventType = "policy.decision"

	// Sandbox lifecycle events
	EventSandboxCreate         EventType = "sandbox.create"
	EventSandboxReady          EventType = "sandbox.ready"
	EventSandboxExec           EventType = "sandbox.exec"
	EventSandboxResult         EventType = "sandbox.result"
	EventSandboxError          EventType = "sandbox.error"
	EventSandboxDestroy        EventType = "sandbox.destroy"
	EventSandboxGitClone       EventType = "sandbox.git.clone"
	EventSandboxPortExpose     EventType = "sandbox.port.expose"
	EventSandboxPortUnexpose   EventType = "sandbox.port.unexpose"
	EventSandboxTemplateSelect EventType = "sandbox.template.select"

	// User input (ask_user) lifecycle events
	EventUserInputRequested EventType = "user_input.requested"
	EventUserInputReceived  EventType = "user_input.received"
	EventUserInputHeartbeat EventType = "user_input.heartbeat"
	EventUserInputCancelled EventType = "user_input.cancelled"

	// Fallback lifecycle events
	EventFallbackTriggered EventType = "fallback.triggered"
	EventFallbackSucceeded EventType = "fallback.succeeded"
	EventFallbackFailed    EventType = "fallback.failed"

	// Planner lifecycle events
	EventPlanStart    EventType = "plan.start"
	EventPlanComplete EventType = "plan.complete"

	// Async job lifecycle events (Phase 1)
	EventJobEnqueued  EventType = "job.enqueued"
	EventJobStarted   EventType = "job.started"
	EventJobCompleted EventType = "job.completed"
	EventJobFailed    EventType = "job.failed"
	EventJobCancelled EventType = "job.cancelled"

	// Fork lifecycle events (Phase 2)
	EventForkStart     EventType = "fork.start"
	EventForkCompleted EventType = "fork.completed"
	EventForkFailed    EventType = "fork.failed"
	EventForkCancelled EventType = "fork.cancelled"

	// Web search/fetch lifecycle events
	EventWebSearchStart   EventType = "web.search.start"
	EventWebSearchResults EventType = "web.search.results"
	EventWebFetchStart    EventType = "web.fetch.start"
	EventWebFetchComplete EventType = "web.fetch.complete"

	// Compaction lifecycle events (Phase 3)
	EventCompactionTriggered EventType = "compaction.triggered"
	EventCompactionApplied   EventType = "compaction.applied"
	EventCompactionFailed    EventType = "compaction.failed"

	// Digest lifecycle events (Phase 4)
	EventDigestRefreshed EventType = "digest.refreshed"
	EventDigestFailed    EventType = "digest.failed"

	// Skill lifecycle events
	EventSkillStart EventType = "skill.start"
	EventSkillEnd   EventType = "skill.end"
	EventSkillError EventType = "skill.error"

	// Platform system block events (chat-first UI)
	EventSystemBlock EventType = "system_block"

	// Browser automation lifecycle events
	EventBrowserStart      EventType = "browser.start"
	EventBrowserReady      EventType = "browser.ready"
	EventBrowserNavigate   EventType = "browser.navigate"
	EventBrowserScreenshot EventType = "browser.screenshot"
	EventBrowserAction     EventType = "browser.action"
	EventBrowserError      EventType = "browser.error"
	EventBrowserClose      EventType = "browser.close"
)

// Event represents a single runtime lifecycle event.
// Hot-path typed fields avoid map lookups during streaming.
type Event struct {
	Type       EventType              `json:"type"`
	SessionID  string                 `json:"session_id"`
	TurnNumber int32                  `json:"turn_number,omitempty"`
	Timestamp  time.Time              `json:"timestamp"`
	Data       map[string]interface{} `json:"data,omitempty"`

	// HITL approval gate
	ReviewID string `json:"review_id,omitempty"`

	// HITL user input (ask_user)
	UserInputID string `json:"user_input_id,omitempty"`

	// Hot-path typed fields for streaming
	TextDelta    string      `json:"text_delta,omitempty"`
	ToolCallID   string      `json:"tool_call_id,omitempty"`
	ToolName     string      `json:"tool_name,omitempty"`
	ToolArgs     string      `json:"tool_args,omitempty"`
	ToolResult   string      `json:"tool_result,omitempty"`
	ToolSuccess  bool        `json:"tool_success,omitempty"`
	ToolDuration int64       `json:"tool_duration_ms,omitempty"`
	Reason       string      `json:"reason,omitempty"`
	Usage        *UsageDelta `json:"usage,omitempty"`
	Error        string      `json:"error,omitempty"`

	// Sandbox hot-path fields
	SandboxID         string `json:"sandbox_id,omitempty"`
	SandboxExitCode   int    `json:"sandbox_exit_code,omitempty"`
	SandboxDurationMs int64  `json:"sandbox_duration_ms,omitempty"`

	// Fallback hot-path fields
	FallbackFromModel string `json:"fallback_from_model,omitempty"`
	FallbackToModel   string `json:"fallback_to_model,omitempty"`
	FallbackAttempt   int32  `json:"fallback_attempt,omitempty"`
}

// UsageDelta tracks token usage for a single LLM call.
//
// PromptTokens is inclusive of cached tokens (cached + fresh = prompt),
// matching the gateway Usage convention. CacheReadTokens and
// CacheWriteTokens are non-overlapping subsets of PromptTokens so
// consumers can compute the fresh portion (PromptTokens − CacheReadTokens
// − CacheWriteTokens) and apply per-bucket billing rates.
type UsageDelta struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}
