package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"connectrpc.com/connect"
	agentmem "github.com/everstacklabs/everstack/internal/agents/memory"
	agentpolicy "github.com/everstacklabs/everstack/internal/agents/policy"
	agentrt "github.com/everstacklabs/everstack/internal/agents/runtime"
	agenttools "github.com/everstacklabs/everstack/internal/agents/runtime/tools"
	agentskills "github.com/everstacklabs/everstack/internal/agents/skills"
	agentscmd "github.com/everstacklabs/everstack/internal/commands/handlers/agents"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/utils"
	sandboxlc "github.com/everstacklabs/everstack/internal/orchestrator/sandbox"
	"github.com/everstacklabs/everstack/internal/query"
	agentsquery "github.com/everstacklabs/everstack/internal/query/handlers/agents"
	"github.com/everstacklabs/everstack/internal/sandbox"
	sandboxcp "github.com/everstacklabs/everstack/internal/sandbox/controlplane"
	"github.com/everstacklabs/everstack/internal/storageauth"
	"github.com/everstacklabs/everstack/internal/telemetry"
	attrs "github.com/everstacklabs/everstack/internal/telemetry/attributes"
	"github.com/everstacklabs/everstack/internal/telemetry/autoscorer"
	"github.com/everstacklabs/everstack/internal/telemetry/metrics"
	"github.com/everstacklabs/everstack/internal/trooper"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// resolveUserID extracts the user ID from context. In self-hosted deployments
// the admin UI has no user authentication, so we fall back to "admin".
// resolveUserID returns the caller's user identity for ownership +
// audit columns. Resolution order matches how the request actually
// landed:
//
//  1. UserIDKey  — set by service-internal handlers that already
//     resolved a user (rare on the request path).
//  2. CloudUserIDKey — set by tenant_middleware.Wrap after a
//     successful cloud session validation. This is the
//     primary source for browser-cookie traffic on a
//     tenant subdomain. WITHOUT this lookup, every
//     cookie-authenticated request falls through to
//     "anonymous" and writes that into ownership rows
//     (e.g. user_ssh_keys.user_id="anonymous"), which
//     then prevents auth lookups from matching real
//     grants.
//  3. ExtractUserID — gRPC metadata headers + API-key hash derivation
//     for programmatic clients.
//  4. "admin" — last-resort fallback for unauthenticated internal
//     callers (cron sweeps, idle reapers).
func (s *Server) resolveUserID(ctx context.Context) string {
	directUserID := contextkeys.GetUserID(ctx)
	cloudUserID := contextkeys.CloudUserIDFromContext(ctx)
	apiKeyHash := contextkeys.ExtractAPIKeyHash(ctx)
	extracted := contextkeys.ExtractUserID(ctx, apiKeyHash)

	userID := directUserID
	if userID == "" {
		userID = cloudUserID
	}
	if userID == "" {
		userID = extracted
	}
	if userID == "" || userID == "anonymous" {
		userID = "admin"
	}

	// Warn only when a request carried some auth signal but still landed on the
	// admin fallback. Plain standalone self-hosted traffic is expected to resolve
	// this way and should not spam warning logs.
	if userID == "admin" {
		fields := []interface{}{
			"direct_user_id", directUserID,
			"cloud_user_id", cloudUserID,
			"extracted_user_id", extracted,
			"api_key_hash_present", apiKeyHash != "",
		}
		if directUserID != "" || cloudUserID != "" || apiKeyHash != "" || (extracted != "" && extracted != "anonymous") {
			logger.WithFields(fields...).Warn("resolveUserID: fell through to admin fallback")
		} else {
			logger.WithFields(fields...).Debug("resolveUserID: using standalone admin fallback")
		}
	}
	return userID
}

// resolveTenantID returns the tenant id for the current request.
//
// History: this function used to (1) fall back to a client-supplied
// requestTenantID when the auth context was empty, and (2) fall back to the
// first row of the organizations table in self-hosted deployments. In any
// cloud deployment with more than one tenant, an unauthenticated request
// would silently inherit either the caller-chosen tenant id or the oldest
// organization's id, and every downstream query would run scoped to a
// stranger's data. That is the cross-tenant leak this rewrite closes.
//
// New contract:
//   - The auth middleware is the single trusted source. The tenant id is
//     read from context only.
//   - The requestTenantID parameter is intentionally ignored; it stays on
//     the signature so callers compile but is never consulted.
//   - The "first organization in the DB" fallback survives only when there
//     is exactly one organization (genuinely single-tenant self-hosted).
//     Any second org disengages the fallback for the lifetime of the
//     process.
func (s *Server) resolveTenantID(ctx context.Context, _ string) (string, error) {
	if tid := contextkeys.GetTenantID(ctx); tid != "" {
		return tid, nil
	}
	if tid := contextkeys.ExtractTenantID(ctx); tid != "" {
		return tid, nil
	}
	if s.db != nil {
		if tid, ok := s.singleTenantFallback(ctx); ok {
			return tid, nil
		}
	}
	return "", connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
}

// singleTenantFallback returns the only organization id in the database
// when — and only when — there is exactly one. In a multi-tenant
// deployment any other answer would mean serving foreign data, so we
// refuse. The result is cached on the Server because every sandbox RPC
// hits this path; a COUNT(*) on each call would be wasteful.
func (s *Server) singleTenantFallback(ctx context.Context) (string, bool) {
	if cached := s.singleTenantCache.Load(); cached != nil {
		return cached.tenantID, cached.ok
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	var count int
	if err := s.db.GetContext(lookupCtx, &count,
		`SELECT COUNT(*) FROM organizations`); err != nil {
		return "", false
	}
	if count != 1 {
		s.singleTenantCache.Store(&singleTenantCacheEntry{ok: false})
		return "", false
	}
	var orgID string
	if err := s.db.GetContext(lookupCtx, &orgID,
		`SELECT id::text FROM organizations LIMIT 1`); err != nil {
		return "", false
	}
	s.singleTenantCache.Store(&singleTenantCacheEntry{tenantID: orgID, ok: true})
	return orgID, true
}

func protoAgentModeToString(mode agentspb.AgentMode) string {
	switch mode {
	case agentspb.AgentMode_AGENT_MODE_PRIMARY:
		return "primary"
	case agentspb.AgentMode_AGENT_MODE_SUBAGENT:
		return "subagent"
	default:
		return ""
	}
}

func stringToProtoAgentMode(mode string) agentspb.AgentMode {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "subagent":
		return agentspb.AgentMode_AGENT_MODE_SUBAGENT
	case "primary":
		return agentspb.AgentMode_AGENT_MODE_PRIMARY
	default:
		return agentspb.AgentMode_AGENT_MODE_UNSPECIFIED
	}
}

func protoAgentLifecycleModeToString(mode agentspb.AgentLifecycleMode) string {
	switch mode {
	case agentspb.AgentLifecycleMode_AGENT_LIFECYCLE_MODE_EPHEMERAL:
		return "ephemeral"
	case agentspb.AgentLifecycleMode_AGENT_LIFECYCLE_MODE_PERSISTENT:
		return "persistent"
	default:
		return ""
	}
}

func stringToProtoAgentLifecycleMode(mode string) agentspb.AgentLifecycleMode {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "ephemeral":
		return agentspb.AgentLifecycleMode_AGENT_LIFECYCLE_MODE_EPHEMERAL
	case "persistent":
		return agentspb.AgentLifecycleMode_AGENT_LIFECYCLE_MODE_PERSISTENT
	default:
		return agentspb.AgentLifecycleMode_AGENT_LIFECYCLE_MODE_UNSPECIFIED
	}
}

func protoAgentLifecycleStatusToString(status agentspb.AgentLifecycleStatus) string {
	switch status {
	case agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_CREATED:
		return "created"
	case agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_PROVISIONING:
		return "provisioning"
	case agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_RUNNING:
		return "running"
	case agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_SLEEPING:
		return "sleeping"
	case agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_WAKING:
		return "waking"
	case agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_FAILED:
		return "failed"
	case agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_TERMINATED:
		return "terminated"
	case agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_IDLE:
		return "idle"
	default:
		return ""
	}
}

func stringToProtoAgentLifecycleStatus(status string) agentspb.AgentLifecycleStatus {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "created":
		return agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_CREATED
	case "provisioning":
		return agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_PROVISIONING
	case "running":
		return agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_RUNNING
	case "sleeping":
		return agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_SLEEPING
	case "waking":
		return agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_WAKING
	case "failed":
		return agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_FAILED
	case "terminated":
		return agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_TERMINATED
	case "idle":
		return agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_IDLE
	default:
		return agentspb.AgentLifecycleStatus_AGENT_LIFECYCLE_STATUS_UNSPECIFIED
	}
}

func protoAgentLinkTypeToString(lt agentspb.AgentLinkType) string {
	switch lt {
	case agentspb.AgentLinkType_AGENT_LINK_TYPE_COLLABORATOR:
		return "collaborator"
	case agentspb.AgentLinkType_AGENT_LINK_TYPE_SUPERVISOR:
		return "supervisor"
	case agentspb.AgentLinkType_AGENT_LINK_TYPE_SUBORDINATE:
		return "subordinate"
	case agentspb.AgentLinkType_AGENT_LINK_TYPE_PEER:
		return "peer"
	default:
		return "peer"
	}
}

func stringToProtoAgentLinkType(lt string) agentspb.AgentLinkType {
	switch strings.ToLower(strings.TrimSpace(lt)) {
	case "collaborator":
		return agentspb.AgentLinkType_AGENT_LINK_TYPE_COLLABORATOR
	case "supervisor":
		return agentspb.AgentLinkType_AGENT_LINK_TYPE_SUPERVISOR
	case "subordinate":
		return agentspb.AgentLinkType_AGENT_LINK_TYPE_SUBORDINATE
	case "peer":
		return agentspb.AgentLinkType_AGENT_LINK_TYPE_PEER
	default:
		return agentspb.AgentLinkType_AGENT_LINK_TYPE_UNSPECIFIED
	}
}

func protoAgentLinkProtocolToString(p agentspb.AgentLinkProtocol) string {
	switch p {
	case agentspb.AgentLinkProtocol_AGENT_LINK_PROTOCOL_INTERNAL:
		return "internal"
	case agentspb.AgentLinkProtocol_AGENT_LINK_PROTOCOL_CHANNEL:
		return "channel"
	case agentspb.AgentLinkProtocol_AGENT_LINK_PROTOCOL_WEBHOOK:
		return "webhook"
	default:
		return "internal"
	}
}

func stringToProtoAgentLinkProtocol(p string) agentspb.AgentLinkProtocol {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case "internal":
		return agentspb.AgentLinkProtocol_AGENT_LINK_PROTOCOL_INTERNAL
	case "channel":
		return agentspb.AgentLinkProtocol_AGENT_LINK_PROTOCOL_CHANNEL
	case "webhook":
		return agentspb.AgentLinkProtocol_AGENT_LINK_PROTOCOL_WEBHOOK
	default:
		return agentspb.AgentLinkProtocol_AGENT_LINK_PROTOCOL_UNSPECIFIED
	}
}

func stringToProtoAgentLinkStatus(s string) agentspb.AgentLinkStatus {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "active":
		return agentspb.AgentLinkStatus_AGENT_LINK_STATUS_ACTIVE
	case "paused":
		return agentspb.AgentLinkStatus_AGENT_LINK_STATUS_PAUSED
	case "revoked":
		return agentspb.AgentLinkStatus_AGENT_LINK_STATUS_REVOKED
	default:
		return agentspb.AgentLinkStatus_AGENT_LINK_STATUS_UNSPECIFIED
	}
}

func protoTaskPermissionToString(mode agentspb.TaskPermissionMode) string {
	switch mode {
	case agentspb.TaskPermissionMode_TASK_PERMISSION_MODE_ALWAYS:
		return "always"
	case agentspb.TaskPermissionMode_TASK_PERMISSION_MODE_DENY:
		return "deny"
	case agentspb.TaskPermissionMode_TASK_PERMISSION_MODE_ASK:
		return "ask"
	default:
		return ""
	}
}

func stringToProtoTaskPermissionMode(mode string) agentspb.TaskPermissionMode {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "always":
		return agentspb.TaskPermissionMode_TASK_PERMISSION_MODE_ALWAYS
	case "deny":
		return agentspb.TaskPermissionMode_TASK_PERMISSION_MODE_DENY
	case "ask":
		return agentspb.TaskPermissionMode_TASK_PERMISSION_MODE_ASK
	default:
		return agentspb.TaskPermissionMode_TASK_PERMISSION_MODE_UNSPECIFIED
	}
}

func configStringValue(config map[string]interface{}, key string) (string, bool) {
	if config == nil {
		return "", false
	}
	v, ok := config[key]
	if !ok || v == nil {
		return "", false
	}
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", false
	}
	return s, true
}

func configBoolValue(config map[string]interface{}, key string) (bool, bool) {
	if config == nil {
		return false, false
	}
	v, ok := config[key]
	if !ok || v == nil {
		return false, false
	}
	b, ok := v.(bool)
	return b, ok
}

func configInt32Value(config map[string]interface{}, key string) (*int32, bool) {
	n, ok := numericConfigValue(config, key)
	if !ok {
		return nil, false
	}
	v := int32(n)
	return &v, true
}

func normalizedOptionalString(raw *string) *string {
	if raw == nil {
		return nil
	}
	v := strings.TrimSpace(*raw)
	if v == "" {
		return nil
	}
	return &v
}

// ============================================================================
// Agent CRUD
// ============================================================================

func (s *Server) CreateAgent(ctx context.Context, req *connect.Request[agentspb.CreateAgentRequest]) (*connect.Response[agentspb.CreateAgentResponse], error) {

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

	mode := protoAgentModeToString(req.Msg.GetMode())
	if mode == "" {
		if cfgMode, ok := configStringValue(config, "mode"); ok {
			mode = cfgMode
		} else {
			mode = "primary"
		}
	}

	taskPermissionMode := protoTaskPermissionToString(req.Msg.GetTaskPermissionMode())
	if taskPermissionMode == "" && req.Msg.GetExecutionPolicy() != nil {
		taskPermissionMode = protoTaskPermissionToString(req.Msg.GetExecutionPolicy().GetTaskPermissionMode())
	}
	if taskPermissionMode == "" {
		if cfgTaskMode, ok := configStringValue(config, "task_permission_mode"); ok {
			taskPermissionMode = cfgTaskMode
		} else {
			taskPermissionMode = "ask"
		}
	}

	maxSteps := req.Msg.MaxSteps
	if maxSteps == nil && req.Msg.GetExecutionPolicy() != nil {
		maxSteps = req.Msg.GetExecutionPolicy().MaxSteps
	}
	if maxSteps == nil {
		if cfgMaxSteps, ok := configInt32Value(config, "max_steps"); ok {
			maxSteps = cfgMaxSteps
		}
	}

	workingDirectory := normalizedOptionalString(req.Msg.WorkingDirectory)
	if workingDirectory == nil && req.Msg.GetExecutionPolicy() != nil {
		workingDirectory = normalizedOptionalString(req.Msg.GetExecutionPolicy().WorkingDirectory)
	}
	if workingDirectory == nil {
		if cfgWorkDir, ok := configStringValue(config, "working_directory"); ok {
			workingDirectory = &cfgWorkDir
		}
	}

	color := normalizedOptionalString(req.Msg.Color)
	if color == nil {
		if cfgColor, ok := configStringValue(config, "color"); ok {
			color = &cfgColor
		}
	}

	mentionAlias := normalizedOptionalString(req.Msg.MentionAlias)
	if mentionAlias == nil {
		if cfgMentionAlias, ok := configStringValue(config, "mention_alias"); ok {
			mentionAlias = &cfgMentionAlias
		}
	}

	hidden := false
	if req.Msg.Hidden != nil {
		hidden = req.Msg.GetHidden()
	} else if cfgHidden, ok := configBoolValue(config, "hidden"); ok {
		hidden = cfgHidden
	} else if mode == "subagent" {
		hidden = true
	}

	// Validate HITL config if present
	if err := agentrt.ValidateHITLConfig(config); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}

	// Serialized quota gate: holds a per-(tenant, AGENTS) advisory lock across
	// check -> command dispatch -> read-model confirmation, so concurrent
	// CreateAgent calls cannot race past the cap through the async projection
	// window (editions-and-billing.md, atomic enforcement).
	slot, err := enterprise.ReserveResourceSlot(ctx, s.db, enterprise.LicenseMonitorFromContext(ctx),
		enterprise.UsageTypeAgents,
		`SELECT COUNT(*) FROM agent_definitions WHERE tenant_id = $1 AND deleted_at IS NULL`,
		[]interface{}{tenantID}, 1, "agent", tenantID)
	if err != nil {
		return nil, connect.NewError(connect.CodeResourceExhausted, err)
	}
	defer slot.Release()

	// Check for duplicate agent name within tenant
	if s.db != nil {
		var exists bool
		if checkErr := s.db.GetContext(ctx, &exists, `
			SELECT EXISTS(
				SELECT 1 FROM agent_definitions
				WHERE tenant_id = $1 AND name = $2 AND deleted_at IS NULL
			)
		`, tenantID, req.Msg.GetName()); checkErr == nil && exists {
			err := connect.NewError(connect.CodeAlreadyExists, fmt.Errorf("agent with name %q already exists", req.Msg.GetName()))
			return nil, err
		}
	}

	cmd := agentscmd.NewCreateAgentCommand(
		tenantID,
		req.Msg.GetName(),
		req.Msg.GetDescription(),
		req.Msg.GetModel(),
		req.Msg.GetSystemPrompt(),
		req.Msg.GetTools(),
		config,
		req.Msg.GetMaxTurns(),
		req.Msg.GetMaxToolCallsPerTurn(),
		mode,
		maxSteps,
		taskPermissionMode,
		hidden,
		color,
		workingDirectory,
		mentionAlias,
		protoAgentLifecycleModeToString(req.Msg.GetLifecycleMode()),
		normalizedOptionalString(req.Msg.Icon),
		req.Msg.GetIdentity().GetSoulMd(),
		req.Msg.GetIdentity().GetIdentityMd(),
		req.Msg.GetIdentity().GetUserMd(),
		req.Msg.GetIdentity().GetRoleMd(),
		req.Msg.GetSandboxConfig().GetImage(),
		req.Msg.GetSandboxConfig().GetNetworkMode(),
		req.Msg.GetSandboxConfig().GetCpuLimit(),
		req.Msg.GetSandboxConfig().GetMemoryMb(),
		req.Msg.GetSandboxConfig().GetDiskMb(),
		req.Msg.GetSandboxConfig().GetTimeoutSeconds(),
		req.Msg.GetSandboxConfig().GetAllowedHosts(),
		req.Msg.GetSandboxConfig().GetEnvVars(),
		req.Msg.GetSandboxConfig().GetSshEnabled(),
		req.Msg.GetSandboxConfig().GetGitRepoUrl(),
		req.Msg.GetSandboxConfig().GetGitBranch(),
		req.Msg.GetDatabaseConfig().GetSqlitePath(),
		req.Msg.GetDatabaseConfig().GetLancedbPath(),
		req.Msg.GetDatabaseConfig().GetRedbPath(),
		req.Msg.GetWorkersConfig().GetMaxConcurrentWorkers(),
		nil, // worker pool config - will be populated from proto struct if present
		req.Msg.GetAutoProvision(),
		userID,
		"", // traceID
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// If auto_provision is set and this is a persistent agent, provision the
	// sandbox in the background (same pattern as trooper auto-provision).
	logger.WithFields(
		"agent_id", cmd.ID,
		"auto_provision", req.Msg.GetAutoProvision(),
		"lifecycle_mode", protoAgentLifecycleModeToString(req.Msg.GetLifecycleMode()),
		"has_sandbox_config", req.Msg.GetSandboxConfig() != nil,
	).Info("agents: create auto-provision check")
	if req.Msg.GetAutoProvision() && protoAgentLifecycleModeToString(req.Msg.GetLifecycleMode()) == "persistent" {
		logger.WithFields("agent_id", cmd.ID).Info("agents: starting auto-provision goroutine")
		go func() {
			provBg := contextkeys.WithTenantID(context.Background(), tenantID)
			provBg = database.WithTenantSchema(provBg, tenantID)
			provCtx, provCancel := context.WithTimeout(provBg, 5*time.Minute)
			defer provCancel()
			// Wait for agent projection to persist the row before ProvisionAgent
			// tries to load it via query bus. The projection handler runs
			// asynchronously (goroutine) with a 10s timeout, so we allow up
			// to 15s here to account for DB pool contention.
			if s.db != nil {
				waitCtx, waitCancel := context.WithTimeout(provCtx, 60*time.Second)
				defer waitCancel()
				for {
					var exists bool
					_ = s.db.GetContext(waitCtx, &exists, `SELECT EXISTS(SELECT 1 FROM agent_definitions WHERE id = $1 AND tenant_id = $2)`, cmd.ID, tenantID)
					if exists {
						// Row exists in DB. Add a brief settle delay so the
						// query bus handler (which reads from the same table)
						// sees the row on the first attempt.
						select {
						case <-provCtx.Done():
						case <-time.After(1 * time.Second):
						}
						break
					}
					select {
					case <-waitCtx.Done():
						logger.WithFields("agent_id", cmd.ID).
							Warn("timed out waiting for agent projection before auto-provision, proceeding anyway")
						// Don't return — proceed with provision attempt. The projection
						// may land between our last check and the provision call.
						goto provision
					case <-time.After(200 * time.Millisecond):
					}
				}
			}
		provision:
			logger.WithFields("agent_id", cmd.ID).Info("agents: agent projection ready, calling ProvisionAgent")
			provReq := connect.NewRequest(&agentspb.ProvisionAgentRequest{
				AgentId:  cmd.ID,
				TenantId: tenantID,
			})
			if _, err := s.ProvisionAgent(provCtx, provReq); err != nil {
				logger.WithFields("agent_id", cmd.ID, "error", err.Error()).
					Error("failed to auto-provision agent")
			} else {
				logger.WithFields("agent_id", cmd.ID).Info("agents: auto-provision completed successfully")
			}
		}()
	}

	// Hold the quota slot until the read model reflects this agent so the
	// next serialized creator counts it (bounded wait; see ResourceSlot).
	slot.Confirm(ctx)

	return connect.NewResponse(&agentspb.CreateAgentResponse{
		Agent: &agentspb.AgentDefinition{
			Id:                 cmd.ID,
			TenantId:           tenantID,
			Name:               req.Msg.GetName(),
			Model:              req.Msg.GetModel(),
			Enabled:            true,
			Mode:               stringToProtoAgentMode(mode),
			TaskPermissionMode: stringToProtoTaskPermissionMode(taskPermissionMode),
			Hidden:             hidden,
			MaxSteps:           maxSteps,
			Color:              color,
			WorkingDirectory:   workingDirectory,
			MentionAlias:       mentionAlias,
			ExecutionPolicy: &agentspb.AgentExecutionPolicy{
				TaskPermissionMode: stringToProtoTaskPermissionMode(taskPermissionMode),
				MaxSteps:           maxSteps,
				WorkingDirectory:   workingDirectory,
			},
		},
	}), nil
}

func (s *Server) GetAgent(ctx context.Context, req *connect.Request[agentspb.GetAgentRequest]) (*connect.Response[agentspb.GetAgentResponse], error) {

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

	q := agentsquery.NewGetAgentByIDQuery(req.Msg.GetId(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		err := connect.NewError(connect.CodeNotFound, errors.New("agent not found"))
		return nil, err
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}
	if data == nil {
		err := connect.NewError(connect.CodeNotFound, errors.New("agent not found"))
		return nil, err
	}

	rm, ok := data.(*agentsquery.AgentDefinitionReadModel)
	if !ok {
		err := connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
		return nil, err
	}

	return connect.NewResponse(&agentspb.GetAgentResponse{
		Agent: agentReadModelToProto(rm),
	}), nil
}

func (s *Server) ListAgents(ctx context.Context, req *connect.Request[agentspb.ListAgentsRequest]) (*connect.Response[agentspb.ListAgentsResponse], error) {

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

	var enabled *bool
	if req.Msg.Enabled != nil {
		enabled = req.Msg.Enabled
	}
	var modeFilter *string
	if req.Msg.GetMode() != agentspb.AgentMode_AGENT_MODE_UNSPECIFIED {
		m := protoAgentModeToString(req.Msg.GetMode())
		if m != "" {
			modeFilter = &m
		}
	}
	var lifecycleModeFilter *string
	if req.Msg.GetLifecycleMode() != agentspb.AgentLifecycleMode_AGENT_LIFECYCLE_MODE_UNSPECIFIED {
		m := protoAgentLifecycleModeToString(req.Msg.GetLifecycleMode())
		if m != "" {
			lifecycleModeFilter = &m
		}
	}

	q := agentsquery.NewListAgentsQuery(
		tenantID,
		enabled,
		req.Msg.GetIncludeHidden(),
		modeFilter,
		lifecycleModeFilter,
		int(req.Msg.GetLimit()),
		int(req.Msg.GetOffset()),
	)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var agents []*agentspb.AgentDefinition
	if res != nil {
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}
		if list, ok := data.([]agentsquery.AgentDefinitionReadModel); ok {
			for i := range list {
				agents = append(agents, agentReadModelToProto(&list[i]))
			}
		}
	}

	return connect.NewResponse(&agentspb.ListAgentsResponse{
		Agents: agents,
		Total:  int32(len(agents)),
	}), nil
}

