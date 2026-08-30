package billingcredit

import (
	"context"
	"database/sql"
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

// TestSignupGrantContractStable guards the shared grant identity: the sandbox
// gate (Resolve) and the usage-debit backstop both grant the one-time $5 with
// this exact key + resource type, so a free org that hits both paths is granted
// once, not twice. A drift here would double-grant.
func TestSignupGrantContractStable(t *testing.T) {
	if got, want := SignupGrantIdempotencyKey(testOrganizationID), "signup-grant:"+testOrganizationID; got != want {
		t.Fatalf("SignupGrantIdempotencyKey = %q, want %q", got, want)
	}
	if SignupGrantResourceType != "signup" {
		t.Fatalf("SignupGrantResourceType = %q, want %q", SignupGrantResourceType, "signup")
	}
}

func TestResolveAllowsActiveBilling(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	mock.ExpectQuery(`SELECT status, stripe_customer_id, stripe_subscription_id`).
		WithArgs(testOrganizationID).
		WillReturnRows(sqlmock.NewRows([]string{
			"status", "stripe_customer_id", "stripe_subscription_id",
		}).AddRow("active", "cus_123", "sub_123"))

	access, err := Resolve(context.Background(), sqlx.NewDb(db, "sqlmock"), testOrganizationID, "free")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !access.Allowed || !access.BillingConfigured || !access.BillingActive {
		t.Fatalf("Resolve() = %#v", access)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// mockFreeWalletSequence mocks the free-tier wallet path of Resolve: no
// subscription, then the advisory-locked grant + sandbox-debit + balance
// recompute + holds read. sandboxCost is the cumulative sandbox cost; billed is
// the prior watermark (-1 = no watermark row, seeded from seedBaseline);
// grant/balance/held are the recomputed figures.
func mockFreeWalletSequence(mock sqlmock.Sqlmock, sandboxCost, billed, seedBaseline, grant, balance, held int64) {
	mock.ExpectQuery(`SELECT status, stripe_customer_id, stripe_subscription_id`).
		WithArgs(testOrganizationID).WillReturnError(sql.ErrNoRows)
	mock.ExpectBegin()
	mock.ExpectExec(`pg_advisory_xact_lock`).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(`INSERT INTO billing[.]credit_ledger`).WillReturnResult(sqlmock.NewResult(0, 1)) // grant
	mock.ExpectQuery(`FROM billing[.]usage_meter_watermarks`).
		WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow(sandboxCost))
	if billed < 0 {
		// no watermark row -> seed from the enrollment baseline, then persist it
		mock.ExpectQuery(`FROM billing[.]usage_debit_watermark`).WillReturnError(sql.ErrNoRows)
		mock.ExpectQuery(`baseline_cost_micros`).
			WillReturnRows(sqlmock.NewRows([]string{"b"}).AddRow(seedBaseline))
		mock.ExpectExec(`INSERT INTO billing[.]usage_debit_watermark`).WillReturnResult(sqlmock.NewResult(0, 1))
		billed = seedBaseline
	} else {
		mock.ExpectQuery(`FROM billing[.]usage_debit_watermark`).
			WillReturnRows(sqlmock.NewRows([]string{"b"}).AddRow(billed))
	}
	if sandboxCost-billed > 0 {
		mock.ExpectExec(`INSERT INTO billing[.]credit_ledger`).WillReturnResult(sqlmock.NewResult(0, 1))         // debit
		mock.ExpectExec(`INSERT INTO billing[.]usage_debit_watermark`).WillReturnResult(sqlmock.NewResult(0, 1)) // watermark advance
	}
	mock.ExpectQuery(`FILTER \(WHERE entry_type = 'grant'\)`).
		WillReturnRows(sqlmock.NewRows([]string{"g"}).AddRow(grant))
	mock.ExpectQuery(`SUM\(amount_micros\), 0\)::BIGINT\s+FROM billing[.]credit_ledger`).
		WillReturnRows(sqlmock.NewRows([]string{"b"}).AddRow(balance))
	mock.ExpectExec(`INSERT INTO billing[.]credit_balances`).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`FROM billing[.]credit_holds`).
		WillReturnRows(sqlmock.NewRows([]string{"h"}).AddRow(held))
	mock.ExpectCommit()
}

func TestResolveGrantsFungibleSignupCredit(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	// No sandbox cost yet: grant $5, balance $5, nothing held -> allowed.
	mockFreeWalletSequence(mock, 0, -1, 0, 5_000_000, 5_000_000, 0)

	access, err := Resolve(context.Background(), sqlx.NewDb(db, "sqlmock"), testOrganizationID, "free")
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
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	// $5 of sandbox cost consumes the whole $5 grant: balance $0 -> blocked.
	mockFreeWalletSequence(mock, 5_000_000, 0, 0, 5_000_000, 0, 0)

	access, err := Resolve(context.Background(), sqlx.NewDb(db, "sqlmock"), testOrganizationID, "free")
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

// TestResolvePreservesEnrollmentBaseline guards the migration path: an org with
// $3 of sandbox cost accrued before the credit existed (enrollment baseline $2,
// current cumulative $5) must be charged only the $3 past its baseline, leaving
// $2 spendable — never the full $5 (which would wrongly block it).
func TestResolvePreservesEnrollmentBaseline(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("create sqlmock: %v", err)
	}
	defer db.Close()

	// cumulative $5, no watermark row, enrollment baseline $2 -> debit $3 -> balance $2.
	mockFreeWalletSequence(mock, 5_000_000, -1, 2_000_000, 5_000_000, 2_000_000, 0)

	access, err := Resolve(context.Background(), sqlx.NewDb(db, "sqlmock"), testOrganizationID, "free")
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !access.Allowed || access.Credit.RemainingMicros != 2_000_000 {
		t.Fatalf("Resolve() = %#v", access)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
