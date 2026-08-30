package repository

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// OrganizationRepository handles database operations for organizations
type OrganizationRepository struct {
	db *sqlx.DB
}

// NewOrganizationRepository creates a new organization repository
func NewOrganizationRepository(db *sqlx.DB) *OrganizationRepository {
	return &OrganizationRepository{db: db}
}

// Create creates a new organization
func (r *OrganizationRepository) Create(ctx context.Context, org *domain.Organization) error {
	query := `
		INSERT INTO everstack.organizations (id, slug, name, plan_tier, billing_email, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	now := time.Now()
	org.ID = uuid.New()
	org.CreatedAt = now
	org.UpdatedAt = now

	_, err := r.db.ExecContext(ctx, query,
		org.ID, org.Slug, org.Name, org.PlanTier, org.BillingEmail, org.CreatedAt, org.UpdatedAt,
	)
	return err
}

// GetByID gets an organization by ID
func (r *OrganizationRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Organization, error) {
	query := `
		SELECT id, slug, name, plan_tier, billing_email, stripe_customer_id, created_at, updated_at
		FROM everstack.organizations
		WHERE id = $1
	`
	var org domain.Organization
	if err := r.db.GetContext(ctx, &org, query, id); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &org, nil
}

// GetBySlug gets an organization by slug
func (r *OrganizationRepository) GetBySlug(ctx context.Context, slug string) (*domain.Organization, error) {
	query := `
		SELECT id, slug, name, plan_tier, billing_email, stripe_customer_id, created_at, updated_at
		FROM everstack.organizations
		WHERE slug = $1
	`
	var org domain.Organization
	if err := r.db.GetContext(ctx, &org, query, slug); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &org, nil
}

// ListByUserID lists all organizations a user belongs to
func (r *OrganizationRepository) ListByUserID(ctx context.Context, userID uuid.UUID) ([]domain.OrganizationWithRole, error) {
	query := `
		SELECT o.id, o.slug, o.name, o.plan_tier, o.billing_email, o.stripe_customer_id, 
		       o.created_at, o.updated_at, om.role
		FROM everstack.organizations o
		INNER JOIN everstack.organization_members om ON o.id = om.organization_id
		WHERE om.user_id = $1
		ORDER BY o.name
	`
	var orgs []domain.OrganizationWithRole
	if err := r.db.SelectContext(ctx, &orgs, query, userID); err != nil {
		return nil, err
	}
	return orgs, nil
}

// Update updates an organization
func (r *OrganizationRepository) Update(ctx context.Context, org *domain.Organization) error {
	query := `
		UPDATE everstack.organizations
		SET name = $1, billing_email = $2, updated_at = $3
		WHERE id = $4
	`
	org.UpdatedAt = time.Now()
	_, err := r.db.ExecContext(ctx, query, org.Name, org.BillingEmail, org.UpdatedAt, org.ID)
	return err
}

// Delete deletes an organization
func (r *OrganizationRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM everstack.organizations WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, id)
	return err
}

// SlugExists checks if a slug already exists
func (r *OrganizationRepository) SlugExists(ctx context.Context, slug string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM everstack.organizations WHERE slug = $1)`
	var exists bool
	err := r.db.GetContext(ctx, &exists, query, slug)
	return exists, err
}

// GenerateUniqueSlug generates a unique slug from a name
func (r *OrganizationRepository) GenerateUniqueSlug(ctx context.Context, name string) (string, error) {
	baseSlug := slugify(name)
	slug := baseSlug

	for i := 1; i <= 100; i++ {
		exists, err := r.SlugExists(ctx, slug)
		if err != nil {
			return "", err
		}
		if !exists {
			return slug, nil
		}
		slug = fmt.Sprintf("%s-%d", baseSlug, i)
	}

	// Fallback to UUID suffix
	return fmt.Sprintf("%s-%s", baseSlug, uuid.New().String()[:8]), nil
}

// ========== Member Operations ==========

