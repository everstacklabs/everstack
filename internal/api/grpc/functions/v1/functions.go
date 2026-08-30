package v1

import (
	"context"
	"encoding/json"
	"errors"

	"connectrpc.com/connect"
	functionscmd "github.com/everstacklabs/everstack/internal/commands/handlers/functions"
	"github.com/everstacklabs/everstack/internal/cqrs"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/utils"
	"github.com/everstacklabs/everstack/internal/query"
	functionsquery "github.com/everstacklabs/everstack/internal/query/handlers/functions"
	functionspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/functions/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

func (s *Server) GetIsolationStatus(ctx context.Context, req *connect.Request[functionspb.GetIsolationStatusRequest]) (*connect.Response[functionspb.GetIsolationStatusResponse], error) {
	available := false
	dockerHost := ""
	message := "Docker is not available. Isolated functions require a running Docker daemon."

	if s.ctx != nil {
		if avail, ok := s.ctx.Value(contextkeys.IsolationAvailable).(bool); ok && avail {
			available = true
			message = "Docker isolation backend is available."
		}
	}

	return connect.NewResponse(&functionspb.GetIsolationStatusResponse{
		Available:  available,
		DockerHost: dockerHost,
		Message:    message,
	}), nil
}

func (s *Server) CreateFunction(ctx context.Context, req *connect.Request[functionspb.CreateFunctionRequest]) (*connect.Response[functionspb.CreateFunctionResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	// Extract tenant ID from context (set by auth middleware) or fall back to request
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}

	// Extract user ID from context
	userID := contextkeys.GetUserID(ctx)

	// Convert proto to domain types
	var webhookCfg *functionscmd.WebhookConfig
	if req.Msg.GetWebhook() != nil {
		webhookCfg = &functionscmd.WebhookConfig{
			URL:       req.Msg.GetWebhook().GetUrl(),
			Method:    req.Msg.GetWebhook().GetMethod(),
			Headers:   req.Msg.GetWebhook().GetHeaders(),
			TimeoutMs: req.Msg.GetWebhook().GetTimeoutMs(),
		}
	}

	var proxyCfg *functionscmd.ProxyConfig
	if req.Msg.GetProxy() != nil {
		proxyCfg = &functionscmd.ProxyConfig{
			BaseURL:         req.Msg.GetProxy().GetBaseUrl(),
			Path:            req.Msg.GetProxy().GetPath(),
			Method:          req.Msg.GetProxy().GetMethod(),
			QueryMapping:    req.Msg.GetProxy().GetQueryMapping(),
			HeaderMapping:   req.Msg.GetProxy().GetHeaderMapping(),
			BodyMapping:     req.Msg.GetProxy().GetBodyMapping(),
			ResponseMapping: req.Msg.GetProxy().GetResponseMapping(),
		}
	}

	var isolatedCfg *functionscmd.IsolatedConfig
	if req.Msg.GetIsolated() != nil {
		isolatedCfg = &functionscmd.IsolatedConfig{
			Runtime:    req.Msg.GetIsolated().GetRuntime(),
			Code:       req.Msg.GetIsolated().GetCode(),
			Packages:   req.Msg.GetIsolated().GetPackages(),
			DockerHost: req.Msg.GetIsolated().GetDockerHost(),
		}
	}

	// Convert parameters from Struct to map
	var params map[string]interface{}
	if req.Msg.GetParameters() != nil {
		params = req.Msg.GetParameters().AsMap()
	}

	// Map execution mode
	mode := executionModeToString(req.Msg.GetMode())

	// Guard: block isolated function creation when Docker is not available
	if mode == "isolated" {
		isolationAvailable := false
		if s.ctx != nil {
			if avail, ok := s.ctx.Value(contextkeys.IsolationAvailable).(bool); ok {
				isolationAvailable = avail
			}
		}
		if !isolationAvailable {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("Docker is not available. Isolated functions require a running Docker daemon."))
		}
	}

	cmd := functionscmd.NewCreateFunctionCommand(
		tenantID,
		req.Msg.GetName(),
		req.Msg.GetDescription(),
		mode,
		params,
		webhookCfg,
		proxyCfg,
		isolatedCfg,
		req.Msg.GetTimeoutMs(),
		req.Msg.GetMemoryMb(),
		req.Msg.GetMaxRetries(),
		userID,
		"", // traceID
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &functionspb.CreateFunctionResponse{
		Function: &functionspb.Function{
			Id:       cmd.ID,
			TenantId: tenantID,
			Name:     req.Msg.GetName(),
			Mode:     req.Msg.GetMode(),
			Enabled:  true,
		},
	}

	return connect.NewResponse(resp), nil
}

func (s *Server) GetFunction(ctx context.Context, req *connect.Request[functionspb.GetFunctionRequest]) (*connect.Response[functionspb.GetFunctionResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	// Extract tenant ID from context or fall back to request
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}

	q := functionsquery.NewGetFunctionByIDQuery(req.Msg.GetId(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if res == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("function not found"))
	}

	// Handle wrapped query.Response
	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}
	if data == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("function not found"))
	}

	rm, ok := data.(*functionsquery.FunctionReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}

	fn := readModelToProto(rm)
	return connect.NewResponse(&functionspb.GetFunctionResponse{Function: fn}), nil
}