func (s *Server) UpdateAgent(ctx context.Context, req *connect.Request[agentspb.UpdateAgentRequest]) (*connect.Response[agentspb.UpdateAgentResponse], error) {

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

	// Load current agent state to detect changes for reprovision
	var rm *agentsquery.AgentDefinitionReadModel
	{
		q := agentsquery.NewGetAgentByIDQuery(req.Msg.GetId(), tenantID)
		res, qErr := sys.QueryBus.Execute(ctx, q)
		if qErr == nil && res != nil {
			var data interface{} = res
			if resp, ok := res.(*query.Response); ok {
				data = resp.Data
			}
			rm, _ = data.(*agentsquery.AgentDefinitionReadModel)
		}
	}

	cmd := agentscmd.NewUpdateAgentCommand(req.Msg.GetId(), tenantID, userID, "")

	if req.Msg.Name != nil {
		cmd.Name = req.Msg.Name
	}
	if req.Msg.Description != nil {
		cmd.Description = req.Msg.Description
	}
	if req.Msg.Model != nil {
		cmd.Model = req.Msg.Model
	}
	if req.Msg.SystemPrompt != nil {
		cmd.SystemPrompt = req.Msg.SystemPrompt
	}
	if req.Msg.GetClearTools() {
		cmd.Tools = []string{}
	} else if req.Msg.Tools != nil {
		cmd.Tools = req.Msg.GetTools()
	}
	if req.Msg.GetConfig() != nil {
		cmd.Config = req.Msg.GetConfig().AsMap()
		// Validate HITL config if present
		if err := agentrt.ValidateHITLConfig(cmd.Config); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
	}
	if req.Msg.MaxTurns != nil {
		cmd.MaxTurns = req.Msg.MaxTurns
	}
	if req.Msg.MaxToolCallsPerTurn != nil {
		cmd.MaxToolCallsPerTurn = req.Msg.MaxToolCallsPerTurn
	}
	if req.Msg.Enabled != nil {
		cmd.Enabled = req.Msg.Enabled
	}
	if req.Msg.Mode != nil {
		mode := protoAgentModeToString(req.Msg.GetMode())
		if mode == "" {
			err := connect.NewError(connect.CodeInvalidArgument, errors.New("invalid mode"))
			return nil, err
		}
		cmd.Mode = &mode
	}
	if req.Msg.MaxSteps != nil {
		cmd.MaxSteps = req.Msg.MaxSteps
	}
	if req.Msg.TaskPermissionMode != nil {
		taskPermissionMode := protoTaskPermissionToString(req.Msg.GetTaskPermissionMode())
		if taskPermissionMode == "" {
			err := connect.NewError(connect.CodeInvalidArgument, errors.New("invalid task_permission_mode"))
			return nil, err
		}
		cmd.TaskPermissionMode = &taskPermissionMode
	}
	if req.Msg.Hidden != nil {
		cmd.Hidden = req.Msg.Hidden
	}
	if req.Msg.Color != nil {
		cmd.Color = normalizedOptionalString(req.Msg.Color)
	}
	if req.Msg.WorkingDirectory != nil {
		cmd.WorkingDirectory = normalizedOptionalString(req.Msg.WorkingDirectory)
	}
	if req.Msg.MentionAlias != nil {
		cmd.MentionAlias = normalizedOptionalString(req.Msg.MentionAlias)
	}
	if req.Msg.GetExecutionPolicy() != nil {
		policy := req.Msg.GetExecutionPolicy()
		if cmd.TaskPermissionMode == nil {
			if taskPermissionMode := protoTaskPermissionToString(policy.GetTaskPermissionMode()); taskPermissionMode != "" {
				cmd.TaskPermissionMode = &taskPermissionMode
			}
		}
		if cmd.MaxSteps == nil && policy.MaxSteps != nil {
			cmd.MaxSteps = policy.MaxSteps
		}
		if cmd.WorkingDirectory == nil && policy.WorkingDirectory != nil {
			cmd.WorkingDirectory = normalizedOptionalString(policy.WorkingDirectory)
		}
	}

	// Backward-compatibility: honor legacy config keys when typed fields are omitted.
	if cmd.Config != nil {
		if cmd.Mode == nil {
			if cfgMode, ok := configStringValue(cmd.Config, "mode"); ok {
				cmd.Mode = &cfgMode
			}
		}
		if cmd.MaxSteps == nil {
			if cfgMaxSteps, ok := configInt32Value(cmd.Config, "max_steps"); ok {
				cmd.MaxSteps = cfgMaxSteps
			}
		}
		if cmd.TaskPermissionMode == nil {
			if cfgTaskMode, ok := configStringValue(cmd.Config, "task_permission_mode"); ok {
				cmd.TaskPermissionMode = &cfgTaskMode
			}
		}
		if cmd.Hidden == nil {
			if cfgHidden, ok := configBoolValue(cmd.Config, "hidden"); ok {
				cmd.Hidden = &cfgHidden
			}
		}
		if cmd.Color == nil {
			if cfgColor, ok := configStringValue(cmd.Config, "color"); ok {
				cmd.Color = &cfgColor
			}
		}
		if cmd.WorkingDirectory == nil {
			if cfgWorkingDirectory, ok := configStringValue(cmd.Config, "working_directory"); ok {
				cmd.WorkingDirectory = &cfgWorkingDirectory
			}
		}
		if cmd.MentionAlias == nil {
			if cfgMentionAlias, ok := configStringValue(cmd.Config, "mention_alias"); ok {
				cmd.MentionAlias = &cfgMentionAlias
			}
		}
	}

	// Persistent agent fields
	if req.Msg.GetIdentity() != nil {
		soulMD := req.Msg.GetIdentity().GetSoulMd()
		cmd.SoulMD = &soulMD
		identityMD := req.Msg.GetIdentity().GetIdentityMd()
		cmd.IdentityMD = &identityMD
		userMD := req.Msg.GetIdentity().GetUserMd()
		cmd.UserMD = &userMD
		roleMD := req.Msg.GetIdentity().GetRoleMd()
		cmd.RoleMD = &roleMD
	}
	if req.Msg.GetSandboxConfig() != nil {
		sc := req.Msg.GetSandboxConfig()
		if sc.GetImage() != "" {
			img := sc.GetImage()
			cmd.SandboxImage = &img
		}
		cpuLimit := sc.GetCpuLimit()
		if cpuLimit > 0 {
			cmd.SandboxCPULimit = &cpuLimit
		}
		memMB := sc.GetMemoryMb()
		if memMB > 0 {
			cmd.SandboxMemoryMB = &memMB
		}
		diskMB := sc.GetDiskMb()
		if diskMB > 0 {
			cmd.SandboxDiskMB = &diskMB
		}
		timeout := sc.GetTimeoutSeconds()
		cmd.SandboxTimeoutSeconds = &timeout
		nm := sc.GetNetworkMode()
		if nm != "" {
			cmd.SandboxNetworkMode = &nm
		}
		if len(sc.GetAllowedHosts()) > 0 {
			cmd.SandboxAllowedHosts = sc.GetAllowedHosts()
		}
		if len(sc.GetEnvVars()) > 0 {
			cmd.SandboxEnvVars = sc.GetEnvVars()
		}
		sshEnabled := sc.GetSshEnabled()
		cmd.SandboxSSHEnabled = &sshEnabled
		gitURL := sc.GetGitRepoUrl()
		if gitURL != "" {
			cmd.SandboxGitRepoURL = &gitURL
		}
		gitBranch := sc.GetGitBranch()
		if gitBranch != "" {
			cmd.SandboxGitBranch = &gitBranch
		}
	}
	if req.Msg.GetDatabaseConfig() != nil {
		dc := req.Msg.GetDatabaseConfig()
		if dc.GetSqlitePath() != "" {
			p := dc.GetSqlitePath()
			cmd.DBSqlitePath = &p
		}
		if dc.GetLancedbPath() != "" {
			p := dc.GetLancedbPath()
			cmd.DBLanceDBPath = &p
		}
		if dc.GetRedbPath() != "" {
			p := dc.GetRedbPath()
			cmd.DBRedbPath = &p
		}
	}
	if req.Msg.GetWorkersConfig() != nil {
		wc := req.Msg.GetWorkersConfig()
		maxW := wc.GetMaxConcurrentWorkers()
		if maxW > 0 {
			cmd.MaxConcurrentWorkers = &maxW
		}
	}
	if req.Msg.Icon != nil {
		cmd.Icon = normalizedOptionalString(req.Msg.Icon)
	}

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Persistent agent reprovision check
	if s.trooperMgr != nil && s.sandboxMgr != nil && rm != nil {
		isPersistent := strings.ToLower(rm.LifecycleMode) == "persistent"
		// Check if lifecycle_mode is being changed to persistent in this update
		if cmd.LifecycleMode != nil && strings.ToLower(*cmd.LifecycleMode) == "persistent" {
			isPersistent = true
		}
		hasSandbox := rm.SandboxID.Valid && rm.SandboxID.String != ""

		sbxCfg := req.Msg.GetSandboxConfig()
		sbxImage := ""
		if sbxCfg != nil {
			sbxImage = sbxCfg.GetImage()
		}
		logger.WithFields(
			"agent_id", rm.ID,
			"is_persistent", isPersistent,
			"has_sandbox", hasSandbox,
			"sandbox_id", rm.SandboxID.String,
			"lifecycle_status", rm.LifecycleStatus,
			"has_sandbox_config", sbxCfg != nil,
			"sandbox_image", sbxImage,
			"rm_sandbox_image", rm.SandboxImage.String,
		).Info("agents: update reprovision check")

		// Verify the sandbox actually exists and is usable at the backend. Merely
		// having an in-memory placeholder is not enough; pending sandboxes should
		// remain provisioning instead of being flipped to idle.
		sandboxActuallyExists := false
		sandboxActuallyRunning := false
		if hasSandbox {
			if inst, err := s.sandboxMgr.BackendStatus(ctx, rm.SandboxID.String); err == nil && inst != nil {
				sandboxActuallyExists = true
				sandboxActuallyRunning = strings.EqualFold(string(inst.Status), "running")
			}
		}

		if isPersistent && hasSandbox && sandboxActuallyExists {
			// Recovery: if lifecycle_status is stuck at "provisioning" or "created"
			// and the sandbox is actually running, reset to "idle".
			lcStatus := strings.ToLower(rm.LifecycleStatus)
			if (lcStatus == "provisioning" || lcStatus == "created") && sandboxActuallyRunning && s.db != nil {
				logger.WithFields("agent_id", rm.ID, "sandbox_id", rm.SandboxID.String).
					Info("agents: recovering stuck lifecycle_status to idle (sandbox exists)")
				s.db.ExecContext(ctx, `UPDATE agent_definitions SET lifecycle_status = 'idle', updated_at = NOW() WHERE id = $1 AND tenant_id = $2`, rm.ID, tenantID)
			}
			// Already provisioned — check if infra/identity changed and reprovision
			s.maybeReprovisionAgent(ctx, rm, cmd)
		} else if isPersistent && hasSandbox && !sandboxActuallyExists {
			// Orphaned sandbox reference — the sandbox was deleted externally.
			// Clear the stale reference and mark the sandbox_instances row as
			// terminated so it doesn't count against the persistent agent limit.
			logger.WithFields("agent_id", rm.ID, "sandbox_id", rm.SandboxID.String).
				Warn("agents: sandbox referenced in DB no longer exists, re-provisioning")
			if s.db != nil {
				s.db.ExecContext(ctx, `UPDATE agent_definitions SET sandbox_id = NULL, lifecycle_status = 'created', updated_at = NOW() WHERE id = $1 AND tenant_id = $2`, rm.ID, tenantID)
				s.db.ExecContext(ctx, `UPDATE sandbox_instances SET lifecycle_state = 'terminated', status = 'terminated', updated_at = NOW() WHERE id = $1 AND tenant_id = $2`, rm.SandboxID.String, tenantID)
			}
			go func() {
				time.Sleep(500 * time.Millisecond)
				provCtx := contextkeys.WithTenantID(context.Background(), tenantID)
				provCtx = database.WithTenantSchema(provCtx, tenantID)
				provReq := connect.NewRequest(&agentspb.ProvisionAgentRequest{
					AgentId:  req.Msg.GetId(),
					TenantId: tenantID,
				})
				if _, err := s.ProvisionAgent(provCtx, provReq); err != nil {
					logger.WithFields("agent_id", req.Msg.GetId(), "error", err.Error()).
						Error("failed to re-provision agent after orphaned sandbox detected")
				}
			}()
		} else if isPersistent && !hasSandbox && (sbxCfg != nil || rm.SandboxImage.Valid) {
			// Newly adding sandbox to a persistent agent — auto-provision
			go func() {
				time.Sleep(500 * time.Millisecond)
				provCtx := contextkeys.WithTenantID(context.Background(), tenantID)
				provCtx = database.WithTenantSchema(provCtx, tenantID)
				provReq := connect.NewRequest(&agentspb.ProvisionAgentRequest{
					AgentId:  req.Msg.GetId(),
					TenantId: tenantID,
				})
				if _, err := s.ProvisionAgent(provCtx, provReq); err != nil {
					logger.WithFields("agent_id", req.Msg.GetId(), "error", err.Error()).
						Error("failed to auto-provision agent after update")
				}
			}()
		}
	}

	return connect.NewResponse(&agentspb.UpdateAgentResponse{
		Agent: &agentspb.AgentDefinition{
			Id:       req.Msg.GetId(),
			TenantId: tenantID,
		},
	}), nil
}

func (s *Server) DeleteAgent(ctx context.Context, req *connect.Request[agentspb.DeleteAgentRequest]) (*connect.Response[agentspb.DeleteAgentResponse], error) {

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

	cmd := agentscmd.NewDeleteAgentCommand(req.Msg.GetId(), tenantID, userID, "")
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.DeleteAgentResponse{
		Success: true,
		Message: "agent deletion dispatched",
	}), nil
}

func (s *Server) ImportAgentFromOpencode(ctx context.Context, req *connect.Request[agentspb.ImportAgentFromOpencodeRequest]) (*connect.Response[agentspb.ImportAgentFromOpencodeResponse], error) {

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	var raw map[string]interface{}
	if err := json.Unmarshal([]byte(req.Msg.GetOpencodeAgentJson()), &raw); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid opencode_agent_json: %w", err))
	}

	warnings := make([]string, 0, 4)
	name, _ := raw["name"].(string)
	model, _ := raw["model"].(string)
	systemPrompt := ""
	if p, ok := raw["prompt"].(string); ok && p != "" {
		systemPrompt = p
	} else if p, ok := raw["system_prompt"].(string); ok && p != "" {
		systemPrompt = p
	}
	if strings.TrimSpace(name) == "" {
		return connect.NewResponse(&agentspb.ImportAgentFromOpencodeResponse{
			Valid:    false,
			Warnings: append(warnings, "name is required in Opencode payload"),
		}), nil
	}
	if strings.TrimSpace(model) == "" {
		return connect.NewResponse(&agentspb.ImportAgentFromOpencodeResponse{
			Valid:    false,
			Warnings: append(warnings, "model is required in Opencode payload"),
		}), nil
	}

	mode := "primary"
	if m, ok := raw["mode"].(string); ok && m != "" {
		mode = strings.ToLower(strings.TrimSpace(m))
	}
	taskPermission := "ask"
	if p, ok := raw["permission"].(map[string]interface{}); ok {
		if task, ok := p["task"].(string); ok && task != "" {
			taskPermission = strings.ToLower(strings.TrimSpace(task))
		}
	}
	hidden := false
	if h, ok := raw["hidden"].(bool); ok {
		hidden = h
	} else if mode == "subagent" {
		hidden = true
	}

	var maxSteps *int32
	if ms, ok := numericConfigValue(raw, "max_steps"); ok && ms > 0 {
		v := int32(ms)
		maxSteps = &v
	} else if ms, ok := numericConfigValue(raw, "maxSteps"); ok && ms > 0 {
		v := int32(ms)
		maxSteps = &v
	}

	var tools []string
	if tRaw, ok := raw["tools"].([]interface{}); ok {
		for _, t := range tRaw {
			if ts, ok := t.(string); ok && strings.TrimSpace(ts) != "" {
				tools = append(tools, strings.TrimSpace(ts))
			}
		}
	}

	color := normalizedOptionalString(func() *string {
		if v, ok := raw["color"].(string); ok {
			return &v
		}
		return nil
	}())
	workingDirectory := normalizedOptionalString(func() *string {
		if v, ok := raw["directory"].(string); ok {
			return &v
		}
		if v, ok := raw["working_directory"].(string); ok {
			return &v
		}
		return nil
	}())
	mentionAlias := normalizedOptionalString(func() *string {
		if v, ok := raw["mention_alias"].(string); ok {
			return &v
		}
		return nil
	}())

	preview := &agentspb.AgentDefinition{
		TenantId:           tenantID,
		Name:               strings.TrimSpace(name),
		Model:              strings.TrimSpace(model),
		SystemPrompt:       systemPrompt,
		Tools:              tools,
		Mode:               stringToProtoAgentMode(mode),
		MaxSteps:           maxSteps,
		TaskPermissionMode: stringToProtoTaskPermissionMode(taskPermission),
		Hidden:             hidden,
		Color:              color,
		WorkingDirectory:   workingDirectory,
		MentionAlias:       mentionAlias,
		ExecutionPolicy: &agentspb.AgentExecutionPolicy{
			TaskPermissionMode: stringToProtoTaskPermissionMode(taskPermission),
			MaxSteps:           maxSteps,
			WorkingDirectory:   workingDirectory,
		},
	}

	if req.Msg.GetDryRun() {
		return connect.NewResponse(&agentspb.ImportAgentFromOpencodeResponse{
			Valid:    true,
			Warnings: warnings,
			Agent:    preview,
		}), nil
	}

	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	cmd := agentscmd.NewCreateAgentCommand(
		tenantID,
		preview.GetName(),
		"",
		preview.GetModel(),
		preview.GetSystemPrompt(),
		preview.GetTools(),
		nil,
		25,
		10,
		mode,
		maxSteps,
		taskPermission,
		hidden,
		color,
		workingDirectory,
		mentionAlias,
		"",                  // lifecycleMode (ephemeral default for imports)
		nil,                 // icon
		"",                  // soulMD
		"",                  // identityMD
		"",                  // userMD
		"",                  // roleMD
		"",                  // sandboxImage
		"",                  // sandboxNetworkMode
		0.0,                 // sandboxCPULimit
		int64(0),            // sandboxMemoryMB
		int64(0),            // sandboxDiskMB
		int32(0),            // sandboxTimeoutSeconds
		[]string{},          // sandboxAllowedHosts
		map[string]string{}, // sandboxEnvVars
		false,               // sandboxSSHEnabled
		"",                  // sandboxGitRepoURL
		"",                  // sandboxGitBranch
		"",                  // dbSqlitePath
		"",                  // dbLanceDBPath
		"",                  // dbRedbPath
		int32(0),            // maxConcurrentWorkers
		nil,                 // workerPoolConfig
		false,               // autoProvision
		contextkeys.GetUserID(ctx),
		"", // traceID
	)
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	preview.Id = cmd.ID

	return connect.NewResponse(&agentspb.ImportAgentFromOpencodeResponse{
		Valid:    true,
		Warnings: warnings,
		Agent:    preview,
	}), nil
}

func (s *Server) ExportAgentToOpencode(ctx context.Context, req *connect.Request[agentspb.ExportAgentToOpencodeRequest]) (*connect.Response[agentspb.ExportAgentToOpencodeResponse], error) {

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

	q := agentsquery.NewGetAgentByIDQuery(req.Msg.GetId(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("agent not found"))
	}
	data := interface{}(res)
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}
	rm, ok := data.(*agentsquery.AgentDefinitionReadModel)
	if !ok || rm == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("agent not found"))
	}

	opencode := map[string]interface{}{
		"name":   rm.Name,
		"model":  rm.Model,
		"prompt": rm.SystemPrompt.String,
		"mode":   rm.Mode,
		"hidden": rm.Hidden,
		"color":  rm.Color.String,
		"permission": map[string]interface{}{
			"task": rm.TaskPermissionMode,
		},
	}
	if rm.MaxSteps.Valid {
		opencode["maxSteps"] = rm.MaxSteps.Int32
	}
	if rm.WorkingDirectory.Valid && strings.TrimSpace(rm.WorkingDirectory.String) != "" {
		opencode["directory"] = rm.WorkingDirectory.String
	}
	if rm.MentionAlias.Valid && strings.TrimSpace(rm.MentionAlias.String) != "" {
		opencode["mention_alias"] = rm.MentionAlias.String
	}
	if len(rm.Tools) > 0 {
		opencode["tools"] = rm.Tools
	}

	payload, err := json.MarshalIndent(opencode, "", "  ")
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.ExportAgentToOpencodeResponse{
		OpencodeAgentJson: string(payload),
	}), nil
}

// ============================================================================
// Session Management
// ============================================================================

func (s *Server) CreateSession(ctx context.Context, req *connect.Request[agentspb.CreateSessionRequest]) (*connect.Response[agentspb.CreateSessionResponse], error) {

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

	// Verify agent exists (retry briefly for CQRS eventual consistency)
	agentQ := agentsquery.NewGetAgentByIDQuery(req.Msg.GetAgentId(), tenantID)
	var agentData interface{}
	for attempt := 0; attempt < 5; attempt++ {
		agentRes, qErr := sys.QueryBus.Execute(ctx, agentQ)
		if qErr != nil {
			return nil, connect.NewError(connect.CodeInternal, qErr)
		}
		if resp, ok := agentRes.(*query.Response); ok {
			agentData = resp.Data
		}
		if agentData != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if agentData == nil {
		err := connect.NewError(connect.CodeNotFound, errors.New("agent not found"))
		return nil, err
	}
	agent, ok := agentData.(*agentsquery.AgentDefinitionReadModel)
	if !ok || agent == nil {
		err := connect.NewError(connect.CodeInternal, errors.New("unexpected agent data type"))
		return nil, err
	}
	if strings.EqualFold(strings.TrimSpace(agent.Mode), "subagent") {
		err := connect.NewError(connect.CodeFailedPrecondition, errors.New("subagent definitions cannot be started directly"))
		return nil, err
	}

	var metadata map[string]interface{}
	if req.Msg.GetMetadata() != nil {
		metadata = req.Msg.GetMetadata().AsMap()
	}

	cmd := agentscmd.NewCreateSessionCommand(tenantID, req.Msg.GetAgentId(), metadata, userID, "")
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.CreateSessionResponse{
		Session: &agentspb.AgentSession{
			Id:       cmd.ID,
			TenantId: tenantID,
			AgentId:  req.Msg.GetAgentId(),
			Status:   agentspb.SessionStatus_SESSION_STATUS_CREATED,
		},
	}), nil
}

func (s *Server) GetSession(ctx context.Context, req *connect.Request[agentspb.GetSessionRequest]) (*connect.Response[agentspb.GetSessionResponse], error) {

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

	q := agentsquery.NewGetSessionByIDQuery(req.Msg.GetId(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		err := connect.NewError(connect.CodeNotFound, errors.New("session not found"))
		return nil, err
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}
	if data == nil {
		err := connect.NewError(connect.CodeNotFound, errors.New("session not found"))
		return nil, err
	}

	swt, ok := data.(*agentsquery.SessionWithTurns)
	if !ok {
		err := connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
		return nil, err
	}

	session := sessionReadModelToProto(&swt.Session)
	for i := range swt.Turns {
		session.Turns = append(session.Turns, turnReadModelToProto(&swt.Turns[i]))
	}

	return connect.NewResponse(&agentspb.GetSessionResponse{Session: session}), nil
}

func (s *Server) ListSessions(ctx context.Context, req *connect.Request[agentspb.ListSessionsRequest]) (*connect.Response[agentspb.ListSessionsResponse], error) {

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

	var agentID *string
	if req.Msg.AgentId != nil {
		agentID = req.Msg.AgentId
	}

	var status *string
	if req.Msg.GetStatus() != agentspb.SessionStatus_SESSION_STATUS_UNSPECIFIED {
		s := sessionStatusToString(req.Msg.GetStatus())
		status = &s
	}

	q := agentsquery.NewListSessionsQuery(tenantID, agentID, status, int(req.Msg.GetLimit()), int(req.Msg.GetOffset()))
	if req.Msg.TrooperId != nil {
		q.TrooperID = req.Msg.TrooperId
	}
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var sessions []*agentspb.AgentSession
	if res != nil {
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}
		if list, ok := data.([]agentsquery.AgentSessionReadModel); ok {
			for i := range list {
				sessions = append(sessions, sessionReadModelToProto(&list[i]))
			}
		}
	}

	return connect.NewResponse(&agentspb.ListSessionsResponse{Sessions: sessions}), nil
}

// ============================================================================
// Runtime
// ============================================================================

func numericConfigValue(config map[string]interface{}, key string) (float64, bool) {
	if config == nil {
		return 0, false
	}
	v, ok := config[key]
	if !ok || v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	default:
		return 0, false
	}
}

func resolveAgentLoopLimits(config map[string]interface{}) (maxHistoryMessages int32, sessionTokenBudget int64) {
	maxHistoryMessages = 100
	if mhm, ok := numericConfigValue(config, "max_history_messages"); ok && mhm > 0 {
		maxHistoryMessages = int32(mhm)
	}
	if budget, ok := numericConfigValue(config, "token_budget"); ok && budget > 0 {
		sessionTokenBudget = int64(budget)
	}
	return maxHistoryMessages, sessionTokenBudget
}

func trimTurnsToHistoryBudget(
	turns []agentsquery.AgentSessionTurnReadModel,
	maxHistoryMessages int32,
	hasSystemPrompt bool,
	reservedNewMessages int,
) []agentsquery.AgentSessionTurnReadModel {
	if maxHistoryMessages <= 0 || len(turns) == 0 {
		return turns
	}

	available := int(maxHistoryMessages) - reservedNewMessages
	if hasSystemPrompt {
		available--
	}
	if available <= 0 {
		return nil
	}

	total := 0
	start := len(turns)
	for i := len(turns) - 1; i >= 0; i-- {
		msgCount := 0
		if turns[i].UserInput.Valid && turns[i].UserInput.String != "" {
			msgCount++
		}
		if turns[i].AssistantOutput.Valid && turns[i].AssistantOutput.String != "" {
			msgCount++
		}
		if msgCount == 0 {
			start = i
			continue
		}
		if total+msgCount > available {
			break
		}
		total += msgCount
		start = i
	}

	if start >= len(turns) {
		return nil
	}
	return turns[start:]
}

