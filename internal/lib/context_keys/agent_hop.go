package contextkeys

import (
	"context"
	"errors"
	"net/http"
	"strconv"
)

const AgentHopHeader = "X-Evs-Agent-Hop"

type agentHopKey struct{}

func WithAgentHop(ctx context.Context, hop int) context.Context {
	return context.WithValue(ctx, agentHopKey{}, hop)
}
func AgentHop(ctx context.Context) int { n, _ := ctx.Value(agentHopKey{}).(int); return n }
func ReadAgentHop(ctx context.Context, h http.Header) context.Context {
	values := h.Values(AgentHopHeader)
	if len(values) == 0 {
		return WithAgentHop(ctx, 0)
	}
	n, err := strconv.Atoi(h.Get(AgentHopHeader))
	if len(values) != 1 || err != nil || n < 0 || n > 16 {
		n = 16
	}
	return WithAgentHop(ctx, n)
}
func WriteAgentHop(ctx context.Context, h http.Header) {
	// Configuration headers must never reset the runtime's depth.
	h.Del(AgentHopHeader)
	if n := AgentHop(ctx); n > 0 {
		h.Set(AgentHopHeader, strconv.Itoa(n))
	}
}
func NextAgentHop(ctx context.Context) (context.Context, error) {
	if AgentHop(ctx) >= 2 {
		return ctx, errors.New("agent call depth limit reached")
	}
	return WithAgentHop(ctx, AgentHop(ctx)+1), nil
}
