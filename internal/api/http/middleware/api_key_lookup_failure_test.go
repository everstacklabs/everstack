package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/cqrs"
	apikeylib "github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	apikeyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
)

// flakyKeyStore fails its first `failures` lookups the way a database that is
// out of connections does, then answers normally.
type flakyKeyStore struct {
	failures int
	calls    int
}

func (s *flakyKeyStore) QueryType() string { return "GetApiKeyByHash" }

func (s *flakyKeyStore) Handle(_ context.Context, q query.Query) (interface{}, error) {
	s.calls++
	if s.calls <= s.failures {
		return nil, errors.New("failed to execute query: FATAL: sorry, too many clients already (SQLSTATE 53300)")
	}
	if h, _ := apikeylib.HashWithSecret(tenantAKey, hostScopeSecret); h == q.(*apikeyq.GetApiKeyByHashQuery).Hash {
		return apikeyq.APIKeyReadModel{ID: "key-a", OrgID: strp(tenantA)}, nil
	}
	return nil, nil
}

// A lookup that fails says nothing about the key. It is refused as retryable
// (503 with Retry-After). For a short window the database is not asked about
// that key again; after it, the same valid key is looked up again rather than
// refused from a cached "invalid".
func TestAFailedKeyLookupIsNotCachedAsInvalid(t *testing.T) {
	store := &flakyKeyStore{failures: 1}
	interceptor := NewAPIKeyInterceptor(false)
	const window = 50 * time.Millisecond
	interceptor.lookups = apikeylib.NewLookupGuard(window)
	do := func(key string) *httptest.ResponseRecorder {
		handler := interceptor.WithAPIKeyValidation(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set(common.EverstackApiKey, key)
		bus := query.NewQueryBus()
		bus.RegisterHandler(store)
		ctx := contextkeys.WithAPIKeyHashSecret(req.Context(), hostScopeSecret)
		ctx = cqrs.WithSystem(ctx, &cqrs.System{QueryBus: bus})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req.WithContext(ctx))
		return rec
	}

	rec := do(tenantAKey)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("lookup failed: status %d, Retry-After %q; want 503 with Retry-After", rec.Code, rec.Header().Get("Retry-After"))
	}
	if rec := do(tenantAKey); rec.Code != http.StatusServiceUnavailable || store.calls != 1 {
		t.Fatalf("within the failure window: status %d after %d lookups, want 503 without asking again", rec.Code, store.calls)
	}
	time.Sleep(window + 10*time.Millisecond)
	if rec := do(tenantAKey); rec.Code != http.StatusNoContent || store.calls != 2 {
		t.Fatalf("after the database recovered: status %d after %d lookups, want the request through", rec.Code, store.calls)
	}
	if rec := do(tenantAKey); rec.Code != http.StatusNoContent || store.calls != 2 {
		t.Fatalf("warm cache: status %d after %d lookups, want through after 2", rec.Code, store.calls)
	}

	// An unknown key is still refused and remembered as invalid.
	for n := 0; n < 2; n++ {
		if rec := do("sk-mf-svcacct-api01-not-a-key"); rec.Code != http.StatusUnauthorized {
			t.Fatalf("unknown key, request %d: status %d, want 401", n, rec.Code)
		}
	}
	if store.calls != 3 {
		t.Fatalf("unknown key looked up %d times in all, want once (cached)", store.calls-2)
	}
}
