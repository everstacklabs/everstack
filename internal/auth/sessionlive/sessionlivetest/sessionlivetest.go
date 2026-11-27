// Package sessionlivetest provides real-Postgres fixtures for testing
// session validators against Better Auth revocation. Tests import it; it
// is never linked into a binary.
//
// Fixtures need EVS_SESSION_TEST_DSN: a superuser DSN whose database is
// "postgres" (for example postgres://postgres:pw@localhost:54322/postgres).
// Each fixture creates its own scratch database and drops it on cleanup.
// Without the variable the calling test is skipped.
package sessionlivetest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/auth/sessionlive"
	"github.com/everstacklabs/everstack/internal/database/migrations"
)

// DSNEnv names the environment variable holding the superuser DSN.
const DSNEnv = "EVS_SESSION_TEST_DSN"

// Env is a scratch database with one everstack user.
type Env struct {
	DB *sqlx.DB
	// DSN connects to the scratch database (for code under test that opens
	// its own pool).
	DSN    string
	UserID uuid.UUID
	baUser string
}

// NewPlatform returns a scratch database shaped like the cloud platform DB:
// cloud migrations (everstack.sessions with the identity link columns and
// the sessions_identity_pair constraint) plus the real Better Auth DDL.
func NewPlatform(t *testing.T) *Env {
	t.Helper()
	ctx := context.Background()
	db, dsn := scratchDB(t, ctx)
	root := repoRoot(t)
	withRepoRoot(t, root, func() {
		if err := migrations.EnsureForService(ctx, db, "postgres", "cloud"); err != nil {
			t.Fatalf("cloud migrations: %v", err)
		}
	})
	ddl, err := os.ReadFile(filepath.Join(root, "services/identity/migrations/postgres/better_auth_core_20260727120000/up.sql"))
	if err != nil {
		t.Fatalf("read identity migration: %v", err)
	}
	e := &Env{DB: db, DSN: dsn, UserID: uuid.New(), baUser: "ba_user_" + uuid.NewString()[:8]}
	e.Exec(t, `CREATE SCHEMA IF NOT EXISTS identity`)
	e.Exec(t, string(ddl))
	e.Exec(t, `INSERT INTO everstack.users (id, external_id, email) VALUES ($1, $2, $3)`,
		e.UserID, "ext-"+e.UserID.String(), e.UserID.String()+"@example.test")
	e.Exec(t, `INSERT INTO identity."user" (id, name, email, "emailVerified") VALUES ($1, 'Test', $2, true)`,
		e.baUser, e.baUser+"@example.test")
	return e
}

// NewCE returns a scratch database shaped like a self-hosted (CE)
// deployment: everstack.sessions from the self-hosted auth migration, with
// no identity link columns and no identity schema.
func NewCE(t *testing.T) *Env {
	t.Helper()
	ctx := context.Background()
	db, dsn := scratchDB(t, ctx)
	root := repoRoot(t)
	ddl, err := os.ReadFile(filepath.Join(root, "internal/database/migrations/sql/postgres/auth_self_hosted_20260111120000/up.sql"))
	if err != nil {
		t.Fatalf("read self-hosted auth migration: %v", err)
	}
	e := &Env{DB: db, DSN: dsn, UserID: uuid.New()}
	e.Exec(t, `CREATE SCHEMA IF NOT EXISTS everstack`)
	// Run the DDL on one connection whose search_path is everstack, the
	// schema the gateway queries.
	conn, err := db.Connx(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `SET search_path TO everstack`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, string(ddl)); err != nil {
		t.Fatalf("self-hosted auth DDL: %v", err)
	}
	_ = conn.Close()
	e.Exec(t, `INSERT INTO everstack.users (id, email) VALUES ($1, $2)`, e.UserID, e.UserID.String()+"@example.test")
	return e
}

// Exec runs q or fails the test.
func (e *Env) Exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := e.DB.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", firstWord(q), err)
	}
}

// LinkedSession inserts a Better Auth session expiring after identityTTL
// and its mirrored everstack.sessions row (valid 30 days). It returns the
// Better Auth session id and the mirror token.
func (e *Env) LinkedSession(t *testing.T, identityTTL time.Duration) (string, string) {
	t.Helper()
	if e.baUser == "" {
		t.Fatal("LinkedSession needs a platform env")
	}
	baSession := "ba_sess_" + uuid.NewString()
	e.Exec(t, `INSERT INTO identity.session (id, "expiresAt", token, "updatedAt", "userId")
	           VALUES ($1, $2, $3, now(), $4)`,
		baSession, time.Now().Add(identityTTL), "ba_tok_"+uuid.NewString(), e.baUser)
	token := "mirror_" + uuid.NewString()
	e.Exec(t, `INSERT INTO everstack.sessions (user_id, token, expires_at, identity_provider, identity_session_id)
	           VALUES ($1, $2, now() + interval '30 days', $3, $4)`,
		e.UserID, token, sessionlive.BetterAuthProvider, baSession)
	return baSession, token
}

