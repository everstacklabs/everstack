package sessionlive_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"

	"github.com/everstacklabs/everstack/internal/auth/sessionlive"
	"github.com/everstacklabs/everstack/internal/auth/sessionlive/sessionlivetest"
)

func ns(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

func TestCheck_LinkShapes(t *testing.T) {
	// No query may run for these shapes: sqlmock fails on any unexpected one.
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	cases := []struct {
		name string
		link sessionlive.Link
		want sessionlive.Status
	}{
		{"legacy", sessionlive.Link{}, sessionlive.Live},
		{"unknown provider", sessionlive.Link{Provider: ns("some-idp"), SessionID: ns("x")}, sessionlive.Revoked},
		{"provider without id", sessionlive.Link{Provider: ns(sessionlive.BetterAuthProvider)}, sessionlive.Revoked},
		{"id without provider", sessionlive.Link{SessionID: ns("x")}, sessionlive.Revoked},
		{"blank id", sessionlive.Link{Provider: ns(sessionlive.BetterAuthProvider), SessionID: ns("  ")}, sessionlive.Revoked},
	}
	for _, c := range cases {
		got, err := sessionlive.Check(context.Background(), rawDB, c.link)
		if err != nil || got != c.want {
			t.Errorf("%s: got (%v, %v), want (%v, nil)", c.name, got, err, c.want)
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestLinkOf(t *testing.T) {
	p, id := "better-auth", "s1"
	if !sessionlive.LinkOf(nil, nil).Legacy() {
		t.Fatal("nil/nil must be legacy")
	}
	for _, l := range []sessionlive.Link{sessionlive.LinkOf(&p, nil), sessionlive.LinkOf(nil, &id), sessionlive.LinkOf(&p, &id)} {
		if l.Legacy() {
			t.Fatalf("%+v must not be legacy", l)
		}
	}
}

func TestCheck_StoreErrorFailsClosed(t *testing.T) {
	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	mock.ExpectQuery(`FROM identity\.session`).WillReturnError(errors.New("connection reset"))
	got, err := sessionlive.Check(context.Background(), rawDB,
		sessionlive.Link{Provider: ns(sessionlive.BetterAuthProvider), SessionID: ns("s1")})
	if got != sessionlive.Revoked || !errors.Is(err, sessionlive.ErrUnavailable) {
		t.Fatalf("got (%v, %v), want (Revoked, ErrUnavailable)", got, err)
	}
	if sessionlive.IsRejected(err) {
		t.Fatal("a store error must not read as a caller rejection")
	}
}

func TestLookup_NilDBFailsClosed(t *testing.T) {
	if _, err := sessionlive.Lookup(context.Background(), nil, "tok"); !errors.Is(err, sessionlive.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

func TestLookup_Platform(t *testing.T) {
	e := sessionlivetest.NewPlatform(t)
	ctx := context.Background()

	t.Run("live linked session", func(t *testing.T) {
		_, token := e.LinkedSession(t, time.Hour)
		s, err := sessionlive.Lookup(ctx, e.DB, token)
		if err != nil || s.UserID != e.UserID.String() {
			t.Fatalf("got (%+v, %v)", s, err)
		}
	})
	t.Run("signed out", func(t *testing.T) {
		id, token := e.LinkedSession(t, time.Hour)
		e.RevokeIdentitySession(t, id)
		if _, err := sessionlive.Lookup(ctx, e.DB, token); !errors.Is(err, sessionlive.ErrRevoked) {
			t.Fatalf("err = %v, want ErrRevoked", err)
		}
	})
	t.Run("identity session expired", func(t *testing.T) {
		id, token := e.LinkedSession(t, time.Hour)
		e.ExpireIdentitySession(t, id)
		if _, err := sessionlive.Lookup(ctx, e.DB, token); !errors.Is(err, sessionlive.ErrRevoked) {
			t.Fatalf("err = %v, want ErrRevoked", err)
		}
	})
	t.Run("legacy row", func(t *testing.T) {
		token := e.LegacySession(t)
		if _, err := sessionlive.Lookup(ctx, e.DB, token); err != nil {
			t.Fatalf("legacy row rejected: %v", err)
		}
	})
	t.Run("unknown token", func(t *testing.T) {
		if _, err := sessionlive.Lookup(ctx, e.DB, "nope"); !errors.Is(err, sessionlive.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})
	t.Run("mirror expired", func(t *testing.T) {
		_, token := e.LinkedSession(t, time.Hour)
		e.Exec(t, `UPDATE everstack.sessions SET expires_at = now() - interval '1 second' WHERE token = $1`, token)
		if _, err := sessionlive.Lookup(ctx, e.DB, token); !errors.Is(err, sessionlive.ErrExpired) {
			t.Fatalf("err = %v, want ErrExpired", err)
		}
	})
	t.Run("identity unreachable fails closed", func(t *testing.T) {
		_, token := e.LinkedSession(t, time.Hour)
		restore := e.BreakIdentity(t)
		defer restore()
		_, err := sessionlive.Lookup(ctx, e.DB, token)
		if !errors.Is(err, sessionlive.ErrUnavailable) {
			t.Fatalf("err = %v, want ErrUnavailable", err)
		}
	})
	t.Run("half or blank links are rejected by the schema", func(t *testing.T) {
		// sessions_identity_pair originally let a half pair through (its
		// second branch was NULL with one side NULL, and a NULL CHECK
		// passes). The tightened constraint rejects every such shape.
		for _, c := range []struct{ provider, id any }{
			{"better-auth", nil},
			{nil, "ba_sess_x"},
			{"  ", "ba_sess_x"},
			{"better-auth", "  "},
		} {
			_, err := e.DB.Exec(`INSERT INTO everstack.sessions (user_id, token, expires_at, identity_provider, identity_session_id)
			                     VALUES ($1, $2, now() + interval '1 hour', $3, $4)`,
				e.UserID, "bad_"+uuid.NewString(), c.provider, c.id)
			if err == nil {
				t.Errorf("provider=%v id=%v: insert accepted, want sessions_identity_pair violation", c.provider, c.id)
			}
		}
		var validated bool
		if err := e.DB.Get(&validated, `SELECT convalidated FROM pg_constraint
		                                WHERE conrelid = 'everstack.sessions'::regclass AND conname = 'sessions_identity_pair'`); err != nil || !validated {
			t.Fatalf("sessions_identity_pair validated=%v err=%v", validated, err)
		}
	})
}

func TestLookup_CEDatabase(t *testing.T) {
	e := sessionlivetest.NewCE(t)
	token := e.LegacySession(t)
	s, err := sessionlive.Lookup(context.Background(), e.DB, token)
	if err != nil || s.UserID != e.UserID.String() {
		t.Fatalf("CE legacy session: got (%+v, %v)", s, err)
	}
}

func TestLookup_InconsistentPlatformFailsClosed(t *testing.T) {
	// identity.session exists but everstack.sessions lacks the link columns:
	// treating rows as legacy here would fail open, so it is an error.
	e := sessionlivetest.NewCE(t)
	token := e.LegacySession(t)
	e.Exec(t, `CREATE SCHEMA identity`)
	e.Exec(t, `CREATE TABLE identity.session (id text PRIMARY KEY, "expiresAt" timestamptz NOT NULL)`)
	if _, err := sessionlive.Lookup(context.Background(), e.DB, token); !errors.Is(err, sessionlive.ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable", err)
	}
}

// The strict sessions_identity_pair migrations round-trip: down restores the
// original definition (a half pair is accepted again), up re-applies the
// strict one.
func TestIdentityPairStrictMigrationsRoundTrip(t *testing.T) {
	e := sessionlivetest.NewPlatform(t)
	dir := "../../../services/cloud/internal/database/migrations/postgres/"
	run := func(path string) {
		t.Helper()
		b, err := os.ReadFile(dir + path)
		if err != nil {
			t.Fatal(err)
		}
		e.Exec(t, string(b))
	}
	half := func() error {
		_, err := e.DB.Exec(`INSERT INTO everstack.sessions (user_id, token, expires_at, identity_provider)
		                     VALUES ($1, $2, now() + interval '1 hour', 'better-auth')`, e.UserID, "half_"+uuid.NewString())
		return err
	}
	run("identity_pair_strict_validate_20261004175552/down.sql")
	run("identity_pair_strict_20261004175551/down.sql")
	if err := half(); err != nil {
		t.Fatalf("after down, the original constraint should accept a half pair: %v", err)
	}
	e.Exec(t, `DELETE FROM everstack.sessions WHERE identity_session_id IS NULL AND identity_provider IS NOT NULL`)
	run("identity_pair_strict_20261004175551/up.sql")
	run("identity_pair_strict_validate_20261004175552/up.sql")
	if err := half(); err == nil {
		t.Fatal("after up, a half pair must be rejected")
	}
}
