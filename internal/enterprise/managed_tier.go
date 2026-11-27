package enterprise

import "context"

// ManagedPlanTier returns the plan tier of tenantID's organization on a
// managed gateway, for subsystems that decide enforcement themselves (the
// sandbox and browser concurrency scope). ok is false when no resolver is
// installed or the organization is unknown.
func ManagedPlanTier(ctx context.Context, tenantID string) (string, bool) {
	r := currentPlanResolver()
	if r == nil || !ManagedGateway() || tenantID == "" {
		return "", false
	}
	return r.PlanTier(ctx, tenantID)
}
