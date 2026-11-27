package billingcredit

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
)

// Grant against a real Postgres with just the tables it touches. Requires
// EVS_BILLING_TEST_DSN (superuser ending in /postgres); skipped otherwise.
func TestGrantAgainstPostgres(t *testing.T) {
	adminDSN := strings.TrimSpace(os.Getenv("EVS_BILLING_TEST_DSN"))
	if adminDSN == "" {
		t.Skip("EVS_BILLING_TEST_DSN not set")
	}
	ctx := context.Background()
	admin, err := sqlx.ConnectContext(ctx, "pgx", adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("evs_grant_%d", time.Now().UnixNano())
	admin.MustExec(`CREATE DATABASE ` + name)
	t.Cleanup(func() { _, _ = admin.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`); _ = admin.Close() })
	slash := strings.LastIndex(adminDSN, "/")
	db, err := sqlx.ConnectContext(ctx, "pgx", adminDSN[:slash+1]+name+strings.TrimPrefix(adminDSN[slash+1:], "postgres"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`CREATE SCHEMA everstack; CREATE SCHEMA billing;
		CREATE TABLE everstack.organizations (id UUID PRIMARY KEY);
		CREATE TABLE billing.credit_ledger (id UUID PRIMARY KEY DEFAULT gen_random_uuid(), organization_id UUID NOT NULL,
			entry_type TEXT NOT NULL, amount_micros BIGINT NOT NULL, resource_type TEXT, idempotency_key TEXT NOT NULL UNIQUE,
			expires_at TIMESTAMPTZ, metadata JSONB NOT NULL DEFAULT '{}', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW());
		CREATE TABLE billing.credit_balances (organization_id UUID PRIMARY KEY, balance_micros BIGINT NOT NULL DEFAULT 0,
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW());`)
	org, other := uuid.New(), uuid.New()
	db.MustExec(`INSERT INTO everstack.organizations (id) VALUES ($1), ($2)`, org, other)

	grantID := uuid.New()
	in := GrantInput{OrganizationID: org, GrantID: grantID, AmountMicros: 5_000_000, Reason: "incident", Actor: "admin@everstack.ai"}
	first, err := Grant(ctx, db, in)
	if err != nil || !first.Granted || first.BalanceMicros != 5_000_000 {
		t.Fatalf("first grant = %+v, %v", first, err)
	}
	again, err := Grant(ctx, db, in)
	if err != nil || again.Granted || again.LedgerEntryID != first.LedgerEntryID || again.BalanceMicros != 5_000_000 {
		t.Fatalf("retried grant = %+v, %v; want a no-op on the same entry", again, err)
	}
	if _, err := Grant(ctx, db, GrantInput{OrganizationID: other, GrantID: grantID, AmountMicros: 1, Reason: "x"}); err == nil {
		t.Fatal("a grant id reused for another organization was accepted")
	}
	if _, err := Grant(ctx, db, GrantInput{OrganizationID: uuid.New(), GrantID: uuid.New(), AmountMicros: 1, Reason: "x"}); err != ErrOrganizationNotFound {
		t.Fatalf("unknown org err = %v", err)
	}
	soon := time.Now().Add(24 * time.Hour)
	if _, err := Grant(ctx, db, GrantInput{OrganizationID: org, GrantID: uuid.New(), AmountMicros: 1, Reason: "promo", ExpiresAt: &soon}); err != ErrExpiringGrantUnsupported {
		t.Fatalf("expiring grant err = %v, want ErrExpiringGrantUnsupported", err)
	}
	var entryType, key string
	var expires *time.Time
	if err := db.QueryRowx(`SELECT entry_type, idempotency_key, expires_at FROM billing.credit_ledger WHERE organization_id = $1`, org).Scan(&entryType, &key, &expires); err != nil {
		t.Fatal(err)
	}
	if entryType != "grant" || key != "grant:"+grantID.String() || expires != nil {
		t.Fatalf("ledger row = %s %s %v", entryType, key, expires)
	}
	var otherBalance int64
	_ = db.Get(&otherBalance, `SELECT COALESCE((SELECT balance_micros FROM billing.credit_balances WHERE organization_id = $1),0)`, other)
	if otherBalance != 0 {
		t.Fatalf("other org balance = %d", otherBalance)
	}
}
