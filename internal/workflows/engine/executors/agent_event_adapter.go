package executors

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

// AgentEventSinkAdapter bridges the agent runtime EventSink interface to the
// workflow engine's ExecutionEvent callback. It translates agent loop lifecycle
// events (llm.chunk, tool_call.start, etc.) into ExecutionEvent values that
// the workflow streaming infrastructure understands.
//
// Implements agentrt.EventSink.
type AgentEventSinkAdapter struct {
	ctx         context.Context
	tenantID    string
	executionID string
	nodeID      string
	nodeType    string
	nodeLabel   string
	onEvent     func(engine.ExecutionEvent) error
	artifacts   browserArtifactStore

	browserSequence atomic.Int64
}

type browserArtifactStore interface {
	UploadObject(
		ctx context.Context,
		tenantID, purpose, filename, contentType string,
		data io.Reader,
		size int64,
		refType, refID string,
	) (string, error)
}

// Compile-time interface check.
var _ agentrt.EventSink = (*AgentEventSinkAdapter)(nil)

// OnEvent maps an agent runtime Event to a workflow ExecutionEvent and
// forwards it via the onEvent callback.
func (a *AgentEventSinkAdapter) OnEvent(evt agentrt.Event) error {
	if a.onEvent == nil {
		return nil
	}

	if evt.Type == agentrt.EventBrowserScreenshot {
		evt = a.persistBrowserSnapshot(evt)
	}
	we := a.mapEvent(evt)
	return a.onEvent(we)
}

