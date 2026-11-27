package middleware

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"

	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/lib/apikey"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	apikeyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
)

// Connect counterpart of the HTTP interceptor's host-scope tests: a key bound
// to tenant A presented on tenant B's hostname is refused with the same error
// as an unknown key.

const (
	connectHostSecret = "connect-host-scope-secret"
	connectKeyA       = "sk-" + "mf-svcacct-api01-connect-tenant-a"
	connectTenantA    = "aaaaaaaa-1111-0000-0000-00000000000a"
	connectTenantB    = "bbbbbbbb-1111-0000-0000-00000000000b"
	connectProc       = "/everstack.storage.v1.ObjectStorageService/ListObjects"
)

type connectKeyStore struct{ calls int }

func (s *connectKeyStore) QueryType() string { return "GetApiKeyByHash" }

func (s *connectKeyStore) Handle(_ context.Context, q query.Query) (interface{}, error) {
	s.calls++
	if h, _ := apikey.HashWithSecret(connectKeyA, connectHostSecret); h == q.(*apikeyq.GetApiKeyByHashQuery).Hash {
		tenant := connectTenantA
		return apikeyq.APIKeyReadModel{OrgID: &tenant}, nil
	}
	return nil, nil
}

func connectValidate(t *testing.T, i *APIKeyConnectInterceptor, store *connectKeyStore, key string, scope *contextkeys.RequestInstanceScope) (context.Context, error) {
	t.Helper()
	bus := query.NewQueryBus()
	bus.RegisterHandler(store)
	ctx := contextkeys.WithAPIKeyHashSecret(context.Background(), connectHostSecret)
	ctx = cqrs.WithSystem(ctx, &cqrs.System{QueryBus: bus})
	if scope != nil {
		ctx = contextkeys.WithHostSelectedTenant(ctx, *scope)
	}
	header := http.Header{}
	header.Set(common.EverstackApiKey, key)
	return i.validateAPIKey(ctx, header, connectProc, "corr")
}

func TestConnectAPIKeyHostScope(t *testing.T) {
	i := NewAPIKeyInterceptorWithPolicy(false, nil)
	store := &connectKeyStore{}
	hostA := &contextkeys.RequestInstanceScope{InstanceID: connectTenantA, OrganizationID: "0a0a0a0a-1111-0000-0000-0000000000a0"}
	hostB := &contextkeys.RequestInstanceScope{InstanceID: connectTenantB, OrganizationID: "0b0b0b0b-1111-0000-0000-0000000000b0"}

	_, invalidErr := connectValidate(t, i, store, "sk-mf-svcacct-api01-never-issued", hostB)
	if connect.CodeOf(invalidErr) != connect.CodeUnauthenticated {
		t.Fatalf("unknown key: %v", invalidErr)
	}

	// Cold (DB) then warm (cache): refused on B's host, same error as an
	// unknown key.
	for n := 0; n < 2; n++ {
		_, err := connectValidate(t, i, store, connectKeyA, hostB)
		if connect.CodeOf(err) != connect.CodeOf(invalidErr) || err.Error() != invalidErr.Error() {
			t.Fatalf("attempt %d on another tenant's host: err %v, want %v", n, err, invalidErr)
		}
	}

	// Still valid on its own host and with no host scope at all: the refusal
	// did not poison the cache.
	for name, scope := range map[string]*contextkeys.RequestInstanceScope{"own host": hostA, "no host scope": nil} {
		ctx, err := connectValidate(t, i, store, connectKeyA, scope)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := contextkeys.GetTenantID(ctx); got != connectTenantA {
			t.Fatalf("%s: tenant %q, want %q", name, got, connectTenantA)
		}
	}
}
