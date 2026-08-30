package compact

import (
	"context"
	"fmt"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// SummarizerResolver is invoked lazily to obtain the summarisation
// provider. The middleware can't bind the provider at construction
// time because of a chicken-and-egg between provider-factory wiring
// and gateway-router construction — by the time the router can resolve
// the summarisation model, the per-provider middleware chain has
// already been built. Pattern matches agents/runtime/engine.go's
// ResolveProvider lookup.
//
// Resolver returns the ChatProvider that can serve the configured
// summarisation model. If it returns an error, the middleware skips
// summarisation for that turn (no fallback to truncation — the
// caller's input still hits the inner provider with full context).
type SummarizerResolver func(ctx context.Context, model string) (gw.ChatProvider, error)

// Middleware wraps a gw.Provider with context-window compaction.
// Implements both unary Chat and streaming ChatStream paths so any
// provider that already supports the gw.Provider interface can be
// wrapped without changes elsewhere.
//
// Behaviour:
//
//  1. Skip if cfg.Enabled is false, or if the wrapped provider's name
//     isn't in cfg.EnabledForProviders, or if the caller passed
//     req.Metadata["compact"] = "off".
//  2. Otherwise call Compactor.Decide with the gateway-side estimate
//     (we don't have provider-reported tokens until after the call).
//  3. If ActionSummarize, run the summariser and splice the summary
//     into the messages slice before delegating.
//  4. If ActionTruncate, splice a sentinel message into the slice
//     before delegating.
//
// The middleware does not retry on summarisation failure — it logs +
// emits a warn and falls through with the original messages. The next
// turn will hit a higher tier and self-correct.
type Middleware struct {
	inner             gw.Provider
	providerName      string
	cfg               Config
	compactor         *Compactor
	summarizerResolve SummarizerResolver
}

// NewMiddleware constructs a Middleware over inner. providerName is
// the wrapped provider's Name() value, captured at construction so we
// don't call Name() on every turn. resolver may be nil — when nil the
// middleware still operates but only the truncation tier fires
// (summarisation requires a resolver to obtain the LLM).
func NewMiddleware(inner gw.Provider, providerName string, cfg Config, resolver SummarizerResolver) *Middleware {
	return &Middleware{
		inner:             inner,
		providerName:      providerName,
		cfg:               cfg,
		compactor:         New(cfg),
		summarizerResolve: resolver,
	}
}

// Name proxies to the inner provider so logging / telemetry treat the
// middleware as transparent. Same pattern as LoggingMiddleware.
func (m *Middleware) Name() string {
	if n, ok := m.inner.(interface{ Name() string }); ok {
		return n.Name()
	}
	return m.providerName
}

// SupportsModel proxies to the inner provider.
func (m *Middleware) SupportsModel(model string) bool {
	if s, ok := m.inner.(interface{ SupportsModel(string) bool }); ok {
		return s.SupportsModel(model)
	}
	return false
}

// Chat applies compaction synchronously then delegates to the inner
// provider. Errors from compaction are non-fatal — we log and pass
// through the original request.
func (m *Middleware) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	req = m.maybeCompact(ctx, req)
	if chat, ok := m.inner.(gw.ChatProvider); ok {
		return chat.Chat(ctx, req)
	}
	return gw.ChatCompletionResponse{}, fmt.Errorf("compact: inner provider does not implement ChatProvider")
}

// ChatStream applies compaction synchronously then delegates streaming
// to the inner provider.
func (m *Middleware) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	req = m.maybeCompact(ctx, req)
	if chat, ok := m.inner.(gw.ChatProvider); ok {
		return chat.ChatStream(ctx, req, onChunk)
	}
	return fmt.Errorf("compact: inner provider does not implement ChatProvider")
}

// Embed forwards directly — embeddings don't have a compaction concern
// and may not be implemented by the inner provider, in which case we
// surface gw.ErrNotSupported the same way the underlying provider
// would.
func (m *Middleware) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	if e, ok := m.inner.(gw.EmbeddingsProvider); ok {
		return e.Embed(ctx, req)
	}
	return gw.EmbeddingsResponse{}, gw.ErrNotSupported{Operation: "embed", Provider: m.Name()}
}

// Unwrap returns the inner provider, mirroring the pattern used by
// LoggingMiddleware so capability discovery through middleware layers
// keeps working.
func (m *Middleware) Unwrap() gw.Provider { return m.inner }

