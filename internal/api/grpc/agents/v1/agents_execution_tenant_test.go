package v1

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	agentdeploy "github.com/everstacklabs/everstack/internal/agents/deployment"
	agenttrigger "github.com/everstacklabs/everstack/internal/agents/trigger"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

const (
	ownerTenant = "11111111-1111-1111-1111-111111111111"
	otherTenant = "22222222-2222-2222-2222-222222222222"
)

// ownedDeploymentStore knows one deployment, owned by ownerTenant, and
// records whether any keyed or listing call got past the handler.
type ownedDeploymentStore struct {
	agentdeploy.Store
	reached []string
}

func (s *ownedDeploymentStore) GetDeployment(_ context.Context, id, tenantID string) (*agentdeploy.Deployment, error) {
	if id == "dep-1" && tenantID == ownerTenant {
		return &agentdeploy.Deployment{ID: id, TenantID: tenantID}, nil
	}
	return nil, errors.New("not found")
}

func (s *ownedDeploymentStore) ListKeys(_ context.Context, _, tenantID string) ([]*agentdeploy.DeploymentKey, error) {
	s.reached = append(s.reached, "ListKeys:"+tenantID)
	return nil, nil
}

func (s *ownedDeploymentStore) CreateKey(_ context.Context, key *agentdeploy.DeploymentKey) error {
	s.reached = append(s.reached, "CreateKey:"+key.TenantID)
	return nil
}

func (s *ownedDeploymentStore) RevokeKey(_ context.Context, keyID, tenantID string) error {
	s.reached = append(s.reached, "RevokeKey:"+tenantID)
	if keyID == "key-1" && tenantID == ownerTenant {
		return nil
	}
	return agentdeploy.ErrKeyNotFound
}

func (s *ownedDeploymentStore) ListInvocations(_ context.Context, _, tenantID string, _, _ int) ([]*agentdeploy.Invocation, int, error) {
	s.reached = append(s.reached, "ListInvocations:"+tenantID)
	return nil, 0, nil
}

type ownedTriggerStore struct {
	agenttrigger.Store
	reached []string
}

func (s *ownedTriggerStore) GetTrigger(_ context.Context, id, tenantID string) (*agenttrigger.Trigger, error) {
	if id == "trg-1" && tenantID == ownerTenant {
		return &agenttrigger.Trigger{ID: id, TenantID: tenantID}, nil
	}
	return nil, errors.New("not found")
}

func (s *ownedTriggerStore) ListExecutions(_ context.Context, _, tenantID string, _, _ int) ([]*agenttrigger.Execution, int, error) {
	s.reached = append(s.reached, "ListExecutions:"+tenantID)
	return nil, 0, nil
}

func wantNotFound(t *testing.T, name string, err error) {
	t.Helper()
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("%s from another tenant = %v, want NotFound", name, err)
	}
}

func TestDeploymentKeysAndInvocationsStayInsideTheOwningTenant(t *testing.T) {
	store := &ownedDeploymentStore{}
	s := &Server{deploymentStore: store}
	other := sessionPrincipal(otherTenant)

	_, err := s.ListDeploymentKeys(other, connect.NewRequest(&agentspb.ListDeploymentKeysRequest{DeploymentId: "dep-1"}))
	wantNotFound(t, "ListDeploymentKeys", err)
	_, err = s.ListDeploymentInvocations(other, connect.NewRequest(&agentspb.ListDeploymentInvocationsRequest{DeploymentId: "dep-1"}))
	wantNotFound(t, "ListDeploymentInvocations", err)
	_, err = s.CreateDeploymentKey(other, connect.NewRequest(&agentspb.CreateDeploymentKeyRequest{DeploymentId: "dep-1", Name: "x"}))
	wantNotFound(t, "CreateDeploymentKey", err)
	_, err = s.RevokeDeploymentKey(other, connect.NewRequest(&agentspb.RevokeDeploymentKeyRequest{KeyId: "key-1"}))
	wantNotFound(t, "RevokeDeploymentKey", err)
	for _, call := range store.reached {
		if call != "RevokeKey:"+otherTenant {
			t.Fatalf("another tenant's request reached the store: %v", store.reached)
		}
	}

	store.reached = nil
	owner := sessionPrincipal(ownerTenant)
	if _, err := s.ListDeploymentKeys(owner, connect.NewRequest(&agentspb.ListDeploymentKeysRequest{DeploymentId: "dep-1"})); err != nil {
		t.Fatalf("owner ListDeploymentKeys: %v", err)
	}
	if _, err := s.ListDeploymentInvocations(owner, connect.NewRequest(&agentspb.ListDeploymentInvocationsRequest{DeploymentId: "dep-1"})); err != nil {
		t.Fatalf("owner ListDeploymentInvocations: %v", err)
	}
	if _, err := s.RevokeDeploymentKey(owner, connect.NewRequest(&agentspb.RevokeDeploymentKeyRequest{KeyId: "key-1"})); err != nil {
		t.Fatalf("owner RevokeDeploymentKey: %v", err)
	}
	want := []string{"ListKeys:" + ownerTenant, "ListInvocations:" + ownerTenant, "RevokeKey:" + ownerTenant}
	if len(store.reached) != len(want) {
		t.Fatalf("owner calls = %v, want %v", store.reached, want)
	}
	for i := range want {
		if store.reached[i] != want[i] {
			t.Fatalf("owner calls = %v, want %v", store.reached, want)
		}
	}
}

func TestTriggerExecutionsStayInsideTheOwningTenant(t *testing.T) {
	store := &ownedTriggerStore{}
	s := &Server{triggerStore: store}

	_, err := s.ListAgentTriggerExecutions(sessionPrincipal(otherTenant), connect.NewRequest(&agentspb.ListAgentTriggerExecutionsRequest{TriggerId: "trg-1"}))
	wantNotFound(t, "ListAgentTriggerExecutions", err)
	if len(store.reached) != 0 {
		t.Fatalf("another tenant's request reached the store: %v", store.reached)
	}

	if _, err := s.ListAgentTriggerExecutions(sessionPrincipal(ownerTenant), connect.NewRequest(&agentspb.ListAgentTriggerExecutionsRequest{TriggerId: "trg-1"})); err != nil {
		t.Fatalf("owner ListAgentTriggerExecutions: %v", err)
	}
	if len(store.reached) != 1 || store.reached[0] != "ListExecutions:"+ownerTenant {
		t.Fatalf("owner calls = %v", store.reached)
	}
}
