package service

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/repository"
	"github.com/everstacklabs/everstack/internal/enterprise"
	"github.com/everstacklabs/everstack/internal/lib/emailaddr"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/lib/secretlog"
	"github.com/google/uuid"
)

// AuthMode represents the authentication mode
type AuthMode string

const (
	AuthModeCloud      AuthMode = "cloud"
	AuthModeSelfHosted AuthMode = "self_hosted"
)

// Magic link expiry duration
const MagicLinkExpiry = 24 * time.Hour

// Invitation expiry duration
const InvitationExpiry = 7 * 24 * time.Hour

var (
	ErrUserAlreadyExists   = errors.New("user already exists")
	ErrInstanceHasOwner    = errors.New("instance already has an owner")
	ErrInvalidCredentials  = errors.New("invalid email or password")
	ErrUserNotFound        = errors.New("user not found")
	ErrInvalidToken        = errors.New("invalid or expired token")
	ErrInvitationNotFound  = errors.New("invitation not found")
	ErrInvitationExpired   = errors.New("invitation has expired")
	ErrInvitationUsed      = errors.New("invitation has already been used")
	ErrSeatLimitReached    = errors.New("seat limit reached")
	ErrSelfHostedOnly      = errors.New("this endpoint is only available in self-hosted mode")
	ErrEmailAlreadyInvited = errors.New("email has already been invited")
	ErrCannotRemoveOwner   = errors.New("cannot remove the instance owner")
	ErrCannotRemoveSelf    = errors.New("cannot remove yourself")
	ErrMemberNotFound      = errors.New("member not found")
	// ErrNoOrganization refuses a sign-in by someone whose password is right
	// but who belongs to no organization on this instance, for example a
	// removed member. Only an invitation from an owner or admin lets them in.
	ErrNoOrganization = errors.New("you're not a member of any organization on this instance; ask an admin to invite you")

	// ErrRoleNotGrantable refuses an invitation for a role above the
	// inviter's own, or from someone who cannot manage members at all.
	ErrRoleNotGrantable = errors.New("insufficient permissions to invite with this role")
	// ErrOrganizationRequired means the inviter belongs to several
	// organizations and did not say which one the invitation is for.
	ErrOrganizationRequired = errors.New("choose the organization to invite into")
	// ErrInvitationRevoked means the invitation's inviter no longer holds
	// the authority it needs, so it cannot be used.
	ErrInvitationRevoked = errors.New("invitation is no longer valid")
)

// EmailSender is the narrow outbound-email surface self-hosted auth needs.
// It is an interface, and injected, because this package sits under internal/
// and must not import services/email: outbound email is a cloud overlay, and
// routing it through a slot is what keeps that import edge from forming.
// services/email's Sender satisfies this structurally.
type EmailSender interface {
	SendMagicLink(ctx context.Context, to, magicLink, expiresIn string) error
	SendInvitation(ctx context.Context, to, orgName, inviterName, inviterEmail, inviteLink, role string, expiresAt time.Time) error
}

// SelfHostedAuthService handles self-hosted authentication
type SelfHostedAuthService struct {
	*AuthService
	credentialsRepo *repository.CredentialsRepository
	invitationRepo  *repository.InvitationRepository
	magicLinkRepo   *repository.MagicLinkRepository
	orgRepo         *repository.OrganizationRepository
	authConfigRepo  *repository.AuthConfigRepository

	// email and externalURL are set together by WithEmail. When email is nil
	// there is no configured provider, and the magic-link and invitation flows
	// fall back to logging the token so a self-hosted operator can still
	// deliver it by hand.
	email       EmailSender
	externalURL string
}

// WithEmail attaches an outbound email sender and the instance's external URL,
// which is needed to build the links that go in the mail. Both are required:
// a sender without a base URL cannot produce a working link, so passing an
// empty externalURL leaves the service in its log-the-token fallback.
func (s *SelfHostedAuthService) WithEmail(sender EmailSender, externalURL string) *SelfHostedAuthService {
	if sender == nil || strings.TrimSpace(externalURL) == "" {
		return s
	}
	s.email = sender
	s.externalURL = strings.TrimRight(strings.TrimSpace(externalURL), "/")
	return s
}

// NewSelfHostedAuthService creates a new self-hosted auth service
func NewSelfHostedAuthService(
	cfg *domain.InternalConfig,
	userRepo *repository.UserRepository,
	sessionRepo *repository.SessionRepository,
	credentialsRepo *repository.CredentialsRepository,
	invitationRepo *repository.InvitationRepository,
	magicLinkRepo *repository.MagicLinkRepository,
	orgRepo *repository.OrganizationRepository,
	authConfigRepo *repository.AuthConfigRepository,
) *SelfHostedAuthService {
	return &SelfHostedAuthService{
		AuthService:     NewAuthService(cfg, userRepo, sessionRepo),
		credentialsRepo: credentialsRepo,
		invitationRepo:  invitationRepo,
		magicLinkRepo:   magicLinkRepo,
		orgRepo:         orgRepo,
		authConfigRepo:  authConfigRepo,
	}
}

