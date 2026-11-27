package v1

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// streamAttemptPlan is the order a streaming request dispatches in: the
// requested model first, exactly as the unary path does (one try, no step
// timeout), then the fallback plan in order. An alias is dispatched at most
// once per request, compared case-insensitively (plan aliases are
// lower-cased, the requested model is not), so a plan that also lists the
// requested model or repeats an alias does not try it twice. Steps left with
// no alias are dropped.
//
// The plan used to put the requested model first only when it was absent
// from the plan; when the plan listed it (for example as the
// default_fallback), a stream started at steps[0] and could be served by a
// model the caller did not ask for while the requested one was never tried.
func streamAttemptPlan(model string, plan []FallbackStep) []FallbackStep {
	out := make([]FallbackStep, 0, len(plan)+1)
	out = append(out, FallbackStep{Aliases: []string{model}, Strategy: "priority", Name: "primary"})
	seen := map[string]bool{strings.ToLower(model): true}
	for _, step := range plan {
		aliases := make([]string, 0, len(step.Aliases))
		for _, alias := range step.Aliases {
			key := strings.ToLower(alias)
			if alias == "" || seen[key] {
				continue
			}
			seen[key] = true
			aliases = append(aliases, alias)
		}
		if len(aliases) == 0 {
			continue
		}
		step.Aliases = aliases
		out = append(out, step)
	}
	return out
}

// chunkCarriesContent reports whether a chunk carries anything a client
// renders or acts on: text or any other content part, a tool call, or a
// finish reason. Role-only, empty and usage-only chunks do not.
func chunkCarriesContent(c gw.ChatResponseChunk) bool {
	for _, d := range c.Choices {
		if d.FinishReason != "" || len(d.Delta.ToolCalls) > 0 || d.Delta.ToolCallID != "" {
			return true
		}
		for _, p := range d.Delta.Content {
			if (p.Text != nil && *p.Text != "") || p.ImageURL != nil || p.FileID != nil ||
				p.ToolCallID != nil || p.FunctionCall != nil || p.ProviderJSON != nil {
				return true
			}
		}
	}
	return false
}

// streamDelivery decides when a streaming request has put content on the
// wire. gw.ToolStream holds tool calls back until an attempt succeeds but
// passes every other chunk through as it arrives, including chunks it
// stripped down to nothing; each one is a message on the wire (and the first
// one commits the response headers). So the gate holds leading content-free
// chunks (role-only, empty, usage-only) and releases them in front of the
// first chunk that carries content, or when the attempt succeeds. Until then
// nothing has been sent and a failed attempt's held chunks are dropped; after
// it, delivered is true and the request may not retry, rotate keys or fall
// back, because the client would receive model A's partial answer followed by
// model B's complete one in a single response.
type streamDelivery struct {
	// sink sends one chunk to the client. first is true for the first chunk
	// of the response (headers can still be set).
	sink      func(c gw.ChatResponseChunk, first bool) error
	held      []gw.ChatResponseChunk
	delivered bool
}

func (g *streamDelivery) push(c gw.ChatResponseChunk) error {
	if !g.delivered && !chunkCarriesContent(c) {
		g.held = append(g.held, c)
		return nil
	}
	if err := g.flush(); err != nil {
		return err
	}
	return g.emit(c)
}

// flush sends the held chunks. Called on success and before the first chunk
// that carries content.
func (g *streamDelivery) flush() error {
	held := g.held
	g.held = nil
	for _, c := range held {
		if err := g.emit(c); err != nil {
			return err
		}
	}
	return nil
}

// discard drops the held chunks of a failed attempt.
func (g *streamDelivery) discard() { g.held = nil }

func (g *streamDelivery) emit(c gw.ChatResponseChunk) error {
	first := !g.delivered
	// Set before sending: a send that fails may still have written bytes.
	g.delivered = true
	return g.sink(c, first)
}

// errFirstByteTimeout is a stream attempt whose upstream sent nothing within
// the step's timeout. It wraps context.DeadlineExceeded so retry and fallback
// classification treat it as a timeout.
var errFirstByteTimeout = fmt.Errorf("upstream sent no response within the step timeout: %w", context.DeadlineExceeded)

