package middleware

import (
	"connectrpc.com/connect"
	"context"
	"github.com/everstacklabs/everstack/internal/api/common"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"net/http"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	"github.com/everstacklabs/everstack/internal/query"
	apikeyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
)

type cacheSurfaceStore struct {
	rpm      *int
	surfaces []string
	during   func()
	calls    int
}

func (*cacheSurfaceStore) QueryType() string { return "GetApiKeyByHash" }
func (s *cacheSurfaceStore) Handle(context.Context, query.Query) (interface{}, error) {
	s.calls++
	if s.during != nil {
		s.during()
	}
	tenant := "tenant"
	return apikeyq.APIKeyReadModel{ID: "cache-test-key", OrgID: &tenant, Surfaces: s.surfaces, RateLimitRPM: s.rpm}, nil
}
func cacheContext(s *cacheSurfaceStore) context.Context {
	bus := query.NewQueryBus()
	bus.RegisterHandler(s)
	return cqrs.WithSystem(context.Background(), &cqrs.System{QueryBus: bus})
}

func TestRestrictedConnectCacheTTLAndInvalidation(t *testing.T) {
	for _, surfaces := range [][]string{nil, {}, {"inference"}} {
		store := &cacheSurfaceStore{surfaces: surfaces}
		ctx := cacheContext(store)
		interceptors := []*APIKeyConnectInterceptor{NewAPIKeyInterceptor(false), NewAPIKeyInterceptorWithSessionDB(false, nil, nil)}
		for _, i := range interceptors {
			if _, _, _, _, valid, _ := i.validateCached(ctx, "key", "digest"); !valid {
				t.Fatal("lookup failed")
			}
			cached, _ := i.cache.Load("digest")
			entry := cached.(cacheEntry)
			bound := cacheExpiration
			if surfaces != nil {
				bound = 30 * time.Second
			}
			remaining := time.Until(entry.expiresAt)
			if remaining > bound || remaining < bound-time.Second {
				t.Fatalf("cache remaining %s, want %s", remaining, bound)
			}
			i.cache.Store("unrelated", cacheEntry{keyID: "other", valid: true, expiresAt: time.Now().Add(time.Hour)})
		}
		apikey.InvalidateCaches("", "cache-test-key")
		for _, i := range interceptors {
			if _, exists := i.cache.Load("digest"); exists {
				t.Fatal("id invalidation missed cache")
			}
			if _, exists := i.cache.Load("unrelated"); !exists {
				t.Fatal("unrelated key evicted")
			}
			i.validateCached(ctx, "key", "digest")
		}
		apikey.InvalidateCaches("digest", "")
		for _, i := range interceptors {
			if _, exists := i.cache.Load("digest"); exists {
				t.Fatal("hash invalidation missed cache")
			}
		}
	}
}
func TestRestrictedConnectCacheCannotRepopulateAfterInvalidation(t *testing.T) {
	store := &cacheSurfaceStore{surfaces: []string{"inference"}, during: func() { apikey.InvalidateCaches("", "cache-test-key") }}
	i := NewAPIKeyInterceptor(false)
	if _, _, _, _, valid, _ := i.validateCached(cacheContext(store), "key", "digest"); valid {
		t.Fatal("pre-change lookup authenticated after invalidation")
	}
	if _, exists := i.cache.Load("digest"); exists {
		t.Fatal("stale lookup repopulated cache")
	}
}

func TestRestrictedConnectPatchIsImmediate(t *testing.T) {
	store := &cacheSurfaceStore{surfaces: []string{"inference"}}
	ctx := contextkeys.WithAPIKeyHashSecret(cacheContext(store), "test-secret")
	interceptor := NewAPIKeyInterceptor(false)
	header := http.Header{}
	header.Set(common.EverstackApiKey, "key")
	procedure := "/everstack.gateway.v1.GatewayService/ListModels"
	if _, err := interceptor.validateAPIKey(ctx, header, procedure, ""); err != nil {
		t.Fatal(err)
	}
	store.surfaces = []string{"mcp"}
	apikey.InvalidateCaches("", "cache-test-key")
	if _, err := interceptor.validateAPIKey(ctx, header, procedure, ""); connect.CodeOf(err) != connect.CodePermissionDenied || store.calls != 2 {
		t.Fatalf("stale surfaces admitted inference: %v calls=%d", err, store.calls)
	}
}
