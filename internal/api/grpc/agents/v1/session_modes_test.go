package v1

import (
	"testing"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

func TestClassifySessionModes(t *testing.T) {
	tests := []struct {
		name               string
		agentConfig        map[string]interface{}
		sandboxConfig      sandbox.SandboxConfig
		wantExecutionMode  string
		wantPersistence    string
		wantTemplateConfig bool
	}{
		{
			name: "unsandboxed",
			sandboxConfig: sandbox.SandboxConfig{
				Enabled: false,
			},
			wantExecutionMode:  ExecutionModeUnsandboxed,
			wantPersistence:    PersistenceModeEphemeral,
			wantTemplateConfig: false,
		},
		{
			name: "repo edit mode",
			sandboxConfig: sandbox.SandboxConfig{
				Enabled:    true,
				GitRepoURL: "owner/repo",
			},
			wantExecutionMode:  ExecutionModeRepoEdit,
			wantPersistence:    PersistenceModeCheckpointed,
			wantTemplateConfig: false,
		},
		{
			name: "template mode",
			agentConfig: map[string]interface{}{
				"sandbox": map[string]interface{}{
					"enabled":  true,
					"template": "nextjs",
				},
			},
			sandboxConfig: sandbox.SandboxConfig{
				Enabled: true,
			},
			wantExecutionMode:  ExecutionModeTemplate,
			wantPersistence:    PersistenceModeCheckpointed,
			wantTemplateConfig: true,
		},
		{
			name: "greenfield mode",
			sandboxConfig: sandbox.SandboxConfig{
				Enabled: true,
			},
			wantExecutionMode:  ExecutionModeGreenfield,
			wantPersistence:    PersistenceModeCheckpointed,
			wantTemplateConfig: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotExecution, gotPersistence, gotTemplate := classifySessionModes(tt.agentConfig, tt.sandboxConfig)
			if gotExecution != tt.wantExecutionMode {
				t.Fatalf("execution mode: got %q, want %q", gotExecution, tt.wantExecutionMode)
			}
			if gotPersistence != tt.wantPersistence {
				t.Fatalf("persistence mode: got %q, want %q", gotPersistence, tt.wantPersistence)
			}
			if gotTemplate != tt.wantTemplateConfig {
				t.Fatalf("template configured: got %v, want %v", gotTemplate, tt.wantTemplateConfig)
			}
		})
	}
}
