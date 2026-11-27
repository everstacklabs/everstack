// Package sessionlive decides whether an everstack.sessions row may still
// authenticate a request.
//
// Better Auth (services/identity, table identity.session) is the source of
// truth for human login sessions. AuthService mirrors each one into an
// everstack.sessions row (cookie es_everstack_session) linked back through
// identity_provider + identity_session_id. The mirror is written for up to
// 30 days and can outlive its source: the identity delete hook is best
// effort, user deletion cascades in SQL without firing it, and revocation or
// password reset remove only the identity row. A mirror is therefore only as
// good as the identity session it points at, and every validator must
// re-check the source on each call (or within MaxCacheTTL of one).
//
// Semantics (shared with billing, PR #820):
//   - Legacy row: identity_provider and identity_session_id are both NULL.
//     It is governed by its own expires_at alone. Only BOTH being NULL
//     counts; a half pair is treated as revoked. (The sessions_identity_pair
//     CHECK forbids half pairs since identity_pair_strict_*, but older or
//     self-managed databases may predate it.)
//   - Linked row: the provider must be one this package can verify
//     ("better-auth") and identity.session must hold an unexpired row with
//     that id. Anything else is revoked.
//   - Any error reaching the session or identity data fails closed with
//     ErrUnavailable, which callers surface as a 5xx, never as a pass.
//
// The package reads identity.session directly. In production the gateway
// (EVS_PLATFORM_DSN), the services binary and the identity service all
// connect to the same platform database with the same role, so a primary
// key lookup on the authoritative row is fresher than any cache and needs no
// network hop.
package sessionlive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// MaxCacheTTL bounds how long any caller may reuse a positive answer from
// this package. A revoked session keeps working for at most this long.
const MaxCacheTTL = 30 * time.Second

// BetterAuthProvider is the identity_provider value AuthService writes on
// rows mirrored from a Better Auth session (services/identity connect.ts).
const BetterAuthProvider = "better-auth"

// Caller-side outcomes: the presented token does not authenticate. Callers
// map these to 401 / Unauthenticated.
var (
	ErrNotFound = errors.New("invalid session")
	ErrExpired  = errors.New("session expired")
	ErrRevoked  = errors.New("session revoked")
)

// ErrUnavailable means the session could not be verified (database error,
// identity data unreachable, inconsistent schema). Callers must deny and
// should answer 5xx / Unavailable so a store outage is not mistaken for a
// sign-out.
var ErrUnavailable = errors.New("session verification unavailable")

// IsRejected reports whether err is a caller-side rejection (as opposed to
// ErrUnavailable or another store failure).
func IsRejected(err error) bool {
	return errors.Is(err, ErrNotFound) || errors.Is(err, ErrExpired) || errors.Is(err, ErrRevoked)
}

// Querier is satisfied by *sql.DB, *sqlx.DB, *sql.Tx and *sqlx.Tx.
type Querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Status is the liveness of a mirror row's upstream identity session.
type Status int

const (
	// Revoked is the zero value so an ignored status never reads as live.
	Revoked Status = iota
	Live
)

// Link is the pair of link columns on an everstack.sessions row.
type Link struct {
	Provider  sql.NullString
	SessionID sql.NullString
}

// LinkOf builds a Link from nullable column values as repositories scan
// them (*string).
func LinkOf(provider, sessionID *string) Link {
	var l Link
	if provider != nil {
		l.Provider = sql.NullString{String: *provider, Valid: true}
	}
	if sessionID != nil {
		l.SessionID = sql.NullString{String: *sessionID, Valid: true}
	}
	return l
}

// Legacy reports whether the row carries no identity link at all. Only
// both columns NULL is legacy; either half alone is an (unverifiable) link.
func (l Link) Legacy() bool { return !l.Provider.Valid && !l.SessionID.Valid }

