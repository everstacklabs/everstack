package v1

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
)

type rawRouteLifecycleBus struct {
	subscribeCalls int
}

func (*rawRouteLifecycleBus) Publish(context.Context, agentrt.LifecycleEvent) error { return nil }

func (b *rawRouteLifecycleBus) Subscribe(context.Context, string, string) (<-chan agentrt.LifecycleEvent, error) {
	b.subscribeCalls++
	ch := make(chan agentrt.LifecycleEvent)
	close(ch)
	return ch, nil
}

func (*rawRouteLifecycleBus) Close() error { return nil }

func rawRouteContext(tenantID string, data interface{}) context.Context {
	return cqrs.WithSystem(sessionPrincipal(tenantID), &cqrs.System{QueryBus: stubQueryBus{data: data}})
}

func TestRawSessionStreamsRequireOwnedSession(t *testing.T) {
	t.Run("turn stream refuses an unverified caller", func(t *testing.T) {
		s := &Server{}
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)).WithContext(unverifiedContexts()["tenant id only"])
		rec := httptest.NewRecorder()
		s.handleRunTurnStreamGateway(rec, req, map[string]string{"session_id": "session-1"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("turn stream refuses a foreign session before runtime access", func(t *testing.T) {
		s := &Server{}
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)).WithContext(rawRouteContext(ownerTenant, nil))
		rec := httptest.NewRecorder()
		s.handleRunTurnStreamGateway(rec, req, map[string]string{"session_id": "foreign-session"})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("turn stream accepts the owned session", func(t *testing.T) {
		s := &Server{}
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{}`)).WithContext(rawRouteContext(ownerTenant, struct{}{}))
		rec := httptest.NewRecorder()
		s.handleRunTurnStreamGateway(rec, req, map[string]string{"session_id": "session-1"})
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "agent session manager not initialized") {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("event stream refuses a foreign session before subscribing", func(t *testing.T) {
		s := &Server{sessionMgr: &agentrt.SessionManager{}}
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(rawRouteContext(ownerTenant, nil))
		rec := httptest.NewRecorder()
		s.handleSubscribeSessionEvents(rec, req, map[string]string{"session_id": "foreign-session"})
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("event stream refuses an unverified caller", func(t *testing.T) {
		s := &Server{sessionMgr: &agentrt.SessionManager{}}
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(unverifiedContexts()["tenant id only"])
		rec := httptest.NewRecorder()
		s.handleSubscribeSessionEvents(rec, req, map[string]string{"session_id": "session-1"})
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("event stream accepts the owned session", func(t *testing.T) {
		s := &Server{sessionMgr: &agentrt.SessionManager{}}
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(rawRouteContext(ownerTenant, struct{}{}))
		rec := httptest.NewRecorder()
		s.handleSubscribeSessionEvents(rec, req, map[string]string{"session_id": "session-1"})
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
		}
	})
}

func TestRawAgentLifecycleStreamRequiresOwnedAgent(t *testing.T) {
	t.Run("unverified caller is refused", func(t *testing.T) {
		bus := &rawRouteLifecycleBus{}
		s := &Server{lifecycleBus: bus}
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(unverifiedContexts()["tenant id only"])
		rec := httptest.NewRecorder()
		s.handleSubscribeAgentLifecycle(rec, req, map[string]string{"agent_id": "agent-1"})
		if rec.Code != http.StatusUnauthorized || bus.subscribeCalls != 0 {
			t.Fatalf("status = %d, subscribe calls = %d", rec.Code, bus.subscribeCalls)
		}
	})

	t.Run("foreign agent is not found before subscribing", func(t *testing.T) {
		bus := &rawRouteLifecycleBus{}
		s := &Server{lifecycleBus: bus}
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(rawRouteContext(ownerTenant, nil))
		rec := httptest.NewRecorder()
		s.handleSubscribeAgentLifecycle(rec, req, map[string]string{"agent_id": "foreign-agent"})
		if rec.Code != http.StatusNotFound || bus.subscribeCalls != 0 {
			t.Fatalf("status = %d, subscribe calls = %d", rec.Code, bus.subscribeCalls)
		}
	})

	t.Run("owner subscribes to the agent", func(t *testing.T) {
		bus := &rawRouteLifecycleBus{}
		s := &Server{lifecycleBus: bus}
		agent := &agentsquery.AgentDefinitionReadModel{ID: "agent-1", TenantID: ownerTenant}
		req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(rawRouteContext(ownerTenant, agent))
		rec := httptest.NewRecorder()
		s.handleSubscribeAgentLifecycle(rec, req, map[string]string{"agent_id": "agent-1"})
		if rec.Code != http.StatusOK || bus.subscribeCalls != 1 {
			t.Fatalf("status = %d, subscribe calls = %d", rec.Code, bus.subscribeCalls)
		}
	})
}

func TestRunCronNowRequiresVerifiedCaller(t *testing.T) {
	s := &Server{sandboxMgr: ownershipTestManager()}
	req := httptest.NewRequest(http.MethodPost, "/", nil).WithContext(unverifiedContexts()["tenant id only"])
	rec := httptest.NewRecorder()
	s.handleRunCronNow(rec, req, map[string]string{"cron_id": "7"})
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

var _ query.QueryBus = stubQueryBus{}
