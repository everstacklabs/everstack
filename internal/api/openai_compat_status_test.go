package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// The gateway's error middleware writes an HTTP status into error.code. Running
// those through the gRPC table sent every one of them to the default arm, so a
// caller on /openai/v1 saw 500 for everything.
//
// Reproduced live on the launch endpoint before this fix: an invalid key on
// /openai/v1/chat/completions returned HTTP 500 with type "api_error", while
// the same key on /v1 correctly returned 401.
func TestGatewayHTTPStatusIsNotTreatedAsAGRPCCode(t *testing.T) {
	cases := []struct {
		code int
		want int
		kind string
	}{
		{code: 401, want: http.StatusUnauthorized, kind: "authentication_error"},
		{code: 403, want: http.StatusForbidden, kind: "authentication_error"},
		{code: 404, want: http.StatusNotFound, kind: "invalid_request_error"},
		{code: 400, want: http.StatusBadRequest, kind: "invalid_request_error"},
		// The one that matters most: an SDK backs off on 429 but retries a 500
		// as a server fault, so a rate limit that reports 500 is retried wrong.
		{code: 429, want: http.StatusTooManyRequests, kind: "rate_limit_error"},
		{code: 503, want: http.StatusServiceUnavailable, kind: "api_error"},
	}
	for _, testCase := range cases {
		t.Run(fmt.Sprintf("http_%d", testCase.code), func(t *testing.T) {
			body := []byte(fmt.Sprintf(
				`{"error":{"code":%d,"message":"boom","type":"error"}}`, testCase.code))
			out, status, ok := protoErrorToOpenAI(body, http.StatusInternalServerError)
			if !ok {
				t.Fatalf("protoErrorToOpenAI() ok = false, want it to recognise the gateway error shape")
			}
			if status != testCase.want {
				t.Fatalf("status = %d, want %d", status, testCase.want)
			}
			var parsed struct {
				Error struct {
					Type    string `json:"type"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.Unmarshal(out, &parsed); err != nil {
				t.Fatalf("output is not JSON: %v", err)
			}
			if parsed.Error.Type != testCase.kind {
				t.Fatalf("type = %q, want %q", parsed.Error.Type, testCase.kind)
			}
			if parsed.Error.Message != "boom" {
				t.Fatalf("message = %q, want it preserved", parsed.Error.Message)
			}
		})
	}
}

// grpc-gateway still writes real gRPC codes, and those must keep mapping.
func TestGRPCCodesStillMapThroughTheGRPCTable(t *testing.T) {
	cases := []struct {
		code int
		want int
	}{
		{code: 16, want: http.StatusUnauthorized},       // UNAUTHENTICATED
		{code: 8, want: http.StatusTooManyRequests},     // RESOURCE_EXHAUSTED
		{code: 5, want: http.StatusNotFound},            // NOT_FOUND
		{code: 3, want: http.StatusBadRequest},          // INVALID_ARGUMENT
		{code: 14, want: http.StatusServiceUnavailable}, // UNAVAILABLE
	}
	for _, testCase := range cases {
		t.Run(fmt.Sprintf("grpc_%d", testCase.code), func(t *testing.T) {
			body := []byte(fmt.Sprintf(`{"code":%d,"message":"boom"}`, testCase.code))
			_, status, ok := protoErrorToOpenAI(body, http.StatusInternalServerError)
			if !ok {
				t.Fatalf("protoErrorToOpenAI() ok = false, want the grpc-gateway shape recognised")
			}
			if status != testCase.want {
				t.Fatalf("status = %d, want %d", status, testCase.want)
			}
		})
	}
}

// With no usable code, the caller's fallback stands rather than being invented.
func TestMissingCodeKeepsTheFallbackStatus(t *testing.T) {
	body := []byte(`{"error":{"message":"boom","type":"error"}}`)
	_, status, ok := protoErrorToOpenAI(body, http.StatusBadGateway)
	if !ok {
		t.Fatalf("protoErrorToOpenAI() ok = false, want the shape recognised")
	}
	if status != http.StatusBadGateway {
		t.Fatalf("status = %d, want the fallback %d", status, http.StatusBadGateway)
	}
}
