package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/cqrs"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	apikeyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
)

func TestBotSurfacesHTTPExactRoutes(t *testing.T) {
	cases := []struct {
		method, path string
		allowed      bool
	}{
		{"POST", "/v1/chat/completions", true}, {"POST", "/v1/responses", true}, {"POST", "/v1/embeddings", true}, {"POST", "/v1/messages", true}, {"GET", "/v1/models", true}, {"GET", "/v1/gateway/models", true},
		{"GET", "/v1/agents", false}, {"POST", "/v1/api-keys", false},
		{"POST", "/v1/chat/completions/../api-keys", false}, {"GET", "/v1/gateway/models/x", false},
		{"GET", "/v1/chat/completions", false}, {"POST", "/v1/models", false}, {"DELETE", "/v1/responses", false},
		{"POST", "/v1/chat/completions/", false}, {"POST", "/v1/responses/x/cancel", false},
	}
	for _, surfaces := range [][]string{nil, {}, {"mcp"}, {"inference", "mcp", "telemetry", "a2a"}} {
		for _, alias := range []string{"", "/openai"} {
			for _, tc := range cases {
				for _, mode := range []string{"cold", "warm", "context"} {
					interceptor := NewAPIKeyInterceptor(false)
					store := &oneKeyStore{key: tenantAKey, read: apikeyq.APIKeyReadModel{ID: "key", OrgID: strp(tenantA), Surfaces: surfaces}}
					bus := query.NewQueryBus()
					bus.RegisterHandler(store)
					ctx := cqrs.WithSystem(contextkeys.WithAPIKeyHashSecret(context.Background(), hostScopeSecret), &cqrs.System{QueryBus: bus})
					if mode == "warm" { // Prime via the actual middleware, rather than a fabricated cache record.
						prime := httptest.NewRequest("POST", "/v1/chat/completions", nil)
						prime.Header.Set(common.EverstackApiKey, tenantAKey)
						interceptor.WithAPIKeyValidation(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(httptest.NewRecorder(), prime.WithContext(ctx))
					}
					if mode == "context" {
						ctx = contextkeys.WithAPIKeySurfaces(contextkeys.WithAuthenticatedAPIKeyID(ctx, tenantA, "digest", "key"), surfaces)
					}
					reached := false
					request := httptest.NewRequest(tc.method, alias+tc.path, nil)
					request.Header.Set(common.EverstackApiKey, tenantAKey)
					rec := httptest.NewRecorder()
					interceptor.WithAPIKeyValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true; w.WriteHeader(204) })).ServeHTTP(rec, request.WithContext(ctx))
					allowed := surfaces == nil || len(surfaces) == 4 && tc.allowed
					if reached != allowed || !allowed && (rec.Code != 403 || !strings.Contains(rec.Body.String(), "this API key cannot call this API")) {
						t.Fatalf("surfaces=%v %s %s mode=%s: %d %s", surfaces, tc.method, alias+tc.path, mode, rec.Code, rec.Body.String())
					}
				}
			}
		}
	}
}

func TestHTTPSurfaceCacheExpires(t *testing.T) {
	interceptor := NewAPIKeyInterceptor(false)
	interceptor.cacheTTL = time.Millisecond
	interceptor.rememberCache("digest", true, "tenant", "id", []string{"inference"})
	if _, _, surfaces, _, valid, hit := interceptor.lookupCache("digest"); !hit || !valid || len(surfaces) != 1 {
		t.Fatal("set not cached")
	}
	time.Sleep(3 * time.Millisecond)
	if _, _, _, _, _, hit := interceptor.lookupCache("digest"); hit {
		t.Fatal("stale surfaces survived TTL")
	}
}

type malformedKeyStore struct{ result interface{} }

func (*malformedKeyStore) QueryType() string { return "GetApiKeyByHash" }
func (s *malformedKeyStore) Handle(context.Context, query.Query) (interface{}, error) {
	return s.result, nil
}
func TestHTTPUnknownKeyNeverBecomesUnrestricted(t *testing.T) {
	for _, result := range []interface{}{nil, "unexpected", struct{}{}} {
		bus := query.NewQueryBus()
		bus.RegisterHandler(&malformedKeyStore{result: result})
		ctx := cqrs.WithSystem(contextkeys.WithAPIKeyHashSecret(context.Background(), "test-secret"), &cqrs.System{QueryBus: bus})
		interceptor := NewAPIKeyInterceptor(false)
		handler := interceptor.WithAPIKeyValidation(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("invalid identity reached handler") }))
		for attempt := 0; attempt < 2; attempt++ {
			r := httptest.NewRequest("POST", "/v1/api-keys", nil)
			r.Header.Set(common.EverstackApiKey, "unknown-or-revoked-key")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r.WithContext(ctx))
			if w.Code != 401 {
				t.Fatalf("result=%T status=%d", result, w.Code)
			}
		}
	}
}
