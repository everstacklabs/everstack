package billing

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	dto "github.com/prometheus/client_model/go"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/correlation"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/services/catalog"
)

const testMarkup = 1.25

const testCatalogYAML = `
providers:
  fake:
    name: fake
    models:
      - name: priced-1
        input_cost_per_1k: 0.010
        output_cost_per_1k: 0.030
        capabilities: [chat]
        status: available
      - name: priced-embed
        input_cost_per_1k: 0.002
        output_cost_per_1k: 0
        capabilities: [embeddings]
        status: available
      - name: zero-rate
        input_cost_per_1k: 0
        output_cost_per_1k: 0
        capabilities: [chat]
        status: available
`

func testCatalog(t *testing.T) *catalog.Cache {
	t.Helper()
	c := catalog.NewCache()
	if err := c.Load([]byte(testCatalogYAML), []byte("providers: {}\n")); err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	return c
}

// fakeSink records every settle unit; org resolution is a fixed map.
type fakeSink struct {
	mu      sync.Mutex
	units   []billingmeter.SettleUnit
	counts  map[string]billingmeter.UsageDelta
	orgs    map[string]string
	resolve int
	err     error
	markup  float64
	active  bool
	// ctxErrs records ctx.Err() at every ResolveOrg and Settle call, so a
	// test can prove settles run on a context detached from the request.
	ctxErrs []error
	admits  []billingmeter.AdmissionRequest
}

func newSink() *fakeSink {
	return &fakeSink{orgs: map[string]string{"tenant-a": "org-a"}, markup: testMarkup, active: true}
}
func (s *fakeSink) Active() bool    { return s.active }
func (s *fakeSink) Markup() float64 { return s.markup }
func (s *fakeSink) ResolveOrg(ctx context.Context) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolve++
	s.ctxErrs = append(s.ctxErrs, ctx.Err())
	if s.err != nil {
		return "", false, s.err
	}
	org, ok := s.orgs[contextkeys.ExtractTenantID(ctx)]
	return org, ok, nil
}
func (s *fakeSink) Settle(ctx context.Context, u billingmeter.SettleUnit) {
	s.mu.Lock()
	s.ctxErrs = append(s.ctxErrs, ctx.Err())
	s.units = append(s.units, u)
	s.mu.Unlock()
}
func (s *fakeSink) CountUsage(org string, d billingmeter.UsageDelta) {
	s.mu.Lock()
	if s.counts == nil {
		s.counts = map[string]billingmeter.UsageDelta{}
	}
	c := s.counts[org]
	c.Requests += d.Requests
	c.InputTokens += d.InputTokens
	c.OutputTokens += d.OutputTokens
	s.counts[org] = c
	s.mu.Unlock()
}
func (s *fakeSink) ShadowAdmit(req billingmeter.AdmissionRequest) {
	s.mu.Lock()
	s.admits = append(s.admits, req)
	s.mu.Unlock()
}
func (s *fakeSink) admitted() []billingmeter.AdmissionRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]billingmeter.AdmissionRequest(nil), s.admits...)
}
func (s *fakeSink) counted(org string) billingmeter.UsageDelta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.counts[org]
}
func (s *fakeSink) settled() []billingmeter.SettleUnit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]billingmeter.SettleUnit(nil), s.units...)
}

// fakeProvider is a base provider that reports fixed usage.
type fakeProvider struct {
	calls     int
	err       error
	respModel string
	usage     gw.Usage
}

func (p *fakeProvider) Name() string              { return "fake" }
func (p *fakeProvider) SupportsModel(string) bool { return true }
func (p *fakeProvider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	p.calls++
	if p.err != nil {
		return gw.ChatCompletionResponse{}, p.err
	}
	model := p.respModel
	if model == "" {
		model = req.Model
	}
	return gw.ChatCompletionResponse{Model: model, Usage: p.usage}, nil
}
func (p *fakeProvider) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	p.calls++
	if err := onChunk(gw.ChatResponseChunk{Model: req.Model}); err != nil {
		return err
	}
	u := p.usage
	if err := onChunk(gw.ChatResponseChunk{Usage: &u}); err != nil {
		return err
	}
	return p.err
}
func (p *fakeProvider) Embed(ctx context.Context, req gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	p.calls++
	if p.err != nil {
		return gw.EmbeddingsResponse{}, p.err
	}
	return gw.EmbeddingsResponse{Usage: &gw.Usage{PromptTokens: p.usage.PromptTokens, TotalTokens: p.usage.PromptTokens}}, nil
}

