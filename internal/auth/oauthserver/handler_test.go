package oauthserver

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type authorizationStoreStub struct {
	grant  AuthorizationGrant
	code   string
	redeem func(context.Context, string, string, string, string, string, IssueAccessToken) (*TokenSet, error)
	rotate func(context.Context, string, string, string, AuthorizeRefresh, IssueAccessToken) (*TokenSet, error)
	revoke func(context.Context, string, string, string) error
}

func (s *authorizationStoreStub) CreateAuthorizationCode(_ context.Context, grant AuthorizationGrant, _ time.Duration) (string, error) {
	s.grant = grant
	return s.code, nil
}

func (s *authorizationStoreStub) RedeemAuthorizationCode(ctx context.Context, code, clientID, redirectURI, verifier, instanceID string, issue IssueAccessToken) (*TokenSet, error) {
	if s.redeem != nil {
		return s.redeem(ctx, code, clientID, redirectURI, verifier, instanceID, issue)
	}
	return nil, errors.New("not implemented")
}

func (s *authorizationStoreStub) RotateRefreshToken(ctx context.Context, refreshToken, clientID, instanceID string, authorize AuthorizeRefresh, issue IssueAccessToken) (*TokenSet, error) {
	if s.rotate != nil {
		return s.rotate(ctx, refreshToken, clientID, instanceID, authorize, issue)
	}
	return nil, errors.New("not implemented")
}

func (s *authorizationStoreStub) RevokeRefreshToken(ctx context.Context, refreshToken, clientID, instanceID string) error {
	if s.revoke != nil {
		return s.revoke(ctx, refreshToken, clientID, instanceID)
	}
	return nil
}

func TestAuthorizeRedirectsAuthenticatedCLIWithOpaqueCode(t *testing.T) {
	t.Parallel()

	store := &authorizationStoreStub{code: "opaque-authorization-code"}
	handler := NewHandler(Config{
		Store: store,
		ResolveIdentity: func(*http.Request) (*Identity, error) {
			return &Identity{
				UserID:           "user-1",
				Email:            "dev@example.com",
				OrganizationID:   "org-1",
				OrganizationSlug: "everstack-dev",
			}, nil
		},
	})

	verifier := strings.Repeat("v", 64)
	challengeSum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeSum[:])
	redirectURI := "http://127.0.0.1:43127/oauth/callback"
	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {CLIClientID},
		"redirect_uri":          {redirectURI},
		"scope":                 {CLIScope},
		"state":                 {"state-123"},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}

	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+query.Encode(), nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusFound, rec.Body.String())
	}
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if got := location.Scheme + "://" + location.Host + location.Path; got != redirectURI {
		t.Fatalf("redirect URI = %q, want %q", got, redirectURI)
	}
	if got := location.Query().Get("code"); got != store.code {
		t.Fatalf("code = %q, want opaque code", got)
	}
	if got := location.Query().Get("state"); got != "state-123" {
		t.Fatalf("state = %q, want original state", got)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}
	if store.grant.CodeChallenge != challenge {
		t.Fatalf("stored code challenge = %q, want request challenge", store.grant.CodeChallenge)
	}
	if store.grant.Identity.OrganizationID != "org-1" {
		t.Fatalf("stored organization = %q, want org-1", store.grant.Identity.OrganizationID)
	}
}

func TestAuthorizeRequiresLoginAndPreservesLocalRequest(t *testing.T) {
	t.Parallel()

	handler := NewHandler(Config{
		Store: &authorizationStoreStub{},
		ResolveIdentity: func(*http.Request) (*Identity, error) {
			return nil, ErrUnauthenticated
		},
	})
	query := validAuthorizationQuery()
	req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+query.Encode(), nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	location, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse Location: %v", err)
	}
	if location.Path != "/login" {
		t.Fatalf("login path = %q, want /login", location.Path)
	}
	returnURL := location.Query().Get("returnUrl")
	if !strings.HasPrefix(returnURL, "/oauth/authorize?") {
		t.Fatalf("returnUrl = %q, want local authorization request", returnURL)
	}
	if strings.Contains(returnURL, "://") {
		t.Fatalf("returnUrl = %q, must remain local", returnURL)
	}
}

