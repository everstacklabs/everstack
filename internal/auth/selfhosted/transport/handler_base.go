package transport

import (
	"context"
	"errors"
	"github.com/everstacklabs/everstack/internal/lib/httpcors"
	"net/http"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/service"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/requesthost"
	authv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/auth/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// AuthHandler implements the base AuthService ConnectRPC interface.
// In the self-hosted extraction, cloud-only methods return Unimplemented.
type AuthHandler struct {
	svc *service.AuthService
	cfg *domain.InternalConfig
}

// NewAuthHandler creates a new ConnectRPC auth handler for self-hosted mode.
func NewAuthHandler(cfg *domain.InternalConfig, svc *service.AuthService) *AuthHandler {
	return &AuthHandler{
		svc: svc,
		cfg: cfg,
	}
}

// getSessionToken extracts session token from cookie header
func (h *AuthHandler) getSessionToken(header http.Header) string {
	cookies := header.Get("Cookie")
	if cookies == "" {
		return ""
	}

	// Parse cookies manually to find session cookie
	cookieName := h.cfg.Session.CookieName
	req := &http.Request{Header: http.Header{"Cookie": []string{cookies}}}
	cookie, err := req.Cookie(cookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// instanceSignedOut reports whether this browser explicitly signed out of this
// instance. Read from the raw Cookie header so it works for ConnectRPC calls,
// which hand us headers rather than an *http.Request.
func instanceSignedOut(header http.Header) bool {
	cookies := header.Get("Cookie")
	if cookies == "" {
		return false
	}
	req := &http.Request{Header: http.Header{"Cookie": []string{cookies}}}
	cookie, err := req.Cookie(domain.InstanceSignedOutCookie)
	return err == nil && cookie.Value != ""
}

// buildSessionCookie builds a Set-Cookie header value for the session on the
// Connect Login/Register path. It is the same cookie the HTTP routes set
// (AuthService.SessionCookie), Domain included, so sign-out clears it; the
// Host it needs is carried in ctx by requesthost.Middleware on the Connect
// registration.
func (h *AuthHandler) buildSessionCookie(ctx context.Context, header http.Header, session *domain.Session) string {
	return h.svc.SessionCookie(requesthost.Request(ctx, header), session).String()
}

// ============================================
// Cloud-mode stub methods (not available in self-hosted mode)
// ============================================

var errNotAvailableInSelfHostedMode = connect.NewError(
	connect.CodeUnimplemented,
	errors.New("this endpoint is not available in self-hosted mode"),
)

// GetAuthURL is not available in self-hosted mode
func (h *AuthHandler) GetAuthURL(ctx context.Context, req *connect.Request[authv1.GetAuthURLRequest]) (*connect.Response[authv1.GetAuthURLResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// Authenticate is not available in self-hosted mode
func (h *AuthHandler) Authenticate(ctx context.Context, req *connect.Request[authv1.AuthenticateRequest]) (*connect.Response[authv1.AuthenticateResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// RefreshSession is not available in self-hosted mode
func (h *AuthHandler) RefreshSession(ctx context.Context, req *connect.Request[authv1.RefreshSessionRequest]) (*connect.Response[authv1.RefreshSessionResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// CreateInstanceConnectSession is not available on the instance-local auth service.
func (h *AuthHandler) CreateInstanceConnectSession(ctx context.Context, req *connect.Request[authv1.CreateInstanceConnectSessionRequest]) (*connect.Response[authv1.CreateInstanceConnectSessionResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// GetInstanceConnectSession is not available on the instance-local auth service.
func (h *AuthHandler) GetInstanceConnectSession(ctx context.Context, req *connect.Request[authv1.GetInstanceConnectSessionRequest]) (*connect.Response[authv1.GetInstanceConnectSessionResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// CompleteInstanceConnect is not available on the instance-local auth service.
func (h *AuthHandler) CompleteInstanceConnect(ctx context.Context, req *connect.Request[authv1.CompleteInstanceConnectRequest]) (*connect.Response[authv1.CompleteInstanceConnectResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// CreateDeviceAuthorization is not available in self-hosted mode.
func (h *AuthHandler) CreateDeviceAuthorization(ctx context.Context, req *connect.Request[authv1.CreateDeviceAuthorizationRequest]) (*connect.Response[authv1.CreateDeviceAuthorizationResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// ExchangeDeviceCode is not available in self-hosted mode.
func (h *AuthHandler) ExchangeDeviceCode(ctx context.Context, req *connect.Request[authv1.ExchangeDeviceCodeRequest]) (*connect.Response[authv1.ExchangeDeviceCodeResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// ApproveDeviceAuthorization is not available in self-hosted mode.
func (h *AuthHandler) ApproveDeviceAuthorization(ctx context.Context, req *connect.Request[authv1.ApproveDeviceAuthorizationRequest]) (*connect.Response[authv1.ApproveDeviceAuthorizationResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// GetDeviceAuthorizationStatus is not available in self-hosted mode.
func (h *AuthHandler) GetDeviceAuthorizationStatus(ctx context.Context, req *connect.Request[authv1.GetDeviceAuthorizationStatusRequest]) (*connect.Response[authv1.GetDeviceAuthorizationStatusResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// SignOut ends this browser's session: the session row is deleted
// server-side, then the session cookie is expired. A failed delete is an
// error, never a success: the admin app keeps the user signed in and says
// so, rather than reporting a sign-out that left the session usable.
//
// It deliberately does not set the instance signed-out marker that
// /auth/instance-signout sets for cloud-managed instances: the marker makes
// the gateway ignore every session cookie, and the Connect Login path does
// not clear it, so a self-hosted user could not sign back in.
func (h *AuthHandler) SignOut(ctx context.Context, req *connect.Request[authv1.SignOutRequest]) (*connect.Response[authv1.SignOutResponse], error) {
	if token := h.getSessionToken(req.Header()); token != "" {
		if err := h.svc.SignOut(ctx, token); err != nil {
			logger.WithError(err).Error("auth: sign out failed to delete session")
			return nil, connect.NewError(connect.CodeInternal, errors.New("sign out failed"))
		}
	}
	resp := connect.NewResponse(&authv1.SignOutResponse{Success: true})
	resp.Header().Add("Set-Cookie", h.svc.ExpiredSessionCookie(requesthost.Request(ctx, req.Header())).String())
	return resp, nil
}

// GetSession returns the current user session if authenticated
func (h *AuthHandler) GetSession(ctx context.Context, req *connect.Request[authv1.GetSessionRequest]) (*connect.Response[authv1.GetSessionResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// GetAuthMode returns the authentication mode
func (h *AuthHandler) GetAuthMode(ctx context.Context, req *connect.Request[authv1.GetAuthModeRequest]) (*connect.Response[authv1.GetAuthModeResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// Register is not available via the base handler
func (h *AuthHandler) Register(ctx context.Context, req *connect.Request[authv1.RegisterRequest]) (*connect.Response[authv1.RegisterResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// Login is not available via the base handler
func (h *AuthHandler) Login(ctx context.Context, req *connect.Request[authv1.LoginRequest]) (*connect.Response[authv1.LoginResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// RequestMagicLink is not available via the base handler
func (h *AuthHandler) RequestMagicLink(ctx context.Context, req *connect.Request[authv1.RequestMagicLinkRequest]) (*connect.Response[authv1.RequestMagicLinkResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// VerifyMagicLink is not available via the base handler
func (h *AuthHandler) VerifyMagicLink(ctx context.Context, req *connect.Request[authv1.VerifyMagicLinkRequest]) (*connect.Response[authv1.VerifyMagicLinkResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// InviteTeamMember is not available via the base handler
func (h *AuthHandler) InviteTeamMember(ctx context.Context, req *connect.Request[authv1.InviteTeamMemberRequest]) (*connect.Response[authv1.InviteTeamMemberResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// AcceptInvitation is not available via the base handler
func (h *AuthHandler) AcceptInvitation(ctx context.Context, req *connect.Request[authv1.AcceptInvitationRequest]) (*connect.Response[authv1.AcceptInvitationResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// ListTeamMembers is not available via the base handler
func (h *AuthHandler) ListTeamMembers(ctx context.Context, req *connect.Request[authv1.ListTeamMembersRequest]) (*connect.Response[authv1.ListTeamMembersResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// RemoveTeamMember is not available via the base handler
func (h *AuthHandler) RemoveTeamMember(ctx context.Context, req *connect.Request[authv1.RemoveTeamMemberRequest]) (*connect.Response[authv1.RemoveTeamMemberResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// RevokeInvitation is not available via the base handler
func (h *AuthHandler) RevokeInvitation(ctx context.Context, req *connect.Request[authv1.RevokeInvitationRequest]) (*connect.Response[authv1.RevokeInvitationResponse], error) {
	return nil, errNotAvailableInSelfHostedMode
}

// ============================================
// Shared helper functions
// ============================================

func userToProto(user *domain.User, orgs []domain.OrganizationMembership) *authv1.UserWithOrganizations {
	protoUser := &authv1.UserWithOrganizations{
		User: &authv1.User{
			Id:        user.ID.String(),
			Email:     user.Email,
			Name:      user.Name,
			AvatarUrl: user.AvatarURL,
			CreatedAt: timestamppb.New(user.CreatedAt),
			UpdatedAt: timestamppb.New(user.UpdatedAt),
		},
		Organizations: make([]*authv1.Organization, 0, len(orgs)),
	}

	for _, org := range orgs {
		protoUser.Organizations = append(protoUser.Organizations, &authv1.Organization{
			Id:   org.ID.String(),
			Slug: org.Slug,
			Name: org.Name,
			Role: mapRoleToProto(org.Role),
		})
	}

	return protoUser
}

func mapProtoToRole(role authv1.OrganizationRole) string {
	switch role {
	case authv1.OrganizationRole_ORGANIZATION_ROLE_OWNER:
		return "owner"
	case authv1.OrganizationRole_ORGANIZATION_ROLE_ADMIN:
		return "admin"
	case authv1.OrganizationRole_ORGANIZATION_ROLE_MEMBER:
		return "member"
	case authv1.OrganizationRole_ORGANIZATION_ROLE_VIEWER:
		return "viewer"
	default:
		return "member"
	}
}

func mapRoleToProto(role string) authv1.OrganizationRole {
	switch role {
	case "owner":
		return authv1.OrganizationRole_ORGANIZATION_ROLE_OWNER
	case "admin":
		return authv1.OrganizationRole_ORGANIZATION_ROLE_ADMIN
	case "member":
		return authv1.OrganizationRole_ORGANIZATION_ROLE_MEMBER
	case "viewer":
		return authv1.OrganizationRole_ORGANIZATION_ROLE_VIEWER
	default:
		return authv1.OrganizationRole_ORGANIZATION_ROLE_UNSPECIFIED
	}
}

func getClientIP(header http.Header) string {
	if ips := header.Get("X-Forwarded-For"); ips != "" {
		return ips
	}
	if ip := header.Get("X-Real-Ip"); ip != "" {
		return ip
	}
	return ""
}

// corsMiddleware applies the shared credentialed-CORS policy.
//
// This used to reflect any Origin back with Access-Control-Allow-Credentials
// set, which let any site on the internet make authenticated cross-origin
// requests with the victim's cookies and read the responses. See
// internal/lib/httpcors.
func corsMiddleware(next http.Handler) http.Handler {
	return corsPolicy.Middleware(next)
}

// corsPolicy is resolved once at process start from the environment.
var corsPolicy = httpcors.PolicyFromEnv()
