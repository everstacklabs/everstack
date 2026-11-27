package v1

// What the Responses store keeps, and for how long: store:false keeps
// nothing, stored responses expire, oversized conversations are not kept,
// and history truncation never splits a tool call from its result.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"google.golang.org/protobuf/proto"
)

// fakeClock is a settable clock safe to read from handler goroutines.
type fakeClock struct{ ns atomic.Int64 }

func newFakeClock() *fakeClock {
	c := &fakeClock{}
	c.ns.Store(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	return c
}

func (c *fakeClock) now() time.Time            { return time.Unix(0, c.ns.Load()).UTC() }
func (c *fakeClock) advance(d time.Duration)   { c.ns.Add(int64(d)) }
func useClock(rs *responseStore, c *fakeClock) { rs.now = c.now }

// storedEntries counts every entry the store holds for any owner, tombstones
// included.
func storedEntries(rs *responseStore) int {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	n := 0
	for _, bucket := range rs.responses {
		n += len(bucket)
	}
	return n
}

// postStatus posts a body and returns the HTTP status and body.
func (h *streamHarness) postStatus(t *testing.T, body string) (int, string) {
	t.Helper()
	resp, err := http.Post(h.url+"/v1/responses", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

func (h *streamHarness) getStatus(t *testing.T, id string) int {
	t.Helper()
	resp, err := http.Get(h.url + "/v1/responses/" + id)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	_, _ = io.ReadAll(resp.Body)
	return resp.StatusCode
}

func TestConvertProtoStorePresence(t *testing.T) {
	for _, tc := range []struct {
		name             string
		store            *bool
		configured, want bool
	}{
		{"unset", nil, false, false},
		{"false", proto.Bool(false), true, false},
		{"true", proto.Bool(true), true, true},
	} {
		got := convertProtoToResponseRequest(&gatewaypb.CreateResponseRequest{Model: "m", Store: tc.store})
		if got.StoreConfigured != tc.configured || got.Store != tc.want {
			t.Fatalf("%s: Store=%v StoreConfigured=%v", tc.name, got.Store, got.StoreConfigured)
		}
	}
	// Unset means stored, as OpenAI's default store=true.
	if !shouldStoreResponse(gw.CreateResponseRequest{}) {
		t.Fatal("unset store must default to storing")
	}
	if shouldStoreResponse(gw.CreateResponseRequest{Store: false, StoreConfigured: true}) {
		t.Fatal("store:false must not store")
	}
}

// store:false keeps nothing: not the response, not its conversation. A
// later request chained to it gets the same not-found as an unknown id, and
// the model is not called for it.
func TestResponsesStoreFalsePersistsNothing(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			p := &scriptedStream{
				unary: []gw.ChatCompletionResponse{toolCallTurn()},
				turns: [][]gw.ChatResponseChunk{{toolChunk("call_1", "get_weather", `{"city":"Paris"}`), finishChunk("stop", 5, 2)}},
			}
			h := newStreamHarness(t, p)
			body := fmt.Sprintf(`{"model":%q,"stream":%t,"store":false,"input":[{"type":"message","role":"ROLE_USER","content":[{"type":"text","text":"hi"}]}]}`, streamModelName, stream)
			var id string
			if stream {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				acc := &sdkAccumulator{t: t}
				for _, ev := range drain(t, h.postStreamBody(t, ctx, body)) {
					acc.apply(ev)
				}
				if acc.terminal != "response.completed" {
					t.Fatalf("terminal = %s", acc.terminal)
				}
				id, _ = acc.final["id"].(string)
			} else {
				first := h.postUnary(t, body)
				if first["status"] != "completed" {
					t.Fatalf("status = %v", first["status"])
				}
				id, _ = first["id"].(string)
			}
			if id == "" {
				t.Fatal("no response id")
			}
			h.inflight.Wait()
			if n := storedEntries(globalResponseStore); n != 0 {
				t.Fatalf("store:false left %d stored entries", n)
			}
			if code := h.getStatus(t, id); code != http.StatusNotFound {
				t.Fatalf("GET of a store:false response = %d, want 404", code)
			}
			calls := len(p.requests)
			code, raw := h.postStatus(t, toolResultBody(id, false))
			if code != http.StatusNotFound || !strings.Contains(raw, "previous response not found") {
				t.Fatalf("chain to a store:false response = %d %s, want 404 previous response not found", code, raw)
			}
			if len(p.requests) != calls {
				t.Fatal("the model was called for a chain to an unstored response")
			}
		})
	}
}

// An explicit store:true stores, like the unset default.
func TestResponsesStoreTrueStores(t *testing.T) {
	p := &scriptedStream{unary: []gw.ChatCompletionResponse{toolCallTurn()}}
	h := newStreamHarness(t, p)
	first := h.postUnary(t, fmt.Sprintf(`{"model":%q,"store":true,"input":[{"type":"message","role":"ROLE_USER","content":[{"type":"text","text":"hi"}]}]}`, streamModelName))
	id, _ := first["id"].(string)
	if code := h.getStatus(t, id); code != http.StatusOK {
		t.Fatalf("GET of a stored response = %d", code)
	}
}

