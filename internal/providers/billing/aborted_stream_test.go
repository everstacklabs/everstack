package billing

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/services/catalog"
)

// abortMode is how a scripted stream ends after its content chunks.
type abortMode int

const (
	// abortCtxCancel: the request context is cancelled (client gone,
	// deadline) and the provider returns ctx.Err() like an HTTP read on a
	// cancelled request does.
	abortCtxCancel abortMode = iota
	// abortWriteFail: forwarding a chunk to the client fails and the
	// provider returns that error.
	abortWriteFail
	// abortAfterUsage: the final usage chunk arrives, and forwarding it
	// fails because the client is already gone.
	abortAfterUsage
	// abortUpstreamError: the upstream fails mid-stream after content.
	abortUpstreamError
	// abortBeforeContent: the context is cancelled before any content.
	abortBeforeContent
)

var errClientGone = errors.New("write: broken pipe")

// scriptedStream emits four 100-character content chunks (100 estimated
// output tokens in all), then ends the way mode says.
type scriptedStream struct {
	*fakeProvider
	mode  abortMode
	usage gw.Usage
}

func (p *scriptedStream) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	p.calls++
	if p.mode == abortBeforeContent {
		<-ctx.Done()
		return ctx.Err()
	}
	for i := 0; i < 4; i++ {
		msg := gw.NewMessage(gw.RoleAssistant, gw.Text(strings.Repeat("o", 100)))
		if err := onChunk(gw.ChatResponseChunk{Model: req.Model, Choices: []gw.ChoiceDelta{{Delta: msg}}}); err != nil {
			return err
		}
	}
	switch p.mode {
	case abortCtxCancel:
		<-ctx.Done()
		return ctx.Err()
	case abortAfterUsage:
		u := p.usage
		return onChunk(gw.ChatResponseChunk{Usage: &u})
	case abortUpstreamError:
		return errors.New("upstream: connection reset mid-stream")
	}
	return nil
}

// abortReq is a 1000-token prompt (3984 chars + 16 role overhead).
func abortReq() gw.ChatCompletionRequest {
	return gw.ChatCompletionRequest{Model: "priced-1", Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text(strings.Repeat("p", 3984)))}}
}

// 1000 estimated input x 0.010/1k + 100 estimated output x 0.030/1k.
const abortEstimateUSD = 0.013

func assertDetached(t *testing.T, sink *fakeSink) {
	t.Helper()
	sink.mu.Lock()
	defer sink.mu.Unlock()
	for i, err := range sink.ctxErrs {
		if err != nil {
			t.Fatalf("sink call %d ran on a cancelled context (%v): the settle must be detached from the request", i, err)
		}
	}
}

func abortedCount(usage string) float64 {
	return counterValue(abortedStreamSettled.WithLabelValues("fake", billingmeter.MetricChat, usage))
}