// mapEvent converts a single agent runtime Event into a workflow ExecutionEvent.
func (a *AgentEventSinkAdapter) mapEvent(evt agentrt.Event) engine.ExecutionEvent {
	base := engine.ExecutionEvent{
		NodeID:    a.nodeID,
		NodeType:  a.nodeType,
		NodeLabel: a.nodeLabel,
		Timestamp: evt.Timestamp,
	}

	switch evt.Type {
	// ----------------------------------------------------------------
	// LLM streaming chunk -> workflow "chunk"
	// ----------------------------------------------------------------
	case agentrt.EventLLMChunk:
		base.Type = "chunk"
		base.ChunkContent = evt.TextDelta

	// ----------------------------------------------------------------
	// Tool call lifecycle
	// ----------------------------------------------------------------
	case agentrt.EventToolCallStart:
		base.Type = "agent.tool_call.start"
		base.Data = map[string]string{
			"tool_call_id": evt.ToolCallID,
			"tool_name":    evt.ToolName,
			"tool_args":    sanitizeWorkflowToolArgs(evt.ToolName, evt.ToolArgs),
		}

	case agentrt.EventToolCallEnd:
		base.Type = "agent.tool_call.end"
		base.Data = map[string]string{
			"tool_call_id": evt.ToolCallID,
			"tool_name":    evt.ToolName,
			"tool_result":  truncateEventString(evt.ToolResult, 4000),
			"tool_success": fmt.Sprintf("%v", evt.ToolSuccess),
			"duration_ms":  fmt.Sprintf("%d", evt.ToolDuration),
		}
		base.DurationMs = evt.ToolDuration

	case agentrt.EventToolCallError:
		base.Type = "agent.tool_call.error"
		base.Error = evt.Error
		base.Data = map[string]string{
			"tool_call_id": evt.ToolCallID,
			"tool_name":    evt.ToolName,
			"tool_result":  truncateEventString(evt.ToolResult, 4000),
			"duration_ms":  fmt.Sprintf("%d", evt.ToolDuration),
		}
		base.DurationMs = evt.ToolDuration

	// ----------------------------------------------------------------
	// Turn lifecycle
	// ----------------------------------------------------------------
	case agentrt.EventTurnStart:
		base.Type = "agent.turn.start"
		base.Data = buildStringData(evt.Data)

	case agentrt.EventTurnEnd:
		base.Type = "agent.turn.end"
		base.Data = buildStringData(evt.Data)
		if evt.Usage != nil {
			mergeUsageData(base.Data, evt.Usage)
		}

	// ----------------------------------------------------------------
	// LLM call boundaries (informational)
	// ----------------------------------------------------------------
	case agentrt.EventLLMStart:
		base.Type = "agent.llm.start"
		base.Data = buildStringData(evt.Data)

	case agentrt.EventLLMEnd:
		base.Type = "agent.llm.end"
		base.Data = buildStringData(evt.Data)
		if evt.Usage != nil {
			mergeUsageData(base.Data, evt.Usage)
		}
		if evt.Error != "" {
			base.Error = evt.Error
		}

	// ----------------------------------------------------------------
	// HITL approval
	// ----------------------------------------------------------------
	case agentrt.EventApprovalRequested:
		base.Type = "agent.approval.requested"
		base.Data = buildStringData(evt.Data)
		if evt.ReviewID != "" {
			if base.Data == nil {
				base.Data = make(map[string]string)
			}
			base.Data["review_id"] = evt.ReviewID
		}

	case agentrt.EventApprovalResolved:
		base.Type = "agent.approval.resolved"
		base.Data = buildStringData(evt.Data)
		if evt.ReviewID != "" {
			if base.Data == nil {
				base.Data = make(map[string]string)
			}
			base.Data["review_id"] = evt.ReviewID
		}

	case agentrt.EventApprovalHeartbeat:
		base.Type = "agent.approval.heartbeat"
		if evt.ReviewID != "" {
			base.Data = map[string]string{"review_id": evt.ReviewID}
		}

	case agentrt.EventApprovalCancelled:
		base.Type = "agent.approval.cancelled"
		base.Data = map[string]string{
			"review_id": evt.ReviewID,
			"reason":    evt.Reason,
		}

	// ----------------------------------------------------------------
	// Termination / session errors
	// ----------------------------------------------------------------
	case agentrt.EventTermination:
		base.Type = "agent.termination"
		base.Data = map[string]string{"reason": evt.Reason}

	case agentrt.EventSessionError:
		base.Type = "agent.error"
		base.Error = evt.Error

	case agentrt.EventSteerReceived:
		base.Type = "agent.steer.received"
		base.Data = buildStringData(evt.Data)

	case agentrt.EventCheckpoint:
		base.Type = "agent.checkpoint"
		base.Data = buildStringData(evt.Data)

	case agentrt.EventSessionStart:
		base.Type = "agent.session.start"
		base.Data = buildStringData(evt.Data)

	case agentrt.EventSessionEnd:
		base.Type = "agent.session.end"
		base.Data = buildStringData(evt.Data)

	// ----------------------------------------------------------------
	// Browser/computer-use lifecycle. Screenshots are artifact references,
	// never inline base64, so workflow execution logs remain bounded.
	// ----------------------------------------------------------------
	case agentrt.EventBrowserStart:
		base.Type = "agent.browser.started"
		base.Data = buildStringData(evt.Data)

	case agentrt.EventBrowserReady:
		base.Type = "agent.browser.ready"
		base.Data = buildStringData(evt.Data)
		// Runtime topology is never part of the durable workflow contract.
		// Viewers resolve an authenticated same-origin capability from the run
		// identity; they must not receive a pod, CDP endpoint, or raw stream URL.
		delete(base.Data, "stream_url")
		delete(base.Data, "cdp_url")
		delete(base.Data, "pod")

	case agentrt.EventBrowserNavigate:
		base.Type = "agent.browser.navigate"
		base.Data = buildStringData(evt.Data)

	case agentrt.EventBrowserAction:
		base.Type = "agent.browser.action"
		base.Data = buildStringData(evt.Data)

	case agentrt.EventBrowserScreenshot:
		base.Type = "agent.browser.snapshot"
		base.Data = buildStringData(evt.Data)

	case agentrt.EventBrowserError:
		base.Type = "agent.browser.error"
		base.Data = buildStringData(evt.Data)
		base.Error = evt.Error
		if base.Error == "" && base.Data != nil {
			base.Error = base.Data["error"]
		}

	case agentrt.EventBrowserClose:
		base.Type = "agent.browser.closed"
		base.Data = buildStringData(evt.Data)

	// ----------------------------------------------------------------
	// Fallback: unknown event types get a generic mapping
	// ----------------------------------------------------------------
	default:
		base.Type = fmt.Sprintf("agent.%s", evt.Type)
		base.Data = buildStringData(evt.Data)
		if evt.Error != "" {
			base.Error = evt.Error
		}
	}

	if isBrowserEvent(evt.Type) {
		if base.Data == nil {
			base.Data = make(map[string]string)
		}
		base.Data["sequence"] = fmt.Sprintf("%d", a.browserSequence.Add(1))
		base.Data["browser_session_id"] = evt.SessionID
	}

	// Ensure timestamp is set
	if base.Timestamp.IsZero() {
		base.Timestamp = time.Now()
	}

	return base
}

