package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/api/common"
	httpmw "github.com/everstacklabs/everstack/internal/api/http/middleware"
	"github.com/everstacklabs/everstack/internal/cqrs"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// These tests drive POST /v1/responses and /openai/v1/responses through the
// real compat wrappers, the real SSE negotiator and the real API-key
// interceptor, against a stub grpc-gateway that emits the CreateResponse
// server stream the way the real one does: one {"result":...} line per
// message, flushed as it is produced.

const (
	respFrameInProgress = `{"result":{"id":"resp_1","object":"response","created_at":"1700000000","status":"in_progress","model":"gpt-5.5"}}` + "\n"
	respFrameCompleted  = `{"result":{"id":"resp_1","object":"response","created_at":"1700000000","status":"completed","model":"gpt-5.5",` +
		`"output":[{"id":"msg_1","type":"message","status":"completed","role":3,"content":[{"type":"text","text":"hello"}]}],` +
		`"usage":{"input_tokens":3,"output_tokens":1,"total_tokens":4}}}` + "\n"
)

// blockingResponsesGateway writes the first CreateResponse frame, flushes,
// and then holds the stream open until release is closed, standing in for an
// upstream model that is still generating.
func blockingResponsesGateway(release <-chan struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, respFrameInProgress)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, respFrameCompleted)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	})
}

// responsesChain assembles the production order: compat wrapper, then the
// /v1 SSE negotiator, then the API-key interceptor, then the gateway. The
// negotiator's route selector answers "proto" the way an instance without
// an SSE route for /v1/responses does. withSSE=false leaves the negotiator
// out, which is what an instance with SSE disabled runs.
func responsesChain(store *keyStore, gateway http.Handler, withSSE bool) (v1, alias http.Handler) {
	var chain http.Handler = httpmw.NewAPIKeyInterceptor(false).WithAPIKeyValidation(gateway)
	if withSSE {
		chain = httpmw.WrapWithSSENegotiationWith(chain, func(r *http.Request) string {
			if h := r.Header.Get("X-SSE-Format"); h != "" {
				return strings.ToLower(h)
			}
			return "proto"
		})
	}
	inject := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			bus := query.NewQueryBus()
			bus.RegisterHandler(store)
			ctx := contextkeys.WithAPIKeyHashSecret(r.Context(), testHashSecret)
			ctx = cqrs.WithSystem(ctx, &cqrs.System{QueryBus: bus})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	return inject(withV1OpenAICompat(chain)), inject(withOpenAIRoundTrip(chain))
}

func newKeyStore() *keyStore {
	return &keyStore{live: map[string]bool{liveKey: true}}
}

type sseEvent struct {
	event string
	data  string
}

// readEvent reads one SSE event (event + data lines up to the blank line).
func readEvent(br *bufio.Reader) (sseEvent, error) {
	var ev sseEvent
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return ev, err
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			if ev.event != "" || ev.data != "" {
				return ev, nil
			}
		case strings.HasPrefix(line, "event: "):
			ev.event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			ev.data = strings.TrimPrefix(line, "data: ")
		}
	}
}

