package repository

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// UserRepository handles database operations for users
type UserRepository struct {
	db *sqlx.DB
}

// NewUserRepository creates a new user repository
func NewUserRepository(db *sqlx.DB) *UserRepository {
	return &UserRepository{db: db}
}

// Create creates a new user
func (r *UserRepository) Create(ctx context.Context, user *domain.User) error {
	if user.ID == uuid.Nil {
		user.ID = uuid.New()
	}
	if user.ExternalID == "" {
		user.ExternalID = "local:" + user.ID.String()
	}
	now := time.Now()
	user.CreatedAt = now
	user.UpdatedAt = now

	query := `
		INSERT INTO everstack.users (id, external_id, email, name, avatar_url, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
	`
	_, err := r.db.ExecContext(ctx, query,
		user.ID, user.ExternalID, user.Email, user.Name, user.AvatarURL,
		user.CreatedAt, user.UpdatedAt,
	)
	return err
}

// GetByID retrieves a user by ID
func (r *UserRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.User, error) {
	query := `SELECT * FROM everstack.users WHERE id = $1`
	var user domain.User
	err := r.db.GetContext(ctx, &user, query, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &user, err
}

// GetByExternalID retrieves a user by external ID
func (r *UserRepository) GetByExternalID(ctx context.Context, externalID string) (*domain.User, error) {
	query := `SELECT * FROM everstack.users WHERE external_id = $1`
	var user domain.User
	err := r.db.GetContext(ctx, &user, query, externalID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &user, err
}

// GetByEmail retrieves a user by email
func (r *UserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	query := `SELECT * FROM everstack.users WHERE email = $1`
	var user domain.User
	err := r.db.GetContext(ctx, &user, query, email)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &user, err
}

// Update updates a user
func (r *UserRepository) Update(ctx context.Context, user *domain.User) error {
	user.UpdatedAt = time.Now()

	query := `
		UPDATE everstack.users 
		SET email = $2, name = $3, avatar_url = $4, updated_at = $5
		WHERE id = $1
	`
	_, err := r.db.ExecContext(ctx, query,
		user.ID, user.Email, user.Name, user.AvatarURL, user.UpdatedAt,
	)
	return err
}

// GetWithOrganizations retrieves a user with their organization memberships
func (r *UserRepository) GetWithOrganizations(ctx context.Context, userID uuid.UUID) (*domain.UserWithOrganizations, error) {
	user, err := r.GetByID(ctx, userID)
	if err != nil || user == nil {
		return nil, err
	}

	query := `
		SELECT o.id, o.slug, o.name, m.role
		FROM everstack.organizations o
		INNER JOIN everstack.organization_members m ON o.id = m.organization_id
		WHERE m.user_id = $1
		ORDER BY o.name
	`
	var orgs []domain.OrganizationMembership
	if err := r.db.SelectContext(ctx, &orgs, query, userID); err != nil {
		return nil, err
	}

	return &domain.UserWithOrganizations{
		User:          *user,
		Organizations: orgs,
	}, nil
}

// Count returns the total number of users
func (r *UserRepository) Count(ctx context.Context) (int, error) {
	query := `SELECT COUNT(*) FROM everstack.users`
	var count int
	err := r.db.GetContext(ctx, &count, query)
	return count, err
}

// ListPaginated returns users with pagination support
func (r *UserRepository) ListPaginated(ctx context.Context, limit, offset int) ([]domain.User, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	query := `SELECT * FROM everstack.users ORDER BY created_at LIMIT $1 OFFSET $2`
	var users []domain.User
	err := r.db.SelectContext(ctx, &users, query, limit, offset)
	return users, err
}

// List returns all users (deprecated: use ListPaginated for large datasets)
func (r *UserRepository) List(ctx context.Context) ([]domain.User, error) {
	return r.ListPaginated(ctx, 1000, 0)
}