// fakeResponsesProvider adds the Responses API.
type fakeResponsesProvider struct {
	*fakeProvider
	rusage gw.ResponseUsage
}

func (p *fakeResponsesProvider) CreateResponse(ctx context.Context, req gw.CreateResponseRequest) (gw.CreateResponseResponse, error) {
	p.calls++
	u := p.rusage
	return gw.CreateResponseResponse{ID: "resp_1", Model: req.Model, Usage: &u}, nil
}
func (p *fakeResponsesProvider) CreateResponseStream(ctx context.Context, req gw.CreateResponseRequest, onChunk func(gw.CreateResponseResponse) error) error {
	p.calls++
	u := p.rusage
	return onChunk(gw.CreateResponseResponse{ID: "resp_1", Model: req.Model, Usage: &u})
}

func platformCfg(sink Sink, cat *catalog.Cache) Config {
	return Config{Meter: sink, Provenance: billingmeter.ProvenancePlatform, KeyID: "key-1", CatalogCache: cat}
}

func tenantCtx(tenant string) context.Context {
	return correlation.WithCorrelationID(contextkeys.WithTenantID(context.Background(), tenant), "corr-1")
}

func TestWrapReturnsInnerForBYOKAndInactive(t *testing.T) {
	inner := &fakeProvider{}
	cat := testCatalog(t)
	// BYOK on a managed gateway is counted, so it is wrapped, but the
	// wrapper is see-through for capability discovery.
	byok := Wrap(inner, Config{Meter: newSink(), Provenance: billingmeter.ProvenanceBYOK, CatalogCache: cat})
	if byok == inner {
		t.Fatal("BYOK provider on a managed gateway must be wrapped for counting")
	}
	if u, ok := byok.(gw.Unwrapper); !ok || u.Unwrap() != inner {
		t.Fatal("BYOK wrapper must unwrap to the base provider")
	}
	if got := Wrap(inner, Config{Meter: newSink(), CatalogCache: cat}); got != inner {
		t.Fatal("unknown provenance must be returned unwrapped")
	}
	off := newSink()
	off.active = false
	if got := Wrap(inner, platformCfg(off, cat)); got != inner {
		t.Fatal("inactive meter must leave the provider unwrapped")
	}
	var nilMeter *billingmeter.Meter
	if got := Wrap(inner, Config{Meter: nilMeter, Provenance: billingmeter.ProvenancePlatform, CatalogCache: cat}); got != inner {
		t.Fatal("nil *Meter is an inactive sink")
	}
	// A wrapped platform provider must not be see-through for capability
	// discovery: that is how unpriced modalities would be served free.
	wrapped := Wrap(inner, platformCfg(newSink(), cat))
	if _, ok := wrapped.(gw.Unwrapper); ok {
		t.Fatal("platform wrapper must not implement Unwrapper")
	}
	if _, ok := gw.UnwrapTo[gw.AudioProvider](wrapped); ok {
		t.Fatal("audio must not be discoverable through the platform wrapper")
	}
}

// Every provider attempt is its own settle unit: an agent run that makes N
// turns through the same provider produces N ledger rows with N distinct
// keys, even though they share one correlation id.
func TestEveryAttemptIsASettleUnit(t *testing.T) {
	sink := newSink()
	inner := &fakeProvider{usage: gw.Usage{PromptTokens: 1000, CompletionTokens: 500, TotalTokens: 1500}}
	p := Wrap(inner, platformCfg(sink, testCatalog(t)))
	ctx := tenantCtx("tenant-a")
	const turns = 5
	for i := 0; i < turns; i++ {
		if _, err := p.Chat(ctx, gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
			t.Fatalf("turn %d: %v", i, err)
		}
	}
	units := sink.settled()
	if len(units) != turns {
		t.Fatalf("settled %d units, want %d", len(units), turns)
	}
	keys := map[string]bool{}
	for _, u := range units {
		if keys[u.IdempotencyKey()] {
			t.Fatalf("duplicate idempotency key %s", u.IdempotencyKey())
		}
		keys[u.IdempotencyKey()] = true
		if u.OrgID != "org-a" || u.Modality != billingmeter.MetricChat {
			t.Fatalf("unit mis-attributed: %+v", u)
		}
		// 1000 in x 0.010/1k + 500 out x 0.030/1k = 0.025 USD; applying testMarkup gives 31250 micros.
		if u.Micros != 31250 {
			t.Fatalf("micros = %d, want 31250", u.Micros)
		}
		if u.Metadata[billingmeter.MetaCorrelationID] != "corr-1" || u.Metadata[billingmeter.MetaProvenance] != billingmeter.ProvenancePlatform ||
			u.Metadata[billingmeter.MetaKeyID] != "key-1" || u.Metadata[billingmeter.MetaInternal] != false {
			t.Fatalf("metadata incomplete: %+v", u.Metadata)
		}
	}
}

