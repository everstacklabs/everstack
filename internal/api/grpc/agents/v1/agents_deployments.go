package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"connectrpc.com/connect"

	deployment "github.com/everstacklabs/everstack/internal/agents/deployment"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ─── Deploy Agent ─────────────────────────────────────────────────────

func (s *Server) DeployAgent(ctx context.Context, req *connect.Request[agentspb.DeployAgentRequest]) (*connect.Response[agentspb.DeployAgentResponse], error) {
	if s.deploymentStore == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("deployments not configured"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	agentID := req.Msg.GetAgentId()
	if agentID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("agent_id is required"))
	}

	// Load agent definition to snapshot its config
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	q := agentsquery.NewGetAgentByIDQuery(agentID, tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("agent not found: %w", err))
	}
	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}
	agent, ok := data.(*agentsquery.AgentDefinitionReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected agent data type"))
	}

	// Build config snapshot
	snapshot := map[string]interface{}{
		"model":                  agent.Model,
		"system_prompt":          agent.SystemPrompt.String,
		"tools":                  []string(agent.Tools),
		"max_turns":              agent.MaxTurns,
		"max_tool_calls_per_turn": agent.MaxToolCallsPerTurn,
		"mode":                   agent.Mode,
	}
	if len(agent.Config) > 0 {
		var cfg map[string]interface{}
		if json.Unmarshal(agent.Config, &cfg) == nil {
			snapshot["config"] = cfg
		}
	}
	snapshotBytes, err := json.Marshal(snapshot)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("marshal snapshot: %w", err))
	}

	// Determine next version
	existing, _, _ := s.deploymentStore.ListDeployments(ctx, agentID, tenantID, 1, 0)
	nextVersion := 1
	if len(existing) > 0 {
		nextVersion = existing[0].Version + 1
	}

	name := req.Msg.GetName()
	if name == "" {
		name = fmt.Sprintf("%s v%d", agent.Name, nextVersion)
	}

	maxConcurrent := int(req.Msg.GetMaxConcurrentSessions())
	if maxConcurrent <= 0 {
		maxConcurrent = 10
	}
	sessionTimeout := int(req.Msg.GetSessionTimeoutSeconds())
	if sessionTimeout <= 0 {
		sessionTimeout = 300
	}

	d := &deployment.Deployment{
		TenantID:              tenantID,
		AgentID:               agentID,
		Name:                  name,
		Version:               nextVersion,
		Status:                deployment.StatusActive,
		AgentConfigSnapshot:   snapshotBytes,
		MaxConcurrentSessions: maxConcurrent,
		SessionTimeoutSeconds: sessionTimeout,
		TrackSessions:         !req.Msg.GetDisableSessionTracking(),
		Description:           req.Msg.GetDescription(),
		Changelog:             req.Msg.GetChangelog(),
		DeployedBy:            s.resolveUserID(ctx),
	}
	if rpm := req.Msg.GetRateLimitRpm(); rpm > 0 {
		v := int(rpm)
		d.RateLimitRPM = &v
	}
	if mt := req.Msg.GetMaxTurnsPerSession(); mt > 0 {
		v := int(mt)
		d.MaxTurnsPerSession = &v
	}

	if err := s.deploymentStore.CreateDeployment(ctx, d); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create deployment: %w", err))
	}

	logger.WithFields("deployment_id", d.ID, "agent_id", agentID, "version", nextVersion).Info("agents: deployment created")

	return connect.NewResponse(&agentspb.DeployAgentResponse{
		Deployment: deploymentToProto(d),
	}), nil
}

// ─── List Deployments ─────────────────────────────────────────────────

