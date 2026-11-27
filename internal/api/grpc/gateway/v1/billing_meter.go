package v1

import (
	"context"
	"os"
	"strings"
	"sync"

	"github.com/jmoiron/sqlx"

	"github.com/everstacklabs/everstack/internal/domain/provider_config"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	contextkeys "github.com/everstacklabs/everstack/internal/lib/context_keys"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// meterFor returns the wallet meter providers built by this server settle
// through, or nil when metering does not apply. Metering applies only on a
// managed (shared) gateway with a platform DB: on self-hosted, instance-level
// keys are the operator's own and nothing is platform provenance
// (docs/design/prepaid-credits-v2.md 6.1b). The meter is built once from the
// server context (BillingDB owns billing.*, PrimaryDB owns the settle outbox)
// and its outbox replay loop runs for the life of the server.
func (s *Server) meterFor() *billingmeter.Meter {
	if s == nil || s.ctx == nil {
		return nil
	}
	s.billingOnce.Do(func() {
		if !s.isSharedMode() {
			return
		}
		billingDB, _ := s.ctx.Value(contextkeys.BillingDB).(*sqlx.DB)
		if billingDB == nil {
			return
		}
		gatewayDB, _ := s.ctx.Value(contextkeys.PrimaryDB).(*sqlx.DB)
		if gatewayDB == nil {
			logger.Warn("gateway: billing meter has no primary DB; failed settles cannot be parked in the outbox")
		}
		m := billingmeter.New(billingDB, gatewayDB)
		m.StartOutboxReplay(s.ctx)
		s.billingMeter = m
		// Resolve the platform key pool policy now, at bootstrap, so a
		// held-closed allowlist is logged when the gateway starts rather than
		// on the first platform-eligible request.
		_ = platformKeyOrgAllowed("")
	})
	return s.billingMeter
}

// billingProvenance classifies a provider configuration for the billing seam.
// Ownership is the truth: on a shared gateway a configuration nobody owns
// (organization_id IS NULL) is a platform key, anything owned by an
// organization is BYOK, and the model trial's row (is_trial) is trial, which
// is served only through the trial gate. Off a shared gateway nothing is
// platform or trial (a trial row there is never merged either). A nil
// configuration is unowned, so platform.
func (s *Server) billingProvenance(cfg *provider_config.Configuration) string {
	if s == nil || !s.isSharedMode() {
		return ""
	}
	if cfg == nil {
		return billingmeter.ProvenancePlatform
	}
	if cfg.OrganizationID != nil && *cfg.OrganizationID != "" {
		return billingmeter.ProvenanceBYOK
	}
	if cfg.IsTrial {
		return billingmeter.ProvenanceTrial
	}
	return billingmeter.ProvenancePlatform
}

// platformKeyOrgsEnv lists the organizations (ids, comma separated) that may
// be served on platform keys before enforcement ships. Until S5 the platform
// pool is opened only to funded or test organizations named here; S5 removes
// the allowlist (docs/design/prepaid-credits-v2.md section 7, ordering
// constraints).
const platformKeyOrgsEnv = "EVS_PLATFORM_KEY_ORGS"

// platformKeyPoolShadowOverrideEnv, set to exactly "true", lets the allowlist
// open the platform key pool while inference admission is still shadow-only.
// It exists for a deliberate internal test on a funded org; leaving it unset
// is the only safe production value, because a shadow admission never refuses
// an empty wallet.
const platformKeyPoolShadowOverrideEnv = "EVS_PLATFORM_KEY_POOL_ALLOW_SHADOW_ADMISSION"

var (
	platformKeyOrgsOnce sync.Once
	platformKeyOrgs     map[string]struct{}
)

// platformKeyOrgAllowed reports whether orgID is on the platform-key
// allowlist. An empty or unset list means nobody. The list is read once and
// passed through platformKeyPoolPolicy, so it is empty while inference
// admission cannot refuse an empty wallet.
func platformKeyOrgAllowed(orgID string) bool {
	platformKeyOrgsOnce.Do(func() {
		allowed, closed := platformKeyPoolPolicy(
			os.Getenv(platformKeyOrgsEnv),
			billingmeter.InferenceAdmissionEnforced,
			os.Getenv(platformKeyPoolShadowOverrideEnv),
		)
		if closed {
			logger.WithFields(
				"env", platformKeyOrgsEnv,
				"override_env", platformKeyPoolShadowOverrideEnv,
			).Error("gateway: platform key pool held CLOSED: an allowlist is set but inference admission is shadow-only and would serve empty wallets for free; every organization is served on its own keys only")
		}
		platformKeyOrgs = allowed
	})
	_, ok := platformKeyOrgs[strings.ToLower(strings.TrimSpace(orgID))]
	return ok
}

// platformKeyPoolPolicy decides which organizations may be served on platform
// keys. A non-empty allowlist is honoured only when admission refuses an
// empty wallet, or when the explicit shadow override is set; otherwise the
// pool is held closed and closed reports true so the caller can say so.
//
// Closing the pool, rather than refusing to start, is deliberate: a gateway
// that will not boot takes down every BYOK request too, while a closed pool
// only returns the allowlisted orgs to serving on their own keys, which is
// the behaviour before the pool existed.
func platformKeyPoolPolicy(rawAllowlist string, admissionEnforced bool, override string) (allowed map[string]struct{}, closed bool) {
	parsed := parsePlatformKeyOrgs(rawAllowlist)
	if len(parsed) == 0 || admissionEnforced || strings.TrimSpace(strings.ToLower(override)) == "true" {
		return parsed, false
	}
	return map[string]struct{}{}, true
}

func parsePlatformKeyOrgs(raw string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, part := range strings.Split(raw, ",") {
		if id := strings.ToLower(strings.TrimSpace(part)); id != "" {
			out[id] = struct{}{}
		}
	}
	return out
}

// platformProviderConfigs returns the platform key pool to merge into the
// request's tenant bundle, or nil when the request is not entitled to it:
// not a shared gateway, no active meter (nothing to bill against), the
// tenant resolves to no organization, or the organization is not on the
// allowlist. Never errors on a resolution failure: a tenant simply gets its
// own keys, which is the pre-pool behaviour.
func (s *Server) platformProviderConfigs(ctx context.Context, repo *provider_config.Repository) ([]*provider_config.Configuration, error) {
	if s == nil || repo == nil || !s.isSharedMode() {
		return nil, nil
	}
	meter := s.meterFor()
	if !meter.Active() {
		return nil, nil
	}
	org, ok, err := meter.ResolveOrg(ctx)
	if err != nil || !ok || !platformKeyOrgAllowed(org) {
		return nil, nil
	}
	configs, err := repo.ListPlatformOwned(ctx)
	if err != nil {
		return nil, err
	}
	return configs, nil
}

// mergePlatformConfigs appends the platform rows after the tenant's, so the
// tenant's own key always wins. Two rules keep BYOK traffic off the platform
// pool:
//   - a provider the tenant configured is never taken from the platform;
//   - no platform row carries a model alias any tenant row lists (active or
//     not, keyed or not). The bundle builder maps aliases last-write-wins
//     across providers, so without this a platform openai row listing gpt-4o
//     would take every gpt-4o request from a tenant serving it on its own
//     azure_openai key and debit the wallet for it. A tenant alias that
//     cannot be served (inactive row, no key) fails instead of silently
//     moving to a paid key.
//
// Platform rows are copied, never mutated.
func mergePlatformConfigs(tenant, platform []*provider_config.Configuration) []*provider_config.Configuration {
	if len(platform) == 0 {
		return tenant
	}
	own := make(map[string]struct{}, len(tenant))
	aliases := make(map[string]struct{})
	for _, cfg := range tenant {
		if cfg == nil {
			continue
		}
		own[strings.ToLower(cfg.ProviderName)] = struct{}{}
		for _, m := range cfg.EnabledModels {
			aliases[strings.ToLower(strings.TrimSpace(m))] = struct{}{}
		}
	}
	out := append([]*provider_config.Configuration(nil), tenant...)
	for _, cfg := range platform {
		if cfg == nil {
			continue
		}
		if _, has := own[strings.ToLower(cfg.ProviderName)]; has {
			continue
		}
		models := make([]string, 0, len(cfg.EnabledModels))
		for _, m := range cfg.EnabledModels {
			if _, taken := aliases[strings.ToLower(strings.TrimSpace(m))]; !taken {
				models = append(models, m)
			}
		}
		if len(models) == 0 && len(cfg.EnabledModels) > 0 {
			continue
		}
		c := *cfg
		c.EnabledModels = models
		out = append(out, &c)
	}
	return out
}
