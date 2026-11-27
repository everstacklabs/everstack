package billing

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	"github.com/everstacklabs/everstack/internal/billingtrial"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/services/catalog"
)

// trialSink is a Sink with the TrialGate capability. Takes are counted per
// organization against a fixed allowance.
type trialSink struct {
	*fakeSink
	mu          sync.Mutex
	settings    billingtrial.Settings
	settingsErr error
	takeErr     error
	allowance   int
	taken       map[string]int
	refunds     []uuid.UUID
	refundErr   error
	// rowless lists orgs with no trial row yet; claimed ones get a row on
	// GrantTrial.
	rowless map[string]bool
	claimed map[string]bool
	grants  int
}

func newTrialSink() *trialSink {
	return &trialSink{
		fakeSink: newSink(),
		settings: billingtrial.Settings{Enabled: true, Model: "gpt-5.4-mini", MaxOutputTokens: 300,
			MaxInputBytes: 4000, RequestsPerOrg: 20, DailyRequests: 430},
		allowance: 20,
		taken:     map[string]int{},
	}
}

func (s *trialSink) TrialSettings(context.Context) (billingtrial.Settings, error) {
	return s.settings, s.settingsErr
}

func (s *trialSink) TakeTrial(_ context.Context, org string) (billingtrial.TakeResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.takeErr != nil {
		return billingtrial.TakeResult{}, s.takeErr
	}
	if s.rowless[org] {
		return billingtrial.TakeResult{}, &billingtrial.RefusedError{Reason: billingtrial.ReasonNotEligible}
	}
	if s.taken[org] >= s.allowance {
		return billingtrial.TakeResult{}, &billingtrial.RefusedError{Reason: billingtrial.ReasonExhausted}
	}
	s.taken[org]++
	return billingtrial.TakeResult{ID: uuid.New(), Used: s.taken[org], Granted: s.allowance, DayUsed: s.taken[org]}, nil
}

func (s *trialSink) RefundTrial(_ context.Context, org string, id uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.refundErr != nil {
		return s.refundErr
	}
	s.refunds = append(s.refunds, id)
	s.taken[org]--
	return nil
}

func (s *trialSink) GrantTrial(_ context.Context, org string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.grants++
	if s.claimed[org] {
		delete(s.rowless, org)
		return true, nil
	}
	return !s.rowless[org], nil
}

func (s *trialSink) takenBy(org string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.taken[org]
}

func trialCfg(sink Sink) Config {
	return Config{Meter: sink, Provenance: billingmeter.ProvenanceTrial, KeyID: ""}
}

func textReq(model, text string) gw.ChatCompletionRequest {
	return gw.ChatCompletionRequest{Model: model, Messages: []gw.Message{gw.NewMessage(gw.RoleUser, gw.Text(text))}}
}

func wantRefusal(t *testing.T, err error, reason string) *billingmeter.ModelTrialRefusedError {
	t.Helper()
	r, ok := billingmeter.AsModelTrialRefusal(err)
	if !ok {
		t.Fatalf("err = %v, want a model trial refusal %s", err, reason)
	}
	if r.Reason != reason {
		t.Fatalf("reason = %s, want %s", r.Reason, reason)
	}
	return r
}

// recordingProvider records the request it was dispatched with.
type recordingProvider struct {
	fakeProvider
	got gw.ChatCompletionRequest
}

func (p *recordingProvider) Chat(ctx context.Context, req gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	p.got = req
	return p.fakeProvider.Chat(ctx, req)
}