func (s *Server) RunTurn(ctx context.Context, req *connect.Request[agentspb.RunTurnRequest]) (*connect.Response[agentspb.RunTurnResponse], error) {
	spanCtx, span := telemetry.StartGatewaySpan(ctx, "agents.turn")
	ctx = spanCtx
	defer span.End()
	telemetry.AddSpanEvent(span, attrs.EventRequestReceived)
	span.SetAttributes(attribute.String(attrs.AgentSessionID, req.Msg.GetSessionId()))

	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	if s.engine == nil {
		err := connect.NewError(connect.CodeInternal, errors.New("agent runtime engine not initialized"))
		telemetry.RecordError(span, err)
		return nil, err
	}
	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, err
	}
	ctx = contextkeys.WithTenantID(ctx, tenantID)
	span.SetAttributes(attribute.String(attrs.TenantID, tenantID))

	if _, _, err := s.engine.ProvidersForContext(ctx); err != nil {
		logger.WithFields("error", err.Error()).Error("agents: failed to resolve providers for tenant")
		telemetry.RecordError(span, err)
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("provider resolution failed: %w", err))
	}

	// Look up session (retry briefly for CQRS eventual consistency)
	sessionQ := agentsquery.NewGetSessionByIDQuery(req.Msg.GetSessionId(), tenantID)
	var sessionData interface{}
	for attempt := 0; attempt < 5; attempt++ {
		sessionRes, qErr := sys.QueryBus.Execute(ctx, sessionQ)
		if qErr != nil {
			telemetry.RecordError(span, qErr)
			return nil, connect.NewError(connect.CodeInternal, qErr)
		}
		if resp, ok := sessionRes.(*query.Response); ok {
			sessionData = resp.Data
		}
		if sessionData != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if sessionData == nil {
		err := connect.NewError(connect.CodeNotFound, errors.New("session not found"))
		telemetry.RecordError(span, err)
		return nil, err
	}
	swt, ok := sessionData.(*agentsquery.SessionWithTurns)
	if !ok {
		err := connect.NewError(connect.CodeInternal, errors.New("unexpected session data type"))
		telemetry.RecordError(span, err)
		return nil, err
	}
	span.SetAttributes(
		attribute.String(attrs.AgentID, swt.Session.AgentID.String),
		attribute.Int("session.turn_count", len(swt.Turns)),
	)

	// Check session status — cancelled sessions are terminal.
	if swt.Session.Status == "cancelled" {
		err := connect.NewError(connect.CodeFailedPrecondition, errors.New("session is "+swt.Session.Status+" and cannot be restarted"))
		telemetry.RecordError(span, err)
		return nil, err
	}

	// Look up agent definition
	agentQ := agentsquery.NewGetAgentByIDQuery(swt.Session.AgentID.String, tenantID)
	var agentData interface{}
	for attempt := 0; attempt < 5; attempt++ {
		agentRes, qErr := sys.QueryBus.Execute(ctx, agentQ)
		if qErr != nil {
			telemetry.RecordError(span, qErr)
			return nil, connect.NewError(connect.CodeInternal, qErr)
		}
		if resp, ok := agentRes.(*query.Response); ok {
			agentData = resp.Data
		}
		if agentData != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if agentData == nil {
		err := connect.NewError(connect.CodeNotFound, errors.New("agent definition not found"))
		telemetry.RecordError(span, err)
		return nil, err
	}
	agent, ok := agentData.(*agentsquery.AgentDefinitionReadModel)
	if !ok {
		err := connect.NewError(connect.CodeInternal, errors.New("unexpected agent data type"))
		telemetry.RecordError(span, err)
		return nil, err
	}
	span.SetAttributes(
		attribute.String(attrs.AgentName, agent.Name),
		attribute.String(attrs.AgentModel, agent.Model),
		// Set standard trace attributes so agent traces show model/provider in the traces list
		attribute.String("model.requested", agent.Model),
		attribute.String("llm.request.model", agent.Model),
	)
	// Resolve provider name from the engine's router for trace display
	if s.engine != nil {
		if cp, _, resolveErr := s.engine.ResolveProvider(ctx, agent.Model); resolveErr == nil {
			if named, ok := cp.(interface{ Name() string }); ok {
				span.SetAttributes(attribute.String("provider", named.Name()))
			}
		}
	}

	// Check max turns (0 = unlimited)
	nextTurnNumber := int32(len(swt.Turns)) + 1
	if agent.MaxTurns > 0 && nextTurnNumber > agent.MaxTurns {
		// Auto-complete the session when max turns are reached
		completeCmd := agentscmd.NewCompleteSessionCommand(tenantID, swt.Session.ID, contextkeys.GetUserID(ctx), "")
		_ = sys.CommandBus.Dispatch(ctx, completeCmd)
		err := connect.NewError(connect.CodeResourceExhausted, errors.New("maximum turns exceeded"))
		telemetry.RecordError(span, err)
		return nil, err
	}

	// Parse agent config
	agentConfig := agenttools.AgentRuntimeConfig(agent)
	agentMode := strings.ToLower(strings.TrimSpace(agent.Mode))
	if agentMode == "" {
		agentMode = "primary"
	}
	taskPermissionMode := strings.ToLower(strings.TrimSpace(agent.TaskPermissionMode))
	if taskPermissionMode == "" {
		taskPermissionMode = "ask"
	}
	workingDirectory := ""
	if agent.WorkingDirectory.Valid {
		workingDirectory = strings.TrimSpace(agent.WorkingDirectory.String)
	}
	var maxSteps int32
	if agent.MaxSteps.Valid && agent.MaxSteps.Int32 > 0 {
		maxSteps = agent.MaxSteps.Int32
		agentConfig["max_steps"] = float64(maxSteps)
	}
	agentConfig["mode"] = agentMode
	agentConfig["task_permission_mode"] = taskPermissionMode
	if workingDirectory != "" {
		agentConfig["working_directory"] = workingDirectory
	}
	attrs.SetAgentPolicyContext(span, agentMode, taskPermissionMode, workingDirectory, maxSteps)
	maxHistoryMessages, _ := resolveAgentLoopLimits(agentConfig)
	hasSystemPrompt := agent.SystemPrompt.Valid && agent.SystemPrompt.String != ""

	// Build previous turns for conversation history.
	// Keep the newest turns that can still fit in the configured message budget
	// after accounting for system prompt and the current user message.
	turns := trimTurnsToHistoryBudget(swt.Turns, maxHistoryMessages, hasSystemPrompt, 1)
	var prevTurns []agentrt.TurnHistory
	for _, t := range turns {
		th := agentrt.TurnHistory{}
		if t.UserInput.Valid {
			th.UserInput = t.UserInput.String
		}
		if t.AssistantOutput.Valid {
			th.AssistantOutput = t.AssistantOutput.String
		}
		prevTurns = append(prevTurns, th)
	}

	// Execute turn via runtime engine with full tool loop support.
	// Uses ExecuteTurnWithToolLoop to ensure tool calls are actually executed
	// (not just returned as metadata), matching the streaming path's behavior.
	turnInput := &agentrt.TurnInput{
		AgentID:       agent.ID,
		SessionID:     swt.Session.ID,
		Model:         agent.Model,
		SystemPrompt:  agent.SystemPrompt.String,
		Tools:         agent.Tools,
		Config:        agentConfig,
		PreviousTurns: prevTurns,
		UserInput:     req.Msg.GetUserInput(),
	}

	result, err := s.engine.ExecuteTurnWithToolLoop(ctx, turnInput)
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	telemetry.AddSpanEvent(span, attrs.EventAgentTurnEnd,
		attribute.String(attrs.AgentFinishReason, result.FinishReason),
		attribute.Int(attrs.AgentTotalTokens, int(result.TotalTokens)),
	)

	// Determine session status after turn
	sessionStatus := "running"
	if result.Error != "" {
		sessionStatus = "failed"
	} else if result.FinishReason == "max_iterations" || result.FinishReason == "max_steps" || result.FinishReason == "max_tool_calls" ||
		result.FinishReason == "timeout" || result.FinishReason == "cancelled" ||
		result.FinishReason == "explicit_complete" || result.FinishReason == "token_budget_exhausted" {
		sessionStatus = "completed"
	} else if result.FinishReason == "stop" || result.FinishReason == "end_turn" || result.FinishReason == "interrupted" {
		// Normal turn completion — session stays open for multi-turn conversations
		sessionStatus = "waiting_for_input"
	}

	// Persist turn result via command
	toolCallsJSON := string(result.ToolCalls)
	if toolCallsJSON == "" {
		toolCallsJSON = "[]"
	}
	toolCallCount := 0
	var toolCallsArr []json.RawMessage
	if unmarshalErr := json.Unmarshal([]byte(toolCallsJSON), &toolCallsArr); unmarshalErr == nil {
		toolCallCount = len(toolCallsArr)
	}

	// ExpectedTurnCount is the current session turn_count before this turn.
	// Used for optimistic concurrency: the projection will only update metrics
	// if the session's turn_count still matches, preventing double-increments
	// from concurrent RunTurn calls.
	expectedTurnCount := swt.Session.TurnCount

	completeTurnCmd := agentscmd.NewCompleteTurnCommand(
		swt.Session.ID,
		nextTurnNumber,
		expectedTurnCount,
		req.Msg.GetUserInput(),
		result.AssistantOutput,
		toolCallsJSON,
		result.PromptTokens,
		result.CompletionTokens,
		result.TotalTokens,
		result.LatencyMs,
		result.Error,
		sessionStatus,
	)
	completeTurnCmd.CacheReadInputTokens = result.CacheReadTokens
	completeTurnCmd.CacheWriteInputTokens = result.CacheWriteTokens

	if err := sys.CommandBus.Dispatch(ctx, completeTurnCmd); err != nil {
		telemetry.RecordError(span, err)
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Record usage metrics to license monitor (best-effort, don't fail the turn)
	if result.TotalTokens > 0 {
		provider := s.getProviderForModel(ctx, agent.Model)
		costDetails := metrics.CalculateCost(provider, agent.Model, int(result.PromptTokens), int(result.CompletionTokens), 0)
		if err := s.recordUsageMetrics(int64(result.PromptTokens), int64(result.CompletionTokens), costDetails.EstimatedUSD, 0, false); err != nil {
			logger.Warnf("agents: RunTurn: spend limit exceeded after turn completion: %v", err)
		}
	}

	// Build turn proto
	turnStatus := agentspb.TurnStatus_TURN_STATUS_COMPLETED
	if result.Error != "" {
		turnStatus = agentspb.TurnStatus_TURN_STATUS_FAILED
	}
	attrs.SetAgentTokens(span, int64(result.PromptTokens), int64(result.CompletionTokens), int64(result.TotalTokens))
	span.SetAttributes(
		attribute.Int(attrs.AgentIteration, 1),
		attribute.Int(attrs.AgentToolsCount, toolCallCount),
		attribute.Int64(attrs.LatencyMs, result.LatencyMs),
		attribute.String(attrs.AgentFinishReason, result.FinishReason),
		attribute.Int(attrs.AgentTotalTokens, int(result.TotalTokens)),
		attribute.Int(attrs.AgentTurnNumber, int(nextTurnNumber)),
	)
	telemetry.AddSpanEvent(span, attrs.EventRequestComplete)

	return connect.NewResponse(&agentspb.RunTurnResponse{
		Turn: &agentspb.AgentSessionTurn{
			Id:               completeTurnCmd.ID,
			SessionId:        swt.Session.ID,
			TurnNumber:       nextTurnNumber,
			Status:           turnStatus,
			UserInput:        req.Msg.GetUserInput(),
			AssistantOutput:  result.AssistantOutput,
			ToolCalls:        toolCallsJSON,
			PromptTokens:     result.PromptTokens,
			CompletionTokens: result.CompletionTokens,
			TotalTokens:      result.TotalTokens,
			LatencyMs:        result.LatencyMs,
			Error:            result.Error,
		},
		SessionStatus: stringToSessionStatus(sessionStatus),
	}), nil
}

func (s *Server) CancelSession(ctx context.Context, req *connect.Request[agentspb.CancelSessionRequest]) (*connect.Response[agentspb.CancelSessionResponse], error) {

	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	tenantID, err := s.resolveTenantID(ctx, "")
	if err != nil {
		return nil, err
	}

	// Verify session exists and belongs to this tenant before cancelling
	sessionQ := agentsquery.NewGetSessionByIDQuery(req.Msg.GetSessionId(), tenantID)
	sessionRes, err := sys.QueryBus.Execute(ctx, sessionQ)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if sessionRes == nil {
		err := connect.NewError(connect.CodeNotFound, errors.New("session not found"))
		return nil, err
	}

	userID := contextkeys.GetUserID(ctx)

	cmd := agentscmd.NewCancelSessionCommand(tenantID, req.Msg.GetSessionId(), userID, "")
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Also cancel the in-memory runner if it's active
	if s.sessionMgr != nil {
		_ = s.sessionMgr.CancelSession(req.Msg.GetSessionId())
	}

	// Clean up sandbox if session was in waiting_for_input state (no active runner)
	if s.sandboxMgr != nil {
		_ = s.sandboxMgr.Destroy(ctx, req.Msg.GetSessionId())
	}

	return connect.NewResponse(&agentspb.CancelSessionResponse{
		Success: true,
		Message: "session cancellation dispatched",
	}), nil
}

func (s *Server) CompleteSession(ctx context.Context, req *connect.Request[agentspb.CompleteSessionRequest]) (*connect.Response[agentspb.CompleteSessionResponse], error) {

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

	// Verify session exists and belongs to this tenant
	sessionQ := agentsquery.NewGetSessionByIDQuery(req.Msg.GetSessionId(), tenantID)
	sessionRes, err := sys.QueryBus.Execute(ctx, sessionQ)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if sessionRes == nil {
		err := connect.NewError(connect.CodeNotFound, errors.New("session not found"))
		return nil, err
	}

	// Check the session isn't already in a terminal state
	var sessionData interface{} = sessionRes
	if resp, ok := sessionRes.(*query.Response); ok {
		sessionData = resp.Data
	}
	if swt, ok := sessionData.(*agentsquery.SessionWithTurns); ok {
		if swt.Session.Status == "completed" || swt.Session.Status == "cancelled" || swt.Session.Status == "failed" {
			err := connect.NewError(connect.CodeFailedPrecondition, errors.New("session is already "+swt.Session.Status))
			return nil, err
		}
	}

	userID := contextkeys.GetUserID(ctx)

	cmd := agentscmd.NewCompleteSessionCommand(tenantID, req.Msg.GetSessionId(), userID, "")
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Also cancel the in-memory runner if it's active
	if s.sessionMgr != nil {
		_ = s.sessionMgr.CancelSession(req.Msg.GetSessionId())
	}

	// Clean up sandbox if session was in waiting_for_input state (no active runner)
	if s.sandboxMgr != nil {
		_ = s.sandboxMgr.Destroy(ctx, req.Msg.GetSessionId())
	}

	return connect.NewResponse(&agentspb.CompleteSessionResponse{
		Success: true,
		Message: "session completed",
	}), nil
}

// ============================================================================
// Streaming Runtime
// ============================================================================

// RunTurnStream handles the Connect server-streaming RPC.
func (s *Server) RunTurnStream(ctx context.Context, req *connect.Request[agentspb.RunTurnStreamRequest], stream *connect.ServerStream[agentspb.AgentEvent]) error {
	return s.runTurnStreamInternal(ctx, req, &connectAgentEventStreamAdapter{stream: stream})
}

// runTurnStreamInternal is the shared implementation for both Connect and classic gRPC.
func (s *Server) runTurnStreamInternal(ctx context.Context, req *connect.Request[agentspb.RunTurnStreamRequest], stream agentEventSender) error {
	spanCtx, span := telemetry.StartGatewaySpan(ctx, "agents.turn.stream")
	ctx = spanCtx
	defer span.End()
	telemetry.AddSpanEvent(span, attrs.EventRequestReceived)
	span.SetAttributes(
		attribute.String(attrs.AgentSessionID, req.Msg.GetSessionId()),
		attribute.Int("agent.user_input_length", len(req.Msg.GetUserInput())),
	)

	logger.WithFields(
		"session_id", req.Msg.GetSessionId(),
		"user_input_len", len(req.Msg.GetUserInput()),
	).Info("agents: runTurnStreamInternal called")

	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		err := connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
		telemetry.RecordError(span, err)
		return err
	}

	if s.sessionMgr == nil {
		err := connect.NewError(connect.CodeInternal, errors.New("agent session manager not initialized"))
		telemetry.RecordError(span, err)
		return err
	}
	logger.WithFields("session_id", req.Msg.GetSessionId()).Info("agents: resolving tenant ID")
	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		logger.WithFields("session_id", req.Msg.GetSessionId(), "error", err.Error()).
			Error("agents: tenant ID resolution failed")
		telemetry.RecordError(span, err)
		return err
	}
	ctx = contextkeys.WithTenantID(ctx, tenantID)
	span.SetAttributes(attribute.String(attrs.TenantID, tenantID))
	logger.WithFields("session_id", req.Msg.GetSessionId(), "tenant_id", tenantID).
		Info("agents: tenant resolved, refreshing providers")

	// Resolve the provider bundle for this request. The gateway replaces
	// tenant bundles when provider configuration changes, so retaining the
	// startup registry/router would make newly configured models invisible to
	// agents (and could select the wrong tenant in shared mode).
	requestProviderRegistry, requestProviderRouter, err := s.engine.ProvidersForContext(ctx)
	if err != nil {
		logger.WithFields("session_id", req.Msg.GetSessionId(), "error", err.Error()).
			Error("agents: failed to resolve providers for tenant")
		telemetry.RecordError(span, err)
		return connect.NewError(connect.CodeInternal, fmt.Errorf("provider resolution failed: %w", err))
	}
	logger.WithFields("session_id", req.Msg.GetSessionId()).Info("agents: providers refreshed, loading session")

	// Look up session (retry briefly for CQRS eventual consistency)
	sessionQ := agentsquery.NewGetSessionByIDQuery(req.Msg.GetSessionId(), tenantID)
	var sessionData interface{}
	for attempt := 0; attempt < 5; attempt++ {
		// Use a per-attempt timeout to prevent blocking on slow DB
		queryCtx, queryCancel := context.WithTimeout(ctx, 3*time.Second)
		sessionRes, qErr := sys.QueryBus.Execute(queryCtx, sessionQ)
		queryCancel()
		if qErr != nil {
			logger.WithFields("session_id", req.Msg.GetSessionId(), "attempt", attempt, "error", qErr.Error()).
				Warn("agents: session query failed, retrying")
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if resp, ok := sessionRes.(*query.Response); ok {
			sessionData = resp.Data
		}
		if sessionData != nil {
			break
		}
		logger.WithFields("session_id", req.Msg.GetSessionId(), "attempt", attempt).
			Info("agents: session not found yet, retrying")
		time.Sleep(100 * time.Millisecond)
	}
	if sessionData == nil {
		logger.WithFields("session_id", req.Msg.GetSessionId()).Error("agents: session not found after retries")
		err := connect.NewError(connect.CodeNotFound, errors.New("session not found"))
		telemetry.RecordError(span, err)
		return err
	}
	swt, ok := sessionData.(*agentsquery.SessionWithTurns)
	if !ok {
		logger.Error("agents: unexpected session data type")
		err := connect.NewError(connect.CodeInternal, errors.New("unexpected session data type"))
		telemetry.RecordError(span, err)
		return err
	}
	span.SetAttributes(
		attribute.String(attrs.AgentID, swt.Session.AgentID.String),
		attribute.Int("session.turn_count", len(swt.Turns)),
	)

	logger.WithFields(
		"session_id", swt.Session.ID,
		"status", swt.Session.Status,
		"agent_id", swt.Session.AgentID.String,
		"trooper_id", swt.Session.TrooperID.String,
		"turns", len(swt.Turns),
	).Info("agents: session loaded")

	// Check session status — cancelled sessions are terminal.
	if swt.Session.Status == "cancelled" {
		logger.WithFields("session_id", swt.Session.ID, "status", swt.Session.Status).Error("agents: session in terminal state")
		err := connect.NewError(connect.CodeFailedPrecondition, errors.New("session is "+swt.Session.Status+" and cannot be restarted"))
		telemetry.RecordError(span, err)
		return err
	}

	// If session was completed/failed, waiting, or hibernated, update status to running to indicate a new turn is starting.
	if swt.Session.Status == "completed" || swt.Session.Status == "failed" || swt.Session.Status == "waiting_for_input" || swt.Session.Status == "hibernated" {
		if s.db != nil {
			if _, err := s.db.ExecContext(ctx,
				`UPDATE everstack.agent_sessions SET status = 'running', hibernated_at = NULL, updated_at = NOW() WHERE id = $1`,
				swt.Session.ID); err != nil {
				logger.WithFields("session_id", swt.Session.ID, "error", err.Error()).
					Error("agents: failed to update session status to running")
			}
		}
	}

	// If this is a trooper session (no agent_id, has trooper_id), delegate to trooper steer flow.
	// This allows the unified agent steer endpoint to handle both agent and trooper sessions,
	// so SessionTimeline's startTurn works transparently for trooper sessions.
	if !swt.Session.AgentID.Valid || swt.Session.AgentID.String == "" {
		if swt.Session.TrooperID.Valid && swt.Session.TrooperID.String != "" {
			logger.WithFields(
				"session_id", swt.Session.ID,
				"trooper_id", swt.Session.TrooperID.String,
				"agent_id_valid", swt.Session.AgentID.Valid,
				"agent_id", swt.Session.AgentID.String,
			).Info("agents: detected trooper session, delegating to runTrooperTurnInternal")
			return s.runTrooperTurnInternal(ctx, sys, swt, tenantID, req.Msg.GetUserInput(), stream, span)
		}
		err := connect.NewError(connect.CodeFailedPrecondition, errors.New("session has no agent_id or trooper_id"))
		telemetry.RecordError(span, err)
		return err
	}

	// Look up agent definition
	agentQ := agentsquery.NewGetAgentByIDQuery(swt.Session.AgentID.String, tenantID)
	var agentData interface{}
	for attempt := 0; attempt < 5; attempt++ {
		// Use a per-attempt timeout to prevent blocking on slow DB
		queryCtx, queryCancel := context.WithTimeout(ctx, 3*time.Second)
		agentRes, qErr := sys.QueryBus.Execute(queryCtx, agentQ)
		queryCancel()
		if qErr != nil {
			logger.WithFields("attempt", attempt, "error", qErr.Error()).
				Warn("agents: agent query failed, retrying")
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if resp, ok := agentRes.(*query.Response); ok {
			agentData = resp.Data
		}
		if agentData != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if agentData == nil {
		logger.WithFields("agent_id", swt.Session.AgentID.String).Error("agents: agent definition not found after retries")
		err := connect.NewError(connect.CodeNotFound, errors.New("agent definition not found"))
		telemetry.RecordError(span, err)
		return err
	}
	agent, ok := agentData.(*agentsquery.AgentDefinitionReadModel)
	if !ok {
		logger.Error("agents: unexpected agent data type")
		err := connect.NewError(connect.CodeInternal, errors.New("unexpected agent data type"))
		telemetry.RecordError(span, err)
		return err
	}
	span.SetAttributes(
		attribute.String(attrs.AgentName, agent.Name),
		attribute.String(attrs.AgentModel, agent.Model),
		// Set standard trace attributes so agent traces show model/provider in the traces list
		attribute.String("model.requested", agent.Model),
		attribute.String("llm.request.model", agent.Model),
	)
	// Resolve provider name from the engine's router for trace display
	if s.engine != nil {
		if cp, _, resolveErr := s.engine.ResolveProvider(ctx, agent.Model); resolveErr == nil {
			if named, ok := cp.(interface{ Name() string }); ok {
				span.SetAttributes(attribute.String("provider", named.Name()))
			}
		}
	}

	logger.WithFields("agent_id", agent.ID, "agent_name", agent.Name).Debug("agents: agent loaded")

	// Check session-level max turns BEFORE auto-wake to avoid waking a sandbox
	// only to immediately reject the turn.
	nextStreamTurnNumber := int32(len(swt.Turns)) + 1
	if agent.MaxTurns > 0 && nextStreamTurnNumber > agent.MaxTurns {
		completeCmd := agentscmd.NewCompleteSessionCommand(tenantID, swt.Session.ID, "", "")
		_ = sys.CommandBus.Dispatch(ctx, completeCmd)
		err := connect.NewError(connect.CodeResourceExhausted, errors.New("maximum turns exceeded"))
		telemetry.RecordError(span, err)
		return err
	}

	// ── Persistent agent handling ──────────────────────────────────
	// If this agent is persistent (lifecycle_mode == 'persistent'), apply
	// identity injection, auto-wake, and persistent sandbox — same behavior
	// previously handled by the separate trooper session flow.
	isPersistent := agent.LifecycleMode == "persistent"
	if isPersistent {
		// Auto-wake sleeping agents by reviving their sandbox directly.
		// Persistent agents store sandbox_id on agent_definitions, not in the troopers table,
		// so we use the sandbox manager directly instead of trooperMgr.Wake.
		if agent.LifecycleStatus == "sleeping" && s.sandboxMgr != nil {
			sandboxID := agent.SandboxID.String
			if sandboxID == "" {
				logger.WithFields("agent_id", agent.ID).Error("agents: sleeping agent has no sandbox_id")
				return connect.NewError(connect.CodeInternal, fmt.Errorf("sleeping agent %s has no sandbox_id", agent.ID))
			}

			// Check if the sandbox is already running in the manager (e.g. server restarted
			// but the pod stayed up — DB says 'sleeping' but the sandbox is actually alive).
			alreadyRunning := false
			if inst, ok := s.sandboxMgr.GetBySandboxID(sandboxID); ok && inst != nil && inst.Status == "running" {
				logger.WithFields("agent_id", agent.ID, "sandbox_id", sandboxID).
					Info("agents: sandbox already running, skipping revive — fixing stale lifecycle_status")
				alreadyRunning = true
			}

			if !alreadyRunning {
				logger.WithFields("agent_id", agent.ID, "sandbox_id", sandboxID).Info("agents: auto-waking sleeping persistent agent")
				if _, err := s.sandboxMgr.ReviveSandbox(ctx, sandboxID); err != nil {
					logger.WithFields("agent_id", agent.ID, "sandbox_id", sandboxID, "error", err.Error()).
						Error("agents: failed to auto-wake persistent agent")
					telemetry.RecordError(span, err)
					return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to wake agent: %w", err))
				}
				// Poll for readiness (sandbox needs time to revive)
				for i := 0; i < 10; i++ {
					time.Sleep(200 * time.Millisecond)
					if inst, ok := s.sandboxMgr.GetBySandboxID(sandboxID); ok && inst != nil && inst.Status == "running" {
						break
					}
				}
			}

			// Update lifecycle_status in DB (idle, not running — turn hasn't started yet)
			if s.db != nil {
				s.db.ExecContext(context.Background(), `UPDATE agent_definitions SET lifecycle_status = 'idle', updated_at = NOW() WHERE id = $1 AND tenant_id = $2`, agent.ID, tenantID)
			}
		}
		if s.sandboxMgr != nil {
			s.sandboxMgr.TouchActivity("trp-"+agent.ID, "agent_message")
		}

		// Sync identity files to sandbox — only if the sandbox pod is currently running.
		// Stopped/sleeping sandboxes will get identity files synced when they are revived
		// or reprovisioned. Attempting to write to a stopped pod generates noisy errors.
		if s.trooperMgr != nil && agent.SandboxID.Valid && agent.SandboxID.String != "" {
			if s.sandboxMgr != nil {
				if inst, ok := s.sandboxMgr.GetBySandboxID(agent.SandboxID.String); ok && inst != nil && inst.Status == "running" {
					identityFiles := trooper.IdentityFiles{
						SoulMD:     agent.SoulMD,
						IdentityMD: agent.IdentityMD,
						UserMD:     agent.UserMD,
						RoleMD:     agent.RoleMD,
					}
					if err := s.trooperMgr.SyncIdentityFiles(ctx, "trp-"+agent.ID, identityFiles); err != nil {
						logger.WithFields("agent_id", agent.ID, "error", err.Error()).
							Warn("agents: failed to sync identity files for persistent agent")
					}
				}
			}
		}
	}

	turnStartAt := time.Now()

	// Build sampling params from agent config
	var agentConfig map[string]interface{}
	if len(agent.Config) > 0 {
		_ = json.Unmarshal(agent.Config, &agentConfig)
	}
	if agentConfig == nil {
		agentConfig = make(map[string]interface{})
	}
	agentMode := strings.ToLower(strings.TrimSpace(agent.Mode))
	if agentMode == "" {
		agentMode = "primary"
	}
	taskPermissionMode := strings.ToLower(strings.TrimSpace(agent.TaskPermissionMode))
	if taskPermissionMode == "" {
		taskPermissionMode = "ask"
	}
	workingDirectory := ""
	if agent.WorkingDirectory.Valid {
		workingDirectory = strings.TrimSpace(agent.WorkingDirectory.String)
	}
	var maxSteps int32
	if agent.MaxSteps.Valid && agent.MaxSteps.Int32 > 0 {
		maxSteps = agent.MaxSteps.Int32
		agentConfig["max_steps"] = float64(maxSteps)
	}
	agentConfig["mode"] = agentMode
	agentConfig["task_permission_mode"] = taskPermissionMode
	if workingDirectory != "" {
		agentConfig["working_directory"] = workingDirectory
	}
	attrs.SetAgentPolicyContext(span, agentMode, taskPermissionMode, workingDirectory, maxSteps)
	sampling := gw.SamplingParams{}
	if temp, ok := agentConfig["temperature"].(float64); ok {
		sampling.Temperature = temp
	}
	if maxTokens, ok := agentConfig["max_tokens"].(float64); ok {
		sampling.MaxTokens = int(maxTokens)
	}
	if topP, ok := agentConfig["top_p"].(float64); ok {
		sampling.TopP = topP
	}

	// Build loop input — allow per-turn model override from the client
	turnModel := agent.Model
	if override := req.Header().Get("X-Model-Override"); override != "" {
		logger.WithFields("agent_model", agent.Model, "override_model", override).
			Info("agents: applying model override for turn")
		turnModel = override
	}
	loopInput := &agentrt.LoopInput{
		TenantID:           tenantID,
		AgentID:            agent.ID,
		SessionID:          swt.Session.ID,
		Model:              turnModel,
		SystemPrompt:       agent.SystemPrompt.String,
		Tools:              agent.Tools,
		Sampling:           sampling,
		UserInput:          req.Msg.GetUserInput(),
		AgentMode:          agentMode,
		TaskPermissionMode: taskPermissionMode,
		WorkingDirectory:   workingDirectory,
		MaxSteps:           maxSteps,
	}

	// Parse HITL config from agent definition
	hitlConfig := agentrt.ParseHITLConfig(agentConfig)
	if hitlConfig != nil {
		loopInput.HITLConfig = hitlConfig
	}

	// Parse fallback config and API type from agent definition.
	// If agent has no per-agent fallback, inherit from gateway config.
	// Try request ctx first, then server ctx (GatewayConfig is on server startup ctx).
	fallbackConfig := agentrt.ParseFallbackConfig(agentConfig)
	if fallbackConfig == nil {
		fallbackConfig = agentrt.FallbackConfigFromGateway(ctx)
		if fallbackConfig == nil && s.ctx != nil {
			fallbackConfig = agentrt.FallbackConfigFromGateway(s.ctx)
		}
	}
	if fallbackConfig != nil {
		loopInput.FallbackConfig = fallbackConfig
		logger.WithFields(
			"session_id", swt.Session.ID,
			"fallback_models", fallbackConfig.Models,
			"fallback_max_attempts", fallbackConfig.MaxAttempts,
			"fallback_source", func() string {
				if agentrt.ParseFallbackConfig(agentConfig) != nil {
					return "agent"
				}
				return "gateway"
			}(),
		).Info("agents: fallback config resolved")
	} else {
		logger.WithFields("session_id", swt.Session.ID).Warn("agents: no fallback config found (agent or gateway)")
	}
	loopInput.APIType = agentrt.ParseAPIType(agentConfig)

	// Parse spawn config and sandbox config, wire tool interceptor if any synthetic tools are enabled
	spawnConfig := agenttools.ParseSpawnConfig(agentConfig)
	sandboxConfig := sandbox.ParseSandboxConfig(agentConfig)
	forkConfig := agentrt.ParseForkConfig(agentConfig)
	monitorConfig := agentrt.ParseMonitorConfig(agentConfig)
	digestConfig := agentrt.ParseDigestConfig(agentConfig)
	peerConfig := sandbox.ParsePeerConfig(agentConfig)
	disabledRuntimeTools := make(map[string]struct{})
	if raw, ok := agentConfig["disabled_runtime_tools"].([]interface{}); ok {
		for _, item := range raw {
			if name, ok := item.(string); ok && strings.TrimSpace(name) != "" {
				disabledRuntimeTools[strings.TrimSpace(name)] = struct{}{}
			}
		}
	}
	isRuntimeToolEnabled := func(name string) bool {
		_, disabled := disabledRuntimeTools[name]
		return !disabled
	}

	// Phase holders — created early, emitters wired post-PrepareSession
	var jobQueue *agentrt.LocalJobQueue
	var forkManager *agentrt.ForkManager
	var monitor *agentrt.Monitor
	executionMode, persistenceMode, templateConfigured := classifySessionModes(agentConfig, sandboxConfig)
	gitRepoConfigured := strings.TrimSpace(sandboxConfig.GitRepoURL) != ""
	span.SetAttributes(
		attribute.String(attrs.AgentExecutionMode, executionMode),
		attribute.String(attrs.AgentPersistenceMode, persistenceMode),
		attribute.Bool(attrs.AgentSandboxEnabled, sandboxConfig.Enabled),
		attribute.Bool(attrs.AgentGitRepoConfigured, gitRepoConfigured),
		attribute.Bool(attrs.AgentTemplateConfigured, templateConfigured),
	)
	loopInput.ExecutionMode = executionMode
	loopInput.PersistenceMode = persistenceMode
	loopInput.SandboxEnabled = sandboxConfig.Enabled
	loopInput.GitRepoConfigured = gitRepoConfigured
	loopInput.TemplateConfigured = templateConfigured

	// ── Persistent agent overrides ─────────────────────────────────
	// Override persistence mode, sandbox flag, and prepend identity to system
	// prompt so that persistent agents get the same treatment as the old
	// trooper session flow — but through the unified steer path.
	if isPersistent {
		// Build identity prompt block
		var identityParts []string
		if agent.SoulMD != "" {
			identityParts = append(identityParts, "## SOUL\n"+agent.SoulMD)
		}
		if agent.IdentityMD != "" {
			identityParts = append(identityParts, "## IDENTITY\n"+agent.IdentityMD)
		}
		if agent.UserMD != "" {
			identityParts = append(identityParts, "## USER\n"+agent.UserMD)
		}
		if agent.RoleMD != "" {
			identityParts = append(identityParts, "## ROLE\n"+agent.RoleMD)
		}
		if len(identityParts) > 0 {
			identityBlock := "<identity>\n" + strings.Join(identityParts, "\n\n") + "\n</identity>"
			loopInput.SystemPrompt = identityBlock + "\n\n" + loopInput.SystemPrompt
		}
		loopInput.PersistenceMode = "persistent"
		loopInput.SandboxEnabled = true
		persistenceMode = "persistent"
		sandboxConfig.Enabled = true
	}

	hasMemory := s.memoryStore != nil && s.memoryEmbedder != nil
	searxngURL := os.Getenv("EVS_SEARXNG_URL")
	jinaAPIKey := os.Getenv("EVS_JINA_API_KEY")
	// Web tools are on by default for every agent (no per-request toggle):
	// web_search + web_fetch are enabled whenever a self-hosted SearXNG instance
	// is configured via EVS_SEARXNG_URL.
	enableWebSearch := searxngURL != ""
	if sandboxConfig.Enabled {
		loopInput.SystemPrompt = augmentSandboxSystemPrompt(loopInput.SystemPrompt, sandboxConfig)
	}

	// Inject web tool names into the allowlist so they pass the interceptor
	// filter. web_fetch is default-on for every agent (needs no SearXNG);
	// web_search is added only when a SearXNG instance is configured.
	webTools := []string{"web_fetch"}
	if enableWebSearch {
		webTools = append(webTools, "web_search")
	}
	for _, toolName := range webTools {
		if isRuntimeToolEnabled(toolName) {
			loopInput.Tools = appendUnique(loopInput.Tools, toolName)
		}
	}

	logger.WithFields(
		"session_id", swt.Session.ID,
		"sandbox_enabled", sandboxConfig.Enabled,
		"execution_mode", executionMode,
		"persistence_mode", persistenceMode,
		"template_configured", templateConfigured,
		"git_repo_configured", gitRepoConfigured,
		"sandbox_mgr_available", s.sandboxMgr != nil,
		"spawn_enabled", spawnConfig.Enabled,
		"spawn_async", spawnConfig.Async,
		"fork_enabled", forkConfig.Enabled,
		"monitor_enabled", monitorConfig.Enabled,
		"digest_enabled", digestConfig.Enabled,
		"has_memory", hasMemory,
		"has_web_search", searxngURL != "",
		"enable_web_search_toggle", enableWebSearch,
	).Debug("agents: synthetic tool config")

	// Always create interceptor — at minimum, create_workflow is always registered.
	var interceptor *agenttools.ToolInterceptor
	{
		interceptor = agenttools.NewToolInterceptor(s.sessionMgr.GetToolLoop())

		// Register spawn handler
		if spawnConfig.Enabled {
			spawnTracker := agenttools.NewSpawnTracker(swt.Session.ID, spawnConfig)
			spawnHandler := &agenttools.SpawnAgentHandler{
				ServerCtx:          s.ctx,
				Registry:           requestProviderRegistry,
				Router:             requestProviderRouter,
				ToolLoop:           s.sessionMgr.GetToolLoop(),
				Tracker:            spawnTracker,
				ParentInput:        loopInput,
				DB:                 s.db,
				TaskPermissionMode: taskPermissionMode,
				ParentMode:         agentMode,
				BranchStore:        s.branchStore,
				RevisionStore:      s.revisionStore,
				ProjectRuntime:     s.projectRuntime,
				SandboxManager:     s.sandboxMgr,
				BrowserPool:        s.browserPool,
			}
			if isRuntimeToolEnabled("spawn_agent") {
				interceptor.RegisterHandler(spawnHandler)
				loopInput.Tools = appendUnique(loopInput.Tools, "spawn_agent")
			}

			// Register parallel_tasks handler (opt-in via agent's tools allowlist)
			if isRuntimeToolEnabled("parallel_tasks") {
				interceptor.RegisterHandler(&agenttools.ParallelTasksHandler{
					ServerCtx:   s.ctx,
					Registry:    requestProviderRegistry,
					Router:      requestProviderRouter,
					ToolLoop:    s.sessionMgr.GetToolLoop(),
					Tracker:     spawnTracker,
					Interceptor: interceptor,
					ParentInput: loopInput,
					BranchStore: s.branchStore,
				})
				loopInput.Tools = appendUnique(loopInput.Tools, "parallel_tasks")
			}

			// Phase 1 (Job): wire async job queue if spawn.async is enabled
			if spawnConfig.Async {
				jobQueue = s.sessionMgr.GetOrCreateJobQueue(swt.Session.ID, spawnConfig.MaxConcurrentJobs)
				spawnHandler.JobQueue = jobQueue
				loopInput.JobResultCh = jobQueue.ResultCh()
				if isRuntimeToolEnabled("check_job") {
					interceptor.RegisterHandler(&agenttools.CheckJobHandler{JobQueue: jobQueue})
					loopInput.Tools = appendUnique(loopInput.Tools, "check_job")
				}
			}
		}

		// Phase 2 (Fork): register fork handler
		if forkConfig.Enabled {
			forkManager = agentrt.NewForkManager(forkConfig, s.engine, nil)
			forkManager.BranchStore = s.branchStore
			loopInput.ForkResultCh = forkManager.ResultCh()
			forkHandler := &agenttools.ForkHandler{
				ForkManager: forkManager,
				ParentInput: loopInput,
			}
			if isRuntimeToolEnabled("fork") {
				interceptor.RegisterHandler(forkHandler)
				loopInput.Tools = appendUnique(loopInput.Tools, "fork")
			}
		}

		// Register sandbox handlers
		var sandboxCtx *agenttools.SandboxSessionContext
		if sandboxConfig.Enabled && s.sandboxMgr != nil {
			clampedConfig := s.sandboxMgr.ClampToGlobalLimitsForTenant(sandboxConfig, tenantID)
			s.injectPortExposureDomain(&clampedConfig)
			// For persistent agents, reuse the agent's provisioned sandbox
			sandboxSessionID := swt.Session.ID
			if isPersistent {
				sandboxSessionID = "trp-" + agent.ID
				// Ensure sandbox config has persistent/agent fields set so
				// ensureSandbox takes the trooper path (GetOrCreateTrooper),
				// even if the agent's JSON config didn't include these fields.
				clampedConfig.Persistent = true
				clampedConfig.AgentID = agent.ID
			}
			// If the agent config specifies a linked sandbox, propagate to context
			linkedSessionID := clampedConfig.LinkedSessionID

			// Attach browser sidecar config if browser automation is enabled.
			// This must happen before sandboxCtx is built so the manager
			// provisions the sidecar when the sandbox pod is created.
			browserCfg := sandbox.ParseBrowserConfig(agentConfig)
			// Gate headed mode behind browser_headed feature flag
			if !browserCfg.Headless && !s.sandboxMgr.IsBrowserHeadedEnabled(tenantID) {
				browserCfg.Headless = true
			}
			clampedConfig.BrowserSidecar = browserCfg.ToSidecarConfig()

			sandboxCtx = &agenttools.SandboxSessionContext{
				Manager:                 s.sandboxMgr,
				SessionID:               sandboxSessionID,
				TenantID:                tenantID,
				Config:                  clampedConfig,
				SessionStartedAt:        turnStartAt,
				ExecutionMode:           executionMode,
				PersistenceMode:         persistenceMode,
				AllowedWorkingDirectory: workingDirectory,
				PortExposureBaseDomain:  s.portExposureBaseDomain,
				PortExposureTLSEnabled:  s.portExposureTLSEnabled,
				PortExposureListenPort:  s.portExposureListenPort,
				AgentID:                 agent.ID,
				LinkedSessionID:         linkedSessionID,
			}
			handlers := agenttools.NewSandboxHandlers(sandboxCtx)
			logger.WithFields(
				"session_id", swt.Session.ID,
				"handler_count", len(handlers),
			).Info("agents: registering sandbox handlers")
			for _, h := range handlers {
				if !isRuntimeToolEnabled(h.Name()) {
					continue
				}
				interceptor.RegisterHandler(h)
				loopInput.Tools = appendUnique(loopInput.Tools, h.Name())
			}
			// Plumb the parent's sandbox context into spawn_agent so a
			// spawned PERSISTENT child agent gets its OWN trooper
			// sandbox at "trp-<childAgentID>" instead of inheriting
			// the parent's. Non-persistent children continue sharing.
			if existing, ok := interceptor.Handlers["spawn_agent"].(*agenttools.SpawnAgentHandler); ok {
				existing.ParentSandboxCtx = sandboxCtx
			}

			// Register browser automation handlers (requires sandbox).
			// Re-use browserCfg parsed above (already has headed mode gated).
			if browserCfg.Enabled {
				browserCtx := &agenttools.BrowserSessionContext{
					SandboxCtx: sandboxCtx,
					Config:     browserCfg,
					Pool:       s.browserPool,
				}
				for _, h := range agenttools.NewBrowserHandlers(browserCtx) {
					if !isRuntimeToolEnabled(h.Name()) {
						continue
					}
					interceptor.RegisterHandler(h)
					loopInput.Tools = appendUnique(loopInput.Tools, h.Name())
				}
			}
		}

		// Register web search/fetch handlers (standalone — not sandbox-gated).
		// web_fetch is default-on for every agent; its nil HTTPClient makes it
		// use the SSRF-guarded client. web_search needs a configured SearXNG.
		interceptor.RegisterHandler(&agenttools.WebFetchHandler{
			JinaAPIKey: jinaAPIKey,
		})
		if enableWebSearch {
			interceptor.RegisterHandler(&agenttools.WebSearchHandler{
				SearXNGURL: searxngURL,
				HTTPClient: http.DefaultClient,
			})
		}

		// Register the A2A client tool. Opt-in via the agent's configured tools
		// list (the interceptor only surfaces synthetic tools the agent lists),
		// so registering it unconditionally is safe. Lets an agent call remote
		// A2A agents — Google ADK agents and any A2A-compliant server. When the
		// interop store is wired, the agent can reference saved remotes by name.
		var a2aRemotes agenttools.RemoteResolver
		if s.interopStore != nil {
			a2aRemotes = interopRemoteResolver{store: s.interopStore, tenant: tenantID}
		}
		interceptor.RegisterHandler(&agenttools.A2ACallHandler{
			HTTPClient: http.DefaultClient,
			Remotes:    a2aRemotes,
		})

		// Register memory handlers (always-on when backend is configured)
		if hasMemory {
			if isRuntimeToolEnabled("memory_store") {
				interceptor.RegisterHandler(&agenttools.MemoryStoreHandler{
					Store:                     s.memoryStore,
					Embedder:                  s.memoryEmbedder,
					TenantID:                  tenantID,
					DefaultEmbeddingModel:     s.memoryEmbeddingModel,
					DefaultEmbeddingDimension: s.memoryEmbeddingDimension,
				})
				loopInput.Tools = appendUnique(loopInput.Tools, "memory_store")
			}
			if isRuntimeToolEnabled("memory_query") {
				interceptor.RegisterHandler(&agenttools.MemoryQueryHandler{
					Store:    s.memoryStore,
					Embedder: s.memoryEmbedder,
					TenantID: tenantID,
				})
				loopInput.Tools = appendUnique(loopInput.Tools, "memory_query")
			}
		}

		// Register trigger self-management tools (opt-in via agent tools allowlist)
		if s.triggerStore != nil {
			interceptor.RegisterHandler(&agenttools.TriggerCreateHandler{
				Store: s.triggerStore, TenantID: tenantID, AgentID: agent.ID,
			})
			interceptor.RegisterHandler(&agenttools.TriggerListHandler{
				Store: s.triggerStore, TenantID: tenantID, AgentID: agent.ID,
			})
			interceptor.RegisterHandler(&agenttools.TriggerDeleteHandler{
				Store: s.triggerStore, TenantID: tenantID, AgentID: agent.ID,
			})
		}

		// Register workflow creation tool (always available — every agent can create workflows)
		if isRuntimeToolEnabled("create_workflow") {
			interceptor.RegisterAlwaysInclude(&agenttools.CreateWorkflowHandler{
				TenantID:  tenantID,
				AgentID:   agent.ID,
				ServerCtx: s.ctx,
			})
			loopInput.Tools = appendUnique(loopInput.Tools, "create_workflow")
		}

		// Register storage artifact tools (available when tenant has storage configured)
		storageCtx, storageErr := s.resolveStorageContext(ctx, tenantID, swt.Session.ID)
		if storageErr != nil {
			code := connect.CodePermissionDenied
			if errors.Is(storageErr, storageauth.ErrUnauthenticated) {
				code = connect.CodeUnauthenticated
			}
			return connect.NewError(code, storageErr)
		}
		if storageCtx != nil {
			for _, h := range agenttools.NewStorageHandlers(storageCtx) {
				interceptor.RegisterHandler(h)
			}
		}

		// Register repo browsing tools (available when agent has a GitHub repo attached)
		if repoCtx := s.resolveRepoContext(sandboxConfig); repoCtx != nil {
			for _, h := range agenttools.NewRepoHandlers(repoCtx) {
				if !isRuntimeToolEnabled(h.Name()) {
					continue
				}
				interceptor.RegisterHandler(h)
				loopInput.Tools = appendUnique(loopInput.Tools, h.Name())
			}
		}

		// Register federated MCP tools from the MCP gateway registry. Each
		// remote MCP tool is exposed as a synthetic tool with a namespaced
		// name ("mcp__<server>__<tool>"). MCP tools are explicit opt-in:
		// only registered when the user added the tool name to agent.Tools.
		if s.mcpRegistry != nil {
			// Hydrate the in-memory registry from DB so this tenant's
			// enabled MCP servers are present even if the gateway has
			// restarted since the user last visited the MCP page.
			// Without this, FederatedToolsForTenant returns an empty
			// list and the agent reports "I don't have access to MCP
			// servers" despite the user having configured plenty.
			// Idempotent: already-registered servers are skipped.
			if s.mcpHydrator != nil {
				if err := s.mcpHydrator.HydrateRegistryForTenant(ctx, tenantID); err != nil {
					logger.WithFields(
						"session_id", swt.Session.ID,
						"tenant_id", tenantID,
						"error", err.Error(),
					).Warn("agents: mcp registry hydration failed; continuing with whatever is already registered")
				}
			}

			explicitTools := make(map[string]struct{}, len(agent.Tools))
			for _, t := range agent.Tools {
				explicitTools[t] = struct{}{}
			}
			mcpHandlers := agenttools.NewMcpToolHandlers(s.mcpRegistry, tenantID)
			registered := 0
			for _, h := range mcpHandlers {
				if _, ok := explicitTools[h.Name()]; !ok {
					continue
				}
				if !isRuntimeToolEnabled(h.Name()) {
					continue
				}
				interceptor.RegisterHandler(h)
				registered++
			}
			logger.WithFields(
				"session_id", swt.Session.ID,
				"available", len(mcpHandlers),
				"registered", registered,
			).Debug("agents: registered MCP tool handlers")
		}

		// Phase 5: Register cross-agent messaging tools when peer config is enabled
		if peerConfig.Enabled && s.messageBus != nil {
			interceptor.RegisterHandler(&agenttools.SendMessageHandler{
				MessageBus:    s.messageBus,
				SenderAgentID: agent.ID,
				SenderName:    agent.Name,
				TenantID:      tenantID,
				DB:            s.db,
				ServerCtx:     s.ctx,
			})
			interceptor.RegisterHandler(&agenttools.CheckMessagesHandler{
				MessageBus: s.messageBus,
				AgentID:    agent.ID,
				TenantID:   tenantID,
			})
			interceptor.RegisterHandler(&agenttools.DelegateJobHandler{
				MessageBus:    s.messageBus,
				SenderAgentID: agent.ID,
				SenderName:    agent.Name,
				TenantID:      tenantID,
				DB:            s.db,
				ServerCtx:     s.ctx,
			})
		}

		// Wire skills: user-installed + built-in defaults for enabled tools
		{
			installedNames := make(map[string]struct{})
			if skillDefs := agentskills.ParseSkillsConfig(agentConfig); len(skillDefs) > 0 {
				for _, sd := range skillDefs {
					loopInput.Skills = append(loopInput.Skills, agentrt.SkillEntry{
						Name:        sd.Name,
						Description: sd.Description,
						Content:     sd.Content,
					})
					installedNames[sd.Name] = struct{}{}
				}
			}
			// Add built-in skills for enabled tools (skip duplicates)
			for _, bs := range agentskills.ResolveBuiltinSkills(loopInput.Tools) {
				if _, exists := installedNames[bs.Name]; exists {
					continue
				}
				loopInput.Skills = append(loopInput.Skills, agentrt.SkillEntry{
					Name:        bs.Name,
					Description: bs.Description,
					Content:     bs.Content,
				})
			}
			// Register use_skill synthetic tool and provision skills to sandbox
			if len(loopInput.Skills) > 0 && sandboxCtx != nil {
				sandboxCtx.SkillEntries = loopInput.Skills
				if isRuntimeToolEnabled("use_skill") {
					interceptor.RegisterAlwaysInclude(&agenttools.UseSkillHandler{
						SandboxCtx:      sandboxCtx,
						SessionID:       swt.Session.ID,
						AvailableSkills: loopInput.Skills,
					})
					loopInput.Tools = appendUnique(loopInput.Tools, "use_skill")
				}
			}
		}

		if err := s.registerProjectFunctions(
			ctx, interceptor, sandboxCtx, tenantID, agent.ID, swt.Session.ID, agentConfig, &loopInput.Tools,
		); err != nil {
			return connect.NewError(connect.CodeFailedPrecondition, err)
		}

		loopInput.Interceptor = interceptor
	}

	// Phase 3 (Monitor): context compaction — no interceptor needed
	if monitorConfig.Enabled {
		monitor = agentrt.NewMonitor(monitorConfig, s.engine, nil, swt.Session.ID)
		loopInput.CompactCh = monitor.CompactCh()
		loopInput.Monitor = monitor
	}

	// Phase 4 (Digest): knowledge bulletin from agent memories
	if digestConfig.Enabled && s.agentMemStore != nil {
		dm := s.sessionMgr.GetDigestManager()
		if dm == nil {
			dm = agentrt.NewDigestManager(digestConfig, s.engine, s.agentMemStore, s.db)
			s.sessionMgr.SetDigestManager(dm)
		}
		dm.EnsureWorker(agent.ID, tenantID)
		loopInput.DigestBulletin = dm.GetBulletin(agent.ID, tenantID)
	}

	// Phase 5 (Peer Messages): register agent session for cross-agent communication
	if peerConfig.Enabled && s.sessionMgr != nil {
		peerCh := s.sessionMgr.RegisterAgentSession(agent.ID, swt.Session.ID)
		loopInput.PeerMessageCh = peerCh
	}

	// Augment system prompt with autonomous capability guidance
	var spawnable []SpawnableAgent
	if spawnConfig.Enabled {
		spawnable = listSpawnableAgents(ctx, tenantID)
	}
	loopInput.SystemPrompt = augmentCapabilitiesSystemPrompt(loopInput.SystemPrompt, spawnConfig, forkConfig, monitorConfig, spawnable...)
	loopInput.SystemPrompt = augmentToolCapabilitiesPrompt(loopInput.SystemPrompt, loopInput.Tools)

	// Wire persistent agent memory provider if configured
	memoryConfig := agentmem.ParseMemoryConfig(agentConfig)
	if memoryConfig != nil && s.agentMemStore != nil {
		extractionModel := agent.Model // use agent's model for extraction; could be overridden
		if em, ok := agentConfig["memory_extraction_model"].(string); ok && em != "" {
			extractionModel = em
		}
		loopInput.MemoryProvider = agentmem.NewAgentMemoryProvider(
			s.agentMemStore,
			s.memoryStore,
			s.memoryEmbedder,
			requestProviderRouter,
			extractionModel,
			*memoryConfig,
		)
		logger.WithFields(
			"session_id", swt.Session.ID,
			"agent_id", agent.ID,
			"auto_retrieve", memoryConfig.AutoRetrieve,
			"auto_extract", memoryConfig.AutoExtract,
			"scope", memoryConfig.Scope,
		).Info("agents: persistent memory enabled")
	}

	// Wire auto-scoring pipeline if score recorder is available
	if s.scoreRecorder != nil {
		var policyConfig *autoscorer.PolicyConfig
		if policyRaw, ok := agentConfig["autoscorer"].(map[string]interface{}); ok {
			if policyCfg, ok := policyRaw["policy"].(map[string]interface{}); ok {
				pc := &autoscorer.PolicyConfig{}
				if patterns, ok := policyCfg["blocked_patterns"].([]interface{}); ok {
					for _, p := range patterns {
						if s, ok := p.(string); ok {
							pc.BlockedPatterns = append(pc.BlockedPatterns, s)
						}
					}
				}
				if keywords, ok := policyCfg["blocked_keywords"].([]interface{}); ok {
					for _, k := range keywords {
						if s, ok := k.(string); ok {
							pc.BlockedKeywords = append(pc.BlockedKeywords, s)
						}
					}
				}
				if maxLen, ok := policyCfg["max_output_length"].(float64); ok {
					pc.MaxOutputLength = int(maxLen)
				}
				policyConfig = pc
			}
		}
		loopInput.AutoScorer = autoscorer.DefaultPipeline(s.scoreRecorder, policyConfig)
	}

	// Wire policy evaluator (always enabled — built-in defaults + user config)
	userPolicies := agentpolicy.ParsePolicyConfig(agentConfig)
	mergedPolicies := agentpolicy.MergeWithDefaults(userPolicies)
	loopInput.PolicyEvaluator = agentpolicy.NewEvaluator(mergedPolicies)

	// Determine streaming preference
	enableStreaming := req.Msg.GetEnableStreaming()

	// MaxIterations is the per-turn LLM→tool cycle limit, NOT the session-level
	// turn limit (which is agent.MaxTurns). A single user message may require
	// multiple LLM calls when tools are involved. Default to 200 iterations per
	// turn, which is the loop's built-in default.
	turnTimeout := 30 * time.Minute

	// Extend turn timeout to accommodate approval wait time if HITL is configured
	if hitlConfig != nil {
		approvalDuration := time.Duration(hitlConfig.TimeoutSeconds) * time.Second
		if turnTimeout < approvalDuration+time.Minute {
			turnTimeout = approvalDuration + time.Minute
		}
	}

	// Extend turn timeout to accommodate ask_user wait time
	askUserTimeout := 31 * time.Minute
	if turnTimeout < askUserTimeout {
		turnTimeout = askUserTimeout
	}

	maxHistoryMessages, sessionTokenBudget := resolveAgentLoopLimits(agentConfig)

	maxIterations := int32(0)
	if maxSteps > 0 {
		maxIterations = maxSteps
	}
	loopConfig := agentrt.LoopConfig{
		MaxIterations:       maxIterations,
		MaxToolCallsPerTurn: agent.MaxToolCallsPerTurn,
		MaxHistoryMessages:  maxHistoryMessages,
		EnableStreaming:     enableStreaming,
		TurnTimeout:         turnTimeout,
		SessionTokenBudget:  sessionTokenBudget,
	}

	// Build initial state from previous turns.
	// Keep the newest turns that can still fit in the configured message budget
	// after accounting for system prompt and the current user message.
	hasSystemPrompt := agent.SystemPrompt.Valid && agent.SystemPrompt.String != ""
	streamTurns := trimTurnsToHistoryBudget(swt.Turns, maxHistoryMessages, hasSystemPrompt, 1)
	var messages []gw.Message
	if agent.SystemPrompt.Valid && agent.SystemPrompt.String != "" {
		messages = append(messages, gw.Message{
			Role:    gw.RoleSystem,
			Content: []gw.ContentPart{{Type: "text", Text: strPtr(agent.SystemPrompt.String)}},
		})
	}
	for _, t := range streamTurns {
		if t.UserInput.Valid && t.UserInput.String != "" {
			messages = append(messages, gw.Message{
				Role:    gw.RoleUser,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr(t.UserInput.String)}},
			})
		}
		if t.AssistantOutput.Valid && t.AssistantOutput.String != "" {
			messages = append(messages, gw.Message{
				Role:    gw.RoleAssistant,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr(t.AssistantOutput.String)}},
			})
		}
	}

	// Drain any steers that were queued while the session was idle (between
	// turns). These are context updates from the UI that arrived after the
	// previous turn completed.
	if pendingSteers := s.sessionMgr.DrainPendingSteers(swt.Session.ID, tenantID); len(pendingSteers) > 0 {
		for _, ps := range pendingSteers {
			role := gw.RoleSystem
			if ps.Role == "user" {
				role = gw.RoleUser
			}
			messages = append(messages, gw.Message{
				Role:    role,
				Content: []gw.ContentPart{{Type: "text", Text: strPtr(ps.Content)}},
			})
		}
		logger.WithFields("session_id", swt.Session.ID, "count", len(pendingSteers)).
			Info("agents: injected pending steer messages into turn")
	}

	config := agentrt.SessionRunnerConfig{
		LoopConfig: loopConfig,
		InitialState: &agentrt.LoopState{
			TurnNumber:         int32(len(swt.Turns)), // so loop's TurnNumber++ gives the correct next turn
			Messages:           messages,
			PriorSessionTokens: int(swt.Session.TotalTokens),
		},
	}

	// Prepare session first (creates runner but does NOT start goroutine)
	loopInput.SystemPrompt += "\n\n<ask_user_policy>\nIf you need information from the user before you can continue, you MUST call the ask_user tool instead of asking in plain assistant text. Use ask_user only for true blockers or missing secrets/credentials, not for permission-seeking. When you can present a small set of concrete choices, include them in the tool call's options array so the UI can render selectable answers.\n</ask_user_policy>"

	logger.WithFields("session_id", swt.Session.ID, "user_input", req.Msg.GetUserInput()).
		Info("agents: preparing session for new turn")
	emitter, err := s.sessionMgr.PrepareSession(ctx, swt.Session.ID, agent.ID, tenantID, loopInput, config)
	if err != nil {
		logger.WithFields("session_id", swt.Session.ID, "error", err.Error()).
			Error("agents: PrepareSession failed")
		telemetry.RecordError(span, err)
		return connect.NewError(connect.CodeInternal, err)
	}
	logger.WithFields("session_id", swt.Session.ID).Info("agents: session prepared successfully")

	// PrepareSession may have waited for an interrupted runner. Re-query the
	// actual max turn_number to prevent collisions with stale swt.Turns data.
	if s.db != nil {
		s.reconcileTurnNumber(ctx, swt.Session.ID, config.InitialState)
	}

	// Ensure an interceptor exists for ask_user (create one if needed)
	if loopInput.Interceptor == nil {
		loopInput.Interceptor = agenttools.NewToolInterceptor(s.sessionMgr.GetToolLoop())
	}

	// Wire parent emitter into handlers now that we have it
	if interceptor, ok := loopInput.Interceptor.(*agenttools.ToolInterceptor); ok {
		if spawnConfig.Enabled {
			if handler, ok := interceptor.Handlers["spawn_agent"]; ok {
				if spawnHandler, ok := handler.(*agenttools.SpawnAgentHandler); ok {
					spawnHandler.ParentEmitter = emitter
				}
			}
			if handler, ok := interceptor.Handlers["parallel_tasks"]; ok {
				if ptHandler, ok := handler.(*agenttools.ParallelTasksHandler); ok {
					ptHandler.Emitter = emitter
				}
			}
		}
		if sandboxConfig.Enabled && s.sandboxMgr != nil {
			agenttools.WireSandboxEmitter(interceptor, emitter)
			agenttools.WireBrowserEmitter(interceptor, emitter)
		}

		// Wire web handler emitters
		if handler, ok := interceptor.Handlers["web_search"]; ok {
			if wsh, ok := handler.(*agenttools.WebSearchHandler); ok {
				wsh.Emitter = emitter
			}
		}
		if handler, ok := interceptor.Handlers["web_fetch"]; ok {
			if wfh, ok := handler.(*agenttools.WebFetchHandler); ok {
				wfh.Emitter = emitter
			}
		}

		// Phase 1: wire task queue emitter
		if jobQueue != nil {
			jobQueue.SetEmitter(emitter)
		}

		// Phase 2: wire fork manager emitter
		if forkManager != nil {
			forkManager.SetEmitter(emitter)
		}

		// Register platform tools (chat-first UI: agent CRUD via platform meta-agent)
		{
			platformCtx := &agenttools.PlatformToolContext{
				CommandBus: sys.CommandBus,
				QueryBus:   sys.QueryBus,
				TenantID:   tenantID,
				UserID:     s.resolveUserID(ctx),
				Emitter:    emitter,
				SessionID:  swt.Session.ID,
			}
			for _, h := range agenttools.NewPlatformHandlers(platformCtx) {
				if !isRuntimeToolEnabled(h.Name()) {
					continue
				}
				interceptor.RegisterHandler(h)
				loopInput.Tools = appendUnique(loopInput.Tools, h.Name())
			}
		}

		// Register ask_user handler — channels are now wired by PrepareSession
		if isRuntimeToolEnabled("ask_user") {
			interceptor.RegisterAlwaysInclude(&agenttools.AskUserHandler{
				Emitter:    emitter,
				SessionID:  swt.Session.ID,
				RequestCh:  loopInput.UserInputReqCh,
				ResponseCh: loopInput.UserInputRespCh,
			})
			loopInput.Tools = appendUnique(loopInput.Tools, "ask_user")
		}
	}

	// Phase 3: wire monitor emitter (outside interceptor block — monitor is not a tool handler)
	if monitor != nil {
		monitor.SetEmitter(emitter)
	}

	// Attach event sink BEFORE launching so no events are missed
	eventCh := make(chan agentrt.Event, 64)
	emitter.AddSink(agentrt.EventSinkFunc(func(e agentrt.Event) error {
		select {
		case eventCh <- e:
			return nil
		default:
			return errors.New("event channel full")
		}
	}))

	// For persistent agents, mark lifecycle_status = 'running' on turn start.
	// CONCURRENT_RUNNING is enforced here (the transition to running), with
	// the agent itself excluded from the count so another turn on an
	// already-running agent is never blocked.
	if isPersistent && s.db != nil {
		if err := enterprise.CheckResourceLimit(ctx, s.db, enterprise.LicenseMonitorFromContext(ctx),
			enterprise.UsageTypeConcurrentRunning,
			`SELECT COUNT(*) FROM agent_definitions WHERE tenant_id = $1 AND deleted_at IS NULL AND lifecycle_status = 'running' AND id <> $2`,
			[]interface{}{tenantID, agent.ID}, 1, "concurrently running agent"); err != nil {
			return connect.NewError(connect.CodeResourceExhausted, err)
		}
		s.db.ExecContext(ctx, `UPDATE agent_definitions SET lifecycle_status = 'running', updated_at = NOW() WHERE id = $1 AND tenant_id = $2`, agent.ID, tenantID)
	}

	// Now launch the session goroutine with sinks already wired
	logger.WithFields("session_id", swt.Session.ID).Info("agents: launching session")
	if err := s.sessionMgr.LaunchSession(ctx, swt.Session.ID, loopInput); err != nil {
		logger.WithFields("session_id", swt.Session.ID, "error", err.Error()).
			Error("agents: LaunchSession failed")
		telemetry.RecordError(span, err)
		return connect.NewError(connect.CodeInternal, err)
	}
	logger.WithFields("session_id", swt.Session.ID).Info("agents: session launched successfully")

	runner := s.sessionMgr.GetRunner(swt.Session.ID)
	if runner == nil {
		logger.WithFields("session_id", swt.Session.ID).Error("agents: runner not found after launch")
		err := connect.NewError(connect.CodeInternal, errors.New("session runner not found after start"))
		telemetry.RecordError(span, err)
		return err
	}
	logger.WithFields("session_id", swt.Session.ID).Info("agents: streaming events to client")

	// setIdleOnTurnEnd transitions persistent agents back to idle after the turn.
	setIdleOnTurnEnd := func() {
		if isPersistent && s.db != nil {
			s.db.ExecContext(context.Background(), `UPDATE agent_definitions SET lifecycle_status = 'idle', updated_at = NOW() WHERE id = $1 AND tenant_id = $2`, agent.ID, tenantID)
		}
	}

	// Set trace input for trace list display
	span.SetAttributes(attribute.String("trace.input", req.Msg.GetUserInput()))

	// Stream events to client until session ends, accumulating usage for span enrichment
	eventCount := 0
	var totalPromptTokens, totalCompletionTokens, totalTurnTokens int64
	var lastAssistantText string
	enrichSpanUsage := func(reason string) {
		attrs.SetAgentTokens(span, totalPromptTokens, totalCompletionTokens, totalTurnTokens)
		if lastAssistantText != "" {
			// Truncate for trace list preview
			output := lastAssistantText
			if len(output) > 500 {
				output = output[:500]
			}
			span.SetAttributes(attribute.String("trace.output", output))
		}
	}
	for {
		select {
		case <-ctx.Done():
			setIdleOnTurnEnd()
			enrichSpanUsage("context_cancelled")
			telemetry.AddSpanEvent(span, attrs.EventRequestComplete,
				attribute.String("stream.end_reason", "context_cancelled"),
				attribute.Int("stream.events_sent", eventCount),
			)
			logger.WithFields("session_id", swt.Session.ID, "events_sent", eventCount, "reason", "context_cancelled").
				Info("agents: streaming loop ended")
			return nil
		case event, ok := <-eventCh:
			if !ok {
				setIdleOnTurnEnd()
				enrichSpanUsage("channel_closed")
				telemetry.AddSpanEvent(span, attrs.EventRequestComplete,
					attribute.String("stream.end_reason", "channel_closed"),
					attribute.Int("stream.events_sent", eventCount),
				)
				logger.WithFields("session_id", swt.Session.ID, "events_sent", eventCount, "reason", "channel_closed").
					Info("agents: streaming loop ended")
				return nil
			}
			eventCount++
			// Accumulate only per-call LLM deltas. Turn/session end events carry
			// cumulative usage and would double count if included here.
			if event.Type == agentrt.EventLLMEnd && event.Usage != nil {
				totalPromptTokens += int64(event.Usage.PromptTokens)
				totalCompletionTokens += int64(event.Usage.CompletionTokens)
				totalTurnTokens += int64(event.Usage.TotalTokens)
			}
			// Capture last assistant text for trace output
			if event.TextDelta != "" {
				lastAssistantText += event.TextDelta
			}
			if event.Type == agentrt.EventSandboxReady && event.Data != nil {
				if readyMs, ok := event.Data["session_ready_ms"].(int64); ok && readyMs > 0 {
					span.SetAttributes(attribute.Int64(attrs.AgentSessionReadyMs, readyMs))
				} else if readyMsFloat, ok := event.Data["session_ready_ms"].(float64); ok && readyMsFloat > 0 {
					span.SetAttributes(attribute.Int64(attrs.AgentSessionReadyMs, int64(readyMsFloat)))
				}
			}
			if event.Type == agentrt.EventSandboxGitClone && event.Data != nil {
				cloneDur, _ := event.Data["clone_duration_ms"].(int64)
				cloneBytes, _ := event.Data["clone_bytes_total"].(int64)
				cloneStrategy, _ := event.Data["clone_strategy"].(string)
				attrs.SetSandboxGitCloneMetrics(span, cloneDur, cloneBytes, cloneStrategy, true)
			}
			logger.WithFields("session_id", swt.Session.ID, "event_type", string(event.Type), "event_count", eventCount).
				Debug("agents: sending event to stream")
			protoEvent := runtimeEventToProto(&event)
			if err := stream.Send(protoEvent, event.Data); err != nil {
				logger.WithFields("session_id", swt.Session.ID, "event_type", string(event.Type), "error", err.Error()).
					Error("agents: failed to send event to stream")
				telemetry.RecordError(span, err)
				return err
			}
			// Stop streaming when session ends
			if event.Type == agentrt.EventSessionEnd || event.Type == agentrt.EventSessionError {
				// Record cumulative usage to license monitor before returning
				if event.Usage != nil && event.Usage.TotalTokens > 0 {
					provider := s.getProviderForModel(ctx, agent.Model)
					costDetails := metrics.CalculateCost(provider, agent.Model, event.Usage.PromptTokens, event.Usage.CompletionTokens, 0)
					if err := s.recordUsageMetrics(int64(event.Usage.PromptTokens), int64(event.Usage.CompletionTokens), costDetails.EstimatedUSD, 0, false); err != nil {
						logger.Warnf("agents: streaming: spend limit exceeded after session end: %v", err)
					}
				}
				logger.WithFields("session_id", swt.Session.ID, "events_sent", eventCount, "reason", string(event.Type)).
					Info("agents: streaming loop ended")
				telemetry.AddSpanEvent(span, attrs.EventRequestComplete,
					attribute.String("stream.end_reason", string(event.Type)),
					attribute.Int("stream.events_sent", eventCount),
				)
				if event.Usage != nil {
					attrs.SetAgentTokens(
						span,
						int64(event.Usage.PromptTokens),
						int64(event.Usage.CompletionTokens),
						int64(event.Usage.TotalTokens),
					)
				}
				return nil
			}
		case <-runner.Done():
			logger.WithFields("session_id", swt.Session.ID, "events_sent", eventCount).
				Debug("agents: runner done, draining remaining events")
			// Drain remaining events
			drainCount := 0
			for {
				select {
				case event := <-eventCh:
					drainCount++
					protoEvent := runtimeEventToProto(&event)
					if err := stream.Send(protoEvent, event.Data); err != nil {
						logger.WithFields("session_id", swt.Session.ID, "event_type", string(event.Type), "error", err.Error()).
							Warn("agents: failed to send drained event")
						telemetry.RecordError(span, err)
					}
					// Record usage on session end/error during drain
					if (event.Type == agentrt.EventSessionEnd || event.Type == agentrt.EventSessionError) &&
						event.Usage != nil && event.Usage.TotalTokens > 0 {
						provider := s.getProviderForModel(ctx, agent.Model)
						costDetails := metrics.CalculateCost(provider, agent.Model, event.Usage.PromptTokens, event.Usage.CompletionTokens, 0)
						if err := s.recordUsageMetrics(int64(event.Usage.PromptTokens), int64(event.Usage.CompletionTokens), costDetails.EstimatedUSD, 0, false); err != nil {
							logger.Warnf("agents: streaming drain: spend limit exceeded: %v", err)
						}
					}
				default:
					logger.WithFields("session_id", swt.Session.ID, "events_sent", eventCount, "drained", drainCount, "reason", "runner_done").
						Info("agents: streaming loop ended")
					telemetry.AddSpanEvent(span, attrs.EventRequestComplete,
						attribute.String("stream.end_reason", "runner_done"),
						attribute.Int("stream.events_sent", eventCount),
						attribute.Int("stream.events_drained", drainCount),
					)
					return nil
				}
			}
		}
	}
}

