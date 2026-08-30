package oidc

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// AuthCode is a pending authorization code plus the identity it was minted for.
type AuthCode struct {
	Code                string
	ClientID            string
	RedirectURI         string
	Scope               string
	Nonce               string
	CodeChallenge       string
	CodeChallengeMethod string

	// Resolved identity (the OP authenticated the user before issuing the code).
	UserID        string
	Email         string
	EmailVerified bool
	Name          string
	OrgID         string
	OrgSlug       string
	InstanceID    string
	AuthTime      time.Time

	ExpiresAt time.Time
}

// CodeStore persists single-use authorization codes.
type CodeStore interface {
	Save(ctx context.Context, c AuthCode) error
	// Consume atomically returns and removes a code. ok=false if missing.
	Consume(ctx context.Context, code string) (AuthCode, bool, error)
}

// MemCodeStore is an in-memory CodeStore (suitable single-process; back with a
// shared store for multi-replica OPs).
type MemCodeStore struct {
	mu sync.Mutex
	m  map[string]AuthCode
}

// NewMemCodeStore creates an empty code store.
func NewMemCodeStore() *MemCodeStore { return &MemCodeStore{m: make(map[string]AuthCode)} }

// Save implements CodeStore.
func (s *MemCodeStore) Save(_ context.Context, c AuthCode) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[c.Code] = c
	return nil
}

// Consume implements CodeStore (single-use + expiry).
func (s *MemCodeStore) Consume(_ context.Context, code string) (AuthCode, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.m[code]
	if !ok {
		return AuthCode{}, false, nil
	}
	delete(s.m, code) // single-use: gone whether or not it's expired
	if time.Now().After(c.ExpiresAt) {
		return AuthCode{}, false, nil
	}
	return c, true, nil
}

type ClientKind string

const (
	ClientKindInstance ClientKind = "instance"
	ClientKindPlatform ClientKind = "platform"
)

// Client is an OAuth client. Managed instances use instance clients; private
// staff applications use platform clients and are authorized independently of
// customer organization membership.
type Client struct {
	ID           string
	Kind         ClientKind
	RedirectURIs []string
	OrgID        string // org that owns the instance this client represents
	InstanceID   string
}

func (c Client) EffectiveKind() ClientKind {
	if c.Kind == "" {
		return ClientKindInstance
	}
	return c.Kind
}

// ValidRedirect reports whether uri exactly matches a registered redirect URI.
// Exact match only — no prefix/substring matching (prevents open redirects).
func (c Client) ValidRedirect(uri string) bool {
	for _, r := range c.RedirectURIs {
		if r == uri {
			return true
		}
	}
	return false
}

// ClientStore resolves clients by id.
type ClientStore interface {
	Get(ctx context.Context, clientID string) (Client, bool, error)
}

// MemClientStore is an in-memory ClientStore.
type MemClientStore struct {
	mu sync.RWMutex
	m  map[string]Client
}

// NewMemClientStore creates an empty client store.
func NewMemClientStore() *MemClientStore { return &MemClientStore{m: make(map[string]Client)} }

// Register adds/updates a client.
func (s *MemClientStore) Register(c Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[c.ID] = c
}

// Get implements ClientStore.
func (s *MemClientStore) Get(_ context.Context, clientID string) (Client, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.m[clientID]
	return c, ok, nil
}

// randToken returns a URL-safe random token of n bytes.
func randToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
