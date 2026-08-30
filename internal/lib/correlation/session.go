package correlation

import (
	"sync"
	"time"
)

// AuthSession stores correlation information for auth flows
type AuthSession struct {
	CorrelationID string
	DeviceID      string
	CodeVerifier  string
	State         string
	CreatedAt     time.Time
}

// SessionStore provides a simple in-memory store for auth sessions
// with automatic cleanup of expired sessions
type SessionStore struct {
	sessions map[string]*AuthSession
	mu       sync.RWMutex
	// Time after which sessions are considered expired
	sessionTTL time.Duration
}

// NewSessionStore creates a new session store with the specified TTL
func NewSessionStore(sessionTTL time.Duration) *SessionStore {
	store := &SessionStore{
		sessions:   make(map[string]*AuthSession),
		sessionTTL: sessionTTL,
	}

	// Start a cleanup goroutine
	go store.periodicCleanup()

	return store
}

// StoreSession stores a session with the given state as the key
func (s *SessionStore) StoreSession(state, codeVerifier, correlationID, deviceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions[state] = &AuthSession{
		CorrelationID: correlationID,
		DeviceID:      deviceID,
		CodeVerifier:  codeVerifier,
		State:         state,
		CreatedAt:     time.Now(),
	}
}

// GetSession retrieves a session by state
func (s *SessionStore) GetSession(state string) *AuthSession {
	s.mu.RLock()
	defer s.mu.RUnlock()

	session, exists := s.sessions[state]
	if !exists {
		return nil
	}

	// Check if session is expired
	if time.Since(session.CreatedAt) > s.sessionTTL {
		return nil
	}

	return session
}

// GetSessionByCodeVerifier retrieves a session by code verifier (for backward compatibility)
func (s *SessionStore) GetSessionByCodeVerifier(codeVerifier string) *AuthSession {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Iterate through all sessions to find one with matching code verifier
	for _, session := range s.sessions {
		if session.CodeVerifier == codeVerifier {
			// Check if session is expired
			if time.Since(session.CreatedAt) > s.sessionTTL {
				return nil
			}
			return session
		}
	}

	return nil
}

// RemoveSession removes a session by state
func (s *SessionStore) RemoveSession(state string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, state)
}

// RemoveSessionByCodeVerifier removes a session by code verifier (for backward compatibility)
func (s *SessionStore) RemoveSessionByCodeVerifier(codeVerifier string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Find and remove any session with matching code verifier
	for key, session := range s.sessions {
		if session.CodeVerifier == codeVerifier {
			delete(s.sessions, key)
			// Only remove the first matching session
			break
		}
	}
}

// periodicCleanup removes expired sessions periodically
func (s *SessionStore) periodicCleanup() {
	ticker := time.NewTicker(s.sessionTTL / 2)
	defer ticker.Stop()

	for range ticker.C {
		s.cleanup()
	}
}

// cleanup removes all expired sessions
func (s *SessionStore) cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for key, session := range s.sessions {
		if now.Sub(session.CreatedAt) > s.sessionTTL {
			delete(s.sessions, key)
		}
	}
}

// Global session store instance with a 10-minute TTL
var DefaultSessionStore = NewSessionStore(10 * time.Minute)