func (s *Server) ListDeployments(ctx context.Context, req *connect.Request[agentspb.ListDeploymentsRequest]) (*connect.Response[agentspb.ListDeploymentsResponse], error) {
	if s.deploymentStore == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("deployments not configured"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	deployments, total, err := s.deploymentStore.ListDeployments(ctx, req.Msg.GetAgentId(), tenantID, int(req.Msg.GetLimit()), int(req.Msg.GetOffset()))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	pb := make([]*agentspb.AgentDeployment, len(deployments))
	for i, d := range deployments {
		pb[i] = deploymentToProto(d)
	}

	return connect.NewResponse(&agentspb.ListDeploymentsResponse{
		Deployments: pb,
		Total:       int32(total),
	}), nil
}

// ─── Get Deployment ───────────────────────────────────────────────────

func (s *Server) GetDeployment(ctx context.Context, req *connect.Request[agentspb.GetDeploymentRequest]) (*connect.Response[agentspb.GetDeploymentResponse], error) {
	if s.deploymentStore == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("deployments not configured"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	d, err := s.deploymentStore.GetDeployment(ctx, req.Msg.GetId(), tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&agentspb.GetDeploymentResponse{
		Deployment: deploymentToProto(d),
	}), nil
}

// ─── Update Deployment ────────────────────────────────────────────────

func (s *Server) UpdateDeployment(ctx context.Context, req *connect.Request[agentspb.UpdateDeploymentRequest]) (*connect.Response[agentspb.UpdateDeploymentResponse], error) {
	if s.deploymentStore == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("deployments not configured"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	d, err := s.deploymentStore.GetDeployment(ctx, req.Msg.GetId(), tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	if status := req.Msg.GetStatus(); status != "" {
		d.Status = deployment.DeploymentStatus(status)
	}
	if rpm := req.Msg.GetRateLimitRpm(); rpm > 0 {
		v := int(rpm)
		d.RateLimitRPM = &v
	}
	if mc := req.Msg.GetMaxConcurrentSessions(); mc > 0 {
		d.MaxConcurrentSessions = int(mc)
	}
	if mt := req.Msg.GetMaxTurnsPerSession(); mt > 0 {
		v := int(mt)
		d.MaxTurnsPerSession = &v
	}
	if st := req.Msg.GetSessionTimeoutSeconds(); st > 0 {
		d.SessionTimeoutSeconds = int(st)
	}
	if req.Msg.DisableSessionTracking != nil {
		d.TrackSessions = !req.Msg.GetDisableSessionTracking()
	}

	if err := s.deploymentStore.UpdateDeployment(ctx, d); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.UpdateDeploymentResponse{
		Deployment: deploymentToProto(d),
	}), nil
}

// ─── Deployment Keys ──────────────────────────────────────────────────

func (s *Server) CreateDeploymentKey(ctx context.Context, req *connect.Request[agentspb.CreateDeploymentKeyRequest]) (*connect.Response[agentspb.CreateDeploymentKeyResponse], error) {
	if s.deploymentStore == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("deployments not configured"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	// Generate the key
	rawKey, keyHash, keyPrefix, ok := deployment.GenerateDeploymentKey(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to generate API key — hash secret not configured"))
	}

	key := &deployment.DeploymentKey{
		TenantID:     tenantID,
		DeploymentID: req.Msg.GetDeploymentId(),
		KeyHash:      keyHash,
		KeyPrefix:    keyPrefix,
		Name:         req.Msg.GetName(),
		IsActive:     true,
	}
	if req.Msg.GetExpiresAt() != nil {
		t := req.Msg.GetExpiresAt().AsTime()
		key.ExpiresAt = &t
	}

	if err := s.deploymentStore.CreateKey(ctx, key); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create key: %w", err))
	}

	return connect.NewResponse(&agentspb.CreateDeploymentKeyResponse{
		Key:    deploymentKeyToProto(key),
		RawKey: rawKey,
	}), nil
}

func (s *Server) ListDeploymentKeys(ctx context.Context, req *connect.Request[agentspb.ListDeploymentKeysRequest]) (*connect.Response[agentspb.ListDeploymentKeysResponse], error) {
	if s.deploymentStore == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("deployments not configured"))
	}

	keys, err := s.deploymentStore.ListKeys(ctx, req.Msg.GetDeploymentId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	pb := make([]*agentspb.DeploymentKey, len(keys))
	for i, k := range keys {
		pb[i] = deploymentKeyToProto(k)
	}

	return connect.NewResponse(&agentspb.ListDeploymentKeysResponse{
		Keys: pb,
	}), nil
}

func (s *Server) RevokeDeploymentKey(ctx context.Context, req *connect.Request[agentspb.RevokeDeploymentKeyRequest]) (*connect.Response[agentspb.RevokeDeploymentKeyResponse], error) {
	if s.deploymentStore == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("deployments not configured"))
	}

	if err := s.deploymentStore.RevokeKey(ctx, req.Msg.GetKeyId()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.RevokeDeploymentKeyResponse{}), nil
}

