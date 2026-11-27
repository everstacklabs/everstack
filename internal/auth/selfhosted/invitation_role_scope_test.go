package selfhosted

import (
	"context"
	"database/sql/driver"
	"fmt"
	"regexp"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	authv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/auth/v1"
)

// These tests cover the builtin team invitation flow: who may invite whom,
// as what, and into which organization an accepted invitation lands.
//
// The mocks are unordered and deliberately cover every statement either the
// old or the new code path issues, so a regression shows up as the wrong
// outcome (an invitation created, a membership written to the wrong
// organization) rather than as an unexpected-query error.

// captureArg is a sqlmock argument matcher that accepts anything and records
// what it saw, so a test can assert on the value a statement was run with.
type captureArg struct {
	mu     sync.Mutex
	values []driver.Value
}

func (c *captureArg) Match(v driver.Value) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.values = append(c.values, v)
	return true
}

// onlyValue reports whether every recorded value is want (sqlmock may consult
// a matcher more than once per statement) and at least one was recorded.
func (c *captureArg) onlyValue(want driver.Value) bool {
	got := c.seen()
	if len(got) == 0 {
		return false
	}
	for _, v := range got {
		if v != want {
			return false
		}
	}
	return true
}

func (c *captureArg) seen() []driver.Value {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]driver.Value(nil), c.values...)
}

type membership struct {
	org  uuid.UUID
	role string
}

func newInvitationServer(t *testing.T) (*Server, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	mock.MatchExpectationsInOrder(false)
	t.Cleanup(func() { _ = db.Close() })
	server, err := CreateServer(sqlx.NewDb(db, "sqlmock"), &Config{
		SessionSecret:     "session-secret-at-least-32-bytes-long!!",
		SessionCookieName: "everstack_session",
		SessionMaxAge:     3600,
		ExternalURL:       "http://localhost:8089",
	}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return server, mock
}

// expectSessionWithMemberships: cookie "tok" belongs to callerID, who holds
// exactly the given memberships.
func expectSessionWithMemberships(mock sqlmock.Sqlmock, callerID uuid.UUID, memberships ...membership) {
	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM everstack.sessions WHERE token = $1")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "token", "expires_at", "created_at"}).
			AddRow(uuid.New(), callerID, "tok", now.Add(time.Hour), now))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM everstack.users WHERE id = $1")).
		WithArgs(callerID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "created_at", "updated_at"}).
			AddRow(callerID, "caller@example.com", now, now))
	rows := sqlmock.NewRows([]string{"id", "slug", "name", "role"})
	for i, m := range memberships {
		rows.AddRow(m.org, fmt.Sprintf("org-%d", i), fmt.Sprintf("Org %d", i), m.role)
	}
	mock.ExpectQuery(regexp.QuoteMeta("WHERE m.user_id = $1")).WithArgs(callerID).WillReturnRows(rows)
}

// expectInvitationWrite allows every statement the invite path may issue
// after authorization, and captures the organization_id of any invitation
// row written.
func expectInvitationWrite(mock sqlmock.Sqlmock) *captureArg {
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM everstack.users WHERE email = $1")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(regexp.QuoteMeta("WHERE email = $1 AND accepted_at IS NULL")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM everstack.users")).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT COUNT(*) FROM everstack.invitations")).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))
	org := &captureArg{}
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO everstack.invitations")).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), org, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	return org
}

func invite(t *testing.T, server *Server, role authv1.OrganizationRole, activeOrg string) error {
	t.Helper()
	req := connect.NewRequest(&authv1.InviteTeamMemberRequest{Email: "invitee@example.com", Role: role})
	req.Header().Set("Cookie", "everstack_session=tok")
	if activeOrg != "" {
		req.Header().Set("x-org-id", activeOrg)
	}
	_, err := server.handler.InviteTeamMember(context.Background(), req)
	return err
}

// An inviter may grant at most their own role, only owners may grant owner,
// and members and viewers may not invite at all.
func TestInviteTeamMemberRefusesRolesAboveTheInviter(t *testing.T) {
	cases := []struct {
		name    string
		inviter string
		grant   authv1.OrganizationRole
	}{
		{"viewer grants owner", "viewer", authv1.OrganizationRole_ORGANIZATION_ROLE_OWNER},
		{"viewer grants viewer", "viewer", authv1.OrganizationRole_ORGANIZATION_ROLE_VIEWER},
		{"member grants viewer", "member", authv1.OrganizationRole_ORGANIZATION_ROLE_VIEWER},
		{"admin grants owner", "admin", authv1.OrganizationRole_ORGANIZATION_ROLE_OWNER},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, mock := newInvitationServer(t)
			callerID, orgA := uuid.New(), uuid.New()
			expectSessionWithMemberships(mock, callerID, membership{orgA, tc.inviter})
			written := expectInvitationWrite(mock)

			err := invite(t, server, tc.grant, "")
			if connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatalf("%s: err = %v, want PermissionDenied", tc.name, err)
			}
			if got := written.seen(); len(got) != 0 {
				t.Fatalf("%s: an invitation row was written (organization_id %v)", tc.name, got)
			}
		})
	}
}

