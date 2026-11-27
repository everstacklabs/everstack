package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	grpcserver "github.com/everstacklabs/everstack/internal/api/grpc/server"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1/gatewayconnect"
)

// These tests post the exact request bodies the OpenAI Python and Node SDKs
// send for client.responses.create(...) through the real compat wrappers,
// the real request transform and the real grpc-gateway (proto-JSON
// unmarshal), to a Connect handler that records the CreateResponseRequest it
// receives.

// recordingResponses is a GatewayService whose CreateResponse records its
// request and streams an in-progress then a completed response.
type recordingResponses struct {
	gatewayconnect.UnimplementedGatewayServiceHandler
	mu   sync.Mutex
	reqs []*gatewaypb.CreateResponseRequest
}

func (g *recordingResponses) CreateResponse(_ context.Context, req *connect.Request[gatewaypb.CreateResponseRequest], stream *connect.ServerStream[gatewaypb.CreateResponseResponse]) error {
	g.mu.Lock()
	g.reqs = append(g.reqs, req.Msg)
	g.mu.Unlock()
	base := func(status string) *gatewaypb.CreateResponseResponse {
		return &gatewaypb.CreateResponseResponse{Id: "resp_1", Object: "response", CreatedAt: 1700000000, Status: status, Model: req.Msg.Model}
	}
	if err := stream.Send(base("in_progress")); err != nil {
		return err
	}
	done := base("completed")
	done.Output = []*gatewaypb.ResponseOutputItem{{
		Id: "msg_1", Type: "message", Status: "completed", Role: gatewaypb.Role_ROLE_ASSISTANT,
		Content: []*gatewaypb.ContentPart{{Type: "text", Data: &gatewaypb.ContentPart_Text{Text: "hello"}}},
	}}
	done.Usage = &gatewaypb.ResponseUsage{InputTokens: 3, OutputTokens: 1, TotalTokens: 4}
	return stream.Send(done)
}

func (g *recordingResponses) last(t *testing.T) *gatewaypb.CreateResponseRequest {
	t.Helper()
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.reqs) == 0 {
		t.Fatal("the gateway handler was never reached")
	}
	return g.reqs[len(g.reqs)-1]
}

// realGatewayChain serves the recorder behind the production grpc-gateway
// mux (dialing it over gRPC/TLS) and the production request transform.
func realGatewayChain(t *testing.T) (v1, alias http.Handler, rec *recordingResponses) {
	t.Helper()
	rec = &recordingResponses{}
	path, handler := gatewayconnect.NewGatewayServiceHandler(rec)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	backend := httptest.NewUnstartedServer(mux)
	backend.EnableHTTP2 = true
	backend.StartTLS()
	t.Cleanup(backend.Close)

	u, _ := url.Parse(backend.URL)
	port, _ := strconv.Atoi(u.Port())
	roots := x509.NewCertPool()
	roots.AddCert(backend.Certificate())
	gwMux, err := grpcserver.NewGRPCJSONGateway(context.Background(), u.Hostname(), uint16(port), &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12})
	if err != nil {
		t.Fatal(err)
	}
	v1, alias = responsesChain(newKeyStore(), withOpenAICompatTransform(gwMux), true)
	return v1, alias, rec
}

// openAISDKHeaders are the headers both SDKs send that matter here.
func openAISDKHeaders() map[string]string {
	return map[string]string{"Authorization": "Bearer " + liveKey, "Accept": "application/json"}
}

func assertOneUserMessage(t *testing.T, req *gatewaypb.CreateResponseRequest, text string) {
	t.Helper()
	if len(req.Input) != 1 {
		t.Fatalf("input = %v, want one message", req.Input)
	}
	in := req.Input[0]
	if in.Type != "message" || in.Role != gatewaypb.Role_ROLE_USER || len(in.Content) != 1 ||
		in.Content[0].Type != "text" || in.Content[0].GetText() != text {
		t.Fatalf("input[0] = %v, want a user message %q", in, text)
	}
}

