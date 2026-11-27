package v1

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	agentdeploy "github.com/everstacklabs/everstack/internal/agents/deployment"
	agentmem "github.com/everstacklabs/everstack/internal/agents/memory"
	agentrevision "github.com/everstacklabs/everstack/internal/agents/revision"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

type childDeploymentStore struct {
	agentdeploy.Store
	listCalls   int
	createCalls int
}

func (s *childDeploymentStore) ListDeployments(context.Context, string, string, int, int) ([]*agentdeploy.Deployment, int, error) {
	s.listCalls++
	return nil, 0, nil
}

func (s *childDeploymentStore) CreateDeployment(_ context.Context, deployment *agentdeploy.Deployment) error {
	s.createCalls++
	deployment.ID = "deployment-1"
	return nil
}

type childMemoryStore struct {
	agentmem.Store
	reads  int
	writes int
	lists  int
}

func (s *childMemoryStore) Get(_ context.Context, tenantID, id string) (*agentmem.AgentMemory, error) {
	s.reads++
	if tenantID != ownerTenant || id != "memory-1" {
		return nil, errors.New("not found")
	}
	return &agentmem.AgentMemory{ID: id, TenantID: tenantID, AgentID: "agent-1", IsActive: true}, nil
}

func (s *childMemoryStore) List(context.Context, agentmem.ListOptions) ([]*agentmem.AgentMemory, error) {
	s.lists++
	return nil, nil
}

func (s *childMemoryStore) Count(context.Context, agentmem.ListOptions) (int64, error) {
	s.lists++
	return 0, nil
}

func (s *childMemoryStore) Save(_ context.Context, memory *agentmem.AgentMemory) error {
	s.writes++
	memory.ID = "memory-1"
	return nil
}

func (s *childMemoryStore) Update(context.Context, string, string, string, *string, float64) error {
	s.writes++
	return nil
}

func (s *childMemoryStore) Deactivate(context.Context, string, string) error {
	s.writes++
	return nil
}

func (s *childMemoryStore) Delete(context.Context, string, string) error {
	s.writes++
	return nil
}

type childRevisionStore struct {
	agentrevision.Store
	createCalls int
	activeCalls int
}

func (s *childRevisionStore) CreateAndActivate(context.Context, string, string, string, *agentrevision.Manifest) (*agentrevision.Revision, bool, error) {
	s.createCalls++
	return &agentrevision.Revision{ID: "revision-1", TenantID: ownerTenant, AgentID: "agent-1"}, true, nil
}

func (s *childRevisionStore) GetActive(context.Context, string, string) (*agentrevision.Revision, error) {
	s.activeCalls++
	return &agentrevision.Revision{ID: "revision-1", TenantID: ownerTenant, AgentID: "agent-1"}, nil
}

func TestDeploymentOperationsRequireOwnedAgent(t *testing.T) {
	t.Run("deploy refuses a foreign agent before store access", func(t *testing.T) {
		store := &childDeploymentStore{}
		s := &Server{deploymentStore: store}
		_, err := s.DeployAgent(triggerOwnershipContext(ownerTenant), connect.NewRequest(&agentspb.DeployAgentRequest{AgentId: "foreign-agent"}))
		wantNotFound(t, "DeployAgent", err)
		if store.listCalls != 0 || store.createCalls != 0 {
			t.Fatalf("list calls = %d, create calls = %d", store.listCalls, store.createCalls)
		}
	})

	t.Run("owner deploys the agent", func(t *testing.T) {
		store := &childDeploymentStore{}
		s := &Server{deploymentStore: store}
		if _, err := s.DeployAgent(triggerOwnershipContext(ownerTenant), connect.NewRequest(&agentspb.DeployAgentRequest{AgentId: "agent-1"})); err != nil {
			t.Fatalf("owner DeployAgent: %v", err)
		}
		if store.listCalls != 1 || store.createCalls != 1 {
			t.Fatalf("list calls = %d, create calls = %d", store.listCalls, store.createCalls)
		}
	})

	t.Run("list refuses a foreign agent before count", func(t *testing.T) {
		store := &childDeploymentStore{}
		s := &Server{deploymentStore: store}
		_, err := s.ListDeployments(triggerOwnershipContext(ownerTenant), connect.NewRequest(&agentspb.ListDeploymentsRequest{AgentId: "foreign-agent"}))
		wantNotFound(t, "ListDeployments", err)
		if store.listCalls != 0 {
			t.Fatalf("list calls = %d", store.listCalls)
		}
	})

	t.Run("owner lists deployments", func(t *testing.T) {
		store := &childDeploymentStore{}
		s := &Server{deploymentStore: store}
		if _, err := s.ListDeployments(triggerOwnershipContext(ownerTenant), connect.NewRequest(&agentspb.ListDeploymentsRequest{AgentId: "agent-1"})); err != nil {
			t.Fatalf("owner ListDeployments: %v", err)
		}
		if store.listCalls != 1 {
			t.Fatalf("list calls = %d", store.listCalls)
		}
	})
}