// GetAuthMode returns the current authentication mode
func (s *SelfHostedAuthService) GetAuthMode(ctx context.Context) (AuthMode, bool, error) {
	if s.authConfigRepo != nil {
		cfg, err := s.authConfigRepo.Get(ctx)
		if err != nil {
			return AuthModeSelfHosted, false, err
		}
		if cfg != nil && cfg.AuthMode == string(AuthModeCloud) {
			return AuthModeCloud, true, nil
		}
	}

	// Self-hosted mode - check if any users exist
	hasUsers, err := s.HasUsers(ctx)
	if err != nil {
		return AuthModeSelfHosted, false, err
	}

	return AuthModeSelfHosted, hasUsers, nil
}

// HasUsers checks if any users exist in the system
func (s *SelfHostedAuthService) HasUsers(ctx context.Context) (bool, error) {
	members, err := s.CountAllUsers(ctx)
	if err != nil {
		return false, err
	}
	return members > 0, nil
}

// CountAllUsers counts all users in the system
func (s *SelfHostedAuthService) CountAllUsers(ctx context.Context) (int, error) {
	return s.userRepo.Count(ctx)
}

// Register creates the first admin user (instance owner)
//
// Never on a managed gateway: its database is shared by every tenant and its
// users come from the Cloud identity flow, so "no users yet" there (a fresh
// region, an emptied database) must not let an anonymous caller become the
// owner of the gateway's first organization.
func (s *SelfHostedAuthService) Register(ctx context.Context, email, password string, name *string, ipAddress, userAgent *string) (*domain.UserWithOrganizations, *domain.Session, error) {
	if enterprise.ManagedGateway() {
		return nil, nil, ErrSelfHostedOnly
	}
	// Check if self-hosted mode
	mode, hasUsers, err := s.GetAuthMode(ctx)
	if err != nil {
		return nil, nil, err
	}
	if mode == AuthModeCloud {
		return nil, nil, ErrSelfHostedOnly
	}

	// Only allow registration if no users exist
	if hasUsers {
		return nil, nil, ErrInstanceHasOwner
	}

	// Validate the address before anything is created. A user row keyed to an
	// address that cannot receive mail can never verify or reset itself.
	if err := emailaddr.Validate(email); err != nil {
		return nil, nil, err
	}

	// Validate password
	if err := ValidatePasswordStrength(password); err != nil {
		return nil, nil, err
	}

	// Check if email is already taken
	existing, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to check existing user: %w", err)
	}
	if existing != nil {
		return nil, nil, ErrUserAlreadyExists
	}

	// Hash password
	passwordHash, err := HashPassword(password)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to hash password: %w", err)
	}

	// Create the user, their password and the instance's first organization
	// with them as owner in one step under the bootstrap lock. The hasUsers
	// check above is not atomic; this is, so when several people register
	// at once exactly one succeeds and the others leave nothing behind.
	user := &domain.User{ID: uuid.New(), Email: email, Name: name}
	org, err := s.orgRepo.RegisterFirstOwner(ctx, user, passwordHash, "default", "Default Organization")
	if err != nil {
		if errors.Is(err, repository.ErrInstanceHasUsers) {
			return nil, nil, ErrInstanceHasOwner
		}
		return nil, nil, fmt.Errorf("failed to register: %w", err)
	}
	s.createDefaultWorkspace(ctx, org.ID)
	orgs := []domain.OrganizationMembership{{ID: org.ID, Slug: org.Slug, Name: org.Name, Role: domain.RoleOwner}}

	// Create session
	session, err := s.CreateSession(ctx, user.ID, ipAddress, userAgent)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create session: %w", err)
	}

	logger.Infof("auth: registered first user %s as instance owner", email)
	return &domain.UserWithOrganizations{
		User:          *user,
		Organizations: orgs,
	}, session, nil
}

