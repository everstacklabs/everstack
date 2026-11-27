package v1

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	troopersquery "github.com/everstacklabs/everstack/internal/query/handlers/troopers"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

type sessionSweepQueryBus struct {
	listCalls int
}

func (b *sessionSweepQueryBus) Execute(_ context.Context, q query.Query) (interface{}, error) {
	var data interface{}
	switch value := q.(type) {
	case *agentsquery.GetSessionByIDQuery:
		if value.ID == "session-1" && value.TenantID == ownerTenant {
			data = &agentsquery.SessionWithTurns{Session: agentsquery.AgentSessionReadModel{ID: value.ID, TenantID: value.TenantID}}
		}
	case *agentsquery.GetAgentByIDQuery:
		if value.ID == "agent-1" && value.TenantID == ownerTenant {
			data = &agentsquery.AgentDefinitionReadModel{ID: value.ID, TenantID: value.TenantID}
		}
	case *agentsquery.GetApprovalReviewByIDQuery:
		if value.ReviewID == "review-1" && value.TenantID == ownerTenant {
			data = &agentsquery.ApprovalReviewReadModel{ID: value.ReviewID, TenantID: value.TenantID}
		}
	case *troopersquery.GetTrooperByIDQuery:
		if value.ID == "trooper-1" && value.TenantID == ownerTenant {
			data = &troopersquery.TrooperReadModel{ID: value.ID, TenantID: value.TenantID}
		}
	case *agentsquery.ListSessionsQuery:
		b.listCalls++
		data = []agentsquery.AgentSessionReadModel{}
	case *agentsquery.ListApprovalReviewsQuery:
		b.listCalls++
		data = &agentsquery.ApprovalReviewsResult{}
	case *agentsquery.GetSpawnTreeQuery:
		b.listCalls++
		data = &agentsquery.SpawnTreeResult{}
	case *agentsquery.ListSpawnNodesQuery:
		b.listCalls++
		data = &agentsquery.SpawnTreeResult{}
	}
	return &query.Response{Data: data}, nil
}

func (*sessionSweepQueryBus) RegisterHandler(query.QueryHandler) {}

func sessionSweepContext(bus query.QueryBus) context.Context {
	return cqrs.WithSystem(sessionPrincipal(ownerTenant), &cqrs.System{QueryBus: bus})
}

func TestSteerSessionRequiresOwnedSession(t *testing.T) {
	t.Run("foreign session is not found before steering", func(t *testing.T) {
		bus := &sessionSweepQueryBus{}
		calls := 0
		s := &Server{sessionMgr: &agentrt.SessionManager{}, steerSession: func(string, string, agentrt.SteerMessage) error {
			calls++
			return nil
		}}
		_, err := s.SteerSession(sessionSweepContext(bus), connect.NewRequest(&agentspb.SteerSessionRequest{
			SessionId: "foreign-session", Role: "user", Content: "hello",
		}))
		wantNotFound(t, "SteerSession", err)
		if calls != 0 {
			t.Fatalf("steer calls = %d", calls)
		}
	})

	t.Run("owner steers the session", func(t *testing.T) {
		bus := &sessionSweepQueryBus{}
		calls := 0
		s := &Server{sessionMgr: &agentrt.SessionManager{}, steerSession: func(string, string, agentrt.SteerMessage) error {
			calls++
			return nil
		}}
		if _, err := s.SteerSession(sessionSweepContext(bus), connect.NewRequest(&agentspb.SteerSessionRequest{
			SessionId: "session-1", Role: "user", Content: "hello",
		})); err != nil {
			t.Fatalf("owner SteerSession: %v", err)
		}
		if calls != 1 {
			t.Fatalf("steer calls = %d", calls)
		}
	})
}

func TestSubmitReviewRequiresOwnedReview(t *testing.T) {
	t.Run("foreign review is not found before submission", func(t *testing.T) {
		bus := &sessionSweepQueryBus{}
		calls := 0
		s := &Server{sessionMgr: &agentrt.SessionManager{}, submitReview: func(context.Context, string, string, agentrt.ApprovalDecision) error {
			calls++
			return nil
		}}
		_, err := s.SubmitReview(sessionSweepContext(bus), connect.NewRequest(&agentspb.SubmitReviewRequest{ReviewId: "foreign-review"}))
		wantNotFound(t, "SubmitReview", err)
		if calls != 0 {
			t.Fatalf("submit calls = %d", calls)
		}
	})

	t.Run("owner submits the review", func(t *testing.T) {
		bus := &sessionSweepQueryBus{}
		calls := 0
		s := &Server{sessionMgr: &agentrt.SessionManager{}, submitReview: func(context.Context, string, string, agentrt.ApprovalDecision) error {
			calls++
			return nil
		}}
		if _, err := s.SubmitReview(sessionSweepContext(bus), connect.NewRequest(&agentspb.SubmitReviewRequest{ReviewId: "review-1"})); err != nil {
			t.Fatalf("owner SubmitReview: %v", err)
		}
		if calls != 1 {
			t.Fatalf("submit calls = %d", calls)
		}
	})
}

func TestSessionFilteredListsRequireOwnedParent(t *testing.T) {
	tests := []struct {
		name string
		call func(*Server, context.Context, string) error
	}{
		{
			name: "sessions by agent",
			call: func(s *Server, ctx context.Context, id string) error {
				_, err := s.ListSessions(ctx, connect.NewRequest(&agentspb.ListSessionsRequest{AgentId: &id}))
				return err
			},
		},
		{
			name: "sessions by trooper",
			call: func(s *Server, ctx context.Context, id string) error {
				_, err := s.ListSessions(ctx, connect.NewRequest(&agentspb.ListSessionsRequest{TrooperId: &id}))
				return err
			},
		},
		{
			name: "reviews by session",
			call: func(s *Server, ctx context.Context, id string) error {
				_, err := s.ListReviews(ctx, connect.NewRequest(&agentspb.ListReviewsRequest{SessionId: &id}))
				return err
			},
		},
		{
			name: "spawn tree",
			call: func(s *Server, ctx context.Context, id string) error {
				_, err := s.GetSpawnTree(ctx, connect.NewRequest(&agentspb.GetSpawnTreeRequest{TreeId: id}))
				return err
			},
		},
		{
			name: "spawn nodes",
			call: func(s *Server, ctx context.Context, id string) error {
				_, err := s.ListSpawnNodes(ctx, connect.NewRequest(&agentspb.ListSpawnNodesRequest{SessionId: id}))
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name+" refuses a foreign parent before listing", func(t *testing.T) {
			bus := &sessionSweepQueryBus{}
			s := &Server{}
			wantNotFound(t, tt.name, tt.call(s, sessionSweepContext(bus), "foreign-session"))
			if bus.listCalls != 0 {
				t.Fatalf("list calls = %d", bus.listCalls)
			}
		})

		t.Run(tt.name+" lists for the owner", func(t *testing.T) {
			bus := &sessionSweepQueryBus{}
			s := &Server{}
			ownedID := "session-1"
			if tt.name == "sessions by agent" {
				ownedID = "agent-1"
			} else if tt.name == "sessions by trooper" {
				ownedID = "trooper-1"
			}
			if err := tt.call(s, sessionSweepContext(bus), ownedID); err != nil {
				t.Fatalf("owner call: %v", err)
			}
			if bus.listCalls != 1 {
				t.Fatalf("list calls = %d", bus.listCalls)
			}
		})
	}
}