// The core contract: with stream:true the first event reaches the client
// while the upstream is still generating, as text/event-stream, and the rest
// follows once it finishes. Covers the documented /v1 base URL and the older
// /openai/v1 alias.
func TestResponsesStreamIsDeliveredIncrementally(t *testing.T) {
	for _, tc := range []struct {
		name, path string
		alias      bool
	}{
		{"v1", "/v1/responses", false},
		{"openai alias", "/openai/v1/responses", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := make(chan struct{})
			v1, alias := responsesChain(newKeyStore(), blockingResponsesGateway(release), true)
			h := v1
			if tc.alias {
				h = alias
			}
			srv := httptest.NewServer(h)
			defer srv.Close()
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+tc.path,
				strings.NewReader(`{"model":"gpt-5.5","input":"hi","stream":true}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+liveKey)

			// Headers must arrive while the upstream is still blocked.
			type result struct {
				resp *http.Response
				err  error
			}
			got := make(chan result, 1)
			go func() {
				resp, err := srv.Client().Do(req)
				got <- result{resp, err}
			}()
			var resp *http.Response
			select {
			case r := <-got:
				if r.err != nil {
					t.Fatalf("request: %v", r.err)
				}
				resp = r.resp
			case <-time.After(3 * time.Second):
				t.Fatal("no response headers while the upstream was still generating: the stream is buffered")
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				b, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d: %s", resp.StatusCode, b)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
				t.Fatalf("content type = %q, want text/event-stream", ct)
			}

			br := bufio.NewReader(resp.Body)
			first := make(chan sseEvent, 1)
			go func() {
				ev, err := readEvent(br)
				if err != nil {
					ev = sseEvent{event: "read error: " + err.Error()}
				}
				first <- ev
			}()
			var ev sseEvent
			select {
			case ev = <-first:
			case <-time.After(3 * time.Second):
				t.Fatal("no event delivered before the upstream finished: the stream is buffered")
			}
			if ev.event != "response.created" {
				t.Fatalf("first event = %q (%s), want response.created", ev.event, ev.data)
			}
			var created struct {
				Type           string `json:"type"`
				SequenceNumber int    `json:"sequence_number"`
				Response       struct {
					ID        string `json:"id"`
					Object    string `json:"object"`
					Status    string `json:"status"`
					CreatedAt int64  `json:"created_at"`
				} `json:"response"`
			}
			if err := json.Unmarshal([]byte(ev.data), &created); err != nil {
				t.Fatalf("response.created data is not JSON: %v: %s", err, ev.data)
			}
			if created.Type != "response.created" || created.Response.ID != "resp_1" ||
				created.Response.Object != "response" || created.Response.Status != "in_progress" ||
				created.Response.CreatedAt != 1700000000 {
				t.Fatalf("response.created payload = %s", ev.data)
			}

			// Let the upstream finish and read the rest of the stream.
			close(release)
			var events []sseEvent
			for {
				ev, err := readEvent(br)
				if err != nil {
					break
				}
				events = append(events, ev)
			}
			names := make([]string, 0, len(events))
			var delta, completed string
			for _, e := range events {
				names = append(names, e.event)
				switch e.event {
				case "response.output_text.delta":
					var d struct {
						Delta string `json:"delta"`
					}
					_ = json.Unmarshal([]byte(e.data), &d)
					delta += d.Delta
				case "response.completed":
					completed = e.data
				}
				if e.data == "[DONE]" {
					t.Fatalf("responses stream must not end with the chat [DONE] sentinel")
				}
			}
			if delta != "hello" {
				t.Fatalf("output_text deltas = %q, want hello; events %v", delta, names)
			}
			if completed == "" {
				t.Fatalf("no response.completed event; events %v", names)
			}
			var done struct {
				Response struct {
					Status string `json:"status"`
					Output []struct {
						Type    string `json:"type"`
						Role    string `json:"role"`
						Content []struct {
							Type string `json:"type"`
							Text string `json:"text"`
						} `json:"content"`
					} `json:"output"`
					Usage struct {
						InputTokens  int `json:"input_tokens"`
						OutputTokens int `json:"output_tokens"`
					} `json:"usage"`
				} `json:"response"`
			}
			if err := json.Unmarshal([]byte(completed), &done); err != nil {
				t.Fatalf("response.completed not JSON: %v", err)
			}
			if done.Response.Status != "completed" || len(done.Response.Output) != 1 ||
				done.Response.Output[0].Role != "assistant" || len(done.Response.Output[0].Content) != 1 ||
				done.Response.Output[0].Content[0].Type != "output_text" ||
				done.Response.Output[0].Content[0].Text != "hello" || done.Response.Usage.OutputTokens != 1 {
				t.Fatalf("response.completed payload = %s", completed)
			}
		})
	}
}

// fixedResponsesGateway answers the whole CreateResponse stream at once.
func fixedResponsesGateway() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, respFrameInProgress+respFrameCompleted)
	})
}

func assertOpenAIResponseObject(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type = %q", ct)
	}
	var parsed struct {
		Object string `json:"object"`
		Status string `json:"status"`
		Output []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("body is not a single OpenAI response object: %v\n%s", err, rec.Body.String())
	}
	if parsed.Object != "response" || parsed.Status != "completed" || len(parsed.Output) != 1 ||
		len(parsed.Output[0].Content) != 1 || parsed.Output[0].Content[0].Text != "hello" {
		t.Fatalf("not the final OpenAI response object: %s", rec.Body.String())
	}
}

func doResponses(t *testing.T, h http.Handler, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// Without stream:true an OpenAI client gets one response object, not the
// raw server-stream frames.
func TestResponsesNonStreamReturnsOneOpenAIObject(t *testing.T) {
	v1, alias := responsesChain(newKeyStore(), fixedResponsesGateway(), true)
	assertOpenAIResponseObject(t, doResponses(t, v1, "/v1/responses", `{"model":"gpt-5.5","input":"hi"}`, bearer(liveKey)))
	assertOpenAIResponseObject(t, doResponses(t, alias, "/openai/v1/responses", `{"model":"gpt-5.5","input":"hi"}`, bearer(liveKey)))
}

// An instance with SSE disabled cannot stream; stream:true then degrades to
// the single final response object, like chat does.
func TestResponsesStreamDegradesWhenSSEIsDisabled(t *testing.T) {
	v1, _ := responsesChain(newKeyStore(), fixedResponsesGateway(), false)
	assertOpenAIResponseObject(t, doResponses(t, v1, "/v1/responses", `{"model":"gpt-5.5","input":"hi","stream":true}`, bearer(liveKey)))
}

// A refused key on a streaming request answers in the same OpenAI error
// envelope as on a non-streaming one, not an empty event stream.
func TestResponsesStreamWithRefusedKeyIsAnOpenAIError(t *testing.T) {
	v1, alias := responsesChain(newKeyStore(), fixedResponsesGateway(), true)
	for _, tc := range []struct {
		h    http.Handler
		path string
	}{{v1, "/v1/responses"}, {alias, "/openai/v1/responses"}} {
		rec := doResponses(t, tc.h, tc.path, `{"model":"gpt-5.5","input":"hi","stream":true}`, bearer(revokedKey))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401: %s", tc.path, rec.Code, rec.Body.String())
		}
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Fatalf("%s: content type = %q", tc.path, ct)
		}
		var parsed struct {
			Error *struct {
				Message string `json:"message"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil || parsed.Error == nil || parsed.Error.Message == "" {
			t.Fatalf("%s: not an OpenAI error envelope: %s", tc.path, rec.Body.String())
		}
	}
}

// Native callers (x-evs-api-key, no bearer) keep the proto-format SSE they
// were built against: the compat reshaping keys off the bearer.
func TestResponsesNativeCallersKeepProtoStream(t *testing.T) {
	v1, _ := responsesChain(newKeyStore(), fixedResponsesGateway(), true)
	rec := doResponses(t, v1, "/v1/responses", `{"model":"gpt-5.5","input":"hi","stream":true}`,
		map[string]string{common.EverstackApiKey: liveKey})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data: {"result":{"id":"resp_1"`) || strings.Contains(body, "event: response.") {
		t.Fatalf("native stream was reshaped: %s", body)
	}
}

// The buffered answer (unary, or SSE disabled) is the last snapshot: a delta
// frame after it is not a response and must not replace it.
func TestResponsesBufferedSkipsDeltaFrames(t *testing.T) {
	body := []byte(respFrameInProgress + respFrameCompleted +
		`{"result":{"id":"resp_1","object":"response","delta":{"item_id":"msg_1","content_delta":"x"}}}` + "\n")
	out, ok := protoResponseStreamToOpenAI(body)
	if !ok || !strings.Contains(string(out), `"status":"completed"`) || !strings.Contains(string(out), `"hello"`) {
		t.Fatalf("buffered response = %s (ok=%v)", out, ok)
	}
}

// An OpenAI SDK sends a tool's result back as a function_call_output input
// item chained with previous_response_id. The compat layer must hand it to
// the gateway intact, both unary and streaming, so the grpc-gateway (proto
// names, unknown fields discarded) decodes call_id and output.
func TestResponsesToolResultInputReachesGateway(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			var got []byte
			gateway := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				got, _ = io.ReadAll(r.Body)
				fixedResponsesGateway().ServeHTTP(w, r)
			})
			v1, _ := responsesChain(newKeyStore(), gateway, true)
			body := fmt.Sprintf(`{"model":"gpt-5.5","stream":%t,"previous_response_id":"resp_prev",`+
				`"input":[{"type":"function_call_output","call_id":"call_1","output":"{\"temp_c\":21}"}]}`, stream)
			rec := doResponses(t, v1, "/v1/responses", body, bearer(liveKey))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
			}
			var req gatewaypb.CreateResponseRequest
			if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(got, &req); err != nil {
				t.Fatalf("gateway body does not decode: %v: %s", err, got)
			}
			if req.GetPreviousResponseId() != "resp_prev" || len(req.GetInput()) != 1 {
				t.Fatalf("gateway request = %s", got)
			}
			in := req.GetInput()[0]
			if in.GetType() != "function_call_output" || in.GetCallId() != "call_1" || in.GetOutput() != `{"temp_c":21}` {
				t.Fatalf("input item = %v (body %s)", in, got)
			}
		})
	}
}
