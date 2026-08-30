package v1

import (
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
	gatewaypb "github.com/everstacklabs/everstack/pkg/grpc/everstack/gateway/v1"
)

func TestParseModelRequestDefaultsKeepsModelsIsolated(t *testing.T) {
	t.Parallel()

	defaults, err := parseModelRequestDefaults("openai", `{
		"gpt-5.6": {
			"reasoning_effort": "xhigh",
			"max_output_tokens": "32000"
		},
		"gpt-5.6-terra": {
			"reasoning_effort": "low",
			"temperature": "0"
		}
	}`)
	if err != nil {
		t.Fatalf("parseModelRequestDefaults() error = %v", err)
	}

	sol, ok := defaults[modelDefaultsKey("openai", "gpt-5.6")]
	if !ok {
		t.Fatal("gpt-5.6 defaults missing")
	}
	if sol.ReasoningEffort != "xhigh" {
		t.Fatalf("gpt-5.6 reasoning effort = %q, want xhigh", sol.ReasoningEffort)
	}
	if sol.MaxOutputTokens == nil || *sol.MaxOutputTokens != 32000 {
		t.Fatalf("gpt-5.6 max output tokens = %v, want 32000", sol.MaxOutputTokens)
	}
	if sol.Temperature != nil {
		t.Fatalf("gpt-5.6 temperature = %v, want unset", sol.Temperature)
	}

	terra, ok := defaults[modelDefaultsKey("OPENAI", "GPT-5.6-TERRA")]
	if !ok {
		t.Fatal("gpt-5.6-terra defaults missing")
	}
	if terra.ReasoningEffort != "low" {
		t.Fatalf("gpt-5.6-terra reasoning effort = %q, want low", terra.ReasoningEffort)
	}
	if terra.Temperature == nil || *terra.Temperature != 0 {
		t.Fatalf("gpt-5.6-terra temperature = %v, want configured zero", terra.Temperature)
	}
}

func TestModelRequestDefaultsApplyToSampling(t *testing.T) {
	t.Parallel()

	temperature := 0.25
	maxOutputTokens := 4096
	reasoningEnabled := false
	defaults := modelRequestDefaults{
		MaxOutputTokens:  &maxOutputTokens,
		Temperature:      &temperature,
		ReasoningEffort:  "high",
		ReasoningEnabled: &reasoningEnabled,
	}

	var sampling gw.SamplingParams
	if !defaults.applySampling(&sampling) {
		t.Fatal("applySampling() = false, want true")
	}
	if sampling.MaxTokens != 4096 {
		t.Fatalf("MaxTokens = %d, want 4096", sampling.MaxTokens)
	}
	if sampling.Temperature != 0.25 {
		t.Fatalf("Temperature = %v, want 0.25", sampling.Temperature)
	}
	if !sampling.TemperatureConfigured {
		t.Fatal("TemperatureConfigured = false, want true")
	}
	if sampling.ReasoningEffort != "high" {
		t.Fatalf("ReasoningEffort = %q, want high", sampling.ReasoningEffort)
	}
	if sampling.ReasoningEnabled == nil || *sampling.ReasoningEnabled {
		t.Fatalf("ReasoningEnabled = %v, want configured false", sampling.ReasoningEnabled)
	}
}

func TestModelRequestDefaultsDoNotOverwriteRequestValues(t *testing.T) {
	t.Parallel()

	maxOutputTokens := 4096
	temperature := 0.0
	topP := 0.9
	defaults := modelRequestDefaults{
		MaxOutputTokens: &maxOutputTokens,
		Temperature:     &temperature,
		TopP:            &topP,
		ReasoningEffort: "high",
	}
	sampling := gw.SamplingParams{
		MaxTokens:             128,
		Temperature:           0.25,
		TemperatureConfigured: true,
		TopP:                  0.2,
		TopPConfigured:        true,
		ReasoningEffort:       "low",
	}

	if defaults.applySampling(&sampling) {
		t.Fatal("applySampling() = true, want false when every value is overridden")
	}
	if sampling.MaxTokens != 128 ||
		sampling.Temperature != 0.25 ||
		sampling.TopP != 0.2 ||
		sampling.ReasoningEffort != "low" {
		t.Fatalf("request values were overwritten: %#v", sampling)
	}
}

