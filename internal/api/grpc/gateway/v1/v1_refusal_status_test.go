package v1

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	httpmw "github.com/everstacklabs/everstack/internal/api/http/middleware"
	"github.com/everstacklabs/everstack/internal/billingmeter"
	"github.com/everstacklabs/everstack/internal/billingtrial"
	"github.com/everstacklabs/everstack/internal/providers/billing"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
)

// The deployed /v1 shape: grpc-gateway, wrapped in SpendLimitStatus, dialing
// the classic gRPC server, whose ChatCompletion turns the handler's Connect
// error into a gRPC status through connectToGRPCError. The refusal's
// ErrorInfo reason must survive that hop for /v1 to answer 402 or 429.
func v1OverClassicGRPC(t *testing.T, srv gatewaypb.GatewayServiceServer) *httptest.Server {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gs := grpc.NewServer()
	gatewaypb.RegisterGatewayServiceServer(gs, srv)
	go func() { _ = gs.Serve(lis) }()
	t.Cleanup(gs.Stop)
	mux := runtime.NewServeMux()
	if err := gatewaypb.RegisterGatewayServiceHandlerFromEndpoint(context.Background(), mux, lis.Addr().String(),
		[]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(httpmw.SpendLimitStatus(mux))
	t.Cleanup(api.Close)
	return api
}

type v1Error struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    string `json:"code"`
	} `json:"error"`
}

func postV1(t *testing.T, api *httptest.Server, model string, stream bool) (*http.Response, v1Error) {
	t.Helper()
	body := `{"model":"` + model + `","messages":[{"role":"ROLE_USER","content":[{"type":"text","text":"hi"}]}]`
	if stream {
		body += `,"stream":true`
	}
	resp, err := http.Post(api.URL+"/v1/chat/completions", "application/json", strings.NewReader(body+"}"))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var e v1Error
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("status %d, not an OpenAI error body: %s", resp.StatusCode, raw)
	}
	return resp, e
}

// A model trial refusal from the real chat handler reaches /v1 as 402
// insufficient_quota (exhausted) or 429 with Retry-After (the interval),
// unary and streaming.
func TestModelTrialRefusalOverClassicGRPCToV1(t *testing.T) {
	cases := []struct {
		takeErr    error
		status     int
		errType    string
		code       string
		retryAfter string
	}{
		{&billingtrial.RefusedError{Reason: billingtrial.ReasonExhausted}, http.StatusPaymentRequired, "insufficient_quota", billingmeter.ReasonModelTrialExhausted, ""},
		{&billingtrial.RefusedError{Reason: billingtrial.ReasonRateLimited, RetryAfter: 4 * time.Second}, http.StatusTooManyRequests, "rate_limit_exceeded", billingmeter.ReasonModelTrialRateLimited, "4"},
	}
	for _, tc := range cases {
		sink := &trialAttemptSink{attemptSink: newAttemptSink(), takeErr: tc.takeErr}
		s := attemptServer(t, sink.attemptSink, nil)
		s.defaultBundle.Load().reg.Register(billing.Wrap(&scriptedModel{name: "pp", model: "m-primary"},
			billing.Config{Meter: sink, Provenance: billingmeter.ProvenanceTrial}))
		api := v1OverClassicGRPC(t, &GrpcServer{base: s})
		for _, stream := range []bool{false, true} {
			resp, e := postV1(t, api, "m-primary", stream)
			if resp.StatusCode != tc.status || e.Error.Type != tc.errType || e.Error.Code != tc.code {
				t.Fatalf("%s stream=%v: status %d, error %+v", tc.code, stream, resp.StatusCode, e.Error)
			}
			if got := resp.Header.Get("Retry-After"); got != tc.retryAfter {
				t.Fatalf("%s stream=%v: Retry-After = %q, want %q", tc.code, stream, got, tc.retryAfter)
			}
		}
	}
}

// spendRefusingServer answers like the chat handler does when the Cloud
// spend limit refuses: the Connect error, through connectToGRPCError.
type spendRefusingServer struct {
	gatewaypb.UnimplementedGatewayServiceServer
}

func (spendRefusingServer) ChatCompletion(*gatewaypb.ChatCompletionRequest, gatewaypb.GatewayService_ChatCompletionServer) error {
	return connectToGRPCError(billingmeter.SpendLimitError(billingmeter.SpendLimit{Period: "monthly", LimitMicros: 10_000_000}, time.Now()))
}

// The Cloud spend limit refusal shares the hop and now reaches /v1 as 402.
func TestSpendLimitRefusalOverClassicGRPCToV1(t *testing.T) {
	api := v1OverClassicGRPC(t, spendRefusingServer{})
	for _, stream := range []bool{false, true} {
		resp, e := postV1(t, api, "gpt-5.5", stream)
		if resp.StatusCode != http.StatusPaymentRequired || e.Error.Code != billingmeter.ReasonSpendLimitExceeded {
			t.Fatalf("stream=%v: status %d, error %+v", stream, resp.StatusCode, e.Error)
		}
	}
}
