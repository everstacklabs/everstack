// Deprecated: This file contains the legacy Trooper CRUD handlers.
// All Trooper RPCs are deprecated in favor of the unified Agent API
// with lifecycle_mode=PERSISTENT. These handlers remain as shims
// during migration. Do not add new functionality here.
// See agents_lifecycle.go for the unified agent lifecycle RPCs.
package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	trooperscmd "github.com/everstacklabs/everstack/internal/commands/handlers/troopers"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/query"
	troopersquery "github.com/everstacklabs/everstack/internal/query/handlers/troopers"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/internal/trooper"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ─── Trooper CRUD (DEPRECATED) ───────────────────────────────────────────

func (s *Server) CreateTrooper(ctx context.Context, req *connect.Request[agentspb.CreateTrooperRequest]) (*connect.Response[agentspb.CreateTrooperResponse], error) {
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

	// Build sandbox env vars
	envVars := make(map[string]string)
	if req.Msg.GetSandbox() != nil {
		for k, v := range req.Msg.GetSandbox().GetEnvVars() {
			envVars[k] = v
		}
	}

	// Build agent config
	var agentConfig map[string]interface{}
	if req.Msg.GetAgentConfig() != nil {
		agentConfig = req.Msg.GetAgentConfig().AsMap()
	}

	// Build worker pool config
	var workerPoolConfig map[string]interface{}
	if req.Msg.GetWorkers() != nil && req.Msg.GetWorkers().GetPoolConfig() != nil {
		workerPoolConfig = req.Msg.GetWorkers().GetPoolConfig().AsMap()
	}

	// Defaults
	image := sandbox.DefaultDevImage
	cpuLimit := 1.0
	var memoryMB int64 = 512
	var diskMB int64 = 2048
	networkMode := "allow"
	sqlitePath := "/workspace/data/trooper.db"
	lancedbPath := "/workspace/data/lancedb"
	redbPath := "/workspace/data/trooper.redb"
	var maxConcurrentWorkers int32 = 3
	var maxToolCallsPerTurn int32 = 10

	if sb := req.Msg.GetSandbox(); sb != nil {
		if sb.Image != "" {
			image = sb.Image
		}
		if sb.CpuLimit > 0 {
			cpuLimit = sb.CpuLimit
		}
		if sb.MemoryMb > 0 {
			memoryMB = sb.MemoryMb
		}
		if sb.DiskMb > 0 {
			diskMB = sb.DiskMb
		}
		if sb.NetworkMode != "" {
			networkMode = sb.NetworkMode
		}
	}
	if db := req.Msg.GetDatabases(); db != nil {
		if db.SqlitePath != "" {
			sqlitePath = db.SqlitePath
		}
		if db.LancedbPath != "" {
			lancedbPath = db.LancedbPath
		}
		if db.RedbPath != "" {
			redbPath = db.RedbPath
		}
	}
	if w := req.Msg.GetWorkers(); w != nil {
		if w.MaxConcurrentWorkers > 0 {
			maxConcurrentWorkers = w.MaxConcurrentWorkers
		}
	}
	if req.Msg.GetMaxToolCallsPerTurn() > 0 {
		maxToolCallsPerTurn = req.Msg.GetMaxToolCallsPerTurn()
	}

	cmd := trooperscmd.NewCreateTrooperCommand(
		tenantID,
		req.Msg.GetName(),
		req.Msg.GetDescription(),
		req.Msg.GetModel(),
		req.Msg.GetSystemPrompt(),
		req.Msg.GetTools(),
		agentConfig,
		req.Msg.GetMaxTurns(),
		maxToolCallsPerTurn,
		nil, // maxSteps
		getIdentitySoulMD(req.Msg.GetIdentity()),
		getIdentityIdentityMD(req.Msg.GetIdentity()),
		getIdentityUserMD(req.Msg.GetIdentity()),
		getIdentityRoleMD(req.Msg.GetIdentity()),
		image, networkMode,
		cpuLimit, memoryMB, diskMB,
		req.Msg.GetSandbox().GetTimeoutSeconds(),
		getSandboxAllowedHosts(req.Msg.GetSandbox()),
		envVars,
		getSandboxSSHEnabled(req.Msg.GetSandbox()),
		getSandboxGitRepoURL(req.Msg.GetSandbox()),
		getSandboxGitBranch(req.Msg.GetSandbox()),
		sqlitePath, lancedbPath, redbPath,
		maxConcurrentWorkers,
		workerPoolConfig,
		getOptionalStringField(req.Msg.Color),
		getOptionalStringField(req.Msg.Icon),
		req.Msg.GetAutoProvision(),
		userID, "",
	)

	if req.Msg.MaxSteps != nil {
		v := req.Msg.GetMaxSteps()
		cmd.MaxSteps = &v
	}

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// If auto_provision, provision the trooper
	if req.Msg.GetAutoProvision() && s.trooperMgr != nil {
		go func() {
			provCtx := context.Background()
			_, provErr := s.trooperMgr.Provision(provCtx, trooper.ProvisionConfig{
				TrooperID:   cmd.ID,
				TenantID:    tenantID,
				Name:        req.Msg.GetName(),
				Image:       image,
				CPULimit:    cpuLimit,
				MemoryMB:    memoryMB,
				DiskMB:      diskMB,
				NetworkMode: networkMode,
				EnvVars:     envVars,
				SSHEnabled:  getSandboxSSHEnabled(req.Msg.GetSandbox()),
				GitRepoURL:  getSandboxGitRepoURL(req.Msg.GetSandbox()),
				GitBranch:   getSandboxGitBranch(req.Msg.GetSandbox()),
				Identity: trooper.IdentityFiles{
					SoulMD:     getIdentitySoulMD(req.Msg.GetIdentity()),
					IdentityMD: getIdentityIdentityMD(req.Msg.GetIdentity()),
					UserMD:     getIdentityUserMD(req.Msg.GetIdentity()),
					RoleMD:     getIdentityRoleMD(req.Msg.GetIdentity()),
				},
				Databases: trooper.DatabaseConfig{
					SqlitePath:  sqlitePath,
					LanceDBPath: lancedbPath,
					RedbPath:    redbPath,
				},
			})
			if provErr != nil {
				logger.WithFields("trooper_id", cmd.ID, "error", provErr.Error()).
					Error("failed to auto-provision trooper")
			}
		}()
	}

	return connect.NewResponse(&agentspb.CreateTrooperResponse{
		Trooper: &agentspb.Trooper{
			Id:       cmd.ID,
			TenantId: tenantID,
			Name:     req.Msg.GetName(),
			Status:   agentspb.TrooperStatus_TROOPER_STATUS_CREATED,
		},
	}), nil
}

