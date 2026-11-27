package middleware

import (
	"context"
	"net/http"
	"testing"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	apikeyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
)

// Spend attribution (docs/design/spend-limits-v2.md S1): the Connect
// interceptor installs the verified key's id on the cold (DB) path and on
// the warm cache path.
type keyIDStore struct{ calls int }

func (s *keyIDStore) QueryType() string { return "GetApiKeyByHash" }

func (s *keyIDStore) Handle(_ context.Context, q query.Query) (interface{}, error) {
	s.calls++
	if h, _ := apikey.HashWithSecret(connectKeyA, connectHostSecret); h == q.(*apikeyq.GetApiKeyByHashQuery).Hash {
		tenant := connectTenantA
		return apikeyq.APIKeyReadModel{ID: "key-id-a", OrgID: &tenant}, nil
	}
	return nil, nil
}

func TestConnectAPIKeyIDInstalledOnColdAndWarmPaths(t *testing.T) {
	i := NewAPIKeyInterceptorWithPolicy(false, nil)
	store := &keyIDStore{}
	for n := 0; n < 2; n++ {
		bus := query.NewQueryBus()
		bus.RegisterHandler(store)
		ctx := contextkeys.WithAPIKeyHashSecret(context.Background(), connectHostSecret)
		ctx = cqrs.WithSystem(ctx, &cqrs.System{QueryBus: bus})
		header := http.Header{}
		header.Set(common.EverstackApiKey, connectKeyA)
		ctx, err := i.validateAPIKey(ctx, header, connectProc, "corr")
		if err != nil {
			t.Fatalf("request %d: %v", n, err)
		}
		if got := contextkeys.GetAPIKeyID(ctx); got != "key-id-a" {
			t.Fatalf("request %d: api key id %q, want key-id-a", n, got)
		}
		if got := contextkeys.GetTenantID(ctx); got != connectTenantA {
			t.Fatalf("request %d: tenant %q", n, got)
		}
	}
	if store.calls != 1 {
		t.Fatalf("key store consulted %d times, want 1", store.calls)
	}
}
