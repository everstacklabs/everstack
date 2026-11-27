package transport

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/auth/selfhosted/domain"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/repository"
	"github.com/everstacklabs/everstack/internal/auth/selfhosted/service"
	"github.com/everstacklabs/everstack/internal/enterprise"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

const (
	exchangeHostA = "acme-a1b2c3.eu-gra-1.everstack.ai"
	exchangeHostB = "globex-d4e5f6.eu-gra-1.everstack.ai"
)

var (
	exchangeOrgA = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	exchangeOrgB = uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000002")
)

// exchangeTestEnv signs tokens with the same derivation the cloud uses and
// runs the handler against a sqlmock DB that records every statement.
type exchangeTestEnv struct {
	t       *testing.T
	key     []byte
	handler *SelfHostedAuthHandler
	mock    sqlmock.Sqlmock

	mu   sync.Mutex
	seen map[string]bool
}

func newExchangeTestEnv(t *testing.T, managed bool) *exchangeTestEnv {
	t.Helper()
	master := []byte(strings.Repeat("m", 32))
	t.Setenv("EVS_M2M_SIGNING_KEY", base64.StdEncoding.EncodeToString(master))
	prev := enterprise.ManagedGateway()
	enterprise.SetManagedGateway(managed)
	t.Cleanup(func() { enterprise.SetManagedGateway(prev) })

	env := &exchangeTestEnv{t: t, key: deriveInstanceTokenKey(master), seen: map[string]bool{}}
	matcher := sqlmock.QueryMatcherFunc(func(expected, actual string) error {
		env.mu.Lock()
		env.seen[strings.Join(strings.Fields(actual), " ")] = true
		env.mu.Unlock()
		return sqlmock.QueryMatcherRegexp.Match(expected, actual)
	})
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(matcher))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mock.MatchExpectationsInOrder(false)
	// Never match; keep an unfulfilled Exec and Query expectation around so the
	// recording matcher sees every statement, including unexpected ones.
	mock.ExpectExec(`^__never__$`)
	mock.ExpectQuery(`^__never__$`)
	env.mock = mock

	xdb := sqlx.NewDb(db, "sqlmock")
	cfg := &domain.InternalConfig{Session: domain.SessionConfig{
		CookieName: "everstack_session",
		MaxAge:     time.Hour,
		HTTPOnly:   true,
		SameSite:   "lax",
	}}
	svc := service.NewSelfHostedAuthService(cfg,
		repository.NewUserRepository(xdb),
		repository.NewSessionRepository(xdb),
		nil, nil, nil,
		repository.NewOrganizationRepository(xdb),
		repository.NewAuthConfigRepository(xdb),
	)
	env.handler = NewSelfHostedAuthHandler(cfg, svc, 0, nil)
	return env
}

func (e *exchangeTestEnv) token(instanceURL string, org uuid.UUID, externalID string) string {
	e.t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":         uuid.NewString(),
		"external_id": externalID,
		"email":       externalID + "@example.com",
		"aud":         "everstack-instance-exchange",
		"jti":         uuid.NewString(),
		"iat":         now.Unix(),
		"exp":         now.Add(60 * time.Second).Unix(),
	}
	if org != uuid.Nil {
		claims["organization_id"] = org.String()
		claims["organization_slug"] = "slug-" + org.String()[:4]
	}
	if instanceURL != "" {
		claims["instance_url"] = instanceURL
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(e.key)
	if err != nil {
		e.t.Fatal(err)
	}
	return signed
}

// exchange redeems token on host. scopeOrg, when non-nil, is the instance
// the gateway resolved from the host (cmd/serve cookieSessionAuthMiddleware).
func (e *exchangeTestEnv) exchange(host, token string, scopeOrg *uuid.UUID) *httptest.ResponseRecorder {
	e.t.Helper()
	req := httptest.NewRequest(http.MethodGet, "https://"+host+"/api/auth/exchange?token="+url.QueryEscape(token), nil)
	if scopeOrg != nil {
		req = req.WithContext(contextkeys.WithRequestInstanceScope(context.Background(), contextkeys.RequestInstanceScope{
			InstanceID:       "instance-" + scopeOrg.String()[:4],
			OrganizationID:   scopeOrg.String(),
			OrganizationSlug: "slug-" + scopeOrg.String()[:4],
		}))
	}
	rec := httptest.NewRecorder()
	e.handler.handleTokenExchange(rec, req)
	return rec
}

