package github

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/telemetry"
	"github.com/golang-jwt/jwt/v5"
)

// App manages GitHub App authentication and API interactions.
// It handles JWT generation, installation token caching, and API calls
// using installation tokens (never URL-embedded).
type App struct {
	appID         int64
	privateKey    *rsa.PrivateKey
	webhookSecret string
	httpClient    *http.Client

	// Installation token cache: installationID → cached token
	tokenMu    sync.RWMutex
	tokenCache map[int64]*cachedToken
}

type cachedToken struct {
	Token     string
	ExpiresAt time.Time
}

// Repository represents a GitHub repository from the API.
type Repository struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	FullName      string `json:"full_name"`
	Description   string `json:"description"`
	Private       bool   `json:"private"`
	DefaultBranch string `json:"default_branch"`
	Language      string `json:"language"`
	Size          int    `json:"size"` // in KB
	HTMLURL       string `json:"html_url"`
	CloneURL      string `json:"clone_url"`
}

// Branch represents a GitHub branch from the API.
type Branch struct {
	Name      string `json:"name"`
	Protected bool   `json:"protected"`
	Commit    struct {
		SHA string `json:"sha"`
	} `json:"commit"`
}

// NewApp creates a new GitHub App client from the given credentials.
func NewApp(appID int64, privateKeyPEM, webhookSecret string) (*App, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, fmt.Errorf("github: failed to decode PEM block from private key")
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS8 format as fallback
		pkcs8Key, pkcs8Err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if pkcs8Err != nil {
			return nil, fmt.Errorf("github: failed to parse private key: %w (also tried PKCS8: %v)", err, pkcs8Err)
		}
		var ok bool
		key, ok = pkcs8Key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("github: PKCS8 key is not RSA")
		}
	}

	return &App{
		appID:         appID,
		privateKey:    key,
		webhookSecret: webhookSecret,
		httpClient: &http.Client{
			Timeout:   30 * time.Second,
			Transport: telemetry.NewIntegrationRoundTripper(nil, "github"),
		},
		tokenCache: make(map[int64]*cachedToken),
	}, nil
}

// WebhookSecret returns the configured webhook secret for HMAC verification.
func (a *App) WebhookSecret() string {
	return a.webhookSecret
}

// generateJWT creates a short-lived JWT for GitHub App authentication.
// JWTs are valid for 10 minutes per GitHub's requirements.
func (a *App) generateJWT() (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-60 * time.Second)), // clock skew buffer
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
		Issuer:    fmt.Sprintf("%d", a.appID),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return token.SignedString(a.privateKey)
}

