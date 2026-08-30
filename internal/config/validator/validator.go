package validator

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ValidationResult represents the result of a validation operation
type ValidationResult struct {
	Valid         bool
	Errors        []ValidationError
	Warnings      []ValidationWarning
	Status        *ValidationStatus
	ValidatedAt   time.Time
	ConfigHash    string
	ValidatedSections []string
}

// ValidationError represents a validation error
type ValidationError struct {
	Field    string
	Message  string
	Code     string
	Severity ValidationSeverity
}

// ValidationWarning represents a validation warning
type ValidationWarning struct {
	Field   string
	Message string
	Code    string
}

// ValidationStatus represents the overall validation status
type ValidationStatus struct {
	State           ValidationState
	Message         string
	ErrorCount      int
	WarningCount    int
	LastValidated   time.Time
	ValidatedSections []string
	FailedSections  []string
	mu              sync.RWMutex
}

// ValidationState represents the current validation state
type ValidationState int

const (
	ValidationStateUnspecified ValidationState = iota
	ValidationStateValid
	ValidationStateInvalid
	ValidationStateWarning
	ValidationStatePartial
	ValidationStatePending
	ValidationStateError
)

func (s ValidationState) String() string {
	switch s {
	case ValidationStateValid:
		return "VALID"
	case ValidationStateInvalid:
		return "INVALID"
	case ValidationStateWarning:
		return "WARNING"
	case ValidationStatePartial:
		return "PARTIAL"
	case ValidationStatePending:
		return "PENDING"
	case ValidationStateError:
		return "ERROR"
	default:
		return "UNSPECIFIED"
	}
}

// ValidationSeverity represents the severity level of validation issues
type ValidationSeverity int

const (
	ValidationSeverityUnspecified ValidationSeverity = iota
	ValidationSeverityInfo
	ValidationSeverityWarning
	ValidationSeverityError
	ValidationSeverityCritical
)

func (s ValidationSeverity) String() string {
	switch s {
	case ValidationSeverityInfo:
		return "INFO"
	case ValidationSeverityWarning:
		return "WARNING"
	case ValidationSeverityError:
		return "ERROR"
	case ValidationSeverityCritical:
		return "CRITICAL"
	default:
		return "UNSPECIFIED"
	}
}

// ValidationHistoryEntry represents a single validation history entry
type ValidationHistoryEntry struct {
	Timestamp   time.Time
	State       ValidationState
	ErrorCount  int
	WarningCount int
	ConfigHash  string
	Sections    []string
}

// ConfigValidator provides validation functionality for configuration
type ConfigValidator struct {
	schemas         map[string]map[string]interface{}
	status          *ValidationStatus
	history         []ValidationHistoryEntry
	maxHistorySize  int
	mu              sync.RWMutex
}

// NewConfigValidator creates a new configuration validator
func NewConfigValidator(schemas map[string]map[string]interface{}) *ConfigValidator {
	return &ConfigValidator{
		schemas:        schemas,
		status:         &ValidationStatus{State: ValidationStatePending},
		history:        make([]ValidationHistoryEntry, 0),
		maxHistorySize: 100, // Keep last 100 validation attempts
	}
}

// ValidateConfig validates a complete configuration against the main schema
func (v *ConfigValidator) ValidateConfig(config map[string]interface{}) *ValidationResult {
	now := time.Now()
	configHash := v.calculateConfigHash(config)
	
	result := &ValidationResult{
		Valid:             true,
		Errors:            []ValidationError{},
		Warnings:          []ValidationWarning{},
		ValidatedAt:       now,
		ConfigHash:        configHash,
		ValidatedSections: []string{"config"},
	}

	// Get the main config schema
	schema, exists := v.schemas["config"]
	if !exists {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:    "schema",
			Message:  "main configuration schema not found",
			Code:     "SCHEMA_NOT_FOUND",
			Severity: ValidationSeverityCritical,
		})
		v.updateStatus(result)
		return result
	}

	// Validate against the schema
	validationErrors := v.validateAgainstSchema(config, schema, "")
	if len(validationErrors) > 0 {
		result.Valid = false
		result.Errors = append(result.Errors, validationErrors...)
	}

	// Update status and history
	v.updateStatus(result)
	
	return result
}

