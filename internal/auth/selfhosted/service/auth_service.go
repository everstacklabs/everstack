package service

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/repository"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/google/uuid"
)

// AuthService handles authentication logic (self-hosted only)
type AuthService struct {
	config      *domain.InternalConfig
	userRepo    *repository.UserRepository
	sessionRepo *repository.SessionRepository
}

// NewAuthService creates a new auth service
func NewAuthService(cfg *domain.InternalConfig, userRepo *repository.UserRepository, sessionRepo *repository.SessionRepository) *AuthService {
	return &AuthService{
		config:      cfg,
		userRepo:    userRepo,
		sessionRepo: sessionRepo,
	}
}

// CreateSession creates a new session for a user
func (s *AuthService) CreateSession(ctx context.Context, userID uuid.UUID, ipAddress, userAgent *string) (*domain.Session, error) {
	session, err := s.sessionRepo.Create(ctx, userID, s.config.Session.MaxAge, ipAddress, userAgent)
	if err != nil {
		return nil, fmt.Errorf("failed to create session: %w", err)
	}
	return session, nil
}

// GetSessionByID retrieves a session by its ID
func (s *AuthService) GetSessionByID(ctx context.Context, sessionID uuid.UUID) (*domain.Session, error) {
	session, err := s.sessionRepo.GetByID(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get session by ID: %w", err)
	}
	return session, nil
}

// GetConfig returns the auth service configuration
func (s *AuthService) GetConfig() *domain.InternalConfig {
	return s.config
}

// ValidateSession validates a session token and returns the session if valid
func (s *AuthService) ValidateSession(ctx context.Context, token string) (*domain.Session, error) {
	tokenPrefix := token
	if len(token) > 8 {
		tokenPrefix = token[:8] + "..."
	}
	logger.Debugf("auth: ValidateSession - looking up token: %s", tokenPrefix)

	session, err := s.sessionRepo.GetByToken(ctx, token)
	if err != nil {
		logger.WithError(err).Debug("auth: ValidateSession - GetByToken error")
		return nil, fmt.Errorf("failed to get session: %w", err)
	}
	if session == nil {
		logger.Debug("auth: ValidateSession - session not found in database")
		return nil, nil
	}
	logger.Debugf("auth: ValidateSession - found session ID=%s, UserID=%s, IsValid=%v",
		session.ID, session.UserID, session.IsValid())
	if !session.IsValid() {
		logger.Debug("auth: ValidateSession - session is expired, deleting")
		_ = s.sessionRepo.DeleteByToken(ctx, token)
		return nil, nil
	}
	return session, nil
}

// GetSessionUser gets the user and their organizations for a session
func (s *AuthService) GetSessionUser(ctx context.Context, token string) (*domain.UserWithOrganizations, error) {
	session, err := s.ValidateSession(ctx, token)
	if err != nil {
		logger.WithError(err).Debug("auth: GetSessionUser - ValidateSession error")
		return nil, err
	}
	if session == nil {
		logger.Debug("auth: GetSessionUser - session is nil (not found or expired)")
		return nil, nil
	}

	user, err := s.userRepo.GetWithOrganizations(ctx, session.UserID)
	if err != nil {
		logger.WithError(err).Debug("auth: GetSessionUser - GetWithOrganizations error")
		return nil, fmt.Errorf("failed to get user: %w", err)
	}
	return user, nil
}

// GetUserWithOrganizations gets a user and their organizations by user ID.
// Used in cloud/tenant mode where the user ID comes from the tenant middleware
// rather than a self-hosted session.
func (s *AuthService) GetUserWithOrganizations(ctx context.Context, userID uuid.UUID) (*domain.UserWithOrganizations, error) {
	return s.userRepo.GetWithOrganizations(ctx, userID)
}

// SignOut invalidates a session
func (s *AuthService) SignOut(ctx context.Context, token string) error {
	return s.sessionRepo.DeleteByToken(ctx, token)
}

// SignOutAll invalidates all sessions for a user
func (s *AuthService) SignOutAll(ctx context.Context, userID uuid.UUID) error {
	return s.sessionRepo.DeleteByUserID(ctx, userID)
}

// CleanupExpiredSessions removes all expired sessions
func (s *AuthService) CleanupExpiredSessions(ctx context.Context) (int64, error) {
	return s.sessionRepo.DeleteExpired(ctx)
}

