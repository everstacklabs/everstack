package repository

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Invitation represents a team invitation
type Invitation struct {
	ID             uuid.UUID  `db:"id"`
	Email          string     `db:"email"`
	Role           string     `db:"role"`
	Token          string     `db:"token"`
	InvitedBy      *uuid.UUID `db:"invited_by"`
	OrganizationID *uuid.UUID `db:"organization_id"`
	ExpiresAt      time.Time  `db:"expires_at"`
	AcceptedAt     *time.Time `db:"accepted_at"`
	CreatedAt      time.Time  `db:"created_at"`
}

// InvitationWithInviter includes invitation with inviter details
type InvitationWithInviter struct {
	Invitation
	InvitedByEmail *string `db:"invited_by_email"`
}

// InvitationRepository handles database operations for invitations
type InvitationRepository struct {
	db *sqlx.DB
}

// NewInvitationRepository creates a new invitation repository
func NewInvitationRepository(db *sqlx.DB) *InvitationRepository {
	return &InvitationRepository{db: db}
}

// Create creates a new invitation
func (r *InvitationRepository) Create(ctx context.Context, email, role string, invitedBy, orgID *uuid.UUID, expiresIn time.Duration) (*Invitation, error) {
	token, err := generateInviteToken()
	if err != nil {
		return nil, err
	}

	inv := &Invitation{
		ID:             uuid.New(),
		Email:          email,
		Role:           role,
		Token:          token,
		InvitedBy:      invitedBy,
		OrganizationID: orgID,
		ExpiresAt:      time.Now().Add(expiresIn),
		CreatedAt:      time.Now(),
	}

	query := `
		INSERT INTO everstack.invitations (id, email, role, token, invited_by, organization_id, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
	`
	_, err = r.db.ExecContext(ctx, query,
		inv.ID, inv.Email, inv.Role, inv.Token, inv.InvitedBy, inv.OrganizationID, inv.ExpiresAt, inv.CreatedAt,
	)
	if err != nil {
		return nil, err
	}

	return inv, nil
}

// GetByToken retrieves an invitation by token
func (r *InvitationRepository) GetByToken(ctx context.Context, token string) (*Invitation, error) {
	query := `SELECT * FROM everstack.invitations WHERE token = $1`
	var inv Invitation
	err := r.db.GetContext(ctx, &inv, query, token)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &inv, err
}

// GetByID retrieves an invitation by ID
func (r *InvitationRepository) GetByID(ctx context.Context, id uuid.UUID) (*Invitation, error) {
	query := `SELECT * FROM everstack.invitations WHERE id = $1`
	var inv Invitation
	err := r.db.GetContext(ctx, &inv, query, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &inv, err
}

// GetByEmail retrieves pending invitations for an email
func (r *InvitationRepository) GetByEmail(ctx context.Context, email string) ([]Invitation, error) {
	query := `
		SELECT * FROM everstack.invitations 
		WHERE email = $1 AND accepted_at IS NULL AND expires_at > NOW()
		ORDER BY created_at DESC
	`
	var invitations []Invitation
	err := r.db.SelectContext(ctx, &invitations, query, email)
	return invitations, err
}

// ListPending lists all pending invitations (optionally for an organization)
func (r *InvitationRepository) ListPending(ctx context.Context, orgID *uuid.UUID) ([]InvitationWithInviter, error) {
	var invitations []InvitationWithInviter
	var err error

	if orgID != nil {
		query := `
			SELECT i.*, u.email as invited_by_email
			FROM everstack.invitations i
			LEFT JOIN everstack.users u ON i.invited_by = u.id
			WHERE i.organization_id = $1 AND i.accepted_at IS NULL AND i.expires_at > NOW()
			ORDER BY i.created_at DESC
		`
		err = r.db.SelectContext(ctx, &invitations, query, orgID)
	} else {
		query := `
			SELECT i.*, u.email as invited_by_email
			FROM everstack.invitations i
			LEFT JOIN everstack.users u ON i.invited_by = u.id
			WHERE i.accepted_at IS NULL AND i.expires_at > NOW()
			ORDER BY i.created_at DESC
		`
		err = r.db.SelectContext(ctx, &invitations, query)
	}
	return invitations, err
}

// MarkAccepted marks an invitation as accepted
func (r *InvitationRepository) MarkAccepted(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE everstack.invitations SET accepted_at = $2 WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, id, time.Now())
	return err
}

// Delete deletes an invitation
func (r *InvitationRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM everstack.invitations WHERE id = $1`
	_, err := r.db.ExecContext(ctx, query, id)
	return err
}

// DeleteExpired deletes all expired invitations
func (r *InvitationRepository) DeleteExpired(ctx context.Context) (int64, error) {
	query := `DELETE FROM everstack.invitations WHERE expires_at < $1`
	result, err := r.db.ExecContext(ctx, query, time.Now())
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// CountPendingForOrg counts pending invitations for an organization
func (r *InvitationRepository) CountPendingForOrg(ctx context.Context, orgID uuid.UUID) (int, error) {
	query := `
		SELECT COUNT(*) FROM everstack.invitations 
		WHERE organization_id = $1 AND accepted_at IS NULL AND expires_at > NOW()
	`
	var count int
	err := r.db.GetContext(ctx, &count, query, orgID)
	return count, err
}

// generateInviteToken generates a secure random token for invitations
func generateInviteToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(b), nil
}
