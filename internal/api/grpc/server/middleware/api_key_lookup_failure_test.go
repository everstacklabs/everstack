package middleware

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	apikeyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
)

type flakyConnectKeyStore struct {
	failures int
	calls    int
}

func (s *flakyConnectKeyStore) QueryType() string { return "GetApiKeyByHash" }

func (s *flakyConnectKeyStore) Handle(_ context.Context, q query.Query) (interface{}, error) {
	s.calls++
	if s.calls <= s.failures {
		return nil, errors.New("failed to execute query: FATAL: sorry, too many clients already (SQLSTATE 53300)")
	}
	if h, _ := apikey.HashWithSecret(connectKeyA, connectHostSecret); h == q.(*apikeyq.GetApiKeyByHashQuery).Hash {
		tenant := connectTenantA
		return apikeyq.APIKeyReadModel{ID: "key-id-a", OrgID: &tenant}, nil
	}
	return nil, nil
}

// A failed lookup is refused as Unavailable and not cached as invalid. For a
// short window the database is not asked again; after it the same valid key
// works. An unknown key is still Unauthenticated and cached.
func TestConnectFailedKeyLookupIsNotCachedAsInvalid(t *testing.T) {
	i := NewAPIKeyInterceptorWithPolicy(false, nil)
	const window = 50 * time.Millisecond
	i.lookups = apikey.NewLookupGuard(window)
	store := &flakyConnectKeyStore{failures: 1}
	do := func(key string) error {
		bus := query.NewQueryBus()
		bus.RegisterHandler(store)
		ctx := contextkeys.WithAPIKeyHashSecret(context.Background(), connectHostSecret)
		ctx = cqrs.WithSystem(ctx, &cqrs.System{QueryBus: bus})
		header := http.Header{}
		header.Set(common.EverstackApiKey, key)
		_, err := i.validateAPIKey(ctx, header, connectProc, "corr")
		return err
	}
	if err := do(connectKeyA); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("lookup failed: %v, want Unavailable", err)
	}
	if err := do(connectKeyA); connect.CodeOf(err) != connect.CodeUnavailable || store.calls != 1 {
		t.Fatalf("within the failure window: %v after %d lookups, want Unavailable without asking again", err, store.calls)
	}
	time.Sleep(window + 10*time.Millisecond)
	if err := do(connectKeyA); err != nil {
		t.Fatalf("after the database recovered: %v", err)
	}
	if err := do(connectKeyA); err != nil || store.calls != 2 {
		t.Fatalf("warm cache: %v after %d lookups", err, store.calls)
	}
	for n := 0; n < 2; n++ {
		if err := do("sk-mf-svcacct-api01-not-a-key"); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("unknown key, request %d: %v, want Unauthenticated", n, err)
		}
	}
	if store.calls != 3 {
		t.Fatalf("unknown key looked up %d times, want once", store.calls-2)
	}
}

// The query bus wraps "no such key" in a non-nil *query.Response. On a
// self-hosted gateway, with no tenant host to narrow it, a key that was never
// issued used to pass this check with an empty tenant, and a chat completion
// made with it reached the provider.
func TestConnectNeverIssuedKeyIsRefusedWithoutAHostScope(t *testing.T) {
	i := NewAPIKeyInterceptorWithPolicy(false, nil)
	bus := query.NewQueryBus()
	bus.RegisterHandler(&keyIDStore{})
	ctx := contextkeys.WithAPIKeyHashSecret(context.Background(), connectHostSecret)
	ctx = cqrs.WithSystem(ctx, &cqrs.System{QueryBus: bus})
	header := http.Header{}
	header.Set(common.EverstackApiKey, "sk-garbage-never-issued-0000")
	for n := 0; n < 2; n++ { // cold, then from the cache
		if _, err := i.validateAPIKey(ctx, header, "/everstack.gateway.v1.GatewayService/ChatCompletion", "corr"); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("request %d: %v, want Unauthenticated", n, err)
		}
	}
}