// SteerSession injects a message into a running agent session.
func (s *Server) SteerSession(ctx context.Context, req *connect.Request[agentspb.SteerSessionRequest]) (*connect.Response[agentspb.SteerSessionResponse], error) {

	if s.sessionMgr == nil {
		err := connect.NewError(connect.CodeInternal, errors.New("agent session manager not initialized"))
		return nil, err
	}

	tenantID, err := s.resolveTenantID(ctx, "")
	if err != nil {
		return nil, err
	}

	// Verify session belongs to this tenant before allowing steering.
	// Without this check, any caller who knows a session ID could inject
	// messages into another tenant's running session.
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		err := connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
		return nil, err
	}
	sessionQ := agentsquery.NewGetSessionByIDQuery(req.Msg.GetSessionId(), tenantID)
	sessionRes, err := sys.QueryBus.Execute(ctx, sessionQ)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if sessionRes == nil {
		err := connect.NewError(connect.CodeNotFound, errors.New("session not found"))
		return nil, err
	}

	// Validate steer role
	role := req.Msg.GetRole()
	if role != "user" && role != "system" {
		err := connect.NewError(connect.CodeInvalidArgument, errors.New("role must be 'user' or 'system'"))
		return nil, err
	}

	msg := agentrt.SteerMessage{
		Role:    role,
		Content: req.Msg.GetContent(),
	}

	if err := s.sessionMgr.SteerSession(req.Msg.GetSessionId(), tenantID, msg); err != nil {
		return connect.NewResponse(&agentspb.SteerSessionResponse{
			Accepted: false,
			Message:  err.Error(),
		}), nil
	}

	return connect.NewResponse(&agentspb.SteerSessionResponse{
		Accepted: true,
		Message:  "steer message accepted",
	}), nil
}

