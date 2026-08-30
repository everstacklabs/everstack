package runtime_config

import (
	"encoding/json"
	"time"
)

const (
	// EventTypeRuntimeConfigUpdated is the event type for runtime config updates
	EventTypeRuntimeConfigUpdated = "runtime_config.updated"
	// EventTypeRuntimeConfigReset is the event type for runtime config resets
	EventTypeRuntimeConfigReset = "runtime_config.reset"
)

// RuntimeConfigUpdatedEvent is published when a runtime config section is updated
type RuntimeConfigUpdatedEvent struct {
	TenantID  string          `json:"tenant_id"`
	Section   string          `json:"section"`
	Config    json.RawMessage `json:"config"`
	Version   int             `json:"version"`
	UpdatedBy string          `json:"updated_by,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
}

// Event returns the event type identifier
func (e RuntimeConfigUpdatedEvent) Event() string {
	return EventTypeRuntimeConfigUpdated
}

// RuntimeConfigResetEvent is published when a runtime config section is reset to defaults
type RuntimeConfigResetEvent struct {
	TenantID  string    `json:"tenant_id"`
	Section   string    `json:"section"`
	Version   int       `json:"version"`
	ResetBy   string    `json:"reset_by,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Event returns the event type identifier
func (e RuntimeConfigResetEvent) Event() string {
	return EventTypeRuntimeConfigReset
}
