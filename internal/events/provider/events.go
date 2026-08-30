package provider

import "time"

// ProviderConfiguredEvent is emitted when a provider is configured
type ProviderConfiguredEvent struct {
	ProviderName   string
	ConfigID       string
	EnabledModels  []string
	CustomBaseURL  *string
	CustomSettings map[string]string
	UserID         string
	TraceID        string
	Timestamp      time.Time
}

// ProviderAPIKeyAddedEvent is emitted when an API key is added to a provider
type ProviderAPIKeyAddedEvent struct {
	KeyID            string
	ProviderConfigID string
	KeyName          string
	Weight           int
	IsActive         bool
	UserID           string
	TraceID          string
	Timestamp        time.Time
}

// ProviderAPIKeyWeightUpdatedEvent is emitted when an API key's weight is updated
type ProviderAPIKeyWeightUpdatedEvent struct {
	KeyID     string
	OldWeight int
	NewWeight int
	UserID    string
	TraceID   string
	Timestamp time.Time
}

// ProviderAPIKeyToggledEvent is emitted when an API key is activated/deactivated
type ProviderAPIKeyToggledEvent struct {
	KeyID     string
	IsActive  bool
	UserID    string
	TraceID   string
	Timestamp time.Time
}

// ProviderAPIKeyDeletedEvent is emitted when an API key is deleted
type ProviderAPIKeyDeletedEvent struct {
	KeyID            string
	ProviderConfigID string
	KeyName          string
	UserID           string
	TraceID          string
	Timestamp        time.Time
}

// ProviderToggledEvent is emitted when a provider is activated/deactivated
type ProviderToggledEvent struct {
	ProviderName string
	ConfigID     string
	IsActive     bool
	UserID       string
	TraceID      string
	Timestamp    time.Time
}

// ProviderDeletedEvent is emitted when a provider configuration is deleted
type ProviderDeletedEvent struct {
	ProviderName string
	ConfigID     string
	UserID       string
	TraceID      string
	Timestamp    time.Time
}

// ProviderConfigAPIKeySyncedEvent is published when a config-based API key is synced from YAML
type ProviderConfigAPIKeySyncedEvent struct {
	KeyID            string
	ProviderConfigID string
	ProviderName     string
	KeyName          string
	Weight           int
	IsActive         bool
	UserID           string
	TraceID          string
	Timestamp        time.Time
}

// ConfigReloadedEvent is published when config is reloaded from YAML
type ConfigReloadedEvent struct {
	ProvidersSynced int
	UserID          string
	TraceID         string
	Timestamp       time.Time
}

// Event returns the event name for each event type
func (e ProviderConfiguredEvent) Event() string          { return "provider.configured" }
func (e ProviderAPIKeyAddedEvent) Event() string         { return "provider.api_key.added" }
func (e ProviderAPIKeyWeightUpdatedEvent) Event() string { return "provider.api_key.weight_updated" }
func (e ProviderAPIKeyToggledEvent) Event() string       { return "provider.api_key.toggled" }
func (e ProviderAPIKeyDeletedEvent) Event() string       { return "provider.api_key.deleted" }
func (e ProviderToggledEvent) Event() string             { return "provider.toggled" }
func (e ProviderDeletedEvent) Event() string             { return "provider.deleted" }
func (e ProviderConfigAPIKeySyncedEvent) Event() string  { return "provider.config_api_key.synced" }
func (e ConfigReloadedEvent) Event() string              { return "provider.config.reloaded" }