func (e *exchangeTestEnv) statements() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]string, 0, len(e.seen))
	for s := range e.seen {
		out = append(out, s)
	}
	return out
}

func (e *exchangeTestEnv) expectLogin(userID uuid.UUID, externalID string) {
	now := time.Now()
	e.mock.ExpectQuery(`SELECT \* FROM everstack\.users WHERE external_id = \$1`).
		WithArgs(externalID).
		WillReturnRows(sqlmock.NewRows([]string{"id", "external_id", "email", "name", "avatar_url", "created_at", "updated_at"}).
			AddRow(userID, externalID, externalID+"@example.com", nil, nil, now, now))
	e.mock.ExpectExec(`INSERT INTO everstack\.sessions`).
		WillReturnResult(sqlmock.NewResult(0, 1))
}

func locationHas(rec *httptest.ResponseRecorder, want string) bool {
	return rec.Code == http.StatusFound && strings.Contains(rec.Header().Get("Location"), want)
}

// A token minted for instance A must not open a session on instance B's
// host, and a token whose instance_url names a host the managed gateway does
// not serve (CompleteInstanceConnect accepts arbitrary URLs) must not open a
// session on any host.
func TestTokenExchangeRefusesTokenForAnotherInstance(t *testing.T) {
	env := newExchangeTestEnv(t, true)
	orgB := exchangeOrgB

	cases := []struct {
		name  string
		host  string
		token string
		scope *uuid.UUID
	}{
		{"token for A on B's host", exchangeHostB, env.token("https://"+exchangeHostA, exchangeOrgA, "ext-victim"), &orgB},
		{"token for A, B's org resolved on A's spelling", exchangeHostA, env.token("https://"+exchangeHostA, exchangeOrgA, "ext-victim"), &orgB},
		{"attacker instance_url replayed on its own host", "evil.example", env.token("https://evil.example", exchangeOrgA, "ext-victim"), nil},
		{"attacker instance_url replayed on a tenant host", exchangeHostB, env.token("https://evil.example", exchangeOrgB, "ext-victim"), &orgB},
		{"token without instance_url", exchangeHostB, env.token("", exchangeOrgB, "ext-victim"), &orgB},
		{"token without organization", exchangeHostB, env.token("https://"+exchangeHostB, uuid.Nil, "ext-victim"), &orgB},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := env.exchange(tc.host, tc.token, tc.scope)
			if !locationHas(rec, "error=invalid_token") {
				t.Fatalf("status=%d location=%q, want redirect to invalid_token", rec.Code, rec.Header().Get("Location"))
			}
			for _, c := range rec.Result().Cookies() {
				if c.Name == "everstack_session" && c.Value != "" {
					t.Fatalf("session cookie issued: %+v", c)
				}
			}
		})
	}
	if stmts := env.statements(); len(stmts) != 0 {
		t.Fatalf("refused exchanges touched the database: %v", stmts)
	}
}

