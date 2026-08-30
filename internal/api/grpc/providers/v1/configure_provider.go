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

// ConfigureProvider creates or updates a provider configuration
func (s *Server) ConfigureProvider(ctx context.Context, req *connect.Request[providerspb.ConfigureProviderRequest]) (*connect.Response[providerspb.ConfigureProviderResponse], error) {
	// Tenant gate. Without this, the handler would happily mutate the
	// shared provider_configurations table on behalf of any caller — the
	// missing-context regression that fed the LLM-key cross-tenant leak.
	// Per-tenant repository scoping (UpsertForOrg etc.) is a follow-up
	// once the service layer threads orgID through; until then this gate
	// at least refuses unauthenticated writes.
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	if req.Msg.ProviderName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("provider_name is required"))
	}
	if len(req.Msg.EnabledModels) == 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("at least one model must be enabled"))
	}

	var customBaseURL *string
	if req.Msg.CustomBaseUrl != "" {
		customBaseURL = &req.Msg.CustomBaseUrl
	}

	cmd := providerCmd.ConfigureProviderCommand{
		TenantID:       tenantID,
		ProviderName:   req.Msg.ProviderName,
		APIKey:         req.Msg.ApiKey,
		APIKeyName:     req.Msg.ApiKeyName,
		APIKeyWeight:   int(req.Msg.ApiKeyWeight),
		EnabledModels:  req.Msg.EnabledModels,
		CustomBaseURL:  customBaseURL,
		CustomSettings: req.Msg.CustomSettings,
		UserID:         contextkeys.GetUserID(ctx),
		TraceID:        "",
		Timestamp:      time.Now(),
	}

	if _, err := s.providerHandler.HandleConfigureProvider(ctx, cmd); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	status, err := s.configService.GetProviderStatusForOrg(ctx, tenantID, req.Msg.ProviderName)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("failed to get provider status: %w", err))
	}

	// Trigger async YAML sync
	syncTriggered := false
	if s.syncWorker != nil {
		s.syncWorker.TriggerSync()
		syncTriggered = true
	}

	resp := &providerspb.ConfigureProviderResponse{
		Provider:      convertProviderStatus(status),
		SyncTriggered: syncTriggered,
	}

	return connect.NewResponse(resp), nil
}