func (s *Server) GetFunctionByName(ctx context.Context, req *connect.Request[functionspb.GetFunctionByNameRequest]) (*connect.Response[functionspb.GetFunctionByNameResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	// Extract tenant ID from context or fall back to request
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}

	q := functionsquery.NewGetFunctionByNameQuery(req.Msg.GetName(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if res == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("function not found"))
	}

	// Handle wrapped query.Response
	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}
	if data == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("function not found"))
	}

	rm, ok := data.(*functionsquery.FunctionReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}

	fn := readModelToProto(rm)
	return connect.NewResponse(&functionspb.GetFunctionByNameResponse{Function: fn}), nil
}

func (s *Server) ListFunctions(ctx context.Context, req *connect.Request[functionspb.ListFunctionsRequest]) (*connect.Response[functionspb.ListFunctionsResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	// Extract tenant ID from context or fall back to request
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}

	// Convert optional filters
	var mode *string
	if req.Msg.GetMode() != functionspb.ExecutionMode_EXECUTION_MODE_UNSPECIFIED {
		m := executionModeToString(req.Msg.GetMode())
		mode = &m
	}

	var enabled *bool
	if req.Msg.Enabled != nil {
		enabled = req.Msg.Enabled
	}

	q := functionsquery.NewListFunctionsQuery(
		tenantID,
		mode,
		enabled,
		int(req.Msg.GetLimit()),
		int(req.Msg.GetOffset()),
	)

	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var functions []*functionspb.Function
	if res != nil {
		// Handle both direct result and wrapped query.Response
		var data interface{} = res

		// Check if wrapped in query.Response
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}

		if list, ok := data.([]functionsquery.FunctionReadModel); ok {
			for i := range list {
				functions = append(functions, readModelToProto(&list[i]))
			}
		}
	}

	return connect.NewResponse(&functionspb.ListFunctionsResponse{Functions: functions}), nil
}

