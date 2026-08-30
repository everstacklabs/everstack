package transport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	"github.com/everstacklabs/everstack/internal/auth/oauthserver"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/service"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/pkg/authz/authzhttp"
	"github.com/everstacklabs/everstack/pkg/ctxkeys"
	authv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/auth/v1"
	"github.com/everstacklabs/everstack/pkg/grpc/everstack/auth/v1/authconnect"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SelfHostedAuthHandler implements the self-hosted auth endpoints
type SelfHostedAuthHandler struct {
	*AuthHandler
	selfHostedSvc   *service.SelfHostedAuthService
	cfg             *domain.InternalConfig
	seatLimit       int // From license, 0 = unlimited
	batchCheck      *authzhttp.Handler
	deviceAuth      deviceauth.Store
	organizations   *service.OrganizationService
	deviceTokens    *deviceauth.TokenManager
	externalAuthURL string
	oauthHandler    http.Handler
}

type DeviceAuthorizationDependencies struct {
	Store         deviceauth.Store
	Organizations *service.OrganizationService
	Tokens        *deviceauth.TokenManager
	ExternalURL   string
}

// NewSelfHostedAuthHandler creates a new self-hosted auth handler.
func NewSelfHostedAuthHandler(
	cfg *domain.InternalConfig,
	svc *service.SelfHostedAuthService,
	seatLimit int,
	batchCheck *authzhttp.Handler,
	deviceDeps ...DeviceAuthorizationDependencies,
) *SelfHostedAuthHandler {
	handler := &SelfHostedAuthHandler{
		AuthHandler:   &AuthHandler{svc: svc.AuthService, cfg: cfg},
		selfHostedSvc: svc,
		cfg:           cfg,
		seatLimit:     seatLimit,
		batchCheck:    batchCheck,
	}
	if len(deviceDeps) > 0 {
		handler.deviceAuth = deviceDeps[0].Store
		handler.organizations = deviceDeps[0].Organizations
		handler.deviceTokens = deviceDeps[0].Tokens
		handler.externalAuthURL = deviceDeps[0].ExternalURL
	}
	return handler
}

// ConfigureOAuth mounts the first-party CLI authorization server on the same
// instance origin as the dashboard session. The browser session proves the
// user identity; the selected organization is bound into the one-time code.
func (h *SelfHostedAuthHandler) ConfigureOAuth(store oauthserver.Store, tokens *deviceauth.TokenManager) {
	if h == nil || store == nil || tokens == nil {
		return
	}
	h.oauthHandler = oauthserver.NewHandler(oauthserver.Config{
		Store: store,
		ResolveIdentity: func(r *http.Request) (*oauthserver.Identity, error) {
			user := h.resolveSessionUser(r.Context(), r.Header)
			if user == nil {
				return nil, oauthserver.ErrUnauthenticated
			}
			requestInstance, hasRequestInstance := contextkeys.RequestInstanceScopeFromContext(r.Context())
			return oauthIdentityForRequest(user, requestInstance, hasRequestInstance)
		},
		ResolveInstance: func(r *http.Request) (string, error) {
			requestInstance, ok := contextkeys.RequestInstanceScopeFromContext(r.Context())
			if !ok {
				return "", nil
			}
			return requestInstance.InstanceID, nil
		},
		AuthorizeRefresh: func(ctx context.Context, identity oauthserver.Identity) error {
			userID, err := uuid.Parse(identity.UserID)
			if err != nil {
				return oauthserver.ErrAccessDenied
			}
			orgID, err := uuid.Parse(identity.OrganizationID)
			if err != nil {
				return oauthserver.ErrAccessDenied
			}
			user, err := h.selfHostedSvc.GetUserWithOrganizations(ctx, userID)
			if err != nil {
				return err
			}
			if user == nil {
				return oauthserver.ErrAccessDenied
			}
			for _, org := range user.Organizations {
				if org.ID == orgID {
					return nil
				}
			}
			return oauthserver.ErrAccessDenied
		},
		IssueAccessToken: oauthserver.NewDeviceTokenIssuer(tokens, oauthserver.AccessTokenTTL),
	})
}

