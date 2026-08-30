package middleware

import "testing"

// The anonymous surface is the publish/claim flow, which has to work before a
// caller has any credential. Everything else under hosting must stay
// authenticated — matching the wrong path here would expose another tenant's
// sites to an unauthenticated request.
func TestIsAnonymousHostingPath(t *testing.T) {
	anonymous := []string{
		"/v1/hosting/request-code",
		"/v1/hosting/verify-code",
		"/v1/publish",
		"/v1/publish/abc123",
		"/v1/sites/my-slug/claim",
		"/everstack.hosting.v1.SitesService/PublishSite",
		"/everstack.hosting.v1.SitesService/FinalizeSite",
		"/everstack.hosting.v1.SitesService/ClaimSite",
		"/everstack.hosting.v1.SitesService/RequestCode",
		"/everstack.hosting.v1.SitesService/VerifyCode",
	}
	for _, p := range anonymous {
		if !isAnonymousHostingPath(p) {
			t.Errorf("%s must be reachable without a credential", p)
		}
	}

	authenticated := []string{
		// Site CRUD is owner-scoped. A prefix match on /v1/sites/ would let an
		// unauthenticated caller read or delete anyone's site.
		"/v1/sites",
		"/v1/sites/my-slug",
		"/v1/sites/my-slug/versions",
		"/everstack.hosting.v1.SitesService/ListSites",
		"/everstack.hosting.v1.SitesService/GetSite",
		"/everstack.hosting.v1.SitesService/UpdateSite",
		"/everstack.hosting.v1.SitesService/DeleteSite",
		// Neighbouring surfaces must not be caught by the hosting matcher.
		"/v1/agents",
		"/v1/traces",
		"/mcp",
		"",
	}
	for _, p := range authenticated {
		if isAnonymousHostingPath(p) {
			t.Errorf("%s must NOT bypass authentication", p)
		}
	}
}

// "/claim" is only anonymous as the trailing segment of a site path. A path
// that merely ends in the word elsewhere must not slip through.
func TestClaimSuffixIsNotOverbroad(t *testing.T) {
	for _, p := range []string{
		"/v1/claim",
		"/v1/agents/claim",
		"/v1/sitesomething/x/claim",
	} {
		if isAnonymousHostingPath(p) && p != "/v1/sites/x/claim" {
			t.Errorf("%s should not be treated as the site claim endpoint", p)
		}
	}
}
