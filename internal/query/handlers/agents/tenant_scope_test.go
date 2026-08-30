package agents

import (
	"context"
	"testing"
)

// These tests pin the post-2026-05-06 P0 contract for the agents query
// layer: every list-style handler must filter by tenant_id, and an
// empty tenant must short-circuit to an empty result rather than
// running an unscoped SQL query that returns every tenant's rows.
//
// We pass a nil *sqlx.DB on the handler. That guarantees that the
// short-circuit fires — any code path reaching SQL would panic.
// Asserting an empty, non-error return is therefore equivalent to
// proving the early return runs.

func TestListAgentsQueryHandler_EmptyTenantShortCircuits(t *testing.T) {
	h := &ListAgentsQueryHandler{db: nil}
	res, err := h.Handle(context.Background(), &ListAgentsQuery{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := res.([]AgentDefinitionReadModel)
	if !ok {
		t.Fatalf("expected []AgentDefinitionReadModel, got %T", res)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty result, got %d agents", len(out))
	}
}

func TestListSessionsQueryHandler_EmptyTenantShortCircuits(t *testing.T) {
	h := &ListSessionsQueryHandler{db: nil}
	res, err := h.Handle(context.Background(), &ListSessionsQuery{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := res.([]AgentSessionReadModel)
	if !ok {
		t.Fatalf("expected []AgentSessionReadModel, got %T", res)
	}
	if len(out) != 0 {
		t.Fatalf("expected empty result, got %d sessions", len(out))
	}
}

func TestListApprovalReviewsQueryHandler_EmptyTenantShortCircuits(t *testing.T) {
	h := &ListApprovalReviewsQueryHandler{db: nil}
	res, err := h.Handle(context.Background(), &ListApprovalReviewsQuery{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := res.(*ApprovalReviewsResult)
	if !ok {
		t.Fatalf("expected *ApprovalReviewsResult, got %T", res)
	}
	if out.Total != 0 || len(out.Reviews) != 0 {
		t.Fatalf("expected empty result, got total=%d reviews=%d", out.Total, len(out.Reviews))
	}
}

// --- GetByID handlers: empty tenant must short-circuit to nil ---

func TestAgentByIDQueryHandler_EmptyTenantShortCircuits(t *testing.T) {
	h := &AgentByIDQueryHandler{db: nil}
	res, err := h.Handle(context.Background(), &GetAgentByIDQuery{ID: "abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result, got %T", res)
	}
}

func TestAgentByNameQueryHandler_EmptyTenantShortCircuits(t *testing.T) {
	h := &AgentByNameQueryHandler{db: nil}
	res, err := h.Handle(context.Background(), &GetAgentByNameQuery{Name: "test"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result, got %T", res)
	}
}

func TestSessionByIDQueryHandler_EmptyTenantShortCircuits(t *testing.T) {
	h := &SessionByIDQueryHandler{db: nil}
	res, err := h.Handle(context.Background(), &GetSessionByIDQuery{ID: "abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result, got %T", res)
	}
}

func TestApprovalReviewByIDQueryHandler_EmptyTenantShortCircuits(t *testing.T) {
	h := &ApprovalReviewByIDQueryHandler{db: nil}
	res, err := h.Handle(context.Background(), &GetApprovalReviewByIDQuery{ReviewID: "abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res != nil {
		t.Fatalf("expected nil result, got %T", res)
	}
}

func TestGetSpawnTreeQueryHandler_EmptyTenantShortCircuits(t *testing.T) {
	h := &GetSpawnTreeQueryHandler{db: nil}
	res, err := h.Handle(context.Background(), &GetSpawnTreeQuery{TreeID: "abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := res.(*SpawnTreeResult)
	if !ok {
		t.Fatalf("expected *SpawnTreeResult, got %T", res)
	}
	if len(out.Nodes) != 0 {
		t.Fatalf("expected empty nodes, got %d", len(out.Nodes))
	}
}

func TestListSpawnNodesQueryHandler_EmptyTenantShortCircuits(t *testing.T) {
	h := &ListSpawnNodesQueryHandler{db: nil}
	res, err := h.Handle(context.Background(), &ListSpawnNodesQuery{SessionID: "abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := res.(*SpawnTreeResult)
	if !ok {
		t.Fatalf("expected *SpawnTreeResult, got %T", res)
	}
	if len(out.Nodes) != 0 {
		t.Fatalf("expected empty nodes, got %d", len(out.Nodes))
	}
}

func TestListSandboxInstancesQueryHandler_EmptyTenantShortCircuits(t *testing.T) {
	h := &ListSandboxInstancesQueryHandler{db: nil}
	res, err := h.Handle(context.Background(), &ListSandboxInstancesQuery{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := res.(*SandboxInstancesResult)
	if !ok {
		t.Fatalf("expected *SandboxInstancesResult, got %T", res)
	}
	if len(out.Instances) != 0 {
		t.Fatalf("expected empty instances, got %d", len(out.Instances))
	}
}

func TestListSandboxExecutionsQueryHandler_EmptyTenantShortCircuits(t *testing.T) {
	h := &ListSandboxExecutionsQueryHandler{db: nil}
	res, err := h.Handle(context.Background(), &ListSandboxExecutionsQuery{SandboxID: "abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := res.(*SandboxExecutionsResult)
	if !ok {
		t.Fatalf("expected *SandboxExecutionsResult, got %T", res)
	}
	if len(out.Executions) != 0 {
		t.Fatalf("expected empty executions, got %d", len(out.Executions))
	}
}

func TestListSandboxEventsQueryHandler_EmptyTenantShortCircuits(t *testing.T) {
	h := &ListSandboxEventsQueryHandler{db: nil}
	res, err := h.Handle(context.Background(), &ListSandboxEventsQuery{SandboxID: "abc"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	out, ok := res.(*SandboxEventsResult)
	if !ok {
		t.Fatalf("expected *SandboxEventsResult, got %T", res)
	}
	if len(out.Events) != 0 {
		t.Fatalf("expected empty events, got %d", len(out.Events))
	}
}