// The trial row is never served unwrapped: without an active meter that can
// take requests, its provider refuses everything instead of returning the
// raw provider.
func TestWrapTrialNeverReturnsInner(t *testing.T) {
	inactive := newSink()
	inactive.active = false
	for name, cfg := range map[string]Config{
		"nil meter":       {Provenance: billingmeter.ProvenanceTrial},
		"inactive meter":  trialCfg(inactive),
		"no trial gate":   trialCfg(newSink()),
		"inactive w/gate": func() Config { s := newTrialSink(); s.active = false; return trialCfg(s) }(),
	} {
		t.Run(name, func(t *testing.T) {
			base := &fakeProvider{}
			p := Wrap(base, cfg)
			if p == gw.Provider(base) {
				t.Fatal("Wrap returned the raw provider for the trial row")
			}
			_, err := p.Chat(tenantCtx("tenant-a"), textReq("gpt-5.4-mini", "hi"))
			wantRefusal(t, err, billingmeter.ReasonModelTrialPaused)
			if base.calls != 0 {
				t.Fatal("the raw provider was called")
			}
		})
	}
}

// UnwrapTo and every capability lookup stop at the trial gate: the raw
// provider (and its Responses API) is unreachable through it.
func TestTrialMiddlewareCannotBeUnwrapped(t *testing.T) {
	base := &fakeResponsesProvider{fakeProvider: &fakeProvider{}}
	p := Wrap(base, trialCfg(newTrialSink()))
	if _, ok := gw.UnwrapTo[gw.ResponsesProvider](p); ok {
		t.Fatal("UnwrapTo reached a ResponsesProvider through the trial gate")
	}
	if _, ok := gw.UnwrapTo[*fakeResponsesProvider](p); ok {
		t.Fatal("UnwrapTo reached the raw provider through the trial gate")
	}
	if _, ok := p.(gw.Unwrapper); ok {
		t.Fatal("the trial gate implements Unwrapper")
	}
	if _, ok := p.(gw.ResponsesProvider); ok {
		t.Fatal("the trial gate exposes the Responses API")
	}
	if _, err := p.Embed(tenantCtx("tenant-a"), gw.EmbeddingsRequest{Model: "gpt-5.4-mini", Input: "x"}); err == nil {
		t.Fatal("Embed was served on the trial row")
	} else {
		wantRefusal(t, err, billingmeter.ReasonModelTrialModelUnsupported)
	}
	if base.calls != 0 {
		t.Fatal("the raw provider was called")
	}
}

// Requests outside the trial's shape are refused before anything is taken.
func TestTrialValidationRefusesBeforeTaking(t *testing.T) {
	img := "https://example.com/a.png"
	big := strings.Repeat("a", 4001)
	cases := map[string]struct {
		req    gw.ChatCompletionRequest
		reason string
	}{
		// "@openai/gpt-5" and a tenant custom model reach the provider as
		// their post-route upstream model name.
		"other model via @openai/":     {textReq("gpt-5", "hi"), billingmeter.ReasonModelTrialModelUnsupported},
		"custom model to another name": {textReq("ft:gpt-4o:acme", "hi"), billingmeter.ReasonModelTrialModelUnsupported},
		"image part": {gw.ChatCompletionRequest{Model: "gpt-5.4-mini", Messages: []gw.Message{{Role: gw.RoleUser,
			Content: []gw.ContentPart{{Type: "image_url", ImageURL: &img}}}}}, billingmeter.ReasonModelTrialTextOnly},
		"tools": {func() gw.ChatCompletionRequest {
			r := textReq("gpt-5.4-mini", "hi")
			r.Tools = []gw.ToolDefinition{{Type: "function"}}
			return r
		}(), billingmeter.ReasonModelTrialToolsUnsupported},
		"tool_choice": {func() gw.ChatCompletionRequest {
			r := textReq("gpt-5.4-mini", "hi")
			r.ToolChoice = "auto"
			return r
		}(), billingmeter.ReasonModelTrialToolsUnsupported},
		"tool message": {gw.ChatCompletionRequest{Model: "gpt-5.4-mini", Messages: []gw.Message{
			{Role: gw.RoleTool, ToolCallID: "c1", Content: []gw.ContentPart{gw.Text("r")}}}}, billingmeter.ReasonModelTrialToolsUnsupported},
		"input too large": {textReq("gpt-5.4-mini", big), billingmeter.ReasonModelTrialInputTooLarge},
		"input too large summed": {gw.ChatCompletionRequest{Model: "gpt-5.4-mini", Messages: []gw.Message{
			gw.NewMessage(gw.RoleSystem, gw.Text(strings.Repeat("a", 2001))),
			gw.NewMessage(gw.RoleUser, gw.Text(strings.Repeat("b", 2000)))}}, billingmeter.ReasonModelTrialInputTooLarge},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			sink := newTrialSink()
			base := &fakeProvider{}
			p := Wrap(base, trialCfg(sink))
			_, err := p.Chat(tenantCtx("tenant-a"), tc.req)
			wantRefusal(t, err, tc.reason)
			if err := p.ChatStream(tenantCtx("tenant-a"), tc.req, func(gw.ChatResponseChunk) error { return nil }); err == nil {
				t.Fatal("stream was served")
			}
			if sink.takenBy("org-a") != 0 || base.calls != 0 {
				t.Fatalf("taken = %d, provider calls = %d; want nothing", sink.takenBy("org-a"), base.calls)
			}
		})
	}
}

