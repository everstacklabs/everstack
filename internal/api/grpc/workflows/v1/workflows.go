package v1

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	workflowscmd "github.com/everstacklabs/everstack/internal/commands/handlers/workflows"
	"github.com/everstacklabs/everstack/internal/cqrs"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/utils"
	"github.com/everstacklabs/everstack/internal/query"
	workflowsquery "github.com/everstacklabs/everstack/internal/query/handlers/workflows"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
	"github.com/everstacklabs/everstack/internal/workflows/engine/executors"
	workflowspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/workflows/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) CreateWorkflow(ctx context.Context, req *connect.Request[workflowspb.CreateWorkflowRequest]) (*connect.Response[workflowspb.CreateWorkflowResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	userID := contextkeys.GetUserID(ctx)

	// CE limit: max workflows. WORKFLOWS has no plans.json usage type yet
	// (Phase 1c of editions-and-billing.md), so this stays a CE-only gate —
	// but tenant-scoped, like every other count.
	if err := enterprise.CheckCELimit(ctx, s.db, enterprise.LicenseMonitorFromContext(ctx),
		`SELECT COUNT(*) FROM workflows WHERE tenant_id = $1 AND deleted_at IS NULL`,
		[]interface{}{tenantID}, enterprise.CEMaxWorkflows, "workflow"); err != nil {
		return nil, connect.NewError(connect.CodeResourceExhausted, err)
	}

	// Serialize nodes, edges, viewport to JSON bytes
	nodesJSON, _ := json.Marshal(nodesToMaps(req.Msg.GetNodes()))
	edgesJSON, _ := json.Marshal(edgesToMaps(req.Msg.GetEdges()))
	var viewportJSON []byte
	if req.Msg.GetViewport() != nil {
		viewportJSON, _ = json.Marshal(viewportToMap(req.Msg.GetViewport()))
	}

	cmd := workflowscmd.NewCreateWorkflowCommand(
		tenantID,
		req.Msg.GetName(),
		req.Msg.GetDescription(),
		nodesJSON,
		edgesJSON,
		viewportJSON,
		userID,
		"",
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &workflowspb.CreateWorkflowResponse{
		Workflow: &workflowspb.Workflow{
			Id:       cmd.ID,
			TenantId: tenantID,
			Name:     req.Msg.GetName(),
			Enabled:  false,
			Version:  1,
		},
	}

	return connect.NewResponse(resp), nil
}

func (s *Server) GetWorkflow(ctx context.Context, req *connect.Request[workflowspb.GetWorkflowRequest]) (*connect.Response[workflowspb.GetWorkflowResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}

	q := workflowsquery.NewGetWorkflowByIDQuery(req.Msg.GetId(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if res == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("workflow not found"))
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	if data == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("workflow not found"))
	}

	rm, ok := data.(*workflowsquery.WorkflowReadModel)
	if !ok {
		return nil, connect.NewError(connect.CodeInternal, errors.New("unexpected data type"))
	}

	wf := readModelToProto(rm)
	return connect.NewResponse(&workflowspb.GetWorkflowResponse{Workflow: wf}), nil
}

func (s *Server) ListWorkflows(ctx context.Context, req *connect.Request[workflowspb.ListWorkflowsRequest]) (*connect.Response[workflowspb.ListWorkflowsResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}

	var enabled *bool
	if req.Msg.Enabled != nil {
		enabled = req.Msg.Enabled
	}

	q := workflowsquery.NewListWorkflowsQuery(
		tenantID,
		enabled,
		int(req.Msg.GetLimit()),
		int(req.Msg.GetOffset()),
	)

	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var workflows []*workflowspb.Workflow
	if res != nil {
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}

		if list, ok := data.([]workflowsquery.WorkflowReadModel); ok {
			for i := range list {
				workflows = append(workflows, readModelToProto(&list[i]))
			}
		}
	}

	return connect.NewResponse(&workflowspb.ListWorkflowsResponse{Workflows: workflows}), nil
}

func (s *Server) UpdateWorkflow(ctx context.Context, req *connect.Request[workflowspb.UpdateWorkflowRequest]) (*connect.Response[workflowspb.UpdateWorkflowResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	userID := contextkeys.GetUserID(ctx)

	cmd := workflowscmd.NewUpdateWorkflowCommand(
		req.Msg.GetId(),
		tenantID,
		userID,
		"",
	)

	if req.Msg.Name != nil {
		cmd.Name = req.Msg.Name
	}
	if req.Msg.Description != nil {
		cmd.Description = req.Msg.Description
	}
	if len(req.Msg.GetNodes()) > 0 {
		nodesJSON, _ := json.Marshal(nodesToMaps(req.Msg.GetNodes()))
		cmd.Nodes = nodesJSON
	}
	if len(req.Msg.GetEdges()) > 0 {
		edgesJSON, _ := json.Marshal(edgesToMaps(req.Msg.GetEdges()))
		cmd.Edges = edgesJSON
	}
	if req.Msg.GetViewport() != nil {
		viewportJSON, _ := json.Marshal(viewportToMap(req.Msg.GetViewport()))
		cmd.Viewport = viewportJSON
	}
	if req.Msg.Enabled != nil {
		cmd.Enabled = req.Msg.Enabled
	}

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Brief sleep to allow the event bus projection goroutine to complete
	time.Sleep(100 * time.Millisecond)

	// Re-query the workflow to return the updated version
	q := workflowsquery.NewGetWorkflowByIDQuery(req.Msg.GetId(), tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err == nil && res != nil {
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}
		if rm, ok := data.(*workflowsquery.WorkflowReadModel); ok {
			return connect.NewResponse(&workflowspb.UpdateWorkflowResponse{
				Workflow: readModelToProto(rm),
			}), nil
		}
	}

	// Fallback to minimal response if re-query fails
	logger.WithFields("workflow_id", req.Msg.GetId()).Warn("failed to re-query workflow after update, returning minimal response")
	return connect.NewResponse(&workflowspb.UpdateWorkflowResponse{
		Workflow: &workflowspb.Workflow{
			Id:       req.Msg.GetId(),
			TenantId: tenantID,
		},
	}), nil
}

func (s *Server) DeleteWorkflow(ctx context.Context, req *connect.Request[workflowspb.DeleteWorkflowRequest]) (*connect.Response[workflowspb.DeleteWorkflowResponse], error) {
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	userID := contextkeys.GetUserID(ctx)

	cmd := workflowscmd.NewDeleteWorkflowCommand(
		req.Msg.GetId(),
		tenantID,
		userID,
		"",
	)

	if err := sys.CommandBus.Dispatch(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&workflowspb.DeleteWorkflowResponse{
		Success: true,
		Message: "workflow deletion dispatched",
	}), nil
}

// SaveWorkflowDraft performs a direct SQL UPDATE without emitting CQRS events.
func (s *Server) SaveWorkflowDraft(ctx context.Context, req *connect.Request[workflowspb.SaveWorkflowDraftRequest]) (*connect.Response[workflowspb.SaveWorkflowDraftResponse], error) {
	if s.db == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("database not available"))
	}

	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	workflowID := req.Msg.GetId()
	if workflowID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id is required"))
	}

	// Build dynamic SET clause from provided fields
	setClauses := []string{"updated_at = NOW()"}
	args := []interface{}{}
	argIdx := 1

	if req.Msg.Name != nil {
		setClauses = append(setClauses, fmt.Sprintf("name = $%d", argIdx))
		args = append(args, *req.Msg.Name)
		argIdx++
	}
	if req.Msg.Description != nil {
		setClauses = append(setClauses, fmt.Sprintf("description = $%d", argIdx))
		args = append(args, *req.Msg.Description)
		argIdx++
	}
	if len(req.Msg.GetNodes()) > 0 {
		nodesJSON, _ := json.Marshal(nodesToMaps(req.Msg.GetNodes()))
		setClauses = append(setClauses, fmt.Sprintf("nodes = $%d", argIdx))
		args = append(args, nodesJSON)
		argIdx++
	}
	if len(req.Msg.GetEdges()) > 0 {
		edgesJSON, _ := json.Marshal(edgesToMaps(req.Msg.GetEdges()))
		setClauses = append(setClauses, fmt.Sprintf("edges = $%d", argIdx))
		args = append(args, edgesJSON)
		argIdx++
	}
	if req.Msg.GetViewport() != nil {
		viewportJSON, _ := json.Marshal(viewportToMap(req.Msg.GetViewport()))
		setClauses = append(setClauses, fmt.Sprintf("viewport = $%d", argIdx))
		args = append(args, viewportJSON)
		argIdx++
	}

	// WHERE clause — `workflows` lives in the shared `everstack`
	// schema, so filtering by id alone leaked: any tenant could
	// overwrite another tenant's draft by guessing or harvesting an
	// id. The "schema isolation handles tenancy" comment that used to
	// be here was incorrect; same Pattern B as agent_definitions.
	args = append(args, workflowID, tenantID)
	query := fmt.Sprintf(
		"UPDATE workflows SET %s WHERE id = $%d AND tenant_id = $%d",
		joinStrings(setClauses, ", "),
		argIdx,
		argIdx+1,
	)

	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		logger.WithFields("workflow_id", workflowID, "error", err.Error()).Error("failed to save workflow draft")
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to save draft: %w", err))
	}

	// Re-query to return updated workflow
	var rm workflowsquery.WorkflowReadModel
	err := s.db.GetContext(ctx, &rm,
		"SELECT * FROM workflows WHERE id = $1 AND tenant_id = $2",
		workflowID, tenantID,
	)
	if err != nil {
		logger.WithFields("workflow_id", workflowID, "error", err.Error()).Warn("failed to re-query after draft save")
		return connect.NewResponse(&workflowspb.SaveWorkflowDraftResponse{
			Workflow: &workflowspb.Workflow{Id: workflowID, TenantId: tenantID},
		}), nil
	}

	return connect.NewResponse(&workflowspb.SaveWorkflowDraftResponse{
		Workflow: readModelToProto(&rm),
	}), nil
}

