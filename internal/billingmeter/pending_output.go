package billingmeter

import (
	"context"
	"sync"
)

// PendingOutput is output a provider has generated upstream (and will be
// billed for) but has not yet emitted as a stream chunk. Anthropic, for
// one, streams a thinking block as deltas but the provider emits it only
// whole, when the block ends: a stream that breaks off mid-thinking has
// paid-for reasoning that no chunk ever carried. The metering seam installs
// a PendingOutput on the attempt's context; a provider that buffers keeps it
// current, and an aborted stream's estimate adds what is still pending.
//
// It also carries the prompt usage when the provider knows it before the
// stream's final usage (Anthropic reports it in message_start), so an
// aborted stream's prompt is priced on the real counts, cache reads at the
// cache rate, instead of the character estimate at the full input rate.
type PendingOutput struct {
	mu             sync.Mutex
	reasoningChars int
	promptKnown    bool
	promptTokens   int
	cacheRead      int
	cacheWrite     int
}

// SetPromptUsage records the request's exact prompt usage in the gateway's
// convention: promptTokens includes cacheRead and cacheWrite.
func (p *PendingOutput) SetPromptUsage(promptTokens, cacheRead, cacheWrite int) {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.promptKnown = true
	p.promptTokens, p.cacheRead, p.cacheWrite = promptTokens, cacheRead, cacheWrite
	p.mu.Unlock()
}

// PromptUsage reports the prompt usage set by SetPromptUsage; ok is false
// when the provider reported none.
func (p *PendingOutput) PromptUsage() (promptTokens, cacheRead, cacheWrite int, ok bool) {
	if p == nil {
		return 0, 0, 0, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.promptTokens, p.cacheRead, p.cacheWrite, p.promptKnown
}

type pendingOutputKey struct{}

// WithPendingOutput installs p on ctx.
func WithPendingOutput(ctx context.Context, p *PendingOutput) context.Context {
	return context.WithValue(ctx, pendingOutputKey{}, p)
}

// PendingOutputFrom returns the PendingOutput on ctx, or nil. A nil
// *PendingOutput accepts and reports nothing, so providers can call it
// unconditionally.
func PendingOutputFrom(ctx context.Context) *PendingOutput {
	if ctx == nil {
		return nil
	}
	p, _ := ctx.Value(pendingOutputKey{}).(*PendingOutput)
	return p
}

// SetReasoningChars records how many characters of reasoning the provider
// holds unemitted right now (zero once it has emitted them).
func (p *PendingOutput) SetReasoningChars(n int) {
	if p == nil {
		return
	}
	if n < 0 {
		n = 0
	}
	p.mu.Lock()
	p.reasoningChars = n
	p.mu.Unlock()
}

// ReasoningChars reports the reasoning characters still unemitted.
func (p *PendingOutput) ReasoningChars() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.reasoningChars
}