func oauthIdentityForRequest(
	user *domain.UserWithOrganizations,
	requestInstance contextkeys.RequestInstanceScope,
	hasRequestInstance bool,
) (*oauthserver.Identity, error) {
	if user == nil || len(user.Organizations) == 0 {
		return nil, oauthserver.ErrAccessDenied
	}
	org := user.Organizations[0]
	instanceID := ""
	if hasRequestInstance {
		instanceID = requestInstance.InstanceID
		matched := false
		for _, candidate := range user.Organizations {
			if candidate.ID.String() == requestInstance.OrganizationID {
				org = candidate
				matched = true
				break
			}
		}
		if !matched {
			return nil, oauthserver.ErrAccessDenied
		}
	}
	return &oauthserver.Identity{
		UserID:           user.User.ID.String(),
		Email:            user.User.Email,
		OrganizationID:   org.ID.String(),
		OrganizationSlug: org.Slug,
		InstanceID:       instanceID,
	}, nil
}

// GetSession returns the current user session if authenticated (overrides base handler for self-hosted)
//
// Resolution order:
//  1. Local self-hosted session cookie → DB lookup (most direct, fully populated)
//  2. Cloud-relay context (cloudUserID + tenant org from tenant_middleware) → DB lookup,
//     fallback to context-only synthesis. The tenant middleware has already
//     validated the cookie against the cloud DB; if our local DB doesn't have
//     a corresponding row, we still know the user is authenticated and which
//     org they're in, so we synthesize a UserWithOrganizations rather than
//     telling the FE the session is dead.
//
// Without step 2's synthesis, cloud-relay users on tenant instances whose
// local services-bundle DB does not yet have the cloud user/org rows hit a
// cascade: GetUserWithOrganizations returns nil → GetSession returns
// Authenticated:false → AuthGuard would redirect, OR the SPA renders with an
// empty session.user and useOrganizationId returns "" → sandbox queries are
// disabled → the overview tile reads "Sandbox runtime is not available".
func (h *SelfHostedAuthHandler) GetSession(ctx context.Context, req *connect.Request[authv1.GetSessionRequest]) (*connect.Response[authv1.GetSessionResponse], error) {
	user := h.resolveSessionUser(ctx, req.Header())
	if user == nil {
		return connect.NewResponse(&authv1.GetSessionResponse{
			Authenticated: false,
		}), nil
	}

	protoUser := userToProto(&user.User, user.Organizations)

	return connect.NewResponse(&authv1.GetSessionResponse{
		Authenticated: true,
		User:          protoUser,
	}), nil
}

