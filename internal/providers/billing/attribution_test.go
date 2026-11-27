package billing

// Spend attribution (docs/design/spend-limits-v2.md, slice S1): every
// attempt records the gateway API key that made it (api_key_id, never the
// provider key, which is MetaKeyID), and a BYOK attempt records its cost at
// catalog list price with no markup. Nothing reads these yet.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/services/catalog"
	"github.com/everstacklabs/everstack/internal/telemetry/metrics"
)

// attributionCatalogYAML adds a model with exact cache rates to the shared
// test models.
const attributionCatalogYAML = `
providers:
  fake:
    name: fake
    models:
      - name: priced-1
        input_cost_per_1k: 0.010
        output_cost_per_1k: 0.030
        capabilities: [chat]
        status: available
      - name: cached-1
        input_cost_per_1k: 0.003
        output_cost_per_1k: 0.015
        cache_read_cost_per_1k: 0.0003
        cache_write_cost_per_1k: 0.00375
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

func attributionCatalog(t *testing.T) *catalog.Cache {
	t.Helper()
	c := catalog.NewCache()
	if err := c.Load([]byte(attributionCatalogYAML), []byte("providers: {}\n")); err != nil {
		t.Fatalf("load catalog: %v", err)
	}
	return c
}

func byokCfg(sink Sink, cat *catalog.Cache) Config {
	return Config{Meter: sink, Provenance: billingmeter.ProvenanceBYOK, KeyID: "provider-key-1", CatalogCache: cat}
}

// keyCtx is a request authenticated with gateway API key keyID of tenant-a.
func keyCtx(keyID string) context.Context {
	return contextkeys.WithAuthenticatedAPIKeyID(tenantCtx("tenant-a"), "tenant-a", "hash-"+keyID, keyID)
}

// listPrice is what the shared cost calculator says, converted to micros
// with no markup. Tests compare against it rather than hand arithmetic.
func listPrice(t *testing.T, cat *catalog.Cache, model string, in, out, cacheRead, cacheWrite int) int64 {
	t.Helper()
	cost := metrics.NewCostCalculatorFromCache(cat).CalculateCostWithCacheTokens("fake", model, in, out, cacheRead, cacheWrite)
	if !cost.Priceable {
		t.Fatalf("test model %q must be priced", model)
	}
	return billingmeter.ListPriceMicros(cost.EstimatedUSD)
}

func estOf(t *testing.T, u billingmeter.SettleUnit) int64 {
	t.Helper()
	if u.Attempt == nil || u.Attempt.EstCostMicros == nil {
		t.Fatalf("BYOK attempt must carry an estimate: %+v", u.Attempt)
	}
	return *u.Attempt.EstCostMicros
}

func TestAttemptAttributedToItsAPIKey(t *testing.T) {
	cat := attributionCatalog(t)
	for _, cfg := range []func(Sink, *catalog.Cache) Config{platformCfg, byokCfg} {
		sink := newSink()
		p := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 100, CompletionTokens: 10}}, cfg(sink, cat))
		for _, ctx := range []context.Context{keyCtx("key-A"), keyCtx("key-B"), tenantCtx("tenant-a")} {
			if _, err := p.Chat(ctx, gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
				t.Fatal(err)
			}
		}
		units := sink.settled()
		if len(units) != 3 {
			t.Fatalf("want 3 units, got %d", len(units))
		}
		for i, want := range []string{"key-A", "key-B", ""} {
			u := units[i]
			if u.Attempt.APIKeyID != want {
				t.Fatalf("%s unit %d: api_key_id %q, want %q", u.Attempt.Provenance, i, u.Attempt.APIKeyID, want)
			}
			got, has := u.Metadata[billingmeter.MetaAPIKeyID]
			if want == "" && has {
				t.Fatalf("no API key: metadata must not carry api_key_id, got %v", got)
			}
			if want != "" && got != want {
				t.Fatalf("metadata api_key_id %v, want %q", got, want)
			}
			// The provider key keeps its own, unrelated name.
			if u.Metadata[billingmeter.MetaKeyID] == want && want != "" {
				t.Fatalf("api_key_id must not be written into the provider key field")
			}
		}
	}
}

// The BYOK estimate is the shared calculator's list price, never the
// Everstack rate card: the customer's provider does not charge markup.
func TestBYOKEstimateIsListPriceWithoutMarkup(t *testing.T) {
	cat := attributionCatalog(t)
	sink := newSink()
	p := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 1000, CompletionTokens: 500}}, byokCfg(sink, cat))
	if _, err := p.Chat(keyCtx("key-A"), gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatal(err)
	}
	u := onlyUnit(t, sink)
	usd := metrics.NewCostCalculatorFromCache(cat).CalculateCostWithCacheTokens("fake", "priced-1", 1000, 500, 0, 0).EstimatedUSD
	want := billingmeter.MicrosFor(usd, 1)
	if got := estOf(t, u); got != want || got != 25000 {
		t.Fatalf("estimate %d, want list price %d (25000)", got, want)
	}
	if marked := billingmeter.MicrosFor(usd, sink.Markup()); estOf(t, u) == marked {
		t.Fatalf("estimate %d carries the markup", marked)
	}
	if u.Micros != 0 || u.Attempt.Unpriced {
		t.Fatalf("BYOK is never charged and this model is priced: micros %d unpriced %t", u.Micros, u.Attempt.Unpriced)
	}
}

// Cache reads and writes are priced at their own rates, and reasoning tokens
// count once: the providers fold them into CompletionTokens (#904, #906),
// and the breakdown is not added on top.
func TestBYOKEstimatePricesCacheAndReasoningOnce(t *testing.T) {
	cat := attributionCatalog(t)
	sink := newSink()
	usage := gw.Usage{
		PromptTokens:      10000,
		CompletionTokens:  1000,
		PromptDetails:     &gw.TokenDetails{CacheReadTokens: 6000, CacheWriteTokens: 2000},
		CompletionDetails: &gw.TokenDetails{ReasoningTokens: 400},
	}
	p := Wrap(&fakeProvider{usage: usage}, byokCfg(sink, cat))
	if _, err := p.Chat(keyCtx("key-A"), gw.ChatCompletionRequest{Model: "cached-1"}); err != nil {
		t.Fatal(err)
	}
	got := estOf(t, onlyUnit(t, sink))
	want := listPrice(t, cat, "cached-1", 10000, 1000, 6000, 2000)
	if got != want || got != 30300 {
		t.Fatalf("estimate %d, want %d (30300)", got, want)
	}
	if doubled := listPrice(t, cat, "cached-1", 10000, 1400, 6000, 2000); got == doubled {
		t.Fatal("reasoning tokens counted twice")
	}
	if flat := listPrice(t, cat, "cached-1", 10000, 1000, 0, 0); got == flat {
		t.Fatal("cached tokens priced at the full input rate")
	}

	// Streaming reports the same usage in its final chunk.
	sink = newSink()
	p = Wrap(&fakeProvider{usage: usage}, byokCfg(sink, cat))
	if err := p.ChatStream(keyCtx("key-A"), gw.ChatCompletionRequest{Model: "cached-1"}, func(gw.ChatResponseChunk) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := estOf(t, onlyUnit(t, sink)); got != want {
		t.Fatalf("stream estimate %d, want %d", got, want)
	}
}

// A dated or unknown variant the provider reports back falls back to the
// requested model's price, as platform billing does.
func TestBYOKEstimateFallsBackToRequestedModel(t *testing.T) {
	cat := attributionCatalog(t)
	sink := newSink()
	p := Wrap(&fakeProvider{respModel: "mystery-variant", usage: gw.Usage{PromptTokens: 1000, CompletionTokens: 500}}, byokCfg(sink, cat))
	if _, err := p.Chat(keyCtx("key-A"), gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatal(err)
	}
	u := onlyUnit(t, sink)
	if got := estOf(t, u); got != listPrice(t, cat, "priced-1", 1000, 500, 0, 0) || u.Attempt.Unpriced {
		t.Fatalf("estimate %d unpriced %t", got, u.Attempt.Unpriced)
	}
}

// A model the catalog cannot price is served on BYOK (never refused), records
// $0 and is flagged unpriced. A zero-token attempt is not flagged: there was
// nothing to price.
func TestBYOKUnpricedModelRecordsZeroAndFlag(t *testing.T) {
	cat := attributionCatalog(t)
	for _, model := range []string{"my-fine-tune", "zero-rate"} {
		sink := newSink()
		inner := &fakeProvider{usage: gw.Usage{PromptTokens: 1000, CompletionTokens: 500}}
		p := Wrap(inner, byokCfg(sink, cat))
		if _, err := p.Chat(keyCtx("key-A"), gw.ChatCompletionRequest{Model: model}); err != nil {
			t.Fatalf("%s: BYOK must serve an unpriced model: %v", model, err)
		}
		u := onlyUnit(t, sink)
		if estOf(t, u) != 0 || !u.Attempt.Unpriced {
			t.Fatalf("%s: estimate %d unpriced %t, want 0 and flagged", model, estOf(t, u), u.Attempt.Unpriced)
		}
	}

	sink := newSink()
	p := Wrap(&fakeProvider{err: errors.New("upstream 503")}, byokCfg(sink, cat))
	ctx := billingmeter.WithAttemptKind(keyCtx("key-A"), billingmeter.KindPrimary)
	if _, err := p.Chat(ctx, gw.ChatCompletionRequest{Model: "my-fine-tune"}); err == nil {
		t.Fatal("upstream error must surface")
	}
	u := onlyUnit(t, sink)
	if estOf(t, u) != 0 || u.Attempt.Unpriced || u.Attempt.APIKeyID != "key-A" {
		t.Fatalf("failed zero-token attempt: %+v", u.Attempt)
	}
}

// Platform attempts keep their charged amount (marked up) and carry no BYOK
// estimate, so S2 never counts one attempt twice.
func TestPlatformKeepsChargedCostAndNoEstimate(t *testing.T) {
	cat := attributionCatalog(t)
	sink := newSink()
	p := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 1000, CompletionTokens: 500}}, platformCfg(sink, cat))
	if _, err := p.Chat(keyCtx("key-A"), gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatal(err)
	}
	u := onlyUnit(t, sink)
	if u.Micros != 31250 {
		t.Fatalf("platform charge changed: %d, want 31250", u.Micros)
	}
	if u.Attempt.EstCostMicros != nil || u.Attempt.Unpriced || u.Attempt.APIKeyID != "key-A" {
		t.Fatalf("platform record: est %v unpriced %t key %q", u.Attempt.EstCostMicros, u.Attempt.Unpriced, u.Attempt.APIKeyID)
	}
}

func TestBYOKEmbeddingsEstimate(t *testing.T) {
	cat := attributionCatalog(t)
	sink := newSink()
	p := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 5000}}, byokCfg(sink, cat))
	if _, err := p.(gw.Provider).Embed(keyCtx("key-E"), gw.EmbeddingsRequest{Model: "priced-embed", Input: "hello"}); err != nil {
		t.Fatal(err)
	}
	u := onlyUnit(t, sink)
	if got := estOf(t, u); got != listPrice(t, cat, "priced-embed", 5000, 0, 0, 0) || got != 10000 || u.Attempt.APIKeyID != "key-E" {
		t.Fatalf("embeddings: est %d key %q", got, u.Attempt.APIKeyID)
	}
}

func TestBYOKResponsesEstimate(t *testing.T) {
	cat := attributionCatalog(t)
	rusage := gw.ResponseUsage{
		InputTokens: 10000, OutputTokens: 1000,
		InputTokensDetails:  &gw.ResponseUsageDetails{CachedTokens: 6000},
		OutputTokensDetails: &gw.ResponseUsageDetails{ReasoningTokens: 400},
	}
	want := listPrice(t, cat, "cached-1", 10000, 1000, 6000, 0)
	for _, stream := range []bool{false, true} {
		sink := newSink()
		p := Wrap(&fakeResponsesProvider{fakeProvider: &fakeProvider{}, rusage: rusage}, byokCfg(sink, cat)).(gw.ResponsesProvider)
		req := gw.CreateResponseRequest{Model: "cached-1"}
		var err error
		if stream {
			err = p.CreateResponseStream(keyCtx("key-R"), req, func(gw.CreateResponseResponse) error { return nil })
		} else {
			_, err = p.CreateResponse(keyCtx("key-R"), req)
		}
		if err != nil {
			t.Fatal(err)
		}
		u := onlyUnit(t, sink)
		if got := estOf(t, u); got != want || u.Attempt.APIKeyID != "key-R" {
			t.Fatalf("stream=%t: est %d want %d, key %q", stream, got, want, u.Attempt.APIKeyID)
		}
	}
}

// contentNoUsageStream streams content and ends cleanly without a usage
// chunk, as some OpenAI-compatible providers do.
type contentNoUsageStream struct{ *fakeProvider }

func (p *contentNoUsageStream) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	for i := 0; i < 4; i++ {
		msg := gw.NewMessage(gw.RoleAssistant, gw.Text(strings.Repeat("o", 100)))
		if err := onChunk(gw.ChatResponseChunk{Model: req.Model, Choices: []gw.ChoiceDelta{{Delta: msg}}}); err != nil {
			return err
		}
	}
	return nil
}

// A BYOK stream without usage settles the character estimate, like a
// platform stream, instead of counting as free.
func TestBYOKStreamWithoutUsageSettlesEstimate(t *testing.T) {
	cat := attributionCatalog(t)
	want := billingmeter.ListPriceMicros(abortEstimateUSD)
	for name, inner := range map[string]gw.Provider{
		"served, no usage chunk": &contentNoUsageStream{&fakeProvider{}},
		"upstream error":         &scriptedStream{fakeProvider: &fakeProvider{}, mode: abortUpstreamError},
	} {
		sink := newSink()
		p := Wrap(inner, byokCfg(sink, cat))
		_ = p.ChatStream(keyCtx("key-S"), abortReq(), func(gw.ChatResponseChunk) error { return nil })
		u := onlyUnit(t, sink)
		if got := estOf(t, u); got != want || !u.Attempt.Estimated || u.Micros != 0 {
			t.Fatalf("%s: est %d (want %d) estimated %t micros %d", name, got, want, u.Attempt.Estimated, u.Micros)
		}
		if u.Attempt.InputTokens != 1000 || u.Attempt.OutputTokens != 100 {
			t.Fatalf("%s: estimated tokens %d/%d", name, u.Attempt.InputTokens, u.Attempt.OutputTokens)
		}
		// The usage display (and any meter fed from it) sees provider
		// reported tokens only: one request, no estimated volume.
		if got := sink.counted("org-a"); got.Requests != 1 || got.InputTokens != 0 || got.OutputTokens != 0 {
			t.Fatalf("%s: display counters fed from an estimate: %+v", name, got)
		}
	}
}

// Reported BYOK usage still reaches the display counters.
func TestBYOKReportedUsageCounted(t *testing.T) {
	sink := newSink()
	p := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 100, CompletionTokens: 20}}, byokCfg(sink, attributionCatalog(t)))
	if err := p.ChatStream(keyCtx("key-S"), gw.ChatCompletionRequest{Model: "priced-1"}, func(gw.ChatResponseChunk) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := sink.counted("org-a"); got.Requests != 1 || got.InputTokens != 100 || got.OutputTokens != 20 {
		t.Fatalf("reported usage not counted: %+v", got)
	}
}

// Every race leg is recorded exactly once with its own estimate and the
// request's key: the winner and a loser that finished both count, a loser
// cancelled before it returned anything records 0.
func TestBYOKRaceLegsEachRecordedOnce(t *testing.T) {
	cat := attributionCatalog(t)
	sink := newSink()
	race := billingmeter.NewRace()
	base := keyCtx("key-race")
	winner := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 1000, CompletionTokens: 100}}, byokCfg(sink, cat))
	finisher := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 2000, CompletionTokens: 200}}, byokCfg(sink, cat))
	cancelled := Wrap(&fakeProvider{err: context.Canceled}, byokCfg(sink, cat))

	var wg sync.WaitGroup
	for i, p := range []gw.Provider{winner, finisher} {
		wg.Add(1)
		go func(i int, p gw.Provider) {
			defer wg.Done()
			_, _ = p.Chat(race.Leg(base, i), gw.ChatCompletionRequest{Model: "priced-1"})
		}(i, p)
	}
	wg.Wait()
	race.Claim(0)
	_, _ = cancelled.Chat(race.Leg(base, 2), gw.ChatCompletionRequest{Model: "priced-1"})
	race.Close()
	race.Close() // idempotent: must not settle anything again

	units := sink.settled()
	if len(units) != 3 {
		t.Fatalf("want one unit per leg, got %d", len(units))
	}
	ids := map[string]bool{}
	est := map[int64]int64{}
	for _, u := range units {
		if ids[u.ID] {
			t.Fatalf("settle unit %s recorded twice", u.ID)
		}
		ids[u.ID] = true
		if u.Attempt.APIKeyID != "key-race" {
			t.Fatalf("leg key %q", u.Attempt.APIKeyID)
		}
		est[u.Attempt.InputTokens] = estOf(t, u)
	}
	if est[1000] != listPrice(t, cat, "priced-1", 1000, 100, 0, 0) || est[2000] != listPrice(t, cat, "priced-1", 2000, 200, 0, 0) || est[0] != 0 {
		t.Fatalf("leg estimates %v", est)
	}
}

// A primary that breaks part way and the fallback that serves are two
// attempts, each recorded once with its own estimate; a configured retry and
// a platform key rotation are recorded too.
func TestBYOKFallbackAndRetriesEachRecordedOnce(t *testing.T) {
	cat := attributionCatalog(t)
	sink := newSink()
	broken := Wrap(&scriptedStream{fakeProvider: &fakeProvider{}, mode: abortUpstreamError}, byokCfg(sink, cat))
	served := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 1000, CompletionTokens: 100}}, byokCfg(sink, cat))
	rotated := Wrap(&fakeProvider{err: errors.New("401 invalid provider key")}, byokCfg(sink, cat))
	base := keyCtx("key-F")

	_ = broken.ChatStream(billingmeter.WithAttemptKind(base, billingmeter.KindPrimary), abortReq(), func(gw.ChatResponseChunk) error { return nil })
	_, _ = rotated.Chat(billingmeter.WithAttemptKind(base, billingmeter.KindPlatformRetry), gw.ChatCompletionRequest{Model: "priced-1"})
	_ = broken.ChatStream(billingmeter.WithAttemptKind(base, billingmeter.KindRetry), abortReq(), func(gw.ChatResponseChunk) error { return nil })
	if _, err := served.Chat(billingmeter.WithLastAttempt(billingmeter.WithAttemptKind(base, billingmeter.KindFallback), true), gw.ChatCompletionRequest{Model: "priced-1"}); err != nil {
		t.Fatal(err)
	}

	units := sink.settled()
	if len(units) != 4 {
		t.Fatalf("want 4 attempts, got %d: %+v", len(units), units)
	}
	type rec struct {
		role string
		est  int64
	}
	var got []rec
	for _, u := range units {
		if u.Attempt.APIKeyID != "key-F" {
			t.Fatalf("attempt key %q", u.Attempt.APIKeyID)
		}
		got = append(got, rec{u.Attempt.Role, estOf(t, u)})
	}
	streamEst := billingmeter.ListPriceMicros(abortEstimateUSD)
	want := []rec{
		{billingmeter.RoleFailedFallback, streamEst},
		{billingmeter.RolePlatformRetry, 0},
		{billingmeter.RoleRetry, streamEst},
		{billingmeter.RoleServed, listPrice(t, cat, "priced-1", 1000, 100, 0, 0)},
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("attempt %d: %+v, want %+v", i, got[i], want[i])
		}
	}
}

// midThinkingCachedStream reports the exact prompt usage at message start
// (Anthropic's message_start, cache reads and writes included), buffers 400
// characters of thinking and breaks off before any chunk.
type midThinkingCachedStream struct{ *fakeProvider }

func (p *midThinkingCachedStream) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, onChunk func(gw.ChatResponseChunk) error) error {
	pending := billingmeter.PendingOutputFrom(ctx)
	pending.SetPromptUsage(10000, 6000, 2000)
	pending.SetReasoningChars(400)
	return errors.New("upstream: connection reset mid-thinking")
}

// A BYOK stream that breaks off mid-thinking (#917) is recorded once, with
// the request's API key and a list-price estimate over the exact cache-aware
// prompt plus the pending reasoning (400 chars = 100 output tokens). The
// estimate stays off the usage display.
func TestBYOKAbortedMidThinkingAttributedAndEstimatedOnce(t *testing.T) {
	cat := attributionCatalog(t)
	sink := newSink()
	p := Wrap(&midThinkingCachedStream{&fakeProvider{}}, byokCfg(sink, cat))
	ctx := billingmeter.WithAttemptKind(keyCtx("key-think"), billingmeter.KindPrimary)
	if err := p.ChatStream(ctx, gw.ChatCompletionRequest{Model: "cached-1"}, func(gw.ChatResponseChunk) error { return nil }); err == nil {
		t.Fatal("aborted stream must surface its error")
	}
	u := onlyUnit(t, sink)
	if u.Attempt.APIKeyID != "key-think" || u.Metadata[billingmeter.MetaAbortedInThinking] != true || !u.Attempt.Estimated || u.Micros != 0 {
		t.Fatalf("attempt %+v metadata %+v micros %d", u.Attempt, u.Metadata, u.Micros)
	}
	if u.Attempt.InputTokens != 10000 || u.Attempt.OutputTokens != 100 {
		t.Fatalf("tokens %d/%d, want the exact 10000 prompt and 100 reasoning", u.Attempt.InputTokens, u.Attempt.OutputTokens)
	}
	if got, want := estOf(t, u), listPrice(t, cat, "cached-1", 10000, 100, 6000, 2000); got != want {
		t.Fatalf("estimate %d, want %d (cache-aware list price)", got, want)
	}
	if got := sink.counted("org-a"); got.Requests != 1 || got.InputTokens != 0 || got.OutputTokens != 0 {
		t.Fatalf("display counters fed from an estimate: %+v", got)
	}
}
