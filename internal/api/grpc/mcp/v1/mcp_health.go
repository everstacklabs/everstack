package v1

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/mcp"
	mcppb "github.com/everstacklabs/everstack/pkg/grpc/everstack/mcp/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ─── GetMcpServerHealth ─────────────────────────────────────────────

func (s *Server) GetMcpServerHealth(ctx context.Context, req *connect.Request[mcppb.GetMcpServerHealthRequest]) (*connect.Response[mcppb.GetMcpServerHealthResponse], error) {
	tenantID, err := s.resolveTenantID(ctx, req.Msg.TenantId)
	if err != nil {
		return nil, err
	}

	if req.Msg.Id == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("id is required"))
	}

	if err := s.ensureServerRegistered(ctx, tenantID, req.Msg.Id); err != nil {
		return nil, err
	}

	status, checkErr := s.health.CheckServer(ctx, req.Msg.Id)

	protoStatus := healthStatusToProto(status)
	resp := &mcppb.GetMcpServerHealthResponse{
		Status:    protoStatus,
		LastCheck: timestamppb.Now(),
	}
	if checkErr != nil {
		resp.Error = checkErr.Error()
	}

	// Also update DB
	_, _ = s.db.ExecContext(ctx,
		`UPDATE mcp_servers SET health_status = $1, health_last_check = NOW() WHERE id = $2`,
		string(status), mustParseInt64(req.Msg.Id),
	)

	// Emit health event
	s.publishEvent(ctx, "mcp.server.health.changed", map[string]interface{}{
		"server_id": req.Msg.Id,
		"status":    string(status),
	})

	return connect.NewResponse(resp), nil
}

func healthStatusToProto(s mcp.HealthStatus) mcppb.McpServerHealthStatus {
	switch s {
	case mcp.HealthStatusHealthy:
		return mcppb.McpServerHealthStatus_MCP_SERVER_HEALTH_STATUS_HEALTHY
	case mcp.HealthStatusUnhealthy:
		return mcppb.McpServerHealthStatus_MCP_SERVER_HEALTH_STATUS_UNHEALTHY
	default:
		return mcppb.McpServerHealthStatus_MCP_SERVER_HEALTH_STATUS_UNKNOWN
	}
}
