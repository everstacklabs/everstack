package tools

import "strings"

type ToolCapability string

const (
	ToolCapabilityUnknown          ToolCapability = "unknown"
	ToolCapabilityFilesystem       ToolCapability = "filesystem"
	ToolCapabilityProcess          ToolCapability = "process"
	ToolCapabilityNetwork          ToolCapability = "network"
	ToolCapabilityMemory           ToolCapability = "memory"
	ToolCapabilityMCP              ToolCapability = "mcp"
	ToolCapabilityPlatformMutation ToolCapability = "platform_mutation"
	ToolCapabilityHumanInteraction ToolCapability = "human_interaction"
	ToolCapabilityBrowser          ToolCapability = "browser"
	ToolCapabilityWorkflow         ToolCapability = "workflow"
	ToolCapabilityAgentDelegation  ToolCapability = "agent_delegation"
	ToolCapabilityStorage          ToolCapability = "storage"
	ToolCapabilityRepository       ToolCapability = "repository"
	ToolCapabilitySandboxRuntime   ToolCapability = "sandbox_runtime"
)

func ToolCapabilityForName(name string) ToolCapability {
	name = strings.TrimSpace(name)
	if name == "" {
		return ToolCapabilityUnknown
	}
	if strings.HasPrefix(name, "mcp__") {
		return ToolCapabilityMCP
	}
	if strings.HasPrefix(name, "browser_") {
		return ToolCapabilityBrowser
	}

	switch name {
	case "sandbox_read_file", "sandbox_write_file", "sandbox_list_files", "sandbox_edit", "sandbox_grep", "sandbox_glob", "sandbox_patch":
		return ToolCapabilityFilesystem
	case "sandbox_execute", "sandbox_shell":
		return ToolCapabilityProcess
	case "sandbox_expose_port", "sandbox_unexpose_port", "sandbox_list_ports":
		return ToolCapabilityNetwork
	case "sandbox_git_clone", "sandbox_git_status", "sandbox_git_diff", "sandbox_git_commit", "sandbox_git_push":
		return ToolCapabilityRepository
	case "sandbox_lsp_info", "sandbox_lsp_diagnostics", "sandbox_lsp_symbols", "sandbox_lsp_hover", "sandbox_lsp_definition", "sandbox_lsp_references", "sandbox_screenshot":
		return ToolCapabilitySandboxRuntime
	case "memory_store", "memory_query":
		return ToolCapabilityMemory
	case "web_search", "web_fetch":
		return ToolCapabilityNetwork
	case "create_workflow":
		return ToolCapabilityWorkflow
	case "spawn_agent", "parallel_tasks", "check_job", "fork":
		return ToolCapabilityAgentDelegation
	case "ask_user", "send_message", "check_messages", "delegate_job", "read_channel_history":
		return ToolCapabilityHumanInteraction
	case "upload_artifact", "download_artifact", "list_artifacts":
		return ToolCapabilityStorage
	case "platform_create_agent", "platform_update_agent", "platform_delete_agent":
		return ToolCapabilityPlatformMutation
	case "platform_get_agent", "platform_list_agents":
		return ToolCapabilityRepository
	case "use_skill":
		return ToolCapabilitySandboxRuntime
	default:
		if strings.HasPrefix(name, "sandbox_") {
			return ToolCapabilitySandboxRuntime
		}
		return ToolCapabilityUnknown
	}
}
