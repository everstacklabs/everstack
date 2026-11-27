package v1

// Streaming and unary routing reliability: plan order, what counts as
// delivered, step timeouts, fallback gating and observability.

import (
	"context"
	"errors"
	"net"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/billingmeter"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/billing"
	"github.com/everstacklabs/everstack/internal/services/catalog"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	pb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
)

// routeModel serves one model with a scripted stream: wait firstDelay, send
// chunks (each after chunkDelay), then return streamErr. fail, when set,
// fails Chat and ChatStream before anything is sent.
type routeModel struct {
	name, model string
	fail        error
	chunks      []gw.ChatResponseChunk
	firstDelay  time.Duration
	chunkDelay  time.Duration
	streamErr   error
	// called, when set, receives a value on every call (non-blocking).
	called chan struct{}

	mu    sync.Mutex
	calls int
}

func (p *routeModel) noteCall() {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	if p.called != nil {
		select {
		case p.called <- struct{}{}:
		default:
		}
	}
}

func (p *routeModel) Name() string                { return p.name }
func (p *routeModel) SupportsModel(m string) bool { return strings.EqualFold(m, p.model) }
func (p *routeModel) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}
func (p *routeModel) Chat(ctx context.Context, r gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	p.noteCall()
	if p.fail != nil {
		return gw.ChatCompletionResponse{}, p.fail
	}
	return gw.ChatCompletionResponse{ID: "x", Model: p.model, Choices: []gw.Choice{{Message: gw.NewMessage(gw.RoleAssistant, gw.Text("from "+p.model))}}}, nil
}
func (p *routeModel) ChatStream(ctx context.Context, r gw.ChatCompletionRequest, on func(gw.ChatResponseChunk) error) error {
	p.noteCall()
	if p.fail != nil {
		return p.fail
	}
	wait := func(d time.Duration) error {
		if d <= 0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(d):
			return nil
		}
	}
	if err := wait(p.firstDelay); err != nil {
		return err
	}
	chunks := p.chunks
	if chunks == nil {
		chunks = textChunks(p.model, "from "+p.model)
	}
	for i, c := range chunks {
		if i > 0 {
			if err := wait(p.chunkDelay); err != nil {
				return err
			}
		}
		if err := on(c); err != nil {
			return err
		}
	}
	return p.streamErr
}
func (p *routeModel) Embed(context.Context, gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	if p.fail != nil {
		return gw.EmbeddingsResponse{}, p.fail
	}
	return gw.EmbeddingsResponse{Embedding: []float64{1}, Model: p.model}, nil
}

// textChunks is a role-only opening chunk followed by one text chunk per
// part, the shape OpenAI-compatible upstreams stream.
func textChunks(model string, parts ...string) []gw.ChatResponseChunk {
	out := []gw.ChatResponseChunk{{ID: "c", Model: model, Choices: []gw.ChoiceDelta{{Delta: gw.Message{Role: gw.RoleAssistant}}}}}
	for _, p := range parts {
		out = append(out, gw.ChatResponseChunk{ID: "c", Model: model, Choices: []gw.ChoiceDelta{{Delta: gw.NewMessage(gw.RoleAssistant, gw.Text(p))}}})
	}
	return out
}

func routeServer(t *testing.T, fb validator.FallbackConfig, providers ...*routeModel) *Server {
	t.Helper()
	fb.Enabled = true
	cfg := &validator.GatewayConfig{LoadBalancer: validator.LoadBalancerConfig{Fallback: fb}}
	s := CreateServerWithContext(context.Background(), cfg, &validator.FeaturesConfig{})
	s.feat.Gateway.EnableStreaming = true
	for _, p := range providers {
		s.defaultBundle.Load().reg.Register(p)
	}
	return s
}

// collected is what a client received: the text of every message, and the
// fallback info of the first one.
type collected struct {
	mu       sync.Mutex
	text     strings.Builder
	messages int
	first    *pb.ChatCompletionResponse
}

func (c *collected) send(r *pb.ChatCompletionResponse) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.first == nil {
		c.first = r
	}
	c.messages++
	for _, ch := range r.GetChoices() {
		for _, part := range ch.GetMessage().GetContent() {
			c.text.WriteString(part.GetText())
		}
	}
	return nil
}

