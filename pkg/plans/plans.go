// Package plans owns the canonical plans configuration schema and parsing.
package plans

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"

	licv1 "github.com/everstacklabs/everstack/pkg/grpc/everstack/license/v1"
)

// DefaultPath is the repository and runtime location of the canonical plans file.
const DefaultPath = "pkg/plans/plans.json"

//go:embed plans.json
var embeddedPlansJSON []byte

var (
	embeddedOnce   sync.Once
	embeddedConfig *PlansConfig
	embeddedErr    error
)

// PlansConfig holds all plan definitions and shared pricing configuration.
type PlansConfig struct {
	Plans                 map[string]Plan        `json:"plans"`
	SiteDeliveryPricing   *SiteDeliveryPricing   `json:"site_delivery_pricing,omitempty"`
	BrowserRuntimePricing *BrowserRuntimePricing `json:"browser_runtime_pricing,omitempty"`
	SandboxComputePricing *SandboxComputePricing `json:"sandbox_compute_pricing,omitempty"`
	CreditPricing         *CreditPricing         `json:"credit_pricing,omitempty"`
}

// CreditPricing is the machine-readable rate card for the usage-credit wallet.
// Every metered resource debits fungible credit at these rates. Rates that
// differ on Free carry a *Free variant. Retention is billed only for bytes held
// beyond the plan's retention window (its SESSION_RETENTION_DAYS usage limit).
// These values are the source of truth for the plan card's display strings,
// which used to be maintained by hand and drifted.
type CreditPricing struct {
	Currency                string  `json:"currency"`
	InferenceMarkup         float64 `json:"inference_markup"`
	ProcessedDataPerGiB     float64 `json:"processed_data_per_gib"`
	ProcessedDataPerGiBFree float64 `json:"processed_data_per_gib_free"`
	RetentionPerGiBMonth    float64 `json:"retention_per_gib_month"`
	ScoresPer1k             float64 `json:"scores_per_1k"`
	ScoresPer1kFree         float64 `json:"scores_per_1k_free"`
}

// Plan defines a license and billing tier.
type Plan struct {
	Tier              string       `json:"tier"`
	Name              string       `json:"name"`
	Description       string       `json:"description,omitempty"`
	TrialDurationDays int          `json:"trial_duration_days,omitempty"`
	Pricing           Pricing      `json:"pricing"`
	Highlight         bool         `json:"highlight"`
	SeatLimit         int          `json:"seat_limit"`
	InstanceLimit     int          `json:"instance_limit"`
	Features          []Feature    `json:"features"`
	UsageLimits       []UsageLimit `json:"usage_limits"`

	// Usage-credit wallet grant (see CreditPricing for the rate card). The
	// subscription fee is granted as fungible credit each period; usage debits
	// it. CreditGrantUSD is the recurring per-period grant (0 = no recurring
	// grant; Free's one-time signup grant is SandboxComputePricing.StarterCreditUSD,
	// not duplicated here). InferenceCapUSD caps the share of the grant spendable
	// on platform-key inference. The retention window is the plan's
	// SESSION_RETENTION_DAYS usage limit; retention beyond it bills at
	// CreditPricing.RetentionPerGiBMonth.
	CreditGrantUSD  float64 `json:"credit_grant_usd,omitempty"`
	InferenceCapUSD float64 `json:"inference_cap_usd,omitempty"`
	CreditRollover  bool    `json:"credit_rollover,omitempty"`
}

// Feature represents a feature flag for a plan.
type Feature struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

// UsageLimit represents a named plan usage limit. A value of -1 is unlimited.
type UsageLimit struct {
	Type    string `json:"type"`
	Value   int64  `json:"value"`
	SubText string `json:"subText,omitempty"`
}

// Pricing represents plan pricing for the supported billing periods.
type Pricing struct {
	Monthly    string          `json:"monthly"`
	Yearly     string          `json:"yearly"`
	Discounted string          `json:"discounted,omitempty"`
	Suggested  string          `json:"suggested,omitempty"`
	PerSeat    *PerSeatPricing `json:"per_seat,omitempty"`
}