func TestAuthorizeRedirectsPostValidationErrorsToLoopback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		resolve   func(*http.Request) (*Identity, error)
		wantError string
	}{
		{
			name: "membership denied",
			resolve: func(*http.Request) (*Identity, error) {
				return nil, ErrAccessDenied
			},
			wantError: "access_denied",
		},
		{
			name: "session backend failure",
			resolve: func(*http.Request) (*Identity, error) {
				return nil, errors.New("database unavailable")
			},
			wantError: "server_error",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			handler := NewHandler(Config{
				Store:           &authorizationStoreStub{},
				ResolveIdentity: tt.resolve,
			})
			req := httptest.NewRequest(
				http.MethodGet,
				"/oauth/authorize?"+validAuthorizationQuery().Encode(),
				nil,
			)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusFound {
				t.Fatalf("status = %d, want 302; body=%s", rec.Code, rec.Body.String())
			}
			location, err := url.Parse(rec.Header().Get("Location"))
			if err != nil {
				t.Fatalf("parse Location: %v", err)
			}
			if location.Host != "127.0.0.1:43127" || location.Path != "/oauth/callback" {
				t.Fatalf("Location = %q, want loopback callback", location.String())
			}
			if got := location.Query().Get("error"); got != tt.wantError {
				t.Fatalf("error = %q, want %q", got, tt.wantError)
			}
			if got := location.Query().Get("state"); got != "state-123" {
				t.Fatalf("state = %q, want state-123", got)
			}
		})
	}
}

func TestAuthorizeRejectsNonLoopbackRedirectAndPlainPKCE(t *testing.T) {
	t.Parallel()

	handler := NewHandler(Config{
		Store: &authorizationStoreStub{},
		ResolveIdentity: func(*http.Request) (*Identity, error) {
			return &Identity{UserID: "user-1", OrganizationID: "org-1"}, nil
		},
	})

	tests := []struct {
		name   string
		mutate func(url.Values)
	}{
		{
			name: "remote redirect",
			mutate: func(q url.Values) {
				q.Set("redirect_uri", "https://attacker.example/oauth/callback")
			},
		},
		{
			name: "plain challenge",
			mutate: func(q url.Values) {
				q.Set("code_challenge_method", "plain")
			},
		},
		{
			name: "unregistered client",
			mutate: func(q url.Values) {
				q.Set("client_id", "other-client")
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			q := validAuthorizationQuery()
			tt.mutate(q)
			req := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+q.Encode(), nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d; Location=%q", rec.Code, http.StatusBadRequest, rec.Header().Get("Location"))
			}
		})
	}
}