// A stored response and its conversation expire responseStoreTTL after
// they were last written: reads miss, a chain gets not-found, and the
// entry is swept from memory.
func TestResponsesStoreExpiresAfterTTL(t *testing.T) {
	p := &scriptedStream{unary: []gw.ChatCompletionResponse{toolCallTurn(), sunnyTurn()}}
	h := newStreamHarness(t, p)
	clock := newFakeClock()
	useClock(globalResponseStore, clock)

	first := h.postUnary(t, responsesBody(false))
	id, _ := first["id"].(string)
	h.inflight.Wait()

	clock.advance(responseStoreTTL - time.Second)
	if code := h.getStatus(t, id); code != http.StatusOK {
		t.Fatalf("GET just before the TTL = %d, want 200", code)
	}

	clock.advance(2 * time.Second)
	if code := h.getStatus(t, id); code != http.StatusNotFound {
		t.Fatalf("GET after the TTL = %d, want 404", code)
	}
	code, raw := h.postStatus(t, toolResultBody(id, false))
	if code != http.StatusNotFound || !strings.Contains(raw, "previous response not found") {
		t.Fatalf("chain after the TTL = %d %s", code, raw)
	}

	// A later write sweeps the expired entry out of memory.
	s := &Server{ctx: context.Background()}
	s.storeResponse(context.Background(), "resp_fresh", &gw.CreateResponseResponse{ID: "resp_fresh"})
	if n := storedEntries(globalResponseStore); n != 1 {
		t.Fatalf("entries after sweep = %d, want only the fresh one", n)
	}
}

// setStoreCaps lowers the storage caps for one test.
func setStoreCaps(t *testing.T, toolOutput, conversation int) {
	t.Helper()
	prevTool, prevConv := maxStoredToolOutputBytes, maxStoredConversationBytes
	maxStoredToolOutputBytes, maxStoredConversationBytes = toolOutput, conversation
	t.Cleanup(func() { maxStoredToolOutputBytes, maxStoredConversationBytes = prevTool, prevConv })
}

// A tool output over the cap is not stored. The request that carried it
// still completes (its answer was already generated); a request chained to
// it gets a clear error naming the limit and what to do instead.
func TestResponsesStoreToolOutputCap(t *testing.T) {
	setStoreCaps(t, 64, 1<<20)
	p := &scriptedStream{unary: []gw.ChatCompletionResponse{sunnyTurn()}}
	h := newStreamHarness(t, p)

	big := strings.Repeat("x", 65)
	body := fmt.Sprintf(`{"model":%q,"input":[`+
		`{"type":"message","role":"ROLE_USER","content":[{"type":"text","text":"hi"}]},`+
		`{"type":"function_call","call_id":"call_1","name":"get_weather","arguments":"{}"},`+
		`{"type":"function_call_output","call_id":"call_1","output":%q}]}`, streamModelName, big)
	resp := h.postUnary(t, body)
	if resp["status"] != "completed" {
		t.Fatalf("request with an oversized tool output: status %v", resp["status"])
	}
	id, _ := resp["id"].(string)
	h.inflight.Wait()
	assertChainRefused(t, h, id, "tool output", "64")
}

// A conversation over the cap is not stored either, with the same clear
// error on the chain.
func TestResponsesStoreConversationCap(t *testing.T) {
	setStoreCaps(t, 1<<20, 256)
	p := &scriptedStream{unary: []gw.ChatCompletionResponse{sunnyTurn()}}
	h := newStreamHarness(t, p)

	body := fmt.Sprintf(`{"model":%q,"input":[{"type":"message","role":"ROLE_USER","content":[{"type":"text","text":%q}]}]}`,
		streamModelName, strings.Repeat("y", 300))
	resp := h.postUnary(t, body)
	if resp["status"] != "completed" {
		t.Fatalf("request with an oversized conversation: status %v", resp["status"])
	}
	id, _ := resp["id"].(string)
	h.inflight.Wait()
	assertChainRefused(t, h, id, "conversation", "256")
}

func assertChainRefused(t *testing.T, h *streamHarness, id string, what, limit string) {
	t.Helper()
	if code := h.getStatus(t, id); code != http.StatusNotFound {
		t.Fatalf("GET of an unstored oversized response = %d, want 404", code)
	}
	code, raw := h.postStatus(t, toolResultBody(id, false))
	if code != http.StatusBadRequest || !strings.Contains(raw, "was not stored") ||
		!strings.Contains(raw, what) || !strings.Contains(raw, limit) || !strings.Contains(raw, "input items") {
		t.Fatalf("chain to an oversized response = %d %s", code, raw)
	}
	// Only the refusal marker is kept, not the conversation.
	if n := storedEntries(globalResponseStore); n != 1 {
		t.Fatalf("entries = %d, want the one refusal marker", n)
	}
}

