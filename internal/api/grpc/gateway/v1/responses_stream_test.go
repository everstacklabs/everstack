package v1

// End to end through the chat-backed Responses fallback with stream:true:
// the real Connect handler, grpc-gateway dialing it over gRPC (as
// production does), and the SSE negotiator in its openai-responses format.
// The provider is billing-wrapped so the tests see the metering seam too.

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	grpcserver "github.com/everstacklabs/everstack/internal/api/grpc/server"
	httpmw "github.com/everstacklabs/everstack/internal/api/http/middleware"
	"github.com/everstacklabs/everstack/internal/billingmeter"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/billing"
	"github.com/everstacklabs/everstack/internal/services/catalog"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	gatewayconnect "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1/gatewayconnect"
)

const streamModelName = "m-stream"

const streamCatalog = `
providers:
  ps: {name: ps, models: [{name: m-stream, input_cost_per_1k: 0.01, output_cost_per_1k: 0.03, capabilities: [chat], status: available}]}
`

// scriptedStream plays one scripted chat stream per ChatStream call. With
// holdAfter > 0, the first stream stops after that many chunks until hold
// is closed (an upstream still generating) and reports a cancellation it
// sees meanwhile on cancelled.
type scriptedStream struct {
	turns     [][]gw.ChatResponseChunk
	holdAfter int
	hold      chan struct{}
	held      chan struct{}
	cancelled chan struct{}
	// fail ends the last scripted stream with this error instead of nil.
	fail error
	// holdFirst holds the first stream before its first chunk, like
	// holdAfter does after one.
	holdFirst bool
	// unaryErr fails every Chat call.
	unaryErr error
	// unary scripts the Chat calls in order; past its end Chat answers
	// "Hello world".
	unary []gw.ChatCompletionResponse

	mu          sync.Mutex
	streamCalls int
	chatCalls   int
	requests    []gw.ChatCompletionRequest
}

func (p *scriptedStream) Name() string                { return "ps" }
func (p *scriptedStream) SupportsModel(m string) bool { return m == streamModelName }
func (p *scriptedStream) Embed(context.Context, gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	return gw.EmbeddingsResponse{}, errors.New("not used")
}

func (p *scriptedStream) Chat(_ context.Context, r gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	p.mu.Lock()
	call := p.chatCalls
	p.chatCalls++
	p.requests = append(p.requests, r)
	p.mu.Unlock()
	if p.unaryErr != nil {
		return gw.ChatCompletionResponse{}, p.unaryErr
	}
	if call < len(p.unary) {
		return p.unary[call], nil
	}
	return gw.ChatCompletionResponse{ID: "x", Model: r.Model, Choices: []gw.Choice{{Message: gw.NewMessage(gw.RoleAssistant, gw.Text("Hello world")), FinishReason: "stop"}},
		Usage: gw.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10}}, nil
}

func (p *scriptedStream) ChatStream(ctx context.Context, r gw.ChatCompletionRequest, on func(gw.ChatResponseChunk) error) error {
	p.mu.Lock()
	turn := p.streamCalls
	p.streamCalls++
	p.requests = append(p.requests, r)
	p.mu.Unlock()
	if turn >= len(p.turns) {
		return errors.New("unscripted turn")
	}
	if turn == 0 && p.holdFirst {
		close(p.held)
		select {
		case <-p.hold:
		case <-ctx.Done():
			close(p.cancelled)
			return ctx.Err()
		}
	}
	for i, c := range p.turns[turn] {
		if err := on(c); err != nil {
			return err
		}
		if turn == 0 && p.holdAfter > 0 && i+1 == p.holdAfter {
			close(p.held)
			select {
			case <-p.hold:
			case <-ctx.Done():
				close(p.cancelled)
				return ctx.Err()
			}
		}
	}
	if p.fail != nil && turn == len(p.turns)-1 {
		return p.fail
	}
	return nil
}

func (p *scriptedStream) counts() (stream, chat int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.streamCalls, p.chatCalls
}

func textChunk(s string) gw.ChatResponseChunk {
	return gw.ChatResponseChunk{ID: "c", Model: streamModelName, Choices: []gw.ChoiceDelta{{Delta: gw.NewMessage(gw.RoleAssistant, gw.Text(s))}}}
}

