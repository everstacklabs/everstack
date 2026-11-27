package v1

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

func TestProviderErrorToConnectMapsUpstreamStatus(t *testing.T) {
	cases := []struct {
		status   int
		message  string
		wantCode connect.Code
	}{
		{400, "Unsupported parameter: 'temperature'", connect.CodeInvalidArgument},
		{422, "bad schema", connect.CodeInvalidArgument},
		// A rejected provider key is the operator's to fix; answering 401 or
		// 403 would tell the caller their gateway key is wrong (and the
		// dashboard treats PermissionDenied as a logout).
		{401, "Incorrect API key provided", connect.CodeFailedPrecondition},
		{403, "forbidden", connect.CodeFailedPrecondition},
		{404, "The model does not exist", connect.CodeNotFound},
		{429, "Rate limit reached", connect.CodeResourceExhausted},
		{500, "server error", connect.CodeUnavailable},
		{503, "overloaded", connect.CodeUnavailable},
	}
	for _, tc := range cases {
		err := providerErrorToConnect(&gw.UpstreamError{Provider: "openai", StatusCode: tc.status, Message: tc.message})
		if got := connect.CodeOf(err); got != tc.wantCode {
			t.Errorf("status %d: code = %v, want %v", tc.status, got, tc.wantCode)
		}
		var ce *connect.Error
		if !errors.As(err, &ce) || !strings.Contains(ce.Message(), tc.message) {
			t.Errorf("status %d: message %q lost", tc.status, err)
		}
	}

	cfgErr := providerErrorToConnect(gw.ValidateAPIKeyFormat("openai", "not a key"))
	if connect.CodeOf(cfgErr) != connect.CodeFailedPrecondition {
		t.Errorf("malformed key: code = %v, want FailedPrecondition", connect.CodeOf(cfgErr))
	}
	existing := connect.NewError(connect.CodeNotFound, errors.New("model not found"))
	if providerErrorToConnect(existing) != existing {
		t.Error("an existing connect error must pass through unchanged")
	}
	if providerErrorToConnect(nil) != nil {
		t.Error("nil must stay nil")
	}
}

// Dev (EVS-328): key A answered 401 invalid_api_key, rotation moved to key B,
// which answered a bare 400, and only the 400 reached the caller. The
// actionable 401 must survive rotation.
func TestKeyRotationKeepsTheFirstFailure(t *testing.T) {
	first := &gw.UpstreamError{Provider: "openai", StatusCode: 401, Message: "Incorrect API key provided", Code: "invalid_api_key"}
	last := &gw.UpstreamError{Provider: "openai", StatusCode: 400, Message: "Bad Request"}
	err := keyRotationError(first, last)
	for _, want := range []string{"Incorrect API key provided", "invalid_api_key", "Bad Request"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("combined error %q is missing %q", err, want)
		}
	}
	if connect.CodeOf(providerErrorToConnect(err)) != connect.CodeFailedPrecondition {
		t.Errorf("combined error should classify by the auth failure, got %v", connect.CodeOf(providerErrorToConnect(err)))
	}
	if keyRotationError(first, nil) != first || keyRotationError(first, first) != first {
		t.Error("nothing to combine should return the first error")
	}
}

func TestConnectErrorToGRPCStatus(t *testing.T) {
	err := connectToGRPCError(connect.NewError(connect.CodeInvalidArgument, errors.New("openai returned 400: bad")))
	st, ok := status.FromError(err)
	if !ok || st.Code() != codes.InvalidArgument || !strings.Contains(st.Message(), "openai returned 400: bad") {
		t.Fatalf("status = %v, %v", st, ok)
	}
	plain := errors.New("x")
	if connectToGRPCError(plain) != plain {
		t.Fatal("non-connect errors must pass through")
	}
}

