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

func TestManagedSandboxBillingEnabledResolvesInstanceToOrganization(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	const (
		instanceID     = "22222222-2222-2222-2222-222222222222"
		organizationID = "11111111-1111-1111-1111-111111111111"
	)

	mock.ExpectQuery(`FROM everstack[.]organizations`).
		WithArgs(instanceID).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "plan_tier"}).
			AddRow(organizationID, "basic"))
	mock.ExpectQuery(`SELECT status, stripe_customer_id, stripe_subscription_id`).
		WithArgs(organizationID).
		WillReturnRows(sqlmock.NewRows([]string{
			"status", "stripe_customer_id", "stripe_subscription_id",
		}).AddRow("active", "cus_basic", "sub_basic"))

	serverCtx := context.WithValue(context.Background(), contextkeys.BillingDB, sqlx.NewDb(db, "sqlmock"))
	server := &Server{ctx: serverCtx}
	if !server.managedSandboxBillingEnabled(context.Background(), instanceID) {
		t.Fatal("active Basic subscription should enable sandbox compute for its managed instance")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestManagedLicenseStatusUsesVerifiedHostBillingIdentity(t *testing.T) {
	t.Setenv("EVS_AUTH_SERVICE_URL", "https://auth.example.test")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	const (
		instanceID     = "22222222-2222-2222-2222-222222222222"
		organizationID = "11111111-1111-1111-1111-111111111111"
	)
	mock.ExpectQuery(`FROM everstack[.]organizations`).
		WithArgs(organizationID).
		WillReturnRows(sqlmock.NewRows([]string{"organization_id", "plan_tier"}).
			AddRow(organizationID, "basic"))
	mock.ExpectQuery(`SELECT status, stripe_customer_id, stripe_subscription_id`).
		WithArgs(organizationID).
		WillReturnRows(sqlmock.NewRows([]string{
			"status", "stripe_customer_id", "stripe_subscription_id",
		}).AddRow("active", "cus_basic", "sub_basic"))

	serverCtx := context.WithValue(context.Background(), contextkeys.BillingDB, sqlx.NewDb(db, "sqlmock"))
	server := &Server{ctx: serverCtx}
	requestCtx := contextkeys.WithRequestInstanceScope(context.Background(), contextkeys.RequestInstanceScope{
		InstanceID: instanceID, OrganizationID: organizationID, OrganizationSlug: "everstack",
	})
	response, err := server.GetLicenseMonitorStatus(
		requestCtx,
		connect.NewRequest(&gatewaypb.GetLicenseMonitorStatusRequest{}),
	)
	if err != nil {
		t.Fatalf("GetLicenseMonitorStatus() error = %v", err)
	}
	license := response.Msg.GetLicense()
	if license.GetTenantId() != organizationID || license.GetInstanceId() != instanceID {
		t.Fatalf("license identity = tenant %q instance %q", license.GetTenantId(), license.GetInstanceId())
	}
	if !license.GetSandboxBillingEnabled() {
		t.Fatal("active Basic subscription should enable sandbox billing")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