func (s *Server) GetTrooper(ctx context.Context, req *connect.Request[agentspb.GetTrooperRequest]) (*connect.Response[agentspb.GetTrooperResponse], error) {
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

	q := troopersquery.NewGetTrooperByIDQuery(req.Msg.GetId(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if res == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("trooper not found"))
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	rm, ok := data.(*troopersquery.TrooperReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}

	return connect.NewResponse(&agentspb.GetTrooperResponse{
		Trooper: trooperReadModelToProto(rm),
	}), nil
}

func (s *Server) ListTroopers(ctx context.Context, req *connect.Request[agentspb.ListTroopersRequest]) (*connect.Response[agentspb.ListTroopersResponse], error) {
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

	var statusFilter *string
	if req.Msg.Status != nil {
		sf := protoTrooperStatusToString(req.Msg.GetStatus())
		if sf != "" {
			statusFilter = &sf
		}
	}

	q := troopersquery.NewListTroopersQuery(
		tenantID, statusFilter,
		int(req.Msg.GetLimit()), int(req.Msg.GetOffset()),
	)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	result, ok := data.(*troopersquery.TroopersResult)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}

	var protos []*agentspb.Trooper
	for i := range result.Troopers {
		protos = append(protos, trooperReadModelToProto(&result.Troopers[i]))
	}

	return connect.NewResponse(&agentspb.ListTroopersResponse{
		Troopers: protos,
		Total:    int32(result.Total),
	}), nil
}

