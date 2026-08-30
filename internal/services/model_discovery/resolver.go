package model_discovery

import (
	"context"
	"strings"

	"github.com/everstacklabs/everstack/internal/domain/custom_models"
)

// CustomModelResolverService implements the gateway.CustomModelResolver interface
type CustomModelResolverService struct {
	repo *custom_models.Repository
}

// NewCustomModelResolverService creates a new custom model resolver
func NewCustomModelResolverService(repo *custom_models.Repository) *CustomModelResolverService {
	return &CustomModelResolverService{repo: repo}
}

// ResolveCustomModel attempts to resolve a model name to a custom model configuration
// Returns: providerName, customModelName, found, error
func (s *CustomModelResolverService) ResolveCustomModel(ctx context.Context, modelName string) (string, string, bool, error) {
	if s.repo == nil {
		return "", "", false, nil
	}

	// Get all active custom models
	models, err := s.repo.List(ctx)
	if err != nil {
		return "", "", false, err
	}

	// Look for a matching model (case-insensitive)
	lowerModelName := strings.ToLower(modelName)
	for _, model := range models {
		if !model.IsActive {
			continue
		}

		// Check both model_name and display_name for matches
		if strings.ToLower(model.ModelName) == lowerModelName ||
			strings.ToLower(model.DisplayName) == lowerModelName {
			return model.ProviderName, model.ModelName, true, nil
		}
	}

	return "", "", false, nil
}
