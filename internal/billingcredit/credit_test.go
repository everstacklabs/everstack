package billingcredit

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
)

const testOrganizationID = "11111111-1111-1111-1111-111111111111"

func TestStarterCreditMicrosUsesCanonicalPlans(t *testing.T) {
	if got := StarterCreditMicros(); got != 5_000_000 {
		t.Fatalf("StarterCreditMicros() = %d, want 5000000", got)
	}
}

// TestSignupGrantContractStable guards the shared grant identity: the compute
// gate (Resolve), the usage-debit loop, and the startup sweep all grant the
// one-time $5 with this exact key + resource type, so an org that hits every
// path is granted once, not twice. A drift here would double-grant.
func TestSignupGrantContractStable(t *testing.T) {
	if got, want := SignupGrantIdempotencyKey(testOrganizationID), "signup-grant:"+testOrganizationID; got != want {
		t.Fatalf("SignupGrantIdempotencyKey = %q, want %q", got, want)
	}
	if SignupGrantResourceType != "signup" {
		t.Fatalf("SignupGrantResourceType = %q, want %q", SignupGrantResourceType, "signup")
	}
}

// mockWalletSequence mocks the wallet path of Resolve after the informational
// subscription read: the advisory-locked idempotent grant, then the balance
// recompute and the holds read. There is deliberately NO debit statement in
// this sequence: sqlmock is strictly ordered, so a debit sneaking back into
// Resolve fails these tests. Debits belong to the billing service's usage-debit
// loop alone.
func mockWalletSequence(mock sqlmock.Sqlmock, grant, balance, held int64) {
	mockWalletTx(mock, grant, balance, held)
	expectExempt(mock, false)
}

