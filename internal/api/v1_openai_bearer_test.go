package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/everstacklabs/everstack/internal/api/common"
	httpmw "github.com/everstacklabs/everstack/internal/api/http/middleware"
	"github.com/everstacklabs/everstack/internal/cqrs"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	apikeyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
	"github.com/gorilla/mux"
)

// These tests drive the real API-key interceptor behind the /v1 and
// /openai/v1 wrappers, with a key store that knows exactly one live key, and
// a stub standing in for the grpc-gateway. They pin the contract the docs now
// publish: an unmodified OpenAI SDK pointed at https://<instance>/v1 works.

const (
	testHashSecret = "v1-bearer-test-secret"
	liveKey        = "sk-mf-svcacct-api01-livekey"
	liveHostingKey = "evs_live_livehostingkey"
	revokedKey     = "sk-mf-usracct-api01-revokedkey"
	liveInstanceID = "inst-live"
)

// keyStore answers GetApiKeyByHash like the projection does: a live key
// resolves to its row, an unknown or revoked one resolves to nothing.
type keyStore struct {
	live  map[string]bool
	calls atomic.Int32
}

func (k *keyStore) QueryType() string { return "GetApiKeyByHash" }

func (k *keyStore) Handle(_ context.Context, q query.Query) (interface{}, error) {
	k.calls.Add(1)
	hq := q.(*apikeyq.GetApiKeyByHashQuery)
	for key := range k.live {
		if h, _ := apikeylib.HashWithSecret(key, testHashSecret); h == hq.Hash {
			inst := liveInstanceID
			return apikeyq.APIKeyReadModel{InstanceID: &inst}, nil
		}
	}
	return nil, nil
}

// gatewaySeen is what the stub grpc-gateway observed.
type gatewaySeen struct {
	called        bool
	path          string
	apiKey        string
	authorization string
	sseFormat     string
	tenantID      string
}

const protoChatFrames = `{"result":{"id":"chat_1","created":"1700000000","model":"gpt-5.5","choices":[{"message":{"content":[{"text":"hel"}]}}]}}
{"result":{"choices":[{"message":{"content":[{"text":"lo"}]},"finish_reason":"stop"}]}}
`

// stubGateway answers like the grpc-gateway: NDJSON proto frames for chat,
// an OpenAI SSE stream when the SSE middleware would have engaged, and the
// native model listing.
func stubGateway(seen *gatewaySeen) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.called = true
		seen.path = r.URL.Path
		seen.apiKey = r.Header.Get(common.EverstackApiKey)
		seen.authorization = r.Header.Get(common.Authorization)
		seen.sseFormat = r.Header.Get("X-SSE-Format")
		seen.tenantID = contextkeys.GetTenantID(r.Context())
		switch {
		case r.URL.Path == "/v1/gateway/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(gatewayModels))
		case r.URL.Path == "/v1/chat/completions" && seen.sseFormat == "openai":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"object\":\"chat.completion.chunk\"}\n\ndata: [DONE]\n\n"))
		default:
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(protoChatFrames))
		}
	})
}

type harness struct {
	store *keyStore
	seen  *gatewaySeen
	v1    http.Handler
	alias http.Handler
}

func newHarness() *harness {
	h := &harness{
		store: &keyStore{live: map[string]bool{liveKey: true, liveHostingKey: true}},
		seen:  &gatewaySeen{},
	}
	chain := httpmw.NewAPIKeyInterceptor(false).WithAPIKeyValidation(stubGateway(h.seen))
	// Match production's root raw-route guard and /v1 catch-all subrouters.
	router := mux.NewRouter()
	router.Use(httpmw.NewAPIKeyInterceptor(false).WithKeyedRESTValidation)
	router.PathPrefix("/v1").Subrouter().PathPrefix("").Handler(withV1OpenAICompat(chain))
	router.PathPrefix("/openai/v1").Subrouter().PathPrefix("").Handler(withOpenAIRoundTrip(chain))
	h.v1 = router
	h.alias = router
	return h
}

func (h *harness) do(t *testing.T, srv http.Handler, method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	*h.seen = gatewaySeen{}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	bus := query.NewQueryBus()
	bus.RegisterHandler(h.store)
	ctx := contextkeys.WithAPIKeyHashSecret(req.Context(), testHashSecret)
	ctx = cqrs.WithSystem(ctx, &cqrs.System{QueryBus: bus})
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req.WithContext(ctx))
	return rec
}

func bearer(key string) map[string]string { return map[string]string{"Authorization": "Bearer " + key} }

