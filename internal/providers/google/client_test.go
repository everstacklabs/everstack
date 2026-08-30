package google

import (
	"testing"

	gw "github.com/everstacklabs/everstack/internal/lib/handlers/gateway"
)

func TestApplyThinkingConfigUsesGenerateContentThinkingLevel(t *testing.T) {
	t.Parallel()

	config := make(map[string]interface{})
	budget := 4096
	applyThinkingConfig(config, gw.SamplingParams{
		ReasoningEffort: "LOW",
		ReasoningBudget: &budget,
	})

	thinking, ok := config["thinkingConfig"].(map[string]interface{})
	if !ok {
		t.Fatalf("thinkingConfig = %#v, want object", config["thinkingConfig"])
	}
	if got := thinking["thinkingLevel"]; got != "low" {
		t.Fatalf("thinkingLevel = %#v, want low", got)
	}
	if _, exists := thinking["thinkingBudget"]; exists {
		t.Fatal("thinkingBudget must not be sent with thinkingLevel")
	}
}

func TestApplyThinkingConfigSupportsLegacyBudget(t *testing.T) {
	t.Parallel()

	config := make(map[string]interface{})
	budget := 2048
	applyThinkingConfig(config, gw.SamplingParams{ReasoningBudget: &budget})

	thinking := config["thinkingConfig"].(map[string]interface{})
	if got := thinking["thinkingBudget"]; got != 2048 {
		t.Fatalf("thinkingBudget = %#v, want 2048", got)
	}
}

func TestApplyThinkingConfigPreservesZeroBudget(t *testing.T) {
	t.Parallel()

	config := make(map[string]interface{})
	budget := 0
	applyThinkingConfig(config, gw.SamplingParams{ReasoningBudget: &budget})

	thinking := config["thinkingConfig"].(map[string]interface{})
	if got := thinking["thinkingBudget"]; got != 0 {
		t.Fatalf("thinkingBudget = %#v, want 0", got)
	}
}
