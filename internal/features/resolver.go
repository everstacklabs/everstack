// Package features resolves runtime feature availability from three sources,
// evaluated in decreasing precedence:
//
//  1. Edge manifests (Cloudflare KV, polled every 60s) — global kill-switches
//     and per-tenant overrides pushed from the control plane. Highest precedence
//     because they let us disable a feature instantly without a restart.
//
//  2. License entitlements — per-tier permissions baked into the signed license
//     token. A feature can only be enabled if the license tier allows it.
//
//  3. YAML config (`features:` section) — infrastructure capability gates.
//     Even if the manifest and license allow a feature, it can only run if the
//     underlying infrastructure is configured (e.g. memory requires a vector
//     store, sandbox requires a container backend).
//
// A feature is available only when ALL applicable sources allow it. Most
// features have no YAML counterpart — they are available whenever the manifest
// and license say so. Features that require local infrastructure are listed in
// yamlOverlapMap below.
package features

import (
	validator "github.com/everstacklabs/everstack/cmd/config/gateway/validator"
)

// YAMLOverlapChecker checks if a feature's YAML infrastructure toggle is enabled.
// For features that exist in both the manifest (authorization) and YAML config (capability),
// a feature is available only if BOTH allow it.
type YAMLOverlapChecker func(cfg *validator.FeaturesConfig) bool

// yamlOverlapMap maps manifest feature keys to YAML config checkers.
// Most features have no YAML counterpart, so they default to true (always available
// if the manifest says so). Features listed here require BOTH the manifest AND
// the YAML config to allow them.
var yamlOverlapMap = map[string]YAMLOverlapChecker{
	"semantic_cache": func(f *validator.FeaturesConfig) bool {
		return f.Gateway.EnableSemanticCache
	},
	"rate_limiting": func(f *validator.FeaturesConfig) bool {
		// Rate limiting is always infrastructure-available; license controls access
		return true
	},
	"memory": func(f *validator.FeaturesConfig) bool {
		return f.EnableMemory
	},
	"voice": func(f *validator.FeaturesConfig) bool {
		return f.EnableVoice
	},
	"mcp_gateway": func(f *validator.FeaturesConfig) bool {
		return f.McpGateway.Enabled
	},
	"sandbox": func(f *validator.FeaturesConfig) bool {
		return f.Sandbox.Enabled
	},
	"streaming": func(f *validator.FeaturesConfig) bool {
		return f.Gateway.EnableStreaming
	},
}

// ResolveWithYAML takes manifest-resolved features and filters them against YAML
// infrastructure config. A feature is available only if BOTH the manifest allows it
// AND the YAML infrastructure toggle is on (for features that have a YAML counterpart).
func ResolveWithYAML(resolved map[string]*ResolvedFeature, yamlCfg *validator.FeaturesConfig) map[string]*ResolvedFeature {
	if yamlCfg == nil {
		return resolved
	}

	result := make(map[string]*ResolvedFeature, len(resolved))
	for key, feat := range resolved {
		checker, hasYAML := yamlOverlapMap[key]
		if hasYAML && !checker(yamlCfg) {
			// YAML says infrastructure is not configured — skip this feature
			continue
		}
		result[key] = feat
	}
	return result
}
