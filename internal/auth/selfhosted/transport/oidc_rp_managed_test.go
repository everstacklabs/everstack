package transport

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/everstacklabs/everstack/pkg/oidc"
)

// The OIDC relying-party login had the same flaw #781 removed from the
// exchange and cloud-callback paths: it wrote the signing-in tenant's org
// into the single auth_config row a managed gateway shares, then upserted
// the user as OWNER of whatever org that row named. On a managed gateway it
// must only mark cloud mode, like recordCloudLogin.
func TestOIDCLoginOnManagedGatewayDoesNotWriteSharedTenantState(t *testing.T) {
	env := newExchangeTestEnv(t, true)
	env.expectLogin(uuid.New(), "ext-carol")
	env.mock.ExpectExec(`UPDATE auth_config\s+SET auth_mode = 'cloud'`).
		WillReturnResult(sqlmock.NewResult(0, 0))

	minter := &oidcSessionMinter{h: env.handler}
	req := httptest.NewRequest(http.MethodGet, "https://"+exchangeHostA+"/auth/oidc/callback", nil)
	rec := httptest.NewRecorder()
	err := minter.Mint(rec, req, &oidc.IDClaims{
		RegisteredClaims: jwt.RegisteredClaims{Subject: "ext-carol"},
		Email:            "ext-carol@example.com",
		OrgID:            exchangeOrgA.String(),
		OrgSlug:          "acme",
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	var sessionInserted bool
	for _, s := range env.statements() {
		if strings.Contains(s, "INSERT INTO everstack.sessions") {
			sessionInserted = true
		}
		if regexp.MustCompile(`organization_members|cloud_organization_id|cloud_gateway_url|FROM auth_config`).MatchString(s) {
			t.Errorf("managed-gateway OIDC login touched shared tenant state: %s", s)
		}
	}
	if !sessionInserted {
		t.Fatalf("no session created; statements: %v", env.statements())
	}
}
