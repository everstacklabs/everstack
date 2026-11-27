package selfhosted

import (
	"context"
	"regexp"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	authv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/auth/v1"
)

// ListTeamMembers accepted any valid session and read
// everstack.organization_members and everstack.invitations with no
// organization filter. The managed gateway runs this builtin handler against
// a database shared by every tenant, so a member of org A listed every
// org's members and invitees. The listing must be scoped to the caller's
// own organization in SQL.
func TestListTeamMembersIsScopedToCallersOrganization(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
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

	callerID := uuid.New()
	orgA := uuid.New()
	now := time.Now().UTC()

	// Session -> user -> the caller's memberships (org A only).
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM everstack.sessions WHERE token = $1")).
		WithArgs("tok").
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "token", "expires_at", "created_at"}).
			AddRow(uuid.New(), callerID, "tok", now.Add(time.Hour), now))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM everstack.users WHERE id = $1")).
		WithArgs(callerID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "created_at", "updated_at"}).
			AddRow(callerID, "a@example.com", now, now))
	mock.ExpectQuery(regexp.QuoteMeta("WHERE m.user_id = $1")).
		WithArgs(callerID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "slug", "name", "role"}).
			AddRow(orgA, "org-a", "Org A", "owner"))

	// The listing itself: org A's members and invitations only.
	mock.ExpectQuery(regexp.QuoteMeta("FROM everstack.organization_members\n\t\tWHERE organization_id = $1")).
		WithArgs(orgA).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "role", "created_at"}).
			AddRow(callerID, "owner", now))
	mock.ExpectQuery(regexp.QuoteMeta("i.organization_id = $1")).
		WithArgs(orgA).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email"}))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM everstack.users WHERE id = $1")).
		WithArgs(callerID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "created_at", "updated_at"}).
			AddRow(callerID, "a@example.com", now, now))

	req := connect.NewRequest(&authv1.ListTeamMembersRequest{})
	req.Header().Set("Cookie", "everstack_session=tok")
	resp, err := server.handler.ListTeamMembers(context.Background(), req)
	if err != nil {
		t.Fatalf("ListTeamMembers: %v", err)
	}
	if got := len(resp.Msg.GetMembers()); got != 1 {
		t.Fatalf("members = %d, want 1 (the caller's org only)", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("team listing must be filtered by the caller's organization: %v", err)
	}
}

func newTeamScopeServer(t *testing.T) (*Server, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
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

// expectAdminSession: cookie "tok" belongs to callerID, admin of orgID only.
func expectAdminSession(mock sqlmock.Sqlmock, callerID, orgID uuid.UUID) {
	now := time.Now().UTC()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM everstack.sessions WHERE token = $1")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "token", "expires_at", "created_at"}).
			AddRow(uuid.New(), callerID, "tok", now.Add(time.Hour), now))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM everstack.users WHERE id = $1")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "created_at", "updated_at"}).
			AddRow(callerID, "admin@a.example", now, now))
	mock.ExpectQuery(regexp.QuoteMeta("WHERE m.user_id = $1")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "slug", "name", "role"}).AddRow(orgID, "org-a", "Org A", "admin"))
}

// RemoveTeamMember deleted the target from EVERY organization and signed
// them out, for any valid session. An admin of org A must not be able to
// evict a member of org B: no DELETE may run.
func TestRemoveTeamMemberCannotReachAnotherOrganization(t *testing.T) {
	server, mock := newTeamScopeServer(t)
	callerID, orgA, victim := uuid.New(), uuid.New(), uuid.New()
	expectAdminSession(mock, callerID, orgA)
	// The victim is not a member of org A.
	mock.ExpectQuery(regexp.QuoteMeta("WHERE om.organization_id = $1 AND om.user_id = $2")).
		WithArgs(orgA, victim).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	req := connect.NewRequest(&authv1.RemoveTeamMemberRequest{UserId: victim.String()})
	req.Header().Set("Cookie", "everstack_session=tok")
	_, err := server.handler.RemoveTeamMember(context.Background(), req)
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("cross-org removal: err = %v, want NotFound", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("no membership or session may be deleted: %v", err)
	}
}

// RevokeInvitation deleted any invitation id for any valid session.
func TestRevokeInvitationCannotReachAnotherOrganization(t *testing.T) {
	server, mock := newTeamScopeServer(t)
	callerID, orgA, orgB := uuid.New(), uuid.New(), uuid.New()
	invID := uuid.New()
	expectAdminSession(mock, callerID, orgA)
	mock.ExpectQuery(regexp.QuoteMeta("FROM everstack.invitations")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "email", "role", "token", "organization_id", "expires_at"}).
			AddRow(invID, "b@b.example", "member", "t", orgB, time.Now().Add(time.Hour)))

	req := connect.NewRequest(&authv1.RevokeInvitationRequest{InvitationId: invID.String()})
	req.Header().Set("Cookie", "everstack_session=tok")
	_, err := server.handler.RevokeInvitation(context.Background(), req)
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("cross-org revoke: err = %v, want NotFound", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("no invitation may be deleted: %v", err)
	}
}

// The admin's own org still works: remove a member of org A.
func TestRemoveTeamMemberWithinOwnOrganization(t *testing.T) {
	server, mock := newTeamScopeServer(t)
	callerID, orgA, member := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	expectAdminSession(mock, callerID, orgA)
	mock.ExpectQuery(regexp.QuoteMeta("WHERE om.organization_id = $1 AND om.user_id = $2")).
		WithArgs(orgA, member).
		WillReturnRows(sqlmock.NewRows([]string{"id", "organization_id", "user_id", "role", "joined_at", "email"}).
			AddRow(uuid.New(), orgA, member, "member", now, "m@a.example"))
	// One transaction: lock the owners and the member, delete, and end the
	// member's sessions because no membership remains.
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("AND role = 'owner'")).WithArgs(orgA).
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(callerID))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT role FROM everstack.organization_members")).WithArgs(orgA, member).
		WillReturnRows(sqlmock.NewRows([]string{"role"}).AddRow("member"))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM everstack.organization_members WHERE organization_id = $1 AND user_id = $2")).
		WithArgs(orgA, member).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("SELECT EXISTS (SELECT 1 FROM everstack.organization_members WHERE user_id = $1)")).
		WithArgs(member).WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM everstack.sessions WHERE user_id = $1")).
		WithArgs(member).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	req := connect.NewRequest(&authv1.RemoveTeamMemberRequest{UserId: member.String()})
	req.Header().Set("Cookie", "everstack_session=tok")
	if _, err := server.handler.RemoveTeamMember(context.Background(), req); err != nil {
		t.Fatalf("own-org removal: %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