// A served request is reshaped: output clamped, reasoning none, metadata
// dropped, Chat Completions only; the remaining count reaches the handler.
func TestTrialShapesTheRequestAndNotesRemaining(t *testing.T) {
	sink := newTrialSink()
	base := &recordingProvider{fakeProvider: fakeProvider{usage: gw.Usage{PromptTokens: 10, CompletionTokens: 5}}}
	p := Wrap(base, trialCfg(sink))
	req := textReq("GPT-5.4-mini", strings.Repeat("a", 4000))
	req.Sampling.MaxTokens = 5000
	req.Sampling.ReasoningEffort = "high"
	req.Metadata = map[string]any{"user": "x"}
	req.UseResponsesAPI = true
	ctx, note := billingmeter.WithModelTrialNote(tenantCtx("tenant-a"))
	if _, err := p.Chat(ctx, req); err != nil {
		t.Fatal(err)
	}
	got := base.got
	if got.Sampling.MaxCompletionTokens != 300 || got.Sampling.MaxTokens != 0 {
		t.Fatalf("max tokens = %d/%d, want 300/0", got.Sampling.MaxCompletionTokens, got.Sampling.MaxTokens)
	}
	if got.Sampling.ReasoningEffort != "none" || got.Metadata != nil || got.UseResponsesAPI || got.Model != "gpt-5.4-mini" {
		t.Fatalf("request not reshaped: %+v", got)
	}
	if n, ok := note.Remaining(); !ok || n != 19 {
		t.Fatalf("remaining = %d, %v; want 19", n, ok)
	}
	// A smaller request is kept.
	req2 := textReq("gpt-5.4-mini", "hi")
	req2.Sampling.MaxCompletionTokens = 50
	if _, err := p.Chat(tenantCtx("tenant-a"), req2); err != nil {
		t.Fatal(err)
	}
	if base.got.Sampling.MaxCompletionTokens != 50 {
		t.Fatalf("max completion tokens = %d, want 50", base.got.Sampling.MaxCompletionTokens)
	}
}

// Trial units are recorded with provenance trial, never charged, whatever
// the provider reported; the usage display still counts them.
func TestTrialUnitsAreNeverCharged(t *testing.T) {
	sink := newTrialSink()
	p := Wrap(&fakeProvider{usage: gw.Usage{PromptTokens: 1000, CompletionTokens: 300}}, trialCfg(sink))
	if _, err := p.Chat(tenantCtx("tenant-a"), textReq("gpt-5.4-mini", "hi")); err != nil {
		t.Fatal(err)
	}
	if err := p.ChatStream(tenantCtx("tenant-a"), textReq("gpt-5.4-mini", "hi"), func(gw.ChatResponseChunk) error { return nil }); err != nil {
		t.Fatal(err)
	}
	units := sink.settled()
	if len(units) != 2 {
		t.Fatalf("units = %d, want 2", len(units))
	}
	for _, u := range units {
		if u.Micros != 0 || u.Attempt == nil || u.Attempt.Provenance != billingmeter.ProvenanceTrial || u.OrgID != "org-a" {
			t.Fatalf("unit = %+v", u)
		}
	}
	if c := sink.counted("org-a"); c.Requests != 2 {
		t.Fatalf("counted requests = %d, want 2", c.Requests)
	}
}

