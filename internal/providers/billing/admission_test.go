package billing

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/services/catalog"
)

const admissionCatalogYAML = `
providers:
  fake:
    name: fake
    models:
      - name: priced-1
        input_cost_per_1k: 0.010
        output_cost_per_1k: 0.030
        capabilities: [chat]
        status: available
      - name: capped
        input_cost_per_1k: 0.001
        output_cost_per_1k: 0.002
        max_tokens: 128000
        max_output_tokens: 1000
        capabilities: [chat]
        status: available
      - name: priced-embed
        input_cost_per_1k: 0.002
        output_cost_per_1k: 0
        capabilities: [embeddings]
        status: available
`

func admissionCatalog(t *testing.T) *catalog.Cache {
	t.Helper()
	c := catalog.NewCache()
	if err := c.Load([]byte(admissionCatalogYAML), []byte("providers: {}\n")); err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	return c
}

func text(s string) []gw.ContentPart { return []gw.ContentPart{{Type: "text", Text: &s}} }

// A platform attempt at zero balance is evaluated (the sink would log the
// would-deny) and still served, with the inner provider's response returned
// unchanged.
func TestShadowAdmissionNeverChangesTheServedResponse(t *testing.T) {
	sink := newSink()
	inner := &fakeProvider{respModel: "priced-1-2026", usage: gw.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30}}
	p := Wrap(inner, platformCfg(sink, admissionCatalog(t)))
	req := gw.ChatCompletionRequest{Model: "priced-1", Messages: []gw.Message{{Role: "user", Content: text("hello")}}, Sampling: gw.SamplingParams{MaxTokens: 100}}

	got, err := p.Chat(tenantCtx("tenant-a"), req)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	want, _ := (&fakeProvider{respModel: "priced-1-2026", usage: inner.usage}).Chat(context.Background(), req)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("served response changed by admission:\n got %+v\nwant %+v", got, want)
	}
	if inner.calls != 1 {
		t.Fatalf("inner called %d times, want 1", inner.calls)
	}
	admits := sink.admitted()
	if len(admits) != 1 {
		t.Fatalf("ShadowAdmit called %d times, want 1", len(admits))
	}
	a := admits[0]
	if a.OrgID != "org-a" || a.Model != "priced-1" || a.Provider != "fake" || a.KeyID != "key-1" || a.Modality != billingmeter.MetricChat || a.CorrelationID != "corr-1" {
		t.Fatalf("admission request mis-attributed: %+v", a)
	}
	if a.FreeAllowed {
		t.Fatal("fake/priced-1 is not on the embedded free allowlist")
	}
	if a.Inflight != 1 || a.InflightCap <= 0 {
		t.Fatalf("inflight = %d cap = %d, want 1 and the plans.json cap", a.Inflight, a.InflightCap)
	}
	if a.SettleUnit == "" || a.SettleUnit != sink.settled()[0].ID {
		t.Fatal("admission and settle must share the settle unit id")
	}
	if n := platformInflight.current("org-a"); n != 0 {
		t.Fatalf("in-flight slot leaked: %d", n)
	}
}

func TestShadowAdmissionStreamAndErrorsReleaseInflight(t *testing.T) {
	sink := newSink()
	inner := &fakeProvider{usage: gw.Usage{PromptTokens: 1, CompletionTokens: 1}}
	p := Wrap(inner, platformCfg(sink, admissionCatalog(t)))
	ctx := tenantCtx("tenant-a")
	if err := p.ChatStream(ctx, gw.ChatCompletionRequest{Model: "priced-1"}, func(gw.ChatResponseChunk) error { return nil }); err != nil {
		t.Fatalf("stream: %v", err)
	}
	inner.err = errors.New("upstream 500")
	if _, err := p.Chat(ctx, gw.ChatCompletionRequest{Model: "priced-1"}); err == nil {
		t.Fatal("upstream error must pass through")
	}
	if n := platformInflight.current("org-a"); n != 0 {
		t.Fatalf("in-flight slot leaked after stream/error: %d", n)
	}
	if len(sink.admitted()) != 2 {
		t.Fatalf("admissions = %d, want 2", len(sink.admitted()))
	}
}

