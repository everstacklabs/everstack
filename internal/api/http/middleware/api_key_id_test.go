package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/cqrs"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	apikeyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
)

// Spend attribution (docs/design/spend-limits-v2.md S1): every request that
// authenticates with an API key carries that key's id on its context, on the
// DB path and on the warm cache path alike, so each inference attempt can be
// attributed to the key that made it without another lookup.
func TestAPIKeyIDInstalledOnColdAndWarmPaths(t *testing.T) {
	const keyID = "11111111-2222-3333-4444-555555555555"
	store := &oneKeyStore{key: tenantAKey, read: apikeyq.APIKeyReadModel{ID: keyID, OrgID: strp(tenantA)}}
	interceptor := NewAPIKeyInterceptor(false)
	for n := 0; n < 2; n++ {
		var gotID, gotTenant string
		handler := interceptor.WithAPIKeyValidation(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotID = contextkeys.GetAPIKeyID(r.Context())
			gotTenant = contextkeys.GetTenantID(r.Context())
			w.WriteHeader(http.StatusNoContent)
		}))
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
		req.Header.Set(common.EverstackApiKey, tenantAKey)
		bus := query.NewQueryBus()
		bus.RegisterHandler(store)
		ctx := contextkeys.WithAPIKeyHashSecret(req.Context(), hostScopeSecret)
		ctx = cqrs.WithSystem(ctx, &cqrs.System{QueryBus: bus})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req.WithContext(ctx))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("request %d: status %d", n, rec.Code)
		}
		if gotID != keyID || gotTenant != tenantA {
			t.Fatalf("request %d: api key id %q tenant %q, want %q %q", n, gotID, gotTenant, keyID, tenantA)
		}
	}
	if store.calls != 1 {
		t.Fatalf("key store consulted %d times, want 1 (the second request must hit the cache)", store.calls)
	}
}