// Login authenticates with email and password
func (s *SelfHostedAuthService) Login(ctx context.Context, email, password string, ipAddress, userAgent *string) (*domain.UserWithOrganizations, *domain.Session, error) {
	mode, _, err := s.GetAuthMode(ctx)
	if err != nil {
		return nil, nil, err
	}
	if mode == AuthModeCloud {
		return nil, nil, ErrSelfHostedOnly
	}

	// Get user by email
	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return nil, nil, ErrInvalidCredentials
	}

	// Get credentials
	creds, err := s.credentialsRepo.GetByUserID(ctx, user.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get credentials: %w", err)
	}
	if creds == nil {
		// User exists but no password set (might be SSO user)
		return nil, nil, ErrInvalidCredentials
	}

	// Verify password
	valid, err := VerifyPassword(password, creds.PasswordHash)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to verify password: %w", err)
	}
	if !valid {
		return nil, nil, ErrInvalidCredentials
	}

	userWithOrgs, err := s.signInMemberships(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}

	// Create session
	session, err := s.CreateSession(ctx, user.ID, ipAddress, userAgent)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create session: %w", err)
	}

	logger.Infof("auth: user %s logged in", email)
	return userWithOrgs, session, nil
}

func (s *SelfHostedAuthService) SetCloudManaged(ctx context.Context, organizationID, organizationSlug, workspaceID, workspaceSlug, gatewayURL string) error {
	if s.authConfigRepo == nil {
		return nil
	}
	return s.authConfigRepo.SetCloudManaged(ctx, organizationID, organizationSlug, workspaceID, workspaceSlug, gatewayURL)
}

// MarkCloudManaged records only that this instance is cloud-managed. Used by
// a managed gateway, whose single auth_config row is shared by every tenant.
func (s *SelfHostedAuthService) MarkCloudManaged(ctx context.Context) error {
	if s.authConfigRepo == nil {
		return nil
	}
	return s.authConfigRepo.MarkCloudManaged(ctx)
}

// CloudOrganizationSlug returns the cloud organization slug recorded when this
// instance was connected to the cloud, or "" when it isn't known. Callers use
// it to build cloud-side URLs (e.g. the instances picker to land on after an
// instance sign-out); "" means "fall back to the cloud root".
func (s *SelfHostedAuthService) CloudOrganizationSlug(ctx context.Context) string {
	if s.authConfigRepo == nil {
		return ""
	}
	cfg, err := s.authConfigRepo.Get(ctx)
	if err != nil {
		logger.WithError(err).Warn("auth: failed to read cloud organization slug")
		return ""
	}
	if cfg == nil || cfg.CloudOrganizationSlug == nil {
		return ""
	}
	return strings.TrimSpace(*cfg.CloudOrganizationSlug)
}

// createDefaultWorkspace gives a newly bootstrapped organization its default
// workspace. Best effort: an organization without one still works.
func (s *SelfHostedAuthService) createDefaultWorkspace(ctx context.Context, orgID uuid.UUID) {
	ws := &domain.Workspace{
		OrganizationID: orgID,
		Slug:           "default",
		Name:           "Default",
		Environment:    domain.EnvProduction,
	}
	if err := s.orgRepo.CreateWorkspace(ctx, ws); err != nil {
		logger.WithError(err).Warn("auth: failed to create default workspace")
	}
}

// signInMemberships returns userID with their organizations, for a sign-in
// whose credential has already been verified. Proving who you are is not a
// membership: someone who belongs to no organization (a removed member, say)
// gets ErrNoOrganization and must not be given a session; only an invitation
// brings them back. The one exception is first run, on an instance with no
// organization and only this one user, where they bootstrap the first one.
func (s *SelfHostedAuthService) signInMemberships(ctx context.Context, userID uuid.UUID) (*domain.UserWithOrganizations, error) {
	userWithOrgs, err := s.userRepo.GetWithOrganizations(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user organizations: %w", err)
	}
	if len(userWithOrgs.Organizations) > 0 {
		return userWithOrgs, nil
	}
	membership, ok, err := s.bootstrapFirstOrganization(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to bootstrap organization: %w", err)
	}
	if !ok {
		// A concurrent sign-in by the same user may have bootstrapped while
		// this one waited for the lock.
		if again, err := s.userRepo.GetWithOrganizations(ctx, userID); err == nil && len(again.Organizations) > 0 {
			return again, nil
		}
		logger.Infof("auth: refused sign-in for %s: not a member of any organization", userID)
		return nil, ErrNoOrganization
	}
	userWithOrgs.Organizations = append(userWithOrgs.Organizations, membership)
	return userWithOrgs, nil
}

// bootstrapFirstOrganization makes userID the owner of a new first
// organization, with its default workspace, when the instance has no
// organization and userID is its only user. It never adds anyone to
// an organization that already exists; ok is false when it did nothing.
func (s *SelfHostedAuthService) bootstrapFirstOrganization(ctx context.Context, userID uuid.UUID) (domain.OrganizationMembership, bool, error) {
	org, ok, err := s.orgRepo.BootstrapFirstOrganization(ctx, userID, "default", "Default Organization")
	if err != nil || !ok {
		return domain.OrganizationMembership{}, false, err
	}
	s.createDefaultWorkspace(ctx, org.ID)
	logger.Infof("auth: user %s bootstrapped the first organization", userID)
	return domain.OrganizationMembership{ID: org.ID, Slug: org.Slug, Name: org.Name, Role: domain.RoleOwner}, true, nil
}

