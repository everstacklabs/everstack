package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Membership writes whose safety depends on what else is in the table (the
// last owner, the first organization) check and write in one transaction
// under a lock, so two concurrent requests cannot both pass the check.

// ErrLastOwner refuses a removal or demotion that would leave an
// organization with no owner.
var ErrLastOwner = errors.New("cannot remove the last owner")

// ErrInviterCannotGrant refuses an invitation whose inviter, at the moment
// it is accepted, is not a member of the organization or may not grant the
// invited role there.
var ErrInviterCannotGrant = errors.New("inviter can no longer grant this role")

// ErrInstanceHasUsers refuses first-run registration on an instance that
// already has a user.
var ErrInstanceHasUsers = errors.New("instance already has users")

// ErrLastOrganization refuses deleting an instance's only organization.
var ErrLastOrganization = errors.New("cannot delete the last organization")

// bootstrapLockKey serializes first-run bootstrap across every gateway
// process sharing the database.
const bootstrapLockKey = "everstack.selfhosted.instance_bootstrap"

// BootstrapFirstOrganization creates the instance's first organization with
// userID as its owner, and reports whether it did. It does so only on a
// genuinely fresh instance: no organization, and userID is its only user.
// "No organization" alone is not enough; an instance whose organizations
// were deleted still has its other users, and none of them is entitled to
// ownership. It never adds anyone to an organization that already exists.
// Concurrent callers are serialized by an advisory lock.
func (r *OrganizationRepository) BootstrapFirstOrganization(ctx context.Context, userID uuid.UUID, slug, name string) (*domain.Organization, bool, error) {
	var org *domain.Organization
	err := r.inTx(ctx, func(tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, bootstrapLockKey); err != nil {
			return fmt.Errorf("bootstrap lock: %w", err)
		}
		fresh, err := freshInstanceFor(ctx, tx, userID)
		if err != nil || !fresh {
			return err
		}
		o, err := insertOrganizationWithOwner(ctx, tx, userID, slug, name)
		org = o
		return err
	})
	if err != nil {
		return nil, false, err
	}
	return org, org != nil, nil
}

// BootstrapOwnerByEmail makes the existing user with email the owner of a
// new first organization and returns it, or nil when it did nothing. It is
// the operator's way out of an instance that has users but no organization,
// where sign-in cannot bootstrap: it does nothing once any organization
// exists, or when no user has that email. It takes the bootstrap lock.
func (r *OrganizationRepository) BootstrapOwnerByEmail(ctx context.Context, email, slug, name string) (*domain.Organization, error) {
	var org *domain.Organization
	err := r.inTx(ctx, func(tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, bootstrapLockKey); err != nil {
			return fmt.Errorf("bootstrap lock: %w", err)
		}
		var hasOrg bool
		if err := tx.GetContext(ctx, &hasOrg, `SELECT EXISTS (SELECT 1 FROM everstack.organizations)`); err != nil {
			return err
		}
		if hasOrg {
			return nil
		}
		var userID uuid.UUID
		if err := tx.GetContext(ctx, &userID,
			`SELECT id FROM everstack.users WHERE lower(email) = lower($1) LIMIT 1`, strings.TrimSpace(email)); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		o, err := insertOrganizationWithOwner(ctx, tx, userID, slug, name)
		org = o
		return err
	})
	return org, err
}

// RegisterFirstOwner creates user, with passwordHash as their password, and
// the instance's first organization with them as owner, all in one
// transaction under the bootstrap lock. On an instance that already has any
// user it writes nothing and returns ErrInstanceHasUsers, so concurrent
// first registrations produce exactly one account and one owner.
func (r *OrganizationRepository) RegisterFirstOwner(ctx context.Context, user *domain.User, passwordHash, slug, name string) (*domain.Organization, error) {
	var org *domain.Organization
	err := r.inTx(ctx, func(tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, bootstrapLockKey); err != nil {
			return fmt.Errorf("bootstrap lock: %w", err)
		}
		var hasUser bool
		if err := tx.GetContext(ctx, &hasUser, `SELECT EXISTS (SELECT 1 FROM everstack.users)`); err != nil {
			return err
		}
		if hasUser {
			return ErrInstanceHasUsers
		}
		if user.ID == uuid.Nil {
			user.ID = uuid.New()
		}
		if user.ExternalID == "" {
			user.ExternalID = "local:" + user.ID.String()
		}
		now := time.Now()
		user.CreatedAt, user.UpdatedAt = now, now
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO everstack.users (id, external_id, email, name, avatar_url, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			user.ID, user.ExternalID, user.Email, user.Name, user.AvatarURL, user.CreatedAt, user.UpdatedAt); err != nil {
			return fmt.Errorf("create user: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO everstack.user_credentials (user_id, password_hash, created_at, updated_at) VALUES ($1, $2, $3, $4)`,
			user.ID, passwordHash, now, now); err != nil {
			return fmt.Errorf("store credentials: %w", err)
		}
		o, err := insertOrganizationWithOwner(ctx, tx, user.ID, slug, name)
		org = o
		return err
	})
	return org, err
}