// The refusal is per owner: on the shared gateway another tenant chaining
// to the same id gets the ordinary not-found.
func TestResponsesStoreRefusalIsTenantScoped(t *testing.T) {
	setStoreCaps(t, 1<<20, 16)
	s := newSharedResponsesServer(t)
	ctxA, ctxB := asTenant("tenant-a"), asTenant("tenant-b")
	s.storeConversation(ctxA, "resp_big", &gw.CreateResponseResponse{ID: "resp_big"},
		[]gw.Message{gw.NewMessage(gw.RoleUser, gw.Text(strings.Repeat("z", 64)))})
	if err := s.authorizePreviousResponse(ctxA, "resp_big"); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("tenant A chain = %v, want the refusal", err)
	}
	errB := s.authorizePreviousResponse(ctxB, "resp_big")
	missing := s.authorizePreviousResponse(ctxB, "resp_nope")
	if connect.CodeOf(errB) != connect.CodeNotFound || errB.Error() != missing.Error() {
		t.Fatalf("tenant B chain = %v, want %v", errB, missing)
	}
}

func roles(msgs []gw.Message) string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		r := string(m.Role)
		if len(m.ToolCalls) > 0 {
			r += "(calls)"
		}
		out[i] = r
	}
	return strings.Join(out, ",")
}

// assertNoSplitToolGroups fails if a tool message has no assistant
// tool_calls message before it in its group, or an assistant tool_calls
// message is missing any of its results.
func assertNoSplitToolGroups(t *testing.T, msgs []gw.Message) {
	t.Helper()
	pending := map[string]bool{}
	for i, m := range msgs {
		switch {
		case m.Role == gw.RoleTool:
			if !pending[m.ToolCallID] {
				t.Fatalf("tool message %d (%s) has no tool call before it: %s", i, m.ToolCallID, roles(msgs))
			}
			delete(pending, m.ToolCallID)
		default:
			if len(pending) > 0 {
				t.Fatalf("tool calls %v have no results before message %d: %s", pending, i, roles(msgs))
			}
			for _, tc := range m.ToolCalls {
				pending[tc.ID] = true
			}
		}
	}
	if len(pending) > 0 {
		t.Fatalf("tool calls %v have no results: %s", pending, roles(msgs))
	}
}

func toolExchange() []gw.Message {
	return []gw.Message{
		gw.NewMessage(gw.RoleSystem, gw.Text("be brief")),
		gw.NewMessage(gw.RoleUser, gw.Text("weather in Paris and Rome?")),
		{Role: gw.RoleAssistant, ToolCalls: []gw.ToolCall{{ID: "a", Type: "function"}, {ID: "b", Type: "function"}}},
		{Role: gw.RoleTool, ToolCallID: "a", Content: []gw.ContentPart{gw.Text("sunny")}},
		{Role: gw.RoleTool, ToolCallID: "b", Content: []gw.ContentPart{gw.Text("rain")}},
		gw.NewMessage(gw.RoleAssistant, gw.Text("Paris sunny, Rome rain.")),
		gw.NewMessage(gw.RoleUser, gw.Text("thanks")),
	}
}

// The cap landing inside a call/result group drops the rest of that group
// rather than leaving tool results without their call. The system message
// is kept as before.
func TestTruncateChatHistoryKeepsToolGroupsWhole(t *testing.T) {
	msgs := toolExchange()
	for max := 1; max <= len(msgs); max++ {
		got := truncateChatHistory(msgs, max)
		assertNoSplitToolGroups(t, got)
		if got[0].Role != gw.RoleSystem {
			t.Fatalf("max %d: system message dropped: %s", max, roles(got))
		}
	}
	// Cap of 5 lands on tool result "a" (system + last 4): both results go.
	if got := roles(truncateChatHistory(msgs, 5)); got != "system,assistant,user" {
		t.Fatalf("max 5 = %s", got)
	}
	// Cap of 6 lands on the call itself: the group is kept whole.
	if got := roles(truncateChatHistory(msgs, 6)); got != "system,assistant(calls),tool,tool,assistant,user" {
		t.Fatalf("max 6 = %s", got)
	}

	// Without a system message.
	noSys := msgs[1:]
	for max := 1; max <= len(noSys); max++ {
		assertNoSplitToolGroups(t, truncateChatHistory(noSys, max))
	}
	if got := roles(truncateChatHistory(noSys, 4)); got != "assistant,user" {
		t.Fatalf("no system, max 4 = %s", got)
	}

	// When the conversation ends in the group the cap lands inside, dropping
	// it would leave nothing to answer, so the whole group is kept instead.
	ending := msgs[:5]
	if got := roles(truncateChatHistory(ending, 3)); got != "system,assistant(calls),tool,tool" {
		t.Fatalf("ending in a group, max 3 = %s", got)
	}
}
