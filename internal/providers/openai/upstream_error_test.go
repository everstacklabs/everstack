package openai

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// Error bodies below are the shapes OpenAI actually returned on dev for
// gpt-5.2-codex (EVS-328): a revoked key (401 invalid_api_key) and a bare 400
// whose fields are all null, which is what OpenAI's edge sends when the
// Authorization header carries a value that is not an API key at all.
const (
	invalidKeyBody = `{
  "error": {
    "message": "Incorrect API key provided: sk-test*****abcd. You can find your API key at https://platform.openai.com/account/api-keys.",
    "type": "invalid_request_error",
    "code": "invalid_api_key",
    "param": null
  },
  "status": 401
}`
	bareBadRequestBody = `{
  "error": {
    "message": "Bad Request",
    "type": null,
    "code": null,
    "param": null
  },
  "status": 400
}`
	unsupportedParamBody = `{
  "error": {
    "message": "Unsupported parameter: 'temperature' is not supported with this model.",
    "type": "invalid_request_error",
    "param": "temperature",
    "code": "unsupported_parameter"
  }
}`
)

func upstreamErrorServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestUpstreamErrorsAreTyped(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		wantMsg   string
		wantCode  string
		wantParam string
	}{
		{"invalid key", 401, invalidKeyBody, "Incorrect API key provided", "invalid_api_key", ""},
		{"bare bad request", 400, bareBadRequestBody, "Bad Request", "", ""},
		{"unsupported param", 400, unsupportedParamBody, "Unsupported parameter", "unsupported_parameter", "temperature"},
		{"non-json body", 502, "upstream connect error", "upstream connect error", "", ""},
		{"empty body", 400, "", "Bad Request", "", ""},
	}
	models := []string{"gpt-5.2-codex", "gpt-4o"} // Responses and Chat Completions paths
	for _, tc := range cases {
		for _, model := range models {
			for _, stream := range []bool{false, true} {
				name := tc.name + "/" + model
				if stream {
					name += "/stream"
				}
				t.Run(name, func(t *testing.T) {
					srv := upstreamErrorServer(t, tc.status, tc.body)
					p := NewProvider(Config{APIKey: "test-key", BaseURL: srv.URL})
					req := gw.ChatCompletionRequest{
						Model:    model,
						Messages: []gw.Message{{Role: gw.RoleUser, Content: []gw.ContentPart{gw.Text("say hi")}}},
					}
					var err error
					if stream {
						req.Stream = true
						err = p.ChatStream(t.Context(), req, func(gw.ChatResponseChunk) error { return nil })
					} else {
						_, err = p.Chat(t.Context(), req)
					}
					if err == nil {
						t.Fatal("expected an error")
					}
					upstream, ok := gw.AsUpstreamError(err)
					if !ok {
						t.Fatalf("error is not an UpstreamError: %T %v", err, err)
					}
					if upstream.StatusCode != tc.status {
						t.Errorf("StatusCode = %d, want %d", upstream.StatusCode, tc.status)
					}
					if upstream.Provider != "openai" {
						t.Errorf("Provider = %q, want openai", upstream.Provider)
					}
					if !strings.Contains(upstream.Message, tc.wantMsg) {
						t.Errorf("Message = %q, want it to contain %q", upstream.Message, tc.wantMsg)
					}
					if upstream.Code != tc.wantCode || upstream.Param != tc.wantParam {
						t.Errorf("Code/Param = %q/%q, want %q/%q", upstream.Code, upstream.Param, tc.wantCode, tc.wantParam)
					}
					if strings.Contains(err.Error(), "{") {
						t.Errorf("error leaks the raw JSON body: %q", err.Error())
					}
				})
			}
		}
	}
}

// A key that cannot be sent as an HTTP header value (here: pasted text with a
// newline and non-ASCII characters, like the dev tenant's "Config API Key")
// must never reach OpenAI. The provider fails before dialing with a message
// that tells the operator what to fix.
func TestMalformedAPIKeyIsRejectedBeforeSending(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer srv.Close()

	for _, key := range []string{"2026-10-01 log line — not a key", "sk-abc\ndef", "sk abc"} {
		p := NewProvider(Config{APIKey: key, BaseURL: srv.URL})
		req := gw.ChatCompletionRequest{
			Model:    "gpt-5.2-codex",
			Messages: []gw.Message{{Role: gw.RoleUser, Content: []gw.ContentPart{gw.Text("hi")}}},
		}
		_, err := p.Chat(t.Context(), req)
		if err == nil || !strings.Contains(err.Error(), "API key") {
			t.Fatalf("Chat(%q) error = %v, want malformed API key error", key, err)
		}
		err = p.ChatStream(t.Context(), req, func(gw.ChatResponseChunk) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "API key") {
			t.Fatalf("ChatStream(%q) error = %v, want malformed API key error", key, err)
		}
		if strings.Contains(err.Error(), key) {
			t.Fatalf("error echoes the key material: %v", err)
		}
	}
	if called {
		t.Fatal("malformed key was sent upstream")
	}
}

// Surrounding whitespace on a configured key (a YAML `api_key: "sk-... "` or a
// pasted key with a trailing tab or newline) worked before key validation was
// added, because net/http trims header values. It must keep working: the key
// is trimmed once and sent as `Bearer <trimmed>`. Interior whitespace is still
// rejected by TestMalformedAPIKeyIsRejectedBeforeSending.
func TestSurroundingWhitespaceInAPIKeyIsTrimmed(t *testing.T) {
	const trimmed = "sk-test-abc123"
	keys := map[string]string{
		"leading space":    " " + trimmed,
		"trailing space":   trimmed + " ",
		"leading tab":      "\t" + trimmed,
		"trailing tab":     trimmed + "\t",
		"trailing newline": trimmed + "\n",
		"crlf and spaces":  "  " + trimmed + " \r\n",
	}
	for name, key := range keys {
		for _, model := range []string{"gpt-5.2-codex", "gpt-4o"} {
			for _, stream := range []bool{false, true} {
				tn := name + "/" + model
				if stream {
					tn += "/stream"
				}
				t.Run(tn, func(t *testing.T) {
					var gotAuth string
					srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						gotAuth = r.Header.Get("Authorization")
						// Answer with an upstream error: it proves the request
						// was sent without needing a full success fixture.
						w.Header().Set("Content-Type", "application/json")
						w.WriteHeader(http.StatusTeapot)
						_, _ = w.Write([]byte(`{"error":{"message":"reached upstream"}}`))
					}))
					defer srv.Close()
					p := NewProvider(Config{APIKey: key, BaseURL: srv.URL})
					req := gw.ChatCompletionRequest{
						Model:    model,
						Messages: []gw.Message{{Role: gw.RoleUser, Content: []gw.ContentPart{gw.Text("hi")}}},
					}
					var err error
					if stream {
						req.Stream = true
						err = p.ChatStream(t.Context(), req, func(gw.ChatResponseChunk) error { return nil })
					} else {
						_, err = p.Chat(t.Context(), req)
					}
					var cfgErr *gw.ProviderConfigError
					if errors.As(err, &cfgErr) {
						t.Fatalf("key with surrounding whitespace rejected as misconfigured: %v", err)
					}
					if gotAuth != "Bearer "+trimmed {
						t.Fatalf("Authorization = %q, want %q", gotAuth, "Bearer "+trimmed)
					}
				})
			}
		}
	}
}
