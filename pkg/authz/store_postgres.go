package authz

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
)

// PostgresStore is a TupleStore backed by the relation_tuples table. It targets
// the cloud control-plane DB (everstack schema) where the org/workspace/
// instance graph lives; a self-hosted instance can point it at its own schema.
type PostgresStore struct {
	db    *sqlx.DB
	table string // qualified or unqualified table name (default "relation_tuples")
}

// NewPostgresStore builds a Postgres-backed tuple store. table defaults to
// "relation_tuples" (resolved via the connection's search_path) when empty.
func NewPostgresStore(db *sqlx.DB, table string) *PostgresStore {
	if table == "" {
		table = "relation_tuples"
	}
	return &PostgresStore{db: db, table: table}
}

// withTenantTx runs fn inside a transaction with app.current_tenant set to
// tenant, so Postgres row-level security (once armed on relation_tuples via
// scripts/db/arm-rls.sql) admits exactly that tenant's rows. set_config(...,
// is_local=true) is transaction-scoped, so the query MUST run in the same tx —
// hence every store operation goes through here. The explicit `tenant_id=...`
// filters in each query remain too, so the store is correct (and tenant-scoped)
// whether or not RLS is enabled.
//
// Cost: one short transaction per store call. The engine's Check walks the graph
// with several ListSubjects calls, so a check is a handful of these txs; that is
// fine for authz (not a data-plane hot path). If a request-scoped single tx is
// ever needed, thread one tx through ctx and reuse it here.
func (s *PostgresStore) withTenantTx(ctx context.Context, tenant string, fn func(*sqlx.Tx) error) (err error) {
	if tenant == "" {
		return fmt.Errorf("authz: withTenantTx requires a non-empty tenant")
	}
	tx, err := s.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if _, err = tx.ExecContext(ctx, `SELECT set_config('app.current_tenant', $1, true)`, tenant); err != nil {
		return fmt.Errorf("authz: set tenant GUC: %w", err)
	}
	if err = fn(tx); err != nil {
		return err
	}
	err = tx.Commit()
	return err
}

// Write implements TupleStore. Idempotent via ON CONFLICT DO NOTHING. Every row
// is stamped with the ctx tenant; a missing tenant fails closed.
func (s *PostgresStore) Write(ctx context.Context, tuples ...Tuple) error {
	if len(tuples) == 0 {
		return nil
	}
	tenant := TenantFromContext(ctx)
	if tenant == "" {
		return fmt.Errorf("authz: refusing to write tuples without a tenant in context")
	}
	q := fmt.Sprintf(`INSERT INTO %s
		(tenant_id, object_type, object_id, relation, subject_type, subject_id, subject_relation)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		ON CONFLICT DO NOTHING`, s.table)
	return s.withTenantTx(ctx, tenant, func(tx *sqlx.Tx) error {
		for _, t := range tuples {
			if _, err := tx.ExecContext(ctx, q,
				tenant, t.Object.Type, t.Object.ID, t.Relation,
				t.Subject.Object.Type, t.Subject.Object.ID, t.Subject.Relation,
			); err != nil {
				return fmt.Errorf("authz: write tuple %s: %w", t, err)
			}
		}
		return nil
	})
}

// Delete implements TupleStore.
func (s *PostgresStore) Delete(ctx context.Context, tuples ...Tuple) error {
	if len(tuples) == 0 {
		return nil
	}
	tenant := TenantFromContext(ctx)
	if tenant == "" {
		return fmt.Errorf("authz: refusing to delete tuples without a tenant in context")
	}
	q := fmt.Sprintf(`DELETE FROM %s WHERE
		tenant_id=$1 AND object_type=$2 AND object_id=$3 AND relation=$4 AND
		subject_type=$5 AND subject_id=$6 AND subject_relation=$7`, s.table)
	return s.withTenantTx(ctx, tenant, func(tx *sqlx.Tx) error {
		for _, t := range tuples {
			if _, err := tx.ExecContext(ctx, q,
				tenant, t.Object.Type, t.Object.ID, t.Relation,
				t.Subject.Object.Type, t.Subject.Object.ID, t.Subject.Relation,
			); err != nil {
				return fmt.Errorf("authz: delete tuple %s: %w", t, err)
			}
		}
		return nil
	})
}

// DeleteObject implements TupleStore: removes every tuple where object is the
// object, tenant-scoped, fail-closed without a tenant.
func (s *PostgresStore) DeleteObject(ctx context.Context, object Object) error {
	tenant := TenantFromContext(ctx)
	if tenant == "" {
		return fmt.Errorf("authz: refusing to delete object without a tenant in context")
	}
	q := fmt.Sprintf(`DELETE FROM %s WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3`, s.table)
	return s.withTenantTx(ctx, tenant, func(tx *sqlx.Tx) error {
		if _, err := tx.ExecContext(ctx, q, tenant, object.Type, object.ID); err != nil {
			return fmt.Errorf("authz: delete object %s: %w", object, err)
		}
		return nil
	})
}

// ListSubjects implements TupleStore. Scoped to the ctx tenant; fails closed
// without one so a check can never read across tenants.
func (s *PostgresStore) ListSubjects(ctx context.Context, object Object, relation string) ([]Subject, error) {
	tenant := TenantFromContext(ctx)
	if tenant == "" {
		return nil, fmt.Errorf("authz: refusing to read tuples without a tenant in context")
	}
	q := fmt.Sprintf(`SELECT subject_type, subject_id, subject_relation
		FROM %s WHERE tenant_id=$1 AND object_type=$2 AND object_id=$3 AND relation=$4`, s.table)
	var out []Subject
	err := s.withTenantTx(ctx, tenant, func(tx *sqlx.Tx) error {
		rows, err := tx.QueryxContext(ctx, q, tenant, object.Type, object.ID, relation)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var st, sid, srel string
			if err := rows.Scan(&st, &sid, &srel); err != nil {
				return err
			}
			out = append(out, Subject{Object: Object{Type: st, ID: sid}, Relation: srel})
		}
		return rows.Err()
	})
	return out, err
}

// compile-time interface check
var _ TupleStore = (*PostgresStore)(nil)