// ============================================================================
// HITL Approval Reviews
// ============================================================================

func (s *Server) SubmitReview(ctx context.Context, req *connect.Request[agentspb.SubmitReviewRequest]) (*connect.Response[agentspb.SubmitReviewResponse], error) {
	if s.sessionMgr == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("agent session manager not initialized"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	action := "approve"
	if req.Msg.GetAction() == agentspb.ApprovalAction_APPROVAL_ACTION_DENY {
		action = "deny"
	}

	// Build per-tool decisions if provided
	var perToolDecisions []agentrt.PerToolDecision
	for _, d := range req.Msg.GetDecisions() {
		ptAction := "approve"
		if d.GetAction() == agentspb.ApprovalAction_APPROVAL_ACTION_DENY {
			ptAction = "deny"
		}
		perToolDecisions = append(perToolDecisions, agentrt.PerToolDecision{
			ToolCallID: d.GetToolCallId(),
			Action:     ptAction,
			Reason:     d.GetReason(),
		})
	}

	decision := agentrt.ApprovalDecision{
		ReviewID:   req.Msg.GetReviewId(),
		Action:     action,
		Decisions:  perToolDecisions,
		Reason:     req.Msg.GetReason(),
		ResolvedBy: req.Msg.GetResolvedBy(),
	}

	if err := s.sessionMgr.SubmitReview(ctx, req.Msg.GetReviewId(), tenantID, decision); err != nil {
		return connect.NewResponse(&agentspb.SubmitReviewResponse{
			Accepted: false,
			Message:  err.Error(),
		}), nil
	}

	return connect.NewResponse(&agentspb.SubmitReviewResponse{
		Accepted: true,
		Message:  "review " + action + "d",
	}), nil
}

func (s *Server) GetReview(ctx context.Context, req *connect.Request[agentspb.GetReviewRequest]) (*connect.Response[agentspb.GetReviewResponse], error) {
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

	q := agentsquery.NewGetApprovalReviewByIDQuery(req.Msg.GetReviewId(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("review not found"))
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}
	if data == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("review not found"))
	}

	rm, ok := data.(*agentsquery.ApprovalReviewReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}

	return connect.NewResponse(&agentspb.GetReviewResponse{
		Review: approvalReviewToProto(rm),
	}), nil
}

func (s *Server) ListReviews(ctx context.Context, req *connect.Request[agentspb.ListReviewsRequest]) (*connect.Response[agentspb.ListReviewsResponse], error) {
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

	var sessionID *string
	if req.Msg.SessionId != nil {
		sessionID = req.Msg.SessionId
	}

	var status *string
	if req.Msg.GetStatus() != agentspb.ApprovalReviewStatus_APPROVAL_REVIEW_STATUS_UNSPECIFIED {
		s := approvalStatusToString(req.Msg.GetStatus())
		status = &s
	}

	limit := int(req.Msg.GetLimit())
	offset := int(req.Msg.GetOffset())

	q := agentsquery.NewListApprovalReviewsQuery(tenantID, sessionID, status, limit, offset)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &agentspb.ListReviewsResponse{}
	if res != nil {
		var data interface{} = res
		if qResp, ok := res.(*query.Response); ok {
			data = qResp.Data
		}
		if result, ok := data.(*agentsquery.ApprovalReviewsResult); ok {
			resp.Total = int32(result.Total)
			for i := range result.Reviews {
				resp.Reviews = append(resp.Reviews, approvalReviewToProto(&result.Reviews[i]))
			}
		}
	}

	return connect.NewResponse(resp), nil
}

// runtimeEventToProto converts a runtime Event to an AgentEvent proto.
func runtimeEventToProto(e *agentrt.Event) *agentspb.AgentEvent {
	proto := &agentspb.AgentEvent{
		Type:           string(e.Type),
		SessionId:      e.SessionID,
		TurnNumber:     e.TurnNumber,
		TextDelta:      e.TextDelta,
		ToolCallId:     e.ToolCallID,
		ToolName:       e.ToolName,
		ToolArgs:       e.ToolArgs,
		ToolResult:     e.ToolResult,
		ToolSuccess:    e.ToolSuccess,
		ToolDurationMs: e.ToolDuration,
		FinishReason:   e.Reason,
		Error:          e.Error,
		ReviewId:       e.ReviewID,
	}
	if e.Usage != nil {
		proto.PromptTokens = int32(e.Usage.PromptTokens)
		proto.CompletionTokens = int32(e.Usage.CompletionTokens)
		proto.TotalTokens = int32(e.Usage.TotalTokens)
		proto.CacheReadTokens = int32(e.Usage.CacheReadTokens)
		proto.CacheWriteTokens = int32(e.Usage.CacheWriteTokens)
	}

	// Populate spawn-specific fields in the proto Data
	if e.Type == agentrt.EventSpawnStart || e.Type == agentrt.EventSpawnEnd || e.Type == agentrt.EventSpawnError {
		// Spawn metadata is passed via e.Data — no special proto fields needed,
		// the frontend reads from the SSE event type and data fields.
	}

	// Populate approval-specific fields
	if e.Type == agentrt.EventApprovalRequested {
		if toolCalls, ok := e.Data["tool_calls"].([]gw.ToolCall); ok {
			for _, tc := range toolCalls {
				proto.PendingToolCalls = append(proto.PendingToolCalls, &agentspb.PendingToolCall{
					ToolCallId: tc.ID,
					ToolName:   tc.Function.Name,
					ToolArgs:   tc.Function.Arguments,
				})
			}
		}
	}
	if e.Type == agentrt.EventApprovalResolved {
		if action, ok := e.Data["action"].(string); ok {
			proto.ApprovalAction = action
		}
	}

	// User input hot-path field
	if e.UserInputID != "" {
		proto.UserInputId = e.UserInputID
	}

	// Sandbox hot-path fields
	if e.SandboxID != "" {
		proto.SandboxId = e.SandboxID
	}
	if e.SandboxExitCode != 0 {
		proto.SandboxExitCode = int32(e.SandboxExitCode)
	}
	if e.SandboxDurationMs != 0 {
		proto.SandboxDurationMs = e.SandboxDurationMs
	}

	// Fallback hot-path fields
	if e.FallbackFromModel != "" {
		proto.FallbackFromModel = e.FallbackFromModel
	}
	if e.FallbackToModel != "" {
		proto.FallbackToModel = e.FallbackToModel
	}
	if e.FallbackAttempt != 0 {
		proto.FallbackAttempt = e.FallbackAttempt
	}

	return proto
}

func strPtr(s string) *string {
	return &s
}

// SpawnableAgent is a minimal summary for system prompt injection.
type SpawnableAgent struct {
	ID          string
	Name        string
	Description string
}

func augmentCapabilitiesSystemPrompt(base string, spawnConfig agenttools.SpawnConfig, forkConfig agentrt.ForkConfig, monitorConfig agentrt.MonitorConfig, availableAgents ...SpawnableAgent) string {
	var parts []string

	if spawnConfig.Enabled {
		var spawnGuidance strings.Builder
		if spawnConfig.Async {
			spawnGuidance.WriteString(`## Async Sub-Agent Spawning
You can delegate jobs to sub-agents using the spawn_agent tool. In async mode, spawns return immediately and run in the background. Use check_job to poll their status. Use this when:
- A job is independent and can run concurrently with the current conversation
- You need to parallelize work across multiple sub-agents
- A job may take long and you don't want to block the conversation`)
		} else {
			spawnGuidance.WriteString(`## Sub-Agent Spawning
You can delegate tasks to sub-agents using the spawn_agent tool. The sub-agent runs synchronously and returns its result. Use this when:
- A subtask benefits from focused, independent attention
- You want to delegate a well-scoped piece of work`)
		}

		if len(availableAgents) > 0 {
			spawnGuidance.WriteString("\n\nAvailable agents for spawning (use exact name or ID in the agent_id parameter):")
			for _, a := range availableAgents {
				if a.Description != "" {
					spawnGuidance.WriteString(fmt.Sprintf("\n- **%s** (id: %s) — %s", a.Name, a.ID, a.Description))
				} else {
					spawnGuidance.WriteString(fmt.Sprintf("\n- **%s** (id: %s)", a.Name, a.ID))
				}
			}
		} else {
			spawnGuidance.WriteString("\n\nIf you omit agent_id, the sub-agent will use the same configuration as yours.")
		}

		parts = append(parts, spawnGuidance.String())
	}

	if forkConfig.Enabled {
		parts = append(parts, `## Context Forking
You have access to the fork tool. Use it to branch the current conversation context for independent reasoning. The fork runs concurrently and its conclusion is automatically injected back. Use this when:
- You need to deeply analyze a specific sub-problem without losing your current train of thought
- You want to explore an approach speculatively before committing
- A question requires focused reasoning that would derail the main conversation flow`)
	}

	if monitorConfig.Enabled {
		parts = append(parts, `## Context Window Management
Context compaction is active. When the conversation grows long, older messages will be automatically summarized to free context space. This is transparent to you — continue the conversation normally. If you notice context summaries appearing, they represent condensed versions of earlier exchanges.`)
	}

	if len(parts) == 0 {
		return base
	}
	guidance := strings.Join(parts, "\n\n")
	if strings.TrimSpace(base) == "" {
		return guidance
	}
	return base + "\n\n" + guidance
}

