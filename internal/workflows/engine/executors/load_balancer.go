package executors

import (
	"context"
	"fmt"
	"math/rand"
	"sync"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/providers/ratelimit"
	"github.com/everstacklabs/everstack/internal/workflows/engine"
)

// LoadBalancerExecutor handles load balancing across multiple providers.
//
// Config fields (from frontend LoadBalancerConfig):
//   - strategy: "router" | "round_robin" | "weighted" | "random" (default: "router")
//   - weights: array of {provider: string, weight: float64} — used for weighted strategy
//   - fallback: string — fallback provider name if primary selection is unavailable
//
// Strategies:
//   - "router": Delegates to the gateway's Router.ResolveWithContext() which handles
//     4-tier model resolution (FastPath cache → Custom models → Route map → Catalog).
//   - "weighted": Weighted random selection from explicitly configured provider weights.
//   - "round_robin": Round-robin selection across configured or registered providers.
//   - "random": Random selection across configured or registered providers.
//
// The executor sets ec.ResolvedProvider (and ec.ResolvedModel for the router strategy).
// Manual strategies (weighted/round_robin/random) filter out rate-limited providers.
//
// Handles: "out" on success. Returns an error if no providers are available.
type LoadBalancerExecutor struct {
	Registry *gw.Registry
	Router   *gw.Router

	// round-robin state
	mu      sync.Mutex
	rrIndex uint64
}

func (e *LoadBalancerExecutor) NodeType() string { return "loadBalancer" }

func (e *LoadBalancerExecutor) Execute(ctx context.Context, node *engine.GraphNode, ec *engine.ExecutionContext) engine.NodeResult {
	strategy := node.GetConfigString("strategy")
	if strategy == "" {
		strategy = "router"
	}

	// Router strategy: delegate to the gateway's full model resolution pipeline
	if strategy == "router" {
		return e.executeRouter(ctx, ec)
	}

	// Manual strategies: weighted, round_robin, random
	fallback := node.GetConfigString("fallback")
	weights := e.parseWeights(node)

	candidates := e.buildCandidates(weights)
	if len(candidates) == 0 && fallback != "" {
		candidates = []weightedProvider{{Name: fallback, Weight: 1.0}}
	}

	if len(candidates) == 0 {
		return engine.NodeResult{Error: fmt.Errorf("load balancer: no providers configured")}
	}

	// Filter out rate-limited providers
	available := e.filterAvailable(candidates)
	if len(available) == 0 {
		logger.Warn("load balancer: all providers rate limited, using unfiltered list")
		available = candidates
	}

	var selected string
	switch strategy {
	case "round_robin":
		selected = e.selectRoundRobin(available)
	case "weighted":
		selected = e.selectWeighted(available)
	case "random":
		selected = e.selectRandom(available)
	default:
		selected = e.selectRoundRobin(available)
	}

	if selected == "" && fallback != "" {
		selected = fallback
	}

	if selected == "" {
		return engine.NodeResult{Error: fmt.Errorf("load balancer: could not select a provider")}
	}

	ec.ResolvedProvider = selected
	ec.SetNodeData("strategy", strategy)
	ec.SetNodeData("selected_provider", selected)
	ec.SetNodeData("candidates_count", fmt.Sprintf("%d", len(available)))
	logger.WithFields("strategy", strategy, "selected", selected, "candidates", len(available)).
		Debug("load balancer: provider selected")

	return engine.NodeResult{NextHandle: "out", Output: map[string]interface{}{
		"strategy": strategy,
		"selected": selected,
	}}
}

// executeRouter uses the gateway's Router.ResolveWithContext to resolve
// the model to a provider using the full 4-tier resolution pipeline.
func (e *LoadBalancerExecutor) executeRouter(ctx context.Context, ec *engine.ExecutionContext) engine.NodeResult {
	if e.Router == nil {
		return engine.NodeResult{Error: fmt.Errorf("load balancer: router not configured")}
	}

	model := ec.ResolvedModel
	if model == "" {
		return engine.NodeResult{Error: fmt.Errorf("load balancer: no model specified for router resolution")}
	}

	_, route, err := e.Router.ResolveWithContext(ctx, model)
	if err != nil {
		return engine.NodeResult{Error: fmt.Errorf("load balancer: router resolution failed: %w", err)}
	}

	ec.ResolvedProvider = route.ProviderName
	ec.ResolvedModel = route.ModelName
	ec.SetNodeData("strategy", "router")
	ec.SetNodeData("selected_provider", route.ProviderName)
	ec.SetNodeData("candidates_count", "1")
	logger.WithFields("provider", route.ProviderName, "model", route.ModelName, "custom", route.IsCustom).
		Debug("load balancer: router resolved provider")

	return engine.NodeResult{NextHandle: "out", Output: map[string]interface{}{
		"strategy": "router",
		"selected": route.ProviderName,
	}}
}

type weightedProvider struct {
	Name   string
	Weight float64
}

// parseWeights extracts the weights array from the node config.
// Expected format: [{provider: "openai", weight: 0.7}, {provider: "anthropic", weight: 0.3}]
func (e *LoadBalancerExecutor) parseWeights(node *engine.GraphNode) []weightedProvider {
	if node.Config == nil {
		return nil
	}

	raw, ok := node.Config["weights"]
	if !ok {
		return nil
	}

	arr, ok := raw.([]interface{})
	if !ok {
		return nil
	}

	var weights []weightedProvider
	for _, item := range arr {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := m["provider"].(string)
		weight, _ := m["weight"].(float64)
		if name != "" {
			if weight <= 0 {
				weight = 1.0
			}
			weights = append(weights, weightedProvider{Name: name, Weight: weight})
		}
	}
	return weights
}

// buildCandidates builds the candidate list from weights or the registry.
func (e *LoadBalancerExecutor) buildCandidates(weights []weightedProvider) []weightedProvider {
	if len(weights) > 0 {
		return weights
	}

	if e.Registry == nil {
		return nil
	}

	var candidates []weightedProvider
	for name := range e.Registry.All() {
		candidates = append(candidates, weightedProvider{Name: name, Weight: 1.0})
	}
	return candidates
}

// filterAvailable removes providers that are currently rate-limited.
func (e *LoadBalancerExecutor) filterAvailable(candidates []weightedProvider) []weightedProvider {
	var available []weightedProvider
	for _, c := range candidates {
		if !ratelimit.GlobalMonitor.IsRateLimited(c.Name) {
			available = append(available, c)
		}
	}
	return available
}

func (e *LoadBalancerExecutor) selectRoundRobin(candidates []weightedProvider) string {
	if len(candidates) == 0 {
		return ""
	}
	e.mu.Lock()
	idx := e.rrIndex
	e.rrIndex++
	e.mu.Unlock()
	return candidates[idx%uint64(len(candidates))].Name
}

func (e *LoadBalancerExecutor) selectWeighted(candidates []weightedProvider) string {
	if len(candidates) == 0 {
		return ""
	}

	totalWeight := 0.0
	for _, c := range candidates {
		totalWeight += c.Weight
	}

	r := rand.Float64() * totalWeight
	cumulative := 0.0
	for _, c := range candidates {
		cumulative += c.Weight
		if r <= cumulative {
			return c.Name
		}
	}
	return candidates[len(candidates)-1].Name
}

func (e *LoadBalancerExecutor) selectRandom(candidates []weightedProvider) string {
	if len(candidates) == 0 {
		return ""
	}
	return candidates[rand.Intn(len(candidates))].Name
}