// Allowed grants still work, and the invitation is bound to the inviter's
// organization rather than left organization-less.
func TestInviteTeamMemberBindsInvitationToInvitersOrganization(t *testing.T) {
	cases := []struct {
		inviter string
		grant   authv1.OrganizationRole
	}{
		{"owner", authv1.OrganizationRole_ORGANIZATION_ROLE_OWNER},
		{"admin", authv1.OrganizationRole_ORGANIZATION_ROLE_ADMIN},
		{"admin", authv1.OrganizationRole_ORGANIZATION_ROLE_VIEWER},
	}
	for _, tc := range cases {
		t.Run(tc.inviter+" grants "+tc.grant.String(), func(t *testing.T) {
			server, mock := newInvitationServer(t)
			callerID, orgA := uuid.New(), uuid.New()
			expectSessionWithMemberships(mock, callerID, membership{orgA, tc.inviter})
			written := expectInvitationWrite(mock)

			if err := invite(t, server, tc.grant, ""); err != nil {
				t.Fatalf("invite: %v", err)
			}
			if !written.onlyValue(orgA.String()) {
				t.Fatalf("invitation organization_id = %v, want %s", written.seen(), orgA)
			}
		})
	}
}

// A member of org A cannot invite into org B by naming it as the active
// organization, and a caller in two organizations is judged by their role in
// the one they named.
func TestInviteTeamMemberCannotTargetAnotherOrganization(t *testing.T) {
	t.Run("not a member of the named org", func(t *testing.T) {
		server, mock := newInvitationServer(t)
		callerID, orgA, orgB := uuid.New(), uuid.New(), uuid.New()
		expectSessionWithMemberships(mock, callerID, membership{orgA, "owner"})
		written := expectInvitationWrite(mock)

		err := invite(t, server, authv1.OrganizationRole_ORGANIZATION_ROLE_MEMBER, orgB.String())
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("err = %v, want PermissionDenied", err)
		}
		if got := written.seen(); len(got) != 0 {
			t.Fatalf("an invitation row was written (organization_id %v)", got)
		}
	})

	t.Run("viewer in the named org", func(t *testing.T) {
		server, mock := newInvitationServer(t)
		callerID, orgA, orgB := uuid.New(), uuid.New(), uuid.New()
		expectSessionWithMemberships(mock, callerID, membership{orgA, "owner"}, membership{orgB, "viewer"})
		written := expectInvitationWrite(mock)

		err := invite(t, server, authv1.OrganizationRole_ORGANIZATION_ROLE_ADMIN, orgB.String())
		if connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatalf("err = %v, want PermissionDenied", err)
		}
		if got := written.seen(); len(got) != 0 {
			t.Fatalf("an invitation row was written (organization_id %v)", got)
		}
	})

	t.Run("owner in the named org", func(t *testing.T) {
		server, mock := newInvitationServer(t)
		callerID, orgA, orgB := uuid.New(), uuid.New(), uuid.New()
		expectSessionWithMemberships(mock, callerID, membership{orgA, "owner"}, membership{orgB, "viewer"})
		written := expectInvitationWrite(mock)

		if err := invite(t, server, authv1.OrganizationRole_ORGANIZATION_ROLE_ADMIN, orgA.String()); err != nil {
			t.Fatalf("invite: %v", err)
		}
		if !written.onlyValue(orgA.String()) {
			t.Fatalf("invitation organization_id = %v, want %s", written.seen(), orgA)
		}
	})
}

type acceptFixture struct {
	userInsert   *captureArg
	memberOrg    *captureArg
	memberRole   *captureArg
	invitationID uuid.UUID
}