// mockWalletTx is the wallet transaction alone, without the exemption read.
func mockWalletTx(mock sqlmock.Sqlmock, grant, balance, held int64) {
	mock.ExpectBegin()
	mock.ExpectExec(`pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`'expiry'`).
		WillReturnRows(sqlmock.NewRows([]string{"amount_micros"})) // nothing due
	mock.ExpectExec(`SAVEPOINT signup_claims`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`WITH owner AS`).
		WithArgs(testOrganizationID, SignupGrantResourceType).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "email_key"})) // already granted: no candidate
	mock.ExpectExec(`RELEASE SAVEPOINT signup_claims`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(`FILTER \(WHERE entry_type = 'grant' AND resource_type = [$]2\)`).
		WillReturnRows(sqlmock.NewRows([]string{"g"}).AddRow(grant))
	mock.ExpectQuery(`SUM\(amount_micros\), 0\)::BIGINT\s+FROM billing[.]credit_ledger`).
		WillReturnRows(sqlmock.NewRows([]string{"b"}).AddRow(balance))
	mock.ExpectExec(`INSERT INTO billing[.]credit_balances`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`FROM billing[.]credit_holds`).
		WillReturnRows(sqlmock.NewRows([]string{"h"}).AddRow(held))
	mock.ExpectCommit()
}

// expectExempt mocks the internal-organization read that follows the wallet.
func expectExempt(mock sqlmock.Sqlmock, exempt bool) {
	mock.ExpectQuery(`FROM everstack[.]organizations`).
		WithArgs(testOrganizationID).
		WillReturnRows(sqlmock.NewRows([]string{"exempt"}).AddRow(exempt))
}

func expectNoSubscription(mock sqlmock.Sqlmock) {
	mock.ExpectQuery(`SELECT status, stripe_customer_id, stripe_subscription_id`).
		WithArgs(testOrganizationID).WillReturnError(sql.ErrNoRows)
}

func expectSubscription(mock sqlmock.Sqlmock, status string) {
	mock.ExpectQuery(`SELECT status, stripe_customer_id, stripe_subscription_id`).
		WithArgs(testOrganizationID).
		WillReturnRows(sqlmock.NewRows([]string{
			"status", "stripe_customer_id", "stripe_subscription_id",
		}).AddRow(status, "cus_123", "sub_123"))
}

func newMock(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sqlx.NewDb(db, "sqlmock"), mock
}

// TestResolveSubscribedOrgIsWalletGated is the S0 contract: an active
// subscription no longer bypasses the wallet. The org is granted its signup
// credit like everyone else and is allowed only while the balance is positive.
func TestResolveSubscribedOrgIsWalletGated(t *testing.T) {
	db, mock := newMock(t)
	expectSubscription(mock, "active")
	mockWalletSequence(mock, 5_000_000, 5_000_000, 0)

	access, err := Resolve(context.Background(), db, testOrganizationID, "free")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !access.Allowed || !access.BillingConfigured || !access.BillingActive || access.Credit.RemainingMicros != 5_000_000 {
		t.Fatalf("Resolve() = %#v", access)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestResolveSubscribedOrgBlockedOnEmptyWallet: no free compute for subscribed
// orgs. Once the wallet is spent, an active Stripe subscription does not admit.
func TestResolveSubscribedOrgBlockedOnEmptyWallet(t *testing.T) {
	db, mock := newMock(t)
	expectSubscription(mock, "active")
	mockWalletSequence(mock, 5_000_000, 0, 0)

	access, err := Resolve(context.Background(), db, testOrganizationID, "free")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if access.Allowed || !access.BillingActive || access.Credit.RemainingMicros != 0 || access.Credit.UsedMicros != 5_000_000 {
		t.Fatalf("Resolve() = %#v", access)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// TestResolveCanceledSubscriptionStillSpendsWallet: a lapsed subscription is
// irrelevant to admission. The wallet decides.
func TestResolveCanceledSubscriptionStillSpendsWallet(t *testing.T) {
	db, mock := newMock(t)
	expectSubscription(mock, "canceled")
	mockWalletSequence(mock, 5_000_000, 4_000_000, 0)

	access, err := Resolve(context.Background(), db, testOrganizationID, "basic")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !access.Allowed || !access.BillingConfigured || access.BillingActive || access.Credit.RemainingMicros != 4_000_000 {
		t.Fatalf("Resolve() = %#v", access)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveGrantsFungibleSignupCredit(t *testing.T) {
	db, mock := newMock(t)
	expectNoSubscription(mock)
	mockWalletSequence(mock, 5_000_000, 5_000_000, 0)

	access, err := Resolve(context.Background(), db, testOrganizationID, "free")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !access.Allowed || access.BillingConfigured || access.Credit.RemainingMicros != 5_000_000 {
		t.Fatalf("Resolve() = %#v", access)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveBlocksExhaustedWallet(t *testing.T) {
	db, mock := newMock(t)
	expectNoSubscription(mock)
	// $5 of usage debited by the billing loop consumed the whole $5 grant.
	mockWalletSequence(mock, 5_000_000, 0, 0)

	access, err := Resolve(context.Background(), db, testOrganizationID, "free")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if access.Allowed || access.Credit.RemainingMicros != 0 {
		t.Fatalf("Resolve() = %#v", access)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveSubtractsHeldCredit(t *testing.T) {
	db, mock := newMock(t)
	expectNoSubscription(mock)
	mockWalletSequence(mock, 5_000_000, 1_000_000, 1_000_000)

	access, err := Resolve(context.Background(), db, testOrganizationID, "free")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if access.Allowed || access.Credit.RemainingMicros != 0 {
		t.Fatalf("Resolve() = %#v", access)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveEnterpriseBypassesWallet(t *testing.T) {
	db, mock := newMock(t)
	access, err := Resolve(context.Background(), db, testOrganizationID, "enterprise")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !access.Allowed {
		t.Fatalf("Resolve() = %#v", access)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A failed internal-organization read falls back to the wallet decision: it
// neither admits a $0 organization nor refuses a funded one.
func TestResolveExemptLookupFailureFallsBackToWallet(t *testing.T) {
	for _, tc := range []struct {
		name    string
		balance int64
		allowed bool
	}{
		{"empty wallet stays refused", 0, false},
		{"funded wallet stays admitted", 2_000_000, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock := newMock(t)
			expectNoSubscription(mock)
			mockWalletTx(mock, 0, tc.balance, 0)
			mock.ExpectQuery(`FROM everstack[.]organizations`).WillReturnError(errors.New("connection reset"))

			access, err := Resolve(context.Background(), db, testOrganizationID, "free")
			if err != nil {
				t.Fatalf("Resolve() error = %v", err)
			}
			if access.Allowed != tc.allowed || access.Exempt {
				t.Fatalf("Resolve() = %#v, want allowed=%t and not exempt", access, tc.allowed)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// An internal organization is admitted on an empty wallet through the same
// sequence: the wallet is still read (and its signup grant still ensured).
func TestResolveExemptOrgAdmittedOnEmptyWallet(t *testing.T) {
	db, mock := newMock(t)
	expectNoSubscription(mock)
	mockWalletTx(mock, 0, 0, 0)
	expectExempt(mock, true)

	access, err := Resolve(context.Background(), db, testOrganizationID, "free")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !access.Allowed || !access.Exempt || access.Credit.RemainingMicros != 0 {
		t.Fatalf("Resolve() = %#v", access)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
