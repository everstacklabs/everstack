package v1

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/api/policy"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"github.com/everstacklabs/everstack/pkg/database"
	"github.com/everstacklabs/everstack/pkg/decision"
	pb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1/gatewayconnect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/protobuf/types/known/structpb"
)

func TestSystemOneRESTAllProviders(t *testing.T) {
	for _, name := range []string{"typesafe", "kev", "laya"} {
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1/systemone" || r.Header.Get("Authorization") != "Bearer own-key" {
					t.Error("wrong upstream request")
				}
				var got decision.Request
				_ = json.NewDecoder(r.Body).Decode(&got)
				if got.Model != "native" {
					t.Errorf("model=%s", got.Model)
				}
				_, _ = io.WriteString(w, `{"model":"native","answers":{"safe":{"type":"noul","noul":0}},"usage":{"input_tokens":3,"output_tokens":0}}`)
			}))
			defer upstream.Close()
			s := CreateServerWithContext(context.Background(), &validator.GatewayConfig{Models: []validator.ModelConfig{{Provider: name, Model: []string{"native"}, APIKey: "own-key", BaseURL: upstream.URL + "/v1"}}}, nil)
			mux := runtime.NewServeMux()
			if err := pb.RegisterGatewayServiceHandlerServer(context.Background(), mux, &GrpcServer{base: s}); err != nil {
				t.Fatal(err)
			}
			api := httptest.NewServer(mux)
			defer api.Close()
			response, err := http.Post(api.URL+"/v1/systemone", "application/json", strings.NewReader(`{"model":"@`+name+`/native","state":{"text":"hi"},"questions":{"safe":{"type":"noul"}}}`))
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, _ := io.ReadAll(response.Body)
			if response.StatusCode != 200 || !strings.Contains(string(body), `"noul":0`) {
				t.Fatalf("REST %d %s", response.StatusCode, body)
			}
		})
	}
	p := policy.NewDefaultPolicy()
	if !p.ShouldMeterRequest("/v1/systemone") {
		t.Fatal("decision requests are unmetered")
	}
}
func TestDecisionTenantIsolation(t *testing.T) {
	s := &Server{ctx: context.WithValue(context.Background(), contextkeys.SharedGatewayMode, true)}
	for _, tenant := range []string{"tenant_a", "tenant_b"} {
		tenant := tenant
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+tenant {
				t.Error("cross-tenant key")
			}
			_, _ = io.WriteString(w, `{"model":"native","answers":{"q":{"type":"noul","noul":0}},"usage":{}}`)
		}))
		defer upstream.Close()
		f, _ := factory.Default.Get("typesafe")
		provider, err := f(factory.AggregatedInput{APIKey: tenant, BaseURL: upstream.URL + "/v1", Models: []string{"native"}})
		if err != nil {
			t.Fatal(err)
		}
		reg := gw.NewRegistry()
		reg.Register(provider)
		s.installBundle(tenant, &tenantBundle{reg: reg, router: gw.NewRouter(reg, nil)})
	}
	req := connect.NewRequest(&pb.SystemOneRequest{Model: "@typesafe/native", State: structpb.NewStringValue("hello"), Questions: map[string]*pb.DecisionQuestion{"q": {Type: "noul"}}})
	if _, err := s.SystemOne(context.Background(), req); err == nil {
		t.Fatal("missing tenant accepted")
	}
	for _, tenant := range []string{"tenant_a", "tenant_b"} {
		ctx := database.WithTenantSchema(context.Background(), tenant)
		if _, err := s.SystemOne(ctx, req); err != nil {
			t.Fatal(err)
		}
		req.Msg.Model = "@missing/native"
		if _, err := s.SystemOne(ctx, req); err == nil {
			t.Fatal("fell back from missing provider")
		}
		req.Msg.Model = "@typesafe/native"
	}
}

