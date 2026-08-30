package transport

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/service"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/pkg/ctxkeys"
	orgv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/org/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/org/v1/orgconnect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// OrgHandler implements the OrganizationService ConnectRPC interface
type OrgHandler struct {
	authSvc *service.AuthService
	orgSvc  *service.OrganizationService
	cfg     *domain.InternalConfig
}

// NewOrgHandler creates a new organization handler
func NewOrgHandler(cfg *domain.InternalConfig, authSvc *service.AuthService, orgSvc *service.OrganizationService) *OrgHandler {
	return &OrgHandler{
		authSvc: authSvc,
		orgSvc:  orgSvc,
		cfg:     cfg,
	}
}

// mapOrgError converts a service-layer error into a Connect error with the
// correct status code. The deployed org handler previously mapped every error
// to CodeInternal, so authorization failures surfaced to clients as HTTP 500
// and were invisible to observability (audit Phase 1 finding). The service
// returns dynamic fmt.Errorf messages, so we classify by message; unrecognized
// errors keep CodeInternal.
func mapOrgError(err error) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	switch {
	// Target-user-not-member is a NOT FOUND about the subject of the operation;
	// check it before the generic caller "not a member" permission case, which
	// it would otherwise match as a substring.
	case strings.Contains(msg, "target user is not a member"),
		strings.Contains(msg, "user is not a member"),
		strings.Contains(msg, "not found"):
		return connect.NewError(connect.CodeNotFound, err)
	case strings.Contains(msg, "insufficient permissions"),
		strings.Contains(msg, "permissions to"),
		strings.Contains(msg, "not a member"),
		strings.Contains(msg, "cannot remove the instance owner"),
		strings.Contains(msg, "only owners"):
		return connect.NewError(connect.CodePermissionDenied, err)
	case strings.Contains(msg, "already exists"),
		strings.Contains(msg, "already been invited"),
		strings.Contains(msg, "already has an owner"),
		strings.Contains(msg, "already been used"):
		return connect.NewError(connect.CodeAlreadyExists, err)
	case strings.Contains(msg, "expired"),
		strings.Contains(msg, "cannot remove yourself"),
		strings.Contains(msg, "last owner"):
		return connect.NewError(connect.CodeFailedPrecondition, err)
	case strings.Contains(msg, "seat limit"):
		return connect.NewError(connect.CodeResourceExhausted, err)
	case strings.Contains(msg, "invalid"):
		return connect.NewError(connect.CodeInvalidArgument, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}

// getSessionToken extracts the session token from the request header
func (h *OrgHandler) getSessionToken(header http.Header) string {
	cookies := header.Get("Cookie")
	if cookies == "" {
		return ""
	}

	cookieName := h.cfg.Session.CookieName
	req := &http.Request{Header: http.Header{"Cookie": []string{cookies}}}
	cookie, err := req.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// getUserID gets the current user's ID from the session.
// In cloud/tenant mode, falls back to the cloud user ID injected by the tenant middleware.
func (h *OrgHandler) getUserID(ctx context.Context, header http.Header) (uuid.UUID, error) {
	// Try self-hosted session first
	token := h.getSessionToken(header)
	if token != "" {
		user, err := h.authSvc.GetSessionUser(ctx, token)
		if err == nil && user != nil {
			return user.User.ID, nil
		}
	}

	// Fall back to cloud user ID from tenant middleware
	if cloudUID := ctxkeys.CloudUserIDFromContext(ctx); cloudUID != "" {
		uid, err := uuid.Parse(cloudUID)
		if err != nil {
			return uuid.Nil, fmt.Errorf("invalid cloud user ID: %w", err)
		}
		return uid, nil
	}

	return uuid.Nil, fmt.Errorf("not authenticated")
}

// ========== Organization CRUD ==========

// CreateOrganization creates a new organization
func (h *OrgHandler) CreateOrganization(ctx context.Context, req *connect.Request[orgv1.CreateOrganizationRequest]) (*connect.Response[orgv1.CreateOrganizationResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	var slug *string
	if req.Msg.Slug != nil && *req.Msg.Slug != "" {
		slug = req.Msg.Slug
	}

	var billingEmail *string
	if req.Msg.BillingEmail != nil && *req.Msg.BillingEmail != "" {
		billingEmail = req.Msg.BillingEmail
	}

	org, err := h.orgSvc.CreateOrganization(ctx, userID, req.Msg.Name, slug, billingEmail)
	if err != nil {
		logger.WithError(err).Error("org: failed to create organization")
		return nil, mapOrgError(err)
	}

	return connect.NewResponse(&orgv1.CreateOrganizationResponse{
		Organization: domainOrgToProto(org),
	}), nil
}

// GetOrganization gets an organization by ID or slug
func (h *OrgHandler) GetOrganization(ctx context.Context, req *connect.Request[orgv1.GetOrganizationRequest]) (*connect.Response[orgv1.GetOrganizationResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	var org *domain.Organization
	var role string

	switch id := req.Msg.Identifier.(type) {
	case *orgv1.GetOrganizationRequest_Id:
		orgID, err := uuid.Parse(id.Id)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid organization ID"))
		}
		org, role, err = h.orgSvc.GetOrganizationWithRole(ctx, orgID, userID)
		if err != nil {
			return nil, mapOrgError(err)
		}
	case *orgv1.GetOrganizationRequest_Slug:
		orgBySlug, err := h.orgSvc.GetOrganization(ctx, nil, &id.Slug)
		if err != nil {
			return nil, mapOrgError(err)
		}
		if orgBySlug != nil {
			org, role, err = h.orgSvc.GetOrganizationWithRole(ctx, orgBySlug.ID, userID)
			if err != nil {
				return nil, mapOrgError(err)
			}
		}
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("either id or slug must be provided"))
	}

	if org == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("organization not found"))
	}

	return connect.NewResponse(&orgv1.GetOrganizationResponse{
		Organization:    domainOrgToProto(org),
		CurrentUserRole: roleToProto(role),
	}), nil
}