func (c *collected) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text.String()
}

func tenantCtx() context.Context {
	return contextkeys.WithTenantID(context.Background(), "tenant-1")
}

func TestStreamAttemptPlanOrder(t *testing.T) {
	step := func(name string, aliases ...string) FallbackStep {
		return FallbackStep{Aliases: aliases, Strategy: "priority", Name: name}
	}
	cases := []struct {
		name  string
		model string
		plan  []FallbackStep
		want  [][]string
	}{
		{"no plan", "m-primary", nil, [][]string{{"m-primary"}}},
		{"primary absent", "m-primary", []FallbackStep{step("default_fallback", "m-a"), step("f", "m-b")},
			[][]string{{"m-primary"}, {"m-a"}, {"m-b"}}},
		// The bug: the requested model listed in the plan started the
		// stream at steps[0], the default fallback.
		{"primary listed later", "m-primary", []FallbackStep{step("default_fallback", "m-a"), step("f", "m-primary"), step("g", "m-b")},
			[][]string{{"m-primary"}, {"m-a"}, {"m-b"}}},
		{"primary listed first", "m-primary", []FallbackStep{step("default_fallback", "m-primary"), step("f", "m-a")},
			[][]string{{"m-primary"}, {"m-a"}}},
		// Plan aliases are lower-cased; the requested model is not.
		{"case-insensitive", "GPT-X", []FallbackStep{step("f", "gpt-x"), step("g", "m-a")},
			[][]string{{"GPT-X"}, {"m-a"}}},
		{"repeated alias", "m-primary", []FallbackStep{step("f", "m-a"), step("g", "m-a"), step("h", "m-b")},
			[][]string{{"m-primary"}, {"m-a"}, {"m-b"}}},
		{"parallel step keeps its other legs", "m-primary", []FallbackStep{{Aliases: []string{"m-primary", "m-a", "m-b"}, Strategy: "parallel"}},
			[][]string{{"m-primary"}, {"m-a", "m-b"}}},
	}
	for _, tc := range cases {
		got := streamAttemptPlan(tc.model, tc.plan)
		var aliases [][]string
		for _, st := range got {
			aliases = append(aliases, st.Aliases)
		}
		if !reflect.DeepEqual(aliases, tc.want) {
			t.Errorf("%s: plan = %v, want %v", tc.name, aliases, tc.want)
		}
	}
}

func TestFallbackPlanOrdersDefaultThenFactorsByPriority(t *testing.T) {
	s := routeServer(t, validator.FallbackConfig{
		Default: validator.FallbackModelConfig{Enabled: true, Model: "M-Default"},
		Factors: []validator.FallbackFactorConfig{
			{Name: "second", Priority: 2, Strategy: "priority", Models: models("m-c")},
			{Name: "first", Priority: 1, Strategy: "priority", TimeoutMs: 7, Models: models("m-a", "m-b")},
			{Name: "race", Priority: 3, Strategy: "parallel", Models: models("m-d", "m-e")},
		},
	})
	var got []string
	for _, st := range s.fallbackPlan(tenantCtx()) {
		got = append(got, st.Name+":"+strings.Join(st.Aliases, ","))
	}
	want := []string{"default_fallback:m-default", "first:m-a", "first:m-b", "second:m-c", "race:m-d,m-e"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fallbackPlan = %v, want %v", got, want)
	}
	if !hasFallbackTarget(s.fallbackPlan(tenantCtx()), "m-a") || hasFallbackTarget([]FallbackStep{{Aliases: []string{"gpt-x"}}}, "GPT-X") {
		t.Fatal("hasFallbackTarget must ignore the requested model, case-insensitively")
	}
}

