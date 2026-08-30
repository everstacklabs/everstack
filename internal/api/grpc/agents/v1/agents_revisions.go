package v1

import (
	"context"
	"errors"
	"fmt"

	"connectrpc.com/connect"
	agentprojectruntime "github.com/everstacklabs/everstack/internal/agents/projectruntime"
	agentrevision "github.com/everstacklabs/everstack/internal/agents/revision"
	agenttools "github.com/everstacklabs/everstack/internal/agents/runtime/tools"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/functions/isolation"
	"github.com/everstacklabs/everstack/internal/lib/context_keys"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) CreateAgentRevision(
	ctx context.Context,
	req *connect.Request[agentspb.CreateAgentRevisionRequest],
) (*connect.Response[agentspb.CreateAgentRevisionResponse], error) {
	if s.revisionStore == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("agent revisions are not configured"))
	}
	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	if req.Msg.GetAgentId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("agent_id is required"))
	}
	files := make([]agentrevision.File, 0, len(req.Msg.GetFiles()))
	for _, file := range req.Msg.GetFiles() {
		if file == nil {
			continue
		}
		files = append(files, agentrevision.File{
			Path: file.GetPath(), Mode: file.GetMode(), Content: append([]byte(nil), file.GetContent()...),
		})
	}
	functions := make([]agentrevision.Function, 0, len(req.Msg.GetFunctions()))
	for _, function := range req.Msg.GetFunctions() {
		if function == nil {
			continue
		}
		var parameters map[string]interface{}
		if function.GetParameters() != nil {
			parameters = function.GetParameters().AsMap()
		}
		functions = append(functions, agentrevision.Function{
			Name:        function.GetName(),
			Description: function.GetDescription(),
			Path:        function.GetPath(),
			Export:      function.GetExportName(),
			Runtime:     isolation.Runtime(function.GetRuntime()),
			Parameters:  parameters,
		})
	}
	manifest, err := agentrevision.NewManifest(files, functions)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if len(manifest.Functions) > 0 {
		sys, systemErr := cqrs.GetSystemFromContext(ctx)
		if systemErr != nil && s.ctx != nil {
			sys, systemErr = cqrs.GetSystemFromContext(s.ctx)
		}
		if systemErr != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
		}
		agent, loadErr := s.loadAgentWithRetry(ctx, sys, req.Msg.GetAgentId(), tenantID)
		if loadErr != nil {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("agent not found"))
		}
		if policyErr := agentprojectruntime.ValidateFunctionSandboxPolicy(agenttools.AgentRuntimeConfig(agent)); policyErr != nil {
			return nil, connect.NewError(connect.CodeFailedPrecondition, policyErr)
		}
	}
	rev, created, err := s.revisionStore.CreateAndActivate(
		ctx, tenantID, req.Msg.GetAgentId(), contextkeys.GetUserID(ctx), manifest,
	)
	if err != nil {
		if errors.Is(err, agentrevision.ErrAgentNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("agent not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("create agent revision: %w", err))
	}
	return connect.NewResponse(&agentspb.CreateAgentRevisionResponse{
		Revision: agentRevisionToProto(rev), Created: created,
	}), nil
}

func (s *Server) GetActiveAgentRevision(
	ctx context.Context,
	req *connect.Request[agentspb.GetActiveAgentRevisionRequest],
) (*connect.Response[agentspb.GetActiveAgentRevisionResponse], error) {
	if s.revisionStore == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("agent revisions are not configured"))
	}
	if req.Msg.GetAgentId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("agent_id is required"))
	}
	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	rev, err := s.revisionStore.GetActive(ctx, tenantID, req.Msg.GetAgentId())
	if err != nil {
		if errors.Is(err, agentrevision.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("active agent revision not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentspb.GetActiveAgentRevisionResponse{Revision: agentRevisionToProto(rev)}), nil
}

func (s *Server) GetAgentRevision(
	ctx context.Context,
	req *connect.Request[agentspb.GetAgentRevisionRequest],
) (*connect.Response[agentspb.GetAgentRevisionResponse], error) {
	if s.revisionStore == nil {
		return nil, connect.NewError(connect.CodeUnimplemented, errors.New("agent revisions are not configured"))
	}
	if req.Msg.GetRevisionId() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("revision_id is required"))
	}
	tenantID, err := s.resolveTenantID(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	rev, err := s.revisionStore.Get(ctx, tenantID, req.Msg.GetRevisionId())
	if err != nil {
		if errors.Is(err, agentrevision.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("agent revision not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&agentspb.GetAgentRevisionResponse{Revision: agentRevisionToProto(rev)}), nil
}

func agentRevisionToProto(rev *agentrevision.Revision) *agentspb.AgentRevision {
	if rev == nil {
		return nil
	}
	result := &agentspb.AgentRevision{
		Id: rev.ID, TenantId: rev.TenantID, AgentId: rev.AgentID,
		Number: int32(rev.Number), Digest: rev.Digest, Format: int32(rev.Manifest.Format),
		CreatedBy: rev.CreatedBy, CreatedAt: timestamppb.New(rev.CreatedAt),
	}
	for _, file := range rev.Manifest.Files {
		result.Files = append(result.Files, &agentspb.AgentRevisionFile{
			Path: file.Path, Content: append([]byte(nil), file.Content...), Sha256: file.SHA256,
			Mode: file.Mode, SizeBytes: file.Size,
		})
	}
	for _, function := range rev.Manifest.Functions {
		var parameters *structpb.Struct
		if len(function.Parameters) > 0 {
			parameters, _ = structpb.NewStruct(function.Parameters)
		}
		result.Functions = append(result.Functions, &agentspb.AgentProjectFunction{
			Name: function.Name, Description: function.Description, Path: function.Path,
			ExportName: function.Export, Runtime: string(function.Runtime), Parameters: parameters,
		})
	}
	return result
}

func (s *Server) registerProjectFunctions(
	ctx context.Context,
	interceptor *agenttools.ToolInterceptor,
	sandboxCtx *agenttools.SandboxSessionContext,
	tenantID, agentID, sessionID string,
	agentConfig map[string]interface{},
	tools *[]string,
) error {
	if s.revisionStore == nil || s.projectRuntime == nil || interceptor == nil {
		return nil
	}
	rev, err := s.revisionStore.GetForSession(ctx, tenantID, agentID, sessionID)
	if err != nil {
		if errors.Is(err, agentrevision.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("resolve session agent revision: %w", err)
	}
	if len(rev.Manifest.Functions) == 0 {
		return nil
	}
	if err := agentprojectruntime.ValidateFunctionSandboxPolicy(agentConfig); err != nil {
		return fmt.Errorf("agent revision %s: %w", rev.ID, err)
	}
	if sandboxCtx == nil || sandboxCtx.Manager == nil {
		return fmt.Errorf("agent revision %s declares project functions but the agent sandbox is disabled", rev.ID)
	}
	for _, handler := range agenttools.NewProjectFunctionHandlers(s.projectRuntime, rev, sandboxCtx) {
		if interceptor.IsSyntheticTool(handler.Name()) {
			return fmt.Errorf("project function %q conflicts with a runtime tool", handler.Name())
		}
		// Project functions intentionally take precedence over a tenant-global
		// Function of the same name because the session is pinned to this source
		// revision. Runtime tools may never be shadowed.
		interceptor.RegisterHandler(handler)
		if tools != nil {
			*tools = appendUnique(*tools, handler.Name())
		}
	}
	return nil
}