// A client that disconnects mid-stream (request context cancelled) after
// receiving content is billed once, on the estimate, on a detached context.
// The org is not resolvable at dispatch here, so the settle-time retry must
// also run on the detached context or the unit is dropped unattributed.
func TestClientDisconnectMidStreamBillsEstimateOnce(t *testing.T) {
	sink := newSink()
	delete(sink.orgs, "tenant-a")
	p := Wrap(&scriptedStream{fakeProvider: &fakeProvider{}, mode: abortCtxCancel}, platformCfg(sink, testCatalog(t)))
	ctx, cancel := context.WithCancel(tenantCtx("tenant-a"))
	defer cancel()
	beforeEst := counterValue(usageEstimated.WithLabelValues("fake", billingmeter.MetricChat))
	beforeAbort := abortedCount("estimated")
	chunks := 0
	err := p.ChatStream(ctx, abortReq(), func(gw.ChatResponseChunk) error {
		chunks++
		if chunks == 4 {
			sink.mu.Lock()
			sink.orgs["tenant-a"] = "org-a"
			sink.mu.Unlock()
			cancel()
		}
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled surfaced", err)
	}
	units := sink.settled()
	if len(units) != 1 {
		t.Fatalf("aborted stream with content must settle exactly once: %+v", units)
	}
	u := units[0]
	if u.OrgID != "org-a" {
		t.Fatalf("org = %q, want org-a (settle-time resolve on a detached context)", u.OrgID)
	}
	if want := billingmeter.MicrosFor(abortEstimateUSD, testMarkup); u.Micros != want {
		t.Fatalf("micros = %d, want %d", u.Micros, want)
	}
	if u.Metadata[billingmeter.MetaUsageEstimated] != true {
		t.Fatalf("estimate must be marked: %+v", u.Metadata)
	}
	if u.Metadata[billingmeter.MetaCorrelationID] != "corr-1" {
		t.Fatalf("detached context must keep request values: %+v", u.Metadata)
	}
	assertDetached(t, sink)
	if d := counterValue(usageEstimated.WithLabelValues("fake", billingmeter.MetricChat)) - beforeEst; d != 1 {
		t.Fatalf("usage estimated counter delta = %v, want 1", d)
	}
	if d := abortedCount("estimated") - beforeAbort; d != 1 {
		t.Fatalf("aborted estimated counter delta = %v, want 1", d)
	}
}

// A client whose connection fails on write (the handler's onChunk returns
// an error) is billed the same way.
func TestClientWriteFailureMidStreamBillsEstimateOnce(t *testing.T) {
	sink := newSink()
	p := Wrap(&scriptedStream{fakeProvider: &fakeProvider{}, mode: abortWriteFail}, platformCfg(sink, testCatalog(t)))
	chunks := 0
	err := p.ChatStream(tenantCtx("tenant-a"), abortReq(), func(gw.ChatResponseChunk) error {
		chunks++
		if chunks == 4 {
			return errClientGone
		}
		return nil
	})
	if !errors.Is(err, errClientGone) {
		t.Fatalf("err = %v", err)
	}
	units := sink.settled()
	if len(units) != 1 || units[0].Micros != billingmeter.MicrosFor(abortEstimateUSD, testMarkup) || units[0].Metadata[billingmeter.MetaUsageEstimated] != true {
		t.Fatalf("write-failure abort must settle the estimate once: %+v", units)
	}
}

// A client that disconnects after the final usage chunk arrived is billed
// the real usage, once, never the estimate.
func TestDisconnectAfterUsageChunkBillsRealUsageOnce(t *testing.T) {
	sink := newSink()
	inner := &scriptedStream{fakeProvider: &fakeProvider{}, mode: abortAfterUsage, usage: gw.Usage{PromptTokens: 2000, CompletionTokens: 300}}
	p := Wrap(inner, platformCfg(sink, testCatalog(t)))
	ctx, cancel := context.WithCancel(tenantCtx("tenant-a"))
	defer cancel()
	before := abortedCount("reported")
	err := p.ChatStream(ctx, abortReq(), func(c gw.ChatResponseChunk) error {
		if c.Usage != nil {
			cancel()
			return errClientGone
		}
		return nil
	})
	if !errors.Is(err, errClientGone) {
		t.Fatalf("err = %v", err)
	}
	units := sink.settled()
	if len(units) != 1 {
		t.Fatalf("must settle exactly once: %+v", units)
	}
	// 2000 x 0.010/1k + 300 x 0.030/1k = 0.029 USD
	if want := billingmeter.MicrosFor(0.029, testMarkup); units[0].Micros != want {
		t.Fatalf("micros = %d, want %d (real usage)", units[0].Micros, want)
	}
	if units[0].Metadata[billingmeter.MetaUsageEstimated] != nil {
		t.Fatalf("real usage must not be marked estimated: %+v", units[0].Metadata)
	}
	assertDetached(t, sink)
	if d := abortedCount("reported") - before; d != 1 {
		t.Fatalf("aborted reported counter delta = %v, want 1", d)
	}
}

// An upstream that fails mid-stream after producing content was still paid
// for upstream, so the attempt settles the estimate.
func TestUpstreamErrorMidStreamWithContentBillsEstimate(t *testing.T) {
	sink := newSink()
	p := Wrap(&scriptedStream{fakeProvider: &fakeProvider{}, mode: abortUpstreamError}, platformCfg(sink, testCatalog(t)))
	if err := p.ChatStream(tenantCtx("tenant-a"), abortReq(), func(gw.ChatResponseChunk) error { return nil }); err == nil {
		t.Fatal("upstream error must surface")
	}
	units := sink.settled()
	if len(units) != 1 || units[0].Micros != billingmeter.MicrosFor(abortEstimateUSD, testMarkup) || units[0].Metadata[billingmeter.MetaUsageEstimated] != true {
		t.Fatalf("upstream error after content must settle the estimate once: %+v", units)
	}
}

// An abort before any content produced nothing to bill.
func TestAbortBeforeContentNotBilled(t *testing.T) {
	sink := newSink()
	p := Wrap(&scriptedStream{fakeProvider: &fakeProvider{}, mode: abortBeforeContent}, platformCfg(sink, testCatalog(t)))
	ctx, cancel := context.WithCancel(tenantCtx("tenant-a"))
	cancel()
	if err := p.ChatStream(ctx, abortReq(), func(gw.ChatResponseChunk) error { return nil }); err == nil {
		t.Fatal("expected cancellation error")
	}
	if units := sink.settled(); len(units) != 0 {
		t.Fatalf("no content, no charge: %+v", units)
	}
}

// BYOK streams are never charged, however they end.
func TestBYOKAbortedStreamNeverBilled(t *testing.T) {
	for _, mode := range []abortMode{abortCtxCancel, abortWriteFail, abortAfterUsage, abortUpstreamError} {
		sink := newSink()
		inner := &scriptedStream{fakeProvider: &fakeProvider{}, mode: mode, usage: gw.Usage{PromptTokens: 10, CompletionTokens: 10}}
		p := Wrap(inner, Config{Meter: sink, Provenance: billingmeter.ProvenanceBYOK, CatalogCache: testCatalog(t)})
		ctx, cancel := context.WithCancel(tenantCtx("tenant-a"))
		chunks := 0
		_ = p.ChatStream(ctx, abortReq(), func(c gw.ChatResponseChunk) error {
			chunks++
			switch {
			case mode == abortCtxCancel && chunks == 4:
				cancel()
			case mode == abortWriteFail && chunks == 4:
				return errClientGone
			case mode == abortAfterUsage && c.Usage != nil:
				return errClientGone
			}
			return nil
		})
		cancel()
		// Recorded for display (mode 2 served real usage), never charged.
		if units := chargedUnits(sink.settled()); len(units) != 0 {
			t.Fatalf("mode %d: BYOK must never be charged: %+v", mode, units)
		}
	}
}

// blockingOrgSink resolves no org at dispatch and, once armed, blocks
// ResolveOrg until its context ends, like a hung billing database.
type blockingOrgSink struct {
	*fakeSink
	armed chan struct{}
}

func (s *blockingOrgSink) ResolveOrg(ctx context.Context) (string, bool, error) {
	select {
	case <-s.armed:
		<-ctx.Done()
		return "", false, ctx.Err()
	default:
		return "", false, errors.New("billing db: transient")
	}
}

// A hung org lookup at settle time must not hold the handler: the detached
// settle context has a deadline. The unit still reaches the sink, with no
// org; the production Meter drops such a unit loudly (unresolved-org
// counter plus an error log naming the settle unit) rather than losing it
// silently.
func TestAbortedSettleBoundedWhenOrgLookupHangs(t *testing.T) {
	prev := streamSettleTimeout
	streamSettleTimeout = 100 * time.Millisecond
	defer func() { streamSettleTimeout = prev }()

	sink := &blockingOrgSink{fakeSink: newSink(), armed: make(chan struct{})}
	p := Wrap(&scriptedStream{fakeProvider: &fakeProvider{}, mode: abortCtxCancel}, platformCfg(sink, testCatalog(t)))
	ctx, cancel := context.WithCancel(tenantCtx("tenant-a"))
	defer cancel()
	chunks := 0
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		done <- p.ChatStream(ctx, abortReq(), func(gw.ChatResponseChunk) error {
			chunks++
			if chunks == 4 {
				close(sink.armed)
				cancel()
			}
			return nil
		})
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("settle blocked on a hung org lookup: the detached context has no deadline")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("settle took %v, want about the settle timeout", elapsed)
	}
	units := sink.settled()
	if len(units) != 1 || units[0].OrgID != "" || units[0].Micros != billingmeter.MicrosFor(abortEstimateUSD, testMarkup) {
		t.Fatalf("unit must reach the sink (unattributed) for the meter to count: %+v", units)
	}
}