func toolChunk(id, name, args string) gw.ChatResponseChunk {
	return gw.ChatResponseChunk{ID: "c", Model: streamModelName, Choices: []gw.ChoiceDelta{{Delta: gw.Message{Role: gw.RoleAssistant,
		ToolCalls: []gw.ToolCall{{ID: id, Type: "function", Function: gw.ToolCallFunction{Name: name, Arguments: args}}}}}}}
}

func finishChunk(reason string, in, out int) gw.ChatResponseChunk {
	return gw.ChatResponseChunk{ID: "c", Model: streamModelName, Choices: []gw.ChoiceDelta{{FinishReason: reason}},
		Usage: &gw.Usage{PromptTokens: in, CompletionTokens: out, TotalTokens: in + out}}
}

// countingMonitor records the handler's license-side usage records.
type countingMonitor struct {
	enterprise.LicenseMonitor
	mu    sync.Mutex
	calls []enterprise.RequestMetrics
}

func (m *countingMonitor) RecordRequestWithMetrics(r enterprise.RequestMetrics) error {
	m.mu.Lock()
	m.calls = append(m.calls, r)
	m.mu.Unlock()
	return nil
}

func (m *countingMonitor) records() []enterprise.RequestMetrics {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]enterprise.RequestMetrics(nil), m.calls...)
}

type streamHarness struct {
	url  string
	sink *attemptSink
	mon  *countingMonitor
	// backend is the Connect handler itself, below the REST gateway.
	backend *httptest.Server
	// inflight counts running handlers: Wait returns once every request
	// has finished, metering included.
	inflight *sync.WaitGroup
}

func newStreamHarness(t *testing.T, p *scriptedStream) *streamHarness {
	t.Helper()
	prev := globalResponseStore
	globalResponseStore = newResponseStore()
	t.Cleanup(func() { globalResponseStore = prev })

	cat := catalog.NewCache()
	if err := cat.Load([]byte(streamCatalog), []byte("providers: {}\n")); err != nil {
		t.Fatal(err)
	}
	mon := &countingMonitor{LicenseMonitor: enterprise.LicenseMonitorFromContext(context.Background())}
	sink := newAttemptSink()
	ctx := context.WithValue(context.Background(), contextkeys.LicenseMonitor, enterprise.LicenseMonitor(mon))
	s := CreateServerWithContext(ctx, &validator.GatewayConfig{}, &validator.FeaturesConfig{})
	s.defaultBundle.Load().reg.Register(billing.Wrap(p, billing.Config{Meter: sink, Provenance: billingmeter.ProvenancePlatform, KeyID: "k", CatalogCache: cat}))

	path, handler := gatewayconnect.NewGatewayServiceHandler(s)
	mux := http.NewServeMux()
	// The test's cleanups (restoring the response store) wait for every
	// handler to return, including one still finishing after its client
	// disconnected.
	inflight := &sync.WaitGroup{}
	t.Cleanup(inflight.Wait)
	mux.Handle(path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		inflight.Add(1)
		defer inflight.Done()
		handler.ServeHTTP(w, r)
	}))
	backend := httptest.NewUnstartedServer(mux)
	backend.EnableHTTP2 = true
	backend.StartTLS()
	t.Cleanup(backend.Close)

	// The production REST gateway (marshaler options included), dialing
	// the Connect handler over gRPC.
	u, _ := url.Parse(backend.URL)
	port, _ := strconv.Atoi(u.Port())
	roots := x509.NewCertPool()
	roots.AddCert(backend.Certificate())
	gwMux, err := grpcserver.NewGRPCJSONGateway(context.Background(), u.Hostname(), uint16(port), &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	front := httptest.NewServer(httpmw.WrapWithSSENegotiationWith(gwMux, func(r *http.Request) string {
		if h := r.Header.Get("X-SSE-Format"); h != "" {
			return strings.ToLower(h)
		}
		return "proto"
	}))
	t.Cleanup(front.Close)
	return &streamHarness{url: front.URL, sink: sink, mon: mon, backend: backend, inflight: inflight}
}

