package service

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/repository"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// OrganizationService handles organization management
type OrganizationService struct {
	orgRepo        *repository.OrganizationRepository
	authConfigRepo *repository.AuthConfigRepository
}

// NewOrganizationService creates a new organization service
func NewOrganizationService(orgRepo *repository.OrganizationRepository, authConfigRepo *repository.AuthConfigRepository) *OrganizationService {
	return &OrganizationService{
		orgRepo:        orgRepo,
		authConfigRepo: authConfigRepo,
	}
}

// CreateOrganization creates a new organization and adds the user as owner
func (s *OrganizationService) CreateOrganization(ctx context.Context, userID uuid.UUID, name string, slug *string, billingEmail *string) (*domain.Organization, error) {
	// Generate slug if not provided
	var orgSlug string
	var err error
	if slug != nil && *slug != "" {
		orgSlug = *slug
		// Check if slug is available
		exists, err := s.orgRepo.SlugExists(ctx, orgSlug)
		if err != nil {
			return nil, fmt.Errorf("failed to check slug: %w", err)
		}
		if exists {
			return nil, fmt.Errorf("slug '%s' is already taken", orgSlug)
		}
	} else {
		orgSlug, err = s.orgRepo.GenerateUniqueSlug(ctx, name)
		if err != nil {
			return nil, fmt.Errorf("failed to generate slug: %w", err)
		}
	}

	// Create organization
	org := &domain.Organization{
		Slug:         orgSlug,
		Name:         name,
		PlanTier:     "free",
		BillingEmail: billingEmail,
	}

	if err := s.orgRepo.Create(ctx, org); err != nil {
		return nil, fmt.Errorf("failed to create organization: %w", err)
	}

	// Add the creator as owner
	if err := s.orgRepo.AddMember(ctx, org.ID, userID, domain.RoleOwner, nil); err != nil {
		// Rollback org creation
		_ = s.orgRepo.Delete(ctx, org.ID)
		return nil, fmt.Errorf("failed to add owner: %w", err)
	}

	logger.Infof("org: created organization %s (%s) by user %s", org.Name, org.Slug, userID)
	return org, nil
}

// GetOrganization gets an organization by ID or slug
func (s *OrganizationService) GetOrganization(ctx context.Context, id *uuid.UUID, slug *string) (*domain.Organization, error) {
	if id != nil {
		return s.orgRepo.GetByID(ctx, *id)
	}
	if slug != nil {
		return s.orgRepo.GetBySlug(ctx, *slug)
	}
	return nil, fmt.Errorf("either id or slug must be provided")
}

// GetOrganizationWithRole gets an organization and the user's role in it
func (s *OrganizationService) GetOrganizationWithRole(ctx context.Context, orgID, userID uuid.UUID) (*domain.Organization, string, error) {
	org, err := s.orgRepo.GetByID(ctx, orgID)
	if err != nil {
		return nil, "", err
	}
	if org == nil {
		return nil, "", nil
	}

	member, err := s.orgRepo.GetMember(ctx, orgID, userID)
	if err != nil {
		return nil, "", err
	}
	if member == nil {
		return nil, "", nil // User is not a member
	}

	return org, member.Role, nil
}

// ListUserOrganizations lists all organizations a user belongs to
func (s *OrganizationService) ListUserOrganizations(ctx context.Context, userID uuid.UUID) ([]domain.OrganizationWithRole, error) {
	return s.orgRepo.ListByUserID(ctx, userID)
}

// UpdateOrganization updates an organization's settings
func (s *OrganizationService) UpdateOrganization(ctx context.Context, userID, orgID uuid.UUID, name *string, billingEmail *string) (*domain.Organization, error) {
	// Check permissions
	member, err := s.orgRepo.GetMember(ctx, orgID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get member: %w", err)
	}
	if member == nil {
		return nil, fmt.Errorf("not a member of this organization")
	}
	if !domain.CanManageMembers(member.Role) {
		return nil, fmt.Errorf("insufficient permissions")
	}

	// Get existing org
	org, err := s.orgRepo.GetByID(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("failed to get organization: %w", err)
	}
	if org == nil {
		return nil, fmt.Errorf("organization not found")
	}

	// Update fields
	if name != nil {
		org.Name = *name
	}
	if billingEmail != nil {
		org.BillingEmail = billingEmail
	}

	if err := s.orgRepo.Update(ctx, org); err != nil {
		return nil, fmt.Errorf("failed to update organization: %w", err)
	}

	return org, nil
}

