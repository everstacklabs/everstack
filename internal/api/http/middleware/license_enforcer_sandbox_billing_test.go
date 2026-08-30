package middleware

import (
	"testing"

	licpkg "github.com/everstacklabs/everstack/internal/license"
)

func TestClaimsToLicenseStatePreservesSandboxBillingEntitlement(t *testing.T) {
	t.Parallel()

	state := claimsToLicenseState(&licpkg.Claims{
		Tier:                  "free",
		Status:                "active",
		SandboxBillingEnabled: true,
	})
	if !state.SandboxBillingEnabled {
		t.Fatal("sandbox billing entitlement was dropped from verified claims")
	}
}
