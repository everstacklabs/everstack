package vertex_ai

import (
	"encoding/json"
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

const testVertexRoot = "https://us-central1-aiplatform.googleapis.com/v1/projects/demo/locations/us-central1"

func generationConfigFor(t *testing.T, model string, sampling gw.SamplingParams) map[string]interface{} {
	t.Helper()
	p := &Provider{cfg: Config{BaseURL: testVertexRoot}}
	body, _, err := p.buildRequest(gw.ChatCompletionRequest{Model: model, Sampling: sampling}, false)
	if err != nil {
		t.Fatalf("buildRequest(%s) error = %v", model, err)
	}
	var decoded struct {
		GenerationConfig map[string]interface{} `json:"generationConfig"`
	}
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return decoded.GenerationConfig
}

func TestBuildRequestSendsGeminiThinkingConfig(t *testing.T) {
	budget := 2048
	enabled := false
	tests := []struct {
		name     string
		sampling gw.SamplingParams
		want     map[string]interface{}
	}{
		{name: "effort", sampling: gw.SamplingParams{ReasoningEffort: "HIGH"}, want: map[string]interface{}{"thinkingLevel": "high"}},
		{name: "budget", sampling: gw.SamplingParams{ReasoningBudget: &budget}, want: map[string]interface{}{"thinkingBudget": float64(2048)}},
		{name: "disabled", sampling: gw.SamplingParams{ReasoningEnabled: &enabled}, want: map[string]interface{}{"thinkingBudget": float64(0)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := generationConfigFor(t, "gemini-3-pro", tt.sampling)
			thinking, ok := config["thinkingConfig"].(map[string]interface{})
			if !ok {
				t.Fatalf("generationConfig = %v, want thinkingConfig", config)
			}
			if len(thinking) != len(tt.want) {
				t.Fatalf("thinkingConfig = %v, want %v", thinking, tt.want)
			}
			for key, value := range tt.want {
				if thinking[key] != value {
					t.Fatalf("thinkingConfig[%s] = %v, want %v", key, thinking[key], value)
				}
			}
		})
	}

	if config := generationConfigFor(t, "gemini-3-pro", gw.SamplingParams{}); config["thinkingConfig"] != nil {
		t.Fatalf("thinkingConfig sent with no reasoning controls: %v", config)
	}
}

func TestBuildRequestLeavesNonGeminiPublishersUnchanged(t *testing.T) {
	budget := 2048
	sampling := gw.SamplingParams{ReasoningEffort: "high", ReasoningBudget: &budget, Temperature: 0.5}
	for _, model := range []string{"claude-3-7-sonnet@20250219", "llama-3.3-70b", "mistral-large"} {
		config := generationConfigFor(t, model, sampling)
		if _, present := config["thinkingConfig"]; present {
			t.Fatalf("%s: thinkingConfig = %v, want none for a non-Gemini publisher", model, config["thinkingConfig"])
		}
		if config["temperature"] != 0.5 {
			t.Fatalf("%s: generationConfig = %v, want temperature kept", model, config)
		}
	}
}

func TestBuildRequestSkipsThinkingConfigForPreThinkingGemini(t *testing.T) {
	budget := 2048
	sampling := gw.SamplingParams{ReasoningEffort: "high", ReasoningBudget: &budget}
	for _, model := range []string{
		"gemini-1.5-pro",
		"gemini-1.0-pro",
		"gemini-2.0-flash",
		"gemini-2.0-flash-lite-001",
		"publishers/google/models/gemini-1.5-pro",
	} {
		if config := generationConfigFor(t, model, sampling); config["thinkingConfig"] != nil {
			t.Fatalf("%s: thinkingConfig = %v, want none for a model without thinking", model, config["thinkingConfig"])
		}
	}
	for _, model := range []string{"gemini-4-argon", "gemini-2.5-pro", "publishers/google/models/gemini-4-argon"} {
		if _, ok := generationConfigFor(t, model, sampling)["thinkingConfig"].(map[string]interface{}); !ok {
			t.Fatalf("%s: want thinkingConfig for a thinking-capable Gemini model", model)
		}
	}
}