// expectAcceptance registers every statement the accept path may issue. The
// organization list (what the old code joined) returns orgB first; the
// inviter's current membership in inviterOrg is inviterRole ("" for none).
func expectAcceptance(mock sqlmock.Sqlmock, invOrg *uuid.UUID, invRole string, inviterID, inviterOrg uuid.UUID, inviterRole string, orgB uuid.UUID) acceptFixture {
	now := time.Now().UTC()
	f := acceptFixture{userInsert: &captureArg{}, memberOrg: &captureArg{}, memberRole: &captureArg{}, invitationID: uuid.New()}

	var orgValue driver.Value
	if invOrg != nil {
		orgValue = *invOrg
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM everstack.invitations WHERE token = $1")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "role", "token", "invited_by", "organization_id", "expires_at", "accepted_at", "created_at"}).
			AddRow(f.invitationID, "invitee@example.com", invRole, "invite-token", inviterID, orgValue, now.Add(time.Hour), nil, now))

	// Inviter's current membership, by organization and by user.
	memberRows := sqlmock.NewRows([]string{"id", "organization_id", "user_id", "role", "joined_at", "email"})
	listRows := sqlmock.NewRows([]string{"id", "slug", "name", "created_at", "updated_at", "role"})
	if inviterRole != "" {
		memberRows.AddRow(uuid.New(), inviterOrg, inviterID, inviterRole, now, "inviter@example.com")
		listRows.AddRow(inviterOrg, "org-a", "Org A", now, now, inviterRole)
	}
	mock.ExpectQuery(regexp.QuoteMeta("WHERE om.organization_id = $1 AND om.user_id = $2")).
		WithArgs(inviterOrg, inviterID).WillReturnRows(memberRows)
	mock.ExpectQuery(regexp.QuoteMeta("WHERE om.user_id = $1")).
		WithArgs(inviterID).WillReturnRows(listRows)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM everstack.users WHERE email = $1")).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO everstack.users")).
		WithArgs(f.userInsert, sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO everstack.user_credentials")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	// The old code joined "the first organization in the database".
	mock.ExpectQuery(regexp.QuoteMeta("FROM everstack.organizations\n\t\tORDER BY created_at")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "slug", "name", "plan_tier", "billing_email", "stripe_customer_id", "created_at", "updated_at"}).
			AddRow(orgB, "org-b", "Org B", "", nil, nil, now, now).
			AddRow(inviterOrg, "org-a", "Org A", "", nil, nil, now, now))
	// The membership is written in one transaction that re-reads the
	// inviter's role under a share lock; a refusal deletes the new account.
	lockedRole := sqlmock.NewRows([]string{"role"})
	if inviterRole != "" {
		lockedRole.AddRow(inviterRole)
	}
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("FOR SHARE")).WithArgs(inviterOrg, inviterID).WillReturnRows(lockedRole)
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO everstack.organization_members")).
		WithArgs(sqlmock.AnyArg(), f.memberOrg, sqlmock.AnyArg(), f.memberRole, sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	mock.ExpectRollback()
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM everstack.users")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE everstack.invitations SET accepted_at")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO everstack.sessions")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM everstack.users WHERE id = $1")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "created_at", "updated_at"}).
			AddRow(uuid.New(), "invitee@example.com", now, now))
	mock.ExpectQuery(regexp.QuoteMeta("WHERE m.user_id = $1")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "slug", "name", "role"}))
	return f
}

func accept(server *Server) error {
	req := connect.NewRequest(&authv1.AcceptInvitationRequest{Token: "invite-token", Password: "Correct-Horse-9-battery"})
	_, err := server.handler.AcceptInvitation(context.Background(), req)
	return err
}

// Acceptance joins exactly the invitation's organization, never whichever
// organization the database happens to list first.
func TestAcceptInvitationJoinsTheInvitationsOrganization(t *testing.T) {
	server, mock := newInvitationServer(t)
	inviterID, orgA, orgB := uuid.New(), uuid.New(), uuid.New()
	f := expectAcceptance(mock, &orgA, "member", inviterID, orgA, "owner", orgB)

	if err := accept(server); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if !f.memberOrg.onlyValue(orgA.String()) {
		t.Fatalf("membership written for organization %v, want only %s", f.memberOrg.seen(), orgA)
	}
}

// Invitations created before they carried an organization belong to their
// inviter's organization when the inviter has exactly one.
func TestAcceptLegacyInvitationJoinsTheInvitersOrganization(t *testing.T) {
	server, mock := newInvitationServer(t)
	inviterID, orgA, orgB := uuid.New(), uuid.New(), uuid.New()
	f := expectAcceptance(mock, nil, "member", inviterID, orgA, "admin", orgB)

	if err := accept(server); err != nil {
		t.Fatalf("accept: %v", err)
	}
	if !f.memberOrg.onlyValue(orgA.String()) {
		t.Fatalf("membership written for organization %v, want only %s", f.memberOrg.seen(), orgA)
	}
}

// The inviter's authority is re-checked when the invitation is used: an
// owner invitation from someone who is no longer an owner, or no longer in
// the organization at all, grants nothing, and no account is created.
func TestAcceptInvitationRechecksTheInvitersCurrentRole(t *testing.T) {
	cases := []struct {
		name        string
		invRole     string
		inviterRole string
	}{
		{"owner invite from a demoted admin", "owner", "admin"},
		{"admin invite from a demoted member", "admin", "member"},
		{"member invite from a removed inviter", "member", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, mock := newInvitationServer(t)
			inviterID, orgA, orgB := uuid.New(), uuid.New(), uuid.New()
			f := expectAcceptance(mock, &orgA, tc.invRole, inviterID, orgA, tc.inviterRole, orgB)

			err := accept(server)
			if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("err = %v, want FailedPrecondition", err)
			}
			if got := f.memberRole.seen(); len(got) != 0 {
				t.Fatalf("membership written with role %v", got)
			}
			if got := f.userInsert.seen(); len(got) != 0 {
				t.Fatalf("an account was created for a refused invitation")
			}
		})
	}
}