// runStreamAttempt dispatches one streaming attempt on router and forwards its
// chunks through gate. A positive firstByte bounds the time until the
// upstream's first chunk, not the stream's total duration: the step timeout
// used to wrap the whole stream, so with timeout_ms: 5000 every stream longer
// than five seconds was cut.
func runStreamAttempt(ctx context.Context, firstByte time.Duration, router *gw.Router, req gw.ChatCompletionRequest, gate *streamDelivery) error {
	if router == nil {
		return errProvidersUnavailable()
	}
	attemptCtx := ctx
	var fb *firstByteCtx
	if firstByte > 0 {
		fb = newFirstByteCtx(ctx, firstByte)
		defer fb.release()
		attemptCtx = fb
	}
	err := gw.StreamToolAttempt(func(on func(gw.ChatResponseChunk) error) error {
		return gw.HandleChatStream(attemptCtx, router, req, func(c gw.ChatResponseChunk) error {
			if fb != nil && !fb.arrived() {
				return errFirstByteTimeout
			}
			return on(c)
		})
	}, gate.push)
	if err != nil && fb != nil && fb.timedOut() && ctx.Err() == nil {
		return fmt.Errorf("%w: %w", errFirstByteTimeout, err)
	}
	return err
}

// firstByteCtx is an attempt context that ends with errFirstByteTimeout when
// the upstream has sent nothing within the timeout, and otherwise lives as
// long as its parent.
//
// It is a custom context rather than context.WithCancelCause because the
// billing middleware reads errors.Is(ctx.Err(), context.Canceled) as "the
// client walked away", which settles a failed attempt as served (charged).
// A timed-out attempt must read as a deadline, the way context.WithTimeout
// reported it before. Value and Deadline come from the parent; Done is its
// own channel, closed on timeout, on parent cancellation (context.AfterFunc,
// no goroutine) or on release.
type firstByteCtx struct {
	context.Context
	done      chan struct{}
	mu        sync.Mutex
	err       error
	state     atomic.Int32 // 0 waiting, 1 first chunk arrived, 2 timed out
	timer     *time.Timer
	stopAfter func() bool
}

func newFirstByteCtx(parent context.Context, timeout time.Duration) *firstByteCtx {
	c := &firstByteCtx{Context: parent, done: make(chan struct{})}
	c.stopAfter = context.AfterFunc(parent, func() { c.finish(parent.Err()) })
	c.timer = time.AfterFunc(timeout, func() {
		if c.state.CompareAndSwap(0, 2) {
			c.finish(errFirstByteTimeout)
		}
	})
	return c
}

func (c *firstByteCtx) Done() <-chan struct{} { return c.done }

func (c *firstByteCtx) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *firstByteCtx) finish(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return
	}
	c.err = err
	close(c.done)
}

// arrived records the first upstream chunk and stops the timer. It reports
// false when the timeout already fired, in which case the chunk must not be
// forwarded.
func (c *firstByteCtx) arrived() bool {
	if c.state.CompareAndSwap(0, 1) {
		c.timer.Stop()
		return true
	}
	return c.state.Load() == 1
}

func (c *firstByteCtx) timedOut() bool { return c.state.Load() == 2 }

// release frees the timer and the parent registration once the attempt has
// returned, and ends the context so anything derived from it exits.
func (c *firstByteCtx) release() {
	c.timer.Stop()
	c.stopAfter()
	c.finish(context.Canceled)
}

// errProvidersUnavailable answers a request whose tenant has no provider bundle:
// the bundle reload failed (a provider database outage longer than the
// bundle TTL) and no recent bundle of the same tenant is left to serve.
// Retrying later is the right answer, so it is Unavailable (503), not the
// nil-router panic it used to be (HTTP 500 INTERNAL_ERROR).
//
// Built per call: a *connect.Error carries mutable metadata, so one shared
// value must not be handed to every request.
func errProvidersUnavailable() error {
	return connect.NewError(connect.CodeUnavailable,
		errors.New("provider configuration is temporarily unavailable; retry the request"))
}

// handleChatOn is gw.HandleChat that answers errProvidersUnavailable for a
// nil router. Every dispatch reads the router afresh (a key rotation or a
// reload installs a new bundle mid-request), so every dispatch checks it.
func handleChatOn(ctx context.Context, router *gw.Router, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	if router == nil {
		return gw.ChatCompletionResponse{}, errProvidersUnavailable()
	}
	return gw.HandleChat(ctx, router, req)
}

// handleEmbeddingsOn is gw.HandleEmbeddings with the same nil-router check.
func handleEmbeddingsOn(ctx context.Context, router *gw.Router, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	if router == nil {
		return gw.EmbeddingsResponse{}, errProvidersUnavailable()
	}
	return gw.HandleEmbeddings(ctx, router, req)
}