// DeleteOrganization deletes an organization (owner only)
func (s *OrganizationService) DeleteOrganization(ctx context.Context, userID, orgID uuid.UUID) error {
	// Check permissions
	member, err := s.orgRepo.GetMember(ctx, orgID, userID)
	if err != nil {
		return fmt.Errorf("failed to get member: %w", err)
	}
	if member == nil {
		return fmt.Errorf("not a member of this organization")
	}
	if !domain.CanDeleteOrganization(member.Role) {
		return fmt.Errorf("only owners can delete organizations")
	}

	if err := s.orgRepo.Delete(ctx, orgID); err != nil {
		return fmt.Errorf("failed to delete organization: %w", err)
	}

	logger.Infof("org: deleted organization %s by user %s", orgID, userID)
	return nil
}

// ========== Member Management ==========

// ListMembers lists all members of an organization
func (s *OrganizationService) ListMembers(ctx context.Context, userID, orgID uuid.UUID) ([]domain.OrganizationMember, error) {
	// Check membership
	member, err := s.orgRepo.GetMember(ctx, orgID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get member: %w", err)
	}
	if member == nil {
		return nil, fmt.Errorf("not a member of this organization")
	}

	return s.orgRepo.ListMembers(ctx, orgID)
}

// UpdateMemberRole updates a member's role
func (s *OrganizationService) UpdateMemberRole(ctx context.Context, currentUserID, orgID, targetUserID uuid.UUID, newRole string) (*domain.OrganizationMember, error) {
	// Check permissions
	member, err := s.orgRepo.GetMember(ctx, orgID, currentUserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get member: %w", err)
	}
	if member == nil {
		return nil, fmt.Errorf("not a member of this organization")
	}
	if !domain.CanManageMembers(member.Role) {
		return nil, fmt.Errorf("insufficient permissions")
	}

	// Can't change your own role
	if currentUserID == targetUserID {
		return nil, fmt.Errorf("cannot change your own role")
	}

	// Check if changing to/from owner
	targetMember, err := s.orgRepo.GetMember(ctx, orgID, targetUserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get target member: %w", err)
	}
	if targetMember == nil {
		return nil, fmt.Errorf("target user is not a member")
	}

	// Only owners can change owner roles
	if (targetMember.Role == domain.RoleOwner || newRole == domain.RoleOwner) && member.Role != domain.RoleOwner {
		return nil, fmt.Errorf("only owners can modify owner roles")
	}

	// Prevent removing the last owner
	if targetMember.Role == domain.RoleOwner && newRole != domain.RoleOwner {
		count, err := s.orgRepo.CountOwners(ctx, orgID)
		if err != nil {
			return nil, fmt.Errorf("failed to count owners: %w", err)
		}
		if count <= 1 {
			return nil, fmt.Errorf("cannot remove the last owner")
		}
	}

	if err := s.orgRepo.UpdateMemberRole(ctx, orgID, targetUserID, newRole); err != nil {
		return nil, fmt.Errorf("failed to update role: %w", err)
	}

	return s.orgRepo.GetMember(ctx, orgID, targetUserID)
}

// RemoveMember removes a member from an organization
func (s *OrganizationService) RemoveMember(ctx context.Context, currentUserID, orgID, targetUserID uuid.UUID) error {
	// Check permissions
	member, err := s.orgRepo.GetMember(ctx, orgID, currentUserID)
	if err != nil {
		return fmt.Errorf("failed to get member: %w", err)
	}
	if member == nil {
		return fmt.Errorf("not a member of this organization")
	}

	// Users can remove themselves (leave org)
	if currentUserID != targetUserID && !domain.CanManageMembers(member.Role) {
		return fmt.Errorf("insufficient permissions")
	}

	// Get target member
	targetMember, err := s.orgRepo.GetMember(ctx, orgID, targetUserID)
	if err != nil {
		return fmt.Errorf("failed to get target member: %w", err)
	}
	if targetMember == nil {
		return fmt.Errorf("target user is not a member")
	}

	// Prevent removing the last owner
	if targetMember.Role == domain.RoleOwner {
		count, err := s.orgRepo.CountOwners(ctx, orgID)
		if err != nil {
			return fmt.Errorf("failed to count owners: %w", err)
		}
		if count <= 1 {
			return fmt.Errorf("cannot remove the last owner")
		}
	}

	if err := s.orgRepo.RemoveMember(ctx, orgID, targetUserID); err != nil {
		return fmt.Errorf("failed to remove member: %w", err)
	}

	logger.Infof("org: removed member %s from org %s by user %s", targetUserID, orgID, currentUserID)
	return nil
}

