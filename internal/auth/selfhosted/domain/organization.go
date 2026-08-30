package domain

import (
	"time"

	"github.com/google/uuid"
)

// Organization represents a tenant/organization
type Organization struct {
	ID               uuid.UUID `json:"id" db:"id"`
	Slug             string    `json:"slug" db:"slug"`
	Name             string    `json:"name" db:"name"`
	PlanTier         string    `json:"plan_tier" db:"plan_tier"`
	BillingEmail     *string   `json:"billing_email,omitempty" db:"billing_email"`
	StripeCustomerID *string   `json:"stripe_customer_id,omitempty" db:"stripe_customer_id"`
	CreatedAt        time.Time `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time `json:"updated_at" db:"updated_at"`
}

// OrganizationMember represents a user's membership in an organization
type OrganizationMember struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	OrganizationID uuid.UUID  `json:"organization_id" db:"organization_id"`
	UserID         uuid.UUID  `json:"user_id" db:"user_id"`
	Role           string     `json:"role" db:"role"`
	InvitedBy      *uuid.UUID `json:"invited_by,omitempty" db:"invited_by"`
	JoinedAt       time.Time  `json:"joined_at" db:"joined_at"`

	// User info (populated via join)
	Email     string  `json:"email" db:"email"`
	Name      *string `json:"name,omitempty" db:"name"`
	AvatarURL *string `json:"avatar_url,omitempty" db:"avatar_url"`
}

// OrganizationWithRole combines organization with the current user's role
type OrganizationWithRole struct {
	Organization
	Role string `json:"role" db:"role"`
}

// Workspace represents a workspace within an organization
type Workspace struct {
	ID             uuid.UUID `json:"id" db:"id"`
	OrganizationID uuid.UUID `json:"organization_id" db:"organization_id"`
	Slug           string    `json:"slug" db:"slug"`
	Name           string    `json:"name" db:"name"`
	Environment    string    `json:"environment" db:"environment"`
	GatewayURL     string    `json:"gateway_url" db:"gateway_url"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

// OrganizationInvitation represents a pending invitation
type OrganizationInvitation struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	OrganizationID uuid.UUID  `json:"organization_id" db:"organization_id"`
	Email          string     `json:"email" db:"email"`
	Role           string     `json:"role" db:"role"`
	Token          string     `json:"token" db:"token"`
	InvitedBy      uuid.UUID  `json:"invited_by" db:"invited_by"`
	ExpiresAt      time.Time  `json:"expires_at" db:"expires_at"`
	AcceptedAt     *time.Time `json:"accepted_at,omitempty" db:"accepted_at"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
}

// Role constants
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
	RoleViewer = "viewer"
)

// Environment constants
const (
	EnvDevelopment = "development"
	EnvStaging     = "staging"
	EnvProduction  = "production"
)

// CanManageMembers checks if role can manage members
func CanManageMembers(role string) bool {
	return role == RoleOwner || role == RoleAdmin
}

// CanManageBilling checks if role can manage billing
func CanManageBilling(role string) bool {
	return role == RoleOwner
}

// CanManageWorkspaces checks if role can manage workspaces
func CanManageWorkspaces(role string) bool {
	return role == RoleOwner || role == RoleAdmin
}

// CanDeleteOrganization checks if role can delete the organization
func CanDeleteOrganization(role string) bool {
	return role == RoleOwner
}

// ========== Workspace Member Types ==========

// Workspace member role constants (no owner — ownership is at org level)
const (
	WsRoleAdmin  = "admin"
	WsRoleMember = "member"
	WsRoleViewer = "viewer"
)

// WorkspaceMember represents a user's membership in a workspace
type WorkspaceMember struct {
	ID             uuid.UUID  `json:"id" db:"id"`
	WorkspaceID    uuid.UUID  `json:"workspace_id" db:"workspace_id"`
	OrganizationID uuid.UUID  `json:"organization_id" db:"organization_id"`
	UserID         uuid.UUID  `json:"user_id" db:"user_id"`
	Role           string     `json:"role" db:"role"`
	AddedBy        *uuid.UUID `json:"added_by,omitempty" db:"added_by"`
	CreatedAt      time.Time  `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at" db:"updated_at"`

	// User info (populated via join)
	Email     string  `json:"email" db:"email"`
	Name      *string `json:"name,omitempty" db:"name"`
	AvatarURL *string `json:"avatar_url,omitempty" db:"avatar_url"`

	// "explicit" (in workspace_members table) or "implicit" (org owner/admin)
	AccessSource string `json:"access_source" db:"-"`
	// Org-level role (populated for implicit members)
	OrgRole string `json:"org_role,omitempty" db:"-"`
}

// CanManageWorkspaceMembers checks if a user can manage workspace members
func CanManageWorkspaceMembers(orgRole, wsRole string) bool {
	return orgRole == RoleOwner || orgRole == RoleAdmin || wsRole == WsRoleAdmin
}
