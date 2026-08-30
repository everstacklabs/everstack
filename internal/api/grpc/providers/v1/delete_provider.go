package v1

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	providerspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/providers/v1"
)

// DeleteProviderConfiguration removes a provider configuration
func (s *Server) DeleteProviderConfiguration(ctx context.Context, req *connect.Request[providerspb.DeleteProviderConfigurationRequest]) (*connect.Response[providerspb.DeleteProviderConfigurationResponse], error) {
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	if req.Msg.ProviderName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("provider_name is required"))
	}

	err := s.configService.DeleteConfigurationForOrg(ctx, tenantID, req.Msg.ProviderName)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Trigger async YAML sync
	syncTriggered := false
	if s.syncWorker != nil {
		s.syncWorker.TriggerSync()
		syncTriggered = true
	}

	resp := &providerspb.DeleteProviderConfigurationResponse{
		Success:       true,
		Message:       fmt.Sprintf("Provider '%s' configuration deleted successfully", req.Msg.ProviderName),
		SyncTriggered: syncTriggered,
	}

	return connect.NewResponse(resp), nil
}
