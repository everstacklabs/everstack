package v1

import (
	"context"
	"strings"
	"testing"

	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
)

func TestParsePlatformKeyOrgs(t *testing.T) {
	got := parsePlatformKeyOrgs(" Org-A, org-b ,, ")
	if len(got) != 2 {
		t.Fatalf("parsed %v", got)
	}
	for _, id := range []string{"org-a", "org-b"} {
		if _, ok := got[id]; !ok {
			t.Fatalf("missing %s in %v", id, got)
		}
	}
	if len(parsePlatformKeyOrgs("")) != 0 {
		t.Fatal("empty list must allow nobody")
	}
}

// The tenant's own configuration always wins for a provider it owns; the
// platform pool only fills providers the tenant did not configure, after the
// tenant's rows.
func TestMergePlatformConfigsIsBYOKFirst(t *testing.T) {
	org := "org-1"
	tenantOpenAI := &provider_config.Configuration{ID: "t-openai", OrganizationID: &org, ProviderName: "OpenAI"}
	platformOpenAI := &provider_config.Configuration{ID: "p-openai", ProviderName: "openai"}
	platformAnthropic := &provider_config.Configuration{ID: "p-anthropic", ProviderName: "anthropic"}

	merged := mergePlatformConfigs([]*provider_config.Configuration{tenantOpenAI}, []*provider_config.Configuration{platformOpenAI, nil, platformAnthropic})
	if len(merged) != 2 || merged[0] != tenantOpenAI || merged[1].ID != platformAnthropic.ID {
		t.Fatalf("merged = %+v", merged)
	}
	if got := mergePlatformConfigs(nil, nil); len(got) != 0 {
		t.Fatalf("nothing in, nothing out: %v", got)
	}
	if got := mergePlatformConfigs([]*provider_config.Configuration{tenantOpenAI}, nil); len(got) != 1 || got[0] != tenantOpenAI {
		t.Fatalf("no platform rows: %v", got)
	}
}

// Off a shared gateway, or without a meter to bill against, no platform rows
// are ever merged, whatever the allowlist says.
func TestPlatformProviderConfigsClosedWithoutSharedModeOrMeter(t *testing.T) {
	t.Setenv(platformKeyOrgsEnv, "org-1")
	dedicated := &Server{ctx: context.Background()}
	if got, err := dedicated.platformProviderConfigs(context.Background(), &provider_config.Repository{}); err != nil || got != nil {
		t.Fatalf("dedicated gateway: %v %v", got, err)
	}
	shared := &Server{ctx: context.WithValue(context.Background(), contextkeys.SharedGatewayMode, true)}
	if got, err := shared.platformProviderConfigs(contextkeys.WithTenantID(context.Background(), "org-1"), &provider_config.Repository{}); err != nil || got != nil {
		t.Fatalf("shared gateway without billing DB: %v %v", got, err)
	}
	var nilServer *Server
	if got, err := nilServer.platformProviderConfigs(context.Background(), nil); err != nil || got != nil {
		t.Fatalf("nil server: %v %v", got, err)
	}
}

// Alias collision across providers: a tenant serving gpt-4o on its own
// azure_openai key must keep every gpt-4o request on that key even when a
// platform openai row also lists gpt-4o. The bundle builder maps aliases
// last-write-wins and platform rows come last, so the platform row must not
// carry any alias a tenant row configures (active or not, keyed or not).
func TestMergePlatformConfigsNeverTakesATenantAlias(t *testing.T) {
	org := "org-1"
	tenantAzure := &provider_config.Configuration{ID: "t-azure", OrganizationID: &org, ProviderName: "azure_openai",
		IsActive: true, EnabledModels: []string{"GPT-4o"}}
	tenantInactive := &provider_config.Configuration{ID: "t-mistral", OrganizationID: &org, ProviderName: "mistral",
		IsActive: false, EnabledModels: []string{"mistral-large"}}
	platformOpenAI := &provider_config.Configuration{ID: "p-openai", ProviderName: "openai", IsActive: true,
		EnabledModels: []string{"gpt-4o", "gpt-4o-mini"}}
	platformMistral := &provider_config.Configuration{ID: "p-mistral-2", ProviderName: "mistral_platform", IsActive: true,
		EnabledModels: []string{"mistral-large"}}

	merged := mergePlatformConfigs([]*provider_config.Configuration{tenantAzure, tenantInactive},
		[]*provider_config.Configuration{platformOpenAI, platformMistral})
	if len(merged) != 3 {
		t.Fatalf("merged = %+v", merged)
	}
	if got := merged[2].EnabledModels; len(got) != 1 || got[0] != "gpt-4o-mini" {
		t.Fatalf("platform openai must lose gpt-4o to the tenant's azure key, got %v", got)
	}
	if len(platformOpenAI.EnabledModels) != 2 {
		t.Fatal("the platform row must not be mutated in place")
	}

	// End to end through the alias map the bundle builder fills: gpt-4o
	// stays on the tenant's provider.
	modelMap := map[string]string{}
	for _, cfg := range merged {
		if !cfg.IsActive {
			continue
		}
		for _, m := range cfg.EnabledModels {
			modelMap[strings.ToLower(m)] = strings.ToLower(cfg.ProviderName)
		}
	}
	if modelMap["gpt-4o"] != "azure_openai" || modelMap["gpt-4o-mini"] != "openai" {
		t.Fatalf("alias map = %v", modelMap)
	}
}