func (s *Server) UpdateFunction(ctx context.Context, req *connect.Request[functionspb.UpdateFunctionRequest]) (*connect.Response[functionspb.UpdateFunctionResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	// Extract tenant ID and user ID from context
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	userID := contextkeys.GetUserID(ctx)

	cmd := functionscmd.NewUpdateFunctionCommand(
		req.Msg.GetId(),
		tenantID,
		userID,
		"", // traceID
	)

	// Set optional fields
	if req.Msg.Name != nil {
		cmd.Name = req.Msg.Name
	}
	if req.Msg.Description != nil {
		cmd.Description = req.Msg.Description
	}
	if req.Msg.GetMode() != functionspb.ExecutionMode_EXECUTION_MODE_UNSPECIFIED {
		m := executionModeToString(req.Msg.GetMode())
		cmd.Mode = &m
	}
	if req.Msg.GetParameters() != nil {
		cmd.Parameters = req.Msg.GetParameters().AsMap()
	}
	if req.Msg.GetWebhook() != nil {
		cmd.Webhook = &functionscmd.WebhookConfig{
			URL:       req.Msg.GetWebhook().GetUrl(),
			Method:    req.Msg.GetWebhook().GetMethod(),
			Headers:   req.Msg.GetWebhook().GetHeaders(),
			TimeoutMs: req.Msg.GetWebhook().GetTimeoutMs(),
		}
	}
	if req.Msg.GetProxy() != nil {
		cmd.Proxy = &functionscmd.ProxyConfig{
			BaseURL:         req.Msg.GetProxy().GetBaseUrl(),
			Path:            req.Msg.GetProxy().GetPath(),
			Method:          req.Msg.GetProxy().GetMethod(),
			QueryMapping:    req.Msg.GetProxy().GetQueryMapping(),
			HeaderMapping:   req.Msg.GetProxy().GetHeaderMapping(),
			BodyMapping:     req.Msg.GetProxy().GetBodyMapping(),
			ResponseMapping: req.Msg.GetProxy().GetResponseMapping(),
		}
	}
	if req.Msg.GetIsolated() != nil {
		cmd.Isolated = &functionscmd.IsolatedConfig{
			Runtime:    req.Msg.GetIsolated().GetRuntime(),
			Code:       req.Msg.GetIsolated().GetCode(),
			Packages:   req.Msg.GetIsolated().GetPackages(),
			DockerHost: req.Msg.GetIsolated().GetDockerHost(),
		}
	}

	// Guard: block switching to isolated mode when Docker is not available
	if cmd.Mode != nil && *cmd.Mode == "isolated" {
		isolationAvailable := false
		if s.ctx != nil {
			if avail, ok := s.ctx.Value(contextkeys.IsolationAvailable).(bool); ok {
				isolationAvailable = avail
			}
		}
		if !isolationAvailable {
			return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("Docker is not available. Isolated functions require a running Docker daemon."))
		}
	}

	if req.Msg.TimeoutMs != nil {
		cmd.TimeoutMs = req.Msg.TimeoutMs
	}
	if req.Msg.MemoryMb != nil {
		cmd.MemoryMB = req.Msg.MemoryMb
	}
	if req.Msg.MaxRetries != nil {
		cmd.MaxRetries = req.Msg.MaxRetries
	}
	if req.Msg.Enabled != nil {
		cmd.Enabled = req.Msg.Enabled
	}

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Return the updated function - for now, return a partial response
	// A full implementation would re-fetch the function after update
	return connect.NewResponse(&functionspb.UpdateFunctionResponse{
		Function: &functionspb.Function{
			Id:       req.Msg.GetId(),
			TenantId: tenantID,
		},
	}), nil
}

