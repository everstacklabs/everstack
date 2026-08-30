package executors

import (
	"context"
	"fmt"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/providers/ratelimit"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

// RateLimiterExecutor handles rate limiting nodes in the workflow.
//
// It uses the gateway's reactive rate limiting infrastructure (GlobalMonitor)
// which tracks provider rate limit state from response headers.
//
// Config fields (from frontend RateLimiterConfig):
//   - (none required — rate limit state is driven by provider responses)
//
// The executor checks whether the resolved provider is currently rate-limited
// based on upstream response headers tracked by the GlobalMonitor.
//
// Handles: "out" on success. Returns an error if rate limited.
type RateLimiterExecutor struct{}

func (e *RateLimiterExecutor) NodeType() string { return "rateLimiter" }

func (e *RateLimiterExecutor) Execute(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	provider := ec.ResolvedProvider

	// If no provider is resolved yet, pass through — the rate limit check
	// happens based on response headers from the provider, so this is a
	// pre-flight check for known rate-limited providers.
	if provider == "" {
		logger.Debug("rate limiter: no provider resolved yet, passing through")
		return engine.NodeResult{NextHandle: "out", Output: map[string]interface{}{
			"provider":        "",
			"is_rate_limited": false,
		}}
	}

	if shouldBackoff, waitDuration := ratelimit.GlobalMonitor.ShouldBackoff(provider); shouldBackoff {
		logger.WithFields("provider", provider, "wait", waitDuration.String()).
			Warn("rate limiter: upstream provider is rate limited")
		return engine.NodeResult{
			Error: fmt.Errorf("rate limited by upstream provider %q (retry after %s)", provider, waitDuration.Round(time.Second)),
		}
	}

	ec.SetNodeData("is_rate_limited", "false")
	ec.SetNodeData("provider", provider)

	logger.WithFields("provider", provider).
		Debug("rate limiter: request allowed")

	return engine.NodeResult{NextHandle: "out", Output: map[string]interface{}{
		"provider":        provider,
		"is_rate_limited": false,
	}}
}