func (a *AgentEventSinkAdapter) persistBrowserSnapshot(evt agentrt.Event) agentrt.Event {
	rawBase64, _ := evt.Data["image_base64"].(string)
	data := cloneEventData(evt.Data)
	delete(data, "image_base64")
	evt.Data = data

	if rawBase64 == "" {
		data["snapshot_status"] = "missing"
		return evt
	}
	image, err := base64.StdEncoding.DecodeString(rawBase64)
	if err != nil {
		data["snapshot_status"] = "invalid"
		data["snapshot_error"] = "invalid base64 image"
		return evt
	}
	if len(image) > 750_000 {
		data["snapshot_status"] = "too_large"
		data["snapshot_error"] = "snapshot exceeds artifact limit"
		return evt
	}
	if a.artifacts == nil || a.tenantID == "" {
		data["snapshot_status"] = "unavailable"
		data["snapshot_error"] = "artifact storage is not configured"
		return evt
	}

	uploadCtx := a.ctx
	if uploadCtx == nil {
		uploadCtx = context.Background()
	}
	uploadCtx, cancel := context.WithTimeout(context.WithoutCancel(uploadCtx), 15*time.Second)
	defer cancel()

	filename := fmt.Sprintf("browser-%d.jpg", evt.Timestamp.UnixNano())
	objectID, err := a.artifacts.UploadObject(
		uploadCtx,
		a.tenantID,
		"artifact",
		filename,
		"image/jpeg",
		bytes.NewReader(image),
		int64(len(image)),
		"workflow_execution",
		a.executionID,
	)
	if err != nil {
		data["snapshot_status"] = "failed"
		data["snapshot_error"] = truncateEventString(err.Error(), 240)
		return evt
	}

	data["artifact_id"] = objectID
	data["content_type"] = "image/jpeg"
	data["snapshot_status"] = "stored"
	data["size_bytes"] = len(image)
	return evt
}

// ---------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------

func cloneEventData(src map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(src)+2)
	for key, value := range src {
		out[key] = value
	}
	return out
}

func isBrowserEvent(eventType agentrt.EventType) bool {
	switch eventType {
	case agentrt.EventBrowserStart,
		agentrt.EventBrowserReady,
		agentrt.EventBrowserNavigate,
		agentrt.EventBrowserAction,
		agentrt.EventBrowserScreenshot,
		agentrt.EventBrowserError,
		agentrt.EventBrowserClose:
		return true
	default:
		return false
	}
}

// buildStringData converts a map[string]interface{} to map[string]string,
// formatting non-string values with fmt.Sprintf.
func buildStringData(src map[string]interface{}) map[string]string {
	if len(src) == 0 {
		return nil
	}
	out := make(map[string]string, len(src))
	for k, v := range src {
		switch tv := v.(type) {
		case string:
			out[k] = tv
		default:
			out[k] = fmt.Sprintf("%v", v)
		}
	}
	return out
}

// mergeUsageData adds token usage fields into an existing string data map.
func mergeUsageData(data map[string]string, usage *agentrt.UsageDelta) {
	if data == nil || usage == nil {
		return
	}
	data["prompt_tokens"] = fmt.Sprintf("%d", usage.PromptTokens)
	data["completion_tokens"] = fmt.Sprintf("%d", usage.CompletionTokens)
	data["total_tokens"] = fmt.Sprintf("%d", usage.TotalTokens)
}

// truncateEventString truncates a string to maxLen for inclusion in event data.
func truncateEventString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func sanitizeWorkflowToolArgs(toolName, raw string) string {
	if toolName != "browser_type" || strings.TrimSpace(raw) == "" {
		return truncateEventString(raw, 4000)
	}
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return `{"text":"[redacted]"}`
	}
	if value, ok := args["text"].(string); ok {
		args["text"] = fmt.Sprintf("[redacted %d chars]", len(value))
	}
	sanitized, err := json.Marshal(args)
	if err != nil {
		return `{"text":"[redacted]"}`
	}
	return truncateEventString(string(sanitized), 4000)
}