// ========== Workspace Management ==========

// CreateWorkspace creates a new workspace in an organization
func (s *OrganizationService) CreateWorkspace(ctx context.Context, userID, orgID uuid.UUID, name string, slug *string, environment, gatewayURL string) (*domain.Workspace, error) {
	// Check permissions
	member, err := s.orgRepo.GetMember(ctx, orgID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get member: %w", err)
	}
	if member == nil {
		return nil, fmt.Errorf("not a member of this organization")
	}
	if !domain.CanManageWorkspaces(member.Role) {
		return nil, fmt.Errorf("insufficient permissions")
	}

	// Generate slug if not provided
	var wsSlug string
	if slug != nil && *slug != "" {
		wsSlug = *slug
		exists, err := s.orgRepo.WorkspaceSlugExists(ctx, orgID, wsSlug)
		if err != nil {
			return nil, fmt.Errorf("failed to check slug: %w", err)
		}
		if exists {
			return nil, fmt.Errorf("workspace slug '%s' is already taken", wsSlug)
		}
	} else {
		wsSlug, err = s.orgRepo.GenerateUniqueWorkspaceSlug(ctx, orgID, name)
		if err != nil {
			return nil, fmt.Errorf("failed to generate slug: %w", err)
		}
	}

	ws := &domain.Workspace{
		OrganizationID: orgID,
		Slug:           wsSlug,
		Name:           name,
		Environment:    environment,
		GatewayURL:     gatewayURL,
	}

	if err := s.orgRepo.CreateWorkspace(ctx, ws); err != nil {
		return nil, fmt.Errorf("failed to create workspace: %w", err)
	}

	logger.Infof("org: created workspace %s in org %s by user %s", ws.Name, orgID, userID)
	return ws, nil
}

// GetWorkspace gets a workspace by ID or slug
func (s *OrganizationService) GetWorkspace(ctx context.Context, userID, orgID uuid.UUID, wsID *uuid.UUID, wsSlug *string) (*domain.Workspace, error) {
	// Check membership
	member, err := s.orgRepo.GetMember(ctx, orgID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get member: %w", err)
	}
	if member == nil {
		return nil, fmt.Errorf("not a member of this organization")
	}

	if wsID != nil {
		return s.orgRepo.GetWorkspaceByID(ctx, *wsID)
	}
	if wsSlug != nil {
		return s.orgRepo.GetWorkspaceBySlug(ctx, orgID, *wsSlug)
	}
	return nil, fmt.Errorf("either workspace_id or workspace_slug must be provided")
}

// ListWorkspaces lists all workspaces in an organization
func (s *OrganizationService) ListWorkspaces(ctx context.Context, userID, orgID uuid.UUID) ([]domain.Workspace, error) {
	// Check membership
	member, err := s.orgRepo.GetMember(ctx, orgID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get member: %w", err)
	}
	if member == nil {
		return nil, fmt.Errorf("not a member of this organization")
	}

	workspaces, err := s.orgRepo.ListWorkspaces(ctx, orgID)
	if err != nil {
		return nil, err
	}

	// If no local workspaces, check if we're in cloud/EE mode and auto-create
	// the workspace from the cloud config
	if len(workspaces) == 0 {
		ws := s.ensureDefaultWorkspace(ctx, orgID)
		if ws != nil {
			return []domain.Workspace{*ws}, nil
		}
	}

	return workspaces, nil
}