func responsesBody(stream bool) string {
	return fmt.Sprintf(`{"model":%q,"stream":%t,"input":[{"type":"message","role":"ROLE_USER","content":[{"type":"text","text":"hi"}]}]}`, streamModelName, stream)
}

// postStream starts a streaming request and returns a channel of its SSE
// events, closed at end of stream.
func (h *streamHarness) postStream(t *testing.T, ctx context.Context) <-chan sseEv {
	t.Helper()
	return h.postStreamBody(t, ctx, responsesBody(true))
}

// postStreamBody is postStream with a caller-supplied request body.
func (h *streamHarness) postStreamBody(t *testing.T, ctx context.Context, body string) <-chan sseEv {
	t.Helper()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, h.url+"/v1/responses", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-SSE-Format", httpmw.SSEFormatOpenAIResponses)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/event-stream") {
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("status %d content-type %q: %s", resp.StatusCode, resp.Header.Get("Content-Type"), b)
	}
	out := make(chan sseEv, 256)
	go func() {
		defer close(out)
		defer resp.Body.Close()
		br := bufio.NewReader(resp.Body)
		var ev sseEv
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case line == "":
				if ev.name != "" || ev.data != "" {
					out <- ev
					ev = sseEv{}
				}
			case strings.HasPrefix(line, "event: "):
				ev.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = strings.TrimPrefix(line, "data: ")
			}
		}
	}()
	return out
}

type sseEv struct{ name, data string }

func nextEvent(t *testing.T, evs <-chan sseEv, what string) sseEv {
	t.Helper()
	select {
	case ev, ok := <-evs:
		if !ok {
			t.Fatalf("stream ended while waiting for %s", what)
		}
		return ev
	case <-time.After(5 * time.Second):
		t.Fatalf("no event while waiting for %s: the stream is buffered", what)
	}
	return sseEv{}
}

func drain(t *testing.T, evs <-chan sseEv) []sseEv {
	t.Helper()
	var all []sseEv
	timeout := time.After(10 * time.Second)
	for {
		select {
		case ev, ok := <-evs:
			if !ok {
				return all
			}
			all = append(all, ev)
		case <-timeout:
			t.Fatalf("stream did not end; got %v", names(all))
		}
	}
}

func names(evs []sseEv) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.name
	}
	return out
}

// sdkAccumulator applies a Responses event stream the way the OpenAI SDKs
// do (openai-node ResponseStream, openai-python ResponseStreamState): the
// snapshot starts from response.created, items and parts are pushed by the
// *.added events, text and arguments grow by their deltas, and the stream
// is over at the terminal response.* event. It fails the test where the SDK
// would throw (a delta for an item or part it has not seen, a missing
// required field, a sequence number out of order).
type sdkAccumulator struct {
	t        *testing.T
	seq      int
	snapshot map[string]interface{}
	final    map[string]interface{}
	terminal string
}

