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

// expectWalletGate mocks billingcredit.Resolve's wallet path after the
// informational subscription read: idempotent signup grant, balance recompute,
// holds. A subscription never bypasses it; the wallet decides.
func expectWalletGate(mock sqlmock.Sqlmock, balance int64) {
	mock.ExpectBegin()
	mock.ExpectExec(`pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`'expiry'`).WillReturnRows(sqlmock.NewRows([]string{"amount_micros"}))
	mock.ExpectExec(`SAVEPOINT signup_claims`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`WITH owner AS`).WillReturnRows(sqlmock.NewRows([]string{"user_id", "email_key"})) // not eligible: already granted
	mock.ExpectExec(`RELEASE SAVEPOINT signup_claims`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`FILTER \(WHERE entry_type = 'grant' AND resource_type = [$]2\)`).
		WillReturnRows(sqlmock.NewRows([]string{"g"}).AddRow(int64(5_000_000)))
	mock.ExpectQuery(`SUM\(amount_micros\), 0\)::BIGINT\s+FROM billing[.]credit_ledger`).
		WillReturnRows(sqlmock.NewRows([]string{"b"}).AddRow(balance))
	mock.ExpectExec(`INSERT INTO billing[.]credit_balances`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`FROM billing[.]credit_holds`).
		WillReturnRows(sqlmock.NewRows([]string{"h"}).AddRow(int64(0)))
	mock.ExpectCommit()
}

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
	expectWalletGate(mock, 5_000_000)

	serverCtx := context.WithValue(context.Background(), contextkeys.BillingDB, sqlx.NewDb(db, "sqlmock"))
	server := &Server{ctx: serverCtx}
	if !server.managedSandboxBillingEnabled(context.Background(), instanceID) {
		t.Fatal("a subscribed org with wallet credit should run sandboxes on its managed instance")
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
	expectWalletGate(mock, 5_000_000)

	serverCtx := context.WithValue(context.Background(), contextkeys.BillingDB, sqlx.NewDb(db, "sqlmock"))
	server := &Server{ctx: serverCtx}
	// The scope is routing evidence; the authenticated marker is what the
	// cookie/API-key middleware sets after verifying membership (EVS-309).
	requestCtx := contextkeys.WithRequestInstanceScope(context.Background(), contextkeys.RequestInstanceScope{
		InstanceID: instanceID, OrganizationID: organizationID, OrganizationSlug: "everstack",
	})
	requestCtx = contextkeys.WithTenantAuthenticated(requestCtx)
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
		t.Fatal("a subscribed org with wallet credit should have sandbox billing enabled")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// No free compute on a managed instance either: an active subscription with an
// empty wallet is refused.
func TestManagedSandboxBillingDisabledOnEmptyWalletDespiteSubscription(t *testing.T) {
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
	expectWalletGate(mock, 0)

	serverCtx := context.WithValue(context.Background(), contextkeys.BillingDB, sqlx.NewDb(db, "sqlmock"))
	server := &Server{ctx: serverCtx}
	if server.managedSandboxBillingEnabled(context.Background(), instanceID) {
		t.Fatal("an empty wallet must refuse sandbox compute even with an active subscription")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
