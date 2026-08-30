package catalog

import (
	"encoding/json"
	"time"

	"github.com/everstacklabs/everstack/internal/database"
)

// Domain Events for Catalog Sync

// CatalogSyncStartedEvent emitted when catalog sync begins
type CatalogSyncStartedEvent struct {
	Source      string    `json:"source"`
	SourceURL   string    `json:"source_url"`
	CatalogType string    `json:"catalog_type"`
	Force       bool      `json:"force"`
	StartedAt   time.Time `json:"started_at"`
}

func NewCatalogSyncStartedEvent(source, sourceURL, catalogType string, force bool) database.Event {
	payload, _ := json.Marshal(CatalogSyncStartedEvent{
		Source:      source,
		SourceURL:   sourceURL,
		CatalogType: catalogType,
		Force:       force,
		StartedAt:   time.Now(),
	})

	return database.Event{
		ID:        generateEventID(),
		Type:      "CatalogSyncStarted",
		Stream:    "catalog",
		Payload:   payload,
		CreatedAt: time.Now().Unix(),
	}
}

// CatalogSyncCompletedEvent emitted when catalog sync completes successfully
type CatalogSyncCompletedEvent struct {
	Source          string    `json:"source"`
	OldVersion      string    `json:"old_version"`
	NewVersion      string    `json:"new_version"`
	NewProviders    int       `json:"new_providers"`
	NewModels       int       `json:"new_models"`
	UpdatedModels   int       `json:"updated_models"`
	DeprecatedCount int       `json:"deprecated_count"`
	CompletedAt     time.Time `json:"completed_at"`
	Duration        int64     `json:"duration_ms"`
}

func NewCatalogSyncCompletedEvent(source, oldVersion, newVersion string, newProviders, newModels, updatedModels, deprecatedCount int, duration int64) database.Event {
	payload, _ := json.Marshal(CatalogSyncCompletedEvent{
		Source:          source,
		OldVersion:      oldVersion,
		NewVersion:      newVersion,
		NewProviders:    newProviders,
		NewModels:       newModels,
		UpdatedModels:   updatedModels,
		DeprecatedCount: deprecatedCount,
		CompletedAt:     time.Now(),
		Duration:        duration,
	})

	return database.Event{
		ID:        generateEventID(),
		Type:      "CatalogSyncCompleted",
		Stream:    "catalog",
		Payload:   payload,
		CreatedAt: time.Now().Unix(),
	}
}

// CatalogSyncFailedEvent emitted when catalog sync fails
type CatalogSyncFailedEvent struct {
	Source   string    `json:"source"`
	Error    string    `json:"error"`
	FailedAt time.Time `json:"failed_at"`
	Duration int64     `json:"duration_ms"`
}

func NewCatalogSyncFailedEvent(source, errorMsg string, duration int64) database.Event {
	payload, _ := json.Marshal(CatalogSyncFailedEvent{
		Source:   source,
		Error:    errorMsg,
		FailedAt: time.Now(),
		Duration: duration,
	})

	return database.Event{
		ID:        generateEventID(),
		Type:      "CatalogSyncFailed",
		Stream:    "catalog",
		Payload:   payload,
		CreatedAt: time.Now().Unix(),
	}
}

// ProviderAddedFromCatalogEvent emitted when a new provider is added from catalog
type ProviderAddedFromCatalogEvent struct {
	ProviderName   string                 `json:"provider_name"`
	CatalogStatus  string                 `json:"catalog_status"`
	ProviderData   map[string]interface{} `json:"provider_data"`
	AddedAt        time.Time              `json:"added_at"`
	CatalogVersion string                 `json:"catalog_version"`
}

func NewProviderAddedFromCatalogEvent(providerName, catalogStatus, catalogVersion string, providerData map[string]interface{}) database.Event {
	payload, _ := json.Marshal(ProviderAddedFromCatalogEvent{
		ProviderName:   providerName,
		CatalogStatus:  catalogStatus,
		ProviderData:   providerData,
		AddedAt:        time.Now(),
		CatalogVersion: catalogVersion,
	})

	return database.Event{
		ID:        generateEventID(),
		Type:      "catalog.provider.added",
		Stream:    "provider." + providerName,
		Payload:   payload,
		CreatedAt: time.Now().Unix(),
	}
}

// ProviderUpdatedFromCatalogEvent emitted when a provider is updated from catalog
type ProviderUpdatedFromCatalogEvent struct {
	ProviderName   string                 `json:"provider_name"`
	UpdatedFields  []string               `json:"updated_fields"`
	ProviderData   map[string]interface{} `json:"provider_data"`
	UpdatedAt      time.Time              `json:"updated_at"`
	CatalogVersion string                 `json:"catalog_version"`
}

