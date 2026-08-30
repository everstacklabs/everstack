// Package compact implements context-window compaction for chat
// completions. It exists in two forms:
//
//   - As a Compactor that decides what to do (no I/O, pure function),
//     used directly by both the agent runtime's Monitor and the
//     gateway-side middleware.
//   - As a Summarizer that performs the LLM call to fold older
//     messages into a summary, given any gw.Provider.
//   - As a Middleware that wraps a gw.Provider and applies compaction
//     transparently for /v1/chat/completions traffic that doesn't go
//     through the agent runtime.
//
// The 3-tier policy (background ≥80%, aggressive ≥85%, emergency ≥95%)
// originated in internal/agents/runtime/monitor.go; this package is
// the extraction so the same algorithm runs for both agent sessions
// and raw chat-completions clients.
package compact

import "fmt"

// Tier identifies the urgency level of a compaction request. The
// background tier kicks in early to avoid latency spikes; aggressive
// summarises a larger fraction; emergency hard-truncates without an
// LLM call so we always finish under the context window.
type Tier string

const (
	TierBackground Tier = "background"
	TierAggressive Tier = "aggressive"
	TierEmergency  Tier = "emergency"
)

// Config controls the compactor's thresholds and target model.
//
// Defaults match the agent runtime's MonitorConfig — see
// DefaultConfig — so the behaviour is identical when the same input is
// fed through either path.
type Config struct {
	// Enabled gates the entire feature. When false, Decide always
	// returns ActionNone.
	Enabled bool `json:"enabled"`

	// MaxContextTokens is the model's context window cap (or a safer
	// percentage of it). Utilization is computed as
	// promptTokens / MaxContextTokens.
	MaxContextTokens int `json:"max_context_tokens"`

	// BackgroundThreshold is the utilization ratio (0..1) at which to
	// summarise the oldest 30% of non-system messages. Default 0.80.
	BackgroundThreshold float64 `json:"background_threshold"`

	// AggressiveThreshold is the utilization ratio at which to
	// summarise the oldest 60% of non-system messages. Default 0.85.
	AggressiveThreshold float64 `json:"aggressive_threshold"`

	// EmergencyThreshold is the utilization ratio at which to
	// hard-truncate (no LLM call) keeping system + last 20 messages.
	// Default 0.95.
	EmergencyThreshold float64 `json:"emergency_threshold"`

	// SummarizationModel is the model name used for the summarisation
	// LLM call. Should be a fast, cheap model (e.g. "gpt-4o-mini").
	SummarizationModel string `json:"summarization_model"`

	// EnabledForProviders restricts compaction to the listed provider
	// names ("anthropic", "openai", …). Empty = all providers. Used by
	// the middleware decorator to skip work for providers that don't
	// benefit (e.g. those without prompt caching where the compaction
	// LLM call is pure overhead).
	EnabledForProviders []string `json:"enabled_for_providers,omitempty"`
}

// DefaultConfig returns the canonical defaults — disabled by default
// so embedding the package alone is a no-op. Callers (gateway YAML
// loader, agent runtime config parser) override fields.
func DefaultConfig() Config {
	return Config{
		Enabled:             false,
		MaxContextTokens:    128000,
		BackgroundThreshold: 0.80,
		AggressiveThreshold: 0.85,
		EmergencyThreshold:  0.95,
		SummarizationModel:  "gpt-4o-mini",
	}
}

// Validate returns an error when the config is malformed. Threshold
// ordering is asserted because Decide assumes Emergency > Aggressive
// > Background.
func (c Config) Validate() error {
	if c.MaxContextTokens <= 0 {
		return fmt.Errorf("compact: max_context_tokens must be > 0")
	}
	if !(c.BackgroundThreshold > 0 && c.BackgroundThreshold < 1) {
		return fmt.Errorf("compact: background_threshold must be in (0, 1)")
	}
	if !(c.AggressiveThreshold > c.BackgroundThreshold && c.AggressiveThreshold < 1) {
		return fmt.Errorf("compact: aggressive_threshold must be > background_threshold and < 1")
	}
	if !(c.EmergencyThreshold > c.AggressiveThreshold && c.EmergencyThreshold <= 1) {
		return fmt.Errorf("compact: emergency_threshold must be > aggressive_threshold and <= 1")
	}
	return nil
}

// IsProviderAllowed reports whether the named provider should run the
// compaction middleware. Empty allowlist allows every provider.
func (c Config) IsProviderAllowed(name string) bool {
	if len(c.EnabledForProviders) == 0 {
		return true
	}
	for _, p := range c.EnabledForProviders {
		if p == name {
			return true
		}
	}
	return false
}