func TestModelRequestDefaultsTreatRequestReasoningAsOneOverride(t *testing.T) {
	t.Parallel()

	budget := 2048
	enabled := true
	defaults := modelRequestDefaults{
		ReasoningEffort:       "high",
		ReasoningBudgetTokens: &budget,
		ReasoningEnabled:      &enabled,
	}
	requestDisabled := false
	sampling := gw.SamplingParams{ReasoningEnabled: &requestDisabled}

	if defaults.applySampling(&sampling) {
		t.Fatal("applySampling() = true, want false for explicit request reasoning")
	}
	if sampling.ReasoningEffort != "" ||
		sampling.ReasoningBudget != nil ||
		sampling.ReasoningEnabled == nil ||
		*sampling.ReasoningEnabled {
		t.Fatalf("reasoning defaults leaked into request: %#v", sampling)
	}
}

func TestProtoConversionPreservesExplicitZeroPresence(t *testing.T) {
	t.Parallel()

	zero := float32(0)
	zeroTokens := int32(0)
	zeroBudget := int32(0)
	reasoningDisabled := false
	chat := toGatewayRequest(&gatewaypb.ChatCompletionRequest{
		Sampling: &gatewaypb.SamplingParams{
			Temperature:           &zero,
			TopP:                  &zero,
			FrequencyPenalty:      &zero,
			PresencePenalty:       &zero,
			ReasoningEffort:       stringPointer("low"),
			ReasoningBudgetTokens: &zeroBudget,
			ReasoningEnabled:      &reasoningDisabled,
		},
	})
	if !chat.Sampling.TemperatureConfigured ||
		!chat.Sampling.TopPConfigured ||
		!chat.Sampling.FrequencyConfigured ||
		!chat.Sampling.PresenceConfigured {
		t.Fatalf("chat sampling presence was lost: %#v", chat.Sampling)
	}
	if chat.Sampling.ReasoningEffort != "low" ||
		chat.Sampling.ReasoningBudget == nil ||
		*chat.Sampling.ReasoningBudget != 0 ||
		chat.Sampling.ReasoningEnabled == nil ||
		*chat.Sampling.ReasoningEnabled {
		t.Fatalf("chat reasoning presence was lost: %#v", chat.Sampling)
	}

	response := convertProtoToResponseRequest(&gatewaypb.CreateResponseRequest{
		MaxOutputTokens: &zeroTokens,
		Temperature:     &zero,
		TopP:            &zero,
	})
	if !response.MaxOutputConfigured ||
		!response.TemperatureConfigured ||
		!response.TopPConfigured {
		t.Fatalf("response parameter presence was lost: %#v", response)
	}
}

func stringPointer(value string) *string {
	return &value
}

func TestModelResponseDefaultsDoNotOverwriteRequestValues(t *testing.T) {
	t.Parallel()

	maxOutputTokens := 4096
	temperature := 0.0
	topP := 0.9
	defaults := modelRequestDefaults{
		MaxOutputTokens: &maxOutputTokens,
		Temperature:     &temperature,
		TopP:            &topP,
		ReasoningEffort: "high",
	}
	request := gw.CreateResponseRequest{
		MaxOutputTokens:       128,
		MaxOutputConfigured:   true,
		Temperature:           0.25,
		TemperatureConfigured: true,
		TopP:                  0.2,
		TopPConfigured:        true,
		Reasoning:             &gw.ReasoningConfig{Effort: "low"},
	}

	if defaults.applyResponse(&request) {
		t.Fatal("applyResponse() = true, want false when every value is overridden")
	}
	if request.MaxOutputTokens != 128 ||
		request.Temperature != 0.25 ||
		request.TopP != 0.2 ||
		request.Reasoning.Effort != "low" {
		t.Fatalf("response values were overwritten: %#v", request)
	}
}

func TestModelParametersForNameIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	values, ok := modelParametersForName(
		map[string]map[string]string{
			"GPT-5.6": {"reasoning_effort": "medium"},
		},
		"gpt-5.6",
	)
	if !ok {
		t.Fatal("modelParametersForName() did not find case-insensitive model")
	}
	if values["reasoning_effort"] != "medium" {
		t.Fatalf("reasoning_effort = %q, want medium", values["reasoning_effort"])
	}
}

func TestValidateOutputTokenLimitsRejectsExplicitZero(t *testing.T) {
	t.Parallel()

	zero := int32(0)
	for name, sampling := range map[string]*gatewaypb.SamplingParams{
		"max tokens": {
			MaxTokens: &zero,
		},
		"max completion tokens": {
			MaxCompletionTokens: &zero,
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateOutputTokenLimits(sampling); err == nil {
				t.Fatal("validateOutputTokenLimits() error = nil, want invalid zero")
			}
		})
	}

	one := int32(1)
	if err := validateOutputTokenLimits(&gatewaypb.SamplingParams{
		MaxTokens:           &one,
		MaxCompletionTokens: &one,
	}); err != nil {
		t.Fatalf("validateOutputTokenLimits() error = %v, want nil", err)
	}
}