func (s *Server) UpdateTrooper(ctx context.Context, req *connect.Request[agentspb.UpdateTrooperRequest]) (*connect.Response[agentspb.UpdateTrooperResponse], error) {
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

	cmd := trooperscmd.NewUpdateTrooperCommand(req.Msg.GetId(), tenantID, userID, "")

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
	if len(req.Msg.Tools) > 0 {
		cmd.Tools = req.Msg.Tools
	}
	if req.Msg.GetAgentConfig() != nil {
		m := req.Msg.GetAgentConfig().AsMap()
		cmd.AgentConfig = m
	}
	if req.Msg.MaxTurns != nil {
		v := req.Msg.GetMaxTurns()
		cmd.MaxTurns = &v
	}
	if req.Msg.MaxToolCallsPerTurn != nil {
		v := req.Msg.GetMaxToolCallsPerTurn()
		cmd.MaxToolCallsPerTurn = &v
	}
	if req.Msg.MaxSteps != nil {
		v := req.Msg.GetMaxSteps()
		cmd.MaxSteps = &v
	}
	if req.Msg.GetIdentity() != nil {
		id := req.Msg.GetIdentity()
		if id.SoulMd != "" {
			cmd.SoulMD = &id.SoulMd
		}
		if id.IdentityMd != "" {
			cmd.IdentityMD = &id.IdentityMd
		}
		if id.UserMd != "" {
			cmd.UserMD = &id.UserMd
		}
		if id.RoleMd != "" {
			cmd.RoleMD = &id.RoleMd
		}
	}
	if req.Msg.Color != nil {
		cmd.Color = req.Msg.Color
	}
	if req.Msg.Icon != nil {
		cmd.Icon = req.Msg.Icon
	}

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.UpdateTrooperResponse{
		Trooper: &agentspb.Trooper{
			Id:       req.Msg.GetId(),
			TenantId: tenantID,
		},
	}), nil
}

func (s *Server) DeleteTrooper(ctx context.Context, req *connect.Request[agentspb.DeleteTrooperRequest]) (*connect.Response[agentspb.DeleteTrooperResponse], error) {
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

	cmd := trooperscmd.NewDeleteTrooperCommand(req.Msg.GetId(), tenantID, userID, "")
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.DeleteTrooperResponse{
		Success: true,
		Message: "trooper deleted",
	}), nil
}

// ─── Trooper Lifecycle ────────────────────────────────────────────────────