func TestShadowAdmissionInflightCountsConcurrentAttempts(t *testing.T) {
	sink := newSink()
	release := make(chan struct{})
	started := make(chan struct{}, 8)
	inner := &blockingProvider{fakeProvider: &fakeProvider{usage: gw.Usage{PromptTokens: 1, CompletionTokens: 1}}, started: started, release: release}
	p := Wrap(inner, platformCfg(sink, admissionCatalog(t)))
	ctx := tenantCtx("tenant-a")
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = p.Chat(ctx, gw.ChatCompletionRequest{Model: "priced-1"})
		}()
	}
	for i := 0; i < 8; i++ {
		<-started
	}
	if n := platformInflight.current("org-a"); n != 8 {
		t.Fatalf("in-flight while blocked = %d, want 8", n)
	}
	close(release)
	wg.Wait()
	seen := []int{}
	for _, a := range sink.admitted() {
		seen = append(seen, a.Inflight)
	}
	sort.Ints(seen)
	if !reflect.DeepEqual(seen, []int{1, 2, 3, 4, 5, 6, 7, 8}) {
		t.Fatalf("in-flight at dispatch = %v, want 1..8", seen)
	}
	if n := platformInflight.current("org-a"); n != 0 {
		t.Fatalf("in-flight slot leaked: %d", n)
	}
}

type blockingProvider struct {
	*fakeProvider
	started chan struct{}
	release chan struct{}
	mu      sync.Mutex
}

func (p *blockingProvider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	p.started <- struct{}{}
	<-p.release
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fakeProvider.Chat(ctx, req)
}

func TestShadowAdmissionResolveErrorStillServes(t *testing.T) {
	sink := newSink()
	sink.err = errors.New("platform db down")
	inner := &fakeProvider{usage: gw.Usage{PromptTokens: 1, CompletionTokens: 1}}
	p := Wrap(inner, platformCfg(sink, admissionCatalog(t)))
	if _, err := p.Chat(tenantCtx("tenant-a"), gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatalf("a DB outage must not deny in G1: %v", err)
	}
	admits := sink.admitted()
	if len(admits) != 1 || admits[0].ResolveErr == nil || admits[0].OrgID != "" {
		t.Fatalf("want one admission carrying the resolve error, got %+v", admits)
	}
}

func TestShadowAdmissionInternalAndAllowlistFlags(t *testing.T) {
	sink := newSink()
	inner := &namedProvider{fakeProvider: &fakeProvider{usage: gw.Usage{PromptTokens: 1, CompletionTokens: 1}}, name: "openai"}
	cat := catalog.NewCache()
	if err := cat.Load([]byte(`
providers:
  openai:
    name: openai
    models:
      - name: gpt-4o-mini
        input_cost_per_1k: 0.00015
        output_cost_per_1k: 0.0006
        capabilities: [chat]
        status: available
      - name: gpt-4o
        input_cost_per_1k: 0.0025
        output_cost_per_1k: 0.01
        capabilities: [chat]
        status: available
`), []byte("providers: {}\n")); err != nil {
		t.Fatal(err)
	}
	p := Wrap(inner, platformCfg(sink, cat))
	ctx := tenantCtx("tenant-a")
	_, _ = p.Chat(ctx, gw.ChatCompletionRequest{Model: "gpt-4o-mini"})
	_, _ = p.Chat(ctx, gw.ChatCompletionRequest{Model: "gpt-4o"})
	_, _ = p.Chat(contextkeys.WithInternalCall(ctx), gw.ChatCompletionRequest{Model: "gpt-4o"})
	admits := sink.admitted()
	if len(admits) != 3 {
		t.Fatalf("admissions = %d, want 3", len(admits))
	}
	if !admits[0].FreeAllowed || admits[1].FreeAllowed {
		t.Fatalf("allowlist flags = %v, %v; want true, false", admits[0].FreeAllowed, admits[1].FreeAllowed)
	}
	if admits[1].Internal || !admits[2].Internal {
		t.Fatal("internal flag not carried")
	}
}