type routingLLM struct {
	model string
	calls atomic.Int32
	seen  gw.ChatCompletionRequest
}

func (p *routingLLM) Name() string                { return "llm" }
func (p *routingLLM) SupportsModel(m string) bool { return m == "small" || m == "large" }
func (p *routingLLM) Chat(_ context.Context, r gw.ChatCompletionRequest) (gw.ChatCompletionResponse, error) {
	p.calls.Add(1)
	p.seen = r
	return gw.ChatCompletionResponse{ID: "test", Model: r.Model, Choices: []gw.Choice{{Message: gw.Message{Role: gw.RoleAssistant}}}}, nil
}
func (p *routingLLM) ChatStream(_ context.Context, r gw.ChatCompletionRequest, on func(gw.ChatResponseChunk) error) error {
	p.calls.Add(1)
	p.seen = r
	return on(gw.ChatResponseChunk{ID: "test", Model: r.Model})
}
func (p *routingLLM) Embed(context.Context, gw.EmbeddingsRequest) (gw.EmbeddingsResponse, error) {
	return gw.EmbeddingsResponse{}, nil
}
func TestAutomaticRoutingDispatchesUnaryAndStream(t *testing.T) {
	for _, stream := range []bool{false, true} {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, `{"answers":{"route":{"type":"choice","choice":"small","confidence":0.99,"probabilities":{"small":1,"large":0}}},"usage":{}}`)
		}))
		cfg := decision.RoutingConfig{Enabled: true, Model: "@kev/native", Fallback: "@llm/large", Targets: map[string]decision.Target{"small": {Model: "@llm/small", Description: "easy"}, "large": {Model: "@llm/large", Description: "hard"}}}
		s := CreateServerWithContext(context.Background(), &validator.GatewayConfig{DecisionRouting: cfg, Models: []validator.ModelConfig{{Provider: "kev", Model: []string{"native"}, BaseURL: upstream.URL + "/v1"}}}, &validator.FeaturesConfig{})
		s.feat.Gateway.EnableStreaming = true
		llm := &routingLLM{}
		s.defaultBundle.Load().reg.Register(llm)
		req := &pb.ChatCompletionRequest{Model: "auto", Stream: &stream, Messages: []*pb.Message{{Role: pb.Role_ROLE_USER, Content: []*pb.ContentPart{{Type: "text", Data: &pb.ContentPart_Text{Text: "hello"}}}}}}
		err := processChatCompletion(context.Background(), s, req, func(*pb.ChatCompletionResponse) error { return nil })
		upstream.Close()
		if err != nil {
			t.Fatal(err)
		}
		if llm.calls.Load() != 1 || llm.seen.Model != "small" || *llm.seen.Messages[0].Content[0].Text != "hello" {
			t.Fatalf("stream=%v calls=%d request=%+v", stream, llm.calls.Load(), llm.seen)
		}
	}
}

