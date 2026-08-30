package v1

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"

	"github.com/everstacklabs/everstack/internal/agents/trigger"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ─── Create Agent Trigger ────────────────────────────────────────────

func (s *Server) CreateAgentTrigger(ctx context.Context, req *connect.Request[agentspb.CreateAgentTriggerRequest]) (*connect.Response[agentspb.CreateAgentTriggerResponse], error) {
	if s.triggerStore == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("triggers not configured"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	agentID := req.Msg.GetAgentId()
	if agentID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("agent_id is required"))
	}

	triggerType := trigger.TriggerType(req.Msg.GetTriggerType())
	if triggerType != trigger.TriggerCron && triggerType != trigger.TriggerWebhook && triggerType != trigger.TriggerEvent {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("trigger_type must be 'cron', 'webhook', or 'event'"))
	}

	name := strings.TrimSpace(req.Msg.GetName())
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	if err := s.ensureUniqueTriggerName(ctx, agentID, tenantID, name, ""); err != nil {
		return nil, err
	}

	t := &trigger.Trigger{
		TenantID:           tenantID,
		AgentID:            agentID,
		Name:               name,
		Type:               triggerType,
		Enabled:            true,
		CronExpression:     req.Msg.GetCronExpression(),
		CronTimezone:       req.Msg.GetCronTimezone(),
		EventSourceAgentID: req.Msg.GetEventSourceAgentId(),
		EventType:          req.Msg.GetEventType(),
		InputTemplate:      req.Msg.GetInputTemplate(),
		MaxRetries:         int(req.Msg.GetMaxRetries()),
		RetryDelaySeconds:  int(req.Msg.GetRetryDelaySeconds()),
		TimeoutSeconds:     int(req.Msg.GetTimeoutSeconds()),
		MaxConcurrent:      int(req.Msg.GetMaxConcurrent()),
	}

	if t.TimeoutSeconds <= 0 {
		t.TimeoutSeconds = 300
	}
	if t.MaxConcurrent <= 0 {
		t.MaxConcurrent = 1
	}
	if t.CronTimezone == "" {
		t.CronTimezone = "UTC"
	}
	if t.RetryDelaySeconds <= 0 {
		t.RetryDelaySeconds = 60
	}
	if err := trigger.ValidateConfiguration(t); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Handle event filter
	if req.Msg.GetEventFilter() != nil {
		filterBytes, err := json.Marshal(req.Msg.GetEventFilter().AsMap())
		if err == nil {
			t.EventFilter = filterBytes
		}
	}

	resp := &agentspb.CreateAgentTriggerResponse{}

	// For webhook triggers, generate a path and secret
	if triggerType == trigger.TriggerWebhook {
		rawSecret, secretHash := trigger.GenerateWebhookSecret()
		t.WebhookSecretHash = secretHash
		t.WebhookPath = fmt.Sprintf("%s-%s", agentID[:8], randomHex(8))
		resp.WebhookSecret = rawSecret
		resp.WebhookUrl = fmt.Sprintf("/v1/triggers/webhook/%s", t.WebhookPath)
	}

	if err := s.triggerStore.CreateTrigger(ctx, t); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create trigger: %w", err))
	}

	logger.WithFields("trigger_id", t.ID, "agent_id", agentID, "type", triggerType).Info("agents: trigger created")

	resp.Trigger = triggerToProto(t)
	return connect.NewResponse(resp), nil
}

// ─── List Agent Triggers ─────────────────────────────────────────────