// PerSeatPricing represents pricing for each additional admin seat.
type PerSeatPricing struct {
	Monthly string `json:"monthly"`
	Yearly  string `json:"yearly"`
	SubText string `json:"subText,omitempty"`
}

// BrowserRuntimePricing defines the public hosted-browser meter. Browser time
// is separate from sandbox compute because the managed runtime reserves an
// additional tenant-isolated Chromium pod.
type BrowserRuntimePricing struct {
	Currency                string  `json:"currency"`
	BrowserHour             float64 `json:"browser_hour"`
	BillingIncrementSeconds int64   `json:"billing_increment_seconds"`
	MinimumSessionSeconds   int64   `json:"minimum_session_seconds"`
	IdlePoolBilling         bool    `json:"idle_pool_billing"`
}

// SiteDeliveryPricing defines pooled Sites allowances and on-demand rates.
// Sites are not a billing dimension: requests, transfer, and retained storage
// are aggregated across all of a tenant's sites.
type SiteDeliveryPricing struct {
	Currency               string                           `json:"currency"`
	EdgeRequestsPerMillion float64                          `json:"edge_requests_per_million"`
	TransferPerGB          float64                          `json:"transfer_per_gb"`
	StoragePerGBMonth      float64                          `json:"storage_per_gb_month"`
	Plans                  map[string]SiteDeliveryAllowance `json:"plans"`
}

type SiteDeliveryAllowance struct {
	EdgeRequests   int64 `json:"edge_requests"`
	TransferBytes  int64 `json:"transfer_bytes"`
	StorageBytes   int64 `json:"storage_bytes"`
	OverageEnabled bool  `json:"overage_enabled"`
	Custom         bool  `json:"custom"`
}

// SandboxComputePricing contains the canonical sandbox compute and storage rates.
type SandboxComputePricing struct {
	Currency               string        `json:"currency"`
	StarterCreditUSD       float64       `json:"starter_credit_usd"`
	CPUPerVCPUHour         float64       `json:"cpu_per_vcpu_hour"`
	MemoryPerGiBSecond     float64       `json:"memory_per_gib_second"`
	MemoryPerGiBHour       float64       `json:"memory_per_gib_hour"`
	DiskPerGiBSecond       float64       `json:"disk_per_gib_second"`
	DiskPerGiBHour         float64       `json:"disk_per_gib_hour"`
	PlatformPerSandboxHour float64       `json:"platform_per_sandbox_hour"`
	IncludedDiskGiB        float64       `json:"included_disk_gib"`
	DiskTier2ThresholdGiB  float64       `json:"disk_tier2_threshold_gib"`
	DiskTier2Multiplier    float64       `json:"disk_tier2_multiplier"`
	Sizes                  []SandboxSize `json:"sizes"`
}

// SandboxSize is one supported CPU/memory shape. Customer-facing estimates
// and runtime validation should use these shapes instead of arbitrary mixes.
type SandboxSize struct {
	ID        string  `json:"id"`
	Label     string  `json:"label"`
	VCPU      float64 `json:"vcpu"`
	MemoryGiB float64 `json:"memory_gib"`
	DiskGiB   float64 `json:"disk_gib"`
}

// PlanLimits is the enforcement-oriented projection of a plan. UsageLimits
// includes every named entry in the plan's usage_limits block.
type PlanLimits struct {
	UsageLimits   map[string]int64
	InstanceLimit int
	SeatLimit     int
}

// Embedded parses and caches the plans configuration compiled into this package.
func Embedded() (*PlansConfig, error) {
	embeddedOnce.Do(func() {
		embeddedConfig, embeddedErr = Parse(embeddedPlansJSON)
	})
	return embeddedConfig, embeddedErr
}

// Parse parses plans configuration JSON.
func Parse(data []byte) (*PlansConfig, error) {
	var config PlansConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse plans config: %w", err)
	}
	return &config, nil
}

// LoadFromFile parses plans configuration from an on-disk override.
func LoadFromFile(path string) (*PlansConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read plans config: %w", err)
	}
	return Parse(data)
}