// End to end over the deployed HTTP shape: grpc-gateway dials the Connect
// handler over gRPC, exactly as internal/api/grpc/server/gateway.go wires
// /v1/chat/completions. A fake OpenAI returns the real error envelopes.
func TestChatCompletionHTTPSurfacesUpstream4xx(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantHTTP   int
		wantInBody []string
	}{
		{
			name:       "unsupported parameter",
			status:     400,
			body:       `{"error":{"message":"Unsupported parameter: 'temperature' is not supported with this model.","type":"invalid_request_error","param":"temperature","code":"unsupported_parameter"}}`,
			wantHTTP:   http.StatusBadRequest,
			wantInBody: []string{"Unsupported parameter", "param=temperature"},
		},
		{
			name:       "bare bad request",
			status:     400,
			body:       `{"error":{"message":"Bad Request","type":null,"code":null,"param":null},"status":400}`,
			wantHTTP:   http.StatusBadRequest,
			wantInBody: []string{"openai returned 400: Bad Request"},
		},
		{
			name:       "revoked provider key",
			status:     401,
			body:       `{"error":{"message":"Incorrect API key provided: sk-test*****abcd.","type":"invalid_request_error","code":"invalid_api_key","param":null},"status":401}`,
			wantHTTP:   http.StatusBadRequest, // FailedPrecondition renders as 400 in grpc-gateway
			wantInBody: []string{"rejected the configured API key", "invalid_api_key"},
		},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			name := tc.name
			if stream {
				name += "/stream"
			}
			t.Run(name, func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.body)
				}))
				defer upstream.Close()

				s := CreateServerWithContext(context.Background(), &validator.GatewayConfig{Models: []validator.ModelConfig{{
					Provider: "openai", Model: []string{"gpt-5.2-codex"}, APIKey: "sk-test", BaseURL: upstream.URL,
				}}}, &validator.FeaturesConfig{})
				s.feat.Gateway.EnableStreaming = true

				_, handler := s.RegisterConnectServer()
				backend := httptest.NewUnstartedServer(handler)
				backend.EnableHTTP2 = true
				backend.StartTLS()
				defer backend.Close()

				mux := runtime.NewServeMux()
				creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true}) //nolint:gosec // test-only self-signed server
				if err := s.RegisterGateway(context.Background(), mux, strings.TrimPrefix(backend.URL, "https://"), []grpc.DialOption{grpc.WithTransportCredentials(creds)}); err != nil {
					t.Fatal(err)
				}
				api := httptest.NewServer(mux)
				defer api.Close()

				streamField := ""
				if stream {
					streamField = `,"stream":true`
				}
				resp, err := http.Post(api.URL+"/v1/chat/completions", "application/json",
					strings.NewReader(`{"model":"gpt-5.2-codex","messages":[{"role":"ROLE_USER","content":[{"type":"text","text":"say hi"}]}]`+streamField+`}`))
				if err != nil {
					t.Fatal(err)
				}
				defer resp.Body.Close()
				body, _ := io.ReadAll(resp.Body)
				if resp.StatusCode != tc.wantHTTP {
					t.Fatalf("HTTP %d, want %d; body %s", resp.StatusCode, tc.wantHTTP, body)
				}
				for _, want := range tc.wantInBody {
					if !strings.Contains(string(body), want) {
						t.Errorf("body %s is missing %q", body, want)
					}
				}
				if strings.Contains(string(body), `\"error\"`) {
					t.Errorf("body embeds the raw upstream JSON: %s", body)
				}
			})
		}
	}
}

// The Responses API keeps a Connect status from the provider chain (the
// spend limit refusal) instead of turning it into Internal (HTTP 500).
func TestResponsesKeepsProviderConnectStatus(t *testing.T) {
	refusal := connect.NewError(connect.CodeResourceExhausted, errors.New("spend limit"))
	if got := responsesConnectError(refusal); got != error(refusal) {
		t.Fatalf("connect error replaced: %v", got)
	}
	if connect.CodeOf(responsesConnectError(errors.New("boom"))) != connect.CodeInternal {
		t.Fatal("a plain error must stay Internal")
	}
}
