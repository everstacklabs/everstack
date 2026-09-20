package plans

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// SafeTier is the tier used when a plan cannot be resolved. It must always
// exist in plans.json.
const SafeTier = "free"

// ErrUnknownPlan is returned when a tier is not in the catalog. It is returned
// ALONGSIDE usable free-tier entitlements, never instead of them -- see
// ResolveEntitlements.
var ErrUnknownPlan = errors.New("plan tier not found in catalog")

// TrialPolicy is a plan's trial contract. A plan either declares one or is
// explicitly perpetual.
//
// The JSON is deliberately a pointer on Plan so that `"trial": null` and an
// absent key are distinguishable: null means "decided: no trial", absent means
// nobody has decided. See docs/design/free-tier-and-trial-contract.md.
type TrialPolicy struct {
	DurationDays int `json:"duration_days"`
	// Subject is what the clock belongs to: "organization" for Cloud,
	// "installation" for Community Edition. Naming it is what prevents a trial
	// resetting on process restart, new browser or re-activation.
	Subject string `json:"subject"`
	// OnExpiry is "downgrade_to" or "block".
	OnExpiry string `json:"on_expiry"`
	// OnExpiryTarget is the tier to fall back to when OnExpiry is
	// "downgrade_to".
	OnExpiryTarget string `json:"on_expiry_target,omitempty"`
	GraceDays      int    `json:"grace_days"`
}

// Entitlements is the normalized answer to "what may this tier do". Every
// consumer -- billing, the License Service, gateway entitlements and the CE
// trial manager -- is meant to read this rather than deriving its own.
type Entitlements struct {
	Tier          string
	UsageLimits   map[string]int64
	InstanceLimit int
	SeatLimit     int
	Features      map[string]bool
	// Trial is nil for a perpetual plan.
	Trial *TrialPolicy
	// Degraded is true when the requested tier could not be resolved and these
	// are the safe-tier entitlements instead. Callers that surface plan state to
	// a customer should treat this as "unknown", not as "free".
	Degraded bool
	// RequestedTier is what the caller asked for, preserved for logging when
	// Degraded is true.
	RequestedTier string
}

// ResolveEntitlements returns the entitlements for tier.
//
// It FAILS CLOSED. An unknown tier yields free-tier entitlements plus
// ErrUnknownPlan, rather than nil or an unlimited set. This is deliberate and
// is the single most important property of this function: a caller that
// ignores the error still gets safe limits.
//
// GetPlanLimits, by contrast, returns nil for an unknown tier, and a nil limit
// map reads downstream as "no limits apply". That is how managed tenants came
// to run uncapped. New code should use this function; GetPlanLimits is kept
// unchanged for existing callers.
func (pc *PlansConfig) ResolveEntitlements(tier string) (*Entitlements, error) {
	requested := strings.ToLower(strings.TrimSpace(tier))

	if pc == nil {
		return nil, fmt.Errorf("%w: no plan catalog loaded", ErrUnknownPlan)
	}

	plan, ok := pc.GetPlan(requested)
	if !ok || requested == "" {
		safe, safeOK := pc.GetPlan(SafeTier)
		if !safeOK {
			// The catalog is unusable. Returning nil here is correct: there is
			// no safe answer to fall back to, and inventing one would be worse
			// than making the caller handle it.
			return nil, fmt.Errorf("%w: %q, and safe tier %q is also missing", ErrUnknownPlan, tier, SafeTier)
		}
		ent := entitlementsFrom(safe)
		ent.Degraded = true
		ent.RequestedTier = tier
		return ent, fmt.Errorf("%w: %q, falling back to %q", ErrUnknownPlan, tier, SafeTier)
	}

	ent := entitlementsFrom(plan)
	ent.RequestedTier = tier
	return ent, nil
}

func entitlementsFrom(plan *Plan) *Entitlements {
	ent := &Entitlements{
		Tier:          plan.Tier,
		UsageLimits:   make(map[string]int64, len(plan.UsageLimits)),
		InstanceLimit: plan.InstanceLimit,
		SeatLimit:     plan.SeatLimit,
		Features:      make(map[string]bool, len(plan.Features)),
		Trial:         plan.Trial,
	}
	for _, l := range plan.UsageLimits {
		ent.UsageLimits[l.Type] = l.Value
	}
	for _, f := range plan.Features {
		ent.Features[f.Name] = f.Enabled
	}
	return ent
}

// Limit returns the limit for a named usage type and whether it was declared.
// A declared value of -1 means unlimited; an undeclared limit returns ok=false
// so a caller can tell "this plan permits unlimited X" apart from "nobody said".
func (e *Entitlements) Limit(name string) (int64, bool) {
	if e == nil || e.UsageLimits == nil {
		return 0, false
	}
	v, ok := e.UsageLimits[name]
	return v, ok
}

// IsPerpetual reports whether the plan has no trial clock.
func (e *Entitlements) IsPerpetual() bool {
	return e != nil && e.Trial == nil
}

// plansJSON exposes the embedded catalog bytes for tests that need to assert on
// the raw document rather than the parsed struct -- specifically, that a
// "trial" key is present rather than merely decoding to nil.
func plansJSON() ([]byte, error) { return embeddedPlansJSON, nil }

// hasExplicitTrialKey reports whether tier's object contains a literal "trial"
// key. Decoding cannot answer this: an absent key and `"trial": null` both
// produce a nil pointer.
func hasExplicitTrialKey(raw []byte, tier string) bool {
	var doc struct {
		Plans map[string]map[string]any `json:"plans"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false
	}
	plan, ok := doc.Plans[tier]
	if !ok {
		return false
	}
	_, present := plan["trial"]
	return present
}
