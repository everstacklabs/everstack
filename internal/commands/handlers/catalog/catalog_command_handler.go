package catalog

import (
	"context"
	"fmt"

	"github.com/everstacklabs/everstack/internal/commands"
	"github.com/everstacklabs/everstack/internal/database"
	"github.com/everstacklabs/everstack/internal/domain/provider_config"
)

// BaseCatalogCommandHandler provides shared logic for catalog commands
type BaseCatalogCommandHandler struct {
	providerRepo *provider_config.Repository
	modelRepo    *provider_config.ModelRepository
}

// UpsertProviderHandler handles UpsertProviderFromCatalog commands
type UpsertProviderHandler struct {
	*BaseCatalogCommandHandler
}

func (h *UpsertProviderHandler) CommandType() string { return "UpsertProviderFromCatalog" }
func (h *UpsertProviderHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	return h.handleUpsertProviderFromCatalog(ctx, cmd.(*UpsertProviderFromCatalogCommand))
}

// UpsertModelHandler handles UpsertModelFromCatalog commands
type UpsertModelHandler struct {
	*BaseCatalogCommandHandler
}

func (h *UpsertModelHandler) CommandType() string { return "UpsertModelFromCatalog" }
func (h *UpsertModelHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	return h.handleUpsertModelFromCatalog(ctx, cmd.(*UpsertModelFromCatalogCommand))
}

// DeprecateProviderHandler handles DeprecateProvider commands
type DeprecateProviderHandler struct {
	*BaseCatalogCommandHandler
}

func (h *DeprecateProviderHandler) CommandType() string { return "DeprecateProvider" }
func (h *DeprecateProviderHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	return h.handleDeprecateProvider(ctx, cmd.(*DeprecateProviderCommand))
}

// UpdateModelFreshnessHandler handles UpdateModelFreshness commands
type UpdateModelFreshnessHandler struct {
	*BaseCatalogCommandHandler
}

func (h *UpdateModelFreshnessHandler) CommandType() string { return "UpdateModelFreshness" }
func (h *UpdateModelFreshnessHandler) Handle(ctx context.Context, cmd commands.Command) ([]database.Event, error) {
	return h.handleUpdateModelFreshness(ctx, cmd.(*UpdateModelFreshnessCommand))
}

// NewCatalogCommandHandlers creates all catalog command handlers
func NewCatalogCommandHandlers(providerRepo *provider_config.Repository, modelRepo *provider_config.ModelRepository) []commands.CommandHandler {
	base := &BaseCatalogCommandHandler{
		providerRepo: providerRepo,
		modelRepo:    modelRepo,
	}

	return []commands.CommandHandler{
		&UpsertProviderHandler{base},
		&UpsertModelHandler{base},
		&DeprecateProviderHandler{base},
		&UpdateModelFreshnessHandler{base},
	}
}

// handleUpsertProviderFromCatalog processes provider upsert from catalog
func (h *BaseCatalogCommandHandler) handleUpsertProviderFromCatalog(ctx context.Context, cmd *UpsertProviderFromCatalogCommand) ([]database.Event, error) {
	// Check if provider exists
	existing, err := h.providerRepo.Get(ctx, cmd.ProviderName)
	isUpdate := err == nil && existing != nil

	// Upsert provider
	if err := h.providerRepo.UpsertFromCatalog(ctx, cmd.ProviderName, cmd.CatalogStatus); err != nil {
		return nil, fmt.Errorf("failed to upsert provider: %w", err)
	}

	// Emit appropriate event
	var events []database.Event
	if isUpdate {
		events = append(events, NewProviderUpdatedFromCatalogEvent(
			cmd.ProviderName,
			"", // catalog version - to be filled by sync orchestrator
			[]string{"catalog_status"},
			cmd.ProviderData,
		))
	} else {
		events = append(events, NewProviderAddedFromCatalogEvent(
			cmd.ProviderName,
			cmd.CatalogStatus,
			"", // catalog version - to be filled by sync orchestrator
			cmd.ProviderData,
		))
	}

	return events, nil
}

// handleUpsertModelFromCatalog processes model upsert from catalog
func (h *BaseCatalogCommandHandler) handleUpsertModelFromCatalog(ctx context.Context, cmd *UpsertModelFromCatalogCommand) ([]database.Event, error) {
	// Upsert model
	if err := h.modelRepo.UpsertModel(ctx, cmd.ProviderName, cmd.ModelName, cmd.IsNew); err != nil {
		return nil, fmt.Errorf("failed to upsert model: %w", err)
	}

	// Emit event
	var events []database.Event
	if cmd.IsNew {
		events = append(events, NewModelAddedFromCatalogEvent(
			cmd.ProviderName,
			cmd.ModelName,
			cmd.Freshness,
			cmd.Status,
			cmd.ModelData,
		))
	}

	return events, nil
}

// handleDeprecateProvider processes provider deprecation
func (h *BaseCatalogCommandHandler) handleDeprecateProvider(ctx context.Context, cmd *DeprecateProviderCommand) ([]database.Event, error) {
	// Mark provider as deprecated
	if err := h.providerRepo.MarkDeprecated(ctx, cmd.ProviderName); err != nil {
		return nil, fmt.Errorf("failed to deprecate provider: %w", err)
	}

	// Emit event
	events := []database.Event{
		NewProviderDeprecatedEvent(cmd.ProviderName, cmd.Reason),
	}

	return events, nil
}

// handleUpdateModelFreshness processes model freshness update
func (h *BaseCatalogCommandHandler) handleUpdateModelFreshness(ctx context.Context, cmd *UpdateModelFreshnessCommand) ([]database.Event, error) {
	// Update freshness via repository (bulk update)
	// Note: This is typically called by a cron job for multiple models
	// For individual updates, we'd need a different approach

	// Emit event
	events := []database.Event{
		NewModelFreshnessUpdatedEvent(
			cmd.ProviderName,
			cmd.ModelName,
			cmd.OldFreshness,
			cmd.NewFreshness,
			"Age threshold exceeded (8 weeks)",
		),
	}

	return events, nil
}