func (s *Server) ListAgentTriggers(ctx context.Context, req *connect.Request[agentspb.ListAgentTriggersRequest]) (*connect.Response[agentspb.ListAgentTriggersResponse], error) {
	if s.triggerStore == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("triggers not configured"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	triggers, err := s.triggerStore.ListTriggers(ctx, req.Msg.GetAgentId(), tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	pb := make([]*agentspb.AgentTrigger, len(triggers))
	for i, t := range triggers {
		pb[i] = triggerToProto(t)
	}

	return connect.NewResponse(&agentspb.ListAgentTriggersResponse{
		Triggers: pb,
	}), nil
}

// ─── Get Agent Trigger ───────────────────────────────────────────────

func (s *Server) GetAgentTrigger(ctx context.Context, req *connect.Request[agentspb.GetAgentTriggerRequest]) (*connect.Response[agentspb.GetAgentTriggerResponse], error) {
	if s.triggerStore == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("triggers not configured"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	t, err := s.triggerStore.GetTrigger(ctx, req.Msg.GetId(), tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	return connect.NewResponse(&agentspb.GetAgentTriggerResponse{
		Trigger: triggerToProto(t),
	}), nil
}

// ─── Update Agent Trigger ────────────────────────────────────────────

func (s *Server) UpdateAgentTrigger(ctx context.Context, req *connect.Request[agentspb.UpdateAgentTriggerRequest]) (*connect.Response[agentspb.UpdateAgentTriggerResponse], error) {
	if s.triggerStore == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("triggers not configured"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	t, err := s.triggerStore.GetTrigger(ctx, req.Msg.GetId(), tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	if name := strings.TrimSpace(req.Msg.GetName()); name != "" {
		if err := s.ensureUniqueTriggerName(ctx, t.AgentID, tenantID, name, t.ID); err != nil {
			return nil, err
		}
		t.Name = name
	}
	t.Enabled = req.Msg.GetEnabled()

	if expr := req.Msg.GetCronExpression(); expr != "" {
		t.CronExpression = expr
	}
	if tz := req.Msg.GetCronTimezone(); tz != "" {
		t.CronTimezone = tz
	}
	if src := req.Msg.GetEventSourceAgentId(); src != "" {
		t.EventSourceAgentID = src
	}
	if et := req.Msg.GetEventType(); et != "" {
		t.EventType = et
	}
	if tmpl := req.Msg.GetInputTemplate(); tmpl != "" {
		t.InputTemplate = tmpl
	}
	if t.Enabled {
		if err := trigger.ValidateConfiguration(t); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
	}
	if mr := req.Msg.GetMaxRetries(); mr > 0 {
		t.MaxRetries = int(mr)
	}
	if rd := req.Msg.GetRetryDelaySeconds(); rd > 0 {
		t.RetryDelaySeconds = int(rd)
	}
	if ts := req.Msg.GetTimeoutSeconds(); ts > 0 {
		t.TimeoutSeconds = int(ts)
	}
	if mc := req.Msg.GetMaxConcurrent(); mc > 0 {
		t.MaxConcurrent = int(mc)
	}
	if req.Msg.GetEventFilter() != nil {
		filterBytes, err := json.Marshal(req.Msg.GetEventFilter().AsMap())
		if err == nil {
			t.EventFilter = filterBytes
		}
	}

	if err := s.triggerStore.UpdateTrigger(ctx, t); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.UpdateAgentTriggerResponse{
		Trigger: triggerToProto(t),
	}), nil
}

func (s *Server) ensureUniqueTriggerName(ctx context.Context, agentID, tenantID, name, exceptID string) error {
	triggers, err := s.triggerStore.ListTriggers(ctx, agentID, tenantID)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("check trigger name: %w", err))
	}
	if triggerNameExists(triggers, name, exceptID) {
		return connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("a trigger named %q already exists for this agent", name))
	}
	return nil
}

func triggerNameExists(triggers []*trigger.Trigger, name, exceptID string) bool {
	for _, existing := range triggers {
		if existing != nil && existing.ID != exceptID && existing.Name == name {
			return true
		}
	}
	return false
}

// ─── Delete Agent Trigger ────────────────────────────────────────────

func (s *Server) DeleteAgentTrigger(ctx context.Context, req *connect.Request[agentspb.DeleteAgentTriggerRequest]) (*connect.Response[agentspb.DeleteAgentTriggerResponse], error) {
	if s.triggerStore == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("triggers not configured"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	if err := s.triggerStore.DeleteTrigger(ctx, req.Msg.GetId(), tenantID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.DeleteAgentTriggerResponse{}), nil
}

// ─── Test Agent Trigger ──────────────────────────────────────────────

func (s *Server) TestAgentTrigger(ctx context.Context, req *connect.Request[agentspb.TestAgentTriggerRequest]) (*connect.Response[agentspb.TestAgentTriggerResponse], error) {
	if s.triggerStore == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("triggers not configured"))
	}
	if s.triggerExecutor == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("trigger executor not configured"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	t, err := s.triggerStore.GetTrigger(ctx, req.Msg.GetId(), tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	// Build test payload
	var payload []byte
	if req.Msg.GetTestPayload() != nil {
		payload, _ = json.Marshal(req.Msg.GetTestPayload().AsMap())
	} else {
		payload = []byte(fmt.Sprintf(`{"test":true,"trigger_id":"%s","trigger_name":"%s"}`, t.ID, t.Name))
	}

	// Fire asynchronously
	go s.triggerExecutor.Execute(context.Background(), t, payload)

	return connect.NewResponse(&agentspb.TestAgentTriggerResponse{
		Execution: &agentspb.AgentTriggerExecution{
			TriggerId: t.ID,
			Status:    "accepted",
		},
	}), nil
}

// ─── List Agent Trigger Executions ───────────────────────────────────

func (s *Server) ListAgentTriggerExecutions(ctx context.Context, req *connect.Request[agentspb.ListAgentTriggerExecutionsRequest]) (*connect.Response[agentspb.ListAgentTriggerExecutionsResponse], error) {
	if s.triggerStore == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("triggers not configured"))
	}

	executions, total, err := s.triggerStore.ListExecutions(ctx, req.Msg.GetTriggerId(), int(req.Msg.GetLimit()), int(req.Msg.GetOffset()))
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	pb := make([]*agentspb.AgentTriggerExecution, len(executions))
	for i, e := range executions {
		pb[i] = executionToProto(e)
	}

	return connect.NewResponse(&agentspb.ListAgentTriggerExecutionsResponse{
		Executions: pb,
		Total:      int32(total),
	}), nil
}

// ─── Proto Converters ────────────────────────────────────────────────

func triggerToProto(t *trigger.Trigger) *agentspb.AgentTrigger {
	pb := &agentspb.AgentTrigger{
		Id:                  t.ID,
		TenantId:            t.TenantID,
		AgentId:             t.AgentID,
		Name:                t.Name,
		TriggerType:         string(t.Type),
		Enabled:             t.Enabled,
		CronExpression:      t.CronExpression,
		CronTimezone:        t.CronTimezone,
		WebhookPath:         t.WebhookPath,
		EventSourceAgentId:  t.EventSourceAgentID,
		EventType:           t.EventType,
		InputTemplate:       t.InputTemplate,
		MaxRetries:          int32(t.MaxRetries),
		TimeoutSeconds:      int32(t.TimeoutSeconds),
		MaxConcurrent:       int32(t.MaxConcurrent),
		CircuitState:        string(t.CircuitState),
		ConsecutiveFailures: int32(t.ConsecutiveFailures),
		RetryDelaySeconds:   int32(t.RetryDelaySeconds),
		CreatedAt:           timestamppb.New(t.CreatedAt),
		UpdatedAt:           timestamppb.New(t.UpdatedAt),
	}

	if len(t.EventFilter) > 0 {
		var m map[string]interface{}
		if json.Unmarshal(t.EventFilter, &m) == nil {
			pb.EventFilter, _ = structpb.NewStruct(m)
		}
	}

	return pb
}

func executionToProto(e *trigger.Execution) *agentspb.AgentTriggerExecution {
	pb := &agentspb.AgentTriggerExecution{
		Id:            e.ID,
		TriggerId:     e.TriggerID,
		Status:        string(e.Status),
		InputRendered: e.InputRendered,
		OutputPreview: e.OutputPreview,
		ErrorMessage:  e.ErrorMessage,
		Attempt:       int32(e.Attempt),
		DurationMs:    int32(e.DurationMs),
		StartedAt:     timestamppb.New(e.StartedAt),
	}
	if e.SessionID != nil {
		pb.SessionId = *e.SessionID
	}
	if e.CompletedAt != nil {
		pb.CompletedAt = timestamppb.New(*e.CompletedAt)
	}
	return pb
}

// randomHex generates n random hex characters using crypto/rand.
func randomHex(n int) string {
	b := make([]byte, (n+1)/2)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)[:n]
}
