package v1

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"

	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
	"github.com/everstacklabs/everstack/pkg/tenant"
)

// EVS-309: on a managed gateway the status RPCs are reachable pre-auth.
// A hostname-selected instance scope (or an injected tenant config) is
// routing evidence, not identity; without an authenticated principal the
// response must carry no tenant/instance IDs and no billing oracle.

func newManagedStatusServer(t *testing.T) (*Server, sqlmock.Sqlmock) {
	t.Helper()
	t.Setenv("EVS_AUTH_SERVICE_URL", "https://auth.example.test")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	serverCtx := context.WithValue(context.Background(), contextkeys.BillingDB, sqlx.NewDb(db, "sqlmock"))
	serverCtx = context.WithValue(serverCtx, contextkeys.SharedGatewayMode, true)
	return &Server{ctx: serverCtx}, mock
}

func TestGetLicenseMonitorStatus_UnauthenticatedHostScopeIsRedacted(t *testing.T) {
	server, mock := newManagedStatusServer(t)
	// No expectations: any billing lookup for the victim org is itself the leak.

	requestCtx := contextkeys.WithRequestInstanceScope(context.Background(), contextkeys.RequestInstanceScope{
		InstanceID: "22222222-2222-2222-2222-222222222222", OrganizationID: "11111111-1111-1111-1111-111111111111", OrganizationSlug: "victim",
	})
	response, err := server.GetLicenseMonitorStatus(requestCtx, connect.NewRequest(&gatewaypb.GetLicenseMonitorStatusRequest{}))
	if err != nil {
		t.Fatalf("GetLicenseMonitorStatus() error = %v", err)
	}
	license := response.Msg.GetLicense()
	if license.GetTenantId() != "" || license.GetInstanceId() != "" {
		t.Fatalf("unauthenticated host scope leaked identity: tenant %q instance %q", license.GetTenantId(), license.GetInstanceId())
	}
	if license.GetSandboxBillingEnabled() {
		t.Fatal("unauthenticated host scope leaked the sandbox billing flag")
	}
	if license.GetTier() != "cloud" || !license.GetActive() {
		t.Fatalf("managed status must still tell the SPA it is on cloud, got %+v", license)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetLicenseMonitorStatus_UnauthenticatedTenantConfigIsRedacted(t *testing.T) {
	server, mock := newManagedStatusServer(t)

	requestCtx := tenant.WithConfig(context.Background(), &tenant.Config{
		InstanceID: "22222222-2222-2222-2222-222222222222", OrganizationID: "11111111-1111-1111-1111-111111111111",
	})
	requestCtx = contextkeys.WithTenantID(requestCtx, "22222222-2222-2222-2222-222222222222")
	response, err := server.GetLicenseMonitorStatus(requestCtx, connect.NewRequest(&gatewaypb.GetLicenseMonitorStatusRequest{}))
	if err != nil {
		t.Fatalf("GetLicenseMonitorStatus() error = %v", err)
	}
	license := response.Msg.GetLicense()
	if license.GetTenantId() != "" || license.GetInstanceId() != "" || license.GetSandboxBillingEnabled() {
		t.Fatalf("unauthenticated tenant config leaked identity/billing: %+v", license)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestGetGatewayInstanceStatus_ManagedUnauthenticatedHidesInstanceID(t *testing.T) {
	server, _ := newManagedStatusServer(t)
	t.Setenv("EVS_INSTANCE_ID", "22222222-2222-2222-2222-222222222222")

	requestCtx := tenant.WithConfig(context.Background(), &tenant.Config{
		InstanceID: "22222222-2222-2222-2222-222222222222", OrganizationID: "11111111-1111-1111-1111-111111111111",
	})
	response, err := server.GetGatewayInstanceStatus(requestCtx, connect.NewRequest(&gatewaypb.GetGatewayInstanceStatusRequest{}))
	if err != nil {
		t.Fatalf("GetGatewayInstanceStatus() error = %v", err)
	}
	if got := response.Msg.GetInstanceId(); got != "" {
		t.Fatalf("unauthenticated managed status leaked instance id %q", got)
	}
	if !response.Msg.GetActivated() || response.Msg.GetStatus() != "active" {
		t.Fatalf("managed status must still report active, got %+v", response.Msg)
	}

	authedCtx := contextkeys.WithTenantAuthenticated(requestCtx)
	response, err = server.GetGatewayInstanceStatus(authedCtx, connect.NewRequest(&gatewaypb.GetGatewayInstanceStatusRequest{}))
	if err != nil {
		t.Fatalf("GetGatewayInstanceStatus() error = %v", err)
	}
	if got := response.Msg.GetInstanceId(); got != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("authenticated managed status must report its instance id, got %q", got)
	}
}