func NewProviderUpdatedFromCatalogEvent(providerName, catalogVersion string, updatedFields []string, providerData map[string]interface{}) database.Event {
	payload, _ := json.Marshal(ProviderUpdatedFromCatalogEvent{
		ProviderName:   providerName,
		UpdatedFields:  updatedFields,
		ProviderData:   providerData,
		UpdatedAt:      time.Now(),
		CatalogVersion: catalogVersion,
	})

	return database.Event{
		ID:        generateEventID(),
		Type:      "catalog.provider.updated",
		Stream:    "provider." + providerName,
		Payload:   payload,
		CreatedAt: time.Now().Unix(),
	}
}

// ProviderDeprecatedEvent emitted when a provider is deprecated
type ProviderDeprecatedEvent struct {
	ProviderName string    `json:"provider_name"`
	Reason       string    `json:"reason"`
	DeprecatedAt time.Time `json:"deprecated_at"`
}

func NewProviderDeprecatedEvent(providerName, reason string) database.Event {
	payload, _ := json.Marshal(ProviderDeprecatedEvent{
		ProviderName: providerName,
		Reason:       reason,
		DeprecatedAt: time.Now(),
	})

	return database.Event{
		ID:        generateEventID(),
		Type:      "ProviderDeprecated",
		Stream:    "provider:" + providerName,
		Payload:   payload,
		CreatedAt: time.Now().Unix(),
	}
}

// ModelAddedFromCatalogEvent emitted when a new model is added from catalog
type ModelAddedFromCatalogEvent struct {
	ProviderName string                 `json:"provider_name"`
	ModelName    string                 `json:"model_name"`
	Freshness    string                 `json:"freshness"`
	Status       string                 `json:"status"`
	ModelData    map[string]interface{} `json:"model_data"`
	AddedAt      time.Time              `json:"added_at"`
}

func NewModelAddedFromCatalogEvent(providerName, modelName, freshness, status string, modelData map[string]interface{}) database.Event {
	payload, _ := json.Marshal(ModelAddedFromCatalogEvent{
		ProviderName: providerName,
		ModelName:    modelName,
		Freshness:    freshness,
		Status:       status,
		ModelData:    modelData,
		AddedAt:      time.Now(),
	})

	return database.Event{
		ID:        generateEventID(),
		Type:      "ModelAddedFromCatalog",
		Stream:    "model:" + providerName + "/" + modelName,
		Payload:   payload,
		CreatedAt: time.Now().Unix(),
	}
}

// ModelFreshnessUpdatedEvent emitted when model freshness changes (new -> stable)
type ModelFreshnessUpdatedEvent struct {
	ProviderName string    `json:"provider_name"`
	ModelName    string    `json:"model_name"`
	OldFreshness string    `json:"old_freshness"`
	NewFreshness string    `json:"new_freshness"`
	UpdatedAt    time.Time `json:"updated_at"`
	Reason       string    `json:"reason"`
}

func NewModelFreshnessUpdatedEvent(providerName, modelName, oldFreshness, newFreshness, reason string) database.Event {
	payload, _ := json.Marshal(ModelFreshnessUpdatedEvent{
		ProviderName: providerName,
		ModelName:    modelName,
		OldFreshness: oldFreshness,
		NewFreshness: newFreshness,
		UpdatedAt:    time.Now(),
		Reason:       reason,
	})

	return database.Event{
		ID:        generateEventID(),
		Type:      "ModelFreshnessUpdated",
		Stream:    "model:" + providerName + "/" + modelName,
		Payload:   payload,
		CreatedAt: time.Now().Unix(),
	}
}

// ModelDeprecatedEvent emitted when a model is deprecated
type ModelDeprecatedEvent struct {
	ProviderName string    `json:"provider_name"`
	ModelName    string    `json:"model_name"`
	Reason       string    `json:"reason"`
	DeprecatedAt time.Time `json:"deprecated_at"`
}

func NewModelDeprecatedEvent(providerName, modelName, reason string) database.Event {
	payload, _ := json.Marshal(ModelDeprecatedEvent{
		ProviderName: providerName,
		ModelName:    modelName,
		Reason:       reason,
		DeprecatedAt: time.Now(),
	})

	return database.Event{
		ID:        generateEventID(),
		Type:      "ModelDeprecated",
		Stream:    "model:" + providerName + "/" + modelName,
		Payload:   payload,
		CreatedAt: time.Now().Unix(),
	}
}

// Helper function to generate event IDs
func generateEventID() string {
	return time.Now().Format("20060102150405") + "-" + randomString(8)
}

func randomString(n int) string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = letters[time.Now().UnixNano()%int64(len(letters))]
	}
	return string(b)
}
