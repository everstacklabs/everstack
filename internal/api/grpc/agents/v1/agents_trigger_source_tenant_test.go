package v1

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/agents/trigger"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

type triggerAgentQueryBus struct{}

func (triggerAgentQueryBus) Execute(_ context.Context, q query.Query) (interface{}, error) {
	lookup, ok := q.(*agentsquery.GetAgentByIDQuery)
	if !ok || lookup.TenantID != ownerTenant || (lookup.ID != "agent-1" && lookup.ID != "source-1") {
		return &query.Response{Data: nil}, nil
	}
	return &query.Response{Data: &agentsquery.AgentDefinitionReadModel{ID: lookup.ID, TenantID: lookup.TenantID}}, nil
}

func (triggerAgentQueryBus) RegisterHandler(query.QueryHandler) {}

type triggerOwnershipStore struct {
	trigger.Store
	listCalls   int
	createCalls int
	updateCalls int
	deleteCalls int
}

func (s *triggerOwnershipStore) ListTriggers(context.Context, string, string) ([]*trigger.Trigger, error) {
	s.listCalls++
	return nil, nil
}

func (s *triggerOwnershipStore) CreateTrigger(_ context.Context, value *trigger.Trigger) error {
	s.createCalls++
	value.ID = "trigger-1"
	return nil
}

func (s *triggerOwnershipStore) GetTrigger(_ context.Context, id, tenantID string) (*trigger.Trigger, error) {
	if id != "trigger-1" || tenantID != ownerTenant {
		return nil, errors.New("not found")
	}
	return &trigger.Trigger{
		ID:                 id,
		TenantID:           tenantID,
		AgentID:            "agent-1",
		Name:               "event",
		Type:               trigger.TriggerEvent,
		Enabled:            true,
		EventSourceAgentID: "source-1",
		EventType:          "session.end",
	}, nil
}

func (s *triggerOwnershipStore) UpdateTrigger(_ context.Context, _ *trigger.Trigger) error {
	s.updateCalls++
	return nil
}

func (s *triggerOwnershipStore) DeleteTrigger(context.Context, string, string) error {
	s.deleteCalls++
	return nil
}

func triggerOwnershipContext(tenantID string) context.Context {
	return cqrs.WithSystem(sessionPrincipal(tenantID), &cqrs.System{QueryBus: triggerAgentQueryBus{}})
}

func createEventTriggerRequest(sourceAgentID string) *connect.Request[agentspb.CreateAgentTriggerRequest] {
	return connect.NewRequest(&agentspb.CreateAgentTriggerRequest{
		AgentId:            "agent-1",
		Name:               "event",
		TriggerType:        "event",
		EventSourceAgentId: sourceAgentID,
		EventType:          "session.end",
	})
}

func TestCreateAgentTriggerRequiresOwnedTargetAndEventSourceAgents(t *testing.T) {
	t.Run("foreign source is not found before store access", func(t *testing.T) {
		store := &triggerOwnershipStore{}
		s := &Server{triggerStore: store}
		_, err := s.CreateAgentTrigger(triggerOwnershipContext(ownerTenant), createEventTriggerRequest("foreign-source"))
		wantNotFound(t, "CreateAgentTrigger", err)
		if store.listCalls != 0 || store.createCalls != 0 {
			t.Fatalf("list calls = %d, create calls = %d", store.listCalls, store.createCalls)
		}
	})

	t.Run("foreign target is not found before store access", func(t *testing.T) {
		store := &triggerOwnershipStore{}
		s := &Server{triggerStore: store}
		req := createEventTriggerRequest("source-1")
		req.Msg.AgentId = "foreign-agent"
		_, err := s.CreateAgentTrigger(triggerOwnershipContext(ownerTenant), req)
		wantNotFound(t, "CreateAgentTrigger", err)
		if store.listCalls != 0 || store.createCalls != 0 {
			t.Fatalf("list calls = %d, create calls = %d", store.listCalls, store.createCalls)
		}
	})

	t.Run("owner creates the trigger", func(t *testing.T) {
		store := &triggerOwnershipStore{}
		s := &Server{triggerStore: store}
		if _, err := s.CreateAgentTrigger(triggerOwnershipContext(ownerTenant), createEventTriggerRequest("source-1")); err != nil {
			t.Fatalf("owner CreateAgentTrigger: %v", err)
		}
		if store.listCalls != 1 || store.createCalls != 1 {
			t.Fatalf("list calls = %d, create calls = %d", store.listCalls, store.createCalls)
		}
	})
}

