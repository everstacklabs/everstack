package providers

import (
	"time"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

// Intentionally left as a placeholder for provider-specific packages to import
// a common namespace if needed in the future. The registry and interfaces live
// under internal/lib/handlers/gateway to avoid import cycles.

// ChatRequestTimeout gives reasoning requests enough time to complete while
// preserving the provider's existing latency guard for ordinary completions.
func ChatRequestTimeout(sampling gw.SamplingParams, standard time.Duration) time.Duration {
	if sampling.ReasoningEffort != "" ||
		sampling.ReasoningBudget != nil ||
		sampling.ReasoningEnabled != nil {
		return 120 * time.Second
	}
	return standard
}
