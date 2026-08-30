package catalog

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/everstacklabs/everstack/internal/commands"
)

// SyncCatalogCommand requests a full catalog sync from remote/local source
type SyncCatalogCommand struct {
	commands.BaseCommand
	Force       bool   `json:"force"`        // Force sync even if version matches
	Source      string `json:"source"`       // "remote" or "local"
	SourceURL   string `json:"source_url"`   // URL or path to catalog
	CatalogType string `json:"catalog_type"` // "providers", "models", "both"
}

func NewSyncCatalogCommand(force bool, source, sourceURL, catalogType, userID, traceID string) *SyncCatalogCommand {
	return &SyncCatalogCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		Force:       force,
		Source:      source,
		SourceURL:   sourceURL,
		CatalogType: catalogType,
	}
}

func (c SyncCatalogCommand) AggregateID() string { return "catalog" }
func (c SyncCatalogCommand) CommandType() string { return "SyncCatalog" }
func (c SyncCatalogCommand) Validate() error {
	if c.Source != "remote" && c.Source != "local" {
		return fmt.Errorf("source must be 'remote' or 'local'")
	}
	if c.SourceURL == "" {
		return fmt.Errorf("source_url cannot be empty")
	}
	if c.CatalogType != "providers" && c.CatalogType != "models" && c.CatalogType != "both" {
		return fmt.Errorf("catalog_type must be 'providers', 'models', or 'both'")
	}
	return nil
}

// UpsertProviderFromCatalogCommand adds/updates a provider from catalog sync
type UpsertProviderFromCatalogCommand struct {
	commands.BaseCommand
	ProviderName  string                 `json:"provider_name"`
	CatalogStatus string                 `json:"catalog_status"` // "available", "configured", "active"
	ProviderData  map[string]interface{} `json:"provider_data"`  // Full provider metadata
	IsNew         bool                   `json:"is_new"`         // True if this is a new provider
}

func NewUpsertProviderFromCatalogCommand(providerName, catalogStatus string, providerData map[string]interface{}, isNew bool, userID, traceID string) *UpsertProviderFromCatalogCommand {
	return &UpsertProviderFromCatalogCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		ProviderName:  providerName,
		CatalogStatus: catalogStatus,
		ProviderData:  providerData,
		IsNew:         isNew,
	}
}

func (c UpsertProviderFromCatalogCommand) AggregateID() string { return c.ProviderName }
func (c UpsertProviderFromCatalogCommand) CommandType() string { return "UpsertProviderFromCatalog" }
func (c UpsertProviderFromCatalogCommand) Validate() error {
	if c.ProviderName == "" {
		return fmt.Errorf("provider_name cannot be empty")
	}
	if c.CatalogStatus == "" {
		return fmt.Errorf("catalog_status cannot be empty")
	}
	return nil
}

// UpsertModelFromCatalogCommand adds/updates a model from catalog sync
type UpsertModelFromCatalogCommand struct {
	commands.BaseCommand
	ProviderName string                 `json:"provider_name"`
	ModelName    string                 `json:"model_name"`
	Freshness    string                 `json:"freshness"` // "new", "stable"
	Status       string                 `json:"status"`    // "available", "configured", "active"
	ModelData    map[string]interface{} `json:"model_data"`
	IsNew        bool                   `json:"is_new"` // True if this is a new model
}

func NewUpsertModelFromCatalogCommand(providerName, modelName, freshness, status string, modelData map[string]interface{}, isNew bool, userID, traceID string) *UpsertModelFromCatalogCommand {
	return &UpsertModelFromCatalogCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		ProviderName: providerName,
		ModelName:    modelName,
		Freshness:    freshness,
		Status:       status,
		ModelData:    modelData,
		IsNew:        isNew,
	}
}

func (c UpsertModelFromCatalogCommand) AggregateID() string {
	return c.ProviderName + "/" + c.ModelName
}
func (c UpsertModelFromCatalogCommand) CommandType() string { return "UpsertModelFromCatalog" }
func (c UpsertModelFromCatalogCommand) Validate() error {
	if c.ProviderName == "" {
		return fmt.Errorf("provider_name cannot be empty")
	}
	if c.ModelName == "" {
		return fmt.Errorf("model_name cannot be empty")
	}
	if c.Freshness != "new" && c.Freshness != "stable" {
		return fmt.Errorf("freshness must be 'new' or 'stable'")
	}
	return nil
}

// DeprecateProviderCommand marks a provider as deprecated (removed from catalog)
type DeprecateProviderCommand struct {
	commands.BaseCommand
	ProviderName string `json:"provider_name"`
	Reason       string `json:"reason"`
}

func NewDeprecateProviderCommand(providerName, reason, userID, traceID string) *DeprecateProviderCommand {
	return &DeprecateProviderCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		ProviderName: providerName,
		Reason:       reason,
	}
}

func (c DeprecateProviderCommand) AggregateID() string { return c.ProviderName }
func (c DeprecateProviderCommand) CommandType() string { return "DeprecateProvider" }
func (c DeprecateProviderCommand) Validate() error {
	if c.ProviderName == "" {
		return fmt.Errorf("provider_name cannot be empty")
	}
	return nil
}

// UpdateModelFreshnessCommand updates model freshness (new -> stable after 8 weeks)
type UpdateModelFreshnessCommand struct {
	commands.BaseCommand
	ProviderName string `json:"provider_name"`
	ModelName    string `json:"model_name"`
	OldFreshness string `json:"old_freshness"`
	NewFreshness string `json:"new_freshness"`
}

func NewUpdateModelFreshnessCommand(providerName, modelName, oldFreshness, newFreshness, userID, traceID string) *UpdateModelFreshnessCommand {
	return &UpdateModelFreshnessCommand{
		BaseCommand: commands.BaseCommand{
			ID:        uuid.New().String(),
			Timestamp: time.Now(),
			UserID:    userID,
			TraceID:   traceID,
		},
		ProviderName: providerName,
		ModelName:    modelName,
		OldFreshness: oldFreshness,
		NewFreshness: newFreshness,
	}
}

func (c UpdateModelFreshnessCommand) AggregateID() string { return c.ProviderName + "/" + c.ModelName }
func (c UpdateModelFreshnessCommand) CommandType() string { return "UpdateModelFreshness" }
func (c UpdateModelFreshnessCommand) Validate() error {
	if c.ProviderName == "" {
		return fmt.Errorf("provider_name cannot be empty")
	}
	if c.ModelName == "" {
		return fmt.Errorf("model_name cannot be empty")
	}
	return nil
}
