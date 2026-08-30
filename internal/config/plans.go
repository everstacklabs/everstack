package config

import "github.com/everstacklabs/everstack/pkg/plans"

// DefaultPlansPath is the canonical on-disk plans location.
const DefaultPlansPath = plans.DefaultPath

// Legacy aliases keep gateway callers source-compatible while pkg/plans owns
// the canonical schema and parsing.
type PlanFeature = plans.Feature
type PlanUsageLimit = plans.UsageLimit
type PerSeatPricing = plans.PerSeatPricing
type PlanPricing = plans.Pricing
type PlanConfig = plans.Plan
type PlansConfig = plans.PlansConfig
type BrowserRuntimePricing = plans.BrowserRuntimePricing
type SiteDeliveryPricing = plans.SiteDeliveryPricing
type SiteDeliveryAllowance = plans.SiteDeliveryAllowance
type SandboxComputePricing = plans.SandboxComputePricing
type SandboxSize = plans.SandboxSize

// LoadPlansConfig loads an on-disk override or falls back to embedded plans.
func LoadPlansConfig(path string) (*PlansConfig, error) {
	return plans.Load(path)
}

// LoadPlansConfigFromBytes parses plans configuration from byte data.
func LoadPlansConfigFromBytes(data []byte) (*PlansConfig, error) {
	return plans.Parse(data)
}