// AddMember adds a user to an organization
func (r *OrganizationRepository) AddMember(ctx context.Context, orgID, userID uuid.UUID, role string, invitedBy *uuid.UUID) error {
	query := `
		INSERT INTO everstack.organization_members (id, organization_id, user_id, role, invited_by, joined_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := r.db.ExecContext(ctx, query, uuid.New(), orgID, userID, role, invitedBy, time.Now())
	return err
}

// GetMember gets a member by org and user ID
func (r *OrganizationRepository) GetMember(ctx context.Context, orgID, userID uuid.UUID) (*domain.OrganizationMember, error) {
	query := `
		SELECT om.id, om.organization_id, om.user_id, om.role, om.invited_by, om.joined_at,
		       u.email, u.name, u.avatar_url
		FROM everstack.organization_members om
		INNER JOIN everstack.users u ON om.user_id = u.id
		WHERE om.organization_id = $1 AND om.user_id = $2
	`
	var member domain.OrganizationMember
	if err := r.db.GetContext(ctx, &member, query, orgID, userID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &member, nil
}

// ListMembers lists all members of an organization
func (r *OrganizationRepository) ListMembers(ctx context.Context, orgID uuid.UUID) ([]domain.OrganizationMember, error) {
	query := `
		SELECT om.id, om.organization_id, om.user_id, om.role, om.invited_by, om.joined_at,
		       u.email, u.name, u.avatar_url
		FROM everstack.organization_members om
		INNER JOIN everstack.users u ON om.user_id = u.id
		WHERE om.organization_id = $1
		ORDER BY om.joined_at
	`
	var members []domain.OrganizationMember
	if err := r.db.SelectContext(ctx, &members, query, orgID); err != nil {
		return nil, err
	}
	return members, nil
}

// UpdateMemberRole updates a member's role
func (r *OrganizationRepository) UpdateMemberRole(ctx context.Context, orgID, userID uuid.UUID, role string) error {
	query := `
		UPDATE everstack.organization_members
		SET role = $1
		WHERE organization_id = $2 AND user_id = $3
	`
	_, err := r.db.ExecContext(ctx, query, role, orgID, userID)
	return err
}

// RemoveMember removes a member from an organization
func (r *OrganizationRepository) RemoveMember(ctx context.Context, orgID, userID uuid.UUID) error {
	query := `DELETE FROM everstack.organization_members WHERE organization_id = $1 AND user_id = $2`
	_, err := r.db.ExecContext(ctx, query, orgID, userID)
	return err
}

// CountOwners counts the number of owners in an organization
func (r *OrganizationRepository) CountOwners(ctx context.Context, orgID uuid.UUID) (int, error) {
	query := `
		SELECT COUNT(*) FROM everstack.organization_members
		WHERE organization_id = $1 AND role = 'owner'
	`
	var count int
	err := r.db.GetContext(ctx, &count, query, orgID)
	return count, err
}

// ========== Workspace Operations ==========

// CreateWorkspace creates a new workspace
func (r *OrganizationRepository) CreateWorkspace(ctx context.Context, ws *domain.Workspace) error {
	query := `
		INSERT INTO everstack.workspaces (id, organization_id, slug, name, environment, gateway_url, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	now := time.Now()
	if ws.ID == uuid.Nil {
		ws.ID = uuid.New()
	}
	ws.CreatedAt = now
	ws.UpdatedAt = now

	_, err := r.db.ExecContext(ctx, query,
		ws.ID, ws.OrganizationID, ws.Slug, ws.Name, ws.Environment, ws.GatewayURL, ws.CreatedAt, ws.UpdatedAt,
	)
	return err
}

// GetWorkspaceByID gets a workspace by ID
func (r *OrganizationRepository) GetWorkspaceByID(ctx context.Context, id uuid.UUID) (*domain.Workspace, error) {
	query := `
		SELECT id, organization_id, slug, name, environment, gateway_url, created_at, updated_at
		FROM everstack.workspaces
		WHERE id = $1
	`
	var ws domain.Workspace
	if err := r.db.GetContext(ctx, &ws, query, id); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &ws, nil
}

// GetWorkspaceBySlug gets a workspace by organization ID and slug
func (r *OrganizationRepository) GetWorkspaceBySlug(ctx context.Context, orgID uuid.UUID, slug string) (*domain.Workspace, error) {
	query := `
		SELECT id, organization_id, slug, name, environment, gateway_url, created_at, updated_at
		FROM everstack.workspaces
		WHERE organization_id = $1 AND slug = $2
	`
	var ws domain.Workspace
	if err := r.db.GetContext(ctx, &ws, query, orgID, slug); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &ws, nil
}

// ListWorkspaces lists all workspaces in an organization
func (r *OrganizationRepository) ListWorkspaces(ctx context.Context, orgID uuid.UUID) ([]domain.Workspace, error) {
	query := `
		SELECT id, organization_id, slug, name, environment, gateway_url, created_at, updated_at
		FROM everstack.workspaces
		WHERE organization_id = $1
		ORDER BY name
	`
	var workspaces []domain.Workspace
	if err := r.db.SelectContext(ctx, &workspaces, query, orgID); err != nil {
		return nil, err
	}
	return workspaces, nil
}

// UpdateWorkspace updates a workspace
func (r *OrganizationRepository) UpdateWorkspace(ctx context.Context, ws *domain.Workspace) error {
	query := `
		UPDATE everstack.workspaces
		SET name = $1, gateway_url = $2, updated_at = $3
		WHERE id = $4
	`
	ws.UpdatedAt = time.Now()
	_, err := r.db.ExecContext(ctx, query, ws.Name, ws.GatewayURL, ws.UpdatedAt, ws.ID)
	return err
}

// DeleteWorkspace deletes a workspace
func (r *OrganizationRepository) DeleteWorkspace(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM everstack.workspaces WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, id)
	return err
}