func TestStreamSettlesOnUsageChunk(t *testing.T) {
	sink := newSink()
	inner := &fakeProvider{usage: gw.Usage{PromptTokens: 100, CompletionTokens: 100}}
	p := Wrap(inner, platformCfg(sink, testCatalog(t)))
	var chunks int
	if err := p.ChatStream(tenantCtx("tenant-a"), gw.ChatCompletionRequest{Model: "priced-1"}, func(gw.ChatResponseChunk) error { chunks++; return nil }); err != nil {
		t.Fatal(err)
	}
	if chunks != 2 {
		t.Fatalf("stream chunks forwarded = %d, want 2", chunks)
	}
	units := sink.settled()
	if len(units) != 1 || units[0].Micros != billingmeter.MicrosFor(0.004, testMarkup) {
		t.Fatalf("stream settle wrong: %+v", units)
	}
}

// A stream that reports usage and then fails still spent the tokens.
func TestStreamErrorAfterUsageStillBills(t *testing.T) {
	sink := newSink()
	inner := &fakeProvider{usage: gw.Usage{PromptTokens: 100, CompletionTokens: 100}, err: errors.New("upstream closed")}
	p := Wrap(inner, platformCfg(sink, testCatalog(t)))
	if err := p.ChatStream(tenantCtx("tenant-a"), gw.ChatCompletionRequest{Model: "priced-1"}, func(gw.ChatResponseChunk) error { return nil }); err == nil {
		t.Fatal("expected the provider error to surface")
	}
	if len(sink.settled()) != 1 {
		t.Fatal("usage was reported, so the attempt must be billed")
	}
}

func TestFailedAttemptIsNotBilled(t *testing.T) {
	sink := newSink()
	inner := &fakeProvider{err: errors.New("boom")}
	p := Wrap(inner, platformCfg(sink, testCatalog(t)))
	if _, err := p.Chat(tenantCtx("tenant-a"), gw.ChatCompletionRequest{Model: "priced-1"}); err == nil {
		t.Fatal("expected error")
	}
	if len(sink.settled()) != 0 {
		t.Fatal("a failed attempt reports no usage and must not be billed")
	}
}

func TestEmbedBilled(t *testing.T) {
	sink := newSink()
	inner := &fakeProvider{usage: gw.Usage{PromptTokens: 2000}}
	p := Wrap(inner, platformCfg(sink, testCatalog(t)))
	if _, err := p.Embed(tenantCtx("tenant-a"), gw.EmbeddingsRequest{Model: "priced-embed", Input: "hello"}); err != nil {
		t.Fatal(err)
	}
	units := sink.settled()
	if len(units) != 1 || units[0].Modality != billingmeter.MetricEmbeddings || units[0].Micros != billingmeter.MicrosFor(0.004, testMarkup) {
		t.Fatalf("embed settle wrong: %+v", units)
	}
}