// Load uses an existing on-disk override when path is non-empty. If path is
// empty or does not exist, it falls back to the embedded configuration.
func Load(path string) (*PlansConfig, error) {
	if path == "" {
		return Embedded()
	}
	if _, err := os.Stat(path); err == nil {
		return LoadFromFile(path)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("failed to stat plans config: %w", err)
	}
	return Embedded()
}

// GetPlan returns the plan definition for tier.
func (pc *PlansConfig) GetPlan(tier string) (*Plan, bool) {
	if pc == nil {
		return nil, false
	}
	plan, ok := pc.Plans[tier]
	return &plan, ok
}

// GetAllPlans returns plans in the stable free, basic, pro, enterprise order,
// followed by any additional tiers.
func (pc *PlansConfig) GetAllPlans() []Plan {
	if pc == nil {
		return nil
	}

	result := make([]Plan, 0, len(pc.Plans))
	ordered := map[string]struct{}{}
	for _, tier := range []string{"free", "basic", "pro", "enterprise"} {
		if plan, ok := pc.Plans[tier]; ok {
			result = append(result, plan)
			ordered[tier] = struct{}{}
		}
	}
	for tier, plan := range pc.Plans {
		if _, ok := ordered[tier]; !ok {
			result = append(result, plan)
		}
	}
	return result
}

// GetPlanLimits returns all usage and top-level limits for tier.
func (pc *PlansConfig) GetPlanLimits(tier string) *PlanLimits {
	plan, ok := pc.GetPlan(tier)
	if !ok {
		return nil
	}

	limits := &PlanLimits{
		UsageLimits:   make(map[string]int64, len(plan.UsageLimits)),
		InstanceLimit: plan.InstanceLimit,
		SeatLimit:     plan.SeatLimit,
	}
	for _, limit := range plan.UsageLimits {
		limits.UsageLimits[limit.Type] = limit.Value
	}
	return limits
}

// GetCreditPricing returns the usage-credit rate card, or nil when unset.
func (pc *PlansConfig) GetCreditPricing() *CreditPricing {
	if pc == nil {
		return nil
	}
	return pc.CreditPricing
}

// GetUsageLimit returns a named usage limit and whether the plan defined it.
func (limits *PlanLimits) GetUsageLimit(limitType string) (int64, bool) {
	if limits == nil {
		return 0, false
	}
	value, ok := limits.UsageLimits[limitType]
	return value, ok
}

// ToProtoFeatures converts plan features to license protobuf feature flags.
func (p *Plan) ToProtoFeatures() []*licv1.FeatureFlag {
	features := make([]*licv1.FeatureFlag, 0, len(p.Features))
	for _, feature := range p.Features {
		features = append(features, &licv1.FeatureFlag{
			Name:    feature.Name,
			Enabled: feature.Enabled,
		})
	}
	return features
}

// ToProtoUsageLimits converts plan usage limits to license protobuf limits.
func (p *Plan) ToProtoUsageLimits() []*licv1.UsageLimits {
	limits := make([]*licv1.UsageLimits, 0, len(p.UsageLimits))
	for _, limit := range p.UsageLimits {
		limits = append(limits, &licv1.UsageLimits{
			Type:  parseUsageLimitType(limit.Type),
			Value: limit.Value,
		})
	}
	return limits
}

func parseUsageLimitType(limitType string) licv1.UsageLimitsType {
	switch limitType {
	case "REQUESTS":
		return licv1.UsageLimitsType_USAGE_LIMITS_TYPE_REQUESTS
	case "EXECUTIONS":
		return licv1.UsageLimitsType_USAGE_LIMITS_TYPE_EXECUTIONS
	case "STORAGE":
		return licv1.UsageLimitsType_USAGE_LIMITS_TYPE_STORAGE
	case "BANDWIDTH":
		return licv1.UsageLimitsType_USAGE_LIMITS_TYPE_BANDWIDTH
	case "RPS":
		return licv1.UsageLimitsType_USAGE_LIMITS_TYPE_RPS
	case "RPM":
		return licv1.UsageLimitsType_USAGE_LIMITS_TYPE_RPM
	case "RPH":
		return licv1.UsageLimitsType_USAGE_LIMITS_TYPE_RPH
	default:
		return licv1.UsageLimitsType_USAGE_LIMITS_TYPE_UNSPECIFIED
	}
}