// Refunds follow the fixed rule: only a provider error before any output.
func TestTrialRefundRule(t *testing.T) {
	upstream := &gw.UpstreamError{Provider: "openai", StatusCode: 500, Message: "server error"}

	t.Run("unary provider error status refunds", func(t *testing.T) {
		sink := newTrialSink()
		p := Wrap(&fakeProvider{err: upstream}, trialCfg(sink))
		if _, err := p.Chat(tenantCtx("tenant-a"), textReq("gpt-5.4-mini", "hi")); err == nil {
			t.Fatal("want the provider error")
		}
		if sink.takenBy("org-a") != 0 || len(sink.refunds) != 1 {
			t.Fatalf("taken = %d, refunds = %d; want 0, 1", sink.takenBy("org-a"), len(sink.refunds))
		}
	})
	t.Run("unary timeout keeps the count", func(t *testing.T) {
		sink := newTrialSink()
		p := Wrap(&fakeProvider{err: context.DeadlineExceeded}, trialCfg(sink))
		_, _ = p.Chat(tenantCtx("tenant-a"), textReq("gpt-5.4-mini", "hi"))
		if sink.takenBy("org-a") != 1 || len(sink.refunds) != 0 {
			t.Fatalf("taken = %d, refunds = %d; want 1, 0", sink.takenBy("org-a"), len(sink.refunds))
		}
	})
	t.Run("client cancel keeps the count", func(t *testing.T) {
		sink := newTrialSink()
		p := Wrap(&fakeProvider{err: upstream}, trialCfg(sink))
		ctx, cancel := context.WithCancel(tenantCtx("tenant-a"))
		cancel()
		_, _ = p.Chat(ctx, textReq("gpt-5.4-mini", "hi"))
		if sink.takenBy("org-a") != 1 {
			t.Fatalf("taken = %d, want 1", sink.takenBy("org-a"))
		}
	})
	t.Run("stream error before content refunds", func(t *testing.T) {
		sink := newTrialSink()
		p := Wrap(&failingStream{fakeProvider: &fakeProvider{}, err: upstream}, trialCfg(sink))
		if err := p.ChatStream(tenantCtx("tenant-a"), textReq("gpt-5.4-mini", "hi"), func(gw.ChatResponseChunk) error { return nil }); err == nil {
			t.Fatal("want the provider error")
		}
		if sink.takenBy("org-a") != 0 {
			t.Fatalf("taken = %d, want 0", sink.takenBy("org-a"))
		}
	})
	t.Run("stream cut after content keeps the count", func(t *testing.T) {
		sink := newTrialSink()
		p := Wrap(&failingStream{fakeProvider: &fakeProvider{}, err: upstream, content: "partial"}, trialCfg(sink))
		_ = p.ChatStream(tenantCtx("tenant-a"), textReq("gpt-5.4-mini", "hi"), func(gw.ChatResponseChunk) error { return nil })
		if sink.takenBy("org-a") != 1 {
			t.Fatalf("taken = %d, want 1", sink.takenBy("org-a"))
		}
	})
	t.Run("failed refund keeps the count", func(t *testing.T) {
		sink := newTrialSink()
		sink.refundErr = errors.New("billing db down")
		p := Wrap(&fakeProvider{err: upstream}, trialCfg(sink))
		_, _ = p.Chat(tenantCtx("tenant-a"), textReq("gpt-5.4-mini", "hi"))
		if sink.takenBy("org-a") != 1 {
			t.Fatalf("taken = %d, want 1", sink.takenBy("org-a"))
		}
	})
}

type failingStream struct {
	*fakeProvider
	err     error
	content string
}

func (p *failingStream) ChatStream(ctx context.Context, req gw.ChatCompletionRequest, on func(gw.ChatResponseChunk) error) error {
	p.calls++
	if p.content != "" {
		if err := on(gw.ChatResponseChunk{Choices: []gw.ChoiceDelta{{Delta: gw.NewMessage(gw.RoleAssistant, gw.Text(p.content))}}}); err != nil {
			return err
		}
	}
	return p.err
}

