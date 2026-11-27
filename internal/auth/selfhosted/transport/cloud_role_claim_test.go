package transport

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

// The instance exchange token lists the user's organizations with their
// roles; the role used to attach the user is the one for the token's
// organization, and nothing when the token does not say.
func TestCloudOrganizationRoleFromExchangeClaims(t *testing.T) {
	claims := jwt.MapClaims{"orgs": `[{"id":"AAA","role":"owner"},{"id":"bbb","role":"viewer"}]`}
	for org, want := range map[string]string{"bbb": "viewer", "aaa": "owner", "ccc": "", "": ""} {
		if got := cloudOrganizationRole(claims, org); got != want {
			t.Errorf("org %q: role %q, want %q", org, got, want)
		}
	}
	for name, c := range map[string]jwt.MapClaims{
		"no orgs claim": {},
		"malformed":     {"orgs": "not json"},
	} {
		if got := cloudOrganizationRole(c, "bbb"); got != "" {
			t.Errorf("%s: role %q, want none", name, got)
		}
	}
}