// ValidateModelsConfig validates models configuration against the models schema
func (v *ConfigValidator) ValidateModelsConfig(config map[string]interface{}) *ValidationResult {
	result := &ValidationResult{
		Valid:    true,
		Errors:   []ValidationError{},
		Warnings: []ValidationWarning{},
	}

	// Get the models schema
	schema, exists := v.schemas["models"]
	if !exists {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "schema",
			Message: "models schema not found",
			Code:    "SCHEMA_NOT_FOUND",
		})
		return result
	}

	// Validate against the schema
	validationErrors := v.validateAgainstSchema(config, schema, "")
	if len(validationErrors) > 0 {
		result.Valid = false
		result.Errors = append(result.Errors, validationErrors...)
	}

	return result
}

// ValidateSection validates a specific configuration section against its schema
func (v *ConfigValidator) ValidateSection(section string, config map[string]interface{}) *ValidationResult {
	result := &ValidationResult{
		Valid:    true,
		Errors:   []ValidationError{},
		Warnings: []ValidationWarning{},
	}

	// Get the section schema
	schema, exists := v.schemas[section]
	if !exists {
		result.Valid = false
		result.Errors = append(result.Errors, ValidationError{
			Field:   "schema",
			Message: fmt.Sprintf("%s schema not found", section),
			Code:    "SCHEMA_NOT_FOUND",
		})
		return result
	}

	// Validate against the schema
	validationErrors := v.validateAgainstSchema(config, schema, "")
	if len(validationErrors) > 0 {
		result.Valid = false
		result.Errors = append(result.Errors, validationErrors...)
	}

	return result
}

// validateAgainstSchema performs basic schema validation
func (v *ConfigValidator) validateAgainstSchema(data interface{}, schema map[string]interface{}, path string) []ValidationError {
	var errors []ValidationError

	// Check required fields
	if required, ok := schema["required"].([]interface{}); ok {
		if dataMap, ok := data.(map[string]interface{}); ok {
			for _, req := range required {
				if reqStr, ok := req.(string); ok {
					if _, exists := dataMap[reqStr]; !exists {
						fieldPath := reqStr
						if path != "" {
							fieldPath = path + "." + reqStr
						}
						errors = append(errors, ValidationError{
							Field:    fieldPath,
							Message:  fmt.Sprintf("field '%s' is required", reqStr),
							Code:     "MISSING_FIELD",
							Severity: ValidationSeverityError,
						})
					}
				}
			}
		}
	}

	// Check type
	if schemaType, ok := schema["type"].(string); ok {
		if !v.validateType(data, schemaType) {
			errors = append(errors, ValidationError{
				Field:    path,
				Message:  fmt.Sprintf("expected type '%s', got '%T'", schemaType, data),
				Code:     "INVALID_TYPE",
				Severity: ValidationSeverityError,
			})
		}
	}

	// Validate properties for objects
	if properties, ok := schema["properties"].(map[string]interface{}); ok {
		if dataMap, ok := data.(map[string]interface{}); ok {
			for key, value := range dataMap {
				if propSchema, ok := properties[key].(map[string]interface{}); ok {
					fieldPath := key
					if path != "" {
						fieldPath = path + "." + key
					}
					propErrors := v.validateAgainstSchema(value, propSchema, fieldPath)
					errors = append(errors, propErrors...)
				}
			}
		}
	}

	// Validate items for arrays
	if items, ok := schema["items"].(map[string]interface{}); ok {
		if dataArray, ok := data.([]interface{}); ok {
			for i, item := range dataArray {
				fieldPath := fmt.Sprintf("[%d]", i)
				if path != "" {
					fieldPath = path + fieldPath
				}
				itemErrors := v.validateAgainstSchema(item, items, fieldPath)
				errors = append(errors, itemErrors...)
			}
		}
	}

	// Validate minimum/maximum for numbers
	if num, ok := data.(float64); ok {
		if min, ok := schema["minimum"].(float64); ok {
			if num < min {
				errors = append(errors, ValidationError{
					Field:    path,
					Message:  fmt.Sprintf("value %f is less than minimum %f", num, min),
					Code:     "BELOW_MINIMUM",
					Severity: ValidationSeverityError,
				})
			}
		}
		if max, ok := schema["maximum"].(float64); ok {
			if num > max {
				errors = append(errors, ValidationError{
					Field:    path,
					Message:  fmt.Sprintf("value %f is greater than maximum %f", num, max),
					Code:     "ABOVE_MAXIMUM",
					Severity: ValidationSeverityError,
				})
			}
		}
	}

	// Validate minimum/maximum for integers
	if num, ok := data.(int); ok {
		if min, ok := schema["minimum"].(int); ok {
			if num < min {
				errors = append(errors, ValidationError{
					Field:    path,
					Message:  fmt.Sprintf("value %d is less than minimum %d", num, min),
					Code:     "BELOW_MINIMUM",
					Severity: ValidationSeverityError,
				})
			}
		}
		if max, ok := schema["maximum"].(int); ok {
			if num > max {
				errors = append(errors, ValidationError{
					Field:    path,
					Message:  fmt.Sprintf("value %d is greater than maximum %d", num, max),
					Code:     "ABOVE_MAXIMUM",
					Severity: ValidationSeverityError,
				})
			}
		}
	}

	// Validate enum values
	if enum, ok := schema["enum"].([]interface{}); ok {
		found := false
		for _, enumValue := range enum {
			if data == enumValue {
				found = true
				break
			}
		}
		if !found {
			errors = append(errors, ValidationError{
				Field:    path,
				Message:  fmt.Sprintf("value '%v' is not in allowed enum values: %v", data, enum),
				Code:     "INVALID_ENUM",
				Severity: ValidationSeverityError,
			})
		}
	}

	return errors
}

