package v1

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	mcppb "github.com/everstacklabs/everstack/pkg/grpc/everstack/mcp/v1"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ─── DiscoverTools ──────────────────────────────────────────────────

func (s *Server) DiscoverTools(ctx context.Context, req *connect.Request[mcppb.DiscoverToolsRequest]) (*connect.Response[mcppb.DiscoverToolsResponse], error) {
	tenantID, err := s.resolveTenantID(ctx, req.Msg.TenantId)
	if err != nil {
		return nil, err
	}

	if req.Msg.ServerId == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("server_id is required"))
	}

	if err := s.ensureServerRegistered(ctx, tenantID, req.Msg.ServerId); err != nil {
		return nil, err
	}

	// Refresh tools from the live connection
	tools, err := s.registry.RefreshTools(ctx, req.Msg.ServerId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("discover tools: %w", err))
	}

	serverID := mustParseInt64(req.Msg.ServerId)

	// Persist discovered tools
	for _, t := range tools {
		if err := s.persistTool(ctx, serverID, t); err != nil {
			logger.Error("failed to persist tool", "error", err, "tool", t.Name)
		}
	}

	// Convert to proto
	var protoTools []*mcppb.McpTool
	for _, t := range tools {
		protoTools = append(protoTools, toolInfoToProto(req.Msg.ServerId, t))
	}

	return connect.NewResponse(&mcppb.DiscoverToolsResponse{Tools: protoTools}), nil
}

// ─── ListFederatedTools ─────────────────────────────────────────────

func (s *Server) ListFederatedTools(ctx context.Context, req *connect.Request[mcppb.ListFederatedToolsRequest]) (*connect.Response[mcppb.ListFederatedToolsResponse], error) {
	tenantID, err := s.resolveTenantID(ctx, req.Msg.TenantId)
	if err != nil {
		return nil, err
	}

	// `s.tenant_id = $1` is the post-2026-05-06 P0 fix: this query
	// previously joined on `mcp_server_tools` and `mcp_servers` with
	// no tenant predicate, so any caller saw every tenant's federated
	// tools. The explicit ServerIds filter (if provided) is layered
	// on top — and the join already enforces ownership through the
	// tenant column on the parent server row.
	query := `SELECT t.id, t.server_id, t.name, t.description, t.input_schema, t.annotations, t.discovered_at, t.last_seen_at
	          FROM mcp_server_tools t
	          JOIN mcp_servers s ON s.id = t.server_id
	          WHERE s.tenant_id = $1 AND s.deleted_at IS NULL AND s.enabled = true`
	args := []interface{}{tenantID}

	if len(req.Msg.ServerIds) > 0 {
		// Filter by server IDs
		placeholders := ""
		for i, sid := range req.Msg.ServerIds {
			if i > 0 {
				placeholders += ","
			}
			args = append(args, mustParseInt64(sid))
			placeholders += fmt.Sprintf("$%d", len(args))
		}
		query += fmt.Sprintf(" AND t.server_id IN (%s)", placeholders)
	}
	if req.Msg.NamePrefix != "" {
		args = append(args, req.Msg.NamePrefix+"%")
		query += fmt.Sprintf(" AND t.name LIKE $%d", len(args))
	}
	query += " ORDER BY t.name"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("list federated tools: %w", err))
	}
	defer rows.Close()

	var tools []*mcppb.McpTool
	for rows.Next() {
		var (
			id              int64
			serverID        int64
			name            string
			description     string
			inputSchemaJSON string
			annotationsJSON string
			discoveredAt    time.Time
			lastSeenAt      time.Time
		)
		if err := rows.Scan(&id, &serverID, &name, &description, &inputSchemaJSON, &annotationsJSON, &discoveredAt, &lastSeenAt); err != nil {
			return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("scan tool row: %w", err))
		}
		tool := &mcppb.McpTool{
			Id:           strconv.FormatInt(id, 10),
			ServerId:     strconv.FormatInt(serverID, 10),
			Name:         name,
			Description:  description,
			DiscoveredAt: timestamppb.New(discoveredAt),
			LastSeenAt:   timestamppb.New(lastSeenAt),
		}
		if inputSchemaJSON != "" && inputSchemaJSON != "{}" {
			var m map[string]interface{}
			if json.Unmarshal([]byte(inputSchemaJSON), &m) == nil {
				tool.InputSchema, _ = structpb.NewStruct(m)
			}
		}
		if annotationsJSON != "" && annotationsJSON != "{}" {
			var m map[string]interface{}
			if json.Unmarshal([]byte(annotationsJSON), &m) == nil {
				tool.Annotations, _ = structpb.NewStruct(m)
			}
		}
		tools = append(tools, tool)
	}

	return connect.NewResponse(&mcppb.ListFederatedToolsResponse{Tools: tools}), nil
}