// WorkspaceSlugExists checks if a workspace slug exists in an organization
func (r *OrganizationRepository) WorkspaceSlugExists(ctx context.Context, orgID uuid.UUID, slug string) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM everstack.workspaces WHERE organization_id = $1 AND slug = $2)`
	var exists bool
	err := r.db.GetContext(ctx, &exists, query, orgID, slug)
	return exists, err
}

// GenerateUniqueWorkspaceSlug generates a unique workspace slug
func (r *OrganizationRepository) GenerateUniqueWorkspaceSlug(ctx context.Context, orgID uuid.UUID, name string) (string, error) {
	baseSlug := slugify(name)
	slug := baseSlug

	for i := 1; i <= 100; i++ {
		exists, err := r.WorkspaceSlugExists(ctx, orgID, slug)
		if err != nil {
			return "", err
		}
		if !exists {
			return slug, nil
		}
		slug = fmt.Sprintf("%s-%d", baseSlug, i)
	}

	return fmt.Sprintf("%s-%s", baseSlug, uuid.New().String()[:8]), nil
}

// ========== Self-hosted Auth Helpers ==========

// OrganizationMemberBasic represents a basic member record
type OrganizationMemberBasic struct {
	UserID    uuid.UUID `db:"user_id"`
	Role      string    `db:"role"`
	CreatedAt time.Time `db:"created_at"`
}

// CreateSimple creates a new organization with just slug and name (for self-hosted)
func (r *OrganizationRepository) CreateSimple(ctx context.Context, slug, name string) (*domain.Organization, error) {
	org := &domain.Organization{
		ID:        uuid.New(),
		Slug:      slug,
		Name:      name,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	query := `
		INSERT INTO everstack.organizations (id, slug, name, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
	`
	_, err := r.db.ExecContext(ctx, query, org.ID, org.Slug, org.Name, org.CreatedAt, org.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return org, nil
}

// EnsureSimpleWithID ensures a cloud-managed organization exists locally with
// the cloud organization UUID. Keeping the IDs aligned lets the admin app use
// the same tenant_id for auth, sandbox, billing, and control-plane calls.
func (r *OrganizationRepository) EnsureSimpleWithID(ctx context.Context, id uuid.UUID, slug, name string) (*domain.Organization, error) {
	if org, err := r.GetByID(ctx, id); err != nil {
		return nil, err
	} else if org != nil {
		return org, nil
	}
	if org, err := r.GetBySlug(ctx, slug); err != nil {
		return nil, err
	} else if org != nil {
		return org, nil
	}

	org := &domain.Organization{
		ID:        id,
		Slug:      slug,
		Name:      name,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	query := `
		INSERT INTO everstack.organizations (id, slug, name, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE SET
			slug = EXCLUDED.slug,
			name = EXCLUDED.name,
			updated_at = EXCLUDED.updated_at
	`
	if _, err := r.db.ExecContext(ctx, query, org.ID, org.Slug, org.Name, org.CreatedAt, org.UpdatedAt); err != nil {
		if existing, getErr := r.GetBySlug(ctx, slug); getErr == nil && existing != nil {
			return existing, nil
		}
		return nil, err
	}
	return org, nil
}

// AddMemberSimple adds a user to an organization (simpler version for self-hosted)
func (r *OrganizationRepository) AddMemberSimple(ctx context.Context, orgID, userID uuid.UUID, role string) error {
	query := `
		INSERT INTO everstack.organization_members (id, organization_id, user_id, role, joined_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (organization_id, user_id) DO UPDATE SET role = EXCLUDED.role
	`
	_, err := r.db.ExecContext(ctx, query, uuid.New(), orgID, userID, role, time.Now())
	return err
}

// ListPaginated returns organizations with pagination support
func (r *OrganizationRepository) ListPaginated(ctx context.Context, limit, offset int) ([]domain.Organization, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	query := `
		SELECT id, slug, name, COALESCE(plan_tier, '') as plan_tier, billing_email, stripe_customer_id, created_at, updated_at
		FROM everstack.organizations
		ORDER BY created_at
		LIMIT $1 OFFSET $2
	`
	var orgs []domain.Organization
	if err := r.db.SelectContext(ctx, &orgs, query, limit, offset); err != nil {
		return nil, err
	}
	return orgs, nil
}

// List returns all organizations
func (r *OrganizationRepository) List(ctx context.Context) ([]domain.Organization, error) {
	return r.ListPaginated(ctx, 1000, 0)
}

// ListAllMembersPaginated lists all members across all organizations with pagination
func (r *OrganizationRepository) ListAllMembersPaginated(ctx context.Context, limit, offset int) ([]OrganizationMemberBasic, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	query := `
		SELECT DISTINCT user_id, role, created_at
		FROM everstack.organization_members
		ORDER BY created_at
		LIMIT $1 OFFSET $2
	`
	var members []OrganizationMemberBasic
	if err := r.db.SelectContext(ctx, &members, query, limit, offset); err != nil {
		return nil, err
	}
	return members, nil
}

// ListAllMembers lists all members across all organizations
func (r *OrganizationRepository) ListAllMembers(ctx context.Context) ([]OrganizationMemberBasic, error) {
	return r.ListAllMembersPaginated(ctx, 1000, 0)
}

// IsOwner checks if a user is an owner of any organization
func (r *OrganizationRepository) IsOwner(ctx context.Context, userID uuid.UUID) (bool, error) {
	query := `SELECT EXISTS(SELECT 1 FROM everstack.organization_members WHERE user_id = $1 AND role = 'owner')`
	var exists bool
	err := r.db.GetContext(ctx, &exists, query, userID)
	return exists, err
}

// RemoveMemberFromAll removes a user from all organizations
func (r *OrganizationRepository) RemoveMemberFromAll(ctx context.Context, userID uuid.UUID) error {
	query := `DELETE FROM everstack.organization_members WHERE user_id = $1`
	_, err := r.db.ExecContext(ctx, query, userID)
	return err
}

// ========== Workspace Member Operations ==========

// AddWorkspaceMember adds a user to a workspace
func (r *OrganizationRepository) AddWorkspaceMember(ctx context.Context, wsID, orgID, userID uuid.UUID, role string, addedBy *uuid.UUID) error {
	query := `
		INSERT INTO everstack.workspace_members (id, workspace_id, organization_id, user_id, role, added_by, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $7)
		ON CONFLICT (workspace_id, user_id) DO NOTHING
	`
	_, err := r.db.ExecContext(ctx, query, uuid.New(), wsID, orgID, userID, role, addedBy, time.Now())
	return err
}

// GetWorkspaceMember gets a workspace member by workspace and user ID
func (r *OrganizationRepository) GetWorkspaceMember(ctx context.Context, wsID, userID uuid.UUID) (*domain.WorkspaceMember, error) {
	query := `
		SELECT wm.id, wm.workspace_id, wm.organization_id, wm.user_id, wm.role,
		       wm.added_by, wm.created_at, wm.updated_at,
		       u.email, u.name, u.avatar_url
		FROM everstack.workspace_members wm
		INNER JOIN everstack.users u ON wm.user_id = u.id
		WHERE wm.workspace_id = $1 AND wm.user_id = $2
	`
	var member domain.WorkspaceMember
	if err := r.db.GetContext(ctx, &member, query, wsID, userID); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	member.AccessSource = "explicit"
	return &member, nil
}

// ListWorkspaceMembers lists all explicit members of a workspace
func (r *OrganizationRepository) ListWorkspaceMembers(ctx context.Context, wsID uuid.UUID) ([]domain.WorkspaceMember, error) {
	query := `
		SELECT wm.id, wm.workspace_id, wm.organization_id, wm.user_id, wm.role,
		       wm.added_by, wm.created_at, wm.updated_at,
		       u.email, u.name, u.avatar_url
		FROM everstack.workspace_members wm
		INNER JOIN everstack.users u ON wm.user_id = u.id
		WHERE wm.workspace_id = $1
		ORDER BY wm.created_at
	`
	var members []domain.WorkspaceMember
	if err := r.db.SelectContext(ctx, &members, query, wsID); err != nil {
		return nil, err
	}
	for i := range members {
		members[i].AccessSource = "explicit"
	}
	return members, nil
}

// UpdateWorkspaceMemberRole updates a workspace member's role. The orgID is
// part of the predicate so a workspace id from another organization cannot
// be used to mutate that organization's members.
func (r *OrganizationRepository) UpdateWorkspaceMemberRole(ctx context.Context, orgID, wsID, userID uuid.UUID, role string) error {
	query := `
		UPDATE everstack.workspace_members
		SET role = $1, updated_at = $2
		WHERE organization_id = $3 AND workspace_id = $4 AND user_id = $5
	`
	res, err := r.db.ExecContext(ctx, query, role, time.Now(), orgID, wsID, userID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// RemoveWorkspaceMember removes a member from a workspace, scoped to the
// owning organization.
func (r *OrganizationRepository) RemoveWorkspaceMember(ctx context.Context, orgID, wsID, userID uuid.UUID) error {
	query := `DELETE FROM everstack.workspace_members WHERE organization_id = $1 AND workspace_id = $2 AND user_id = $3`
	res, err := r.db.ExecContext(ctx, query, orgID, wsID, userID)
	if err != nil {
		return err
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// ListAvailableWorkspaceMembers lists org members not yet in this workspace
// and not org owners/admins (who have implicit access)
func (r *OrganizationRepository) ListAvailableWorkspaceMembers(ctx context.Context, wsID, orgID uuid.UUID) ([]domain.OrganizationMember, error) {
	query := `
		SELECT om.id, om.organization_id, om.user_id, om.role, om.invited_by, om.joined_at,
		       u.email, u.name, u.avatar_url
		FROM everstack.organization_members om
		INNER JOIN everstack.users u ON om.user_id = u.id
		WHERE om.organization_id = $1
		  AND om.role NOT IN ('owner', 'admin')
		  AND om.user_id NOT IN (
		    SELECT wm.user_id FROM everstack.workspace_members wm WHERE wm.workspace_id = $2
		  )
		ORDER BY u.email
	`
	var members []domain.OrganizationMember
	if err := r.db.SelectContext(ctx, &members, query, orgID, wsID); err != nil {
		return nil, err
	}
	return members, nil
}

// GetWorkspaceOrgID returns the organization ID for a workspace
func (r *OrganizationRepository) GetWorkspaceOrgID(ctx context.Context, wsID uuid.UUID) (uuid.UUID, error) {
	query := `SELECT organization_id FROM everstack.workspaces WHERE id = $1`
	var orgID uuid.UUID
	err := r.db.GetContext(ctx, &orgID, query, wsID)
	return orgID, err
}

// ========== Helpers ==========

// slugify converts a name to a URL-safe slug
func slugify(name string) string {
	slug := strings.ToLower(name)
	slug = strings.ReplaceAll(slug, " ", "-")
	reg := regexp.MustCompile("[^a-z0-9-]")
	slug = reg.ReplaceAllString(slug, "")
	reg = regexp.MustCompile("-+")
	slug = reg.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 63 {
		slug = slug[:63]
	}
	if slug == "" {
		slug = "org"
	}
	return slug
}
