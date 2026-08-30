package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	agentmemory "github.com/everstacklabs/everstack/internal/agents/memory"
	"github.com/everstacklabs/everstack/internal/database"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

type tenantCapturingMemoryStore struct {
	agentmemory.Store
	contexts chan context.Context
}

func (s *tenantCapturingMemoryStore) List(ctx context.Context, _ agentmemory.ListOptions) ([]*agentmemory.AgentMemory, error) {
	s.contexts <- ctx
	return []*agentmemory.AgentMemory{{
		ID:         "memory-1",
		TenantID:   "tenant-1",
		AgentID:    "agent-1",
		MemoryType: agentmemory.MemoryTypeFact,
		Content:    "Remember the tenant boundary.",
	}}, nil
}

type digestTenantCapturingProviderSource struct {
	contexts chan context.Context
}

func (s *digestTenantCapturingProviderSource) ProviderBundleForRequest(ctx context.Context) (*gw.Registry, *gw.Router, error) {
	s.contexts <- ctx
	return nil, nil, errors.New("stop after tenant capture")
}

func TestDigestWorkerUsesStoredTenantIdentity(t *testing.T) {
	store := &tenantCapturingMemoryStore{contexts: make(chan context.Context, 1)}
	providerSource := &digestTenantCapturingProviderSource{contexts: make(chan context.Context, 1)}
	engine := NewEngine(gw.NewRegistry(), gw.NewRouter(gw.NewRegistry(), nil), nil)
	engine.SetProviderSource(providerSource)
	manager := NewDigestManager(DigestConfig{
		Enabled:         true,
		RefreshInterval: time.Hour,
	}, engine, store, nil)
	manager.EnsureWorker("agent-1", "tenant-1")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := manager.Shutdown(ctx); err != nil {
			t.Errorf("Shutdown() error = %v", err)
		}
	})

	select {
	case ctx := <-store.contexts:
		if got := contextkeys.GetTenantID(ctx); got != "tenant-1" {
			t.Fatalf("tenant context = %q, want tenant-1", got)
		}
		if got := database.TenantSchemaFromContext(ctx); got != "tenant-1" {
			t.Fatalf("database tenant context = %q, want tenant-1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("digest worker did not attempt a tenant-scoped memory read")
	}

	select {
	case ctx := <-providerSource.contexts:
		if got := contextkeys.GetTenantID(ctx); got != "tenant-1" {
			t.Fatalf("provider tenant context = %q, want tenant-1", got)
		}
		if got := database.TenantSchemaFromContext(ctx); got != "tenant-1" {
			t.Fatalf("provider database tenant context = %q, want tenant-1", got)
		}
	case <-time.After(time.Second):
		t.Fatal("digest worker did not attempt tenant-scoped provider resolution")
	}
}