// ─── CallTool ───────────────────────────────────────────────────────

func (s *Server) CallTool(ctx context.Context, req *connect.Request[mcppb.CallToolRequest]) (*connect.Response[mcppb.CallToolResponse], error) {
	tenantID, err := s.resolveTenantID(ctx, req.Msg.TenantId)
	if err != nil {
		return nil, err
	}

	if req.Msg.ServerId == "" {
		// Auto-resolve server from tool name
		serverID, ok := s.registry.FindServerForTool(req.Msg.ToolName)
		if !ok {
			serverID, err = s.resolveServerIDForToolFromDB(ctx, tenantID, req.Msg.ToolName)
			if err != nil {
				return nil, err
			}
		}
		req.Msg.ServerId = serverID
	}

	if err := s.ensureServerRegistered(ctx, tenantID, req.Msg.ServerId); err != nil {
		return nil, err
	}

	var args map[string]interface{}
	if req.Msg.Arguments != nil {
		args = req.Msg.Arguments.AsMap()
	}

	start := time.Now()
	result, err := s.registry.CallTool(ctx, req.Msg.ServerId, req.Msg.ToolName, args)
	latencyMs := time.Since(start).Milliseconds()

	// Build tool call record
	status := "success"
	output := ""
	errMsg := ""
	if err != nil {
		status = "error"
		errMsg = err.Error()
	} else if result != nil {
		if result.IsError {
			status = "error"
		}
		// Concatenate text content blocks
		for _, c := range result.Content {
			if c.Text != "" {
				if output != "" {
					output += "\n"
				}
				output += c.Text
			}
		}
	}

	// Persist call log
	inputJSON := structToJSON(req.Msg.Arguments)
	_, dbErr := s.db.ExecContext(ctx,
		`INSERT INTO mcp_tool_calls (server_id, tool_name, tenant_id, status, input_args, output, latency_ms, error)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		mustParseInt64(req.Msg.ServerId), req.Msg.ToolName, tenantID, status, inputJSON, output, latencyMs, errMsg,
	)
	if dbErr != nil {
		logger.Error("failed to persist tool call", "error", dbErr)
	}

	// Emit event
	{
		eventType := "mcp.tool.call.completed"
		if err != nil {
			eventType = "mcp.tool.call.failed"
		}
		s.publishEvent(ctx, eventType, map[string]interface{}{
			"server_id":  req.Msg.ServerId,
			"tool_name":  req.Msg.ToolName,
			"tenant_id":  tenantID,
			"status":     status,
			"latency_ms": latencyMs,
		})
	}

	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("call tool: %w", err))
	}

	var protoStatus mcppb.McpToolCallStatus
	switch status {
	case "success":
		protoStatus = mcppb.McpToolCallStatus_MCP_TOOL_CALL_STATUS_SUCCESS
	case "error":
		protoStatus = mcppb.McpToolCallStatus_MCP_TOOL_CALL_STATUS_ERROR
	}

	return connect.NewResponse(&mcppb.CallToolResponse{
		Result: &mcppb.McpToolCallResult{
			ServerId:  req.Msg.ServerId,
			ToolName:  req.Msg.ToolName,
			TenantId:  tenantID,
			Status:    protoStatus,
			InputArgs: req.Msg.Arguments,
			Output:    output,
			Error:     errMsg,
			LatencyMs: latencyMs,
			CreatedAt: timestamppb.Now(),
		},
	}), nil
}
