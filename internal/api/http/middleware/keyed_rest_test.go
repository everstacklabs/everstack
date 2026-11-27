package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/cqrs"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	apikeyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
	"github.com/gorilla/mux"
)

func TestRawRESTKeyCannotUseSandboxFallback(t *testing.T) {
	for _, surfaces := range [][]string{nil, {}, {"inference", "mcp", "telemetry", "a2a"}} {
		for _, path := range []string{"/v1/sandboxes/events", "/v1/sandbox/session/shell", "/v1/sandbox/session/browser", "/v1/settings/otlp", "/v1/openai-agents"} {
			for _, bearer := range []bool{false, true} {
				interceptor := NewAPIKeyInterceptor(false)
				store := &oneKeyStore{key: tenantAKey, read: apikeyq.APIKeyReadModel{ID: "key", OrgID: strp(tenantA), Surfaces: surfaces}}
				bus := query.NewQueryBus()
				bus.RegisterHandler(store)
				ctx := cqrs.WithSystem(contextkeys.WithAPIKeyHashSecret(context.Background(), hostScopeSecret), &cqrs.System{QueryBus: bus})
				// A raw handler's local-tenant fallback is not authentication evidence.
				ctx = contextkeys.WithTenantID(ctx, tenantA)
				reached := false
				r := mux.NewRouter()
				r.Use(interceptor.WithKeyedRESTValidation)
				r.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) { reached = true; w.WriteHeader(204) })
				request := httptest.NewRequest("GET", path, nil)
				if bearer {
					request.Header.Set("Authorization", "Bearer "+tenantAKey)
				} else {
					request.Header.Set(common.EverstackApiKey, tenantAKey)
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, request.WithContext(ctx))
				if reached != (surfaces == nil) || surfaces != nil && w.Code != 403 {
					t.Fatalf("set=%v path=%s bearer=%v: %d", surfaces, path, bearer, w.Code)
				}
			}
		}
	}
}
func TestRawRESTGuardLeavesSessionSSHAndInteropPathsToHandlers(t *testing.T) {
	interceptor := NewAPIKeyInterceptor(false)
	called := 0
	handler := interceptor.WithKeyedRESTValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called++ }))
	for _, path := range []string{"/v1/sandbox/session/shell?ts=1&sig=ssh", "/api/interop/remotes", "/api/bots"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", path, nil))
	}
	if called != 3 {
		t.Fatal("non-key paths intercepted")
	}
}

func TestRawRESTGuardPreservesOpenAIBearerTransport(t *testing.T) {
	interceptor := NewAPIKeyInterceptor(false)
	store := &oneKeyStore{key: tenantAKey, read: apikeyq.APIKeyReadModel{ID: "key", OrgID: strp(tenantA), Surfaces: []string{"inference"}}}
	bus := query.NewQueryBus()
	bus.RegisterHandler(store)
	ctx := cqrs.WithSystem(contextkeys.WithAPIKeyHashSecret(context.Background(), hostScopeSecret), &cqrs.System{QueryBus: bus})
	called := false
	handler := interceptor.WithKeyedRESTValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Header.Get("Authorization") != "Bearer "+tenantAKey || r.Header.Get(common.EverstackApiKey) != "" {
			t.Fatal("guard changed OpenAI response-shaping signal")
		}
		if contextkeys.GetAPIKeyHash(r.Context()) == "" || contextkeys.GetAPIKeySurfaces(r.Context()) == nil {
			t.Fatal("verified identity missing")
		}
	}))
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set("Authorization", "Bearer "+tenantAKey)
	handler.ServeHTTP(httptest.NewRecorder(), r.WithContext(ctx))
	if !called {
		t.Fatal("inference refused")
	}
}

func TestRawRESTGuardExplicitKeyWinsOverSDKBearer(t *testing.T) {
	for _, header := range []string{common.EverstackApiKey, common.LegacyMFApiKey, common.LegacyEverstackApiKey} {
		interceptor := NewAPIKeyInterceptor(false)
		store := &oneKeyStore{key: tenantAKey, read: apikeyq.APIKeyReadModel{OrgID: strp(tenantA), Surfaces: []string{"inference"}}}
		bus := query.NewQueryBus()
		bus.RegisterHandler(store)
		ctx := cqrs.WithSystem(contextkeys.WithAPIKeyHashSecret(context.Background(), hostScopeSecret), &cqrs.System{QueryBus: bus})
		called := false
		handler := interceptor.WithKeyedRESTValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			called = true
			if r.Header.Get("Authorization") != "Bearer everstack" {
				t.Fatal("original SDK signal lost")
			}
		}))
		r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		r.Header.Set(header, tenantAKey)
		r.Header.Set("Authorization", "Bearer everstack")
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r.WithContext(ctx))
		if !called {
			t.Fatalf("%s rejected: %d %s", header, w.Code, w.Body.String())
		}
	}
}

// The Messages route authenticates inside its own chain so that refusals come
// back in Anthropic's error shape. The guard must hand it through untouched,
// with the caller's original credential headers, whatever key is presented.
func TestRawRESTGuardLeavesMessagesToItsOwnChain(t *testing.T) {
	interceptor := NewAPIKeyInterceptor(false)
	for _, path := range []string{"/v1/messages", "/v1/messages/count_tokens"} {
		for _, header := range []string{"x-api-key", "Authorization", common.EverstackApiKey} {
			var got *http.Request
			r := mux.NewRouter()
			r.Use(interceptor.WithKeyedRESTValidation)
			r.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) { got = r; w.WriteHeader(204) })
			request := httptest.NewRequest("POST", path, nil)
			value := tenantAKey + "-wrong"
			if header == "Authorization" {
				value = "Bearer " + value
			}
			request.Header.Set(header, value)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, request)
			if got == nil || w.Code != 204 {
				t.Fatalf("%s with %s: the guard answered %d instead of the route", path, header, w.Code)
			}
			if got.Header.Get(header) != value {
				t.Fatalf("%s with %s: credential header was altered", path, header)
			}
		}
	}
}