// GetInstallationToken returns a cached or fresh installation access token.
// Tokens are refreshed 5 minutes before expiry.
func (a *App) GetInstallationToken(ctx context.Context, installationID int64) (string, error) {
	// Check cache first
	a.tokenMu.RLock()
	cached, ok := a.tokenCache[installationID]
	a.tokenMu.RUnlock()

	if ok && time.Now().Add(5*time.Minute).Before(cached.ExpiresAt) {
		return cached.Token, nil
	}

	// Generate new token
	jwtToken, err := a.generateJWT()
	if err != nil {
		return "", fmt.Errorf("github: failed to generate JWT: %w", err)
	}

	url := fmt.Sprintf("https://api.github.com/app/installations/%d/access_tokens", installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, nil)
	if err != nil {
		return "", fmt.Errorf("github: failed to create token request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: failed to request installation token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("github: installation token request failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("github: failed to decode token response: %w", err)
	}

	// Cache the token
	a.tokenMu.Lock()
	a.tokenCache[installationID] = &cachedToken{
		Token:     result.Token,
		ExpiresAt: result.ExpiresAt,
	}
	a.tokenMu.Unlock()

	logger.WithFields("installation_id", installationID).
		Debug("github: refreshed installation token")

	return result.Token, nil
}

// InvalidateToken removes a cached token for the given installation.
func (a *App) InvalidateToken(installationID int64) {
	a.tokenMu.Lock()
	delete(a.tokenCache, installationID)
	a.tokenMu.Unlock()
}

// ListRepositories lists repositories accessible to an installation.
// Supports pagination and search via the query parameter.
func (a *App) ListRepositories(ctx context.Context, installationID int64, page, perPage int, query string) ([]Repository, int, error) {
	token, err := a.GetInstallationToken(ctx, installationID)
	if err != nil {
		return nil, 0, err
	}

	if perPage == 0 {
		perPage = 30
	}
	if page == 0 {
		page = 1
	}

	url := fmt.Sprintf("https://api.github.com/installation/repositories?page=%d&per_page=%d", page, perPage)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, fmt.Errorf("github: failed to create list repos request: %w", err)
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("github: failed to list repos: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, 0, fmt.Errorf("github: list repos failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		TotalCount   int          `json:"total_count"`
		Repositories []Repository `json:"repositories"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, 0, fmt.Errorf("github: failed to decode repos response: %w", err)
	}

	// Client-side filter if query is provided (GitHub installation repos API doesn't support search)
	if query != "" {
		filtered := make([]Repository, 0)
		for _, repo := range result.Repositories {
			if containsInsensitive(repo.Name, query) || containsInsensitive(repo.FullName, query) {
				filtered = append(filtered, repo)
			}
		}
		return filtered, len(filtered), nil
	}

	return result.Repositories, result.TotalCount, nil
}

// ListBranches lists branches for a specific repository accessible to an installation.
func (a *App) ListBranches(ctx context.Context, installationID int64, owner, repo string, page, perPage int) ([]Branch, error) {
	token, err := a.GetInstallationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}

	if perPage == 0 {
		perPage = 30
	}
	if page == 0 {
		page = 1
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/branches?page=%d&per_page=%d", owner, repo, page, perPage)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("github: failed to create list branches request: %w", err)
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: failed to list branches: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("github: list branches failed (status %d): %s", resp.StatusCode, string(body))
	}

	var branches []Branch
	if err := json.NewDecoder(resp.Body).Decode(&branches); err != nil {
		return nil, fmt.Errorf("github: failed to decode branches response: %w", err)
	}

	return branches, nil
}

// GetRepository fetches a single repository's metadata.
func (a *App) GetRepository(ctx context.Context, installationID int64, owner, repo string) (*Repository, error) {
	token, err := a.GetInstallationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("github: failed to create get repo request: %w", err)
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: failed to get repo: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("github: repository %s/%s not found", owner, repo)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("github: get repo failed (status %d): %s", resp.StatusCode, string(body))
	}

	var repository Repository
	if err := json.NewDecoder(resp.Body).Decode(&repository); err != nil {
		return nil, fmt.Errorf("github: failed to decode repo response: %w", err)
	}

	return &repository, nil
}

// GetBranch fetches a single branch's metadata (validates branch exists).
func (a *App) GetBranch(ctx context.Context, installationID int64, owner, repo, branch string) (*Branch, error) {
	token, err := a.GetInstallationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/branches/%s", owner, repo, branch)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("github: failed to create get branch request: %w", err)
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: failed to get branch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("github: branch %q not found in %s/%s", branch, owner, repo)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("github: get branch failed (status %d): %s", resp.StatusCode, string(body))
	}

	var b Branch
	if err := json.NewDecoder(resp.Body).Decode(&b); err != nil {
		return nil, fmt.Errorf("github: failed to decode branch response: %w", err)
	}

	return &b, nil
}

// TreeEntry represents a single node in a GitHub git tree.
type TreeEntry struct {
	Path string `json:"path"`
	Mode string `json:"mode"`
	Type string `json:"type"` // "blob" or "tree"
	Size int64  `json:"size"`
	SHA  string `json:"sha"`
}

// ListTree fetches the full recursive git tree for a repo at the given ref (branch/tag/SHA).
// Returns a flat list of all files and directories.
func (a *App) ListTree(ctx context.Context, installationID int64, owner, repo, ref string) ([]TreeEntry, error) {
	token, err := a.GetInstallationToken(ctx, installationID)
	if err != nil {
		return nil, err
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/git/trees/%s?recursive=1", owner, repo, ref)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("github: failed to create list tree request: %w", err)
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: failed to list tree: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, fmt.Errorf("github: list tree failed (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		SHA       string      `json:"sha"`
		Tree      []TreeEntry `json:"tree"`
		Truncated bool        `json:"truncated"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("github: failed to decode tree response: %w", err)
	}

	return result.Tree, nil
}

// GetFileContent fetches the raw content of a file from a GitHub repository.
// The ref parameter can be a branch name, tag, or commit SHA.
func (a *App) GetFileContent(ctx context.Context, installationID int64, owner, repo, ref, path string) (string, error) {
	token, err := a.GetInstallationToken(ctx, installationID)
	if err != nil {
		return "", err
	}

	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/contents/%s?ref=%s", owner, repo, path, ref)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", fmt.Errorf("github: failed to create file content request: %w", err)
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github.raw+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("github: failed to fetch file content: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", fmt.Errorf("file not found: %s", path)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("github: fetch file failed (status %d): %s", resp.StatusCode, string(body))
	}

	// Limit to 1MB to avoid loading huge files into agent context
	const maxFileSize = 1 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxFileSize+1))
	if err != nil {
		return "", fmt.Errorf("github: failed to read file content: %w", err)
	}
	if len(body) > maxFileSize {
		return "", fmt.Errorf("file too large (>1MB): %s — use sandbox_read_file with a sandbox instead", path)
	}

	return string(body), nil
}

// containsInsensitive checks if s contains substr (case-insensitive).
func containsInsensitive(s, substr string) bool {
	if substr == "" {
		return true
	}
	sl := len(s)
	subl := len(substr)
	if subl > sl {
		return false
	}
	for i := 0; i <= sl-subl; i++ {
		match := true
		for j := 0; j < subl; j++ {
			a := s[i+j]
			b := substr[j]
			if a >= 'A' && a <= 'Z' {
				a += 'a' - 'A'
			}
			if b >= 'A' && b <= 'Z' {
				b += 'a' - 'A'
			}
			if a != b {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
