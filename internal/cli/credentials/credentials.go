package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/cli/oauthflow"
)

const (
	credDir  = ".config/everstack"
	credFile = "credentials"
)

// Token holds the stored auth credential for a named context.
type Token struct {
	// AccessToken is a Bearer JWT (from device-auth) or empty when using API key auth.
	AccessToken string `json:"access_token,omitempty"`
	// RefreshToken is an opaque, rotating OAuth credential.
	RefreshToken string `json:"refresh_token,omitempty"`
	// ExpiresAt is the access-token expiry. Zero preserves legacy device tokens.
	ExpiresAt time.Time `json:"expires_at,omitempty"`
	// APIKey is a raw API key (sent as x-evs-api-key). Only set when using --api-key login path.
	APIKey string `json:"api_key,omitempty"`
	// OrgID and UserID are cached from the exchange response for display purposes.
	OrgID   string `json:"org_id,omitempty"`
	OrgSlug string `json:"org_slug,omitempty"`
	UserID  string `json:"user_id,omitempty"`
	Email   string `json:"email,omitempty"`
}

const refreshLeeway = time.Minute

var credentialProcessLock sync.Mutex

// Source loads and refreshes a stored OAuth access token.
type Source struct {
	contextName string
	apiURL      string
	httpClient  *http.Client
}

// NewSource creates a refreshing token source for a named CLI context.
func NewSource(contextName, apiURL string, httpClient *http.Client) *Source {
	return &Source{
		contextName: contextName,
		apiURL:      apiURL,
		httpClient:  httpClient,
	}
}

// AccessToken returns a current bearer token. OAuth credentials are refreshed
// while holding a process and filesystem lock, then re-read after the lock is
// acquired so concurrent CLI processes cannot rotate the same token twice.
func (s *Source) AccessToken(ctx context.Context) (string, error) {
	credentialProcessLock.Lock()
	defer credentialProcessLock.Unlock()

	unlock, err := lockStore(ctx)
	if err != nil {
		return "", fmt.Errorf("lock credentials: %w", err)
	}
	defer unlock()

	token, err := Load(s.contextName)
	if err != nil {
		return "", err
	}
	if token.AccessToken == "" {
		return "", errors.New("stored credential has no access token")
	}
	if token.RefreshToken == "" || token.ExpiresAt.IsZero() ||
		time.Now().UTC().Add(refreshLeeway).Before(token.ExpiresAt) {
		return token.AccessToken, nil
	}

	refreshed, err := oauthflow.Refresh(ctx, oauthflow.RefreshOptions{
		APIURL:       s.apiURL,
		RefreshToken: token.RefreshToken,
		HTTPClient:   s.httpClient,
	})
	if err != nil {
		return "", fmt.Errorf("refresh OAuth credentials: %w; run `evs login` again", err)
	}
	token.AccessToken = refreshed.AccessToken
	token.RefreshToken = refreshed.RefreshToken
	token.ExpiresAt = refreshed.ExpiresAt
	if err := saveUnlocked(s.contextName, token); err != nil {
		return "", fmt.Errorf("save refreshed credentials: %w", err)
	}
	return token.AccessToken, nil
}

// IsEmpty returns true when the token carries no credential.
func (t *Token) IsEmpty() bool {
	return t.AccessToken == "" && t.APIKey == ""
}

type store struct {
	Contexts map[string]Token `json:"contexts"`
}

// Load reads the credential for the given context name. Returns an empty Token if not found.
func Load(contextName string) (Token, error) {
	s, err := readStore()
	if err != nil {
		return Token{}, err
	}
	return s.Contexts[contextName], nil
}

// Save writes the token for the given context name, atomically.
func Save(contextName string, t Token) error {
	credentialProcessLock.Lock()
	defer credentialProcessLock.Unlock()
	unlock, err := lockStore(context.Background())
	if err != nil {
		return fmt.Errorf("lock credentials: %w", err)
	}
	defer unlock()
	return saveUnlocked(contextName, t)
}

func saveUnlocked(contextName string, t Token) error {
	s, err := readStore()
	if err != nil {
		return err
	}
	if s.Contexts == nil {
		s.Contexts = map[string]Token{}
	}
	s.Contexts[contextName] = t
	return writeStore(s)
}

// Delete removes the token for the given context name.
func Delete(contextName string) error {
	credentialProcessLock.Lock()
	defer credentialProcessLock.Unlock()
	unlock, err := lockStore(context.Background())
	if err != nil {
		return fmt.Errorf("lock credentials: %w", err)
	}
	defer unlock()

	s, err := readStore()
	if err != nil {
		return err
	}
	delete(s.Contexts, contextName)
	return writeStore(s)
}

func lockStore(ctx context.Context) (func(), error) {
	p, err := path()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, fmt.Errorf("create credentials dir: %w", err)
	}
	return lockCredentialsFile(ctx, p+".lock")
}

func path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, credDir, credFile), nil
}

func readStore() (store, error) {
	p, err := path()
	if err != nil {
		return store{}, err
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		return store{Contexts: map[string]Token{}}, nil
	}
	if err != nil {
		return store{}, fmt.Errorf("read credentials: %w", err)
	}
	var s store
	if err := json.Unmarshal(data, &s); err != nil {
		return store{}, fmt.Errorf("parse credentials: %w", err)
	}
	if s.Contexts == nil {
		s.Contexts = map[string]Token{}
	}
	return s, nil
}

func writeStore(s store) error {
	p, err := path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return fmt.Errorf("create credentials dir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal credentials: %w", err)
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write credentials: %w", err)
	}
	return os.Rename(tmp, p)
}
