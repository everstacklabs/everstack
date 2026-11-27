package v1

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

type ownedAgentQueryBus struct{}

func (ownedAgentQueryBus) Execute(_ context.Context, q query.Query) (interface{}, error) {
	lookup, ok := q.(*agentsquery.GetAgentByIDQuery)
	if !ok || lookup.ID != "agent-1" || lookup.TenantID != ownerTenant {
		return &query.Response{Data: nil}, nil
	}
	return &query.Response{Data: &agentsquery.AgentDefinitionReadModel{ID: lookup.ID, TenantID: lookup.TenantID}}, nil
}

func (ownedAgentQueryBus) RegisterHandler(query.QueryHandler) {}

func agentMutationContext(tenantID string, commandBus *recordingCommandBus) context.Context {
	return cqrs.WithSystem(sessionPrincipal(tenantID), &cqrs.System{
		QueryBus:   ownedAgentQueryBus{},
		CommandBus: commandBus,
	})
}

func TestUpdateAndDeleteAgentCheckOwnershipBeforeDispatch(t *testing.T) {
	tests := []struct {
		name string
		call func(*Server, context.Context) error
	}{
		{
			name: "update",
			call: func(s *Server, ctx context.Context) error {
				name := "updated"
				_, err := s.UpdateAgent(ctx, connect.NewRequest(&agentspb.UpdateAgentRequest{Id: "agent-1", Name: &name}))
				return err
			},
		},
		{
			name: "delete",
			call: func(s *Server, ctx context.Context) error {
				_, err := s.DeleteAgent(ctx, connect.NewRequest(&agentspb.DeleteAgentRequest{Id: "agent-1"}))
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" another tenant gets not found", func(t *testing.T) {
			bus := &recordingCommandBus{}
			err := tt.call(&Server{}, agentMutationContext(otherTenant, bus))
			wantNotFound(t, tt.name, err)
			if bus.dispatched != 0 {
				t.Fatalf("commands dispatched = %d", bus.dispatched)
			}
		})

		t.Run(tt.name+" owner dispatches", func(t *testing.T) {
			bus := &recordingCommandBus{}
			if err := tt.call(&Server{}, agentMutationContext(ownerTenant, bus)); err != nil {
				t.Fatalf("owner call: %v", err)
			}
			if bus.dispatched != 1 {
				t.Fatalf("commands dispatched = %d", bus.dispatched)
			}
		})
	}
}