func assertOpenAIChat(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var parsed struct {
		Object  string `json:"object"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("body is not a single OpenAI JSON object: %v\n%s", err, rec.Body.String())
	}
	if parsed.Object != "chat.completion" || len(parsed.Choices) != 1 ||
		parsed.Choices[0].Message.Content != "hello" || parsed.Choices[0].FinishReason != "stop" {
		t.Fatalf("not an OpenAI chat.completion: %s", rec.Body.String())
	}
}

// The SDK quick-start: OpenAI(api_key=<issued key>, base_url=".../v1").
func TestV1BearerIssuedKeyServesOpenAIChatCompletion(t *testing.T) {
	for _, key := range []string{liveKey, liveHostingKey} {
		h := newHarness()
		rec := h.do(t, h.v1, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-5.5","messages":[{"role":"user","content":"hi"}]}`, bearer(key))
		assertOpenAIChat(t, rec)
		if h.seen.apiKey != key {
			t.Fatalf("%s: gateway saw api key %q", key, h.seen.apiKey)
		}
		// The grpc-gateway forwards Authorization as metadata and the Connect
		// interceptor refuses any bearer, so it must not travel past here.
		if h.seen.authorization != "" {
			t.Fatalf("%s: Authorization leaked to the gateway: %q", key, h.seen.authorization)
		}
		if h.seen.tenantID != liveInstanceID {
			t.Fatalf("%s: tenant = %q, want the key's instance", key, h.seen.tenantID)
		}
	}
}

func TestV1BearerIssuedKeyStreamsChatCompletion(t *testing.T) {
	h := newHarness()
	rec := h.do(t, h.v1, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-5.5","stream":true,"messages":[{"role":"user","content":"hi"}]}`, bearer(liveKey))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if h.seen.sseFormat != "openai" {
		t.Fatalf("stream not requested in OpenAI chunk format (X-SSE-Format=%q)", h.seen.sseFormat)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content type = %q, want an SSE stream", ct)
	}
	if !strings.Contains(rec.Body.String(), "data: [DONE]") {
		t.Fatalf("stream not passed through: %s", rec.Body.String())
	}
}

func TestV1BearerIssuedKeyListsModels(t *testing.T) {
	h := newHarness()
	rec := h.do(t, h.v1, http.MethodGet, "/v1/models", "", bearer(liveKey))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	var parsed struct {
		Object string            `json:"object"`
		Data   []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil || parsed.Object != "list" || len(parsed.Data) != 2 {
		t.Fatalf("not an OpenAI model list (err=%v): %s", err, rec.Body.String())
	}
	if h.seen.path != "/v1/gateway/models" || h.seen.authorization != "" {
		t.Fatalf("gateway saw path %q, authorization %q", h.seen.path, h.seen.authorization)
	}
}

// An unknown or revoked key carried as Bearer goes through the same key-store
// check as x-evs-api-key and is refused, in the OpenAI error envelope.
func TestV1BearerUnknownOrRevokedKeyIsRefused(t *testing.T) {
	for _, key := range []string{revokedKey, "sk-mf-svcacct-api01-neverissued", "evs_live_neverissued"} {
		h := newHarness()
		rec := h.do(t, h.v1, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-5.5","messages":[]}`, bearer(key))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s: status = %d, want 401", key, rec.Code)
		}
		if h.seen.called {
			t.Fatalf("%s: reached the gateway", key)
		}
		if h.store.calls.Load() == 0 {
			t.Fatalf("%s: refused without consulting the key store", key)
		}
		var parsed struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &parsed); err != nil || parsed.Error == nil {
			t.Fatalf("%s: not an OpenAI error envelope: %s", key, rec.Body.String())
		}
	}
}

// A device-auth JWT or a session token on Authorization is not an API key. It
// must keep getting the interceptor's rejection without ever being hashed and
// looked up as one.
func TestV1BearerNonKeyTokensAreNotTreatedAsAPIKeys(t *testing.T) {
	for name, token := range map[string]string{
		"device-auth jwt": "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiJ1c2VyIn0.c2ln",
		"session token":   "3b1f0c9e8a7d4c2b9e6f5a4d3c2b1a0f",
		"bare prefix":     "sk-mf-",
		"other vendor":    "sk-proj-abc123",
	} {
		for _, path := range []string{"/v1/chat/completions", "/v1/models"} {
			h := newHarness()
			method := http.MethodPost
			if path == "/v1/models" {
				method = http.MethodGet
			}
			rec := h.do(t, h.v1, method, path, `{}`, bearer(token))
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("%s on %s: status = %d, want 401", name, path, rec.Code)
			}
			if h.seen.called {
				t.Fatalf("%s on %s: reached the gateway", name, path)
			}
			if n := h.store.calls.Load(); n != 0 {
				t.Fatalf("%s on %s: probed the key store %d times", name, path, n)
			}
		}
	}
}