func (a *sdkAccumulator) apply(ev sseEv) {
	t := a.t
	t.Helper()
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(ev.data), &m); err != nil {
		t.Fatalf("%s: data is not JSON: %s", ev.name, ev.data)
	}
	if m["type"] != ev.name {
		t.Fatalf("%s: type field %v", ev.name, m["type"])
	}
	if seq, ok := m["sequence_number"].(float64); !ok || int(seq) != a.seq {
		t.Fatalf("%s: sequence_number %v, want %d", ev.name, m["sequence_number"], a.seq)
	}
	a.seq++
	if a.terminal != "" {
		t.Fatalf("%s after terminal %s", ev.name, a.terminal)
	}
	need := func(keys ...string) {
		for _, k := range keys {
			if _, ok := m[k]; !ok {
				t.Fatalf("%s: missing required field %q in %s", ev.name, k, ev.data)
			}
		}
	}
	output := func() []interface{} {
		o, _ := a.snapshot["output"].([]interface{})
		return o
	}
	item := func() map[string]interface{} {
		idx := int(m["output_index"].(float64))
		o := output()
		if idx >= len(o) {
			t.Fatalf("%s: missing output at index %d", ev.name, idx)
		}
		return o[idx].(map[string]interface{})
	}
	part := func() map[string]interface{} {
		it := item()
		parts, _ := it["content"].([]interface{})
		ci := int(m["content_index"].(float64))
		if ci >= len(parts) {
			t.Fatalf("%s: missing content at index %d", ev.name, ci)
		}
		return parts[ci].(map[string]interface{})
	}
	switch ev.name {
	case "response.created":
		need("response")
		a.snapshot = m["response"].(map[string]interface{})
		if a.snapshot["output"] == nil {
			a.snapshot["output"] = []interface{}{}
		}
	case "response.in_progress":
		need("response")
	case "response.output_item.added":
		need("output_index", "item")
		if int(m["output_index"].(float64)) != len(output()) {
			t.Fatalf("output_item.added at %v, snapshot has %d items", m["output_index"], len(output()))
		}
		a.snapshot["output"] = append(output(), m["item"])
	case "response.content_part.added":
		need("item_id", "output_index", "content_index", "part")
		it := item()
		parts, _ := it["content"].([]interface{})
		it["content"] = append(parts, m["part"])
	case "response.output_text.delta":
		need("item_id", "output_index", "content_index", "delta", "logprobs")
		p := part()
		if p["type"] != "output_text" {
			t.Fatalf("text delta into a %v part", p["type"])
		}
		p["text"] = p["text"].(string) + m["delta"].(string)
	case "response.function_call_arguments.delta":
		need("item_id", "output_index", "delta")
		it := item()
		args, _ := it["arguments"].(string)
		it["arguments"] = args + m["delta"].(string)
	case "response.output_text.done":
		need("item_id", "output_index", "content_index", "text")
		if got := part()["text"]; got != m["text"] {
			t.Fatalf("output_text.done text %q, accumulated %q", m["text"], got)
		}
	case "response.function_call_arguments.done":
		need("item_id", "output_index", "arguments")
		if got := item()["arguments"]; got != m["arguments"] {
			t.Fatalf("function_call_arguments.done %q, accumulated %q", m["arguments"], got)
		}
	case "response.content_part.done":
		need("item_id", "output_index", "content_index", "part")
	case "response.output_item.done":
		need("output_index", "item")
		o := output()
		o[int(m["output_index"].(float64))] = m["item"]
	case "response.completed", "response.failed", "response.incomplete":
		need("response")
		a.terminal = ev.name
		a.final = m["response"].(map[string]interface{})
	default:
		t.Fatalf("unexpected event %s: %s", ev.name, ev.data)
	}
}

func outputText(resp map[string]interface{}) string {
	var b strings.Builder
	o, _ := resp["output"].([]interface{})
	for _, raw := range o {
		it, _ := raw.(map[string]interface{})
		if it["type"] != "message" {
			continue
		}
		parts, _ := it["content"].([]interface{})
		for _, p := range parts {
			pm, _ := p.(map[string]interface{})
			if s, ok := pm["text"].(string); ok {
				b.WriteString(s)
			}
		}
	}
	return b.String()
}