func TestResponsesBilledWhenBaseSupportsIt(t *testing.T) {
	sink := newSink()
	inner := &fakeResponsesProvider{fakeProvider: &fakeProvider{}, rusage: gw.ResponseUsage{InputTokens: 1000, OutputTokens: 1000}}
	p := Wrap(inner, platformCfg(sink, testCatalog(t)))
	rp, ok := p.(gw.ResponsesProvider)
	if !ok {
		t.Fatal("wrapper must expose the Responses API when the base has it")
	}
	if _, ok := gw.UnwrapTo[gw.ResponsesProvider](p); !ok {
		t.Fatal("registry discovery must find the metered Responses provider")
	}
	if _, err := rp.CreateResponse(tenantCtx("tenant-a"), gw.CreateResponseRequest{Model: "priced-1"}); err != nil {
		t.Fatal(err)
	}
	if err := rp.CreateResponseStream(tenantCtx("tenant-a"), gw.CreateResponseRequest{Model: "priced-1"}, func(gw.CreateResponseResponse) error { return nil }); err != nil {
		t.Fatal(err)
	}
	units := sink.settled()
	if len(units) != 2 {
		t.Fatalf("settled %d, want 2", len(units))
	}
	for _, u := range units {
		if u.Modality != billingmeter.MetricResponses || u.Micros != billingmeter.MicrosFor(0.040, testMarkup) {
			t.Fatalf("responses settle wrong: %+v", u)
		}
	}
	// A base without Responses must not grow one.
	plain := Wrap(&fakeProvider{}, platformCfg(sink, testCatalog(t)))
	if _, ok := plain.(gw.ResponsesProvider); ok {
		t.Fatal("wrapper must not claim Responses the base lacks")
	}
}

// Unpriced models fail closed on every attempt: not served, not "debited
// zero", and the error is typed and non-retryable.
func TestUnpricedModelRefusedOnPlatformKey(t *testing.T) {
	sink := newSink()
	inner := &fakeProvider{usage: gw.Usage{PromptTokens: 10, CompletionTokens: 10}}
	p := Wrap(inner, platformCfg(sink, testCatalog(t)))
	ctx := tenantCtx("tenant-a")
	for _, model := range []string{"not-in-catalog", "zero-rate"} {
		_, err := p.Chat(ctx, gw.ChatCompletionRequest{Model: model})
		if !errors.Is(err, ErrUnpricedModel) {
			t.Fatalf("%s: err = %v, want unpriced_model", model, err)
		}
		var typed *UnpricedModelError
		if !errors.As(err, &typed) || typed.Model != model {
			t.Fatalf("%s: error not typed: %v", model, err)
		}
		if err := p.ChatStream(ctx, gw.ChatCompletionRequest{Model: model}, func(gw.ChatResponseChunk) error { return nil }); !errors.Is(err, ErrUnpricedModel) {
			t.Fatalf("%s stream: err = %v", model, err)
		}
		if _, err := p.Embed(ctx, gw.EmbeddingsRequest{Model: model}); !errors.Is(err, ErrUnpricedModel) {
			t.Fatalf("%s embed: err = %v", model, err)
		}
	}
	if inner.calls != 0 {
		t.Fatalf("provider was called %d times for unpriced models; must be 0", inner.calls)
	}
	if len(sink.settled()) != 0 {
		t.Fatal("nothing may be settled for a refused attempt")
	}
	// No catalog at all: nothing is priceable.
	nocat := Wrap(&fakeProvider{}, platformCfg(newSink(), nil))
	if _, err := nocat.Chat(ctx, gw.ChatCompletionRequest{Model: "priced-1"}); !errors.Is(err, ErrUnpricedModel) {
		t.Fatalf("no catalog: err = %v", err)
	}
	// BYOK is never subject to the price check.
	byok := Wrap(&fakeProvider{}, Config{Meter: sink, Provenance: billingmeter.ProvenanceBYOK})
	if _, err := byok.Chat(ctx, gw.ChatCompletionRequest{Model: "not-in-catalog"}); err != nil {
		t.Fatalf("BYOK must serve unpriced models: %v", err)
	}
}

// Internal calls (semantic-cache embeddings, compaction summaries) are
// debited like any other platform attempt and carry the marker; nothing
// on this path can deny them.
func TestInternalCallDebitedAndMarked(t *testing.T) {
	sink := newSink()
	inner := &fakeProvider{usage: gw.Usage{PromptTokens: 1000, CompletionTokens: 0}}
	p := Wrap(inner, platformCfg(sink, testCatalog(t)))
	ctx := contextkeys.WithInternalCall(tenantCtx("tenant-a"))
	if _, err := p.Chat(ctx, gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatal(err)
	}
	units := sink.settled()
	if len(units) != 1 || units[0].Micros != billingmeter.MicrosFor(0.010, testMarkup) || units[0].Metadata[billingmeter.MetaInternal] != true {
		t.Fatalf("internal call settle wrong: %+v", units)
	}
}

