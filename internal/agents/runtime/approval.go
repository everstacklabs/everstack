package runtime

import (
	"encoding/json"
	"path/filepath"
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// HITLConfig holds the human-in-the-loop approval gate configuration.
// Nested under the existing agent config JSONB column as config.hitl.
type HITLConfig struct {
	Tools          []string `json:"tools"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	DefaultAction  string   `json:"default_action"`
	MatchMode      string   `json:"match_mode"`
}

// ApprovalRequest is sent from the loop to SessionManager for DB persistence.
type ApprovalRequest struct {
	ReviewID      string
	SessionID     string
	TenantID      string
	AgentID       string
	TurnNumber    int32
	Iteration     int32
	ToolCalls     []gw.ToolCall
	Config        *HITLConfig
	ExpiresAt     time.Time
}

// ApprovalDecision is sent from SessionManager back to the loop.
type ApprovalDecision struct {
	ReviewID         string            `json:"review_id"`
	Action           string            `json:"action"` // "approve" or "deny"
	Decisions        []PerToolDecision `json:"decisions,omitempty"`
	Reason           string            `json:"reason,omitempty"`
	ResolvedBy       string            `json:"resolved_by,omitempty"`
	TargetInstanceID string            `json:"target_instance_id,omitempty"` // set by SubmitReview for cross-instance routing
	TenantID         string            `json:"tenant_id,omitempty"`          // tenant scope for pub/sub channel isolation
}

// PerToolDecision is a per-tool approval decision (v2: per-tool overrides).
type PerToolDecision struct {
	ToolCallID string `json:"tool_call_id"`
	Action     string `json:"action"`
	Reason     string `json:"reason"`
}

// ParseHITLConfig extracts the HITL configuration from the agent config map.
// Returns nil if no hitl key or empty tools.
func ParseHITLConfig(config map[string]interface{}) *HITLConfig {
	if config == nil {
		return nil
	}
	hitlRaw, ok := config["hitl"]
	if !ok {
		return nil
	}
	hitlMap, ok := hitlRaw.(map[string]interface{})
	if !ok {
		return nil
	}

	cfg := &HITLConfig{
		TimeoutSeconds: 3600,
		DefaultAction:  "deny",
		MatchMode:      "exact",
	}

	if tools, ok := hitlMap["tools"].([]interface{}); ok {
		for _, t := range tools {
			if ts, ok := t.(string); ok {
				cfg.Tools = append(cfg.Tools, ts)
			}
		}
	}
	if len(cfg.Tools) == 0 {
		return nil
	}

	if timeout, ok := hitlMap["timeout_seconds"].(float64); ok && timeout > 0 {
		cfg.TimeoutSeconds = int(timeout)
	}
	if da, ok := hitlMap["default_action"].(string); ok && (da == "approve" || da == "deny") {
		cfg.DefaultAction = da
	}
	if mm, ok := hitlMap["match_mode"].(string); ok && (mm == "exact" || mm == "glob") {
		cfg.MatchMode = mm
	}

	return cfg
}

// FilterToolCallsNeedingApproval returns tool calls that match the HITL config patterns.
func FilterToolCallsNeedingApproval(toolCalls []gw.ToolCall, cfg *HITLConfig) []gw.ToolCall {
	if cfg == nil || len(cfg.Tools) == 0 {
		return nil
	}

	var matched []gw.ToolCall
	for _, tc := range toolCalls {
		if matchesToolPattern(tc.Function.Name, cfg.Tools, cfg.MatchMode) {
			matched = append(matched, tc)
		}
	}
	return matched
}

// matchesToolPattern checks if a tool name matches any of the patterns.
func matchesToolPattern(toolName string, patterns []string, matchMode string) bool {
	for _, pattern := range patterns {
		if matchMode == "glob" {
			if matched, _ := filepath.Match(pattern, toolName); matched {
				return true
			}
		} else {
			if pattern == toolName {
				return true
			}
		}
	}
	return false
}

// ExpandBatchDecision expands a batch action into per-tool decisions.
func ExpandBatchDecision(action string, toolCalls []gw.ToolCall, reason string) []PerToolDecision {
	decisions := make([]PerToolDecision, len(toolCalls))
	for i, tc := range toolCalls {
		decisions[i] = PerToolDecision{
			ToolCallID: tc.ID,
			Action:     action,
			Reason:     reason,
		}
	}
	return decisions
}

// MarshalDecisions converts per-tool decisions to JSON.
func MarshalDecisions(decisions []PerToolDecision) json.RawMessage {
	data, err := json.Marshal(decisions)
	if err != nil {
		return []byte("[]")
	}
	return data
}

// ValidateHITLConfig validates the hitl section of an agent config.
func ValidateHITLConfig(config map[string]interface{}) error {
	if config == nil {
		return nil
	}
	hitlRaw, ok := config["hitl"]
	if !ok {
		return nil
	}
	hitlMap, ok := hitlRaw.(map[string]interface{})
	if !ok {
		return nil
	}

	// Validate tools
	if toolsRaw, ok := hitlMap["tools"]; ok {
		tools, ok := toolsRaw.([]interface{})
		if !ok {
			return errInvalid("hitl.tools must be an array of strings")
		}
		for _, t := range tools {
			if _, ok := t.(string); !ok {
				return errInvalid("hitl.tools must be an array of strings")
			}
		}
	}

	// Validate timeout_seconds
	if timeout, ok := hitlMap["timeout_seconds"]; ok {
		ts, ok := timeout.(float64)
		if !ok || ts < 1 || ts > 86400 {
			return errInvalid("hitl.timeout_seconds must be between 1 and 86400")
		}
	}

	// Validate default_action
	if da, ok := hitlMap["default_action"]; ok {
		daStr, ok := da.(string)
		if !ok || (daStr != "approve" && daStr != "deny") {
			return errInvalid("hitl.default_action must be 'approve' or 'deny'")
		}
	}

	// Validate match_mode
	if mm, ok := hitlMap["match_mode"]; ok {
		mmStr, ok := mm.(string)
		if !ok || (mmStr != "exact" && mmStr != "glob") {
			return errInvalid("hitl.match_mode must be 'exact' or 'glob'")
		}
	}

	return nil
}

type hitlValidationError struct {
	msg string
}

func (e *hitlValidationError) Error() string { return e.msg }

func errInvalid(msg string) error {
	return &hitlValidationError{msg: msg}
}
