package v1

import (
	"context"
	"testing"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"

	"github.com/everstacklabs/everstack/internal/domain/provider_api_keys"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
)

// The served key's Source label is derived from row ownership when the tenant
// bundle is built, so a customer's own key can never read as platform because
// of how its row was labelled, and a platform row never reads as BYOK.
func TestServedKeyInputsDeriveBillingSourceFromOwnership(t *testing.T) {
	tenant := "org-123"
	tenantCfg := &provider_config.Configuration{ID: "cfg-tenant", OrganizationID: &tenant, ProviderName: "openai"}
	platformCfg := &provider_config.Configuration{ID: "cfg-platform", OrganizationID: nil, ProviderName: "openai"}

	// Exactly the rows HandleConfigureProvider writes: an unnamed key becomes a
	// "Config API Key" with source='config'; a named key is source='manual'.
	unnamedTenantKey := &provider_api_keys.ProviderAPIKey{ID: "k-unnamed", KeyName: "Config API Key", KeyEncrypted: "sk-tenant-1", Weight: 1, IsActive: true, Source: "config"}
	namedTenantKey := &provider_api_keys.ProviderAPIKey{ID: "k-named", KeyName: "prod", KeyEncrypted: "sk-tenant-2", Weight: 2, IsActive: true, Source: "manual"}
	inactiveTenantKey := &provider_api_keys.ProviderAPIKey{ID: "k-off", KeyName: "old", KeyEncrypted: "sk-tenant-3", Weight: 1, IsActive: false, Source: "manual"}
	platformKey := &provider_api_keys.ProviderAPIKey{ID: "k-platform", KeyName: "Config API Key", KeyEncrypted: "sk-platform", Weight: 1, IsActive: true, Source: "config"}

	t.Run("tenant-owned rows are BYOK regardless of label", func(t *testing.T) {
		inputs := servedKeyInputs(tenantCfg, []*provider_api_keys.ProviderAPIKey{unnamedTenantKey, namedTenantKey, inactiveTenantKey})
		if len(inputs) != 2 {
			t.Fatalf("want 2 active inputs, got %d", len(inputs))
		}
		for _, in := range inputs {
			if in.Source != provider_api_keys.SourceBYOK {
				t.Fatalf("key %s: Source = %q, want %q", in.ID, in.Source, provider_api_keys.SourceBYOK)
			}
		}
		// Everything else is carried through unchanged.
		if inputs[0].ID != "k-unnamed" || inputs[0].KeyName != "Config API Key" || inputs[0].KeyEncrypted != "sk-tenant-1" || inputs[0].Weight != 1 || !inputs[0].IsActive {
			t.Fatalf("unnamed key fields not carried through: %+v", inputs[0])
		}
		if inputs[1].ID != "k-named" || inputs[1].Weight != 2 {
			t.Fatalf("named key fields not carried through: %+v", inputs[1])
		}
	})

	t.Run("platform-owned rows keep the platform label", func(t *testing.T) {
		inputs := servedKeyInputs(platformCfg, []*provider_api_keys.ProviderAPIKey{platformKey})
		if len(inputs) != 1 || inputs[0].Source != provider_api_keys.SourcePlatform {
			t.Fatalf("platform key inputs = %+v, want one %q", inputs, provider_api_keys.SourcePlatform)
		}
	})

	t.Run("nil config is treated as platform-owned", func(t *testing.T) {
		inputs := servedKeyInputs(nil, []*provider_api_keys.ProviderAPIKey{platformKey})
		if len(inputs) != 1 || inputs[0].Source != provider_api_keys.SourcePlatform {
			t.Fatalf("nil-config inputs = %+v", inputs)
		}
	})

	// A NULL-owned row labelled "manual" is still the platform's key: the
	// label must never make platform spend read as BYOK (the under-billing
	// gap in the previous BillingSource, which returned the label).
	t.Run("platform-owned rows labelled manual are still platform", func(t *testing.T) {
		manualPlatform := &provider_api_keys.ProviderAPIKey{ID: "k-platform-manual", KeyName: "ops", KeyEncrypted: "sk-platform-2", Weight: 1, IsActive: true, Source: "manual"}
		inputs := servedKeyInputs(platformCfg, []*provider_api_keys.ProviderAPIKey{manualPlatform})
		if len(inputs) != 1 || inputs[0].Source != provider_api_keys.SourcePlatform {
			t.Fatalf("manual-labelled platform key inputs = %+v, want %q", inputs, provider_api_keys.SourcePlatform)
		}
	})
}

// The billing seam classifies a provider by the ownership of its configuration
// and by gateway mode, independent of any key row: a configuration with no
// key rows (the legacy api_key_encrypted path) and a provider with no key
// concept at all (Bedrock) are classified exactly like a multi-key one.
func TestBillingProvenanceFromOwnershipAndMode(t *testing.T) {
	tenant := "org-123"
	shared := &Server{ctx: context.WithValue(context.Background(), contextkeys.SharedGatewayMode, true)}
	dedicated := &Server{ctx: context.Background()}

	if got := shared.billingProvenance(nil); got != billingmeter.ProvenancePlatform {
		t.Fatalf("shared + NULL owner = %q, want platform", got)
	}
	empty := ""
	if got := shared.billingProvenance(&provider_config.Configuration{OrganizationID: &empty}); got != billingmeter.ProvenancePlatform {
		t.Fatalf("shared + empty owner = %q, want platform", got)
	}
	if got := shared.billingProvenance(&provider_config.Configuration{OrganizationID: &tenant}); got != billingmeter.ProvenanceBYOK {
		t.Fatalf("shared + org owner = %q, want byok", got)
	}
	// The model trial's row is trial, from its flag, never platform.
	trialRow := &provider_config.Configuration{ProviderName: provider_config.TrialProviderName, IsTrial: true}
	if got := shared.billingProvenance(trialRow); got != billingmeter.ProvenanceTrial {
		t.Fatalf("shared + trial row = %q, want trial", got)
	}
	// Self-hosted: instance-level keys are the operator's own; nothing is
	// platform and nothing is metered. A trial row there gets no provenance.
	if got := dedicated.billingProvenance(nil); got != "" {
		t.Fatalf("dedicated + NULL owner = %q, want unmetered", got)
	}
	if got := dedicated.billingProvenance(&provider_config.Configuration{OrganizationID: &tenant}); got != "" {
		t.Fatalf("dedicated + org owner = %q, want unmetered", got)
	}
	if got := dedicated.billingProvenance(trialRow); got != "" {
		t.Fatalf("dedicated + trial row = %q, want none", got)
	}
	var nilServer *Server
	if got := nilServer.billingProvenance(nil); got != "" {
		t.Fatalf("nil server = %q", got)
	}
	// No billing DB on the context: the meter is nil (inactive) even on a
	// shared gateway, so a platform-provenance provider is left unwrapped.
	if m := shared.meterFor(); m != nil {
		t.Fatal("shared gateway without BillingDB must have no meter")
	}
	if m := dedicated.meterFor(); m != nil {
		t.Fatal("dedicated gateway must have no meter")
	}
}
