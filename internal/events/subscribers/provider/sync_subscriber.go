package provider

import (
	"context"
	"fmt"

	"github.com/everstacklabs/everstack/internal/events"
	providerEvents "github.com/everstacklabs/everstack/internal/events/provider"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"github.com/everstacklabs/everstack/internal/services/config_sync"
)

// SyncSubscriber subscribes to provider events and triggers YAML sync
type SyncSubscriber struct {
	syncWorker *config_sync.Worker
}

// NewSyncSubscriber creates a new sync subscriber
func NewSyncSubscriber(syncWorker *config_sync.Worker) *SyncSubscriber {
	return &SyncSubscriber{
		syncWorker: syncWorker,
	}
}

// Subscribe registers event handlers with the event bus
func (s *SyncSubscriber) Subscribe(bus events.Bus) {
	// Subscribe to provider configuration events
	bus.Subscribe("provider.configured", s.handleProviderConfigured)
	bus.Subscribe("provider.toggled", s.handleProviderToggled)
	bus.Subscribe("provider.deleted", s.handleProviderDeleted)

	// Subscribe to API key events
	bus.Subscribe("provider.api_key.added", s.handleAPIKeyAdded)
	bus.Subscribe("provider.api_key.weight_updated", s.handleAPIKeyWeightUpdated)
	bus.Subscribe("provider.api_key.toggled", s.handleAPIKeyToggled)
	bus.Subscribe("provider.api_key.deleted", s.handleAPIKeyDeleted)

	// Subscribe to config API key sync event
	bus.Subscribe("provider.config_api_key.synced", s.handleConfigAPIKeySynced)
}

// handleProviderConfigured handles the provider configured event
func (s *SyncSubscriber) handleProviderConfigured(ctx context.Context, event interface{}) error {
	e, ok := event.(providerEvents.ProviderConfiguredEvent)
	if !ok {
		return fmt.Errorf("invalid event type for provider.configured")
	}

	logger.WithFields(
		"provider_name", e.ProviderName,
		"config_id", e.ConfigID,
		"user_id", e.UserID,
		"trace_id", e.TraceID,
	).Debug("Provider configured")

	// REMOVED: Automatic YAML sync
	// Users will manually sync from config tab

	return nil
}

// handleProviderToggled handles the provider toggled event
func (s *SyncSubscriber) handleProviderToggled(ctx context.Context, event interface{}) error {
	_, ok := event.(providerEvents.ProviderToggledEvent)
	if !ok {
		return fmt.Errorf("invalid event type for provider.toggled")
	}
	return nil
}

// handleProviderDeleted handles the provider deleted event
func (s *SyncSubscriber) handleProviderDeleted(ctx context.Context, event interface{}) error {
	e, ok := event.(providerEvents.ProviderDeletedEvent)
	if !ok {
		return fmt.Errorf("invalid event type for provider.deleted")
	}

	logger.WithFields(
		"provider_name", e.ProviderName,
		"config_id", e.ConfigID,
		"user_id", e.UserID,
		"trace_id", e.TraceID,
	).Debug("Provider deleted")

	// REMOVED: Automatic YAML sync
	// Users will manually sync from config tab

	return nil
}

// handleAPIKeyAdded handles the API key added event
func (s *SyncSubscriber) handleAPIKeyAdded(ctx context.Context, event interface{}) error {
	e, ok := event.(providerEvents.ProviderAPIKeyAddedEvent)
	if !ok {
		return fmt.Errorf("invalid event type for provider.api_key.added")
	}

	logger.WithFields(
		"key_id", e.KeyID,
		"provider_config_id", e.ProviderConfigID,
		"key_name", e.KeyName,
		"weight", e.Weight,
		"user_id", e.UserID,
		"trace_id", e.TraceID,
	).Debug("API key added")

	// Note: We don't trigger YAML sync for API key changes since they're stored in DB only
	// YAML config uses environment variables for API keys

	return nil
}

// handleAPIKeyWeightUpdated handles the API key weight updated event
func (s *SyncSubscriber) handleAPIKeyWeightUpdated(ctx context.Context, event interface{}) error {
	e, ok := event.(providerEvents.ProviderAPIKeyWeightUpdatedEvent)
	if !ok {
		return fmt.Errorf("invalid event type for provider.api_key.weight_updated")
	}

	logger.WithFields(
		"key_id", e.KeyID,
		"old_weight", e.OldWeight,
		"new_weight", e.NewWeight,
		"user_id", e.UserID,
		"trace_id", e.TraceID,
	).Debug("API key weight updated")

	return nil
}

// handleAPIKeyToggled handles the API key toggled event
func (s *SyncSubscriber) handleAPIKeyToggled(ctx context.Context, event interface{}) error {
	e, ok := event.(providerEvents.ProviderAPIKeyToggledEvent)
	if !ok {
		return fmt.Errorf("invalid event type for provider.api_key.toggled")
	}

	logger.WithFields(
		"key_id", e.KeyID,
		"is_active", e.IsActive,
		"user_id", e.UserID,
		"trace_id", e.TraceID,
	).Debug("API key toggled")

	return nil
}

// handleAPIKeyDeleted handles the API key deleted event
func (s *SyncSubscriber) handleAPIKeyDeleted(ctx context.Context, event interface{}) error {
	e, ok := event.(providerEvents.ProviderAPIKeyDeletedEvent)
	if !ok {
		return fmt.Errorf("invalid event type for provider.api_key.deleted")
	}

	logger.WithFields(
		"key_id", e.KeyID,
		"provider_config_id", e.ProviderConfigID,
		"key_name", e.KeyName,
		"user_id", e.UserID,
		"trace_id", e.TraceID,
	).Debug("API key deleted")

	return nil
}

// handleConfigAPIKeySynced handles the config API key synced event
func (s *SyncSubscriber) handleConfigAPIKeySynced(ctx context.Context, event interface{}) error {
	e, ok := event.(providerEvents.ProviderConfigAPIKeySyncedEvent)
	if !ok {
		return fmt.Errorf("invalid event type for provider.config_api_key.synced")
	}

	logger.WithFields(
		"key_id", e.KeyID,
		"provider_config_id", e.ProviderConfigID,
		"provider_name", e.ProviderName,
		"key_name", e.KeyName,
		"user_id", e.UserID,
		"trace_id", e.TraceID,
	).Debug("Config API key synced from YAML")

	return nil
}