// resolveSessionUser is shared by the dashboard and device approval. It
// accepts an instance-local session (including one minted by the OIDC auth
// overhaul) and retains the verified cloud-relay fallback during migration.
func (h *SelfHostedAuthHandler) resolveSessionUser(ctx context.Context, header http.Header) *domain.UserWithOrganizations {
	// 1. Local cookie path
	token := h.getSessionToken(header)

	var user *domain.UserWithOrganizations

	if token != "" {
		var err error
		user, err = h.selfHostedSvc.GetSessionUser(ctx, token)
		if err != nil {
			logger.WithError(err).Info("auth: session check failed")
		}
	}

	// 1b. Explicit instance sign-out short-circuits every fallback below.
	//     The browser still holds the cloud's parent-domain cookie, so
	//     without this the cloud-relay paths re-authenticate the user on the
	//     very next request and sign-out reads as a no-op. Returning nil here
	//     makes the SPA's AuthGuard bounce to the cloud, which is where a
	//     signed-out user belongs; re-entering through the relay mints a
	//     fresh session and clears the marker.
	if user == nil && instanceSignedOut(header) {
		return nil
	}

	// 2. Cloud-relay path: tenant middleware already validated the cloud
	//    session and pushed the cloud user id plus the verified request instance
	//    into context. The instance scope carries its owning organization.
	cloudUID := ctxkeys.CloudUserIDFromContext(ctx)
	tenantOrgID := sessionOrganizationID(ctx)

	if user == nil && cloudUID != "" {
		uid, err := uuid.Parse(cloudUID)
		if err == nil {
			user, err = h.selfHostedSvc.GetUserWithOrganizations(ctx, uid)
			if err != nil {
				logger.WithError(err).Info("auth: cloud user lookup failed")
			}
		}
	}

	// 3. Synthesize from cloud-relay context when the local DB doesn't have
	//    the user. The middleware's validation is the source of truth here —
	//    the local DB is an out-of-sync replica from the FE's perspective.
	if user == nil && cloudUID != "" && tenantOrgID != "" {
		uid, err := uuid.Parse(cloudUID)
		if err == nil {
			orgUUID, parseErr := uuid.Parse(tenantOrgID)
			if parseErr == nil {
				user = &domain.UserWithOrganizations{
					User: domain.User{ID: uid},
					Organizations: []domain.OrganizationMembership{
						{ID: orgUUID, Role: "member"},
					},
				}
				logger.WithFields("cloud_user_id", cloudUID, "tenant_org_id", tenantOrgID).
					Info("auth: synthesized session from tenant context (local DB lookup miss)")
			}
		}
	}

	if user == nil {
		return nil
	}

	// 4. Final safety net for users whose local row exists but predates the
	//    org feature. In cloud-relay mode the tenant_org context already
	//    seeded an org, so this only kicks in for purely-self-hosted sessions.
	if len(user.Organizations) == 0 {
		if tenantOrgID != "" {
			if orgUUID, err := uuid.Parse(tenantOrgID); err == nil {
				user.Organizations = []domain.OrganizationMembership{
					{ID: orgUUID, Role: "member"},
				}
			}
		} else {
			logger.Info("auth: session user has no organization, creating default organization")
			if err := h.selfHostedSvc.EnsureUserHasOrganization(ctx, user.User.ID, user); err != nil {
				logger.WithError(err).Warn("auth: failed to create default organization for session user")
			}
		}
	}

	return user
}

func sessionOrganizationID(ctx context.Context) string {
	if requestInstance, ok := contextkeys.RequestInstanceScopeFromContext(ctx); ok {
		return requestInstance.OrganizationID
	}
	return ctxkeys.TenantIDFromContext(ctx)
}

// handleAuthzBatchCheck resolves the same authenticated identity used by the
// dashboard, installs its tenant and role in context, and delegates to the
// authorization engine.
func (h *SelfHostedAuthHandler) handleAuthzBatchCheck(w http.ResponseWriter, r *http.Request) {
	if h.batchCheck == nil {
		http.NotFound(w, r)
		return
	}

	ctx := r.Context()
	user := h.resolveSessionUser(ctx, r.Header)
	if user == nil {
		http.Error(w, "unauthenticated", http.StatusUnauthorized)
		return
	}

	tenant := contextkeys.GetTenantID(ctx)
	role := ""
	if len(user.Organizations) > 0 {
		if tenant == "" {
			tenant = user.Organizations[0].ID.String()
		}
		role = user.Organizations[0].Role
	}

	ctx = contextkeys.WithUserID(ctx, user.User.ID.String())
	if tenant != "" {
		ctx = contextkeys.WithTenantID(ctx, tenant)
	}
	if role != "" {
		ctx = contextkeys.WithUserRole(ctx, role)
	}
	h.batchCheck.BatchCheck(w, r.WithContext(ctx))
}

