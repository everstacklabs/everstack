package middleware

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1/gatewayconnect"
)

// trialGateway answers every chat the way the gateway does for a model
// trial request: a refusal rendered by billingmeter.ModelTrialConnectError,
// or, with refusal nil, a served response carrying x-evs-model-trial.
type trialGateway struct {
	gatewayconnect.UnimplementedGatewayServiceHandler
	refusal *billingmeter.ModelTrialRefusedError
}

func (g trialGateway) ChatCompletion(_ context.Context, _ *connect.Request[gatewaypb.ChatCompletionRequest], stream *connect.ServerStream[gatewaypb.ChatCompletionResponse]) error {
	if g.refusal != nil {
		cErr, _ := billingmeter.ModelTrialConnectError(g.refusal)
		return cErr
	}
	stream.ResponseHeader().Set("x-evs-model-trial", "remaining=19")
	return stream.Send(&gatewaypb.ChatCompletionResponse{Id: "x", Model: "gpt-5.4-mini"})
}

// v1Server serves h over grpc-gateway dialing a Connect backend over gRPC,
// the deployed /v1 shape, wrapped in SpendLimitStatus.
func v1Server(t *testing.T, h gatewayconnect.GatewayServiceHandler) *httptest.Server {
	t.Helper()
	_, handler := gatewayconnect.NewGatewayServiceHandler(h)
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	backend := httptest.NewUnstartedServer(mux)
	backend.EnableHTTP2 = true
	backend.StartTLS()
	t.Cleanup(backend.Close)
	gw := runtime.NewServeMux()
	creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true}) //nolint:gosec // test-only self-signed server
	if err := gatewaypb.RegisterGatewayServiceHandlerFromEndpoint(context.Background(), gw,
		strings.TrimPrefix(backend.URL, "https://"), []grpc.DialOption{grpc.WithTransportCredentials(creds)}); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(SpendLimitStatus(gw))
	t.Cleanup(api.Close)
	return api
}

func postChat(t *testing.T, api *httptest.Server, stream string) (*http.Response, []byte) {
	t.Helper()
	resp, err := http.Post(api.URL+"/v1/chat/completions", "application/json",
		strings.NewReader(`{"model":"gpt-5.4-mini","messages":[{"role":"ROLE_USER","content":[{"type":"text","text":"hi"}]}]`+stream+`}`))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp, body
}

// Every model trial refusal except the interval reaches /v1 as 402 with an
// OpenAI-shaped insufficient_quota body naming the reason, unary and
// streaming, so OpenAI SDKs do not retry it.
func TestModelTrialRefusalIs402OnV1(t *testing.T) {
	for _, reason := range []string{
		billingmeter.ReasonModelTrialExhausted, billingmeter.ReasonModelTrialExpired,
		billingmeter.ReasonModelTrialPaused, billingmeter.ReasonModelTrialTextOnly,
		billingmeter.ReasonModelTrialToolsUnsupported, billingmeter.ReasonModelTrialInputTooLarge,
		billingmeter.ReasonModelTrialModelUnsupported, billingmeter.ReasonModelTrialNotEligible,
	} {
		refusal := billingmeter.ModelTrialRefusal(reason, "gpt-5.4-mini", 4000, 0, time.Time{})
		api := v1Server(t, trialGateway{refusal: refusal})
		for _, stream := range []string{"", `,"stream":true`} {
			resp, body := postChat(t, api, stream)
			if resp.StatusCode != http.StatusPaymentRequired {
				t.Fatalf("%s stream=%q: status = %d, want 402: %s", reason, stream, resp.StatusCode, body)
			}
			e := decodeOpenAIError(t, body)
			if e.Error.Code != reason || e.Error.Type != "insufficient_quota" || e.Error.Message != refusal.Message {
				t.Fatalf("%s stream=%q: error = %+v", reason, stream, e.Error)
			}
			if resp.Header.Get("Retry-After") != "" {
				t.Fatalf("%s: a 402 carries no Retry-After", reason)
			}
		}
	}
}

// The per-organization interval stays 429 and carries Retry-After.
func TestModelTrialRateLimitIs429WithRetryAfter(t *testing.T) {
	refusal := billingmeter.ModelTrialRefusal(billingmeter.ReasonModelTrialRateLimited, "gpt-5.4-mini", 0, 4200*time.Millisecond, time.Time{})
	api := v1Server(t, trialGateway{refusal: refusal})
	for _, stream := range []string{"", `,"stream":true`} {
		resp, body := postChat(t, api, stream)
		if resp.StatusCode != http.StatusTooManyRequests {
			t.Fatalf("stream=%q: status = %d, want 429: %s", stream, resp.StatusCode, body)
		}
		if got := resp.Header.Get("Retry-After"); got != "5" {
			t.Fatalf("stream=%q: Retry-After = %q, want 5", stream, got)
		}
		e := decodeOpenAIError(t, body)
		if e.Error.Code != billingmeter.ReasonModelTrialRateLimited {
			t.Fatalf("stream=%q: error = %+v", stream, e.Error)
		}
	}
}

// A served trial response carries x-evs-model-trial on /v1 under its own
// name, not grpc-gateway's Grpc-Metadata- prefix.
func TestModelTrialHeaderOnV1(t *testing.T) {
	api := v1Server(t, trialGateway{})
	for _, stream := range []string{"", `,"stream":true`} {
		resp, body := postChat(t, api, stream)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("stream=%q: status = %d: %s", stream, resp.StatusCode, body)
		}
		if got := resp.Header.Get("X-Evs-Model-Trial"); got != "remaining=19" {
			t.Fatalf("stream=%q: x-evs-model-trial = %q", stream, got)
		}
		if resp.Header.Get("Grpc-Metadata-X-Evs-Model-Trial") != "" {
			t.Fatalf("stream=%q: the grpc-gateway form leaked", stream)
		}
	}
}