// A dated response model that is not in the catalog is priced on the
// requested model, never dropped.
func TestServedVariantFallsBackToRequestedModelPrice(t *testing.T) {
	sink := newSink()
	inner := &fakeProvider{respModel: "priced-1-2026-09-30-preview", usage: gw.Usage{PromptTokens: 1000, CompletionTokens: 0}}
	p := Wrap(inner, platformCfg(sink, testCatalog(t)))
	if _, err := p.Chat(tenantCtx("tenant-a"), gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatal(err)
	}
	units := sink.settled()
	if len(units) != 1 || units[0].Micros != billingmeter.MicrosFor(0.010, testMarkup) {
		t.Fatalf("variant settle wrong: %+v", units)
	}
}

// The org is resolved once at dispatch; a transient resolver error does not
// block the attempt, and the settle retries resolution before handing the
// unit over so a blip does not turn into an unbilled attempt.
func TestOrgResolutionRetriedAtSettle(t *testing.T) {
	sink := newSink()
	sink.err = errors.New("db blip")
	inner := &fakeProvider{usage: gw.Usage{PromptTokens: 1000}}
	p := Wrap(inner, platformCfg(sink, testCatalog(t)))
	inner.err = nil
	ctx := tenantCtx("tenant-a")
	// First attempt: resolver fails before and after; the sink sees an
	// empty org and is responsible for counting it.
	if _, err := p.Chat(ctx, gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatal(err)
	}
	if units := sink.settled(); len(units) != 1 || units[0].OrgID != "" {
		t.Fatalf("attempt must still be handed to the sink with an empty org: %+v", units)
	}
	// Resolver recovers: the next attempt is attributed.
	sink.mu.Lock()
	sink.err = nil
	sink.mu.Unlock()
	if _, err := p.Chat(ctx, gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatal(err)
	}
	if units := sink.settled(); len(units) != 2 || units[1].OrgID != "org-a" {
		t.Fatalf("recovered resolver must attribute the unit: %+v", units)
	}
	// Unmapped tenant: attempt served, unit handed over with no org.
	if _, err := p.Chat(tenantCtx("tenant-unknown"), gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatal(err)
	}
	if units := sink.settled(); len(units) != 3 || units[2].OrgID != "" {
		t.Fatalf("unmapped tenant: %+v", units)
	}
}

// A gateway fallback is a second provider attempt through a second wrapped
// provider: the failed primary is not billed, the fallback winner is billed
// once, an unpriced fallback model is refused before dispatch, and a
// parallel race bills every attempt that completed (winner and loser), each
// under its own key.
func TestFallbackAndParallelAttemptsBilled(t *testing.T) {
	sink := newSink()
	cat := testCatalog(t)
	ctx := tenantCtx("tenant-a")

	primary := &fakeProvider{err: errors.New("503 upstream")}
	fallback := &fakeProvider{usage: gw.Usage{PromptTokens: 1000}}
	p1 := Wrap(primary, platformCfg(sink, cat))
	p2 := Wrap(fallback, platformCfg(sink, cat))
	if _, err := p1.Chat(ctx, gw.ChatCompletionRequest{Model: "priced-1"}); err == nil {
		t.Fatal("primary must fail")
	}
	if _, err := p2.Chat(ctx, gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatal(err)
	}
	if units := sink.settled(); len(units) != 1 || units[0].Micros != billingmeter.MicrosFor(0.010, testMarkup) {
		t.Fatalf("fallback winner must be billed exactly once: %+v", units)
	}

	// Unpriced fallback on a platform key: refused, never dispatched.
	unpriced := &fakeProvider{usage: gw.Usage{PromptTokens: 1000}}
	p3 := Wrap(unpriced, platformCfg(sink, cat))
	if _, err := p3.Chat(ctx, gw.ChatCompletionRequest{Model: "not-in-catalog"}); !errors.Is(err, ErrUnpricedModel) {
		t.Fatalf("unpriced fallback: err = %v", err)
	}
	if unpriced.calls != 0 {
		t.Fatal("unpriced fallback must not reach the provider")
	}

	// Parallel race: both complete, both billed, distinct keys.
	a := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 1000}}, platformCfg(sink, cat))
	b := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 1000}}, platformCfg(sink, cat))
	var wg sync.WaitGroup
	for _, p := range []gw.Provider{a, b} {
		wg.Add(1)
		go func(p gw.Provider) {
			defer wg.Done()
			_, _ = p.Chat(ctx, gw.ChatCompletionRequest{Model: "priced-1"})
		}(p)
	}
	wg.Wait()
	units := sink.settled()
	if len(units) != 3 || units[1].IdempotencyKey() == units[2].IdempotencyKey() {
		t.Fatalf("parallel attempts must each settle once: %+v", units)
	}
}