// thinkingStream sends one provider-native thinking block (400 characters of
// reasoning, plus a signature that is not text), then 100 characters of
// text, then fails upstream before reporting usage.
type thinkingStream struct{ *fakeProvider }

func (p *thinkingStream) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	native := json.RawMessage(`{"type":"thinking","thinking":"` + strings.Repeat("r", 400) + `","signature":"` + strings.Repeat("s", 4000) + `"}`)
	thinking := gw.Message{Role: gw.RoleAssistant, Content: []gw.ContentPart{{Type: "thinking", ProviderJSON: &native}}}
	if err := onChunk(gw.ChatResponseChunk{Model: req.Model, Choices: []gw.ChoiceDelta{{Delta: thinking}}}); err != nil {
		return err
	}
	text := gw.NewMessage(gw.RoleAssistant, gw.Text(strings.Repeat("o", 100)))
	if err := onChunk(gw.ChatResponseChunk{Model: req.Model, Choices: []gw.ChoiceDelta{{Delta: text}}}); err != nil {
		return err
	}
	return errors.New("upstream: connection reset mid-stream")
}

// Reasoning is billed as output, so an aborted stream's estimate counts the
// thinking it streamed (400 chars = 100 tokens) next to its text (25 tokens),
// and not the signature.
func TestAbortedStreamEstimateCountsReasoning(t *testing.T) {
	sink := newSink()
	p := Wrap(&thinkingStream{fakeProvider: &fakeProvider{}}, platformCfg(sink, testCatalog(t)))
	if err := p.ChatStream(tenantCtx("tenant-a"), abortReq(), func(gw.ChatResponseChunk) error { return nil }); err == nil {
		t.Fatal("upstream error must surface")
	}
	units := sink.settled()
	if len(units) != 1 || units[0].Attempt == nil {
		t.Fatalf("units = %+v", units)
	}
	if got := units[0].Attempt.OutputTokens; got != 125 {
		t.Fatalf("estimated output tokens = %d, want 125 (100 reasoning + 25 text)", got)
	}
}

