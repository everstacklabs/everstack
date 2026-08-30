package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"reflect"

	"connectrpc.com/connect"
	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	agentscmd "github.com/everstacklabs/everstack/internal/commands/handlers/agents"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/utils"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"github.com/everstacklabs/everstack/internal/trooper"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/types/known/structpb"
)

// defaultStr returns s if non-empty, otherwise fallback.
func defaultStr(s, fallback string) string {
	if s != "" {
		return s
	}
	return fallback
}

const (
	reconcilePendingGracePeriod = 10 * time.Minute
	provisionAttemptTimeout     = 4 * time.Minute
	stalePendingReprovisionAge  = 5 * time.Minute
)

func persistentSandboxID(agentID string) string {
	return fmt.Sprintf("wks_%s", agentID)
}

// linkedSessionIDFromAgentConfig returns the linked sandbox session ID stored
// at config.sandbox.linked_session_id, or "" if the agent is not linked.
// All provision paths must consult this before creating a new sandbox so a
// linked agent does not get a fresh sandbox provisioned alongside the one
// it is meant to share.
func linkedSessionIDFromAgentConfig(rawConfig []byte) string {
	if len(rawConfig) == 0 {
		return ""
	}
	var cfg map[string]interface{}
	if err := json.Unmarshal(rawConfig, &cfg); err != nil {
		return ""
	}
	sbx, ok := cfg["sandbox"].(map[string]interface{})
	if !ok {
		return ""
	}
	lsid, _ := sbx["linked_session_id"].(string)
	return lsid
}

func (s *Server) agentSandboxStillProvisioning(ctx context.Context, agentID string) bool {
	if s.sandboxMgr == nil {
		return false
	}
	statusCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	inst, err := s.sandboxMgr.BackendStatus(statusCtx, persistentSandboxID(agentID))
	if err != nil || inst == nil {
		return false
	}
	status := strings.ToLower(string(inst.Status))
	return status == "pending" || status == "running"
}

