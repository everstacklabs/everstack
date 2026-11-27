package v1

import (
	"context"
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/sandbox"
	"github.com/everstacklabs/everstack/pkg/tenant"
)

// sandboxIDCarrier is implemented by every generated request message that
// targets a specific sandbox (GetSandboxInstanceRequest, StopSandboxRequest,
// GetSandboxPreviewUrlRequest, etc. all expose GetSandboxId).
type sandboxIDCarrier interface {
	GetSandboxId() string
}

// sandboxOwnershipInterceptor returns a ConnectRPC unary interceptor that
// enforces tenant/instance ownership of the sandbox addressed by a request.
//
// For any unary RPC whose request carries a non-empty sandbox id, it resolves
// that sandbox SCOPED to the caller's tenant (the auth context tenant_id, which
// is the instance_id in cloud multi-instance mode) and rejects with CodeNotFound
// if the caller's tenant does not own it. This closes the cross-tenant /
// cross-instance access vector at the transport layer, uniformly across every
// current and future sandbox RPC method — the ConnectRPC counterpart of the
// RequireSandboxOwnership HTTP middleware.
//
// Rollout: constructors enable enforcement by default. When
// sandboxOwnershipEnforce is explicitly set to false the interceptor only
// audit-logs would-be rejections and lets the request through.
//
// Requests that do not target a sandbox (CreateSandbox, ListSandboxInstances,
// non-sandbox RPCs) pass through untouched.
func (s *Server) sandboxOwnershipInterceptor() connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if err := s.checkSandboxOwnership(ctx, req.Any()); err != nil {
				return nil, err
			}
			return next(ctx, req)
		}
	})
}

// checkSandboxOwnership verifies ownership for any request that addresses a
// sandbox. Requests without a sandbox id and servers without a sandbox manager
// do not require an ownership decision.
func (s *Server) checkSandboxOwnership(ctx context.Context, msg any) error {
	carrier, ok := msg.(sandboxIDCarrier)
	if !ok {
		return nil
	}
	sandboxID := carrier.GetSandboxId()
	if sandboxID == "" || s.sandboxMgr == nil {
		return nil
	}

	requestType := fmt.Sprintf("%T", msg)
	scope, err := s.resolveSandboxTenantInstanceScope(ctx, "")
	if err != nil {
		if s.sandboxOwnershipEnforce {
			return err
		}
		logger.WithFields("request_type", requestType, "sandbox_id", sandboxID, "error", err.Error()).
			Warn("sandbox_ownership(audit): no tenant context; would reject")
		return nil
	}

	if inst := s.resolveSandboxByScope(ctx, sandboxID, scope); inst != nil {
		if err := rewriteSandboxID(msg, inst.ID); err != nil {
			return connect.NewError(connect.CodeInternal, err)
		}
		return nil
	}
	if s.sandboxOwnershipEnforce {
		// Return 404 so the response does not confirm whether a sandbox exists
		// outside the caller's tenant and instance scope.
		return connect.NewError(connect.CodeNotFound, fmt.Errorf("sandbox not found"))
	}
	logger.WithFields(
		"request_type", requestType,
		"sandbox_id", sandboxID,
		"organization_id", scope.OrganizationID,
		"tenant_id", scope.TenantID,
		"instance_id", scope.InstanceID,
		"sandbox_tenant_id", scope.SandboxTenantID(),
	).
		Warn("sandbox_ownership(audit): caller does not own sandbox; would reject")
	return nil
}

// resolveSandboxTenantInstanceScope converts the authenticated tenant context
// into the canonical scope used by sandbox control-plane checks. The current
// sandbox table still keys ownership by an effective tenant ID, which is the
// instance ID in cloud deployments and the tenant/org ID in self-hosted paths.
func (s *Server) resolveSandboxTenantInstanceScope(ctx context.Context, requestTenantID string) (sandbox.TenantInstanceScope, error) {
	authTenantID, err := s.resolveTenantID(ctx, requestTenantID)
	if err != nil {
		return sandbox.TenantInstanceScope{}, err
	}
	scope := sandbox.TenantInstanceScope{
		OrganizationID: authTenantID,
		TenantID:       authTenantID,
		InstanceID:     authTenantID,
	}
	if tc := tenant.ConfigFromContext(ctx); tc != nil {
		if tc.OrganizationID != "" {
			scope.OrganizationID = tc.OrganizationID
		}
		if tc.WorkspaceID != "" {
			scope.TenantID = tc.WorkspaceID
		}
		if tc.InstanceID != "" {
			scope.InstanceID = tc.InstanceID
		}
	}
	if scope.InstanceID == "" {
		scope.InstanceID = authTenantID
	}
	if scope.TenantID == "" {
		scope.TenantID = scope.InstanceID
	}
	if scope.OrganizationID == "" {
		scope.OrganizationID = scope.TenantID
	}
	return scope.Normalize(), nil
}

// sandboxOwnedByTenant reports whether a sandbox addressed by id/name/short_code
// is owned by tenantID. It is retained for compatibility with older call sites;
// new public paths should use sandboxOwnedByScope.
func (s *Server) sandboxOwnedByTenant(ctx context.Context, sandboxID, tenantID string) bool {
	return s.sandboxOwnedByScope(ctx, sandboxID, sandbox.TenantInstanceScope{TenantID: tenantID})
}

func rewriteSandboxID(msg any, sandboxID string) error {
	message, ok := msg.(proto.Message)
	if !ok {
		return fmt.Errorf("sandbox request is not a protobuf message")
	}
	reflected := message.ProtoReflect()
	field := reflected.Descriptor().Fields().ByName(protoreflect.Name("sandbox_id"))
	if field == nil || field.Kind() != protoreflect.StringKind {
		return fmt.Errorf("sandbox request has no writable sandbox_id field")
	}
	reflected.Set(field, protoreflect.ValueOfString(sandboxID))
	return nil
}

// sandboxOwnedByScope reports whether a sandbox addressed by id/name/short_code
// is owned by the canonical caller scope, checking the in-memory map first then
// the DB. Both lookups are scoped and fail closed when scope is incomplete.
func (s *Server) sandboxOwnedByScope(ctx context.Context, sandboxID string, scope sandbox.TenantInstanceScope) bool {
	return s.resolveSandboxByScope(ctx, sandboxID, scope) != nil
}

func (s *Server) resolveSandboxByScope(ctx context.Context, sandboxID string, scope sandbox.TenantInstanceScope) *sandbox.Instance {
	if !scope.HasSandboxTenant() {
		return nil
	}
	if inst, ok := s.sandboxMgr.GetBySandboxIDOrNameInScope(sandboxID, scope); ok {
		return inst
	}
	if inst, err := s.sandboxMgr.LookupInstanceByIDFromDBInScope(ctx, sandboxID, scope); err == nil && inst != nil {
		return inst
	}
	return nil
}