// The core contract: text deltas reach the client while the upstream is
// still generating, in OpenAI's event order, and the request is metered and
// recorded exactly once at the end.
func TestResponsesFallbackStreamsDeltasBeforeUpstreamCompletes(t *testing.T) {
	p := &scriptedStream{
		turns: [][]gw.ChatResponseChunk{{
			textChunk("Hel"), textChunk("lo"), textChunk(" world"), finishChunk("stop", 7, 3),
		}},
		holdAfter: 1,
		hold:      make(chan struct{}),
		held:      make(chan struct{}),
		cancelled: make(chan struct{}),
	}
	h := newStreamHarness(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	evs := h.postStream(t, ctx)

	acc := &sdkAccumulator{t: t}
	var before []sseEv
	for {
		ev := nextEvent(t, evs, "the first text delta")
		before = append(before, ev)
		acc.apply(ev)
		if ev.name == "response.output_text.delta" {
			break
		}
	}
	// The provider signals that it is holding just after it hands over the
	// chunk, so the delta can reach this goroutine first. Wait for the signal
	// rather than sampling it.
	select {
	case <-p.held:
	case <-time.After(10 * time.Second):
		t.Fatal("first delta arrived but the provider is not holding")
	}
	select {
	case <-p.hold:
		t.Fatal("hold released early")
	default:
	}
	var d map[string]interface{}
	_ = json.Unmarshal([]byte(before[len(before)-1].data), &d)
	if d["delta"] != "Hel" {
		t.Fatalf("first delta = %v, want Hel", d["delta"])
	}

	close(p.hold)
	rest := drain(t, evs)
	for _, ev := range rest {
		acc.apply(ev)
	}
	all := append(before, rest...)

	want := []string{
		"response.created", "response.in_progress",
		"response.output_item.added", "response.content_part.added",
		"response.output_text.delta", "response.output_text.delta", "response.output_text.delta",
		"response.output_text.done", "response.content_part.done", "response.output_item.done",
		"response.completed",
	}
	if got := names(all); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("events:\n got %v\nwant %v", got, want)
	}
	if acc.terminal != "response.completed" {
		t.Fatalf("terminal = %s", acc.terminal)
	}
	if got := outputText(acc.snapshot); got != "Hello world" {
		t.Fatalf("SDK-accumulated text = %q", got)
	}
	if got := outputText(acc.final); got != "Hello world" {
		t.Fatalf("completed response text = %q", got)
	}
	usage, _ := acc.final["usage"].(map[string]interface{})
	if usage == nil || fmt.Sprint(usage["input_tokens"]) != "7" || fmt.Sprint(usage["output_tokens"]) != "3" {
		t.Fatalf("completed usage = %v", acc.final["usage"])
	}

	if sc, cc := p.counts(); sc != 1 || cc != 0 {
		t.Fatalf("provider calls: stream=%d chat=%d, want one stream and no unary call", sc, cc)
	}
	if !p.requests[0].Stream {
		t.Fatal("provider stream request is not marked stream")
	}
	h.sink.waitUnits(1)
	u := onlyUnitOf(t, h.sink)
	if u.Attempt == nil || u.Attempt.Role != billingmeter.RoleServed || u.Attempt.InputTokens != 7 || u.Attempt.OutputTokens != 3 || u.Attempt.Estimated || u.Micros <= 0 {
		t.Fatalf("settle unit: %+v attempt %+v", u, u.Attempt)
	}
	h.inflight.Wait()
	recs := h.mon.records()
	if len(recs) != 1 || recs[0].InputTokens != 7 || recs[0].OutputTokens != 3 {
		t.Fatalf("usage records = %+v, want exactly one of 7/3", recs)
	}
}

