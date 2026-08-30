package provider

import "time"

// ConfigureProviderCommand represents a command to configure a provider.
// TenantID is required: the row written by this command is keyed on
// (organization_id, provider_name) since the 2026-05-06 migration. With
// organization_id NULL the new partial unique index doesn't apply, and
// the legacy ON CONFLICT (provider_name) target no longer exists — both
// fixed by routing the write through UpsertForOrg.
type ConfigureProviderCommand struct {
	TenantID       string
	ProviderName   string
	APIKey         string
	APIKeyName     string
	APIKeyWeight   int
	EnabledModels  []string
	CustomBaseURL  *string
	CustomSettings map[string]string
	UserID         string
	TraceID        string
	Timestamp      time.Time
}

// AddProviderAPIKeyCommand represents a command to add an API key to a provider
type AddProviderAPIKeyCommand struct {
	ProviderConfigID string
	KeyName          string
	APIKey           string
	Weight           int
	UserID           string
	TraceID          string
	Timestamp        time.Time
}

// UpdateAPIKeyWeightCommand represents a command to update an API key's weight
type UpdateAPIKeyWeightCommand struct {
	KeyID     string
	Weight    int
	UserID    string
	TraceID   string
	Timestamp time.Time
}

// ToggleAPIKeyCommand represents a command to activate/deactivate an API key
type ToggleAPIKeyCommand struct {
	KeyID     string
	IsActive  bool
	UserID    string
	TraceID   string
	Timestamp time.Time
}

// DeleteProviderAPIKeyCommand represents a command to delete an API key
type DeleteProviderAPIKeyCommand struct {
	KeyID     string
	UserID    string
	TraceID   string
	Timestamp time.Time
}

// ToggleProviderCommand represents a command to activate/deactivate a
// provider. TenantID is required so the write hits UpsertForOrg.
type ToggleProviderCommand struct {
	TenantID     string
	ProviderName string
	IsActive     bool
	UserID       string
	TraceID      string
	Timestamp    time.Time
}

// DeleteProviderCommand represents a command to delete a provider
// configuration. TenantID is required so DeleteForOrg removes only this
// tenant's row.
type DeleteProviderCommand struct {
	TenantID     string
	ProviderName string
	UserID       string
	TraceID      string
	Timestamp    time.Time
}