// The settle observer hears exactly what was settled: the estimate for an
// aborted stream, the reported usage otherwise, once per attempt.
func TestSettleObserverReportsSettledUsage(t *testing.T) {
	sink := newSink()
	var seen []billingmeter.SettledUsage
	ctx := billingmeter.WithSettleObserver(tenantCtx("tenant-a"), func(u billingmeter.SettledUsage) { seen = append(seen, u) })

	aborted := Wrap(&scriptedStream{fakeProvider: &fakeProvider{}, mode: abortUpstreamError}, platformCfg(sink, testCatalog(t)))
	_ = aborted.ChatStream(ctx, abortReq(), func(gw.ChatResponseChunk) error { return nil })
	if len(seen) != 1 || !seen[0].Estimated || seen[0].InputTokens != 1000 || seen[0].OutputTokens != 100 || seen[0].Model != "priced-1" {
		t.Fatalf("aborted stream observed = %+v", seen)
	}

	seen = nil
	reported := Wrap(&scriptedStream{fakeProvider: &fakeProvider{}, mode: abortAfterUsage,
		usage: gw.Usage{PromptTokens: 900, CompletionTokens: 90, TotalTokens: 990}}, platformCfg(sink, testCatalog(t)))
	_ = reported.ChatStream(ctx, abortReq(), func(c gw.ChatResponseChunk) error {
		if c.Usage != nil {
			return errClientGone
		}
		return nil
	})
	if len(seen) != 1 || seen[0].Estimated || seen[0].InputTokens != 900 || seen[0].OutputTokens != 90 {
		t.Fatalf("reported usage observed = %+v", seen)
	}
	if units := sink.settled(); len(units) != 2 {
		t.Fatalf("settle units = %d, want 2", len(units))
	}
}

// midThinkingStream buffers 400 characters of thinking (reporting them as
// pending, as the Anthropic provider does) and never emits a chunk: the
// stream breaks off mid-thinking.
type midThinkingStream struct {
	*fakeProvider
	clientGone bool
}

func (p *midThinkingStream) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	billingmeter.PendingOutputFrom(ctx).SetReasoningChars(400)
	if p.clientGone {
		<-ctx.Done()
		return ctx.Err()
	}
	return errors.New("upstream: connection reset mid-thinking")
}

// A stream that breaks off mid-thinking was billed upstream for the prompt
// and the reasoning so far, though no chunk carried any of it: it settles
// the estimate (400 chars = 100 output tokens) and is flagged.
func TestAbortedMidThinkingSettlesPendingReasoning(t *testing.T) {
	for _, clientGone := range []bool{false, true} {
		sink := newSink()
		p := Wrap(&midThinkingStream{fakeProvider: &fakeProvider{}, clientGone: clientGone}, platformCfg(sink, testCatalog(t)))
		ctx, cancel := context.WithCancel(tenantCtx("tenant-a"))
		if clientGone {
			cancel()
		}
		before := counterValue(abortedInThinking.WithLabelValues("fake", billingmeter.MetricChat))
		if err := p.ChatStream(ctx, abortReq(), func(gw.ChatResponseChunk) error { return nil }); err == nil {
			t.Fatal("aborted stream must surface its error")
		}
		cancel()
		units := sink.settled()
		if len(units) != 1 || units[0].Attempt == nil {
			t.Fatalf("clientGone=%v: units = %+v", clientGone, units)
		}
		u := units[0]
		if u.Attempt.InputTokens != 1000 || u.Attempt.OutputTokens != 100 || !u.Attempt.Estimated || u.Micros <= 0 {
			t.Fatalf("clientGone=%v: attempt %+v micros %d, want the 1000/100 estimate charged", clientGone, u.Attempt, u.Micros)
		}
		if u.Metadata[billingmeter.MetaAbortedInThinking] != true {
			t.Fatalf("clientGone=%v: metadata %+v, want aborted_in_thinking", clientGone, u.Metadata)
		}
		if d := counterValue(abortedInThinking.WithLabelValues("fake", billingmeter.MetricChat)) - before; d != 1 {
			t.Fatalf("clientGone=%v: aborted-in-thinking counter delta = %v, want 1", clientGone, d)
		}
	}
}

