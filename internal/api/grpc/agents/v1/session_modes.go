package v1

import (
	"strings"

	"github.com/everstacklabs/everstack/internal/sandbox"
)

// Execution mode constants describe how the sandbox is provisioned.
const (
	ExecutionModeUnsandboxed = "unsandboxed"
	ExecutionModeRepoEdit    = "repo_edit"
	ExecutionModeTemplate    = "template"
	ExecutionModeGreenfield  = "greenfield"
)

// Persistence mode constants describe how sandbox state is retained.
const (
	PersistenceModeEphemeral    = "ephemeral"
	PersistenceModeCheckpointed = "checkpointed"
	// PersistenceModeGitSynced is defined in the north-star architecture
	// but not yet implemented. Will require explicit git commit/push
	// sync on checkpoint events.
)

func classifySessionModes(agentConfig map[string]interface{}, sandboxConfig sandbox.SandboxConfig) (executionMode string, persistenceMode string, templateConfigured bool) {
	if !sandboxConfig.Enabled {
		return ExecutionModeUnsandboxed, PersistenceModeEphemeral, false
	}

	templateConfigured = hasSandboxTemplate(agentConfig)
	switch {
	case strings.TrimSpace(sandboxConfig.GitRepoURL) != "":
		executionMode = ExecutionModeRepoEdit
	case templateConfigured:
		executionMode = ExecutionModeTemplate
	default:
		executionMode = ExecutionModeGreenfield
	}

	// Current sandbox behavior persists state across turns and supports snapshot restore.
	persistenceMode = PersistenceModeCheckpointed
	return executionMode, persistenceMode, templateConfigured
}

func hasSandboxTemplate(agentConfig map[string]interface{}) bool {
	if agentConfig == nil {
		return false
	}
	raw, ok := agentConfig["sandbox"]
	if !ok {
		return false
	}
	sandboxMap, ok := raw.(map[string]interface{})
	if !ok {
		return false
	}
	templateID, ok := sandboxMap["template"].(string)
	return ok && strings.TrimSpace(templateID) != ""
}