// A streaming request for a model the plan also lists is served by that
// model, not by the plan's first step.
func TestStreamServesRequestedModelFirst(t *testing.T) {
	primary := &routeModel{name: "pp", model: "m-primary"}
	other := &routeModel{name: "pa", model: "m-a"}
	s := routeServer(t, validator.FallbackConfig{
		Default: validator.FallbackModelConfig{Enabled: true, Model: "m-a"},
		Factors: []validator.FallbackFactorConfig{{Name: "f", Priority: 1, Strategy: "priority", Models: models("m-primary")}},
	}, primary, other)
	var got collected
	if err := processChatCompletion(tenantCtx(), s, chatReq("m-primary", true), got.send); err != nil {
		t.Fatal(err)
	}
	if got.String() != "from m-primary" || other.callCount() != 0 || primary.callCount() != 1 {
		t.Fatalf("served %q; primary calls %d, default fallback calls %d", got.String(), primary.callCount(), other.callCount())
	}
}

// Once text has reached the client, a failing stream ends with its error:
// no fallback may append a second model's answer to the partial one.
func TestStreamNoFallbackAfterContentDelivered(t *testing.T) {
	primary := &routeModel{name: "pp", model: "m-primary",
		chunks: textChunks("m-primary", "partial "), streamErr: errors.New("upstream 503: connection reset mid-stream")}
	fallback := &routeModel{name: "pa", model: "m-a"}
	s := routeServer(t, validator.FallbackConfig{
		Factors: []validator.FallbackFactorConfig{{Name: "f", Priority: 1, Strategy: "priority", Models: models("m-a")}},
	}, primary, fallback)
	var got collected
	err := processChatCompletion(tenantCtx(), s, chatReq("m-primary", true), got.send)
	if err == nil {
		t.Fatal("a stream broken after content must surface its error")
	}
	if got.String() != "partial " || fallback.callCount() != 0 {
		t.Fatalf("client received %q; fallback calls %d (want only the partial answer, no fallback)", got.String(), fallback.callCount())
	}
}

// A role-only opening chunk is not content: a stream that fails after it
// still falls back, and the client never sees the failed attempt's chunk.
func TestStreamFallsBackWhenOnlyEmptyChunksWereHeld(t *testing.T) {
	primary := &routeModel{name: "pp", model: "m-primary",
		chunks: textChunks("m-primary"), streamErr: errors.New("upstream 503: connection reset")}
	fallback := &routeModel{name: "pa", model: "m-a"}
	s := routeServer(t, validator.FallbackConfig{
		Factors: []validator.FallbackFactorConfig{{Name: "f", Priority: 1, Strategy: "priority", Models: models("m-a")}},
	}, primary, fallback)
	var got collected
	if err := processChatCompletion(tenantCtx(), s, chatReq("m-primary", true), got.send); err != nil {
		t.Fatal(err)
	}
	if got.String() != "from m-a" || fallback.callCount() != 1 {
		t.Fatalf("client received %q; fallback calls %d", got.String(), fallback.callCount())
	}
	if m := got.first.GetModel(); m != "m-a" {
		t.Fatalf("first message to the client came from %q, want m-a (the failed attempt's held chunk leaked)", m)
	}
}

// Tool calls are held until the attempt succeeds, so a stream that fails
// after sending only a tool-call fragment has delivered nothing and may
// still fall back.
func TestStreamFallsBackAfterHeldToolCallFragment(t *testing.T) {
	idx := 0
	toolChunk := gw.ChatResponseChunk{ID: "c", Model: "m-primary", Choices: []gw.ChoiceDelta{{Delta: gw.Message{Role: gw.RoleAssistant,
		ToolCalls: []gw.ToolCall{{Index: &idx, ID: "call_1", Function: gw.ToolCallFunction{Name: "lookup", Arguments: `{"q":`}}}}}}}
	primary := &routeModel{name: "pp", model: "m-primary",
		chunks: []gw.ChatResponseChunk{toolChunk}, streamErr: errors.New("upstream 503: connection reset")}
	fallback := &routeModel{name: "pa", model: "m-a"}
	s := routeServer(t, validator.FallbackConfig{
		Factors: []validator.FallbackFactorConfig{{Name: "f", Priority: 1, Strategy: "priority", Models: models("m-a")}},
	}, primary, fallback)
	var got collected
	if err := processChatCompletion(tenantCtx(), s, chatReq("m-primary", true), got.send); err != nil {
		t.Fatal(err)
	}
	if got.String() != "from m-a" || got.first.GetModel() != "m-a" {
		t.Fatalf("client received %q first from %q", got.String(), got.first.GetModel())
	}
}

