package v1

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/auth/deviceauth"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

// EVS-309 review: when a device JWT arrives in x-evs-api-key, the tenant
// middleware has already installed the membership user and the instance id
// as TenantID. The org must come from the token, not from that instance id.
func TestResolveAPIKeyContext_JWTInAPIKeyHeaderUsesTokenOrg(t *testing.T) {
	mgr, err := deviceauth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	const org, instance = "11111111-1111-1111-1111-111111111111", "22222222-2222-2222-2222-222222222222"
	tok, err := mgr.Issue(deviceauth.Identity{UserID: "user-1", OrganizationID: org, InstanceID: instance, ClientID: "ewt"})
	if err != nil {
		t.Fatal(err)
	}
	s := &CLIServer{deviceTokens: mgr}
	ctx := contextkeys.WithTenantID(contextkeys.WithUserID(context.Background(), "user-1"), instance)
	hdr := http.Header{}
	hdr.Set("x-evs-api-key", tok)
	ak, err := s.resolveAPIKeyContext(ctx, hdr)
	if err != nil {
		t.Fatal(err)
	}
	if ak.OrgID != org {
		t.Fatalf("OrgID = %q, want the token's organization %q (not the instance id)", ak.OrgID, org)
	}
}

// EVS-309 review, third round: mixed credentials. A member session for org
// B's instance plus a device JWT for org A in x-evs-api-key must not let
// the CLI act as A (its sandboxes would be listed from the process-wide
// registry). The JWT must agree with the request's org, and its membership
// is rechecked whenever a JWT is the acting principal.
func TestResolveAPIKeyContext_JWTMustAgreeWithSessionOrg(t *testing.T) {
	mgr, err := deviceauth.NewTokenManager([]byte("0123456789abcdef0123456789abcdef"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	const orgA, orgB, instanceB = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "22222222-2222-2222-2222-222222222222"
	tokA, _ := mgr.Issue(deviceauth.Identity{UserID: "user-1", OrganizationID: orgA, ClientID: "ewt"})
	tokB, _ := mgr.Issue(deviceauth.Identity{UserID: "user-1", Email: "dev@example.test", OrganizationID: orgB, ClientID: "ewt"})

	sessionCtx := contextkeys.WithTenantAuthenticated(contextkeys.WithUserID(context.Background(), "user-1"))
	sessionCtx = contextkeys.WithTenantID(sessionCtx, instanceB)
	sessionCtx = contextkeys.WithRequestInstanceScope(sessionCtx, contextkeys.RequestInstanceScope{InstanceID: instanceB, OrganizationID: orgB})

	t.Run("foreign org JWT rejected without DB", func(t *testing.T) {
		s := &CLIServer{deviceTokens: mgr}
		hdr := http.Header{}
		hdr.Set("x-evs-api-key", tokA)
		if _, err := s.resolveAPIKeyContext(sessionCtx, hdr); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("org A JWT on org B's request must be rejected, got %v", err)
		}
	})
	t.Run("same org JWT rechecks current membership", func(t *testing.T) {
		rawDB, mock, err := sqlmock.New()
		if err != nil {
			t.Fatal(err)
		}
		defer rawDB.Close()
		s := &CLIServer{deviceTokens: mgr, authDB: sqlx.NewDb(rawDB, "sqlmock")}
		hdr := http.Header{}
		hdr.Set("x-evs-api-key", tokB)

		mock.ExpectQuery(`FROM everstack\.organization_members om[\s\S]*om\.user_id::text = \$1`).
			WithArgs("user-1", orgB).WillReturnRows(sqlmock.NewRows([]string{"role", "user_id"}))
		mock.ExpectQuery(`JOIN everstack\.identity_links il`).
			WithArgs(orgB, "dev@example.test").WillReturnRows(sqlmock.NewRows([]string{"role", "user_id"}))
		if _, err := s.resolveAPIKeyContext(sessionCtx, hdr); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("removed member must be rejected, got %v", err)
		}

		mock.ExpectQuery(`FROM everstack\.organization_members om[\s\S]*om\.user_id::text = \$1`).
			WithArgs("user-1", orgB).WillReturnRows(sqlmock.NewRows([]string{"role", "user_id"}).AddRow("member", "user-1"))
		mock.ExpectQuery(`SELECT revoked_at FROM everstack\.user_credential_revocations`).
			WithArgs("user-1").WillReturnRows(sqlmock.NewRows([]string{"revoked_at"}).AddRow(nil))
		ak, err := s.resolveAPIKeyContext(sessionCtx, hdr)
		if err != nil || ak.OrgID != orgB {
			t.Fatalf("current member must resolve to org B, got %+v err %v", ak, err)
		}

		// Once the account's credentials are revoked (an undone email
		// change), a token issued before that is refused although the
		// membership stands.
		mock.ExpectQuery(`FROM everstack\.organization_members om[\s\S]*om\.user_id::text = \$1`).
			WithArgs("user-1", orgB).WillReturnRows(sqlmock.NewRows([]string{"role", "user_id"}).AddRow("member", "user-1"))
		mock.ExpectQuery(`SELECT revoked_at FROM everstack\.user_credential_revocations`).
			WithArgs("user-1").WillReturnRows(sqlmock.NewRows([]string{"revoked_at"}).AddRow(time.Now().Add(time.Minute)))
		if _, err := s.resolveAPIKeyContext(sessionCtx, hdr); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("a token issued before the revocation must be rejected, got %v", err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatal(err)
		}
	})
}
