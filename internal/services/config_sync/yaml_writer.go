package config_sync

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/everstacklabs/everstack/internal/domain/provider_config"
	"gopkg.in/yaml.v3"
)

// SyncToYAML writes provider configurations to the gateway.models[] section of a YAML file
// It preserves existing YAML structure and only updates the models section
func SyncToYAML(configPath string, configs []*provider_config.Configuration) error {
	// Read the existing YAML file
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	// Parse YAML into a generic structure
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("failed to parse YAML: %w", err)
	}

	// Find or create the gateway.models node
	gatewayNode, err := findOrCreateNode(&root, "gateway")
	if err != nil {
		return fmt.Errorf("failed to find/create gateway node: %w", err)
	}

	// Convert configurations to model configs
	modelConfigs := configsToModels(configs)

	// Create models YAML node
	modelsNode, err := createModelsNode(modelConfigs)
	if err != nil {
		return fmt.Errorf("failed to create models node: %w", err)
	}

	// Update or add models in gateway node
	if err := setNodeField(gatewayNode, "models", modelsNode); err != nil {
		return fmt.Errorf("failed to set models field: %w", err)
	}

	// Clean up load_balancer.weights to remove deleted providers
	if err := cleanupLoadBalancerWeights(gatewayNode, configs); err != nil {
		return fmt.Errorf("failed to cleanup load_balancer weights: %w", err)
	}

	// Write to temporary file first (atomic write)
	tempPath := configPath + ".tmp"
	output, err := yaml.Marshal(&root)
	if err != nil {
		return fmt.Errorf("failed to marshal YAML: %w", err)
	}

	if err := os.WriteFile(tempPath, output, 0644); err != nil {
		return fmt.Errorf("failed to write temp file: %w", err)
	}

	// Atomic rename
	if err := os.Rename(tempPath, configPath); err != nil {
		// Clean up temp file on error
		_ = os.Remove(tempPath)
		return fmt.Errorf("failed to rename temp file: %w", err)
	}

	return nil
}

// ModelConfig represents a gateway model configuration for YAML output
type ModelConfig struct {
	Provider         string                       `yaml:"provider"`
	Model            []string                     `yaml:"model"`
	APIKey           string                       `yaml:"api_key"`
	BaseURL          string                       `yaml:"base_url,omitempty"`
	MaxTokens        int                          `yaml:"max_tokens,omitempty"`
	Default          bool                         `yaml:"default,omitempty"`
	DefaultAlias     string                       `yaml:"default_alias,omitempty"`
	Temperature      float64                      `yaml:"temperature,omitempty"`
	TopP             float64                      `yaml:"top_p,omitempty"`
	FrequencyPenalty float64                      `yaml:"frequency_penalty,omitempty"`
	PresencePenalty  float64                      `yaml:"presence_penalty,omitempty"`
	Stop             []string                     `yaml:"stop,omitempty"`
	ModelParameters  map[string]map[string]string `yaml:"model_parameters,omitempty"`
	RateLimit        *RateLimitConfig             `yaml:"rate_limit,omitempty"`
}

// RateLimitConfig for YAML output
type RateLimitConfig struct {
	Enabled           bool   `yaml:"enabled"`
	RequestsPerMinute int    `yaml:"requests_per_minute"`
	Burst             int    `yaml:"burst"`
	KeySource         string `yaml:"key_source"`
}

// configsToModels converts provider configurations to model configs for YAML
func configsToModels(configs []*provider_config.Configuration) []ModelConfig {
	models := make([]ModelConfig, 0, len(configs))

	for _, config := range configs {
		// Only include active providers in YAML
		if !config.IsActive {
			continue
		}

		baseURL := ""
		if config.CustomBaseURL != nil {
			baseURL = *config.CustomBaseURL
		}

		// Determine API key value
		apiKey := config.APIKeyEncrypted

		// If key was added from UI (not from YAML), use env variable placeholder
		if config.APIKeySource == "ui" && apiKey != "" {
			// Convert provider name to uppercase for env variable
			providerUpper := strings.ToUpper(config.ProviderName)
			apiKey = fmt.Sprintf("${%s_API_KEY}", providerUpper)
		}

		model := ModelConfig{
			Provider: config.ProviderName,
			Model:    config.EnabledModels,
			APIKey:   apiKey,
			BaseURL:  baseURL,
		}

		// Add custom settings if present
		if val, ok := config.CustomSettings["max_tokens"]; ok {
			fmt.Sscanf(val, "%d", &model.MaxTokens)
		}
		if val, ok := config.CustomSettings["default"]; ok {
			model.Default = val == "true"
		}
		if val, ok := config.CustomSettings["default_alias"]; ok {
			model.DefaultAlias = val
		}
		if val, ok := config.CustomSettings["temperature"]; ok {
			fmt.Sscanf(val, "%f", &model.Temperature)
		}
		if val, ok := config.CustomSettings["top_p"]; ok {
			fmt.Sscanf(val, "%f", &model.TopP)
		}
		if val, ok := config.CustomSettings[modelParametersSetting]; ok {
			_ = json.Unmarshal([]byte(val), &model.ModelParameters)
		}

		models = append(models, model)
	}

	return models
}