// The step timeout bounds the wait for the first chunk, not the stream: a
// fallback target that starts at once and streams for longer than timeout_ms
// is served whole.
func TestStreamStepTimeoutBoundsFirstByteOnly(t *testing.T) {
	primary := &routeModel{name: "pp", model: "m-primary", fail: errors.New("upstream 503")}
	slow := &routeModel{name: "pa", model: "m-a", chunks: textChunks("m-a", "one ", "two ", "three"), chunkDelay: 120 * time.Millisecond}
	s := routeServer(t, validator.FallbackConfig{
		Factors: []validator.FallbackFactorConfig{{Name: "f", Priority: 1, Strategy: "priority", TimeoutMs: 100, Models: models("m-a")}},
	}, primary, slow)
	var got collected
	if err := processChatCompletion(tenantCtx(), s, chatReq("m-primary", true), got.send); err != nil {
		t.Fatalf("a stream longer than the step timeout was cut: %v (received %q)", err, got.String())
	}
	if got.String() != "one two three" {
		t.Fatalf("received %q, want the whole stream", got.String())
	}
}

// An upstream that sends nothing within the step timeout is abandoned and
// the next target serves; the abandoned attempt is a failed fallback, never
// mistaken for a client that walked away (which bills as served).
func TestStreamFirstByteTimeoutFallsBack(t *testing.T) {
	sink := newAttemptSink()
	primary := &routeModel{name: "pp", model: "m-primary", fail: errors.New("upstream 503")}
	stalled := &routeModel{name: "pa", model: "m-a", firstDelay: time.Hour}
	next := &routeModel{name: "pb", model: "m-b"}
	s := routeServerBilled(t, sink, validator.FallbackConfig{
		Factors: []validator.FallbackFactorConfig{
			{Name: "f", Priority: 1, Strategy: "priority", TimeoutMs: 50, Models: models("m-a")},
			{Name: "g", Priority: 2, Strategy: "priority", Models: models("m-b")},
		},
	}, primary, stalled, next)
	var got collected
	start := time.Now()
	if err := processChatCompletion(tenantCtx(), s, chatReq("m-primary", true), got.send); err != nil {
		t.Fatal(err)
	}
	if got.String() != "from m-b" {
		t.Fatalf("received %q, want the next target's answer", got.String())
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Fatalf("first-byte timeout did not fire (took %v)", elapsed)
	}
	if roles := sink.byModel()["m-a"]; !equalStrings(roles, []string{billingmeter.RoleFailedFallback}) {
		t.Fatalf("timed-out attempt roles = %v, want [failed_fallback]", roles)
	}
}

// routeServerBilled is routeServer with every provider behind the billing
// middleware, so a test can read each attempt's role.
func routeServerBilled(t *testing.T, sink *attemptSink, fb validator.FallbackConfig, providers ...*routeModel) *Server {
	t.Helper()
	cat := catalog.NewCache()
	if err := cat.Load([]byte(attemptCatalog), []byte("providers: {}\n")); err != nil {
		t.Fatal(err)
	}
	s := routeServer(t, fb)
	for _, p := range providers {
		s.defaultBundle.Load().reg.Register(billing.Wrap(p, billing.Config{Meter: sink, Provenance: billingmeter.ProvenancePlatform, KeyID: "k", CatalogCache: cat}))
	}
	return s
}

