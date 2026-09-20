package enterprise

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Prometheus sink for the EVS-6 shadow exposure report (Stage 0, slice 1).
// Registered via promauto on the default registry. Label budget follows the
// firecracker convention: source/tier/limit_type are small bounded sets.
// Tenant IDs intentionally NEVER appear in labels — per-tenant drill-down
// comes from the structured operational log (reportShadowLimits), which flows
// via OTEL to ClickHouse. Would-deny counts (usage vs cap) are phase 2; this
// ships the publishable exposure report without touching enforcement.
var (
	// entitlementResolutions counts every ResolveEntitlements outcome by
	// source and tier. Query the bypass-vs-shadow share with:
	//   sum by (source) (rate(entitlement_resolutions_total[5m]))
	entitlementResolutions = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "everstack",
		Subsystem: "gateway",
		Name:      "entitlement_resolutions_total",
		Help:      "Count of entitlement resolutions by source and tier. Labels: source, tier.",
	}, []string{"source", "tier"})

	// entitlementShadowLimitedTenants tracks how many distinct tenants would
	// hit each capped limit if enforcement were on today. Process-lifetime
	// exposure set (resets on restart); first-seen tier sticks for a
	// tenant|limit pair, so treat tier as approximate. Per-tier exposure:
	//   sum by (tier, limit_type) (entitlement_shadow_limited_tenants)
	entitlementShadowLimitedTenants = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "everstack",
		Subsystem: "gateway",
		Name:      "entitlement_shadow_limited_tenants",
		Help:      "Distinct tenants that would hit a capped plan limit under enforcement. Labels: tier, limit_type.",
	}, []string{"tier", "limit_type"})
)

// observeEntitlementResolution records one resolution. Empty source/tier
// collapse to "unknown" so label values stay meaningful.
func observeEntitlementResolution(ent Entitlements) {
	source := ent.Source
	if source == "" {
		source = "unknown"
	}
	tier := ent.Tier
	if tier == "" {
		tier = "unknown"
	}
	entitlementResolutions.WithLabelValues(source, tier).Inc()
}