// EnsureUserHasConfiguredCloudOrganization attaches a local user, with
// cloudRole (their role in the cloud organization, as the cloud vouched for
// it in this sign-in), to the
// cloud-managed organization recorded during /auth/cloud-callback. This is the
// tenant identity the rest of the gateway uses, so prefer it over creating a
// self-hosted "default" organization.
func (s *SelfHostedAuthService) EnsureUserHasConfiguredCloudOrganization(ctx context.Context, userID uuid.UUID, cloudRole string, userWithOrgs *domain.UserWithOrganizations) (bool, error) {
	if s.authConfigRepo == nil {
		return false, nil
	}
	cfg, err := s.authConfigRepo.Get(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get auth config: %w", err)
	}
	if cfg == nil || cfg.AuthMode != string(AuthModeCloud) || cfg.CloudOrganizationID == nil || *cfg.CloudOrganizationID == "" {
		return false, nil
	}

	orgID, err := uuid.Parse(*cfg.CloudOrganizationID)
	if err != nil {
		return false, fmt.Errorf("invalid cloud organization id %q: %w", *cfg.CloudOrganizationID, err)
	}

	slug := "cloud"
	if cfg.CloudOrganizationSlug != nil && *cfg.CloudOrganizationSlug != "" {
		slug = *cfg.CloudOrganizationSlug
	}
	org, err := s.orgRepo.EnsureSimpleWithID(ctx, orgID, slug, slug)
	if err != nil {
		return false, fmt.Errorf("failed to ensure cloud organization: %w", err)
	}
	// The local role follows the role the cloud vouched for in this
	// sign-in, so a cloud viewer is a viewer here, not an owner. The cloud is
	// the authority, so it moves the local role down as well as up. A cloud
	// that names no role (or an unknown one) grants least privilege to a
	// newcomer and leaves an existing role alone.
	if role := knownOrganizationRole(cloudRole); role != "" {
		if err := s.orgRepo.AddMemberSimple(ctx, org.ID, userID, role); err != nil {
			return false, fmt.Errorf("failed to add user to cloud organization: %w", err)
		}
	} else if err := s.orgRepo.AddMemberIfAbsent(ctx, org.ID, userID, domain.RoleMember); err != nil {
		return false, fmt.Errorf("failed to add user to cloud organization: %w", err)
	}

	if userWithOrgs != nil {
		member, err := s.orgRepo.GetMember(ctx, org.ID, userID)
		if err != nil || member == nil {
			return false, fmt.Errorf("failed to read cloud organization membership: %v", err)
		}
		userWithOrgs.Organizations = append(userWithOrgs.Organizations, domain.OrganizationMembership{
			ID:   org.ID,
			Slug: org.Slug,
			Name: org.Name,
			Role: member.Role,
		})
	}
	return true, nil
}

// knownOrganizationRole returns role when it is one of the organization
// roles, and "" otherwise.
func knownOrganizationRole(role string) string {
	switch role = strings.ToLower(strings.TrimSpace(role)); role {
	case domain.RoleOwner, domain.RoleAdmin, domain.RoleMember, domain.RoleViewer:
		return role
	default:
		return ""
	}
}

// RequestMagicLink creates and "sends" a magic link for passwordless login
func (s *SelfHostedAuthService) RequestMagicLink(ctx context.Context, email string) (string, error) {
	// Reject a malformed address up front rather than reporting the usual
	// "if an account exists" reassurance for something that could never be an
	// account. Anti-enumeration only needs to hide whether a valid address is
	// registered; it has no reason to cover for a typo.
	if err := emailaddr.Validate(email); err != nil {
		return "", err
	}

	// Check if user exists
	user, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		return "", fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		// Don't reveal if user exists or not
		logger.Debugf("auth: magic link requested for non-existent email %s", email)
		return "If an account exists with this email, a magic link has been sent.", nil
	}

	// Delete any existing magic links for this email
	if err := s.magicLinkRepo.DeleteByEmail(ctx, email); err != nil {
		logger.WithError(err).Warn("auth: failed to delete existing magic links")
	}

	// Create magic link token
	magicLink, err := s.magicLinkRepo.Create(ctx, email, MagicLinkExpiry)
	if err != nil {
		return "", fmt.Errorf("failed to create magic link: %w", err)
	}

	if s.email == nil {
		// No email provider configured. The token signs the user in, so it is
		// not logged: anyone who can read the logs could use it. Password
		// sign-in still works without email.
		logger.WithFields("token_sha256", secretlog.Fingerprint(magicLink.Token)).Warn("auth: magic link not delivered because no email sender is configured")
	} else {
		if sendErr := s.email.SendMagicLink(ctx, email, magicLinkURL(s.externalURL, magicLink.Token), "24 hours"); sendErr != nil {
			// Logged, not returned. The response below is identical whether or
			// not the account exists, and failing here would leak that
			// distinction to anyone who could trigger a mail outage.
			logger.WithError(sendErr).Errorf("auth: failed to send magic link email to %s", email)
		}
	}

	return "If an account exists with this email, a magic link has been sent.", nil
}

