package v1

// Dynamic sandbox resize (POR-83) — ConnectRPC.
//
// POST /v1/sandbox/instances/{sandbox_id}/resize  (via gRPC Gateway)
// Body: { "cpu_millicores": 2000, "memory_mb": 2048 }
//
// Auth + tenant/instance ownership are enforced by the AgentsService
// interceptor chain (api-key/cookie validation + sandboxOwnershipInterceptor),
// so this method does not re-implement request authentication.
//
// Constraints:
//   - CPU/memory increases: allowed on running sandboxes
//   - CPU/memory decreases: require the sandbox to be stopped first
//   - Disk changes: always require a stop
//   - Out-of-plan limits: rejected (same validation as create)

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

// ResizeSandbox implements AgentsServiceHandler.ResizeSandbox via ConnectRPC.
func (s *Server) ResizeSandbox(
	ctx context.Context,
	req *connect.Request[agentspb.ResizeSandboxRequest],
) (*connect.Response[agentspb.ResizeSandboxResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errSandboxNotEnabled)
	}
	msg := req.Msg
	sandboxID := msg.GetSandboxId()
	if sandboxID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("sandbox_id is required"))
	}

	// Validate request bounds (same plan limits as create).
	if msg.GetDiskMb() > 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("disk resize requires the sandbox to be stopped first"))
	}
	if msg.GetCpuMillicores() < 0 || msg.GetMemoryMb() < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("negative resource values are not allowed"))
	}
	if msg.GetCpuMillicores() > 4000 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("cpu_millicores exceeds maximum (4000)"))
	}
	if msg.GetMemoryMb() > 8192 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("memory_mb exceeds maximum (8192)"))
	}

	scope, err := s.resolveSandboxTenantInstanceScope(ctx, "")
	if err != nil {
		return nil, err
	}
	liveInst, ok := s.sandboxMgr.GetBySandboxIDOrNameInScope(sandboxID, scope)
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("sandbox not found: %s", sandboxID))
	}
	return nil, connect.NewError(connect.CodeUnimplemented,
		fmt.Errorf("sandbox resize is not implemented yet for sandbox %s; runtime resize support is tracked for follow-up", liveInst.ID))
}
