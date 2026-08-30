package v1

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

const modelParametersSetting = "model_parameters"

// modelRequestDefaults preserves parameter presence. A configured value of
// zero (for example temperature=0) must not be confused with an unset value.
type modelRequestDefaults struct {
	MaxOutputTokens       *int
	Temperature           *float64
	TopP                  *float64
	FrequencyPenalty      *float64
	PresencePenalty       *float64
	ReasoningEffort       string
	ReasoningBudgetTokens *int
	ReasoningEnabled      *bool
	TopK                  *int
	Seed                  *int64
	Verbosity             string
}

func modelDefaultsKey(providerName, modelName string) string {
	return strings.ToLower(strings.TrimSpace(providerName)) + "\x00" +
		strings.ToLower(strings.TrimSpace(modelName))
}

func parseModelRequestDefaults(providerName, raw string) (map[string]modelRequestDefaults, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	var models map[string]map[string]string
	if err := json.Unmarshal([]byte(raw), &models); err != nil {
		return nil, fmt.Errorf("invalid %s: %w", modelParametersSetting, err)
	}

	result := make(map[string]modelRequestDefaults, len(models))
	for modelName, values := range models {
		defaults, err := parseOneModelRequestDefaults(values)
		if err != nil {
			return nil, fmt.Errorf("%s[%q]: %w", modelParametersSetting, modelName, err)
		}
		result[modelDefaultsKey(providerName, modelName)] = defaults
	}
	return result, nil
}

// providerParameterKeys are the request parameters a provider-wide default may
// set. They live as flat keys in custom_settings alongside unrelated settings
// such as `default` and `default_alias`, so the set is an allowlist rather than
// "everything that is not recognised elsewhere".
var providerParameterKeys = map[string]string{
	// Left is the custom_settings key, right is the parameter name.
	// `max_tokens` predates the model-scoped tier, which named the same
	// control `max_output_tokens`.
	"max_tokens":              "max_output_tokens",
	"max_output_tokens":       "max_output_tokens",
	"temperature":             "temperature",
	"top_p":                   "top_p",
	"top_k":                   "top_k",
	"frequency_penalty":       "frequency_penalty",
	"presence_penalty":        "presence_penalty",
	"seed":                    "seed",
	"verbosity":               "verbosity",
	"reasoning_effort":        "reasoning_effort",
	"reasoning_budget_tokens": "reasoning_budget_tokens",
	"reasoning_enabled":       "reasoning_enabled",
}

// parseProviderRequestDefaults reads the provider-wide tier: defaults that
// apply to every model under one provider unless that model overrides them.
func parseProviderRequestDefaults(customSettings map[string]string) (modelRequestDefaults, error) {
	if len(customSettings) == 0 {
		return modelRequestDefaults{}, nil
	}
	values := make(map[string]string, len(customSettings))
	for key, value := range customSettings {
		parameter, ok := providerParameterKeys[key]
		if !ok {
			continue
		}
		// max_output_tokens wins if a configuration carries both spellings.
		if _, taken := values[parameter]; taken && key == "max_tokens" {
			continue
		}
		values[parameter] = value
	}
	return parseOneModelRequestDefaults(values)
}

