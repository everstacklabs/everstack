package middleware

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/api/common"
	"github.com/everstacklabs/everstack/internal/cqrs"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
)

type missingKeyStore struct{ result interface{} }

func (*missingKeyStore) QueryType() string { return "GetApiKeyByHash" }
func (s *missingKeyStore) Handle(context.Context, query.Query) (interface{}, error) {
	return s.result, nil
}
func TestConnectUnknownKeyNeverBecomesUnrestricted(t *testing.T) {
	for _, result := range []interface{}{nil, "unexpected", struct{}{}} {
		bus := query.NewQueryBus()
		bus.RegisterHandler(&missingKeyStore{result: result})
		ctx := cqrs.WithSystem(contextkeys.WithAPIKeyHashSecret(context.Background(), "test-secret"), &cqrs.System{QueryBus: bus})
		interceptor := NewAPIKeyInterceptor(false)
		header := http.Header{}
		header.Set(common.EverstackApiKey, "unknown-or-revoked-key")
		for attempt := 0; attempt < 2; attempt++ {
			if _, err := interceptor.validateAPIKey(ctx, header, "/everstack.api_key.v1.ApiKeyService/CreateApiKey", ""); connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatalf("result=%T attempt=%d: %v", result, attempt, err)
			}
		}
	}
}