func augmentSandboxSystemPrompt(base string, sandboxConfig sandbox.SandboxConfig) string {
	workflowGuidance := `YOU ARE AN AUTONOMOUS AGENT WITH A LIVE SANDBOX. You MUST execute ALL commands and write ALL files yourself using your sandbox tools. NEVER give the user instructions to follow — they cannot run commands, only you can via your tools.

When using sandbox tools, follow this workflow:
1. For full-stack requests, create and run backend first.
2. Expose backend port and capture the public URL.
3. Build frontend next and configure API calls to that exposed backend URL (never localhost).
4. Start frontend with explicit host/port/strictPort, then expose frontend port.
5. After each server start, verify with sandbox_list_ports and fix failures before ending the turn.
6. Be decisive — choose the right tools and environment based on the task. Do not ask the user to make choices you can infer from context.

CRITICAL — Tool usage policy:
- ALWAYS use sandbox_execute or sandbox_shell to run commands. NEVER write a command in your response and ask the user to run it.
- ALWAYS use sandbox_write_file to create files. NEVER paste file contents in your response and ask the user to save them.
- If you find yourself writing a code block with instructions like "run this" or "create this file" — STOP and use the appropriate tool instead.

IMPORTANT — Task completion policy:
- Do NOT stop after initial setup (installing dependencies, writing files, etc.). Keep working until the task is fully complete and verified.
- After writing code, RUN it. After running it, verify the output is correct. If there are errors, debug and fix them.
- If a command fails, diagnose the error and retry with a fix. Do not give up or ask the user unless you have exhausted all reasonable approaches.
- Only end your turn when you have delivered a working result or are genuinely blocked and need user input.
- When building an app, the turn should end with the app running and accessible, not just with files written or dependencies installed.`

	envGuidance := `Sandbox environment details:
- The root filesystem is READ-ONLY. Only /workspace (your working directory) and /tmp are writable.
- Pre-installed runtimes: Node.js 24 (npm, tsx, typescript), Python 3.12 (pip), Ruby, Go 1.22, Rust.
- npm/pip/cargo are configured to install into /workspace/.npm-global, /workspace/.pip-packages, /workspace/.cargo respectively. The PATH already includes these directories.
- Always create projects and write files under /workspace. Never try to write to /usr, /etc, or other system paths.
- Global npm installs (npm install -g) work because NPM_CONFIG_PREFIX is set to /workspace/.npm-global.`

	workflowMarker := "When using sandbox tools, follow this workflow:"
	repoMarker := "Git repository context for this session:"
	envMarker := "Sandbox environment details:"

	var guidanceParts []string
	if !strings.Contains(base, envMarker) {
		guidanceParts = append(guidanceParts, envGuidance)
	}
	if !strings.Contains(base, workflowMarker) {
		guidanceParts = append(guidanceParts, workflowGuidance)
	}
	if sandboxConfig.GitRepoURL != "" && !strings.Contains(base, repoMarker) {
		branch := strings.TrimSpace(sandboxConfig.GitBranch)
		if branch == "" {
			branch = "(default branch)"
		}
		repoGuidance := fmt.Sprintf(`Git repository context for this session:
- Preconfigured repo: %s
- Branch: %s
- The repository is mounted read-only at /repo once the sandbox is created.
- Use sandbox_list_files on /repo to verify access.
- If /repo is unavailable, diagnose with sandbox tools and report the concrete error.`, sandboxConfig.GitRepoURL, branch)
		guidanceParts = append(guidanceParts, repoGuidance)
	}

	if len(guidanceParts) == 0 {
		return base
	}
	if strings.TrimSpace(base) == "" {
		return strings.Join(guidanceParts, "\n\n")
	}
	return base + "\n\n" + strings.Join(guidanceParts, "\n\n")
}

// augmentToolCapabilitiesPrompt injects a capabilities summary into the system prompt
// so the LLM knows what tools it has access to and can respond appropriately.
func augmentToolCapabilitiesPrompt(base string, tools []string) string {
	if len(tools) == 0 {
		return base
	}

	// Categorize tools into capability groups for a concise summary
	var capabilities []string
	hasSandbox := false
	hasWebSearch := false
	hasMemory := false
	hasSpawn := false
	hasBrowser := false
	hasStorage := false
	hasHumanInteraction := false
	hasPlatformMutation := false

	for _, t := range tools {
		switch agenttools.ToolCapabilityForName(t) {
		case agenttools.ToolCapabilityFilesystem, agenttools.ToolCapabilityProcess, agenttools.ToolCapabilitySandboxRuntime, agenttools.ToolCapabilityRepository:
			hasSandbox = true
		case agenttools.ToolCapabilityNetwork:
			if t == "web_search" || t == "web_fetch" {
				hasWebSearch = true
			} else if strings.HasPrefix(t, "sandbox_") {
				hasSandbox = true
			}
		case agenttools.ToolCapabilityBrowser:
			hasBrowser = true
		case agenttools.ToolCapabilityMemory:
			hasMemory = true
		case agenttools.ToolCapabilityAgentDelegation:
			hasSpawn = true
		case agenttools.ToolCapabilityStorage:
			hasStorage = true
		case agenttools.ToolCapabilityHumanInteraction:
			hasHumanInteraction = true
		case agenttools.ToolCapabilityPlatformMutation:
			hasPlatformMutation = true
		}
	}

	if hasSandbox {
		capabilities = append(capabilities, "- **Sandbox environment**: You have a live, isolated sandbox container. You MUST use it to execute code, run shell commands, write/read/edit files, install dependencies, start servers, and expose ports. NEVER tell the user to run commands themselves — execute everything directly using your sandbox tools (sandbox_execute, sandbox_write_file, sandbox_read_file, sandbox_shell, etc.).")
	}
	if hasWebSearch {
		capabilities = append(capabilities, "- **Web access**: You can search the web and fetch webpage content to find documentation, APIs, and solutions.")
	}
	if hasMemory {
		capabilities = append(capabilities, "- **Persistent memory**: You can store and recall information across sessions.")
	}
	if hasSpawn {
		capabilities = append(capabilities, "- **Sub-agents**: You can spawn specialized sub-agents for parallel tasks.")
	}
	if hasStorage {
		capabilities = append(capabilities, "- **Artifacts**: You can upload, download, and list generated artifacts in tenant-scoped storage.")
	}
	if hasHumanInteraction {
		capabilities = append(capabilities, "- **Human interaction**: You can ask the user for input or coordinate with configured channels/peer agents when those tools are available.")
	}
	if hasPlatformMutation {
		capabilities = append(capabilities, "- **Platform mutation**: You can create, update, or delete platform resources exposed by your enabled tools. Use these only when explicitly relevant to the task.")
	}
	if hasBrowser {
		capabilities = append(capabilities, `- **Browser automation**: You can control a Chromium browser. Workflow:
  1. Call browser_navigate to go to a URL — it automatically returns numbered interactive elements.
  2. Use element index numbers with browser_click(index=N), browser_type(index=N, text="..."), browser_select(index=N, values=["..."]).
  3. After any action that changes the page, call browser_observe to re-index elements.
  4. Use browser_scroll to see more content, browser_evaluate for custom JS, browser_screenshot for visual capture.
  IMPORTANT: Always use element indices from browser_observe/browser_navigate output. Do NOT guess CSS selectors.
  RESILIENCE: If a browser tool fails, DO NOT give up. Retry the operation. If an element click fails, call browser_observe to refresh elements and try again. If the sandbox is starting up, wait and retry browser_navigate. Complex websites (SPAs, booking sites) require patience — use browser_wait for dynamic content, browser_scroll to find hidden elements, and browser_observe after every page change. NEVER suggest the user visit a URL themselves — you MUST complete the task using your browser tools.`)
	}

	// Workflow creation is always available (registered via AlwaysInclude)
	capabilities = append(capabilities, "- **Workflow creation**: You can create Studio workflows using create_workflow. Compose nodes (start, provider, agent, cache, ifElse, response, etc.) and edges to build automation pipelines. Created workflows appear in Studio for further editing and execution.")

	capBlock := "<capabilities>\nYou have access to the following capabilities:\n" +
		strings.Join(capabilities, "\n") +
		"\n\nCRITICAL INSTRUCTION: You MUST use your tools to perform tasks directly. NEVER give the user instructions, code snippets, commands to copy-paste, or links to visit. NEVER say \"run this command\", \"create a file with this content\", or \"visit this URL\" — instead, call the appropriate tool yourself. If you have sandbox tools, every command must be executed via sandbox_execute or sandbox_shell, every file must be created via sandbox_write_file. If a tool fails, RETRY — do not apologize and give up. You are an autonomous agent that persists through errors, not an assistant that gives instructions.\n</capabilities>"

	if strings.TrimSpace(base) == "" {
		return capBlock
	}
	return base + "\n\n" + capBlock
}

// listSpawnableAgents queries enabled sub-agents for system prompt injection.
func listSpawnableAgents(ctx context.Context, tenantID string) []SpawnableAgent {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil {
		return nil
	}
	enabled := true
	subagentMode := "subagent"
	q := agentsquery.NewListAgentsQuery(tenantID, &enabled, false, &subagentMode, nil, 20, 0)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil
	}
	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}
	agents, ok := data.([]agentsquery.AgentDefinitionReadModel)
	if !ok || len(agents) == 0 {
		return nil
	}
	out := make([]SpawnableAgent, 0, len(agents))
	for _, a := range agents {
		desc := ""
		if a.Description.Valid {
			desc = a.Description.String
		}
		out = append(out, SpawnableAgent{ID: a.ID, Name: a.Name, Description: desc})
	}
	return out
}

// ============================================================================
// Helpers
// ============================================================================

func agentReadModelToProto(rm *agentsquery.AgentDefinitionReadModel) *agentspb.AgentDefinition {
	agent := &agentspb.AgentDefinition{
		Id:                  rm.ID,
		TenantId:            rm.TenantID,
		Name:                rm.Name,
		Model:               rm.Model,
		Tools:               rm.Tools,
		MaxTurns:            rm.MaxTurns,
		MaxToolCallsPerTurn: rm.MaxToolCallsPerTurn,
		Mode:                stringToProtoAgentMode(rm.Mode),
		TaskPermissionMode:  stringToProtoTaskPermissionMode(rm.TaskPermissionMode),
		Hidden:              rm.Hidden,
		Enabled:             rm.Enabled,
		CreatedAt:           utils.ParseTimestamp(rm.CreatedAt),
		UpdatedAt:           utils.ParseTimestamp(rm.UpdatedAt),
	}

	if rm.MaxSteps.Valid {
		agent.MaxSteps = &rm.MaxSteps.Int32
	}
	if rm.Color.Valid && strings.TrimSpace(rm.Color.String) != "" {
		v := rm.Color.String
		agent.Color = &v
	}
	if rm.WorkingDirectory.Valid && strings.TrimSpace(rm.WorkingDirectory.String) != "" {
		v := rm.WorkingDirectory.String
		agent.WorkingDirectory = &v
	}
	if rm.MentionAlias.Valid && strings.TrimSpace(rm.MentionAlias.String) != "" {
		v := rm.MentionAlias.String
		agent.MentionAlias = &v
	}

	agent.ExecutionPolicy = &agentspb.AgentExecutionPolicy{
		TaskPermissionMode: agent.TaskPermissionMode,
		MaxSteps:           agent.MaxSteps,
		WorkingDirectory:   agent.WorkingDirectory,
	}

	if rm.Description.Valid {
		agent.Description = rm.Description.String
	}
	if rm.SystemPrompt.Valid {
		agent.SystemPrompt = rm.SystemPrompt.String
	}

	if len(rm.Config) > 0 {
		var configMap map[string]interface{}
		if err := json.Unmarshal(rm.Config, &configMap); err == nil {
			if s, err := structpb.NewStruct(configMap); err == nil {
				agent.Config = s
			}
		}
	}

	agent.LifecycleMode = stringToProtoAgentLifecycleMode(rm.LifecycleMode)
	agent.LifecycleStatus = stringToProtoAgentLifecycleStatus(rm.LifecycleStatus)

	if rm.Icon.Valid && strings.TrimSpace(rm.Icon.String) != "" {
		v := rm.Icon.String
		agent.Icon = &v
	}
	if rm.SandboxID.Valid && strings.TrimSpace(rm.SandboxID.String) != "" {
		agent.SandboxId = rm.SandboxID.String
	}
	if rm.PrimarySessionID.Valid && strings.TrimSpace(rm.PrimarySessionID.String) != "" {
		agent.PrimarySessionId = rm.PrimarySessionID.String
	}
	if rm.ActiveRevisionID.Valid && strings.TrimSpace(rm.ActiveRevisionID.String) != "" {
		agent.ActiveRevisionId = rm.ActiveRevisionID.String
	}

	// Identity (only populated for persistent agents)
	if rm.LifecycleMode == "persistent" {
		agent.Identity = &agentspb.AgentIdentity{
			SoulMd:     rm.SoulMD,
			IdentityMd: rm.IdentityMD,
			UserMd:     rm.UserMD,
			RoleMd:     rm.RoleMD,
		}

		sbCfg := &agentspb.AgentSandboxConfig{}
		if rm.SandboxImage.Valid {
			sbCfg.Image = rm.SandboxImage.String
		}
		if rm.SandboxCPULimit.Valid {
			sbCfg.CpuLimit = rm.SandboxCPULimit.Float64
		}
		if rm.SandboxMemoryMB.Valid {
			sbCfg.MemoryMb = int64(rm.SandboxMemoryMB.Int32)
		}
		if rm.SandboxDiskMB.Valid {
			sbCfg.DiskMb = int64(rm.SandboxDiskMB.Int32)
		}
		if rm.SandboxTimeoutSeconds.Valid {
			sbCfg.TimeoutSeconds = rm.SandboxTimeoutSeconds.Int32
		}
		if rm.SandboxNetworkMode.Valid {
			sbCfg.NetworkMode = rm.SandboxNetworkMode.String
		}
		sbCfg.AllowedHosts = rm.SandboxAllowedHosts
		if rm.SandboxSSHEnabled.Valid {
			sbCfg.SshEnabled = rm.SandboxSSHEnabled.Bool
		}
		if rm.SandboxGitRepoURL.Valid {
			sbCfg.GitRepoUrl = rm.SandboxGitRepoURL.String
		}
		if rm.SandboxGitBranch.Valid {
			sbCfg.GitBranch = rm.SandboxGitBranch.String
		}
		if len(rm.SandboxEnvVars) > 0 {
			var envVars map[string]string
			if err := json.Unmarshal(rm.SandboxEnvVars, &envVars); err == nil {
				sbCfg.EnvVars = envVars
			}
		}
		agent.SandboxConfig = sbCfg

		dbCfg := &agentspb.AgentDatabaseConfig{}
		if rm.DBSqlitePath.Valid {
			dbCfg.SqlitePath = rm.DBSqlitePath.String
		}
		if rm.DBLanceDBPath.Valid {
			dbCfg.LancedbPath = rm.DBLanceDBPath.String
		}
		if rm.DBRedbPath.Valid {
			dbCfg.RedbPath = rm.DBRedbPath.String
		}
		agent.DatabaseConfig = dbCfg

		wCfg := &agentspb.AgentWorkersConfig{}
		if rm.MaxConcurrentWorkers.Valid {
			wCfg.MaxConcurrentWorkers = rm.MaxConcurrentWorkers.Int32
		}
		if len(rm.WorkerPoolConfig) > 0 {
			var wpMap map[string]interface{}
			if err := json.Unmarshal(rm.WorkerPoolConfig, &wpMap); err == nil {
				if s, err := structpb.NewStruct(wpMap); err == nil {
					wCfg.PoolConfig = s
				}
			}
		}
		agent.WorkersConfig = wCfg
	}

	return agent
}

func sessionReadModelToProto(rm *agentsquery.AgentSessionReadModel) *agentspb.AgentSession {
	session := &agentspb.AgentSession{
		Id:          rm.ID,
		TenantId:    rm.TenantID,
		AgentId:     rm.AgentID.String,
		Status:      stringToSessionStatus(rm.Status),
		TurnCount:   rm.TurnCount,
		TotalTokens: rm.TotalTokens,
		CreatedAt:   utils.ParseTimestamp(rm.CreatedAt),
		UpdatedAt:   utils.ParseTimestamp(rm.UpdatedAt),
	}

	if rm.CompletedAt.Valid {
		session.CompletedAt = utils.ParseTimestamp(rm.CompletedAt.String)
	}

	if len(rm.Metadata) > 0 {
		var metadataMap map[string]interface{}
		if err := json.Unmarshal(rm.Metadata, &metadataMap); err == nil {
			if s, err := structpb.NewStruct(metadataMap); err == nil {
				session.Metadata = s
			}
		}
	}

	if rm.Summary.Valid && rm.Summary.String != "" {
		session.Summary = rm.Summary.String
	}
	if rm.RevisionID.Valid {
		session.RevisionId = rm.RevisionID.String
	}

	return session
}

func turnReadModelToProto(rm *agentsquery.AgentSessionTurnReadModel) *agentspb.AgentSessionTurn {
	turn := &agentspb.AgentSessionTurn{
		Id:                    rm.ID,
		SessionId:             rm.SessionID,
		TurnNumber:            rm.TurnNumber,
		Status:                stringToTurnStatus(rm.Status),
		PromptTokens:          rm.PromptTokens,
		CompletionTokens:      rm.CompletionTokens,
		TotalTokens:           rm.TotalTokens,
		CacheReadInputTokens:  rm.CacheReadInputTokens,
		CacheWriteInputTokens: rm.CacheWriteInputTokens,
		LatencyMs:             rm.LatencyMs,
		CreatedAt:             utils.ParseTimestamp(rm.CreatedAt),
	}

	if rm.UserInput.Valid {
		turn.UserInput = rm.UserInput.String
	}
	if rm.AssistantOutput.Valid {
		turn.AssistantOutput = rm.AssistantOutput.String
	}
	if rm.Error.Valid {
		turn.Error = rm.Error.String
	}
	if rm.CompletedAt.Valid {
		turn.CompletedAt = utils.ParseTimestamp(rm.CompletedAt.String)
	}
	if len(rm.ToolCalls) > 0 {
		turn.ToolCalls = string(rm.ToolCalls)
	}
	if len(rm.Timeline) > 0 {
		turn.Timeline = string(rm.Timeline)
	}

	return turn
}

func sessionStatusToString(status agentspb.SessionStatus) string {
	switch status {
	case agentspb.SessionStatus_SESSION_STATUS_CREATED:
		return "created"
	case agentspb.SessionStatus_SESSION_STATUS_RUNNING:
		return "running"
	case agentspb.SessionStatus_SESSION_STATUS_WAITING_FOR_INPUT:
		return "waiting_for_input"
	case agentspb.SessionStatus_SESSION_STATUS_WAITING_FOR_APPROVAL:
		return "waiting_for_approval"
	case agentspb.SessionStatus_SESSION_STATUS_COMPLETED:
		return "completed"
	case agentspb.SessionStatus_SESSION_STATUS_FAILED:
		return "failed"
	case agentspb.SessionStatus_SESSION_STATUS_CANCELLED:
		return "cancelled"
	default:
		return ""
	}
}

func stringToSessionStatus(s string) agentspb.SessionStatus {
	switch s {
	case "created":
		return agentspb.SessionStatus_SESSION_STATUS_CREATED
	case "running":
		return agentspb.SessionStatus_SESSION_STATUS_RUNNING
	case "waiting_for_input":
		return agentspb.SessionStatus_SESSION_STATUS_WAITING_FOR_INPUT
	case "waiting_for_approval":
		return agentspb.SessionStatus_SESSION_STATUS_WAITING_FOR_APPROVAL
	case "completed":
		return agentspb.SessionStatus_SESSION_STATUS_COMPLETED
	case "failed":
		return agentspb.SessionStatus_SESSION_STATUS_FAILED
	case "cancelled":
		return agentspb.SessionStatus_SESSION_STATUS_CANCELLED
	default:
		return agentspb.SessionStatus_SESSION_STATUS_UNSPECIFIED
	}
}

func stringToTurnStatus(s string) agentspb.TurnStatus {
	switch s {
	case "pending":
		return agentspb.TurnStatus_TURN_STATUS_PENDING
	case "running":
		return agentspb.TurnStatus_TURN_STATUS_RUNNING
	case "completed":
		return agentspb.TurnStatus_TURN_STATUS_COMPLETED
	case "failed":
		return agentspb.TurnStatus_TURN_STATUS_FAILED
	default:
		return agentspb.TurnStatus_TURN_STATUS_UNSPECIFIED
	}
}

func approvalStatusToString(status agentspb.ApprovalReviewStatus) string {
	switch status {
	case agentspb.ApprovalReviewStatus_APPROVAL_REVIEW_STATUS_PENDING:
		return "pending"
	case agentspb.ApprovalReviewStatus_APPROVAL_REVIEW_STATUS_APPROVED:
		return "approved"
	case agentspb.ApprovalReviewStatus_APPROVAL_REVIEW_STATUS_DENIED:
		return "denied"
	case agentspb.ApprovalReviewStatus_APPROVAL_REVIEW_STATUS_EXPIRED:
		return "expired"
	case agentspb.ApprovalReviewStatus_APPROVAL_REVIEW_STATUS_CANCELLED:
		return "cancelled"
	default:
		return ""
	}
}

func stringToApprovalStatus(s string) agentspb.ApprovalReviewStatus {
	switch s {
	case "pending":
		return agentspb.ApprovalReviewStatus_APPROVAL_REVIEW_STATUS_PENDING
	case "approved":
		return agentspb.ApprovalReviewStatus_APPROVAL_REVIEW_STATUS_APPROVED
	case "denied":
		return agentspb.ApprovalReviewStatus_APPROVAL_REVIEW_STATUS_DENIED
	case "expired":
		return agentspb.ApprovalReviewStatus_APPROVAL_REVIEW_STATUS_EXPIRED
	case "cancelled":
		return agentspb.ApprovalReviewStatus_APPROVAL_REVIEW_STATUS_CANCELLED
	default:
		return agentspb.ApprovalReviewStatus_APPROVAL_REVIEW_STATUS_UNSPECIFIED
	}
}

// ============================================================================
// Sandbox Management
// ============================================================================

func (s *Server) GetSandboxOverview(ctx context.Context, req *connect.Request[agentspb.GetSandboxOverviewRequest]) (*connect.Response[agentspb.GetSandboxOverviewResponse], error) {
	// Belt-and-suspenders RPC ceiling. The FE polls this every 5s; if a
	// single call runs longer than that, callers pile up. Individual
	// subroutines below are bounded too, but a top-level cap means no
	// future unbounded path can wedge the whole RPC.
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	// The overview is a customer/instance surface. Treating it as a global
	// health check leaked other tenants' sandboxes and the process-wide host
	// ceiling into the Capacity card.
	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	if s.sandboxMgr == nil {
		return connect.NewResponse(&agentspb.GetSandboxOverviewResponse{
			Overview: &agentspb.SandboxOverview{
				Healthy: false,
			},
		}), nil
	}

	instances := s.sandboxMgr.ListInstancesForTenant(tenantID)
	config := s.sandboxMgr.GlobalConfig()

	running := 0
	for _, inst := range instances {
		if sandboxInstanceIsRunning(inst) {
			running++
		}
	}

	// Bound the health check — a wedged firecracker-agent (or unreachable
	// docker daemon) was hanging this RPC indefinitely, which read in the
	// admin UI as "Sandbox runtime not available" because the React Query
	// for GetSandboxOverview never resolved. 3s is plenty for a local
	// daemon ping; treat anything slower as unhealthy.
	healthCtx, healthCancel := context.WithTimeout(ctx, 3*time.Second)
	healthy := s.sandboxMgr.Healthy(healthCtx) == nil
	healthCancel()

	overview := &agentspb.SandboxOverview{
		TotalInstances:   int32(len(instances)),
		RunningInstances: int32(running),
		MaxSandboxes:     int32(s.sandboxMgr.ConcurrentSandboxLimit(tenantID)),
		Backend:          publicBackendLabel(s.sandboxMgr.BackendName()),
		MaxCpu:           config.MaxCPU,
		MaxMemoryMb:      config.MaxMemoryMB,
		Healthy:          healthy,
	}

	// Aggregate resource stats across all running instances.
	if running > 0 {
		agg := s.sandboxMgr.AggregateStatsForTenant(ctx, tenantID)
		overview.AggregateCpuPercent = agg.CPUPercent
		overview.AggregateMemoryUsage = agg.MemoryUsage
		overview.AggregateMemoryLimit = agg.MemoryLimit
		overview.AggregateMemoryPercent = agg.MemoryPercent
		overview.AggregateNetworkRxBytes = agg.NetworkRxBytes
		overview.AggregateNetworkTxBytes = agg.NetworkTxBytes
		overview.AggregateBlockRead = agg.BlockRead
		overview.AggregateBlockWrite = agg.BlockWrite
		overview.AggregatePids = int32(agg.PIDs)
	}

	// Execution metrics from the database. Filtered by tenant_id and
	// covered by idx_sandbox_executions_tenant_created from migration
	// sandbox_executions_tenant_id_20260501163934. Without that filter
	// this was a full-table scan on a globally-shared table that hung
	// the overview RPC. The 2s timeout is kept as a defense-in-depth
	// belt-and-suspenders; with the index the typical run is <50ms.
	//
	// When tenantID is empty (server-side fallback didn't resolve a
	// tenant), skip the query rather than scan the whole table — the
	// FE doesn't currently render these fields anyway.
	if s.db != nil && tenantID != "" {
		statsCtx, statsCancel := context.WithTimeout(ctx, 2*time.Second)
		var execStats struct {
			Total int     `db:"total"`
			AvgMs float64 `db:"avg_ms"`
		}
		if err := s.db.GetContext(statsCtx, &execStats, `
			SELECT COALESCE(COUNT(*), 0) AS total,
			       COALESCE(AVG(duration_ms), 0) AS avg_ms
			FROM sandbox_executions
			WHERE tenant_id = $1
		`, tenantID); err == nil {
			overview.TotalExecutions = int32(execStats.Total)
			overview.AvgExecutionDurationMs = execStats.AvgMs
		}
		statsCancel()

		// Lifetime billing aggregate.
		//
		// "Cost so far" must satisfy three properties:
		//   1. Never rewind when a sandbox terminates — terminated time
		//      stays charged.
		//   2. Never undercount currently-running sandboxes whose
		//      ledger row hasn't been written yet (usage records are
		//      written at lifecycle events, not continuously).
		//   3. Not jump at termination — the live estimate for an
		//      in-flight sandbox must equal the ledger row it eventually
		//      becomes.
		//
		// Property 1 comes from reading sandbox_usage_records, which is
		// the immutable ledger that also feeds Stripe meters.
		// Properties 2 and 3 come from adding the sandbox manager's
		// LiveAccruedCost — running instances priced through the same
		// computeSandboxCost helper that the ledger writer uses.
		//
		// SUM covered by idx_sandbox_usage_records_tenant_created
		// (tenant_id, period_end DESC); leading tenant_id column means
		// the aggregate scans only this tenant's rows.
		billingCtx, billingCancel := context.WithTimeout(ctx, 2*time.Second)
		var billingStats struct {
			TotalCostUSD     float64 `db:"total_cost_usd"`
			TotalDurationSec int64   `db:"total_duration_sec"`
		}
		if err := s.db.GetContext(billingCtx, &billingStats, `
			SELECT COALESCE(SUM(cost_total_usd), 0) AS total_cost_usd,
			       COALESCE(SUM(duration_seconds), 0) AS total_duration_sec
			FROM sandbox_usage_records
			WHERE tenant_id = $1
		`, tenantID); err != nil {
			logger.WithFields("tenant_id", tenantID, "error", err.Error()).
				Warn("sandbox-billing: usage_records query failed; live accrual still computed")
		}
		billingCancel()

		liveCost, liveSecs := s.sandboxMgr.LiveAccruedCost(ctx, tenantID)
		overview.LifetimeCostUsd = billingStats.TotalCostUSD + liveCost
		overview.LifetimeComputeSeconds = billingStats.TotalDurationSec + liveSecs
		overview.ActiveCostUsd = liveCost
		overview.ActiveComputeSeconds = liveSecs
	}

	return connect.NewResponse(&agentspb.GetSandboxOverviewResponse{
		Overview: overview,
	}), nil
}