// Tool calls stream as function_call items whose arguments grow by delta:
// OpenAI sends the id and name first, then id-less argument fragments. With
// the server-side tool loop off, the caller runs the tool: the response
// completes with the function_call item, as OpenAI's does, and the model is
// not called again.
func TestResponsesFallbackStreamsFunctionCallArguments(t *testing.T) {
	p := &scriptedStream{
		turns: [][]gw.ChatResponseChunk{
			{toolChunk("call_1", "get_weather", ""), toolChunk("", "", `{"city":`), toolChunk("", "", `"Paris"}`), finishChunk("tool_calls", 5, 2)},
		},
	}
	h := newStreamHarness(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	all := drain(t, h.postStream(t, ctx))
	acc := &sdkAccumulator{t: t}
	for _, ev := range all {
		acc.apply(ev)
	}
	if acc.terminal != "response.completed" {
		t.Fatalf("terminal = %s, events %v, final %v", acc.terminal, names(all), acc.final)
	}
	var argDeltas []string
	for _, ev := range all {
		if ev.name == "response.function_call_arguments.delta" {
			var m map[string]interface{}
			_ = json.Unmarshal([]byte(ev.data), &m)
			argDeltas = append(argDeltas, m["delta"].(string))
		}
	}
	if strings.Join(argDeltas, "|") != `{"city":|"Paris"}` {
		t.Fatalf("argument deltas = %q", argDeltas)
	}
	fc := functionCallItem(acc.final)
	if fc == nil || fc["arguments"] != `{"city":"Paris"}` || fc["name"] != "get_weather" || fc["call_id"] != "call_1" || fc["status"] != "completed" {
		t.Fatalf("function_call item = %v", fc)
	}
	if sc, cc := p.counts(); sc != 1 || cc != 0 {
		t.Fatalf("provider calls: stream=%d chat=%d, want one stream turn", sc, cc)
	}
	h.sink.waitUnits(1)
	_ = onlyUnitOf(t, h.sink)
	if recs := h.mon.records(); len(recs) != 1 || recs[0].InputTokens != 5 || recs[0].OutputTokens != 2 {
		t.Fatalf("usage records = %+v, want exactly one of 5/2", recs)
	}
}

// The unary path stops the same way: a turn that ends in tool calls is the
// answer when the tool loop is off.
func TestResponsesFallbackUnaryReturnsFunctionCalls(t *testing.T) {
	p := &scriptedStream{unary: []gw.ChatCompletionResponse{{
		ID: "x", Model: streamModelName,
		Choices: []gw.Choice{{
			Message: gw.Message{Role: gw.RoleAssistant, ToolCalls: []gw.ToolCall{{ID: "call_1", Type: "function",
				Function: gw.ToolCallFunction{Name: "get_weather", Arguments: `{"city":"Paris"}`}}}},
			FinishReason: "tool_calls",
		}},
		Usage: gw.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7},
	}}}
	h := newStreamHarness(t, p)
	resp, err := http.Post(h.url+"/v1/responses", "application/json", strings.NewReader(responsesBody(false)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unary: %d %s", resp.StatusCode, body)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	var last struct {
		Result map[string]interface{} `json:"result"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatalf("last frame: %v: %s", err, body)
	}
	if last.Result["status"] != "completed" {
		t.Fatalf("final status = %v: %s", last.Result["status"], body)
	}
	fc := functionCallItem(last.Result)
	if fc == nil || fc["arguments"] != `{"city":"Paris"}` || fc["name"] != "get_weather" || fc["call_id"] != "call_1" {
		t.Fatalf("function_call item = %v", fc)
	}
	// A turn with tool calls and no text has no message item, as OpenAI's
	// output does: just the function_call.
	if out, _ := last.Result["output"].([]interface{}); len(out) != 1 {
		t.Fatalf("output = %v, want only the function_call item", out)
	}
	if sc, cc := p.counts(); sc != 0 || cc != 1 {
		t.Fatalf("provider calls: stream=%d chat=%d, want one unary turn", sc, cc)
	}
	if recs := h.mon.records(); len(recs) != 1 {
		t.Fatalf("usage records = %d", len(recs))
	}
}

func functionCallItem(resp map[string]interface{}) map[string]interface{} {
	out, _ := resp["output"].([]interface{})
	for _, raw := range out {
		if it, _ := raw.(map[string]interface{}); it["type"] == "function_call" {
			return it
		}
	}
	return nil
}

// A client that goes away mid-stream cancels the upstream generation.
func TestResponsesFallbackStreamDisconnectCancelsUpstream(t *testing.T) {
	p := &scriptedStream{
		turns:     [][]gw.ChatResponseChunk{{textChunk("Hel"), textChunk("lo"), finishChunk("stop", 7, 3)}},
		holdAfter: 1,
		hold:      make(chan struct{}),
		held:      make(chan struct{}),
		cancelled: make(chan struct{}),
	}
	h := newStreamHarness(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	evs := h.postStream(t, ctx)
	for {
		if ev := nextEvent(t, evs, "the first text delta"); ev.name == "response.output_text.delta" {
			break
		}
	}
	cancel()
	select {
	case <-p.cancelled:
	case <-time.After(5 * time.Second):
		close(p.hold)
		t.Fatal("upstream was not cancelled after the client disconnected")
	}
	// The aborted attempt is settled once, on its estimate, and the
	// license-side usage record carries the same tokens the wallet was
	// debited for.
	h.sink.waitUnits(1)
	h.inflight.Wait()
	assertAbortedTurnRecordedOnce(t, h)
}

// assertAbortedTurnRecordedOnce checks an aborted turn that produced text:
// exactly one attempt, charged on its estimate, and exactly one license-side
// usage record of the same tokens.
func assertAbortedTurnRecordedOnce(t *testing.T, h *streamHarness) {
	t.Helper()
	u := onlyUnitOf(t, h.sink)
	if u.Attempt == nil || !u.Attempt.Estimated || u.Attempt.OutputTokens <= 0 || u.Micros <= 0 {
		t.Fatalf("aborted attempt: %+v attempt %+v", u, u.Attempt)
	}
	recs := h.mon.records()
	if len(recs) != 1 || recs[0].InputTokens != u.Attempt.InputTokens || recs[0].OutputTokens != u.Attempt.OutputTokens {
		t.Fatalf("usage records = %+v, want exactly one of %d/%d (the debited estimate)", recs, u.Attempt.InputTokens, u.Attempt.OutputTokens)
	}
}

// A client that goes away before the first token still leaves its attempt
// on record (billing.inference_attempts): served, as everything it was sent,
// with no tokens and no charge. Nothing was generated, so there is no usage
// to record.
func TestResponsesFallbackStreamDisconnectBeforeFirstToken(t *testing.T) {
	p := &scriptedStream{
		turns:     [][]gw.ChatResponseChunk{{textChunk("Hel"), finishChunk("stop", 7, 1)}},
		holdFirst: true,
		hold:      make(chan struct{}),
		held:      make(chan struct{}),
		cancelled: make(chan struct{}),
	}
	h := newStreamHarness(t, p)
	ctx, cancel := context.WithCancel(context.Background())
	evs := h.postStream(t, ctx)
	for {
		if ev := nextEvent(t, evs, "response.in_progress"); ev.name == "response.in_progress" {
			break
		}
	}
	<-p.held
	cancel()
	select {
	case <-p.cancelled:
	case <-time.After(5 * time.Second):
		close(p.hold)
		t.Fatal("upstream was not cancelled after the client disconnected")
	}
	h.sink.waitUnits(1)
	h.inflight.Wait()
	u := onlyUnitOf(t, h.sink)
	if u.Attempt == nil || u.Attempt.Role != billingmeter.RoleServed || u.Attempt.InputTokens != 0 || u.Attempt.OutputTokens != 0 || u.Micros != 0 {
		t.Fatalf("attempt before the first token: %+v attempt %+v", u, u.Attempt)
	}
	if recs := h.mon.records(); len(recs) != 0 {
		t.Fatalf("usage records = %+v, want none", recs)
	}
}

// A unary turn the provider fails is on record too, as interrupted: nothing
// is configured to take over on this path.
func TestResponsesFallbackUnaryFailureRecordsAttempt(t *testing.T) {
	p := &scriptedStream{unaryErr: errors.New("upstream 500")}
	h := newStreamHarness(t, p)
	resp, err := http.Post(h.url+"/v1/responses", "application/json", strings.NewReader(responsesBody(false)))
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	h.sink.waitUnits(1)
	u := onlyUnitOf(t, h.sink)
	if u.Attempt == nil || u.Attempt.Role != billingmeter.RoleInterrupted || u.Micros != 0 {
		t.Fatalf("failed unary attempt: %+v attempt %+v", u, u.Attempt)
	}
}

// An upstream error after some text ends the stream with response.failed.
func TestResponsesFallbackStreamMidStreamErrorFails(t *testing.T) {
	p := &scriptedStream{
		turns: [][]gw.ChatResponseChunk{{textChunk("Hel")}},
		fail:  errors.New("upstream 503: connection reset"),
	}
	h := newStreamHarness(t, p)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	all := drain(t, h.postStream(t, ctx))
	acc := &sdkAccumulator{t: t}
	for _, ev := range all {
		acc.apply(ev)
	}
	if acc.terminal != "response.failed" || all[len(all)-1].name != "response.failed" {
		t.Fatalf("events = %v", names(all))
	}
	errObj, _ := acc.final["error"].(map[string]interface{})
	if errObj == nil || !strings.Contains(fmt.Sprint(errObj["message"]), "connection reset") {
		t.Fatalf("failed response error = %v", acc.final["error"])
	}
	// The tokens generated before the failure were paid for upstream and
	// debited on the estimate; the usage record matches.
	h.sink.waitUnits(1)
	h.inflight.Wait()
	assertAbortedTurnRecordedOnce(t, h)
}

// Non-streaming requests keep the unary chat call and one final response.
func TestResponsesFallbackUnaryUnchanged(t *testing.T) {
	p := &scriptedStream{}
	h := newStreamHarness(t, p)
	resp, err := http.Post(h.url+"/v1/responses", "application/json", strings.NewReader(responsesBody(false)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"status":"completed"`) || !strings.Contains(string(body), "Hello world") {
		t.Fatalf("unary: %d %s", resp.StatusCode, body)
	}
	if strings.Count(string(body), `"result"`) != 2 {
		t.Fatalf("unary frames: want in_progress + completed, got %s", body)
	}
	if sc, cc := p.counts(); sc != 0 || cc != 1 {
		t.Fatalf("provider calls: stream=%d chat=%d", sc, cc)
	}
	if recs := h.mon.records(); len(recs) != 1 {
		t.Fatalf("usage records = %d", len(recs))
	}
}

func onlyUnitOf(t *testing.T, s *attemptSink) billingmeter.SettleUnit {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.units) != 1 {
		t.Fatalf("settle units = %d, want 1", len(s.units))
	}
	return s.units[0]
}