// maybeCompact runs the gateway-side compaction step. Returns the
// (possibly modified) request to forward to the inner provider.
func (m *Middleware) maybeCompact(ctx context.Context, req gw.ChatCompletionRequest) gw.ChatCompletionRequest {
	if !m.cfg.Enabled {
		return req
	}
	if !m.cfg.IsProviderAllowed(m.providerName) {
		return req
	}
	// Per-call opt-out. Useful for clients that already manage their
	// own context window and don't want the gateway to second-guess
	// them (long-running agents that maintain their own summary stack,
	// for instance).
	if v, ok := req.Metadata["compact"]; ok {
		if s, _ := v.(string); s == "off" {
			return req
		}
	}
	if len(req.Messages) == 0 {
		return req
	}

	estimated := Estimate(req.Messages)
	decision := m.compactor.Decide(req.Messages, estimated)

	switch decision.Action {
	case ActionNone:
		return req
	case ActionTruncate:
		req.Messages = applyTruncate(req.Messages, decision)
		logger.WithFields(
			"provider", m.providerName,
			"tier", string(decision.Tier),
			"freed_tokens", decision.FreedTokens,
		).Debug("compact: truncated context")
	case ActionSummarize:
		summary, err := m.runSummarize(ctx, req.Messages, decision)
		if err != nil {
			logger.WithFields(
				"provider", m.providerName,
				"tier", string(decision.Tier),
				"error", err.Error(),
			).Warn("compact: summarisation failed; passing through full context")
			return req
		}
		req.Messages = applySummary(req.Messages, decision, summary)
		logger.WithFields(
			"provider", m.providerName,
			"tier", string(decision.Tier),
			"freed_tokens", decision.FreedTokens,
		).Debug("compact: summarised context")
	}
	return req
}

// runSummarize resolves the summarisation provider lazily and runs
// the summariser over the chosen message range.
func (m *Middleware) runSummarize(ctx context.Context, messages []gw.Message, d Decision) (string, error) {
	if m.summarizerResolve == nil {
		return "", fmt.Errorf("compact: no summariser resolver configured")
	}
	provider, err := m.summarizerResolve(ctx, m.cfg.SummarizationModel)
	if err != nil {
		return "", fmt.Errorf("compact: resolve summarisation model %s: %w", m.cfg.SummarizationModel, err)
	}
	summarizer := NewSummarizer(provider, m.cfg.SummarizationModel)
	return summarizer.Summarize(ctx, messages[d.ReplaceStart:d.ReplaceEnd])
}

// applyTruncate replaces messages[start:end] with a single sentinel
// system message indicating how many turns were dropped.
func applyTruncate(messages []gw.Message, d Decision) []gw.Message {
	dropped := d.ReplaceEnd - d.ReplaceStart
	sentinel := gw.Message{
		Role: gw.RoleSystem,
		Content: []gw.ContentPart{{
			Type: "text",
			Text: ptrTo(fmt.Sprintf("[Context compacted: %d earlier messages were removed to free context space.]", dropped)),
		}},
	}
	return spliceOne(messages, d.ReplaceStart, d.ReplaceEnd, sentinel)
}

// applySummary replaces messages[start:end] with a single system
// message carrying the generated summary text.
func applySummary(messages []gw.Message, d Decision, summary string) []gw.Message {
	formatted := fmt.Sprintf("[Context summary (%s compaction)]\n%s", d.Tier, summary)
	sentinel := gw.Message{
		Role: gw.RoleSystem,
		Content: []gw.ContentPart{{
			Type: "text",
			Text: ptrTo(formatted),
		}},
	}
	return spliceOne(messages, d.ReplaceStart, d.ReplaceEnd, sentinel)
}

// spliceOne returns a new slice with messages[start:end] replaced by
// the single replacement message. The original slice is not mutated.
func spliceOne(messages []gw.Message, start, end int, replacement gw.Message) []gw.Message {
	out := make([]gw.Message, 0, len(messages)-(end-start)+1)
	out = append(out, messages[:start]...)
	out = append(out, replacement)
	out = append(out, messages[end:]...)
	return out
}

// ptrTo is the package-local *string helper. Keeping it next to the
// callers that need it avoids a cross-file dependency on summarize.go.
func ptrTo(s string) *string { return &s }