// GetAuthMode returns the authentication mode (cloud vs self-hosted)
func (h *SelfHostedAuthHandler) GetAuthMode(ctx context.Context, req *connect.Request[authv1.GetAuthModeRequest]) (*connect.Response[authv1.GetAuthModeResponse], error) {
	mode, hasUsers, err := h.selfHostedSvc.GetAuthMode(ctx)
	if err != nil {
		logger.WithError(err).Error("auth: failed to get auth mode")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	var protoMode authv1.AuthMode
	switch mode {
	case service.AuthModeCloud:
		protoMode = authv1.AuthMode_AUTH_MODE_CLOUD
	case service.AuthModeSelfHosted:
		protoMode = authv1.AuthMode_AUTH_MODE_SELF_HOSTED
	default:
		protoMode = authv1.AuthMode_AUTH_MODE_UNSPECIFIED
	}

	return connect.NewResponse(&authv1.GetAuthModeResponse{
		Mode:     protoMode,
		HasUsers: hasUsers,
	}), nil
}

// Register creates the first admin user (becomes instance owner)
func (h *SelfHostedAuthHandler) Register(ctx context.Context, req *connect.Request[authv1.RegisterRequest]) (*connect.Response[authv1.RegisterResponse], error) {
	email := req.Msg.Email
	password := req.Msg.Password
	name := req.Msg.Name

	if email == "" || password == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("email and password are required"))
	}

	ipAddress := getClientIP(req.Header())
	userAgent := req.Header().Get("User-Agent")

	userWithOrgs, session, err := h.selfHostedSvc.Register(ctx, email, password, name, &ipAddress, &userAgent)
	if err != nil {
		if errors.Is(err, service.ErrInstanceHasOwner) {
			return nil, connect.NewError(connect.CodeAlreadyExists, err)
		}
		if errors.Is(err, service.ErrSelfHostedOnly) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		logger.WithError(err).Error("auth: registration failed")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoUser := userToProto(&userWithOrgs.User, userWithOrgs.Organizations)

	resp := connect.NewResponse(&authv1.RegisterResponse{
		Success:      true,
		User:         protoUser,
		SessionToken: session.Token,
	})

	// Set session cookie in response header
	cookie := h.buildSessionCookie(session)
	resp.Header().Add("Set-Cookie", cookie)

	return resp, nil
}

// Login authenticates with email and password
func (h *SelfHostedAuthHandler) Login(ctx context.Context, req *connect.Request[authv1.LoginRequest]) (*connect.Response[authv1.LoginResponse], error) {
	email := req.Msg.Email
	password := req.Msg.Password

	if email == "" || password == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("email and password are required"))
	}

	ipAddress := getClientIP(req.Header())
	userAgent := req.Header().Get("User-Agent")

	userWithOrgs, session, err := h.selfHostedSvc.Login(ctx, email, password, &ipAddress, &userAgent)
	if err != nil {
		if errors.Is(err, service.ErrInvalidCredentials) {
			return nil, connect.NewError(connect.CodeUnauthenticated, err)
		}
		logger.WithError(err).Error("auth: login failed")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoUser := userToProto(&userWithOrgs.User, userWithOrgs.Organizations)

	resp := connect.NewResponse(&authv1.LoginResponse{
		Success:      true,
		User:         protoUser,
		SessionToken: session.Token,
	})

	// Set session cookie in response header
	cookie := h.buildSessionCookie(session)
	resp.Header().Add("Set-Cookie", cookie)

	return resp, nil
}

// RequestMagicLink sends a magic link email for passwordless login
func (h *SelfHostedAuthHandler) RequestMagicLink(ctx context.Context, req *connect.Request[authv1.RequestMagicLinkRequest]) (*connect.Response[authv1.RequestMagicLinkResponse], error) {
	email := req.Msg.Email
	if email == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("email is required"))
	}

	message, err := h.selfHostedSvc.RequestMagicLink(ctx, email)
	if err != nil {
		logger.WithError(err).Error("auth: magic link request failed")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&authv1.RequestMagicLinkResponse{
		Success: true,
		Message: message,
	}), nil
}