func (s *Server) ProvisionTrooper(ctx context.Context, req *connect.Request[agentspb.ProvisionTrooperRequest]) (*connect.Response[agentspb.ProvisionTrooperResponse], error) {
	if s.trooperMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("trooper manager not available"))
	}

	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}

	// Read trooper from DB
	if s.db == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("database not available"))
	}

	var ws struct {
		Name               string  `db:"name"`
		SandboxImage       string  `db:"sandbox_image"`
		SandboxCPULimit    float64 `db:"sandbox_cpu_limit"`
		SandboxMemoryMB    int64   `db:"sandbox_memory_mb"`
		SandboxDiskMB      int64   `db:"sandbox_disk_mb"`
		SandboxNetworkMode string  `db:"sandbox_network_mode"`
		SandboxSSHEnabled  bool    `db:"sandbox_ssh_enabled"`
		SandboxGitRepoURL  *string `db:"sandbox_git_repo_url"`
		SandboxGitBranch   *string `db:"sandbox_git_branch"`
		SoulMD             string  `db:"soul_md"`
		IdentityMD         string  `db:"identity_md"`
		UserMD             string  `db:"user_md"`
		RoleMD             string  `db:"role_md"`
		DBSqlitePath       string  `db:"db_sqlite_path"`
		DBLanceDBPath      string  `db:"db_lancedb_path"`
		DBRedbPath         string  `db:"db_redb_path"`
	}
	err = s.db.GetContext(ctx, &ws, `
		SELECT name, sandbox_image, sandbox_cpu_limit, sandbox_memory_mb, sandbox_disk_mb,
			sandbox_network_mode, sandbox_ssh_enabled, sandbox_git_repo_url, sandbox_git_branch,
			soul_md, identity_md, user_md, role_md, db_sqlite_path, db_lancedb_path, db_redb_path
		FROM troopers WHERE id = $1 AND deleted_at IS NULL
	`, req.Msg.GetTrooperId())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("trooper not found: %w", err))
	}

	gitURL := ""
	gitBranch := ""
	if ws.SandboxGitRepoURL != nil {
		gitURL = *ws.SandboxGitRepoURL
	}
	if ws.SandboxGitBranch != nil {
		gitBranch = *ws.SandboxGitBranch
	}

	sandboxID, err := s.trooperMgr.Provision(ctx, trooper.ProvisionConfig{
		TrooperID:   req.Msg.GetTrooperId(),
		TenantID:    tenantID,
		Name:        ws.Name,
		Image:       ws.SandboxImage,
		CPULimit:    ws.SandboxCPULimit,
		MemoryMB:    ws.SandboxMemoryMB,
		DiskMB:      ws.SandboxDiskMB,
		NetworkMode: ws.SandboxNetworkMode,
		SSHEnabled:  ws.SandboxSSHEnabled,
		GitRepoURL:  gitURL,
		GitBranch:   gitBranch,
		Identity: trooper.IdentityFiles{
			SoulMD:     ws.SoulMD,
			IdentityMD: ws.IdentityMD,
			UserMD:     ws.UserMD,
			RoleMD:     ws.RoleMD,
		},
		Databases: trooper.DatabaseConfig{
			SqlitePath:  ws.DBSqlitePath,
			LanceDBPath: ws.DBLanceDBPath,
			RedbPath:    ws.DBRedbPath,
		},
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	_ = sandboxID

	return connect.NewResponse(&agentspb.ProvisionTrooperResponse{
		Trooper: &agentspb.Trooper{
			Id:       req.Msg.GetTrooperId(),
			TenantId: tenantID,
			Status:   agentspb.TrooperStatus_TROOPER_STATUS_RUNNING,
		},
	}), nil
}

func (s *Server) SleepTrooper(ctx context.Context, req *connect.Request[agentspb.SleepTrooperRequest]) (*connect.Response[agentspb.SleepTrooperResponse], error) {
	if s.trooperMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("trooper manager not available"))
	}

	if err := s.trooperMgr.Sleep(ctx, req.Msg.GetTrooperId()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.SleepTrooperResponse{
		Success: true,
		Message: "trooper is sleeping",
	}), nil
}

func (s *Server) WakeTrooper(ctx context.Context, req *connect.Request[agentspb.WakeTrooperRequest]) (*connect.Response[agentspb.WakeTrooperResponse], error) {
	if s.trooperMgr == nil {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("trooper manager not available"))
	}

	if err := s.trooperMgr.Wake(ctx, req.Msg.GetTrooperId()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.WakeTrooperResponse{
		Trooper: &agentspb.Trooper{
			Id:     req.Msg.GetTrooperId(),
			Status: agentspb.TrooperStatus_TROOPER_STATUS_RUNNING,
		},
	}), nil
}

// ─── Trooper Links ────────────────────────────────────────────────────────