// DeleteOrganizationKeepingOne deletes orgID unless it is the instance's
// only organization (ErrLastOrganization). It takes the bootstrap lock, so
// two concurrent deletes of the last two organizations cannot both succeed.
func (r *OrganizationRepository) DeleteOrganizationKeepingOne(ctx context.Context, orgID uuid.UUID) error {
	return r.inTx(ctx, func(tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, bootstrapLockKey); err != nil {
			return fmt.Errorf("bootstrap lock: %w", err)
		}
		var others bool
		if err := tx.GetContext(ctx, &others,
			`SELECT EXISTS (SELECT 1 FROM everstack.organizations WHERE id <> $1)`, orgID); err != nil {
			return err
		}
		if !others {
			return ErrLastOrganization
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM everstack.organizations WHERE id = $1`, orgID)
		return err
	})
}

// RevokeSessionsWithoutMembership deletes every session whose user belongs
// to no organization and returns how many it deleted. Such a session grants
// nothing in the UI, but it is still a credential, so self-hosted startup
// sweeps the ones minted before sign-in started refusing users without a
// membership.
func (r *OrganizationRepository) RevokeSessionsWithoutMembership(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx, `
		DELETE FROM everstack.sessions s
		WHERE NOT EXISTS (SELECT 1 FROM everstack.organization_members m WHERE m.user_id = s.user_id)`)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// freshInstanceFor reports whether the instance has no organization and
// userID is its only user.
func freshInstanceFor(ctx context.Context, tx *sqlx.Tx, userID uuid.UUID) (bool, error) {
	var hasOrg bool
	if err := tx.GetContext(ctx, &hasOrg, `SELECT EXISTS (SELECT 1 FROM everstack.organizations)`); err != nil {
		return false, err
	}
	if hasOrg {
		return false, nil
	}
	var otherUser bool
	if err := tx.GetContext(ctx, &otherUser,
		`SELECT EXISTS (SELECT 1 FROM everstack.users WHERE id <> $1)`, userID); err != nil {
		return false, err
	}
	return !otherUser, nil
}

// insertOrganizationWithOwner creates an organization with ownerID as owner.
func insertOrganizationWithOwner(ctx context.Context, tx *sqlx.Tx, ownerID uuid.UUID, slug, name string) (*domain.Organization, error) {
	now := time.Now()
	o := &domain.Organization{ID: uuid.New(), Slug: slug, Name: name, CreatedAt: now, UpdatedAt: now}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO everstack.organizations (id, slug, name, created_at, updated_at) VALUES ($1, $2, $3, $4, $5)`,
		o.ID, o.Slug, o.Name, o.CreatedAt, o.UpdatedAt); err != nil {
		return nil, fmt.Errorf("create organization: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO everstack.organization_members (id, organization_id, user_id, role, joined_at) VALUES ($1, $2, $3, $4, $5)`,
		uuid.New(), o.ID, ownerID, domain.RoleOwner, now); err != nil {
		return nil, fmt.Errorf("add owner: %w", err)
	}
	return o, nil
}

// RemoveMemberKeepingOwner removes userID from orgID unless that would leave
// the organization with no owner (ErrLastOwner). The organization's owner
// rows are locked first, so concurrent removals and demotions see each
// other's writes. When revokeSessionsIfLast is set and this was the user's
// last membership, their sessions are deleted in the same transaction.
func (r *OrganizationRepository) RemoveMemberKeepingOwner(ctx context.Context, orgID, userID uuid.UUID, revokeSessionsIfLast bool) error {
	return r.inTx(ctx, func(tx *sqlx.Tx) error {
		role, err := lockMemberAndOwners(ctx, tx, orgID, userID)
		if err != nil || role == "" {
			return err
		}
		if role == domain.RoleOwner {
			if err := requireAnotherOwner(ctx, tx, orgID, userID); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM everstack.organization_members WHERE organization_id = $1 AND user_id = $2`, orgID, userID); err != nil {
			return err
		}
		if !revokeSessionsIfLast {
			return nil
		}
		var remaining bool
		if err := tx.GetContext(ctx, &remaining,
			`SELECT EXISTS (SELECT 1 FROM everstack.organization_members WHERE user_id = $1)`, userID); err != nil {
			return err
		}
		if remaining {
			return nil
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM everstack.sessions WHERE user_id = $1`, userID)
		return err
	})
}

// UpdateMemberRoleKeepingOwner changes userID's role in orgID unless that
// would demote the organization's last owner (ErrLastOwner). It locks like
// RemoveMemberKeepingOwner.
func (r *OrganizationRepository) UpdateMemberRoleKeepingOwner(ctx context.Context, orgID, userID uuid.UUID, newRole string) error {
	return r.inTx(ctx, func(tx *sqlx.Tx) error {
		role, err := lockMemberAndOwners(ctx, tx, orgID, userID)
		if err != nil || role == "" {
			return err
		}
		if role == domain.RoleOwner && newRole != domain.RoleOwner {
			if err := requireAnotherOwner(ctx, tx, orgID, userID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE everstack.organization_members SET role = $1 WHERE organization_id = $2 AND user_id = $3`,
			newRole, orgID, userID)
		return err
	})
}

// AddInvitedMember adds userID to orgID with role on inviterID's invitation,
// provided inviterID can still grant that role there (ErrInviterCannotGrant
// otherwise). The inviter's membership row is read under a share lock in the
// same transaction as the insert, so a concurrent demotion or removal of the
// inviter either commits first and is seen, or waits for the acceptance.
func (r *OrganizationRepository) AddInvitedMember(ctx context.Context, orgID, userID uuid.UUID, role string, inviterID uuid.UUID) error {
	return r.inTx(ctx, func(tx *sqlx.Tx) error {
		var inviterRole string
		if err := tx.GetContext(ctx, &inviterRole,
			`SELECT role FROM everstack.organization_members
			 WHERE organization_id = $1 AND user_id = $2
			 FOR SHARE`, orgID, inviterID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrInviterCannotGrant
			}
			return fmt.Errorf("lock inviter: %w", err)
		}
		if !domain.CanGrantRole(inviterRole, role) {
			return ErrInviterCannotGrant
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO everstack.organization_members (id, organization_id, user_id, role, invited_by, joined_at)
			 VALUES ($1, $2, $3, $4, $5, $6)`,
			uuid.New(), orgID, userID, role, inviterID, time.Now())
		return err
	})
}

// AddMemberIfAbsent adds userID to orgID with role unless they are already a
// member, in which case their role is left as it is.
func (r *OrganizationRepository) AddMemberIfAbsent(ctx context.Context, orgID, userID uuid.UUID, role string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO everstack.organization_members (id, organization_id, user_id, role, joined_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (organization_id, user_id) DO NOTHING`, uuid.New(), orgID, userID, role, time.Now())
	return err
}

// lockMemberAndOwners locks orgID's owner rows (in a fixed order, so
// concurrent callers cannot deadlock) and then userID's own row, and returns
// userID's current role, or "" when they are not a member.
func lockMemberAndOwners(ctx context.Context, tx *sqlx.Tx, orgID, userID uuid.UUID) (string, error) {
	var owners []uuid.UUID
	if err := tx.SelectContext(ctx, &owners,
		`SELECT user_id FROM everstack.organization_members
		 WHERE organization_id = $1 AND role = 'owner'
		 ORDER BY user_id
		 FOR UPDATE`, orgID); err != nil {
		return "", fmt.Errorf("lock owners: %w", err)
	}
	var role string
	if err := tx.GetContext(ctx, &role,
		`SELECT role FROM everstack.organization_members
		 WHERE organization_id = $1 AND user_id = $2
		 FOR UPDATE`, orgID, userID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", fmt.Errorf("lock member: %w", err)
	}
	return role, nil
}

// requireAnotherOwner returns ErrLastOwner unless orgID has an owner other
// than userID. Callers hold the owner-row locks.
func requireAnotherOwner(ctx context.Context, tx *sqlx.Tx, orgID, userID uuid.UUID) error {
	var others int
	if err := tx.GetContext(ctx, &others,
		`SELECT COUNT(*) FROM everstack.organization_members
		 WHERE organization_id = $1 AND role = 'owner' AND user_id <> $2`, orgID, userID); err != nil {
		return err
	}
	if others == 0 {
		return ErrLastOwner
	}
	return nil
}

func (r *OrganizationRepository) inTx(ctx context.Context, fn func(tx *sqlx.Tx) error) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
