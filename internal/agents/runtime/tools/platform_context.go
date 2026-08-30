package tools

import (
	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/query"
)

// PlatformToolContext provides shared dependencies for all platform tools.
// Platform tools allow the platform meta-agent to manage agents, query
// observability data, and perform other control-plane operations via the
// chat-first UI.
type PlatformToolContext struct {
	CommandBus commands.CommandBus
	QueryBus   query.QueryBus
	TenantID   string
	UserID     string
	Emitter    *agentrt.Emitter
	SessionID  string
}

// emitSystemBlock emits a system_block event through the emitter.
func (c *PlatformToolContext) emitSystemBlock(blockType string, payload map[string]interface{}) {
	if c.Emitter == nil {
		return
	}
	c.Emitter.Emit(agentrt.Event{
		Type:      agentrt.EventSystemBlock,
		SessionID: c.SessionID,
		Data: map[string]interface{}{
			"block_type": blockType,
			"payload":    payload,
		},
	})
}

// NewPlatformHandlers creates all platform tool handlers.
func NewPlatformHandlers(ctx *PlatformToolContext) []SyntheticToolHandler {
	return []SyntheticToolHandler{
		&PlatformCreateAgentHandler{Ctx: ctx},
		&PlatformListAgentsHandler{Ctx: ctx},
		&PlatformGetAgentHandler{Ctx: ctx},
		&PlatformUpdateAgentHandler{Ctx: ctx},
		&PlatformDeleteAgentHandler{Ctx: ctx},
	}
}