// VerifyMagicLink verifies a magic link token and creates a session
func (h *SelfHostedAuthHandler) VerifyMagicLink(ctx context.Context, req *connect.Request[authv1.VerifyMagicLinkRequest]) (*connect.Response[authv1.VerifyMagicLinkResponse], error) {
	token := req.Msg.Token
	if token == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("token is required"))
	}

	ipAddress := getClientIP(req.Header())
	userAgent := req.Header().Get("User-Agent")

	userWithOrgs, session, err := h.selfHostedSvc.VerifyMagicLink(ctx, token, &ipAddress, &userAgent)
	if err != nil {
		if errors.Is(err, service.ErrInvalidToken) {
			return nil, connect.NewError(connect.CodeUnauthenticated, err)
		}
		logger.WithError(err).Error("auth: magic link verification failed")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoUser := userToProto(&userWithOrgs.User, userWithOrgs.Organizations)

	return connect.NewResponse(&authv1.VerifyMagicLinkResponse{
		Success:      true,
		User:         protoUser,
		SessionToken: session.Token,
	}), nil
}

// errPasswordResetNotImplementedSelfHosted documents the gap: self-hosted has
// no local-DB password-reset flow yet. Operators reset passwords via DB tooling
// until this is implemented.
//
// TODO(self-hosted): implement local-DB password reset (token table + email
// dispatch via Sender). See services/auth/internal/transport/connect/
// cloud_handler.go for the BYOE pattern to mirror.
var errPasswordResetNotImplementedSelfHosted = connect.NewError(
	connect.CodeUnimplemented,
	errors.New("password reset is not yet implemented in self-hosted mode; reset via DB admin tooling"),
)

// RequestPasswordReset is not implemented in self-hosted mode.
func (h *SelfHostedAuthHandler) RequestPasswordReset(ctx context.Context, req *connect.Request[authv1.RequestPasswordResetRequest]) (*connect.Response[authv1.RequestPasswordResetResponse], error) {
	return nil, errPasswordResetNotImplementedSelfHosted
}

// CompletePasswordReset is not implemented in self-hosted mode.
func (h *SelfHostedAuthHandler) CompletePasswordReset(ctx context.Context, req *connect.Request[authv1.CompletePasswordResetRequest]) (*connect.Response[authv1.CompletePasswordResetResponse], error) {
	return nil, errPasswordResetNotImplementedSelfHosted
}

// CompleteEmailVerification is intentionally not supported in self-hosted mode.
func (h *SelfHostedAuthHandler) CompleteEmailVerification(ctx context.Context, req *connect.Request[authv1.CompleteEmailVerificationRequest]) (*connect.Response[authv1.CompleteEmailVerificationResponse], error) {
	return nil, connect.NewError(connect.CodeUnimplemented, errors.New("email verification is not used in self-hosted mode"))
}

// InviteTeamMember sends an invitation to join the team
func (h *SelfHostedAuthHandler) InviteTeamMember(ctx context.Context, req *connect.Request[authv1.InviteTeamMemberRequest]) (*connect.Response[authv1.InviteTeamMemberResponse], error) {
	// Get current user from session
	token := h.getSessionToken(req.Header())
	if token == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	sessionUser, err := h.selfHostedSvc.GetSessionUser(ctx, token)
	if err != nil || sessionUser == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid session"))
	}

	email := req.Msg.Email
	role := mapProtoToRole(req.Msg.Role)

	if email == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("email is required"))
	}

	result, err := h.selfHostedSvc.InviteTeamMember(ctx, sessionUser.User.ID, email, role, h.seatLimit)
	if err != nil {
		if errors.Is(err, service.ErrUserAlreadyExists) {
			return nil, connect.NewError(connect.CodeAlreadyExists, err)
		}
		if errors.Is(err, service.ErrEmailAlreadyInvited) {
			return nil, connect.NewError(connect.CodeAlreadyExists, err)
		}
		logger.WithError(err).Error("auth: invite failed")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	resp := &authv1.InviteTeamMemberResponse{
		Success: result.Invitation != nil,
	}

	if result.UpgradeMessage != "" {
		resp.UpgradeMessage = &result.UpgradeMessage
	}

	if result.Invitation != nil {
		resp.Invitation = &authv1.Invitation{
			Id:        result.Invitation.ID.String(),
			Email:     result.Invitation.Email,
			Role:      mapRoleToProto(result.Invitation.Role),
			ExpiresAt: timestamppb.New(result.Invitation.ExpiresAt),
			CreatedAt: timestamppb.New(result.Invitation.CreatedAt),
			Accepted:  false,
		}
	}

	return connect.NewResponse(resp), nil
}

