package provider_api_keys

import "testing"

func TestBillingSourceDerivesFromOwnership(t *testing.T) {
	tenant := "org-123"
	empty := ""

	cases := []struct {
		name     string
		ownerOrg *string
		label    string
		want     string
	}{
		// Tenant-owned rows are BYOK whatever the label says. The unnamed key
		// written by HandleConfigureProvider lands with source='config'; that
		// label is a key-management marker, not billing provenance.
		{"tenant-owned, labelled config", &tenant, SourcePlatform, SourceBYOK},
		{"tenant-owned, labelled manual", &tenant, SourceBYOK, SourceBYOK},
		{"tenant-owned, unlabelled", &tenant, "", SourceBYOK},
		// Platform-owned rows (organization_id IS NULL: seeded from gateway
		// YAML/env by the operator) are platform whatever their label: a
		// NULL-owned row labelled manual used to read as BYOK and go unbilled.
		{"platform-owned, labelled config", nil, SourcePlatform, SourcePlatform},
		{"platform-owned, labelled manual", nil, SourceBYOK, SourcePlatform},
		{"platform-owned, unlabelled", nil, "", SourcePlatform},
		// An empty owner string is not an owner.
		{"empty owner, labelled config", &empty, SourcePlatform, SourcePlatform},
		{"empty owner, labelled manual", &empty, SourceBYOK, SourcePlatform},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BillingSource(tc.ownerOrg, tc.label); got != tc.want {
				t.Fatalf("BillingSource(%v, %q) = %q, want %q", tc.ownerOrg, tc.label, got, tc.want)
			}
		})
	}
}