// noUsageChatStream streams text and tool-call deltas and never reports
// usage, like an OpenAI-compatible upstream that ignores include_usage.
type noUsageChatStream struct{ *fakeProvider }

func (p *noUsageChatStream) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	p.calls++
	out := strings.Repeat("o", 400) // 100 estimated output tokens
	msg := gw.NewMessage(gw.RoleAssistant, gw.Text(out))
	msg.ToolCalls = []gw.ToolCall{{Function: gw.ToolCallFunction{Name: "", Arguments: strings.Repeat("a", 40)}}} // +10
	return onChunk(gw.ChatResponseChunk{Model: req.Model, Choices: []gw.ChoiceDelta{{Delta: msg}}})
}

// A platform stream that ends cleanly without a usage chunk is not free: it
// is settled on a character estimate (prompt plus streamed output, ~4
// characters per token), marked estimated, counted and logged. Streams that
// end abnormally after content are covered in aborted_stream_test.go.
func TestPlatformStreamWithoutUsageSettlesEstimate(t *testing.T) {
	sink := newSink()
	p := Wrap(&noUsageChatStream{&fakeProvider{}}, platformCfg(sink, testCatalog(t)))
	prompt := strings.Repeat("p", 3984) // + 16 role overhead = 4000 chars = 1000 tokens
	req := gw.ChatCompletionRequest{Model: "priced-1", Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text(prompt))}}
	before := counterValue(usageEstimated.WithLabelValues("fake", billingmeter.MetricChat))
	if err := p.ChatStream(tenantCtx("tenant-a"), req, func(gw.ChatResponseChunk) error { return nil }); err != nil {
		t.Fatal(err)
	}
	units := sink.settled()
	if len(units) != 1 {
		t.Fatalf("an estimate must be settled: %+v", units)
	}
	// 1000 in x 0.010/1k + 110 out x 0.030/1k = 0.0133 USD
	if want := billingmeter.MicrosFor(0.0133, testMarkup); units[0].Micros != want {
		t.Fatalf("micros = %d, want %d", units[0].Micros, want)
	}
	if units[0].Metadata[billingmeter.MetaUsageEstimated] != true {
		t.Fatalf("estimate must be marked: %+v", units[0].Metadata)
	}
	if got := counterValue(usageEstimated.WithLabelValues("fake", billingmeter.MetricChat)) - before; got != 1 {
		t.Fatalf("estimated counter delta = %v", got)
	}
	// Real usage is never marked estimated.
	sink2 := newSink()
	p2 := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 10}}, platformCfg(sink2, testCatalog(t)))
	if err := p2.ChatStream(tenantCtx("tenant-a"), req, func(gw.ChatResponseChunk) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if u := sink2.settled(); len(u) != 1 || u[0].Metadata[billingmeter.MetaUsageEstimated] != nil {
		t.Fatalf("reported usage must not be marked estimated: %+v", u)
	}
}

func counterValue(c interface{ Write(*dto.Metric) error }) float64 {
	m := &dto.Metric{}
	if err := c.Write(m); err != nil {
		return -1
	}
	return m.GetCounter().GetValue()
}

