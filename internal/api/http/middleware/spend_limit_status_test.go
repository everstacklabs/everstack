package middleware

import (
	"context"
	"crypto/tls"
	"encoding/json"
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

type openAIError struct {
	Error struct {
		Message string  `json:"message"`
		Type    string  `json:"type"`
		Param   *string `json:"param"`
		Code    string  `json:"code"`
	} `json:"error"`
}

func decodeOpenAIError(t *testing.T, body []byte) openAIError {
	t.Helper()
	var e openAIError
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("not an OpenAI error body: %v: %s", err, body)
	}
	return e
}

const spendStreamError = `{"error":{"code":8,"message":"Your organization has reached its monthly spend limit of $10.00.","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"spend_limit_exceeded","domain":"everstack.ai","metadata":{}}]}}` + "\n"

func TestSpendLimitStatusRewritesTheRefusalTo402(t *testing.T) {
	for name, body := range map[string]string{
		"stream error": spendStreamError,
		"unary error":  `{"code":8,"message":"Your organization has reached its monthly spend limit of $10.00.","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"spend_limit_exceeded"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			h := SpendLimitStatus(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = io.WriteString(w, body)
			}))
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
			if rec.Code != http.StatusPaymentRequired {
				t.Fatalf("status = %d, want 402: %s", rec.Code, rec.Body.String())
			}
			e := decodeOpenAIError(t, rec.Body.Bytes())
			if e.Error.Code != billingmeter.ReasonSpendLimitExceeded || e.Error.Type != "insufficient_quota" || e.Error.Param != nil {
				t.Fatalf("error = %+v", e.Error)
			}
			if !strings.Contains(e.Error.Message, "monthly spend limit of $10.00") {
				t.Fatalf("message = %q", e.Error.Message)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Fatalf("content type = %q", ct)
			}
		})
	}
}

// A real rate limit, and every other response, passes through untouched.
func TestSpendLimitStatusLeavesOtherResponsesAlone(t *testing.T) {
	rateLimited := `{"error":{"code":8,"message":"rate limit reached","details":[]}}`
	h := SpendLimitStatus(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, rateLimited)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	if rec.Code != http.StatusTooManyRequests || rec.Body.String() != rateLimited {
		t.Fatalf("rate limit changed: %d %s", rec.Code, rec.Body.String())
	}

	h = SpendLimitStatus(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "data: one\n\n")
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, "data: two\n\n")
	}))
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil))
	if rec.Code != http.StatusOK || rec.Body.String() != "data: one\n\ndata: two\n\n" || !rec.Flushed {
		t.Fatalf("stream changed: %d flushed=%v %q", rec.Code, rec.Flushed, rec.Body.String())
	}
}

type refusingGateway struct {
	gatewayconnect.UnimplementedGatewayServiceHandler
}

func (refusingGateway) ChatCompletion(context.Context, *connect.Request[gatewaypb.ChatCompletionRequest], *connect.ServerStream[gatewaypb.ChatCompletionResponse]) error {
	return billingmeter.SpendLimitError(billingmeter.SpendLimit{Period: "monthly", LimitMicros: 10_000_000}, time.Now())
}

// Over the deployed shape: grpc-gateway dials the Connect handler over gRPC,
// as internal/api/grpc/server/gateway.go wires /v1. Without the wrapper the
// refusal is a 429 an OpenAI SDK would retry.
func TestSpendLimitStatusOverGRPCGateway(t *testing.T) {
	_, handler := gatewayconnect.NewGatewayServiceHandler(refusingGateway{})
	mux := http.NewServeMux()
	mux.Handle("/", handler)
	backend := httptest.NewUnstartedServer(mux)
	backend.EnableHTTP2 = true
	backend.StartTLS()
	defer backend.Close()

	gw := runtime.NewServeMux()
	creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true}) //nolint:gosec // test-only self-signed server
	if err := gatewaypb.RegisterGatewayServiceHandlerFromEndpoint(context.Background(), gw,
		strings.TrimPrefix(backend.URL, "https://"), []grpc.DialOption{grpc.WithTransportCredentials(creds)}); err != nil {
		t.Fatal(err)
	}
	for _, wrap := range []bool{false, true} {
		var h http.Handler = gw
		if wrap {
			h = SpendLimitStatus(gw)
		}
		api := httptest.NewServer(h)
		for _, stream := range []string{"", `,"stream":true`} {
			resp, err := http.Post(api.URL+"/v1/chat/completions", "application/json",
				strings.NewReader(`{"model":"gpt-5.5","messages":[{"role":"ROLE_USER","content":[{"type":"text","text":"hi"}]}]`+stream+`}`))
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if !wrap {
				if resp.StatusCode != http.StatusTooManyRequests {
					t.Fatalf("unwrapped status = %d, the premise of this fix is a 429: %s", resp.StatusCode, body)
				}
				continue
			}
			if resp.StatusCode != http.StatusPaymentRequired {
				t.Fatalf("stream=%q status = %d, want 402: %s", stream, resp.StatusCode, body)
			}
			e := decodeOpenAIError(t, body)
			if e.Error.Code != billingmeter.ReasonSpendLimitExceeded || !strings.Contains(e.Error.Message, "$10.00") {
				t.Fatalf("stream=%q error = %+v", stream, e.Error)
			}
		}
		api.Close()
	}
}
