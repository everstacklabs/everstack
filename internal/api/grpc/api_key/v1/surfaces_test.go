package v1

import (
	"connectrpc.com/connect"
	"context"
	"github.com/everstacklabs/everstack/internal/cqrs"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/query"
	apikeyq "github.com/everstacklabs/everstack/internal/query/handlers/api_key"
	pb "github.com/everstacklabs/everstack/pkg/grpc/everstack/api_key/v1"
	"testing"
)

type keyByIDStub struct{ surfaces []string }

func (*keyByIDStub) QueryType() string { return "GetApiKeyByID" }
func (s *keyByIDStub) Handle(context.Context, query.Query) (interface{}, error) {
	return apikeyq.APIKeyReadModel{ID: "key", Surfaces: s.surfaces}, nil
}
func TestPublicDeleteRefusesEvenEmptyBotSet(t *testing.T) {
	for _, surfaces := range [][]string{{}, {"mcp"}} {
		bus := query.NewQueryBus()
		bus.RegisterHandler(&keyByIDStub{surfaces: surfaces})
		ctx := cqrs.WithSystem(contextkeys.WithTenantID(context.Background(), "tenant"), &cqrs.System{QueryBus: bus})
		_, err := (&Server{}).DeleteApiKey(ctx, connect.NewRequest(&pb.DeleteApiKeyRequest{Id: "key"}))
		if connect.CodeOf(err) != connect.CodeFailedPrecondition || err.(*connect.Error).Message() != "this key belongs to a bot; remove the bot instead" {
			t.Fatalf("delete: %v", err)
		}
	}
}