func TestSharedRoutingNeverInheritsHostPolicy(t *testing.T) {
	cfg := decision.RoutingConfig{Enabled: true, Model: "@kev/native", Fallback: "@llm/large", Targets: map[string]decision.Target{"small": {Model: "@llm/small", Description: "easy"}, "large": {Model: "@llm/large", Description: "hard"}}}
	s := &Server{ctx: context.WithValue(context.Background(), contextkeys.SharedGatewayMode, true), cfg: &validator.GatewayConfig{DecisionRouting: cfg}}
	reg := gw.NewRegistry()
	reg.Register(&routingLLM{})
	s.installBundle("tenant", &tenantBundle{reg: reg, router: gw.NewRouter(reg, nil)})
	ctx := database.WithTenantSchema(context.Background(), "tenant")
	if _, err := s.selectDecisionRoute(ctx, "auto", nil); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("host routing leaked to tenant: %v", err)
	}
	s.providersFor(ctx).decisionRouting = cfg
	model, err := s.selectDecisionRoute(ctx, "auto", nil)
	if err != nil || model != cfg.Fallback {
		t.Fatalf("tenant fallback=%s error=%v", model, err)
	}
	explicit, err := s.selectDecisionRoute(ctx, "@llm/small", nil)
	if err != nil || explicit != "@llm/small" {
		t.Fatal("explicit model changed")
	}
}
func TestRoutingStateSkipsUnsupportedContent(t *testing.T) {
	text := "hi"
	image := "data:image/png;base64,secret"
	messages := []gw.Message{{Role: gw.RoleUser, Content: []gw.ContentPart{{Type: "image_url", ImageURL: &image}}}}
	if state := decisionChatState(messages, false); state != nil {
		t.Fatal("image sent to text decision engine")
	}
	messages[0].Content = []gw.ContentPart{{Type: "text", Text: &text}}
	if state := decisionChatState(messages, true); state != nil {
		t.Fatal("tool request judged without tool context")
	}
	if state := decisionChatState(messages, false); state == nil {
		t.Fatal("text request not routed")
	}
	p := policy.NewDefaultPolicy()
	if p.ShouldBypassPath("/v1/systemone") || p.ShouldBypassProcedure("/everstack.gateway.v1.GatewayService/SystemOne") {
		t.Fatal("decision endpoint bypasses authentication")
	}
}

type responseRoutingLLM struct {
	routingLLM
	name         string
	seenResponse string
}

func (p *responseRoutingLLM) Name() string { return p.name }
func (p *responseRoutingLLM) CreateResponse(_ context.Context, r gw.CreateResponseRequest) (gw.CreateResponseResponse, error) {
	p.seenResponse = r.Model
	return gw.CreateResponseResponse{ID: "resp_test", Model: r.Model, Status: "completed"}, nil
}
func (p *responseRoutingLLM) CreateResponseStream(ctx context.Context, r gw.CreateResponseRequest, on func(gw.CreateResponseResponse) error) error {
	out, err := p.CreateResponse(ctx, r)
	if err != nil {
		return err
	}
	return on(out)
}
func TestAutomaticResponsesUsesSelectedProvider(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"answers":{"route":{"type":"choice","choice":"small","confidence":0.99,"probabilities":{"small":1,"large":0}}},"usage":{}}`)
	}))
	defer upstream.Close()
	cfg := decision.RoutingConfig{Enabled: true, Model: "@kev/native", Fallback: "@llm/large", Targets: map[string]decision.Target{"small": {Model: "@llm/small", Description: "easy"}, "large": {Model: "@llm/large", Description: "hard"}}}
	s := CreateServerWithContext(context.Background(), &validator.GatewayConfig{DecisionRouting: cfg, Models: []validator.ModelConfig{{Provider: "kev", Model: []string{"native"}, BaseURL: upstream.URL + "/v1"}}}, nil)
	selected := &responseRoutingLLM{name: "llm"}
	other := &responseRoutingLLM{name: "other"}
	s.defaultBundle.Load().reg.Register(other)
	s.defaultBundle.Load().reg.Register(selected)
	_, handler := gatewayconnect.NewGatewayServiceHandler(s)
	api := httptest.NewServer(handler)
	defer api.Close()
	client := gatewayconnect.NewGatewayServiceClient(api.Client(), api.URL)
	stream, err := client.CreateResponse(context.Background(), connect.NewRequest(&pb.CreateResponseRequest{Model: "auto", Input: []*pb.ResponseInput{{Type: "message", Role: pb.Role_ROLE_USER, Content: []*pb.ContentPart{{Type: "text", Data: &pb.ContentPart_Text{Text: "hello"}}}}}}))
	if err != nil {
		t.Fatal(err)
	}
	for stream.Receive() {
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	if selected.seenResponse != "small" || other.seenResponse != "" {
		t.Fatalf("selected=%s unrelated=%s", selected.seenResponse, other.seenResponse)
	}
}