// VerifyMagicLink verifies a magic link token and creates a session
func (s *SelfHostedAuthService) VerifyMagicLink(ctx context.Context, token string, ipAddress, userAgent *string) (*domain.UserWithOrganizations, *domain.Session, error) {
	// Spend the token in one statement so concurrent verifications of the
	// same link cannot each pass a read and mint a session.
	magicLink, err := s.magicLinkRepo.Consume(ctx, token)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to consume magic link: %w", err)
	}
	if magicLink == nil {
		return nil, nil, ErrInvalidToken
	}

	// Get user
	user, err := s.userRepo.GetByEmail(ctx, magicLink.Email)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get user: %w", err)
	}
	if user == nil {
		return nil, nil, ErrUserNotFound
	}

	userWithOrgs, err := s.signInMemberships(ctx, user.ID)
	if err != nil {
		return nil, nil, err
	}

	// Create session
	session, err := s.CreateSession(ctx, user.ID, ipAddress, userAgent)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create session: %w", err)
	}

	logger.Infof("auth: user %s logged in via magic link", magicLink.Email)
	return userWithOrgs, session, nil
}

// InviteResult contains the result of an invitation attempt
type InviteResult struct {
	Invitation     *repository.Invitation
	UpgradeMessage string
}

// InviteTeamMember creates an invitation into one of the inviter's
// organizations: activeOrgID when given (it must be one of theirs), or their
// only organization. The inviter must be able to manage members there and
// may grant at most their own role, so only an owner can invite an owner.
func (s *SelfHostedAuthService) InviteTeamMember(ctx context.Context, inviter *domain.UserWithOrganizations, activeOrgID, email, role string, seatLimit int) (*InviteResult, error) {
	if inviter == nil {
		return nil, ErrRoleNotGrantable
	}
	inviterID := inviter.User.ID
	org, err := inviteOrganization(inviter.Organizations, activeOrgID)
	if err != nil {
		return nil, err
	}
	if !domain.CanGrantRole(org.Role, role) {
		return nil, ErrRoleNotGrantable
	}

	if err := emailaddr.Validate(email); err != nil {
		return nil, err
	}

	// Check if email is already a user
	existing, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing user: %w", err)
	}
	// An existing account (a removed member, say) can be invited back; only
	// someone already in this organization cannot.
	if existing != nil {
		member, err := s.orgRepo.GetMember(ctx, org.ID, existing.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to check existing membership: %w", err)
		}
		if member != nil {
			return nil, ErrUserAlreadyExists
		}
	}

	// Check for existing pending invitation
	pending, err := s.invitationRepo.GetByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("failed to check pending invitations: %w", err)
	}
	if len(pending) > 0 {
		return nil, ErrEmailAlreadyInvited
	}

	// Check seat limit
	userCount, err := s.CountAllUsers(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count users: %w", err)
	}
	pendingCount, err := s.invitationRepo.CountPendingForOrg(ctx, uuid.Nil) // Count all pending
	if err != nil {
		return nil, fmt.Errorf("failed to count pending invitations: %w", err)
	}

	totalSeatsUsed := userCount + pendingCount
	if seatLimit > 0 && totalSeatsUsed >= seatLimit {
		return &InviteResult{
			UpgradeMessage: fmt.Sprintf("You've reached your seat limit of %d. Upgrade to invite more team members.", seatLimit),
		}, nil
	}

	// Create invitation
	orgID := org.ID
	invitation, err := s.invitationRepo.Create(ctx, email, role, &inviterID, &orgID, InvitationExpiry)
	if err != nil {
		return nil, fmt.Errorf("failed to create invitation: %w", err)
	}

	if s.email == nil {
		// No email provider configured. The token admits whoever holds it to
		// the organization, so it is not logged.
		logger.WithFields("token_sha256", secretlog.Fingerprint(invitation.Token)).Warn("auth: invitation not delivered because no email sender is configured")
	} else {
		inviteLink := s.externalURL + "/auth/invite?token=" + invitation.Token
		// Look up the inviter so the mail carries a real name and reply-to
		// rather than arriving from nobody. A miss here is not worth failing
		// the invite over, so the fields just stay empty.
		inviterName, inviterEmail := "", ""
		if inviter, lookupErr := s.userRepo.GetByID(ctx, inviterID); lookupErr == nil && inviter != nil {
			inviterEmail = inviter.Email
			if inviter.Name != nil {
				inviterName = *inviter.Name
			}
		}
		orgName := ""
		if invitation.OrganizationID != nil {
			if org, orgErr := s.orgRepo.GetByID(ctx, *invitation.OrganizationID); orgErr == nil && org != nil {
				orgName = org.Name
			}
		}
		if sendErr := s.email.SendInvitation(ctx, email, orgName, inviterName, inviterEmail, inviteLink, invitation.Role, invitation.ExpiresAt); sendErr != nil {
			logger.WithError(sendErr).Errorf("auth: failed to send invitation email to %s", email)
		}
	}

	return &InviteResult{Invitation: invitation}, nil
}