func (s *Server) ListSandboxInstances(ctx context.Context, req *connect.Request[agentspb.ListSandboxInstancesRequest]) (*connect.Response[agentspb.ListSandboxInstancesResponse], error) {
	// Belt-and-suspenders RPC ceiling — see GetSandboxOverview for rationale.
	// 4s is generous given the inner query is bounded at 3s.
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	// Reconciler path: pure SELECT, no merge with in-memory state.
	// The DB is the source of truth for lifecycle. State changes flow
	// from the reconciler tick → UPDATE → NOTIFY → SSE; this list
	// query is just for the FE's initial paint and the safety-net
	// 30s refresh.
	if s.LifecycleRepoEnabled() {
		return s.listSandboxInstancesViaRepo(ctx, tenantID, req.Msg)
	}

	// Return live in-memory instances from the sandbox manager
	var protoInstances []*agentspb.SandboxInstance
	if s.sandboxMgr != nil {
		instances := s.sandboxMgr.ListInstances()
		logger.WithFields(
			"tenant_id", tenantID,
			"total_instances", len(instances),
		).Debug("sandbox: listing instances")
		for _, inst := range instances {
			logger.WithFields(
				"sandbox_id", inst.ID,
				"session_id", inst.Config.SessionID,
				"inst_tenant_id", inst.Config.TenantID,
				"filter_tenant_id", tenantID,
				"status", inst.Status,
			).Debug("sandbox: checking instance")
			if inst.Config.TenantID != "" && inst.Config.TenantID != tenantID {
				continue
			}
			pb := sandboxInstanceToProto(inst)
			periodEnd := time.Now()
			if !inst.BillingEndedAt.IsZero() {
				periodEnd = inst.BillingEndedAt
			}
			s.populateSandboxBillingSnapshot(ctx, pb, inst.Config, inst.Config.TenantID, inst.BillingStartedAt, periodEnd)
			protoInstances = append(protoInstances, pb)
		}
	} else {
		logger.Debug("sandbox: sandboxMgr is nil")
	}

	// Also try to fetch from DB for historical instances
	sys, sysErr := cqrs.GetSystemFromContext(ctx)
	if sysErr != nil && s.ctx != nil {
		sys, sysErr = cqrs.GetSystemFromContext(s.ctx)
	}
	if sysErr == nil {
		var status *string
		if req.Msg.GetStatus() != agentspb.SandboxStatus_SANDBOX_STATUS_UNSPECIFIED {
			s := sandboxStatusToString(req.Msg.GetStatus())
			status = &s
		}
		q := agentsquery.NewListSandboxInstancesQuery(tenantID, status, int(req.Msg.GetLimit()), int(req.Msg.GetOffset()))
		if res, err := sys.QueryBus.Execute(ctx, q); err == nil && res != nil {
			var data interface{} = res
			if resp, ok := res.(*query.Response); ok {
				data = resp.Data
			}
			if result, ok := data.(*agentsquery.SandboxInstancesResult); ok {
				// Build a set of live sandbox IDs for quick lookup
				liveIDs := make(map[string]struct{}, len(protoInstances))
				for _, live := range protoInstances {
					liveIDs[live.Id] = struct{}{}
				}

				for i := range result.Instances {
					rm := &result.Instances[i]
					// Skip if already represented by a live instance
					if _, isLive := liveIDs[rm.ID]; isLive {
						continue
					}
					// IMPORTANT: do NOT mutate DB rows from this read path
					// just because they're missing from m.instances. Every
					// gateway pod restart empties the in-memory map; until
					// restoreInstances finishes (~30s after backend
					// discovery converges) every row looks "stale", and
					// the previous version of this code aggressively
					// flipped them to status='stopped'. That's exactly
					// what was causing the "rollout kills my sandboxes"
					// bug — the read path was destroying state owned by
					// fcagent.
					//
					// Source-of-truth ordering for live state is:
					//   1. fcagent (the actual VM is alive there)
					//   2. DB row (gateway's recorded last-known state)
					//   3. in-memory map (gateway's local cache, lost on
					//      restart)
					// The DB row already reflects 'running' — return it
					// as-is. If the VM is genuinely dead, the next Stop/
					// Exec/Shell hits fcagent and surfaces the error.
					// The reaper's targeted reconciliation (which knows
					// to consult fcagent before flipping state) handles
					// truly stale rows.
					// Async sandbox creation used to persist a pending row before
					// backend.Create completed. If the gateway restarted, the agent
					// call hung, or the detached goroutine died, that DB-only row has
					// no live backend instance to reconcile against and the UI polls
					// it forever. Treat old DB-only pending rows as failed so users
					// see a terminal state and can retry.
					if sandboxReadModelIsStalePending(rm, 10*time.Minute) {
						rm.Status = "failed"
						rm.LifecycleState.Valid = true
						rm.LifecycleState.String = sandbox.LifecycleFailed
						rm.Error.Valid = true
						rm.Error.String = "sandbox creation did not complete before the startup timeout"
						if s.db != nil {
							go func(sandboxID string) {
								const uq = `
									UPDATE sandbox_instances
									SET status = 'failed', lifecycle_state = 'failed',
									    error = COALESCE(NULLIF(error, ''), 'sandbox creation did not complete before the startup timeout'),
									    updated_at = NOW()
									WHERE id = $1 AND status = 'pending'
									  AND COALESCE(NULLIF(lifecycle_state, ''), 'pending') IN ('pending', 'creating', 'provisioning')`
								dbCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
								defer cancel()
								if _, err := s.db.ExecContext(dbCtx, uq, sandboxID); err != nil {
									logger.WithFields("sandbox_id", sandboxID, "error", err.Error()).
										Warn("sandbox: failed to mark stale pending instance as failed")
								}
							}(rm.ID)
						}
					}
					pb := sandboxInstanceReadModelToProto(rm)
					if rm.BillingStartedAt.Valid {
						var cfg sandbox.InstanceConfig
						if err := json.Unmarshal(rm.Config, &cfg); err == nil {
							periodEnd := time.Now()
							if rm.BillingEndedAt.Valid {
								periodEnd = rm.BillingEndedAt.Time
							}
							s.populateSandboxBillingSnapshot(ctx, pb, cfg, rm.TenantID, rm.BillingStartedAt.Time, periodEnd)
						}
					}
					protoInstances = append(protoInstances, pb)
				}
			}
		}
	}

	return connect.NewResponse(&agentspb.ListSandboxInstancesResponse{
		Instances: protoInstances,
		Total:     int32(len(protoInstances)),
	}), nil
}

// listSandboxInstancesViaRepo serves ListSandboxInstances from the
// lifecycle repository — pure SELECT, no in-memory merge, no sweepers.
// The reconciler keeps the DB row in sync with backend state; this
// query just reads what's there.
func (s *Server) listSandboxInstancesViaRepo(
	ctx context.Context,
	tenantID string,
	msg *agentspb.ListSandboxInstancesRequest,
) (*connect.Response[agentspb.ListSandboxInstancesResponse], error) {
	limit := int(msg.GetLimit())
	offset := int(msg.GetOffset())
	rows, total, err := s.lifecycleRepo.ListByTenantFiltered(ctx, tenantID, limit, offset, msg.GetLabelFilter())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoInstances := make([]*agentspb.SandboxInstance, 0, len(rows))
	now := time.Now()
	for i := range rows {
		row := &rows[i]
		pb := lifecycleRowToProto(row)
		if row.BillingStartedAt.Valid {
			var cfg sandbox.InstanceConfig
			if err := json.Unmarshal(row.Config, &cfg); err == nil {
				periodEnd := now
				if row.BillingEndedAt.Valid {
					periodEnd = row.BillingEndedAt.Time
				}
				s.populateSandboxBillingSnapshot(ctx, pb, cfg, row.TenantID, row.BillingStartedAt.Time, periodEnd)
			}
		}
		protoInstances = append(protoInstances, pb)
	}
	return connect.NewResponse(&agentspb.ListSandboxInstancesResponse{
		Instances: protoInstances,
		Total:     int32(total),
	}), nil
}

// lifecycleRowToProto maps a sandboxlc.Row to the protobuf wire type.
// Mirror sandboxInstanceReadModelToProto's field selection so the FE
// sees identical responses regardless of which path served the read.
func lifecycleRowToProto(row *sandboxlc.Row) *agentspb.SandboxInstance {
	pb := &agentspb.SandboxInstance{
		Id:             row.ID,
		SessionId:      row.SessionID,
		TenantId:       row.TenantID,
		Backend:        publicBackendLabel(row.Backend),
		ContainerId:    row.ContainerID.String,
		Image:          row.Image,
		Status:         sandboxStatusFromString(row.Status),
		CreatedAt:      timestamppb.New(row.CreatedAt),
		Name:           row.Name,
		LifecycleState: sandbox.PublicLifecycleState(row.LifecycleState, sandbox.Status(row.Status)),
		// agent_healthy is a real-time signal, not persisted in the
		// DB. DB-row reads are typically used for listing terminated
		// sandboxes where in-guest health is irrelevant; we report
		// true (healthy/unknown) so the UI dot doesn't go red on a
		// row that hasn't been refreshed from the live backend.
		AgentHealthy: true,
	}
	if row.AgentID.Valid {
		pb.AgentId = row.AgentID.String
	}
	if row.ShortCode.Valid {
		pb.ShortCode = row.ShortCode.String
	}
	if len(row.Labels) > 0 && string(row.Labels) != "{}" {
		var labels map[string]string
		if err := json.Unmarshal(row.Labels, &labels); err == nil {
			pb.Labels = labels
		}
	}
	pb.AutoArchiveAfterDays = int32(row.AutoArchiveAfterDays)
	pb.AutoDeleteAfterDays = int32(row.AutoDeleteAfterDays)
	if row.ArchivedAt.Valid {
		pb.ArchivedAt = timestamppb.New(row.ArchivedAt.Time)
	}
	if row.BillingStartedAt.Valid {
		pb.BillingStartedAt = timestamppb.New(row.BillingStartedAt.Time)
	}
	if row.BillingEndedAt.Valid {
		pb.BillingEndedAt = timestamppb.New(row.BillingEndedAt.Time)
	}
	if len(row.Config) > 0 {
		var configMap map[string]interface{}
		if err := json.Unmarshal(row.Config, &configMap); err == nil {
			if cfg, err := structpb.NewStruct(configMap); err == nil {
				pb.Config = cfg
			}
		}
	}

	// Daytona-style public surface: clients branch on `state` (label
	// vocabulary) and the minute intervals; lifecycle_state and the
	// day-granularity fields above remain for backward compatibility.
	pb.State = sandboxlc.PublicState(row.LifecycleState)
	pb.DesiredState = row.DesiredState
	if row.ErrorReason.Valid {
		pb.ErrorReason = row.ErrorReason.String
	}
	if row.AutoStopMinutes.Valid {
		pb.AutoStopInterval = int32(row.AutoStopMinutes.Int64)
	}
	if row.AutoArchiveMinutes.Valid {
		pb.AutoArchiveInterval = int32(row.AutoArchiveMinutes.Int64)
	} else if row.AutoArchiveAfterDays > 0 {
		pb.AutoArchiveInterval = int32(row.AutoArchiveAfterDays) * 1440
	}
	if row.AutoDeleteMinutes.Valid {
		pb.AutoDeleteInterval = int32(row.AutoDeleteMinutes.Int64)
	} else if row.AutoDeleteAfterDays >= 0 {
		pb.AutoDeleteInterval = int32(row.AutoDeleteAfterDays) * 1440
	} else {
		pb.AutoDeleteInterval = -1
	}
	return pb
}

func sandboxStatusFromString(s string) agentspb.SandboxStatus {
	switch s {
	case "pending", "creating", "provisioning", "stopping", "reviving", "restoring":
		return agentspb.SandboxStatus_SANDBOX_STATUS_PENDING
	case "running":
		return agentspb.SandboxStatus_SANDBOX_STATUS_RUNNING
	case "stopped", "sleeping", "archiving", "archived", "terminating", "terminated", "deleting", "deleted":
		return agentspb.SandboxStatus_SANDBOX_STATUS_STOPPED
	// 'error' is the recoverable failure state the reconciler writes to
	// the status column on terminal convergence failure / VM death. It
	// MUST map to a non-zero enum: SANDBOX_STATUS_UNSPECIFIED (0) is
	// omitted by proto-JSON, which surfaced as status===undefined on the
	// client and crashed the instances list
	// (normalizeStatus(undefined).startsWith).
	case "failed", "error":
		return agentspb.SandboxStatus_SANDBOX_STATUS_FAILED
	}
	return agentspb.SandboxStatus_SANDBOX_STATUS_UNSPECIFIED
}

func (s *Server) GetSandboxInstance(ctx context.Context, req *connect.Request[agentspb.GetSandboxInstanceRequest]) (*connect.Response[agentspb.GetSandboxInstanceResponse], error) {
	_, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("sandbox instance not found"))
	}

	id := req.Msg.GetSandboxId()

	// Live in-memory cache first.
	for _, inst := range s.sandboxMgr.ListInstances() {
		if inst.ID == id {
			return connect.NewResponse(&agentspb.GetSandboxInstanceResponse{
				Instance: sandboxInstanceToProto(inst),
			}), nil
		}
	}

	// Cache miss → DB fallback. Without this every existing sandbox
	// 404s on its detail card during the gateway's post-rollout
	// cold-start window. Single index hit; matches id, name, or
	// short_code.
	dbCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if dbInst, dbErr := s.sandboxMgr.LookupInstanceByIDFromDB(dbCtx, id); dbErr == nil && dbInst != nil {
		return connect.NewResponse(&agentspb.GetSandboxInstanceResponse{
			Instance: sandboxInstanceToProto(dbInst),
		}), nil
	}

	return nil, connect.NewError(connect.CodeNotFound, errors.New("sandbox instance not found"))
}

func (s *Server) DestroySandbox(ctx context.Context, req *connect.Request[agentspb.DestroySandboxRequest]) (*connect.Response[agentspb.DestroySandboxResponse], error) {
	if _, err := s.resolveTenantID(ctx, req.Msg.GetTenantId()); err != nil {
		return nil, err
	}

	result, err := sandboxcp.NewLifecycleService(s.lifecycleRepo, s.sandboxMgr).Destroy(ctx, req.Msg.GetSessionId())
	if err != nil {
		if errors.Is(err, sandboxcp.ErrLifecycleSessionIDRequired) {
			return nil, connect.NewError(connect.CodeInvalidArgument, err)
		}
		return nil, mapLifecycleError(err)
	}

	return connect.NewResponse(&agentspb.DestroySandboxResponse{
		Success: result.Success,
		Message: result.Message,
	}), nil
}

func (s *Server) ListSandboxExecutions(ctx context.Context, req *connect.Request[agentspb.ListSandboxExecutionsRequest]) (*connect.Response[agentspb.ListSandboxExecutionsResponse], error) {
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

	q := agentsquery.NewListSandboxExecutionsQuery(tenantID, req.Msg.GetSandboxId(), int(req.Msg.GetLimit()), int(req.Msg.GetOffset()))
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &agentspb.ListSandboxExecutionsResponse{}
	if res != nil {
		var data interface{} = res
		if qResp, ok := res.(*query.Response); ok {
			data = qResp.Data
		}
		if result, ok := data.(*agentsquery.SandboxExecutionsResult); ok {
			resp.Total = int32(result.Total)
			for i := range result.Executions {
				resp.Executions = append(resp.Executions, sandboxExecutionToProto(&result.Executions[i]))
			}
		}
	}

	return connect.NewResponse(resp), nil
}

func (s *Server) GetSandboxStats(ctx context.Context, req *connect.Request[agentspb.GetSandboxStatsRequest]) (*connect.Response[agentspb.GetSandboxStatsResponse], error) {
	_, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("sandbox feature is not enabled"))
	}

	stats, err := s.sandboxMgr.Stats(ctx, req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.GetSandboxStatsResponse{
		Stats: &agentspb.SandboxStats{
			CpuPercent:     stats.CPUPercent,
			MemoryUsage:    stats.MemoryUsage,
			MemoryLimit:    stats.MemoryLimit,
			MemoryPercent:  stats.MemoryPercent,
			NetworkRxBytes: stats.NetworkRxBytes,
			NetworkTxBytes: stats.NetworkTxBytes,
			BlockRead:      stats.BlockRead,
			BlockWrite:     stats.BlockWrite,
			Pids:           int32(stats.PIDs),
		},
	}), nil
}

// ============================================================================
// Sandbox Events
// ============================================================================

func (s *Server) ListSandboxEvents(ctx context.Context, req *connect.Request[agentspb.ListSandboxEventsRequest]) (*connect.Response[agentspb.ListSandboxEventsResponse], error) {
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

	var eventType *string
	if req.Msg.EventType != nil {
		et := req.Msg.GetEventType()
		eventType = &et
	}

	q := agentsquery.NewListSandboxEventsQuery(tenantID, req.Msg.GetSandboxId(), eventType, int(req.Msg.GetLimit()), int(req.Msg.GetOffset()))
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &agentspb.ListSandboxEventsResponse{}
	if res != nil {
		var data interface{} = res
		if qResp, ok := res.(*query.Response); ok {
			data = qResp.Data
		}
		if result, ok := data.(*agentsquery.SandboxEventsResult); ok {
			resp.Total = int32(result.Total)
			for i := range result.Events {
				resp.Events = append(resp.Events, sandboxEventToProto(&result.Events[i]))
			}
		}
	}

	return connect.NewResponse(resp), nil
}

func sandboxEventToProto(e *agentsquery.SandboxEventReadModel) *agentspb.SandboxEvent {
	return &agentspb.SandboxEvent{
		Id:         e.ID,
		SandboxId:  e.SandboxID,
		SessionId:  e.SessionID,
		TenantId:   e.TenantID,
		EventType:  e.EventType,
		Message:    e.Message.String,
		DurationMs: e.DurationMs.Int64,
		Error:      e.Error.String,
		CreatedAt:  utils.ParseTimestampProto(e.CreatedAt),
	}
}

// ============================================================================
// Port Exposure
// ============================================================================

func (s *Server) ExposePort(ctx context.Context, req *connect.Request[agentspb.ExposePortRequest]) (*connect.Response[agentspb.ExposePortResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("sandbox feature is not enabled"))
	}

	_, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	mapping, err := s.sandboxMgr.ExposePort(ctx, req.Msg.GetSessionId(), int(req.Msg.GetPort()), req.Msg.GetProtocol())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.ExposePortResponse{
		Mapping: &agentspb.SandboxPortMapping{
			Id:        mapping.ID,
			SandboxId: mapping.SandboxID,
			Port:      int32(mapping.Port),
			Protocol:  mapping.Protocol,
			Subdomain: mapping.Subdomain,
			Url:       s.buildPortURL(mapping.Subdomain, mapping.SandboxID, mapping.Port),
			Status:    mapping.Status,
			CreatedAt: timestamppb.New(mapping.CreatedAt),
		},
	}), nil
}

func (s *Server) UnexposePort(ctx context.Context, req *connect.Request[agentspb.UnexposePortRequest]) (*connect.Response[agentspb.UnexposePortResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("sandbox feature is not enabled"))
	}

	_, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	if err := s.sandboxMgr.UnexposePort(ctx, req.Msg.GetSessionId(), int(req.Msg.GetPort())); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.UnexposePortResponse{
		Success: true,
		Message: "Port unexposed successfully",
	}), nil
}

func (s *Server) ListExposedPorts(ctx context.Context, req *connect.Request[agentspb.ListExposedPortsRequest]) (*connect.Response[agentspb.ListExposedPortsResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("sandbox feature is not enabled"))
	}

	_, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	mappings, err := s.sandboxMgr.ListExposedPorts(ctx, req.Msg.GetSessionId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &agentspb.ListExposedPortsResponse{}
	for _, m := range mappings {
		resp.Ports = append(resp.Ports, &agentspb.SandboxPortMapping{
			Id:        m.ID,
			SandboxId: m.SandboxID,
			Port:      int32(m.Port),
			Protocol:  m.Protocol,
			Subdomain: m.Subdomain,
			Url:       s.buildPortURL(m.Subdomain, m.SandboxID, m.Port),
			Status:    m.Status,
			CreatedAt: timestamppb.New(m.CreatedAt),
		})
	}

	return connect.NewResponse(resp), nil
}

func (s *Server) DetectListeningPorts(ctx context.Context, req *connect.Request[agentspb.DetectListeningPortsRequest]) (*connect.Response[agentspb.DetectListeningPortsResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("sandbox feature is not enabled"))
	}

	_, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	sessionID := req.Msg.GetSessionId()

	detected, err := s.sandboxMgr.DetectListeningPorts(ctx, sessionID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Cross-reference with exposed ports to set is_exposed flag
	exposed, _ := s.sandboxMgr.ListExposedPorts(ctx, sessionID)
	exposedSet := make(map[int]bool, len(exposed))
	for _, m := range exposed {
		exposedSet[m.Port] = true
	}

	resp := &agentspb.DetectListeningPortsResponse{}
	for _, lp := range detected {
		resp.Ports = append(resp.Ports, &agentspb.DetectedPort{
			Port:      int32(lp.Port),
			Protocol:  lp.Protocol,
			Address:   lp.Address,
			Pid:       int32(lp.PID),
			Process:   lp.Process,
			IsExposed: exposedSet[lp.Port],
		})
	}

	return connect.NewResponse(resp), nil
}

// ============================================================================
// Crons
// ============================================================================

func (s *Server) CreateCron(ctx context.Context, req *connect.Request[agentspb.CreateCronRequest]) (*connect.Response[agentspb.CreateCronResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("sandbox feature is not enabled"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	db := s.sandboxMgr.DB()
	if db == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("database not available"))
	}

	var sandboxConfig []byte
	if req.Msg.GetSandboxConfig() != nil {
		sandboxConfig, _ = json.Marshal(req.Msg.GetSandboxConfig().AsMap())
	}
	if sandboxConfig == nil {
		sandboxConfig = []byte("{}")
	}

	workDir := req.Msg.GetWorkDir()
	if workDir == "" {
		workDir = "/workspace"
	}
	timeoutSecs := req.Msg.GetTimeoutSeconds()
	if timeoutSecs <= 0 {
		timeoutSecs = 300
	}

	const q = `
		INSERT INTO sandbox_crons
			(tenant_id, sandbox_id, session_id, name, schedule, command, work_dir, timeout_seconds, auto_recreate, sandbox_config)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING id, created_at`

	var row struct {
		ID        int64  `db:"id"`
		CreatedAt string `db:"created_at"`
	}
	if err := db.GetContext(ctx, &row, q,
		tenantID, req.Msg.GetSandboxId(), req.Msg.GetSessionId(),
		req.Msg.GetName(), req.Msg.GetSchedule(), req.Msg.GetCommand(),
		workDir, timeoutSecs, req.Msg.GetAutoRecreate(), sandboxConfig,
	); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create cron: %w", err))
	}

	return connect.NewResponse(&agentspb.CreateCronResponse{
		Cron: &agentspb.SandboxCron{
			Id:             row.ID,
			TenantId:       tenantID,
			SandboxId:      req.Msg.GetSandboxId(),
			SessionId:      req.Msg.GetSessionId(),
			Name:           req.Msg.GetName(),
			Schedule:       req.Msg.GetSchedule(),
			Command:        req.Msg.GetCommand(),
			WorkDir:        workDir,
			TimeoutSeconds: timeoutSecs,
			Enabled:        true,
			AutoRecreate:   req.Msg.GetAutoRecreate(),
		},
	}), nil
}

// sandboxCronToProto converts a SandboxCronReadModel to its protobuf representation.
func sandboxCronToProto(c agentsquery.SandboxCronReadModel) *agentspb.SandboxCron {
	pb := &agentspb.SandboxCron{
		Id:             c.ID,
		TenantId:       c.TenantID,
		SandboxId:      c.SandboxID,
		SessionId:      c.SessionID,
		Name:           c.Name,
		Schedule:       c.Schedule,
		Command:        c.Command,
		WorkDir:        c.WorkDir,
		TimeoutSeconds: int32(c.TimeoutSeconds),
		Enabled:        c.Enabled,
		AutoRecreate:   c.AutoRecreate,
		RunCount:       int32(c.RunCount),
		ErrorCount:     int32(c.ErrorCount),
	}
	if c.LastRunAt.Valid {
		if t, err := time.Parse(time.RFC3339Nano, c.LastRunAt.String); err == nil {
			pb.LastRunAt = timestamppb.New(t)
		}
	}
	if c.NextRunAt.Valid {
		if t, err := time.Parse(time.RFC3339Nano, c.NextRunAt.String); err == nil {
			pb.NextRunAt = timestamppb.New(t)
		}
	}
	if c.LastError.Valid {
		pb.LastError = c.LastError.String
	}
	return pb
}

func (s *Server) UpdateCron(ctx context.Context, req *connect.Request[agentspb.UpdateCronRequest]) (*connect.Response[agentspb.UpdateCronResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("sandbox feature is not enabled"))
	}

	if _, err := s.resolveTenantID(ctx, req.Msg.GetTenantId()); err != nil {
		return nil, err
	}

	db := s.sandboxMgr.DB()
	if db == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("database not available"))
	}

	// Build dynamic update
	sets := []string{"updated_at = NOW()"}
	args := []interface{}{req.Msg.GetId()}
	argIdx := 2

	if req.Msg.Name != nil {
		sets = append(sets, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, req.Msg.GetName())
		argIdx++
	}
	if req.Msg.Schedule != nil {
		sets = append(sets, fmt.Sprintf("schedule = $%d", argIdx))
		args = append(args, req.Msg.GetSchedule())
		argIdx++
	}
	if req.Msg.Command != nil {
		sets = append(sets, fmt.Sprintf("command = $%d", argIdx))
		args = append(args, req.Msg.GetCommand())
		argIdx++
	}
	if req.Msg.Enabled != nil {
		sets = append(sets, fmt.Sprintf("enabled = $%d", argIdx))
		args = append(args, req.Msg.GetEnabled())
		argIdx++
	}
	if req.Msg.TimeoutSeconds != nil {
		sets = append(sets, fmt.Sprintf("timeout_seconds = $%d", argIdx))
		args = append(args, req.Msg.GetTimeoutSeconds())
		argIdx++
	}

	queryStr := fmt.Sprintf("UPDATE sandbox_crons SET %s WHERE id = $1", strings.Join(sets, ", "))
	if _, err := db.ExecContext(ctx, queryStr, args...); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to update cron: %w", err))
	}

	// Fetch updated row
	var c agentsquery.SandboxCronReadModel
	if err := db.GetContext(ctx, &c, `SELECT * FROM sandbox_crons WHERE id = $1`, req.Msg.GetId()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to fetch updated cron: %w", err))
	}

	return connect.NewResponse(&agentspb.UpdateCronResponse{
		Cron: sandboxCronToProto(c),
	}), nil
}

