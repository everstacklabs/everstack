package model_discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DiscoveredModel represents a model discovered from a meta-provider's API
type DiscoveredModel struct {
	ID            string            `json:"id"`
	Name          string            `json:"name"`
	DisplayName   string            `json:"display_name"`
	Provider      string            `json:"provider"`
	Metadata      map[string]string `json:"metadata"`
	Description   string            `json:"description"`
	Capabilities  []string          `json:"capabilities"`
	ContextLength int64             `json:"context_length"`
	PricingInfo   string            `json:"pricing_info"`
}

// OllamaModelsResult represents the result of listing Ollama models
type OllamaModelsResult struct {
	Models  []DiscoveredModel
	Warning string // Optional warning message (e.g., local connection failed)
}

// Service provides model discovery functionality for meta-providers
type Service struct {
	httpClient *http.Client
}

// NewService creates a new model discovery service
func NewService() *Service {
	return &Service{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// SearchOpenRouterModels searches for models from OpenRouter
func (s *Service) SearchOpenRouterModels(ctx context.Context, apiKey, query string, limit int) ([]DiscoveredModel, error) {
	endpoint := "https://openrouter.ai/api/v1/models"

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add authorization header
	if apiKey != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch models: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("openrouter API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Data []struct {
			ID          string `json:"id"`
			Name        string `json:"name"`
			Description string `json:"description"`
			Pricing     struct {
				Prompt     float64 `json:"prompt,string"`
				Completion float64 `json:"completion,string"`
			} `json:"pricing"`
			ContextLength int64 `json:"context_length"`
			TopProvider   struct {
				MaxCompletionTokens int64 `json:"max_completion_tokens"`
			} `json:"top_provider"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	models := make([]DiscoveredModel, 0, len(result.Data))
	for i, model := range result.Data {
		if limit > 0 && i >= limit {
			break
		}

		// Filter by query if provided
		if query != "" {
			// Simple substring match for now
			// TODO: Implement better fuzzy matching
			if !contains(model.Name, query) && !contains(model.Description, query) {
				continue
			}
		}

		pricingJSON, _ := json.Marshal(map[string]interface{}{
			"input_cost_per_1m":  model.Pricing.Prompt,
			"output_cost_per_1m": model.Pricing.Completion,
		})

		discovered := DiscoveredModel{
			ID:            model.ID,
			Name:          model.ID,
			DisplayName:   model.Name,
			Provider:      "openrouter",
			Description:   model.Description,
			ContextLength: model.ContextLength,
			PricingInfo:   string(pricingJSON),
			Metadata: map[string]string{
				"context_length": fmt.Sprintf("%d", model.ContextLength),
				"max_tokens":     fmt.Sprintf("%d", model.TopProvider.MaxCompletionTokens),
			},
			Capabilities: []string{"chat", "completions"},
		}

		models = append(models, discovered)
	}

	return models, nil
}

// SearchHuggingFaceModels searches for models from Hugging Face Inference Providers
// This fetches the list of models available through the inference router, then filters by query
func (s *Service) SearchHuggingFaceModels(ctx context.Context, apiKey, query string, limit int) ([]DiscoveredModel, error) {
	// Step 1: Get all available models from the inference router
	endpoint := "https://router.huggingface.co/v1/models"

	req, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add authorization header if API key is provided
	// Note: The /v1/models endpoint may work without auth, but will show more models with auth
	if apiKey != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch models from HuggingFace Inference API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("huggingface inference API returned status %d: %s", resp.StatusCode, string(body))
	}

	// OpenAI-compatible response format
	var result struct {
		Object string `json:"object"`
		Data   []struct {
			ID      string `json:"id"`
			Object  string `json:"object"`
			Created int64  `json:"created"`
			OwnedBy string `json:"owned_by"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Step 2: Filter and format results
	models := make([]DiscoveredModel, 0)
	for _, model := range result.Data {
		// Filter by query if provided (case-insensitive substring match)
		if query != "" {
			if !containsIgnoreCase(model.ID, query) && !containsIgnoreCase(model.OwnedBy, query) {
				continue
			}
		}

		discovered := DiscoveredModel{
			ID:          model.ID,
			Name:        model.ID,
			DisplayName: model.ID,
			Provider:    "huggingface",
			Description: fmt.Sprintf("Available via HuggingFace Inference Providers (owned by %s)", model.OwnedBy),
			Metadata: map[string]string{
				"owned_by": model.OwnedBy,
				"object":   model.Object,
				"source":   "inference_router",
			},
			Capabilities: []string{"chat", "completions"},
		}

		models = append(models, discovered)

		// Apply limit if specified
		if limit > 0 && len(models) >= limit {
			break
		}
	}

	return models, nil
}

// ListOllamaModels lists available models from an Ollama instance
//
// For local Ollama, this fetches:
// 1. Local models from the local instance
// 2. Available cloud models from ollama.com (if API key provided)
//
// For direct cloud API (https://ollama.com), this fetches only cloud models
func (s *Service) ListOllamaModels(ctx context.Context, baseURL string, apiKey string, query string) (*OllamaModelsResult, error) {
	if baseURL == "" {
		baseURL = "http://localhost:11434"
	}

	isDirectCloud := baseURL == "https://ollama.com" || baseURL == "https://ollama.com/"

	// For direct cloud access, just list cloud models
	if isDirectCloud {
		models, err := s.listOllamaModelsFromEndpoint(ctx, baseURL, apiKey, true)
		if err != nil {
			return nil, err
		}
		return &OllamaModelsResult{
			Models:  s.filterOllamaModels(models, query),
			Warning: "",
		}, nil
	}

	// For local Ollama, fetch both local and available cloud models
	allModels := make([]DiscoveredModel, 0)
	var warning string

	// 1. Fetch local models (don't pass API key to local Ollama for listing)
	localModels, localErr := s.listOllamaModelsFromEndpoint(ctx, baseURL, "", false)
	if localErr == nil {
		allModels = append(allModels, localModels...)
	} else {
		// Store the warning but continue to fetch cloud models
		warning = localErr.Error()
	}

	// 2. Fetch available cloud models (if API key provided)
	if apiKey != "" {
		cloudModels, err := s.listOllamaModelsFromEndpoint(ctx, "https://ollama.com", apiKey, true)
		if err != nil {
			// Don't fail if cloud models can't be fetched - just log and continue
			// This allows users to still see local models even if cloud API is down
			// or API key is invalid
		} else {
			// Filter out cloud models that are already pulled locally
			localModelNames := make(map[string]bool)
			for _, m := range localModels {
				localModelNames[m.Name] = true
			}

			for _, cloudModel := range cloudModels {
				// Only add cloud models that aren't already available locally
				if !localModelNames[cloudModel.Name] {
					allModels = append(allModels, cloudModel)
				}
			}
		}
	}

	// If we couldn't fetch any models (both local and cloud failed), return the local error
	if len(allModels) == 0 {
		if localErr != nil {
			return nil, localErr
		}
		// If local succeeded but returned no models, and cloud failed/not configured
		return &OllamaModelsResult{
			Models:  []DiscoveredModel{},
			Warning: warning,
		}, nil
	}

	// Apply search filter if query provided
	return &OllamaModelsResult{
		Models:  s.filterOllamaModels(allModels, query),
		Warning: warning,
	}, nil
}

// filterOllamaModels filters Ollama models by query string
func (s *Service) filterOllamaModels(models []DiscoveredModel, query string) []DiscoveredModel {
	if query == "" {
		return models
	}

	filtered := make([]DiscoveredModel, 0)
	for _, model := range models {
		// Case-insensitive search in model name and description
		if containsIgnoreCase(model.Name, query) ||
			containsIgnoreCase(model.DisplayName, query) ||
			containsIgnoreCase(model.Description, query) {
			filtered = append(filtered, model)
		}
	}
	return filtered
}

// listOllamaModelsFromEndpoint fetches models from a specific Ollama endpoint
func (s *Service) listOllamaModelsFromEndpoint(ctx context.Context, baseURL string, apiKey string, isCloudEndpoint bool) ([]DiscoveredModel, error) {
	endpoint := fmt.Sprintf("%s/api/tags", baseURL)

	// Create context with 10 second timeout
	timeoutCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(timeoutCtx, "GET", endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Add authorization header if API key is provided
	// For local Ollama: Optional (enables cloud model access)
	// For direct cloud API: Required
	if apiKey != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		// Check if it's a timeout or connection error
		if timeoutCtx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("connection to Ollama timed out. Please ensure Ollama is running at %s", baseURL)
		}
		// Check for connection refused or similar errors
		if isConnectionError(err) {
			return nil, fmt.Errorf("cannot connect to Ollama at %s. Please start the Ollama server to use local models", baseURL)
		}
		return nil, fmt.Errorf("failed to fetch models from Ollama: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("ollama API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		Models []struct {
			Name       string `json:"name"`
			Model      string `json:"model"`
			ModifiedAt string `json:"modified_at"`
			Size       int64  `json:"size"`
			Digest     string `json:"digest"`
			Details    struct {
				Format            string   `json:"format"`
				Family            string   `json:"family"`
				Families          []string `json:"families"`
				ParameterSize     string   `json:"parameter_size"`
				QuantizationLevel string   `json:"quantization_level"`
			} `json:"details"`
		} `json:"models"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// If no models found, return empty array (not an error)
	if len(result.Models) == 0 {
		return []DiscoveredModel{}, nil
	}

	models := make([]DiscoveredModel, 0, len(result.Models))
	for _, model := range result.Models {
		// Detect cloud models by the -cloud suffix
		hasCloudSuffix := strings.HasSuffix(model.Name, "-cloud")

		var description string
		var source string
		var badge string

		if isCloudEndpoint {
			// Models from ollama.com API - available cloud models
			description = fmt.Sprintf("☁️ Cloud Model: %s (%s) - Available to pull", model.Details.Family, model.Details.ParameterSize)
			source = "cloud-available"
			badge = "☁️ Available"
		} else if hasCloudSuffix {
			// Cloud model that's already pulled locally
			description = fmt.Sprintf("☁️ Cloud Model: %s (%s) - Pulled locally", model.Details.Family, model.Details.ParameterSize)
			source = "cloud-local"
			badge = "☁️ Local"
		} else {
			// Regular local model
			description = fmt.Sprintf("💻 Local Model: %s (%s)", model.Details.Family, model.Details.ParameterSize)
			source = "local"
			badge = "💻 Local"
		}

		discovered := DiscoveredModel{
			ID:          model.Name,
			Name:        model.Name,
			DisplayName: model.Name,
			Provider:    "ollama",
			Description: description,
			Metadata: map[string]string{
				"size":               fmt.Sprintf("%d", model.Size),
				"family":             model.Details.Family,
				"parameter_size":     model.Details.ParameterSize,
				"quantization_level": model.Details.QuantizationLevel,
				"format":             model.Details.Format,
				"source":             source,
				"badge":              badge,
			},
			Capabilities: []string{"chat", "completions"},
		}

		models = append(models, discovered)
	}

	return models, nil
}

// contains checks if a string contains a substring (case-insensitive)
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr ||
			len(s) > len(substr) &&
				(s[:len(substr)] == substr ||
					s[len(s)-len(substr):] == substr ||
					findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// containsIgnoreCase checks if a string contains a substring (case-insensitive)
func containsIgnoreCase(s, substr string) bool {
	// Simple case-insensitive check by converting to lowercase
	sLower := toLower(s)
	substrLower := toLower(substr)
	return contains(sLower, substrLower)
}

// toLower converts a string to lowercase (simple ASCII implementation)
func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			result[i] = c + 32
		} else {
			result[i] = c
		}
	}
	return string(result)
}

// isConnectionError checks if an error is a connection-related error
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	// Check for common connection error patterns
	return contains(errStr, "connection refused") ||
		contains(errStr, "no such host") ||
		contains(errStr, "connection reset") ||
		contains(errStr, "connection timeout") ||
		contains(errStr, "context canceled") ||
		contains(errStr, "dial tcp") ||
		contains(errStr, "network is unreachable")
}
