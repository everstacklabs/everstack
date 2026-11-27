package api

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	httpmw "github.com/everstacklabs/everstack/internal/api/http/middleware"
)

const spendRefusal = `{"error":{"message":"Your organization has reached its monthly spend limit of $10.00.","type":"insufficient_quota","param":null,"code":"spend_limit_exceeded"}}`

// An error body that is already OpenAI-shaped is not re-classified: its
// string code is not a gRPC or HTTP number.
func TestProtoErrorToOpenAIKeepsOpenAIShapedErrors(t *testing.T) {
	if _, _, ok := protoErrorToOpenAI([]byte(spendRefusal), http.StatusPaymentRequired); ok {
		t.Fatal("an OpenAI-shaped error must be kept as is")
	}
}

// The spend limit refusal reaches OpenAI clients on /v1 (Bearer) and
// /openai/v1 as HTTP 402 with the OpenAI error envelope and a string code,
// streamed or not; never as a 429 an SDK would retry.
func TestSpendLimitRefusalReachesOpenAIClientsAs402(t *testing.T) {
	refusing := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"code":8,"message":"Your organization has reached its monthly spend limit of $10.00.","details":[{"@type":"type.googleapis.com/google.rpc.ErrorInfo","reason":"spend_limit_exceeded"}]}}`+"\n")
	})
	h := newHarness()
	chain := httpmw.NewAPIKeyInterceptor(false).WithAPIKeyValidation(httpmw.SpendLimitStatus(refusing))
	for name, tc := range map[string]struct {
		srv  http.Handler
		path string
	}{
		"v1 bearer":    {withV1OpenAICompat(chain), "/v1/chat/completions"},
		"openai alias": {withOpenAIRoundTrip(chain), "/openai/v1/chat/completions"},
	} {
		for _, stream := range []string{"", `"stream":true,`} {
			rec := h.do(t, tc.srv, http.MethodPost, tc.path, `{"model":"gpt-5.5",`+stream+`"messages":[{"role":"user","content":"hi"}]}`, bearer(liveKey))
			if rec.Code != http.StatusPaymentRequired {
				t.Fatalf("%s stream=%q: status %d, want 402: %s", name, stream, rec.Code, rec.Body.String())
			}
			var e struct {
				Error struct {
					Type string `json:"type"`
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil || e.Error.Code != "spend_limit_exceeded" || e.Error.Type != "insufficient_quota" {
				t.Fatalf("%s stream=%q: body %s (%v)", name, stream, rec.Body.String(), err)
			}
		}
	}
}