// Take refusals and failures map to the gateway reasons; any failure to ask
// the database, or to read the settings, refuses (fail closed).
func TestTrialTakeRefusals(t *testing.T) {
	cases := map[string]struct {
		takeErr     error
		settingsErr error
		reason      string
	}{
		"exhausted":     {takeErr: &billingtrial.RefusedError{Reason: billingtrial.ReasonExhausted}, reason: billingmeter.ReasonModelTrialExhausted},
		"expired":       {takeErr: &billingtrial.RefusedError{Reason: billingtrial.ReasonExpired}, reason: billingmeter.ReasonModelTrialExpired},
		"paused":        {takeErr: &billingtrial.RefusedError{Reason: billingtrial.ReasonPaused}, reason: billingmeter.ReasonModelTrialPaused},
		"capped":        {takeErr: &billingtrial.RefusedError{Reason: billingtrial.ReasonPaused, ResumesAt: time.Now().Add(time.Hour)}, reason: billingmeter.ReasonModelTrialPaused},
		"not eligible":  {takeErr: &billingtrial.RefusedError{Reason: billingtrial.ReasonNotEligible}, reason: billingmeter.ReasonModelTrialNotEligible},
		"revoked":       {takeErr: &billingtrial.RefusedError{Reason: billingtrial.ReasonRevoked}, reason: billingmeter.ReasonModelTrialNotEligible},
		"db error":      {takeErr: errors.New("connection refused"), reason: billingmeter.ReasonModelTrialPaused},
		"settings down": {settingsErr: errors.New("connection refused"), reason: billingmeter.ReasonModelTrialPaused},
		"rate limited":  {takeErr: &billingtrial.RefusedError{Reason: billingtrial.ReasonRateLimited, RetryAfter: 2500 * time.Millisecond}, reason: billingmeter.ReasonModelTrialRateLimited},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			sink := newTrialSink()
			sink.takeErr, sink.settingsErr = tc.takeErr, tc.settingsErr
			base := &fakeProvider{}
			_, err := Wrap(base, trialCfg(sink)).Chat(tenantCtx("tenant-a"), textReq("gpt-5.4-mini", "hi"))
			r := wantRefusal(t, err, tc.reason)
			if base.calls != 0 {
				t.Fatal("dispatched after a refusal")
			}
			if strings.Contains(r.Error(), "org-a") || r.Error() == "" {
				t.Fatalf("message = %q", r.Error())
			}
			if tc.reason == billingmeter.ReasonModelTrialRateLimited {
				if r.HTTPStatusCode() != 429 || r.RetryAfterSeconds() != 3 {
					t.Fatalf("rate limited: status %d, retry %d", r.HTTPStatusCode(), r.RetryAfterSeconds())
				}
			} else if r.HTTPStatusCode() != 402 {
				t.Fatalf("status = %d, want 402", r.HTTPStatusCode())
			}
		})
	}
}

// An unknown tenant has no organization: refused, nothing taken.
func TestTrialRefusesWithoutAnOrganization(t *testing.T) {
	sink := newTrialSink()
	_, err := Wrap(&fakeProvider{}, trialCfg(sink)).Chat(tenantCtx("tenant-unknown"), textReq("gpt-5.4-mini", "hi"))
	wantRefusal(t, err, billingmeter.ReasonModelTrialNotEligible)
}