func (s *Server) CreateTrooperLink(ctx context.Context, req *connect.Request[agentspb.CreateTrooperLinkRequest]) (*connect.Response[agentspb.CreateTrooperLinkResponse], error) {
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

	cmd := trooperscmd.NewCreateTrooperLinkCommand(
		tenantID,
		req.Msg.GetSourceTrooperId(),
		req.Msg.GetTargetType(),
		req.Msg.GetTargetId(),
		req.Msg.GetTargetName(),
		protoTrooperLinkTypeToString(req.Msg.GetLinkType()),
		protoTrooperLinkProtocolToString(req.Msg.GetProtocol()),
		config,
		userID, "",
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.CreateTrooperLinkResponse{
		Link: &agentspb.TrooperLink{
			Id:              cmd.ID,
			TenantId:        tenantID,
			SourceTrooperId: req.Msg.GetSourceTrooperId(),
			TargetType:      req.Msg.GetTargetType(),
			TargetId:        req.Msg.GetTargetId(),
		},
	}), nil
}

func (s *Server) ListTrooperLinks(ctx context.Context, req *connect.Request[agentspb.ListTrooperLinksRequest]) (*connect.Response[agentspb.ListTrooperLinksResponse], error) {
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

	q := troopersquery.NewListTrooperLinksQuery(tenantID, req.Msg.GetTrooperId())
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	links, ok := data.([]troopersquery.TrooperLinkReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}

	var protos []*agentspb.TrooperLink
	for _, l := range links {
		protos = append(protos, trooperLinkReadModelToProto(&l))
	}

	return connect.NewResponse(&agentspb.ListTrooperLinksResponse{
		Links: protos,
		Total: int32(len(protos)),
	}), nil
}

func (s *Server) DeleteTrooperLink(ctx context.Context, req *connect.Request[agentspb.DeleteTrooperLinkRequest]) (*connect.Response[agentspb.DeleteTrooperLinkResponse], error) {
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

	cmd := trooperscmd.NewDeleteTrooperLinkCommand(req.Msg.GetLinkId(), tenantID, userID, "")
	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.DeleteTrooperLinkResponse{
		Success: true,
		Message: "link deleted",
	}), nil
}

// ─── Trooper Channel Bindings ─────────────────────────────────────────────

func (s *Server) BindChannelToTrooper(ctx context.Context, req *connect.Request[agentspb.BindChannelToTrooperRequest]) (*connect.Response[agentspb.BindChannelToTrooperResponse], error) {
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

	// CHANNEL_BINDINGS covers agent AND trooper bindings together; without
	// this gate the legacy trooper RPC bypassed the limit entirely.
	if err := enterprise.CheckResourceLimit(ctx, s.db, enterprise.LicenseMonitorFromContext(ctx),
		enterprise.UsageTypeChannelBindings,
		`SELECT (SELECT COUNT(*) FROM agent_channel_bindings WHERE tenant_id = $1 AND deleted_at IS NULL)
		      + (SELECT COUNT(*) FROM trooper_channel_bindings WHERE tenant_id = $1)`,
		[]interface{}{tenantID}, 1, "channel binding"); err != nil {
		return nil, connect.NewError(connect.CodeResourceExhausted, err)
	}

	cmd := trooperscmd.NewBindChannelCommand(
		tenantID, req.Msg.GetTrooperId(), req.Msg.GetChannelConfigId(),
		userID, "",
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.BindChannelToTrooperResponse{
		Binding: &agentspb.TrooperChannelBinding{
			Id:              cmd.ID,
			TrooperId:       req.Msg.GetTrooperId(),
			ChannelConfigId: req.Msg.GetChannelConfigId(),
			Enabled:         true,
		},
	}), nil
}

func (s *Server) UnbindChannelFromTrooper(ctx context.Context, req *connect.Request[agentspb.UnbindChannelFromTrooperRequest]) (*connect.Response[agentspb.UnbindChannelFromTrooperResponse], error) {
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

	cmd := trooperscmd.NewUnbindChannelCommand(
		tenantID, req.Msg.GetTrooperId(), req.Msg.GetChannelConfigId(),
		userID, "",
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.UnbindChannelFromTrooperResponse{
		Success: true,
		Message: "channel unbound",
	}), nil
}