// AcceptInvitation accepts a team invitation and sets up the user account
func (h *SelfHostedAuthHandler) AcceptInvitation(ctx context.Context, req *connect.Request[authv1.AcceptInvitationRequest]) (*connect.Response[authv1.AcceptInvitationResponse], error) {
	token := req.Msg.Token
	password := req.Msg.Password
	name := req.Msg.Name

	if token == "" || password == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("token and password are required"))
	}

	ipAddress := getClientIP(req.Header())
	userAgent := req.Header().Get("User-Agent")

	userWithOrgs, session, err := h.selfHostedSvc.AcceptInvitation(ctx, token, password, name, &ipAddress, &userAgent)
	if err != nil {
		if errors.Is(err, service.ErrInvitationNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		if errors.Is(err, service.ErrInvitationExpired) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		if errors.Is(err, service.ErrInvitationUsed) {
			return nil, connect.NewError(connect.CodeAlreadyExists, err)
		}
		logger.WithError(err).Error("auth: accept invitation failed")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoUser := userToProto(&userWithOrgs.User, userWithOrgs.Organizations)

	return connect.NewResponse(&authv1.AcceptInvitationResponse{
		Success:      true,
		User:         protoUser,
		SessionToken: session.Token,
	}), nil
}

// ListTeamMembers returns all team members and pending invitations
func (h *SelfHostedAuthHandler) ListTeamMembers(ctx context.Context, req *connect.Request[authv1.ListTeamMembersRequest]) (*connect.Response[authv1.ListTeamMembersResponse], error) {
	// Get current user from session
	token := h.getSessionToken(req.Header())
	if token == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	_, err := h.selfHostedSvc.GetSessionUser(ctx, token)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid session"))
	}

	members, pending, err := h.selfHostedSvc.ListTeamMembers(ctx)
	if err != nil {
		logger.WithError(err).Error("auth: list team members failed")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	protoMembers := make([]*authv1.TeamMember, 0, len(members))
	for _, m := range members {
		protoMembers = append(protoMembers, &authv1.TeamMember{
			User:     userToProto(m.User, nil).User,
			Role:     mapRoleToProto(m.Role),
			JoinedAt: timestamppb.New(m.JoinedAt),
		})
	}

	protoInvitations := make([]*authv1.Invitation, 0, len(pending))
	for _, inv := range pending {
		invitedByEmail := ""
		if inv.InvitedByEmail != nil {
			invitedByEmail = *inv.InvitedByEmail
		}
		protoInvitations = append(protoInvitations, &authv1.Invitation{
			Id:             inv.ID.String(),
			Email:          inv.Email,
			Role:           mapRoleToProto(inv.Role),
			InvitedByEmail: invitedByEmail,
			ExpiresAt:      timestamppb.New(inv.ExpiresAt),
			CreatedAt:      timestamppb.New(inv.CreatedAt),
			Accepted:       inv.AcceptedAt != nil,
		})
	}

	return connect.NewResponse(&authv1.ListTeamMembersResponse{
		Members:            protoMembers,
		PendingInvitations: protoInvitations,
		SeatLimit:          int32(h.seatLimit),
		SeatsUsed:          int32(len(members)),
	}), nil
}

// RemoveTeamMember removes a user from the team
func (h *SelfHostedAuthHandler) RemoveTeamMember(ctx context.Context, req *connect.Request[authv1.RemoveTeamMemberRequest]) (*connect.Response[authv1.RemoveTeamMemberResponse], error) {
	// Get current user from session
	token := h.getSessionToken(req.Header())
	if token == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	sessionUser, err := h.selfHostedSvc.GetSessionUser(ctx, token)
	if err != nil || sessionUser == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid session"))
	}

	userIDStr := req.Msg.UserId
	if userIDStr == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("user_id is required"))
	}

	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid user_id"))
	}

	err = h.selfHostedSvc.RemoveTeamMember(ctx, sessionUser.User.ID, userID)
	if err != nil {
		if errors.Is(err, service.ErrCannotRemoveOwner) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		if errors.Is(err, service.ErrCannotRemoveSelf) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		logger.WithError(err).Error("auth: remove team member failed")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&authv1.RemoveTeamMemberResponse{
		Success: true,
	}), nil
}