// ─── Deployment Invocations ───────────────────────────────────────────

func (s *Server) ListDeploymentInvocations(ctx context.Context, req *connect.Request[agentspb.ListDeploymentInvocationsRequest]) (*connect.Response[agentspb.ListDeploymentInvocationsResponse], error) {
	if s.deploymentStore == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("deployments not configured"))
	}

	invocations, total, err := s.deploymentStore.ListInvocations(ctx, req.Msg.GetDeploymentId(), int(req.Msg.GetLimit()), int(req.Msg.GetOffset()))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	pb := make([]*agentspb.DeploymentInvocation, len(invocations))
	for i, inv := range invocations {
		pb[i] = invocationToProto(inv)
	}

	return connect.NewResponse(&agentspb.ListDeploymentInvocationsResponse{
		Invocations: pb,
		Total:       int32(total),
	}), nil
}

// ─── Proto Converters ─────────────────────────────────────────────────

func deploymentToProto(d *deployment.Deployment) *agentspb.AgentDeployment {
	pb := &agentspb.AgentDeployment{
		Id:                    d.ID,
		TenantId:              d.TenantID,
		AgentId:               d.AgentID,
		Name:                  d.Name,
		Version:               int32(d.Version),
		Status:                string(d.Status),
		Description:           d.Description,
		Changelog:             d.Changelog,
		MaxConcurrentSessions: int32(d.MaxConcurrentSessions),
		SessionTimeoutSeconds: int32(d.SessionTimeoutSeconds),
		DeployedBy:               d.DeployedBy,
		DisableSessionTracking:   !d.TrackSessions,
		CreatedAt:                timestamppb.New(d.CreatedAt),
		UpdatedAt:                timestamppb.New(d.UpdatedAt),
	}
	if d.RateLimitRPM != nil {
		pb.RateLimitRpm = int32(*d.RateLimitRPM)
	}
	if d.MaxTurnsPerSession != nil {
		pb.MaxTurnsPerSession = int32(*d.MaxTurnsPerSession)
	}
	return pb
}

func deploymentKeyToProto(k *deployment.DeploymentKey) *agentspb.DeploymentKey {
	pb := &agentspb.DeploymentKey{
		Id:           k.ID,
		DeploymentId: k.DeploymentID,
		Name:         k.Name,
		KeyPrefix:    k.KeyPrefix,
		IsActive:     k.IsActive,
		CreatedAt:    timestamppb.New(k.CreatedAt),
	}
	if k.ExpiresAt != nil {
		pb.ExpiresAt = timestamppb.New(*k.ExpiresAt)
	}
	if k.LastUsedAt != nil {
		pb.LastUsedAt = timestamppb.New(*k.LastUsedAt)
	}
	return pb
}

func invocationToProto(inv *deployment.Invocation) *agentspb.DeploymentInvocation {
	pb := &agentspb.DeploymentInvocation{
		Id:               inv.ID,
		DeploymentId:     inv.DeploymentID,
		Status:           inv.Status,
		Turns:            int32(inv.Turns),
		PromptTokens:     int32(inv.PromptTokens),
		CompletionTokens: int32(inv.CompletionTokens),
		DurationMs:       int32(inv.DurationMs),
		ErrorMessage:     inv.ErrorMessage,
		CreatedAt:        timestamppb.New(inv.CreatedAt),
	}
	if inv.CompletedAt != nil {
		pb.CompletedAt = timestamppb.New(*inv.CompletedAt)
	}
	return pb
}