func (s *Server) ListTrooperChannelBindings(ctx context.Context, req *connect.Request[agentspb.ListTrooperChannelBindingsRequest]) (*connect.Response[agentspb.ListTrooperChannelBindingsResponse], error) {
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

	q := troopersquery.NewListChannelBindingsQuery(tenantID, req.Msg.GetTrooperId())
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	bindings, ok := data.([]troopersquery.TrooperChannelBindingReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}

	var protos []*agentspb.TrooperChannelBinding
	for _, b := range bindings {
		protos = append(protos, &agentspb.TrooperChannelBinding{
			Id:              b.ID,
			TrooperId:       b.TrooperID,
			ChannelConfigId: b.ChannelConfigID,
			Enabled:         b.Enabled,
		})
	}

	return connect.NewResponse(&agentspb.ListTrooperChannelBindingsResponse{
		Bindings: protos,
		Total:    int32(len(protos)),
	}), nil
}

// ─── Helpers ────────────────────────────────────────────────────────────────

func getIdentitySoulMD(id *agentspb.TrooperIdentity) string {
	if id == nil {
		return ""
	}
	return id.SoulMd
}

func getIdentityIdentityMD(id *agentspb.TrooperIdentity) string {
	if id == nil {
		return ""
	}
	return id.IdentityMd
}

func getIdentityUserMD(id *agentspb.TrooperIdentity) string {
	if id == nil {
		return ""
	}
	return id.UserMd
}

func getIdentityRoleMD(id *agentspb.TrooperIdentity) string {
	if id == nil {
		return ""
	}
	return id.RoleMd
}

func getSandboxAllowedHosts(sb *agentspb.TrooperSandboxConfig) []string {
	if sb == nil {
		return nil
	}
	return sb.AllowedHosts
}

func getSandboxSSHEnabled(sb *agentspb.TrooperSandboxConfig) bool {
	if sb == nil {
		return false
	}
	return sb.SshEnabled
}

func getSandboxGitRepoURL(sb *agentspb.TrooperSandboxConfig) string {
	if sb == nil {
		return ""
	}
	return sb.GitRepoUrl
}

func getSandboxGitBranch(sb *agentspb.TrooperSandboxConfig) string {
	if sb == nil {
		return ""
	}
	return sb.GitBranch
}

func getOptionalStringField(s *string) *string {
	return s
}

