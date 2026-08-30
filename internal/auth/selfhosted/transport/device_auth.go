package transport

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	authv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/auth/v1"
	"github.com/google/uuid"
)

const (
	deviceCodeTTL          = 15 * time.Minute
	deviceCodePollInterval = int32(deviceauth.DefaultPollInterval / time.Second)
	defaultCLIClientID     = "evs-cli"
	defaultCLIScope        = "cli:full"
)

func (h *SelfHostedAuthHandler) CreateDeviceAuthorization(ctx context.Context, req *connect.Request[authv1.CreateDeviceAuthorizationRequest]) (*connect.Response[authv1.CreateDeviceAuthorizationResponse], error) {
	if err := h.requireDeviceAuthorization(); err != nil {
		return nil, err
	}
	clientID := strings.TrimSpace(req.Msg.GetClientId())
	if clientID == "" {
		clientID = defaultCLIClientID
	}
	scope := strings.TrimSpace(req.Msg.GetScope())
	if scope == "" {
		scope = defaultCLIScope
	}
	if scope != defaultCLIScope {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unsupported device authorization scope"))
	}

	session, err := h.deviceAuth.Create(ctx, clientID, scope, deviceCodeTTL)
	if err != nil {
		logger.WithError(err).Error("auth: failed to create device authorization session")
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to create device authorization"))
	}
	return connect.NewResponse(&authv1.CreateDeviceAuthorizationResponse{
		DeviceCode:      session.DeviceCode,
		UserCode:        session.UserCode,
		VerificationUri: deviceauth.VerificationURI(h.externalAuthURL, req.Header()),
		ExpiresIn:       int32(deviceCodeTTL.Seconds()),
		Interval:        deviceCodePollInterval,
	}), nil
}

func (h *SelfHostedAuthHandler) ExchangeDeviceCode(ctx context.Context, req *connect.Request[authv1.ExchangeDeviceCodeRequest]) (*connect.Response[authv1.ExchangeDeviceCodeResponse], error) {
	if err := h.requireDeviceAuthorization(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Msg.GetDeviceCode()) == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("device_code is required"))
	}
	clientID := strings.TrimSpace(req.Msg.GetClientId())
	if clientID == "" {
		clientID = defaultCLIClientID
	}

	var response *connect.Response[authv1.ExchangeDeviceCodeResponse]
	err := h.deviceAuth.Redeem(ctx, req.Msg.GetDeviceCode(), clientID, func(session *deviceauth.Session) error {
		var issueErr error
		response, issueErr = h.issueDeviceAuthorizationToken(ctx, session)
		return issueErr
	})
	if errors.Is(err, deviceauth.ErrSessionNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("invalid device code"))
	}
	if errors.Is(err, deviceauth.ErrClientMismatch) {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("device code was issued to a different client"))
	}
	if result, ok := deviceauth.ResultForError(err); ok {
		status := result.Status
		// Preserve the self-hosted RPC's original pending value for clients
		// released before oauth_error was added.
		if status == "pending" {
			status = "authorization_pending"
		}
		return connect.NewResponse(&authv1.ExchangeDeviceCodeResponse{
			Status:     status,
			OauthError: result.OAuthError,
		}), nil
	}
	if err != nil {
		var connectErr *connect.Error
		if errors.As(err, &connectErr) {
			return nil, connectErr
		}
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if response == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("device authorization completed without a token response"))
	}
	return response, nil
}

