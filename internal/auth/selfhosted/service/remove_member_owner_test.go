package service

import (
	"context"
	"database/sql/driver"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/repository"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// newRemoveMemberService returns an OrganizationService whose org has two
// owners, so the last-owner guard never decides these cases. Statements are
// matched unordered; removal is captured so a test can see whether it ran.
func newRemoveMemberService(t *testing.T, orgID, actorID uuid.UUID, actorRole string, targetID uuid.UUID, targetRole string) (*OrganizationService, *bool) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	mock.MatchExpectationsInOrder(false)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().UTC()
	member := func(userID uuid.UUID, role string) *sqlmock.Rows {
		return sqlmock.NewRows([]string{"id", "organization_id", "user_id", "role", "joined_at", "email"}).
			AddRow(uuid.New(), orgID, userID, role, now, "x@example.com")
	}
	getMember := regexp.QuoteMeta("WHERE om.organization_id = $1 AND om.user_id = $2")
	mock.ExpectQuery(getMember).WithArgs(orgID, actorID).WillReturnRows(member(actorID, actorRole))
	mock.ExpectQuery(getMember).WithArgs(orgID, targetID).WillReturnRows(member(targetID, targetRole))
	// The removal itself is one transaction that locks the owners and the
	// target; the other owner keeps the last-owner guard satisfied.
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("ORDER BY user_id")).
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(actorID).AddRow(targetID))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT role FROM everstack.organization_members")).
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow(targetRole))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM everstack.organization_members")).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS")).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectCommit()
	mock.ExpectRollback()

	removed := false
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM everstack.organization_members WHERE organization_id = $1 AND user_id = $2")).
		WillReturnResult(sqlmock.NewResult(0, 1)).
		WithArgs(orgID, removalArg{&removed})

	repo := repository.NewOrganizationRepository(sqlx.NewDb(db, "sqlmock"))
	return NewOrganizationService(repo, nil), &removed
}

// removalArg matches any user id and records that a removal was issued.
type removalArg struct{ hit *bool }

func (a removalArg) Match(driver.Value) bool { *a.hit = true; return true }

// An admin manages non-owner members but must not be able to remove an
// owner; only an owner can.
func TestRemoveMemberOnlyOwnersRemoveOwners(t *testing.T) {
	orgID, actor, target := uuid.New(), uuid.New(), uuid.New()

	svc, removed := newRemoveMemberService(t, orgID, actor, "admin", target, "owner")
	err := svc.RemoveMember(context.Background(), actor, orgID, target)
	if err == nil || !strings.Contains(err.Error(), "only owners") {
		t.Fatalf("admin removing an owner: err = %v, want an only-owners refusal", err)
	}
	if *removed {
		t.Fatal("admin removed an owner")
	}

	svc, removed = newRemoveMemberService(t, orgID, actor, "owner", target, "owner")
	if err := svc.RemoveMember(context.Background(), actor, orgID, target); err != nil {
		t.Fatalf("owner removing a co-owner: %v", err)
	}
	if !*removed {
		t.Fatal("owner could not remove a co-owner")
	}

	svc, removed = newRemoveMemberService(t, orgID, actor, "admin", target, "member")
	if err := svc.RemoveMember(context.Background(), actor, orgID, target); err != nil {
		t.Fatalf("admin removing a member: %v", err)
	}
	if !*removed {
		t.Fatal("admin could not remove a member")
	}
}