func trooperReadModelToProto(rm *troopersquery.TrooperReadModel) *agentspb.Trooper {
	ws := &agentspb.Trooper{
		Id:                  rm.ID,
		TenantId:            rm.TenantID,
		Name:                rm.Name,
		Status:              stringToProtoTrooperStatus(rm.Status),
		Model:               rm.Model,
		Tools:               rm.Tools,
		MaxTurns:            rm.MaxTurns,
		MaxToolCallsPerTurn: rm.MaxToolCallsPerTurn,
		Identity: &agentspb.TrooperIdentity{
			SoulMd:     rm.SoulMD,
			IdentityMd: rm.IdentityMD,
			UserMd:     rm.UserMD,
			RoleMd:     rm.RoleMD,
		},
		Sandbox: &agentspb.TrooperSandboxConfig{
			Image:       rm.SandboxImage,
			CpuLimit:    rm.SandboxCPULimit,
			MemoryMb:    int64(rm.SandboxMemoryMB),
			DiskMb:      int64(rm.SandboxDiskMB),
			NetworkMode: rm.SandboxNetworkMode,
			SshEnabled:  rm.SandboxSSHEnabled,
		},
		Databases: &agentspb.TrooperDatabaseConfig{
			SqlitePath:  rm.DBSqlitePath,
			LancedbPath: rm.DBLanceDBPath,
			RedbPath:    rm.DBRedbPath,
		},
		Workers: &agentspb.TrooperWorkersConfig{
			MaxConcurrentWorkers: rm.MaxConcurrentWorkers,
		},
		CreatedAt: parseTimestampStr(rm.CreatedAt),
		UpdatedAt: parseTimestampStr(rm.UpdatedAt),
	}

	if rm.Description.Valid {
		ws.Description = rm.Description.String
	}
	if rm.SystemPrompt.Valid {
		ws.SystemPrompt = rm.SystemPrompt.String
	}
	if rm.MaxSteps.Valid {
		v := rm.MaxSteps.Int32
		ws.MaxSteps = &v
	}
	if rm.Color.Valid {
		v := rm.Color.String
		ws.Color = &v
	}
	if rm.Icon.Valid {
		v := rm.Icon.String
		ws.Icon = &v
	}
	if rm.SandboxID.Valid {
		ws.SandboxId = rm.SandboxID.String
	}
	if rm.SandboxGitRepoURL.Valid {
		ws.Sandbox.GitRepoUrl = rm.SandboxGitRepoURL.String
	}
	if rm.SandboxGitBranch.Valid {
		ws.Sandbox.GitBranch = rm.SandboxGitBranch.String
	}
	if len(rm.SandboxAllowedHosts) > 0 {
		ws.Sandbox.AllowedHosts = rm.SandboxAllowedHosts
	}

	// Parse agent_config JSONB
	if len(rm.AgentConfig) > 0 {
		var configMap map[string]interface{}
		if err := json.Unmarshal(rm.AgentConfig, &configMap); err == nil {
			if s, err := structpb.NewStruct(configMap); err == nil {
				ws.AgentConfig = s
			}
		}
	}

	// Parse env vars
	if len(rm.SandboxEnvVars) > 0 {
		var envMap map[string]string
		if err := json.Unmarshal(rm.SandboxEnvVars, &envMap); err == nil {
			ws.Sandbox.EnvVars = envMap
		}
	}

	// Parse worker pool config
	if len(rm.WorkerPoolConfig) > 0 {
		var poolMap map[string]interface{}
		if err := json.Unmarshal(rm.WorkerPoolConfig, &poolMap); err == nil {
			if s, err := structpb.NewStruct(poolMap); err == nil {
				ws.Workers.PoolConfig = s
			}
		}
	}

	return ws
}