func TestTokenEndpointExchangesAuthorizationCodeWithPKCE(t *testing.T) {
	t.Parallel()

	expiresAt := time.Now().Add(15 * time.Minute).Truncate(time.Second)
	store := &authorizationStoreStub{}
	store.redeem = func(_ context.Context, code, clientID, redirectURI, verifier, instanceID string, issue IssueAccessToken) (*TokenSet, error) {
		if code != "opaque-authorization-code" {
			t.Fatalf("code = %q", code)
		}
		if clientID != CLIClientID {
			t.Fatalf("clientID = %q", clientID)
		}
		if redirectURI != "http://127.0.0.1:43127/oauth/callback" {
			t.Fatalf("redirectURI = %q", redirectURI)
		}
		if verifier != strings.Repeat("v", 64) {
			t.Fatalf("verifier = %q", verifier)
		}
		if instanceID != "" {
			t.Fatalf("instanceID = %q, want empty self-hosted binding", instanceID)
		}
		access, err := issue(Identity{
			UserID:           "user-1",
			OrganizationID:   "org-1",
			OrganizationSlug: "everstack-dev",
		}, CLIClientID)
		if err != nil {
			return nil, err
		}
		return &TokenSet{
			AccessToken:  access.Token,
			RefreshToken: "opaque-refresh-token",
			ExpiresAt:    access.ExpiresAt,
			Scope:        CLIScope,
		}, nil
	}
	handler := NewHandler(Config{
		Store: store,
		IssueAccessToken: func(identity Identity, clientID string) (AccessToken, error) {
			if identity.UserID != "user-1" || clientID != CLIClientID {
				t.Fatalf("unexpected token identity: %+v client=%q", identity, clientID)
			}
			return AccessToken{Token: "signed-access-jwt", ExpiresAt: expiresAt}, nil
		},
	})

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {CLIClientID},
		"code":          {"opaque-authorization-code"},
		"redirect_uri":  {"http://127.0.0.1:43127/oauth/callback"},
		"code_verifier": {strings.Repeat("v", 64)},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var got struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		TokenType    string `json:"token_type"`
		ExpiresIn    int64  `json:"expires_in"`
		Scope        string `json:"scope"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	if got.AccessToken != "signed-access-jwt" || got.RefreshToken != "opaque-refresh-token" {
		t.Fatalf("tokens = access:%q refresh:%q", got.AccessToken, got.RefreshToken)
	}
	if got.TokenType != "Bearer" || got.Scope != CLIScope {
		t.Fatalf("metadata = token_type:%q scope:%q", got.TokenType, got.Scope)
	}
	if got.ExpiresIn < 899 || got.ExpiresIn > 900 {
		t.Fatalf("expires_in = %d, want about 900", got.ExpiresIn)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cache)
	}
}

func TestTokenEndpointCollapsesPKCEMismatchToInvalidGrant(t *testing.T) {
	t.Parallel()

	handler := NewHandler(Config{
		Store: &authorizationStoreStub{
			redeem: func(context.Context, string, string, string, string, string, IssueAccessToken) (*TokenSet, error) {
				return nil, ErrInvalidGrant
			},
		},
		IssueAccessToken: func(Identity, string) (AccessToken, error) {
			t.Fatal("issuer must not be called for an invalid grant")
			return AccessToken{}, nil
		},
	})
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {CLIClientID},
		"code":          {"opaque-authorization-code"},
		"redirect_uri":  {"http://127.0.0.1:43127/oauth/callback"},
		"code_verifier": {strings.Repeat("x", 64)},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var got map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if got["error"] != "invalid_grant" {
		t.Fatalf("error = %q, want invalid_grant", got["error"])
	}
}

func TestRefreshEndpointFailsClosedWithoutAuthorizationCallback(t *testing.T) {
	t.Parallel()

	rotateCalls := 0
	handler := NewHandler(Config{
		Store: &authorizationStoreStub{
			rotate: func(context.Context, string, string, string, AuthorizeRefresh, IssueAccessToken) (*TokenSet, error) {
				rotateCalls++
				return &TokenSet{
					AccessToken:  "must-not-be-issued",
					RefreshToken: "must-not-be-issued",
					ExpiresAt:    time.Now().Add(time.Minute),
				}, nil
			},
		},
		IssueAccessToken: func(Identity, string) (AccessToken, error) {
			return AccessToken{Token: "must-not-be-issued", ExpiresAt: time.Now().Add(time.Minute)}, nil
		},
	})
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {CLIClientID},
		"refresh_token": {"opaque-refresh-token"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", rec.Code, rec.Body.String())
	}
	if rotateCalls != 0 {
		t.Fatalf("rotate calls = %d, want 0", rotateCalls)
	}
}

func TestPostgresAuthorizationCodeFlowThroughHTTPStoresOnlyDigests(t *testing.T) {
	t.Parallel()

	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	db := sqlx.NewDb(rawDB, "sqlmock")
	store := NewPostgresStore(db)

	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM oauth_authorization_codes")).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM oauth_refresh_tokens")).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO oauth_authorization_codes")).
		WithArgs(
			sqlmock.AnyArg(),
			sha256DigestArgument{},
			CLIClientID,
			"http://127.0.0.1:43127/oauth/callback",
			CLIScope,
			sqlmock.AnyArg(),
			"user-1",
			"dev@example.com",
			"org-1",
			"everstack-dev",
			"instance-a",
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	expiresAt := time.Now().Add(15 * time.Minute).Truncate(time.Second)
	handler := NewHandler(Config{
		Store: store,
		ResolveIdentity: func(*http.Request) (*Identity, error) {
			return &Identity{
				UserID:           "user-1",
				Email:            "dev@example.com",
				OrganizationID:   "org-1",
				OrganizationSlug: "everstack-dev",
				InstanceID:       "instance-a",
			}, nil
		},
		ResolveInstance: func(*http.Request) (string, error) {
			return "instance-a", nil
		},
		IssueAccessToken: func(Identity, string) (AccessToken, error) {
			return AccessToken{Token: "signed-access-jwt", ExpiresAt: expiresAt}, nil
		},
	})

	authorizeReq := httptest.NewRequest(http.MethodGet, "/oauth/authorize?"+validAuthorizationQuery().Encode(), nil)
	authorizeRec := httptest.NewRecorder()
	handler.ServeHTTP(authorizeRec, authorizeReq)
	location, err := url.Parse(authorizeRec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("parse authorize redirect: %v", err)
	}
	code := location.Query().Get("code")
	if code == "" || strings.HasPrefix(code, "sha256:") {
		t.Fatalf("browser code = %q, want a raw opaque code", code)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("authorize SQL: %v", err)
	}

	codeID := uuid.New()
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("FROM oauth_authorization_codes")).
		WithArgs(tokenDigest(code)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "client_id", "redirect_uri", "scope", "code_challenge",
			"user_id", "user_email", "org_id", "org_slug", "instance_id", "expires_at", "consumed_at",
		}).AddRow(
			codeID, CLIClientID, "http://127.0.0.1:43127/oauth/callback", CLIScope,
			validAuthorizationQuery().Get("code_challenge"),
			"user-1", "dev@example.com", "org-1", "everstack-dev", "instance-a",
			time.Now().Add(5*time.Minute), nil,
		))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO oauth_refresh_tokens")).
		WithArgs(
			sqlmock.AnyArg(),
			sha256DigestArgument{},
			sqlmock.AnyArg(),
			CLIClientID,
			CLIScope,
			"user-1",
			"dev@example.com",
			"org-1",
			"everstack-dev",
			"instance-a",
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE oauth_authorization_codes")).
		WithArgs(codeID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {CLIClientID},
		"code":          {code},
		"redirect_uri":  {"http://127.0.0.1:43127/oauth/callback"},
		"code_verifier": {strings.Repeat("v", 64)},
	}
	tokenReq := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	tokenReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenRec := httptest.NewRecorder()
	handler.ServeHTTP(tokenRec, tokenReq)

	if tokenRec.Code != http.StatusOK {
		t.Fatalf("token status = %d; body=%s", tokenRec.Code, tokenRec.Body.String())
	}
	var tokens struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.NewDecoder(tokenRec.Body).Decode(&tokens); err != nil {
		t.Fatalf("decode token response: %v", err)
	}
	if tokens.AccessToken != "signed-access-jwt" || tokens.RefreshToken == "" ||
		strings.HasPrefix(tokens.RefreshToken, "sha256:") {
		t.Fatalf("unexpected token response: access=%q refresh_present=%t", tokens.AccessToken, tokens.RefreshToken != "")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("exchange SQL: %v", err)
	}
}

func TestPostgresRefreshRotationRevokesFamilyOnReplay(t *testing.T) {
	t.Parallel()

	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	store := NewPostgresStore(sqlx.NewDb(rawDB, "sqlmock"))
	recordID := uuid.New()
	familyID := uuid.New()
	expiresAt := time.Now().Add(30 * 24 * time.Hour)
	accessExpiresAt := time.Now().Add(15 * time.Minute)
	issuerCalls := 0
	handler := NewHandler(Config{
		Store: store,
		ResolveInstance: func(*http.Request) (string, error) {
			return "instance-a", nil
		},
		AuthorizeRefresh: func(_ context.Context, identity Identity) error {
			if identity.InstanceID != "instance-a" || identity.OrganizationID != "org-1" {
				t.Fatalf("refresh identity = %+v", identity)
			}
			return nil
		},
		IssueAccessToken: func(identity Identity, clientID string) (AccessToken, error) {
			issuerCalls++
			if identity.UserID != "user-1" || clientID != CLIClientID {
				t.Fatalf("unexpected refresh identity: %+v client=%q", identity, clientID)
			}
			return AccessToken{Token: "refreshed-access-jwt", ExpiresAt: accessExpiresAt}, nil
		},
	})

	currentRefresh := "current-opaque-refresh-token"
	refreshColumns := []string{
		"id", "family_id", "client_id", "scope", "user_id", "user_email",
		"org_id", "org_slug", "instance_id", "expires_at", "rotated_at", "revoked_at", "replaced_by_hash",
	}
	mock.ExpectQuery(regexp.QuoteMeta("FROM oauth_refresh_tokens")).
		WithArgs(tokenDigest(currentRefresh)).
		WillReturnRows(sqlmock.NewRows(refreshColumns).AddRow(
			recordID, familyID, CLIClientID, CLIScope, "user-1", "dev@example.com",
			"org-1", "everstack-dev", "instance-a", expiresAt, nil, nil, nil,
		))
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock")).
		WithArgs(familyID.String()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(regexp.QuoteMeta("FROM oauth_refresh_tokens")).
		WithArgs(tokenDigest(currentRefresh)).
		WillReturnRows(sqlmock.NewRows(refreshColumns).AddRow(
			recordID, familyID, CLIClientID, CLIScope, "user-1", "dev@example.com",
			"org-1", "everstack-dev", "instance-a", expiresAt, nil, nil, nil,
		))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO oauth_refresh_tokens")).
		WithArgs(
			sqlmock.AnyArg(),
			sha256DigestArgument{},
			familyID,
			CLIClientID,
			CLIScope,
			"user-1",
			"dev@example.com",
			"org-1",
			"everstack-dev",
			"instance-a",
			expiresAt,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE oauth_refresh_tokens")).
		WithArgs(sha256DigestArgument{}, recordID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	first := exchangeRefreshThroughHTTP(t, handler, currentRefresh)
	if first.AccessToken != "refreshed-access-jwt" || first.RefreshToken == "" ||
		first.RefreshToken == currentRefresh {
		t.Fatalf("rotation response = access:%q refresh_changed:%t", first.AccessToken, first.RefreshToken != currentRefresh)
	}

	rotatedAt := time.Now()
	replacementHash := tokenDigest(first.RefreshToken)
	mock.ExpectQuery(regexp.QuoteMeta("FROM oauth_refresh_tokens")).
		WithArgs(tokenDigest(currentRefresh)).
		WillReturnRows(sqlmock.NewRows(refreshColumns).AddRow(
			recordID, familyID, CLIClientID, CLIScope, "user-1", "dev@example.com",
			"org-1", "everstack-dev", "instance-a", expiresAt, rotatedAt, nil, replacementHash,
		))
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock")).
		WithArgs(familyID.String()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE oauth_refresh_tokens")).
		WithArgs(familyID).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {CLIClientID},
		"refresh_token": {currentRefresh},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("replay status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	var replay map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&replay); err != nil {
		t.Fatalf("decode replay response: %v", err)
	}
	if replay["error"] != "invalid_grant" {
		t.Fatalf("replay error = %q, want invalid_grant", replay["error"])
	}
	if issuerCalls != 1 {
		t.Fatalf("issuer calls = %d, want 1; replay must not mint a token", issuerCalls)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("refresh SQL: %v", err)
	}
}

func TestPostgresRefreshRevokesFamilyWhenMembershipIsRemoved(t *testing.T) {
	t.Parallel()

	rawDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New: %v", err)
	}
	t.Cleanup(func() { _ = rawDB.Close() })
	store := NewPostgresStore(sqlx.NewDb(rawDB, "sqlmock"))
	recordID := uuid.New()
	familyID := uuid.New()
	refreshToken := "refresh-for-removed-member"

	mock.ExpectQuery(regexp.QuoteMeta("FROM oauth_refresh_tokens")).
		WithArgs(tokenDigest(refreshToken)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "family_id", "client_id", "scope", "user_id", "user_email",
			"org_id", "org_slug", "instance_id", "expires_at", "rotated_at", "revoked_at", "replaced_by_hash",
		}).AddRow(
			recordID, familyID, CLIClientID, CLIScope, "user-1", "dev@example.com",
			"org-1", "everstack-dev", "instance-a", time.Now().Add(24*time.Hour), nil, nil, nil,
		))
	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock")).
		WithArgs(familyID.String()).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE oauth_refresh_tokens")).
		WithArgs(familyID).
		WillReturnResult(sqlmock.NewResult(0, 2))
	mock.ExpectCommit()

	issuerCalls := 0
	handler := NewHandler(Config{
		Store: store,
		ResolveInstance: func(*http.Request) (string, error) {
			return "instance-a", nil
		},
		AuthorizeRefresh: func(context.Context, Identity) error {
			return ErrAccessDenied
		},
		IssueAccessToken: func(Identity, string) (AccessToken, error) {
			issuerCalls++
			return AccessToken{Token: "must-not-be-issued", ExpiresAt: time.Now().Add(time.Minute)}, nil
		},
	})

	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {CLIClientID},
		"refresh_token": {refreshToken},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	if issuerCalls != 0 {
		t.Fatalf("issuer calls = %d, want 0", issuerCalls)
	}
	var response map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response["error"] != "invalid_grant" {
		t.Fatalf("error = %q, want invalid_grant", response["error"])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("refresh SQL: %v", err)
	}
}

func TestRevokeEndpointRevokesOnlyCurrentInstanceFamily(t *testing.T) {
	t.Parallel()

	var gotToken, gotClientID, gotInstanceID string
	store := &authorizationStoreStub{
		revoke: func(_ context.Context, token, clientID, instanceID string) error {
			gotToken = token
			gotClientID = clientID
			gotInstanceID = instanceID
			return nil
		},
	}
	handler := NewHandler(Config{
		Store: store,
		ResolveInstance: func(*http.Request) (string, error) {
			return "instance-a", nil
		},
	})
	form := url.Values{
		"client_id":       {CLIClientID},
		"token":           {"refresh-to-revoke"},
		"token_type_hint": {"refresh_token"},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/revoke", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if gotToken != "refresh-to-revoke" || gotClientID != CLIClientID || gotInstanceID != "instance-a" {
		t.Fatalf("revoke = token:%q client:%q instance:%q", gotToken, gotClientID, gotInstanceID)
	}
}

type refreshResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
}

func exchangeRefreshThroughHTTP(t *testing.T, handler http.Handler, refreshToken string) refreshResponse {
	t.Helper()
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {CLIClientID},
		"refresh_token": {refreshToken},
	}
	req := httptest.NewRequest(http.MethodPost, "/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var response refreshResponse
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode refresh response: %v", err)
	}
	return response
}

type sha256DigestArgument struct{}

func (sha256DigestArgument) Match(value driver.Value) bool {
	digest, ok := value.(string)
	return ok && strings.HasPrefix(digest, "sha256:") && len(digest) == len("sha256:")+64
}

func validAuthorizationQuery() url.Values {
	sum := sha256.Sum256([]byte(strings.Repeat("v", 64)))
	return url.Values{
		"response_type":         {"code"},
		"client_id":             {CLIClientID},
		"redirect_uri":          {"http://127.0.0.1:43127/oauth/callback"},
		"scope":                 {CLIScope},
		"state":                 {"state-123"},
		"code_challenge":        {base64.RawURLEncoding.EncodeToString(sum[:])},
		"code_challenge_method": {"S256"},
	}
}