func (s *Server) ensurePrimarySession(ctx context.Context, tenantID, agentID, currentPrimarySessionID string) string {
	if currentPrimarySessionID != "" {
		return currentPrimarySessionID
	}
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return ""
	}
	primaryCmd := agentscmd.NewCreateSessionCommand(tenantID, agentID, map[string]interface{}{
		"source": "system",
		"type":   "primary",
	}, "system", "")
	if dispatchErr := sys.CommandBus.Dispatch(ctx, primaryCmd); dispatchErr != nil {
		logger.WithFields("agent_id", agentID, "error", dispatchErr.Error()).
			Warn("agents: failed to create primary session")
		return ""
	}
	primarySessionID := primaryCmd.AggregateID()
	if s.db != nil {
		waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
		defer waitCancel()
		for {
			var exists bool
			_ = s.db.GetContext(waitCtx, &exists, `SELECT EXISTS(SELECT 1 FROM agent_sessions WHERE id = $1 AND tenant_id = $2)`, primarySessionID, tenantID)
			if exists || waitCtx.Err() != nil {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return primarySessionID
}

func (s *Server) finalizeRecoveredPersistentAgent(ctx context.Context, tenantID, agentID, sandboxID, primarySessionID string) {
	primarySessionID = s.ensurePrimarySession(ctx, tenantID, agentID, primarySessionID)
	if s.db == nil {
		return
	}
	bgCtx := context.Background()
	if primarySessionID != "" {
		if _, err := s.db.ExecContext(bgCtx, `
			UPDATE agent_definitions
			SET sandbox_id = $2, primary_session_id = $3, lifecycle_status = 'idle', updated_at = NOW()
			WHERE id = $1 AND tenant_id = $4
		`, agentID, sandboxID, primarySessionID, tenantID); err != nil {
			logger.WithFields("agent_id", agentID, "sandbox_id", sandboxID, "error", err.Error()).
				Warn("agents: failed to finalize recovered persistent agent")
		}
		return
	}
	if _, err := s.db.ExecContext(bgCtx, `
		UPDATE agent_definitions
		SET sandbox_id = $2, lifecycle_status = 'idle', updated_at = NOW()
		WHERE id = $1 AND tenant_id = $3
	`, agentID, sandboxID, tenantID); err != nil {
		logger.WithFields("agent_id", agentID, "sandbox_id", sandboxID, "error", err.Error()).
			Warn("agents: failed to finalize recovered persistent agent without primary session")
	}
}

func agentReadModelFromQueryResult(data interface{}) (*agentsquery.AgentDefinitionReadModel, error) {
	switch v := data.(type) {
	case *agentsquery.AgentDefinitionReadModel:
		if v == nil {
			return nil, errors.New("agent read model is nil")
		}
		return v, nil
	case agentsquery.AgentDefinitionReadModel:
		copy := v
		return &copy, nil
	default:
		return nil, fmt.Errorf("unexpected agent query data type %T", data)
	}
}

// ─── Agent Lifecycle ──────────────────────────────────────────────────────

func (s *Server) ProvisionAgent(ctx context.Context, req *connect.Request[agentspb.ProvisionAgentRequest]) (*connect.Response[agentspb.ProvisionAgentResponse], error) {
	spanCtx, span := telemetry.StartGatewaySpan(ctx, "agents.provision")
	ctx = spanCtx
	defer span.End()
	telemetry.AddSpanEvent(span, attrs.EventRequestReceived)
	span.SetAttributes(attribute.String(attrs.AgentID, req.Msg.GetAgentId()))

	if s.trooperMgr == nil {
		err := errors.New("trooper manager not available")
		telemetry.RecordError(span, err)
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, err
	}
	userID := contextkeys.GetUserID(ctx)
	span.SetAttributes(
		attribute.String(attrs.TenantID, tenantID),
		attribute.String(attrs.UserID, userID),
	)

	// Load agent via query bus
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	// Retry query to handle eventual consistency — the projection may still
	// be writing the row when auto-provision calls this immediately after create.
	q := agentsquery.NewGetAgentByIDQuery(req.Msg.GetAgentId(), tenantID)
	var rm *agentsquery.AgentDefinitionReadModel
	for attempt := 0; attempt < 120; attempt++ {
		queryCtx, queryCancel := context.WithTimeout(ctx, 3*time.Second)
		res, qErr := sys.QueryBus.Execute(queryCtx, q)
		queryCancel()
		if qErr != nil {
			telemetry.RecordError(span, qErr)
			return nil, connect.NewError(connect.CodeInternal, qErr)
		}

		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}
		if data != nil {
			rm, err = agentReadModelFromQueryResult(data)
			if err == nil {
				break
			}
		}
		// Agent projection not ready yet — wait and retry.
		select {
		case <-ctx.Done():
			err = fmt.Errorf("agent %s not found after %d attempts: context cancelled", req.Msg.GetAgentId(), attempt+1)
			telemetry.RecordError(span, err)
			return nil, connect.NewError(connect.CodeNotFound, err)
		case <-time.After(500 * time.Millisecond):
		}
	}
	if rm == nil && s.db != nil {
		dbCtx, dbCancel := context.WithTimeout(ctx, 10*time.Second)
		defer dbCancel()
		var direct agentsquery.AgentDefinitionReadModel
		if dbErr := s.db.GetContext(dbCtx, &direct, `SELECT `+agentsquery.AgentDefinitionColumns()+` FROM agent_definitions WHERE id = $1 AND tenant_id = $2 AND deleted_at IS NULL`, req.Msg.GetAgentId(), tenantID); dbErr == nil {
			rm = &direct
		}
	}
	if rm == nil {
		err = fmt.Errorf("agent %s not found after retries (projection may not have completed in time)", req.Msg.GetAgentId())
		telemetry.RecordError(span, err)
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	// Verify lifecycle_mode == "persistent"
	if strings.ToLower(rm.LifecycleMode) != "persistent" {
		err = fmt.Errorf("agent %s has lifecycle_mode %q; only persistent agents can be provisioned", rm.ID, rm.LifecycleMode)
		telemetry.RecordError(span, err)
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	// Set lifecycle_status to "provisioning" via direct DB update.
	// Do NOT use CQRS command dispatch here — the async event projection
	// can race with the direct DB update that sets "idle" after sandbox
	// creation completes, overwriting "idle" back to "provisioning".
	if s.db != nil {
		if _, dbErr := s.db.ExecContext(ctx,
			`UPDATE agent_definitions SET lifecycle_status = 'provisioning', updated_at = NOW() WHERE id = $1 AND tenant_id = $2`,
			rm.ID, tenantID,
		); dbErr != nil {
			telemetry.RecordError(span, dbErr)
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to set provisioning status: %w", dbErr))
		}
	}

	// Check if the agent config specifies a linked sandbox (skip provisioning a new one)
	linkedSessionID := linkedSessionIDFromAgentConfig(rm.Config)

	if linkedSessionID != "" {
		// Link to existing sandbox: verify it exists and belongs to same tenant
		if s.sandboxMgr != nil {
			inst, linkErr := s.sandboxMgr.GetLinked(ctx, linkedSessionID, "trp-"+rm.ID, tenantID)
			if linkErr != nil {
				telemetry.RecordError(span, linkErr)
				return nil, connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("cannot link sandbox: %w", linkErr))
			}

			// Create a primary session for the persistent agent
			linkedPrimaryCmd := agentscmd.NewCreateSessionCommand(tenantID, rm.ID, map[string]interface{}{
				"source": "system",
				"type":   "primary",
			}, defaultStr(contextkeys.GetUserID(ctx), "system"), "")
			if dispatchErr := sys.CommandBus.Dispatch(ctx, linkedPrimaryCmd); dispatchErr != nil {
				logger.WithFields("agent_id", rm.ID, "error", dispatchErr.Error()).
					Warn("failed to create primary session during linked provisioning")
			}
			linkedPrimarySessionID := linkedPrimaryCmd.AggregateID()

			// Wait for the session projection before referencing it as a FK
			if s.db != nil {
				waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
				defer waitCancel()
				for {
					var exists bool
					_ = s.db.GetContext(waitCtx, &exists, `SELECT EXISTS(SELECT 1 FROM agent_sessions WHERE id = $1 AND tenant_id = $2)`, linkedPrimarySessionID, tenantID)
					if exists {
						break
					}
					select {
					case <-waitCtx.Done():
						logger.WithFields("agent_id", rm.ID, "session_id", linkedPrimarySessionID).
							Warn("timed out waiting for primary session projection (linked)")
						break
					case <-time.After(100 * time.Millisecond):
					}
					if waitCtx.Err() != nil {
						break
					}
				}
			}

			// Update agent_definitions: lifecycle_status='idle', sandbox_id, primary_session_id
			if s.db != nil {
				_, dbErr := s.db.ExecContext(ctx, `
					UPDATE agent_definitions
					SET lifecycle_status = 'idle', sandbox_id = $2, primary_session_id = $3, updated_at = NOW()
					WHERE id = $1 AND tenant_id = $4
				`, rm.ID, inst.ID, linkedPrimarySessionID, tenantID)
				if dbErr != nil {
					logger.WithFields("agent_id", rm.ID, "error", dbErr.Error()).
						Warn("failed to update agent lifecycle_status after linking sandbox")
				}
			}

			span.SetAttributes(attribute.String("sandbox_id", inst.ID))
			telemetry.AddSpanEvent(span, attrs.EventRequestComplete)

			agent := agentReadModelToProto(rm)
			agent.LifecycleStatus = agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_IDLE
			agent.SandboxId = inst.ID

			return connect.NewResponse(&agentspb.ProvisionAgentResponse{
				Agent: agent,
			}), nil
		}
		err := errors.New("sandbox manager not available for linked sandbox")
		telemetry.RecordError(span, err)
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	cfg := buildProvisionConfigFromReadModel(rm, tenantID)
	sandboxID, err := s.trooperMgr.Provision(ctx, cfg)
	if err != nil {
		telemetry.RecordError(span, err)
		// Rollback lifecycle_status so agent doesn't stay stuck in "provisioning".
		// If the sandbox is still pending/running at the backend level, keep the
		// agent in provisioning so reconciliation doesn't tear down an in-flight
		// sandbox that simply needs more time to come up.
		if s.db != nil {
			targetStatus := "created"
			if s.agentSandboxStillProvisioning(context.Background(), rm.ID) {
				targetStatus = "provisioning"
			}
			s.db.ExecContext(context.Background(), `UPDATE agent_definitions SET lifecycle_status = $2, updated_at = NOW() WHERE id = $1 AND tenant_id = $3`, rm.ID, targetStatus, tenantID)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Create a primary session for the persistent agent.
	// All triggers (peer messages, webhooks, cron) route through this session.
	primarySessionCmd := agentscmd.NewCreateSessionCommand(tenantID, rm.ID, map[string]interface{}{
		"source": "system",
		"type":   "primary",
	}, defaultStr(contextkeys.GetUserID(ctx), "system"), "")
	if err := sys.CommandBus.Dispatch(ctx, primarySessionCmd); err != nil {
		logger.WithFields("agent_id", rm.ID, "error", err.Error()).
			Warn("failed to create primary session during provisioning")
		// Still set to idle with sandbox — session can be created later.
		// Use context.Background() to ensure this succeeds even if request ctx timed out.
		if s.db != nil {
			s.db.ExecContext(context.Background(), `
				UPDATE agent_definitions SET lifecycle_status = 'idle', sandbox_id = $2, updated_at = NOW()
				WHERE id = $1 AND tenant_id = $3
			`, rm.ID, sandboxID, tenantID)
		}
		agent := agentReadModelToProto(rm)
		agent.LifecycleStatus = agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_IDLE
		agent.SandboxId = sandboxID
		return connect.NewResponse(&agentspb.ProvisionAgentResponse{Agent: agent}), nil
	}
	primarySessionID := primarySessionCmd.AggregateID()

	// Wait for the session projection to persist the row before referencing it
	// as a foreign key in agent_definitions. The CQRS projection runs async,
	// so the INSERT into agent_sessions may not have completed yet.
	if s.db != nil {
		waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
		defer waitCancel()
		for {
			var exists bool
			_ = s.db.GetContext(waitCtx, &exists, `SELECT EXISTS(SELECT 1 FROM agent_sessions WHERE id = $1 AND tenant_id = $2)`, primarySessionID, tenantID)
			if exists {
				break
			}
			select {
			case <-waitCtx.Done():
				logger.WithFields("agent_id", rm.ID, "session_id", primarySessionID).
					Warn("timed out waiting for primary session projection")
				break
			case <-time.After(100 * time.Millisecond):
			}
			if waitCtx.Err() != nil {
				break
			}
		}
	}

	// Update agent_definitions: lifecycle_status='idle', sandbox_id, primary_session_id.
	// Use context.Background() — the request context may have timed out during
	// the session projection wait, but we still need to persist the result.
	if s.db != nil {
		_, dbErr := s.db.ExecContext(context.Background(), `
			UPDATE agent_definitions
			SET lifecycle_status = 'idle', sandbox_id = $2, primary_session_id = $3, updated_at = NOW()
			WHERE id = $1 AND tenant_id = $4
		`, rm.ID, sandboxID, primarySessionID, tenantID)
		if dbErr != nil {
			logger.WithFields("agent_id", rm.ID, "error", dbErr.Error()).
				Warn("failed to update agent lifecycle_status after provisioning")
		}
	}

	span.SetAttributes(attribute.String("sandbox_id", sandboxID))
	telemetry.AddSpanEvent(span, attrs.EventRequestComplete)

	// Re-read the agent for the response
	agent := agentReadModelToProto(rm)
	agent.LifecycleStatus = agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_IDLE
	agent.SandboxId = sandboxID

	return connect.NewResponse(&agentspb.ProvisionAgentResponse{
		Agent: agent,
	}), nil
}

func (s *Server) SleepAgent(ctx context.Context, req *connect.Request[agentspb.SleepAgentRequest]) (*connect.Response[agentspb.SleepAgentResponse], error) {

	if s.trooperMgr == nil {
		err := errors.New("trooper manager not available")
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	// Load agent via query bus
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	q := agentsquery.NewGetAgentByIDQuery(req.Msg.GetAgentId(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		err = errors.New("agent not found")
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	rm, err := agentReadModelFromQueryResult(data)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Verify lifecycle_mode == "persistent" and lifecycle_status == "running"
	if strings.ToLower(rm.LifecycleMode) != "persistent" {
		err = fmt.Errorf("agent %s has lifecycle_mode %q; only persistent agents can be put to sleep", rm.ID, rm.LifecycleMode)
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	lcStatus := strings.ToLower(rm.LifecycleStatus)
	if lcStatus != "running" && lcStatus != "idle" {
		err = fmt.Errorf("agent %s has lifecycle_status %q; only running or idle agents can be put to sleep", rm.ID, rm.LifecycleStatus)
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	// Call trooperMgr.Sleep with the agent ID
	if err := s.trooperMgr.Sleep(ctx, req.Msg.GetAgentId()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Update agent_definitions: lifecycle_status='sleeping'
	if s.db != nil {
		_, dbErr := s.db.ExecContext(ctx, `
			UPDATE agent_definitions SET lifecycle_status = 'sleeping', updated_at = NOW()
			WHERE id = $1 AND tenant_id = $2
		`, req.Msg.GetAgentId(), tenantID)
		if dbErr != nil {
			logger.WithFields("agent_id", req.Msg.GetAgentId(), "error", dbErr.Error()).
				Warn("failed to update agent lifecycle_status to sleeping")
		}
	}

	return connect.NewResponse(&agentspb.SleepAgentResponse{
		Success: true,
		Message: "agent is sleeping",
	}), nil
}

func (s *Server) WakeAgent(ctx context.Context, req *connect.Request[agentspb.WakeAgentRequest]) (*connect.Response[agentspb.WakeAgentResponse], error) {

	if s.trooperMgr == nil {
		err := errors.New("trooper manager not available")
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	// Load agent via query bus
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	q := agentsquery.NewGetAgentByIDQuery(req.Msg.GetAgentId(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		err = errors.New("agent not found")
		return nil, connect.NewError(connect.CodeNotFound, err)
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	rm, err := agentReadModelFromQueryResult(data)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Verify lifecycle_mode == "persistent" and lifecycle_status == "sleeping"
	if strings.ToLower(rm.LifecycleMode) != "persistent" {
		err = fmt.Errorf("agent %s has lifecycle_mode %q; only persistent agents can be woken", rm.ID, rm.LifecycleMode)
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}
	if strings.ToLower(rm.LifecycleStatus) != "sleeping" {
		err = fmt.Errorf("agent %s has lifecycle_status %q; only sleeping agents can be woken", rm.ID, rm.LifecycleStatus)
		return nil, connect.NewError(connect.CodeFailedPrecondition, err)
	}

	// Call trooperMgr.Wake with the agent ID
	if err := s.trooperMgr.Wake(ctx, req.Msg.GetAgentId()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Update agent_definitions: lifecycle_status='idle' (not 'running' — no turn is active yet)
	if s.db != nil {
		_, dbErr := s.db.ExecContext(ctx, `
			UPDATE agent_definitions SET lifecycle_status = 'idle', updated_at = NOW()
			WHERE id = $1 AND tenant_id = $2
		`, req.Msg.GetAgentId(), tenantID)
		if dbErr != nil {
			logger.WithFields("agent_id", req.Msg.GetAgentId(), "error", dbErr.Error()).
				Warn("failed to update agent lifecycle_status to idle")
		}
	}

	// Return the updated agent
	agent := agentReadModelToProto(rm)
	agent.LifecycleStatus = agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_IDLE

	return connect.NewResponse(&agentspb.WakeAgentResponse{
		Agent: agent,
	}), nil
}

// ─── Agent Links ──────────────────────────────────────────────────────────

func (s *Server) CreateAgentLink(ctx context.Context, req *connect.Request[agentspb.CreateAgentLinkRequest]) (*connect.Response[agentspb.CreateAgentLinkResponse], error) {

	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	userID := contextkeys.GetUserID(ctx)

	var config map[string]interface{}
	if req.Msg.GetConfig() != nil {
		config = req.Msg.GetConfig().AsMap()
	}

	cmd := agentscmd.NewCreateAgentLinkCommand(
		tenantID,
		req.Msg.GetSourceAgentId(),
		req.Msg.GetTargetType(),
		req.Msg.GetTargetId(),
		req.Msg.GetTargetName(),
		protoAgentLinkTypeToString(req.Msg.GetLinkType()),
		protoAgentLinkProtocolToString(req.Msg.GetProtocol()),
		config,
		userID, "",
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.CreateAgentLinkResponse{
		Link: &agentspb.AgentLink{
			Id:            cmd.ID,
			TenantId:      tenantID,
			SourceAgentId: req.Msg.GetSourceAgentId(),
			TargetType:    req.Msg.GetTargetType(),
			TargetId:      req.Msg.GetTargetId(),
		},
	}), nil
}

func (s *Server) ListAgentLinks(ctx context.Context, req *connect.Request[agentspb.ListAgentLinksRequest]) (*connect.Response[agentspb.ListAgentLinksResponse], error) {

	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	q := agentsquery.NewListAgentLinksQuery(tenantID, req.Msg.GetAgentId())
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	links, ok := data.([]agentsquery.AgentLinkReadModel)
	if !ok {
		err = errors.New("unexpected data type")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var protos []*agentspb.AgentLink
	for _, l := range links {
		protos = append(protos, agentLinkReadModelToProto(&l))
	}

	return connect.NewResponse(&agentspb.ListAgentLinksResponse{
		Links: protos,
		Total: int32(len(protos)),
	}), nil
}

func (s *Server) DeleteAgentLink(ctx context.Context, req *connect.Request[agentspb.DeleteAgentLinkRequest]) (*connect.Response[agentspb.DeleteAgentLinkResponse], error) {

	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	userID := contextkeys.GetUserID(ctx)

	cmd := agentscmd.NewDeleteAgentLinkCommand(req.Msg.GetLinkId(), tenantID, userID, "")
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.DeleteAgentLinkResponse{
		Success: true,
		Message: "link deleted",
	}), nil
}

// ─── Agent Channel Bindings ───────────────────────────────────────────────

func (s *Server) BindAgentChannel(ctx context.Context, req *connect.Request[agentspb.BindAgentChannelRequest]) (*connect.Response[agentspb.BindAgentChannelResponse], error) {

	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	userID := contextkeys.GetUserID(ctx)

	// CHANNEL_BINDINGS covers agent AND trooper bindings together (the legacy
	// trooper RPC counts against the same cap).
	if err := enterprise.CheckResourceLimit(ctx, s.db, enterprise.LicenseMonitorFromContext(ctx),
		enterprise.UsageTypeChannelBindings,
		`SELECT (SELECT COUNT(*) FROM agent_channel_bindings WHERE tenant_id = $1 AND deleted_at IS NULL)
		      + (SELECT COUNT(*) FROM trooper_channel_bindings WHERE tenant_id = $1)`,
		[]interface{}{tenantID}, 1, "channel binding"); err != nil {
		return nil, connect.NewError(connect.CodeResourceExhausted, err)
	}

	cmd := agentscmd.NewBindAgentChannelCommand(
		tenantID, req.Msg.GetAgentId(), req.Msg.GetChannelConfigId(),
		userID, "",
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.BindAgentChannelResponse{
		Binding: &agentspb.AgentChannelBinding{
			Id:              cmd.ID,
			AgentId:         req.Msg.GetAgentId(),
			ChannelConfigId: req.Msg.GetChannelConfigId(),
			Enabled:         true,
		},
	}), nil
}

func (s *Server) UnbindAgentChannel(ctx context.Context, req *connect.Request[agentspb.UnbindAgentChannelRequest]) (*connect.Response[agentspb.UnbindAgentChannelResponse], error) {

	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	userID := contextkeys.GetUserID(ctx)

	cmd := agentscmd.NewUnbindAgentChannelCommand(
		tenantID, req.Msg.GetAgentId(), req.Msg.GetChannelConfigId(),
		userID, "",
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.UnbindAgentChannelResponse{
		Success: true,
		Message: "channel unbound",
	}), nil
}

func (s *Server) ListAgentChannelBindings(ctx context.Context, req *connect.Request[agentspb.ListAgentChannelBindingsRequest]) (*connect.Response[agentspb.ListAgentChannelBindingsResponse], error) {

	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	q := agentsquery.NewListAgentChannelBindingsQuery(tenantID, req.Msg.GetAgentId())
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	bindings, ok := data.([]agentsquery.AgentChannelBindingReadModel)
	if !ok {
		err = errors.New("unexpected data type")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var protos []*agentspb.AgentChannelBinding
	for _, b := range bindings {
		protos = append(protos, &agentspb.AgentChannelBinding{
			Id:              b.ID,
			AgentId:         b.AgentID,
			ChannelConfigId: b.ChannelConfigID,
			Enabled:         b.Enabled,
		})
	}

	return connect.NewResponse(&agentspb.ListAgentChannelBindingsResponse{
		Bindings: protos,
		Total:    int32(len(protos)),
	}), nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────

func agentLinkReadModelToProto(rm *agentsquery.AgentLinkReadModel) *agentspb.AgentLink {
	link := &agentspb.AgentLink{
		Id:            rm.ID,
		TenantId:      rm.TenantID,
		SourceAgentId: rm.SourceAgentID,
		TargetType:    rm.TargetType,
		TargetId:      rm.TargetID,
		TargetName:    rm.TargetName,
		LinkType:      stringToProtoAgentLinkType(rm.LinkType),
		Protocol:      stringToProtoAgentLinkProtocol(rm.Protocol),
		Status:        stringToProtoAgentLinkStatus(rm.Status),
		CreatedAt:     utils.ParseTimestamp(rm.CreatedAt),
		UpdatedAt:     utils.ParseTimestamp(rm.UpdatedAt),
	}
	if len(rm.Config) > 0 {
		var configMap map[string]interface{}
		if err := json.Unmarshal(rm.Config, &configMap); err == nil {
			if s, err := structpb.NewStruct(configMap); err == nil {
				link.Config = s
			}
		}
	}
	return link
}

// ─── Periodic Reconciliation ──────────────────────────────────────────────

const reconcileInterval = 2 * time.Minute

// ReconcilePersistentAgents runs a periodic reconciliation loop that checks all
// persistent agents and re-provisions any whose sandbox is missing. On the first
// call it runs immediately (startup recovery), then repeats every reconcileInterval.
// If the sandbox backend is unhealthy (e.g. minikube stopped), the tick is skipped
// to avoid burning retries against a dead backend.
func (s *Server) ReconcilePersistentAgents(ctx context.Context) {
	if s.db == nil || s.trooperMgr == nil || s.sandboxMgr == nil {
		return
	}

	// Run immediately on startup
	s.reconcilePersistentAgentsTick(ctx)

	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			logger.Info("agents: reconciliation loop stopped")
			return
		case <-ticker.C:
			s.reconcilePersistentAgentsTick(ctx)
		}
	}
}

func (s *Server) reconcilePersistentAgentsTick(ctx context.Context) {
	// Pre-flight: check if the sandbox backend is reachable.
	// If K8s/Docker is down there's no point attempting provisioning —
	// it would just burn through retries and leave agents in "created".
	healthCtx, healthCancel := context.WithTimeout(ctx, 10*time.Second)
	defer healthCancel()
	if err := s.sandboxMgr.Healthy(healthCtx); err != nil {
		logger.WithFields("error", err.Error()).
			Warn("agents: sandbox backend unhealthy, skipping reconciliation tick")
		return
	}

	logger.Info("agents: reconciling persistent agent sandboxes")

	type orphanedAgent struct {
		ID               string         `db:"id"`
		TenantID         string         `db:"tenant_id"`
		SandboxID        sql.NullString `db:"sandbox_id"`
		AgentTarget      sql.NullString `db:"agent_target"`
		LifecycleStatus  string         `db:"lifecycle_status"`
		PrimarySessionID sql.NullString `db:"primary_session_id"`
		UpdatedAt        time.Time      `db:"updated_at"`
	}

	var agents []orphanedAgent
	err := s.db.SelectContext(ctx, &agents, `
		SELECT ad.id,
		       ad.tenant_id,
		       ad.sandbox_id,
		       si.agent_target,
		       ad.lifecycle_status,
		       ad.primary_session_id,
		       ad.updated_at
		FROM agent_definitions ad
		LEFT JOIN sandbox_instances si ON si.id = COALESCE(NULLIF(ad.sandbox_id, ''), 'wks_' || ad.id)
		WHERE ad.lifecycle_mode = 'persistent'
		  -- 'sleeping' is intentionally excluded: a sleeping agent was
		  -- hibernated on purpose (the reaper stopped its sandbox for
		  -- idleness), so its destroyed VM is the correct resting state,
		  -- not an orphan. Waking is demand-driven (AgentMessageBus
		  -- auto-wakes on SendMessage; WakeAgent RPC for explicit wake).
		  -- Reviving sleeping agents here re-provisions the VM every tick
		  -- and fights the reaper, producing a ~2-min sleep/wake flap.
		  AND ad.lifecycle_status IN ('created', 'idle', 'running', 'provisioning')
		  AND ad.deleted_at IS NULL
		  -- Skip agents currently being provisioned (updated within the last 4 min).
		  -- This prevents concurrent reprovision attempts from the periodic loop.
		  AND NOT (ad.lifecycle_status = 'provisioning' AND ad.updated_at > NOW() - INTERVAL '4 minutes')
	`)
	if err != nil {
		logger.WithError(err).Error("agents: failed to query persistent agents for reconciliation")
		return
	}

	// Clean up stale pending sandbox instances older than 5 minutes.
	// These are leftovers from failed provisioning attempts that got persisted
	// to the DB before backend.Create completed.
	s.db.ExecContext(ctx, `
		UPDATE sandbox_instances SET lifecycle_state = 'terminated', status = 'terminated', updated_at = NOW()
		WHERE lifecycle_state = 'pending'
		  AND created_at < NOW() - INTERVAL '5 minutes'
		  AND agent_id IS NOT NULL AND agent_id != ''
	`)

	if len(agents) == 0 {
		logger.Debug("agents: no persistent agents to reconcile")
		return
	}

	logger.WithFields("count", len(agents)).Info("agents: reconciling persistent agents (health check + reprovision)")

	// Run health checks and reprovisioning concurrently for all agents.
	// BackendStatus can take 10+ seconds to timeout for dead sandboxes,
	// so doing this sequentially would be far too slow on restart.
	var wg sync.WaitGroup
	for _, ag := range agents {
		wg.Add(1)
		go func(ag orphanedAgent) {
			defer wg.Done()
			agentID := ag.ID
			sandboxID := ag.SandboxID.String
			canonicalSandboxID := persistentSandboxID(agentID)
			if sandboxID == "" {
				sandboxID = canonicalSandboxID
			}

			// Check if the sandbox is actually alive and running
			sandboxUsable := false
			if sandboxID != "" {
				if ag.AgentTarget.Valid && strings.TrimSpace(ag.AgentTarget.String) != "" {
					s.sandboxMgr.SeedRoute(sandboxID, ag.AgentTarget.String)
				}
				if inst, err := s.sandboxMgr.BackendStatus(ctx, sandboxID); err == nil && inst != nil {
					if inst.Status == "running" {
						sandboxUsable = true
					} else if strings.EqualFold(string(inst.Status), "pending") {
						pendingReason := s.sandboxMgr.DescribePending(ctx, sandboxID)
						if pendingReason != "" {
							logger.WithFields(
								"agent_id", agentID,
								"sandbox_id", sandboxID,
								"sandbox_status", string(inst.Status),
								"reason", pendingReason,
							).Warn("agents: persistent agent sandbox pending diagnostics")
						}
						if time.Since(ag.UpdatedAt) < reconcilePendingGracePeriod {
							logger.WithFields(
								"agent_id", agentID,
								"sandbox_id", sandboxID,
								"sandbox_status", string(inst.Status),
							).Info("agents: persistent agent sandbox still provisioning, skipping reprovision")
							if ag.LifecycleStatus != "provisioning" {
								s.db.ExecContext(context.Background(), `UPDATE agent_definitions SET lifecycle_status = 'provisioning', updated_at = NOW() WHERE id = $1 AND tenant_id = $2`, agentID, ag.TenantID)
							}
							if !ag.SandboxID.Valid || ag.SandboxID.String == "" {
								s.db.ExecContext(context.Background(), `UPDATE agent_definitions SET sandbox_id = $2, updated_at = NOW() WHERE id = $1 AND tenant_id = $3 AND (sandbox_id IS NULL OR sandbox_id = '')`, agentID, sandboxID, ag.TenantID)
							}
							return
						}
						if time.Since(ag.UpdatedAt) >= stalePendingReprovisionAge {
							logger.WithFields(
								"agent_id", agentID,
								"sandbox_id", sandboxID,
								"sandbox_status", string(inst.Status),
								"pending_for", time.Since(ag.UpdatedAt).String(),
							).Warn("agents: persistent agent sandbox pending too long, forcing reprovision")
						} else {
							logger.WithFields(
								"agent_id", agentID,
								"sandbox_id", sandboxID,
								"sandbox_status", string(inst.Status),
							).Warn("agents: persistent agent sandbox pending beyond grace period, reprovisioning")
						}
					} else {
						logger.WithFields(
							"agent_id", agentID,
							"sandbox_id", sandboxID,
							"sandbox_status", string(inst.Status),
						).Warn("agents: persistent agent sandbox exists but not running")
					}
				} else if err != nil {
					reason := "vm_missing"
					if errors.Is(err, sandbox.ErrSandboxRouteMissing) {
						reason = "route_missing"
					} else if ctx.Err() != nil {
						reason = "agent_unreachable"
					}
					logger.WithFields(
						"agent_id", agentID,
						"sandbox_id", sandboxID,
						"reason", reason,
						"error", err.Error(),
					).Warn("agents: persistent agent sandbox backend status failed")
				}
			}

			if sandboxUsable {
				if ag.LifecycleStatus == "provisioning" || !ag.PrimarySessionID.Valid || ag.PrimarySessionID.String == "" {
					logger.WithFields("agent_id", agentID, "sandbox_id", sandboxID).
						Info("agents: adopting recovered persistent sandbox and finalizing provisioning")
					recoverCtx := contextkeys.WithTenantID(context.Background(), ag.TenantID)
					recoverCtx = database.WithTenantSchema(recoverCtx, ag.TenantID)
					s.finalizeRecoveredPersistentAgent(recoverCtx, ag.TenantID, agentID, sandboxID, ag.PrimarySessionID.String)
				}
				if !ag.SandboxID.Valid || ag.SandboxID.String == "" {
					s.db.ExecContext(context.Background(), `UPDATE agent_definitions SET sandbox_id = $2, updated_at = NOW() WHERE id = $1 AND tenant_id = $3 AND (sandbox_id IS NULL OR sandbox_id = '')`, agentID, sandboxID, ag.TenantID)
				}
				logger.WithFields("agent_id", agentID, "sandbox_id", sandboxID).
					Debug("agents: persistent agent sandbox is healthy, skipping")
				return
			}

			logger.WithFields(
				"agent_id", agentID,
				"old_sandbox_id", sandboxID,
				"lifecycle_status", ag.LifecycleStatus,
			).Warn("agents: persistent agent sandbox unavailable, auto-reprovisioning")

			bgCtx := contextkeys.WithTenantID(context.Background(), ag.TenantID)
			bgCtx = database.WithTenantSchema(bgCtx, ag.TenantID)
			s.reprovisionOrphanedAgent(bgCtx, agentID, ag.TenantID)
		}(ag)
	}

	wg.Wait()
	logger.Info("agents: persistent agent reconciliation complete")
}

// tryR2Restore is the Phase 2 last-resort recovery step. Resolves the
// linked session's sandbox_id from sandbox_instances, then asks the
// SandboxManager to rehydrate it from R2 (or whatever object store is
// wired) onto this host. Returns the restored *sandbox.Instance on
// success and nil for every other case so callers can naturally fall
// through to recovery_pending.
//
// No-op cases (all return nil with no log):
//   - SandboxManager isn't wired
//
// isLinkedSandboxTerminal returns true when the sandbox row identified
// by linkedSessionID is in a terminal lifecycle state — terminated or
// failed — meaning the link from the agent is permanently dead and
// trying to re-link will fail forever.
//
// Distinct from transient unreachability (e.g. fcagent restarting,
// network blip): a stopped or reviving sandbox is RECOVERABLE and
// must NOT be flagged terminal here, because we want the existing
// recovery-pending branch to keep retrying for those.
//
// On any DB error or missing row, returns false — being conservative
// (treat as recoverable) is safer than silently unlinking an agent
// from a sandbox we just couldn't read.
func (s *Server) isLinkedSandboxTerminal(ctx context.Context, linkedSessionID, tenantID string) bool {
	if s.db == nil || linkedSessionID == "" {
		return false
	}
	queryCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var state string
	err := s.db.QueryRowxContext(queryCtx, `
		SELECT lifecycle_state
		FROM sandbox_instances
		WHERE session_id = $1 AND tenant_id = $2
		ORDER BY created_at DESC
		LIMIT 1
	`, linkedSessionID, tenantID).Scan(&state)
	if err != nil {
		return false
	}
	switch state {
	case "terminated", "failed":
		return true
	default:
		return false
	}
}

// clearAgentSandboxLink removes the linked_session_id from an agent's
// config so the next reprovision provisions fresh instead of trying
// to re-link to a dead sandbox. Updates agent_definitions.config in
// place using JSON path manipulation — preserves every other config
// field. Caller is responsible for downstream effects (publishing
// lifecycle events, etc.).
func (s *Server) clearAgentSandboxLink(ctx context.Context, agentID, tenantID string) error {
	if s.db == nil {
		return fmt.Errorf("clearAgentSandboxLink: db unavailable")
	}
	updateCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	// jsonb_set with create_missing=false would leave a null in the
	// field; we want the key removed entirely. The '#-' operator
	// does that.
	_, err := s.db.ExecContext(updateCtx, `
		UPDATE agent_definitions
		SET config = (config::jsonb #- '{sandbox,linked_session_id}')::jsonb,
		    sandbox_id = NULL,
		    updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2
	`, agentID, tenantID)
	return err
}

// - No DB row for the linked_session_id
// - Snapshot store is Disabled (R2 not configured)
// - Backend doesn't implement VMRestorer (docker/k8s/fcagent today)
// - No manifest exists for the sandbox in the store
func (s *Server) tryR2Restore(ctx context.Context, linkedSessionID, agentID, tenantID string) *sandbox.Instance {
	if s.sandboxMgr == nil || s.db == nil || linkedSessionID == "" {
		return nil
	}
	type row struct {
		ID string `db:"id"`
	}
	var r row
	qCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	err := s.db.GetContext(qCtx, &r,
		`SELECT id FROM sandbox_instances
		 WHERE session_id = $1 AND tenant_id = $2 AND COALESCE(lifecycle_state, '') NOT IN ('terminated', 'failed')
		 ORDER BY created_at DESC LIMIT 1`,
		linkedSessionID, tenantID)
	if err != nil || r.ID == "" {
		return nil
	}
	callerSession := "trp-" + agentID
	inst, restoreErr := s.sandboxMgr.RestoreFromR2Snapshot(ctx, r.ID, callerSession, tenantID, agentID)
	if restoreErr != nil {
		logger.WithFields(
			"agent_id", agentID,
			"sandbox_id", r.ID,
			"error", restoreErr.Error(),
		).Warn("agents: R2 snapshot restore failed; falling through to recovery_pending")
		return nil
	}
	return inst
}

// reprovisionOrphanedAgent re-provisions a persistent agent whose sandbox
// was lost (e.g. after server restart). It loads the full agent read model,
// builds a provision config, and creates a new sandbox.
func (s *Server) reprovisionOrphanedAgent(ctx context.Context, agentID, tenantID string) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Minute)
	defer cancel()

	// resetStatus is a helper that always uses a fresh context so the DB
	// update succeeds even if the provisioning context has timed out.
	resetStatus := func(status string) {
		if _, err := s.db.ExecContext(context.Background(),
			`UPDATE agent_definitions SET lifecycle_status = $2, updated_at = NOW() WHERE id = $1 AND tenant_id = $3`,
			agentID, status, tenantID); err != nil {
			logger.WithFields("agent_id", agentID, "target_status", status, "error", err.Error()).
				Error("agents: failed to reset lifecycle_status after reprovision failure")
		}
	}

	// Safety net: if this goroutine panics, ensure the agent doesn't stay
	// stuck in 'provisioning' forever.
	defer func() {
		if r := recover(); r != nil {
			logger.WithFields("agent_id", agentID, "panic", fmt.Sprintf("%v", r)).
				Error("agents: reprovisionOrphanedAgent panicked, resetting to created")
			resetStatus("created")
		}
	}()

	// Set lifecycle_status to 'provisioning'
	if _, err := s.db.ExecContext(ctx, `
		UPDATE agent_definitions SET lifecycle_status = 'provisioning', updated_at = NOW()
		WHERE id = $1 AND tenant_id = $2
	`, agentID, tenantID); err != nil {
		logger.WithFields("agent_id", agentID, "error", err.Error()).
			Error("agents: failed to set provisioning status during auto-reprovision")
		return
	}
	s.publishLifecycle(agentrt.LifecycleEvent{
		AgentID:   agentID,
		TenantID:  tenantID,
		NewStatus: "provisioning",
		Reason:    "auto_reprovision_started",
	})

	rm := s.reloadAgentReadModel(ctx, agentID, tenantID)
	if rm == nil {
		logger.WithFields("agent_id", agentID).
			Error("agents: failed to load agent for auto-reprovision")
		resetStatus("created")
		return
	}

	// If the agent is linked to an existing sandbox, re-link rather than
	// provisioning a new one. Destroying and recreating would tear down a
	// sandbox that the agent does not own and replace it with an unrelated
	// fresh one — exactly the symptom users have reported as "use existing
	// generated a new sandbox of the same resource config."
	if linkedSessionID := linkedSessionIDFromAgentConfig(rm.Config); linkedSessionID != "" {
		if s.sandboxMgr == nil {
			logger.WithFields("agent_id", agentID, "linked_session_id", linkedSessionID).
				Error("agents: cannot re-link sandbox: sandbox manager not available")
			resetStatus("created")
			return
		}

		// Pre-check: if the linked sandbox row is already in a terminal
		// state (terminated/failed), the agent's link is permanently
		// dead. Trying to re-link will fail every time and leave the
		// agent stuck in "provisioning" forever — that's the "fails the
		// lifecycle completely" bug. Clear the link in the agent's
		// config and fall through to fresh provisioning. The user's
		// state IS gone in this case (the sandbox is terminated, not
		// merely unreachable), so the "preserve user state" guard
		// that gates the recovery-pending branch doesn't apply.
		if s.isLinkedSandboxTerminal(ctx, linkedSessionID, tenantID) {
			logger.WithFields(
				"agent_id", agentID,
				"linked_session_id", linkedSessionID,
			).Warn("agents: linked sandbox is in terminal state; clearing link and provisioning fresh")
			if err := s.clearAgentSandboxLink(ctx, agentID, tenantID); err != nil {
				logger.WithFields("agent_id", agentID, "error", err.Error()).
					Error("agents: failed to clear stale sandbox link")
				// Don't abort — fall through to fresh provisioning. The
				// stale linked_session_id in config is harmless on a
				// fresh provision; the new sandbox_id overrides it.
			}
			// Fall through to the fresh-provision path below by
			// skipping the re-link block entirely.
			goto freshProvision
		}
		inst, linkErr := s.sandboxMgr.GetLinked(ctx, linkedSessionID, "trp-"+rm.ID, tenantID)
		if linkErr != nil {
			// All three GetLinked layers (in-memory, Redis, Postgres+probe)
			// missed. Before declaring recovery_pending, try the Phase 2
			// last-resort: rehydrate the VM from an R2 snapshot. This is a
			// no-op when the feature isn't configured, no snapshot exists,
			// or the backend doesn't support VM restore.
			if restored := s.tryR2Restore(ctx, linkedSessionID, agentID, tenantID); restored != nil {
				inst = restored
				logger.WithFields(
					"agent_id", agentID,
					"sandbox_id", restored.ID,
				).Info("agents: relinked persistent agent via R2 snapshot restore")
				// Fall through to the existing success-path DB update + event.
			} else {
				// Do NOT resetStatus("created"): that would silently unlink
				// the agent from its sandbox and tear the user's work off it.
				// Leave the agent in 'provisioning' so the reconciler's
				// 4-min grace skips it briefly and retries on the next sweep.
				logger.WithFields(
					"agent_id", agentID,
					"linked_session_id", linkedSessionID,
					"error", linkErr.Error(),
				).Error("agents: linked sandbox recovery pending — reconciler will retry; user state preserved")
				s.db.ExecContext(context.Background(),
					`UPDATE agent_definitions SET updated_at = NOW() WHERE id = $1 AND tenant_id = $2`, agentID, tenantID)
				s.publishLifecycle(agentrt.LifecycleEvent{
					AgentID:   agentID,
					TenantID:  tenantID,
					NewStatus: "provisioning",
					Reason:    "recovery_pending",
					Detail:    linkErr.Error(),
				})
				return
			}
		}
		primarySessionID := s.ensurePrimarySession(ctx, tenantID, agentID, rm.PrimarySessionID.String)
		bgCtx := context.Background()
		if primarySessionID != "" {
			s.db.ExecContext(bgCtx, `
				UPDATE agent_definitions
				SET sandbox_id = $2, primary_session_id = $3, lifecycle_status = 'idle', updated_at = NOW()
				WHERE id = $1 AND tenant_id = $4
			`, agentID, inst.ID, primarySessionID, tenantID)
		} else {
			s.db.ExecContext(bgCtx, `
				UPDATE agent_definitions
				SET sandbox_id = $2, lifecycle_status = 'idle', updated_at = NOW()
				WHERE id = $1 AND tenant_id = $3
			`, agentID, inst.ID, tenantID)
		}
		logger.WithFields("agent_id", agentID, "linked_session_id", linkedSessionID, "sandbox_id", inst.ID).
			Info("agents: re-linked persistent agent to existing sandbox")
		s.publishLifecycle(agentrt.LifecycleEvent{
			AgentID:   agentID,
			TenantID:  tenantID,
			NewStatus: "idle",
			SandboxID: inst.ID,
			Reason:    "relink_recovered",
		})
		return
	}

freshProvision:
	// Destroy old sandboxes (stopped/orphaned) before creating a new one.
	// This prevents the K8s backend from hanging on waitForPodDeletion.
	//
	// We need to clean up BOTH naming conventions:
	//   - wks_<agentID>  — current trooper sandbox ID (from GetOrCreateTrooper)
	//   - sbx_trp-<agentID> — legacy session-based ID (from GetOrCreate via warm-on-boot)
	// The legacy path was removed but old pods may still be stuck in the cluster.
	legacySandboxID := fmt.Sprintf("sbx_trp-%s", agentID)
	sandboxIDs := []string{legacySandboxID}
	if rm.SandboxID.Valid && rm.SandboxID.String != "" {
		sandboxIDs = append(sandboxIDs, rm.SandboxID.String)
	}

	for _, oldID := range sandboxIDs {
		logger.WithFields("agent_id", agentID, "old_sandbox_id", oldID).
			Info("agents: destroying old sandbox before reprovision")
		if err := s.sandboxMgr.BackendDestroy(ctx, oldID); err != nil {
			logger.WithFields("agent_id", agentID, "old_sandbox_id", oldID, "error", err.Error()).
				Warn("agents: old sandbox cleanup failed; reprovision deferred")
			return
		}
	}

	// Mark old sandbox_instances rows as terminated so they don't pile up.
	s.db.ExecContext(context.Background(), `
		UPDATE sandbox_instances SET lifecycle_state = 'terminated', status = 'terminated', updated_at = NOW()
		WHERE agent_id = $1 AND tenant_id = $2 AND lifecycle_state NOT IN ('terminated')
	`, agentID, tenantID)
	// Brief wait for the backend to fully release resources
	time.Sleep(2 * time.Second)

	cfg := buildProvisionConfigFromReadModel(rm, tenantID)

	// Retry provisioning up to 3 times. Each attempt gets a fresh timeout so a
	// slow image pull or deletion wait on one attempt does not starve later ones.
	var newSandboxID string
	var provisionErr error
	for attempt := 1; attempt <= 3; attempt++ {
		attemptCtx, attemptCancel := context.WithTimeout(ctx, provisionAttemptTimeout)
		newSandboxID, provisionErr = s.trooperMgr.Provision(attemptCtx, cfg)
		attemptCancel()
		if provisionErr == nil {
			break
		}
		logger.WithFields("agent_id", agentID, "attempt", attempt, "error", provisionErr.Error()).
			Warn("agents: auto-reprovision attempt failed")
		if attempt < 3 {
			select {
			case <-time.After(time.Duration(attempt*5) * time.Second):
			case <-ctx.Done():
				provisionErr = ctx.Err()
			}
		}
	}
	if provisionErr != nil {
		if s.agentSandboxStillProvisioning(context.Background(), agentID) {
			logger.WithFields("agent_id", agentID).
				Warn("agents: reprovision timed out but sandbox is still provisioning; leaving agent in provisioning state")
			resetStatus("provisioning")
			s.publishLifecycle(agentrt.LifecycleEvent{
				AgentID:   agentID,
				TenantID:  tenantID,
				NewStatus: "provisioning",
				Reason:    "provision_still_running",
			})
			return
		}
		logger.WithFields("agent_id", agentID, "error", provisionErr.Error()).
			Error("agents: auto-reprovision failed after retries")
		resetStatus("created")
		s.publishLifecycle(agentrt.LifecycleEvent{
			AgentID:   agentID,
			TenantID:  tenantID,
			NewStatus: "created",
			Reason:    "provision_failed",
			Detail:    provisionErr.Error(),
		})
		return
	}

	// Ensure primary session exists — create one if it was lost
	primarySessionID := rm.PrimarySessionID.String
	if primarySessionID == "" {
		sys, err := cqrs.GetSystemFromContext(ctx)
		if err != nil && s.ctx != nil {
			sys, err = cqrs.GetSystemFromContext(s.ctx)
		}
		if err == nil {
			primaryCmd := agentscmd.NewCreateSessionCommand(tenantID, agentID, map[string]interface{}{
				"source": "system",
				"type":   "primary",
			}, "system", "")
			if dispatchErr := sys.CommandBus.Dispatch(ctx, primaryCmd); dispatchErr == nil {
				primarySessionID = primaryCmd.AggregateID()
				// Wait for projection
				waitCtx, waitCancel := context.WithTimeout(ctx, 5*time.Second)
				defer waitCancel()
				for {
					var exists bool
					_ = s.db.GetContext(waitCtx, &exists, `SELECT EXISTS(SELECT 1 FROM agent_sessions WHERE id = $1 AND tenant_id = $2)`, primarySessionID, tenantID)
					if exists || waitCtx.Err() != nil {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
			} else {
				logger.WithFields("agent_id", agentID, "error", dispatchErr.Error()).
					Warn("agents: failed to create primary session during auto-reprovision")
			}
		}
	}

	// Update agent with new sandbox_id and idle status.
	// Use context.Background() — the provisioning context may have expired
	// but we still need to persist the successful result.
	bgCtx := context.Background()
	if primarySessionID != "" {
		s.db.ExecContext(bgCtx, `
			UPDATE agent_definitions
			SET sandbox_id = $2, primary_session_id = $3, lifecycle_status = 'idle', updated_at = NOW()
			WHERE id = $1 AND tenant_id = $4
		`, agentID, newSandboxID, primarySessionID, tenantID)
	} else {
		s.db.ExecContext(bgCtx, `
			UPDATE agent_definitions
			SET sandbox_id = $2, lifecycle_status = 'idle', updated_at = NOW()
			WHERE id = $1 AND tenant_id = $3
		`, agentID, newSandboxID, tenantID)
	}

	logger.WithFields(
		"agent_id", agentID,
		"new_sandbox_id", newSandboxID,
	).Info("agents: auto-reprovisioned persistent agent sandbox")
	s.publishLifecycle(agentrt.LifecycleEvent{
		AgentID:   agentID,
		TenantID:  tenantID,
		NewStatus: "idle",
		SandboxID: newSandboxID,
		Reason:    "auto_reprovision_succeeded",
	})
}

// ─── Shared Provision Helpers ─────────────────────────────────────────────

// buildProvisionConfigFromReadModel builds a trooper.ProvisionConfig from the
// agent read model, applying sensible defaults for unset fields.
func buildProvisionConfigFromReadModel(rm *agentsquery.AgentDefinitionReadModel, tenantID string) trooper.ProvisionConfig {
	image := sandbox.DefaultDevImage
	if rm.SandboxImage.Valid && rm.SandboxImage.String != "" {
		image = rm.SandboxImage.String
	}
	// Fall back to the canonical default rather than to hand-picked
	// numbers. These three used to be 1.0 / 512 / 2048, which predates
	// managed machine profiles and matches none of them, so on managed
	// runtimes every persistent agent with unset sizing columns failed
	// reprovisioning with ErrUnsupportedSandboxSize forever. Sourcing
	// the fallback from DefaultSandboxConfig keeps it a valid profile
	// (nano) by construction, and keeps it that way if the profile
	// table ever changes.
	defaults := sandbox.DefaultSandboxConfig()
	cpuLimit := defaults.CPULimit
	if rm.SandboxCPULimit.Valid && rm.SandboxCPULimit.Float64 > 0 {
		cpuLimit = rm.SandboxCPULimit.Float64
	}
	memoryMB := defaults.MemoryMB
	if rm.SandboxMemoryMB.Valid && rm.SandboxMemoryMB.Int32 > 0 {
		memoryMB = int64(rm.SandboxMemoryMB.Int32)
	}
	diskMB := defaults.DiskMB
	if rm.SandboxDiskMB.Valid && rm.SandboxDiskMB.Int32 > 0 {
		diskMB = int64(rm.SandboxDiskMB.Int32)
	}
	networkMode := "allow"
	if rm.SandboxNetworkMode.Valid && rm.SandboxNetworkMode.String != "" {
		networkMode = rm.SandboxNetworkMode.String
	}

	// Build the allowlist from the read-model column. Until now this
	// function returned a ProvisionConfig with AllowedHosts = nil — every
	// persistent agent's trooper sandbox was provisioned with an empty
	// allowlist regardless of what the operator typed into the form. With
	// network_mode = whitelist that means the per-VM DNS proxy NXDOMAINs
	// every lookup. Indistinguishable from a real network bug, which is
	// what we spent three days chasing.
	var allowedHosts []string
	if len(rm.SandboxAllowedHosts) > 0 {
		allowedHosts = append(allowedHosts, []string(rm.SandboxAllowedHosts)...)
	}
	// Mirror the ParseSandboxConfig behaviour: in whitelist mode with no
	// explicit hosts, fall back to the package-registry defaults so the
	// admin form's "always included" promise is real.
	if networkMode == "whitelist" && len(allowedHosts) == 0 {
		allowedHosts = sandbox.DefaultAllowedHosts()
	}

	sshEnabled := false
	if rm.SandboxSSHEnabled.Valid {
		sshEnabled = rm.SandboxSSHEnabled.Bool
	}
	gitURL := ""
	if rm.SandboxGitRepoURL.Valid {
		gitURL = rm.SandboxGitRepoURL.String
	}
	gitBranch := ""
	if rm.SandboxGitBranch.Valid {
		gitBranch = rm.SandboxGitBranch.String
	}
	sqlitePath := "/workspace/data/trooper.db"
	if rm.DBSqlitePath.Valid && rm.DBSqlitePath.String != "" {
		sqlitePath = rm.DBSqlitePath.String
	}
	lancedbPath := "/workspace/data/lancedb"
	if rm.DBLanceDBPath.Valid && rm.DBLanceDBPath.String != "" {
		lancedbPath = rm.DBLanceDBPath.String
	}
	redbPath := "/workspace/data/trooper.redb"
	if rm.DBRedbPath.Valid && rm.DBRedbPath.String != "" {
		redbPath = rm.DBRedbPath.String
	}

	var envVars map[string]string
	if len(rm.SandboxEnvVars) > 0 {
		if err := json.Unmarshal(rm.SandboxEnvVars, &envVars); err != nil {
			logger.WithFields("agent_id", rm.ID, "error", err.Error()).
				Warn("failed to parse sandbox env vars")
		}
	}

	// Parse browser config from agent config JSON
	var browserSidecar *sandbox.BrowserSidecarConfig
	if len(rm.Config) > 0 {
		var agentConfig map[string]interface{}
		if err := json.Unmarshal(rm.Config, &agentConfig); err == nil {
			browserCfg := sandbox.ParseBrowserConfig(agentConfig)
			// Headed mode is gated at session-level call sites via
			// SandboxManager.IsBrowserHeadedEnabled. At provision time
			// we respect the raw config — the sidecar image supports both modes.
			browserSidecar = browserCfg.ToSidecarConfig()
		}
	}

	// Composer's "Working Directory" field lives on agents.working_directory.
	// Empty / unset → trooper.Provision lets the sandbox manager fall back
	// to the canonical default (/workspace); a non-empty value flows all the
	// way through to InstanceConfig.WorkDir and becomes the shell's landing
	// dir + the agent file tools' validation root.
	workDir := ""
	if rm.WorkingDirectory.Valid {
		workDir = strings.TrimSpace(rm.WorkingDirectory.String)
	}

	return trooper.ProvisionConfig{
		TrooperID:      rm.ID,
		TenantID:       tenantID,
		Name:           rm.Name,
		Image:          image,
		CPULimit:       cpuLimit,
		MemoryMB:       memoryMB,
		DiskMB:         diskMB,
		NetworkMode:    networkMode,
		AllowedHosts:   allowedHosts,
		EnvVars:        envVars,
		SSHEnabled:     sshEnabled,
		GitRepoURL:     gitURL,
		GitBranch:      gitBranch,
		BrowserSidecar: browserSidecar,
		WorkDir:        workDir,
		Identity: trooper.IdentityFiles{
			SoulMD:     rm.SoulMD,
			IdentityMD: rm.IdentityMD,
			UserMD:     rm.UserMD,
			RoleMD:     rm.RoleMD,
		},
		Databases: trooper.DatabaseConfig{
			SqlitePath:  sqlitePath,
			LanceDBPath: lancedbPath,
			RedbPath:    redbPath,
		},
	}
}

// maybeReprovisionAgent checks whether the UpdateAgentCommand changed any
// sandbox-infrastructure fields on a provisioned persistent agent, and if so,
// terminates the old sandbox and provisions a new one. If only identity files
// changed, it syncs them to the running sandbox without full reprovision.
// Workdir changes get a third lighter-still path: live update on the
// running sandbox so the next Shell/Exec lands in the new directory
// without any reprovision or restart.
func (s *Server) maybeReprovisionAgent(ctx context.Context, rm *agentsquery.AgentDefinitionReadModel, cmd *agentscmd.UpdateAgentCommand) {
	infraChanged := s.detectInfraChanges(rm, cmd)
	identityChanged := s.detectIdentityChanges(rm, cmd)
	workDirChanged := s.detectWorkDirChange(rm, cmd)

	logger.WithFields(
		"agent_id", rm.ID,
		"infra_changed", infraChanged,
		"identity_changed", identityChanged,
		"workdir_changed", workDirChanged,
	).Info("agents: maybeReprovisionAgent check")

	// Workdir lives on the agent definition (not in the sandbox config blob
	// the way image/cpu/mem do), so it has its own short-circuit. Done before
	// the early return so a workdir-only update isn't a no-op.
	if workDirChanged && rm.SandboxID.Valid && rm.SandboxID.String != "" {
		newDir := ""
		if cmd.WorkingDirectory != nil {
			newDir = strings.TrimSpace(*cmd.WorkingDirectory)
		}
		if newDir != "" && s.sandboxMgr != nil {
			if err := s.sandboxMgr.UpdateInstanceWorkDir(ctx, rm.SandboxID.String, newDir); err != nil {
				logger.WithFields(
					"agent_id", rm.ID,
					"sandbox_id", rm.SandboxID.String,
					"new_workdir", newDir,
					"error", err.Error(),
				).Warn("agents: failed to propagate workdir to live sandbox; next session will still pick it up from DB")
			} else {
				logger.WithFields(
					"agent_id", rm.ID,
					"sandbox_id", rm.SandboxID.String,
					"new_workdir", newDir,
				).Info("agents: live workdir propagated to running sandbox")
			}
		}
	}

	if !infraChanged && !identityChanged {
		return
	}

	// Linked agents share another session's sandbox; we must never destroy
	// it to apply this agent's resource changes. Resource-shaped updates do
	// not apply to a linked sandbox — log and skip the infra branch. Identity
	// sync below still runs against the shared sandbox.
	if linkedSessionID := linkedSessionIDFromAgentConfig(rm.Config); linkedSessionID != "" {
		if infraChanged {
			logger.WithFields("agent_id", rm.ID, "linked_session_id", linkedSessionID).
				Info("agents: skipping reprovision on update — agent is linked to an existing sandbox; resource changes do not apply")
		}
		infraChanged = false
	}

	if infraChanged {
		// Refuse reprovision if a session is actively running
		if rm.PrimarySessionID.Valid && rm.PrimarySessionID.String != "" {
			runner := s.sessionMgr.GetRunner(rm.PrimarySessionID.String)
			if runner != nil && runner.IsRunning() {
				logger.WithFields("agent_id", rm.ID).
					Warn("skipping reprovision: session is actively running; retry after session completes")
				return
			}
		}

		// Set lifecycle_status to 'provisioning'
		if s.db != nil {
			_, err := s.db.ExecContext(ctx, `
				UPDATE agent_definitions SET lifecycle_status = 'provisioning', updated_at = NOW()
				WHERE id = $1 AND tenant_id = $2
			`, rm.ID, rm.TenantID)
			if err != nil {
				logger.WithFields("agent_id", rm.ID, "error", err.Error()).
					Warn("failed to set lifecycle_status to provisioning for reprovision")
			}
		}

		// Terminate old sandbox
		oldSandboxID := rm.SandboxID.String
		if err := s.sandboxMgr.TerminateSandbox(ctx, oldSandboxID); err != nil {
			logger.WithFields("agent_id", rm.ID, "sandbox_id", oldSandboxID, "error", err.Error()).
				Error("failed to terminate old sandbox during reprovision")
			// Revert lifecycle_status to idle since reprovision failed
			if s.db != nil {
				s.db.ExecContext(context.Background(), `UPDATE agent_definitions SET lifecycle_status = 'idle', updated_at = NOW() WHERE id = $1 AND tenant_id = $2`, rm.ID, rm.TenantID)
			}
			return
		}

		// Re-read agent to get updated field values (the command has already been dispatched)
		newRM := s.reloadAgentReadModel(ctx, rm.ID, rm.TenantID)
		if newRM == nil {
			logger.WithFields("agent_id", rm.ID).
				Error("failed to reload agent after update for reprovision")
			if s.db != nil {
				s.db.ExecContext(context.Background(), `UPDATE agent_definitions SET lifecycle_status = 'idle', updated_at = NOW() WHERE id = $1 AND tenant_id = $2`, rm.ID, rm.TenantID)
			}
			return
		}

		// Provision new sandbox using the updated read model
		cfg := buildProvisionConfigFromReadModel(newRM, rm.TenantID)
		newSandboxID, err := s.trooperMgr.Provision(ctx, cfg)
		if err != nil {
			logger.WithFields("agent_id", rm.ID, "error", err.Error()).
				Error("failed to provision new sandbox during reprovision")
			if s.db != nil {
				s.db.ExecContext(context.Background(), `UPDATE agent_definitions SET lifecycle_status = 'idle', sandbox_id = NULL, updated_at = NOW() WHERE id = $1 AND tenant_id = $2`, rm.ID, rm.TenantID)
			}
			return
		}

		// Update sandbox_id and lifecycle_status
		if s.db != nil {
			_, err := s.db.ExecContext(context.Background(), `
				UPDATE agent_definitions SET sandbox_id = $2, lifecycle_status = 'idle', updated_at = NOW()
				WHERE id = $1 AND tenant_id = $3
			`, rm.ID, newSandboxID, rm.TenantID)
			if err != nil {
				logger.WithFields("agent_id", rm.ID, "error", err.Error()).
					Warn("failed to update sandbox_id after reprovision")
			}
		}

		logger.WithFields("agent_id", rm.ID, "old_sandbox_id", oldSandboxID, "new_sandbox_id", newSandboxID).
			Info("reprovisioned persistent agent sandbox after config update")
		return
	}

	// Identity-only change — sync files to running sandbox
	if identityChanged {
		identity := trooper.IdentityFiles{
			SoulMD:     rm.SoulMD,
			IdentityMD: rm.IdentityMD,
			UserMD:     rm.UserMD,
			RoleMD:     rm.RoleMD,
		}
		// Apply new values from the command
		if cmd.SoulMD != nil {
			identity.SoulMD = *cmd.SoulMD
		}
		if cmd.IdentityMD != nil {
			identity.IdentityMD = *cmd.IdentityMD
		}
		if cmd.UserMD != nil {
			identity.UserMD = *cmd.UserMD
		}
		if cmd.RoleMD != nil {
			identity.RoleMD = *cmd.RoleMD
		}
		if err := s.trooperMgr.SyncIdentityFiles(ctx, "trp-"+rm.ID, identity); err != nil {
			logger.WithFields("agent_id", rm.ID, "error", err.Error()).
				Warn("failed to sync identity files after update")
		} else {
			logger.WithFields("agent_id", rm.ID).
				Info("synced identity files to running sandbox after update")
		}
	}
}

// detectInfraChanges returns true if the update command changes any sandbox
// infrastructure field that requires a full reprovision.
func (s *Server) detectInfraChanges(rm *agentsquery.AgentDefinitionReadModel, cmd *agentscmd.UpdateAgentCommand) bool {
	if cmd.SandboxImage != nil && *cmd.SandboxImage != rm.SandboxImage.String {
		return true
	}
	if cmd.SandboxCPULimit != nil && *cmd.SandboxCPULimit != rm.SandboxCPULimit.Float64 {
		return true
	}
	if cmd.SandboxMemoryMB != nil && *cmd.SandboxMemoryMB != int64(rm.SandboxMemoryMB.Int32) {
		return true
	}
	if cmd.SandboxDiskMB != nil && *cmd.SandboxDiskMB != int64(rm.SandboxDiskMB.Int32) {
		return true
	}
	if cmd.SandboxNetworkMode != nil && *cmd.SandboxNetworkMode != rm.SandboxNetworkMode.String {
		return true
	}
	if cmd.SandboxSSHEnabled != nil && *cmd.SandboxSSHEnabled != rm.SandboxSSHEnabled.Bool {
		return true
	}
	if cmd.SandboxGitRepoURL != nil && *cmd.SandboxGitRepoURL != rm.SandboxGitRepoURL.String {
		return true
	}
	if cmd.SandboxGitBranch != nil && *cmd.SandboxGitBranch != rm.SandboxGitBranch.String {
		return true
	}
	if cmd.SandboxTimeoutSeconds != nil && *cmd.SandboxTimeoutSeconds != rm.SandboxTimeoutSeconds.Int32 {
		return true
	}
	if len(cmd.SandboxAllowedHosts) > 0 && !reflect.DeepEqual(cmd.SandboxAllowedHosts, []string(rm.SandboxAllowedHosts)) {
		return true
	}
	if len(cmd.SandboxEnvVars) > 0 {
		var existingEnvVars map[string]string
		if len(rm.SandboxEnvVars) > 0 {
			json.Unmarshal(rm.SandboxEnvVars, &existingEnvVars)
		}
		if !reflect.DeepEqual(cmd.SandboxEnvVars, existingEnvVars) {
			return true
		}
	}
	return false
}

// detectWorkDirChange returns true if the update command sets a new
// working directory that differs from the agent's currently-persisted
// value. Treats nil (no field in the update) as "no change" and any
// whitespace-only string as equivalent to its trimmed form so a stray
// space doesn't tear down a live sandbox path.
func (s *Server) detectWorkDirChange(rm *agentsquery.AgentDefinitionReadModel, cmd *agentscmd.UpdateAgentCommand) bool {
	if cmd.WorkingDirectory == nil {
		return false
	}
	newDir := strings.TrimSpace(*cmd.WorkingDirectory)
	currentDir := ""
	if rm.WorkingDirectory.Valid {
		currentDir = strings.TrimSpace(rm.WorkingDirectory.String)
	}
	return newDir != currentDir
}

// detectIdentityChanges returns true if the update command changes any
// identity field (soul_md, identity_md, user_md, role_md).
func (s *Server) detectIdentityChanges(rm *agentsquery.AgentDefinitionReadModel, cmd *agentscmd.UpdateAgentCommand) bool {
	if cmd.SoulMD != nil && *cmd.SoulMD != rm.SoulMD {
		return true
	}
	if cmd.IdentityMD != nil && *cmd.IdentityMD != rm.IdentityMD {
		return true
	}
	if cmd.UserMD != nil && *cmd.UserMD != rm.UserMD {
		return true
	}
	if cmd.RoleMD != nil && *cmd.RoleMD != rm.RoleMD {
		return true
	}
	return false
}

// reloadAgentReadModel re-reads an agent from the DB via the query bus.
func (s *Server) reloadAgentReadModel(ctx context.Context, agentID, tenantID string) *agentsquery.AgentDefinitionReadModel {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil
	}
	q := agentsquery.NewGetAgentByIDQuery(agentID, tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil || res == nil {
		return nil
	}
	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}
	rm, err := agentReadModelFromQueryResult(data)
	if err != nil {
		return nil
	}
	return rm
}