// The backoff between configured retries ends as soon as the client goes,
// on both paths, and no further attempt starts.
func TestBackoffAbandonedWhenClientCancels(t *testing.T) {
	for _, stream := range []bool{false, true} {
		primary := &routeModel{name: "pp", model: "m-primary", fail: errors.New("upstream 503")}
		flaky := &routeModel{name: "pa", model: "m-a", fail: errors.New("upstream temporarily unavailable"), called: make(chan struct{}, 1)}
		later := &routeModel{name: "pb", model: "m-b"}
		s := routeServer(t, validator.FallbackConfig{
			Factors: []validator.FallbackFactorConfig{
				{Name: "f", Priority: 1, Strategy: "priority", MaxAttempts: 3, BackoffMs: int(time.Hour / time.Millisecond), Models: models("m-a")},
				{Name: "g", Priority: 2, Strategy: "priority", Models: models("m-b")},
			},
		}, primary, flaky, later)
		ctx, cancel := context.WithCancel(tenantCtx())
		done := make(chan error, 1)
		go func() {
			var got collected
			done <- processChatCompletion(ctx, s, chatReq("m-primary", stream), got.send)
		}()
		// Cancel once the retried target has failed its first try, so
		// the request is in (or entering) its one-hour backoff.
		<-flaky.called
		cancel()
		select {
		case err := <-done:
			if err == nil {
				t.Fatalf("stream=%v: cancelled request returned nil", stream)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("stream=%v: backoff ignored the cancelled request", stream)
		}
		if flaky.callCount() != 1 || later.callCount() != 0 {
			t.Fatalf("stream=%v: attempts after cancel: retried %d, next target %d", stream, flaky.callCount(), later.callCount())
		}
	}
}

// Fallback answers upstream trouble (429, 5xx, timeouts, connection
// failures, a rejected key after rotation), never a request error: the next
// model gets the same request, fails the same way, and every attempt is
// charged.
func TestFallbackGatedOnErrorClass(t *testing.T) {
	_, dialErr := net.Dial("tcp", "127.0.0.1:1")
	if dialErr == nil {
		t.Skip("port 1 accepted a connection")
	}
	cases := []struct {
		name     string
		err      error
		fallback bool
	}{
		{"400 invalid request", gw.NewUpstreamError("pp", 400, []byte(`{"error":{"message":"bad field","type":"invalid_request_error"}}`)), false},
		{"400 context length", gw.NewUpstreamError("pp", 400, []byte(`{"error":{"message":"maximum context length is 8192 tokens","code":"context_length_exceeded"}}`)), false},
		{"404", gw.NewUpstreamError("pp", 404, []byte(`{"error":{"message":"no such model"}}`)), false},
		{"422", gw.NewUpstreamError("pp", 422, []byte(`{"error":{"message":"unprocessable"}}`)), false},
		{"429", gw.NewUpstreamError("pp", 429, []byte(`{"error":{"message":"slow down"}}`)), true},
		{"503", gw.NewUpstreamError("pp", 503, []byte(`{"error":{"message":"overloaded"}}`)), true},
		{"401 with no key to rotate to", gw.NewUpstreamError("pp", 401, []byte(`{"error":{"message":"invalid api key"}}`)), true},
		{"connection refused", dialErr, true},
		{"untyped upstream error", errors.New("upstream 503"), true},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			primary := &routeModel{name: "pp", model: "m-primary", fail: tc.err}
			fb := &routeModel{name: "pa", model: "m-a"}
			s := routeServer(t, validator.FallbackConfig{
				Factors: []validator.FallbackFactorConfig{{Name: "f", Priority: 1, Strategy: "priority", Models: models("m-a")}},
			}, primary, fb)
			var got collected
			err := processChatCompletion(tenantCtx(), s, chatReq("m-primary", stream), got.send)
			if tc.fallback && (err != nil || fb.callCount() != 1) {
				t.Errorf("%s stream=%v: want fallback served, got err=%v fallback calls=%d", tc.name, stream, err, fb.callCount())
			}
			if !tc.fallback && (err == nil || fb.callCount() != 0) {
				t.Errorf("%s stream=%v: want the error with no fallback, got err=%v fallback calls=%d", tc.name, stream, err, fb.callCount())
			}
		}
	}
}

// captureSpans routes the gateway's spans to an in-memory exporter for the
// rest of the test.
func captureSpans(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	telemetry.SetGlobalTracerProvider(tp)
	t.Cleanup(func() { telemetry.SetGlobalTracerProvider(nil) })
	return exporter
}

// rootFallback is what the root span says about fallback: its event names
// and its fallback.attempts attribute (-1 when absent).
func rootFallback(t *testing.T, exp *tracetest.InMemoryExporter) (events map[string]bool, attempts int64) {
	t.Helper()
	events = map[string]bool{}
	attempts = -1
	for _, sp := range exp.GetSpans() {
		if sp.Name != "gateway.chat.completion" {
			continue
		}
		for _, ev := range sp.Events {
			events[ev.Name] = true
		}
		for _, kv := range sp.Attributes {
			if string(kv.Key) == "fallback.attempts" {
				attempts = kv.Value.AsInt64()
			}
		}
	}
	return events, attempts
}

