package systemone

import (
	"github.com/everstacklabs/everstack/internal/providers/factory"
	"testing"
)

func TestFactoryKeyAndEndpointBoundaries(t *testing.T) {
	for _, name := range []string{"typesafe", "kev", "laya"} {
		f, _ := factory.Default.Get(name)
		if _, err := f(factory.AggregatedInput{}); err == nil {
			t.Fatalf("%s accepted missing key or endpoint", name)
		}
		if _, err := f(factory.AggregatedInput{BaseURL: "http://localhost:8000/v1", APIKeys: []factory.APIKeyInput{{KeyEncrypted: "secret", IsActive: true, Source: "platform"}}}); err == nil {
			t.Fatalf("%s accepted platform-funded key", name)
		}
	}
	for _, name := range []string{"kev", "laya"} {
		f, _ := factory.Default.Get(name)
		p, err := f(factory.AggregatedInput{BaseURL: "http://localhost:8000/v1", Models: []string{"enabled"}})
		if err != nil {
			t.Fatal(err)
		}
		if !p.SupportsModel("enabled") || p.SupportsModel("disabled") {
			t.Fatal("model allowlist ignored")
		}
	}
}

// Decision calls (Decide) do not pass the billing seam and have no wallet
// price, so a platform-owned configuration (organization_id IS NULL on a
// managed gateway) must never build a decision provider, whatever its keys
// are labelled ("config" in practice, never "platform") and including the
// legacy single-key path.
func TestFactoryRefusesPlatformProvenance(t *testing.T) {
	for _, name := range []string{"typesafe", "kev", "laya"} {
		f := newFactory(name)
		cases := []factory.AggregatedInput{
			{BaseURL: "http://localhost:8000/v1", BillingProvenance: "platform",
				APIKeys: []factory.APIKeyInput{{KeyEncrypted: "secret", IsActive: true, Source: "config"}}},
			{BaseURL: "http://localhost:8000/v1", BillingProvenance: "platform", APIKey: "legacy"},
		}
		for i, in := range cases {
			if _, err := f(in); err == nil {
				t.Fatalf("%s case %d: platform-owned config must be refused", name, i)
			}
		}
		if _, err := f(factory.AggregatedInput{BaseURL: "http://localhost:8000/v1", BillingProvenance: "byok", APIKey: "k"}); err != nil {
			t.Fatalf("%s: BYOK must still build: %v", name, err)
		}
	}
}