type namedProvider struct {
	*fakeProvider
	name string
}

func (p *namedProvider) Name() string { return p.name }

func TestBYOKAndSelfHostedNeverEvaluated(t *testing.T) {
	sink := newSink()
	inner := &fakeProvider{usage: gw.Usage{PromptTokens: 1, CompletionTokens: 1}}
	byok := Wrap(inner, Config{Meter: sink, Provenance: billingmeter.ProvenanceBYOK, CatalogCache: admissionCatalog(t)})
	if _, err := byok.Chat(tenantCtx("tenant-a"), gw.ChatCompletionRequest{Model: "unpriced-anything"}); err != nil {
		t.Fatalf("BYOK: %v", err)
	}
	selfHosted := Wrap(inner, Config{Meter: sink, CatalogCache: admissionCatalog(t)})
	if selfHosted != gw.Provider(inner) {
		t.Fatal("self-hosted (no provenance) must be returned unwrapped")
	}
	if n := len(sink.admitted()); n != 0 {
		t.Fatalf("BYOK/self-hosted produced %d admissions, want 0", n)
	}
	if n := platformInflight.current("org-a"); n != 0 {
		t.Fatalf("BYOK took an in-flight slot: %d", n)
	}
}

func TestEstimatorFormula(t *testing.T) {
	sink := newSink()
	m := Wrap(&fakeProvider{}, platformCfg(sink, admissionCatalog(t))).(*Middleware)
	// priced-1: 100 prompt x 0.010/1k + 200 out x 0.030/1k = 0.007 USD
	// Applying testMarkup gives 8750 micros.
	if got := m.estimateMicros("priced-1", estimateInput{promptTokens: 100, maxOutputTokens: 200}); got != 8750 {
		t.Fatalf("explicit max_tokens estimate = %d, want 8750", got)
	}
	// capped: request omits max_tokens -> catalog max_output_tokens (1000),
	// not the 128000 context: (10 x 0.001 + 1000 x 0.002)/1k = 0.00201 USD
	// Applying testMarkup and rounding half up gives 2513 micros.
	if got := m.estimateMicros("capped", estimateInput{promptTokens: 10}); got != 2513 {
		t.Fatalf("catalog-default estimate = %d, want 2513", got)
	}
	// priced-1 has no max_output_tokens: default 4096.
	// (0 + 4096 x 0.030)/1k = 0.12288 USD; applying testMarkup gives 153600 micros.
	if got := m.estimateMicros("priced-1", estimateInput{}); got != 153600 {
		t.Fatalf("fallback estimate = %d, want 153600", got)
	}
	// Embeddings price input only: 1000 x 0.002/1k; applying testMarkup gives 2500 micros.
	if got := m.estimateMicros("priced-embed", embedEstimate(gw.EmbeddingsRequest{Input: string(make([]byte, 4000))})); got != 2500 {
		t.Fatalf("embed estimate = %d, want 2500", got)
	}
	if got := m.estimateMicros("not-in-catalog", estimateInput{promptTokens: 10}); got != 0 {
		t.Fatalf("unpriced estimate = %d, want 0", got)
	}
	// The estimate and the settled charge use the same markup, so a call
	// that uses exactly its prompt and max_tokens settles to the estimate.
	inner := &fakeProvider{usage: gw.Usage{PromptTokens: 100, CompletionTokens: 200}}
	p := Wrap(inner, platformCfg(sink, admissionCatalog(t)))
	if _, err := p.Chat(tenantCtx("tenant-a"), gw.ChatCompletionRequest{Model: "priced-1", Sampling: gw.SamplingParams{MaxTokens: 200}}); err != nil {
		t.Fatal(err)
	}
	u := sink.settled()[len(sink.settled())-1]
	if u.Micros != 8750 {
		t.Fatalf("settled = %d, want 8750 (same markup as the estimate)", u.Micros)
	}
}