// An explicit x-evs-api-key wins over a Bearer on the same request, in both
// directions: a valid header is not overridden by a bad bearer, and a valid
// bearer does not rescue a bad header.
func TestV1ExplicitAPIKeyHeaderTakesPrecedenceOverBearer(t *testing.T) {
	h := newHarness()
	rec := h.do(t, h.v1, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-5.5","messages":[]}`,
		map[string]string{common.EverstackApiKey: liveKey, "Authorization": "Bearer " + revokedKey})
	assertOpenAIChat(t, rec)
	if h.seen.apiKey != liveKey || h.seen.authorization != "" {
		t.Fatalf("gateway saw api key %q, authorization %q", h.seen.apiKey, h.seen.authorization)
	}

	h = newHarness()
	rec = h.do(t, h.v1, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-5.5","messages":[]}`,
		map[string]string{common.EverstackApiKey: revokedKey, "Authorization": "Bearer " + liveKey})
	if rec.Code != http.StatusUnauthorized || h.seen.called {
		t.Fatalf("bearer overrode the explicit header: status %d, reached gateway %v", rec.Code, h.seen.called)
	}
}

// createOpenAIConfig (and any OpenAI SDK given defaultHeaders) sends a
// placeholder bearer next to the real key in x-evs-api-key. The header is the
// credential; the placeholder is dropped unread and never looked up.
func TestV1ExplicitAPIKeyHeaderWithPlaceholderBearer(t *testing.T) {
	h := newHarness()
	rec := h.do(t, h.v1, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-5.5","messages":[]}`,
		map[string]string{common.EverstackApiKey: liveKey, "Authorization": "Bearer everstack"})
	assertOpenAIChat(t, rec)
	if h.seen.apiKey != liveKey || h.seen.authorization != "" {
		t.Fatalf("gateway saw api key %q, authorization %q", h.seen.apiKey, h.seen.authorization)
	}
	if n := h.store.calls.Load(); n != 1 {
		t.Fatalf("key store consulted %d times, want once (for the header only)", n)
	}

	// The placeholder does not stand in for a bad header.
	h = newHarness()
	rec = h.do(t, h.v1, http.MethodPost, "/v1/chat/completions", `{}`,
		map[string]string{common.EverstackApiKey: revokedKey, "Authorization": "Bearer eyJhbGciOiJFZERTQSJ9.e30.c2ln"})
	if rec.Code != http.StatusUnauthorized || h.seen.called {
		t.Fatalf("revoked header with a JWT bearer: status %d, reached gateway %v", rec.Code, h.seen.called)
	}
}

// Native callers (x-evs-api-key, no bearer) keep the proto-JSON contract they
// were built against.
func TestV1NativeAPIKeyCallersKeepProtoJSON(t *testing.T) {
	h := newHarness()
	rec := h.do(t, h.v1, http.MethodPost, "/v1/chat/completions", `{"model":"gpt-5.5","messages":[]}`,
		map[string]string{common.EverstackApiKey: liveKey})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", rec.Code, rec.Body.String())
	}
	if rec.Body.String() != protoChatFrames {
		t.Fatalf("native response was rewritten: %s", rec.Body.String())
	}
}

// Bearer is accepted only on the OpenAI verbs. The rest of /v1 keeps its own
// auth contract, so a key-shaped bearer there still gets the 401.
func TestV1BearerIsNotAcceptedOutsideTheOpenAIVerbs(t *testing.T) {
	for _, path := range []string{"/v1/gateway/models", "/v1/agents"} {
		h := newHarness()
		rec := h.do(t, h.v1, http.MethodGet, path, "", bearer(liveKey))
		if rec.Code != http.StatusUnauthorized || h.seen.called {
			t.Fatalf("%s: status %d, reached gateway %v", path, rec.Code, h.seen.called)
		}
	}
}

// /openai/v1 is what existing clients were told to use. It keeps working.
func TestOpenAIV1AliasStillServesBearerClients(t *testing.T) {
	h := newHarness()
	rec := h.do(t, h.alias, http.MethodPost, "/openai/v1/chat/completions", `{"model":"gpt-5.5","messages":[]}`, bearer(liveKey))
	assertOpenAIChat(t, rec)
	if h.seen.path != "/v1/chat/completions" || h.seen.apiKey != liveKey || h.seen.authorization != "" {
		t.Fatalf("alias forwarded path %q, api key %q, authorization %q", h.seen.path, h.seen.apiKey, h.seen.authorization)
	}

	h = newHarness()
	rec = h.do(t, h.alias, http.MethodGet, "/openai/v1/models", "", bearer(liveKey))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"object":"list"`) {
		t.Fatalf("alias model list: status %d: %s", rec.Code, rec.Body.String())
	}

	h = newHarness()
	rec = h.do(t, h.alias, http.MethodPost, "/openai/v1/chat/completions", `{}`, bearer(revokedKey))
	if rec.Code != http.StatusUnauthorized || h.seen.called {
		t.Fatalf("alias accepted a revoked key: status %d", rec.Code)
	}
}