// BYOK attempts are counted on the display counters and recorded as
// attempts, but never priced, never refused and never charged.
func TestBYOKCountedNeverDebited(t *testing.T) {
	sink := newSink()
	inner := &fakeProvider{usage: gw.Usage{PromptTokens: 120, CompletionTokens: 30}}
	p := Wrap(inner, Config{Meter: sink, Provenance: billingmeter.ProvenanceBYOK, CatalogCache: testCatalog(t)})
	ctx := tenantCtx("tenant-a")
	if _, err := p.Chat(ctx, gw.ChatCompletionRequest{Model: "not-in-catalog"}); err != nil {
		t.Fatalf("BYOK must serve any model: %v", err)
	}
	if err := p.ChatStream(ctx, gw.ChatCompletionRequest{Model: "priced-1"}, func(gw.ChatResponseChunk) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Embed(ctx, gw.EmbeddingsRequest{Model: "priced-embed", Input: "x"}); err != nil {
		t.Fatal(err)
	}
	// Recorded as attempts for display, never charged.
	if units := sink.settled(); len(units) != 3 || len(chargedUnits(units)) != 0 {
		t.Fatalf("BYOK attempts must be recorded and never charged: %+v", units)
	}
	for _, u := range sink.settled() {
		if u.Attempt == nil || u.Attempt.Provenance != billingmeter.ProvenanceBYOK || u.Metadata[billingmeter.MetaCatalogUSD] != nil {
			t.Fatalf("BYOK record must say byok and carry no price: %+v", u)
		}
	}
	got := sink.counted("org-a")
	if got.Requests != 3 || got.InputTokens != 120+120+120 || got.OutputTokens != 30+30 {
		t.Fatalf("BYOK counts = %+v", got)
	}
	// Platform attempts are counted too, alongside the settle.
	plat := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 10, CompletionTokens: 5}}, platformCfg(sink, testCatalog(t)))
	if _, err := plat.Chat(ctx, gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatal(err)
	}
	if got := sink.counted("org-a"); got.Requests != 4 || got.InputTokens != 370 || got.OutputTokens != 65 {
		t.Fatalf("platform attempt not counted: %+v", got)
	}
	if len(chargedUnits(sink.settled())) != 1 {
		t.Fatal("platform attempt must still be charged")
	}
	// Two tenants of different organizations count into different rows.
	sink.orgs["tenant-b"] = "org-b"
	if _, err := p.Chat(tenantCtx("tenant-b"), gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatal(err)
	}
	if got := sink.counted("org-b"); got.Requests != 1 {
		t.Fatalf("org-b counts = %+v", got)
	}
	// BYOK with Responses keeps the Responses API and stays see-through.
	rp := Wrap(&fakeResponsesProvider{fakeProvider: &fakeProvider{}, rusage: gw.ResponseUsage{InputTokens: 7, OutputTokens: 3}},
		Config{Meter: sink, Provenance: billingmeter.ProvenanceBYOK})
	if _, ok := rp.(gw.ResponsesProvider); !ok {
		t.Fatal("BYOK Responses wrapper must expose the Responses API")
	}
	if _, ok := rp.(gw.Unwrapper); !ok {
		t.Fatal("BYOK Responses wrapper must be see-through")
	}
	if _, err := rp.(gw.ResponsesProvider).CreateResponse(tenantCtx("tenant-b"), gw.CreateResponseRequest{Model: "anything"}); err != nil {
		t.Fatal(err)
	}
	if got := sink.counted("org-b"); got.Requests != 2 || got.InputTokens != 127 || got.OutputTokens != 33 {
		t.Fatalf("responses not counted: %+v", got)
	}
}

type noUsageStream struct{ *fakeProvider }

func (p *noUsageStream) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	return onChunk(gw.ChatResponseChunk{Model: req.Model})
}

// A served BYOK stream whose provider never reports usage is still one
// request on the display counters, with no tokens: the character estimate
// is its spend estimate on the attempt record only, never display or limit
// volume. It is never debited.
func TestStreamWithoutUsageCountsRequest(t *testing.T) {
	sink := newSink()
	for i := 0; i < 2; i++ {
		prov := billingmeter.ProvenanceBYOK
		p := Wrap(&noUsageStream{&fakeProvider{}}, Config{Meter: sink, Provenance: prov, CatalogCache: testCatalog(t)})
		if err := p.ChatStream(tenantCtx("tenant-a"), gw.ChatCompletionRequest{Model: "priced-1"}, func(gw.ChatResponseChunk) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	if got := sink.counted("org-a"); got.Requests != 2 || got.InputTokens != 0 || got.OutputTokens != 0 {
		t.Fatalf("counts = %+v", got)
	}
	if len(chargedUnits(sink.settled())) != 0 {
		t.Fatal("no tokens, no debit")
	}
}