func TestResponsesEstimateCountsTextWithoutSerializing(t *testing.T) {
	in := responsesEstimate(gw.CreateResponseRequest{
		Instructions:    "abcd",
		Input:           []gw.ResponseInput{{Type: "message", Role: "user", Content: text(string(make([]byte, 400)))}},
		MaxOutputTokens: 50,
	})
	// (4 + 16 overhead + 400) / 4 = 105 tokens.
	if in.promptTokens != 105 || in.maxOutputTokens != 50 {
		t.Fatalf("responses estimate = %+v, want 105 prompt, 50 max", in)
	}
}

// Estimator fixtures. These are representative synthetic tuples (prompt
// text size, requested max_tokens, provider-reported usage), not recorded
// production traffic: the production distribution comes from
// billing_admission_estimate_ratio and the admission_estimate_micros ledger
// metadata during the shadow week. What the fixtures pin: with max_tokens set
// and no extra headroom factor (testMarkup is applied to estimate
// and charge alike), under-coverage is bounded by the prompt-token heuristic
// (a call that uses its whole output cap can exceed the estimate by a few
// percent), and the median sits just above 1.
func TestEstimatorAgainstFixtures(t *testing.T) {
	sink := newSink()
	inner := &fakeProvider{}
	p := Wrap(inner, platformCfg(sink, admissionCatalog(t)))
	ctx := tenantCtx("tenant-a")
	fixtures := []struct {
		promptChars, maxTokens, in, out int
	}{
		{400, 256, 105, 180},
		{2_000, 512, 520, 470},
		{8_000, 1_024, 2_050, 300},
		{16_000, 2_048, 4_100, 1_900},
		{1_200, 64, 310, 64},
		{40_000, 4_096, 10_200, 2_500},
		{600, 1_000, 160, 12},
		{3_000, 300, 790, 299},
		{12_000, 800, 3_050, 650},
		{200, 4_000, 55, 3_100},
	}
	ratios := make([]float64, 0, len(fixtures))
	for i, f := range fixtures {
		inner.usage = gw.Usage{PromptTokens: f.in, CompletionTokens: f.out}
		req := gw.ChatCompletionRequest{Model: "priced-1", Messages: []gw.Message{{Role: "user", Content: text(string(make([]byte, f.promptChars)))}}, Sampling: gw.SamplingParams{MaxTokens: f.maxTokens}}
		if _, err := p.Chat(ctx, req); err != nil {
			t.Fatalf("fixture %d: %v", i, err)
		}
		u := sink.settled()[i]
		est, ok := u.Metadata[billingmeter.MetaAdmissionEstimate].(int64)
		if !ok || est <= 0 {
			t.Fatalf("fixture %d: settle unit lacks %s: %+v", i, billingmeter.MetaAdmissionEstimate, u.Metadata)
		}
		ratios = append(ratios, float64(est)/float64(u.Micros))
	}
	sort.Float64s(ratios)
	median := (ratios[4] + ratios[5]) / 2
	t.Logf("estimate/actual: min %.2f median %.2f max %.2f", ratios[0], median, ratios[len(ratios)-1])
	if ratios[0] < 0.95 {
		t.Fatalf("an estimate under-covered its charge by more than 5%%: min ratio %.3f", ratios[0])
	}
	if median < 1 || median > 3 || math.IsNaN(median) {
		t.Fatalf("median overshoot %.2f too loose for reservations", median)
	}
}

