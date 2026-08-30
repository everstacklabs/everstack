package domain

import (
	"time"

	"github.com/google/uuid"
)

// User represents an authenticated user
type User struct {
	ID         uuid.UUID `json:"id" db:"id"`
	ExternalID string    `json:"external_id" db:"external_id"` // WorkOS user ID
	Email      string    `json:"email" db:"email"`
	Name       *string   `json:"name,omitempty" db:"name"`
	AvatarURL  *string   `json:"avatar_url,omitempty" db:"avatar_url"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time `json:"updated_at" db:"updated_at"`
}

// Session represents a user session
type Session struct {
	ID        uuid.UUID `json:"id" db:"id"`
	UserID    uuid.UUID `json:"user_id" db:"user_id"`
	Token     string    `json:"token" db:"token"`
	ExpiresAt time.Time `json:"expires_at" db:"expires_at"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	IPAddress *string   `json:"ip_address,omitempty" db:"ip_address"`
	UserAgent *string   `json:"user_agent,omitempty" db:"user_agent"`

	// WorkOS OAuth tokens (Cloud mode only - kept for DB schema compatibility)
	WorkOSAccessToken    *string    `json:"-" db:"workos_access_token"`
	WorkOSRefreshToken   *string    `json:"-" db:"workos_refresh_token"`
	WorkOSTokenExpiresAt *time.Time `json:"-" db:"workos_token_expires_at"`
	WorkOSOrganizationID *string    `json:"-" db:"workos_organization_id"`

	// Token refresh backoff tracking (Cloud mode only - kept for DB schema compatibility)
	LastRefreshAttemptAt *time.Time `json:"-" db:"last_refresh_attempt_at"`
	RefreshFailureCount  int        `json:"-" db:"refresh_failure_count"`
}

// UserWithOrganizations includes user's organization memberships
type UserWithOrganizations struct {
	User
	Organizations []OrganizationMembership `json:"organizations"`
}

// OrganizationMembership represents a user's membership in an organization
type OrganizationMembership struct {
	ID   uuid.UUID `json:"id"`
	Slug string    `json:"slug"`
	Name string    `json:"name"`
	Role string    `json:"role"`
}

// IsExpired checks if the session has expired
func (s *Session) IsExpired() bool {
	return time.Now().After(s.ExpiresAt)
}

// IsValid checks if the session is valid
func (s *Session) IsValid() bool {
	return !s.IsExpired()
}

// AuthResult represents the result of authentication
type AuthResult struct {
	User         *User     `json:"user"`
	SessionToken string    `json:"session_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	IsNewUser    bool      `json:"is_new_user"`
}