// restrictedTo drops every parameter the target model does not accept, so a
// provider-wide default set for a fleet is only applied to the models that can
// take it. Without this, one provider-wide temperature would reach a reasoning
// model that rejects it with a 400.
func (d modelRequestDefaults) restrictedTo(supports func(key string) bool) modelRequestDefaults {
	var kept modelRequestDefaults
	if d.MaxOutputTokens != nil && supports("max_output_tokens") {
		kept.MaxOutputTokens = d.MaxOutputTokens
	}
	if d.Temperature != nil && supports("temperature") {
		kept.Temperature = d.Temperature
	}
	if d.TopP != nil && supports("top_p") {
		kept.TopP = d.TopP
	}
	if d.TopK != nil && supports("top_k") {
		kept.TopK = d.TopK
	}
	if d.FrequencyPenalty != nil && supports("frequency_penalty") {
		kept.FrequencyPenalty = d.FrequencyPenalty
	}
	if d.PresencePenalty != nil && supports("presence_penalty") {
		kept.PresencePenalty = d.PresencePenalty
	}
	if d.Seed != nil && supports("seed") {
		kept.Seed = d.Seed
	}
	if d.Verbosity != "" && supports("verbosity") {
		kept.Verbosity = d.Verbosity
	}
	if d.ReasoningEffort != "" && supports("reasoning_effort") {
		kept.ReasoningEffort = d.ReasoningEffort
	}
	if d.ReasoningBudgetTokens != nil && supports("reasoning_budget_tokens") {
		kept.ReasoningBudgetTokens = d.ReasoningBudgetTokens
	}
	if d.ReasoningEnabled != nil && supports("reasoning_enabled") {
		kept.ReasoningEnabled = d.ReasoningEnabled
	}
	return kept
}

// overlay returns d applied on top of base: every parameter d sets wins, and
// the rest fall through. This is what makes the two tiers compose - a model
// that overrides one control keeps the provider's defaults for the others.
func (d modelRequestDefaults) overlay(base modelRequestDefaults) modelRequestDefaults {
	merged := base
	if d.MaxOutputTokens != nil {
		merged.MaxOutputTokens = d.MaxOutputTokens
	}
	if d.Temperature != nil {
		merged.Temperature = d.Temperature
	}
	if d.TopP != nil {
		merged.TopP = d.TopP
	}
	if d.TopK != nil {
		merged.TopK = d.TopK
	}
	if d.FrequencyPenalty != nil {
		merged.FrequencyPenalty = d.FrequencyPenalty
	}
	if d.PresencePenalty != nil {
		merged.PresencePenalty = d.PresencePenalty
	}
	if d.Seed != nil {
		merged.Seed = d.Seed
	}
	if d.Verbosity != "" {
		merged.Verbosity = d.Verbosity
	}
	if d.ReasoningEffort != "" {
		merged.ReasoningEffort = d.ReasoningEffort
	}
	if d.ReasoningBudgetTokens != nil {
		merged.ReasoningBudgetTokens = d.ReasoningBudgetTokens
	}
	if d.ReasoningEnabled != nil {
		merged.ReasoningEnabled = d.ReasoningEnabled
	}
	return merged
}

func parseOneModelRequestDefaults(values map[string]string) (modelRequestDefaults, error) {
	var defaults modelRequestDefaults
	for key, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}

		switch key {
		case "max_output_tokens":
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed <= 0 {
				return defaults, fmt.Errorf("%s must be a positive integer", key)
			}
			defaults.MaxOutputTokens = &parsed
		case "temperature", "top_p", "frequency_penalty", "presence_penalty":
			parsed, err := strconv.ParseFloat(value, 64)
			if err != nil {
				return defaults, fmt.Errorf("%s must be a number", key)
			}
			switch key {
			case "temperature":
				defaults.Temperature = &parsed
			case "top_p":
				defaults.TopP = &parsed
			case "frequency_penalty":
				defaults.FrequencyPenalty = &parsed
			case "presence_penalty":
				defaults.PresencePenalty = &parsed
			}
		case "reasoning_effort":
			defaults.ReasoningEffort = value
		case "verbosity":
			defaults.Verbosity = value
		case "reasoning_budget_tokens":
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 0 {
				return defaults, fmt.Errorf("%s must be a non-negative integer", key)
			}
			defaults.ReasoningBudgetTokens = &parsed
		case "reasoning_enabled":
			parsed, err := strconv.ParseBool(value)
			if err != nil {
				return defaults, fmt.Errorf("%s must be true or false", key)
			}
			defaults.ReasoningEnabled = &parsed
		case "top_k":
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 0 {
				return defaults, fmt.Errorf("%s must be a non-negative integer", key)
			}
			defaults.TopK = &parsed
		case "seed":
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				return defaults, fmt.Errorf("%s must be an integer", key)
			}
			defaults.Seed = &parsed
		}
	}
	return defaults, nil
}