// PublishWorkflow creates a version snapshot in workflow_versions and sets enabled=true.
func (s *Server) PublishWorkflow(ctx context.Context, req *connect.Request[workflowspb.PublishWorkflowRequest]) (*connect.Response[workflowspb.PublishWorkflowResponse], error) {
	if s.db == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("database not available"))
	}

	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	workflowID := req.Msg.GetId()
	if workflowID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id is required"))
	}

	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to begin transaction: %w", err))
	}
	defer tx.Rollback()

	// Read current workflow state — tenant predicate is required
	// because the `workflows` table is in the shared `everstack`
	// schema, not per-tenant.
	var rm workflowsquery.WorkflowReadModel
	err = tx.GetContext(ctx, &rm,
		"SELECT * FROM workflows WHERE id = $1 AND tenant_id = $2 FOR UPDATE",
		workflowID, tenantID,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("workflow not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to read workflow: %w", err))
	}

	// Increment version and enable — tenant-scoped UPDATE.
	newVersion := rm.Version + 1
	_, err = tx.ExecContext(ctx,
		"UPDATE workflows SET version = $1, enabled = true, updated_at = NOW() WHERE id = $2 AND tenant_id = $3",
		newVersion, workflowID, tenantID,
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to update workflow: %w", err))
	}

	// Insert version snapshot
	var description *string
	if rm.Description.Valid {
		description = &rm.Description.String
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO workflow_versions
			(workflow_id, tenant_id, version, name, description, nodes, edges, viewport, variables, published_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NOW())`,
		workflowID, tenantID, newVersion, rm.Name, description,
		rm.Nodes, rm.Edges, rm.Viewport, []byte("{}"),
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to insert version snapshot: %w", err))
	}

	if err := tx.Commit(); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to commit: %w", err))
	}

	// Re-query to return updated workflow (tenant-scoped)
	var updated workflowsquery.WorkflowReadModel
	if err := s.db.GetContext(ctx, &updated,
		"SELECT * FROM workflows WHERE id = $1 AND tenant_id = $2",
		workflowID, tenantID,
	); err != nil {
		logger.WithFields("workflow_id", workflowID, "error", err.Error()).Warn("failed to re-query after publish")
		return connect.NewResponse(&workflowspb.PublishWorkflowResponse{
			Workflow:         &workflowspb.Workflow{Id: workflowID, TenantId: tenantID, Version: newVersion, Enabled: true},
			PublishedVersion: newVersion,
		}), nil
	}

	return connect.NewResponse(&workflowspb.PublishWorkflowResponse{
		Workflow:         readModelToProto(&updated),
		PublishedVersion: newVersion,
	}), nil
}

// UnpublishWorkflow sets enabled=false without creating a version.
func (s *Server) UnpublishWorkflow(ctx context.Context, req *connect.Request[workflowspb.UnpublishWorkflowRequest]) (*connect.Response[workflowspb.UnpublishWorkflowResponse], error) {
	if s.db == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("database not available"))
	}

	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	workflowID := req.Msg.GetId()
	if workflowID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id is required"))
	}

	_, err := s.db.ExecContext(ctx,
		"UPDATE workflows SET enabled = false, updated_at = NOW() WHERE id = $1 AND tenant_id = $2",
		workflowID, tenantID,
	)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to unpublish workflow: %w", err))
	}

	// Re-query to return updated workflow (tenant-scoped)
	var rm workflowsquery.WorkflowReadModel
	if err := s.db.GetContext(ctx, &rm,
		"SELECT * FROM workflows WHERE id = $1 AND tenant_id = $2",
		workflowID, tenantID,
	); err != nil {
		logger.WithFields("workflow_id", workflowID, "error", err.Error()).Warn("failed to re-query after unpublish")
		return connect.NewResponse(&workflowspb.UnpublishWorkflowResponse{
			Workflow: &workflowspb.Workflow{Id: workflowID, TenantId: tenantID, Enabled: false},
		}), nil
	}

	return connect.NewResponse(&workflowspb.UnpublishWorkflowResponse{
		Workflow: readModelToProto(&rm),
	}), nil
}

// joinStrings joins a string slice with a separator (avoids importing strings for one call).
func joinStrings(ss []string, sep string) string {
	result := ""
	for i, s := range ss {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}

// streamSender abstracts the Send method for both Connect and classic gRPC streams.
type streamSender interface {
	Send(*workflowspb.ExecuteWorkflowEvent) error
}

func (s *Server) ExecuteWorkflow(ctx context.Context, req *connect.Request[workflowspb.ExecuteWorkflowRequest], stream *connect.ServerStream[workflowspb.ExecuteWorkflowEvent]) error {
	return s.executeWorkflowInternal(ctx, req, &connectStreamAdapter{stream: stream}, "manual")
}

// connectStreamAdapter adapts a Connect server stream to the streamSender interface.
type connectStreamAdapter struct {
	stream *connect.ServerStream[workflowspb.ExecuteWorkflowEvent]
}

func (a *connectStreamAdapter) Send(msg *workflowspb.ExecuteWorkflowEvent) error {
	return a.stream.Send(msg)
}

func (s *Server) executeWorkflowInternal(ctx context.Context, req *connect.Request[workflowspb.ExecuteWorkflowRequest], stream streamSender, triggerType string) error {
	if triggerType == "" {
		triggerType = "manual"
	}

	// 1. Load workflow via CQRS query
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}

	workflowID := req.Msg.GetWorkflowId()
	if workflowID == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("workflow_id is required"))
	}

	q := workflowsquery.NewGetWorkflowByIDQuery(workflowID, tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}

	// Unwrap the query bus Response wrapper to get the raw result.
	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	if data == nil {
		return connect.NewError(connect.CodeNotFound, errors.New("workflow not found"))
	}

	rm, ok := data.(*workflowsquery.WorkflowReadModel)
	if !ok {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("unexpected query result type %T", data))
	}

	// 2. Build executable graph
	graph, err := engine.BuildGraph(rm.Nodes, rm.Edges)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, err)
	}

	// 3. Create execution context from request messages
	ec := engine.NewExecutionContext()

	// Generate execution identity
	executionID := uuid.New().String()
	correlationID := "wfx_" + uuid.New().String()
	ec.ExecutionID = executionID
	ec.CorrelationID = correlationID
	ec.WorkflowID = workflowID
	ec.TenantID = tenantID

	// Pre-scan the graph for the response node's streaming config.
	// Streaming is only enabled when the response node's config has streaming=true.
	for _, node := range graph.Nodes {
		if node.Type == "response" {
			ec.StreamingEnabled = node.GetConfigBool("streaming")
			logger.WithFields(
				"workflow_id", workflowID,
				"response_node_id", node.ID,
				"streaming_enabled", ec.StreamingEnabled,
				"response_config", fmt.Sprintf("%v", node.Config),
			).Info("workflow execution: response node streaming config")
			break
		}
	}

	for _, msg := range req.Msg.GetMessages() {
		textContent := msg.GetContent()
		role := gw.MessageRole(msg.GetRole())
		ec.Messages = append(ec.Messages, gw.Message{
			Role: role,
			Content: []gw.ContentPart{
				{Type: "text", Text: &textContent},
			},
		})
	}

	for k, v := range req.Msg.GetMetadata() {
		ec.Metadata[k] = v
	}

	// 3a. Insert execution record (running) — use a background context so it
	// doesn't block/fail if the request context is cancelled.
	if s.db != nil {
		inputJSON, _ := json.Marshal(req.Msg.GetMessages())
		metadataJSON, _ := json.Marshal(req.Msg.GetMetadata())
		dbCtx, dbCancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, dbErr := s.db.ExecContext(dbCtx, `INSERT INTO workflow_executions
			(id, workflow_id, tenant_id, correlation_id, trigger_type, status, input_messages, request_metadata, started_at)
			VALUES ($1, $2, $3, $4, $5, 'running', $6, $7, NOW())`,
			executionID, workflowID, tenantID, correlationID, triggerType, inputJSON, metadataJSON)
		dbCancel()
		if dbErr != nil {
			logger.WithFields("execution_id", executionID, "error", dbErr.Error()).
				Error("failed to insert workflow execution record")
		}
	}

	// 4. Set up event emitter that sends proto events on the stream and collects events
	var collectedEvents []engine.ExecutionEvent
	startTime := time.Now()
	firstEvent := true

	ec.OnEvent = func(evt engine.ExecutionEvent) error {
		collectedEvents = append(collectedEvents, evt)

		protoEvt := &workflowspb.ExecuteWorkflowEvent{
			Type:         evt.Type,
			NodeId:       evt.NodeID,
			NodeType:     evt.NodeType,
			NodeLabel:    evt.NodeLabel,
			Error:        evt.Error,
			DurationMs:   evt.DurationMs,
			Timestamp:    evt.Timestamp.UnixMilli(),
			ChunkContent: evt.ChunkContent,
		}
		if len(evt.Data) > 0 {
			protoEvt.Data = make(map[string]string)
			for k, v := range evt.Data {
				protoEvt.Data[k] = v
			}
		} else {
			protoEvt.Data = make(map[string]string)
		}

		// Include execution_id and correlation_id in the first event
		if firstEvent {
			protoEvt.Data["execution_id"] = executionID
			protoEvt.Data["correlation_id"] = correlationID
			firstEvent = false
		}

		return stream.Send(protoEvt)
	}

	// 5. Create engine with deps
	deps := &engine.EngineDeps{
		Registry: s.registry,
		Router:   s.router,
		Ctx:      s.ctx,
	}
	eng := engine.NewEngine(deps)

	// Register all node executors
	eng.RegisterExecutor(&executors.StartExecutor{})
	eng.RegisterExecutor(&executors.AuthExecutor{})
	eng.RegisterExecutor(&executors.RateLimiterExecutor{})
	eng.RegisterExecutor(&executors.CacheExecutor{})
	eng.RegisterExecutor(&executors.RouterExecutor{})
	eng.RegisterExecutor(&executors.LoadBalancerExecutor{Registry: s.registry, Router: s.router})
	eng.RegisterExecutor(&executors.InputGuardrailsExecutor{Registry: s.registry})
	eng.RegisterExecutor(&executors.OutputGuardrailsExecutor{Registry: s.registry})
	eng.RegisterExecutor(&executors.ProviderExecutor{
		Registry: s.registry,
		Router:   s.router,
		ToolLoop: s.toolLoop,
	})
	eng.RegisterExecutor(&executors.FunctionExecutor{ServerCtx: s.ctx})
	eng.RegisterExecutor(&executors.HTTPRequestExecutor{})
	eng.RegisterExecutor(&executors.WebhookExecutor{})
	eng.RegisterExecutor(&executors.IfElseExecutor{})
	eng.RegisterExecutor(&executors.ResponseExecutor{})
	eng.RegisterExecutor(&executors.AgentExecutor{
		ServerCtx:      s.ctx,
		Registry:       s.registry,
		Router:         s.router,
		ToolLoop:       s.toolLoop,
		MemoryStore:    s.memoryStore,
		MemoryEmbedder: s.memoryEmbedder,
		MemoryModel:    s.memoryModel,
		MemoryDim:      s.memoryDim,
		SandboxManager: s.sandboxManager,
		BrowserPool:    s.browserPool,
		StorageServer:  s.storageServer,
	})
	if s.memoryStore != nil && s.memoryEmbedder != nil {
		eng.RegisterExecutor(&executors.MemoryExecutor{
			Store:            s.memoryStore,
			Embedder:         s.memoryEmbedder,
			DefaultModel:     s.memoryModel,
			DefaultDimension: s.memoryDim,
		})
	}
	eng.RegisterExecutor(&executors.TTSExecutor{Registry: s.registry, VoiceCloneRepo: s.voiceCloneRepo, StorageServer: s.storageServer})
	eng.RegisterExecutor(&executors.STTExecutor{Registry: s.registry})
	eng.RegisterExecutor(&executors.VoiceCloneExecutor{Registry: s.registry, VoiceCloneRepo: s.voiceCloneRepo, StorageServer: s.storageServer})

	// 6. Execute — ensure tenant ID is on the context for downstream executors
	if tenantID != "" {
		ctx = contextkeys.WithTenantID(ctx, tenantID)
	}

	logger.WithFields("workflow_id", workflowID, "tenant_id", tenantID, "execution_id", executionID).
		Info("executing workflow")

	execErr := eng.Execute(ctx, graph, ec)
	durationMs := int64(time.Since(startTime).Milliseconds())

	// 7. Update execution record with results — run in a goroutine with a
	// background context so it does NOT block the handler from returning.
	// Blocking here keeps the stream open, which leaves the frontend's
	// isExecuting stuck at true.
	if s.db != nil {
		finalStatus := "completed"
		var errorMsg string
		if execErr != nil {
			finalStatus = "failed"
			errorMsg = execErr.Error()
		}

		outputContent := ec.LastAssistantContent()
		resolvedModel := ec.ResolvedModel
		resolvedProvider := ec.ResolvedProvider

		var promptTokens, completionTokens, totalTokens int32
		if ec.Response != nil {
			promptTokens = int32(ec.Response.Usage.PromptTokens)
			completionTokens = int32(ec.Response.Usage.CompletionTokens)
			totalTokens = int32(ec.Response.Usage.TotalTokens)
		}

		nodeTimingsJSON, _ := json.Marshal(ec.NodeTimings)
		ledgerJSON, _ := ec.Ledger.MarshalJSON()

		type eventRecord struct {
			Type         string            `json:"type"`
			NodeID       string            `json:"node_id"`
			NodeType     string            `json:"node_type"`
			NodeLabel    string            `json:"node_label"`
			Data         map[string]string `json:"data,omitempty"`
			ChunkContent string            `json:"chunk_content,omitempty"`
			Error        string            `json:"error,omitempty"`
			Timestamp    int64             `json:"timestamp"`
			DurationMs   int64             `json:"duration_ms,omitempty"`
		}
		var eventRecords []eventRecord
		for _, evt := range collectedEvents {
			eventRecords = append(eventRecords, eventRecord{
				Type:         evt.Type,
				NodeID:       evt.NodeID,
				NodeType:     evt.NodeType,
				NodeLabel:    evt.NodeLabel,
				Data:         evt.Data,
				ChunkContent: evt.ChunkContent,
				Error:        evt.Error,
				Timestamp:    evt.Timestamp.UnixMilli(),
				DurationMs:   evt.DurationMs,
			})
		}
		eventsJSON, _ := json.Marshal(eventRecords)

		db := s.db
		go func() {
			dbCtx, dbCancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer dbCancel()
			_, dbErr := db.ExecContext(dbCtx, `UPDATE workflow_executions
				SET status=$1, output_content=$2, node_timings=$3, events=$4,
					resolved_model=$5, resolved_provider=$6,
					prompt_tokens=$7, completion_tokens=$8, total_tokens=$9,
					error_message=$10, completed_at=NOW(),
					duration_ms=$11, ledger=$13
				WHERE id=$12`,
				finalStatus, outputContent, nodeTimingsJSON, eventsJSON,
				resolvedModel, resolvedProvider,
				promptTokens, completionTokens, totalTokens,
				errorMsg, int32(durationMs), executionID, ledgerJSON)
			if dbErr != nil {
				logger.WithFields("execution_id", executionID, "error", dbErr.Error()).
					Error("failed to update workflow execution record")
			}
		}()
	}

	if execErr != nil {
		logger.WithFields("workflow_id", workflowID, "execution_id", executionID, "error", execErr.Error()).
			Error("workflow execution failed")
		return connect.NewError(connect.CodeInternal, execErr)
	}

	return nil
}

// ListWorkflowExecutions returns execution history for a workflow.
func (s *Server) ListWorkflowExecutions(ctx context.Context, req *connect.Request[workflowspb.ListWorkflowExecutionsRequest]) (*connect.Response[workflowspb.ListWorkflowExecutionsResponse], error) {
	if s.db == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("database not available"))
	}

	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}

	workflowID := req.Msg.GetWorkflowId()
	if workflowID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("workflow_id is required"))
	}

	pageSize := int(req.Msg.GetPageSize())
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 20
	}
	offset := int(req.Msg.GetOffset())
	if offset < 0 {
		offset = 0
	}

	// Build query with optional status filter
	// Tenant predicate is mandatory — pre-fix, anyone with another
	// tenant's workflow_id could list its executions because the SQL
	// only filtered on workflow_id.
	baseQuery := `FROM workflow_executions WHERE workflow_id = $1 AND tenant_id = $2`
	args := []interface{}{workflowID, tenantID}
	argIdx := 3

	statusFilter := req.Msg.GetStatusFilter()
	if statusFilter != "" {
		baseQuery += fmt.Sprintf(" AND status = $%d", argIdx)
		args = append(args, statusFilter)
		argIdx++
	}

	// Get total count
	var total int32
	countArgs := make([]interface{}, len(args))
	copy(countArgs, args)
	if err := s.db.GetContext(ctx, &total, "SELECT COUNT(*) "+baseQuery, countArgs...); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to count executions: %w", err))
	}

	// Get rows
	selectQuery := fmt.Sprintf(`SELECT id, workflow_id, tenant_id, correlation_id, trigger_type, status,
		input_messages, COALESCE(output_content, '') as output_content, request_metadata,
		COALESCE(resolved_model, '') as resolved_model, COALESCE(resolved_provider, '') as resolved_provider,
		COALESCE(prompt_tokens, 0) as prompt_tokens, COALESCE(completion_tokens, 0) as completion_tokens,
		COALESCE(total_tokens, 0) as total_tokens, COALESCE(error_message, '') as error_message,
		started_at, completed_at, COALESCE(duration_ms, 0) as duration_ms
		%s ORDER BY started_at DESC LIMIT $%d OFFSET $%d`, baseQuery, argIdx, argIdx+1)
	args = append(args, pageSize, offset)

	type execRow struct {
		ID               string       `db:"id"`
		WorkflowID       string       `db:"workflow_id"`
		TenantID         string       `db:"tenant_id"`
		CorrelationID    string       `db:"correlation_id"`
		TriggerType      string       `db:"trigger_type"`
		Status           string       `db:"status"`
		InputMessages    []byte       `db:"input_messages"`
		OutputContent    string       `db:"output_content"`
		RequestMetadata  []byte       `db:"request_metadata"`
		ResolvedModel    string       `db:"resolved_model"`
		ResolvedProvider string       `db:"resolved_provider"`
		PromptTokens     int32        `db:"prompt_tokens"`
		CompletionTokens int32        `db:"completion_tokens"`
		TotalTokens      int32        `db:"total_tokens"`
		ErrorMessage     string       `db:"error_message"`
		StartedAt        time.Time    `db:"started_at"`
		CompletedAt      sql.NullTime `db:"completed_at"`
		DurationMs       int32        `db:"duration_ms"`
	}

	var rows []execRow
	if err := s.db.SelectContext(ctx, &rows, selectQuery, args...); err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to list executions: %w", err))
	}

	var executions []*workflowspb.WorkflowExecution
	for _, row := range rows {
		exec := &workflowspb.WorkflowExecution{
			Id:               row.ID,
			WorkflowId:       row.WorkflowID,
			TenantId:         row.TenantID,
			CorrelationId:    row.CorrelationID,
			TriggerType:      row.TriggerType,
			Status:           row.Status,
			OutputContent:    row.OutputContent,
			ResolvedModel:    row.ResolvedModel,
			ResolvedProvider: row.ResolvedProvider,
			PromptTokens:     row.PromptTokens,
			CompletionTokens: row.CompletionTokens,
			TotalTokens:      row.TotalTokens,
			ErrorMessage:     row.ErrorMessage,
			StartedAt:        row.StartedAt.UnixMilli(),
			DurationMs:       row.DurationMs,
		}
		if row.CompletedAt.Valid {
			exec.CompletedAt = row.CompletedAt.Time.UnixMilli()
		}

		// Parse input messages
		var msgs []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		}
		if json.Unmarshal(row.InputMessages, &msgs) == nil {
			for _, m := range msgs {
				exec.InputMessages = append(exec.InputMessages, &workflowspb.ChatMessage{
					Role:    m.Role,
					Content: m.Content,
				})
			}
		}

		// Parse request metadata
		var meta map[string]string
		if json.Unmarshal(row.RequestMetadata, &meta) == nil {
			exec.RequestMetadata = meta
		}

		// Don't include events_json in list (too large); only in GetWorkflowExecution
		executions = append(executions, exec)
	}

	return connect.NewResponse(&workflowspb.ListWorkflowExecutionsResponse{
		Executions: executions,
		Total:      total,
	}), nil
}

// GetWorkflowExecution returns a single execution with full event log.
func (s *Server) GetWorkflowExecution(ctx context.Context, req *connect.Request[workflowspb.GetWorkflowExecutionRequest]) (*connect.Response[workflowspb.GetWorkflowExecutionResponse], error) {
	if s.db == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("database not available"))
	}

	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}

	executionID := req.Msg.GetExecutionId()
	if executionID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("execution_id is required"))
	}

	type execRow struct {
		ID               string         `db:"id"`
		WorkflowID       string         `db:"workflow_id"`
		TenantID         string         `db:"tenant_id"`
		CorrelationID    string         `db:"correlation_id"`
		TriggerType      string         `db:"trigger_type"`
		Status           string         `db:"status"`
		InputMessages    []byte         `db:"input_messages"`
		OutputContent    sql.NullString `db:"output_content"`
		RequestMetadata  []byte         `db:"request_metadata"`
		Events           []byte         `db:"events"`
		Ledger           []byte         `db:"ledger"`
		ResolvedModel    sql.NullString `db:"resolved_model"`
		ResolvedProvider sql.NullString `db:"resolved_provider"`
		PromptTokens     int32          `db:"prompt_tokens"`
		CompletionTokens int32          `db:"completion_tokens"`
		TotalTokens      int32          `db:"total_tokens"`
		ErrorMessage     sql.NullString `db:"error_message"`
		StartedAt        time.Time      `db:"started_at"`
		CompletedAt      sql.NullTime   `db:"completed_at"`
		DurationMs       sql.NullInt32  `db:"duration_ms"`
	}

	var row execRow
	// Tenant predicate is mandatory — pre-fix, an execution_id alone
	// could fetch a foreign tenant's row (and its events / ledger /
	// input_messages, which contain prompt content).
	err := s.db.GetContext(ctx, &row, `SELECT id, workflow_id, tenant_id, correlation_id, trigger_type, status,
		input_messages, output_content, request_metadata, events, COALESCE(ledger, '[]'::jsonb) as ledger,
		resolved_model, resolved_provider,
		prompt_tokens, completion_tokens, total_tokens,
		error_message, started_at, completed_at, duration_ms
		FROM workflow_executions WHERE id = $1 AND tenant_id = $2`,
		executionID, tenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, connect.NewError(connect.CodeNotFound, errors.New("execution not found"))
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get execution: %w", err))
	}

	exec := &workflowspb.WorkflowExecution{
		Id:               row.ID,
		WorkflowId:       row.WorkflowID,
		TenantId:         row.TenantID,
		CorrelationId:    row.CorrelationID,
		TriggerType:      row.TriggerType,
		Status:           row.Status,
		PromptTokens:     row.PromptTokens,
		CompletionTokens: row.CompletionTokens,
		TotalTokens:      row.TotalTokens,
		StartedAt:        row.StartedAt.UnixMilli(),
	}

	if row.OutputContent.Valid {
		exec.OutputContent = row.OutputContent.String
	}
	if row.ResolvedModel.Valid {
		exec.ResolvedModel = row.ResolvedModel.String
	}
	if row.ResolvedProvider.Valid {
		exec.ResolvedProvider = row.ResolvedProvider.String
	}
	if row.ErrorMessage.Valid {
		exec.ErrorMessage = row.ErrorMessage.String
	}
	if row.CompletedAt.Valid {
		exec.CompletedAt = row.CompletedAt.Time.UnixMilli()
	}
	if row.DurationMs.Valid {
		exec.DurationMs = row.DurationMs.Int32
	}

	// Parse input messages
	var msgs []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if json.Unmarshal(row.InputMessages, &msgs) == nil {
		for _, m := range msgs {
			exec.InputMessages = append(exec.InputMessages, &workflowspb.ChatMessage{
				Role:    m.Role,
				Content: m.Content,
			})
		}
	}

	// Parse request metadata
	var meta map[string]string
	if json.Unmarshal(row.RequestMetadata, &meta) == nil {
		exec.RequestMetadata = meta
	}

	// Include raw events JSON for replay
	if len(row.Events) > 0 {
		exec.EventsJson = string(row.Events)
	}

	// Include execution ledger
	if len(row.Ledger) > 0 {
		exec.LedgerJson = string(row.Ledger)
	}

	return connect.NewResponse(&workflowspb.GetWorkflowExecutionResponse{
		Execution: exec,
	}), nil
}

// ReplayWorkflowExecution re-executes a past execution with the same inputs.
func (s *Server) ReplayWorkflowExecution(ctx context.Context, req *connect.Request[workflowspb.ReplayWorkflowExecutionRequest], stream *connect.ServerStream[workflowspb.ExecuteWorkflowEvent]) error {
	return s.replayWorkflowExecutionInternal(ctx, req, &connectStreamAdapter{stream: stream})
}

func (s *Server) replayWorkflowExecutionInternal(ctx context.Context, req *connect.Request[workflowspb.ReplayWorkflowExecutionRequest], stream streamSender) error {
	if s.db == nil {
		return connect.NewError(connect.CodeInternal, errors.New("database not available"))
	}

	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}

	executionID := req.Msg.GetExecutionId()
	if executionID == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("execution_id is required"))
	}

	// Load the original execution
	type execRow struct {
		WorkflowID      string `db:"workflow_id"`
		InputMessages   []byte `db:"input_messages"`
		RequestMetadata []byte `db:"request_metadata"`
	}
	var row execRow
	err := s.db.GetContext(ctx, &row,
		// Tenant predicate prevents replaying another tenant's
		// execution (and reading its prompt content).
		`SELECT workflow_id, input_messages, request_metadata FROM workflow_executions WHERE id = $1 AND tenant_id = $2`,
		executionID, tenantID)
	if err != nil {
		if err == sql.ErrNoRows {
			return connect.NewError(connect.CodeNotFound, errors.New("execution not found"))
		}
		return connect.NewError(connect.CodeInternal, fmt.Errorf("failed to load execution: %w", err))
	}

	// Deserialize input messages
	var msgs []*workflowspb.ChatMessage
	var rawMsgs []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	if json.Unmarshal(row.InputMessages, &rawMsgs) == nil {
		for _, m := range rawMsgs {
			msgs = append(msgs, &workflowspb.ChatMessage{
				Role:    m.Role,
				Content: m.Content,
			})
		}
	}

	// Deserialize metadata
	meta := make(map[string]string)
	json.Unmarshal(row.RequestMetadata, &meta)

	// Build a new ExecuteWorkflowRequest with the original inputs
	execReq := &connect.Request[workflowspb.ExecuteWorkflowRequest]{
		Msg: &workflowspb.ExecuteWorkflowRequest{
			TenantId:   tenantID,
			WorkflowId: row.WorkflowID,
			Messages:   msgs,
			Metadata:   meta,
		},
	}

	return s.executeWorkflowInternal(ctx, execReq, stream, "replay")
}

// Helper functions

func readModelToProto(rm *workflowsquery.WorkflowReadModel) *workflowspb.Workflow {
	wf := &workflowspb.Workflow{
		Id:        rm.ID,
		TenantId:  rm.TenantID,
		Name:      rm.Name,
		Enabled:   rm.Enabled,
		Version:   rm.Version,
		CreatedAt: utils.ParseTimestamp(rm.CreatedAt),
		UpdatedAt: utils.ParseTimestamp(rm.UpdatedAt),
	}

	if rm.Description.Valid {
		wf.Description = rm.Description.String
	}

	// Parse nodes JSONB into proto WorkflowNode array
	if len(rm.Nodes) > 0 {
		var nodesList []map[string]interface{}
		if err := json.Unmarshal(rm.Nodes, &nodesList); err == nil {
			for _, n := range nodesList {
				node := &workflowspb.WorkflowNode{
					Id:    getStringField(n, "id"),
					Type:  getStringField(n, "type"),
					Label: getStringField(n, "label"),
				}
				if pos, ok := n["position"].(map[string]interface{}); ok {
					node.Position = &workflowspb.WorkflowNodePosition{
						X: getFloatField(pos, "x"),
						Y: getFloatField(pos, "y"),
					}
				}
				if cfg, ok := n["config"].(map[string]interface{}); ok {
					if s, err := structpb.NewStruct(cfg); err == nil {
						node.Config = s
					}
				}
				wf.Nodes = append(wf.Nodes, node)
			}
		}
	}

	// Parse edges JSONB into proto WorkflowEdge array
	if len(rm.Edges) > 0 {
		var edgesList []map[string]interface{}
		if err := json.Unmarshal(rm.Edges, &edgesList); err == nil {
			for _, e := range edgesList {
				edge := &workflowspb.WorkflowEdge{
					Id:           getStringField(e, "id"),
					Source:       getStringField(e, "source"),
					Target:       getStringField(e, "target"),
					SourceHandle: getStringField(e, "source_handle"),
					TargetHandle: getStringField(e, "target_handle"),
				}
				wf.Edges = append(wf.Edges, edge)
			}
		}
	}

	// Parse viewport JSONB
	if len(rm.Viewport) > 0 {
		var vp map[string]interface{}
		if err := json.Unmarshal(rm.Viewport, &vp); err == nil {
			wf.Viewport = &workflowspb.WorkflowViewport{
				X:    getFloatField(vp, "x"),
				Y:    getFloatField(vp, "y"),
				Zoom: getFloatField(vp, "zoom"),
			}
		}
	}

	return wf
}

func nodesToMaps(nodes []*workflowspb.WorkflowNode) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(nodes))
	for _, n := range nodes {
		m := map[string]interface{}{
			"id":    n.GetId(),
			"type":  n.GetType(),
			"label": n.GetLabel(),
		}
		if n.GetPosition() != nil {
			m["position"] = map[string]interface{}{
				"x": n.GetPosition().GetX(),
				"y": n.GetPosition().GetY(),
			}
		}
		if n.GetConfig() != nil {
			m["config"] = n.GetConfig().AsMap()
		}
		result = append(result, m)
	}
	return result
}

func edgesToMaps(edges []*workflowspb.WorkflowEdge) []map[string]interface{} {
	result := make([]map[string]interface{}, 0, len(edges))
	for _, e := range edges {
		m := map[string]interface{}{
			"id":            e.GetId(),
			"source":        e.GetSource(),
			"target":        e.GetTarget(),
			"source_handle": e.GetSourceHandle(),
			"target_handle": e.GetTargetHandle(),
		}
		result = append(result, m)
	}
	return result
}

func viewportToMap(vp *workflowspb.WorkflowViewport) map[string]interface{} {
	return map[string]interface{}{
		"x":    vp.GetX(),
		"y":    vp.GetY(),
		"zoom": vp.GetZoom(),
	}
}

func getStringField(m map[string]interface{}, key string) string {
	if v, ok := m[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func getFloatField(m map[string]interface{}, key string) float64 {
	if v, ok := m[key]; ok {
		switch f := v.(type) {
		case float64:
			return f
		case float32:
			return float64(f)
		case int:
			return float64(f)
		case int64:
			return float64(f)
		}
	}
	return 0
}

func (s *Server) GetWorkflowVersionHistory(ctx context.Context, req *connect.Request[workflowspb.GetWorkflowVersionHistoryRequest]) (*connect.Response[workflowspb.GetWorkflowVersionHistoryResponse], error) {
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	workflowID := req.Msg.GetId()

	// If DB is available, query workflow_versions table (new publish-only versions)
	if s.db != nil {
		type versionRow struct {
			Version     int32   `db:"version"`
			Name        string  `db:"name"`
			Description *string `db:"description"`
			Nodes       []byte  `db:"nodes"`
			Edges       []byte  `db:"edges"`
			Viewport    []byte  `db:"viewport"`
			PublishedAt string  `db:"published_at"`
		}

		var rows []versionRow
		// Tenant predicate prevents history for another tenant's
		// workflow from leaking via a guessed workflow_id.
		err := s.db.SelectContext(ctx, &rows, `
			SELECT version, name, description, nodes, edges, viewport, published_at
			FROM workflow_versions
			WHERE workflow_id = $1 AND tenant_id = $2
			ORDER BY version ASC
		`, workflowID, tenantID)
		if err != nil {
			logger.WithFields("workflow_id", workflowID, "error", err.Error()).
				Error("failed to query workflow_versions")
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query version history: %w", err))
		}

		var versions []*workflowspb.WorkflowVersionEntry
		var prevRow *versionRow
		for i := range rows {
			row := &rows[i]
			entry := &workflowspb.WorkflowVersionEntry{
				Version:   row.Version,
				EventType: "workflow.published",
				Timestamp: utils.ParseTimestamp(row.PublishedAt),
			}

			// Derive change summary by diffing with previous version
			if prevRow == nil {
				entry.Changes = []string{"Initial publish"}
				entry.Details = []*workflowspb.WorkflowChangeDetail{
					{Category: "status", Summary: "Published"},
				}
			} else {
				changes, details := diffVersionSnapshots(prevRow.Name, prevRow.Description, prevRow.Nodes, prevRow.Edges,
					row.Name, row.Description, row.Nodes, row.Edges)
				entry.Changes = changes
				entry.Details = details
			}

			versions = append(versions, entry)
			prevRow = row
		}

		return connect.NewResponse(&workflowspb.GetWorkflowVersionHistoryResponse{
			Versions: versions,
		}), nil
	}

	// Fallback: query CQRS events (legacy path)
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	q := workflowsquery.NewGetWorkflowVersionHistoryQuery(workflowID, tenantID)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var versions []*workflowspb.WorkflowVersionEntry
	if res != nil {
		var data interface{} = res
		if resp, ok := res.(*query.Response); ok {
			data = resp.Data
		}

		if entries, ok := data.([]workflowsquery.WorkflowVersionEntryReadModel); ok {
			for i, evt := range entries {
				versions = append(versions, &workflowspb.WorkflowVersionEntry{
					Version:   int32(i + 1),
					EventType: evt.EventType,
					Timestamp: timestamppb.New(time.Unix(evt.CreatedAt, 0)),
					Changes:   deriveChangeSummary(evt.EventType, evt.Payload),
					Details:   deriveChangeDetails(evt.EventType, evt.Payload),
				})
			}
		}
	}

	return connect.NewResponse(&workflowspb.GetWorkflowVersionHistoryResponse{
		Versions: versions,
	}), nil
}

func (s *Server) GetWorkflowAtVersion(ctx context.Context, req *connect.Request[workflowspb.GetWorkflowAtVersionRequest]) (*connect.Response[workflowspb.GetWorkflowAtVersionResponse], error) {
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}

	version := req.Msg.GetVersion()
	if version < 1 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("version must be >= 1"))
	}

	workflowID := req.Msg.GetId()

	// If DB is available, look up snapshot directly from workflow_versions
	if s.db != nil {
		type snapshotRow struct {
			WorkflowID  string  `db:"workflow_id"`
			TenantID    string  `db:"tenant_id"`
			Version     int32   `db:"version"`
			Name        string  `db:"name"`
			Description *string `db:"description"`
			Nodes       []byte  `db:"nodes"`
			Edges       []byte  `db:"edges"`
			Viewport    []byte  `db:"viewport"`
			PublishedAt string  `db:"published_at"`
		}

		var row snapshotRow
		err := s.db.GetContext(ctx, &row, `
			SELECT workflow_id, tenant_id, version, name, description, nodes, edges, viewport, published_at
			FROM workflow_versions
			WHERE workflow_id = $1 AND version = $2 AND tenant_id = $3
		`, workflowID, version, tenantID)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, connect.NewError(connect.CodeNotFound, errors.New("version not found"))
			}
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to query version: %w", err))
		}

		wf := snapshotRowToProto(row.WorkflowID, row.TenantID, row.Version, row.Name, row.Description, row.Nodes, row.Edges, row.Viewport, row.PublishedAt)

		// Diff with previous version for change details
		var changes []string
		var details []*workflowspb.WorkflowChangeDetail

		var prevRow snapshotRow
		prevErr := s.db.GetContext(ctx, &prevRow, `
			SELECT workflow_id, tenant_id, version, name, description, nodes, edges, viewport, published_at
			FROM workflow_versions
			WHERE workflow_id = $1 AND version = $2 AND tenant_id = $3
		`, workflowID, version-1, tenantID)
		if prevErr != nil {
			// No previous version — this is the first publish
			changes = []string{"Initial publish"}
			details = []*workflowspb.WorkflowChangeDetail{
				{Category: "status", Summary: "Published"},
			}
		} else {
			changes, details = diffVersionSnapshots(prevRow.Name, prevRow.Description, prevRow.Nodes, prevRow.Edges,
				row.Name, row.Description, row.Nodes, row.Edges)
		}

		return connect.NewResponse(&workflowspb.GetWorkflowAtVersionResponse{
			Workflow: wf,
			Details:  details,
			Changes:  changes,
		}), nil
	}

	// Fallback: CQRS event-sourced path (legacy)
	sys, err := cqrs.GetSystemFromContext(ctx)
	if err != nil && s.ctx != nil {
		sys, err = cqrs.GetSystemFromContext(s.ctx)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("CQRS system not available"))
	}

	q := workflowsquery.NewGetWorkflowAtVersionQuery(workflowID, tenantID, version)
	res, err := sys.QueryBus.Execute(ctx, q)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	if res == nil {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("workflow not found"))
	}

	var data interface{} = res
	if resp, ok := res.(*query.Response); ok {
		data = resp.Data
	}

	result, ok := data.(*workflowsquery.GetWorkflowAtVersionResult)
	if !ok || len(result.Events) == 0 {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("version not found"))
	}

	wf := reconstructWorkflowFromEvents(result.Events)
	wf.Version = version

	lastEvt := result.Events[len(result.Events)-1]
	changes := deriveChangeSummary(lastEvt.EventType, lastEvt.Payload)
	details := deriveChangeDetails(lastEvt.EventType, lastEvt.Payload)

	return connect.NewResponse(&workflowspb.GetWorkflowAtVersionResponse{
		Workflow: wf,
		Details:  details,
		Changes:  changes,
	}), nil
}

// reconstructWorkflowFromEvents replays a sequence of events to reconstruct a workflow state.
func reconstructWorkflowFromEvents(events []workflowsquery.WorkflowVersionEntryReadModel) *workflowspb.Workflow {
	state := make(map[string]interface{})

	for _, evt := range events {
		var m map[string]interface{}
		if err := json.Unmarshal(evt.Payload, &m); err != nil {
			continue
		}
		// Merge: overlay each event's fields onto state
		for k, v := range m {
			state[k] = v
		}
	}

	wf := &workflowspb.Workflow{
		Id:       getStringField(state, "id"),
		TenantId: getStringField(state, "tenant_id"),
		Name:     getStringField(state, "name"),
	}

	if desc := getStringField(state, "description"); desc != "" {
		wf.Description = desc
	}

	if enabled, ok := state["enabled"].(bool); ok {
		wf.Enabled = enabled
	}

	// Parse nodes
	if nodesRaw, ok := state["nodes"]; ok {
		nodesData, err := json.Marshal(nodesRaw)
		if err == nil {
			var nodesList []map[string]interface{}
			if json.Unmarshal(nodesData, &nodesList) == nil {
				for _, n := range nodesList {
					node := &workflowspb.WorkflowNode{
						Id:    getStringField(n, "id"),
						Type:  getStringField(n, "type"),
						Label: getStringField(n, "label"),
					}
					if pos, ok := n["position"].(map[string]interface{}); ok {
						node.Position = &workflowspb.WorkflowNodePosition{
							X: getFloatField(pos, "x"),
							Y: getFloatField(pos, "y"),
						}
					}
					if cfg, ok := n["config"].(map[string]interface{}); ok {
						if s, err := structpb.NewStruct(cfg); err == nil {
							node.Config = s
						}
					}
					wf.Nodes = append(wf.Nodes, node)
				}
			}
		}
	}

	// Parse edges
	if edgesRaw, ok := state["edges"]; ok {
		edgesData, err := json.Marshal(edgesRaw)
		if err == nil {
			var edgesList []map[string]interface{}
			if json.Unmarshal(edgesData, &edgesList) == nil {
				for _, e := range edgesList {
					edge := &workflowspb.WorkflowEdge{
						Id:           getStringField(e, "id"),
						Source:       getStringField(e, "source"),
						Target:       getStringField(e, "target"),
						SourceHandle: getStringField(e, "source_handle"),
						TargetHandle: getStringField(e, "target_handle"),
					}
					wf.Edges = append(wf.Edges, edge)
				}
			}
		}
	}

	// Parse viewport
	if vpRaw, ok := state["viewport"]; ok {
		vpData, err := json.Marshal(vpRaw)
		if err == nil {
			var vp map[string]interface{}
			if json.Unmarshal(vpData, &vp) == nil {
				wf.Viewport = &workflowspb.WorkflowViewport{
					X:    getFloatField(vp, "x"),
					Y:    getFloatField(vp, "y"),
					Zoom: getFloatField(vp, "zoom"),
				}
			}
		}
	}

	// Parse timestamps
	if ca := getStringField(state, "created_at"); ca != "" {
		wf.CreatedAt = utils.ParseTimestamp(ca)
	}
	if ua := getStringField(state, "updated_at"); ua != "" {
		wf.UpdatedAt = utils.ParseTimestamp(ua)
	}

	return wf
}

// deriveChangeSummary produces human-readable change summaries from an event payload.
func deriveChangeSummary(eventType string, payload []byte) []string {
	if eventType == "workflow.created" {
		return []string{"Workflow created"}
	}

	var m map[string]interface{}
	if err := json.Unmarshal(payload, &m); err != nil {
		return []string{"Workflow updated"}
	}

	skipKeys := map[string]bool{
		"viewport":       true,
		"id":             true,
		"tenant_id":      true,
		"updated_at":     true,
		"correlation_id": true,
	}

	var changes []string
	for key := range m {
		if skipKeys[key] {
			continue
		}
		switch key {
		case "enabled":
			if val, ok := m[key].(bool); ok {
				if val {
					changes = append(changes, "Published")
				} else {
					changes = append(changes, "Moved to draft")
				}
			}
		case "name":
			changes = append(changes, "Name changed")
		case "description":
			changes = append(changes, "Description updated")
		case "nodes":
			changes = append(changes, "Nodes updated")
		case "edges":
			changes = append(changes, "Connections updated")
		}
	}

	if len(changes) == 0 {
		return []string{"Workflow updated"}
	}
	return changes
}

// extractNodeInfo parses a nodes payload and returns descriptions and IDs.
func extractNodeInfo(nodesRaw interface{}) (descriptions []string, ids []string) {
	data, err := json.Marshal(nodesRaw)
	if err != nil {
		return nil, nil
	}

	var nodes []map[string]interface{}
	if err := json.Unmarshal(data, &nodes); err != nil {
		return nil, nil
	}

	for _, n := range nodes {
		nodeID := getStringField(n, "id")
		if nodeID != "" {
			ids = append(ids, nodeID)
		}

		label := getStringField(n, "label")
		if label == "" {
			label = getStringField(n, "type")
		}
		if label == "" {
			continue
		}

		// Check config for model or functionName
		if cfg, ok := n["config"].(map[string]interface{}); ok {
			if model := getStringField(cfg, "model"); model != "" {
				label = fmt.Sprintf("%s (%s)", label, model)
			} else if funcName := getStringField(cfg, "functionName"); funcName != "" {
				label = fmt.Sprintf("%s (%s)", label, funcName)
			}
		}
		descriptions = append(descriptions, label)
	}
	return descriptions, ids
}

// extractEdgeInfo parses an edges payload and returns the count and IDs.
func extractEdgeInfo(edgesRaw interface{}) (count int, ids []string) {
	data, err := json.Marshal(edgesRaw)
	if err != nil {
		return 0, nil
	}

	var edges []map[string]interface{}
	if err := json.Unmarshal(data, &edges); err != nil {
		return 0, nil
	}
	for _, e := range edges {
		edgeID := getStringField(e, "id")
		if edgeID != "" {
			ids = append(ids, edgeID)
		}
	}
	return len(edges), ids
}

// snapshotRowToProto converts a version snapshot row to a Workflow proto.
// Accepts fields directly to avoid depending on a specific struct type.
func snapshotRowToProto(workflowID, tenantID string, version int32, name string, description *string, nodes, edges, viewport []byte, publishedAt string) *workflowspb.Workflow {
	wf := &workflowspb.Workflow{
		Id:       workflowID,
		TenantId: tenantID,
		Name:     name,
		Version:  version,
		Enabled:  true,
	}

	if description != nil {
		wf.Description = *description
	}

	if ts := utils.ParseTimestamp(publishedAt); ts != nil {
		wf.UpdatedAt = ts
		wf.CreatedAt = ts
	}

	// Parse nodes
	if len(nodes) > 0 {
		var nodesList []map[string]interface{}
		if err := json.Unmarshal(nodes, &nodesList); err == nil {
			for _, n := range nodesList {
				node := &workflowspb.WorkflowNode{
					Id:    getStringField(n, "id"),
					Type:  getStringField(n, "type"),
					Label: getStringField(n, "label"),
				}
				if pos, ok := n["position"].(map[string]interface{}); ok {
					node.Position = &workflowspb.WorkflowNodePosition{
						X: getFloatField(pos, "x"),
						Y: getFloatField(pos, "y"),
					}
				}
				if cfg, ok := n["config"].(map[string]interface{}); ok {
					if s, err := structpb.NewStruct(cfg); err == nil {
						node.Config = s
					}
				}
				wf.Nodes = append(wf.Nodes, node)
			}
		}
	}

	// Parse edges
	if len(edges) > 0 {
		var edgesList []map[string]interface{}
		if err := json.Unmarshal(edges, &edgesList); err == nil {
			for _, e := range edgesList {
				edge := &workflowspb.WorkflowEdge{
					Id:           getStringField(e, "id"),
					Source:       getStringField(e, "source"),
					Target:       getStringField(e, "target"),
					SourceHandle: getStringField(e, "source_handle"),
					TargetHandle: getStringField(e, "target_handle"),
				}
				wf.Edges = append(wf.Edges, edge)
			}
		}
	}

	// Parse viewport
	if len(viewport) > 0 {
		var vp map[string]interface{}
		if err := json.Unmarshal(viewport, &vp); err == nil {
			wf.Viewport = &workflowspb.WorkflowViewport{
				X:    getFloatField(vp, "x"),
				Y:    getFloatField(vp, "y"),
				Zoom: getFloatField(vp, "zoom"),
			}
		}
	}

	return wf
}

// diffVersionSnapshots compares two version snapshots and returns change summaries and details.
func diffVersionSnapshots(prevName string, prevDesc *string, prevNodes, prevEdges []byte,
	currName string, currDesc *string, currNodes, currEdges []byte) ([]string, []*workflowspb.WorkflowChangeDetail) {

	var changes []string
	var details []*workflowspb.WorkflowChangeDetail

	if prevName != currName {
		changes = append(changes, "Name changed")
		details = append(details, &workflowspb.WorkflowChangeDetail{
			Category: "name",
			Summary:  currName,
		})
	}

	prevDescStr := ""
	if prevDesc != nil {
		prevDescStr = *prevDesc
	}
	currDescStr := ""
	if currDesc != nil {
		currDescStr = *currDesc
	}
	if prevDescStr != currDescStr {
		changes = append(changes, "Description updated")
		desc := currDescStr
		if len(desc) > 80 {
			desc = desc[:80] + "..."
		}
		details = append(details, &workflowspb.WorkflowChangeDetail{
			Category: "description",
			Summary:  desc,
		})
	}

	if string(prevNodes) != string(currNodes) {
		nodeDescs, nodeIDs := extractNodeInfo(json.RawMessage(currNodes))
		changes = append(changes, "Nodes updated")
		details = append(details, &workflowspb.WorkflowChangeDetail{
			Category: "nodes",
			Summary:  fmt.Sprintf("%d nodes", len(nodeDescs)),
			Items:    nodeDescs,
			ItemIds:  nodeIDs,
		})
	}

	if string(prevEdges) != string(currEdges) {
		count, edgeIDs := extractEdgeInfo(json.RawMessage(currEdges))
		changes = append(changes, "Connections updated")
		details = append(details, &workflowspb.WorkflowChangeDetail{
			Category: "edges",
			Summary:  fmt.Sprintf("%d connections", count),
			ItemIds:  edgeIDs,
		})
	}

	if len(changes) == 0 {
		changes = []string{"Republished"}
		details = []*workflowspb.WorkflowChangeDetail{
			{Category: "status", Summary: "Republished"},
		}
	}

	return changes, details
}

// deriveChangeDetails produces structured change details from an event payload.
func deriveChangeDetails(eventType string, payload []byte) []*workflowspb.WorkflowChangeDetail {
	if eventType == "workflow.created" {
		return []*workflowspb.WorkflowChangeDetail{
			{Category: "status", Summary: "Workflow created"},
		}
	}

	var m map[string]interface{}
	if err := json.Unmarshal(payload, &m); err != nil {
		return []*workflowspb.WorkflowChangeDetail{
			{Category: "status", Summary: "Workflow updated"},
		}
	}

	skipKeys := map[string]bool{
		"viewport":       true,
		"id":             true,
		"tenant_id":      true,
		"updated_at":     true,
		"correlation_id": true,
	}

	var details []*workflowspb.WorkflowChangeDetail
	for key := range m {
		if skipKeys[key] {
			continue
		}
		switch key {
		case "nodes":
			nodeDescs, nodeIDs := extractNodeInfo(m[key])
			details = append(details, &workflowspb.WorkflowChangeDetail{
				Category: "nodes",
				Summary:  fmt.Sprintf("%d nodes", len(nodeDescs)),
				Items:    nodeDescs,
				ItemIds:  nodeIDs,
			})
		case "edges":
			count, edgeIDs := extractEdgeInfo(m[key])
			details = append(details, &workflowspb.WorkflowChangeDetail{
				Category: "edges",
				Summary:  fmt.Sprintf("%d connections", count),
				ItemIds:  edgeIDs,
			})
		case "name":
			name := ""
			if s, ok := m[key].(string); ok {
				name = s
			}
			details = append(details, &workflowspb.WorkflowChangeDetail{
				Category: "name",
				Summary:  name,
			})
		case "description":
			desc := ""
			if s, ok := m[key].(string); ok {
				desc = s
				if len(desc) > 80 {
					desc = desc[:80] + "…"
				}
			}
			details = append(details, &workflowspb.WorkflowChangeDetail{
				Category: "description",
				Summary:  desc,
			})
		case "enabled":
			if val, ok := m[key].(bool); ok {
				if val {
					details = append(details, &workflowspb.WorkflowChangeDetail{
						Category: "status",
						Summary:  "Published",
					})
				} else {
					details = append(details, &workflowspb.WorkflowChangeDetail{
						Category: "status",
						Summary:  "Moved to draft",
					})
				}
			}
		}
	}

	if len(details) == 0 {
		return []*workflowspb.WorkflowChangeDetail{
			{Category: "status", Summary: "Workflow updated"},
		}
	}
	return details
}