// validateType checks if the data matches the expected type
func (v *ConfigValidator) validateType(data interface{}, expectedType string) bool {
	switch expectedType {
	case "object":
		_, ok := data.(map[string]interface{})
		return ok
	case "array":
		_, ok := data.([]interface{})
		return ok
	case "string":
		_, ok := data.(string)
		return ok
	case "number":
		_, ok := data.(float64)
		return ok
	case "integer":
		_, ok := data.(int)
		return ok
	case "boolean":
		_, ok := data.(bool)
		return ok
	case "null":
		return data == nil
	default:
		return true // Unknown type, assume valid
	}
}

// AddError adds an error to the validation result
func (r *ValidationResult) AddError(field, message, code string) {
	r.AddErrorWithSeverity(field, message, code, ValidationSeverityError)
}

// AddErrorWithSeverity adds an error with specific severity to the validation result
func (r *ValidationResult) AddErrorWithSeverity(field, message, code string, severity ValidationSeverity) {
	r.Valid = false
	r.Errors = append(r.Errors, ValidationError{
		Field:    field,
		Message:  message,
		Code:     code,
		Severity: severity,
	})
}

// AddWarning adds a warning to the validation result
func (r *ValidationResult) AddWarning(field, message, code string) {
	r.Warnings = append(r.Warnings, ValidationWarning{
		Field:   field,
		Message: message,
		Code:    code,
	})
}

// GetErrorMessages returns all error messages as a slice of strings
func (r *ValidationResult) GetErrorMessages() []string {
	messages := make([]string, len(r.Errors))
	for i, err := range r.Errors {
		messages[i] = fmt.Sprintf("%s: %s", err.Field, err.Message)
	}
	return messages
}

// GetWarningMessages returns all warning messages as a slice of strings
func (r *ValidationResult) GetWarningMessages() []string {
	messages := make([]string, len(r.Warnings))
	for i, warning := range r.Warnings {
		messages[i] = fmt.Sprintf("%s: %s", warning.Field, warning.Message)
	}
	return messages
}

// String returns a string representation of the validation result
func (r *ValidationResult) String() string {
	var parts []string

	if !r.Valid {
		parts = append(parts, "Validation failed:")
		for _, err := range r.Errors {
			parts = append(parts, fmt.Sprintf("  ERROR: %s: %s", err.Field, err.Message))
		}
	}

	if len(r.Warnings) > 0 {
		parts = append(parts, "Warnings:")
		for _, warning := range r.Warnings {
			parts = append(parts, fmt.Sprintf("  WARNING: %s: %s", warning.Field, warning.Message))
		}
	}

	if len(parts) == 0 {
		return "Validation passed"
	}

	return strings.Join(parts, "\n")
}

