package telemetry

import (
	"fmt"
)

// TracingConfig holds configuration for distributed tracing
type TracingConfig struct {
	// SamplingRate controls what percentage of requests to trace (0.0 to 1.0)
	// 1.0 = trace everything, 0.5 = trace 50%, 0.0 = trace nothing
	SamplingRate float64

	// Granularity controls the level of detail in traces
	// "minimal" - Gateway-level spans only
	// "standard" - Gateway + provider + embeddings (recommended)
	// "detailed" - Everything including stream chunks, key rotations
	Granularity string

	// TraceProviderCalls enables tracing of individual provider HTTP requests
	// Enabled by default in standard and detailed modes
	TraceProviderCalls bool

	// TraceStreamChunks enables tracing of individual streaming chunks
	// Only enabled in detailed mode by default (can be very noisy)
	TraceStreamChunks bool

	// TraceFallbacks enables child spans for fallback attempts
	// Enabled by default in standard and detailed modes
	TraceFallbacks bool

	// TraceKeyRotation enables tracing of key rotation attempts
	// Enabled by default in detailed mode
	TraceKeyRotation bool
}

// DefaultTracingConfig returns the default tracing configuration
func DefaultTracingConfig() TracingConfig {
	return TracingConfig{
		SamplingRate:       1.0,        // Trace 100% of requests
		Granularity:        "standard", // Recommended default
		TraceProviderCalls: true,
		TraceStreamChunks:  false,
		TraceFallbacks:     true,
		TraceKeyRotation:   false,
	}
}

// ApplyGranularity applies granularity-based defaults to the config
// This is called after loading config to ensure consistency
func (c *TracingConfig) ApplyGranularity() {
	switch c.Granularity {
	case "minimal":
		c.TraceProviderCalls = false
		c.TraceStreamChunks = false
		c.TraceFallbacks = false
		c.TraceKeyRotation = false
	case "standard":
		// Standard is the recommended default
		if !c.explicitlySet("TraceProviderCalls") {
			c.TraceProviderCalls = true
		}
		if !c.explicitlySet("TraceFallbacks") {
			c.TraceFallbacks = true
		}
		c.TraceStreamChunks = false
		c.TraceKeyRotation = false
	case "detailed":
		c.TraceProviderCalls = true
		c.TraceStreamChunks = true
		c.TraceFallbacks = true
		c.TraceKeyRotation = true
	default:
		// Unknown granularity, use standard
		c.Granularity = "standard"
		c.ApplyGranularity()
	}
}

// explicitlySet checks if a field was explicitly set in config
// This is a placeholder - actual implementation would track which fields were set
func (c *TracingConfig) explicitlySet(field string) bool {
	// For now, assume fields are not explicitly set
	// This can be enhanced with a separate tracking mechanism if needed
	return false
}

// Validate validates the tracing configuration
func (c *TracingConfig) Validate() error {
	if c.SamplingRate < 0.0 || c.SamplingRate > 1.0 {
		return fmt.Errorf("sampling_rate must be between 0.0 and 1.0, got %f", c.SamplingRate)
	}

	validGranularities := map[string]bool{
		"minimal":  true,
		"standard": true,
		"detailed": true,
	}
	if !validGranularities[c.Granularity] {
		return fmt.Errorf("granularity must be one of [minimal, standard, detailed], got %s", c.Granularity)
	}

	return nil
}

// ShouldTrace determines if a request should be traced based on sampling rate
// Uses a simple deterministic approach for now
func (c *TracingConfig) ShouldTrace() bool {
	// For 100% sampling, always trace
	if c.SamplingRate >= 1.0 {
		return true
	}

	// For 0% sampling, never trace
	if c.SamplingRate <= 0.0 {
		return false
	}

	// TODO: Implement proper sampling logic
	// For now, if sampling rate is set, we trace everything
	// This can be enhanced with probabilistic sampling later
	return true
}

// IsEnabled checks if tracing is effectively enabled
func (c *TracingConfig) IsEnabled() bool {
	return c.SamplingRate > 0.0
}

// Global tracing config
var globalTracingConfig *TracingConfig

// SetGlobalTracingConfig sets the global tracing configuration
func SetGlobalTracingConfig(cfg *TracingConfig) {
	globalTracingConfig = cfg
}

// GetGlobalTracingConfig returns the global tracing configuration
// Returns nil if tracing is not configured
func GetGlobalTracingConfig() *TracingConfig {
	return globalTracingConfig
}

// IsTracingEnabled returns true if tracing is configured and enabled
func IsTracingEnabled() bool {
	if globalTracingConfig == nil {
		return false
	}
	return globalTracingConfig.IsEnabled()
}