// BYOK is never charged, but a tagged BYOK attempt that broke off
// mid-thinking is still recorded with the flag, so it is visible.
func TestBYOKAbortedMidThinkingIsFlagged(t *testing.T) {
	sink := newSink()
	p := Wrap(&midThinkingStream{fakeProvider: &fakeProvider{}}, Config{Meter: sink, Provenance: billingmeter.ProvenanceBYOK, CatalogCache: testCatalog(t)})
	ctx := billingmeter.WithAttemptKind(tenantCtx("tenant-a"), billingmeter.KindPrimary)
	_ = p.ChatStream(ctx, abortReq(), func(gw.ChatResponseChunk) error { return nil })
	units := sink.settled()
	if len(units) != 1 || units[0].Micros != 0 || units[0].Metadata[billingmeter.MetaAbortedInThinking] != true {
		t.Fatalf("units = %+v", units)
	}
}

// Thinking the provider already emitted is counted from its chunk only.
func TestEmittedThinkingIsNotPending(t *testing.T) {
	sink := newSink()
	p := Wrap(&thinkingStream{fakeProvider: &fakeProvider{}}, platformCfg(sink, testCatalog(t)))
	_ = p.ChatStream(tenantCtx("tenant-a"), abortReq(), func(gw.ChatResponseChunk) error { return nil })
	units := sink.settled()
	if len(units) != 1 || units[0].Metadata[billingmeter.MetaAbortedInThinking] != nil {
		t.Fatalf("units = %+v", units)
	}
}

// cachedPromptStream reports the exact prompt usage up front (as Anthropic's
// message_start does: 50,000 prompt tokens, 45,000 of them cache reads),
// then breaks off mid-thinking.
type cachedPromptStream struct{ *fakeProvider }

func (p *cachedPromptStream) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	pending := billingmeter.PendingOutputFrom(ctx)
	pending.SetPromptUsage(50000, 45000, 0)
	pending.SetReasoningChars(400)
	return errors.New("upstream: connection reset mid-thinking")
}

const cachedCatalogYAML = `
providers:
  fake:
    name: fake
    models:
      - name: priced-1
        input_cost_per_1k: 0.010
        output_cost_per_1k: 0.030
        cache_read_cost_per_1k: 0.001
        capabilities: [chat]
        status: available
`

// The prompt part of an abort estimate uses the prompt usage the provider
// already reported, cache reads included, rather than the character
// estimate priced at the full input rate.
func TestAbortEstimateUsesReportedPromptUsage(t *testing.T) {
	cat := catalog.NewCache()
	if err := cat.Load([]byte(cachedCatalogYAML), []byte("providers: {}\n")); err != nil {
		t.Fatal(err)
	}
	sink := newSink()
	p := Wrap(&cachedPromptStream{fakeProvider: &fakeProvider{}}, platformCfg(sink, cat))
	_ = p.ChatStream(tenantCtx("tenant-a"), abortReq(), func(gw.ChatResponseChunk) error { return nil })
	units := sink.settled()
	if len(units) != 1 || units[0].Attempt == nil {
		t.Fatalf("units = %+v", units)
	}
	u := units[0]
	if u.Attempt.InputTokens != 50000 || u.Attempt.OutputTokens != 100 || !u.Attempt.Estimated {
		t.Fatalf("attempt = %+v, want the reported 50000 prompt tokens and 100 estimated output", u.Attempt)
	}
	// 5,000 fresh x 0.010/1k + 45,000 cached x 0.001/1k + 100 out x 0.030/1k.
	if want := billingmeter.MicrosFor(0.05+0.045+0.003, testMarkup); u.Micros != want {
		t.Fatalf("micros = %d, want %d (cache reads at the cache rate)", u.Micros, want)
	}
}