// BYOK attempts never pay for the G1 admission estimate. Without a spend
// budget (no v2 evaluator) the estimate is not computed at all; with one,
// it is computed exactly once, at list price, for the spend limit hold P.
func TestEstimateIsLazyForBYOK(t *testing.T) {
	sink := newSink()
	byok := Wrap(&fakeProvider{}, Config{Meter: sink, Provenance: billingmeter.ProvenanceBYOK, CatalogCache: admissionCatalog(t)}).(*byokMiddleware)
	called := false
	a, err := byok.preDispatch(tenantCtx("tenant-a"), billingmeter.MetricChat, "priced-1", func() estimateInput { called = true; return estimateInput{} })
	if err != nil {
		t.Fatal(err)
	}
	byok.done(a)
	if called {
		t.Fatal("BYOK attempts without a spend budget must not pay for the admission estimate")
	}

	budget := &budgetSink{fakeSink: newSink()}
	byok = Wrap(&fakeProvider{}, Config{Meter: budget, Provenance: billingmeter.ProvenanceBYOK, CatalogCache: admissionCatalog(t)}).(*byokMiddleware)
	calls := 0
	a, err = byok.preDispatch(tenantCtx("tenant-a"), billingmeter.MetricChat, "priced-1", func() estimateInput { calls++; return estimateInput{promptTokens: 1} })
	if err != nil {
		t.Fatal(err)
	}
	byok.done(a)
	if calls != 1 || len(budget.fakeSink.admits) != 0 {
		t.Fatalf("estimate calls %d, G1 admits %d: want one list-price estimate for P and no G1 admission", calls, len(budget.fakeSink.admits))
	}
}

func TestModelLabelIsBounded(t *testing.T) {
	m := Wrap(&fakeProvider{}, platformCfg(newSink(), admissionCatalog(t))).(*Middleware)
	if got := m.modelLabel("priced-1-any-suffix-a-caller-invents"); got != "priced-1" {
		t.Fatalf("prefix-matched label = %q, want the catalog name", got)
	}
	if got := m.modelLabel("nope"); got != "uncatalogued" {
		t.Fatalf("unknown label = %q", got)
	}
}

type panickingSink struct{ *fakeSink }

func (panickingSink) ShadowAdmit(billingmeter.AdmissionRequest) { panic("shadow bug") }

// A bug in the shadow path must neither fail the request nor leak the
// in-flight slot it took.
func TestShadowAdmitPanicIsContainedAndReleasesSlot(t *testing.T) {
	sink := panickingSink{newSink()}
	inner := &fakeProvider{usage: gw.Usage{PromptTokens: 1, CompletionTokens: 1}}
	p := Wrap(inner, platformCfg(sink, admissionCatalog(t)))
	if _, err := p.Chat(tenantCtx("tenant-a"), gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if inner.calls != 1 {
		t.Fatalf("inner calls = %d, want 1", inner.calls)
	}
	if n := platformInflight.current("org-a"); n != 0 {
		t.Fatalf("in-flight slot leaked after a shadow panic: %d", n)
	}
}

// Every aborted-stream shape (#787) still releases the in-flight slot taken
// at dispatch and still reports one shadow admission.
func TestAbortedStreamsReleaseInflightSlot(t *testing.T) {
	for _, mode := range []abortMode{abortCtxCancel, abortWriteFail, abortAfterUsage, abortUpstreamError, abortBeforeContent} {
		sink := newSink()
		p := Wrap(&scriptedStream{fakeProvider: &fakeProvider{}, mode: mode, usage: gw.Usage{PromptTokens: 10, CompletionTokens: 10}}, platformCfg(sink, testCatalog(t)))
		ctx, cancel := context.WithCancel(tenantCtx("tenant-a"))
		chunks := 0
		if mode == abortBeforeContent {
			cancel()
		}
		_ = p.ChatStream(ctx, abortReq(), func(c gw.ChatResponseChunk) error {
			chunks++
			switch {
			case mode == abortCtxCancel && chunks == 4:
				cancel()
			case mode == abortWriteFail && chunks == 2:
				return errClientGone
			case mode == abortAfterUsage && c.Usage != nil:
				return errClientGone
			}
			return nil
		})
		cancel()
		if n := platformInflight.current("org-a"); n != 0 {
			t.Fatalf("mode %d: in-flight slot leaked: %d", mode, n)
		}
		if len(sink.admitted()) != 1 {
			t.Fatalf("mode %d: admissions = %d, want 1", mode, len(sink.admitted()))
		}
	}
}