// AcceptInvitation accepts an invitation and creates the user account
func (s *SelfHostedAuthService) AcceptInvitation(ctx context.Context, token, password string, name *string, ipAddress, userAgent *string) (*domain.UserWithOrganizations, *domain.Session, error) {
	// Get invitation
	invitation, err := s.invitationRepo.GetByToken(ctx, token)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get invitation: %w", err)
	}
	if invitation == nil {
		return nil, nil, ErrInvitationNotFound
	}

	// Check if expired
	if time.Now().After(invitation.ExpiresAt) {
		return nil, nil, ErrInvitationExpired
	}

	// Check if already used
	if invitation.AcceptedAt != nil {
		return nil, nil, ErrInvitationUsed
	}

	// Resolve the organization and re-check the grant before anything is
	// written, so a refused invitation leaves no account behind.
	orgID, err := s.authorizeInvitation(ctx, invitation)
	if err != nil {
		return nil, nil, err
	}

	// An existing account (a removed member invited back) joins with the
	// password it already has; nothing about the account changes. Anyone
	// else gets a new account with the password they chose.
	user, created, err := s.invitationAccount(ctx, invitation.Email, password, name)
	if err != nil {
		return nil, nil, err
	}

	// Join exactly the invitation's organization with the invited role. The
	// inviter's authority is checked again under a lock in the same step, so
	// a demotion racing this acceptance is seen. A refused or failed join
	// leaves no account behind.
	if err := s.orgRepo.AddInvitedMember(ctx, orgID, user.ID, invitation.Role, *invitation.InvitedBy); err != nil {
		if created {
			if delErr := s.userRepo.Delete(ctx, user.ID); delErr != nil {
				logger.WithError(delErr).Warn("auth: failed to undo a refused invitation acceptance")
			}
		}
		if errors.Is(err, repository.ErrInviterCannotGrant) {
			return nil, nil, ErrInvitationRevoked
		}
		return nil, nil, fmt.Errorf("failed to add user to organization: %w", err)
	}

	// Mark invitation as accepted
	if err := s.invitationRepo.MarkAccepted(ctx, invitation.ID); err != nil {
		logger.WithError(err).Warn("auth: failed to mark invitation as accepted")
	}

	// Create session
	session, err := s.CreateSession(ctx, user.ID, ipAddress, userAgent)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create session: %w", err)
	}

	// Get user with organizations
	userWithOrgs, err := s.userRepo.GetWithOrganizations(ctx, user.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get user organizations: %w", err)
	}

	logger.Infof("auth: user %s accepted invitation and joined", invitation.Email)
	return userWithOrgs, session, nil
}