func TestParseProviderRequestDefaultsIgnoresNonParameterSettings(t *testing.T) {
	defaults, err := parseProviderRequestDefaults(map[string]string{
		"default":          "true",
		"default_alias":    "gpt-4.1",
		"model_parameters": `{"gpt-4.1":{"temperature":"0.2"}}`,
		"max_tokens":       "8192",
		"temperature":      "0.7",
		"top_k":            "40",
	})
	if err != nil {
		t.Fatalf("parseProviderRequestDefaults() error = %v", err)
	}
	if defaults.MaxOutputTokens == nil || *defaults.MaxOutputTokens != 8192 {
		t.Fatalf("MaxOutputTokens = %v, want 8192", defaults.MaxOutputTokens)
	}
	if defaults.Temperature == nil || *defaults.Temperature != 0.7 {
		t.Fatalf("Temperature = %v, want 0.7", defaults.Temperature)
	}
	if defaults.TopK == nil || *defaults.TopK != 40 {
		t.Fatalf("TopK = %v, want 40", defaults.TopK)
	}
	if defaults.Verbosity != "" {
		t.Fatalf("Verbosity = %q, want empty", defaults.Verbosity)
	}
}

func TestOverlayLetsTheModelWinControlByControl(t *testing.T) {
	providerTemp := 0.9
	providerTopK := 40
	providerMax := 4096
	provider := modelRequestDefaults{
		Temperature:     &providerTemp,
		TopK:            &providerTopK,
		MaxOutputTokens: &providerMax,
	}

	modelTemp := 0.1
	model := modelRequestDefaults{Temperature: &modelTemp}

	merged := model.overlay(provider)
	if merged.Temperature == nil || *merged.Temperature != 0.1 {
		t.Fatalf("Temperature = %v, want the model's 0.1", merged.Temperature)
	}
	// The controls the model left alone still come from the provider tier.
	if merged.TopK == nil || *merged.TopK != 40 {
		t.Fatalf("TopK = %v, want the provider's 40", merged.TopK)
	}
	if merged.MaxOutputTokens == nil || *merged.MaxOutputTokens != 4096 {
		t.Fatalf("MaxOutputTokens = %v, want the provider's 4096", merged.MaxOutputTokens)
	}
}

// A provider default of zero is a real setting, not an absent one.
func TestOverlayPreservesAConfiguredZero(t *testing.T) {
	providerTemp := 0.0
	provider := modelRequestDefaults{Temperature: &providerTemp}

	merged := modelRequestDefaults{}.overlay(provider)
	if merged.Temperature == nil || *merged.Temperature != 0 {
		t.Fatalf("Temperature = %v, want a configured 0", merged.Temperature)
	}
}

// A provider-wide default reaches a model only if that model accepts the
// control. Setting a fleet-wide temperature must not send one to a reasoning
// model that rejects it.
func TestRestrictedToDropsControlsTheModelRejects(t *testing.T) {
	temperature := 0.7
	topP := 0.9
	maxOut := 8192
	effort := "high"
	provider := modelRequestDefaults{
		Temperature:     &temperature,
		TopP:            &topP,
		MaxOutputTokens: &maxOut,
		ReasoningEffort: effort,
	}

	// A GPT-5-shaped model: output cap and reasoning effort only.
	accepted := map[string]struct{}{
		"max_output_tokens": {},
		"reasoning_effort":  {},
	}
	supports := func(key string) bool {
		_, ok := accepted[key]
		return ok
	}

	kept := provider.restrictedTo(supports)
	if kept.Temperature != nil {
		t.Fatalf("Temperature = %v, want dropped", *kept.Temperature)
	}
	if kept.TopP != nil {
		t.Fatalf("TopP = %v, want dropped", *kept.TopP)
	}
	if kept.MaxOutputTokens == nil || *kept.MaxOutputTokens != 8192 {
		t.Fatalf("MaxOutputTokens = %v, want 8192", kept.MaxOutputTokens)
	}
	if kept.ReasoningEffort != "high" {
		t.Fatalf("ReasoningEffort = %q, want high", kept.ReasoningEffort)
	}
}

// The same fleet-wide values reach a model that does accept them.
func TestRestrictedToKeepsControlsTheModelAccepts(t *testing.T) {
	temperature := 0.7
	provider := modelRequestDefaults{Temperature: &temperature}

	kept := provider.restrictedTo(func(string) bool { return true })
	if kept.Temperature == nil || *kept.Temperature != 0.7 {
		t.Fatalf("Temperature = %v, want 0.7", kept.Temperature)
	}
}
