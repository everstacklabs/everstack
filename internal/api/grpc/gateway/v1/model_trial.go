package v1

import (
	"context"
	"os"
	"strconv"
	"strings"

	"github.com/everstacklabs/everstack/internal/billingmeter"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/providers/billing"
	catalogsvc "github.com/everstacklabs/everstack/internal/services/catalog"
)

// The model trial on a shared gateway (docs/design/free-trial-onboarding.md
// 3.1, 4.3). The trial row is merged into an organization's bundle, served as
// openai for the one trial model, only when:
//   - the gateway is shared, metering is active and EVS_MODEL_TRIAL is not
//     "off";
//   - an active trial row exists (seeded from EVS_MODEL_TRIAL_OPENAI_API_KEY);
//   - the bundle holds no other openai row (tenant or platform) and no other
//     row lists the trial model, so the tenant's own key always wins;
//   - the organization, resolved from the authenticated context, is
//     eligible (billingmeter.Meter.TrialEligible).
//
// Merging only decides that the provider exists. Every request on it still
// passes the trial gate (internal/providers/billing/trial.go): exhaustion,
// expiry, the daily ceiling and the kill switch are checked per request,
// never at build time.

// modelTrialEnv set to "off" keeps the trial row out of every bundle on this
// gateway (design 4.3, the deploy-time hard off).
const modelTrialEnv = "EVS_MODEL_TRIAL"

// modelTrialKeyEnv holds the trial's OpenAI project key. Unset (the default
// for self-hosted and dev) means no active trial row exists.
const modelTrialKeyEnv = "EVS_MODEL_TRIAL_OPENAI_API_KEY"

// modelTrialProvider is the provider the trial row is served as.
const modelTrialProvider = "openai"

func modelTrialOffByEnv() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv(modelTrialEnv)), "off")
}

// tenantProvenance reports whether a configuration of provenance p belongs
// to the tenant (self-hosted, or BYOK on a shared gateway), as opposed to a
// platform or trial row merged in by the gateway.
func tenantProvenance(p string) bool {
	return p == "" || p == billingmeter.ProvenanceBYOK
}

// seedModelTrialRow writes the trial row from the environment at boot on a
// shared gateway. A pod booting without the key leaves an existing row
// alone (two gateways sharing a database must not flip it on and off with
// their restarts) and says so; the trial is stopped with the settings kill
// switch or EVS_MODEL_TRIAL=off. The key is never logged.
func (s *Server) seedModelTrialRow(ctx context.Context, repo *provider_config.Repository) {
	if s == nil || repo == nil || !s.isSharedMode() {
		return
	}
	key := strings.TrimSpace(os.Getenv(modelTrialKeyEnv))
	if key == "" {
		if rows, err := repo.ListTrialOwned(ctx); err == nil && len(rows) > 0 {
			logger.Warnf("gateway: an active model trial provider row exists but %s is not set on this gateway; the row is left as is", modelTrialKeyEnv)
		}
		return
	}
	if err := repo.UpsertTrial(ctx, key); err != nil {
		logger.Errorf("gateway: could not seed the model trial provider row: %v", err)
		return
	}
	logger.Info("gateway: model trial provider row seeded from the environment")
}

// trialProviderConfig returns the trial row to merge into the request's
// bundle, already shaped as the openai provider for the trial model, or nil.
// configs is the bundle so far (the tenant's rows and any platform rows). It
// never fails the bundle: on any error the organization simply has no trial
// provider.
func (s *Server) trialProviderConfig(ctx context.Context, repo *provider_config.Repository, configs []*provider_config.Configuration) *provider_config.Configuration {
	if s == nil || repo == nil || !s.isSharedMode() || modelTrialOffByEnv() {
		return nil
	}
	if bundleHasProvider(configs, modelTrialProvider) {
		return nil
	}
	meter := s.meterFor()
	if !meter.Active() {
		return nil
	}
	rows, err := repo.ListTrialOwned(ctx)
	if err != nil {
		logger.Warnf("gateway: could not read the model trial provider row: %v", err)
		return nil
	}
	if len(rows) == 0 || rows[0] == nil || !rows[0].IsTrial || rows[0].OrganizationID != nil {
		return nil
	}
	org, ok, err := meter.ResolveOrg(ctx)
	if err != nil || !ok || org == "" {
		return nil
	}
	// Disabled: never merged, so a seeded but disabled trial changes no
	// routing at all. A model the trial cannot shape is never served.
	settings, err := meter.TrialSettings(ctx)
	if err != nil || !settings.Enabled || strings.TrimSpace(settings.Model) == "" {
		return nil
	}
	var cache *catalogsvc.Cache
	if svc := s.catalogServiceFor(ctx); svc != nil {
		cache = svc.GetCache()
	}
	if !billing.TrialModelServable(cache, settings.Model) {
		logger.WithFields("model", settings.Model).Warn("gateway: the model trial's model is served through the Responses API; the trial row is not merged")
		return nil
	}
	if bundleListsModel(configs, settings.Model) {
		return nil
	}
	eligible, err := meter.TrialEligible(ctx, org)
	if err != nil {
		logger.WithFields("org", org, "error", err.Error()).Warn("gateway: could not check model trial eligibility")
		return nil
	}
	if !eligible {
		return nil
	}
	c := *rows[0]
	c.ProviderName = modelTrialProvider
	c.EnabledModels = []string{settings.Model}
	c.CustomSettings = nil
	c.CustomBaseURL = nil
	return &c
}

// mergeTrialConfig appends the trial row after the bundle's rows, unless the
// bundle already serves openai or the trial model (BYOK first, and a tenant
// that added its own key never meets the trial).
func mergeTrialConfig(configs []*provider_config.Configuration, trial *provider_config.Configuration) []*provider_config.Configuration {
	if trial == nil || !trial.IsTrial || len(trial.EnabledModels) != 1 {
		return configs
	}
	if bundleHasProvider(configs, trial.ProviderName) || bundleListsModel(configs, trial.EnabledModels[0]) {
		return configs
	}
	out := append([]*provider_config.Configuration(nil), configs...)
	return append(out, trial)
}

// modelTrialHeader is the x-evs-model-trial value for a response served on
// the model trial ("remaining=<n>"), or ok false when no free request was
// taken for it.
func modelTrialHeader(n *billingmeter.ModelTrialNote) (string, bool) {
	remaining, ok := n.Remaining()
	if !ok {
		return "", false
	}
	return "remaining=" + strconv.Itoa(remaining), true
}

// bundleHasProvider reports whether any row (active or not, keyed or not)
// configures provider.
func bundleHasProvider(configs []*provider_config.Configuration, provider string) bool {
	for _, c := range configs {
		if c != nil && strings.EqualFold(strings.TrimSpace(c.ProviderName), provider) {
			return true
		}
	}
	return false
}

// bundleListsModel reports whether any row lists model as an alias.
func bundleListsModel(configs []*provider_config.Configuration, model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	for _, c := range configs {
		if c == nil {
			continue
		}
		for _, m := range c.EnabledModels {
			if strings.ToLower(strings.TrimSpace(m)) == model {
				return true
			}
		}
	}
	return false
}