// ensureDefaultWorkspace creates a local workspace record.
// In cloud/EE mode, it uses the workspace info from auth_config.
// In self-hosted mode, it creates a generic default workspace.
func (s *OrganizationService) ensureDefaultWorkspace(ctx context.Context, orgID uuid.UUID) *domain.Workspace {
	ws := &domain.Workspace{
		OrganizationID: orgID,
		Slug:           "default",
		Name:           "Default",
		Environment:    domain.EnvProduction,
	}

	// In cloud/EE mode, use the workspace info stored during instance connect
	if s.authConfigRepo != nil {
		cfg, err := s.authConfigRepo.Get(ctx)
		if err == nil && cfg != nil && cfg.AuthMode == "cloud" {
			if cfg.CloudWorkspaceID != nil {
				if wsID, err := uuid.Parse(*cfg.CloudWorkspaceID); err == nil {
					ws.ID = wsID
				}
			}
			if cfg.CloudWorkspaceSlug != nil {
				ws.Slug = *cfg.CloudWorkspaceSlug
				ws.Name = *cfg.CloudWorkspaceSlug
			}
			if cfg.CloudGatewayURL != nil {
				ws.GatewayURL = *cfg.CloudGatewayURL
			}
		}
	}

	if err := s.orgRepo.CreateWorkspace(ctx, ws); err != nil {
		logger.WithError(err).Warn("org: failed to auto-create default workspace")
		return nil
	}
	return ws
}

// UpdateWorkspace updates a workspace
func (s *OrganizationService) UpdateWorkspace(ctx context.Context, userID uuid.UUID, wsID uuid.UUID, name, gatewayURL *string) (*domain.Workspace, error) {
	// Get workspace to find org ID
	ws, err := s.orgRepo.GetWorkspaceByID(ctx, wsID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace: %w", err)
	}
	if ws == nil {
		return nil, fmt.Errorf("workspace not found")
	}

	// Check permissions
	member, err := s.orgRepo.GetMember(ctx, ws.OrganizationID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get member: %w", err)
	}
	if member == nil {
		return nil, fmt.Errorf("not a member of this organization")
	}
	if !domain.CanManageWorkspaces(member.Role) {
		return nil, fmt.Errorf("insufficient permissions")
	}

	// Update fields
	if name != nil {
		ws.Name = *name
	}
	if gatewayURL != nil {
		ws.GatewayURL = *gatewayURL
	}

	if err := s.orgRepo.UpdateWorkspace(ctx, ws); err != nil {
		return nil, fmt.Errorf("failed to update workspace: %w", err)
	}

	return ws, nil
}

// ========== Workspace Member Management ==========

