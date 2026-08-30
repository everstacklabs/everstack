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

// GetProvider returns detailed information about a specific provider
func (s *Server) GetProvider(ctx context.Context, req *connect.Request[providerspb.GetProviderRequest]) (*connect.Response[providerspb.GetProviderResponse], error) {
	tenantID := contextkeys.GetTenantID(ctx)
	if tenantID == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("tenant context missing"))
	}
	providerName := req.Msg.ProviderName
	if providerName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("provider_name is required"))
	}

	status, err := s.configService.GetProviderStatusForOrg(ctx, tenantID, providerName)
	if err != nil {
		if strings.Contains(err.Error(), "not found") {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &providerspb.GetProviderResponse{
		Provider: convertProviderStatus(status),
	}

	return connect.NewResponse(resp), nil
}
