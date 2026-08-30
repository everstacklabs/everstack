package openai

import (
	"encoding/json"
	"strings"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func TestOptionalTemperaturePreservesConfiguredZero(t *testing.T) {
	t.Parallel()

	payload := oaChatRequest{
		Model: "gpt-4o",
		Temperature: optionalTemperature(gw.SamplingParams{
			Temperature:           0,
			TemperatureConfigured: true,
		}),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"temperature":0`) {
		t.Fatalf("payload = %s, want configured zero temperature", encoded)
	}
}

func TestOptionalTemperatureOmitsUnsetZero(t *testing.T) {
	t.Parallel()

	payload := oaChatRequest{
		Model:       "gpt-4o",
		Temperature: optionalTemperature(gw.SamplingParams{}),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), `"temperature"`) {
		t.Fatalf("payload = %s, want temperature omitted", encoded)
	}
}

func TestOptionalTopPPreservesConfiguredZeroAcrossAPIs(t *testing.T) {
	t.Parallel()

	topP := optionalTopP(gw.SamplingParams{
		TopP:           0,
		TopPConfigured: true,
	})
	for name, payload := range map[string]interface{}{
		"chat":      oaChatRequest{Model: "gpt-4o", TopP: topP},
		"responses": oaResponsesRequest{Model: "gpt-5", TopP: topP},
	} {
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("%s json.Marshal() error = %v", name, err)
		}
		if !strings.Contains(string(encoded), `"top_p":0`) {
			t.Fatalf("%s payload = %s, want configured zero top_p", name, encoded)
		}
	}
}

func TestOptionalPenaltiesPreserveConfiguredZero(t *testing.T) {
	t.Parallel()

	sampling := gw.SamplingParams{
		FrequencyPenalty:    0,
		FrequencyConfigured: true,
		PresencePenalty:     0,
		PresenceConfigured:  true,
	}
	payload := oaChatRequest{
		Model:            "gpt-4o",
		FrequencyPenalty: optionalFrequencyPenalty(sampling),
		PresencePenalty:  optionalPresencePenalty(sampling),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, want := range []string{`"frequency_penalty":0`, `"presence_penalty":0`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("payload = %s, want %s", encoded, want)
		}
	}
}

func TestOptionalPenaltiesOmittedWhenUnset(t *testing.T) {
	t.Parallel()

	payload := oaChatRequest{
		Model:            "gpt-5",
		FrequencyPenalty: optionalFrequencyPenalty(gw.SamplingParams{}),
		PresencePenalty:  optionalPresencePenalty(gw.SamplingParams{}),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, unwanted := range []string{`"frequency_penalty"`, `"presence_penalty"`} {
		if strings.Contains(string(encoded), unwanted) {
			t.Fatalf("payload = %s, want %s omitted", encoded, unwanted)
		}
	}
}

func TestChatCompletionsCarriesSeedAndVerbosity(t *testing.T) {
	t.Parallel()

	seed := int64(1234)
	payload := oaChatRequest{Model: "gpt-5.2", Seed: &seed, Verbosity: "low"}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	for _, want := range []string{`"seed":1234`, `"verbosity":"low"`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("payload = %s, want %s", encoded, want)
		}
	}
}

// The Responses API takes verbosity under `text`, not at the top level.
func TestResponsesNestsVerbosityUnderText(t *testing.T) {
	t.Parallel()

	payload := oaResponsesRequest{
		Model: "gpt-5.2",
		Text:  responsesTextConfig(gw.SamplingParams{Verbosity: "high"}),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"text":{"verbosity":"high"}`) {
		t.Fatalf("payload = %s, want verbosity nested under text", encoded)
	}
}

func TestResponsesOmitsTextWhenVerbosityUnset(t *testing.T) {
	t.Parallel()

	payload := oaResponsesRequest{
		Model: "gpt-5.2",
		Text:  responsesTextConfig(gw.SamplingParams{}),
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	if strings.Contains(string(encoded), `"text"`) {
		t.Fatalf("payload = %s, want text omitted", encoded)
	}
}
