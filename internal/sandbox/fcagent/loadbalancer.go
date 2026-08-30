package fcagent

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"

	"github.com/everstacklabs/everstack/internal/lib/logger"
	fcpb "github.com/everstacklabs/everstack/pkg/grpc/everstack/firecracker/v1"
)

// LoadBalancer picks an agent target for new sandbox creation.
//
// Two-stage filter:
//
//  1. Pressure gating — if a HealthCache is attached, targets reporting
//     unhealthy (disk/mem/cpu/fd over threshold, or stale for >30s)
//     are skipped. When ALL targets are degraded the gate opens
//     (better to try a sick node than fail the create entirely), but
//     the event is logged loudly so the operator sees the
//     "everyone's sick" signal even when traffic kept flowing.
//
//  2. Round-robin across the remaining targets. TryEach provides
//     failover when a picked target rejects (e.g. "pool exhausted"
//     or "Unavailable") so a saturated node doesn't fail the
//     request when a sibling has capacity.
type LoadBalancer struct {
	disc   *Discovery
	health *HealthCache // optional; nil = no gating, fall back to round-robin
	idx    uint64
}

// NewLoadBalancer creates a round-robin selector over the discovery's
// targets with no pressure gating. Prefer NewLoadBalancerWithHealth
// in production paths so degraded hosts get skipped.
func NewLoadBalancer(d *Discovery) *LoadBalancer {
	return &LoadBalancer{disc: d}
}

// NewLoadBalancerWithHealth wires a HealthCache into the placement
// path so targets reporting host pressure get filtered out before
// round-robin selection.
func NewLoadBalancerWithHealth(d *Discovery, hc *HealthCache) *LoadBalancer {
	return &LoadBalancer{disc: d, health: hc}
}

// eligibleTargets returns the subset of discovered targets that are
// currently considered placement-eligible by the HealthCache (when
// attached). When the cache is unset or every target is degraded,
// the full target list is returned — we'd rather try a sick node
// than fail every create because the cache hasn't warmed yet.
func (lb *LoadBalancer) eligibleTargets() []string {
	all := lb.disc.Targets()
	if lb.health == nil || len(all) == 0 {
		return all
	}
	eligible := make([]string, 0, len(all))
	for _, t := range all {
		if lb.health.IsHealthy(t) {
			eligible = append(eligible, t)
		}
	}
	if len(eligible) == 0 {
		// All targets degraded. Fall back to the full list rather
		// than return zero — the alternative is "every create fails
		// because every host is briefly stressed," which is the
		// outage cascade we're trying to prevent in the first place.
		logger.WithFields("target_count", len(all)).
			Warn("fcagent_lb: all targets degraded, placing anyway")
		return all
	}
	return eligible
}

// Pick returns the target and connected client for the next selected agent.
//
// Use TryEach for create-class operations where any agent will do — Pick
// is for the simple "give me one" case (rare, kept for callers that
// route by id later).
func (lb *LoadBalancer) Pick(ctx context.Context) (string, fcpb.FirecrackerAgentClient, error) {
	targets := lb.eligibleTargets()
	if len(targets) == 0 {
		return "", nil, errors.New("fcagent: no agents available")
	}
	n := atomic.AddUint64(&lb.idx, 1) - 1
	target := targets[int(n%uint64(len(targets)))]
	cli, err := lb.disc.Client(target)
	if err != nil {
		return target, nil, err
	}
	return target, cli, nil
}

// TryEach calls fn against each discovered target in round-robin order
// until one succeeds. Iteration starts from the next round-robin index
// so concurrent callers fan out across agents on the first attempt.
//
// fn returns (result, retryable, err):
//   - result: the value to return on success.
//   - retryable: true if the error means "try a different target"
//     (capacity-class errors), false to surface immediately.
//   - err: the error from the call.
//
// Returns the result + the target that produced it; or the LAST error
// seen across all targets when every attempt failed.
func TryEach[T any](
	lb *LoadBalancer,
	ctx context.Context,
	fn func(ctx context.Context, target string, cli fcpb.FirecrackerAgentClient) (T, bool, error),
) (T, string, error) {
	var zero T
	targets := lb.eligibleTargets()
	if len(targets) == 0 {
		return zero, "", errors.New("fcagent: no agents available")
	}
	start := int(atomic.AddUint64(&lb.idx, 1) - 1)
	var lastErr error
	for i := 0; i < len(targets); i++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return zero, "", ctxErr
		}
		target := targets[(start+i)%len(targets)]
		cli, err := lb.disc.Client(target)
		if err != nil {
			lastErr = err
			continue
		}
		result, retryable, err := fn(ctx, target, cli)
		if err == nil {
			return result, target, nil
		}
		lastErr = err
		if !retryable {
			return zero, target, err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("fcagent: all targets failed")
	}
	return zero, "", lastErr
}

// IsCapacityError reports whether an fcagent error signals "this node
// is full, try a different one". Conservative — only matches messages
// the agent emits for known capacity-class failures.
func IsCapacityError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "VM pool exhausted") ||
		strings.Contains(msg, "no capacity") ||
		strings.Contains(msg, "ResourceExhausted")
}