// Check decides whether the identity session behind link is still live.
// A legacy link is Live (its mirror expiry is the caller's concern). A
// non-nil error is always ErrUnavailable-wrapped and must be treated as deny.
func Check(ctx context.Context, q Querier, link Link) (Status, error) {
	if link.Legacy() {
		return Live, nil
	}
	if !link.Provider.Valid || link.Provider.String != BetterAuthProvider ||
		!link.SessionID.Valid || strings.TrimSpace(link.SessionID.String) == "" {
		// A half pair (possible on databases predating the strict
		// sessions_identity_pair) or
		// a provider this package cannot verify is never trusted on the
		// mirror row alone.
		return Revoked, nil
	}
	if q == nil {
		return Revoked, fmt.Errorf("%w: no database", ErrUnavailable)
	}
	var live bool
	err := q.QueryRowContext(ctx,
		`SELECT "expiresAt" > now() FROM identity.session WHERE id = $1`,
		link.SessionID.String).Scan(&live)
	if errors.Is(err, sql.ErrNoRows) {
		return Revoked, nil
	}
	if err != nil {
		return Revoked, fmt.Errorf("%w: identity session lookup: %v", ErrUnavailable, err)
	}
	if !live {
		return Revoked, nil
	}
	return Live, nil
}

// Session is a validated mirror row.
type Session struct {
	// ID is the everstack.sessions row id.
	ID        string
	UserID    string
	ExpiresAt time.Time
}

// lookupQuery reads the mirror row and its link columns. The link columns
// are read through to_jsonb so the same query works on a self-hosted (CE)
// database whose everstack.sessions predates them: there they read as NULL,
// i.e. legacy. To keep that tolerance from failing open on a platform
// database, the query also reports whether the columns exist and whether
// identity.session exists; a database with identity data but no link
// columns is inconsistent and fails closed.
//
// The column list starts with "SELECT user_id::text, expires_at" and reads
// FROM everstack.sessions so existing query matchers keep identifying it.
const lookupQuery = `SELECT user_id::text, expires_at,
	jsonb_exists(to_jsonb(s), 'identity_provider') AS has_link_columns,
	to_jsonb(s)->>'identity_provider' AS identity_provider,
	to_jsonb(s)->>'identity_session_id' AS identity_session_id,
	to_regclass('identity.session') IS NOT NULL AS has_identity,
	s.id::text AS id
FROM everstack.sessions AS s WHERE s.token = $1`

// LookupColumns is the column list lookupQuery returns, for tests that
// stub it.
var LookupColumns = []string{"user_id", "expires_at", "has_link_columns", "identity_provider", "identity_session_id", "has_identity", "id"}

// Lookup resolves token to a live session: the mirror row must exist, be
// unexpired, and (when linked) point at a live identity session. Errors are
// ErrNotFound, ErrExpired or ErrRevoked for caller-side rejections and
// ErrUnavailable-wrapped for anything else. The token is never included in
// an error.
func Lookup(ctx context.Context, q Querier, token string) (Session, error) {
	if q == nil {
		return Session{}, fmt.Errorf("%w: no database", ErrUnavailable)
	}
	if token == "" {
		return Session{}, ErrNotFound
	}
	var (
		s              Session
		hasLinkColumns bool
		hasIdentity    bool
		link           Link
	)
	err := q.QueryRowContext(ctx, lookupQuery, token).Scan(
		&s.UserID, &s.ExpiresAt, &hasLinkColumns, &link.Provider, &link.SessionID, &hasIdentity, &s.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, fmt.Errorf("%w: session lookup: %v", ErrUnavailable, err)
	}
	if !time.Now().Before(s.ExpiresAt) {
		return Session{}, ErrExpired
	}
	if hasIdentity && !hasLinkColumns {
		return Session{}, fmt.Errorf("%w: identity.session exists but everstack.sessions has no identity link columns", ErrUnavailable)
	}
	status, err := Check(ctx, q, link)
	if err != nil {
		return Session{}, err
	}
	if status != Live {
		return Session{}, ErrRevoked
	}
	return s, nil
}