// ListWorkspaceMembers returns all members with access to a workspace,
// merging implicit (org owners/admins) and explicit workspace members.
func (s *OrganizationService) ListWorkspaceMembers(ctx context.Context, userID, wsID uuid.UUID) ([]domain.WorkspaceMember, error) {
	// Resolve org ID from workspace
	orgID, err := s.orgRepo.GetWorkspaceOrgID(ctx, wsID)
	if err != nil {
		return nil, fmt.Errorf("workspace not found: %w", err)
	}

	// Verify caller is an org member
	callerMember, err := s.orgRepo.GetMember(ctx, orgID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if callerMember == nil {
		return nil, fmt.Errorf("not a member of this organization")
	}

	// Get explicit workspace members
	explicit, err := s.orgRepo.ListWorkspaceMembers(ctx, wsID)
	if err != nil {
		return nil, fmt.Errorf("failed to list workspace members: %w", err)
	}

	// Get org members to find implicit access (owners/admins)
	orgMembers, err := s.orgRepo.ListMembers(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("failed to list org members: %w", err)
	}

	// Build set of explicit user IDs
	explicitIDs := make(map[uuid.UUID]bool, len(explicit))
	for _, m := range explicit {
		explicitIDs[m.UserID] = true
	}

	// Add implicit members (org owners/admins not already explicit)
	var result []domain.WorkspaceMember
	for _, om := range orgMembers {
		if om.Role != domain.RoleOwner && om.Role != domain.RoleAdmin {
			continue
		}
		if explicitIDs[om.UserID] {
			continue
		}
		result = append(result, domain.WorkspaceMember{
			ID:             om.ID,
			WorkspaceID:    wsID,
			OrganizationID: orgID,
			UserID:         om.UserID,
			Role:           om.Role,
			CreatedAt:      om.JoinedAt,
			Email:          om.Email,
			Name:           om.Name,
			AvatarURL:      om.AvatarURL,
			AccessSource:   "implicit",
			OrgRole:        om.Role,
		})
	}

	// Append explicit members
	result = append(result, explicit...)

	return result, nil
}

// AddWorkspaceMember adds an org member to a workspace
func (s *OrganizationService) AddWorkspaceMember(ctx context.Context, currentUserID, wsID, targetUserID uuid.UUID, role string) (*domain.WorkspaceMember, error) {
	orgID, err := s.orgRepo.GetWorkspaceOrgID(ctx, wsID)
	if err != nil {
		return nil, fmt.Errorf("workspace not found: %w", err)
	}

	// Check caller permissions
	callerMember, err := s.orgRepo.GetMember(ctx, orgID, currentUserID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if callerMember == nil {
		return nil, fmt.Errorf("not a member of this organization")
	}

	callerWsRole := ""
	if callerMember.Role != domain.RoleOwner && callerMember.Role != domain.RoleAdmin {
		callerWs, err := s.orgRepo.GetWorkspaceMember(ctx, wsID, currentUserID)
		if err != nil {
			return nil, fmt.Errorf("failed to check workspace membership: %w", err)
		}
		if callerWs != nil {
			callerWsRole = callerWs.Role
		}
	}

	if !domain.CanManageWorkspaceMembers(callerMember.Role, callerWsRole) {
		return nil, fmt.Errorf("insufficient permissions to manage workspace members")
	}

	// Verify target is an org member
	targetOrgMember, err := s.orgRepo.GetMember(ctx, orgID, targetUserID)
	if err != nil {
		return nil, fmt.Errorf("failed to check target membership: %w", err)
	}
	if targetOrgMember == nil {
		return nil, fmt.Errorf("user is not a member of this organization")
	}

	// Don't add org owners/admins — they already have implicit access
	if targetOrgMember.Role == domain.RoleOwner || targetOrgMember.Role == domain.RoleAdmin {
		return nil, fmt.Errorf("org owners and admins already have implicit access to all workspaces")
	}

	if err := s.orgRepo.AddWorkspaceMember(ctx, wsID, orgID, targetUserID, role, &currentUserID); err != nil {
		return nil, fmt.Errorf("failed to add workspace member: %w", err)
	}

	member, err := s.orgRepo.GetWorkspaceMember(ctx, wsID, targetUserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get new member: %w", err)
	}

	logger.Infof("org: added user %s to workspace %s as %s by user %s", targetUserID, wsID, role, currentUserID)
	return member, nil
}

// UpdateWorkspaceMemberRole updates a workspace member's role
func (s *OrganizationService) UpdateWorkspaceMemberRole(ctx context.Context, currentUserID, wsID, targetUserID uuid.UUID, newRole string) (*domain.WorkspaceMember, error) {
	orgID, err := s.orgRepo.GetWorkspaceOrgID(ctx, wsID)
	if err != nil {
		return nil, fmt.Errorf("workspace not found: %w", err)
	}

	callerMember, err := s.orgRepo.GetMember(ctx, orgID, currentUserID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if callerMember == nil {
		return nil, fmt.Errorf("not a member of this organization")
	}

	callerWsRole := ""
	if callerMember.Role != domain.RoleOwner && callerMember.Role != domain.RoleAdmin {
		callerWs, err := s.orgRepo.GetWorkspaceMember(ctx, wsID, currentUserID)
		if err != nil {
			return nil, fmt.Errorf("failed to check workspace membership: %w", err)
		}
		if callerWs != nil {
			callerWsRole = callerWs.Role
		}
	}

	if !domain.CanManageWorkspaceMembers(callerMember.Role, callerWsRole) {
		return nil, fmt.Errorf("insufficient permissions")
	}

	existing, err := s.orgRepo.GetWorkspaceMember(ctx, wsID, targetUserID)
	if err != nil {
		return nil, fmt.Errorf("failed to get workspace member: %w", err)
	}
	if existing == nil {
		return nil, fmt.Errorf("user is not an explicit workspace member")
	}

	if err := s.orgRepo.UpdateWorkspaceMemberRole(ctx, orgID, wsID, targetUserID, newRole); err != nil {
		return nil, fmt.Errorf("failed to update role: %w", err)
	}

	return s.orgRepo.GetWorkspaceMember(ctx, wsID, targetUserID)
}

// RemoveWorkspaceMember removes a member from a workspace
func (s *OrganizationService) RemoveWorkspaceMember(ctx context.Context, currentUserID, wsID, targetUserID uuid.UUID) error {
	orgID, err := s.orgRepo.GetWorkspaceOrgID(ctx, wsID)
	if err != nil {
		return fmt.Errorf("workspace not found: %w", err)
	}

	callerMember, err := s.orgRepo.GetMember(ctx, orgID, currentUserID)
	if err != nil {
		return fmt.Errorf("failed to check membership: %w", err)
	}
	if callerMember == nil {
		return fmt.Errorf("not a member of this organization")
	}

	callerWsRole := ""
	if callerMember.Role != domain.RoleOwner && callerMember.Role != domain.RoleAdmin {
		callerWs, err := s.orgRepo.GetWorkspaceMember(ctx, wsID, currentUserID)
		if err != nil {
			return fmt.Errorf("failed to check workspace membership: %w", err)
		}
		if callerWs != nil {
			callerWsRole = callerWs.Role
		}
	}

	if currentUserID != targetUserID && !domain.CanManageWorkspaceMembers(callerMember.Role, callerWsRole) {
		return fmt.Errorf("insufficient permissions")
	}

	existing, err := s.orgRepo.GetWorkspaceMember(ctx, wsID, targetUserID)
	if err != nil {
		return fmt.Errorf("failed to get workspace member: %w", err)
	}
	if existing == nil {
		return fmt.Errorf("user is not an explicit workspace member — implicit members must be removed at the org level")
	}

	if err := s.orgRepo.RemoveWorkspaceMember(ctx, orgID, wsID, targetUserID); err != nil {
		return fmt.Errorf("failed to remove workspace member: %w", err)
	}

	logger.Infof("org: removed user %s from workspace %s by user %s", targetUserID, wsID, currentUserID)
	return nil
}

// ListAvailableWorkspaceMembers returns org members who can be added to a workspace
func (s *OrganizationService) ListAvailableWorkspaceMembers(ctx context.Context, userID, wsID uuid.UUID) ([]domain.OrganizationMember, error) {
	orgID, err := s.orgRepo.GetWorkspaceOrgID(ctx, wsID)
	if err != nil {
		return nil, fmt.Errorf("workspace not found: %w", err)
	}

	callerMember, err := s.orgRepo.GetMember(ctx, orgID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to check membership: %w", err)
	}
	if callerMember == nil {
		return nil, fmt.Errorf("not a member of this organization")
	}

	return s.orgRepo.ListAvailableWorkspaceMembers(ctx, wsID, orgID)
}

// DeleteWorkspace deletes a workspace
func (s *OrganizationService) DeleteWorkspace(ctx context.Context, userID, wsID uuid.UUID) error {
	// Get workspace to find org ID
	ws, err := s.orgRepo.GetWorkspaceByID(ctx, wsID)
	if err != nil {
		return fmt.Errorf("failed to get workspace: %w", err)
	}
	if ws == nil {
		return fmt.Errorf("workspace not found")
	}

	// Check permissions
	member, err := s.orgRepo.GetMember(ctx, ws.OrganizationID, userID)
	if err != nil {
		return fmt.Errorf("failed to get member: %w", err)
	}
	if member == nil {
		return fmt.Errorf("not a member of this organization")
	}
	if !domain.CanManageWorkspaces(member.Role) {
		return fmt.Errorf("insufficient permissions")
	}

	if err := s.orgRepo.DeleteWorkspace(ctx, wsID); err != nil {
		return fmt.Errorf("failed to delete workspace: %w", err)
	}

	logger.Infof("org: deleted workspace %s by user %s", wsID, userID)
	return nil
}