func TestUpdateAgentTriggerRequiresOwnedEventSourceAgent(t *testing.T) {
	t.Run("foreign source is not found before update", func(t *testing.T) {
		store := &triggerOwnershipStore{}
		s := &Server{triggerStore: store}
		_, err := s.UpdateAgentTrigger(triggerOwnershipContext(ownerTenant), connect.NewRequest(&agentspb.UpdateAgentTriggerRequest{
			Id: "trigger-1", EventSourceAgentId: "foreign-source",
		}))
		wantNotFound(t, "UpdateAgentTrigger", err)
		if store.updateCalls != 0 {
			t.Fatalf("update calls = %d", store.updateCalls)
		}
	})

	t.Run("owner updates the source", func(t *testing.T) {
		store := &triggerOwnershipStore{}
		s := &Server{triggerStore: store}
		if _, err := s.UpdateAgentTrigger(triggerOwnershipContext(ownerTenant), connect.NewRequest(&agentspb.UpdateAgentTriggerRequest{
			Id: "trigger-1", EventSourceAgentId: "source-1",
		})); err != nil {
			t.Fatalf("owner UpdateAgentTrigger: %v", err)
		}
		if store.updateCalls != 1 {
			t.Fatalf("update calls = %d", store.updateCalls)
		}
	})
}

func TestListAgentTriggersRequiresOwnedAgent(t *testing.T) {
	store := &triggerOwnershipStore{}
	s := &Server{triggerStore: store}
	_, err := s.ListAgentTriggers(triggerOwnershipContext(ownerTenant), connect.NewRequest(&agentspb.ListAgentTriggersRequest{AgentId: "foreign-agent"}))
	wantNotFound(t, "ListAgentTriggers", err)
	if store.listCalls != 0 {
		t.Fatalf("list calls = %d", store.listCalls)
	}

	if _, err := s.ListAgentTriggers(triggerOwnershipContext(ownerTenant), connect.NewRequest(&agentspb.ListAgentTriggersRequest{AgentId: "agent-1"})); err != nil {
		t.Fatalf("owner ListAgentTriggers: %v", err)
	}
	if store.listCalls != 1 {
		t.Fatalf("list calls = %d", store.listCalls)
	}
}

func TestDeleteAgentTriggerRequiresOwnedTrigger(t *testing.T) {
	t.Run("foreign trigger is not found before delete", func(t *testing.T) {
		store := &triggerOwnershipStore{}
		s := &Server{triggerStore: store}
		_, err := s.DeleteAgentTrigger(triggerOwnershipContext(ownerTenant), connect.NewRequest(&agentspb.DeleteAgentTriggerRequest{Id: "foreign-trigger"}))
		wantNotFound(t, "DeleteAgentTrigger", err)
		if store.deleteCalls != 0 {
			t.Fatalf("delete calls = %d", store.deleteCalls)
		}
	})

	t.Run("owner deletes the trigger", func(t *testing.T) {
		store := &triggerOwnershipStore{}
		s := &Server{triggerStore: store}
		if _, err := s.DeleteAgentTrigger(triggerOwnershipContext(ownerTenant), connect.NewRequest(&agentspb.DeleteAgentTriggerRequest{Id: "trigger-1"})); err != nil {
			t.Fatalf("owner DeleteAgentTrigger: %v", err)
		}
		if store.deleteCalls != 1 {
			t.Fatalf("delete calls = %d", store.deleteCalls)
		}
	})
}