// ListOrganizations lists all organizations for the current user
func (h *OrgHandler) ListOrganizations(ctx context.Context, req *connect.Request[orgv1.ListOrganizationsRequest]) (*connect.Response[orgv1.ListOrganizationsResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	orgs, err := h.orgSvc.ListUserOrganizations(ctx, userID)
	if err != nil {
		return nil, mapOrgError(err)
	}

	protoOrgs := make([]*orgv1.OrganizationWithRole, 0, len(orgs))
	for _, org := range orgs {
		protoOrgs = append(protoOrgs, &orgv1.OrganizationWithRole{
			Organization: domainOrgToProto(&org.Organization),
			Role:         roleToProto(org.Role),
		})
	}

	return connect.NewResponse(&orgv1.ListOrganizationsResponse{
		Organizations: protoOrgs,
	}), nil
}

// CheckOrganizationAccess preserves the shared cloud/self-hosted API contract.
// Self-hosted deployments use their local session and membership model and do
// not apply the cloud organization SSO policy.
func (h *OrgHandler) CheckOrganizationAccess(
	ctx context.Context,
	req *connect.Request[orgv1.CheckOrganizationAccessRequest],
) (*connect.Response[orgv1.CheckOrganizationAccessResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	var organization *domain.Organization
	switch identifier := req.Msg.Identifier.(type) {
	case *orgv1.CheckOrganizationAccessRequest_OrganizationId:
		organizationID, parseErr := uuid.Parse(identifier.OrganizationId)
		if parseErr != nil {
			return nil, connect.NewError(
				connect.CodeInvalidArgument,
				fmt.Errorf("invalid organization ID"),
			)
		}
		organization, _, err = h.orgSvc.GetOrganizationWithRole(
			ctx,
			organizationID,
			userID,
		)
	case *orgv1.CheckOrganizationAccessRequest_OrganizationSlug:
		organization, err = h.orgSvc.GetOrganization(
			ctx,
			nil,
			&identifier.OrganizationSlug,
		)
		if err == nil && organization != nil {
			organization, _, err = h.orgSvc.GetOrganizationWithRole(
				ctx,
				organization.ID,
				userID,
			)
		}
	default:
		return nil, connect.NewError(
			connect.CodeInvalidArgument,
			fmt.Errorf("organization identifier is required"),
		)
	}
	if err != nil {
		return nil, mapOrgError(err)
	}
	if organization == nil {
		return nil, connect.NewError(
			connect.CodeNotFound,
			fmt.Errorf("organization not found"),
		)
	}

	return connect.NewResponse(&orgv1.CheckOrganizationAccessResponse{
		Decision:         orgv1.OrganizationAccessDecision_ORGANIZATION_ACCESS_DECISION_ALLOW,
		OrganizationId:   organization.ID.String(),
		OrganizationSlug: organization.Slug,
	}), nil
}

// GetOrganizationIdentitySettings is cloud-only. Self-hosted authentication
// settings remain deployment configuration, not an organization policy.
func (h *OrgHandler) GetOrganizationIdentitySettings(
	context.Context,
	*connect.Request[orgv1.GetOrganizationIdentitySettingsRequest],
) (*connect.Response[orgv1.GetOrganizationIdentitySettingsResponse], error) {
	return nil, connect.NewError(
		connect.CodeUnimplemented,
		fmt.Errorf("organization identity policies are only available in Everstack Cloud"),
	)
}

// UpdateOrganizationIdentityPolicy is cloud-only. See
// GetOrganizationIdentitySettings for the self-hosted boundary.
func (h *OrgHandler) UpdateOrganizationIdentityPolicy(
	context.Context,
	*connect.Request[orgv1.UpdateOrganizationIdentityPolicyRequest],
) (*connect.Response[orgv1.UpdateOrganizationIdentityPolicyResponse], error) {
	return nil, connect.NewError(
		connect.CodeUnimplemented,
		fmt.Errorf("organization identity policies are only available in Everstack Cloud"),
	)
}

// UpdateOrganization updates an organization's settings
func (h *OrgHandler) UpdateOrganization(ctx context.Context, req *connect.Request[orgv1.UpdateOrganizationRequest]) (*connect.Response[orgv1.UpdateOrganizationResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	orgID, err := uuid.Parse(req.Msg.OrganizationId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid organization ID"))
	}

	org, err := h.orgSvc.UpdateOrganization(ctx, userID, orgID, req.Msg.Name, req.Msg.BillingEmail)
	if err != nil {
		return nil, mapOrgError(err)
	}

	return connect.NewResponse(&orgv1.UpdateOrganizationResponse{
		Organization: domainOrgToProto(org),
	}), nil
}

// DeleteOrganization deletes an organization
func (h *OrgHandler) DeleteOrganization(ctx context.Context, req *connect.Request[orgv1.DeleteOrganizationRequest]) (*connect.Response[orgv1.DeleteOrganizationResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	orgID, err := uuid.Parse(req.Msg.OrganizationId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid organization ID"))
	}

	if err := h.orgSvc.DeleteOrganization(ctx, userID, orgID); err != nil {
		return nil, mapOrgError(err)
	}

	return connect.NewResponse(&orgv1.DeleteOrganizationResponse{
		Success: true,
	}), nil
}

// ========== Member Management ==========

// ListMembers lists all members of an organization
func (h *OrgHandler) ListMembers(ctx context.Context, req *connect.Request[orgv1.ListMembersRequest]) (*connect.Response[orgv1.ListMembersResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	orgID, err := uuid.Parse(req.Msg.OrganizationId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid organization ID"))
	}

	members, err := h.orgSvc.ListMembers(ctx, userID, orgID)
	if err != nil {
		return nil, mapOrgError(err)
	}

	protoMembers := make([]*orgv1.OrganizationMember, 0, len(members))
	for _, m := range members {
		protoMembers = append(protoMembers, domainMemberToProto(&m))
	}

	return connect.NewResponse(&orgv1.ListMembersResponse{
		Members: protoMembers,
	}), nil
}

// InviteMember invites a user to an organization
func (h *OrgHandler) InviteMember(ctx context.Context, req *connect.Request[orgv1.InviteMemberRequest]) (*connect.Response[orgv1.InviteMemberResponse], error) {
	// TODO: Implement invitation system
	return nil, connect.NewError(connect.CodeUnimplemented, fmt.Errorf("invitations not yet implemented"))
}

// UpdateMemberRole updates a member's role
func (h *OrgHandler) UpdateMemberRole(ctx context.Context, req *connect.Request[orgv1.UpdateMemberRoleRequest]) (*connect.Response[orgv1.UpdateMemberRoleResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	orgID, err := uuid.Parse(req.Msg.OrganizationId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid organization ID"))
	}

	targetUserID, err := uuid.Parse(req.Msg.UserId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid user ID"))
	}

	newRole := protoRoleToDomain(req.Msg.Role)

	member, err := h.orgSvc.UpdateMemberRole(ctx, userID, orgID, targetUserID, newRole)
	if err != nil {
		return nil, mapOrgError(err)
	}

	return connect.NewResponse(&orgv1.UpdateMemberRoleResponse{
		Member: domainMemberToProto(member),
	}), nil
}

// RemoveMember removes a member from an organization
func (h *OrgHandler) RemoveMember(ctx context.Context, req *connect.Request[orgv1.RemoveMemberRequest]) (*connect.Response[orgv1.RemoveMemberResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	orgID, err := uuid.Parse(req.Msg.OrganizationId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid organization ID"))
	}

	targetUserID, err := uuid.Parse(req.Msg.UserId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid user ID"))
	}

	if err := h.orgSvc.RemoveMember(ctx, userID, orgID, targetUserID); err != nil {
		return nil, mapOrgError(err)
	}

	return connect.NewResponse(&orgv1.RemoveMemberResponse{
		Success: true,
	}), nil
}

// ========== Workspace Management ==========

// CreateWorkspace creates a new workspace
func (h *OrgHandler) CreateWorkspace(ctx context.Context, req *connect.Request[orgv1.CreateWorkspaceRequest]) (*connect.Response[orgv1.CreateWorkspaceResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	orgID, err := uuid.Parse(req.Msg.OrganizationId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid organization ID"))
	}

	var slug *string
	if req.Msg.Slug != nil && *req.Msg.Slug != "" {
		slug = req.Msg.Slug
	}

	env := protoEnvToDomain(req.Msg.Environment)

	ws, err := h.orgSvc.CreateWorkspace(ctx, userID, orgID, req.Msg.Name, slug, env, req.Msg.GatewayUrl)
	if err != nil {
		return nil, mapOrgError(err)
	}

	return connect.NewResponse(&orgv1.CreateWorkspaceResponse{
		Workspace: domainWorkspaceToProto(ws),
	}), nil
}

// GetWorkspace gets a workspace
func (h *OrgHandler) GetWorkspace(ctx context.Context, req *connect.Request[orgv1.GetWorkspaceRequest]) (*connect.Response[orgv1.GetWorkspaceResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	orgID, err := uuid.Parse(req.Msg.OrganizationId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid organization ID"))
	}

	var ws *domain.Workspace

	switch id := req.Msg.Identifier.(type) {
	case *orgv1.GetWorkspaceRequest_WorkspaceId:
		wsID, err := uuid.Parse(id.WorkspaceId)
		if err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid workspace ID"))
		}
		ws, err = h.orgSvc.GetWorkspace(ctx, userID, orgID, &wsID, nil)
		if err != nil {
			return nil, mapOrgError(err)
		}
	case *orgv1.GetWorkspaceRequest_WorkspaceSlug:
		ws, err = h.orgSvc.GetWorkspace(ctx, userID, orgID, nil, &id.WorkspaceSlug)
		if err != nil {
			return nil, mapOrgError(err)
		}
	default:
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("either workspace_id or workspace_slug must be provided"))
	}

	if ws == nil {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("workspace not found"))
	}

	return connect.NewResponse(&orgv1.GetWorkspaceResponse{
		Workspace: domainWorkspaceToProto(ws),
	}), nil
}

// ListWorkspaces lists all workspaces in an organization
func (h *OrgHandler) ListWorkspaces(ctx context.Context, req *connect.Request[orgv1.ListWorkspacesRequest]) (*connect.Response[orgv1.ListWorkspacesResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	orgID, err := uuid.Parse(req.Msg.OrganizationId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid organization ID"))
	}

	workspaces, err := h.orgSvc.ListWorkspaces(ctx, userID, orgID)
	if err != nil {
		return nil, mapOrgError(err)
	}

	protoWorkspaces := make([]*orgv1.Workspace, 0, len(workspaces))
	for _, ws := range workspaces {
		protoWorkspaces = append(protoWorkspaces, domainWorkspaceToProto(&ws))
	}

	return connect.NewResponse(&orgv1.ListWorkspacesResponse{
		Workspaces: protoWorkspaces,
	}), nil
}

// UpdateWorkspace updates a workspace
func (h *OrgHandler) UpdateWorkspace(ctx context.Context, req *connect.Request[orgv1.UpdateWorkspaceRequest]) (*connect.Response[orgv1.UpdateWorkspaceResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	wsID, err := uuid.Parse(req.Msg.WorkspaceId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid workspace ID"))
	}

	ws, err := h.orgSvc.UpdateWorkspace(ctx, userID, wsID, req.Msg.Name, req.Msg.GatewayUrl)
	if err != nil {
		return nil, mapOrgError(err)
	}

	return connect.NewResponse(&orgv1.UpdateWorkspaceResponse{
		Workspace: domainWorkspaceToProto(ws),
	}), nil
}

// DeleteWorkspace deletes a workspace
func (h *OrgHandler) DeleteWorkspace(ctx context.Context, req *connect.Request[orgv1.DeleteWorkspaceRequest]) (*connect.Response[orgv1.DeleteWorkspaceResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	wsID, err := uuid.Parse(req.Msg.WorkspaceId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid workspace ID"))
	}

	if err := h.orgSvc.DeleteWorkspace(ctx, userID, wsID); err != nil {
		return nil, mapOrgError(err)
	}

	return connect.NewResponse(&orgv1.DeleteWorkspaceResponse{
		Success: true,
	}), nil
}

// ========== Converters ==========

func domainOrgToProto(org *domain.Organization) *orgv1.Organization {
	return &orgv1.Organization{
		Id:           org.ID.String(),
		Slug:         org.Slug,
		Name:         org.Name,
		PlanTier:     org.PlanTier,
		BillingEmail: org.BillingEmail,
		CreatedAt:    timestamppb.New(org.CreatedAt),
		UpdatedAt:    timestamppb.New(org.UpdatedAt),
	}
}

func domainMemberToProto(m *domain.OrganizationMember) *orgv1.OrganizationMember {
	return &orgv1.OrganizationMember{
		Id:             m.ID.String(),
		UserId:         m.UserID.String(),
		OrganizationId: m.OrganizationID.String(),
		Role:           roleToProto(m.Role),
		JoinedAt:       timestamppb.New(m.JoinedAt),
		Email:          m.Email,
		Name:           m.Name,
		AvatarUrl:      m.AvatarURL,
	}
}

func domainWorkspaceToProto(ws *domain.Workspace) *orgv1.Workspace {
	return &orgv1.Workspace{
		Id:             ws.ID.String(),
		OrganizationId: ws.OrganizationID.String(),
		Slug:           ws.Slug,
		Name:           ws.Name,
		Environment:    envToProto(ws.Environment),
		GatewayUrl:     ws.GatewayURL,
		CreatedAt:      timestamppb.New(ws.CreatedAt),
		UpdatedAt:      timestamppb.New(ws.UpdatedAt),
	}
}

func roleToProto(role string) orgv1.MemberRole {
	switch role {
	case domain.RoleOwner:
		return orgv1.MemberRole_MEMBER_ROLE_OWNER
	case domain.RoleAdmin:
		return orgv1.MemberRole_MEMBER_ROLE_ADMIN
	case domain.RoleMember:
		return orgv1.MemberRole_MEMBER_ROLE_MEMBER
	case domain.RoleViewer:
		return orgv1.MemberRole_MEMBER_ROLE_VIEWER
	default:
		return orgv1.MemberRole_MEMBER_ROLE_UNSPECIFIED
	}
}

func protoRoleToDomain(role orgv1.MemberRole) string {
	switch role {
	case orgv1.MemberRole_MEMBER_ROLE_OWNER:
		return domain.RoleOwner
	case orgv1.MemberRole_MEMBER_ROLE_ADMIN:
		return domain.RoleAdmin
	case orgv1.MemberRole_MEMBER_ROLE_MEMBER:
		return domain.RoleMember
	case orgv1.MemberRole_MEMBER_ROLE_VIEWER:
		return domain.RoleViewer
	default:
		return domain.RoleMember
	}
}

func envToProto(env string) orgv1.WorkspaceEnvironment {
	switch env {
	case domain.EnvDevelopment:
		return orgv1.WorkspaceEnvironment_WORKSPACE_ENVIRONMENT_DEVELOPMENT
	case domain.EnvStaging:
		return orgv1.WorkspaceEnvironment_WORKSPACE_ENVIRONMENT_STAGING
	case domain.EnvProduction:
		return orgv1.WorkspaceEnvironment_WORKSPACE_ENVIRONMENT_PRODUCTION
	default:
		return orgv1.WorkspaceEnvironment_WORKSPACE_ENVIRONMENT_UNSPECIFIED
	}
}

func protoEnvToDomain(env orgv1.WorkspaceEnvironment) string {
	switch env {
	case orgv1.WorkspaceEnvironment_WORKSPACE_ENVIRONMENT_DEVELOPMENT:
		return domain.EnvDevelopment
	case orgv1.WorkspaceEnvironment_WORKSPACE_ENVIRONMENT_STAGING:
		return domain.EnvStaging
	case orgv1.WorkspaceEnvironment_WORKSPACE_ENVIRONMENT_PRODUCTION:
		return domain.EnvProduction
	default:
		return domain.EnvDevelopment
	}
}

// ========== Workspace Member Management ==========

func (h *OrgHandler) ListWorkspaceMembers(ctx context.Context, req *connect.Request[orgv1.ListWorkspaceMembersRequest]) (*connect.Response[orgv1.ListWorkspaceMembersResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	wsID, err := uuid.Parse(req.Msg.WorkspaceId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid workspace ID"))
	}

	members, err := h.orgSvc.ListWorkspaceMembers(ctx, userID, wsID)
	if err != nil {
		return nil, mapOrgError(err)
	}

	protoMembers := make([]*orgv1.WorkspaceMember, 0, len(members))
	for _, m := range members {
		protoMembers = append(protoMembers, domainWsMemberToProto(&m))
	}

	return connect.NewResponse(&orgv1.ListWorkspaceMembersResponse{
		Members: protoMembers,
	}), nil
}

func (h *OrgHandler) AddWorkspaceMember(ctx context.Context, req *connect.Request[orgv1.AddWorkspaceMemberRequest]) (*connect.Response[orgv1.AddWorkspaceMemberResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	wsID, err := uuid.Parse(req.Msg.WorkspaceId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid workspace ID"))
	}

	targetUserID, err := uuid.Parse(req.Msg.UserId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid user ID"))
	}

	role := protoWsRoleToDomain(req.Msg.Role)

	member, err := h.orgSvc.AddWorkspaceMember(ctx, userID, wsID, targetUserID, role)
	if err != nil {
		return nil, mapOrgError(err)
	}

	return connect.NewResponse(&orgv1.AddWorkspaceMemberResponse{
		Member: domainWsMemberToProto(member),
	}), nil
}

func (h *OrgHandler) UpdateWorkspaceMemberRole(ctx context.Context, req *connect.Request[orgv1.UpdateWorkspaceMemberRoleRequest]) (*connect.Response[orgv1.UpdateWorkspaceMemberRoleResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	wsID, err := uuid.Parse(req.Msg.WorkspaceId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid workspace ID"))
	}

	targetUserID, err := uuid.Parse(req.Msg.UserId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid user ID"))
	}

	newRole := protoWsRoleToDomain(req.Msg.Role)

	member, err := h.orgSvc.UpdateWorkspaceMemberRole(ctx, userID, wsID, targetUserID, newRole)
	if err != nil {
		return nil, mapOrgError(err)
	}

	return connect.NewResponse(&orgv1.UpdateWorkspaceMemberRoleResponse{
		Member: domainWsMemberToProto(member),
	}), nil
}

func (h *OrgHandler) RemoveWorkspaceMember(ctx context.Context, req *connect.Request[orgv1.RemoveWorkspaceMemberRequest]) (*connect.Response[orgv1.RemoveWorkspaceMemberResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	wsID, err := uuid.Parse(req.Msg.WorkspaceId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid workspace ID"))
	}

	targetUserID, err := uuid.Parse(req.Msg.UserId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid user ID"))
	}

	if err := h.orgSvc.RemoveWorkspaceMember(ctx, userID, wsID, targetUserID); err != nil {
		return nil, mapOrgError(err)
	}

	return connect.NewResponse(&orgv1.RemoveWorkspaceMemberResponse{
		Success: true,
	}), nil
}

func (h *OrgHandler) ListAvailableWorkspaceMembers(ctx context.Context, req *connect.Request[orgv1.ListAvailableWorkspaceMembersRequest]) (*connect.Response[orgv1.ListAvailableWorkspaceMembersResponse], error) {
	userID, err := h.getUserID(ctx, req.Header())
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, err)
	}

	wsID, err := uuid.Parse(req.Msg.WorkspaceId)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid workspace ID"))
	}

	members, err := h.orgSvc.ListAvailableWorkspaceMembers(ctx, userID, wsID)
	if err != nil {
		return nil, mapOrgError(err)
	}

	protoMembers := make([]*orgv1.OrganizationMember, 0, len(members))
	for _, m := range members {
		protoMembers = append(protoMembers, domainMemberToProto(&m))
	}

	return connect.NewResponse(&orgv1.ListAvailableWorkspaceMembersResponse{
		Members: protoMembers,
	}), nil
}

func domainWsMemberToProto(m *domain.WorkspaceMember) *orgv1.WorkspaceMember {
	return &orgv1.WorkspaceMember{
		Id:           m.ID.String(),
		WorkspaceId:  m.WorkspaceID.String(),
		UserId:       m.UserID.String(),
		Role:         wsRoleToProto(m.Role),
		CreatedAt:    timestamppb.New(m.CreatedAt),
		Email:        m.Email,
		Name:         m.Name,
		AvatarUrl:    m.AvatarURL,
		AccessSource: m.AccessSource,
		OrgRole:      roleToProto(m.OrgRole),
	}
}

func wsRoleToProto(role string) orgv1.WorkspaceMemberRole {
	switch role {
	case "admin":
		return orgv1.WorkspaceMemberRole_WORKSPACE_MEMBER_ROLE_ADMIN
	case "member":
		return orgv1.WorkspaceMemberRole_WORKSPACE_MEMBER_ROLE_MEMBER
	case "viewer":
		return orgv1.WorkspaceMemberRole_WORKSPACE_MEMBER_ROLE_VIEWER
	case "owner":
		return orgv1.WorkspaceMemberRole_WORKSPACE_MEMBER_ROLE_ADMIN
	default:
		return orgv1.WorkspaceMemberRole_WORKSPACE_MEMBER_ROLE_UNSPECIFIED
	}
}

func protoWsRoleToDomain(role orgv1.WorkspaceMemberRole) string {
	switch role {
	case orgv1.WorkspaceMemberRole_WORKSPACE_MEMBER_ROLE_ADMIN:
		return domain.WsRoleAdmin
	case orgv1.WorkspaceMemberRole_WORKSPACE_MEMBER_ROLE_MEMBER:
		return domain.WsRoleMember
	case orgv1.WorkspaceMemberRole_WORKSPACE_MEMBER_ROLE_VIEWER:
		return domain.WsRoleViewer
	default:
		return domain.WsRoleMember
	}
}

// NewOrgConnectHandler creates an http.Handler for the OrganizationService
func NewOrgConnectHandler(cfg *domain.InternalConfig, authSvc *service.AuthService, orgSvc *service.OrganizationService) (string, http.Handler) {
	return orgconnect.NewOrganizationServiceHandler(NewOrgHandler(cfg, authSvc, orgSvc))
}
