package v1

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"github.com/jmoiron/sqlx"
)

// asSessionUser mirrors the interceptor's session-cookie path: a validated
// user whose active tenant is tenantID.
func asSessionUser(tenantID string) context.Context {
	ctx := contextkeys.WithUserID(context.Background(), "user-1")
	ctx = contextkeys.WithTenantID(ctx, tenantID)
	return contextkeys.WithTenantAuthenticated(ctx)
}

func assertOwnResponseReachable(t *testing.T, s *Server, ctx context.Context, id string) {
	t.Helper()
	if _, err := s.GetResponse(ctx, connect.NewRequest(&gatewaypb.GetResponseRequest{ResponseId: id})); err != nil {
		t.Fatalf("get: %v", err)
	}
	list, err := s.ListResponses(ctx, connect.NewRequest(&gatewaypb.ListResponsesRequest{}))
	if err != nil || len(list.Msg.GetData()) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	if err := s.authorizePreviousResponse(ctx, id); err != nil {
		t.Fatalf("chain: %v", err)
	}
	if _, err := s.CancelResponse(ctx, connect.NewRequest(&gatewaypb.CancelResponseRequest{ResponseId: id})); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	del, err := s.DeleteResponse(ctx, connect.NewRequest(&gatewaypb.DeleteResponseRequest{ResponseId: id}))
	if err != nil || !del.Msg.GetDeleted() {
		t.Fatalf("delete: %v %v", del, err)
	}
}

// Self-hosted with EVS_INSTANCE_ID set: API keys carry the instance id while
// the operator session carries the organization id. A self-hosted gateway is
// one tenant, so both must see the same responses (pre-fix behaviour).
func TestResponsesSingleTenantIgnoresCallerIdentity(t *testing.T) {
	prev := globalResponseStore
	globalResponseStore = newResponseStore()
	t.Cleanup(func() { globalResponseStore = prev })
	s := &Server{ctx: context.Background()}

	seedResponse(s, asTenant("instance-I"), "resp_1", "x")
	assertOwnResponseReachable(t, s, asSessionUser("org-O"), "resp_1")
}

// Shared gateway: an instance-scoped API key (tenant = instance id) and a
// caller carrying the owning organization id are the same tenant and must
// resolve to one owner; a different organization must not.
func TestResponsesSharedModeCanonicalizesInstanceToOrganization(t *testing.T) {
	prev := globalResponseStore
	globalResponseStore = newResponseStore()
	t.Cleanup(func() { globalResponseStore = prev })
	resetResponseOwnerCache(t)

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.MatchExpectationsInOrder(false)

	const (
		instanceID = "22222222-2222-2222-2222-222222222222"
		orgID      = "11111111-1111-1111-1111-111111111111"
		otherOrg   = "33333333-3333-3333-3333-333333333333"
	)
	mock.ExpectQuery(`FROM everstack[.]organizations`).WithArgs(instanceID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))
	mock.ExpectQuery(`FROM everstack[.]managed_instances`).WithArgs(instanceID).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id"}).AddRow(orgID))
	mock.ExpectQuery(`FROM everstack[.]organizations`).WithArgs(orgID).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(orgID))
	mock.ExpectQuery(`FROM everstack[.]organizations`).WithArgs(otherOrg).
		WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(otherOrg))

	serverCtx := context.WithValue(context.Background(), contextkeys.SharedGatewayMode, true)
	serverCtx = context.WithValue(serverCtx, contextkeys.BillingDB, sqlx.NewDb(db, "sqlmock"))
	s := &Server{ctx: serverCtx}

	seedResponse(s, asTenant(instanceID), "resp_1", "x")

	if _, ok := s.lookupResponse(asTenant(otherOrg), "resp_1"); ok {
		t.Fatal("another organization can read the response")
	}
	// Second lookup for the instance is served from the cache (no query).
	if _, ok := s.lookupResponse(asTenant(instanceID), "resp_1"); !ok {
		t.Fatal("owning instance key cannot read its response")
	}
	assertOwnResponseReachable(t, s, asSessionUser(orgID), "resp_1")
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func resetResponseOwnerCache(t *testing.T) {
	t.Helper()
	clear := func() {
		responseOwnerCache.Range(func(k, _ any) bool { responseOwnerCache.Delete(k); return true })
	}
	clear()
	t.Cleanup(clear)
}
