package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	apikeyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
)

func TestRestrictedHTTPCacheTTLAndInvalidation(t *testing.T) {
	for _, surfaces := range [][]string{nil, {}, {"inference"}} {
		interceptors := []*APIKeyInterceptor{NewAPIKeyInterceptor(false), NewAPIKeyInterceptorWithSessionDB(false, nil)}
		for _, i := range interceptors {
			i.rememberCache("digest", true, "tenant", "cache-test-key", surfaces)
			entry := i.cache["digest"]
			bound := 60 * time.Second
			if surfaces != nil {
				bound = 30 * time.Second
			}
			remaining := time.Until(entry.expiresAt)
			if remaining > bound || remaining < bound-time.Second {
				t.Fatalf("remaining=%s bound=%s", remaining, bound)
			}
			i.rememberCache("unrelated", true, "tenant", "other", nil)
		}
		apikey.InvalidateCaches("", "cache-test-key")
		for _, i := range interceptors {
			if _, _, _, _, _, hit := i.lookupCache("digest"); hit {
				t.Fatal("id invalidation missed cache")
			}
			if _, _, _, _, _, hit := i.lookupCache("unrelated"); !hit {
				t.Fatal("unrelated key evicted")
			}
			i.rememberCache("digest", true, "tenant", "cache-test-key", surfaces)
		}
		apikey.InvalidateCaches("digest", "")
		for _, i := range interceptors {
			if _, _, _, _, _, hit := i.lookupCache("digest"); hit {
				t.Fatal("hash invalidation missed cache")
			}
		}
	}
}
func TestRestrictedHTTPPatchIsImmediate(t *testing.T) {
	store := &oneKeyStore{key: tenantAKey, read: apikeyq.APIKeyReadModel{ID: "cache-test-key", OrgID: strp(tenantA), Surfaces: []string{"inference"}}}
	bus := query.NewQueryBus()
	bus.RegisterHandler(store)
	ctx := cqrs.WithSystem(contextkeys.WithAPIKeyHashSecret(context.Background(), hostScopeSecret), &cqrs.System{QueryBus: bus})
	i := NewAPIKeyInterceptor(false)
	handler := i.WithAPIKeyValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	call := func() int {
		r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
		r.Header.Set(common.EverstackApiKey, tenantAKey)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r.WithContext(ctx))
		return w.Code
	}
	if call() != 204 {
		t.Fatal("inference key refused")
	}
	store.read.Surfaces = []string{"mcp"}
	apikey.InvalidateCaches("", "cache-test-key")
	if call() != 403 || store.calls != 2 {
		t.Fatal("stale surfaces admitted inference")
	}
}

type invalidatingHTTPStore struct{}

func (invalidatingHTTPStore) QueryType() string { return "GetApiKeyByHash" }
func (invalidatingHTTPStore) Handle(context.Context, query.Query) (interface{}, error) {
	apikey.InvalidateCaches("", "cache-test-key")
	tenant := "tenant"
	return apikeyq.APIKeyReadModel{ID: "cache-test-key", OrgID: &tenant, Surfaces: []string{"inference"}}, nil
}
func TestRestrictedHTTPCacheCannotRepopulateAfterInvalidation(t *testing.T) {
	bus := query.NewQueryBus()
	bus.RegisterHandler(invalidatingHTTPStore{})
	ctx := cqrs.WithSystem(contextkeys.WithAPIKeyHashSecret(context.Background(), hostScopeSecret), &cqrs.System{QueryBus: bus})
	i := NewAPIKeyInterceptor(false)
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	r.Header.Set(common.EverstackApiKey, tenantAKey)
	w := httptest.NewRecorder()
	i.WithAPIKeyValidation(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("stale lookup reached handler") })).ServeHTTP(w, r.WithContext(ctx))
	if w.Code != 401 {
		t.Fatalf("status %d", w.Code)
	}
	if len(i.cache) != 0 {
		t.Fatal("stale lookup cached")
	}
}