func modelParametersForName(
	parameters map[string]map[string]string,
	modelName string,
) (map[string]string, bool) {
	if values, ok := parameters[modelName]; ok {
		return values, true
	}
	for configuredModel, values := range parameters {
		if strings.EqualFold(configuredModel, modelName) {
			return values, true
		}
	}
	return nil, false
}

func (d modelRequestDefaults) applySampling(sampling *gw.SamplingParams) bool {
	applied := false
	if d.MaxOutputTokens != nil &&
		sampling.MaxTokens == 0 &&
		sampling.MaxCompletionTokens == 0 {
		// MaxTokens is Everstack's provider-neutral output cap. Provider
		// adapters translate it to max_completion_tokens/maxOutputTokens.
		sampling.MaxTokens = *d.MaxOutputTokens
		applied = true
	}
	if d.Temperature != nil &&
		!sampling.TemperatureConfigured &&
		sampling.Temperature == 0 {
		sampling.Temperature = *d.Temperature
		sampling.TemperatureConfigured = true
		applied = true
	}
	if d.TopP != nil && !sampling.TopPConfigured && sampling.TopP == 0 {
		sampling.TopP = *d.TopP
		sampling.TopPConfigured = true
		applied = true
	}
	if d.FrequencyPenalty != nil &&
		!sampling.FrequencyConfigured &&
		sampling.FrequencyPenalty == 0 {
		sampling.FrequencyPenalty = *d.FrequencyPenalty
		sampling.FrequencyConfigured = true
		applied = true
	}
	if d.PresencePenalty != nil &&
		!sampling.PresenceConfigured &&
		sampling.PresencePenalty == 0 {
		sampling.PresencePenalty = *d.PresencePenalty
		sampling.PresenceConfigured = true
		applied = true
	}
	if d.TopK != nil && sampling.TopK == nil {
		sampling.TopK = d.TopK
		applied = true
	}
	if d.Seed != nil && sampling.Seed == nil {
		sampling.Seed = d.Seed
		applied = true
	}
	if d.Verbosity != "" && sampling.Verbosity == "" {
		sampling.Verbosity = d.Verbosity
		applied = true
	}
	requestHasReasoning := sampling.ReasoningEffort != "" ||
		sampling.ReasoningBudget != nil ||
		sampling.ReasoningEnabled != nil
	if !requestHasReasoning {
		if d.ReasoningEffort != "" {
			sampling.ReasoningEffort = d.ReasoningEffort
			applied = true
		}
		if d.ReasoningBudgetTokens != nil {
			sampling.ReasoningBudget = d.ReasoningBudgetTokens
			applied = true
		}
		if d.ReasoningEnabled != nil {
			sampling.ReasoningEnabled = d.ReasoningEnabled
			applied = true
		}
	}
	return applied
}

func (d modelRequestDefaults) applyResponse(req *gw.CreateResponseRequest) bool {
	applied := false
	if d.MaxOutputTokens != nil &&
		!req.MaxOutputConfigured &&
		req.MaxOutputTokens == 0 {
		req.MaxOutputTokens = *d.MaxOutputTokens
		req.MaxOutputConfigured = true
		applied = true
	}
	if d.Temperature != nil &&
		!req.TemperatureConfigured &&
		req.Temperature == 0 {
		req.Temperature = *d.Temperature
		req.TemperatureConfigured = true
		applied = true
	}
	if d.TopP != nil && !req.TopPConfigured && req.TopP == 0 {
		req.TopP = *d.TopP
		req.TopPConfigured = true
		applied = true
	}
	if d.ReasoningEffort != "" &&
		(req.Reasoning == nil || req.Reasoning.Effort == "") {
		if req.Reasoning == nil {
			req.Reasoning = &gw.ReasoningConfig{}
		}
		req.Reasoning.Effort = d.ReasoningEffort
		applied = true
	}
	return applied
}