// internalStreamBytes streams a reply of n equal text chunks through the
// CreateResponse RPC itself (a gRPC client on the Connect handler, the hop
// the REST gateway makes) and returns the total size of the messages, with
// the text the frames carry reassembled the way the SSE layer does.
func internalStreamBytes(t *testing.T, n int) (total int, text string) {
	t.Helper()
	chunks := make([]gw.ChatResponseChunk, 0, n+1)
	for i := 0; i < n; i++ {
		chunks = append(chunks, textChunk("word "))
	}
	chunks = append(chunks, finishChunk("stop", 7, n))
	h := newStreamHarness(t, &scriptedStream{turns: [][]gw.ChatResponseChunk{chunks}})
	client := gatewayconnect.NewGatewayServiceClient(h.backend.Client(), h.backend.URL, connect.WithGRPC())
	stream, err := client.CreateResponse(context.Background(), connect.NewRequest(&gatewaypb.CreateResponseRequest{
		Model: streamModelName, Stream: true,
		Input: []*gatewaypb.ResponseInput{{Type: "message", Role: gatewaypb.Role_ROLE_USER,
			Content: []*gatewaypb.ContentPart{{Type: "text", Data: &gatewaypb.ContentPart_Text{Text: "hi"}}}}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	var b strings.Builder
	var final *gatewaypb.CreateResponseResponse
	for stream.Receive() {
		msg := stream.Msg()
		total += proto.Size(msg)
		if d := msg.GetDelta(); d != nil {
			b.WriteString(d.GetContentDelta())
			continue
		}
		final = msg
		// A snapshot restates the text so far; resume after it.
		if out := msg.GetOutput(); len(out) > 0 && len(out[0].GetContent()) > 0 {
			b.Reset()
			b.WriteString(out[0].GetContent()[0].GetText())
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	if final.GetStatus() != "completed" {
		t.Fatalf("final frame status %q", final.GetStatus())
	}
	return total, b.String()
}

// Internal traffic is linear in the reply: each chunk adds its own fragment,
// not a restatement of everything before it. Quadrupling the chunks must
// roughly quadruple the bytes (a full snapshot per chunk would multiply them
// by sixteen).
func TestResponsesFallbackStreamInternalBytesAreLinear(t *testing.T) {
	small, smallText := internalStreamBytes(t, 100)
	large, largeText := internalStreamBytes(t, 400)
	if smallText != strings.Repeat("word ", 100) || largeText != strings.Repeat("word ", 400) {
		t.Fatalf("reassembled text wrong: %d and %d bytes", len(smallText), len(largeText))
	}
	if ratio := float64(large) / float64(small); ratio > 5 {
		t.Fatalf("internal bytes: %d for 100 chunks, %d for 400 (x%.1f): not linear", small, large, ratio)
	}
	// And in absolute terms: the 5-byte fragment, its ids and indexes, and
	// its share of the one final snapshot (about 70 bytes today).
	if perChunk := large / 400; perChunk > 96 {
		t.Fatalf("internal bytes per chunk = %d", perChunk)
	}
}
