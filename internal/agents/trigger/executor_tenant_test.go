package trigger

import (
	"context"
	"errors"
	"testing"
	"time"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	"github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
)

type tenantCapturingTriggerStore struct {
	Store
	countCalls int
	ctx        context.Context
}

func (s *tenantCapturingTriggerStore) CountRunningExecutions(ctx context.Context, _ string) (int, error) {
	s.countCalls++
	s.ctx = ctx
	return 0, nil
}

func (s *tenantCapturingTriggerStore) RecordExecution(_ context.Context, execution *Execution) error {
	execution.ID = "execution-1"
	return nil
}

func (s *tenantCapturingTriggerStore) CompleteExecution(context.Context, string, ExecutionStatus, string, string, int) error {
	return nil
}

func (s *tenantCapturingTriggerStore) IncrementFailures(context.Context, string) (int, error) {
	return 1, nil
}

func (s *tenantCapturingTriggerStore) OpenCircuit(context.Context, string) error { return nil }

type staticAgentQueryBus struct{}

func (*staticAgentQueryBus) Execute(context.Context, query.Query) (interface{}, error) {
	return &agentsquery.AgentDefinitionReadModel{
		ID:       "agent-1",
		TenantID: "stored-tenant",
		Model:    "tenant-model",
		MaxTurns: 1,
	}, nil
}

func (*staticAgentQueryBus) RegisterHandler(query.QueryHandler) {}

type tenantCapturingProviderSource struct {
	ctx context.Context
}

func (s *tenantCapturingProviderSource) ProviderBundleForRequest(ctx context.Context) (*gw.Registry, *gw.Router, error) {
	s.ctx = ctx
	return nil, nil, errors.New("stop after tenant capture")
}

func TestExecutorUsesStoredTenantForAgentProviderResolution(t *testing.T) {
	store := &tenantCapturingTriggerStore{}
	providerSource := &tenantCapturingProviderSource{}
	engine := agentrt.NewEngine(gw.NewRegistry(), gw.NewRouter(gw.NewRegistry(), nil), nil)
	engine.SetProviderSource(providerSource)
	sessionManager := agentrt.NewSessionManager(engine, nil, nil, nil)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := sessionManager.GracefulShutdown(ctx); err != nil {
			t.Errorf("GracefulShutdown() error = %v", err)
		}
	})
	executor := NewExecutor(store, sessionManager, nil, &staticAgentQueryBus{})
	forged := contextkeys.WithTenantID(context.Background(), "caller-tenant")
	forged = database.WithTenantSchema(forged, "caller-schema")

	executor.Execute(forged, &Trigger{
		ID:            "trigger-1",
		TenantID:      "stored-tenant",
		Name:          "daily-summary",
		MaxConcurrent: 1,
	}, nil)

	if store.countCalls != 1 {
		t.Fatalf("CountRunningExecutions() calls = %d, want 1", store.countCalls)
	}
	if providerSource.ctx == nil {
		t.Fatal("agent provider source was not called")
	}
	if got := contextkeys.GetTenantID(providerSource.ctx); got != "stored-tenant" {
		t.Fatalf("tenant context = %q, want stored tenant", got)
	}
	if got := database.TenantSchemaFromContext(providerSource.ctx); got != "stored-tenant" {
		t.Fatalf("database tenant context = %q, want stored tenant", got)
	}
}

func TestExecutorRejectsTriggerWithoutStoredTenantIdentity(t *testing.T) {
	store := &tenantCapturingTriggerStore{}
	executor := NewExecutor(store, nil, nil, nil)

	executor.Execute(context.Background(), &Trigger{ID: "trigger-1", MaxConcurrent: 1}, nil)

	if store.countCalls != 0 {
		t.Fatalf("CountRunningExecutions() calls = %d, want 0", store.countCalls)
	}
}
