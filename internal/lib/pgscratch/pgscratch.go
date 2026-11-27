// Package pgscratch gives real-Postgres tests a throwaway database.
//
// Tests skip unless EVS_TEST_PG_DSN points at a server the test may create
// databases on, for example
// postgres://postgres:postgres@localhost:54322/postgres?sslmode=disable.
// Each call creates a uniquely named database and drops it (WITH FORCE) when
// the test ends, so nothing outlives the run.
package pgscratch

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"strings"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib" // pgx driver for sqlx.Connect("pgx", ...)
	"github.com/jmoiron/sqlx"
)

// EnvDSN names the environment variable holding the admin DSN.
const EnvDSN = "EVS_TEST_PG_DSN"

// New creates a scratch database whose name starts with label and returns a
// connection to it. The database is dropped on test cleanup.
func New(t testing.TB, label string) *sqlx.DB {
	t.Helper()
	dsn := os.Getenv(EnvDSN)
	if dsn == "" {
		t.Skipf("%s not set; skipping real-Postgres test", EnvDSN)
	}
	admin, err := sqlx.Connect("pgx", dsn)
	if err != nil {
		t.Fatalf("pgscratch: connect admin: %v", err)
	}
	var suffix [4]byte
	_, _ = rand.Read(suffix[:])
	name := "pgscratch_" + sanitize(label) + "_" + hex.EncodeToString(suffix[:])
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		admin.Close()
		t.Fatalf("pgscratch: create database: %v", err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DROP DATABASE IF EXISTS ` + name + ` WITH (FORCE)`)
		admin.Close()
	})

	scratch := dsn
	if i := strings.LastIndex(dsn, "/"); i > 0 {
		rest, query := dsn[i+1:], ""
		if j := strings.Index(rest, "?"); j >= 0 {
			query = rest[j:]
		}
		scratch = dsn[:i+1] + name + query
	}
	db, err := sqlx.Connect("pgx", scratch)
	if err != nil {
		t.Fatalf("pgscratch: connect scratch: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// MustExec runs each statement and fails the test on the first error.
func MustExec(t testing.TB, db *sqlx.DB, statements ...string) {
	t.Helper()
	for _, stmt := range statements {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("pgscratch: exec %q: %v", firstLine(stmt), err)
		}
	}
}

func sanitize(label string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(label) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		}
	}
	if b.Len() == 0 {
		return "db"
	}
	out := b.String()
	if len(out) > 30 {
		out = out[:30]
	}
	return out
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
