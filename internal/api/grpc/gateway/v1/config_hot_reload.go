package v1

import (
	"sort"
	"strings"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
	"github.com/everstacklabs/everstack/internal/lib/logger"
)

// Reload swaps the active gateway configuration and rebuilds routing without server restarts.
// Non-reloadable server settings (ports, TLS, CORS) are intentionally ignored here.
func (s *Server) Reload(newCfg *validator.GatewayConfig, newFeat *validator.FeaturesConfig) {
	if newCfg == nil {
		return
	}
	// Capture previous state for diff logging
	s.mu.RLock()
	prevCfg := s.cfg
	prevFeat := s.feat
	s.mu.RUnlock()

	changes := computeReloadDiff(prevCfg, newCfg, prevFeat, newFeat)

	s.mu.Lock()
	s.cfg = newCfg
	s.feat = newFeat
	s.bootstrapFromConfig()
	s.mu.Unlock()

	entry := logger.WithFields(
		"models_count", len(newCfg.Models),
		"lb_strategy", strings.ToLower(newCfg.LoadBalancer.Strategy),
	)
	// Flatten change fields for our logger helper (key/value variadics)
	for k, v := range changes {
		entry = entry.WithField(k, v)
	}
	entry.Info("gateway config changed and applied successfully -> ")
}

// computeReloadDiff produces a safe, non-sensitive summary of changes between
// old and new gateway/features configs. Secrets are never logged.
func computeReloadDiff(oldCfg, newCfg *validator.GatewayConfig, oldFeat, newFeat *validator.FeaturesConfig) map[string]any {
	out := map[string]any{}
	if oldCfg == nil || newCfg == nil {
		return out
	}

	// Providers
	oldProv := collectProviders(oldCfg)
	newProv := collectProviders(newCfg)
	addedProv, removedProv := diffSets(oldProv, newProv)
	if len(addedProv) > 0 {
		out["providers_added_count"] = len(addedProv)
		out["providers_added"], _ = sampleList(addedProv, 8)
	}
	if len(removedProv) > 0 {
		out["providers_removed_count"] = len(removedProv)
		out["providers_removed"], _ = sampleList(removedProv, 8)
	}

	// Model aliases
	oldAliases := collectAliases(oldCfg)
	newAliases := collectAliases(newCfg)
	addedAlias, removedAlias := diffSets(oldAliases, newAliases)
	if len(addedAlias) > 0 {
		out["models_added_count"] = len(addedAlias)
		out["models_added"], _ = sampleList(addedAlias, 8)
	}
	if len(removedAlias) > 0 {
		out["models_removed_count"] = len(removedAlias)
		out["models_removed"], _ = sampleList(removedAlias, 8)
	}

	// Load balancer knobs
	if !strings.EqualFold(oldCfg.LoadBalancer.Strategy, newCfg.LoadBalancer.Strategy) {
		out["lb_strategy_from"] = strings.ToLower(oldCfg.LoadBalancer.Strategy)
		out["lb_strategy_to"] = strings.ToLower(newCfg.LoadBalancer.Strategy)
	}
	if !strings.EqualFold(oldCfg.LoadBalancer.KeySource, newCfg.LoadBalancer.KeySource) {
		out["lb_key_source_from"] = strings.ToLower(oldCfg.LoadBalancer.KeySource)
		out["lb_key_source_to"] = strings.ToLower(newCfg.LoadBalancer.KeySource)
	}
	changedWeights := diffWeights(oldCfg.LoadBalancer.Weights, newCfg.LoadBalancer.Weights)
	if len(changedWeights) > 0 {
		out["lb_weights_changed_count"] = len(changedWeights)
		out["lb_weights_changed"], _ = sampleList(changedWeights, 8)
	}

	// Feature toggles
	oldStream, oldSSE := featureToggles(oldFeat)
	newStream, newSSE := featureToggles(newFeat)
	if oldStream != newStream {
		out["feature_streaming_from"] = oldStream
		out["feature_streaming_to"] = newStream
	}
	if oldSSE != newSSE {
		out["feature_sse_from"] = oldSSE
		out["feature_sse_to"] = newSSE
	}

	return out
}

func collectProviders(cfg *validator.GatewayConfig) map[string]struct{} {
	providers := make(map[string]struct{})
	if cfg == nil {
		return providers
	}
	for _, mc := range cfg.Models {
		p := strings.ToLower(mc.Provider)
		if p != "" {
			providers[p] = struct{}{}
		}
	}
	return providers
}

func collectAliases(cfg *validator.GatewayConfig) map[string]struct{} {
	aliases := make(map[string]struct{})
	if cfg == nil {
		return aliases
	}
	for _, mc := range cfg.Models {
		for _, m := range mc.Model {
			a := strings.ToLower(m)
			if a != "" {
				aliases[a] = struct{}{}
			}
		}
	}
	return aliases
}

func diffSets(oldSet, newSet map[string]struct{}) (added, removed []string) {
	for k := range newSet {
		if _, ok := oldSet[k]; !ok {
			added = append(added, k)
		}
	}
	for k := range oldSet {
		if _, ok := newSet[k]; !ok {
			removed = append(removed, k)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)
	return
}

func sampleList(in []string, n int) ([]string, int) {
	if len(in) <= n {
		return in, 0
	}
	return in[:n], len(in) - n
}

func diffWeights(oldW, newW map[string]int) []string {
	changed := make([]string, 0)
	keys := make(map[string]struct{})
	for k := range oldW {
		keys[k] = struct{}{}
	}
	for k := range newW {
		keys[k] = struct{}{}
	}
	for k := range keys {
		ov, ook := oldW[k]
		nv, nok := newW[k]
		if !ook || !nok || ov != nv {
			changed = append(changed, strings.ToLower(k))
		}
	}
	sort.Strings(changed)
	return changed
}

func featureToggles(feat *validator.FeaturesConfig) (stream, sse bool) {
	if feat == nil {
		return false, false
	}
	return feat.Gateway.EnableStreaming, feat.Gateway.EnableSSE
}