// GetOrCreateUser gets or creates a user from an external identity profile.
//
// Lookup order:
//  1. external_id (the cloud user id) — returns the linked row if it exists.
//  2. email — falls back here when no external_id match is found. This handles
//     the "user previously registered locally via password (no external_id)
//     and is now signing in via the cloud relay" case. Without this fallback,
//     the Create below trips the email unique constraint and the whole sign-in
//     fails with a 23505 error.
//  3. otherwise create a fresh row.
//
// On the email-fallback path we adopt the cloud's external_id onto the
// existing local row so subsequent sign-ins take the fast path.
func (s *AuthService) GetOrCreateUser(ctx context.Context, externalID, email string, name, avatarURL *string) (*domain.User, bool, error) {
	user, err := s.userRepo.GetByExternalID(ctx, externalID)
	if err != nil {
		return nil, false, fmt.Errorf("failed to check existing user: %w", err)
	}

	if user != nil {
		needsUpdate := false
		if name != nil && (user.Name == nil || *user.Name != *name) {
			user.Name = name
			needsUpdate = true
		}
		if avatarURL != nil && (user.AvatarURL == nil || *user.AvatarURL != *avatarURL) {
			user.AvatarURL = avatarURL
			needsUpdate = true
		}
		if needsUpdate {
			if err := s.userRepo.Update(ctx, user); err != nil {
				return nil, false, fmt.Errorf("failed to update user: %w", err)
			}
		}
		return user, false, nil
	}

	// No external_id match — fall back to email. If a row already exists with
	// this email (e.g. a prior password-only registration on this instance),
	// link the cloud external_id onto it instead of creating a duplicate.
	if email != "" {
		if existing, err := s.userRepo.GetByEmail(ctx, email); err != nil {
			return nil, false, fmt.Errorf("failed to lookup user by email: %w", err)
		} else if existing != nil {
			existing.ExternalID = externalID
			if name != nil && (existing.Name == nil || *existing.Name != *name) {
				existing.Name = name
			}
			if avatarURL != nil && (existing.AvatarURL == nil || *existing.AvatarURL != *avatarURL) {
				existing.AvatarURL = avatarURL
			}
			if err := s.userRepo.Update(ctx, existing); err != nil {
				return nil, false, fmt.Errorf("failed to link external_id to existing user: %w", err)
			}
			return existing, false, nil
		}
	}

	user = &domain.User{
		ID:         uuid.New(),
		ExternalID: externalID,
		Email:      email,
		Name:       name,
		AvatarURL:  avatarURL,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	if err := s.userRepo.Create(ctx, user); err != nil {
		return nil, false, fmt.Errorf("failed to create user: %w", err)
	}

	return user, true, nil
}

// SetSessionCookie sets the session cookie on the response
func (s *AuthService) SetSessionCookie(w http.ResponseWriter, session *domain.Session) {
	s.setSessionCookie(w, nil, session)
}

// SetSessionCookieForRequest sets the session cookie, adjusting secure policy based on request transport.
func (s *AuthService) SetSessionCookieForRequest(w http.ResponseWriter, r *http.Request, session *domain.Session) {
	s.setSessionCookie(w, r, session)
}

func (s *AuthService) setSessionCookie(w http.ResponseWriter, r *http.Request, session *domain.Session) {
	secure := s.config.Session.Secure
	sameSite := parseSameSite(s.config.Session.SameSite)
	if shouldRelaxSecureCookie(r) {
		secure = false
		if sameSite == http.SameSiteNoneMode {
			sameSite = http.SameSiteLaxMode
		}
	}

	cookie := &http.Cookie{
		Name:     s.config.Session.CookieName,
		Value:    session.Token,
		Path:     "/",
		Domain:   sanitizeSessionDomain(s.config.Session.Domain),
		Expires:  session.ExpiresAt,
		HttpOnly: s.config.Session.HTTPOnly,
		Secure:   secure,
		SameSite: sameSite,
	}
	logger.Debugf("auth: Setting session cookie: Name=%s, Domain=%s, Secure=%v, HttpOnly=%v, SameSite=%d, Path=%s, Expires=%v",
		cookie.Name, cookie.Domain, cookie.Secure, cookie.HttpOnly, cookie.SameSite, cookie.Path, cookie.Expires)
	http.SetCookie(w, cookie)

	// Minting a session is the definitive "this browser is signed in here"
	// event, so it always retires the signed-out marker. Doing it here rather
	// than at each call site means a future login path can't forget to, and
	// leave the user stuck bouncing to the cloud with a valid session.
	http.SetCookie(w, s.instanceSignedOutCookie(r, false))
}

// SetInstanceSignedOutMarker records that this browser explicitly signed out of
// this instance, so the instance stops honoring the cloud's parent-domain
// cookie as instance auth. See domain.InstanceSignedOutCookie.
func (s *AuthService) SetInstanceSignedOutMarker(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, s.instanceSignedOutCookie(r, true))
}

