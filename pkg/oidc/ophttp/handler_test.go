package ophttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/everstacklabs/everstack/pkg/oidc"
)

type stepUpAuthenticatorStub struct{}

func (stepUpAuthenticatorStub) CurrentUser(*http.Request) (CurrentUser, bool) {
	return CurrentUser{UserID: "user-acme"}, true
}

func (stepUpAuthenticatorStub) LoginURL(*http.Request) string {
	return "/login"
}

func (stepUpAuthenticatorStub) StepUpURL(
	*http.Request,
	AccessIdentity,
) string {
	return "/sso?organization=acme"
}

type stepUpAccessStub struct{}

func (stepUpAccessStub) Authorize(
	context.Context,
	CurrentUser,
	oidc.Client,
) (AccessResult, error) {
	return AccessResult{
		Decision: AccessAuthenticationRequired,
		Identity: AccessIdentity{OrgID: "org-acme", OrgSlug: "acme"},
	}, nil
}

func TestAuthorizeRedirectsToOrganizationAuthenticationStepUp(t *testing.T) {
	t.Parallel()

	keys, err := oidc.GenerateKeySet(2048)
	if err != nil {
		t.Fatal(err)
	}
	clients := oidc.NewMemClientStore()
	clients.Register(oidc.Client{
		ID:           "instance-acme",
		RedirectURIs: []string{"https://instance.acme.test/auth/callback"},
		OrgID:        "org-acme",
	})
	provider := oidc.NewProvider(
		oidc.ProviderConfig{Issuer: "https://auth.everstack.test"},
		keys,
		oidc.NewMemCodeStore(),
		clients,
	)
	handler := New(
		provider,
		clients,
		stepUpAuthenticatorStub{},
		stepUpAccessStub{},
	)
	request := httptest.NewRequest(
		http.MethodGet,
		"/oauth/authorize?client_id=instance-acme&redirect_uri="+
			"https%3A%2F%2Finstance.acme.test%2Fauth%2Fcallback",
		nil,
	)
	response := httptest.NewRecorder()

	handler.handleAuthorize(response, request)

	if response.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusFound)
	}
	if location := response.Header().Get("Location"); location !=
		"/sso?organization=acme" {
		t.Fatalf("Location = %q", location)
	}
}