// LegacySession inserts an unlinked session row valid for an hour and
// returns its token.
func (e *Env) LegacySession(t *testing.T) string {
	t.Helper()
	token := "legacy_" + uuid.NewString()
	e.Exec(t, `INSERT INTO everstack.sessions (user_id, token, expires_at) VALUES ($1, $2, now() + interval '1 hour')`,
		e.UserID, token)
	return token
}

// RevokeIdentitySession deletes the Better Auth session (sign-out,
// revokeSession), leaving the mirror row behind.
func (e *Env) RevokeIdentitySession(t *testing.T, id string) {
	t.Helper()
	e.Exec(t, `DELETE FROM identity.session WHERE id = $1`, id)
}

// ExpireIdentitySession moves the Better Auth session's expiry into the past.
func (e *Env) ExpireIdentitySession(t *testing.T, id string) {
	t.Helper()
	e.Exec(t, `UPDATE identity.session SET "expiresAt" = now() - interval '1 minute' WHERE id = $1`, id)
}

// BreakIdentity makes identity.session unreachable (as a missing grant or a
// dropped table would) while leaving everstack.sessions intact. Restore
// undoes it.
func (e *Env) BreakIdentity(t *testing.T) (restore func()) {
	t.Helper()
	e.Exec(t, `ALTER TABLE identity.session RENAME TO session_unavailable`)
	return func() { e.Exec(t, `ALTER TABLE identity.session_unavailable RENAME TO session`) }
}

// AddOrgMembership creates organization orgID (if absent) and makes the
// env user a member with role.
func (e *Env) AddOrgMembership(t *testing.T, orgID uuid.UUID, role string) {
	t.Helper()
	e.Exec(t, `INSERT INTO everstack.organizations (id, slug, name) VALUES ($1, $2, 'Test Org')
	           ON CONFLICT (id) DO NOTHING`, orgID, "org-"+orgID.String()[:8])
	e.Exec(t, `INSERT INTO everstack.organization_members (organization_id, user_id, role) VALUES ($1, $2, $3)`,
		orgID, e.UserID, role)
}

// LegacyRows is a sqlmock result for sessionlive's lookup query describing
// a legacy (unlinked) live row on a CE-shaped database. Unit tests that stub
// the session query with sqlmock use it in place of a two-column row.
func LegacyRows(userID string, expiresAt time.Time) *sqlmock.Rows {
	return sqlmock.NewRows(sessionlive.LookupColumns).
		AddRow(userID, expiresAt, false, nil, nil, false, uuid.NewString())
}

// NoRows is a sqlmock result for the lookup query with no matching row.
func NoRows() *sqlmock.Rows { return sqlmock.NewRows(sessionlive.LookupColumns) }

// QueryPattern is a sqlmock regexp matching sessionlive's lookup query.
const QueryPattern = `SELECT user_id::text, expires_at,[\s\S]*FROM everstack\.sessions AS s WHERE s\.token = \$1`

var scratchSeq atomic.Int64

func scratchDB(t *testing.T, ctx context.Context) (*sqlx.DB, string) {
	t.Helper()
	adminDSN := strings.TrimSpace(os.Getenv(DSNEnv))
	if adminDSN == "" {
		t.Skip(DSNEnv + " not set")
	}
	admin, err := sqlx.ConnectContext(ctx, "pgx", adminDSN)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	name := fmt.Sprintf("evs_sessionlive_%d_%d", time.Now().UnixNano(), scratchSeq.Add(1))
	if _, err := admin.ExecContext(ctx, `CREATE DATABASE `+name); err != nil {
		_ = admin.Close()
		t.Fatalf("create scratch database: %v", err)
	}
	slash := strings.LastIndex(adminDSN, "/")
	if slash < 0 || !strings.HasPrefix(adminDSN[slash+1:], "postgres") {
		_ = admin.Close()
		t.Fatalf("%s must name the postgres database so the fixture can swap in %s", DSNEnv, name)
	}
	dsn := adminDSN[:slash+1] + name + strings.TrimPrefix(adminDSN[slash+1:], "postgres")
	db, err := sqlx.ConnectContext(ctx, "pgx", dsn)
	if err != nil {
		t.Fatalf("connect scratch: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
		_, _ = admin.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`)
		_ = admin.Close()
	})
	return db, dsn
}

// repoRoot returns the module root (the directory holding go.mod).
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repo root not found")
		}
		dir = parent
	}
}

// withRepoRoot runs fn with the working directory at root; migrations
// resolve their folders relative to it.
func withRepoRoot(t *testing.T, root string, fn func()) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(orig) }()
	fn()
}

func firstWord(q string) string {
	if f := strings.Fields(q); len(f) > 0 {
		return f[0] + "..."
	}
	return ""
}
