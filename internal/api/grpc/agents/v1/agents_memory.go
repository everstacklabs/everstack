package v1

import (
	"context"

	"connectrpc.com/connect"
	agentmem "github.com/everstacklabs/everstack/internal/agents/memory"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ListAgentMemories returns persistent memories for an agent.
func (s *Server) ListAgentMemories(ctx context.Context, req *connect.Request[agentspb.ListAgentMemoriesRequest]) (*connect.Response[agentspb.ListAgentMemoriesResponse], error) {
	if s.agentMemStore == nil {
		return connect.NewResponse(&agentspb.ListAgentMemoriesResponse{}), nil
	}

	tenantID, err := s.resolveTenantID(ctx, "")
	if err != nil {
		return nil, err
	}
	_ = req // tenant id comes from context only — request body is ignored

	opts := agentmem.ListOptions{
		AgentID:  req.Msg.GetAgentId(),
		TenantID: tenantID,
		Limit:    req.Msg.GetLimit(),
		Offset:   req.Msg.GetOffset(),
	}
	if req.Msg.ActiveOnly != nil && *req.Msg.ActiveOnly {
		opts.ActiveOnly = true
	}
	if req.Msg.MemoryType != nil {
		mt := agentmem.MemoryType(*req.Msg.MemoryType)
		opts.MemoryType = &mt
	}
	if req.Msg.Scope != nil {
		sc := agentmem.MemoryScope(*req.Msg.Scope)
		opts.Scope = &sc
	}

	memories, err := s.agentMemStore.List(ctx, opts)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	total, _ := s.agentMemStore.Count(ctx, opts)

	var entries []*agentspb.AgentMemoryEntry
	for _, m := range memories {
		entries = append(entries, memoryToProto(m))
	}

	return connect.NewResponse(&agentspb.ListAgentMemoriesResponse{
		Memories: entries,
		Total:    int32(total),
	}), nil
}

// CreateAgentMemory manually creates a persistent memory entry.
func (s *Server) CreateAgentMemory(ctx context.Context, req *connect.Request[agentspb.CreateAgentMemoryRequest]) (*connect.Response[agentspb.CreateAgentMemoryResponse], error) {
	if s.agentMemStore == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrMemoryNotConfigured)
	}

	tenantID, err := s.resolveTenantID(ctx, "")
	if err != nil {
		return nil, err
	}

	scope := agentmem.MemoryScope(req.Msg.GetScope())
	if scope == "" {
		scope = agentmem.MemoryScopeAgent
	}

	confidence := req.Msg.GetConfidence()
	if confidence <= 0 {
		confidence = 1.0
	}

	m := &agentmem.AgentMemory{
		TenantID:   tenantID,
		AgentID:    req.Msg.GetAgentId(),
		Scope:      scope,
		MemoryType: agentmem.MemoryType(req.Msg.GetMemoryType()),
		Content:    req.Msg.GetContent(),
		Confidence: float64(confidence),
		Source:     agentmem.MemorySourceUserExplicit,
		IsActive:   true,
	}
	if req.Msg.FactKey != nil {
		m.FactKey = req.Msg.FactKey
	}

	if err := s.agentMemStore.Save(ctx, m); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.CreateAgentMemoryResponse{
		Memory: memoryToProto(m),
	}), nil
}

// UpdateAgentMemory updates a persistent memory entry's content, fact_key, and confidence.
func (s *Server) UpdateAgentMemory(ctx context.Context, req *connect.Request[agentspb.UpdateAgentMemoryRequest]) (*connect.Response[agentspb.UpdateAgentMemoryResponse], error) {
	if s.agentMemStore == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrMemoryNotConfigured)
	}
	tenantID, err := s.resolveTenantID(ctx, "")
	if err != nil {
		return nil, err
	}

	confidence := float64(req.Msg.GetConfidence())
	if confidence <= 0 {
		confidence = 1.0
	}

	var factKey *string
	if req.Msg.FactKey != nil {
		factKey = req.Msg.FactKey
	}

	if err := s.agentMemStore.Update(ctx, tenantID, req.Msg.GetMemoryId(), req.Msg.GetContent(), factKey, confidence); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Re-fetch to return updated entry
	m, err := s.agentMemStore.Get(ctx, tenantID, req.Msg.GetMemoryId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.UpdateAgentMemoryResponse{
		Memory: memoryToProto(m),
	}), nil
}

// DeactivateAgentMemory soft-deletes a persistent memory entry (sets is_active=false).
func (s *Server) DeactivateAgentMemory(ctx context.Context, req *connect.Request[agentspb.DeactivateAgentMemoryRequest]) (*connect.Response[agentspb.DeactivateAgentMemoryResponse], error) {
	if s.agentMemStore == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrMemoryNotConfigured)
	}
	tenantID, err := s.resolveTenantID(ctx, "")
	if err != nil {
		return nil, err
	}

	if err := s.agentMemStore.Deactivate(ctx, tenantID, req.Msg.GetMemoryId()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.DeactivateAgentMemoryResponse{
		Success: true,
	}), nil
}

// DeleteAgentMemory deletes a persistent memory entry.
func (s *Server) DeleteAgentMemory(ctx context.Context, req *connect.Request[agentspb.DeleteAgentMemoryRequest]) (*connect.Response[agentspb.DeleteAgentMemoryResponse], error) {
	if s.agentMemStore == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, ErrMemoryNotConfigured)
	}
	tenantID, err := s.resolveTenantID(ctx, "")
	if err != nil {
		return nil, err
	}

	if err := s.agentMemStore.Delete(ctx, tenantID, req.Msg.GetMemoryId()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.DeleteAgentMemoryResponse{
		Success: true,
	}), nil
}

// memoryToProto converts an AgentMemory to its proto representation.
func memoryToProto(m *agentmem.AgentMemory) *agentspb.AgentMemoryEntry {
	entry := &agentspb.AgentMemoryEntry{
		Id:          m.ID,
		AgentId:     m.AgentID,
		TenantId:    m.TenantID,
		MemoryType:  string(m.MemoryType),
		Content:     m.Content,
		Confidence:  float32(m.Confidence),
		Source:      string(m.Source),
		Scope:       string(m.Scope),
		AccessCount: m.AccessCount,
		IsActive:    m.IsActive,
		CreatedAt:   timestamppb.New(m.CreatedAt),
		UpdatedAt:   timestamppb.New(m.UpdatedAt),
	}
	if m.FactKey != nil {
		entry.FactKey = m.FactKey
	}
	if m.UserID != nil {
		entry.UserId = m.UserID
	}
	if m.SourceSessionID != nil {
		entry.SourceSessionId = m.SourceSessionID
	}
	if m.SourceTurnNumber != nil {
		entry.SourceTurnNumber = m.SourceTurnNumber
	}
	if m.SupersededBy != nil {
		entry.SupersededBy = m.SupersededBy
	}
	if m.LastAccessedAt != nil {
		entry.LastAccessedAt = timestamppb.New(*m.LastAccessedAt)
	}
	return entry
}

var ErrMemoryNotConfigured = &connect.Error{}

func init() {
	ErrMemoryNotConfigured = connect.NewError(connect.CodeFailedPrecondition, nil)
}