// Each organization spends only its own allowance: the org comes from the
// authenticated tenant, never the request.
func TestTrialTakesFromTheRequestOrganizationOnly(t *testing.T) {
	sink := newTrialSink()
	sink.orgs["tenant-b"] = "org-b"
	sink.allowance = 2
	p := Wrap(&fakeProvider{}, trialCfg(sink))
	for i := 0; i < 2; i++ {
		if _, err := p.Chat(tenantCtx("tenant-a"), textReq("gpt-5.4-mini", "hi")); err != nil {
			t.Fatal(err)
		}
	}
	_, err := p.Chat(tenantCtx("tenant-a"), textReq("gpt-5.4-mini", "hi"))
	wantRefusal(t, err, billingmeter.ReasonModelTrialExhausted)
	if _, err := p.Chat(tenantCtx("tenant-b"), textReq("gpt-5.4-mini", "hi")); err != nil {
		t.Fatalf("org b refused after org a exhausted its own allowance: %v", err)
	}
	if sink.takenBy("org-a") != 2 || sink.takenBy("org-b") != 1 {
		t.Fatalf("taken a=%d b=%d", sink.takenBy("org-a"), sink.takenBy("org-b"))
	}
}

// The gateway's own calls never spend a customer's free requests.
func TestTrialRefusesInternalCalls(t *testing.T) {
	sink := newTrialSink()
	base := &fakeProvider{}
	ctx := contextkeys.WithInternalCall(tenantCtx("tenant-a"))
	_, err := Wrap(base, trialCfg(sink)).Chat(ctx, textReq("gpt-5.4-mini", "summarize"))
	wantRefusal(t, err, billingmeter.ReasonModelTrialModelUnsupported)
	if sink.takenBy("org-a") != 0 || base.calls != 0 {
		t.Fatal("an internal call took a free request")
	}
}

// The trial row is created on the first free request, never before: a take
// that finds no row grants from the signup claim and takes again; an org
// without a claim stays not eligible.
func TestTrialGrantsOnTheFirstRequest(t *testing.T) {
	sink := newTrialSink()
	sink.orgs["tenant-b"] = "org-b"
	sink.rowless = map[string]bool{"org-a": true, "org-b": true}
	sink.claimed = map[string]bool{"org-a": true}
	p := Wrap(&fakeProvider{}, trialCfg(sink))
	if _, err := p.Chat(tenantCtx("tenant-a"), textReq("gpt-5.4-mini", "hi")); err != nil {
		t.Fatalf("first request with a claim: %v", err)
	}
	if sink.takenBy("org-a") != 1 {
		t.Fatalf("taken = %d, want 1", sink.takenBy("org-a"))
	}
	_, err := p.Chat(tenantCtx("tenant-b"), textReq("gpt-5.4-mini", "hi"))
	wantRefusal(t, err, billingmeter.ReasonModelTrialNotEligible)
	if sink.grants != 2 {
		t.Fatalf("grants = %d, want 2 (one per rowless org)", sink.grants)
	}
	// Later requests of org a find the row: no further grant.
	if _, err := p.Chat(tenantCtx("tenant-a"), textReq("gpt-5.4-mini", "hi")); err != nil {
		t.Fatal(err)
	}
	if sink.grants != 2 {
		t.Fatalf("grants = %d after the row exists", sink.grants)
	}
}

// A trial model the OpenAI provider would send to the Responses API (the
// unshaped path) is never served.
func TestTrialRefusesAResponsesModel(t *testing.T) {
	cat := catalog.NewCache()
	if err := cat.Load([]byte(`
providers:
  openai: {name: openai, models: [{name: gpt-5.4-mini, api_type: responses, capabilities: [chat], status: available}]}
`), []byte("providers: {}\n")); err != nil {
		t.Fatal(err)
	}
	if TrialModelServable(cat, "gpt-5.4-mini") || TrialModelServable(nil, "gpt-5.1-codex") || !TrialModelServable(nil, "gpt-5.4-mini") {
		t.Fatal("TrialModelServable misclassified")
	}
	sink := newTrialSink()
	cfg := trialCfg(sink)
	cfg.CatalogCache = cat
	base := &fakeProvider{}
	_, err := Wrap(base, cfg).Chat(tenantCtx("tenant-a"), textReq("gpt-5.4-mini", "hi"))
	wantRefusal(t, err, billingmeter.ReasonModelTrialModelUnsupported)
	if sink.takenBy("org-a") != 0 || base.calls != 0 {
		t.Fatal("a responses model was taken or served")
	}
}