func TestResponsesAcceptsOpenAISDKRequests(t *testing.T) {
	v1, alias, rec := realGatewayChain(t)
	cases := []struct {
		name, body, instructions string
	}{
		{"string input", `{"input":"Hello","model":"gpt-5.5"}`, ""},
		{"string input with instructions", `{"input":"Hello","instructions":"Be brief.","model":"gpt-5.5"}`, "Be brief."},
		{"easy message array", `{"input":[{"role":"user","content":"Hello"}],"model":"gpt-5.5"}`, ""},
		{"typed message with input_text parts", `{"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"Hello"}]}],"instructions":"Be brief.","model":"gpt-5.5"}`, "Be brief."},
	}
	for _, tc := range cases {
		for _, ep := range []struct {
			name string
			h    http.Handler
			path string
		}{{"v1", v1, "/v1/responses"}, {"openai alias", alias, "/openai/v1/responses"}} {
			t.Run(tc.name+"/"+ep.name+"/unary", func(t *testing.T) {
				resp := doResponses(t, ep.h, ep.path, tc.body, openAISDKHeaders())
				assertOpenAIResponseObject(t, resp)
				req := rec.last(t)
				assertOneUserMessage(t, req, "Hello")
				if req.Instructions != tc.instructions || req.Model != "gpt-5.5" {
					t.Fatalf("instructions = %q model = %q", req.Instructions, req.Model)
				}
			})
			t.Run(tc.name+"/"+ep.name+"/stream", func(t *testing.T) {
				body := strings.TrimSuffix(tc.body, "}") + `,"stream":true}`
				resp := doResponses(t, ep.h, ep.path, body, openAISDKHeaders())
				if resp.Code != http.StatusOK || !strings.HasPrefix(resp.Header().Get("Content-Type"), "text/event-stream") {
					t.Fatalf("status %d content-type %q: %s", resp.Code, resp.Header().Get("Content-Type"), resp.Body.String())
				}
				out := resp.Body.String()
				for _, want := range []string{"event: response.created", `"delta":"hello"`, "event: response.completed"} {
					if !strings.Contains(out, want) {
						t.Fatalf("stream lacks %q:\n%s", want, out)
					}
				}
				req := rec.last(t)
				assertOneUserMessage(t, req, "Hello")
				if !req.Stream || req.Instructions != tc.instructions {
					t.Fatalf("stream = %v instructions = %q", req.Stream, req.Instructions)
				}
			})
		}
	}
}

// Every OpenAI input role maps to a proto role; developer is a system
// message. Assistant turns written as output_text replay as text.
func TestResponsesNormalisesOpenAIRolesAndParts(t *testing.T) {
	v1, _, rec := realGatewayChain(t)
	body := `{"model":"gpt-5.5","input":[` +
		`{"role":"developer","content":"Rules."},` +
		`{"role":"system","content":"Sys."},` +
		`{"role":"user","content":[{"type":"input_text","text":"Q"}]},` +
		`{"type":"message","role":"assistant","content":[{"type":"output_text","text":"A","annotations":[]}]}]}`
	assertOpenAIResponseObject(t, doResponses(t, v1, "/v1/responses", body, openAISDKHeaders()))
	req := rec.last(t)
	want := []struct {
		role gatewaypb.Role
		text string
	}{{gatewaypb.Role_ROLE_SYSTEM, "Rules."}, {gatewaypb.Role_ROLE_SYSTEM, "Sys."}, {gatewaypb.Role_ROLE_USER, "Q"}, {gatewaypb.Role_ROLE_ASSISTANT, "A"}}
	if len(req.Input) != len(want) {
		t.Fatalf("input = %v", req.Input)
	}
	for i, w := range want {
		in := req.Input[i]
		if in.Type != "message" || in.Role != w.role || len(in.Content) != 1 || in.Content[0].Type != "text" || in.Content[0].GetText() != w.text {
			t.Fatalf("input[%d] = %v, want %v %q", i, in, w.role, w.text)
		}
	}
}

// Native callers that already send the proto shape are untouched.
func TestResponsesNativeProtoInputUnchanged(t *testing.T) {
	v1, _, rec := realGatewayChain(t)
	body := `{"model":"gpt-5.5","input":[{"type":"message","role":"ROLE_USER","content":[{"type":"text","text":"Hello"}]}]}`
	resp := doResponses(t, v1, "/v1/responses", body, map[string]string{"x-evs-api-key": liveKey})
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
	}
	assertOneUserMessage(t, rec.last(t), "Hello")
}