func trooperLinkReadModelToProto(rm *troopersquery.TrooperLinkReadModel) *agentspb.TrooperLink {
	link := &agentspb.TrooperLink{
		Id:              rm.ID,
		TenantId:        rm.TenantID,
		SourceTrooperId: rm.SourceTrooperID,
		TargetType:      rm.TargetType,
		TargetId:        rm.TargetID,
		LinkType:        stringToProtoTrooperLinkType(rm.LinkType),
		Protocol:        stringToProtoTrooperLinkProtocol(rm.Protocol),
		Status:          stringToProtoTrooperLinkStatus(rm.Status),
		CreatedAt:       parseTimestampStr(rm.CreatedAt),
		UpdatedAt:       parseTimestampStr(rm.UpdatedAt),
	}
	if rm.TargetName.Valid {
		link.TargetName = rm.TargetName.String
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

func parseTimestampStr(s string) *timestamppb.Timestamp {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05Z07:00", s)
		if err != nil {
			t, _ = time.Parse("2006-01-02 15:04:05.999999-07:00", s)
		}
	}
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// ─── Enum Conversions ───────────────────────────────────────────────────────

func protoTrooperStatusToString(s agentspb.TrooperStatus) string {
	switch s {
	case agentspb.TrooperStatus_TROOPER_STATUS_CREATED:
		return "created"
	case agentspb.TrooperStatus_TROOPER_STATUS_PROVISIONING:
		return "provisioning"
	case agentspb.TrooperStatus_TROOPER_STATUS_RUNNING:
		return "running"
	case agentspb.TrooperStatus_TROOPER_STATUS_SLEEPING:
		return "sleeping"
	case agentspb.TrooperStatus_TROOPER_STATUS_WAKING:
		return "waking"
	case agentspb.TrooperStatus_TROOPER_STATUS_FAILED:
		return "failed"
	case agentspb.TrooperStatus_TROOPER_STATUS_TERMINATED:
		return "terminated"
	default:
		return ""
	}
}

func stringToProtoTrooperStatus(s string) agentspb.TrooperStatus {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "created":
		return agentspb.TrooperStatus_TROOPER_STATUS_CREATED
	case "provisioning":
		return agentspb.TrooperStatus_TROOPER_STATUS_PROVISIONING
	case "running":
		return agentspb.TrooperStatus_TROOPER_STATUS_RUNNING
	case "sleeping":
		return agentspb.TrooperStatus_TROOPER_STATUS_SLEEPING
	case "waking":
		return agentspb.TrooperStatus_TROOPER_STATUS_WAKING
	case "failed":
		return agentspb.TrooperStatus_TROOPER_STATUS_FAILED
	case "terminated":
		return agentspb.TrooperStatus_TROOPER_STATUS_TERMINATED
	default:
		return agentspb.TrooperStatus_TROOPER_STATUS_UNSPECIFIED
	}
}

func protoTrooperLinkTypeToString(t agentspb.TrooperLinkType) string {
	switch t {
	case agentspb.TrooperLinkType_TROOPER_LINK_TYPE_COLLABORATOR:
		return "collaborator"
	case agentspb.TrooperLinkType_TROOPER_LINK_TYPE_SUPERVISOR:
		return "supervisor"
	case agentspb.TrooperLinkType_TROOPER_LINK_TYPE_SUBORDINATE:
		return "subordinate"
	case agentspb.TrooperLinkType_TROOPER_LINK_TYPE_PEER:
		return "peer"
	default:
		return "peer"
	}
}

func stringToProtoTrooperLinkType(s string) agentspb.TrooperLinkType {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "collaborator":
		return agentspb.TrooperLinkType_TROOPER_LINK_TYPE_COLLABORATOR
	case "supervisor":
		return agentspb.TrooperLinkType_TROOPER_LINK_TYPE_SUPERVISOR
	case "subordinate":
		return agentspb.TrooperLinkType_TROOPER_LINK_TYPE_SUBORDINATE
	case "peer":
		return agentspb.TrooperLinkType_TROOPER_LINK_TYPE_PEER
	default:
		return agentspb.TrooperLinkType_TROOPER_LINK_TYPE_UNSPECIFIED
	}
}

func protoTrooperLinkProtocolToString(p agentspb.TrooperLinkProtocol) string {
	switch p {
	case agentspb.TrooperLinkProtocol_TROOPER_LINK_PROTOCOL_INTERNAL:
		return "internal"
	case agentspb.TrooperLinkProtocol_TROOPER_LINK_PROTOCOL_CHANNEL:
		return "channel"
	case agentspb.TrooperLinkProtocol_TROOPER_LINK_PROTOCOL_WEBHOOK:
		return "webhook"
	default:
		return "internal"
	}
}

func stringToProtoTrooperLinkProtocol(s string) agentspb.TrooperLinkProtocol {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "internal":
		return agentspb.TrooperLinkProtocol_TROOPER_LINK_PROTOCOL_INTERNAL
	case "channel":
		return agentspb.TrooperLinkProtocol_TROOPER_LINK_PROTOCOL_CHANNEL
	case "webhook":
		return agentspb.TrooperLinkProtocol_TROOPER_LINK_PROTOCOL_WEBHOOK
	default:
		return agentspb.TrooperLinkProtocol_TROOPER_LINK_PROTOCOL_UNSPECIFIED
	}
}

func stringToProtoTrooperLinkStatus(s string) agentspb.TrooperLinkStatus {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "active":
		return agentspb.TrooperLinkStatus_TROOPER_LINK_STATUS_ACTIVE
	case "paused":
		return agentspb.TrooperLinkStatus_TROOPER_LINK_STATUS_PAUSED
	case "revoked":
		return agentspb.TrooperLinkStatus_TROOPER_LINK_STATUS_REVOKED
	default:
		return agentspb.TrooperLinkStatus_TROOPER_LINK_STATUS_UNSPECIFIED
	}
}
