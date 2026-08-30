// Package session provides session-based authentication for the cloud portal.
// This allows authenticated users from the cloud dashboard to access specific
// license service endpoints using their session cookie instead of M2M tokens.
package session

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/jmoiron/sqlx"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Session represents a user session from the auth database.
type Session struct {
	ID        string    `db:"id"`
	UserID    string    `db:"user_id"`
	Token     string    `db:"token"`
	ExpiresAt time.Time `db:"expires_at"`
	CreatedAt time.Time `db:"created_at"`
	IPAddress *string   `db:"ip_address"`
	UserAgent *string   `db:"user_agent"`
}

// SessionClaims contains the extracted session information.
type SessionClaims struct {
	SessionID string
	UserID    string
	ExpiresAt time.Time
}

// claimsContextKey is the context key for session claims.
type claimsContextKey struct{}

// authenticatedContextKey is the context key for session authentication flag.
type authenticatedContextKey struct{}

// ClaimsFromContext extracts session claims from the context.
func ClaimsFromContext(ctx context.Context) *SessionClaims {
	claims, _ := ctx.Value(claimsContextKey{}).(*SessionClaims)
	return claims
}

// IsSessionAuthenticated checks if the request was authenticated via session.
// This allows downstream interceptors (like M2M) to skip their auth if session auth succeeded.
func IsSessionAuthenticated(ctx context.Context) bool {
	v, _ := ctx.Value(authenticatedContextKey{}).(bool)
	return v
}

// ContextWithClaims returns a new context with session claims and authenticated flag.
func ContextWithClaims(ctx context.Context, claims *SessionClaims) context.Context {
	ctx = context.WithValue(ctx, claimsContextKey{}, claims)
	ctx = context.WithValue(ctx, authenticatedContextKey{}, true)
	return ctx
}

// InterceptorConfig configures the session auth interceptor.
type InterceptorConfig struct {
	// DB is the database connection to the auth database (sessions table)
	DB *sqlx.DB

	// CookieName is the name of the session cookie (e.g., "everstack_session")
	CookieName string

	// SessionAuthEndpoints are procedures that accept session authentication
	// These endpoints can be accessed with either a valid session cookie OR M2M token
	SessionAuthEndpoints []string

	// FallbackToNext if true, continues to next interceptor if no session found
	// This allows M2M auth to be tried after session auth fails
	FallbackToNext bool
}

// Interceptor is a Connect interceptor that validates session cookies.
type Interceptor struct {
	config *InterceptorConfig
}

// NewInterceptor creates a new session auth interceptor.
func NewInterceptor(config *InterceptorConfig) *Interceptor {
	if config.CookieName == "" {
		config.CookieName = "es_everstack_session"
	}
	return &Interceptor{config: config}
}

// WrapUnary implements connect.Interceptor for unary requests.
func (i *Interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		procedure := req.Spec().Procedure

		// Check if this endpoint accepts session authentication
		if !i.isSessionAuthEndpoint(procedure) {
			return next(ctx, req)
		}

		// Try to extract and validate session
		claims, err := i.validateSession(ctx, req.Header())
		if err != nil {
			if i.config.FallbackToNext {
				// No valid session, let next interceptor handle it (M2M auth)
				logger.Debugf("session: no valid session for %s, falling back to next auth", procedure)
				return next(ctx, req)
			}
			return nil, connect.NewError(connect.CodeUnauthenticated, err)
		}

		// Session is valid, add claims to context
		logger.Debugf("session: authenticated user %s for %s", claims.UserID, procedure)
		ctx = ContextWithClaims(ctx, claims)

		return next(ctx, req)
	}
}

// WrapStreamingClient implements connect.Interceptor (no-op for client-side).
func (i *Interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// WrapStreamingHandler implements connect.Interceptor for streaming requests.
func (i *Interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		procedure := conn.Spec().Procedure

		// Check if this endpoint accepts session authentication
		if !i.isSessionAuthEndpoint(procedure) {
			return next(ctx, conn)
		}

		// Try to extract and validate session
		claims, err := i.validateSession(ctx, conn.RequestHeader())
		if err != nil {
			if i.config.FallbackToNext {
				return next(ctx, conn)
			}
			return connect.NewError(connect.CodeUnauthenticated, err)
		}

		ctx = ContextWithClaims(ctx, claims)
		return next(ctx, conn)
	}
}

// validateSession extracts and validates the session token from cookies.
func (i *Interceptor) validateSession(ctx context.Context, headers interface{ Get(string) string }) (*SessionClaims, error) {
	// Extract session token from Cookie header
	token := i.extractSessionToken(headers)
	if token == "" {
		return nil, errors.New("session: missing session cookie")
	}

	// Look up session in database
	session, err := i.lookupSession(ctx, token)
	if err != nil {
		return nil, err
	}

	return &SessionClaims{
		SessionID: session.ID,
		UserID:    session.UserID,
		ExpiresAt: session.ExpiresAt,
	}, nil
}

// extractSessionToken extracts the session token from the Cookie header.
func (i *Interceptor) extractSessionToken(headers interface{ Get(string) string }) string {
	cookieHeader := headers.Get("Cookie")
	if cookieHeader == "" {
		return ""
	}

	// Parse the Cookie header manually
	// Format: "name1=value1; name2=value2; ..."
	header := http.Header{}
	header.Add("Cookie", cookieHeader)
	request := http.Request{Header: header}

	cookie, err := request.Cookie(i.config.CookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// lookupSession looks up a session in the database.
func (i *Interceptor) lookupSession(ctx context.Context, token string) (*Session, error) {
	if i.config.DB == nil {
		return nil, errors.New("session: database not configured")
	}

	var session Session
	query := `SELECT id, user_id, token, expires_at, created_at, ip_address, user_agent 
	          FROM sessions WHERE token = $1`
	err := i.config.DB.GetContext(ctx, &session, query, token)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("session: invalid or expired session")
	}
	if err != nil {
		logger.WithError(err).Error("session: failed to lookup session")
		return nil, errors.New("session: failed to validate session")
	}

	// Check expiration
	if time.Now().After(session.ExpiresAt) {
		return nil, errors.New("session: session expired")
	}

	return &session, nil
}

// isSessionAuthEndpoint checks if a procedure accepts session authentication.
func (i *Interceptor) isSessionAuthEndpoint(procedure string) bool {
	normalizedProc := normalizeProcedure(procedure)

	for _, ep := range i.config.SessionAuthEndpoints {
		normalizedEp := normalizeProcedure(ep)
		if normalizedEp == normalizedProc {
			return true
		}
	}
	return false
}

// normalizeProcedure removes leading slash from procedure name.
func normalizeProcedure(procedure string) string {
	return strings.TrimPrefix(procedure, "/")
}