// invitationAccount returns the account an invitation for email is accepted
// as. An existing account must present its current password
// (ErrInvalidCredentials otherwise) and is returned unchanged. Otherwise a
// new account is created with password; created reports which happened.
func (s *SelfHostedAuthService) invitationAccount(ctx context.Context, email, password string, name *string) (*domain.User, bool, error) {
	existing, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		return nil, false, fmt.Errorf("failed to check existing user: %w", err)
	}
	if existing != nil {
		creds, err := s.credentialsRepo.GetByUserID(ctx, existing.ID)
		if err != nil {
			return nil, false, fmt.Errorf("failed to get credentials: %w", err)
		}
		if creds == nil {
			return nil, false, ErrUserAlreadyExists
		}
		valid, err := VerifyPassword(password, creds.PasswordHash)
		if err != nil {
			return nil, false, fmt.Errorf("failed to verify password: %w", err)
		}
		if !valid {
			return nil, false, ErrInvalidCredentials
		}
		return existing, false, nil
	}

	if err := ValidatePasswordStrength(password); err != nil {
		return nil, false, err
	}
	passwordHash, err := HashPassword(password)
	if err != nil {
		return nil, false, fmt.Errorf("failed to hash password: %w", err)
	}
	user := &domain.User{
		ID:        uuid.New(),
		Email:     email,
		Name:      name,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, false, fmt.Errorf("failed to create user: %w", err)
	}
	if err := s.credentialsRepo.Create(ctx, user.ID, passwordHash); err != nil {
		if delErr := s.userRepo.Delete(ctx, user.ID); delErr != nil {
			logger.WithError(delErr).Warn("auth: failed to undo a partial invitation account")
		}
		return nil, false, fmt.Errorf("failed to store credentials: %w", err)
	}
	return user, true, nil
}

// inviteOrganization picks the organization an invitation is for from the
// inviter's own memberships. A requested organization must be one of them;
// with none requested, the inviter must belong to exactly one.
func inviteOrganization(memberships []domain.OrganizationMembership, activeOrgID string) (domain.OrganizationMembership, error) {
	if activeOrgID != "" {
		for _, m := range memberships {
			if strings.EqualFold(m.ID.String(), strings.TrimSpace(activeOrgID)) {
				return m, nil
			}
		}
		return domain.OrganizationMembership{}, ErrRoleNotGrantable
	}
	switch len(memberships) {
	case 0:
		return domain.OrganizationMembership{}, ErrRoleNotGrantable
	case 1:
		return memberships[0], nil
	default:
		return domain.OrganizationMembership{}, ErrOrganizationRequired
	}
}

// authorizeInvitation returns the organization an invitation joins and checks
// that its inviter can still grant its role there. Invitations created
// before they carried an organization belong to their inviter's organization
// when the inviter has exactly one (the rule the team listing uses); any
// other organization-less invitation is unusable. The inviter's current
// membership is what counts, so demoting or removing an inviter withdraws
// the authority behind their pending invitations.
func (s *SelfHostedAuthService) authorizeInvitation(ctx context.Context, invitation *repository.Invitation) (uuid.UUID, error) {
	if invitation.InvitedBy == nil {
		return uuid.Nil, ErrInvitationRevoked
	}
	inviterID := *invitation.InvitedBy

	if invitation.OrganizationID == nil {
		orgs, err := s.orgRepo.ListByUserID(ctx, inviterID)
		if err != nil {
			return uuid.Nil, fmt.Errorf("failed to load inviter memberships: %w", err)
		}
		if len(orgs) != 1 || !domain.CanGrantRole(orgs[0].Role, invitation.Role) {
			return uuid.Nil, ErrInvitationRevoked
		}
		return orgs[0].ID, nil
	}

	orgID := *invitation.OrganizationID
	inviter, err := s.orgRepo.GetMember(ctx, orgID, inviterID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("failed to load inviter membership: %w", err)
	}
	if inviter == nil || !domain.CanGrantRole(inviter.Role, invitation.Role) {
		return uuid.Nil, ErrInvitationRevoked
	}
	return orgID, nil
}

// TeamMemberInfo contains team member info
type TeamMemberInfo struct {
	User     *domain.User
	Role     string
	JoinedAt time.Time
}

// ListTeamMembers lists the members and pending invitations of the given
// organizations, which must be the caller's own memberships. The table holds
// every organization in the database, so an unscoped listing would hand any
// signed-in user every other organization's members and invitees.
func (s *SelfHostedAuthService) ListTeamMembers(ctx context.Context, orgIDs []uuid.UUID) ([]TeamMemberInfo, []repository.InvitationWithInviter, error) {
	var members []repository.OrganizationMemberBasic
	var pending []repository.InvitationWithInviter
	seenOrg := make(map[uuid.UUID]bool, len(orgIDs))
	seenInvite := make(map[uuid.UUID]bool)
	for _, orgID := range orgIDs {
		if orgID == uuid.Nil || seenOrg[orgID] {
			continue
		}
		seenOrg[orgID] = true
		orgMembers, err := s.orgRepo.ListMembersBasicForOrg(ctx, orgID)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to list members: %w", err)
		}
		members = append(members, orgMembers...)
		orgPending, err := s.invitationRepo.ListPendingForOrg(ctx, orgID)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to list invitations: %w", err)
		}
		for _, inv := range orgPending {
			if !seenInvite[inv.ID] {
				seenInvite[inv.ID] = true
				pending = append(pending, inv)
			}
		}
	}

	var teamMembers []TeamMemberInfo
	seenUser := make(map[uuid.UUID]bool, len(members))
	for _, m := range members {
		if seenUser[m.UserID] {
			continue
		}
		seenUser[m.UserID] = true
		user, err := s.userRepo.GetByID(ctx, m.UserID)
		if err != nil || user == nil {
			continue
		}
		teamMembers = append(teamMembers, TeamMemberInfo{
			User:     user,
			Role:     m.Role,
			JoinedAt: m.CreatedAt,
		})
	}

	return teamMembers, pending, nil
}