func (s *Server) DeleteCron(ctx context.Context, req *connect.Request[agentspb.DeleteCronRequest]) (*connect.Response[agentspb.DeleteCronResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("sandbox feature is not enabled"))
	}

	if _, err := s.resolveTenantID(ctx, req.Msg.GetTenantId()); err != nil {
		return nil, err
	}

	db := s.sandboxMgr.DB()
	if db == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("database not available"))
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM sandbox_crons WHERE id = $1`, req.Msg.GetId()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to delete cron: %w", err))
	}

	return connect.NewResponse(&agentspb.DeleteCronResponse{Success: true, Message: "Cron deleted"}), nil
}

func (s *Server) ListCrons(ctx context.Context, req *connect.Request[agentspb.ListCronsRequest]) (*connect.Response[agentspb.ListCronsResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("sandbox feature is not enabled"))
	}

	if _, err := s.resolveTenantID(ctx, req.Msg.GetTenantId()); err != nil {
		return nil, err
	}

	db := s.sandboxMgr.DB()
	if db == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("database not available"))
	}

	whereClause := `WHERE 1=1`
	countArgs := []interface{}{}
	countArgIdx := 1

	if req.Msg.SandboxId != nil {
		whereClause += fmt.Sprintf(" AND sandbox_id = $%d", countArgIdx)
		countArgs = append(countArgs, req.Msg.GetSandboxId())
		countArgIdx++
	}

	// Get total count
	var total int32
	countQ := `SELECT COUNT(*) FROM sandbox_crons ` + whereClause
	if err := db.GetContext(ctx, &total, countQ, countArgs...); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to count crons: %w", err))
	}

	queryStr := `SELECT * FROM sandbox_crons ` + whereClause
	args := append([]interface{}{}, countArgs...)
	argIdx := countArgIdx

	queryStr += " ORDER BY created_at DESC"
	if req.Msg.GetLimit() > 0 {
		queryStr += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, req.Msg.GetLimit())
		argIdx++
	}
	if req.Msg.GetOffset() > 0 {
		queryStr += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, req.Msg.GetOffset())
	}

	var crons []agentsquery.SandboxCronReadModel
	if err := db.SelectContext(ctx, &crons, queryStr, args...); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list crons: %w", err))
	}

	resp := &agentspb.ListCronsResponse{Total: total}
	for _, c := range crons {
		resp.Crons = append(resp.Crons, sandboxCronToProto(c))
	}

	return connect.NewResponse(resp), nil
}

// ============================================================================
// Webhooks
// ============================================================================

func (s *Server) CreateWebhook(ctx context.Context, req *connect.Request[agentspb.CreateWebhookRequest]) (*connect.Response[agentspb.CreateWebhookResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("sandbox feature is not enabled"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	db := s.sandboxMgr.DB()
	if db == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("database not available"))
	}

	// Generate a random HMAC secret
	secret := utils.GenerateRandomString(32)

	path := req.Msg.GetPath()
	if path == "" {
		path = utils.GenerateRandomString(16)
	}

	var sandboxConfig []byte
	if req.Msg.GetSandboxConfig() != nil {
		sandboxConfig, _ = json.Marshal(req.Msg.GetSandboxConfig().AsMap())
	}
	if sandboxConfig == nil {
		sandboxConfig = []byte("{}")
	}

	workDir := req.Msg.GetWorkDir()
	if workDir == "" {
		workDir = "/workspace"
	}
	timeoutSecs := req.Msg.GetTimeoutSeconds()
	if timeoutSecs <= 0 {
		timeoutSecs = 300
	}
	rateLimitRPM := req.Msg.GetRateLimitRpm()
	if rateLimitRPM <= 0 {
		rateLimitRPM = 60
	}

	const q = `
		INSERT INTO sandbox_webhooks
			(tenant_id, sandbox_id, session_id, name, path, secret, command, work_dir, timeout_seconds, rate_limit_rpm, auto_recreate, sandbox_config)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		RETURNING id, created_at`

	var row struct {
		ID        int64  `db:"id"`
		CreatedAt string `db:"created_at"`
	}
	if err := db.GetContext(ctx, &row, q,
		tenantID, req.Msg.GetSandboxId(), req.Msg.GetSessionId(),
		req.Msg.GetName(), path, secret, req.Msg.GetCommand(),
		workDir, timeoutSecs, rateLimitRPM, req.Msg.GetAutoRecreate(), sandboxConfig,
	); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to create webhook: %w", err))
	}

	return connect.NewResponse(&agentspb.CreateWebhookResponse{
		Webhook: &agentspb.SandboxWebhookDef{
			Id:             row.ID,
			TenantId:       tenantID,
			SandboxId:      req.Msg.GetSandboxId(),
			SessionId:      req.Msg.GetSessionId(),
			Name:           req.Msg.GetName(),
			Path:           path,
			Url:            fmt.Sprintf("/wh/%s", path),
			Command:        req.Msg.GetCommand(),
			WorkDir:        workDir,
			TimeoutSeconds: timeoutSecs,
			Enabled:        true,
			RateLimitRpm:   rateLimitRPM,
			AutoRecreate:   req.Msg.GetAutoRecreate(),
		},
		Secret: secret,
	}), nil
}

func (s *Server) DeleteWebhook(ctx context.Context, req *connect.Request[agentspb.DeleteWebhookRequest]) (*connect.Response[agentspb.DeleteWebhookResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("sandbox feature is not enabled"))
	}

	if _, err := s.resolveTenantID(ctx, req.Msg.GetTenantId()); err != nil {
		return nil, err
	}

	db := s.sandboxMgr.DB()
	if db == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("database not available"))
	}

	if _, err := db.ExecContext(ctx, `DELETE FROM sandbox_webhooks WHERE id = $1`, req.Msg.GetId()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to delete webhook: %w", err))
	}

	return connect.NewResponse(&agentspb.DeleteWebhookResponse{Success: true, Message: "Webhook deleted"}), nil
}

func (s *Server) ListWebhooks(ctx context.Context, req *connect.Request[agentspb.ListWebhooksRequest]) (*connect.Response[agentspb.ListWebhooksResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("sandbox feature is not enabled"))
	}

	if _, err := s.resolveTenantID(ctx, req.Msg.GetTenantId()); err != nil {
		return nil, err
	}

	db := s.sandboxMgr.DB()
	if db == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("database not available"))
	}

	whereClause := `WHERE 1=1`
	countArgs := []interface{}{}
	countArgIdx := 1

	if req.Msg.SandboxId != nil {
		whereClause += fmt.Sprintf(" AND sandbox_id = $%d", countArgIdx)
		countArgs = append(countArgs, req.Msg.GetSandboxId())
		countArgIdx++
	}

	// Get total count
	var total int32
	countQ := `SELECT COUNT(*) FROM sandbox_webhooks ` + whereClause
	if err := db.GetContext(ctx, &total, countQ, countArgs...); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to count webhooks: %w", err))
	}

	queryStr := `SELECT * FROM sandbox_webhooks ` + whereClause
	args := append([]interface{}{}, countArgs...)
	argIdx := countArgIdx

	queryStr += " ORDER BY created_at DESC"
	if req.Msg.GetLimit() > 0 {
		queryStr += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, req.Msg.GetLimit())
		argIdx++
	}
	if req.Msg.GetOffset() > 0 {
		queryStr += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, req.Msg.GetOffset())
	}

	var webhooks []agentsquery.SandboxWebhookReadModel
	if err := db.SelectContext(ctx, &webhooks, queryStr, args...); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list webhooks: %w", err))
	}

	resp := &agentspb.ListWebhooksResponse{Total: total}
	for _, w := range webhooks {
		resp.Webhooks = append(resp.Webhooks, &agentspb.SandboxWebhookDef{
			Id:             w.ID,
			TenantId:       w.TenantID,
			SandboxId:      w.SandboxID,
			SessionId:      w.SessionID,
			Name:           w.Name,
			Path:           w.Path,
			Url:            fmt.Sprintf("/wh/%s", w.Path),
			Command:        w.Command,
			WorkDir:        w.WorkDir,
			TimeoutSeconds: int32(w.TimeoutSeconds),
			Enabled:        w.Enabled,
			RateLimitRpm:   int32(w.RateLimitRPM),
			TriggerCount:   int32(w.TriggerCount),
			ErrorCount:     int32(w.ErrorCount),
			AutoRecreate:   w.AutoRecreate,
		})
	}

	return connect.NewResponse(resp), nil
}

// ============================================================================
// Triggers
// ============================================================================

func (s *Server) ListTriggers(ctx context.Context, req *connect.Request[agentspb.ListTriggersRequest]) (*connect.Response[agentspb.ListTriggersResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("sandbox feature is not enabled"))
	}

	if _, err := s.resolveTenantID(ctx, req.Msg.GetTenantId()); err != nil {
		return nil, err
	}

	db := s.sandboxMgr.DB()
	if db == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("database not available"))
	}

	joinClause := `FROM sandbox_triggers st
		WHERE 1=1`
	countArgs := []interface{}{}
	countArgIdx := 1

	if req.Msg.SandboxId != nil {
		joinClause += fmt.Sprintf(" AND st.sandbox_id = $%d", countArgIdx)
		countArgs = append(countArgs, req.Msg.GetSandboxId())
		countArgIdx++
	}
	if req.Msg.TriggerType != nil {
		joinClause += fmt.Sprintf(" AND st.trigger_type = $%d", countArgIdx)
		countArgs = append(countArgs, req.Msg.GetTriggerType())
		countArgIdx++
	}

	// Get total count
	var total int32
	countQ := `SELECT COUNT(*) ` + joinClause
	if err := db.GetContext(ctx, &total, countQ, countArgs...); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to count triggers: %w", err))
	}

	queryStr := `SELECT st.* ` + joinClause
	args := append([]interface{}{}, countArgs...)
	argIdx := countArgIdx

	queryStr += " ORDER BY st.created_at DESC"
	if req.Msg.GetLimit() > 0 {
		queryStr += fmt.Sprintf(" LIMIT $%d", argIdx)
		args = append(args, req.Msg.GetLimit())
		argIdx++
	}
	if req.Msg.GetOffset() > 0 {
		queryStr += fmt.Sprintf(" OFFSET $%d", argIdx)
		args = append(args, req.Msg.GetOffset())
	}

	var triggers []agentsquery.SandboxTriggerReadModel
	if err := db.SelectContext(ctx, &triggers, queryStr, args...); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list triggers: %w", err))
	}

	resp := &agentspb.ListTriggersResponse{Total: total}
	for _, t := range triggers {
		resp.Triggers = append(resp.Triggers, &agentspb.SandboxTrigger{
			Id:            t.ID,
			TriggerType:   t.TriggerType,
			TriggerId:     t.TriggerID,
			SandboxId:     t.SandboxID,
			ExecutionId:   t.ExecutionID.String,
			Status:        t.Status,
			Error:         t.Error.String,
			DurationMs:    t.DurationMs.Int64,
			WebhookMethod: t.WebhookMethod.String,
			WebhookBody:   t.WebhookBody.String,
			CreatedAt:     utils.ParseTimestampProto(t.CreatedAt),
		})
	}

	return connect.NewResponse(resp), nil
}

// ============================================================================
// Spawn Tree
// ============================================================================

func (s *Server) GetSpawnTree(ctx context.Context, req *connect.Request[agentspb.GetSpawnTreeRequest]) (*connect.Response[agentspb.GetSpawnTreeResponse], error) {
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

	q := agentsquery.NewGetSpawnTreeQuery(tenantID, req.Msg.GetTreeId())
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &agentspb.GetSpawnTreeResponse{}
	if res != nil {
		var data interface{} = res
		if qResp, ok := res.(*query.Response); ok {
			data = qResp.Data
		}
		if result, ok := data.(*agentsquery.SpawnTreeResult); ok {
			resp.Total = int32(result.Total)
			for i := range result.Nodes {
				resp.Nodes = append(resp.Nodes, spawnNodeReadModelToProto(&result.Nodes[i]))
			}
		}
	}

	return connect.NewResponse(resp), nil
}

func (s *Server) ListSpawnNodes(ctx context.Context, req *connect.Request[agentspb.ListSpawnNodesRequest]) (*connect.Response[agentspb.ListSpawnNodesResponse], error) {
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

	q := agentsquery.NewListSpawnNodesQuery(tenantID, req.Msg.GetSessionId(), int(req.Msg.GetLimit()), int(req.Msg.GetOffset()))
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &agentspb.ListSpawnNodesResponse{}
	if res != nil {
		var data interface{} = res
		if qResp, ok := res.(*query.Response); ok {
			data = qResp.Data
		}
		if result, ok := data.(*agentsquery.SpawnTreeResult); ok {
			resp.Total = int32(result.Total)
			for i := range result.Nodes {
				resp.Nodes = append(resp.Nodes, spawnNodeReadModelToProto(&result.Nodes[i]))
			}
		}
	}

	return connect.NewResponse(resp), nil
}

func spawnNodeReadModelToProto(rm *agentsquery.SpawnTreeNodeReadModel) *agentspb.SpawnTreeNode {
	node := &agentspb.SpawnTreeNode{
		Id:               rm.ID,
		TreeId:           rm.TreeID,
		Depth:            rm.Depth,
		Status:           rm.Status,
		PromptTokens:     rm.PromptTokens,
		CompletionTokens: rm.CompletionTokens,
		TotalTokens:      rm.TotalTokens,
		StartedAt:        utils.ParseTimestamp(rm.StartedAt),
		TenantId:         rm.TenantID,
	}
	if rm.ParentNodeID.Valid {
		node.ParentNodeId = rm.ParentNodeID.String
	}
	if rm.AgentID.Valid {
		node.AgentId = rm.AgentID.String
	}
	if rm.Task.Valid {
		node.Task = rm.Task.String
	}
	if rm.Result.Valid {
		node.Result = rm.Result.String
	}
	if rm.CompletedAt.Valid {
		node.CompletedAt = utils.ParseTimestamp(rm.CompletedAt.String)
	}
	if rm.ExecutionID.Valid {
		node.ExecutionId = rm.ExecutionID.String
	}
	return node
}

// ============================================================================
// Sandbox Helpers
// ============================================================================

// publicBackendLabel maps the internal sandbox backend identifier to the
// label exposed on the public API and SDK. The microVM backends
// ("firecracker", "firecracker-agent") are reported as the generic
// "isolated" so responses never disclose the underlying isolation
// technology; "docker" and "kubernetes" pass through unchanged. Only the
// wire response is translated — the backend's Name(), the persisted
// sandbox_instances.backend column, snapshot manifests, and config keys
// all keep their real values.
func publicBackendLabel(name string) string {
	switch name {
	case "firecracker", "firecracker-agent":
		return "isolated"
	default:
		return name
	}
}

func (s *Server) populateSandboxBillingSnapshot(
	ctx context.Context,
	pb *agentspb.SandboxInstance,
	cfg sandbox.InstanceConfig,
	tenantID string,
	startedAt, now time.Time,
) {
	if pb == nil || s.sandboxMgr == nil || startedAt.IsZero() {
		return
	}
	pb.BillingStartedAt = timestamppb.New(startedAt)
	cost, seconds := s.sandboxMgr.AccruedCostForWindow(ctx, tenantID, cfg, startedAt, now)
	pb.CurrentComputeSeconds = seconds
	pb.CurrentComputeCostUsd = cost
}

func sandboxInstanceToProto(inst *sandbox.Instance) *agentspb.SandboxInstance {
	pi := &agentspb.SandboxInstance{
		Id:                inst.ID,
		SessionId:         inst.Config.SessionID,
		TenantId:          inst.Config.TenantID,
		Backend:           publicBackendLabel(inst.Backend),
		ContainerId:       inst.ContainerID,
		Image:             inst.Config.Image,
		Status:            stringToSandboxStatus(string(inst.Status)),
		CreatedAt:         utils.ParseTimestamp(inst.CreatedAt.Format(time.RFC3339)),
		Name:              inst.Name,
		LastUsedAt:        utils.ParseTimestamp(inst.LastUsedAt.Format(time.RFC3339)),
		IdleRetentionSecs: int32(inst.IdleRetentionSecs),
		KeepWarm:          inst.KeepWarm,
		GitRepoUrl:        inst.GitRepoURL,
		GitBranch:         inst.GitBranch,
		GitCommitSha:      inst.GitCommitSHA,
		LifecycleState:    sandbox.PublicLifecycleState(inst.LifecycleState, inst.Status),
		SshEnabled:        inst.Config.SSHEnabled,
		Persistent:        inst.Persistent,
		AgentId:           inst.AgentID,
		ShortCode:         inst.ShortCode,
		AgentHealthy:      inst.AgentHealthy,
	}
	if !inst.ExpiresAt.IsZero() {
		pi.ExpiresAt = utils.ParseTimestamp(inst.ExpiresAt.Format(time.RFC3339))
	}
	if inst.LastUsedAt.IsZero() {
		pi.LastUsedAt = nil
	}
	if !inst.RevivableUntil.IsZero() {
		pi.RevivableUntil = utils.ParseTimestamp(inst.RevivableUntil.Format(time.RFC3339))
	}
	if !inst.StoppedAt.IsZero() {
		pi.StoppedAt = utils.ParseTimestamp(inst.StoppedAt.Format(time.RFC3339))
	}
	if !inst.BillingStartedAt.IsZero() {
		pi.BillingStartedAt = utils.ParseTimestamp(inst.BillingStartedAt.Format(time.RFC3339Nano))
	}
	if !inst.BillingEndedAt.IsZero() {
		pi.BillingEndedAt = utils.ParseTimestamp(inst.BillingEndedAt.Format(time.RFC3339Nano))
	}
	// Public Daytona-style label derived from lifecycle (or status when
	// the in-memory instance predates lifecycle tracking).
	if inst.LifecycleState != "" {
		pi.State = sandboxlc.PublicState(inst.LifecycleState)
	} else {
		pi.State = sandboxlc.PublicState(string(inst.Status))
	}
	configMap := map[string]interface{}{
		"cpu_limit":       inst.Config.CPULimit,
		"cpuLimit":        inst.Config.CPULimit,
		"memory_mb":       inst.Config.MemoryMB,
		"memoryMb":        inst.Config.MemoryMB,
		"disk_mb":         inst.Config.DiskMB,
		"diskMb":          inst.Config.DiskMB,
		"timeout_seconds": inst.Config.TimeoutSeconds,
		"timeoutSeconds":  inst.Config.TimeoutSeconds,
		"network_mode":    string(inst.Config.NetworkMode),
		"networkMode":     string(inst.Config.NetworkMode),
		"work_dir":        inst.Config.WorkDir,
		"workDir":         inst.Config.WorkDir,
	}
	if cfg, err := structpb.NewStruct(configMap); err == nil {
		pi.Config = cfg
	}
	return pi
}

func sandboxInstanceReadModelToProto(rm *agentsquery.SandboxInstanceReadModel) *agentspb.SandboxInstance {
	pi := &agentspb.SandboxInstance{
		Id:          rm.ID,
		SessionId:   rm.SessionID,
		TenantId:    rm.TenantID,
		Backend:     publicBackendLabel(rm.Backend),
		ContainerId: rm.ContainerID,
		Image:       rm.Image,
		Status:      stringToSandboxStatus(rm.Status),
		CreatedAt:   utils.ParseTimestamp(rm.CreatedAt),
		Name:        rm.Name,
		// Read models are persisted/eventual-consistency snapshots; the
		// in-guest agent health is real-time and not modeled here. We
		// report true so the UI dot stays green for rows that haven't
		// been refreshed from the live backend. The live path
		// (sandboxInstanceToProto) sets the actual probe result.
		AgentHealthy: true,
	}
	if rm.ExpiresAt.Valid {
		pi.ExpiresAt = utils.ParseTimestamp(rm.ExpiresAt.String)
	}
	if rm.LastUsedAt.Valid {
		pi.LastUsedAt = utils.ParseTimestamp(rm.LastUsedAt.String)
	}
	if rm.BillingStartedAt.Valid {
		pi.BillingStartedAt = timestamppb.New(rm.BillingStartedAt.Time)
	}
	if rm.BillingEndedAt.Valid {
		pi.BillingEndedAt = timestamppb.New(rm.BillingEndedAt.Time)
	}
	if rm.IdleRetentionSecs.Valid {
		pi.IdleRetentionSecs = int32(rm.IdleRetentionSecs.Int64)
	}
	if rm.KeepWarm.Valid {
		pi.KeepWarm = rm.KeepWarm.Bool
	}
	if rm.DestroyReason.Valid {
		pi.DestroyReason = rm.DestroyReason.String
	}
	if rm.GitRepoURL.Valid {
		pi.GitRepoUrl = rm.GitRepoURL.String
	}
	if rm.GitBranch.Valid {
		pi.GitBranch = rm.GitBranch.String
	}
	if rm.GitCommitSHA.Valid {
		pi.GitCommitSha = rm.GitCommitSHA.String
	}
	lifecycleState := ""
	if rm.LifecycleState.Valid {
		lifecycleState = rm.LifecycleState.String
	}
	pi.LifecycleState = sandbox.PublicLifecycleState(lifecycleState, sandbox.Status(rm.Status))
	if rm.RevivableUntil.Valid {
		pi.RevivableUntil = utils.ParseTimestamp(rm.RevivableUntil.String)
	}
	if rm.StoppedAt.Valid {
		pi.StoppedAt = utils.ParseTimestamp(rm.StoppedAt.String)
	}
	if rm.Persistent.Valid {
		pi.Persistent = rm.Persistent.Bool
	}
	if rm.AgentID.Valid {
		pi.AgentId = rm.AgentID.String
	}
	if rm.ShortCode.Valid {
		pi.ShortCode = rm.ShortCode.String
	}
	if len(rm.Config) > 0 {
		var configMap map[string]interface{}
		if err := json.Unmarshal(rm.Config, &configMap); err == nil {
			if cfg, err := structpb.NewStruct(configMap); err == nil {
				pi.Config = cfg
			}
		}
	}
	return pi
}

func sandboxInstanceIsRunning(inst *sandbox.Instance) bool {
	if inst == nil {
		return false
	}
	if inst.LifecycleState != "" {
		return inst.LifecycleState == sandbox.LifecycleRunning
	}
	return inst.Status == sandbox.StatusRunning
}

func sandboxReadModelIsStalePending(rm *agentsquery.SandboxInstanceReadModel, maxAge time.Duration) bool {
	if rm == nil || rm.Status != string(sandbox.StatusPending) {
		return false
	}
	if rm.LifecycleState.Valid {
		state := strings.TrimSpace(rm.LifecycleState.String)
		if state != "" && state != "pending" && state != "creating" && state != "provisioning" {
			return false
		}
	}
	createdAt, err := time.Parse(time.RFC3339Nano, rm.CreatedAt)
	if err != nil {
		createdAt, err = time.Parse(time.RFC3339, rm.CreatedAt)
	}
	if err != nil {
		return false
	}
	return time.Since(createdAt) > maxAge
}

func sandboxExecutionToProto(rm *agentsquery.SandboxExecutionReadModel) *agentspb.SandboxExecution {
	pe := &agentspb.SandboxExecution{
		Id:         rm.ID,
		SandboxId:  rm.SandboxID,
		SessionId:  rm.SessionID,
		Command:    rm.Command,
		ExitCode:   int32(rm.ExitCode),
		DurationMs: rm.DurationMs,
		TimedOut:   rm.TimedOut,
		CreatedAt:  utils.ParseTimestamp(rm.CreatedAt),
	}
	if rm.ToolName.Valid {
		pe.ToolName = rm.ToolName.String
	}
	if rm.ToolCallID.Valid {
		pe.ToolCallId = rm.ToolCallID.String
	}
	if rm.Language.Valid {
		pe.Language = rm.Language.String
	}
	if rm.Stdout.Valid {
		pe.Stdout = rm.Stdout.String
	}
	if rm.Stderr.Valid {
		pe.Stderr = rm.Stderr.String
	}
	return pe
}

func sandboxStatusToString(status agentspb.SandboxStatus) string {
	switch status {
	case agentspb.SandboxStatus_SANDBOX_STATUS_PENDING:
		return "pending"
	case agentspb.SandboxStatus_SANDBOX_STATUS_RUNNING:
		return "running"
	case agentspb.SandboxStatus_SANDBOX_STATUS_STOPPED:
		return "stopped"
	case agentspb.SandboxStatus_SANDBOX_STATUS_FAILED:
		return "failed"
	default:
		return ""
	}
}

func stringToSandboxStatus(s string) agentspb.SandboxStatus {
	switch s {
	case "pending":
		return agentspb.SandboxStatus_SANDBOX_STATUS_PENDING
	case "running":
		return agentspb.SandboxStatus_SANDBOX_STATUS_RUNNING
	case "stopped":
		return agentspb.SandboxStatus_SANDBOX_STATUS_STOPPED
	// See sandboxStatusFromString: 'error' must not map to the zero enum.
	case "failed", "error":
		return agentspb.SandboxStatus_SANDBOX_STATUS_FAILED
	default:
		return agentspb.SandboxStatus_SANDBOX_STATUS_UNSPECIFIED
	}
}

func approvalReviewToProto(rm *agentsquery.ApprovalReviewReadModel) *agentspb.ApprovalReview {
	review := &agentspb.ApprovalReview{
		Id:            rm.ID,
		SessionId:     rm.SessionID,
		TenantId:      rm.TenantID,
		AgentId:       rm.AgentID,
		TurnNumber:    rm.TurnNumber,
		Iteration:     rm.Iteration,
		Status:        stringToApprovalStatus(rm.Status),
		DefaultAction: rm.DefaultAction,
		RequestedAt:   utils.ParseTimestamp(rm.RequestedAt),
		ExpiresAt:     utils.ParseTimestamp(rm.ExpiresAt),
	}

	if rm.ResolvedAt.Valid {
		review.ResolvedAt = utils.ParseTimestamp(rm.ResolvedAt.String)
	}
	if rm.ResolvedBy.Valid {
		review.ResolvedBy = rm.ResolvedBy.String
	}
	if rm.ResolutionReason.Valid {
		review.ResolutionReason = rm.ResolutionReason.String
	}

	// Parse tool_calls JSON into proto
	if len(rm.ToolCalls) > 0 {
		var toolCalls []struct {
			ID       string `json:"id"`
			Function struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			} `json:"function"`
		}
		if err := json.Unmarshal(rm.ToolCalls, &toolCalls); err == nil {
			for _, tc := range toolCalls {
				review.ToolCalls = append(review.ToolCalls, &agentspb.PendingToolCall{
					ToolCallId: tc.ID,
					ToolName:   tc.Function.Name,
					ToolArgs:   tc.Function.Arguments,
				})
			}
		}
	}

	// Parse decisions JSON into proto
	if len(rm.Decisions) > 0 {
		var decisions []agentrt.PerToolDecision
		if err := json.Unmarshal(rm.Decisions, &decisions); err == nil {
			for _, d := range decisions {
				action := agentspb.ApprovalAction_APPROVAL_ACTION_APPROVE
				if d.Action == "deny" {
					action = agentspb.ApprovalAction_APPROVAL_ACTION_DENY
				}
				review.Decisions = append(review.Decisions, &agentspb.ToolCallDecision{
					ToolCallId: d.ToolCallID,
					Action:     action,
					Reason:     d.Reason,
				})
			}
		}
	}

	return review
}

// appendUnique appends items to a slice only if they aren't already present.
func appendUnique(slice []string, items ...string) []string {
	existing := make(map[string]struct{}, len(slice))
	for _, s := range slice {
		existing[s] = struct{}{}
	}
	for _, item := range items {
		if _, ok := existing[item]; !ok {
			slice = append(slice, item)
		}
	}
	return slice
}
