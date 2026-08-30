package config_sync

import (
	"encoding/json"
	"testing"

	"github.com/everstacklabs/everstack/cmd/config/gateway/validator"
)

func TestLoadFromGatewayConfigMergesModelParametersForProvider(t *testing.T) {
	t.Parallel()

	configs, err := LoadFromGatewayConfig(&validator.GatewayConfig{
		Models: []validator.ModelConfig{
			{
				Provider: "openai",
				Model:    []string{"gpt-5.6-sol"},
				ModelParameters: map[string]map[string]string{
					"gpt-5.6-sol": {"reasoning_effort": "high"},
				},
			},
			{
				Provider: "openai",
				Model:    []string{"gpt-5.6-terra"},
				ModelParameters: map[string]map[string]string{
					"gpt-5.6-terra": {"reasoning_effort": "low"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("LoadFromGatewayConfig() error = %v", err)
	}
	if len(configs) != 1 {
		t.Fatalf("LoadFromGatewayConfig() returned %d configs, want 1", len(configs))
	}

	var parameters map[string]map[string]string
	raw := configs[0].CustomSettings[modelParametersSetting]
	if err := json.Unmarshal([]byte(raw), &parameters); err != nil {
		t.Fatalf("model_parameters is invalid JSON: %v", err)
	}
	if got := parameters["gpt-5.6-sol"]["reasoning_effort"]; got != "high" {
		t.Fatalf("gpt-5.6-sol reasoning_effort = %q, want high", got)
	}
	if got := parameters["gpt-5.6-terra"]["reasoning_effort"]; got != "low" {
		t.Fatalf("gpt-5.6-terra reasoning_effort = %q, want low", got)
	}
}