// The legitimate flow: token for this host's instance and org opens a
// session for the token's user, and on a managed gateway the only
// auth_config write is the tenant-independent cloud-mode flag. Nothing
// writes the shared row's org/workspace columns or upserts membership.
func TestTokenExchangeCorrectInstanceCreatesSessionWithoutSharedWrites(t *testing.T) {
	env := newExchangeTestEnv(t, true)
	userID := uuid.New()
	env.expectLogin(userID, "ext-alice")
	env.mock.ExpectExec(`UPDATE auth_config\s+SET auth_mode = 'cloud',\s+updated_at = NOW\(\)\s+WHERE auth_mode IS DISTINCT FROM 'cloud'`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	orgA := exchangeOrgA
	// Mixed-case host with a port: same instance after normalization.
	rec := env.exchange("ACME-a1b2c3.eu-gra-1.everstack.ai:443", env.token("https://"+exchangeHostA, exchangeOrgA, "ext-alice"), &orgA)

	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("status=%d location=%q, want redirect to /", rec.Code, rec.Header().Get("Location"))
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == "everstack_session" && c.Value != "" {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("no session cookie issued")
	}

	stmts := env.statements()
	var sessionInserted, modeMarked bool
	for _, s := range stmts {
		switch {
		case strings.Contains(s, "INSERT INTO everstack.sessions"):
			sessionInserted = true
		case strings.Contains(s, "WHERE auth_mode IS DISTINCT FROM 'cloud'"):
			modeMarked = true
		}
		if regexp.MustCompile(`organization_members|cloud_organization_id|cloud_gateway_url|FROM auth_config`).MatchString(s) {
			t.Errorf("managed-gateway login touched shared tenant state: %s", s)
		}
	}
	if !sessionInserted || !modeMarked {
		t.Fatalf("session inserted=%t cloud mode marked=%t; statements: %v", sessionInserted, modeMarked, stmts)
	}
}

// Single use: the second redemption of the same token is refused.
func TestTokenExchangeRefusesReplay(t *testing.T) {
	env := newExchangeTestEnv(t, true)
	env.expectLogin(uuid.New(), "ext-bob")
	env.mock.ExpectExec(`UPDATE auth_config`).WillReturnResult(sqlmock.NewResult(0, 0))
	orgA := exchangeOrgA
	token := env.token("https://"+exchangeHostA, exchangeOrgA, "ext-bob")

	if rec := env.exchange(exchangeHostA, token, &orgA); rec.Header().Get("Location") != "/" {
		t.Fatalf("first redemption: location=%q, want /", rec.Header().Get("Location"))
	}
	if rec := env.exchange(exchangeHostA, token, &orgA); !locationHas(rec, "error=token_replayed") {
		t.Fatalf("replay: status=%d location=%q, want token_replayed", rec.Code, rec.Header().Get("Location"))
	}
}

// A refused token is not consumed: presenting it on the wrong host first
// must not stop its legitimate redemption.
func TestTokenExchangeWrongHostDoesNotBurnToken(t *testing.T) {
	env := newExchangeTestEnv(t, true)
	env.expectLogin(uuid.New(), "ext-carol")
	env.mock.ExpectExec(`UPDATE auth_config`).WillReturnResult(sqlmock.NewResult(0, 0))
	orgA, orgB := exchangeOrgA, exchangeOrgB
	token := env.token("https://"+exchangeHostA, exchangeOrgA, "ext-carol")

	if rec := env.exchange(exchangeHostB, token, &orgB); !locationHas(rec, "error=invalid_token") {
		t.Fatalf("wrong host: location=%q, want invalid_token", rec.Header().Get("Location"))
	}
	if rec := env.exchange(exchangeHostA, token, &orgA); rec.Header().Get("Location") != "/" {
		t.Fatalf("right host after refusal: location=%q, want /", rec.Header().Get("Location"))
	}
}

// Self-hosted gateway connected to the cloud: a single instance, no
// host-resolved scope. The token still has to name this host (directly or
// via the proxy's X-Forwarded-Host), and the org/workspace are recorded on
// the instance's own auth_config row as before.
func TestBindExchangeTokenSelfHosted(t *testing.T) {
	claims := jwt.MapClaims{"instance_url": "https://evs.corp.example", "organization_id": exchangeOrgA.String()}

	req := httptest.NewRequest(http.MethodGet, "http://evs.corp.example/api/auth/exchange", nil)
	if err := bindExchangeTokenToInstance(claims, req, contextkeys.RequestInstanceScope{}, false, false); err != nil {
		t.Fatalf("direct host: %v", err)
	}

	proxied := httptest.NewRequest(http.MethodGet, "http://localhost:8089/api/auth/exchange", nil)
	proxied.Header.Set("X-Forwarded-Host", "evs.corp.example")
	if err := bindExchangeTokenToInstance(claims, proxied, contextkeys.RequestInstanceScope{}, false, false); err != nil {
		t.Fatalf("proxied host: %v", err)
	}

	other := httptest.NewRequest(http.MethodGet, "http://other.example/api/auth/exchange", nil)
	if err := bindExchangeTokenToInstance(claims, other, contextkeys.RequestInstanceScope{}, false, false); err == nil {
		t.Fatal("token for evs.corp.example accepted on other.example")
	}

	// On a managed gateway X-Forwarded-Host is not an alternative to Host:
	// the instance scope was resolved from Host.
	spoof := httptest.NewRequest(http.MethodGet, "https://"+exchangeHostB+"/api/auth/exchange", nil)
	spoof.Header.Set("X-Forwarded-Host", exchangeHostA)
	managedClaims := jwt.MapClaims{"instance_url": "https://" + exchangeHostA, "organization_id": exchangeOrgA.String()}
	scopeB := contextkeys.RequestInstanceScope{InstanceID: "b", OrganizationID: exchangeOrgB.String()}
	if err := bindExchangeTokenToInstance(managedClaims, spoof, scopeB, true, true); err == nil {
		t.Fatal("managed gateway accepted X-Forwarded-Host in place of Host")
	}
}
