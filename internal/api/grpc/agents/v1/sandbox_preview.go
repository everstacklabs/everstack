package v1

import (
	"context"
	"fmt"

	"connectrpc.com/connect"

	sandboxcp "github.com/everstacklabs/everstack/internal/sandbox/controlplane"
	"github.com/everstacklabs/everstack/internal/sandbox/previewtoken"
	agentspb "github.com/everstacklabs/everstack/pkg/grpc/everstack/agents/v1"
)

// SetPreviewSigner configures the HMAC signer used to generate signed preview
// URLs. Must be called before the server starts serving.
func (s *Server) SetPreviewSigner(signer *previewtoken.Signer) {
	s.previewTokenSigner = signer
}

func (s *Server) previewSigner() *previewtoken.Signer {
	return s.previewTokenSigner
}

// GetSandboxPreviewUrl implements AgentsServiceHandler.GetSandboxPreviewUrl via ConnectRPC.
//
// Generates an HMAC-SHA256 signed preview URL for a sandbox port. The token is
// embedded in the URL itself so it can be shared directly without custom headers
// (iframe embeds, link sharing, third-party tools).
//
// REST: POST /v1/sandbox/instances/{sandbox_id}/preview-url
// Body: { "port": 3000, "expires_in_seconds": 3600 }
func (s *Server) GetSandboxPreviewUrl(
	ctx context.Context,
	req *connect.Request[agentspb.GetSandboxPreviewUrlRequest],
) (*connect.Response[agentspb.GetSandboxPreviewUrlResponse], error) {
	if s.sandboxMgr == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errSandboxNotEnabled)
	}

	signer := s.previewSigner()
	if signer == nil {
		return nil, connect.NewError(connect.CodeUnimplemented,
			fmt.Errorf("signed preview URLs are not configured (set EVS_SANDBOX_PREVIEW_TOKEN_SECRET)"))
	}

	sandboxID := req.Msg.GetSandboxId()
	if sandboxID == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("sandbox_id is required"))
	}

	port := int(req.Msg.GetPort())
	if port < 1 || port > 65535 {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("port must be between 1 and 65535"))
	}

	scope, err := s.resolveSandboxTenantInstanceScope(ctx, req.Msg.GetTenantId())
	if err != nil {
		return nil, err
	}
	resolved := s.resolveSandboxInfoInScope(ctx, sandboxID, scope)
	if !resolved.found {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("sandbox not found: %s", sandboxID))
	}
	issued, err := sandboxcp.NewPreviewService(signer, sandboxcp.PreviewURLConfig{
		BaseDomain: s.portExposureBaseDomain,
		TLSEnabled: s.portExposureTLSEnabled,
		ListenPort: s.portExposureListenPort,
	}).IssuePreviewURL(sandboxcp.IssuePreviewURLRequest{
		Scope:            scope.WithSandbox(resolved.sandboxID),
		ShortCode:        resolved.user,
		Port:             port,
		ExpiresInSeconds: req.Msg.GetExpiresInSeconds(),
	})
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&agentspb.GetSandboxPreviewUrlResponse{
		Url:       issued.URL,
		ExpiresAt: issued.ExpiresAt,
	}), nil
}