func (s *Server) DeleteFunction(ctx context.Context, req *connect.Request[functionspb.DeleteFunctionRequest]) (*connect.Response[functionspb.DeleteFunctionResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	// Extract tenant ID and user ID from context
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	userID := contextkeys.GetUserID(ctx)

	cmd := functionscmd.NewDeleteFunctionCommand(
		req.Msg.GetId(),
		tenantID,
		userID,
		"", // traceID
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&functionspb.DeleteFunctionResponse{
		Success: true,
		Message: "function deletion dispatched",
	}), nil
}

// Helper functions

func executionModeToString(mode functionspb.ExecutionMode) string {
	switch mode {
	case functionspb.ExecutionMode_EXECUTION_MODE_WEBHOOK:
		return "webhook"
	case functionspb.ExecutionMode_EXECUTION_MODE_PROXY:
		return "proxy"
	case functionspb.ExecutionMode_EXECUTION_MODE_ISOLATED:
		return "isolated"
	default:
		return ""
	}
}

func stringToExecutionMode(mode string) functionspb.ExecutionMode {
	switch mode {
	case "webhook":
		return functionspb.ExecutionMode_EXECUTION_MODE_WEBHOOK
	case "proxy":
		return functionspb.ExecutionMode_EXECUTION_MODE_PROXY
	case "isolated":
		return functionspb.ExecutionMode_EXECUTION_MODE_ISOLATED
	default:
		return functionspb.ExecutionMode_EXECUTION_MODE_UNSPECIFIED
	}
}

func readModelToProto(rm *functionsquery.FunctionReadModel) *functionspb.Function {
	fn := &functionspb.Function{
		Id:         rm.ID,
		TenantId:   rm.TenantID,
		Name:       rm.Name,
		Mode:       stringToExecutionMode(rm.Mode),
		TimeoutMs:  rm.TimeoutMs,
		MemoryMb:   rm.MemoryMB,
		MaxRetries: rm.MaxRetries,
		Enabled:    rm.Enabled,
		CreatedAt:  utils.ParseTimestamp(rm.CreatedAt),
		UpdatedAt:  utils.ParseTimestamp(rm.UpdatedAt),
	}

	// Handle nullable description
	if rm.Description.Valid {
		fn.Description = rm.Description.String
	}

	// Parse parameters JSONB
	if len(rm.Parameters) > 0 {
		var params map[string]interface{}
		if err := json.Unmarshal(rm.Parameters, &params); err == nil {
			if s, err := structpb.NewStruct(params); err == nil {
				fn.Parameters = s
			}
		}
	}

	// Build webhook config if present
	if rm.WebhookURL.Valid && rm.WebhookURL.String != "" {
		fn.Webhook = &functionspb.WebhookConfig{
			Url:    rm.WebhookURL.String,
			Method: rm.WebhookMethod.String,
		}
		if rm.WebhookTimeoutMs.Valid {
			fn.Webhook.TimeoutMs = rm.WebhookTimeoutMs.Int32
		}
		if len(rm.WebhookHeaders) > 0 {
			var headers map[string]string
			if err := json.Unmarshal(rm.WebhookHeaders, &headers); err == nil {
				fn.Webhook.Headers = headers
			}
		}
	}

	// Build proxy config if present
	if rm.ProxyBaseURL.Valid && rm.ProxyBaseURL.String != "" {
		fn.Proxy = &functionspb.ProxyConfig{
			BaseUrl: rm.ProxyBaseURL.String,
			Path:    rm.ProxyPath.String,
			Method:  rm.ProxyMethod.String,
		}
		if len(rm.ProxyQueryMapping) > 0 {
			var m map[string]string
			if err := json.Unmarshal(rm.ProxyQueryMapping, &m); err == nil {
				fn.Proxy.QueryMapping = m
			}
		}
		if len(rm.ProxyHeaderMapping) > 0 {
			var m map[string]string
			if err := json.Unmarshal(rm.ProxyHeaderMapping, &m); err == nil {
				fn.Proxy.HeaderMapping = m
			}
		}
		if len(rm.ProxyBodyMapping) > 0 {
			var m map[string]string
			if err := json.Unmarshal(rm.ProxyBodyMapping, &m); err == nil {
				fn.Proxy.BodyMapping = m
			}
		}
		if len(rm.ProxyResponseMapping) > 0 {
			var m map[string]string
			if err := json.Unmarshal(rm.ProxyResponseMapping, &m); err == nil {
				fn.Proxy.ResponseMapping = m
			}
		}
	}

	// Build isolated config if present (Phase 2)
	if rm.Runtime.Valid && rm.Runtime.String != "" {
		fn.Isolated = &functionspb.IsolatedConfig{
			Runtime:    rm.Runtime.String,
			Code:       rm.Code.String,
			Packages:   rm.Packages,
			DockerHost: rm.DockerHost.String,
		}
	}

	return fn
}