const modelParametersSetting = "model_parameters"

// findOrCreateNode finds or creates a node in the YAML tree
func findOrCreateNode(root *yaml.Node, key string) (*yaml.Node, error) {
	// Root should be a document node
	if root.Kind != yaml.DocumentNode {
		return nil, fmt.Errorf("expected document node")
	}

	// Get the mapping node (first content)
	if len(root.Content) == 0 {
		// Create root mapping
		root.Content = []*yaml.Node{{Kind: yaml.MappingNode}}
	}

	mapping := root.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("expected mapping node")
	}

	// Search for key in mapping
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			// Found it - return the value node
			return mapping.Content[i+1], nil
		}
	}

	// Not found - create it
	keyNode := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Value: key,
	}
	valueNode := &yaml.Node{
		Kind: yaml.MappingNode,
	}

	mapping.Content = append(mapping.Content, keyNode, valueNode)
	return valueNode, nil
}

// setNodeField sets a field in a mapping node
func setNodeField(mapping *yaml.Node, key string, value *yaml.Node) error {
	if mapping.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node")
	}

	// Search for existing key
	for i := 0; i < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			// Replace existing value
			mapping.Content[i+1] = value
			return nil
		}
	}

	// Add new key-value pair
	keyNode := &yaml.Node{
		Kind:  yaml.ScalarNode,
		Value: key,
	}
	mapping.Content = append(mapping.Content, keyNode, value)
	return nil
}

// createModelsNode creates a YAML sequence node for models
func createModelsNode(models []ModelConfig) (*yaml.Node, error) {
	// Marshal models to YAML
	data, err := yaml.Marshal(models)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal models: %w", err)
	}

	// Parse back to node
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		return nil, fmt.Errorf("failed to unmarshal models node: %w", err)
	}

	// Return the actual sequence node (unwrap document node)
	if node.Kind == yaml.DocumentNode && len(node.Content) > 0 {
		return node.Content[0], nil
	}

	return &node, nil
}

// cleanupLoadBalancerWeights removes providers from load_balancer.weights that are no longer configured
func cleanupLoadBalancerWeights(gatewayNode *yaml.Node, configs []*provider_config.Configuration) error {
	if gatewayNode.Kind != yaml.MappingNode {
		return fmt.Errorf("expected mapping node")
	}

	// Build set of active provider names
	activeProviders := make(map[string]bool)
	for _, config := range configs {
		if config.IsActive {
			activeProviders[config.ProviderName] = true
		}
	}

	// Find load_balancer node
	var loadBalancerNode *yaml.Node
	for i := 0; i < len(gatewayNode.Content); i += 2 {
		if gatewayNode.Content[i].Value == "load_balancer" {
			loadBalancerNode = gatewayNode.Content[i+1]
			break
		}
	}

	// If no load_balancer node, nothing to clean up
	if loadBalancerNode == nil || loadBalancerNode.Kind != yaml.MappingNode {
		return nil
	}

	// Find weights node within load_balancer
	var weightsNode *yaml.Node
	for i := 0; i < len(loadBalancerNode.Content); i += 2 {
		if loadBalancerNode.Content[i].Value == "weights" {
			weightsNode = loadBalancerNode.Content[i+1]
			break
		}
	}

	// If no weights node, nothing to clean up
	if weightsNode == nil || weightsNode.Kind != yaml.MappingNode {
		return nil
	}

	// Filter out weights for providers that no longer exist
	newContent := make([]*yaml.Node, 0, len(weightsNode.Content))
	for i := 0; i < len(weightsNode.Content); i += 2 {
		providerName := weightsNode.Content[i].Value
		if activeProviders[providerName] {
			// Keep this provider weight
			newContent = append(newContent, weightsNode.Content[i], weightsNode.Content[i+1])
		}
		// Otherwise, skip it (effectively removing it)
	}

	// Update weights content
	weightsNode.Content = newContent

	return nil
}

// EnsureDirectoryExists ensures the directory for the config file exists
func EnsureDirectoryExists(configPath string) error {
	dir := filepath.Dir(configPath)
	return os.MkdirAll(dir, 0755)
}
