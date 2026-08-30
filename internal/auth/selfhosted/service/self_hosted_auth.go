package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/repository"
	"github.com/everstacklabs/everstack/internal/lib/logger"
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
)

// SelfHostedAuthService handles self-hosted authentication
type SelfHostedAuthService struct {
	*AuthService
	credentialsRepo *repository.CredentialsRepository
	invitationRepo  *repository.InvitationRepository
	magicLinkRepo   *repository.MagicLinkRepository
	orgRepo         *repository.OrganizationRepository
	authConfigRepo  *repository.AuthConfigRepository
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
func (s *SelfHostedAuthService) Register(ctx context.Context, email, password string, name *string, ipAddress, userAgent *string) (*domain.UserWithOrganizations, *domain.Session, error) {
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

	// Create user
	user := &domain.User{
		ID:        uuid.New(),
		Email:     email,
		Name:      name,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, nil, fmt.Errorf("failed to create user: %w", err)
	}

	// Store credentials
	if err := s.credentialsRepo.Create(ctx, user.ID, passwordHash); err != nil {
		return nil, nil, fmt.Errorf("failed to store credentials: %w", err)
	}

	// Create default organization and track the membership
	var orgs []domain.OrganizationMembership
	org, err := s.orgRepo.CreateSimple(ctx, "default", "Default Organization")
	if err != nil {
		logger.WithError(err).Warn("auth: failed to create default organization")
	} else {
		// Add user as owner
		if err := s.orgRepo.AddMemberSimple(ctx, org.ID, user.ID, "owner"); err != nil {
			logger.WithError(err).Warn("auth: failed to add user to organization")
		} else {
			orgs = append(orgs, domain.OrganizationMembership{
				ID:   org.ID,
				Slug: org.Slug,
				Name: org.Name,
				Role: "owner",
			})
		}

		// Create default workspace for the organization
		ws := &domain.Workspace{
			OrganizationID: org.ID,
			Slug:           "default",
			Name:           "Default",
			Environment:    domain.EnvProduction,
		}
		if err := s.orgRepo.CreateWorkspace(ctx, ws); err != nil {
			logger.WithError(err).Warn("auth: failed to create default workspace")
		}
	}

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

	// Ensure user has an organization (for existing users who registered before org feature)
	if len(userWithOrgs.Organizations) == 0 {
		logger.Info("auth: user has no organization, creating default organization")
		if err := s.EnsureUserHasOrganization(ctx, user.ID, userWithOrgs); err != nil {
			logger.WithError(err).Warn("auth: failed to create default organization for user")
		}
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

// EnsureUserHasOrganization creates a default organization for a user if they don't have one
func (s *SelfHostedAuthService) EnsureUserHasOrganization(ctx context.Context, userID uuid.UUID, userWithOrgs *domain.UserWithOrganizations) error {
	if ok, err := s.EnsureUserHasConfiguredCloudOrganization(ctx, userID, userWithOrgs); err != nil {
		return err
	} else if ok {
		return nil
	}

	// Check if any organization exists
	orgs, err := s.orgRepo.List(ctx)
	if err != nil {
		return fmt.Errorf("failed to list organizations: %w", err)
	}

	var org *domain.Organization
	if len(orgs) > 0 {
		// Use existing organization
		org = &orgs[0]
	} else {
		// Create default organization
		org, err = s.orgRepo.CreateSimple(ctx, "default", "Default Organization")
		if err != nil {
			return fmt.Errorf("failed to create default organization: %w", err)
		}

		// Create default workspace
		ws := &domain.Workspace{
			OrganizationID: org.ID,
			Slug:           "default",
			Name:           "Default",
			Environment:    domain.EnvProduction,
		}
		if err := s.orgRepo.CreateWorkspace(ctx, ws); err != nil {
			logger.WithError(err).Warn("auth: failed to create default workspace")
		}
	}

	// Ensure a workspace exists for the organization
	workspaces, err := s.orgRepo.ListWorkspaces(ctx, org.ID)
	if err == nil && len(workspaces) == 0 {
		ws := &domain.Workspace{
			OrganizationID: org.ID,
			Slug:           "default",
			Name:           "Default",
			Environment:    domain.EnvProduction,
		}
		if err := s.orgRepo.CreateWorkspace(ctx, ws); err != nil {
			logger.WithError(err).Warn("auth: failed to create default workspace")
		}
	}

	// Add user as owner
	if err := s.orgRepo.AddMemberSimple(ctx, org.ID, userID, "owner"); err != nil {
		return fmt.Errorf("failed to add user to organization: %w", err)
	}

	// Update the userWithOrgs struct
	userWithOrgs.Organizations = append(userWithOrgs.Organizations, domain.OrganizationMembership{
		ID:   org.ID,
		Slug: org.Slug,
		Name: org.Name,
		Role: "owner",
	})

	return nil
}

// EnsureUserHasConfiguredCloudOrganization attaches a local user to the
// cloud-managed organization recorded during /auth/cloud-callback. This is the
// tenant identity the rest of the gateway uses, so prefer it over creating a
// self-hosted "default" organization.
func (s *SelfHostedAuthService) EnsureUserHasConfiguredCloudOrganization(ctx context.Context, userID uuid.UUID, userWithOrgs *domain.UserWithOrganizations) (bool, error) {
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
	if err := s.orgRepo.AddMemberSimple(ctx, org.ID, userID, "owner"); err != nil {
		return false, fmt.Errorf("failed to add user to cloud organization: %w", err)
	}

	if userWithOrgs != nil {
		userWithOrgs.Organizations = append(userWithOrgs.Organizations, domain.OrganizationMembership{
			ID:   org.ID,
			Slug: org.Slug,
			Name: org.Name,
			Role: "owner",
		})
	}
	return true, nil
}

// RequestMagicLink creates and "sends" a magic link for passwordless login
func (s *SelfHostedAuthService) RequestMagicLink(ctx context.Context, email string) (string, error) {
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

	// TODO: Send email with magic link
	// For now, log the token (in production, this would send an email)
	logger.Infof("auth: magic link created for %s: %s", email, magicLink.Token)

	return "If an account exists with this email, a magic link has been sent.", nil
}

// VerifyMagicLink verifies a magic link token and creates a session
func (s *SelfHostedAuthService) VerifyMagicLink(ctx context.Context, token string, ipAddress, userAgent *string) (*domain.UserWithOrganizations, *domain.Session, error) {
	// Get valid token
	magicLink, err := s.magicLinkRepo.GetValidByToken(ctx, token)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get magic link: %w", err)
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

	// Mark token as used
	if err := s.magicLinkRepo.MarkUsed(ctx, magicLink.ID); err != nil {
		return nil, nil, fmt.Errorf("failed to mark magic link as used: %w", err)
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

	logger.Infof("auth: user %s logged in via magic link", magicLink.Email)
	return userWithOrgs, session, nil
}

// InviteResult contains the result of an invitation attempt
type InviteResult struct {
	Invitation     *repository.Invitation
	UpgradeMessage string
}

// InviteTeamMember creates an invitation for a new team member
func (s *SelfHostedAuthService) InviteTeamMember(ctx context.Context, inviterID uuid.UUID, email, role string, seatLimit int) (*InviteResult, error) {
	// Check if email is already a user
	existing, err := s.userRepo.GetByEmail(ctx, email)
	if err != nil {
		return nil, fmt.Errorf("failed to check existing user: %w", err)
	}
	if existing != nil {
		return nil, ErrUserAlreadyExists
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
	invitation, err := s.invitationRepo.Create(ctx, email, role, &inviterID, nil, InvitationExpiry)
	if err != nil {
		return nil, fmt.Errorf("failed to create invitation: %w", err)
	}

	// TODO: Send invitation email
	logger.Infof("auth: invitation created for %s (token: %s)", email, invitation.Token)

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

	// Validate password
	if err := ValidatePasswordStrength(password); err != nil {
		return nil, nil, err
	}

	// Check if user already exists
	existing, err := s.userRepo.GetByEmail(ctx, invitation.Email)
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

	// Create user
	user := &domain.User{
		ID:        uuid.New(),
		Email:     invitation.Email,
		Name:      name,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, nil, fmt.Errorf("failed to create user: %w", err)
	}

	// Store credentials
	if err := s.credentialsRepo.Create(ctx, user.ID, passwordHash); err != nil {
		return nil, nil, fmt.Errorf("failed to store credentials: %w", err)
	}

	// Add to default organization with invited role
	orgs, err := s.orgRepo.List(ctx)
	if err == nil && len(orgs) > 0 {
		if err := s.orgRepo.AddMemberSimple(ctx, orgs[0].ID, user.ID, invitation.Role); err != nil {
			logger.WithError(err).Warn("auth: failed to add user to organization")
		}
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

// TeamMemberInfo contains team member info
type TeamMemberInfo struct {
	User     *domain.User
	Role     string
	JoinedAt time.Time
}

// ListTeamMembers lists all team members
func (s *SelfHostedAuthService) ListTeamMembers(ctx context.Context) ([]TeamMemberInfo, []repository.InvitationWithInviter, error) {
	// Get all users with their org membership
	members, err := s.orgRepo.ListAllMembers(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list members: %w", err)
	}

	// Get pending invitations
	pending, err := s.invitationRepo.ListPending(ctx, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to list invitations: %w", err)
	}

	var teamMembers []TeamMemberInfo
	for _, m := range members {
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

// RemoveTeamMember removes a user from the team
func (s *SelfHostedAuthService) RemoveTeamMember(ctx context.Context, requesterID, userID uuid.UUID) error {
	// Can't remove self
	if requesterID == userID {
		return ErrCannotRemoveSelf
	}

	// Check if target is owner
	isOwner, err := s.orgRepo.IsOwner(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to check owner status: %w", err)
	}
	if isOwner {
		return ErrCannotRemoveOwner
	}

	// Remove from all organizations
	if err := s.orgRepo.RemoveMemberFromAll(ctx, userID); err != nil {
		return fmt.Errorf("failed to remove from organizations: %w", err)
	}

	// Delete sessions
	if err := s.sessionRepo.DeleteByUserID(ctx, userID); err != nil {
		logger.WithError(err).Warn("auth: failed to delete user sessions")
	}

	// Optionally delete the user entirely
	// For now, we'll just remove org membership

	logger.Infof("auth: user %s removed from team", userID)
	return nil
}

// RevokeInvitation revokes a pending invitation
func (s *SelfHostedAuthService) RevokeInvitation(ctx context.Context, invitationID uuid.UUID) error {
	invitation, err := s.invitationRepo.GetByID(ctx, invitationID)
	if err != nil {
		return fmt.Errorf("failed to get invitation: %w", err)
	}
	if invitation == nil {
		return ErrInvitationNotFound
	}

	if err := s.invitationRepo.Delete(ctx, invitationID); err != nil {
		return fmt.Errorf("failed to delete invitation: %w", err)
	}

	logger.Infof("auth: invitation %s revoked", invitationID)
	return nil
}