func TestAgentMemoryOperationsRequireOwnedResources(t *testing.T) {
	t.Run("foreign agent cannot list memories", func(t *testing.T) {
		store := &childMemoryStore{}
		s := &Server{agentMemStore: store}
		_, listErr := s.ListAgentMemories(triggerOwnershipContext(ownerTenant), connect.NewRequest(&agentspb.ListAgentMemoriesRequest{AgentId: "foreign-agent"}))
		wantNotFound(t, "ListAgentMemories", listErr)
		if store.lists != 0 || store.writes != 0 {
			t.Fatalf("list calls = %d, write calls = %d", store.lists, store.writes)
		}
	})

	t.Run("foreign agent cannot create a memory", func(t *testing.T) {
		store := &childMemoryStore{}
		s := &Server{agentMemStore: store}
		_, err := s.CreateAgentMemory(triggerOwnershipContext(ownerTenant), connect.NewRequest(&agentspb.CreateAgentMemoryRequest{AgentId: "foreign-agent"}))
		wantNotFound(t, "CreateAgentMemory", err)
		if store.writes != 0 {
			t.Fatalf("write calls = %d", store.writes)
		}
	})

	t.Run("owner lists and creates memories", func(t *testing.T) {
		store := &childMemoryStore{}
		s := &Server{agentMemStore: store}
		if _, err := s.ListAgentMemories(triggerOwnershipContext(ownerTenant), connect.NewRequest(&agentspb.ListAgentMemoriesRequest{AgentId: "agent-1"})); err != nil {
			t.Fatalf("owner ListAgentMemories: %v", err)
		}
		if _, err := s.CreateAgentMemory(triggerOwnershipContext(ownerTenant), connect.NewRequest(&agentspb.CreateAgentMemoryRequest{AgentId: "agent-1"})); err != nil {
			t.Fatalf("owner CreateAgentMemory: %v", err)
		}
		if store.lists != 2 || store.writes != 1 {
			t.Fatalf("list calls = %d, write calls = %d", store.lists, store.writes)
		}
	})

	mutations := []struct {
		name string
		call func(*Server, context.Context, string) error
	}{
		{"update", func(s *Server, ctx context.Context, id string) error {
			_, err := s.UpdateAgentMemory(ctx, connect.NewRequest(&agentspb.UpdateAgentMemoryRequest{MemoryId: id}))
			return err
		}},
		{"deactivate", func(s *Server, ctx context.Context, id string) error {
			_, err := s.DeactivateAgentMemory(ctx, connect.NewRequest(&agentspb.DeactivateAgentMemoryRequest{MemoryId: id}))
			return err
		}},
		{"delete", func(s *Server, ctx context.Context, id string) error {
			_, err := s.DeleteAgentMemory(ctx, connect.NewRequest(&agentspb.DeleteAgentMemoryRequest{MemoryId: id}))
			return err
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name+" refuses a foreign memory before writing", func(t *testing.T) {
			store := &childMemoryStore{}
			s := &Server{agentMemStore: store}
			wantNotFound(t, mutation.name, mutation.call(s, triggerOwnershipContext(ownerTenant), "foreign-memory"))
			if store.writes != 0 {
				t.Fatalf("write calls = %d", store.writes)
			}
		})
		t.Run(mutation.name+" updates the owned memory", func(t *testing.T) {
			store := &childMemoryStore{}
			s := &Server{agentMemStore: store}
			if err := mutation.call(s, triggerOwnershipContext(ownerTenant), "memory-1"); err != nil {
				t.Fatalf("owner call: %v", err)
			}
			if store.writes != 1 {
				t.Fatalf("write calls = %d", store.writes)
			}
		})
	}
}

func TestAgentRevisionOperationsRequireOwnedAgent(t *testing.T) {
	t.Run("foreign agent cannot create a revision", func(t *testing.T) {
		store := &childRevisionStore{}
		s := &Server{revisionStore: store}
		_, createErr := s.CreateAgentRevision(triggerOwnershipContext(ownerTenant), connect.NewRequest(&agentspb.CreateAgentRevisionRequest{AgentId: "foreign-agent"}))
		wantNotFound(t, "CreateAgentRevision", createErr)
		if store.createCalls != 0 {
			t.Fatalf("create calls = %d", store.createCalls)
		}
	})

	t.Run("foreign agent has no active revision", func(t *testing.T) {
		store := &childRevisionStore{}
		s := &Server{revisionStore: store}
		_, err := s.GetActiveAgentRevision(triggerOwnershipContext(ownerTenant), connect.NewRequest(&agentspb.GetActiveAgentRevisionRequest{AgentId: "foreign-agent"}))
		wantNotFound(t, "GetActiveAgentRevision", err)
		if store.activeCalls != 0 {
			t.Fatalf("active calls = %d", store.activeCalls)
		}
	})

	t.Run("owner creates and reads the active revision", func(t *testing.T) {
		store := &childRevisionStore{}
		s := &Server{revisionStore: store}
		if _, err := s.CreateAgentRevision(triggerOwnershipContext(ownerTenant), connect.NewRequest(&agentspb.CreateAgentRevisionRequest{
			AgentId: "agent-1",
			Files:   []*agentspb.AgentRevisionFile{{Path: "AGENTS.md", Content: []byte("instructions")}},
		})); err != nil {
			t.Fatalf("owner CreateAgentRevision: %v", err)
		}
		if _, err := s.GetActiveAgentRevision(triggerOwnershipContext(ownerTenant), connect.NewRequest(&agentspb.GetActiveAgentRevisionRequest{AgentId: "agent-1"})); err != nil {
			t.Fatalf("owner GetActiveAgentRevision: %v", err)
		}
		if store.createCalls != 1 || store.activeCalls != 1 {
			t.Fatalf("create calls = %d, active calls = %d", store.createCalls, store.activeCalls)
		}
	})
}