// calculateConfigHash calculates a hash of the configuration for change detection
func (v *ConfigValidator) calculateConfigHash(config map[string]interface{}) string {
	data := fmt.Sprintf("%+v", config)
	hash := sha256.Sum256([]byte(data))
	return hex.EncodeToString(hash[:])
}

// updateStatus updates the validation status and adds to history
func (v *ConfigValidator) updateStatus(result *ValidationResult) {
	v.mu.Lock()
	defer v.mu.Unlock()

	// Determine validation state
	state := ValidationStateValid
	message := "Configuration is valid"
	var failedSections []string

	if len(result.Errors) > 0 {
		hasCritical := false
		for _, err := range result.Errors {
			if err.Severity == ValidationSeverityCritical {
				hasCritical = true
				break
			}
		}
		
		if hasCritical {
			state = ValidationStateError
			message = "Configuration has critical errors"
		} else {
			state = ValidationStateInvalid
			message = "Configuration has validation errors"
		}
		failedSections = result.ValidatedSections
	} else if len(result.Warnings) > 0 {
		state = ValidationStateWarning
		message = "Configuration is valid but has warnings"
	}

	// Update status
	v.status.State = state
	v.status.Message = message
	v.status.ErrorCount = len(result.Errors)
	v.status.WarningCount = len(result.Warnings)
	v.status.LastValidated = result.ValidatedAt
	v.status.ValidatedSections = result.ValidatedSections
	v.status.FailedSections = failedSections

	// Add to history
	entry := ValidationHistoryEntry{
		Timestamp:    result.ValidatedAt,
		State:        state,
		ErrorCount:   len(result.Errors),
		WarningCount: len(result.Warnings),
		ConfigHash:   result.ConfigHash,
		Sections:     result.ValidatedSections,
	}

	v.history = append(v.history, entry)

	// Trim history if it exceeds max size
	if len(v.history) > v.maxHistorySize {
		v.history = v.history[len(v.history)-v.maxHistorySize:]
	}

	// Set status in result
	result.Status = &ValidationStatus{
		State:             v.status.State,
		Message:           v.status.Message,
		ErrorCount:        v.status.ErrorCount,
		WarningCount:      v.status.WarningCount,
		LastValidated:     v.status.LastValidated,
		ValidatedSections: v.status.ValidatedSections,
		FailedSections:    v.status.FailedSections,
	}
}

// GetValidationStatus returns the current validation status
func (v *ConfigValidator) GetValidationStatus() *ValidationStatus {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if v.status == nil {
		return &ValidationStatus{State: ValidationStatePending}
	}

	// Return a copy to avoid race conditions
	return &ValidationStatus{
		State:             v.status.State,
		Message:           v.status.Message,
		ErrorCount:        v.status.ErrorCount,
		WarningCount:      v.status.WarningCount,
		LastValidated:     v.status.LastValidated,
		ValidatedSections: append([]string{}, v.status.ValidatedSections...),
		FailedSections:    append([]string{}, v.status.FailedSections...),
	}
}

// GetValidationHistory returns the validation history
func (v *ConfigValidator) GetValidationHistory(limit int) []ValidationHistoryEntry {
	v.mu.RLock()
	defer v.mu.RUnlock()

	if limit <= 0 || limit > len(v.history) {
		limit = len(v.history)
	}

	// Return the most recent entries
	start := len(v.history) - limit
	if start < 0 {
		start = 0
	}

	history := make([]ValidationHistoryEntry, limit)
	copy(history, v.history[start:])
	
	// Reverse to get most recent first
	for i := 0; i < len(history)/2; i++ {
		j := len(history) - 1 - i
		history[i], history[j] = history[j], history[i]
	}

	return history
}

// ClearHistory clears the validation history
func (v *ConfigValidator) ClearHistory() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.history = make([]ValidationHistoryEntry, 0)
}
