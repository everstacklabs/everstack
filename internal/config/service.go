package config

import (
	"fmt"
	"os"

	"github.com/everstacklabs/everstack/internal/config/validator"
	"github.com/everstacklabs/everstack/internal/lib/logger"
	"gopkg.in/yaml.v3"
)

// Service provides configuration validation and management functionality
type Service struct {
	validator *validator.ConfigValidator
	schemas   map[string]map[string]interface{}
}

// NewService creates a new configuration service
func NewService() *Service {
	return &Service{}
}

// LoadSchemasFromFiles loads JSON schemas from file paths
func (s *Service) LoadSchemasFromFiles(schemaFiles map[string]string) error {
	schemas := make(map[string]map[string]interface{})

	for name, filePath := range schemaFiles {
		// Read schema file
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("failed to read schema file %s: %w", filePath, err)
		}

		// Parse JSON schema
		var schema map[string]interface{}
		if err := yaml.Unmarshal(data, &schema); err != nil {
			return fmt.Errorf("failed to parse schema %s: %w", name, err)
		}

		schemas[name] = schema
	}

	s.schemas = schemas
	s.validator = validator.NewConfigValidator(schemas)

	logger.Info("Configuration service initialized with schemas", "schema_count", len(schemas))
	return nil
}

// LoadSchemasFromData loads JSON schemas from raw data
func (s *Service) LoadSchemasFromData(schemaData map[string][]byte) error {
	schemas := make(map[string]map[string]interface{})

	for name, data := range schemaData {
		// Parse JSON schema
		var schema map[string]interface{}
		if err := yaml.Unmarshal(data, &schema); err != nil {
			return fmt.Errorf("failed to parse schema %s: %w", name, err)
		}

		schemas[name] = schema
	}

	s.schemas = schemas
	s.validator = validator.NewConfigValidator(schemas)

	logger.Info("Configuration service initialized with schemas", "schema_count", len(schemas))
	return nil
}

// ValidateYAML validates a YAML configuration string against schemas
func (s *Service) ValidateYAML(yamlConfig string) (*validator.ValidationResult, error) {
	if s.validator == nil {
		return nil, fmt.Errorf("configuration service not initialized with schemas")
	}

	// Parse YAML into map
	var config map[string]interface{}
	if err := yaml.Unmarshal([]byte(yamlConfig), &config); err != nil {
		return &validator.ValidationResult{
			Valid: false,
			Errors: []validator.ValidationError{
				{
					Field:   "yaml",
					Message: fmt.Sprintf("invalid YAML format: %v", err),
					Code:    "INVALID_YAML",
				},
			},
		}, nil
	}

	// Validate the configuration
	result := s.validator.ValidateConfig(config)
	return result, nil
}

// ValidateMap validates a configuration map against schemas
func (s *Service) ValidateMap(config map[string]interface{}) (*validator.ValidationResult, error) {
	if s.validator == nil {
		return nil, fmt.Errorf("configuration service not initialized with schemas")
	}

	result := s.validator.ValidateConfig(config)
	return result, nil
}

// ValidateSection validates a specific configuration section
func (s *Service) ValidateSection(section string, config map[string]interface{}) (*validator.ValidationResult, error) {
	if s.validator == nil {
		return nil, fmt.Errorf("configuration service not initialized with schemas")
	}

	result := s.validator.ValidateSection(section, config)
	return result, nil
}

// GetSchemas returns the loaded schemas
func (s *Service) GetSchemas() map[string]map[string]interface{} {
	return s.schemas
}

// GetSchema returns a specific schema by name
func (s *Service) GetSchema(name string) (map[string]interface{}, error) {
	if s.schemas == nil {
		return nil, fmt.Errorf("no schemas loaded")
	}

	schema, exists := s.schemas[name]
	if !exists {
		return nil, fmt.Errorf("schema '%s' not found", name)
	}

	return schema, nil
}

// GetValidationSchema returns a validation schema for the configuration
func (s *Service) GetValidationSchema() map[string]interface{} {
	// Return the main config schema if available
	if schema, err := s.GetSchema("config"); err == nil {
		return schema
	}

	// Fallback to basic schema if main schema not found
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"grpc": map[string]interface{}{
				"type":        "object",
				"description": "gRPC server configuration",
			},
			"log": map[string]interface{}{
				"type":        "object",
				"description": "Logging configuration",
			},
			"gateway": map[string]interface{}{
				"type":        "object",
				"description": "Gateway configuration",
			},
			"secret_manager": map[string]interface{}{
				"type":        "object",
				"description": "Secret manager configuration",
			},
			"database": map[string]interface{}{
				"type":        "object",
				"description": "Database configuration",
			},
			"cache": map[string]interface{}{
				"type":        "object",
				"description": "Cache configuration",
			},
			"backup": map[string]interface{}{
				"type":        "object",
				"description": "Backup configuration",
			},
			"alerts": map[string]interface{}{
				"type":        "object",
				"description": "Alerts configuration",
			},
			"features": map[string]interface{}{
				"type":        "object",
				"description": "Feature flags",
			},
		},
		"required": []string{"grpc", "log", "gateway"},
	}
}

// GetSchemaAsJSON returns a schema as JSON string
func (s *Service) GetSchemaAsJSON(name string) (string, error) {
	schema, err := s.GetSchema(name)
	if err != nil {
		return "", err
	}

	// Convert schema to JSON
	data, err := yaml.Marshal(schema)
	if err != nil {
		return "", fmt.Errorf("failed to marshal schema to JSON: %w", err)
	}

	return string(data), nil
}

// ListAvailableSchemas returns a list of available schema names
func (s *Service) ListAvailableSchemas() []string {
	if s.schemas == nil {
		return []string{}
	}

	names := make([]string, 0, len(s.schemas))
	for name := range s.schemas {
		names = append(names, name)
	}
	return names
}

// GetValidationStatus returns the current validation status
func (s *Service) GetValidationStatus() *validator.ValidationStatus {
	if s.validator == nil {
		return &validator.ValidationStatus{State: validator.ValidationStatePending}
	}
	return s.validator.GetValidationStatus()
}

// GetValidationHistory returns the validation history
func (s *Service) GetValidationHistory(limit int) []validator.ValidationHistoryEntry {
	if s.validator == nil {
		return []validator.ValidationHistoryEntry{}
	}
	return s.validator.GetValidationHistory(limit)
}

// ClearValidationHistory clears the validation history
func (s *Service) ClearValidationHistory() {
	if s.validator != nil {
		s.validator.ClearHistory()
	}
}

// ValidateAndMerge validates a configuration and merges it with defaults
func (s *Service) ValidateAndMerge(userConfig map[string]interface{}) (*validator.ValidationResult, map[string]interface{}, error) {
	// First validate the user configuration
	result, err := s.ValidateMap(userConfig)
	if err != nil {
		return nil, nil, err
	}

	// If validation passes, return the config as-is (no merging for now)
	var mergedConfig map[string]interface{}
	if result.Valid {
		mergedConfig = userConfig
	}

	return result, mergedConfig, nil
}