func TestUnaryFallbackCountsEveryAttempt(t *testing.T) {
	exp := captureSpans(t)
	s := routeServer(t, validator.FallbackConfig{
		Factors: []validator.FallbackFactorConfig{{Name: "f", Priority: 1, Strategy: "priority", Models: models("m-a", "m-b")}},
	},
		&routeModel{name: "pp", model: "m-primary", fail: errors.New("upstream 503")},
		&routeModel{name: "pa", model: "m-a", fail: errors.New("upstream 503")},
		&routeModel{name: "pb", model: "m-b"},
	)
	var got collected
	if err := processChatCompletion(tenantCtx(), s, chatReq("m-primary", false), got.send); err != nil {
		t.Fatal(err)
	}
	if n := got.first.GetFallbackInfo().GetFallbackAttempts(); n != 2 {
		t.Fatalf("fallback attempts reported = %d, want 2 (one failed, one served)", n)
	}
	events, attempts := rootFallback(t, exp)
	if !events[attrs.EventFallbackStarted] || !events[attrs.EventFallbackSuccess] || attempts != 2 {
		t.Fatalf("root span events %v, fallback.attempts %d", events, attempts)
	}
}

func TestUnaryFallbackExhaustedIsRecorded(t *testing.T) {
	exp := captureSpans(t)
	s := routeServer(t, validator.FallbackConfig{
		Factors: []validator.FallbackFactorConfig{{Name: "f", Priority: 1, Strategy: "priority", Models: models("m-a")}},
	},
		&routeModel{name: "pp", model: "m-primary", fail: errors.New("upstream 503")},
		&routeModel{name: "pa", model: "m-a", fail: errors.New("upstream 503")},
	)
	var got collected
	if err := processChatCompletion(tenantCtx(), s, chatReq("m-primary", false), got.send); err == nil {
		t.Fatal("want the failure")
	}
	events, attempts := rootFallback(t, exp)
	if !events[attrs.EventFallbackExhausted] || attempts != 1 {
		t.Fatalf("root span events %v, fallback.attempts %d; want exhausted with 1 attempt", events, attempts)
	}
}

func TestNoFallbackStartedWithoutATarget(t *testing.T) {
	exp := captureSpans(t)
	s := routeServer(t, validator.FallbackConfig{}, &routeModel{name: "pp", model: "m-primary", fail: errors.New("upstream 503")})
	var got collected
	_ = processChatCompletion(tenantCtx(), s, chatReq("m-primary", false), got.send)
	if events, _ := rootFallback(t, exp); events[attrs.EventFallbackStarted] {
		t.Fatalf("fallback.started recorded with no fallback target: %v", events)
	}
}

func TestStreamFallbackIsReported(t *testing.T) {
	exp := captureSpans(t)
	s := routeServer(t, validator.FallbackConfig{
		Factors: []validator.FallbackFactorConfig{{Name: "rate_limit", Priority: 1, Strategy: "priority", Models: models("m-a")}},
	},
		&routeModel{name: "pp", model: "m-primary", fail: errors.New("upstream 503")},
		&routeModel{name: "pa", model: "m-a"},
	)
	var got collected
	if err := processChatCompletion(tenantCtx(), s, chatReq("m-primary", true), got.send); err != nil {
		t.Fatal(err)
	}
	fi := got.first.GetFallbackInfo()
	if !fi.GetFallbackUsed() || fi.GetRequestedModel() != "m-primary" || fi.GetActualModel() != "m-a" || fi.GetFallbackReason() != "rate_limit" || fi.GetFallbackAttempts() != 1 {
		t.Fatalf("first streamed message fallback info = %+v", fi)
	}
	events, attempts := rootFallback(t, exp)
	if !events[attrs.EventFallbackStarted] || !events[attrs.EventFallbackSuccess] || attempts != 1 {
		t.Fatalf("root span events %v, fallback.attempts %d", events, attempts)
	}
}
