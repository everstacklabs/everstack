package database

import (
	"context"
	"fmt"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/jmoiron/sqlx"
)

// SetTenantGUC issues `SET LOCAL app.current_tenant = '<tenant>'` on the
// given connection / transaction. The session GUC drives the
// tenant_isolation RLS policies installed by the
// rls_tenant_isolation_<ts> migration: any SELECT / UPDATE / DELETE
// on a tenant-scoped table is automatically filtered to rows where
// `tenant_id = current_setting('app.current_tenant')`.
//
// SET LOCAL is scoped to the current transaction; for non-transactional
// pool checkouts use SetTenantGUCSession (resets on connection return
// to the pool). In practice every meaningful read/write operates inside
// some transaction or short-lived conn, so SET LOCAL is the safer default
// — it prevents a pooled connection from leaking the previous request's
// tenant identity to the next.
//
// tenant must be the canonical tenant identifier — same value the
// query handlers compare against in their `WHERE tenant_id = $N`
// clauses. Empty tenant fails closed (RLS predicate evaluates to NULL
// → row excluded).
func SetTenantGUC(ctx context.Context, tx *sqlx.Tx, tenant string) error {
	if tx == nil {
		return fmt.Errorf("SetTenantGUC: tx is nil")
	}
	// Use set_config because SET LOCAL doesn't accept a $1 parameter.
	// set_config(setting_name, new_value, is_local) — is_local=true
	// means the value is reset at the end of the transaction, which
	// matches SET LOCAL semantics.
	_, err := tx.ExecContext(ctx,
		`SELECT set_config('app.current_tenant', $1, true)`, tenant)
	if err != nil {
		return fmt.Errorf("set app.current_tenant: %w", err)
	}
	return nil
}

// WithBypassRLS issues `SET LOCAL app.bypass_rls = 'on'` on the given
// transaction. The tenant_matches() helper short-circuits to true when
// this is set, allowing system-internal callers (controlplane,
// background workers, projection replays, schema migrations) to read
// across tenants without juggling tenant identifiers.
//
// Use sparingly. Every call site that needs cross-tenant access must
// be auditable. Prefer running such work inside a dedicated
// transaction so the bypass flag is automatically reset when the
// transaction commits or rolls back.
func WithBypassRLS(ctx context.Context, tx *sqlx.Tx) error {
	if tx == nil {
		return fmt.Errorf("WithBypassRLS: tx is nil")
	}
	_, err := tx.ExecContext(ctx,
		`SELECT set_config('app.bypass_rls', 'on', true)`)
	if err != nil {
		return fmt.Errorf("set app.bypass_rls: %w", err)
	}
	return nil
}

// RunWithTenant opens a transaction, sets app.current_tenant for its
// scope, runs the supplied function, and commits (or rolls back on
// error). The transaction is the natural scope for RLS GUCs because
// they auto-reset on commit/rollback — the next pool checkout starts
// with no tenant identity, so a forgotten SET can never leak.
//
// Use this at the boundary of any handler that needs RLS-scoped
// access. Internal helpers should accept a *sqlx.Tx to avoid nested
// transactions.
func RunWithTenant(ctx context.Context, db *sqlx.DB, tenant string, fn func(*sqlx.Tx) error) (err error) {
	if db == nil {
		return fmt.Errorf("RunWithTenant: db is nil")
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			logger.WithFields("panic", fmt.Sprintf("%v", p)).Error("RunWithTenant: panic during tenant tx")
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
			return
		}
		if cerr := tx.Commit(); cerr != nil {
			err = fmt.Errorf("commit tx: %w", cerr)
		}
	}()
	if err = SetTenantGUC(ctx, tx, tenant); err != nil {
		return err
	}
	return fn(tx)
}

// RunWithBypass opens a transaction, sets app.bypass_rls for its
// scope, runs the supplied function, and commits.
//
// Reserved for system-internal callers — projections replaying events
// across tenants, migrations, controlplane housekeeping. Every call
// site should justify the cross-tenant access in a comment.
func RunWithBypass(ctx context.Context, db *sqlx.DB, fn func(*sqlx.Tx) error) (err error) {
	if db == nil {
		return fmt.Errorf("RunWithBypass: db is nil")
	}
	tx, err := db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
			return
		}
		if cerr := tx.Commit(); cerr != nil {
			err = fmt.Errorf("commit tx: %w", cerr)
		}
	}()
	if err = WithBypassRLS(ctx, tx); err != nil {
		return err
	}
	return fn(tx)
}
