package v1

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	providerCmd "github.com/everstacklabs/everstack/internal/commands/provider"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	providerspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/providers/v1"
)

// ToggleProvider activates or deactivates a provider
func (s *Server) ToggleProvider(ctx context.Context, req *connect.Request[providerspb.ToggleProviderRequest]) (*connect.Response[providerspb.ToggleProviderResponse], error) {
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	if req.Msg.ProviderName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("provider_name is required"))
	}

	cmd := providerCmd.ToggleProviderCommand{
		TenantID:     tenantID,
		ProviderName: req.Msg.ProviderName,
		IsActive:     req.Msg.IsActive,
		UserID:       contextkeys.GetUserID(ctx),
		TraceID:      "",
		Timestamp:    time.Now(),
	}

	if _, err := s.providerHandler.HandleToggleProvider(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	status, err := s.configService.GetProviderStatusForOrg(ctx, tenantID, req.Msg.ProviderName)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get provider status: %w", err))
	}

	// Trigger async YAML sync
	if s.syncWorker != nil {
		s.syncWorker.TriggerSync()
	}

	resp := &providerspb.ToggleProviderResponse{
		Provider: convertProviderStatus(status),
	}

	return connect.NewResponse(resp), nil
}