func (h *SelfHostedAuthHandler) issueDeviceAuthorizationToken(ctx context.Context, session *deviceauth.Session) (*connect.Response[authv1.ExchangeDeviceCodeResponse], error) {
	if session.UserID == nil || session.OrgID == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("authorized session missing user or organization"))
	}
	user, err := h.selfHostedSvc.GetUserWithOrganizations(ctx, *session.UserID)
	if err != nil || user == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("failed to resolve device authorization user"))
	}
	org, role, err := h.organizations.GetOrganizationWithRole(ctx, *session.OrgID, *session.UserID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("resolve device authorization organization: %w", err))
	}
	if org == nil || role == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("user is no longer a member of the organization"))
	}
	identity := deviceauth.Identity{
		UserID:           user.User.ID.String(),
		Email:            user.User.Email,
		OrganizationID:   org.ID.String(),
		OrganizationSlug: org.Slug,
		ClientID:         session.ClientID,
	}
	if err := bindDeviceIdentityToRequest(ctx, &identity); err != nil {
		return nil, connect.NewError(connect.CodePermissionDenied, err)
	}
	token, err := h.deviceTokens.Issue(identity)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("issue device token: %w", err))
	}
	return connect.NewResponse(&authv1.ExchangeDeviceCodeResponse{
		Status:      "authorized",
		AccessToken: token,
		TokenType:   "Bearer",
		OrgId:       org.ID.String(),
		OrgSlug:     org.Slug,
		UserEmail:   user.User.Email,
		UserId:      user.User.ID.String(),
	}), nil
}

func bindDeviceIdentityToRequest(ctx context.Context, identity *deviceauth.Identity) error {
	if identity == nil {
		return errors.New("device authorization identity is missing")
	}
	requestInstance, ok := contextkeys.RequestInstanceScopeFromContext(ctx)
	if !ok {
		return nil
	}
	if requestInstance.OrganizationID != identity.OrganizationID {
		return errors.New("device authorization organization does not own this instance")
	}
	identity.InstanceID = requestInstance.InstanceID
	return nil
}

func (h *SelfHostedAuthHandler) ApproveDeviceAuthorization(ctx context.Context, req *connect.Request[authv1.ApproveDeviceAuthorizationRequest]) (*connect.Response[authv1.ApproveDeviceAuthorizationResponse], error) {
	if err := h.requireDeviceAuthorization(); err != nil {
		return nil, err
	}
	user := h.resolveSessionUser(ctx, req.Header())
	if user == nil {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("invalid session"))
	}
	orgID, err := uuid.Parse(req.Msg.GetOrganizationId())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid organization_id"))
	}
	org, role, err := h.organizations.GetOrganizationWithRole(ctx, orgID, user.User.ID)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if org == nil || role == "" {
		return nil, connect.NewError(connect.CodePermissionDenied, errors.New("not a member of this organization"))
	}
	if err := h.deviceAuth.Approve(ctx, req.Msg.GetUserCode(), user.User.ID, orgID); errors.Is(err, deviceauth.ErrSessionNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("device authorization not found or expired"))
	} else if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	return connect.NewResponse(&authv1.ApproveDeviceAuthorizationResponse{Success: true}), nil
}

func (h *SelfHostedAuthHandler) GetDeviceAuthorizationStatus(ctx context.Context, req *connect.Request[authv1.GetDeviceAuthorizationStatusRequest]) (*connect.Response[authv1.GetDeviceAuthorizationStatusResponse], error) {
	if err := h.requireDeviceAuthorization(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.Msg.GetUserCode()) == "" {
		return connect.NewResponse(&authv1.GetDeviceAuthorizationStatusResponse{Valid: false}), nil
	}
	session, err := h.deviceAuth.GetByUserCode(ctx, req.Msg.GetUserCode())
	if errors.Is(err, deviceauth.ErrSessionNotFound) {
		return connect.NewResponse(&authv1.GetDeviceAuthorizationStatusResponse{Valid: false}), nil
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	status, valid, expired := deviceauth.BrowserStatus(session, time.Now())
	return connect.NewResponse(&authv1.GetDeviceAuthorizationStatusResponse{
		Valid:    valid,
		Expired:  expired,
		ClientId: session.ClientID,
		Scope:    session.Scope,
		Status:   status,
	}), nil
}

func (h *SelfHostedAuthHandler) requireDeviceAuthorization() error {
	if h.deviceAuth == nil || h.organizations == nil || h.deviceTokens == nil {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("device authorization is not configured"))
	}
	return nil
}