// RemoveTeamMember removes userID from the organizations in managedOrgIDs
// (the requester's owner/admin memberships) and nowhere else. The membership
// table holds every organization in the database; removing the user from
// all of them let any signed-in user evict members of other organizations.
// A target outside the requester's organizations is ErrMemberNotFound.
func (s *SelfHostedAuthService) RemoveTeamMember(ctx context.Context, requesterID, userID uuid.UUID, managedOrgIDs []uuid.UUID) error {
	// Can't remove self
	if requesterID == userID {
		return ErrCannotRemoveSelf
	}

	var targets []uuid.UUID
	for _, orgID := range managedOrgIDs {
		member, err := s.orgRepo.GetMember(ctx, orgID, userID)
		if err != nil {
			return fmt.Errorf("failed to load membership: %w", err)
		}
		if member == nil {
			continue
		}
		if member.Role == domain.RoleOwner {
			return ErrCannotRemoveOwner
		}
		targets = append(targets, orgID)
	}
	if len(targets) == 0 {
		return ErrMemberNotFound
	}
	// Removing the user's last membership ends their sessions in the same
	// transaction; a user who still belongs to another organization keeps
	// that access.
	for _, orgID := range targets {
		if err := s.orgRepo.RemoveMemberKeepingOwner(ctx, orgID, userID, true); err != nil {
			if errors.Is(err, repository.ErrLastOwner) {
				return ErrCannotRemoveOwner
			}
			return fmt.Errorf("failed to remove from organization: %w", err)
		}
	}

	logger.Infof("auth: user %s removed from %d organization(s)", userID, len(targets))
	return nil
}

// RevokeInvitation revokes a pending invitation that belongs to one of
// managedOrgIDs (the requester's owner/admin memberships). Builtin
// invitations carry no organization_id; those belong to their inviter's
// organizations. Anything else is ErrInvitationNotFound.
func (s *SelfHostedAuthService) RevokeInvitation(ctx context.Context, invitationID uuid.UUID, managedOrgIDs []uuid.UUID) error {
	invitation, err := s.invitationRepo.GetByID(ctx, invitationID)
	if err != nil {
		return fmt.Errorf("failed to get invitation: %w", err)
	}
	if invitation == nil {
		return ErrInvitationNotFound
	}
	// An organization-less invitation belongs to its inviter's organization,
	// and only when the inviter has exactly one (same rule as the listing).
	invitationOrg := invitation.OrganizationID
	if invitationOrg == nil && invitation.InvitedBy != nil {
		inviterOrgs, err := s.orgRepo.ListByUserID(ctx, *invitation.InvitedBy)
		if err != nil {
			return fmt.Errorf("failed to load inviter memberships: %w", err)
		}
		if len(inviterOrgs) == 1 {
			id := inviterOrgs[0].ID
			invitationOrg = &id
		}
	}
	owned := false
	for _, orgID := range managedOrgIDs {
		if invitationOrg != nil && *invitationOrg == orgID {
			owned = true
			break
		}
	}
	if !owned {
		return ErrInvitationNotFound
	}

	if err := s.invitationRepo.Delete(ctx, invitationID); err != nil {
		return fmt.Errorf("failed to delete invitation: %w", err)
	}

	logger.Infof("auth: invitation %s revoked", invitationID)
	return nil
}

// magicLinkPath is the admin app route that completes a magic-link sign-in.
// It reads the token from the URL fragment and, once the user confirms,
// passes it to VerifyMagicLink, which sets the session cookie.
const magicLinkPath = "/verify-magic-link"

// magicLinkURL builds the link mailed for passwordless sign-in. The token is
// carried in the fragment, which browsers never send to a server or put in a
// Referer, so it stays out of access logs. The page spends the token only
// when the user clicks "Continue signing in": fetching the link, or a mail
// scanner that renders it, does not sign anyone in, and a link planted by
// someone else cannot silently swap the victim into another account.
func magicLinkURL(baseURL, token string) string {
	return strings.TrimRight(baseURL, "/") + magicLinkPath + "#token=" + url.QueryEscape(token)
}