// RevokeInvitation revokes a pending invitation
func (h *SelfHostedAuthHandler) RevokeInvitation(ctx context.Context, req *connect.Request[authv1.RevokeInvitationRequest]) (*connect.Response[authv1.RevokeInvitationResponse], error) {
	// Get current user from session
	token := h.getSessionToken(req.Header())
	if token == "" {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("not authenticated"))
	}

	_, err := h.selfHostedSvc.GetSessionUser(ctx, token)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid session"))
	}

	invitationIDStr := req.Msg.InvitationId
	if invitationIDStr == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invitation_id is required"))
	}

	invitationID, err := uuid.Parse(invitationIDStr)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid invitation_id"))
	}

	err = h.selfHostedSvc.RevokeInvitation(ctx, invitationID)
	if err != nil {
		if errors.Is(err, service.ErrInvitationNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		logger.WithError(err).Error("auth: revoke invitation failed")
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	return connect.NewResponse(&authv1.RevokeInvitationResponse{
		Success: true,
	}), nil
}

// NewSelfHostedConnectHandler creates an http.Handler for the self-hosted auth service
func NewSelfHostedConnectHandler(cfg *domain.InternalConfig, svc *service.SelfHostedAuthService, seatLimit int) (string, http.Handler) {
	h := NewSelfHostedAuthHandler(cfg, svc, seatLimit, nil)
	return authconnect.NewAuthServiceHandler(h)
}

// RegisterHTTPRoutes registers the HTTP endpoints for login/register on the given router
// This is used to integrate with the main gateway router
func (h *SelfHostedAuthHandler) RegisterHTTPRoutes(router *mux.Router) {
	if h.oauthHandler != nil {
		router.Handle("/oauth/authorize", h.oauthHandler).Methods(http.MethodGet)
		router.Handle("/oauth/token", h.oauthHandler).Methods(http.MethodPost)
		router.Handle("/oauth/revoke", h.oauthHandler).Methods(http.MethodPost)
		router.Handle("/.well-known/oauth-authorization-server", h.oauthHandler).Methods(http.MethodGet)
	}
	router.HandleFunc("/api/auth/signin", h.handleCloudSignIn).Methods("GET")
	router.HandleFunc("/api/auth/exchange", h.handleTokenExchange).Methods("GET")
	// Cloud relay → tenant landing. Cloud mints a single-use launch code
	// and 302s here; we exchange the code back-channel for the user identity
	// and mint our own builtin session. Replaces the older legacy
	// "?token=<long-lived JWT>" flow which leaked a real token in the URL.
	router.HandleFunc("/auth/cloud-callback", h.handleCloudCallback).Methods("GET")

	// Instance-side sign-out. The counterpart of cloud-callback above: that
	// route mints this instance's session, so this is the only process that
	// can end it. (internal/controlplane has a same-named endpoint for the
	// cloud-cookie-as-source-of-truth model, but instance hosts route to the
	// gateway, not the control plane, so it never sees these requests.)
	router.HandleFunc("/auth/instance-signout", h.handleInstanceSignout).Methods("POST")

	// OIDC relying-party routes (standard authorization_code + PKCE against the
	// cloud OpenID Provider). Flag-gated; dual-run with the launch-code flow.
	h.registerOIDCRelyingParty(router)

	// Frontend PDP: batch permission checks for per-resource UI gating.
	if h.batchCheck != nil {
		router.HandleFunc("/api/authz/batch-check", h.handleAuthzBatchCheck).Methods("POST")
		logger.Info("authz: BatchCheck endpoint mounted (/api/authz/batch-check)")
	}

	// HTTP Login endpoint - properly sets session cookie (ConnectRPC can't set cookies)
	router.HandleFunc("/auth/login", func(w http.ResponseWriter, r *http.Request) {
		logger.Info("auth: HTTP /auth/login endpoint called")

		// Parse JSON body
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
			return
		}

		if req.Email == "" || req.Password == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "email and password are required"})
			return
		}

		ctx := r.Context()
		ipAddress := r.Header.Get("X-Forwarded-For")
		if ipAddress == "" {
			ipAddress = r.RemoteAddr
		}
		userAgent := r.UserAgent()

		userWithOrgs, session, err := h.selfHostedSvc.Login(ctx, req.Email, req.Password, &ipAddress, &userAgent)
		if err != nil {
			logger.WithError(err).Info("auth: login failed")
			w.Header().Set("Content-Type", "application/json")
			if errors.Is(err, service.ErrInvalidCredentials) {
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid credentials"})
			} else {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "login failed"})
			}
			return
		}

		// Set session cookie using the session returned from the service
		h.selfHostedSvc.SetSessionCookieForRequest(w, r, session)

		logger.Infof("auth: HTTP /auth/login - user %s logged in, session token: %s", userWithOrgs.User.Email, session.Token)

		// Return success response with user info
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"user": map[string]interface{}{
				"id":    userWithOrgs.User.ID.String(),
				"email": userWithOrgs.User.Email,
				"name":  userWithOrgs.User.Name,
			},
		})
	}).Methods("POST")

	// HTTP Register endpoint - properly sets session cookie (ConnectRPC can't set cookies)
	router.HandleFunc("/auth/register", func(w http.ResponseWriter, r *http.Request) {
		// Parse JSON body
		var req struct {
			Email    string  `json:"email"`
			Password string  `json:"password"`
			Name     *string `json:"name,omitempty"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid request body"})
			return
		}

		if req.Email == "" || req.Password == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "email and password are required"})
			return
		}

		ctx := r.Context()
		ipAddress := r.Header.Get("X-Forwarded-For")
		if ipAddress == "" {
			ipAddress = r.RemoteAddr
		}
		userAgent := r.UserAgent()

		userWithOrgs, session, err := h.selfHostedSvc.Register(ctx, req.Email, req.Password, req.Name, &ipAddress, &userAgent)
		if err != nil {
			logger.WithError(err).Info("auth: registration failed")
			w.Header().Set("Content-Type", "application/json")
			if errors.Is(err, service.ErrInstanceHasOwner) {
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]string{"error": "instance already has an owner"})
			} else if errors.Is(err, service.ErrUserAlreadyExists) {
				w.WriteHeader(http.StatusConflict)
				json.NewEncoder(w).Encode(map[string]string{"error": "user already exists"})
			} else {
				w.WriteHeader(http.StatusInternalServerError)
				json.NewEncoder(w).Encode(map[string]string{"error": "registration failed"})
			}
			return
		}

		// Set session cookie using the session returned from the service
		h.selfHostedSvc.SetSessionCookieForRequest(w, r, session)

		logger.Infof("auth: user %s registered via HTTP endpoint", userWithOrgs.User.Email)

		// Return success response with user info
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"user": map[string]interface{}{
				"id":    userWithOrgs.User.ID.String(),
				"email": userWithOrgs.User.Email,
				"name":  userWithOrgs.User.Name,
			},
		})
	}).Methods("POST")
}

// Ensure SelfHostedAuthHandler implements authconnect.AuthServiceHandler at compile time.
var _ authconnect.AuthServiceHandler = (*SelfHostedAuthHandler)(nil)