// instanceSignedOutCookie builds the marker cookie in set or delete form. It
// mirrors the session cookie's transport policy so it survives the same
// deployments, but is deliberately host-only: Domain is never set, because a
// parent-domain marker would leak one instance's sign-out onto the cloud and
// every sibling instance.
func (s *AuthService) instanceSignedOutCookie(r *http.Request, signedOut bool) *http.Cookie {
	secure := s.config.Session.Secure
	sameSite := parseSameSite(s.config.Session.SameSite)
	if shouldRelaxSecureCookie(r) {
		secure = false
		if sameSite == http.SameSiteNoneMode {
			sameSite = http.SameSiteLaxMode
		}
	}

	cookie := &http.Cookie{
		Name:     domain.InstanceSignedOutCookie,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: sameSite,
	}
	if !signedOut {
		cookie.Value = ""
		cookie.Expires = time.Unix(0, 0)
		cookie.MaxAge = -1
		return cookie
	}

	// The marker must outlive the session it replaces: if it lapsed first,
	// the cloud cookie would quietly sign the user back in and the sign-out
	// they performed would expire on a timer.
	ttl := s.config.Session.MaxAge
	if ttl <= 0 {
		ttl = 7 * 24 * time.Hour
	}
	cookie.Value = "1"
	cookie.Expires = time.Now().Add(ttl)
	cookie.MaxAge = int(ttl.Seconds())
	return cookie
}

// ClearSessionCookie clears the session cookie
func (s *AuthService) ClearSessionCookie(w http.ResponseWriter) {
	s.clearSessionCookie(w, nil)
}

// ClearSessionCookieForRequest clears the session cookie with request-aware security policy.
func (s *AuthService) ClearSessionCookieForRequest(w http.ResponseWriter, r *http.Request) {
	s.clearSessionCookie(w, r)
}

func (s *AuthService) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	secure := s.config.Session.Secure
	sameSite := parseSameSite(s.config.Session.SameSite)
	if shouldRelaxSecureCookie(r) {
		secure = false
		if sameSite == http.SameSiteNoneMode {
			sameSite = http.SameSiteLaxMode
		}
	}

	cookie := &http.Cookie{
		Name:     s.config.Session.CookieName,
		Value:    "",
		Path:     "/",
		Domain:   sanitizeSessionDomain(s.config.Session.Domain),
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: s.config.Session.HTTPOnly,
		Secure:   secure,
		SameSite: sameSite,
	}
	http.SetCookie(w, cookie)
}

// GetSessionFromRequest extracts session token from request cookie
func (s *AuthService) GetSessionFromRequest(r *http.Request) string {
	cookie, err := r.Cookie(s.config.Session.CookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// sanitizeSessionDomain — see services/auth/internal/service/auth_service.go
// for rationale. Drops any non-domain characters (e.g. literal "${VAR}"
// templates that the YAML loader didn't expand) so http.SetCookie doesn't
// log "invalid Cookie.Domain ...; dropping domain attribute" on every
// session set.
func sanitizeSessionDomain(d string) string {
	d = strings.TrimSpace(d)
	if d == "" {
		return ""
	}
	for _, c := range d {
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '.' || c == '-':
		default:
			return ""
		}
	}
	return d
}

func parseSameSite(s string) http.SameSite {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "strict":
		return http.SameSiteStrictMode
	case "lax":
		return http.SameSiteLaxMode
	case "none":
		return http.SameSiteNoneMode
	default:
		return http.SameSiteLaxMode
	}
}

func shouldRelaxSecureCookie(r *http.Request) bool {
	if r == nil || requestIsSecure(r) {
		return false
	}

	host := requestHost(r)
	if host == "" {
		return false
	}

	if isLocalHostname(host) {
		return true
	}

	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}

	return addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || isCarrierGradeNAT(addr)
}

func requestIsSecure(r *http.Request) bool {
	if r == nil {
		return false
	}
	if r.TLS != nil {
		return true
	}

	xfp := r.Header.Get("X-Forwarded-Proto")
	if xfp != "" {
		parts := strings.Split(xfp, ",")
		if len(parts) > 0 && strings.EqualFold(strings.TrimSpace(parts[0]), "https") {
			return true
		}
	}
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Ssl")), "on") {
		return true
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Forwarded")), "proto=https") {
		return true
	}

	return false
}

func requestHost(r *http.Request) string {
	if r == nil {
		return ""
	}

	host := strings.TrimSpace(r.Host)
	if host == "" && r.URL != nil {
		host = strings.TrimSpace(r.URL.Hostname())
	}
	if host == "" {
		return ""
	}

	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		return strings.Trim(parsedHost, "[]")
	}
	return strings.Trim(host, "[]")
}

func isLocalHostname(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	return h == "localhost" || h == "::1" || strings.HasSuffix(h, ".localhost") || strings.HasSuffix(h, ".everstack.local")
}

func isCarrierGradeNAT(addr netip.Addr) bool {
	ipv4 := addr.Unmap()
	if !ipv4.Is4() {
		return false
	}
	b := ipv4.As4()
	return b[0] == 100 && b[1] >= 64 && b[1] <= 127
}
